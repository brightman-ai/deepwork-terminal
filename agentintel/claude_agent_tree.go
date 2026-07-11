package agentintel

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ============================================================================
// Schema notes — Claude Code subagent ("Agent" tool) spawn/completion encoding
// ============================================================================
//
// Evidence: a real, live coordinator session's JSONL transcript (22MB,
// snapshotted before study — see ws-ux-r4 agentintel task) plus its sibling
// `<sessionId>/subagents/` directory on disk. [Ref: agentintel r4 CHG,
// 2026-07-12]
//
// This is the CURRENT (2026) Claude Code CLI transcript shape. It is NOT the
// older documented shape some designs assume (a "Task" tool + inline
// `isSidechain:true` rows carrying the subagent's own turns in the SAME
// file — that shape does not appear anywhere in the sample: every row in the
// root session file has isSidechain:false). What follows is written strictly
// against what was actually observed on disk, not against the old assumption:
//
//  1. Spawn: an `assistant` row's message.content contains a tool_use block
//     named "Agent" (NOT "Task"):
//       {"type":"tool_use","id":"toolu_XXXX","name":"Agent","input":
//        {"subagent_type":"...","description":"...","model":"...",
//         "run_in_background":true,"prompt":"..."}}
//     subagent_type/description are used VERBATIM below — never rewritten;
//     truncation for display is a UI concern only.
//
//  2. Resolution: the VERY NEXT `user` row carries the matching tool_result
//     (message.content[].tool_use_id == the toolu_ above), and the row's
//     top-level `toolUseResult` field is:
//       {"isAsync":true,"status":"async_launched","agentId":"<agentId>",
//        "description":...,"resolvedModel":...}
//     `agentId` (e.g. "a86246568ba6c4d5a") is the durable ID for this
//     subagent — NOT the tool_use id, which only correlates the spawn.
//     All spawns sampled (20 at depth 1, 6 more at depth 2) were async
//     (run_in_background:true); no synchronous/foreground Agent SUCCESS was
//     observed anywhere in the transcript. That success path (no agentId in
//     the result — result would presumably carry the full answer inline
//     instead) is therefore NOT handled here. This is a real evidence gap,
//     not an oversight: inventing its shape would violate evidence-led
//     schema work. See the task's final report for this callout.
//
//     SPAWN FAILURE, however, IS handled (Witness-surfaced real scenario):
//     the Agent tool_use can error out before any agent exists — the
//     resolve row then carries an error tool_result (block is_error:true
//     and/or a plain error-string toolUseResult) and NO agentId. Such a
//     resolve makes an immediate AgentError node keyed by the spawn's
//     tool_use id (the only durable handle). See scanUserRow.
//
//  3. Every spawned agent's OWN full transcript is a real, separately
//     durable JSONL file at a deterministic, FLAT path — confirmed to hold
//     for depth-1 AND depth-2 alike; nesting depth does NOT nest the
//     directory:
//       <sessionDir>/subagents/agent-<agentId>.jsonl
//     (there is also a tiny `agent-<agentId>.meta.json` sidecar holding
//     {agentType, description, toolUseId, spawnDepth} — it is redundant
//     with what we already learn from the spawning tool_use/tool_result
//     pair, so it is deliberately NOT read here). sessionDir is the root
//     jsonl path with its ".jsonl" extension stripped. This file grows
//     incrementally exactly like the root session file; its rows carry
//     "isSidechain":true and an "agentId" field matching the file's own
//     agent (a harmless confirmation, not relied upon — the file name
//     already tells us whose transcript it is).
//
//     Depth>=2 nesting is expressed by the SAME spawn/resolve pattern
//     recurring one level down: an "Agent" tool_use INSIDE a subagent's own
//     transcript, resolved by a tool_result in that SAME file. Evidence:
//     agent-<X>.jsonl for a depth-1 agent itself contained 5 further Agent
//     tool_use blocks; the resulting 5 agentIds' own transcripts
//     (spawnDepth:2 per their .meta.json) sit in the exact same FLAT
//     `subagents/` directory as their depth-1 siblings — not nested under
//     agent-<X>/subagents/. So the whole tree, at any depth, is reachable
//     by walking a flat directory keyed by agentId; only the discovery
//     (which tool_use produced which agentId, in which file) is
//     recursive/hierarchical.
//
//  4. Completion/failure signal — the most surprising part: it is delivered
//     ONLY to the ROOT session's transcript, never inside a subagent's own
//     file (confirmed: zero <task-notification> occurrences in any sampled
//     subagent transcript, including one that itself spawned children and
//     was clearly still waiting on them at snapshot time — it had literally
//     issued a throwaway "echo/date" Bash call captioned "Trivial
//     checkpoint call to allow any pending notification to surface" and
//     still received nothing in its OWN file). It shows up twice,
//     redundantly, in the ROOT file only:
//       a. {"type":"queue-operation","operation":"enqueue",...,
//           "content":"<task-notification>...</task-notification>"}
//          (content is a plain string), shortly followed by
//       b. {"type":"user",...,"message":{"role":"user",
//           "content":"<task-notification>...</task-notification>"}}
//          — note message.content is a PLAIN STRING here, not the usual
//          content-block array the rest of this package assumes.
//     Body (XML-ish text, both channels identical):
//       <task-notification><task-id>AGENT_ID</task-id>
//       <tool-use-id>...</tool-use-id><output-file>...</output-file>
//       <status>completed|failed</status><summary>...</summary>
//       <note>...</note><result>...full markdown report...</result>
//       <usage><subagent_tokens>N</subagent_tokens>...</usage>
//       </task-notification>
//     Only "completed" and "failed" were observed for <status>; they map
//     1:1 to AgentDone / AgentError. (The notification's <usage> block
//     carries a final authoritative token count, but it is only available
//     at completion — TokensDown below is instead live-accumulated from the
//     agent's own transcript so the number is present and updates the same
//     way whether the agent is running or done.)
//
//  5. GOTCHA — task-notification is NOT exclusive to Agent spawns: a
//     backgrounded Bash command (run_in_background on the Bash tool) is
//     notified through the EXACT SAME <task-notification> envelope, with
//     <task-id> set to a short backgroundTaskId (e.g. "bqiw0oz9b") instead
//     of an agentId. The correct discriminator is: does <task-id> match a
//     KNOWN agent ID we ourselves resolved from an "Agent" spawn? If not,
//     the notification belongs to something else and is ignored.
//
//  6. Resume: an agent reported "completed" is not necessarily gone for
//     good — SendMessage (tool_use name=="SendMessage", input
//     {"to":"<agentId>", ...}) resumes it, and it can complete (or fail)
//     again later under a NEW tool-use-id (a resume's notification
//     <tool-use-id> is the SendMessage call's own id, not the original
//     spawn's — so status MUST be correlated by <task-id> only, never by
//     tool-use-id). Evidence: one sampled agentId failed (API disconnect),
//     was resumed via SendMessage, then completed ~40 minutes later. A
//     matching SendMessage flips a done/error node back to running.
//
// Net: multi-level nesting IS fully recoverable from on-disk JSONL — just
// not via an inline isSidechain chain. It requires walking a side file per
// agent (discovered from the parent's own tool_use/tool_result pair), all of
// which live flat under one <sessionDir>/subagents/ directory regardless of
// depth, plus reading completion signals ONLY from the root file.

