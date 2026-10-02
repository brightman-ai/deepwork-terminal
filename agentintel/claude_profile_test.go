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
	realCWD, err := filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	cmd := exec.Command("sleep", "30")
	cmd.Dir = realCWD
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+profile)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	processEnv, envErr := processEnvironment(cmd.Process.Pid)
	profileEnvVisible := false
	if envErr == nil {
		for _, field := range processEnv {
			if field == "CLAUDE_CONFIG_DIR="+profile {
				profileEnvVisible = true
				break
			}
		}
	}
	if !profileEnvVisible {
		// Do not read a PID-index file from the server profile when this platform
		// cannot prove the child uses that profile; a stale same-PID record is not
		// ownership evidence. The tracker may still use cwd-based discovery.
		_, err := NewProjectLocator().ClaudeSessionForProcess(cmd.Process.Pid, realCWD)
		require.ErrorIs(t, err, os.ErrNotExist)
		return
	}
	dir := filepath.Join(profile, "projects", strings.ReplaceAll(realCWD, "/", "-"))
	require.NoError(t, os.MkdirAll(dir, 0700))
	path := filepath.Join(dir, "profile-session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0600))
	writeSessionRecord(t, profile, cmd.Process.Pid, "profile-session", realCWD)
	writeSessionRecord(t, serverHome, cmd.Process.Pid, "wrong-session", realCWD)
	got, err := NewProjectLocator().ClaudeSessionForProcess(cmd.Process.Pid, realCWD)
	require.NoError(t, err)
	require.Equal(t, path, got)
	files, err := NewProjectLocator().ClaudeSessionFilesForProcess(cmd.Process.Pid, realCWD)
	require.NoError(t, err)
	require.Equal(t, []string{path}, files)
}
