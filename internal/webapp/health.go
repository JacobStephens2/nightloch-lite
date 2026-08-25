package webapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/JacobStephens2/nightloch-lite/internal/database"
)

// healthCheckTimeout bounds each named check. The whole endpoint stays inside
// one budget of this length, so no number of checks can outlast the timeout a
// single inline check had before health became a collection.
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
	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	status := http.StatusOK
	report := healthReport{
		Status:  healthOK,
		Version: a.version,
		Checks:  make(map[string]healthResult, len(a.healthChecks)),
	}
	for _, check := range a.healthChecks {
		result := healthResult{Status: healthOK, GovernsStatus: check.GovernsStatus}
		if err := a.runHealthCheck(ctx, check); err != nil {
			result.Status = healthUnhealthy
			a.logger.Error("health check failed", "check", check.Name, "error", err)
			if check.GovernsStatus {
				status = http.StatusServiceUnavailable
				report.Status = healthUnhealthy
			}
		}
		report.Checks[check.Name] = result
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(report)
}

// runHealthCheck gives one check its own deadline inside the endpoint's budget,
// so a check that hangs cannot spend the time the remaining checks need.
func (a *App) runHealthCheck(ctx context.Context, check HealthCheck) error {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	return check.Probe(ctx)
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
