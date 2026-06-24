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
		shellPID := sess.ShellPID()
		sess.mu.Unlock()

		// Lightweight agent detection via injected function (avoids import cycle).
		if s.hooks.AgentDetect != nil && shellPID > 0 {
			if tool, status := s.hooks.AgentDetect(r.Context(), shellPID, info.CWD); tool != "" {
				info.AgentTool = tool
				info.AgentStatus = status
			}
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

// handleVersion handles GET /version — returns the running binary's build version so the
// UI can show it (the tab bar). Release builds inject it via ldflags; source builds report
// "dev". Falls back to "dev" if the embedding host never set Config.Version.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	v := s.config.Version
	if v == "" {
		v = "dev"
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": v})
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

	// Light tmux-state poll: recompute on a ~1s tick, push only on change (diff).
	// The provider call is time-boxed internally, so the tick stays cheap and the
	// write loop is never blocked on tmux/ps subprocesses for long.
	tmuxTicker := time.NewTicker(tmuxStatePollInterval)
	defer tmuxTicker.Stop()
	var lastTmuxState []byte

	// Start writer goroutine: PTY output → WS binary frames + agent state → WS control.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		localAgentCh := agentCh // local copy for nil-safe select
		for {
			select {
			case <-tmuxTicker.C:
				if s.tmuxProvider == nil {
					continue
				}
				raw, terr := s.tmuxProvider.TmuxState(ctx, wsShellPID)
				if terr != nil || raw == nil {
					continue
				}
				if bytes.Equal(raw, lastTmuxState) {
					continue // no change → no push
				}
				lastTmuxState = raw
				msg, _ := json.Marshal(WSControlMessage{
					Type:    MsgTypeTmuxState,
					Payload: raw,
				})
				writeCtx, writeCancel := context.WithTimeout(ctx, wsWriteTimeout)
				err := conn.Write(writeCtx, websocket.MessageText, msg)
				writeCancel()
				if err != nil {
					logger.Debug("ws tmux_state write failed", "id", id, "error", err)
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
