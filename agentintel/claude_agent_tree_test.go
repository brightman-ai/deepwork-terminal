package agentintel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- row builders -----------------------------------------------------------
// These mirror the exact field shapes confirmed against a real 22MB Claude
// Code coordinator transcript (see claude_agent_tree.go's schema notes) —
// not invented from the (incorrect, older) "Task tool + isSidechain" shape.

func makeAgentSpawnRow(ts, toolUseID, subagentType, description string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"timestamp": ts,
		"message": map[string]any{
			"id":    "msg-spawn-" + toolUseID,
			"model": "claude-sonnet-5",
			"content": []any{
				map[string]any{
					"type": "tool_use",
					"id":   toolUseID,
					"name": "Agent",
					"input": map[string]any{
						"subagent_type":     subagentType,
						"description":       description,
						"model":             "sonnet",
						"run_in_background": true,
						"prompt":            "do the thing",
					},
				},
			},
			"stop_reason": "tool_use",
		},
	}
}

// makeAgentResolveRow is the tool_result row that follows a spawn, assigning
// the durable agentId (schema note 2). This is the ONLY observed path —
// synchronous/foreground Agent calls (no agentId) were never seen in the
// real sample and are deliberately not fabricated here.
func makeAgentResolveRow(ts, toolUseID, agentID string) map[string]any {
	return map[string]any{
		"type":      "user",
		"timestamp": ts,
		"message": map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"tool_use_id": toolUseID,
					"type":        "tool_result",
					"content": []any{
						map[string]any{"type": "text", "text": "Async agent launched successfully.\nagentId: " + agentID},
					},
				},
			},
		},
		"toolUseResult": map[string]any{
			"isAsync":       true,
			"status":        "async_launched",
			"agentId":       agentID,
			"resolvedModel": "claude-sonnet-5",
		},
	}
}

// makeEndTurnRow is a plain finished main turn (assistant, stop_reason=end_turn) — the state
// that, alone, would flip a pane to a needs-you idle.
func makeEndTurnRow(ts string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"timestamp": ts,
		"message": map[string]any{
			"id":          "msg-endturn-" + ts,
			"model":       "claude-sonnet-5",
			"content":     []any{map[string]any{"type": "text", "text": "done for now."}},
			"stop_reason": "end_turn",
		},
	}
}

// TestClaudeStatus_MainIdleButSubagentRunning pins the fix: after the MAIN turn ends, a pane with a
// still-running background subagent must read RUNNING (not a needs-you idle); once every subagent
// has completed, the finished main turn is a genuine idle+awaiting again.
func TestClaudeStatus_MainIdleButSubagentRunning(t *testing.T) {
	base := []map[string]any{
		makeAgentSpawnRow("2026-07-12T00:00:00.000Z", "toolu_1", "Explore", "scan"),
		makeAgentResolveRow("2026-07-12T00:00:01.000Z", "toolu_1", "agent_abc"),
		makeEndTurnRow("2026-07-12T00:00:02.000Z"),
	}
	d := NewClaudeDriver(writeJSONL(t, base), "sess-sub")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	if got := d.State().Status; got != StatusRunning {
		t.Errorf("main end_turn + running subagent: got %q, want %q", got, StatusRunning)
	}
	if d.AgentState().AwaitingUser {
		t.Errorf("must NOT show needs-you while a subagent is still running")
	}

	// Subagent completes (root-file <task-notification>, channel a) → nothing running → the
	// finished main turn is now a real idle+awaiting.
	done := append(base, makeTaskNotificationQueueOpRow("2026-07-12T00:00:05.000Z", "agent_abc", "toolu_1", "completed", "done"))
	d2 := NewClaudeDriver(writeJSONL(t, done), "sess-sub2")
	if err := d2.Update(); err != nil {
		t.Fatal(err)
	}
	if got := d2.State().Status; got != StatusIdle {
		t.Errorf("main end_turn + all subagents done: got %q, want %q", got, StatusIdle)
	}
	if !d2.AgentState().AwaitingUser {
		t.Errorf("all subagents done + assistant spoke last → needs-you")
	}
}

func taskNotificationBody(taskID, toolUseID, status, summary string) string {
	return "<task-notification>\n<task-id>" + taskID + "</task-id>\n<tool-use-id>" + toolUseID +
		"</tool-use-id>\n<output-file>/tmp/x/tasks/" + taskID + ".output</output-file>\n<status>" + status +
		"</status>\n<summary>" + summary + "</summary>\n<note>resume note</note>\n</task-notification>"
}

// makeTaskNotificationQueueOpRow is channel (a) from schema note 4.
func makeTaskNotificationQueueOpRow(ts, taskID, toolUseID, status, summary string) map[string]any {
	return map[string]any{
		"type":      "queue-operation",
		"operation": "enqueue",
		"timestamp": ts,
		"sessionId": "sess-x",
		"content":   taskNotificationBody(taskID, toolUseID, status, summary),
	}
}

