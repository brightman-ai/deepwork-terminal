package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"github.com/google/uuid"

	"github.com/brightman-ai/kit/obs"
)

const (
	// wsWriteTimeout is the timeout for writing a single WS message.
	wsWriteTimeout = 10 * time.Second

	// wsReplayMaxBytes caps attach/reconnect replay. The ring buffer still keeps
	// 1 MiB for local history, but pushing the full buffer before interactive WS
	// traffic makes remote tabs feel sticky on reconnect.
	wsReplayMaxBytes = 256 * 1024

	// tmuxStatePollInterval controls how often the WS writer recomputes tmux
	// topology and pushes a tmux_state frame on change. Kept light (~1s) so the
	// frontend stays current without a heavy poll; the provider is time-boxed.
	tmuxStatePollInterval = 1 * time.Second
)

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleListSessions handles GET /sessions.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.mgr.List()
	type sessionInfo struct {
		ID          string        `json:"id"`
		SessionID   string        `json:"session_id"`
		Name        string        `json:"name"`
		Title       string        `json:"title"`
		Engine      string        `json:"engine"`
		CWD         string        `json:"cwd"`
		Status      SessionStatus `json:"status"`
		StatusIcon  string        `json:"status_icon"`
		CreatedAt   string        `json:"created_at"`
		LastSeen    string        `json:"last_seen"`
		LastActive  string        `json:"lastActive"`
		AgentTool   string        `json:"agentTool,omitempty"`
		AgentStatus string        `json:"agentStatus,omitempty"`
	}
	// Agent state comes from the SAME per-tick snapshot the overview cards render, so the tab dot
	// and the card can never disagree — they are one computation, not two that happen to match.
	agents := s.sessionAgentStatuses(r.Context())
	result := make([]sessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		sess.mu.Lock()
		info := sessionInfo{
			ID:         sess.ID,
			SessionID:  sess.ID,
			Name:       sess.Name,
			Title:      sessionTitle(sess),
			Engine:     sess.Engine,
			CWD:        sess.CWD,
			Status:     sess.Status,
			StatusIcon: statusIcon(sess.Status),
			CreatedAt:  formatCLITime(sess.CreatedAt),
			LastSeen:   formatCLITime(sess.LastActive),
			LastActive: sess.LastActive.Format("2006-01-02T15:04:05Z07:00"),
		}
		sess.mu.Unlock()

		if a, ok := agents[sess.ID]; ok {
			info.AgentTool, info.AgentStatus = a[0], a[1]
		}
		result = append(result, info)
	}
	writeJSON(w, http.StatusOK, result)
}

// handleCreateSession handles POST /sessions.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Title  string `json:"title"`
		Engine string `json:"engine"`
		Shell  string `json:"shell"`
		CWD    string `json:"cwd"`
	}
	// Allow empty body — use defaults.
	_ = json.NewDecoder(r.Body).Decode(&req)

	sess, err := s.mgr.CreateWithOptions(CreateOptions{
		Name:   req.Name,
		Title:  req.Title,
		Engine: req.Engine,
		Shell:  req.Shell,
		CWD:    req.CWD,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          sess.ID,
		"session_id":  sess.ID,
		"name":        sess.Name,
		"title":       sessionTitle(sess),
		"engine":      sess.Engine,
		"cwd":         sess.CWD,
		"status":      sess.Status,
		"status_icon": statusIcon(sess.Status),
		"created_at":  formatCLITime(sess.CreatedAt),
		"last_seen":   formatCLITime(sess.LastActive),
	})
}

// handleGetSession handles GET /sessions/{id}.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.mgr.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           sess.ID,
		"session_id":   sess.ID,
		"name":         sess.Name,
		"title":        sessionTitle(sess),
		"engine":       sess.Engine,
		"cwd":          sess.CWD,
		"status":       sess.Status,
		"status_icon":  statusIcon(sess.Status),
		"created_at":   formatCLITime(sess.CreatedAt),
		"last_seen":    formatCLITime(sess.LastActive),
		"createdAt":    sess.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"lastActive":   sess.LastActive.Format("2006-01-02T15:04:05Z07:00"),
		"tmuxDetected": sess.TmuxDetected,
	})
}