// AgentNodeStatus is the 3-state lifecycle of one spawned subagent.
type AgentNodeStatus string

const (
	AgentRunning AgentNodeStatus = "running"
	AgentDone    AgentNodeStatus = "done"
	AgentError   AgentNodeStatus = "error"
)

// AgentNode is one node in the subagent spawn tree recovered from a Claude
// Code JSONL transcript. It is a flat (ParentID-linked) representation of an
// unlimited-depth tree — see the schema notes above for how depth/parentage
// is discovered.
type AgentNode struct {
	ID           string // agentId assigned by the harness at spawn time
	ParentID     string // spawning agent's ID; "" when spawned directly by the root session
	Depth        int    // 1 = direct child of the root session; increments per nesting level

	SubagentType string // input.subagent_type, verbatim — never rewritten
	Description  string // input.description, verbatim — never rewritten/truncated

	Status AgentNodeStatus

	StartedAt time.Time // timestamp of the spawning tool_use row
	EndedAt   time.Time // timestamp of the completion/failure notification; zero while running

	TokensDown int // output tokens accumulated from the agent's own transcript (live, best-effort; 0 until its file has been read at least once)
}

// agentSpawnPending is a seen-but-not-yet-resolved "Agent" tool_use: we know
// what was asked for but not yet the agentId the harness assigned it (that
// arrives one row later, in the matching tool_result).
type agentSpawnPending struct {
	subagentType string
	description  string
	startedAt    time.Time
}

