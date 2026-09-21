package agentintel

import (
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeProcessProfileOwnsTranscript(t *testing.T) {
	serverHome := claudeHomeFixture(t)
	profile := t.TempDir()
	cwd := t.TempDir()
	cmd := exec.Command("sleep", "30")
	cmd.Dir = cwd
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+profile)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	dir := filepath.Join(profile, "projects", strings.ReplaceAll(cwd, "/", "-"))
	require.NoError(t, os.MkdirAll(dir, 0700))
	path := filepath.Join(dir, "profile-session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0600))
	writeSessionRecord(t, profile, cmd.Process.Pid, "profile-session", cwd)
	writeSessionRecord(t, serverHome, cmd.Process.Pid, "wrong-session", cwd)
	got, err := NewProjectLocator().ClaudeSessionForProcess(cmd.Process.Pid, cwd)
	require.NoError(t, err)
	require.Equal(t, path, got)
	files, err := NewProjectLocator().ClaudeSessionFilesForProcess(cmd.Process.Pid, cwd)
	require.NoError(t, err)
	require.Equal(t, []string{path}, files)
}
