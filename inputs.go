package terminal

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// inputsCap bounds how many prompts we return — newest-first, so older prompts
// fall off the tail. The drawer's input tab is a quick-reuse surface, not an
// archive, so a couple hundred is plenty and keeps the JSONL scan cheap.
const inputsCap = 200

// inputItem is one HUMAN prompt extracted from a claude/codex transcript. Only
// prompts the user actually typed are surfaced — assistant turns, tool results,
// synthetic command echoes and meta/sidechain rows are all excluded upstream.
type inputItem struct {
	Text        string `json:"text"`
	TsMs        int64  `json:"tsMs"`            // prompt timestamp (best-effort; 0 if unknown)
	Source      string `json:"source"`          // "claude" | "codex"
	CWD         string `json:"cwd"`             // originating working dir
	Project     string `json:"project"`         // basename(cwd) — a short, human label
	SessionName string `json:"sessionName,omitempty"` // transcript session id, when known
}

// inputsResponse is the payload of GET /inputs.
type inputsResponse struct {
	Items []inputItem `json:"items"`
}

// handleInputs handles GET /inputs — the cross-session "输入" (prompts) feed.
//
// It scans every claude transcript (~/.claude/projects/**/*.jsonl) and every
// codex rollout (~/.codex/sessions/**/*.jsonl), extracts the user's own prompts,
// merges them newest-first, dedupes exact repeats and caps the result. A missing
// transcript store, a malformed line, or an unreadable file never fails the
// request — it just yields fewer (or zero) items. Empty is a valid answer.
func (s *Server) handleInputs(w http.ResponseWriter, r *http.Request) {
	pl := agentintel.NewProjectLocator()

	var items []inputItem
	items = append(items, collectClaudeInputs(pl)...)
	items = append(items, collectCodexInputs(pl)...)

	// Newest first. Items with no timestamp sort last (stable) rather than first.
	sort.SliceStable(items, func(i, j int) bool { return items[i].TsMs > items[j].TsMs })

	items = dedupeInputs(items)
	if len(items) > inputsCap {
		items = items[:inputsCap]
	}
	writeJSON(w, http.StatusOK, inputsResponse{Items: items})
}

// dedupeInputs drops consecutive *and* non-consecutive exact-text repeats,
// keeping the first (newest, since the slice is already newest-first) occurrence.
// Re-sending the same prompt across sessions is common; the drawer shows it once.
func dedupeInputs(in []inputItem) []inputItem {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, it := range in {
		if _, dup := seen[it.Text]; dup {
			continue
		}
		seen[it.Text] = struct{}{}
		out = append(out, it)
	}
	return out
}

// collectClaudeInputs walks every claude transcript and extracts human prompts.
// We read all files but stop a file early once we have plenty of items overall —
// transcripts are newest-first, so the first files yield the freshest prompts.
func collectClaudeInputs(pl *agentintel.ProjectLocator) []inputItem {
	var out []inputItem
	for _, path := range pl.ClaudeAllSessionFiles() {
		reader := agentintel.NewJSONLReader(path)
		_ = reader.ReadNewFunc(func(row map[string]any) bool {
			it, ok := claudeRowToInput(row)
			if ok {
				out = append(out, it)
			}
			return true
		})
		if len(out) >= inputsCap*4 {
			break // already far more than we'll keep; stop scanning older files
		}
	}
	return out
}

// claudeRowToInput extracts a human prompt from one claude JSONL row, or reports
// ok=false if the row is anything other than a genuine user-typed prompt.
//
// Excluded: non-"user" rows (assistant), meta/sidechain rows (injected context),
// non-"external" userType (synthetic), tool_result blocks (tool output fed back to
// the model), and slash-command echoes (<command-name>/<local-command-stdout>/…).
func claudeRowToInput(row map[string]any) (inputItem, bool) {
	if t, _ := row["type"].(string); t != "user" {
		return inputItem{}, false
	}
	if b, _ := row["isMeta"].(bool); b {
		return inputItem{}, false
	}
	if b, _ := row["isSidechain"].(bool); b {
		return inputItem{}, false
	}
	// Claude tags real human turns as userType "external"; anything else (e.g. a
	// tool-driven user row) is synthetic. Absent field → treat as human (older logs).
	if ut, ok := row["userType"].(string); ok && ut != "external" {
		return inputItem{}, false
	}

	msg, ok := row["message"].(map[string]any)
	if !ok {
		return inputItem{}, false
	}
	text := extractClaudeText(msg["content"])
	text = strings.TrimSpace(text)
	if text == "" || isSyntheticPrompt(text) {
		return inputItem{}, false
	}

	cwd, _ := row["cwd"].(string)
	return inputItem{
		Text:        text,
		TsMs:        parseTimestampMs(row["timestamp"]),
		Source:      "claude",
		CWD:         cwd,
		Project:     projectLabel(cwd),
		SessionName: stringField(row, "sessionId"),
	}, true
}