// makeTaskNotificationUserRow is channel (b) from schema note 4 — note
// message.content is a PLAIN STRING here, unlike every other user row in
// this package's tests.
func makeTaskNotificationUserRow(ts, taskID, toolUseID, status, summary string) map[string]any {
	return map[string]any{
		"type":      "user",
		"timestamp": ts,
		"message": map[string]any{
			"role":    "user",
			"content": taskNotificationBody(taskID, toolUseID, status, summary),
		},
	}
}

func makeSendMessageRow(ts, toolUseID, to string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"timestamp": ts,
		"message": map[string]any{
			"id": "msg-resume-" + toolUseID,
			"content": []any{
				map[string]any{
					"type":  "tool_use",
					"id":    toolUseID,
					"name":  "SendMessage",
					"input": map[string]any{"to": to, "summary": "resume", "message": "keep going"},
				},
			},
			"stop_reason": "tool_use",
		},
	}
}

// makeAgentUsageRow is an assistant row as it appears WITHIN a subagent's own
// transcript (isSidechain:true, agentId set) — used to exercise per-agent
// TokensDown accumulation (schema note 3 + TokensDown doc comment).
func makeAgentUsageRow(ts, agentID, msgID string, outputTokens int) map[string]any {
	return map[string]any{
		"type":        "assistant",
		"isSidechain": true,
		"agentId":     agentID,
		"timestamp":   ts,
		"message": map[string]any{
			"id":    msgID,
			"model": "claude-sonnet-5",
			"content": []any{
				map[string]any{"type": "text", "text": "working"},
			},
			"usage": map[string]any{
				"input_tokens":                float64(2),
				"output_tokens":               float64(outputTokens),
				"cache_read_input_tokens":     float64(0),
				"cache_creation_input_tokens": float64(0),
			},
		},
	}
}

// makeNestedAgentSpawnRow/makeNestedAgentResolveRow build the depth>=2 case:
// an "Agent" tool_use INSIDE a subagent's own transcript, resolved in that
// SAME file (schema note 3's "depth>=2 nesting" paragraph).
func makeNestedAgentSpawnRow(ts, ownerAgentID, toolUseID, subagentType, description string) map[string]any {
	row := makeAgentSpawnRow(ts, toolUseID, subagentType, description)
	row["isSidechain"] = true
	row["agentId"] = ownerAgentID
	return row
}

func makeNestedAgentResolveRow(ts, ownerAgentID, toolUseID, childAgentID string) map[string]any {
	row := makeAgentResolveRow(ts, toolUseID, childAgentID)
	row["isSidechain"] = true
	row["agentId"] = ownerAgentID
	return row
}

// writeAgentTranscript writes a subagent's own transcript at the exact
// deterministic path a real ClaudeDriver expects: <sessionDir>/subagents/
// agent-<agentID>.jsonl (schema note 3).
func writeAgentTranscript(t *testing.T, sessionDir, agentID string, rows []map[string]any) {
	t.Helper()
	dir := filepath.Join(sessionDir, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-"+agentID+".jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}

// sessionDirFor derives the same sessionDir a ClaudeDriver computes
// internally (root path minus its extension) — used by tests to place a
// subagent transcript where the driver will actually look for it.
func sessionDirFor(jsonlPath string) string {
	return jsonlPath[:len(jsonlPath)-len(filepath.Ext(jsonlPath))]
}

func findNode(nodes []AgentNode, id string) *AgentNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

// --- 1. single agent ---------------------------------------------------

func TestAgentTree_SingleAgentRunning(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_1", "general-purpose", "Extract decisions from transcript"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_1", "aAgentOne"),
	}
	path := writeJSONL(t, rows)
	d := NewClaudeDriver(path, "sess-1")
	if err := d.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tree := d.AgentTree()
	if len(tree) != 1 {
		t.Fatalf("want 1 node, got %d: %+v", len(tree), tree)
	}
	n := tree[0]
	if n.ID != "aAgentOne" || n.ParentID != "" || n.Depth != 1 {
		t.Errorf("node identity: got %+v", n)
	}
	if n.SubagentType != "general-purpose" || n.Description != "Extract decisions from transcript" {
		t.Errorf("subagent_type/description must be verbatim: got %+v", n)
	}
	if n.Status != AgentRunning {
		t.Errorf("status: got %q, want running (no notification yet)", n.Status)
	}
	if n.StartedAt.IsZero() {
		t.Error("StartedAt should be set from the spawn row's timestamp")
	}
	if !n.EndedAt.IsZero() {
		t.Error("EndedAt should be zero while running")
	}
}

// --- 1b. spawn failure (resolve row itself signals the error) -------------

// makeAgentFailedResolveRow builds the resolve row of an Agent tool_use that
// ERRORED before any agent existed: an error tool_result and no agentId
// anywhere. Variants mirror the shapes a failed tool call takes on disk.
func makeAgentFailedResolveRow(ts, toolUseID string, blockIsError bool, toolUseResult any) map[string]any {
	block := map[string]any{
		"tool_use_id": toolUseID,
		"type":        "tool_result",
		"content":     "Error: agent type 'no-such-agent' not found",
	}
	if blockIsError {
		block["is_error"] = true
	}
	row := map[string]any{
		"type":      "user",
		"timestamp": ts,
		"message": map[string]any{
			"role":    "user",
			"content": []any{block},
		},
	}
	if toolUseResult != nil {
		row["toolUseResult"] = toolUseResult
	}
	return row
}

