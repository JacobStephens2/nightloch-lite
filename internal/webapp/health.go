package webapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/database"
	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
)

// healthCheckTimeout bounds each check on its own. Checks run concurrently, so
// the endpoint's whole latency bound is this one timeout however many checks
// there are: a Slice 4 enforcement probe that hangs spends none of the
// database check's budget and adds nothing to the endpoint's. Sharing one
// budget in order, as the endpoint did when every check was cheap, would have
// let a hung enforcer starve the check the Slice 9 deploy gate rides on.
const healthCheckTimeout = 2 * time.Second

// databaseWritableCheck is the check the Slice 9 deploy gate rides on: a
// writable database means the freshly restarted binary can serve.
const databaseWritableCheck = "database_writable"

const (
	healthOK        = "ok"
	healthUnhealthy = "unhealthy"
)

// HealthCheck is one named fact about the running system. A check reports its
// own state, and decides for itself whether that state can fail the endpoint:
// only a check with GovernsStatus set turns a failure into service-unavailable.
// Facts that are worth reporting but must not stop a deploy leave it unset.
type HealthCheck struct {
	Name          string
	GovernsStatus bool
	Probe         func(context.Context) error
}

// healthReport is the /health payload: named checks plus the running version.
type healthReport struct {
	Status  string                  `json:"status"`
	Version string                  `json:"version"`
	Checks  map[string]healthResult `json:"checks"`
}

// healthResult is one check's own state, and whether it governs the response.
type healthResult struct {
	Status        string `json:"status"`
	GovernsStatus bool   `json:"governs_status"`
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	report := healthReport{
		Status:  healthOK,
		Version: a.version,
		Checks:  make(map[string]healthResult, len(a.healthChecks)),
	}
	results := make([]healthResult, len(a.healthChecks))
	var running sync.WaitGroup
	for index, check := range a.healthChecks {
		running.Add(1)
		go func() {
			defer running.Done()
			results[index] = a.runCheck(r.Context(), check)
		}()
	}
	running.Wait()
	for index, check := range a.healthChecks {
		if results[index].Status == healthUnhealthy && check.GovernsStatus {
			status = http.StatusServiceUnavailable
			report.Status = healthUnhealthy
		}
		report.Checks[check.Name] = results[index]
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(report)
}

// runCheck runs one check under its own deadline and reports what it found. A
// failing check is logged with its own name, so an enforcement fault says
// which half of the plane stopped rather than only that health is degraded.
func (a *App) runCheck(ctx context.Context, check HealthCheck) healthResult {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	result := healthResult{Status: healthOK, GovernsStatus: check.GovernsStatus}
	if err := check.run(ctx); err != nil {
		result.Status = healthUnhealthy
		a.logger.Error("health check failed", "check", check.Name, "error", err)
	}
	return result
}

// run reports the check's state. A check without a probe cannot report a fact
// about anything, so it reports failure rather than panicking mid-report.
func (c HealthCheck) run(ctx context.Context) error {
	if c.Probe == nil {
		return errors.New("health check has no probe")
	}
	return c.Probe(ctx)
}

// The enforcement plane's two facts, reported by name so a resolver that is
// running but not filtering is distinguishable from one that is enforcing, and
// so a single opaque flag never hides which half of the plane failed.
const (
	tunnelInterfaceCheck   = "tunnel_interface"
	resolverFilteringCheck = "resolver_filtering"
)

// EnforcementHealthChecks reports the enforcement plane as named facts.
//
// Neither governs the status. The endpoint's status is what the Slice 9 deploy
// gate reads, and it may fail only on what a freshly deployed binary can have
// broken: a resolver outage is not that, and rolling a good binary back would
// not fix one. The facts are still in the payload either way, so honesty does
// not depend on the status code.
func EnforcementHealthChecks(plane *enforcement.Plane) []HealthCheck {
	return []HealthCheck{
		{Name: tunnelInterfaceCheck, Probe: plane.TunnelUp},
		{Name: resolverFilteringCheck, Probe: plane.ResolverEnforcing},
	}
}

// defaultHealthChecks are the checks every App reports. The writable-database
// check governs the status exactly as it did when it was the whole endpoint.
func defaultHealthChecks(db *sql.DB) []HealthCheck {
	return []HealthCheck{{
		Name:          databaseWritableCheck,
		GovernsStatus: true,
		Probe:         func(ctx context.Context) error { return database.Writable(ctx, db) },
	}}
}
