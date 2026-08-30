package webapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
)

func getHealth(t *testing.T, app *App) (*httptest.ResponseRecorder, healthReport) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://lite.nightloch.app/health", nil)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	var report healthReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode health body %q: %v", recorder.Body.String(), err)
	}
	return recorder, report
}

func TestHealthReportsNamedChecksAndTheVersion(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestApp(t)

	recorder, report := getHealth(t, app)

	if recorder.Code != http.StatusOK || report.Status != "ok" {
		t.Fatalf("GET /health = %d %q", recorder.Code, recorder.Body.String())
	}
	if report.Version != "test" {
		t.Fatalf("health version = %q, want %q", report.Version, "test")
	}
	check, ok := report.Checks[databaseWritableCheck]
	if !ok {
		t.Fatalf("health checks = %#v, want a %q check", report.Checks, databaseWritableCheck)
	}
	if check.Status != "ok" || !check.GovernsStatus {
		t.Fatalf("%s check = %#v, want a passing check that governs the status", databaseWritableCheck, check)
	}
}

func TestHealthFailsWhenTheDatabaseIsNotWritable(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t)
	if err := env.db.Close(); err != nil {
		t.Fatal(err)
	}

	recorder, report := getHealth(t, env.app)

	if recorder.Code != http.StatusServiceUnavailable || report.Status != "unhealthy" {
		t.Fatalf("GET /health with an unwritable database = %d %q", recorder.Code, recorder.Body.String())
	}
	if got := report.Checks[databaseWritableCheck].Status; got != "unhealthy" {
		t.Fatalf("%s check status = %q, want %q", databaseWritableCheck, got, "unhealthy")
	}
	if report.Version != "test" {
		t.Fatalf("health version = %q, want %q", report.Version, "test")
	}
}

// A Slice 4 enforcement fact reports its own state without deciding the
// endpoint's, so only the checks that claim the status can fail the deploy gate.
// The facts arrive through Options, which is how Slice 4 will add them.
func TestHealthOnlyFailsOnChecksThatGovernTheStatus(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t,
		HealthCheck{
			Name:  "reporting_only",
			Probe: func(context.Context) error { return errors.New("degraded") },
		},
		HealthCheck{
			Name:          "governing",
			GovernsStatus: true,
			Probe:         func(context.Context) error { return nil },
		},
	)

	recorder, report := getHealth(t, env.app)

	if recorder.Code != http.StatusOK || report.Status != "ok" {
		t.Fatalf("GET /health with a failing non-governing check = %d %q", recorder.Code, recorder.Body.String())
	}
	if got := report.Checks["reporting_only"]; got.Status != "unhealthy" || got.GovernsStatus {
		t.Fatalf("reporting_only check = %#v, want a failing check that does not govern the status", got)
	}
	if got := report.Checks["governing"]; got.Status != "ok" || !got.GovernsStatus {
		t.Fatalf("governing check = %#v, want a passing check that governs the status", got)
	}
	if got := report.Checks[databaseWritableCheck]; got.Status != "ok" || !got.GovernsStatus {
		t.Fatalf("%s check = %#v, want the default check still reported alongside the added ones", databaseWritableCheck, got)
	}
}

func TestHealthChecksAreBoundedByTheEndpointTimeout(t *testing.T) {
	t.Parallel()
	var deadlineIn time.Duration
	env := newTestEnv(t, HealthCheck{
		Name:          "deadline_reader",
		GovernsStatus: true,
		Probe: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				return errors.New("health check ran without a deadline")
			}
			deadlineIn = time.Until(deadline)
			return nil
		},
	})

	recorder, _ := getHealth(t, env.app)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /health = %d %q", recorder.Code, recorder.Body.String())
	}
	if deadlineIn <= 0 || deadlineIn > healthCheckTimeout {
		t.Fatalf("health check deadline in %s, want a positive bound no longer than %s", deadlineIn, healthCheckTimeout)
	}
}

// A check with no probe reports no fact, so it must fail rather than panic —
// and when it governs the status, take the endpoint down deliberately.
func TestHealthCheckWithoutAProbeFails(t *testing.T) {
	t.Parallel()
	env := newTestEnv(t, HealthCheck{Name: "unprobed", GovernsStatus: true})

	recorder, report := getHealth(t, env.app)

	if recorder.Code != http.StatusServiceUnavailable || report.Status != "unhealthy" {
		t.Fatalf("GET /health with an unprobed governing check = %d %q", recorder.Code, recorder.Body.String())
	}
	if got := report.Checks["unprobed"].Status; got != "unhealthy" {
		t.Fatalf("unprobed check status = %q, want %q", got, "unhealthy")
	}
}

// enforcementEnv builds an App reporting the enforcement plane over fakes, so
// every combination of tunnel and resolver state is reachable without a
// droplet. The logger is captured because a failing enforcement fact has to be
// loud in the logs as well as in the payload.
type enforcementEnv struct {
	*testEnv
	logs *bytes.Buffer
}

func newEnforcementEnv(t *testing.T, tunnel *enforcement.RecordingTunnel, resolver *enforcement.RecordingResolver) *enforcementEnv {
	t.Helper()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	env := newTestEnvWithLogger(t, logger, EnforcementHealthChecks(enforcement.NewPlane(tunnel, resolver))...)
	return &enforcementEnv{testEnv: env, logs: logs}
}

// tunnelStates and resolverStates are every state each half of the plane can
// be in, as the payload must report it.
var tunnelStates = map[string]struct {
	peersError error
	want       string
}{
	"up":   {want: healthOK},
	"down": {peersError: enforcement.ErrTunnelUnavailable, want: healthUnhealthy},
}