// A spawn whose resolve row signals failure must become an AgentError node
// immediately (EndedAt = resolve time, ID = the spawn's tool_use id — no
// agentId was ever assigned) instead of sitting invisible/pending forever.
// Table-driven over the observed failure shapes; the final case guards the
// deliberately-narrow rule: a result that still claims async_launched without
// an error flag is NOT treated as failure (no aggressive sweeps).
func TestAgentTree_SpawnFailedResolve(t *testing.T) {
	const spawnTS = "2026-07-12T01:00:00Z"
	const resolveTS = "2026-07-12T01:00:02Z"

	cases := []struct {
		name       string
		resolveRow map[string]any
		wantError  bool // true → AgentError node; false → still pending (no node)
	}{
		{
			// The most common real shape: is_error:true block, toolUseResult
			// is a plain error string (not a map).
			name:       "is_error block + string toolUseResult",
			resolveRow: makeAgentFailedResolveRow(resolveTS, "toolu_f", true, "Error: agent type not found"),
			wantError:  true,
		},
		{
			// toolUseResult is a map but carries no agentId and no launch claim.
			name:       "map toolUseResult without agentId",
			resolveRow: makeAgentFailedResolveRow(resolveTS, "toolu_f", false, map[string]any{"status": "error", "error": "spawn failed"}),
			wantError:  true,
		},
		{
			// No toolUseResult at all — only the error-string tool_result block.
			name:       "error content, no toolUseResult",
			resolveRow: makeAgentFailedResolveRow(resolveTS, "toolu_f", true, nil),
			wantError:  true,
		},
		{
			// Guard: claims async_launched, no error flag, agentId merely missing
			// (malformed-but-optimistic) → stay pending, do NOT fabricate an error.
			name:       "async_launched without agentId stays pending",
			resolveRow: makeAgentFailedResolveRow(resolveTS, "toolu_f", false, map[string]any{"status": "async_launched"}),
			wantError:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := []map[string]any{
				makeAgentSpawnRow(spawnTS, "toolu_f", "general-purpose", "Doomed spawn"),
				tc.resolveRow,
			}
			d := NewClaudeDriver(writeJSONL(t, rows), "sess-fail")
			if err := d.Update(); err != nil {
				t.Fatalf("Update: %v", err)
			}
			tree := d.AgentTree()

			if !tc.wantError {
				if len(tree) != 0 {
					t.Fatalf("want no node (still pending), got %+v", tree)
				}
				return
			}
			if len(tree) != 1 {
				t.Fatalf("want 1 error node, got %d: %+v", len(tree), tree)
			}
			n := tree[0]
			if n.Status != AgentError {
				t.Errorf("status: got %q, want error", n.Status)
			}
			if n.ID != "toolu_f" {
				t.Errorf("ID must be the spawn's tool_use id (no agentId exists): got %q", n.ID)
			}
			if n.SubagentType != "general-purpose" || n.Description != "Doomed spawn" {
				t.Errorf("verbatim spawn fields: got %+v", n)
			}
			if n.EndedAt.IsZero() {
				t.Error("EndedAt must be set from the failing resolve row")
			}
			wantEnd, _ := time.Parse(time.RFC3339Nano, resolveTS)
			if !n.EndedAt.Equal(wantEnd) {
				t.Errorf("EndedAt: got %v, want %v (the resolve row's timestamp)", n.EndedAt, wantEnd)
			}
			if n.StartedAt.IsZero() {
				t.Error("StartedAt must carry the spawn row's timestamp")
			}
		})
	}
}

// A failed spawn must not disturb a sibling that resolved successfully in the
// same turn — mixed outcomes coexist.
func TestAgentTree_FailedSpawnDoesNotAffectSiblings(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_ok", "general-purpose", "Healthy"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_ok", "aOK"),
		makeAgentSpawnRow("2026-07-12T01:00:02Z", "toolu_bad", "general-purpose", "Doomed"),
		makeAgentFailedResolveRow("2026-07-12T01:00:03Z", "toolu_bad", true, "Error: boom"),
	}
	d := NewClaudeDriver(writeJSONL(t, rows), "sess-mixed")
	if err := d.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}
	tree := d.AgentTree()
	if len(tree) != 2 {
		t.Fatalf("want 2 nodes, got %+v", tree)
	}
	ok := findNode(tree, "aOK")
	bad := findNode(tree, "toolu_bad")
	if ok == nil || ok.Status != AgentRunning {
		t.Errorf("healthy sibling must stay running: %+v", ok)
	}
	if bad == nil || bad.Status != AgentError {
		t.Errorf("failed spawn must be error: %+v", bad)
	}
}

// --- 2. parallel multiple agents ----------------------------------------

