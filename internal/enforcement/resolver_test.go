package enforcement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
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

func TestDeviceClientName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id   int64
		want string
	}{
		{id: 1, want: "device-1"},
		{id: 42, want: "device-42"},
		{id: 999999, want: "device-999999"},
	} {
		if got := DeviceClientName(tc.id); got != tc.want {
			t.Errorf("DeviceClientName(%d) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestAdGuardHomeAddsClient(t *testing.T) {
	t.Parallel()
	var (
		gotMethod string
		gotPath   string
		gotUser   string
		gotPass   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotUser, gotPass, _ = r.BasicAuth()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	}))
	t.Cleanup(server.Close)

	resolver, err := NewAdGuardHome(ResolverSettings{
		BaseURL:  server.URL,
		Username: "operator",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	ip := netip.MustParseAddr("10.66.0.2")
	err = resolver.AddClient(context.Background(), "device-1", ip)
	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/control/clients/add" {
		t.Errorf("path = %s, want /control/clients/add", gotPath)
	}
	if gotUser != "operator" || gotPass != "secret" {
		t.Errorf("credentials = %s:%s, want operator:secret", gotUser, gotPass)
	}
	if gotBody["name"] != "device-1" {
		t.Errorf("body name = %v, want device-1", gotBody["name"])
	}
	ids, ok := gotBody["ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != "10.66.0.2" {
		t.Errorf("body ids = %v, want [10.66.0.2]", gotBody["ids"])
	}
	if gotBody["filtering_enabled"] != true {
		t.Errorf("body filtering_enabled = %v, want true", gotBody["filtering_enabled"])
	}
	if gotBody["use_global_settings"] != true {
		t.Errorf("body use_global_settings = %v, want true", gotBody["use_global_settings"])
	}
}

func TestAdGuardHomeRemovesClient(t *testing.T) {
	t.Parallel()
	var (
		gotMethod string
		gotPath   string
		gotUser   string
		gotPass   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotUser, gotPass, _ = r.BasicAuth()
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	}))
	t.Cleanup(server.Close)

	resolver, err := NewAdGuardHome(ResolverSettings{
		BaseURL:  server.URL,
		Username: "operator",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = resolver.RemoveClient(context.Background(), "device-1")
	if err != nil {
		t.Fatalf("RemoveClient: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/control/clients/delete" {
		t.Errorf("path = %s, want /control/clients/delete", gotPath)
	}
	if gotUser != "operator" || gotPass != "secret" {
		t.Errorf("credentials = %s:%s, want operator:secret", gotUser, gotPass)
	}
	if gotBody["name"] != "device-1" {
		t.Errorf("body name = %v, want device-1", gotBody["name"])
	}
}

func TestAdGuardHomeAddAndRemoveClientErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"client already exists"}`))
	}))
	t.Cleanup(server.Close)

	resolver, err := NewAdGuardHome(ResolverSettings{
		BaseURL:  server.URL,
		Username: "operator",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	ip := netip.MustParseAddr("10.66.0.2")
	if err := resolver.AddClient(context.Background(), "device-1", ip); !errors.Is(err, ErrResolverUnreachable) {
		t.Fatalf("AddClient on 400 error = %v, want ErrResolverUnreachable", err)
	}
	if err := resolver.RemoveClient(context.Background(), "device-1"); !errors.Is(err, ErrResolverUnreachable) {
		t.Fatalf("RemoveClient on 400 error = %v, want ErrResolverUnreachable", err)
	}
}
