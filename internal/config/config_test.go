package config

import (
	"path/filepath"
	"testing"

	"github.com/JacobStephens2/nightloch-lite/internal/enforcement"
)

func TestParseBaseURL(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		"", "http://lite.nightloch.app", "https://lite.nightloch.app/path",
		"https://user@lite.nightloch.app", "https://lite.nightloch.app?q=x",
	} {
		if _, err := parseBaseURL(bad); err == nil {
			t.Errorf("parseBaseURL(%q) succeeded, want error", bad)
		}
	}
	got, err := parseBaseURL("https://lite.nightloch.app/")
	if err != nil {
		t.Fatalf("parseBaseURL(valid): %v", err)
	}
	if got.String() != "https://lite.nightloch.app" {
		t.Fatalf("base URL = %q", got.String())
	}
}

func TestValidateLoopbackAddr(t *testing.T) {
	t.Parallel()

	for _, good := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if err := validateLoopbackAddr(good); err != nil {
			t.Errorf("validateLoopbackAddr(%q): %v", good, err)
		}
	}
	for _, bad := range []string{":8080", "0.0.0.0:8080", "10.0.0.1:8080", "broken"} {
		if err := validateLoopbackAddr(bad); err == nil {
			t.Errorf("validateLoopbackAddr(%q) succeeded, want error", bad)
		}
	}
}

func TestLoadTunnelDefaultsThePortAndRequiresTheRest(t *testing.T) {
	// t.Setenv rules this test out of parallel execution, which is the price
	// of reading the real environment the Operator commands read.
	t.Setenv("TUNNEL_ENDPOINT_HOST", "vpn.nightloch.app")
	t.Setenv("TUNNEL_PUBLIC_KEY", " c2VydmVycHVibGljc2VydmVycHVibGljc2VydmVycHU= ")
	t.Setenv("TUNNEL_ENDPOINT_PORT", "")

	tunnel, err := LoadTunnel()
	if err != nil {
		t.Fatalf("LoadTunnel: %v", err)
	}
	if tunnel.EndpointPort != defaultTunnelPort {
		t.Fatalf("port = %d, want the port Slice 1 already opens (%d)", tunnel.EndpointPort, defaultTunnelPort)
	}
	if tunnel.PublicKey != "c2VydmVycHVibGljc2VydmVycHVibGljc2VydmVycHU=" {
		t.Fatalf("public key = %q, want it trimmed", tunnel.PublicKey)
	}

	t.Setenv("TUNNEL_ENDPOINT_PORT", "51821")
	if tunnel, err = LoadTunnel(); err != nil || tunnel.EndpointPort != 51821 {
		t.Fatalf("LoadTunnel with an explicit port = %+v, %v", tunnel, err)
	}
	t.Setenv("TUNNEL_ENDPOINT_PORT", "not-a-port")
	if _, err := LoadTunnel(); err == nil {
		t.Fatal("LoadTunnel accepted a port that is not a number")
	}

	t.Setenv("TUNNEL_ENDPOINT_PORT", "")
	for _, missing := range []string{"TUNNEL_ENDPOINT_HOST", "TUNNEL_PUBLIC_KEY"} {
		t.Run("without "+missing, func(t *testing.T) {
			t.Setenv(missing, "")
			if _, err := LoadTunnel(); err == nil {
				t.Fatalf("LoadTunnel succeeded without %s", missing)
			}
		})
	}
}

// Serving without AdGuard Home's administration credentials would leave the
// process unable to say whether anything is being filtered, so Load requires
// them. Only the URL has a default, because the deployment always puts the
// administration endpoint on the same loopback port.
func TestLoadRequiresTheResolverAdministrationCredentials(t *testing.T) {
	t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "nightloch.db"))
	t.Setenv("BASE_URL", "https://lite.nightloch.app")
	t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
	t.Setenv("RESEND_API_KEY", "re_test")
	t.Setenv("RESEND_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("TURNSTILE_SECRET_KEY", "turnstile-secret")
	t.Setenv("TURNSTILE_SITE_KEY", "turnstile-site")
	t.Setenv("ADGUARD_URL", "")
	t.Setenv("ADGUARD_USERNAME", "operator")
	t.Setenv("ADGUARD_PASSWORD", "admin-secret")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if settings.Resolver.BaseURL != defaultResolverURL {
		t.Fatalf("resolver URL = %q, want the loopback default %q", settings.Resolver.BaseURL, defaultResolverURL)
	}
	if _, err := enforcement.NewAdGuardHome(settings.Resolver); err != nil {
		t.Fatalf("the loaded settings do not build a resolver: %v", err)
	}

	for _, missing := range []string{"ADGUARD_USERNAME", "ADGUARD_PASSWORD"} {
		t.Run("without "+missing, func(t *testing.T) {
			t.Setenv(missing, "")
			if _, err := Load(); err == nil {
				t.Fatalf("Load succeeded without %s", missing)
			}
		})
	}
}

func TestLoadEnrollmentAllowlist(t *testing.T) {
	setValidEnv := func() {
		t.Setenv("DATABASE_PATH", filepath.Join(t.TempDir(), "nightloch.db"))
		t.Setenv("BASE_URL", "https://lite.nightloch.app")
		t.Setenv("HTTP_ADDR", "127.0.0.1:8080")
		t.Setenv("RESEND_API_KEY", "re_test")
		t.Setenv("RESEND_WEBHOOK_SECRET", "whsec_test")
		t.Setenv("TURNSTILE_SECRET_KEY", "turnstile-secret")
		t.Setenv("TURNSTILE_SITE_KEY", "turnstile-site")
		t.Setenv("ADGUARD_URL", "")
		t.Setenv("ADGUARD_USERNAME", "operator")
		t.Setenv("ADGUARD_PASSWORD", "admin-secret")
	}

	t.Run("empty or unset closes enrollment to all users", func(t *testing.T) {
		setValidEnv()
		t.Setenv("ENROLLMENT_ALLOWLIST", "")
		settings, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(settings.EnrollmentAllowlist) != 0 {
			t.Errorf("len(EnrollmentAllowlist) = %d, want 0", len(settings.EnrollmentAllowlist))
		}
	})

	t.Run("normalizes and trims comma-separated emails", func(t *testing.T) {
		setValidEnv()
		t.Setenv("ENROLLMENT_ALLOWLIST", " Protectee@Example.COM , another@domain.org ")
		settings, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(settings.EnrollmentAllowlist) != 2 {
			t.Fatalf("len(EnrollmentAllowlist) = %d, want 2", len(settings.EnrollmentAllowlist))
		}
		if _, ok := settings.EnrollmentAllowlist["protectee@example.com"]; !ok {
			t.Errorf("protectee@example.com not in allowlist map")
		}
		if _, ok := settings.EnrollmentAllowlist["another@domain.org"]; !ok {
			t.Errorf("another@domain.org not in allowlist map")
		}
		if _, ok := settings.EnrollmentAllowlist["stranger@example.com"]; ok {
			t.Errorf("stranger@example.com unexpectedly in allowlist map")
		}
	})

	t.Run("rejects invalid email address in allowlist", func(t *testing.T) {
		setValidEnv()
		t.Setenv("ENROLLMENT_ALLOWLIST", "valid@example.com, not-an-email")
		if _, err := Load(); err == nil {
			t.Fatal("Load succeeded with invalid email in ENROLLMENT_ALLOWLIST")
		}
	})
}

