package enforcement

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// The tunnel network from the security design, and the same bounds the
// nightloch-wg helper enforces. The host sits permanently at HostAddress and
// is never allocated to a Device: it is the tunnel's own address and the
// resolver every Device is pointed at.
var (
	tunnelNetwork = netip.MustParsePrefix("10.66.0.0/24")
	// HostAddress is the tunnel address of the droplet itself.
	HostAddress = netip.MustParseAddr("10.66.0.1")
)

const (
	firstDeviceOctet = 2
	lastDeviceOctet  = 254

	// keepaliveSeconds keeps a Device's tunnel alive through NAT, and is what
	// later Coverage reporting measures against.
	keepaliveSeconds = 25

	// fullTunnel routes every address family through the tunnel. The v6 half
	// is deliberate even though a Device holds no v6 address: without it a
	// dual-stack network resolves and routes around the enforcement plane, and
	// the product's stated fail mode is blackhole rather than open.
	fullTunnel = "0.0.0.0/0, ::/0"

	// publicKeyLength is a WireGuard key in base64: 43 characters and a pad.
	publicKeyLength = 44
)

// TunnelSettings is what a rendered Device configuration says about the server
// side of the tunnel. It is deployment configuration rather than per-Device
// state, so a Device rendered twice against the same settings is identical.
type TunnelSettings struct {
	// EndpointHost is the published tunnel hostname. A hostname rather than an
	// address is what lets the droplet be rebuilt without invalidating every
	// minted Device.
	EndpointHost string
	// EndpointPort is the tunnel's listening port.
	EndpointPort int
	// PublicKey is the tunnel interface's own public key. The server's private
	// key stays on the host and never reaches this process.
	PublicKey string
}

// ErrInvalidTunnelSettings rejects deployment configuration that could not be
// rendered into a working Device configuration.
var ErrInvalidTunnelSettings = errors.New("invalid tunnel settings")

// Validate reports whether these settings can be rendered. It is checked once,
// when the module is built, so rendering itself is total and pure.
func (s TunnelSettings) Validate() error {
	if err := validateEndpointHost(s.EndpointHost); err != nil {
		return err
	}
	if s.EndpointPort < 1 || s.EndpointPort > 65535 {
		return fmt.Errorf("%w: endpoint port %d is not a port", ErrInvalidTunnelSettings, s.EndpointPort)
	}
	if !IsPublicKey(s.PublicKey) {
		return fmt.Errorf("%w: tunnel public key is not a WireGuard public key", ErrInvalidTunnelSettings)
	}
	return nil
}

// validateEndpointHost accepts a bare hostname or IP literal and nothing else.
// Anything carrying a port, a scheme, or whitespace would either not resolve or
// would let deployment configuration write extra directives into every Device
// configuration this module renders.
func validateEndpointHost(host string) error {
	if host == "" {
		return fmt.Errorf("%w: endpoint host is required", ErrInvalidTunnelSettings)
	}
	if strings.ContainsAny(host, ":/ \t\r\n") {
		return fmt.Errorf("%w: endpoint host %q must be a bare hostname", ErrInvalidTunnelSettings, host)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return fmt.Errorf("%w: endpoint host %q is not a hostname", ErrInvalidTunnelSettings, host)
		}
		for _, r := range label {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
				continue
			}
			return fmt.Errorf("%w: endpoint host %q is not a hostname", ErrInvalidTunnelSettings, host)
		}
	}
	return nil
}

// IsPublicKey reports whether key is a WireGuard public key: 32 bytes in
// base64, which is 43 characters and a single trailing pad. It is the same
// shape the helper's own allowlist accepts.
func IsPublicKey(key string) bool {
	if len(key) != publicKeyLength || !strings.HasSuffix(key, "=") || strings.Contains(key[:publicKeyLength-1], "=") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	return err == nil && len(decoded) == 32
}

// RenderConfiguration is the canonical Device configuration: the exact bytes a
// Protectee installs. It is a pure function of the Device, its private key,
// and the deployment's tunnel settings — the same three inputs always produce
// the same file, so a configuration re-issued to a Protectee cannot silently
// differ from the one they already installed.
//
// The defaults are not options. Full tunnel means no traffic routes around
// enforcement. DNS at the tunnel address means resolution is enforced from the
// moment the tunnel comes up. The keepalive keeps NAT from dropping the tunnel
// and gives Coverage something real to measure. The endpoint is the published
// hostname so a rebuilt droplet does not invalidate the Device.
func RenderConfiguration(device Device, privateKey string, settings TunnelSettings) string {
	var config strings.Builder
	config.WriteString("[Interface]\n")
	config.WriteString("PrivateKey = " + privateKey + "\n")
	config.WriteString("Address = " + device.Address.String() + "/32\n")
	config.WriteString("DNS = " + HostAddress.String() + "\n")
	config.WriteString("\n")
	config.WriteString("[Peer]\n")
	config.WriteString("PublicKey = " + settings.PublicKey + "\n")
	config.WriteString("Endpoint = " + settings.EndpointHost + ":" + strconv.Itoa(settings.EndpointPort) + "\n")
	config.WriteString("AllowedIPs = " + fullTunnel + "\n")
	config.WriteString("PersistentKeepalive = " + strconv.Itoa(keepaliveSeconds) + "\n")
	return config.String()
}
