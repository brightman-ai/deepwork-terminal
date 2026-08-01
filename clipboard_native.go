package terminal

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/brightman-ai/kit/obs"
)

// ── zero-copy paste ──────────────────────────────────────────────────────────────────
//
// When you copy a file in Finder and paste it into the web terminal, the browser hands
// the page a File OBJECT: bytes, no path. Uploading those bytes is the only thing a
// REMOTE page can do — and for a 156 MB recording it is also the wrong thing, because
// the file is already sitting on this very disk. The paste resolver therefore asks the
// SERVER what is on its own system clipboard first (frontend/src/composables/cli/
// useCliPasteResolver.ts → readNativeClipboardPaths), and if it gets paths back it types
// `@/abs/path` into the PTY. Zero bytes move; the agent opens the original file in place.
//
// That probe has been shipping against a route this repo never served. Standalone
// deepwork-terminal answered 404, the resolver logged cli.clipboard.native_probe_empty
// and silently fell back to uploading — straight into the size cap, which is how a local
// paste of local files came back as "file exceeds the 10 MB limit". deepwork-pro serves
// the same URL (internal/webui/browser_routes.go), so the shared frontend was correct and
// only this deployment was missing. The path keeps pro's `/browser/…` spelling because the
// URL is the contract the shared page compiled against, not because a browser is involved.
//
// The clipboard read itself is per-platform (clipboard_native_darwin.go and
// clipboard_native_other.go); everything policy-shaped lives here.

// nativeClipboardTimeout bounds the platform read. macOS answers in ~0.5 s (osascript
// process start dominates; the pasteboard call itself is microseconds) and a cold start
// has been measured near 1.8 s, so 3 s is "something is wrong" rather than "slow today".
// A paste is an interactive gesture — failing fast to the upload fallback beats holding
// the keystroke hostage.
const nativeClipboardTimeout = 3 * time.Second

// handleClipboardFilePaths serves GET /browser/clipboard/files → {"paths": [...]}.
//
// 403 is meaningful to the caller: the resolver counts it as nativeRejected and stops
// treating the native path as available, instead of retrying every paste.
func (s *Server) handleClipboardFilePaths(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logCtx := obs.WithStage(r.Context(), stgTerminalClipboard)
	terminalClipboardNativeProbes.Inc()

	// Same machine or nothing. This reads the SERVER's clipboard, so for a remote page it
	// would be someone else's clipboard — an information leak, and useless anyway since a
	// path from this disk means nothing over there.
	if !requestIsSameMachine(r) {
		terminalClipboardNativeRejected.Inc()
		terminalLogger.Info(logCtx, "cli clipboard native probe rejected",
			"reason", "not_same_machine",
			"remote_addr", r.RemoteAddr,
			"elapsed_ms", time.Since(start).Milliseconds())
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "native clipboard requires a same-machine client",
		})
		return
	}

	ctx, cancel := context.WithTimeout(logCtx, nativeClipboardTimeout)
	defer cancel()
	paths, err := readClipboardFilePaths(ctx)
	if err != nil {
		terminalClipboardNativeErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard native probe failed",
			"elapsed_ms", time.Since(start).Milliseconds(),
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	usable, dropped := existingPaths(paths)
	terminalClipboardNativeDuration.Observe(time.Since(start).Seconds())
	if len(usable) > 0 {
		terminalClipboardNativeHits.Inc()
	}
	terminalLogger.Info(logCtx, "cli clipboard native probe completed",
		"count", len(usable),
		"dropped_missing", dropped,
		"elapsed_ms", time.Since(start).Milliseconds())

	// Always 200 with a (possibly empty) list: "the clipboard holds no files" is a normal
	// answer, not a failure, and the resolver's own fallback ladder handles it.
	writeJSON(w, http.StatusOK, map[string]any{"paths": usable})
}

// existingPaths drops clipboard entries that no longer exist on disk and reports how many
// it dropped. A path is only useful here because the agent will open it; handing back one
// that has since been moved or deleted would inject a broken `@reference` that fails LATER,
// far from the paste, whereas dropping it lets the resolver fall back to uploading the
// bytes the browser still holds. The drop count is logged rather than swallowed — if it is
// ever non-zero in the field, the platform reader is returning something we don't expect.
func existingPaths(paths []string) ([]string, int) {
	usable := make([]string, 0, len(paths))
	dropped := 0
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			dropped++
			continue
		}
		usable = append(usable, p)
	}
	return usable, dropped
}
