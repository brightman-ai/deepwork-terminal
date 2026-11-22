package terminal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

const (
	transcriptTailBytes = 4 << 20 // 4 MiB tail bound per transcript
	transcriptMaxTurns  = 100
)

// SessionTranscriptTurn is one user or assistant message bubble in the 💬 Chat view.
type SessionTranscriptTurn struct {
	ID        string   `json:"id"`
	Role      string   `json:"role"` // "user" | "assistant"
	Text      string   `json:"text"`
	Timestamp string   `json:"timestamp,omitempty"`
	Files     []string `json:"files,omitempty"`
}

// SessionTranscriptResponse is the wire payload of GET /sessions/{id}/transcript.
type SessionTranscriptResponse struct {
	SessionID string                  `json:"sessionId"`
	CWD       string                  `json:"cwd"`
	AgentTool string                  `json:"agentTool"`
	Turns     []SessionTranscriptTurn `json:"turns"`
}

// registerBotAndTranscriptRoutes mounts the resident bot endpoints and the structured
// session transcript endpoint onto s.mux without the /api prefix (host mounts /api).
func (s *Server) registerBotAndTranscriptRoutes(wrap func(http.HandlerFunc) http.HandlerFunc) {
	if s.mux == nil {
		return
	}
	if wrap == nil {
		wrap = s.authWrap
	}
	s.mux.HandleFunc("GET /bots", wrap(s.handleListBots))
	s.mux.HandleFunc("GET /bots/{id}", wrap(s.handleGetBot))
	s.mux.HandleFunc("PUT /bots/{id}/docs", wrap(s.handleUpdateBotDocs))
	s.mux.HandleFunc("POST /bots/{id}/chat", wrap(s.handleBotChat))
	s.mux.HandleFunc("POST /bots/{id}/run-check", wrap(s.handleBotRunCheck))
	s.mux.HandleFunc("PATCH /bots/{id}/items/{itemId}", wrap(s.handlePatchBotItem))
	s.mux.HandleFunc("GET /sessions/{id}/transcript", wrap(s.handleSessionTranscript))
}

// handleSessionTranscript handles GET /sessions/{id}/transcript.
//
// It resolves the session's live cwd and active agent tool, reads structured user/assistant
// turns + file refs from the matching Claude or Codex transcript, and falls back to the
// replayed PTY screen tail so plain-shell or fresh tabs still render cleanly in Chat mode.
func (s *Server) handleSessionTranscript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.mgr == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session manager unavailable"})
		return
	}
	sess, err := s.mgr.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	sess.mu.Lock()
	baseCWD := sess.CWD
	buf := sess.Buffer
	engine := strings.ToLower(strings.TrimSpace(sess.Engine))
	sess.mu.Unlock()

	shellPID := sess.ShellPID()
	cwd := baseCWD
	if lc, ok := s.workbenchCWD(r.Context(), id, r.URL.Query().Get("cwd")); ok && lc != "" {
		cwd = lc
	} else if live := processCWD(shellPID); live != "" {
		cwd = live
	}

	toolParam := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tool")))
	agentTool := toolParam
	if agentTool == "" && s.sessionAgent != nil && shellPID > 0 {
		if detected := s.sessionAgent.Tool(r.Context(), shellPID); detected != agentintel.ToolNone {
			agentTool = string(detected)
		}
	}
	if agentTool == "" && (engine == "claude" || engine == "codex") {
		agentTool = engine
	}

	pl := agentintel.NewProjectLocator()
	var turns []SessionTranscriptTurn

	switch agentTool {
	case "codex":
		if path := agentintel.CodexNewestRolloutForCWD(pl, cwd); path != "" {
			turns = parseCodexTranscriptTurns(path, cwd)
		}
	case "claude":
		if files, ferr := pl.ClaudeSessionFiles(cwd); ferr == nil && len(files) > 0 {
			turns = parseClaudeTranscriptTurns(files[0], cwd)
		}
	default:
		// Auto-detect: try Claude transcript first, then fall back to Codex rollout for cwd.
		if files, ferr := pl.ClaudeSessionFiles(cwd); ferr == nil && len(files) > 0 {
			turns = parseClaudeTranscriptTurns(files[0], cwd)
			if len(turns) > 0 {
				agentTool = "claude"
			}
		}
		if len(turns) == 0 {
			if path := agentintel.CodexNewestRolloutForCWD(pl, cwd); path != "" {
				turns = parseCodexTranscriptTurns(path, cwd)
				if len(turns) > 0 {
					agentTool = "codex"
				}
			}
		}
	}

	// Screen tail fallback: plain shell or fresh session with no transcript turns yet.
	if len(turns) == 0 && buf != nil {
		grid := sess.PTYSize()
		cols, rows := grid.Cols, grid.Rows
		if cols <= 0 {
			cols = 120
		}
		if rows <= 0 {
			rows = 40
		}
		screen := s.sessionScreen(sess.ID, buf, cols, rows)
		if len(screen) > 0 {
			tail := agentintel.TailFromLines(screen, agentintel.AgentTool(agentTool), agentintel.OverviewTailLines)
			if fallbackText := compactScreenTail(tail); fallbackText != "" {
				turns = []SessionTranscriptTurn{
					{
						ID:    "screen-tail-1",
						Role:  "assistant",
						Text:  fallbackText,
						Files: extractFileRefs(fallbackText, cwd),
					},
				}
			}
		}
	}

	if len(turns) > transcriptMaxTurns {
		turns = turns[len(turns)-transcriptMaxTurns:]
	}
	if turns == nil {
		turns = []SessionTranscriptTurn{}
	}

	writeJSON(w, http.StatusOK, SessionTranscriptResponse{
		SessionID: sess.ID,
		CWD:       cwd,
		AgentTool: agentTool,
		Turns:     turns,
	})
}

