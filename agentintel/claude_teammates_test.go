package agentintel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClaudeNamedTeammateKeepsIdleParentRunning(t *testing.T) {
	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	spawn := makeAgentSpawnRow(ts(-3*time.Hour), "spawn", "general-purpose", "worker")
	resolve := map[string]any{"type": "user", "timestamp": ts(-3*time.Hour + time.Second), "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "spawn"}}}, "toolUseResult": map[string]any{"status": "teammate_spawned", "agent_id": "impl@team-a", "name": "impl", "team_name": "team-a"}}
	path := writeJSONL(t, []map[string]any{spawn, resolve, makeEndTurnRow(ts(-2 * time.Hour))})
	dir := filepath.Join(path[:len(path)-len(filepath.Ext(path))], "subagents")
	require.NoError(t, os.MkdirAll(dir, 0700))
	meta, _ := json.Marshal(map[string]any{"name": "impl", "teamName": "team-a", "taskKind": "in_process_teammate"})
	// Sidecar is intentionally delayed until after the initial monitor scan.
	child := filepath.Join(dir, "agent-aimpl-123.jsonl")
	row := map[string]any{"type": "assistant", "timestamp": ts(-time.Minute), "message": map[string]any{"id": "work", "stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "id": "build", "name": "Bash"}}}}
	raw, _ := json.Marshal(row)
	require.NoError(t, os.WriteFile(child, append(raw, '\n'), 0600))
	d := NewClaudeDriver(path, "")
	require.NoError(t, d.Update())
	require.Empty(t, d.AgentTree(), "missing identity is pending, not a failed spawn")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-aimpl-123.meta.json"), meta, 0600))
	require.NoError(t, d.Update())
	require.Equal(t, StatusRunning, d.State().Status, "fresh child activity must outlive spawn TTL")
	require.Len(t, d.AgentTree(), 1)
	require.Equal(t, "aimpl-123", d.AgentTree()[0].ID)
	require.False(t, d.AgentState().AwaitingUser)
	end, _ := json.Marshal(makeEndTurnRow(ts(-30 * time.Second)))
	f, err := os.OpenFile(child, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.Write(append(end, '\n'))
	require.NoError(t, err)
	f.Close()
	require.NoError(t, d.Update())
	require.Equal(t, StatusIdle, d.State().Status)
	// A named SendMessage must address the existing agent, not create a new one.
	msg := map[string]any{"type": "assistant", "timestamp": ts(-time.Second), "message": map[string]any{"stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "id": "resume", "name": "SendMessage", "input": map[string]any{"to": "impl"}}}}}
	raw, _ = json.Marshal(msg)
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.Write(append(raw, '\n'))
	require.NoError(t, err)
	end, _ = json.Marshal(makeEndTurnRow(ts(0)))
	_, err = f.Write(append(end, '\n'))
	require.NoError(t, err)
	f.Close()
	require.NoError(t, d.Update())
	require.Equal(t, StatusRunning, d.State().Status)
	require.Len(t, d.AgentTree(), 1)
}

func TestClaudeAPIErrorStopsRunning(t *testing.T) {
	row := makeEndTurnRow(time.Now().UTC().Format(time.RFC3339Nano))
	row["isApiErrorMessage"] = true
	row["apiErrorIsTransient"] = false
	row["message"].(map[string]any)["stop_reason"] = "stop_sequence"
	d := NewClaudeDriver(writeJSONL(t, []map[string]any{row}), "")
	require.NoError(t, d.Update())
	require.Equal(t, StatusIdle, d.State().Status)
}

func TestClaudeTransientAPIErrorKeepsRetryRunning(t *testing.T) {
	row := makeEndTurnRow(time.Now().UTC().Format(time.RFC3339Nano))
	row["isApiErrorMessage"] = true
	row["apiErrorIsTransient"] = true
	row["message"].(map[string]any)["stop_reason"] = "stop_sequence"
	d := NewClaudeDriver(writeJSONL(t, []map[string]any{row}), "")
	require.NoError(t, d.Update())
	require.Equal(t, StatusRunning, d.State().Status)
}
