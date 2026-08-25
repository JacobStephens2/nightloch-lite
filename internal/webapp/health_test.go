package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