var resolverStates = map[string]struct {
	resolver func() *enforcement.RecordingResolver
	want     string
}{
	"filtering": {resolver: enforcement.EnforcingResolver, want: healthOK},
	"unreachable": {resolver: func() *enforcement.RecordingResolver {
		return &enforcement.RecordingResolver{StatusError: errors.New("connection refused")}
	}, want: healthUnhealthy},
	"not filtering": {resolver: func() *enforcement.RecordingResolver {
		return &enforcement.RecordingResolver{Reported: enforcement.ResolverStatus{Running: true, ProtectionEnabled: true}}
	}, want: healthUnhealthy},
}

// The whole cross product through the HTTP seam. A down tunnel or a resolver
// that is not enforcing is visible in the payload and changes no status code;
// an unwritable database fails the endpoint exactly as it did before the plane
// was reported at all.
func TestHealthReportsEveryCombinationOfDatabaseTunnelAndResolverState(t *testing.T) {
	t.Parallel()
	for _, writable := range []bool{true, false} {
		for tunnelName, tunnelState := range tunnelStates {
			for resolverName, resolverState := range resolverStates {
				name := fmt.Sprintf("database writable=%t/tunnel %s/resolver %s", writable, tunnelName, resolverName)
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					tunnel := &enforcement.RecordingTunnel{PeersError: tunnelState.peersError}
					env := newEnforcementEnv(t, tunnel, resolverState.resolver())
					if !writable {
						if err := env.db.Close(); err != nil {
							t.Fatal(err)
						}
					}

					recorder, report := getHealth(t, env.app)

					wantDatabase, wantStatus, wantCode := healthOK, healthOK, http.StatusOK
					if !writable {
						wantDatabase, wantStatus, wantCode = healthUnhealthy, healthUnhealthy, http.StatusServiceUnavailable
					}
					if recorder.Code != wantCode || report.Status != wantStatus {
						t.Fatalf("GET /health = %d %q, want %d %q", recorder.Code, report.Status, wantCode, wantStatus)
					}
					if got := report.Checks[databaseWritableCheck]; got.Status != wantDatabase || !got.GovernsStatus {
						t.Fatalf("%s check = %#v, want %q governing the status", databaseWritableCheck, got, wantDatabase)
					}
					if got := report.Checks[tunnelInterfaceCheck]; got.Status != tunnelState.want || got.GovernsStatus {
						t.Fatalf("%s check = %#v, want %q not governing the status", tunnelInterfaceCheck, got, tunnelState.want)
					}
					if got := report.Checks[resolverFilteringCheck]; got.Status != resolverState.want || got.GovernsStatus {
						t.Fatalf("%s check = %#v, want %q not governing the status", resolverFilteringCheck, got, resolverState.want)
					}
				})
			}
		}
	}
}

// A fact that failed is named in the logs with what went wrong, because the
// Operator watching an enforcement outage is reading logs rather than polling
// a payload.
func TestHealthLogsTheEnforcementFactThatFailed(t *testing.T) {
	t.Parallel()
	env := newEnforcementEnv(t,
		&enforcement.RecordingTunnel{PeersError: enforcement.ErrTunnelUnavailable},
		&enforcement.RecordingResolver{Reported: enforcement.ResolverStatus{Running: true, ProtectionEnabled: true}},
	)

	recorder, _ := getHealth(t, env.app)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /health with the whole plane down = %d, want the process still reported as serving", recorder.Code)
	}
	logs := env.logs.String()
	for _, want := range []string{tunnelInterfaceCheck, resolverFilteringCheck, "filtering off", enforcement.ErrTunnelUnavailable.Error()} {
		if !strings.Contains(logs, want) {
			t.Fatalf("health logs = %q, want them to name %q", logs, want)
		}
	}
}

// A hung enforcer must cost the deploy gate's own check nothing: the checks
// are bounded separately and run at the same time, so the endpoint answers
// within one timeout however many checks hang.
func TestHealthChecksAreBoundedSeparatelyFromEachOther(t *testing.T) {
	t.Parallel()
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	var databaseBudget time.Duration
	env := newTestEnv(t,
		HealthCheck{
			Name: "hung_enforcer",
			Probe: func(ctx context.Context) error {
				select {
				case <-hang:
				case <-ctx.Done():
				}
				return ctx.Err()
			},
		},
		HealthCheck{
			Name:          "budget_reader",
			GovernsStatus: true,
			Probe: func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok {
					return errors.New("health check ran without a deadline")
				}
				databaseBudget = time.Until(deadline)
				return nil
			},
		},
	)

	started := time.Now()
	recorder, report := getHealth(t, env.app)
	elapsed := time.Since(started)

	if recorder.Code != http.StatusOK || report.Status != healthOK {
		t.Fatalf("GET /health with a hung non-governing check = %d %q", recorder.Code, recorder.Body.String())
	}
	if report.Checks["hung_enforcer"].Status != healthUnhealthy {
		t.Fatalf("hung check = %#v, want it reported unhealthy on its own deadline", report.Checks["hung_enforcer"])
	}
	if databaseBudget <= healthCheckTimeout/2 {
		t.Fatalf("the governing check ran with %s of budget, want the whole %s rather than a share of it", databaseBudget, healthCheckTimeout)
	}
	if elapsed > 2*healthCheckTimeout {
		t.Fatalf("GET /health took %s, want one timeout rather than one per check", elapsed)
	}
}
