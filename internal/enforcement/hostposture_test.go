package enforcement

// These tests assert the host's persistent tunnel and NAT posture - the real
// files in deploy/, not a Go restatement of them. The droplet does not exist
// yet, so the posture is asserted the only honest way it can be before it runs
// on a host: the artifacts are read as shipped, the scripts are executed
// against a scratch root with stub executables earlier on PATH, and every
// command they would issue on a live host is asserted by argv.
//
// What the posture is and why it lives in its own nftables table:
// docs/adr/0006-keep-the-nat-posture-in-its-own-nftables-table.md

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const (
	rulesetPath   = "../../deploy/nightloch-nat.nft"
	unitPath      = "../../deploy/nightloch-nat.service"
	sysctlPath    = "../../deploy/99-nightloch-forward.conf"
	wgTemplate    = "../../deploy/wg0.conf.template"
	posturePath   = "../../deploy/nightloch-host-posture.sh"
	verifyPath    = "../../deploy/nightloch-nat-verify.sh"
	tofuMainPath  = "../../infra/opentofu/main.tf"
	envSamplePath = "../../deploy/nightloch.env.example"
)

// tunnelInterface is the one interface name the whole posture is written
// against. Every rule matches it by name rather than by index, which is what
// lets the ruleset load before wg-quick has ever created it.
const tunnelInterface = "wg0"

func readArtifact(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

func requireContains(t *testing.T, path, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s does not contain %q", path, want)
		}
	}
}

func requireAbsent(t *testing.T, path, body string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(body, bad) {
			t.Errorf("%s contains %q, which it must not", path, bad)
		}
	}
}

