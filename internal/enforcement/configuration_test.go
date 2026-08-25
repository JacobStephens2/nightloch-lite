package enforcement

import (
	"net/netip"
	"strings"
	"testing"
)

func testSettings() TunnelSettings {
	return TunnelSettings{
		EndpointHost: "vpn.nightloch.app",
		EndpointPort: 51820,
		PublicKey:    serverPublicKey,
	}
}

const serverPublicKey = "c2VydmVycHVibGljc2VydmVycHVibGljc2VydmVycHU="

const devicePrivateKey = "ZGV2aWNlcHJpdmF0ZWtleWRldmljZXByaXZhdGVrZXk="

func testDevice() Device {
	return Device{
		ID:          7,
		ProtecteeID: 1,
		Name:        "Pixel",
		PublicKey:   "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWY=",
		Address:     netip.MustParseAddr("10.66.0.2"),
	}
}

// The rendered configuration is the one artifact a Protectee installs by hand,
// so it is asserted byte for byte rather than by substring. Every default in
// it was settled by the security design: full tunnel so nothing routes around
// enforcement, DNS at the tunnel address so resolution is enforced from the
// moment the tunnel comes up, a keepalive so NAT does not silently drop the
// tunnel, and the published hostname so the droplet can be rebuilt without
// invalidating the Device.
func TestRenderConfigurationIsTheCanonicalDeviceConfiguration(t *testing.T) {
	t.Parallel()

	got := RenderConfiguration(testDevice(), devicePrivateKey, testSettings())

	want := "[Interface]\n" +
		"PrivateKey = " + devicePrivateKey + "\n" +
		"Address = 10.66.0.2/32\n" +
		"DNS = 10.66.0.1\n" +
		"\n" +
		"[Peer]\n" +
		"PublicKey = " + serverPublicKey + "\n" +
		"Endpoint = vpn.nightloch.app:51820\n" +
		"AllowedIPs = 0.0.0.0/0, ::/0\n" +
		"PersistentKeepalive = 25\n"
	if got != want {
		t.Fatalf("configuration =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderConfigurationIsPure(t *testing.T) {
	t.Parallel()
	first := RenderConfiguration(testDevice(), devicePrivateKey, testSettings())

	for range 100 {
		if again := RenderConfiguration(testDevice(), devicePrivateKey, testSettings()); again != first {
			t.Fatalf("re-rendering the same Device produced different bytes:\n%s\n%s", first, again)
		}
	}
}

func TestRenderConfigurationCarriesNoServerPrivateMaterial(t *testing.T) {
	t.Parallel()
	// The only private key in the file is the Device's own, which the server
	// does not keep. Nothing about the interface's identity belongs here.
	config := RenderConfiguration(testDevice(), devicePrivateKey, testSettings())

	if count := strings.Count(config, "PrivateKey"); count != 1 {
		t.Fatalf("configuration names PrivateKey %d times, want 1:\n%s", count, config)
	}
}

func TestTunnelSettingsRefuseWhatCannotBeRendered(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*TunnelSettings){
		"no endpoint host":        func(s *TunnelSettings) { s.EndpointHost = "" },
		"endpoint carries a port": func(s *TunnelSettings) { s.EndpointHost = "vpn.nightloch.app:51820" },
		"endpoint is a URL":       func(s *TunnelSettings) { s.EndpointHost = "https://vpn.nightloch.app" },
		"endpoint has a newline":  func(s *TunnelSettings) { s.EndpointHost = "vpn.nightloch.app\nDNS = 8.8.8.8" },
		"port is zero":            func(s *TunnelSettings) { s.EndpointPort = 0 },
		"port is out of range":    func(s *TunnelSettings) { s.EndpointPort = 70000 },
		"no public key":           func(s *TunnelSettings) { s.PublicKey = "" },
		"public key is short":     func(s *TunnelSettings) { s.PublicKey = serverPublicKey[:43] },
		"public key is not base64": func(s *TunnelSettings) {
			s.PublicKey = strings.Replace(serverPublicKey, "c", "!", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			settings := testSettings()
			mutate(&settings)
			if err := settings.Validate(); err == nil {
				t.Fatalf("Validate accepted %s: %+v", name, settings)
			}
		})
	}
	if err := testSettings().Validate(); err != nil {
		t.Fatalf("Validate rejected the shipped defaults: %v", err)
	}
}
