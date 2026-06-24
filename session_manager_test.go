package terminal

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipePTYFactory creates a pipe-based mock PTY for testing in environments
// where fork/exec is restricted. The read end acts as the "PTY master",
// and the write end is stored in the Cmd field's Stdout for simulation.
func pipePTYFactory(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	// We return the read end as the "master" (what readLoop reads from),
	// and store the write end so the test can write data to it.
	// Cmd is nil — no real process.
	// We store the write end in a way the test can access it.
	// For simplicity, we'll wrap it: the caller accesses sess.PTY for writes
	// but the read side is what gets passed as master.
	// Actually, let's return write-end as master for I/O (tests write to w,
	// readLoop reads from r). We need readLoop to read from r.
	// So master=r, and the test writes to w.
	// But we also need sess.PTY.Write to work for input simulation.
	// For the mock, we'll just use a pipe pair where:
	// - The returned master (r) is what readLoop reads from
	// - The write end (w) is stored somewhere the test can push data
	// Let's return r as the PTY master and store w in an accessible way.
	// We'll use the Cmd's ExtraFiles or a separate mechanism.

	// Simple approach: return the read end as PTY (readLoop reads from it).
	// The test gets the write end via the session's test helper.
	// But Cmd is nil so we need to handle that in Destroy gracefully.

	// We'll store w in a goroutine-safe way by using a custom approach.
	// For now, swap: master=w (so PTY.Write works for input simulation),
	// but readLoop reads from sess.PTY which would be w... that's wrong.
	// Let's use a different approach: return r as the pseudo-PTY,
	// and the test holds w to inject data.

	_ = w // w will be captured by the test via closure
	return r, nil, nil
}

// newTestManager creates a SessionManager with pipe-based mock PTY.
// Returns the manager and a helper to inject data into the most recently created session.
func newTestManager(t *testing.T) (*SessionManager, func(data []byte)) {
	t.Helper()

	var writeEnd *os.File

	factory := func(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		writeEnd = w
		return r, nil, nil
	}

	sm := NewSessionManagerWithFactory(1024, "/bin/sh", factory)

	inject := func(data []byte) {
		if writeEnd != nil {
			writeEnd.Write(data)
		}
	}

	t.Cleanup(func() {
		sm.DestroyAll()
		if writeEnd != nil {
			writeEnd.Close()
		}
	})

	return sm, inject
}

// TC-08-SM-01: SessionManager.Create() creates a session with running status.
func TestSessionManager_Create(t *testing.T) {
	sm, _ := newTestManager(t)

	sess, err := sm.Create("test-session")
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.NotEmpty(t, sess.ID)
	assert.Equal(t, "test-session", sess.Name)
	assert.Equal(t, StatusRunning, sess.Status)
	assert.NotNil(t, sess.PTY)
	assert.NotNil(t, sess.Buffer)
	assert.False(t, sess.CreatedAt.IsZero())
	assert.False(t, sess.LastActive.IsZero())

	// Verify we can get it back.
	got, err := sm.Get(sess.ID)
	require.NoError(t, err)
	assert.Equal(t, sess.ID, got.ID)

	// Default name when empty.
	sess2, err := sm.Create("")
	require.NoError(t, err)
	// Default name uses MMdd-HHmm format (e.g. "0501-1020").
	assert.Regexp(t, `^\d{4}-\d{4}$`, sess2.Name)
}

func TestPTYEnvForBrowserTerminal(t *testing.T) {
	got := ptyEnv([]string{
		"PATH=/bin",
		"TERM=dumb",
		"COLORTERM=old",
		"SHELL=/bin/zsh",
	})

	assert.Contains(t, got, "PATH=/bin")
	assert.Contains(t, got, "SHELL=/bin/zsh")
	assert.Contains(t, got, "TERM=xterm-256color")
	assert.Contains(t, got, "COLORTERM=truecolor")
	assert.NotContains(t, got, "TERM=dumb")
	assert.NotContains(t, got, "COLORTERM=old")
}

// TC-08-SM-02: SessionManager.List() returns all sessions.
func TestSessionManager_List(t *testing.T) {
	sm, _ := newTestManager(t)

	// Empty list.
	list := sm.List()
	assert.Empty(t, list)

	// Create 3 sessions.
	for i := 0; i < 3; i++ {
		_, err := sm.Create("")
		require.NoError(t, err)
	}

	list = sm.List()
	assert.Len(t, list, 3)

	// All should have unique IDs.
	ids := make(map[string]bool)
	for _, s := range list {
		ids[s.ID] = true
	}
	assert.Len(t, ids, 3)
}

// TC-08-SM-03: SessionManager.Destroy() removes session and cleans up.
func TestSessionManager_Destroy(t *testing.T) {
	sm, _ := newTestManager(t)

	sess, err := sm.Create("to-destroy")
	require.NoError(t, err)

	err = sm.Destroy(sess.ID)
	require.NoError(t, err)

	// Session should be gone.
	_, err = sm.Get(sess.ID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// List should be empty.
	assert.Empty(t, sm.List())

	// Destroying again should fail.
	err = sm.Destroy(sess.ID)
	assert.Error(t, err)
}

// TC-08-SM-04: Shell exit (pipe close) transitions status to "exited".
func TestSessionManager_ShellExitStatus(t *testing.T) {
	var writeEnd *os.File
	factory := func(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		writeEnd = w
		return r, nil, nil
	}

	sm := NewSessionManagerWithFactory(1024, "/bin/sh", factory)

	sess, err := sm.Create("exit-test")
	require.NoError(t, err)

	// Close the write end to simulate shell exit (EOF on read end).
	writeEnd.Close()

	// Wait for the done channel (readLoop detects EOF).
	select {
	case <-sess.done:
		// Shell exited.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for shell to exit")
	}

	sess.mu.Lock()
	status := sess.Status
	sess.mu.Unlock()
	assert.Equal(t, StatusExited, status, "session status should be 'exited' after pipe closes")
}
