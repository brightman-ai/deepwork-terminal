package terminal

import (
	"net/http"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// handleSessionOverview handles GET /sessions/{id}/overview.
//
// It resolves the session's cwd, then routes to the CLAUDE or CODEX metrics extractor and
// parses the active agent's transcript/rollout into per-turn metrics + an aggregate
// summary + session detail — the payload behind the shared @ce OverviewPanel.
//
// Tool routing (claude default):
//   - ?tool=codex → codex extractor directly (the frontend passes the active pane's
//     agentTool from /tmux/state).
//   - else claude. As a param-free fallback, if the claude extractor yields zero turns AND
//     a codex rollout exists for the cwd, route to codex — so a Codex pane still works even
//     when the param is missing.
//
// Unknown session → 404 (mirrors handleGetSession). A pane with no transcript/rollout yet
// → 200 with a valid-but-empty shape (turn_count 0), never an error — the panel just shows
// zeros and "—" for the non-derivable metrics.
func (s *Server) handleSessionOverview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.mgr.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	sess.mu.Lock()
	baseCWD := sess.CWD
	title := sessionTitle(sess)
	// active = the PTY session is still alive (not exited). The transcript-level "agent
	// running right now" nuance lives in agentintel; at the session layer this is the
	// honest, cheap signal.
	active := sess.Status != StatusExited
	sess.mu.Unlock()

	// Prefer the LIVE active tmux pane cwd (frontend-supplied) so the overview follows
	// pane/window switches; fall back to the session's creation cwd.
	cwd := baseCWD
	if lc, ok := s.workbenchCWD(id, r.URL.Query().Get("cwd")); ok && lc != "" {
		cwd = lc
	}

	pl := agentintel.NewProjectLocator()
	metrics := overviewMetrics(pl, cwd, id, title, active, r.URL.Query().Get("tool"))
	writeJSON(w, http.StatusOK, metrics)
}

// overviewMetrics picks the claude or codex extractor for the active pane. tool is the
// frontend-supplied active-pane agentTool (may be empty). When tool=="codex" it goes
// straight to codex. Otherwise it parses the claude transcript; if that yields zero turns
// and a codex rollout exists for cwd, it falls back to codex (param-free Codex support).
func overviewMetrics(pl *agentintel.ProjectLocator, cwd, id, title string, active bool, tool string) agentintel.SessionMetrics {
	if tool == "codex" {
		return agentintel.CodexSessionMetricsForCWD(pl, cwd, id, title, active)
	}
	claude := agentintel.SessionMetricsForCWD(pl, cwd, id, title, active)
	if claude.Detail.TurnCount == 0 && agentintel.CodexRolloutExistsForCWD(pl, cwd) {
		return agentintel.CodexSessionMetricsForCWD(pl, cwd, id, title, active)
	}
	return claude
}
