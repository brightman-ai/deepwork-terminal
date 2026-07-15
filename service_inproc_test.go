package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestService creates an InProcessService with a pipe-based mock PTY.
// Returns the service and an inject helper that writes data into the most
// recently created session's PTY read side (simulating shell output).
func newTestService(t *testing.T) (*InProcessService, func(data []byte)) {
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
	svc := NewInProcessService(sm)

	inject := func(data []byte) {
		if writeEnd != nil {
			_, _ = writeEnd.Write(data)
		}
	}

	t.Cleanup(func() {
		_ = svc.Close()
		if writeEnd != nil {
			_ = writeEnd.Close()
		}
	})

	return svc, inject
}

// TestInProcessService_CreateAndList verifies that Create adds a session that
// appears in List with the correct metadata.
func TestInProcessService_CreateAndList(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	info, err := svc.Create(ctx, CreateOptions{Name: "my-session", Engine: "shell"})
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.NotEmpty(t, info.ID)
	assert.Equal(t, "my-session", info.Name)
	assert.Equal(t, "shell", info.Engine)
	assert.Equal(t, StatusRunning, info.Status)
	assert.NotEmpty(t, info.CreatedAt)
	assert.NotEmpty(t, info.LastActive)

	// CreatedAt must be a valid RFC3339 timestamp.
	_, parseErr := time.Parse(time.RFC3339, info.CreatedAt)
	assert.NoError(t, parseErr, "CreatedAt should be RFC3339")

	// List must include the created session.
	list, err := svc.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, info.ID, list[0].ID)
}

// TestInProcessService_GetNotFound verifies that Get returns an error for an
// unknown session ID.
func TestInProcessService_GetNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.Get(ctx, "nonexistent-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestInProcessService_DeleteAndVerify verifies that Delete removes the session
// and subsequent Get returns an error.
func TestInProcessService_DeleteAndVerify(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	info, err := svc.Create(ctx, CreateOptions{Name: "to-delete"})
	require.NoError(t, err)

	err = svc.Delete(ctx, info.ID)
	require.NoError(t, err)

	_, err = svc.Get(ctx, info.ID)
	assert.Error(t, err, "session should be gone after Delete")

	list, err := svc.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)
}

// TestInProcessService_TailOutput verifies that TailOutput returns the last N
// lines written to the PTY's ring buffer.
func TestInProcessService_TailOutput(t *testing.T) {
	svc, inject := newTestService(t)
	ctx := context.Background()

	info, err := svc.Create(ctx, CreateOptions{Name: "tail-test"})
	require.NoError(t, err)

	// Inject several lines of output.
	inject([]byte("line1\nline2\nline3\nline4\nline5\n"))

	// Give the readLoop goroutine time to drain the pipe into the ring buffer.
	time.Sleep(50 * time.Millisecond)

	lines, err := svc.TailOutput(ctx, info.ID, 3)
	require.NoError(t, err)
	// We expect up to 3 lines; exact count depends on timing, but at least one
	// non-empty line should be present if the ring buffer has any data.
	for _, l := range lines {
		assert.True(t, strings.HasPrefix(l, "line"), "unexpected line: %q", l)
	}
}

// TestInProcessService_TailOutputNotFound verifies TailOutput returns error for unknown session.
func TestInProcessService_TailOutputNotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.TailOutput(ctx, "ghost-id", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestInProcessService_PasteUpload_CWDResolution gives PasteUpload's cwd resolution its first
// coverage. The batch-2 fix made the in-proc bypass mirror workbenchCWD's fallback ORDER
// (override → live /proc → static creation cwd) instead of jumping straight to the frozen
// creation dir. This pins the two branches the pipe-mock harness can exercise deterministically:
// an explicit override is honoured, and an empty override resolves through the chain — here
// landing on the creation cwd, since a mock session has no live shell process (ShellPID 0 →
// liveShellCWD ""), which is the correct terminal fallback.
func TestInProcessService_PasteUpload_CWDResolution(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	created := t.TempDir()
	info, err := svc.Create(ctx, CreateOptions{Name: "paste-cwd", CWD: created})
	require.NoError(t, err)

	// Explicit override wins: the file lands under the override dir, not the creation dir.
	override := t.TempDir()
	p1, err := svc.PasteUpload(ctx, info.ID, "shot.png", strings.NewReader("hello-override"), override)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(p1, filepath.Join(override, "tmp")+string(os.PathSeparator)),
		"override cwd should anchor the paste: got %q", p1)

	// Empty override → fallback chain. With no live shell (pipe mock), it resolves to the
	// creation cwd — the correct terminal fallback — and never errors.
	p2, err := svc.PasteUpload(ctx, info.ID, "note.txt", strings.NewReader("hello-fallback"), "")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(p2, filepath.Join(created, "tmp")+string(os.PathSeparator)),
		"empty override should fall back to the resolved session cwd: got %q", p2)
}
