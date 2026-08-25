package enforcement

// These tests run the real `nightloch-wg` script — not a Go reimplementation of
// it — with stub `wg` and `ip` executables earlier on PATH. Every allowlisted
// subcommand is asserted on the exact arguments it passes through, and every
// refusal is asserted to invoke nothing at all. What the helper is and what
// bounds it: docs/adr/0004-reach-the-tunnel-through-a-root-helper.md

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// helperPath is the script under test, exactly as it is installed.
const helperPath = "../../deploy/nightloch-wg"

// sudoersPath is the grant that ships alongside the script.
const sudoersPath = "../../deploy/nightloch-wg.sudoers"

// installedHelperPath is the single path the sudoers grant may name.
const installedHelperPath = "/usr/local/sbin/nightloch-wg"

// A well-formed WireGuard public key: 43 base64 characters and a final pad.
const validKey = "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZWY="

const otherValidKey = "MDEyMzQ1Njc4OWFiY2RlZmdoaWprbG1ub3BxcnN0dXY="

// interfacePrivateKey is planted in the stub's `wg show ... dump` output. The
// helper must never put it on stdout: the unprivileged caller reading `show`
// would otherwise be handed the server's own identity.
const interfacePrivateKey = "c2VydmVycHJpdmF0ZWtleXNlcnZlcnByaXZhdGVrZXk="

// existingConfig is a wg-quick configuration carrying an existing peer, and a
// comment whose prose contains the literal "[Peer]" so section detection can be
// asserted not to mistake a directive's text for a section header.
const existingConfig = `[Interface]
Address = 10.66.0.1/24
ListenPort = 51820
PrivateKey = ` + interfacePrivateKey + `
PostUp = iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
# Peers below this line are managed by nightloch-wg; the [Peer] word here is prose.

[Peer]
PublicKey = ` + otherValidKey + `
AllowedIPs = 10.66.0.9/32
`

// dumpOutput is what the stub `wg show <iface> dump` reports: an interface line
// carrying the private key, then one tab-separated line per peer.
const dumpOutput = interfacePrivateKey + "\tcHVibGljcHVibGljcHVibGljcHVibGljcHVibGljcHU=\t51820\toff\n" +
	validKey + "\t(none)\t203.0.113.7:51820\t10.66.0.2/32\t1755000000\t1024\t2048\t25\n" +
	otherValidKey + "\t(none)\t(none)\t10.66.0.9/32\t0\t0\t0\t25\n"

// The exit codes the helper documents are the same constants the Go adapter
// in tunnel.go maps: a refusal is a request the allowlist does not accept, an
// unavailable tunnel is a plane that is down, and they must not be confused.
// Asserting the script against the caller's own constants is how the two are
// kept from drifting apart.

type helperRun struct {
	exitCode int
	stdout   string
	stderr   string
}

// helperEnv is one isolated invocation environment: a stub PATH, a scratch
// interface configuration, and the log every stub appends its argv to.
type helperEnv struct {
	t          *testing.T
	binDir     string
	configPath string
	logPath    string
}

func newHelperEnv(t *testing.T) *helperEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the root helper is a POSIX shell script")
	}
	root := t.TempDir()
	env := &helperEnv{
		t:          t,
		binDir:     filepath.Join(root, "bin"),
		configPath: filepath.Join(root, "wg0.conf"),
		logPath:    filepath.Join(root, "stub.log"),
	}
	if err := os.MkdirAll(env.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env.writeStub("wg", wgStub)
	env.writeStub("ip", ipStub)
	env.writeConfig(existingConfig)
	return env
}

const wgStub = `#!/bin/sh
{ printf 'wg'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
if [ -n "${NIGHTLOCH_STUB_WG_FAILS:-}" ]; then
	echo "wg: stub failure" >&2
	exit 1
fi
case "$1 $3" in
	"show dump") printf '%s' "$NIGHTLOCH_STUB_DUMP" ;;
esac
exit 0
`

const ipStub = `#!/bin/sh
{ printf 'ip'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
if [ -n "${NIGHTLOCH_STUB_IP_FAILS:-}" ]; then
	echo "ip: no such device" >&2
	exit 1
fi
exit 0
`