// stripComments drops comment lines so an assertion about what the ruleset
// does is not satisfied by prose describing what it does not do.
func stripComments(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestRulesetCarriesTheWholeDNSPostureForTheTunnel(t *testing.T) {
	rules := stripComments(readArtifact(t, rulesetPath))

	// Port 53 from the tunnel reaches the resolver whatever a Device was told
	// to use, over both transports.
	requireContains(t, rulesetPath, rules,
		`iifname "`+tunnelInterface+`" udp dport 53 redirect to :53`,
		`iifname "`+tunnelInterface+`" tcp dport 53 redirect to :53`,
	)

	// Port 853 fails fast and visibly rather than hanging. A Device set to
	// strict Private DNS must learn that it cannot resolve, not sit silent.
	requireContains(t, rulesetPath, rules,
		`iifname "`+tunnelInterface+`" tcp dport 853 reject with tcp reset`,
		`iifname "`+tunnelInterface+`" udp dport 853 reject`,
	)
	requireAbsent(t, rulesetPath, rules, "dport 853 drop")

	// The host carries tunnel traffic out, and does not masquerade traffic
	// that never leaves the tunnel.
	requireContains(t, rulesetPath, rules,
		"ip saddr "+tunnelNetwork.String()+` oifname != "`+tunnelInterface+`" masquerade`,
	)
}

func TestRulesetNeverFlushesRulesItDoesNotOwn(t *testing.T) {
	rules := readArtifact(t, rulesetPath)

	// ufw owns its own tables on this host. A `flush ruleset` here would wipe
	// them on every load of this file, so the posture replaces exactly one
	// table of its own instead.
	requireAbsent(t, rulesetPath, stripComments(rules), "flush ruleset")
	requireContains(t, rulesetPath, stripComments(rules),
		"table inet nightloch {}",
		"delete table inet nightloch",
	)
}

func TestRulesetMatchesTheInterfaceByNameSoItLoadsBeforeTheTunnelExists(t *testing.T) {
	rules := stripComments(readArtifact(t, rulesetPath))

	// `iif`/`oif` resolve an interface index when the ruleset is parsed, so a
	// ruleset written that way refuses to load on a boot where wg-quick has
	// not run yet - exactly the reboot this posture has to survive.
	for _, line := range strings.Split(rules, "\n") {
		fields := strings.Fields(line)
		for _, field := range fields {
			if field == "iif" || field == "oif" {
				t.Errorf("%s matches an interface by index: %q", rulesetPath, strings.TrimSpace(line))
			}
		}
	}
}

func TestFilterChainsActBeforeTheHostFirewallCanDropThePacket(t *testing.T) {
	rules := stripComments(readArtifact(t, rulesetPath))

	// ufw's own input policy is deny. A reject that hooked after it would
	// never be reached, and a Device pointed at DoT would hang on a drop
	// instead of failing visibly, so both filter chains hook ahead of it.
	requireContains(t, rulesetPath, rules,
		"type filter hook input priority filter - 10;",
		"type filter hook forward priority filter - 10;",
	)
}

func TestRulesetIsAcceptedByNftWhenTheKernelPermitsAParseCheck(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skip("nft is not installed")
	}
	abs, err := filepath.Abs(rulesetPath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(nft, "--check", "--file", abs).CombinedOutput()
	if err == nil {
		return
	}
	// An unprivileged checker cannot initialise the kernel's cache. That is
	// the environment refusing, not the ruleset failing.
	if strings.Contains(string(output), "Operation not permitted") {
		t.Skipf("nft --check needs privileges here: %s", strings.TrimSpace(string(output)))
	}
	t.Fatalf("nft --check rejected %s: %s", rulesetPath, output)
}

func TestForwardingIsPersistentAndIPv6ForwardingStaysOff(t *testing.T) {
	sysctl := stripComments(readArtifact(t, sysctlPath))

	requireContains(t, sysctlPath, sysctl, "net.ipv4.ip_forward = 1")

	// A Device routes ::/0 into the tunnel and holds no v6 address. Leaving v6
	// forwarding off makes that a blackhole rather than a path around the
	// resolver, which is the product's stated fail mode.
	requireContains(t, sysctlPath, sysctl,
		"net.ipv6.conf.all.forwarding = 0",
		"net.ipv6.conf.default.forwarding = 0",
	)
}

func TestNATUnitRestoresThePostureOnAnUnattendedReboot(t *testing.T) {
	unit := readArtifact(t, unitPath)

	requireContains(t, unitPath, unit,
		"Type=oneshot",
		"RemainAfterExit=yes",
		"ExecStart=/usr/sbin/nft -f /etc/nightloch/nightloch-nat.nft",
		// Without an install section the unit can be started but never comes
		// back by itself, which is the reboot this ticket is about.
		"WantedBy=multi-user.target",
		// ufw reapplies its own tables on start; ordering after it keeps the
		// two from racing over a boot.
		"After=network-pre.target ufw.service",
		// The rules match wg0 by name, so they may load first, and loading
		// first means a Device that reconnects immediately after a reboot
		// never sees a window with forwarding but no DNS redirect.
		"Before=wg-quick@wg0.service",
	)
	// A stop with no table present must not fail the unit.
	requireContains(t, unitPath, unit, "ExecStop=-/usr/sbin/nft delete table inet nightloch")
}

func TestTunnelInterfaceTemplateHoldsTheHostAddressAndNoPeer(t *testing.T) {
	template := readArtifact(t, wgTemplate)

	requireContains(t, wgTemplate, template,
		"[Interface]",
		"Address = "+HostAddress.String()+"/"+strconv.Itoa(tunnelNetwork.Bits()),
		"ListenPort = 51820",
	)

	// Peers are the helper's business. A [Peer] section shipped in the
	// template would be a Device nobody minted.
	requireAbsent(t, wgTemplate, stripComments(template), "[Peer]")

	// The template carries a placeholder, never a key. A committed private
	// key would be the tunnel's identity in git.
	requireContains(t, wgTemplate, template, "PrivateKey = __PRIVATE_KEY__")
}

func TestEveryArtifactNamesTheSameTunnelPort(t *testing.T) {
	// The port the cloud firewall opens, the port a minted Device dials, and
	// the port the interface listens on are one fact. Slice 1 already opened
	// it, so the tunnel comes up without revising the firewall.
	const port = "51820"

	requireContains(t, tofuMainPath, readArtifact(t, tofuMainPath), `port_range       = "`+port+`"`)
	requireContains(t, envSamplePath, readArtifact(t, envSamplePath), "TUNNEL_ENDPOINT_PORT="+port)
	requireContains(t, wgTemplate, readArtifact(t, wgTemplate), "ListenPort = "+port)
	requireContains(t, cloudInitPath, readArtifact(t, cloudInitPath), "ufw allow "+port+"/udp")
}

const cloudInitPath = "../../infra/opentofu/cloud-init.yaml.tftpl"

func TestCloudInitInstallsWhatThePostureNeedsToExist(t *testing.T) {
	cloudInit := readArtifact(t, cloudInitPath)

	// First boot brings the packages; it does not bring forwarding up. The
	// first forwarding change is the human's, under docs/ops/NAT-CHECKLIST.md.
	requireContains(t, cloudInitPath, cloudInit, "  - wireguard\n", "  - nftables\n")
	requireAbsent(t, cloudInitPath, cloudInit,
		"ip_forward",
		"MASQUERADE",
		"nightloch-nat.service",
	)
}

// The rendered interface configuration must be one the root helper accepts,
// because every mint and every removal rewrites this exact file.
func TestTheRenderedInterfaceConfigurationIsOneTheHelperWillManage(t *testing.T) {
	env := newHelperEnv(t)
	rendered := strings.Replace(readArtifact(t, wgTemplate), "__PRIVATE_KEY__", interfacePrivateKey, 1)
	env.writeConfig(rendered)

	run := env.run("add-peer", validKey, "10.66.0.2")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	requireContains(t, "the managed interface configuration", env.config(),
		"Address = "+HostAddress.String(),
		"ListenPort = 51820",
		"PublicKey = "+validKey,
		"AllowedIPs = 10.66.0.2/32",
	)
}

// ---------------------------------------------------------------------------
// The posture installer, run against a scratch root with stub executables.
// ---------------------------------------------------------------------------

type postureEnv struct {
	t       *testing.T
	root    string
	binDir  string
	logPath string
}

func newPostureEnv(t *testing.T) *postureEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the posture scripts are POSIX shell")
	}
	base := t.TempDir()
	env := &postureEnv{
		t:       t,
		root:    filepath.Join(base, "root"),
		binDir:  filepath.Join(base, "bin"),
		logPath: filepath.Join(base, "stub.log"),
	}
	if err := os.MkdirAll(env.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(env.root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"systemctl", "sysctl", "ufw", "nft", "ip", "ss"} {
		env.writeStub(name, recordingStub)
	}
	env.writeStub("wg", wgKeyStub)
	env.seedUFWDefaults()
	return env
}

