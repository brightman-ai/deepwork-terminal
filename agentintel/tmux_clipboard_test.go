package agentintel

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

func TestClipboardTmuxSendOnceAndKeepControlRepliesAligned(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir := t.TempDir()
	socket := filepath.Join(dir, "clipboard.sock")
	t.Setenv("TMUX", socket)
	run := func(args ...string) string {
		t.Helper()
		b, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		require.NoError(t, err, string(b))
		return string(b)
	}
	run("-f", "/dev/null", "new-session", "-d", "-s", "clipboard", "/bin/bash --noprofile --norc")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	cmd := exec.Command("tmux", "-S", socket, "attach-session", "-t", "clipboard")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	terminal, err := pty.Start(cmd)
	require.NoError(t, err)
	go func() { _, _ = io.Copy(io.Discard, terminal) }()
	t.Cleanup(func() { _ = terminal.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	service := NewTmuxStateService()
	// Explicit opt-in only on this test's private socket; never a real server.
	service.prober.control = &tmuxControl{}
	t.Cleanup(func() { service.prober.control.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var panes []TmuxPane
	require.Eventually(t, func() bool {
		var attached bool
		panes, attached, err = service.ClipboardPanes(ctx, cmd.Process.Pid)
		return err == nil && attached && len(panes) == 1
	}, 3*time.Second, 20*time.Millisecond)
	target := panes[0]
	path := filepath.Join(dir, "sent")
	for i := 0; i < 3; i++ {
		require.NoError(t, service.PasteClipboard(ctx, cmd.Process.Pid, target.PaneID, target.PanePID, fmt.Sprintf("printf x >> '%s'", path)))
		require.Eventually(t, func() bool { b, _ := os.ReadFile(path); return len(b) == i+1 }, time.Second, 10*time.Millisecond)
		// A compound control command previously left the next lookup reading a stray
		// acknowledgement, and its spawn fallback reported failure AFTER executing.
		for j := 0; j < 3; j++ {
			current, attached, err := service.ClipboardPanes(ctx, cmd.Process.Pid)
			require.NoError(t, err)
			require.True(t, attached)
			require.Len(t, current, 1)
			require.Equal(t, target.PaneID, current[0].PaneID)
		}
	}
	require.Error(t, service.PasteClipboard(ctx, cmd.Process.Pid, target.PaneID, target.PanePID+1, "echo wrong process"))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "xxx", string(b))
}
