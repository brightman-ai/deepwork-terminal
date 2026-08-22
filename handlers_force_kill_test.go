package terminal

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// unsetTMUXForTest strips TMUX from THIS process's environment for the duration of the test and
// restores it after. Session spawn inherits the test process's env verbatim (ptyEnv doesn't
// filter TMUX — see session_manager.go), so running `go test` inside a tmux pane (as this repo's
// own dev sandbox does) leaks TMUX into every spawned child and makes GetTmuxDetected() true for
// what is really a plain PTY — a test-environment artifact (D-type), not a product fact.
func unsetTMUXForTest(t *testing.T) {
	t.Helper()
	orig, ok := os.LookupEnv("TMUX")
	if !ok {
		return
	}
	os.Unsetenv("TMUX")
	t.Cleanup(func() { os.Setenv("TMUX", orig) })
}

// handleForceKillForeground / Session.ForceKillForeground — the recovery command for "the
// foreground program in this tab ignores Ctrl+C". These lock in the three defined behaviors from
// request.md AC-2/AC-3/AC-4/AC-6: it kills a foreground process that ignores SIGTERM/SIGINT while
// leaving the shell alive, it kills the shell itself when there's no distinct foreground child
// (defined, not a no-op), and it rejects cleanly (not panics) for tmux sessions / unknown ids /
// sessions with no active PTY.

// waitForBufferContains polls a session's ring buffer for a substring, the same shape used
// throughout this package's other real-PTY tests to avoid a fixed sleep racing shell timing.
func waitForBufferContains(t *testing.T, sess *Session, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(string(sess.Buffer.ReadTail(8192)), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in session output; last seen: %q", want, string(sess.Buffer.ReadTail(8192)))
}

// TestForceKillForeground_KillsSigtermIgnoringChild reproduces the exact incident that motivated
// this feature: `sh -c 'trap "" TERM INT; exec sleep 999'` run as a FOREGROUND job. The trap is set
// on the shell before `exec`, and POSIX exec() preserves an ignored (SIG_IGN) disposition across
// exec — so the resulting `sleep` process itself has SIGTERM and SIGINT permanently ignored,
// exactly like the hung `codex --yolo` process this feature exists to recover from. Ctrl+C
// (delivered as SIGINT to the PTY's foreground process group) would do nothing to it; only SIGKILL
// (which no process can catch, block, or ignore) can end it — proving the mechanism reaches past
// Ctrl+C's ceiling, not just that it can kill an ordinary child.
func TestForceKillForeground_KillsSigtermIgnoringChild(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	if _, err := exec.LookPath("/bin/bash"); err != nil {
		t.Skip("bash not available")
	}
	unsetTMUXForTest(t)
	mgr := newRealPTYManager(t, 1<<16, "/bin/bash")
	sess, err := mgr.Create("hang")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := sess.WriteInput([]byte("sh -c 'trap \"\" TERM INT; exec sleep 999'\n")); err != nil {
		t.Fatalf("write foreground job: %v", err)
	}
	// Give the shell time to fork/exec the foreground job and hand it the tty before we read pgid.
	time.Sleep(400 * time.Millisecond)

	if err := sess.ForceKillForeground(); err != nil {
		t.Fatalf("ForceKillForeground: %v", err)
	}

	// The shell must survive and regain the tty — proven by it executing a NEW command afterward.
	if err := sess.WriteInput([]byte("echo FORCE_KILL_OK_$$\n")); err != nil {
		t.Fatalf("write follow-up command: %v", err)
	}
	waitForBufferContains(t, sess, "FORCE_KILL_OK_", 5*time.Second)

	if sess.GetStatus() != StatusRunning {
		t.Fatalf("shell should still be running after killing only its foreground child, got status=%v", sess.GetStatus())
	}
}

// TestForceKillForeground_NoForegroundChildKillsShellItself locks in AC-3's defined behavior: when
// the foreground process IS the shell (sitting at its own prompt, no job running), the shell gets
// killed too — the tab disconnects rather than silently no-op'ing. A command whose entire purpose
// is "end whatever's in front" must not pretend to succeed while leaving the user exactly as stuck.
func TestForceKillForeground_NoForegroundChildKillsShellItself(t *testing.T) {
	if _, err := exec.LookPath("/bin/bash"); err != nil {
		t.Skip("bash not available")
	}
	unsetTMUXForTest(t)
	mgr := newRealPTYManager(t, 1<<16, "/bin/bash")
	sess, err := mgr.Create("idle")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // let bash reach its prompt (become its own foreground pgid)

	if err := sess.ForceKillForeground(); err != nil {
		t.Fatalf("ForceKillForeground: %v", err)
	}

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shell was not killed — session never reached done (AC-3 requires the shell itself to die when there is no distinct foreground child)")
	}
	if sess.GetStatus() != StatusExited {
		t.Fatalf("status = %v, want %v", sess.GetStatus(), StatusExited)
	}
}

// TestForceKillForeground_TmuxSessionRejected: tmux's foreground pgid on the PTY master belongs to
// tmux's own client/server plumbing, not a fact the web UI can act on (see request.md ③非目标).
// This is the backend half of the guard — defense in depth alongside the frontend hiding the menu
// item — and it must reject explicitly, not silently do nothing or crash.
func TestForceKillForeground_TmuxSessionRejected(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("tmux-tab")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.mu.Lock()
	sess.TmuxDetected = true
	sess.mu.Unlock()

	if err := sess.ForceKillForeground(); err == nil {
		t.Fatal("expected an error for a tmux-attached session, got nil")
	}

	req := httptest.NewRequest(http.MethodPost, "/sessions/"+sess.ID+"/force-kill-fg", nil)
	req.SetPathValue("id", sess.ID)
	w := httptest.NewRecorder()
	srv.handleForceKillForeground(w, req)
	if w.Code == http.StatusNoContent {
		t.Fatalf("tmux session must not report success, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestForceKillForeground_NoPTYExplicitError covers AC-6's "PTY already gone" branch: a session
// that can no longer reach its PTY (mirrors the state right after Destroy, or before it is
// attached) must return an explicit error, never panic.
//
// The unreachable state is now "no daemon connection" rather than "nil *os.File", because the
// PTY lives in the daemon — but the branch under test, and the guarantee it makes, are the same.
func TestForceKillForeground_NoPTYExplicitError(t *testing.T) {
	_, sm := newOverviewTestServer(t)
	sess, err := sm.Create("no-pty")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.mu.Lock()
	sess.mux = nil
	sess.mu.Unlock()

	if err := sess.ForceKillForeground(); err == nil {
		t.Fatal("expected an explicit error for a session with no PTY, got nil")
	}
}

// TestHandleForceKillForeground_UnknownSession404 — AC-6's other branch.
func TestHandleForceKillForeground_UnknownSession404(t *testing.T) {
	srv, _ := newOverviewTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/sessions/does-not-exist/force-kill-fg", bytes.NewReader(nil))
	req.SetPathValue("id", "does-not-exist")
	w := httptest.NewRecorder()
	srv.handleForceKillForeground(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown session id should 404, got %d", w.Code)
	}
}