// seedUFWDefaults plants the file ufw installs, with the forward policy it
// ships. Deny is where a real host starts, so activation has something to
// change and revert has something to put back.
func (e *postureEnv) seedUFWDefaults() {
	e.t.Helper()
	const defaults = `IPV6=yes
DEFAULT_INPUT_POLICY="DROP"
DEFAULT_OUTPUT_POLICY="ACCEPT"
DEFAULT_FORWARD_POLICY="DROP"
DEFAULT_APPLICATION_POLICY="SKIP"
`
	full := filepath.Join(e.root, "etc/default/ufw")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(defaults), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// recordingStub records its argv and succeeds. Behaviour a test needs is
// planted per command in NIGHTLOCH_STUB_<COMMAND>_OUT / _FAILS.
const recordingStub = `#!/bin/sh
name="$(basename "$0")"
{ printf '%s' "$name"; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
upper="$(printf '%s' "$name" | tr 'a-z-' 'A-Z_')"
eval "out=\${NIGHTLOCH_STUB_${upper}_OUT:-}"
eval "fails=\${NIGHTLOCH_STUB_${upper}_FAILS:-}"
[ -n "$out" ] && printf '%s\n' "$out"
[ -n "$fails" ] && exit "$fails"
exit 0
`

// wgKeyStub answers genkey and pubkey with fixed keys so a generated interface
// configuration is byte-predictable, and records everything else.
const wgKeyStub = `#!/bin/sh
{ printf 'wg'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
case "$1" in
	genkey) printf '%s\n' "` + stubPrivateKey + `" ;;
	pubkey) cat >/dev/null; printf '%s\n' "` + stubPublicKey + `" ;;
	show) [ -n "${NIGHTLOCH_STUB_WG_OUT:-}" ] && printf '%s\n' "$NIGHTLOCH_STUB_WG_OUT" ;;
esac
exit 0
`

const stubPrivateKey = "c3R1YnByaXZhdGVrZXlzdHVicHJpdmF0ZWtleXN0dWI="

const stubPublicKey = "c3R1YnB1YmxpY2tleXN0dWJwdWJsaWNrZXlzdHVicHU="

func (e *postureEnv) writeStub(name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.binDir, name), []byte(body), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *postureEnv) run(script string, args ...string) helperRun {
	e.t.Helper()
	return e.runWith(script, nil, args...)
}

