package config

import "testing"

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
