package agentintel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeCodexRollout(t *testing.T, path string, rows ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
}

func codexMeta(id, parent, agentPath string, depth int) map[string]any {
	payload := map[string]any{"id": id, "session_id": id, "cwd": "/tmp/project"}
	if parent != "" {
		payload["source"] = map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{
			"parent_thread_id": parent, "depth": depth, "agent_path": agentPath,
			"agent_nickname": "reviewer", "agent_role": "review",
		}}}
	}
	return map[string]any{"timestamp": "2026-07-12T09:00:00Z", "type": "session_meta", "payload": payload}
}

func codexSpawn(taskName, message string) map[string]any {
	args, _ := json.Marshal(map[string]any{"task_name": taskName, "message": message})
	return map[string]any{"timestamp": "2026-07-12T09:00:01Z", "type": "response_item", "payload": map[string]any{
		"type": "function_call", "name": "spawn_agent", "arguments": string(args),
	}}
}

func TestCodexAgentTreeProjectsNestedLifecycleAndCurrentTokenSchema(t *testing.T) {
	day := filepath.Join(t.TempDir(), "sessions", "2026", "07", "12")
	root := filepath.Join(day, "rollout-root.jsonl")
	child := filepath.Join(day, "rollout-child.jsonl")
	grandchild := filepath.Join(day, "rollout-grandchild.jsonl")

	writeCodexRollout(t, root,
		codexMeta("root-thread", "", "", 0),
		codexSpawn("reviewer", "Review the workspace structure"),
	)
	writeCodexRollout(t, child,
		codexMeta("child-thread", "root-thread", "/root/reviewer", 1),
		codexSpawn("nested", "Inspect the state reducer"),
		makeCodexRow("event_msg", map[string]any{"type": "task_started"}),
		makeCodexRow("event_msg", map[string]any{"type": "token_count", "info": map[string]any{
			"total_token_usage": map[string]any{"input_tokens": 500, "output_tokens": 123, "cached_input_tokens": 200, "total_tokens": 623},
		}}),
		makeCodexRow("event_msg", map[string]any{"type": "task_complete"}),
	)
	writeCodexRollout(t, grandchild,
		codexMeta("grandchild-thread", "child-thread", "/root/reviewer/nested", 2),
		makeCodexRow("event_msg", map[string]any{"type": "task_started"}),
		makeCodexRow("event_msg", map[string]any{"type": "turn_aborted"}),
	)

	d := NewCodexDriver(root)
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	tree := d.AgentTree()
	if len(tree) != 2 {
		t.Fatalf("want 2 nodes, got %d: %+v", len(tree), tree)
	}
	if tree[0].ID != "child-thread" || tree[0].ParentID != "" || tree[0].Status != AgentDone || tree[0].TokensDown != 123 {
		t.Fatalf("bad root child: %+v", tree[0])
	}
	if tree[0].Description != "Review the workspace structure" || tree[0].Runtime != "codex" || tree[0].Diagnostic != "complete" {
		t.Fatalf("missing runtime-neutral projection: %+v", tree[0])
	}
	if tree[1].ParentID != "child-thread" || tree[1].Depth != 2 || tree[1].Status != AgentError || tree[1].Description != "Inspect the state reducer" {
		t.Fatalf("bad nested child: %+v", tree[1])
	}

	s := d.State()
	if s.SessionID != "root-thread" {
		t.Fatalf("root id must accept current payload.id schema: %+v", s)
	}
}

func TestCodexAgentTreeDoesNotJoinDescriptionsAcrossParents(t *testing.T) {
	day := filepath.Join(t.TempDir(), "sessions", "2026", "07", "12")
	root := filepath.Join(day, "root.jsonl")
	otherRoot := filepath.Join(day, "other-root.jsonl")
	child := filepath.Join(day, "child.jsonl")
	writeCodexRollout(t, root, codexMeta("root", "", "", 0), codexSpawn("same", "correct"))
	writeCodexRollout(t, otherRoot, codexMeta("other", "", "", 0), codexSpawn("same", "wrong"))
	writeCodexRollout(t, child, codexMeta("child", "root", "/root/same", 1), makeCodexRow("event_msg", map[string]any{"type": "task_started"}))

	d := NewCodexDriver(root)
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	if got := d.AgentTree(); len(got) != 1 || got[0].Description != "correct" {
		t.Fatalf("description must join on parent thread + agent path, got %+v", got)
	}
}

func TestCodexAgentTreeEndedRootMakesUnresolvedChildUnknown(t *testing.T) {
	day := filepath.Join(t.TempDir(), "sessions", "2026", "07", "12")
	root := filepath.Join(day, "root.jsonl")
	child := filepath.Join(day, "child.jsonl")
	writeCodexRollout(t, root,
		codexMeta("root", "", "", 0), codexSpawn("child", "work"),
		makeCodexRow("event_msg", map[string]any{"type": "task_complete"}),
	)
	writeCodexRollout(t, child,
		codexMeta("child", "root", "/root/child", 1),
		makeCodexRow("event_msg", map[string]any{"type": "task_started"}),
	)
	d := NewCodexDriver(root)
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	got := d.AgentTree()
	if len(got) != 1 || got[0].Status != AgentUnknown || got[0].Diagnostic != "source-ended" || got[0].EndedAt.IsZero() {
		t.Fatalf("ended root + unterminated child must be explicit unknown: %+v", got)
	}
}
