package terminal

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// newRealPTYManager builds a SessionManager backed by REAL PTYs, on a daemon that
// belongs to this test alone.
//
// Isolation is mandatory, not tidiness: without it a test would connect-or-spawn against
// the developer's live daemon and then "clean up" by destroying sessions — someone's
// actual work. This repo has already lost live tmux sessions to exactly that shape of
// mistake, which is why muxd.SocketPath refuses to answer inside a test binary at all.
// This helper is the sanctioned way to satisfy it.
//
// The daemon runs in-process (no spawned binary) so tests stay fast and leave nothing
// behind; the sessions still travel the real socket and the real protocol.
func newRealPTYManager(t *testing.T, bufferSize int, shell string) *SessionManager {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "run", "muxd.sock")
	t.Setenv(muxd.EnvSocketOverride, sock)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(sock))

	// muxd.RealPTY is the production factory — the same function the spawned daemon
	// uses, not a test double. Only the daemon's hosting differs.
	sm := NewSessionManagerWithFactory(bufferSize, shell, muxd.RealPTY)
	t.Cleanup(sm.DestroyAll)
	return sm
}

// newSpawnedDaemonManager builds a SessionManager backed by a REAL, separately-spawned
// daemon process (not the in-process fixture), on a socket of its own.
//
// Some behaviour only exists across a process boundary — a daemon that is SIGKILLed
// cannot send an exit notification, which is precisely the case that produces ghost
// sessions. An in-process daemon can never reproduce it.
func newSpawnedDaemonManager(t *testing.T) (*SessionManager, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "run", "muxd.sock")
	t.Setenv(muxd.EnvSocketOverride, sock)
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(sock))
	t.Setenv(muxd.EnvDaemonBin, buildTestBinary(t))

	sm := NewSessionManager(1<<16, "/bin/sh")
	t.Cleanup(func() {
		sm.DestroyAll()
		killDaemon(t, sock)
	})
	return sm, sock
}

// buildTestBinary compiles dw-terminal once per package run, so the daemon under test is
// the real binary rather than a stand-in.
var (
	testBinOnce sync.Once
	testBinPath string
	testBinErr  error
)

func buildTestBinary(t *testing.T) string {
	t.Helper()
	testBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dwmux-testbin-")
		if err != nil {
			testBinErr = err
			return
		}
		testBinPath = filepath.Join(dir, "dw-terminal")
		cmd := exec.Command("go", "build", "-o", testBinPath, "./cmd/dw-terminal")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			testBinErr = fmt.Errorf("%w: %s", err, stderr.String())
		}
	})
	if testBinErr != nil {
		t.Fatalf("build dw-terminal: %v", testBinErr)
	}
	return testBinPath
}

// killDaemon SIGKILLs the daemon bound to this socket — scoped to the path so it can
// never reach the developer's own daemon.
func killDaemon(t *testing.T, sock string) {
	t.Helper()
	out, err := exec.Command("pgrep", "-f", "muxd --socket "+sock).Output()
	if err != nil {
		return
	}
	for _, f := range strings.Fields(string(out)) {
		_ = exec.Command("kill", "-9", f).Run()
	}
}

// waitForPTYSize blocks until the session reports the given grid, or fails the test.
//
// It is a poll rather than an assertion because the size is the DAEMON'S answer, not the
// caller's request: a resize is applied by the daemon and reported back over the attached
// stream, so "I asked" and "it happened" are two events with a socket round trip between
// them. Reading PTYSize immediately after asking would be reading our own wish.
func waitForPTYSize(t *testing.T, sess *Session, cols, rows int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		gotC, gotR := sess.PTYSize()
		if gotC == cols && gotR == rows {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s size = %dx%d, want %dx%d after %s",
				sess.ID, gotC, gotR, cols, rows, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