func (e *helperEnv) writeStub(name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.binDir, name), []byte(body), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *helperEnv) writeConfig(body string) {
	e.t.Helper()
	if err := os.WriteFile(e.configPath, []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *helperEnv) config() string {
	e.t.Helper()
	body, err := os.ReadFile(e.configPath)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(body)
}

// invocations is every argv the stubs saw, in order. An empty slice is the
// assertion that a refusal reached nothing.
func (e *helperEnv) invocations() []string {
	e.t.Helper()
	body, err := os.ReadFile(e.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	trimmed := strings.TrimSuffix(string(body), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func (e *helperEnv) run(args ...string) helperRun {
	e.t.Helper()
	return e.runWith(nil, args...)
}

func (e *helperEnv) runWith(extraEnv []string, args ...string) helperRun {
	e.t.Helper()
	abs, err := filepath.Abs(helperPath)
	if err != nil {
		e.t.Fatal(err)
	}
	cmd := exec.Command(abs, args...)
	cmd.Env = append([]string{
		"PATH=" + e.binDir + ":/usr/sbin:/usr/bin:/sbin:/bin",
		"NIGHTLOCH_WG_INTERFACE=wg0",
		"NIGHTLOCH_WG_CONFIG=" + e.configPath,
		"NIGHTLOCH_STUB_LOG=" + e.logPath,
		"NIGHTLOCH_STUB_DUMP=" + dumpOutput,
	}, extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	run := helperRun{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatalf("running helper: %v", err)
		}
		run.exitCode = exit.ExitCode()
	}
	return run
}

func assertInvocations(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("invocations = %q, want %q", got, want)
	}
}

func TestAddPeerPersistsToTheConfigurationAndSetsTheRunningInterface(t *testing.T) {
	env := newHelperEnv(t)

	run := env.run("add-peer", validKey, "10.66.0.2")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	assertInvocations(t, env.invocations(), []string{
		"ip link show dev wg0",
		"wg set wg0 peer " + validKey + " allowed-ips 10.66.0.2/32",
	})

	// Every line the helper does not manage survives verbatim, including the
	// directives wg-quick needs to bring the same interface back up and the
	// comment whose prose contains "[Peer]".
	want := existingConfig + `
[Peer]
PublicKey = ` + validKey + `
AllowedIPs = 10.66.0.2/32
`
	if got := env.config(); got != want {
		t.Fatalf("configuration =\n%s\nwant\n%s", got, want)
	}
}

func TestAddingAPeerTwiceRewritesItsOneBlock(t *testing.T) {
	env := newHelperEnv(t)

	if run := env.run("add-peer", validKey, "10.66.0.2"); run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	if run := env.run("add-peer", validKey, "10.66.0.3"); run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}

	config := env.config()
	if count := strings.Count(config, "PublicKey = "+validKey); count != 1 {
		t.Fatalf("peer appears %d times, want 1:\n%s", count, config)
	}
	if strings.Contains(config, "10.66.0.2/32") {
		t.Fatalf("the superseded address survived:\n%s", config)
	}
	if !strings.Contains(config, "10.66.0.3/32") {
		t.Fatalf("the new address is missing:\n%s", config)
	}
	// The Device that was already there is not collateral.
	if !strings.Contains(config, "PublicKey = "+otherValidKey) {
		t.Fatalf("an unrelated peer was lost:\n%s", config)
	}
}

func TestRemovePeerPersistsToTheConfigurationAndClearsTheRunningInterface(t *testing.T) {
	env := newHelperEnv(t)

	run := env.run("remove-peer", otherValidKey)

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	assertInvocations(t, env.invocations(), []string{
		"ip link show dev wg0",
		"wg set wg0 peer " + otherValidKey + " remove",
	})
	config := env.config()
	if strings.Contains(config, otherValidKey) || strings.Contains(config, "10.66.0.9/32") {
		t.Fatalf("removed peer survived in the configuration:\n%s", config)
	}
	if !strings.Contains(config, "Address = 10.66.0.1/24") {
		t.Fatalf("removal took the interface section with it:\n%s", config)
	}
}

func TestShowReportsHandshakeDataForEachPeerWithoutTheInterfacePrivateKey(t *testing.T) {
	env := newHelperEnv(t)

	run := env.run("show")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	assertInvocations(t, env.invocations(), []string{
		"ip link show dev wg0",
		"wg show wg0 dump",
	})

	want := validKey + "\t10.66.0.2/32\t1755000000\n" +
		otherValidKey + "\t10.66.0.9/32\t0\n"
	if run.stdout != want {
		t.Fatalf("show stdout = %q, want %q", run.stdout, want)
	}
	if strings.Contains(run.stdout, interfacePrivateKey) {
		t.Fatal("show disclosed the interface private key to its unprivileged caller")
	}
}

func TestShowMakesNoChangeToTheConfiguration(t *testing.T) {
	env := newHelperEnv(t)

	if run := env.run("show"); run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	if env.config() != existingConfig {
		t.Fatalf("show rewrote the configuration:\n%s", env.config())
	}
}

// refusalCases are every argv the helper must refuse. Each is asserted to exit
// non-zero, invoke no stub, and leave the configuration byte-for-byte intact.
func refusalCases() map[string][]string {
	injections := []string{
		"; rm -rf /",
		"$(id)",
		"`id`",
		"&& touch /tmp/nightloch-pwned",
		"| tee /etc/passwd",
		"10.66.0.2; wg set wg0 peer x remove",
		"--remove",
		"-n",
	}
	cases := map[string][]string{
		"no subcommand":               {},
		"unknown subcommand":          {"status"},
		"help is not a subcommand":    {"--help"},
		"flag as subcommand":          {"-x"},
		"prefix of a subcommand":      {"add"},
		"subcommand with a suffix":    {"add-peerx", validKey, "10.66.0.2"},
		"show takes no argument":      {"show", "wg0"},
		"add-peer without arguments":  {"add-peer"},
		"add-peer without an address": {"add-peer", validKey},
		"add-peer with an extra":      {"add-peer", validKey, "10.66.0.2", "extra"},
		"remove-peer without a key":   {"remove-peer"},
		"remove-peer with an extra":   {"remove-peer", validKey, "extra"},

		"key too short":         {"add-peer", validKey[:43], "10.66.0.2"},
		"key too long":          {"add-peer", validKey + "=", "10.66.0.2"},
		"key without padding":   {"add-peer", strings.Replace(validKey, "=", "A", 1), "10.66.0.2"},
		"key padded mid-string": {"add-peer", "QUJDREVGR0hJ=ktMTU5PUFFSU1RVVldYWVphYmNkZWY=", "10.66.0.2"},
		"key with a space":      {"add-peer", "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVph YmNkZWY=", "10.66.0.2"},
		"key outside base64":    {"add-peer", "QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZW!=", "10.66.0.2"},
		"empty key":             {"add-peer", "", "10.66.0.2"},

		"address with a prefix length": {"add-peer", validKey, "10.66.0.2/32"},
		"address out of range":         {"add-peer", validKey, "10.66.0.256"},
		"address with a leading zero":  {"add-peer", validKey, "10.66.0.02"},
		"address off the tunnel":       {"add-peer", validKey, "10.66.1.2"},
		"address is the host":          {"add-peer", validKey, "10.66.0.1"},
		"address is the network":       {"add-peer", validKey, "10.66.0.0"},
		"address is the broadcast":     {"add-peer", validKey, "10.66.0.255"},
		"address is a hostname":        {"add-peer", validKey, "vpn.nightloch.app"},
		"empty address":                {"add-peer", validKey, ""},
	}
	for _, injection := range injections {
		cases["injected key: "+injection] = []string{"add-peer", injection, "10.66.0.2"}
		cases["injected address: "+injection] = []string{"add-peer", validKey, injection}
		cases["injected removal key: "+injection] = []string{"remove-peer", injection}
		cases["injected subcommand: "+injection] = []string{injection}
	}
	return cases
}

func TestEveryRefusalExitsNonZeroAndInvokesNothing(t *testing.T) {
	for name, args := range refusalCases() {
		t.Run(name, func(t *testing.T) {
			env := newHelperEnv(t)

			run := env.run(args...)

			if run.exitCode != exitRefused {
				t.Fatalf("exit = %d for %q, want %d; stdout = %q", run.exitCode, args, exitRefused, run.stdout)
			}
			if got := env.invocations(); len(got) != 0 {
				t.Fatalf("refusal invoked %q", got)
			}
			if env.config() != existingConfig {
				t.Fatalf("refusal changed the configuration:\n%s", env.config())
			}
			if run.stdout != "" {
				t.Fatalf("refusal wrote to stdout: %q", run.stdout)
			}
			if run.stderr == "" {
				t.Fatal("refusal said nothing on stderr")
			}
		})
	}
}

func TestAMissingConfigurationRefusesBeforeTouchingTheInterface(t *testing.T) {
	env := newHelperEnv(t)
	if err := os.Remove(env.configPath); err != nil {
		t.Fatal(err)
	}

	run := env.run("add-peer", validKey, "10.66.0.2")

	if run.exitCode != exitUnavailable {
		t.Fatalf("exit = %d, want %d (the tunnel is not in a state to change)", run.exitCode, exitUnavailable)
	}
	if got := env.invocations(); len(got) != 0 {
		t.Fatalf("missing configuration invoked %q", got)
	}
}

func TestAnAbsentInterfaceRefusesBeforeInvokingWireGuard(t *testing.T) {
	env := newHelperEnv(t)

	run := env.runWith([]string{"NIGHTLOCH_STUB_IP_FAILS=1"}, "add-peer", validKey, "10.66.0.2")

	if run.exitCode != exitUnavailable {
		t.Fatalf("exit = %d, want %d (the tunnel is not in a state to change)", run.exitCode, exitUnavailable)
	}
	assertInvocations(t, env.invocations(), []string{"ip link show dev wg0"})
	if env.config() != existingConfig {
		t.Fatalf("absent interface still rewrote the configuration:\n%s", env.config())
	}
}

func TestAConfigurationWithNoInterfaceSectionIsRefusedRatherThanRewritten(t *testing.T) {
	// Replacing a configuration this helper did not understand would leave the
	// tunnel unable to come back up at all, which is worse than dropping a
	// Device.
	for name, body := range map[string]string{
		"empty":            "",
		"peers only":       "[Peer]\nPublicKey = " + otherValidKey + "\n",
		"unrecognised":     "nonsense\n",
		"commented header": "# [Interface]\nAddress = 10.66.0.1/24\n",
	} {
		t.Run(name, func(t *testing.T) {
			env := newHelperEnv(t)
			env.writeConfig(body)

			run := env.run("add-peer", validKey, "10.66.0.2")

			if run.exitCode != exitUnavailable {
				t.Fatalf("exit = %d, want %d; stderr = %q", run.exitCode, exitUnavailable, run.stderr)
			}
			if got := env.invocations(); len(got) != 0 {
				t.Fatalf("an unrecognised configuration still invoked %q", got)
			}
			if env.config() != body {
				t.Fatalf("an unrecognised configuration was rewritten:\n%s", env.config())
			}
		})
	}
}

func TestARefusedInterfaceChangeLeavesNoRenderedConfigurationBehind(t *testing.T) {
	env := newHelperEnv(t)

	run := env.runWith([]string{"NIGHTLOCH_STUB_WG_FAILS=1"}, "add-peer", validKey, "10.66.0.2")

	if run.exitCode == 0 {
		t.Fatal("helper reported success after wg refused the change")
	}
	if env.config() != existingConfig {
		t.Fatalf("a refused wg set still rewrote the configuration:\n%s", env.config())
	}
	// The rendered file is a scratch file, not a second configuration for an
	// operator to find and wonder about.
	entries, err := os.ReadDir(filepath.Dir(env.configPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "nightloch-pending") {
			t.Fatalf("a rendered configuration was left behind: %s", entry.Name())
		}
	}
}

func TestTheSudoersGrantNamesExactlyOnePathAndNoWildcard(t *testing.T) {
	body, err := os.ReadFile(sudoersPath)
	if err != nil {
		t.Fatal(err)
	}
	grant := string(body)

	if strings.Contains(grant, "*") || strings.Contains(grant, "?") {
		t.Fatalf("sudoers grant contains a wildcard:\n%s", grant)
	}
	// Only the directives are the grant; the commentary above them explains
	// where the file itself is installed.
	var directives []string
	for _, line := range strings.Split(grant, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			directives = append(directives, line)
		}
	}
	paths := map[string]bool{}
	for _, field := range strings.Fields(strings.ReplaceAll(strings.Join(directives, " "), ",", " ")) {
		if strings.HasPrefix(field, "/") {
			paths[field] = true
		}
	}
	if len(paths) != 1 || !paths[installedHelperPath] {
		t.Fatalf("sudoers grant names %v, want only %q", paths, installedHelperPath)
	}
	// The grant, not the caller's environment, decides which wg and ip the
	// helper finds.
	for _, required := range []string{"env_reset", "secure_path"} {
		if !strings.Contains(grant, required) {
			t.Fatalf("sudoers grant is missing %q:\n%s", required, grant)
		}
	}
}

func TestTheHelperIsInstallableAsAnExecutableScript(t *testing.T) {
	info, err := os.Stat(helperPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("helper is not executable: mode %v", info.Mode().Perm())
	}
}