func (e *postureEnv) runWith(script string, extraEnv []string, args ...string) helperRun {
	e.t.Helper()
	abs, err := filepath.Abs(script)
	if err != nil {
		e.t.Fatal(err)
	}
	source, err := filepath.Abs("../../deploy")
	if err != nil {
		e.t.Fatal(err)
	}
	cmd := exec.Command(abs, args...)
	cmd.Env = append([]string{
		"PATH=" + e.binDir + ":/usr/sbin:/usr/bin:/sbin:/bin",
		"NIGHTLOCH_POSTURE_ROOT=" + e.root,
		"NIGHTLOCH_POSTURE_SOURCE=" + source,
		"NIGHTLOCH_STUB_LOG=" + e.logPath,
	}, extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	run := helperRun{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatalf("running %s: %v", script, err)
		}
		run.exitCode = exit.ExitCode()
	}
	return run
}

func (e *postureEnv) invocations() []string {
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

func (e *postureEnv) installed(path string) string {
	e.t.Helper()
	body, err := os.ReadFile(filepath.Join(e.root, path))
	if err != nil {
		e.t.Fatalf("reading installed %s: %v", path, err)
	}
	return string(body)
}

func (e *postureEnv) mode(path string) os.FileMode {
	e.t.Helper()
	info, err := os.Stat(filepath.Join(e.root, path))
	if err != nil {
		e.t.Fatalf("stat installed %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func requireInvoked(t *testing.T, got []string, want string) {
	t.Helper()
	for _, line := range got {
		if line == want {
			return
		}
	}
	t.Errorf("no invocation %q in %q", want, got)
}

func requireNotInvoked(t *testing.T, got []string, unwanted string) {
	t.Helper()
	for _, line := range got {
		if strings.HasPrefix(line, unwanted) {
			t.Errorf("invoked %q, which staging must not do: %q", line, got)
		}
	}
}

func TestStagingInstallsThePostureWithoutTurningAnythingOn(t *testing.T) {
	env := newPostureEnv(t)

	run := env.run(posturePath, "stage")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}

	// Every file the posture is made of lands where the unit and the checklist
	// say it is.
	if got := env.installed("etc/nightloch/nightloch-nat.nft"); got != readArtifact(t, rulesetPath) {
		t.Error("the installed ruleset is not the artifact in deploy/")
	}
	if got := env.installed("etc/systemd/system/nightloch-nat.service"); got != readArtifact(t, unitPath) {
		t.Error("the installed unit is not the artifact in deploy/")
	}
	if got := env.installed("etc/sysctl.d/99-nightloch-forward.conf"); got != readArtifact(t, sysctlPath) {
		t.Error("the installed sysctl drop-in is not the artifact in deploy/")
	}

	// The interface configuration is generated, never shipped: the private key
	// is made on the host and stays there.
	config := env.installed("etc/wireguard/wg0.conf")
	requireContains(t, "the generated interface configuration", config,
		"PrivateKey = "+stubPrivateKey,
		"Address = "+HostAddress.String()+"/24",
	)
	requireAbsent(t, "the generated interface configuration", config, "__PRIVATE_KEY__")
	if mode := env.mode("etc/wireguard/wg0.conf"); mode != 0o600 {
		t.Errorf("wg0.conf mode = %o, want 600", mode)
	}

	// The public half is left readable so an Operator can put it in
	// TUNNEL_ENDPOINT_PUBLIC_KEY without reading the private key to get it.
	if got := strings.TrimSpace(env.installed("etc/wireguard/wg0.pub")); got != stubPublicKey {
		t.Errorf("wg0.pub = %q, want %q", got, stubPublicKey)
	}

	// Staging is safe to run before approval precisely because it enables,
	// starts, and forwards nothing.
	invocations := env.invocations()
	requireInvoked(t, invocations, "systemctl daemon-reload")
	requireNotInvoked(t, invocations, "systemctl enable")
	requireNotInvoked(t, invocations, "systemctl start")
	requireNotInvoked(t, invocations, "sysctl --system")
	requireNotInvoked(t, invocations, "nft -f")
}

func TestStagingNeverOverwritesAnInterfaceConfigurationThatAlreadyExists(t *testing.T) {
	env := newPostureEnv(t)
	if run := env.run(posturePath, "stage"); run.exitCode != 0 {
		t.Fatalf("first stage: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	existing := env.installed("etc/wireguard/wg0.conf")

	// A second run is what a redeploy does. Rewriting the file here would
	// discard the interface's identity and every peer the helper has added,
	// which is every minted Device.
	if run := env.run(posturePath, "stage"); run.exitCode != 0 {
		t.Fatalf("second stage: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}

	if got := env.installed("etc/wireguard/wg0.conf"); got != existing {
		t.Error("a second stage rewrote the interface configuration")
	}
}

func TestActivationRefusesWithoutTheChecklistConfirmation(t *testing.T) {
	env := newPostureEnv(t)
	if run := env.run(posturePath, "stage"); run.exitCode != 0 {
		t.Fatalf("stage: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	if err := os.Truncate(env.logPath, 0); err != nil {
		t.Fatal(err)
	}

	run := env.run(posturePath, "activate")

	if run.exitCode != 2 {
		t.Fatalf("exit = %d, want 2 (a refusal); stderr = %q", run.exitCode, run.stderr)
	}
	requireContains(t, "the refusal", run.stderr, "NAT-CHECKLIST")
	// A refusal has no side effect: nothing was enabled and nothing forwards.
	if got := env.invocations(); got != nil {
		t.Errorf("a refused activation invoked %q", got)
	}
	requireContains(t, "the ufw defaults", env.installed("etc/default/ufw"), `DEFAULT_FORWARD_POLICY="DROP"`)
}

func TestActivationTurnsThePostureOnAndMakesItSurviveAReboot(t *testing.T) {
	env := newPostureEnv(t)
	if run := env.run(posturePath, "stage"); run.exitCode != 0 {
		t.Fatalf("stage: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	if err := os.Truncate(env.logPath, 0); err != nil {
		t.Fatal(err)
	}

	run := env.run(posturePath, "activate", "--i-have-run-the-nat-checklist")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	invocations := env.invocations()

	requireInvoked(t, invocations, "sysctl --system")
	// enable --now is both halves of the acceptance criterion in one call:
	// running now, and running again after a reboot nobody attended.
	requireInvoked(t, invocations, "systemctl enable --now nightloch-nat.service")
	requireInvoked(t, invocations, "systemctl enable --now wg-quick@wg0.service")

	// ufw's forward policy is deny by default, and it hooks alongside this
	// posture rather than under it, so forwarding stays dead until it changes.
	requireContains(t, "the ufw defaults", env.installed("etc/default/ufw"), `DEFAULT_FORWARD_POLICY="ACCEPT"`)

	// The redirected DNS packet arrives at the host as ordinary input on wg0,
	// where ufw's deny-incoming default would otherwise eat it.
	requireInvoked(t, invocations, "ufw allow in on wg0 to "+HostAddress.String()+" port 53 proto udp")
	requireInvoked(t, invocations, "ufw allow in on wg0 to "+HostAddress.String()+" port 53 proto tcp")
	requireInvoked(t, invocations, "ufw reload")
}

func TestActivationRefusesBeforeThePostureHasBeenStaged(t *testing.T) {
	env := newPostureEnv(t)

	run := env.run(posturePath, "activate", "--i-have-run-the-nat-checklist")

	if run.exitCode != 3 {
		t.Fatalf("exit = %d, want 3 (nothing to activate); stderr = %q", run.exitCode, run.stderr)
	}
	if got := env.invocations(); got != nil {
		t.Errorf("activating an unstaged host invoked %q", got)
	}
}

func TestRevertTakesTheHostBackToWhereItWasBeforeForwarding(t *testing.T) {
	env := newPostureEnv(t)
	if run := env.run(posturePath, "stage"); run.exitCode != 0 {
		t.Fatalf("stage: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	if run := env.run(posturePath, "activate", "--i-have-run-the-nat-checklist"); run.exitCode != 0 {
		t.Fatalf("activate: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	if err := os.Truncate(env.logPath, 0); err != nil {
		t.Fatal(err)
	}

	// This is what the checklist's self-cancelling `at` job runs, so it must
	// need no argument and no confirmation: it is the way back.
	run := env.run(posturePath, "revert")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	invocations := env.invocations()
	requireInvoked(t, invocations, "systemctl disable --now nightloch-nat.service")
	requireInvoked(t, invocations, "sysctl -w net.ipv4.ip_forward=0")
	requireInvoked(t, invocations, "nft delete table inet nightloch")
	requireInvoked(t, invocations, "ufw reload")
	requireContains(t, "the ufw defaults", env.installed("etc/default/ufw"), `DEFAULT_FORWARD_POLICY="DROP"`)

	// The tunnel itself is not the forwarding change, and dropping it would
	// disconnect the session doing the reverting.
	requireNotInvoked(t, invocations, "systemctl disable --now wg-quick@wg0.service")
}

func TestAnUnknownSubcommandIsRefusedAndDoesNothing(t *testing.T) {
	env := newPostureEnv(t)

	run := env.run(posturePath, "enable-everything")

	if run.exitCode != 2 {
		t.Fatalf("exit = %d, want 2; stderr = %q", run.exitCode, run.stderr)
	}
	if got := env.invocations(); got != nil {
		t.Errorf("an unknown subcommand invoked %q", got)
	}
}

// ---------------------------------------------------------------------------
// The read-only verifier.
// ---------------------------------------------------------------------------

// healthyHost is the stub state of a droplet where every acceptance criterion
// holds. Each test below breaks exactly one fact and asserts the verifier says
// so by name.
func healthyHost() []string {
	return []string{
		"NIGHTLOCH_STUB_IP_OUT=" + ipHealthy,
		"NIGHTLOCH_STUB_WG_OUT=51820",
		"NIGHTLOCH_STUB_NFT_OUT=" + nftHealthy,
		"NIGHTLOCH_STUB_SYSTEMCTL_OUT=enabled",
		"NIGHTLOCH_STUB_SS_OUT=" + ssHealthy,
	}
}

const (
	ipHealthy  = "inet 10.66.0.1/24 scope global wg0"
	ssHealthy  = "udp LISTEN 0 0 10.66.0.1:53 *:*"
	nftHealthy = `iifname "wg0" udp dport 53 redirect to :53
iifname "wg0" tcp dport 53 redirect to :53
iifname "wg0" tcp dport 853 reject with tcp reset
iifname "wg0" udp dport 853 reject
ip saddr 10.66.0.0/24 oifname != "wg0" masquerade`
)

// forwardingFacts plants the kernel's own answers where the verifier reads
// them. They are files rather than a stubbed command because that is what the
// verifier does on a real host: reading /proc cannot change one.
func (e *postureEnv) forwardingFacts(ipv4, ipv6 string) {
	e.t.Helper()
	e.writeProc("proc/sys/net/ipv4/ip_forward", ipv4)
	e.writeProc("proc/sys/net/ipv6/conf/all/forwarding", ipv6)
}

func (e *postureEnv) writeProc(path, value string) {
	e.t.Helper()
	full := filepath.Join(e.root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value+"\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *postureEnv) verify(overrides ...string) helperRun {
	e.t.Helper()
	return e.runWith(verifyPath, append(healthyHost(), overrides...))
}

func TestTheVerifierPassesOnAHostWhereThePostureHolds(t *testing.T) {
	env := newPostureEnv(t)
	env.forwardingFacts("1", "0")

	run := env.verify()

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
	}
	// Every acceptance criterion this ticket can check from the host is a
	// named fact, so a pass is legible rather than a silent zero.
	requireContains(t, "the verifier's report", run.stdout,
		"PASS ip_forward",
		"PASS ipv6_forwarding_off",
		"PASS tunnel_interface",
		"PASS masquerade",
		"PASS dns_redirect",
		"PASS dot_rejected",
		"PASS reboot_persistence",
		"PASS no_public_resolver",
	)
}

func TestTheVerifierIsReadOnly(t *testing.T) {
	env := newPostureEnv(t)
	env.forwardingFacts("1", "0")

	if run := env.verify(); run.exitCode != 0 {
		t.Fatalf("exit = %d", run.exitCode)
	}

	// An agent may run this against a live host. It must not be able to change
	// one, so no invocation may be a mutation.
	for _, line := range env.invocations() {
		for _, mutation := range []string{"sysctl -w", "nft -f", "nft add", "nft delete", "systemctl start", "systemctl enable", "systemctl disable", "ufw allow", "ufw reload", "ip link set"} {
			if strings.HasPrefix(line, mutation) {
				t.Errorf("the verifier invoked %q", line)
			}
		}
	}
}

func TestTheVerifierFailsWhenTheHostIsNotForwarding(t *testing.T) {
	env := newPostureEnv(t)
	env.forwardingFacts("0", "0")

	run := env.verify()

	if run.exitCode == 0 {
		t.Fatal("the verifier passed a host that forwards nothing")
	}
	requireContains(t, "the verifier's report", run.stdout, "FAIL ip_forward")
}

func TestTheVerifierFailsWhenPort853IsDroppedRatherThanRejected(t *testing.T) {
	env := newPostureEnv(t)
	env.forwardingFacts("1", "0")
	dropped := strings.Replace(nftHealthy, `iifname "wg0" tcp dport 853 reject with tcp reset`, `iifname "wg0" tcp dport 853 drop`, 1)

	run := env.verify("NIGHTLOCH_STUB_NFT_OUT=" + dropped)

	if run.exitCode == 0 {
		t.Fatal("the verifier passed a host that drops 853 instead of rejecting it")
	}
	requireContains(t, "the verifier's report", run.stdout, "FAIL dot_rejected")
}

func TestTheVerifierFailsWhenTheResolverAnswersOnThePublicAddress(t *testing.T) {
	env := newPostureEnv(t)
	env.forwardingFacts("1", "0")

	run := env.verify("NIGHTLOCH_STUB_SS_OUT=udp LISTEN 0 0 0.0.0.0:53 *:*")

	if run.exitCode == 0 {
		t.Fatal("the verifier passed a host running an open resolver")
	}
	requireContains(t, "the verifier's report", run.stdout, "FAIL no_public_resolver")
}

func TestTheVerifierFailsWhenThePostureWouldNotSurviveAReboot(t *testing.T) {
	env := newPostureEnv(t)
	env.forwardingFacts("1", "0")

	run := env.verify("NIGHTLOCH_STUB_SYSTEMCTL_OUT=disabled")

	if run.exitCode == 0 {
		t.Fatal("the verifier passed a host whose posture dies at the next reboot")
	}
	requireContains(t, "the verifier's report", run.stdout, "FAIL reboot_persistence")
}

func TestTheVerifierFailsWhenDNSFromTheTunnelIsNotRedirected(t *testing.T) {
	env := newPostureEnv(t)
	env.forwardingFacts("1", "0")
	withoutRedirect := strings.Replace(nftHealthy, `iifname "wg0" udp dport 53 redirect to :53`, "", 1)

	run := env.verify("NIGHTLOCH_STUB_NFT_OUT=" + withoutRedirect)

	if run.exitCode == 0 {
		t.Fatal("the verifier passed a host where a Device can resolve elsewhere")
	}
	requireContains(t, "the verifier's report", run.stdout, "FAIL dns_redirect")
}
