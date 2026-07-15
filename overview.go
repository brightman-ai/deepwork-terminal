package terminal

import (
	"fmt"
	"net/http"
	"os"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// liveCWD returns the shell's CURRENT working directory via /proc/<pid>/cwd (Linux).
// Unlike the session's CREATION cwd, this follows the user's `cd`, so the overview
// finds the claude/codex transcript for the dir where the agent is ACTUALLY running
// even without tmux to report an active-pane cwd. "" on any error (non-Linux, gone, …).
func liveCWD(pid int) string {
	if pid <= 0 {
		return ""
	}
	dir, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return dir
}

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

	// cwd resolution, most→least specific:
	//   1. frontend-supplied active tmux pane cwd (follows pane/window switches),
	//   2. the session shell's LIVE cwd (/proc/<pid>/cwd) — follows `cd` even without
	//      tmux, so a plain-shell session still resolves the agent's actual dir,
	//   3. the session's creation cwd as a last resort.
	// Without (2) a non-tmux session that cd'd into a project resolved the stale creation
	// dir and the overview (and Files panel) came up empty.
	cwd := baseCWD
	if lc, ok := s.workbenchCWD(r.Context(), id, r.URL.Query().Get("cwd")); ok && lc != "" {
		cwd = lc
	} else if live := liveCWD(sess.ShellPID()); live != "" {
		cwd = live
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
