package enforcement

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const resolverTemplatePath = "../../deploy/AdGuardHome.yaml.template"

const (
	resolverUnitPath      = "../../deploy/AdGuardHome.service"
	resolverCaddyPath     = "../../deploy/AdGuardHome.caddy"
	caddyDropInPath       = "../../deploy/caddy-nightloch.service.conf"
	caddyExamplePath      = "../../deploy/Caddyfile.example"
	resolverInstallerPath = "../../deploy/nightloch-resolver-posture.sh"
	resolverVerifyPath    = "../../deploy/nightloch-resolver-verify.sh"
)

// The resolver posture is consumed by a shell installer, but its retention is
// owned here beside tunnel-address quarantine.  This string is the narrow
// bridge between the two: changing QueryLogRetention changes the value the
// installer renders into AdGuard Home rather than leaving a second seven-day
// literal behind in deploy/.
func TestResolverQueryLogRetentionIsDerivedFromTheQuarantineRule(t *testing.T) {
	const want = "168h"
	if got := ResolverQueryLogRetention(); got != want {
		t.Fatalf("ResolverQueryLogRetention() = %q, want %q", got, want)
	}
}

func TestResolverTemplateCarriesTheGlobalBaselineAndNothingPerDevice(t *testing.T) {
	config := stripComments(readArtifact(t, resolverTemplatePath))

	// DNS is reachable from Devices only on the address the tunnel owns.  The
	// administration listener is separate and remains on loopback for Caddy.
	requireContains(t, resolverTemplatePath, config,
		"http:\n  address: 127.0.0.1:3000",
		"dns:\n  bind_hosts:\n    - "+HostAddress.String(),
		"  port: 53",
	)
	requireAbsent(t, resolverTemplatePath, config,
		"0.0.0.0",
		"::",
	)

	// Both subscriptions are global and always enabled.  Automatic refresh is
	// off: an upstream list change lands only when the Operator explicitly
	// refreshes it and can verify the changed enforcement afterwards.
	requireContains(t, resolverTemplatePath, config,
		"filters_update_interval: 0",
		"name: OISD NSFW Baseline list",
		"url: https://nsfw.oisd.nl/",
		"name: HaGeZi Encrypted DNS/VPN/TOR/Proxy Bypass",
		"url: https://adguardteam.github.io/HostlistsRegistry/assets/filter_52.txt",
		"enabled: true",
	)

	// Firefox's canary is global too.  There is deliberately no client-scoped
	// Policy projection in this ticket.
	requireContains(t, resolverTemplatePath, config,
		"||use-application-dns.net^$dnsrewrite=NXDOMAIN;;",
		"interval: __QUERY_LOG_RETENTION__",
	)
	requireAbsent(t, resolverTemplatePath, config,
		"$client",
		"168h",
		"filtering_enabled: false",
	)

	if got := strings.Count(config, "\n  - url:"); got != 2 {
		t.Errorf("configured filter subscriptions = %d, want exactly 2", got)
	}

	environment := readArtifact(t, envSamplePath)
	requireContains(t, envSamplePath, environment,
		"ADGUARD_USERNAME=operator",
		"ADGUARD_PASSWORD=",
	)
}