func TestAgentTree_ParallelMultipleAgents(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_a", "general-purpose", "Task A"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_a", "aAgentA"),
		makeAgentSpawnRow("2026-07-12T01:00:02Z", "toolu_b", "general-purpose", "Task B"),
		makeAgentResolveRow("2026-07-12T01:00:03Z", "toolu_b", "aAgentB"),
	}
	path := writeJSONL(t, rows)
	d := NewClaudeDriver(path, "sess-2")
	if err := d.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tree := d.AgentTree()
	if len(tree) != 2 {
		t.Fatalf("want 2 nodes, got %d: %+v", len(tree), tree)
	}
	// Discovery order is stable spawn order.
	if tree[0].ID != "aAgentA" || tree[1].ID != "aAgentB" {
		t.Errorf("discovery order: got [%s, %s]", tree[0].ID, tree[1].ID)
	}
	for _, n := range tree {
		if n.ParentID != "" || n.Depth != 1 {
			t.Errorf("both are root-spawned siblings: got %+v", n)
		}
		if n.Status != AgentRunning {
			t.Errorf("both still running: got %+v", n)
		}
	}
}

// --- 3. completion states (done / error) --------------------------------

func TestAgentTree_CompletedAndFailedStatus(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_ok", "general-purpose", "Will succeed"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_ok", "aOK"),
		makeAgentSpawnRow("2026-07-12T01:00:02Z", "toolu_bad", "general-purpose", "Will fail"),
		makeAgentResolveRow("2026-07-12T01:00:03Z", "toolu_bad", "aBad"),
		// channel (a): queue-operation carries the notification first…
		makeTaskNotificationQueueOpRow("2026-07-12T01:05:00Z", "aOK", "toolu_ok", "completed", `Agent "Will succeed" finished`),
		// …followed shortly by channel (b): the same notification injected as a user turn.
		makeTaskNotificationUserRow("2026-07-12T01:05:00.05Z", "aOK", "toolu_ok", "completed", `Agent "Will succeed" finished`),
		makeTaskNotificationQueueOpRow("2026-07-12T01:05:05Z", "aBad", "toolu_bad", "failed", `Agent "Will fail" failed: API error`),
		makeTaskNotificationUserRow("2026-07-12T01:05:05.05Z", "aBad", "toolu_bad", "failed", `Agent "Will fail" failed: API error`),
	}
	path := writeJSONL(t, rows)
	d := NewClaudeDriver(path, "sess-3")
	if err := d.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tree := d.AgentTree()
	ok := findNode(tree, "aOK")
	bad := findNode(tree, "aBad")
	if ok == nil || bad == nil {
		t.Fatalf("expected both nodes present: %+v", tree)
	}
	if ok.Status != AgentDone {
		t.Errorf("aOK status: got %q, want done", ok.Status)
	}
	if ok.EndedAt.IsZero() {
		t.Error("aOK EndedAt should be set on completion")
	}
	if bad.Status != AgentError {
		t.Errorf("aBad status: got %q, want error", bad.Status)
	}
	if bad.EndedAt.IsZero() {
		t.Error("aBad EndedAt should be set on failure")
	}
}

// Some Claude Code async runs omit the parent <task-notification>. The child
// transcript's own end_turn is the terminal SSOT and must close the node; otherwise
// a completed agent survives forever as "running" after a reload.
func TestAgentTree_ChildEndTurnCompletesWithoutParentNotification(t *testing.T) {
	rootRows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_child_end", "general-purpose", "Independent witness"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_child_end", "aChildEnd"),
		makeEndTurnRow("2026-07-12T01:00:02Z"),
	}
	path := writeJSONL(t, rootRows)
	writeAgentTranscript(t, sessionDirFor(path), "aChildEnd", []map[string]any{
		makeAgentUsageRow("2026-07-12T01:02:00Z", "aChildEnd", "m-child", 42),
		makeEndTurnRow("2026-07-12T01:02:01Z"),
	})

	d := NewClaudeDriver(path, "sess-child-end")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aChildEnd")
	if n == nil || n.Status != AgentDone || n.EndedAt.IsZero() {
		t.Fatalf("child end_turn must close the node without parent notification: %+v", n)
	}
	if got := d.State().Status; got != StatusIdle {
		t.Fatalf("root end_turn + terminal child: got %q, want idle", got)
	}
}

// Older/synchronous Claude Code Agent runs terminate in the resolving root
// tool_result itself (toolUseResult.status + agentId), not in a later
// <task-notification>. Pin the real session-208 schema so completed historical
// agents cannot resurrect as permanently running after reload.
func TestAgentTree_SynchronousToolResultCompletesExistingAgent(t *testing.T) {
	rows := append(
		[]map[string]any{
			makeAgentSpawnRow("2026-06-10T19:20:00Z", "toolu_sync", "general-purpose", "old schema task"),
			makeAgentResolveRow("2026-06-10T19:20:01Z", "toolu_sync", "aSync"),
		},
		map[string]any{
			"type":      "user",
			"timestamp": "2026-06-10T19:30:00Z",
			"message": map[string]any{
				"role": "user",
				"content": []any{map[string]any{
					"type": "tool_result", "tool_use_id": "toolu_sync", "content": "final answer",
				}},
			},
			"toolUseResult": map[string]any{
				"status": "completed", "agentId": "aSync",
			},
		},
	)
	d := NewClaudeDriver(writeJSONL(t, rows), "sess-sync")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aSync")
	if n == nil || n.Status != AgentDone || n.EndedAt.IsZero() {
		t.Fatalf("sync terminal tool_result must complete existing node: %+v", n)
	}
}

