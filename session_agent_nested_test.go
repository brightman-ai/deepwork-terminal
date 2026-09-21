package terminal

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/stretchr/testify/require"
)

// The PTY's outer shell stays in A while a nested shell starts Claude in B.
// A has an old idle transcript; only the agent's PID and cwd identify the live turn.
func TestSessionAgentTracker_NestedShellUsesAgentDirectory(t *testing.T) {
	home, outer, inner := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("DW_CLAUDE_PROJECTS", "")
	bin := filepath.Join(t.TempDir(), "claude")
	sleep, err := exec.LookPath("sleep")
	require.NoError(t, err)
	executable, err := os.ReadFile(sleep)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bin, executable, 0755))
	cmd := exec.Command(bin, "60")
	cmd.Dir = inner
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	locator := agentintel.NewProjectLocator()
	writeClaudeTranscript(t, locator.ClaudeProjectDir(outer), "unrelated", "finished", time.Now().Add(-time.Hour))
	own := writeClaudeTranscript(t, locator.ClaudeProjectDir(inner), "owned", "previous turn", time.Now().Add(-time.Minute))
	f, err := os.OpenFile(own, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"assistant","message":{"id":"current","model":"claude-test","stop_reason":"tool_use","content":[{"type":"tool_use","id":"bash-1","name":"Bash","input":{"command":"sleep 60"}}]}}` + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	index := filepath.Join(home, ".claude", "sessions")
	require.NoError(t, os.MkdirAll(index, 0700))
	record, err := json.Marshal(map[string]any{"pid": cmd.Process.Pid, "cwd": inner, "sessionId": "owned"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(index, strconv.Itoa(cmd.Process.Pid)+".json"), record, 0600))
	tr := newSessionAgentTracker()
	tr.inspector = agentintel.NewProcessInspector()
	got := tr.State(context.Background(), "nested", os.Getpid(), outer, nil)
	require.Equal(t, agentintel.ToolClaude, got.AgentTool)
	require.Equal(t, agentintel.StatusRunning, got.AgentStatus, "outer shell cwd must not bind an unrelated idle transcript")
	require.Equal(t, string(agentintel.RuleTranscriptRunning), got.StatusRule)
	require.Equal(t, inner, got.CWD)

	// The all-tabs card, REST snapshot and WS provider share that exact binding.
	srv, sm := newOverviewTestServer(t)
	srv.sessionAgent = tr
	sess, err := sm.Create("nested")
	require.NoError(t, err)
	sess.setShellPID(os.Getpid())
	sess.CWD = outer
	entries := srv.sessionsOverview(context.Background())
	require.Len(t, entries, 1)
	require.Equal(t, inner, entries[0].CWD)
	require.Equal(t, agentintel.StatusRunning, entries[0].AgentStatus)
	state, err := srv.sessionAgentSnapshot(context.Background(), sess.ID)
	require.NoError(t, err)
	require.NotNil(t, state.Current)
	require.Equal(t, entries[0].AgentStatus, state.Current.Status)
	require.Equal(t, "claude-test", state.Current.Model)
	require.Empty(t, state.Notifications)

	w := httptest.NewRecorder()
	srv.handleListSessions(w, httptest.NewRequest("GET", "/sessions", nil))
	require.Equal(t, 200, w.Code)
	var listed []struct {
		CWD         string `json:"cwd"`
		AgentStatus string `json:"agentStatus"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.Len(t, listed, 1)
	require.Equal(t, inner, listed[0].CWD)
	require.Equal(t, "running", listed[0].AgentStatus)

	// A genuine completion still clears running and publishes the matching attention.
	f, err = os.OpenFile(own, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.WriteString(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"bash-1","content":"done"}]}}` + "\n")
	require.NoError(t, err)
	row, err := json.Marshal(map[string]any{"type": "assistant", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"id": "finished", "model": "claude-test", "stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": "done"}}}})
	require.NoError(t, err)
	_, err = f.Write(append(row, '\n'))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	srv.overviewCacheMu.Lock()
	srv.overviewCacheAt = time.Time{}
	srv.overviewAttemptAt = time.Time{}
	srv.overviewCacheMu.Unlock()
	state, err = srv.sessionAgentSnapshot(context.Background(), sess.ID)
	require.NoError(t, err)
	require.NotNil(t, state.Current)
	require.Equal(t, agentintel.StatusIdle, state.Current.Status)
	require.True(t, state.Current.AwaitingUser)
	require.Len(t, state.Notifications, 1)
}

func TestSessionAgentSnapshot_NoAgentAndUnknownSession(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("plain-shell")
	require.NoError(t, err)
	state, err := srv.sessionAgentSnapshot(context.Background(), sess.ID)
	require.NoError(t, err)
	require.Nil(t, state.Current)
	require.Empty(t, state.Notifications)
	_, err = srv.sessionAgentSnapshot(context.Background(), "missing")
	require.Error(t, err)
}