// claudeAgentTree is the incremental subagent-tree parsing state for one
// ClaudeDriver. It is a value (not a pointer) embedded in ClaudeDriver;
// newClaudeAgentTree must be used to get one with its maps initialized.
type claudeAgentTree struct {
	sessionDir string // root jsonl path with its extension stripped — see schema note 3

	order   []string // agent IDs, in discovery order (stable AgentTree() output)
	nodes   map[string]*AgentNode
	pending map[string]agentSpawnPending // by tool_use id, awaiting the resolving tool_result

	readers map[string]*JSONLReader      // by agent ID — that agent's OWN transcript, incremental (offset-cached)
	usage   map[string]*UsageAccumulator // by agent ID — dedup'd token totals from that transcript
}

func newClaudeAgentTree(jsonlPath string) claudeAgentTree {
	dir := strings.TrimSuffix(jsonlPath, filepath.Ext(jsonlPath))
	return claudeAgentTree{
		sessionDir: dir,
		nodes:      make(map[string]*AgentNode),
		pending:    make(map[string]agentSpawnPending),
		readers:    make(map[string]*JSONLReader),
		usage:      make(map[string]*UsageAccumulator),
	}
}

var (
	taskNotificationTagRe = regexp.MustCompile(`<task-id>([^<]*)</task-id>`)
	taskStatusTagRe       = regexp.MustCompile(`<status>([^<]*)</status>`)
)

// scanRow inspects one JSONL row for agent-tree-relevant content: spawns,
// resolutions, resumes, and completion notifications. ownerID/ownerDepth
// identify whose transcript this row came from ("" / 0 for the root
// session's own file; an agent's ID / that agent's Depth when called from
// advanceAgentReaders while reading a subagent's own transcript).
func (t *claudeAgentTree) scanRow(row map[string]any, ownerID string, ownerDepth int) {
	rowType, _ := row["type"].(string)
	switch rowType {
	case "assistant":
		t.scanAssistantRow(row)
	case "user":
		t.scanUserRow(row, ownerID, ownerDepth)
	case "queue-operation":
		// Channel (a) from schema note 4: same notification, delivered as
		// scheduling metadata slightly before the injected user row (b).
		if s, ok := row["content"].(string); ok {
			t.applyTaskNotification(s, parseTime(row))
		}
	}
}

// scanAssistantRow handles two tool_use kinds: "Agent" (a new spawn, parked
// in `pending` until the next row resolves its agentId) and "SendMessage"
// (a resume of an already-known agent — see schema note 6).
func (t *claudeAgentTree) scanAssistantRow(row map[string]any) {
	msg, _ := row["message"].(map[string]any)
	content, _ := msg["content"].([]any)
	for _, it := range content {
		block, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if bt, _ := block["type"].(string); bt != "tool_use" {
			continue
		}
		name, _ := block["name"].(string)
		id, _ := block["id"].(string)
		input, _ := block["input"].(map[string]any)

		switch name {
		case "Agent":
			if id == "" {
				continue
			}
			subagentType, _ := input["subagent_type"].(string)
			description, _ := input["description"].(string)
			t.pending[id] = agentSpawnPending{
				subagentType: subagentType,
				description:  description,
				startedAt:    parseTime(row),
			}
		case "SendMessage":
			to, _ := input["to"].(string)
			if node, ok := t.nodes[to]; ok && node.Status != AgentRunning {
				node.Status = AgentRunning
				node.EndedAt = time.Time{}
			}
		}
	}
}