func TestAgentTree_SynchronousFailedToolResultErrorsExistingAgent(t *testing.T) {
	rows := append(
		[]map[string]any{
			makeAgentSpawnRow("2026-06-10T19:21:00Z", "toolu_fail_sync", "Explore", "old failed task"),
			makeAgentResolveRow("2026-06-10T19:21:01Z", "toolu_fail_sync", "aFailSync"),
		},
		map[string]any{
			"type":      "user",
			"timestamp": "2026-06-10T19:31:00Z",
			"message": map[string]any{
				"role": "user",
				"content": []any{map[string]any{
					"type": "tool_result", "tool_use_id": "toolu_fail_sync", "is_error": true,
				}},
			},
			"toolUseResult": map[string]any{
				"status": "failed", "agentId": "aFailSync",
			},
		},
	)
	d := NewClaudeDriver(writeJSONL(t, rows), "sess-fail-sync")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aFailSync")
	if n == nil || n.Status != AgentError || n.EndedAt.IsZero() {
		t.Fatalf("sync failed tool_result must error existing node: %+v", n)
	}
}

// Claude Code 2.1.170 foreground Agent emits no async launch row: the first
// matching tool_result both assigns agentId and reports completed. Session 208
// contains exactly this shape.
func TestAgentTree_FirstResolveMayAlreadyBeTerminal(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-06-10T19:19:42Z", "toolu_direct", "general-purpose", "Bug fix: workspace portal 404"),
		{
			"type": "user", "timestamp": "2026-06-10T19:25:18Z",
			"message": map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": "toolu_direct", "content": "final answer",
			}}},
			"toolUseResult": map[string]any{"status": "completed", "agentId": "aDirect"},
		},
	}
	d := NewClaudeDriver(writeJSONL(t, rows), "sess-direct")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aDirect")
	if n == nil || n.Status != AgentDone || n.EndedAt.IsZero() {
		t.Fatalf("first terminal resolve must create a done node: %+v", n)
	}
}

// A task-notification whose <task-id> does not match any agent WE resolved
// from an "Agent" spawn must be ignored — real transcripts use the identical
// envelope for backgrounded Bash commands (schema note 5). It must not
// fabricate a phantom AgentNode.
func TestAgentTree_UnknownTaskIDIgnored(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_1", "general-purpose", "Real agent"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_1", "aReal"),
		// A background Bash command's completion — short id, unrelated tool_use.
		makeTaskNotificationQueueOpRow("2026-07-12T01:00:05Z", "bshortid1", "toolu_bash", "completed", `Background command "make build" finished`),
	}
	path := writeJSONL(t, rows)
	d := NewClaudeDriver(path, "sess-4")
	if err := d.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tree := d.AgentTree()
	if len(tree) != 1 {
		t.Fatalf("want exactly 1 real agent, no phantom node for the background job: got %d: %+v", len(tree), tree)
	}
	if tree[0].Status != AgentRunning {
		t.Errorf("unrelated notification must not affect the real agent's status: got %+v", tree[0])
	}
}

// A SendMessage to an already-done/failed agent resumes the same durable
// identity under a new causal attempt (schema note 6). The resumed completion
// must match the SendMessage tool-use ID; task-id alone is insufficient because
// late notifications for older attempts are legal.
func TestAgentTree_SendMessageResumesCompletedAgent(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_1", "general-purpose", "Flaky agent"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_1", "aFlaky"),
		makeTaskNotificationQueueOpRow("2026-07-12T01:05:00Z", "aFlaky", "toolu_1", "failed", `Agent "Flaky agent" failed: disconnected`),
		makeTaskNotificationUserRow("2026-07-12T01:05:00.05Z", "aFlaky", "toolu_1", "failed", `Agent "Flaky agent" failed: disconnected`),
		makeSendMessageRow("2026-07-12T01:06:00Z", "toolu_resume", "aFlaky"),
	}
	path := writeJSONL(t, rows)
	d := NewClaudeDriver(path, "sess-5")
	if err := d.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}

	n := findNode(d.AgentTree(), "aFlaky")
	if n == nil {
		t.Fatal("expected aFlaky node")
	}
	if n.Status != AgentRunning {
		t.Errorf("after SendMessage resume: got %q, want running", n.Status)
	}
	if !n.EndedAt.IsZero() {
		t.Error("EndedAt should be cleared on resume")
	}

	// It can then complete again, under a DIFFERENT tool-use-id than the
	// original spawn — must still resolve via task-id.
	rows2 := []map[string]any{
		makeTaskNotificationQueueOpRow("2026-07-12T01:40:00Z", "aFlaky", "toolu_resume", "completed", `Agent "Flaky agent" finished`),
		makeTaskNotificationUserRow("2026-07-12T01:40:00.05Z", "aFlaky", "toolu_resume", "completed", `Agent "Flaky agent" finished`),
	}
	appendJSONL(t, path, rows2)
	if err := d.Update(); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	n = findNode(d.AgentTree(), "aFlaky")
	if n.Status != AgentDone {
		t.Errorf("after resumed completion: got %q, want done", n.Status)
	}
}

