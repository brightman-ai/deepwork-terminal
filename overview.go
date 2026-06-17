package terminal

import (
	"net/http"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// handleSessionOverview handles GET /sessions/{id}/overview.
//
// It resolves the session's cwd, locates the CURRENT Claude transcript (newest-mtime
// .jsonl for that cwd), and parses it into per-turn metrics + an aggregate summary +
// session detail — the payload behind the shared @ce OverviewPanel.
//
// Unknown session → 404 (mirrors handleGetSession). A live session whose tool is codex
// (or that has no transcript yet) → 200 with a valid-but-empty shape (turn_count 0),
// never an error — the panel just shows zeros and "—" for the non-derivable metrics.
func (s *Server) handleSessionOverview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.mgr.Get(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	sess.mu.Lock()
	cwd := sess.CWD
	title := sessionTitle(sess)
	// active = the PTY session is still alive (not exited). The transcript-level "agent
	// running right now" nuance lives in agentintel; at the session layer this is the
	// honest, cheap signal.
	active := sess.Status != StatusExited
	sess.mu.Unlock()

	pl := agentintel.NewProjectLocator()
	metrics := agentintel.SessionMetricsForCWD(pl, cwd, id, title, active)
	writeJSON(w, http.StatusOK, metrics)
}