func TestResolverStartsAfterTheTunnelAndAdministrationStaysBehindCaddy(t *testing.T) {
	unit := stripComments(readArtifact(t, resolverUnitPath))
	requireContains(t, resolverUnitPath, unit,
		"Requires=wg-quick@wg0.service",
		"After=network-online.target wg-quick@wg0.service",
		"ExecStart=/opt/AdGuardHome/AdGuardHome --no-check-update --config /etc/AdGuardHome/AdGuardHome.yaml --work-dir /var/lib/AdGuardHome",
		"Restart=always",
		"MemoryMax=512M",
		"AmbientCapabilities=CAP_NET_BIND_SERVICE",
		"WantedBy=multi-user.target",
	)

	caddy := stripComments(readArtifact(t, resolverCaddyPath))
	requireContains(t, resolverCaddyPath, caddy,
		"agh.nightloch.app {",
		"basic_auth {",
		"operator {$ADGUARD_PASSWORD_HASH}",
		"reverse_proxy 127.0.0.1:3000",
	)
	requireAbsent(t, resolverCaddyPath, caddy, "reverse_proxy 10.66.0.1")

	dropIn := stripComments(readArtifact(t, caddyDropInPath))
	requireContains(t, caddyDropInPath, dropIn,
		"[Service]",
		"EnvironmentFile=/etc/nightloch/caddy.env",
	)
	requireContains(t, caddyExamplePath, readArtifact(t, caddyExamplePath),
		"import /etc/caddy/sites/*.caddy",
	)
	requireContains(t, cloudInitPath, readArtifact(t, cloudInitPath),
		"import /etc/caddy/sites/*.caddy",
	)

	// No cloud or host-firewall artifact opens AdGuard Home's loopback port.
	requireAbsent(t, tofuMainPath, stripComments(readArtifact(t, tofuMainPath)), "3000")
}

type resolverPostureEnv struct {
	*stubEnv
	root string
}

func newResolverPostureEnv(t *testing.T) *resolverPostureEnv {
	t.Helper()
	env := &resolverPostureEnv{
		stubEnv: newStubEnv(t),
		root:    filepath.Join(t.TempDir(), "root"),
	}
	if err := os.MkdirAll(env.root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"chown", "caddy", "getent", "runuser", "useradd"} {
		env.writeStub(name, recordingStub)
	}
	env.writeStub("curl", resolverCurlStub)
	env.writeStub("date", resolverDateStub)
	env.writeStub("dig", recordingStub)
	env.writeStub("sha256sum", resolverSHA256Stub)
	env.writeStub("ss", recordingStub)
	env.writeStub("systemctl", resolverSystemctlStub)
	env.writeStub("tar", resolverTarStub)
	env.writeRootFile("etc/nightloch/adguard-password.bcrypt", stubPasswordHash, 0o600)
	env.writeRootFile("etc/caddy/Caddyfile", "import /etc/caddy/sites/*.caddy\n", 0o644)
	env.writeRootFile("opt/nightloch/bin/nightloch", resolverNightlochStub, 0o755)
	return env
}

const stubPasswordHash = "$2a$14$A1234567890123456789012345678901234567890123456789012"

const resolverNightlochStub = `#!/bin/sh
{ printf 'nightloch'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
[ "$1" = "resolver-query-log-retention" ] || exit 1
printf '168h\n'
`

const resolverCurlStub = `#!/bin/sh
{ printf 'curl'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
while [ "$#" -gt 0 ]; do
	if [ "$1" = "--output" ]; then
		shift
		printf '||downloaded-filter.example^\n' > "$1"
		exit 0
	fi
	shift
done
exit 1
`

const resolverDateStub = `#!/bin/sh
{ printf 'date'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
printf '2026-08-30T18:00:00Z\n'
`

const resolverSHA256Stub = `#!/bin/sh
{ printf 'sha256sum'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
if [ "${1:-}" = "--check" ]; then
	exit 0
fi
printf '%s  %s\n' "${NIGHTLOCH_STUB_SHA256_OUT:-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}" "$1"
`

const resolverTarStub = `#!/bin/sh
{ printf 'tar'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
destination=""
while [ "$#" -gt 0 ]; do
	if [ "$1" = "-C" ]; then
		shift
		destination="$1"
		break
	fi
	shift
done
[ -n "$destination" ] || exit 1
mkdir -p "$destination/AdGuardHome"
printf 'pinned AdGuard Home binary\n' > "$destination/AdGuardHome/AdGuardHome"
`

const resolverSystemctlStub = `#!/bin/sh
{ printf 'systemctl'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
case "$1" in
	is-enabled) printf '%s\n' "${NIGHTLOCH_STUB_RESOLVER_ENABLED:-enabled}" ;;
	is-active) printf '%s\n' "${NIGHTLOCH_STUB_RESOLVER_ACTIVE:-active}" ;;
esac
exit 0
`