// Cold replay reads the complete root stream before any child stream. The old
// reducer therefore applied the 12:00 resume first, then let the child's older
// 11:58 end_turn overwrite it back to done. Pin the real cross-stream ordering:
// the newer attempt remains running and its visible duration starts at resume.
func TestAgentTree_ColdReplayResumeDominatesOlderChildEndTurn(t *testing.T) {
	rootRows := []map[string]any{
		makeAgentSpawnRow("2026-07-13T11:50:00Z", "toolu_spawn", "general-purpose", "Import fidelity"),
		makeAgentResolveRow("2026-07-13T11:50:01Z", "toolu_spawn", "aResume"),
		makeTaskNotificationQueueOpRow("2026-07-13T11:58:37.100Z", "aResume", "toolu_spawn", "completed", "first activation done"),
		makeSendMessageRow("2026-07-13T12:00:03.548Z", "toolu_resume", "aResume"),
	}
	path := writeJSONL(t, rootRows)
	writeAgentTranscript(t, sessionDirFor(path), "aResume", []map[string]any{
		makeAgentUsageRow("2026-07-13T11:58:30Z", "aResume", "m-old", 10),
		makeEndTurnRow("2026-07-13T11:58:37.028Z"),
		{"type": "user", "timestamp": "2026-07-13T12:00:03.648Z", "message": map[string]any{"role": "user", "content": []any{}}},
		makeAgentUsageRow("2026-07-13T12:06:19.005Z", "aResume", "m-new", 20),
	})

	d := NewClaudeDriver(path, "sess-cold-resume")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aResume")
	if n == nil || n.Status != AgentRunning || !n.EndedAt.IsZero() {
		t.Fatalf("older child end_turn must not close resumed attempt: %+v", n)
	}
	wantActive := time.Date(2026, 7, 13, 12, 0, 3, 548_000_000, time.UTC)
	if !n.ActiveSince.Equal(wantActive) {
		t.Fatalf("active duration must restart at SendMessage: got %s want %s", n.ActiveSince, wantActive)
	}
}