// scanUserRow handles the two things a "user" row can carry: a completion
// notification injected as a plain string (schema note 4, channel b), or a
// tool_result RESOLVING a pending "Agent" spawn — successfully (toolUseResult
// carries the durable agentId, schema note 2) or as a SPAWN FAILURE.
//
// Spawn failure (real historical scenario the Witness surfaced): the Agent
// tool_use errors out before any agent exists — the tool_result carries an
// error (block is_error:true and/or a plain error-string toolUseResult) and
// NO agentId anywhere. Without handling this, the spawn would sit in
// `pending` forever and the dock would have no honest terminal state for it.
// The rule is deliberately narrow — only the RESOLVE ROW ITSELF signalling
// failure counts: a matched pending spawn whose result has no agentId and
// does not claim "async_launched" becomes an AgentError node immediately
// (EndedAt = this row's time; ID = the spawn's tool_use id, the only durable
// handle since the harness never assigned an agentId). There is deliberately
// NO turn-boundary sweep clearing running agents — an async agent
// legitimately outlives the turn that spawned it (observed: completion
// notifications landing seconds after turn end).
func (t *claudeAgentTree) scanUserRow(row map[string]any, ownerID string, ownerDepth int) {
	msg, _ := row["message"].(map[string]any)

	if s, ok := msg["content"].(string); ok {
		t.applyTaskNotification(s, parseTime(row))
		return
	}

	tur, _ := row["toolUseResult"].(map[string]any)
	agentID, _ := tur["agentId"].(string)
	turStatus, _ := tur["status"].(string)

	content, _ := msg["content"].([]any)
	for _, it := range content {
		block, ok := it.(map[string]any)
		if !ok {
			continue
		}
		toolUseID, _ := block["tool_use_id"].(string)
		if toolUseID == "" {
			continue
		}
		pending, ok := t.pending[toolUseID]
		if !ok {
			continue // some other tool's result (Bash/Read/…) — not an Agent resolve
		}

		// ── Success: the harness assigned a durable agentId (schema note 2). ──
		if agentID != "" {
			delete(t.pending, toolUseID)
			if _, exists := t.nodes[agentID]; exists {
				return // already resolved — stay idempotent on a re-read
			}
			node := &AgentNode{
				ID:           agentID,
				ParentID:     ownerID,
				Depth:        ownerDepth + 1,
				SubagentType: pending.subagentType,
				Description:  pending.description,
				Status:       AgentRunning,
				StartedAt:    pending.startedAt,
			}
			t.nodes[agentID] = node
			t.order = append(t.order, agentID)
			return
		}

		// ── Failure vs not-enough-signal. is_error:true on the result block is
		// an explicit failure; a result that still CLAIMS "async_launched"
		// without an error flag is malformed-but-optimistic — leave the spawn
		// pending rather than guess (a later row may still resolve it). ──
		isErr, _ := block["is_error"].(bool)
		if !isErr && turStatus == "async_launched" {
			return
		}

		delete(t.pending, toolUseID)
		if _, exists := t.nodes[toolUseID]; exists {
			return // failure node already created — idempotent on a re-read
		}
		node := &AgentNode{
			ID:           toolUseID, // no agentId was ever assigned — the spawn's tool_use id is the only durable handle
			ParentID:     ownerID,
			Depth:        ownerDepth + 1,
			SubagentType: pending.subagentType,
			Description:  pending.description,
			Status:       AgentError,
			StartedAt:    pending.startedAt,
			EndedAt:      parseTime(row),
		}
		t.nodes[toolUseID] = node
		t.order = append(t.order, toolUseID)
		return
	}
}