// handleDeleteSession handles DELETE /sessions/{id}.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mgr.Destroy(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleResize handles POST /sessions/{id}/resize.
func (s *Server) handleResize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.mgr.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	var req struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Cols < 1 || req.Rows < 1 || req.Cols > 500 || req.Rows > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cols/rows out of bounds"})
		return
	}

	sess.mu.Lock()
	ptyFile := sess.PTY
	sess.mu.Unlock()
	if ptyFile == nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": "session has no PTY"})
		return
	}
	if err := pty.Setsize(ptyFile, &pty.Winsize{
		Cols: uint16(req.Cols),
		Rows: uint16(req.Rows),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleInput handles POST /sessions/{id}/input.
// [TH-0501-m9j] WKWebView silently drops WebSocket binary frames. HTTP POST is
// 100% reliable on all platforms. Frontend sends raw bytes as request body.
func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.mgr.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil || len(data) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty body"})
		return
	}

	sess.mu.Lock()
	ptyFile := sess.PTY
	sess.mu.Unlock()
	if ptyFile == nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": "session has no PTY"})
		return
	}

	observeTerminalInput(obs.WithStage(r.Context(), stgTerminalInput), id, data)
	if _, writeErr := ptyFile.Write(data); writeErr != nil {
		logger.Debug("pty write failed (http input)", "id", id, "error", writeErr)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "pty write failed"})
		return
	}

	sess.mu.Lock()
	sess.LastActive = time.Now()
	sess.mu.Unlock()
	// Same clearing rule as the WS paths — iOS clients type through this endpoint, so
	// skipping it here would leave exactly those users with a signal that never clears.
	noteUserInput(sess, data)

	w.WriteHeader(http.StatusNoContent)
}

