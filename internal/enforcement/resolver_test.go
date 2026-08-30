package enforcement

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adGuardStub answers the two control endpoints the adapter reads, recording
// what it was asked for and with what credentials.
type adGuardStub struct {
	statusBody    string
	statusCode    int
	filteringBody string
	filteringCode int

	paths []string
	user  string
	pass  string
}

func (s *adGuardStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.paths = append(s.paths, r.URL.Path)
	s.user, s.pass, _ = r.BasicAuth()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/control/status":
		w.WriteHeader(s.statusCode)
		_, _ = w.Write([]byte(s.statusBody))
	case "/control/filtering/status":
		w.WriteHeader(s.filteringCode)
		_, _ = w.Write([]byte(s.filteringBody))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newAdGuardStub(t *testing.T, stub *adGuardStub) *AdGuardHome {
	t.Helper()
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	resolver, err := NewAdGuardHome(ResolverSettings{
		BaseURL: server.URL, Username: "operator", Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func TestAdGuardHomeReadsRunningProtectionAndFiltering(t *testing.T) {
	t.Parallel()
	stub := &adGuardStub{
		statusCode:    http.StatusOK,
		statusBody:    `{"running":true,"protection_enabled":true,"version":"v0.107.65"}`,
		filteringCode: http.StatusOK,
		filteringBody: `{"enabled":true,"interval":24}`,
	}
	resolver := newAdGuardStub(t, stub)

	status, err := resolver.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Running || !status.ProtectionEnabled || !status.FilteringEnabled {
		t.Fatalf("status = %#v, want every fact true", status)
	}
	if stub.user != "operator" || stub.pass != "secret" {
		t.Fatalf("credentials = %q/%q, want the configured admin basic auth", stub.user, stub.pass)
	}
	if len(stub.paths) != 2 {
		t.Fatalf("paths = %v, want both control endpoints read", stub.paths)
	}
}

func TestAdGuardHomeReportsFilteringOffWithoutClaimingItIsUnreachable(t *testing.T) {
	t.Parallel()
	resolver := newAdGuardStub(t, &adGuardStub{
		statusCode:    http.StatusOK,
		statusBody:    `{"running":true,"protection_enabled":true}`,
		filteringCode: http.StatusOK,
		filteringBody: `{"enabled":false}`,
	})

	status, err := resolver.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.FilteringEnabled {
		t.Fatalf("status = %#v, want filtering reported off", status)
	}
}

// A rejected credential, a refused connection, and an answer that is not the
// API's are all one fact: nothing here knows whether the resolver is filtering.
// None of them may be read as a resolver that is up and enforcing.
func TestAdGuardHomeReportsUnreachableRatherThanGuessing(t *testing.T) {
	t.Parallel()
	for name, stub := range map[string]*adGuardStub{
		"unauthorized": {statusCode: http.StatusUnauthorized, statusBody: `{}`},
		"unparseable":  {statusCode: http.StatusOK, statusBody: `<html>not the api</html>`},
		"filtering unauthorized": {
			statusCode: http.StatusOK, statusBody: `{"running":true,"protection_enabled":true}`,
			filteringCode: http.StatusForbidden, filteringBody: `{}`,
		},
	} {
		resolver := newAdGuardStub(t, stub)
		if _, err := resolver.Status(context.Background()); !errors.Is(err, ErrResolverUnreachable) {
			t.Fatalf("Status with %s = %v, want %v", name, err, ErrResolverUnreachable)
		}
	}

	unreachable, err := NewAdGuardHome(ResolverSettings{
		BaseURL: "http://127.0.0.1:1", Username: "operator", Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unreachable.Status(context.Background()); !errors.Is(err, ErrResolverUnreachable) {
		t.Fatalf("Status against a closed port = %v, want %v", err, ErrResolverUnreachable)
	}
}

func TestAdGuardHomeRefusesSettingsItCannotReach(t *testing.T) {
	t.Parallel()
	for name, settings := range map[string]ResolverSettings{
		"no base URL":  {Username: "operator", Password: "secret"},
		"not a URL":    {BaseURL: "://", Username: "operator", Password: "secret"},
		"no scheme":    {BaseURL: "127.0.0.1:3000", Username: "operator", Password: "secret"},
		"no username":  {BaseURL: "http://127.0.0.1:3000", Password: "secret"},
		"no password":  {BaseURL: "http://127.0.0.1:3000", Username: "operator"},
		"public plain": {BaseURL: "http://adguard.example.com", Username: "operator", Password: "secret"},
	} {
		if _, err := NewAdGuardHome(settings); err == nil {
			t.Fatalf("NewAdGuardHome(%s) succeeded, want an error", name)
		}
	}
}
