package terminal

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// handleClaudeTuiClassic persists Claude Code's `tui` setting to "classic" in the user's
// ~/.claude/settings.json. The "classic"/"inline" values render to the terminal's NORMAL screen
// buffer (not the alternate "fullscreen" screen, DECSET 1049), which is what restores tmux
// copy-mode + scroll-to-copy and native web-terminal text selection.
//
// The in-session `/tui default` keystroke (sent from the client) flips the LIVE claude process for
// the current run; this endpoint makes the choice STICK across future `claude` launches. It is the
// opt-in "remember" half of the advisory — only called when the user ticks that box.
//
// Every existing settings key is preserved verbatim (json.RawMessage round-trip); only `tui` is
// overridden. Keys may be re-ordered (Go marshals maps sorted) but values are byte-identical.
func (s *Server) handleClaudeTuiClassic(w http.ResponseWriter, r *http.Request) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot resolve home dir"})
		return
	}
	path := filepath.Join(home, ".claude", "settings.json")

	settings := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			// Refuse to clobber a file we can't safely round-trip (e.g. hand-edited with comments).
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "~/.claude/settings.json is not a plain JSON object; left untouched",
			})
			return
		}
	}
	settings["tui"] = json.RawMessage(`"classic"`)

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "marshal failed"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir failed"})
		return
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tui": "classic"})
}
