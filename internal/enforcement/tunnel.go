package enforcement

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Tunnel is the enforcement plane's WireGuard dependency, injected exactly as
// the mail provider is. Everything privileged happens on the other side of it,
// so the module that owns Devices can be tested in full without a droplet.
//
// Its three operations are the three the nightloch-wg helper exposes, and no
// more: the interface is the escalation surface, so widening it is a decision
// rather than an accident.
type Tunnel interface {
	// AddPeer admits a Device's public key at one tunnel address. Adding a key
	// that is already present resets its address rather than adding a second.
	AddPeer(ctx context.Context, publicKey string, address netip.Addr) error
	// RemovePeer withdraws a Device's public key from the tunnel.
	RemovePeer(ctx context.Context, publicKey string) error
	// Peers reports current tunnel membership. It never reports an empty
	// membership for a tunnel it could not read: that is what the errors below
	// are for.
	Peers(ctx context.Context) ([]Peer, error)
}

// Peer is one tunnel membership as the helper reports it. LatestHandshake is
// the zero time when the peer has never completed one.
type Peer struct {
	PublicKey       string
	AllowedIPs      string
	LatestHandshake time.Time
}

// The three ways reaching the tunnel fails, kept distinct because they call for
// three different responses. A missing helper is a deployment that is not
// finished; a refusal is a caller that asked for something the helper's
// allowlist does not accept, which is a bug in this code rather than in the
// host; an unavailable tunnel is an enforcement plane that is down. None of
// them may ever be flattened into "no Devices are on the tunnel".
var (
	// ErrHelperMissing means the privileged helper is not installed where the
	// sudoers grant names it.
	ErrHelperMissing = errors.New("the nightloch-wg helper is not installed")
	// ErrHelperRefused means the helper rejected the subcommand or an argument.
	ErrHelperRefused = errors.New("the nightloch-wg helper refused the request")
	// ErrTunnelUnavailable means the tunnel is not in a state the operation
	// could be performed against.
	ErrTunnelUnavailable = errors.New("the tunnel interface is not available")
)

// The exit codes nightloch-wg documents. 127 is the shell's own report of a
// command that is not there, which sudo passes through.
const (
	exitRefused     = 2
	exitUnavailable = 3
	exitNotFound    = 127
)

// HelperPath is the one absolute path the sudoers grant names. It is a
// constant rather than configuration on purpose: a caller that could choose
// the path could choose a program the grant was never written for.
const HelperPath = "/usr/local/sbin/nightloch-wg"

// HelperTunnel reaches the tunnel by invoking the privileged helper under
// sudo, which is the whole of the application's escalation
// (docs/adr/0004-reach-the-tunnel-through-a-root-helper.md). Its zero value is
// the production adapter.
//
// The two overrides are unexported and have no exported setter, so no caller
// outside this package can aim the escalation at a program the sudoers grant
// was never written for. They exist for this package's own tests, which have
// to produce each of the helper's documented exit codes on demand.
type HelperTunnel struct {
	path string
	sudo string
}

func (t *HelperTunnel) helperPath() string {
	if t.path != "" {
		return t.path
	}
	return HelperPath
}

func (t *HelperTunnel) sudoPath() string {
	if t.sudo != "" {
		return t.sudo
	}
	return "sudo"
}

func (t *HelperTunnel) AddPeer(ctx context.Context, publicKey string, address netip.Addr) error {
	_, err := t.run(ctx, "add-peer", publicKey, address.String())
	return err
}

func (t *HelperTunnel) RemovePeer(ctx context.Context, publicKey string) error {
	_, err := t.run(ctx, "remove-peer", publicKey)
	return err
}

func (t *HelperTunnel) Peers(ctx context.Context) ([]Peer, error) {
	stdout, err := t.run(ctx, "show")
	if err != nil {
		return nil, err
	}
	return parsePeers(stdout)
}

// run invokes the helper and translates its exit code into one of this
// package's errors. A helper that is not installed is recognised before sudo
// is invoked as well as after: sudo reports an absent command in its own way,
// and the caller must get the same answer either way.
func (t *HelperTunnel) run(ctx context.Context, args ...string) (string, error) {
	if _, err := os.Stat(t.helperPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w at %s", ErrHelperMissing, t.helperPath())
		}
		return "", fmt.Errorf("inspect %s: %w", t.helperPath(), err)
	}
	command := exec.CommandContext(ctx, t.sudoPath(), append([]string{t.helperPath()}, args...)...)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("%w: %w", ErrHelperMissing, err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "", fmt.Errorf("invoke nightloch-wg %s: %w", args[0], err)
	}
	detail := strings.TrimSpace(stderr.String())
	switch exit.ExitCode() {
	case exitRefused:
		return "", fmt.Errorf("%w: %s", ErrHelperRefused, detail)
	case exitUnavailable:
		return "", fmt.Errorf("%w: %s", ErrTunnelUnavailable, detail)
	case exitNotFound:
		return "", fmt.Errorf("%w at %s", ErrHelperMissing, t.helperPath())
	default:
		return "", fmt.Errorf("nightloch-wg %s failed (exit %d): %s", args[0], exit.ExitCode(), detail)
	}
}

// parsePeers reads the helper's show output: one tab-separated line per peer
// carrying public key, allowed IPs, and the latest handshake as a Unix
// timestamp. A line this parser cannot read is an error rather than a peer
// quietly dropped from membership.
func parsePeers(stdout string) ([]Peer, error) {
	var peers []Peer
	for _, line := range strings.Split(strings.TrimSuffix(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("nightloch-wg show reported an unreadable peer line %q", line)
		}
		seconds, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("nightloch-wg show reported an unreadable handshake %q", fields[2])
		}
		peer := Peer{PublicKey: fields[0], AllowedIPs: fields[1]}
		if seconds > 0 {
			peer.LatestHandshake = time.Unix(seconds, 0).UTC()
		}
		peers = append(peers, peer)
	}
	return peers, nil
}
