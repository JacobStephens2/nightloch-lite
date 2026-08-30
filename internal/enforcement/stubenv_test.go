package enforcement

// The deploy/ scripts are tested by running the real scripts with stub
// executables earlier on PATH, so every command they would issue on a live
// host is asserted by argv and nothing reaches a real wg, nft, or systemctl.
// This file is the harness the root-helper tests and the host-posture tests
// share: one stub PATH, one log every stub appends its argv to, and one way to
// run a script against them.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// helperRun is one script invocation's outcome. The exit code is part of the
// contract of both scripts, not an implementation detail: a refusal and a
// plane that is down are deliberately different numbers.
type helperRun struct {
	exitCode int
	stdout   string
	stderr   string
}

// stubEnv is an isolated invocation environment: a directory of stub
// executables that is first on PATH, and the log they all append to.
type stubEnv struct {
	t       *testing.T
	binDir  string
	logPath string
}

func newStubEnv(t *testing.T) *stubEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the deploy scripts are POSIX shell")
	}
	base := t.TempDir()
	env := &stubEnv{
		t:       t,
		binDir:  filepath.Join(base, "bin"),
		logPath: filepath.Join(base, "stub.log"),
	}
	if err := os.MkdirAll(env.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return env
}

func (e *stubEnv) writeStub(name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.binDir, name), []byte(body), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

// invocations is every argv the stubs saw, in order. An empty slice is the
// assertion that a refusal reached nothing.
func (e *stubEnv) invocations() []string {
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

func (e *stubEnv) forget() {
	e.t.Helper()
	if err := os.WriteFile(e.logPath, nil, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// runScript executes the script exactly as it is installed, with the stub
// directory first on PATH so no command reaches a real one.
func (e *stubEnv) runScript(script string, environment []string, args ...string) helperRun {
	e.t.Helper()
	abs, err := filepath.Abs(script)
	if err != nil {
		e.t.Fatal(err)
	}
	cmd := exec.Command(abs, args...)
	cmd.Env = append([]string{
		"PATH=" + e.binDir + ":/usr/sbin:/usr/bin:/sbin:/bin",
		"NIGHTLOCH_STUB_LOG=" + e.logPath,
	}, environment...)
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

// requireInvoked asserts one exact argv is somewhere in the log.
func requireInvoked(t *testing.T, got []string, want string) {
	t.Helper()
	for _, line := range got {
		if line == want {
			return
		}
	}
	t.Errorf("no invocation %q in %q", want, got)
}

// requireNotInvoked asserts nothing in the log begins with a prefix.
func requireNotInvoked(t *testing.T, got []string, unwanted string) {
	t.Helper()
	for _, line := range got {
		if strings.HasPrefix(line, unwanted) {
			t.Errorf("invoked %q, which it must not: %q", line, got)
		}
	}
}

// requireInvokedBefore asserts an ordering the scripts depend on for safety,
// which a set of independent presence assertions cannot express.
func requireInvokedBefore(t *testing.T, got []string, first, second string) {
	t.Helper()
	firstAt, secondAt := -1, -1
	for i, line := range got {
		if line == first && firstAt < 0 {
			firstAt = i
		}
		if line == second && secondAt < 0 {
			secondAt = i
		}
	}
	if firstAt < 0 || secondAt < 0 {
		t.Errorf("expected both %q and %q in %q", first, second, got)
		return
	}
	if firstAt > secondAt {
		t.Errorf("%q ran after %q, in %q", first, second, got)
	}
}