const missingAccountStub = `#!/bin/sh
{ printf 'getent'; for arg in "$@"; do printf ' %s' "$arg"; done; printf '\n'; } >> "$NIGHTLOCH_STUB_LOG"
exit 2
`

func (e *resolverPostureEnv) writeRootFile(path, body string, mode os.FileMode) {
	e.t.Helper()
	full := filepath.Join(e.root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), mode); err != nil {
		e.t.Fatal(err)
	}
}

func (e *resolverPostureEnv) installed(path string) string {
	e.t.Helper()
	body, err := os.ReadFile(filepath.Join(e.root, path))
	if err != nil {
		e.t.Fatalf("reading installed %s: %v", path, err)
	}
	return string(body)
}

func (e *resolverPostureEnv) mode(path string) os.FileMode {
	e.t.Helper()
	info, err := os.Stat(filepath.Join(e.root, path))
	if err != nil {
		e.t.Fatalf("stat installed %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func (e *resolverPostureEnv) run(script string, args ...string) helperRun {
	e.t.Helper()
	return e.runWith(script, nil, args...)
}

func (e *resolverPostureEnv) runWith(script string, extraEnv []string, args ...string) helperRun {
	e.t.Helper()
	source, err := filepath.Abs("../../deploy")
	if err != nil {
		e.t.Fatal(err)
	}
	environment := []string{
		"NIGHTLOCH_RESOLVER_ROOT=" + e.root,
		"NIGHTLOCH_RESOLVER_SOURCE=" + source,
	}
	environment = append(environment, extraEnv...)
	return e.runScript(script, environment, args...)
}

func TestInstallingResolverPostureStagesFilesWithoutStartingAnything(t *testing.T) {
	env := newResolverPostureEnv(t)

	run := env.run(resolverInstallerPath, "install")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
	}
	if got := env.installed("opt/AdGuardHome/AdGuardHome"); got != "pinned AdGuard Home binary\n" {
		t.Errorf("installed binary = %q", got)
	}
	if got := env.installed("etc/systemd/system/AdGuardHome.service"); got != readArtifact(t, resolverUnitPath) {
		t.Error("the installed unit is not the deploy artifact")
	}
	if got := env.installed("etc/caddy/sites/nightloch-agh.caddy"); got != readArtifact(t, resolverCaddyPath) {
		t.Error("the installed Caddy site is not the deploy artifact")
	}
	if got := env.installed("etc/systemd/system/caddy.service.d/nightloch.conf"); got != readArtifact(t, caddyDropInPath) {
		t.Error("the installed Caddy drop-in is not the deploy artifact")
	}

	config := env.installed("etc/AdGuardHome/AdGuardHome.yaml")
	requireContains(t, "rendered AdGuard Home configuration", config,
		"password: "+stubPasswordHash,
		"interval: 168h",
	)
	requireAbsent(t, "rendered AdGuard Home configuration", config,
		"__ADGUARD_PASSWORD_HASH__",
		"__QUERY_LOG_RETENTION__",
	)
	if mode := env.mode("etc/AdGuardHome/AdGuardHome.yaml"); mode != 0o600 {
		t.Errorf("AdGuardHome.yaml mode = %o, want 600", mode)
	}
	if got := env.installed("etc/nightloch/caddy.env"); got != "ADGUARD_PASSWORD_HASH="+stubPasswordHash+"\n" {
		t.Errorf("caddy.env = %q", got)
	}
	if mode := env.mode("etc/nightloch/caddy.env"); mode != 0o600 {
		t.Errorf("caddy.env mode = %o, want 600", mode)
	}
	for _, id := range []string{"1", "2"} {
		if got := env.installed("var/lib/AdGuardHome/data/filters/" + id + ".txt"); got == "" {
			t.Errorf("initial filter %s is empty", id)
		}
	}
	receipt := env.installed("var/lib/AdGuardHome/nightloch-filter-updates.log")
	requireContains(t, "the initial filter update receipt", receipt,
		"2026-08-30T18:00:00Z\tinitial\tid=1\tsha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\turl=https://nsfw.oisd.nl/",
		"2026-08-30T18:00:00Z\tinitial\tid=2\tsha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\turl=https://adguardteam.github.io/HostlistsRegistry/assets/filter_52.txt",
	)

	invocations := env.invocations()
	requireInvoked(t, invocations, "nightloch resolver-query-log-retention")
	requireContains(t, "the initial downloads", strings.Join(invocations, "\n"),
		"https://nsfw.oisd.nl/",
		"https://adguardteam.github.io/HostlistsRegistry/assets/filter_52.txt",
	)
	requireInvoked(t, invocations,
		"chown adguardhome:adguardhome "+filepath.Join(env.root, "etc/AdGuardHome")+" "+
			filepath.Join(env.root, "etc/AdGuardHome/AdGuardHome.yaml"))
	requireInvoked(t, invocations,
		"chown -R adguardhome:adguardhome "+filepath.Join(env.root, "var/lib/AdGuardHome"))
	requireInvoked(t, invocations, "systemctl daemon-reload")
	requireNotInvoked(t, invocations, "systemctl enable")
	requireNotInvoked(t, invocations, "systemctl start")
	requireNotInvoked(t, invocations, "systemctl reload")
}

func TestResolverInstallCreatesAMatchedServiceUserAndGroupWhenAbsent(t *testing.T) {
	env := newResolverPostureEnv(t)
	env.writeStub("getent", missingAccountStub)

	run := env.run(resolverInstallerPath, "install")

	if run.exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	requireInvoked(t, env.invocations(),
		"useradd --system --user-group --home-dir /var/lib/AdGuardHome --shell /usr/sbin/nologin adguardhome")
}

func TestStartingResolverValidatesBothConfigurationsBeforeEnablingAnything(t *testing.T) {
	env := newResolverPostureEnv(t)
	if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
		t.Fatalf("install: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	env.forget()

	run := env.run(resolverInstallerPath, "start")

	if run.exitCode != 0 {
		t.Fatalf("start: exit = %d, stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
	}
	invocations := env.invocations()
	config := filepath.Join(env.root, "etc/AdGuardHome/AdGuardHome.yaml")
	work := filepath.Join(env.root, "var/lib/AdGuardHome")
	binary := filepath.Join(env.root, "opt/AdGuardHome/AdGuardHome")
	caddyfile := filepath.Join(env.root, "etc/caddy/Caddyfile")
	caddyEnv := filepath.Join(env.root, "etc/nightloch/caddy.env")
	aghCheck := "runuser -u adguardhome -- " + binary + " --no-check-update --check-config --config " + config + " --work-dir " + work
	caddyCheck := "caddy validate --config " + caddyfile + " --envfile " + caddyEnv
	enable := "systemctl enable AdGuardHome.service"
	restart := "systemctl restart AdGuardHome.service"

	requireInvoked(t, invocations, "systemctl is-active --quiet wg-quick@wg0.service")
	requireInvoked(t, invocations, aghCheck)
	requireInvoked(t, invocations, caddyCheck)
	requireInvoked(t, invocations, enable)
	requireInvoked(t, invocations, restart)
	requireInvoked(t, invocations, "systemctl reload caddy")
	requireInvokedBefore(t, invocations, aghCheck, restart)
	requireInvokedBefore(t, invocations, caddyCheck, restart)
	requireInvokedBefore(t, invocations, enable, restart)
}

func TestStartingResolverRefusesWithoutLoadedBaselineListsOrTheirUpdateRecord(t *testing.T) {
	tests := []string{
		"var/lib/AdGuardHome/data/filters/1.txt",
		"var/lib/AdGuardHome/data/filters/2.txt",
		"var/lib/AdGuardHome/nightloch-filter-updates.log",
	}
	for _, missing := range tests {
		t.Run(missing, func(t *testing.T) {
			env := newResolverPostureEnv(t)
			if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
				t.Fatalf("install: exit = %d, stderr = %q", run.exitCode, run.stderr)
			}
			if err := os.Remove(filepath.Join(env.root, missing)); err != nil {
				t.Fatal(err)
			}
			env.forget()

			run := env.run(resolverInstallerPath, "start")

			if run.exitCode != 3 {
				t.Fatalf("start exit = %d, want 3; stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
			}
			requireContains(t, "the refusal", run.stderr, "missing or empty")
			requireNotInvoked(t, env.invocations(), "systemctl enable")
			requireNotInvoked(t, env.invocations(), "systemctl restart")
		})
	}
}

func TestStartingResolverRefusesUnrecordedFilterBytes(t *testing.T) {
	env := newResolverPostureEnv(t)
	if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
		t.Fatalf("install: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	env.forget()

	run := env.runWith(resolverInstallerPath, []string{
		"NIGHTLOCH_STUB_SHA256_OUT=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}, "start")

	if run.exitCode != 3 {
		t.Fatalf("start exit = %d, want 3; stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
	}
	requireContains(t, "the refusal", run.stderr, "does not match its latest Operator record")
	requireNotInvoked(t, env.invocations(), "systemctl enable")
	requireNotInvoked(t, env.invocations(), "systemctl restart")
}

func TestRepeatedResolverInstallPreservesThePersistedConfiguration(t *testing.T) {
	env := newResolverPostureEnv(t)
	if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
		t.Fatalf("first install: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	configPath := "etc/AdGuardHome/AdGuardHome.yaml"
	withLaterRules := env.installed(configPath) + "# later generated Policy projection\n"
	env.writeRootFile(configPath, withLaterRules, 0o600)
	filterPath := "var/lib/AdGuardHome/data/filters/1.txt"
	env.writeRootFile(filterPath, "||known-good-filter.example^\n", 0o600)
	env.forget()

	if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
		t.Fatalf("second install: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	if got := env.installed(configPath); got != withLaterRules {
		t.Error("a repeated install overwrote AdGuard Home's persisted configuration")
	}
	if got := env.installed(filterPath); got != "||known-good-filter.example^\n" {
		t.Error("a repeated install silently refreshed a Baseline list")
	}
	requireAbsent(t, "the repeated install's invocations", strings.Join(env.invocations(), "\n"),
		"https://nsfw.oisd.nl/",
		"https://adguardteam.github.io/HostlistsRegistry/assets/filter_52.txt",
	)
}

func TestRepeatedResolverInstallRefusesADivergentPasswordHash(t *testing.T) {
	env := newResolverPostureEnv(t)
	if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
		t.Fatalf("first install: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	original := env.installed("etc/AdGuardHome/AdGuardHome.yaml")
	env.writeRootFile("etc/nightloch/adguard-password.bcrypt", "$2b$14$B1234567890123456789012345678901234567890123456789012", 0o600)
	env.forget()

	run := env.run(resolverInstallerPath, "install")

	if run.exitCode != 3 {
		t.Fatalf("exit = %d, want 3; stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
	}
	requireContains(t, "the refusal", run.stderr, "password hash does not match")
	if got := env.installed("etc/AdGuardHome/AdGuardHome.yaml"); got != original {
		t.Error("a refused password-hash change rewrote the Resolver configuration")
	}
	if got := env.installed("etc/nightloch/caddy.env"); got != "ADGUARD_PASSWORD_HASH="+stubPasswordHash+"\n" {
		t.Error("a refused password-hash change rewrote Caddy's credential")
	}
	if got := env.invocations(); got != nil {
		t.Errorf("a refused password-hash change invoked %q", got)
	}
}

func TestRecordingAFilterUpdateAppendsCurrentDigestsWithoutRefreshingAnything(t *testing.T) {
	env := newResolverPostureEnv(t)
	if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
		t.Fatalf("install: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	env.forget()

	run := env.run(resolverInstallerPath, "record-filter-update")

	if run.exitCode != 0 {
		t.Fatalf("record: exit = %d, stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
	}
	receipt := env.installed("var/lib/AdGuardHome/nightloch-filter-updates.log")
	requireContains(t, "the filter update receipt", receipt,
		"2026-08-30T18:00:00Z\tmanual\tid=1\tsha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\turl=https://nsfw.oisd.nl/",
		"2026-08-30T18:00:00Z\tmanual\tid=2\tsha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\turl=https://adguardteam.github.io/HostlistsRegistry/assets/filter_52.txt",
	)
	invocations := strings.Join(env.invocations(), "\n")
	requireAbsent(t, "record-filter-update invocations", invocations,
		"curl ",
		"systemctl ",
	)
}

func TestResolverBinaryDownloadIsVersionedAndChecksumPinned(t *testing.T) {
	installer := stripComments(readArtifact(t, resolverInstallerPath))
	requireContains(t, resolverInstallerPath, installer,
		`adguard_version="v0.107.79"`,
		`adguard_archive="AdGuardHome_linux_amd64.tar.gz"`,
		`adguard_sha256="c48f4a43000665484c5ec28177de11a004759b620dae8f77b2aabefc9ef3687f"`,
		"sha256sum --check --status -",
	)
}

const resolverListenersHealthy = `udp UNCONN 0 0 10.66.0.1:53 0.0.0.0:*
tcp LISTEN 0 4096 10.66.0.1:53 0.0.0.0:*
tcp LISTEN 0 4096 127.0.0.1:3000 0.0.0.0:*`

const firefoxCanaryNXDOMAIN = `;; ->>HEADER<<- opcode: QUERY, status: NXDOMAIN, id: 1234`

func installedHealthyResolver(t *testing.T) *resolverPostureEnv {
	t.Helper()
	env := newResolverPostureEnv(t)
	if run := env.run(resolverInstallerPath, "install"); run.exitCode != 0 {
		t.Fatalf("install: exit = %d, stderr = %q", run.exitCode, run.stderr)
	}
	env.writeRootFile("var/lib/AdGuardHome/data/filters/1.txt", "||oisd-baseline.example^\n", 0o600)
	env.writeRootFile("var/lib/AdGuardHome/data/filters/2.txt", "||encrypted-dns.example^\n", 0o600)
	env.forget()
	return env
}

func healthyResolverVerifyEnvironment() []string {
	return []string{
		"NIGHTLOCH_STUB_SS_OUT=" + resolverListenersHealthy,
		"NIGHTLOCH_STUB_DIG_OUT=" + firefoxCanaryNXDOMAIN,
	}
}

func TestResolverVerifierReportsEveryNamedFactOnAHealthyHost(t *testing.T) {
	env := installedHealthyResolver(t)

	run := env.runWith(resolverVerifyPath, healthyResolverVerifyEnvironment())

	if run.exitCode != 0 {
		t.Fatalf("verify: exit = %d, stdout = %q, stderr = %q", run.exitCode, run.stdout, run.stderr)
	}
	requireContains(t, "the Resolver verifier's report", run.stdout,
		"PASS resolver_service",
		"PASS dns_listener",
		"PASS admin_listener",
		"PASS baseline_subscriptions",
		"PASS filter_updates_manual",
		"PASS query_log_retention",
		"PASS firefox_canary",
	)
}

func TestResolverVerifierFailsEachBrokenAcceptanceFactByName(t *testing.T) {
	tests := []struct {
		name     string
		wantFact string
		breakIt  func(*resolverPostureEnv) []string
	}{
		{
			name:     "service not enabled",
			wantFact: "resolver_service",
			breakIt: func(_ *resolverPostureEnv) []string {
				return append(healthyResolverVerifyEnvironment(), "NIGHTLOCH_STUB_RESOLVER_ENABLED=disabled")
			},
		},
		{
			name:     "DNS also public",
			wantFact: "dns_listener",
			breakIt: func(_ *resolverPostureEnv) []string {
				listeners := resolverListenersHealthy + "\nudp UNCONN 0 0 0.0:53 0.0.0.0:*"
				return []string{"NIGHTLOCH_STUB_SS_OUT=" + listeners, "NIGHTLOCH_STUB_DIG_OUT=" + firefoxCanaryNXDOMAIN}
			},
		},
		{
			name:     "admin also public",
			wantFact: "admin_listener",
			breakIt: func(_ *resolverPostureEnv) []string {
				listeners := resolverListenersHealthy + "\ntcp LISTEN 0 4096 0.0.0.0:3000 0.0.0.0:*"
				return []string{"NIGHTLOCH_STUB_SS_OUT=" + listeners, "NIGHTLOCH_STUB_DIG_OUT=" + firefoxCanaryNXDOMAIN}
			},
		},
		{
			name:     "Baseline list empty",
			wantFact: "baseline_subscriptions",
			breakIt: func(env *resolverPostureEnv) []string {
				env.writeRootFile("var/lib/AdGuardHome/data/filters/1.txt", "", 0o600)
				return healthyResolverVerifyEnvironment()
			},
		},
		{
			name:     "automatic filter refresh",
			wantFact: "filter_updates_manual",
			breakIt: func(env *resolverPostureEnv) []string {
				path := "etc/AdGuardHome/AdGuardHome.yaml"
				config := strings.Replace(env.installed(path), "filters_update_interval: 0", "filters_update_interval: 24", 1)
				env.writeRootFile(path, config, 0o600)
				return healthyResolverVerifyEnvironment()
			},
		},
		{
			name:     "missing filter update record",
			wantFact: "filter_updates_manual",
			breakIt: func(env *resolverPostureEnv) []string {
				if err := os.Remove(filepath.Join(env.root, "var/lib/AdGuardHome/nightloch-filter-updates.log")); err != nil {
					env.t.Fatal(err)
				}
				return healthyResolverVerifyEnvironment()
			},
		},
		{
			name:     "unrecorded filter bytes",
			wantFact: "filter_updates_manual",
			breakIt: func(_ *resolverPostureEnv) []string {
				return append(healthyResolverVerifyEnvironment(),
					"NIGHTLOCH_STUB_SHA256_OUT=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			},
		},
		{
			name:     "vendor-default query retention",
			wantFact: "query_log_retention",
			breakIt: func(env *resolverPostureEnv) []string {
				path := "etc/AdGuardHome/AdGuardHome.yaml"
				config := strings.Replace(env.installed(path), "interval: 168h", "interval: 2160h", 1)
				// A coincidentally matching duration elsewhere must not make a
				// wrong querylog.interval look correct.
				config = strings.Replace(config, "interval: 24h", "interval: 168h", 1)
				env.writeRootFile(path, config, 0o600)
				return healthyResolverVerifyEnvironment()
			},
		},
		{
			name:     "canary answer not NXDOMAIN",
			wantFact: "firefox_canary",
			breakIt: func(_ *resolverPostureEnv) []string {
				return []string{"NIGHTLOCH_STUB_SS_OUT=" + resolverListenersHealthy, "NIGHTLOCH_STUB_DIG_OUT=;; status: NOERROR"}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := installedHealthyResolver(t)
			run := env.runWith(resolverVerifyPath, test.breakIt(env))
			if run.exitCode == 0 {
				t.Fatalf("verifier passed; stdout = %q", run.stdout)
			}
			requireContains(t, "the Resolver verifier's report", run.stdout, "FAIL "+test.wantFact)
		})
	}
}

func TestResolverVerifierIsReadOnly(t *testing.T) {
	env := installedHealthyResolver(t)

	run := env.runWith(resolverVerifyPath, healthyResolverVerifyEnvironment())

	if run.exitCode != 0 {
		t.Fatalf("verify: exit = %d, stdout = %q", run.exitCode, run.stdout)
	}
	wants := []string{
		"systemctl is-enabled AdGuardHome.service",
		"systemctl is-active AdGuardHome.service",
		"ss -H -lntu",
		"sha256sum " + filepath.Join(env.root, "var/lib/AdGuardHome/data/filters/1.txt"),
		"sha256sum " + filepath.Join(env.root, "var/lib/AdGuardHome/data/filters/2.txt"),
		"nightloch resolver-query-log-retention",
		"dig +time=2 +tries=1 @10.66.0.1 use-application-dns.net A",
	}
	invocations := env.invocations()
	if len(invocations) != len(wants) {
		t.Fatalf("verifier invoked %q, want exactly %q", invocations, wants)
	}
	for _, want := range wants {
		requireInvoked(t, invocations, want)
	}
}
