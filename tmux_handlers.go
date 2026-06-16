package terminal

import (
	"context"
	"encoding/json"
	"net/http"
)

// handleTmuxState handles GET /tmux/state.
// Returns the full tmux topology snapshot (TmuxState) as JSON.
// Optional query ?session=<id> scopes the Attached flag to that session's shell.
func (s *Server) handleTmuxState(w http.ResponseWriter, r *http.Request) {
	shellPID := s.shellPIDForQuery(r.URL.Query().Get("session"))
	raw, err := s.tmuxProvider.TmuxState(r.Context(), shellPID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// handleTmuxPrefix handles GET /tmux/prefix.
// Returns just the { display, bytes } prefix object — a cheap call for the
// client to learn which control byte(s) emulate the tmux prefix key.
func (s *Server) handleTmuxPrefix(w http.ResponseWriter, r *http.Request) {
	raw, err := s.tmuxProvider.TmuxState(r.Context(), 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Extract the "prefix" field from the full state to keep one source of truth.
	var envelope struct {
		Prefix any `json:"prefix"`
	}
	if json.Unmarshal(raw, &envelope) == nil && envelope.Prefix != nil {
		writeJSON(w, http.StatusOK, envelope.Prefix)
		return
	}
	// Fallback: return the full state rather than fail.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// TmuxCopyMotioner is an OPTIONAL provider capability: drive a copy-mode scroll
// motion directly against the tmux server (bypassing the PTY keystroke stream,
// which silently no-ops for these motions). The default provider implements it; a
// host-injected provider may omit it, in which case the endpoint 501s gracefully.
type TmuxCopyMotioner interface {
	CopyMotion(ctx context.Context, session, motion string) error
}

// handleTmuxCopyMotion handles POST /tmux/copy-motion.
// Body: { "session": "<name>", "motion": "halfpage-up" }. The session scopes the
// target to its active pane; motion is validated against an allowlist provider-side.
func (s *Server) handleTmuxCopyMotion(w http.ResponseWriter, r *http.Request) {
	mover, ok := s.tmuxProvider.(TmuxCopyMotioner)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "copy motion unsupported"})
		return
	}
	var body struct {
		Session string `json:"session"`
		Motion  string `json:"motion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := mover.CopyMotion(r.Context(), body.Session, body.Motion); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// shellPIDForQuery resolves a session ID to its shell PID, or 0 if absent/unknown.
func (s *Server) shellPIDForQuery(sessionID string) int {
	if sessionID == "" {
		return 0
	}
	sess, err := s.mgr.Get(sessionID)
	if err != nil {
		return 0
	}
	return sess.ShellPID()
}