func compactScreenTail(lines []string) string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return ""
	}
	cleaned := make([]string, 0, end-start)
	for _, ln := range lines[start:end] {
		cleaned = append(cleaned, strings.TrimRight(ln, " \t\r"))
	}
	return strings.Join(cleaned, "\n")
}

// parseClaudeTranscriptTurns parses the tail of a Claude Code JSONL transcript into
// structured user and assistant turns with deduplicated file references.
func parseClaudeTranscriptTurns(path, cwd string) []SessionTranscriptTurn {
	var turns []SessionTranscriptTurn
	reader := agentintel.NewJSONLReader(path)
	turnSeq := 0

	_ = reader.ReadTailFunc(transcriptTailBytes, func(row map[string]any) bool {
		if b, _ := row["isMeta"].(bool); b {
			return true
		}
		if b, _ := row["isSidechain"].(bool); b {
			return true
		}
		rowType, _ := row["type"].(string)
		ts, _ := row["timestamp"].(string)
		msg, ok := row["message"].(map[string]any)
		if !ok {
			return true
		}

		switch rowType {
		case "user":
			if ut, ok := row["userType"].(string); ok && ut != "external" {
				return true
			}
			text := strings.TrimSpace(extractClaudeText(msg["content"]))
			if text == "" || isSyntheticPrompt(text) {
				return true
			}
			turnSeq++
			id := stringField(row, "uuid")
			if id == "" {
				id = fmt.Sprintf("u-%d", turnSeq)
			}
			turns = append(turns, SessionTranscriptTurn{
				ID:        id,
				Role:      "user",
				Text:      text,
				Timestamp: ts,
				Files:     extractFileRefs(text, cwd),
			})

		case "assistant":
			text, toolFiles := extractClaudeAssistantContent(msg["content"], cwd)
			text = strings.TrimSpace(text)
			files := mergeUniqueStrings(toolFiles, extractFileRefs(text, cwd))
			if text == "" && len(files) == 0 {
				return true
			}
			// Merge consecutive assistant chunks/tool calls into a single clean assistant bubble.
			if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
				last := &turns[len(turns)-1]
				if text != "" && !strings.Contains(last.Text, text) {
					if last.Text != "" {
						last.Text += "\n\n" + text
					} else {
						last.Text = text
					}
				}
				last.Files = mergeUniqueStrings(last.Files, files)
				if ts != "" {
					last.Timestamp = ts
				}
				return true
			}
			turnSeq++
			id := stringField(row, "uuid")
			if id == "" {
				id = fmt.Sprintf("a-%d", turnSeq)
			}
			turns = append(turns, SessionTranscriptTurn{
				ID:        id,
				Role:      "assistant",
				Text:      text,
				Timestamp: ts,
				Files:     files,
			})
		}
		return true
	})

	// Ensure any tool-only assistant turn carries a readable summary instead of blank text.
	for i := range turns {
		if turns[i].Role == "assistant" && turns[i].Text == "" && len(turns[i].Files) > 0 {
			turns[i].Text = "已检查/修改文件：" + strings.Join(turns[i].Files, ", ")
		}
	}
	return turns
}