// handleHudLog handles POST /debug/logs.
// Accepts client diagnostic events and prints each one to stderr so they
// appear in the server's log stream. Only active when the client has
// cli_diag enabled — zero overhead in normal usage.
func (s *Server) handleHudLog(w http.ResponseWriter, r *http.Request) {
	var req HudLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	// Print each diagnostic event as a structured line to stderr.
	if len(req.Events) > 0 && string(req.Events) != "null" {
		var events []json.RawMessage
		if err := json.Unmarshal(req.Events, &events); err == nil {
			for _, ev := range events {
				fmt.Fprintf(os.Stderr, "[cli-diag] %s\n", ev)
			}
		} else {
			// Fallback: print raw if not an array.
			fmt.Fprintf(os.Stderr, "[cli-diag] %s\n", req.Events)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleTelemetryLog handles POST /telemetry/log — the frontend's structured log sink.
//
// ── Why this exists ──────────────────────────────────────────────────────────────────────────
// The shared frontend calls configureRemoteSink() at boot (main.ts) and every createLogger()
// INFO-or-above line is batched to `/api/telemetry/log`. deepwork-pro serves that route
// (internal/webui/metrics_routes.go); the standalone terminal never did — so on :8087 the entire
// frontend log stream has been POSTing into a 404 and vanishing. Silently, because observability
// "must never block user actions", so the client drops the failure on the floor by design.
//
// The cost is not abstract: a renderer/latency question that the browser could have answered in
// one line instead required a throwaway benchmark, because the one channel built to carry that
// answer was not connected at this end. Serving it here restores parity with pro for every
// consumer of the shared frontend, not just the line that exposed the gap.
//
// Entries arrive as the CE envelope {entries: [{l,t,mod,msg,ext,...}]}. They are re-emitted to the
// server's own log stream, tagged so a frontend line is never mistaken for a backend one.
func (s *Server) handleTelemetryLog(w http.ResponseWriter, r *http.Request) {
	var envelope struct {
		Entries []struct {
			Level   string          `json:"l"`
			Time    string          `json:"t"`
			Module  string          `json:"mod"`
			Message string          `json:"msg"`
			Stage   string          `json:"stg,omitempty"`
			TraceID string          `json:"tid,omitempty"`
			Ext     json.RawMessage `json:"ext,omitempty"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid telemetry payload"})
		return
	}
	for _, e := range envelope.Entries {
		args := []any{"level", e.Level, "mod", e.Module, "client_time", e.Time}
		if e.Stage != "" {
			args = append(args, "stage", e.Stage)
		}
		if e.TraceID != "" {
			args = append(args, "trace_id", e.TraceID)
		}
		if len(e.Ext) > 0 {
			args = append(args, "ext", string(e.Ext))
		}
		logger.Info("[frontend] "+e.Message, args...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ingested": len(envelope.Entries)})
}

// handleVersion handles GET /version — returns the running binary's build version so the
// UI can show it (the tab bar). Release builds inject it via ldflags; source builds report
// "dev". Falls back to "dev" if the embedding host never set Config.Version.
//
// It ALSO carries the newest published release when one is known and strictly newer — the
// "程序旧了" axis (see release_check.go). Riding on /version rather than getting its own
// endpoint is deliberate: the UI asks this exact question at the exact moment it renders the
// version badge, so one round-trip answers both halves of "am I current?". The lookup itself
// never blocks this handler (cached + refreshed in the background), and a build that is not a
// clean release tag never triggers it at all.
//
// releaseState is the authoritative four-way answer (local/current/outdated/unknown) — the UI
// renders it, never re-derives it. "unknown" (offline / not looked up yet) is deliberately a
// DISTINCT state from "current": claiming "已是最新" on a failed lookup would be a confident lie.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	v := s.config.Version
	if v == "" {
		v = "dev"
	}
	body := map[string]string{"version": v, "releaseState": string(ReleaseLocal)}
	if s.releases != nil {
		rel, state := s.releases.Latest(v)
		body["releaseState"] = string(state)
		if state == ReleaseOutdated {
			body["latest"] = rel.Tag
			body["latestUrl"] = rel.URL
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// handleWebSocket handles GET /sessions/{id}/ws — WebSocket terminal I/O.
// Binary frames carry raw terminal data; Text/JSON frames carry control messages.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.mgr.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	// Check session is still running.
	sess.mu.Lock()
	status := sess.Status
	sess.mu.Unlock()
	if status == StatusExited {
		writeJSON(w, http.StatusGone, map[string]string{"error": "session has exited"})
		return
	}

	// Upgrade to WebSocket.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow any origin in dev mode.
	})
	if err != nil {
		logger.Error("ws upgrade failed", "id", id, "error", err)
		return
	}
	defer conn.CloseNow()
	// A paste can be large; coder/websocket's default read limit is 32KB, so a big paste would
	// trip StatusMessageTooBig and CLOSE the connection (input lost / "paste failed"). Keystrokes
	// are tiny, so this only matters for paste — raise it generously so a paste flows in one frame.
	conn.SetReadLimit(16 << 20) // 16 MiB
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	inputLogCtx := obs.WithStage(ctx, stgTerminalInput)
	attachLogCtx := obs.WithStage(ctx, stgTerminalAttach)
	connectedAt := time.Now()

	// BUG-3: Register as active connection — preempts any existing WS for this session.
	s.mgr.SetActiveConn(id, conn, cancel)
	defer s.mgr.ClearActiveConn(id, conn)

	subID := uuid.New().String()

	// Subscribe to PTY output.
	dataCh, unsub := s.mgr.Subscribe(sess, subID)
	defer unsub()

	// Send replay buffer first. Strip terminal report-queries (DA/DSR/color/…): replaying them
	// would make the browser terminal re-answer, and on a reconnect into tmux copy-mode those
	// stray answers are read as keys (the mysterious "(search up)"). See stripDeviceQueries.
	bufferBytes := sess.Buffer.Len()
	replayRaw := sess.Buffer.ReadTail(wsReplayMaxBytes)
	replayTruncated := bufferBytes > len(replayRaw)
	replay := stripDeviceQueries(replayRaw)
	terminalWSConnectionsTotal.Inc()
	terminalLogger.Info(attachLogCtx, "cli ws connected",
		"session_id", id,
		"sub_id", subID,
		"remote_addr", r.RemoteAddr,
		"buffer_bytes", bufferBytes,
		"replay_bytes", len(replay),
		"replay_limit_bytes", wsReplayMaxBytes,
		"replay_truncated", replayTruncated)
	// Wrap in a closure so time.Since is evaluated at disconnect (when the deferred
	// func runs), not at defer registration — otherwise duration_ms is always ~0.
	defer func() {
		terminalLogger.Info(attachLogCtx, "cli ws disconnected",
			"session_id", id,
			"sub_id", subID,
			"duration_ms", time.Since(connectedAt).Milliseconds())
	}()
	if len(replay) > 0 {
		terminalWSReplayBytesTotal.Add(uint64(len(replay)))
		writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
		err = conn.Write(writeCtx, websocket.MessageBinary, replay)
		writeCancel()
		if err != nil {
			logger.Debug("ws replay write failed", "id", id, "error", err)
			return
		}
		terminalLogger.Info(attachLogCtx, "cli ws replay sent",
			"session_id", id,
			"sub_id", subID,
			"buffer_bytes", bufferBytes,
			"replay_bytes", len(replay),
			"replay_limit_bytes", wsReplayMaxBytes,
			"replay_truncated", replayTruncated)
	}

	// Push session metadata immediately after replay so the frontend can enable
	// gesture hints based on TmuxDetected without requiring a round-trip.
	sess.mu.Lock()
	tmuxDetected := sess.TmuxDetected
	sess.mu.Unlock()
	metaPayload, _ := json.Marshal(SessionMetaPayload{TmuxDetected: tmuxDetected})
	metaMsg, _ := json.Marshal(WSControlMessage{Type: MsgTypeSessionMeta, Payload: metaPayload})
	{
		writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
		_ = conn.Write(writeCtx, websocket.MessageText, metaMsg)
		writeCancel()
	}

	// Subscribe to agent state changes (if available) — replaces independent SSE connection.
	var agentCh <-chan json.RawMessage
	if s.hooks.AgentStatePush != nil {
		ch, agentRelease, agentErr := s.hooks.AgentStatePush(ctx, id)
		if agentErr == nil && ch != nil {
			agentCh = ch
			if agentRelease != nil {
				defer agentRelease()
			}
		}
	}

	// Resolve this session's shell PID once for tmux state scoping.
	sess.mu.Lock()
	wsShellPID := sess.ShellPID()
	sess.mu.Unlock()

	// Status frames (tmux topology / sessions overview / explicit signals) are produced on their
	// OWN goroutine and handed to the writer already marshalled.
	//
	// ── Why they cannot be computed in the writer's select ───────────────────────────────────
	// They used to be, on a 1s tick, under the assumption recorded here that "the provider call is
	// time-boxed internally, so the tick stays cheap". On this developer's own machine that
	// assumption is off by three orders of magnitude: one TmuxState() over a 6-pane server measures
	// 0.6s–4.3s (scripts/diag/tmuxprobe — 397ms just for `ServerRunning`, 147ms for `list-panes`, and
	// 40–554ms per window for the overview tail capture). Every one of those milliseconds was time
	// the writer spent NOT reading dataCh, because a Go select serves one case at a time. The user
	// sees exactly that: type two characters, watch them hang, then watch them all land at once
	// when the tick finally returns — a full second of echo lag per second of wall clock, on a
	// terminal whose whole job is to echo.
	//
	// Splitting the producer off makes the writer's cost independent of how slow tmux/ps/lsof are:
	// the writer only ever marshals nothing and calls conn.Write. A slow probe now delays only the
	// STATUS frame it belongs to — which is a 1s-resolution dashboard nobody is typing into.
	//
	// statusCh is capacity 1 and the producer DROPS rather than blocks: if a probe overruns its
	// tick (routinely, per the numbers above) the next result supersedes the stale one, and the
	// producer never accumulates a backlog of screens that have already changed. Diff suppression
	// lives with the producer for the same reason it lived in the writer before — one owner of
	// "what did this connection last see".
	statusCh := make(chan []byte, 1)
	go func() {
		tmuxTicker := time.NewTicker(tmuxStatePollInterval)
		defer tmuxTicker.Stop()
		var lastTmuxState []byte
		// Non-tmux Agent Overview feed — same ticker, same diff suppression, separate frame.
		// See sessions_overview.go for why this rides the existing connection instead of polling.
		var lastSessionsOverview []byte
		// Explicit-signal feed (session_signal.go). Seeded with the EMPTY payload so a quiet
		// machine pushes nothing, while a signal that is already pending at attach time is
		// delivered on the first tick.
		lastAgentSignals := emptyAgentSignals

		// offer hands one finished frame to the writer, or drops it if the writer still holds an
		// undelivered one. Dropping is correct for every feed here: all three are full-state
		// snapshots, so the newer frame says everything the dropped one would have.
		offer := func(msg []byte) bool {
			select {
			case statusCh <- msg:
			case <-ctx.Done():
				return false
			default:
				terminalStatusFramesDroppedTotal.Inc()
			}
			return true
		}

		for {
			select {
			case <-tmuxTicker.C:
				// TWO independent feeds share this tick. tmux_state is gated on a tmux provider;
				// sessions_overview must NOT be — a user without tmux is precisely who needs it,
				// so the old early `continue` on the tmux branch would have starved exactly the
				// intended audience. Keep them in separate blocks.
				if s.tmuxProvider != nil {
					raw, terr := s.tmuxProvider.TmuxState(ctx, wsShellPID)
					if terr == nil && raw != nil && !bytes.Equal(raw, lastTmuxState) {
						lastTmuxState = raw
						msg, _ := json.Marshal(WSControlMessage{
							Type:    MsgTypeTmuxState,
							Payload: raw,
						})
						if !offer(msg) {
							return
						}
					}
				}
				if raw := s.sessionsOverviewJSON(ctx); raw != nil && !bytes.Equal(raw, lastSessionsOverview) {
					lastSessionsOverview = raw
					msg, _ := json.Marshal(WSControlMessage{
						Type:    MsgTypeSessionsOverview,
						Payload: raw,
					})
					if !offer(msg) {
						return
					}
				}
				// Explicit signals ride the same tick for the same reason as the two above:
				// no extra connection, no client polling. A signal can come from ANY session
				// (a bell in a background tab has no WS of its own), so the frame describes
				// all of them, exactly like sessions_overview.
				if raw := s.agentSignalsJSON(); raw != nil && !bytes.Equal(raw, lastAgentSignals) {
					lastAgentSignals = raw
					msg, _ := json.Marshal(WSControlMessage{
						Type:    MsgTypeAgentSignal,
						Payload: raw,
					})
					if !offer(msg) {
						return
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start writer goroutine: PTY output → WS binary frames + agent state → WS control.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		localAgentCh := agentCh // local copy for nil-safe select
		for {
			select {
			case msg := <-statusCh:
				writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
				err := conn.Write(writeCtx, websocket.MessageText, msg)
				writeCancel()
				if err != nil {
					logger.Debug("ws status frame write failed", "id", id, "error", err)
					return
				}
			case data, ok := <-dataCh:
				if !ok {
					return
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
				err := conn.Write(writeCtx, websocket.MessageBinary, data)
				writeCancel()
				if err != nil {
					logger.Debug("ws write failed", "id", id, "error", err)
					return
				}
			case agentData, ok := <-localAgentCh:
				if !ok {
					localAgentCh = nil
					continue
				}
				msg, _ := json.Marshal(WSControlMessage{
					Type:    MsgTypeAgentState,
					Payload: agentData,
				})
				writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
				err := conn.Write(writeCtx, websocket.MessageText, msg)
				writeCancel()
				if err != nil {
					logger.Debug("ws agent_state write failed", "id", id, "error", err)
					return
				}
			case <-sess.done:
				// Shell exited — send shell_exit message.
				sess.mu.Lock()
				exitCode := sess.exitCode
				sess.mu.Unlock()
				payload, _ := json.Marshal(ShellExitPayload{ExitCode: exitCode})
				msg, _ := json.Marshal(WSControlMessage{
					Type:    MsgTypeShellExit,
					Payload: payload,
				})
				writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
				_ = conn.Write(writeCtx, websocket.MessageText, msg)
				writeCancel()
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Reader loop: WS → PTY (binary) or control messages (text/JSON).
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			// Client disconnected — PTY stays alive (IR-07).
			logger.Debug("ws read closed", "id", id, "error", err)
			break
		}

		switch msgType {
		case websocket.MessageBinary:
			observeTerminalInput(inputLogCtx, id, data)
			// Terminal input → write to PTY.
			sess.mu.Lock()
			ptyFile := sess.PTY
			sess.mu.Unlock()
			if ptyFile != nil {
				_, writeErr := ptyFile.Write(data)
				if writeErr != nil {
					logger.Debug("pty write failed", "id", id, "error", writeErr)
					break
				}
				sess.mu.Lock()
				sess.LastActive = time.Now()
				sess.mu.Unlock()
				// The human answered → the "needs you" signal is spent (session_signal.go).
				noteUserInput(sess, data)
			}

		case websocket.MessageText:
			// Control message.
			var ctrl WSControlMessage
			if err := json.Unmarshal(data, &ctrl); err != nil {
				logger.Debug("invalid control message", "id", id, "error", err)
				continue
			}
			s.handleControlMessage(ctx, conn, sess, ctrl)
		}
	}

	cancel()
	<-writerDone
}

// executeTmuxNav runs a tmux command to navigate windows or sessions.
func (s *Server) executeTmuxNav(sess *Session, action string) {
	var args []string
	switch action {
	case "window_next":
		args = []string{"select-window", "-t", ":+"}
	case "window_prev":
		args = []string{"select-window", "-t", ":-"}
	case "session_next":
		args = []string{"switch-client", "-n"}
	case "session_prev":
		args = []string{"switch-client", "-p"}
	default:
		logger.Debug("unknown tmux_nav action", "id", sess.ID, "action", action)
		return
	}
	cmd := exec.Command("tmux", args...)
	if err := cmd.Run(); err != nil {
		logger.Debug("tmux nav failed", "id", sess.ID, "action", action, "err", err)
	}
}

// handleControlMessage processes a JSON control message from the client.
func (s *Server) handleControlMessage(ctx context.Context, conn *websocket.Conn, sess *Session, ctrl WSControlMessage) {
	switch ctrl.Type {
	case MsgTypeResize:
		var payload ResizePayload
		if err := json.Unmarshal(ctrl.Payload, &payload); err != nil {
			logger.Debug("invalid resize payload", "id", sess.ID, "error", err)
			return
		}
		if payload.Cols < 1 || payload.Rows < 1 || payload.Cols > 500 || payload.Rows > 500 {
			logger.Debug("resize out of bounds", "id", sess.ID, "cols", payload.Cols, "rows", payload.Rows)
			return
		}
		sess.mu.Lock()
		ptyFile := sess.PTY
		sess.mu.Unlock()
		if ptyFile != nil {
			if err := pty.Setsize(ptyFile, &pty.Winsize{
				Cols: uint16(payload.Cols),
				Rows: uint16(payload.Rows),
			}); err != nil {
				logger.Debug("pty setsize failed", "id", sess.ID, "error", err)
			}
		}

	case MsgTypeHeartbeat:
		// Echo payload (contains client sentAt for RTT measurement).
		ack, _ := json.Marshal(WSControlMessage{Type: MsgTypeHeartbeatAck, Payload: ctrl.Payload})
		writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
		_ = conn.Write(writeCtx, websocket.MessageText, ack)
		writeCancel()

	case MsgTypePing:
		pong, _ := json.Marshal(WSControlMessage{Type: MsgTypePong, Payload: ctrl.Payload})
		writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
		_ = conn.Write(writeCtx, websocket.MessageText, pong)
		writeCancel()

	case MsgTypeAuthRefresh:
		// Token refresh — just acknowledge.
		logger.Debug("auth refresh received", "id", sess.ID)

	case MsgTypeInput:
		// [TH-0501-m9j] Terminal input via JSON text frame.
		var payload InputPayload
		if err := json.Unmarshal(ctrl.Payload, &payload); err != nil {
			logger.Debug("invalid input payload", "id", sess.ID, "error", err)
			return
		}
		sess.mu.Lock()
		ptyFile := sess.PTY
		sess.mu.Unlock()
		if ptyFile != nil {
			if _, writeErr := ptyFile.Write(payload.Data); writeErr != nil {
				logger.Debug("pty write failed (text input)", "id", sess.ID, "error", writeErr)
			}
			sess.mu.Lock()
			sess.LastActive = time.Now()
			sess.mu.Unlock()
			noteUserInput(sess, payload.Data)
		}

	case MsgTypeTmuxNav:
		// Silently ignored when the shell is not running inside tmux.
		sess.mu.Lock()
		detected := sess.TmuxDetected
		sess.mu.Unlock()
		if !detected {
			return
		}
		var payload TmuxNavPayload
		if err := json.Unmarshal(ctrl.Payload, &payload); err != nil {
			logger.Debug("invalid tmux_nav payload", "id", sess.ID, "error", err)
			return
		}
		s.executeTmuxNav(sess, payload.Action)

	default:
		logger.Debug("unknown control message type", "id", sess.ID, "type", ctrl.Type)
		errPayload, _ := json.Marshal(ErrorPayload{
			Code:    "unknown_message_type",
			Message: "unknown control message type: " + ctrl.Type,
		})
		errMsg, _ := json.Marshal(WSControlMessage{
			Type:    MsgTypeError,
			Payload: errPayload,
		})
		writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
		_ = conn.Write(writeCtx, websocket.MessageText, errMsg)
		writeCancel()
	}
}

func formatCLITime(t time.Time) string {
	return t.Format(time.RFC3339)
}

func statusIcon(status SessionStatus) string {
	switch status {
	case StatusRunning:
		return "active"
	case StatusExited:
		return "terminated"
	default:
		return "disconnected"
	}
}

func sessionTitle(s *Session) string {
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}