// applyTaskNotification parses a <task-notification>...</task-notification>
// body and, if its <task-id> matches an agent we ourselves resolved from an
// "Agent" tool spawn, updates that node's terminal status. A <task-id> that
// does NOT match a known agent belongs to something else (observed:
// backgrounded Bash commands use the identical envelope — schema note 5) and
// is silently ignored — that is the correct, evidence-backed behavior, not a
// missed case.
func (t *claudeAgentTree) applyTaskNotification(text string, at time.Time) {
	if !strings.Contains(text, "<task-notification>") {
		return
	}
	idMatch := taskNotificationTagRe.FindStringSubmatch(text)
	if idMatch == nil {
		return
	}
	node, ok := t.nodes[idMatch[1]]
	if !ok {
		return
	}
	statusMatch := taskStatusTagRe.FindStringSubmatch(text)
	if statusMatch == nil {
		return
	}
	switch statusMatch[1] {
	case "completed":
		node.Status = AgentDone
	case "failed":
		node.Status = AgentError
	default:
		return // unrecognized status — leave the node untouched rather than guess
	}
	node.EndedAt = at
}

// advanceAgentReaders incrementally reads every known agent's OWN transcript
// file, discovering deeper nesting (further "Agent" tool_use spawns inside
// it — schema note 3) and accumulating its live output-token usage. Reuses
// one *JSONLReader per agent, offset-cached exactly like the root session
// reader — never re-reads a file from scratch. A file that does not exist
// yet (the agent was just spawned; its transcript hasn't been created on
// disk) or has nothing new produces an error from the underlying os.Open,
// which is expected and silently ignored; the same reader retries on the
// next Update() call.
func (cd *ClaudeDriver) advanceAgentReaders() {
	t := &cd.agentTree
	for i := 0; i < len(t.order); i++ { // index-based: len() re-checked each iteration picks up newly discovered agents within this same call
		id := t.order[i]
		node := t.nodes[id]

		r, ok := t.readers[id]
		if !ok {
			path := filepath.Join(t.sessionDir, "subagents", "agent-"+id+".jsonl")
			r = NewJSONLReader(path)
			t.readers[id] = r
		}
		ua, ok := t.usage[id]
		if !ok {
			ua = NewUsageAccumulator()
			t.usage[id] = ua
		}

		_ = r.ReadNewFunc(func(row map[string]any) bool {
			t.scanRow(row, id, node.Depth)
			accumulateAgentUsage(row, id, ua)
			node.TokensDown = ua.Totals.OutputTokens
			return true
		})
	}
}

// accumulateAgentUsage extracts and dedups one assistant row's usage block
// into ua. Mirrors ClaudeDriver.Update's root-file usage handling
// (claude_driver.go) in miniature — keyed by agent ID instead of session ID,
// and only OutputTokens is read out (TokensDown), since that is the only
// figure the agent-tree dock needs.
func accumulateAgentUsage(row map[string]any, agentID string, ua *UsageAccumulator) {
	if t, _ := row["type"].(string); t != "assistant" {
		return
	}
	msg, ok := row["message"].(map[string]any)
	if !ok {
		return
	}
	msgID, _ := msg["id"].(string)
	usageRaw, ok := msg["usage"].(map[string]any)
	if !ok || msgID == "" {
		return
	}
	current := UsageTotals{
		InputTokens:       intFromAny(usageRaw["input_tokens"]),
		OutputTokens:      intFromAny(usageRaw["output_tokens"]),
		CacheReadTokens:   intFromAny(usageRaw["cache_read_input_tokens"]),
		CacheCreateTokens: intFromAny(usageRaw["cache_creation_input_tokens"]),
	}
	current.TotalTokens = current.InputTokens + current.OutputTokens +
		current.CacheReadTokens + current.CacheCreateTokens
	ua.Ingest(CountedUsageKey{SessionID: agentID, MessageID: msgID}, current)
}
