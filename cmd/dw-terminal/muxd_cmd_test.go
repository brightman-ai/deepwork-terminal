package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// isolatedSocket keeps every test in this file away from the user's real daemon. The
// commands under test STOP daemons, so a test that resolved the production socket path
// would kill the developer's live terminals — the same hazard that once cost this repo a
// set of running tmux sessions, one layer down.
func isolatedSocket(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run", "muxd.sock")
	t.Setenv(muxd.EnvSocketOverride, path)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(path))
	return path
}

// TestRestartDeclinesByDefault is the safety gate, and it is the most important test in
// this file.
//
// --restart is the one command in the product that deliberately destroys the user's
// running work. Everything that is not an explicit yes must leave the daemon alone: a
// stray newline, a typo, a script that piped nothing to stdin. The failure mode this
// forbids is silent and unrecoverable — the shells are gone and there is nothing to undo.
func TestRestartDeclinesByDefault(t *testing.T) {
	for _, answer := range []string{"", "\n", "n\n", "no\n", "yeah\n", "Y E S\n"} {
		var out bytes.Buffer
		ok, err := confirm(strings.NewReader(answer), &out, "Restart? ")
		if err != nil {
			t.Fatalf("confirm(%q): %v", answer, err)
		}
		if ok {
			t.Errorf("confirm(%q) = true; anything but an explicit yes must decline", answer)
		}
	}
	for _, answer := range []string{"y\n", "Y\n", "yes\n", " YES \n"} {
		var out bytes.Buffer
		ok, err := confirm(strings.NewReader(answer), &out, "Restart? ")
		if err != nil {
			t.Fatalf("confirm(%q): %v", answer, err)
		}
		if !ok {
			t.Errorf("confirm(%q) = false; an explicit yes must be honoured", answer)
		}
	}
}

// TestRestartWithoutConsentLeavesTheDaemonRunning is the same rule checked end to end,
// against a real daemon process: declining must not kill anything.
func TestRestartWithoutConsentLeavesTheDaemonRunning(t *testing.T) {
	path := isolatedSocket(t)
	t.Setenv(muxd.EnvDaemonBin, buildSelf(t))

	c, err := muxd.ConnectOrSpawn(path)
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	before, err := muxd.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	_ = c.Close()
	t.Cleanup(func() { stopDaemon(path) })

	var out bytes.Buffer
	if err := restartDaemon(path, false, strings.NewReader("n\n"), &out); err != nil {
		t.Fatalf("restartDaemon: %v", err)
	}
	after, err := muxd.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect after decline: %v", err)
	}
	if after.PID != before.PID {
		t.Errorf("daemon pid changed %d → %d after the user declined", before.PID, after.PID)
	}
	if !strings.Contains(out.String(), "left alone") {
		t.Errorf("output %q does not say the daemon was left alone", out.String())
	}
}

// TestRestartReplacesTheDaemonAndSaysWhatItCosts covers the accepted path.
//
// Two things must be true afterwards, and the second is the one that is easy to forget:
// the daemon is genuinely a NEW process, and the user was told the price BEFORE being
// asked. A confirmation prompt that does not name the cost is not a confirmation.
func TestRestartReplacesTheDaemonAndSaysWhatItCosts(t *testing.T) {
	path := isolatedSocket(t)
	t.Setenv(muxd.EnvDaemonBin, buildSelf(t))

	c, err := muxd.ConnectOrSpawn(path)
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	if _, _, err := c.Create(muxd.CreateReq{Argv: []string{"/bin/sh"}, Cwd: t.TempDir()}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	before, err := muxd.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	_ = c.Close()
	t.Cleanup(func() { stopDaemon(path) })

	if before.Sessions != 1 {
		t.Fatalf("precondition: daemon reports %d live sessions, want 1", before.Sessions)
	}

	var out bytes.Buffer
	if err := restartDaemon(path, false, strings.NewReader("y\n"), &out); err != nil {
		t.Fatalf("restartDaemon: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "1 live") {
		t.Errorf("output %q never told the user how many sessions the restart would end", text)
	}
	if !strings.Contains(text, "terminated") {
		t.Errorf("output %q never told the user the sessions would be terminated", text)
	}

	after, err := muxd.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect after restart: %v", err)
	}
	if after.PID == before.PID {
		t.Errorf("daemon pid is still %d — nothing was actually restarted", after.PID)
	}
	if after.Sessions != 0 {
		t.Errorf("fresh daemon reports %d sessions, want 0", after.Sessions)
	}
}

// buildSelf compiles this command so the spawned daemon is the real binary rather than
// the test binary (which would re-run the test suite instead of serving).
func buildSelf(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "dw-terminal")
	out, err := runGoBuild(bin)
	if err != nil {
		t.Fatalf("build dw-terminal: %v: %s", err, out)
	}
	return bin
}

// stopDaemon ends the daemon bound to this socket, scoped to the path so it can never
// reach the developer's own.
func stopDaemon(path string) {
	info, err := muxd.Inspect(path)
	if err != nil && !muxd.IsNoDaemon(err) {
		return
	}
	if info.PID > 0 {
		_ = syscallKill(info.PID)
	}
	_ = os.Remove(path)
}

func runGoBuild(bin string) ([]byte, error) {
	cmd := exec.Command("go", "build", "-o", bin, ".")
	return cmd.CombinedOutput()
}

func syscallKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
