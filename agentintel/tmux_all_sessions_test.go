package agentintel

import (
	"context"
	"github.com/stretchr/testify/require"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestListPanesIncludesEverySession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	socket := filepath.Join(t.TempDir(), "audit.sock")
	t.Setenv("TMUX", socket)
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("-f", "/dev/null", "new-session", "-d", "-s", "one")
	t.Cleanup(func() { exec.Command("tmux", "-S", socket, "kill-server").Run() })
	run("new-session", "-d", "-s", "two")
	prober := NewTmuxProber(NewProcessInspector())
	panes, err := prober.ListPanes(context.Background())
	require.NoError(t, err)
	names := map[string]bool{}
	for _, p := range panes {
		names[p.SessionName] = true
	}
	require.Equal(t, map[string]bool{"one": true, "two": true}, names)
}