// Claude delivers the same terminal notification through queue-operation and
// injected-user channels, and can redeliver it much later. The first accepted
// terminal time is the execution fact; delivery retries cannot manufacture a
// new afterglow or extend elapsed duration.
func TestAgentTree_DuplicateTerminalDoesNotMoveEndedAt(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-13T12:00:00Z", "toolu_dup", "general-purpose", "Dedup"),
		makeAgentResolveRow("2026-07-13T12:00:01Z", "toolu_dup", "aDup"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:23:27.879Z", "aDup", "toolu_dup", "completed", "done"),
		makeTaskNotificationUserRow("2026-07-13T12:23:27.900Z", "aDup", "toolu_dup", "completed", "done"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:56:08.133Z", "aDup", "toolu_dup", "completed", "redelivery"),
	}
	d := NewClaudeDriver(writeJSONL(t, rows), "sess-dup-terminal")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aDup")
	wantEnded := time.Date(2026, 7, 13, 12, 23, 27, 879_000_000, time.UTC)
	if n == nil || n.Status != AgentDone || !n.EndedAt.Equal(wantEnded) {
		t.Fatalf("duplicate delivery changed terminal fact: %+v want ended_at=%s", n, wantEnded)
	}
}

// Root notification and child end_turn are two completion channels for one
// attempt. Even if their clocks/order make child end_turn appear slightly later,
// it must dedupe against the original attempt rather than inventing a resume and
// moving EndedAt (which would also re-trigger the UI afterglow).
func TestAgentTree_LaterChildEndTurnDoesNotInventResume(t *testing.T) {
	rootRows := []map[string]any{
		makeAgentSpawnRow("2026-07-13T12:00:00Z", "toolu_finish", "general-purpose", "Finish once"),
		makeAgentResolveRow("2026-07-13T12:00:01Z", "toolu_finish", "aFinish"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:10:00Z", "aFinish", "toolu_finish", "completed", "done"),
	}
	path := writeJSONL(t, rootRows)
	writeAgentTranscript(t, sessionDirFor(path), "aFinish", []map[string]any{
		makeEndTurnRow("2026-07-13T12:10:00.500Z"),
	})
	d := NewClaudeDriver(path, "sess-finish-dedupe")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aFinish")
	wantStarted := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	wantEnded := time.Date(2026, 7, 13, 12, 10, 0, 0, time.UTC)
	if n == nil || n.Status != AgentDone || !n.ActiveSince.Equal(wantStarted) || !n.EndedAt.Equal(wantEnded) {
		t.Fatalf("duplicate child terminal invented an activation: %+v", n)
	}
}

// A cold driver and an incrementally advanced driver must converge on the same
// current attempt. This pins replay-order independence across root and child files.
func TestAgentTree_ColdAndIncrementalLifecycleConverge(t *testing.T) {
	initialRoot := []map[string]any{
		makeAgentSpawnRow("2026-07-13T12:00:00Z", "toolu_first", "general-purpose", "Converge"),
		makeAgentResolveRow("2026-07-13T12:00:01Z", "toolu_first", "aConverge"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:01:00Z", "aConverge", "toolu_first", "completed", "first done"),
	}
	path := writeJSONL(t, initialRoot)
	sessionDir := sessionDirFor(path)
	writeAgentTranscript(t, sessionDir, "aConverge", []map[string]any{makeEndTurnRow("2026-07-13T12:00:59Z")})

	incremental := NewClaudeDriver(path, "sess-incremental")
	if err := incremental.Update(); err != nil {
		t.Fatal(err)
	}
	appendJSONL(t, path, []map[string]any{makeSendMessageRow("2026-07-13T12:02:00Z", "toolu_second", "aConverge")})
	appendAgentTranscript(t, sessionDir, "aConverge", []map[string]any{
		makeAgentUsageRow("2026-07-13T12:02:01Z", "aConverge", "m-second", 12),
		makeEndTurnRow("2026-07-13T12:03:00Z"),
	})
	if err := incremental.Update(); err != nil {
		t.Fatal(err)
	}

	cold := NewClaudeDriver(path, "sess-cold")
	if err := cold.Update(); err != nil {
		t.Fatal(err)
	}
	a := findNode(incremental.AgentTree(), "aConverge")
	b := findNode(cold.AgentTree(), "aConverge")
	if a == nil || b == nil || a.Status != b.Status || !a.ActiveSince.Equal(b.ActiveSince) || !a.EndedAt.Equal(b.EndedAt) {
		t.Fatalf("cold/incremental lifecycle diverged: incremental=%+v cold=%+v", a, b)
	}
}

// A delayed terminal for attempt A may arrive after attempt B was opened.
// Correlation by stable agent ID alone would kill B; tool-use attempt identity
// makes the late A delivery an idempotent historical event.
func TestAgentTree_LateOldAttemptTerminalCannotKillResume(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-13T12:00:00Z", "toolu_A", "general-purpose", "Resume race"),
		makeAgentResolveRow("2026-07-13T12:00:01Z", "toolu_A", "aRace"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:01:00Z", "aRace", "toolu_A", "completed", "A done"),
		makeSendMessageRow("2026-07-13T12:02:00Z", "toolu_B", "aRace"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:30:00Z", "aRace", "toolu_A", "completed", "late A duplicate"),
	}
	d := NewClaudeDriver(writeJSONL(t, rows), "sess-late-old")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aRace")
	if n == nil || n.Status != AgentRunning || !n.EndedAt.IsZero() {
		t.Fatalf("late attempt A terminal killed attempt B: %+v", n)
	}
}

func TestAgentTree_InstantResumeCompletionClosesMatchingAttempt(t *testing.T) {
	rows := []map[string]any{
		makeAgentSpawnRow("2026-07-13T12:00:00Z", "toolu_initial", "general-purpose", "Instant resume"),
		makeAgentResolveRow("2026-07-13T12:00:01Z", "toolu_initial", "aInstant"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:01:00Z", "aInstant", "toolu_initial", "completed", "initial done"),
		makeSendMessageRow("2026-07-13T12:02:00Z", "toolu_instant", "aInstant"),
		makeTaskNotificationQueueOpRow("2026-07-13T12:02:00Z", "aInstant", "toolu_instant", "completed", "instant done"),
	}
	d := NewClaudeDriver(writeJSONL(t, rows), "sess-instant-resume")
	if err := d.Update(); err != nil {
		t.Fatal(err)
	}
	n := findNode(d.AgentTree(), "aInstant")
	want := time.Date(2026, 7, 13, 12, 2, 0, 0, time.UTC)
	if n == nil || n.Status != AgentDone || !n.ActiveSince.Equal(want) || !n.EndedAt.Equal(want) {
		t.Fatalf("matching instant terminal must close resumed attempt: %+v", n)
	}
}

// --- 4. depth>=2 nesting --------------------------------------------------

// A subagent spawning its OWN child is expressed by the same Agent
// tool_use/tool_result pattern recurring INSIDE that subagent's own
// transcript file (schema note 3) — not by any inline isSidechain chain in
// the root file. This is the load-bearing case the design doc's evidence-led
// gate was about.
func TestAgentTree_DepthTwoNesting(t *testing.T) {
	rootRows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_parent", "general-purpose", "Parent agent"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_parent", "aParent"),
	}
	path := writeJSONL(t, rootRows)
	sessionDir := sessionDirFor(path)

	// The parent's OWN transcript spawns a grandchild — flat under the SAME
	// <sessionDir>/subagents/ directory as the parent itself (confirmed:
	// depth does NOT nest the directory).
	childRows := []map[string]any{
		makeNestedAgentSpawnRow("2026-07-12T01:01:00Z", "aParent", "toolu_child", "general-purpose", "Grandchild agent"),
		makeNestedAgentResolveRow("2026-07-12T01:01:01Z", "aParent", "toolu_child", "aChild"),
	}
	writeAgentTranscript(t, sessionDir, "aParent", childRows)

	d := NewClaudeDriver(path, "sess-6")
	if err := d.Update(); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tree := d.AgentTree()
	if len(tree) != 2 {
		t.Fatalf("want 2 nodes (parent+child), got %d: %+v", len(tree), tree)
	}
	parent := findNode(tree, "aParent")
	child := findNode(tree, "aChild")
	if parent == nil || child == nil {
		t.Fatalf("expected both aParent and aChild: %+v", tree)
	}
	if parent.ParentID != "" || parent.Depth != 1 {
		t.Errorf("parent identity: got %+v", parent)
	}
	if child.ParentID != "aParent" || child.Depth != 2 {
		t.Errorf("child must be linked to its spawning agent at depth 2: got %+v", child)
	}
	if child.SubagentType != "general-purpose" || child.Description != "Grandchild agent" {
		t.Errorf("nested spawn fields must be verbatim too: got %+v", child)
	}
}

// --- 5. in-progress / truncated read --------------------------------------

// A half-written trailing line (no terminating '\n' — the writer is
// mid-flush) must NOT be consumed: the agent stays "running" with whatever
// was read so far, and the rest is picked up on the next Update() once the
// line completes — exactly JSONLReader's existing incremental-offset
// contract (jsonl_reader.go), reused here rather than re-implemented.
func TestAgentTree_InProgressTruncatedRead(t *testing.T) {
	rootRows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_1", "general-purpose", "Streaming agent"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_1", "aStream"),
	}
	path := writeJSONL(t, rootRows)
	sessionDir := sessionDirFor(path)

	dir := filepath.Join(sessionDir, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(dir, "agent-aStream.jsonl")

	firstRow, _ := json.Marshal(makeAgentUsageRow("2026-07-12T01:01:00Z", "aStream", "m1", 111))
	secondRowFull, _ := json.Marshal(makeAgentUsageRow("2026-07-12T01:01:05Z", "aStream", "m2", 222))
	// Simulate a writer mid-flush: complete first row + newline, then a
	// PARTIAL second row with no trailing newline.
	partial := secondRowFull[:len(secondRowFull)/2]
	if err := os.WriteFile(childPath, append(append(firstRow, '\n'), partial...), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewClaudeDriver(path, "sess-7")
	if err := d.Update(); err != nil {
		t.Fatalf("first Update: %v", err)
	}
	n := findNode(d.AgentTree(), "aStream")
	if n == nil {
		t.Fatal("expected aStream node")
	}
	if n.Status != AgentRunning {
		t.Errorf("still running (no notification): got %q", n.Status)
	}
	if n.TokensDown != 111 {
		t.Fatalf("TokensDown after partial read: got %d, want 111 (the half-written row must not be consumed)", n.TokensDown)
	}

	// The writer finishes flushing the line; append the missing tail + newline.
	remainder := append(secondRowFull[len(partial):], '\n')
	f, err := os.OpenFile(childPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(remainder); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := d.Update(); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	n = findNode(d.AgentTree(), "aStream")
	if n.TokensDown != 111+222 {
		t.Fatalf("TokensDown after completed line: got %d, want %d", n.TokensDown, 111+222)
	}
}

// --- bonus: live TokensDown accumulates incrementally, offset-cached -----

// This exercises the "复用现有 offset/缓存,禁另起全量读" mandate directly:
// a second Update() call must add only the NEW usage rows, never re-sum the
// whole file (which would double-count).
func TestAgentTree_TokensDownIncrementalNoDoubleCount(t *testing.T) {
	rootRows := []map[string]any{
		makeAgentSpawnRow("2026-07-12T01:00:00Z", "toolu_1", "general-purpose", "Token agent"),
		makeAgentResolveRow("2026-07-12T01:00:01Z", "toolu_1", "aTok"),
	}
	path := writeJSONL(t, rootRows)
	sessionDir := sessionDirFor(path)
	writeAgentTranscript(t, sessionDir, "aTok", []map[string]any{
		makeAgentUsageRow("2026-07-12T01:01:00Z", "aTok", "m1", 100),
	})

	d := NewClaudeDriver(path, "sess-8")
	if err := d.Update(); err != nil {
		t.Fatalf("first Update: %v", err)
	}
	if n := findNode(d.AgentTree(), "aTok"); n == nil || n.TokensDown != 100 {
		t.Fatalf("after first read: got %+v, want TokensDown=100", n)
	}

	appendAgentTranscript(t, sessionDir, "aTok", []map[string]any{
		makeAgentUsageRow("2026-07-12T01:02:00Z", "aTok", "m2", 50),
	})
	if err := d.Update(); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if n := findNode(d.AgentTree(), "aTok"); n == nil || n.TokensDown != 150 {
		t.Fatalf("after incremental read: got %+v, want TokensDown=150 (not re-summed from scratch)", n)
	}
}

func appendAgentTranscript(t *testing.T, sessionDir, agentID string, rows []map[string]any) {
	t.Helper()
	path := filepath.Join(sessionDir, "subagents", "agent-"+agentID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}

// appendJSONL appends rows to an existing JSONL file (root session file).
func appendJSONL(t *testing.T, path string, rows []map[string]any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}