func extractClaudeAssistantContent(content any, cwd string) (string, []string) {
	switch c := content.(type) {
	case string:
		return c, nil
	case []any:
		var textParts []string
		var files []string
		for _, blk := range c {
			m, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "text":
				if t, ok := m["text"].(string); ok {
					t = strings.TrimSpace(t)
					if t != "" {
						textParts = append(textParts, t)
					}
				}
			case "tool_use":
				if input, ok := m["input"].(map[string]any); ok {
					for _, key := range []string{"file_path", "path", "notebook_path"} {
						if p, ok := input[key].(string); ok && strings.TrimSpace(p) != "" {
							if norm := normalizeFileRef(p, cwd); norm != "" {
								files = append(files, norm)
							}
						}
					}
				}
			}
		}
		return strings.Join(textParts, "\n"), mergeUniqueStrings(nil, files)
	}
	return "", nil
}

var applyPatchFileRE = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: (.+)$`)

// parseCodexTranscriptTurns parses a Codex rollout JSONL file into structured user/assistant turns.
func parseCodexTranscriptTurns(path, cwd string) []SessionTranscriptTurn {
	var turns []SessionTranscriptTurn
	reader := agentintel.NewJSONLReader(path)
	turnSeq := 0

	appendAssistant := func(text string, files []string, ts string) {
		text = strings.TrimSpace(text)
		files = mergeUniqueStrings(files, extractFileRefs(text, cwd))
		if text == "" && len(files) == 0 {
			return
		}
		if len(turns) > 0 && turns[len(turns)-1].Role == "assistant" {
			last := &turns[len(turns)-1]
			if text != "" && last.Text != text && !strings.Contains(last.Text, text) {
				if last.Text != "" {
					last.Text += "\n\n" + text
				} else {
					last.Text = text
				}
			}
			last.Files = mergeUniqueStrings(last.Files, files)
			if ts != "" {
				last.Timestamp = ts
			}
			return
		}
		turnSeq++
		turns = append(turns, SessionTranscriptTurn{
			ID:        fmt.Sprintf("a-%d", turnSeq),
			Role:      "assistant",
			Text:      text,
			Timestamp: ts,
			Files:     files,
		})
	}

	_ = reader.ReadTailFunc(transcriptTailBytes, func(row map[string]any) bool {
		rowType, _ := row["type"].(string)
		ts, _ := row["timestamp"].(string)
		payload, _ := row["payload"].(map[string]any)
		if payload == nil {
			return true
		}

		switch rowType {
		case "event_msg":
			switch stringField(payload, "type") {
			case "user_message":
				text := strings.TrimSpace(stringField(payload, "message"))
				if text == "" || isSyntheticPrompt(text) {
					return true
				}
				turnSeq++
				turns = append(turns, SessionTranscriptTurn{
					ID:        fmt.Sprintf("u-%d", turnSeq),
					Role:      "user",
					Text:      text,
					Timestamp: ts,
					Files:     extractFileRefs(text, cwd),
				})
			case "agent_message":
				text := strings.TrimSpace(stringField(payload, "message"))
				appendAssistant(text, nil, ts)
			}

		case "response_item":
			pType := stringField(payload, "type")
			switch pType {
			case "message":
				if stringField(payload, "role") != "assistant" {
					return true
				}
				text := extractCodexMessageText(payload["content"])
				appendAssistant(text, nil, ts)
			case "function_call", "custom_tool_call":
				var files []string
				if rawArgs := stringField(payload, "arguments"); rawArgs != "" {
					files = append(files, extractCodexToolFiles(rawArgs, cwd)...)
				}
				if rawInput := stringField(payload, "input"); rawInput != "" {
					files = append(files, extractCodexToolFiles(rawInput, cwd)...)
				}
				if len(files) > 0 {
					appendAssistant("", files, ts)
				}
			}
		}
		return true
	})

	for i := range turns {
		if turns[i].Role == "assistant" && turns[i].Text == "" && len(turns[i].Files) > 0 {
			turns[i].Text = "已检查/修改文件：" + strings.Join(turns[i].Files, ", ")
		}
	}
	return turns
}

func extractCodexMessageText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, blk := range c {
			m, ok := blk.(map[string]any)
			if !ok {
				continue
			}
			bt := stringField(m, "type")
			if bt == "output_text" || bt == "text" {
				if t := strings.TrimSpace(stringField(m, "text")); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func extractCodexToolFiles(raw, cwd string) []string {
	var out []string
	for _, m := range applyPatchFileRE.FindAllStringSubmatch(raw, -1) {
		if len(m) >= 2 {
			if norm := normalizeFileRef(m[1], cwd); norm != "" {
				out = append(out, norm)
			}
		}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		for _, key := range []string{"file_path", "path", "target_file"} {
			if p, ok := obj[key].(string); ok && strings.TrimSpace(p) != "" {
				if norm := normalizeFileRef(p, cwd); norm != "" {
					out = append(out, norm)
				}
			}
		}
	}
	return mergeUniqueStrings(nil, out)
}

func mergeUniqueStrings(base, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, s := range base {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range extra {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