// extractClaudeText pulls the human-visible text out of a claude message content,
// which is either a plain string or an array of content blocks. Any tool_result
// block disqualifies the row (it is model-bound tool output, not a human prompt),
// so we return "" to signal "not a prompt". Text blocks are concatenated.
func extractClaudeText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, blk := range c {
			m, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "tool_result":
				return "" // tool output, not a prompt — reject the whole row
			case "text":
				if t, ok := m["text"].(string); ok {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(t)
				}
			}
		}
		return b.String()
	}
	return ""
}

// collectCodexInputs walks every codex rollout and extracts human prompts. Codex
// emits a clean "user_message" event per human turn (the response_item copy is
// polluted with injected AGENTS.md context), so user_message is the SSOT here.
// The session's cwd comes from the rollout's session_meta header.
func collectCodexInputs(pl *agentintel.ProjectLocator) []inputItem {
	var out []inputItem
	for _, path := range pl.CodexSessionFiles() {
		reader := agentintel.NewJSONLReader(path)
		cwd := ""
		sessionID := ""
		_ = reader.ReadNewFunc(func(row map[string]any) bool {
			rowType, _ := row["type"].(string)
			payload, _ := row["payload"].(map[string]any)
			if payload == nil {
				return true
			}
			switch rowType {
			case "session_meta":
				if c, ok := payload["cwd"].(string); ok && c != "" {
					cwd = c
				}
				if sid, ok := payload["session_id"].(string); ok && sid != "" {
					sessionID = sid
				}
			case "event_msg":
				if pt, _ := payload["type"].(string); pt != "user_message" {
					return true
				}
				text := strings.TrimSpace(stringField(payload, "message"))
				if text == "" || isSyntheticPrompt(text) {
					return true
				}
				out = append(out, inputItem{
					Text:        text,
					TsMs:        parseTimestampMs(row["timestamp"]),
					Source:      "codex",
					CWD:         cwd,
					Project:     projectLabel(cwd),
					SessionName: sessionID,
				})
			}
			return true
		})
		if len(out) >= inputsCap*4 {
			break
		}
	}
	return out
}

// isSyntheticPrompt rejects machine-generated "prompts": slash-command echoes,
// injected instruction headers, interrupt markers, and tool/task plumbing tags.
// These carry the markup claude/codex insert around tool and command plumbing —
// never something a human would type at the prompt.
//
// Note: background-task notifications arrive as type:user / userType:external /
// isMeta:false, so they pass every structural guard and can only be caught by
// their <task-notification> envelope here. <task-id>/<tool-use-id> are the same
// class of injected tool-plumbing tags.
func isSyntheticPrompt(text string) bool {
	for _, marker := range []string{
		"<command-name>", "<command-message>", "<local-command-stdout>",
		"<command-args>", "[Request interrupted",
		"<task-notification>", "<task-id>", "<tool-use-id>",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	// Codex prepends "# AGENTS.md instructions for ..." to the first turn's
	// response_item; user_message is clean, but guard the header defensively.
	if strings.HasPrefix(text, "# AGENTS.md instructions for ") {
		return true
	}
	return false
}

// projectLabel derives a short, human-friendly label from a working dir: its
// basename (e.g. /home/u/code/foo → "foo"). Empty cwd → "".
func projectLabel(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

// parseTimestampMs parses a transcript timestamp (RFC3339, what both claude and
// codex write) into epoch millis. Unknown/unparseable → 0 (sorts last).
func parseTimestampMs(v any) int64 {
	s, ok := v.(string)
	if !ok || s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	return 0
}

// stringField safely reads a string field from a generic JSON map.
func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
