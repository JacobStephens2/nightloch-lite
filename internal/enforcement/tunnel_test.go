package enforcement

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests exercise the adapter's own contract: what the application makes
// of the helper's exit codes. The helper script itself is asserted separately
// in wghelper_test.go; here a stub stands in for sudo so every documented exit
// code can be produced on demand.
func newHelperTunnel(t *testing.T, script string) (*HelperTunnel, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the root helper is invoked through a POSIX shell")
	}
	root := t.TempDir()
	sudo := filepath.Join(root, "sudo")
	if err := os.WriteFile(sudo, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "nightloch-wg")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &HelperTunnel{path: helper, sudo: sudo}, filepath.Join(root, "argv")
}

// stubSudo is a sudo that records the argv it was handed, emits stdout, and
// exits with the given code. Each test builds its own rather than steering a
// shared one through the environment, so every one of them can run in
// parallel.
func stubSudo(exit int, stdout string) string {
	return `#!/bin/sh
{ for arg in "$@"; do printf '%s\n' "$arg"; done; } > "$(dirname "$0")/argv"
printf '%s' ` + shellQuote(stdout) + `
printf 'stub said so\n' >&2
exit ` + strconv.Itoa(exit) + `
`
}

// shellQuote wraps a value in single quotes for the stub script.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func TestHelperTunnelPassesEachOperationThroughSudoToTheOneHelperPath(t *testing.T) {
	t.Parallel()
	tunnel, argvPath := newHelperTunnel(t, stubSudo(0, ""))
	ctx := context.Background()

	if err := tunnel.AddPeer(ctx, serverPublicKey, netip.MustParseAddr("10.66.0.2")); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	assertArgv(t, argvPath, []string{tunnel.path, "add-peer", serverPublicKey, "10.66.0.2"})

	if err := tunnel.RemovePeer(ctx, serverPublicKey); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	assertArgv(t, argvPath, []string{tunnel.path, "remove-peer", serverPublicKey})

	if _, err := tunnel.Peers(ctx); err != nil {
		t.Fatalf("Peers: %v", err)
	}
	assertArgv(t, argvPath, []string{tunnel.path, "show"})
}

// Each documented exit code keeps its own identity all the way to the caller.
// Flattening them would leave an Operator unable to tell a deployment that is
// not finished from an enforcement plane that is down.
func TestHelperTunnelTranslatesEveryDocumentedExitCode(t *testing.T) {
	t.Parallel()
	for name, testCase := range map[string]struct {
		exit int
		want error
	}{
		"refusal":            {exitRefused, ErrHelperRefused},
		"unavailable tunnel": {exitUnavailable, ErrTunnelUnavailable},
		"command not found":  {exitNotFound, ErrHelperMissing},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tunnel, _ := newHelperTunnel(t, stubSudo(testCase.exit, ""))

			_, err := tunnel.Peers(context.Background())

			if !errors.Is(err, testCase.want) {
				t.Fatalf("exit %d produced %v, want %v", testCase.exit, err, testCase.want)
			}
		})
	}
}

func TestHelperTunnelReportsAnUninstalledHelperBeforeInvokingSudo(t *testing.T) {
	t.Parallel()
	tunnel, argvPath := newHelperTunnel(t, stubSudo(0, ""))
	if err := os.Remove(tunnel.path); err != nil {
		t.Fatal(err)
	}

	peers, err := tunnel.Peers(context.Background())

	if !errors.Is(err, ErrHelperMissing) {
		t.Fatalf("error = %v, want ErrHelperMissing", err)
	}
	if peers != nil {
		t.Fatal("a missing helper reported tunnel membership")
	}
	if _, err := os.Stat(argvPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a missing helper still invoked sudo")
	}
}

func TestHelperTunnelUnknownFailureIsNeitherARefusalNorAnAbsence(t *testing.T) {
	t.Parallel()
	tunnel, _ := newHelperTunnel(t, stubSudo(1, ""))

	_, err := tunnel.Peers(context.Background())

	if err == nil {
		t.Fatal("a failing helper reported success")
	}
	for _, known := range []error{ErrHelperMissing, ErrHelperRefused, ErrTunnelUnavailable} {
		if errors.Is(err, known) {
			t.Fatalf("an unrecognised exit was reported as %v", known)
		}
	}
}

func TestHelperTunnelReadsPeersAndRefusesAnUnreadableLine(t *testing.T) {
	t.Parallel()
	handshake := time.Unix(1755000000, 0).UTC()
	dump := serverPublicKey + "\t10.66.0.2/32\t1755000000\n" +
		devicePrivateKey + "\t10.66.0.9/32\t0\n"

	tunnel, _ := newHelperTunnel(t, stubSudo(0, dump))
	peers, err := tunnel.Peers(context.Background())
	if err != nil {
		t.Fatalf("Peers: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("%d peers, want 2", len(peers))
	}
	if peers[0].PublicKey != serverPublicKey || peers[0].AllowedIPs != "10.66.0.2/32" ||
		!peers[0].LatestHandshake.Equal(handshake) {
		t.Fatalf("first peer = %+v", peers[0])
	}
	if !peers[1].LatestHandshake.IsZero() {
		t.Fatalf("a peer that never handshook reported %v", peers[1].LatestHandshake)
	}

	// A line this parser cannot read is an error, not a peer silently dropped
	// out of membership.
	if _, err := parsePeers("only-one-field\n"); err == nil {
		t.Fatal("an unreadable peer line was accepted")
	}
	if _, err := parsePeers(serverPublicKey + "\t10.66.0.2/32\tnot-a-timestamp\n"); err == nil {
		t.Fatal("an unreadable handshake was accepted")
	}
}

func assertArgv(t *testing.T, path string, want []string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := splitLines(string(body))
	if !equalStrings(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func splitLines(body string) []string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSuffix(body, "\n"), "\n") {
		lines = append(lines, line)
	}
	return lines
}
