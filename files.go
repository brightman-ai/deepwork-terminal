package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// rawPreviewMaxBytes caps how large a file /files/raw streams for inline preview —
// text and images alike. Past it, the response is {tooLarge:true,size} and the reader
// falls back to 下载. Raised 1 MiB → 10 MiB for text: real agent 产物 (meeting
// transcripts, long logs) routinely clear 1 MiB, and refusing to show them is worse
// than a slow render. Images already had this budget (screenshots exceed 1 MiB), so the
// two are now one number.
const rawPreviewMaxBytes = 10 << 20 // 10 MiB

// binarySniffBytes is how many leading bytes we inspect for a NUL byte when
// deciding text-vs-binary.
const binarySniffBytes = 8 << 10 // 8 KiB

// recentFileItem is one entry in GET /files/recent. It mirrors agentintel.RecentFile
// plus the freshly-stat'd Size/Exists (vanished files are still listed, exists:false).
type recentFileItem struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Tool   string `json:"tool"`
	TsMs   int64  `json:"tsMs"`
	Size   int64  `json:"size"`
	Exists bool   `json:"exists"`
}

type recentFilesResponse struct {
	Items []recentFileItem `json:"items"`
}

// treeEntry is one child in GET /files/tree (single directory level).
type treeEntry struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	MtimeMs int64  `json:"mtimeMs"`
}

type treeResponse struct {
	CWD     string      `json:"cwd"`
	Rel     string      `json:"rel"`
	Entries []treeEntry `json:"entries"`
}

// searchEntry is one hit in GET /files/search — a treeEntry plus the path RELATIVE
// to the search root (forward slashes), so the client can preview/navigate it without
// re-deriving the location.
type searchEntry struct {
	Name    string `json:"name"`
	Rel     string `json:"rel"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	MtimeMs int64  `json:"mtimeMs"`
}

type searchResponse struct {
	Entries []searchEntry `json:"entries"`
	// Truncated is true when the walk hit a cap (too many hits or a tree larger than
	// searchMaxScan) and stopped early, so the result set is incomplete. The client
	// surfaces this so a huge tree (e.g. a monorepo cwd) reads as "narrow your search",
	// not "no such file" — silent truncation otherwise hides files that exist.
	Truncated bool `json:"truncated,omitempty"`
}

// errSearchBudget aborts the search WalkDir once a cap is hit. Returning filepath.SkipDir
// from a FILE entry only skips its siblings — the walk keeps grinding the rest of a giant
// tree (slow). A sentinel error returned from the walk fn stops WalkDir immediately.
var errSearchBudget = errors.New("search budget exhausted")

// searchMaxResults caps how many hits /files/search returns — a quick-open list, not a
// full index dump. searchMaxScan caps how many tree entries we walk before stopping, so
// a giant subtree can't hang the request.
const (
	// searchMaxResults is how many ranked hits the client gets (a quick-open list, not an index).
	searchMaxResults = 200
	// searchCollectCap is how many raw hits we gather during the walk BEFORE ranking + trimming
	// to searchMaxResults. Collecting more than we return means a common term ("graph", 200+
	// matches) no longer gets truncated to the first N in WALK order and buries the file you
	// actually want — we rank the fuller set, then keep the top searchMaxResults.
	searchCollectCap = 1200
	// searchMaxScan caps tree entries walked before giving up (was 20000 — too small for real
	// project trees, which silently truncated files that exist, e.g. late-sorted tmp/).
	searchMaxScan = 120000
)

// searchTimeBudget bounds a search's wall-clock so a giant tree can't hang the request even
// under the raised scan cap; hitting it marks the result truncated (partial, not "not found").
const searchTimeBudget = 2500 * time.Millisecond

// searchSkipDirs are directory names we never descend into — build artifacts, vendored
// deps, caches and VCS internals that bury real source under tens of thousands of files.
// `.git` is always skipped; other dot-dirs (.github, .vscode, …) are still walked.
var searchSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"vendor":       true,
	"target":       true,
	".next":        true,
	".cache":       true,
	".idea":        true,
}

// matchesFuzzy reports whether name contains EVERY (already-lowercased) term as a substring,
// order-independent — the gap-tolerant rule shared with the client fuzzyMatch SSOT so
// "test iso" matches "tmux-test-isolation". terms must be pre-lowercased + non-empty.
func matchesFuzzy(terms []string, name string) bool {
	ln := strings.ToLower(name)
	for _, t := range terms {
		if !strings.Contains(ln, t) {
			return false
		}
	}
	return true
}

// searchScore ranks a matched hit for the query terms so the most relevant survive the
// trim to searchMaxResults: a term at the name's start beats one at a word boundary beats an
// incidental mid-substring, with a mild penalty for longer names (a tight match outranks a
// coincidental one). This is why "graph" surfaces "XGraph…docx" instead of burying it under
// the first N "paragraph"/"graphql" hits the walk happened to reach.
func searchScore(name string, terms []string) int {
	ln := strings.ToLower(name)
	score := 0
	for _, t := range terms {
		idx := strings.Index(ln, t)
		switch {
		case idx < 0:
			// term not in the (single) name — only happens for dirs matched via a parent; ignore.
		case idx == 0:
			score += 100
		case isWordBoundary(name, idx):
			score += 60
		default:
			score += 20
		}
	}
	return score - len(ln)/12
}

// isWordBoundary reports whether the match at byte idx begins a "word" within name — right
// after a separator (-_. /) or at a camelCase hump (so "graph" scores as a word start in
// "XGraph"). Operates on the ORIGINAL-case name so casing humps are visible.
func isWordBoundary(name string, idx int) bool {
	if idx <= 0 || idx >= len(name) {
		return false
	}
	switch name[idx-1] {
	case '-', '_', '.', ' ', '/':
		return true
	}
	cur := name[idx]
	// Upper-case char after a non-upper, OR standing after a lone capital (X|Graph): a hump.
	if cur >= 'A' && cur <= 'Z' {
		prev := name[idx-1]
		return !(prev >= 'A' && prev <= 'Z' && idx >= 2 && name[idx-2] >= 'A' && name[idx-2] <= 'Z')
	}
	return false
}

// attachmentDisposition builds a Content-Disposition value that survives non-ASCII filenames
// (e.g. Chinese .docx names): an ASCII-sanitized fallback for old clients plus the RFC 5987
// UTF-8 form modern browsers prefer.
func attachmentDisposition(name string) string {
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r == '"' || r == '\\' || r > 0x7e {
			ascii = append(ascii, '_')
		} else {
			ascii = append(ascii, r)
		}
	}
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", string(ascii), url.PathEscape(name))
}

// handleFilesRecent handles GET /files/recent?session=<id>.
//
// It resolves the session's cwd, asks agentintel for the files agents recently
// wrote/edited (transcript tool_use signal, newest-first, ≤30), then stats each for
// size + existence. A vanished file is still listed with exists:false — it may have
// been deleted and the历史信号 is still useful. A bad/absent session → 200 with an
// empty list (the drawer just shows nothing), matching the soft-fail style of /inputs.
func (s *Server) handleFilesRecent(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusOK, recentFilesResponse{Items: []recentFileItem{}})
		return
	}

	// Two signals, merged: (1) agent edits from the project's recent transcripts (carry tool
	// attribution + precise ts), (2) files uploaded into this cwd (clipboard/attachment — not
	// Write/Edit tool_use, so the transcript can't see them). Transcript entries win on dedup.
	pl := agentintel.NewProjectLocator()
	seen := make(map[string]bool)
	var items []recentFileItem
	push := func(it recentFileItem) {
		if it.Path == "" || seen[it.Path] {
			return
		}
		seen[it.Path] = true
		items = append(items, it)
	}
	// Only surface files the preview endpoint can actually open. A path whose real
	// (symlink-resolved) location escapes the session root 403s on preview, so listing
	// it produced the "shows in 最近文件 but 预览失败" mismatch (e.g. docs/ symlinked to a
	// sibling repo). Gate with the SAME rule as resolvePreviewPath; knownRecent=true for
	// transcript entries skips isRecentFile's transcript rescan.
	for _, f := range agentintel.RecentEditedFiles(pl, cwd) {
		if !previewableInRecent(cwd, f.Path, true) {
			continue
		}
		push(recentFileItem{Path: f.Path, Name: f.Name, Dir: f.Dir, Tool: f.Tool, TsMs: f.TsMs})
	}
	for _, u := range s.uploads.list() {
		if u.CWD != cwd || !previewableInRecent(cwd, u.AbsPath, false) {
			continue
		}
		push(recentFileItem{Path: u.AbsPath, Name: u.Name, Dir: filepath.Dir(u.AbsPath), Tool: "upload", TsMs: u.MtimeMs})
	}

	// Newest-first across both signals, then cap, then stat only the survivors.
	sort.SliceStable(items, func(i, j int) bool { return items[i].TsMs > items[j].TsMs })
	if len(items) > agentintel.RecentFilesCap {
		items = items[:agentintel.RecentFilesCap]
	}
	for i := range items {
		if info, err := os.Stat(items[i].Path); err == nil && !info.IsDir() {
			items[i].Size = info.Size()
			items[i].Exists = true
		}
	}
	if items == nil {
		items = []recentFileItem{}
	}
	writeJSON(w, http.StatusOK, recentFilesResponse{Items: items})
}

// handleFilesTree handles GET /files/tree?session=<id>&path=<rel>.
//
// Lists ONE directory level under the session cwd. Path safety: the rel path is
// cleaned, joined onto cwd, then symlink-resolved and verified to stay within the
// cwd subtree — `..` escape / absolute / symlink-out all yield 403.
func (s *Server) handleFilesTree(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rel := r.URL.Query().Get("path")
	target, err := safeResolve(cwd, rel)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "directory not found"})
		return
	}

	out := make([]treeEntry, 0, len(entries))
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		te := treeEntry{
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			MtimeMs: info.ModTime().UnixMilli(),
		}
		if !e.IsDir() {
			te.Size = info.Size()
		}
		out = append(out, te)
	}
	// Dirs first, then by name (case-insensitive) for a stable, scannable order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	writeJSON(w, http.StatusOK, treeResponse{
		CWD:     cwd,
		Rel:     cleanRel(rel),
		Entries: out,
	})
}

// handleFilesSearch handles GET /files/search?session=<id>&cwd=<dir>&q=<query>.
//
// It recursively walks the resolved cwd subtree and returns files AND directories whose
// NAME contains q (case-insensitive) — VS-Code quick-open style. Noise directories
// (build artifacts / vendored deps / caches, see searchSkipDirs) are skipped entirely so
// a real project's source isn't buried. Results cap at searchMaxResults; the walk caps at
// searchMaxScan entries so a giant tree can't hang the request. An empty/unknown cwd or an
// empty query → 200 with an empty list (soft-fail, like the other /files/* handlers).
func (s *Server) handleFilesSearch(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusOK, searchResponse{Entries: []searchEntry{}})
		return
	}
	// Gap-tolerant match: split the query into whitespace-separated terms; an entry name must
	// contain EVERY term (case-insensitive, order-independent), so "test iso" finds
	// "tmux-test-isolation". Mirrors the client fuzzyMatch SSOT.
	terms := strings.Fields(strings.ToLower(r.URL.Query().Get("q")))
	if len(terms) == 0 {
		writeJSON(w, http.StatusOK, searchResponse{Entries: []searchEntry{}})
		return
	}

	out := make([]searchEntry, 0, 64)
	scanned := 0
	truncated := false
	deadline := time.Now().Add(searchTimeBudget)
	// WalkDir does NOT follow symlinks, so traversal stays within the real subtree.
	walkErr := filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Unreadable dir/file: skip it (or its subtree) but keep walking the rest.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == cwd {
			return nil // never match/emit the root itself
		}
		if d.IsDir() && searchSkipDirs[d.Name()] {
			return filepath.SkipDir
		}
		scanned++
		// Budget hit → abort the ENTIRE walk via sentinel, not filepath.SkipDir. SkipDir on a
		// file entry only skips its siblings, so the walk would keep grinding a giant tree
		// after we stopped emitting (slow) AND starve late-sorted dirs (e.g. tmp/) of matches.
		// Stop on scan count, collected-hit count, OR wall-clock (checked every 1024 entries) —
		// whichever comes first. Collect up to searchCollectCap (> searchMaxResults) so ranking
		// has a fuller set to pick the best from.
		if scanned > searchMaxScan || len(out) >= searchCollectCap || (scanned%1024 == 0 && time.Now().After(deadline)) {
			truncated = true
			return errSearchBudget
		}
		if !matchesFuzzy(terms, d.Name()) {
			return nil
		}
		rel, rerr := filepath.Rel(cwd, path)
		if rerr != nil {
			return nil
		}
		entry := searchEntry{
			Name:  d.Name(),
			Rel:   filepath.ToSlash(rel),
			IsDir: d.IsDir(),
		}
		if info, ierr := d.Info(); ierr == nil {
			entry.MtimeMs = info.ModTime().UnixMilli()
			if !d.IsDir() {
				entry.Size = info.Size()
			}
		}
		out = append(out, entry)
		return nil
	})

	// A real walk error (not our stop sentinel) means traversal ended early → partial results.
	if walkErr != nil && !errors.Is(walkErr, errSearchBudget) {
		truncated = true
	}
	// Rank the collected hits by relevance, then keep the top searchMaxResults — so the file
	// you want surfaces even when a common term matched far more than we return. Score desc,
	// then dirs first, then newest, then rel path for a stable, scannable order.
	scored := make([]struct {
		e searchEntry
		s int
	}, len(out))
	for i, e := range out {
		scored[i].e = e
		scored[i].s = searchScore(e.Name, terms)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].s != scored[j].s {
			return scored[i].s > scored[j].s
		}
		if scored[i].e.IsDir != scored[j].e.IsDir {
			return scored[i].e.IsDir
		}
		if scored[i].e.MtimeMs != scored[j].e.MtimeMs {
			return scored[i].e.MtimeMs > scored[j].e.MtimeMs
		}
		return scored[i].e.Rel < scored[j].e.Rel
	})
	if len(scored) > searchMaxResults {
		scored = scored[:searchMaxResults]
		truncated = true
	}
	out = make([]searchEntry, len(scored))
	for i := range scored {
		out[i] = scored[i].e
	}

	writeJSON(w, http.StatusOK, searchResponse{Entries: out, Truncated: truncated})
}

// handleFilesRaw handles GET /files/raw?session=<id>&path=<rel>.
//
// Same path safety as /files/tree. A directory → 400. An oversized file →
// {tooLarge:true,size} (200) (10 MiB, text and images alike). A raster image with a
// known extension is streamed with its image/* Content-Type so the client renders it
// inline (<img>). Any other binary (NUL byte in the first 8KiB, or a non-text
// content-type) → {binary:true,size} (200). Otherwise the text bytes are streamed as
// text/plain with a no-cache header (the file on disk is mutable — agents may rewrite
// it between previews).
func (s *Server) handleFilesRaw(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	rel := r.URL.Query().Get("path")
	target, err := resolvePreviewPath(cwd, rel)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "is a directory"})
		return
	}

	// Download mode: stream the FULL bytes as an attachment for ANY file — text, image, binary,
	// or oversized — bypassing the inline-preview size caps and text/binary body-shape sniffing.
	// The preview shapes (text/image/{binary}/{tooLarge}) are for glancing; download is the escape
	// hatch to actually get the file (FilesPanel 下载 button, works for un-previewable formats too).
	if r.URL.Query().Get("download") == "1" {
		f, ferr := os.Open(target)
		if ferr != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", attachmentDisposition(filepath.Base(target)))
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
		return
	}

	imgCT := imageContentType(strings.ToLower(filepath.Ext(target)))

	if info.Size() > rawPreviewMaxBytes {
		writeJSON(w, http.StatusOK, map[string]any{"tooLarge": true, "size": info.Size()})
		return
	}

	f, err := os.Open(target)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer f.Close()

	// Sniff the head to decide text-vs-binary before committing to a body shape.
	head := make([]byte, binarySniffBytes)
	n, _ := io.ReadFull(f, head)
	if n < 0 {
		n = 0
	}
	head = head[:n]

	// A known image extension whose bytes really are binary → stream it with its image
	// content-type so the client renders <img>. A ".png" that's actually text (not binary)
	// falls through to the text path, so a mislabeled file still previews gracefully.
	if imgCT != "" && isBinaryContent(head) {
		w.Header().Set("Content-Type", imgCT)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(head)
		_, _ = io.Copy(w, f) // stream the remainder past the sniffed head
		return
	}

	if isBinaryContent(head) {
		writeJSON(w, http.StatusOK, map[string]any{"binary": true, "size": info.Size()})
		return
	}

	// Serve every previewable file as plain text: the drawer renders it read-only with
	// extension-based highlighting (FilePreview), so the precise mime is irrelevant — and
	// forcing text/plain both avoids the browser executing/interpreting a .html and keeps a
	// real .json from colliding with the application/json {binary}/{tooLarge} sentinels the
	// client distinguishes by content-type.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// File is mutable (an agent may rewrite it) — never cache the preview.
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(head)
	_, _ = io.Copy(w, f) // stream the remainder past the sniffed head
}

// resolvePreviewPath resolves a /files/raw preview path. In-cwd paths (tree navigation,
// search results, recent files under cwd) use the traversal-safe safeResolve. An ABSOLUTE
// path OUTSIDE cwd is allowed ONLY when it is a file the agent actually touched — i.e. a
// member of the transcript-derived recent-edited set. Recent files legitimately live
// outside the workbench root (/tmp scratch, sibling repos), and without this an absolute
// outside-cwd path is collapsed INTO cwd by safeResolve → a 404. Arbitrary path traversal
// stays blocked: only the bounded, agent-touched allowlist escapes the root.
func resolvePreviewPath(cwd, rel string) (string, error) {
	if filepath.IsAbs(rel) && !pathUnder(cwd, rel) {
		if isRecentFile(cwd, rel) {
			return filepath.Clean(rel), nil
		}
		return "", errors.New("path not allowed")
	}
	return safeResolve(cwd, rel)
}

// previewableInRecent reports whether path would pass resolvePreviewPath's gate, so
// the 最近文件 list stays in lock-step with what preview can open (no "listed but
// 预览失败"). It mirrors resolvePreviewPath but takes knownRecent instead of calling
// isRecentFile — the caller already knows whether the path came from the recent set,
// which avoids a redundant (and potentially recursive) transcript rescan.
func previewableInRecent(cwd, path string, knownRecent bool) bool {
	if filepath.IsAbs(path) && !pathUnder(cwd, path) {
		return knownRecent // outside-cwd absolute path: preview allows it iff it's a recent file
	}
	_, err := safeResolve(cwd, path) // anchors + symlink-resolves + rejects root escapes
	return err == nil
}

// isRecentFile reports whether abs is in the session cwd's recent-edited file set (the
// transcript tool_use signal that feeds /files/recent) — the allowlist that lets a file
// outside cwd be previewed.
func isRecentFile(cwd, abs string) bool {
	abs = filepath.Clean(abs)
	pl := agentintel.NewProjectLocator()
	for _, f := range agentintel.RecentEditedFiles(pl, cwd) {
		if filepath.Clean(f.Path) == abs {
			return true
		}
	}
	return false
}

// workbenchCWD resolves the working directory for a workbench request (files / overview /
// git-diff / paste). "The active pane's working dir" is a SINGLE fact with ONE authority: the
// tmux state's pane_current_path — the same snapshot GET /tmux/state + the WS push already
// serve — so the workbench anchors to exactly the pane the UI shows as active. Resolution:
//
//  1. explicit client cwd override (absolute, existing dir): a deliberate anchor — the per-pane
//     drawer's owning-pane cwd, or a LOCK-mode frozen snapshot. Honoured as-is.
//  2. tmux authority: the attached session's active-window active-pane CWD. THIS is the fix — a
//     client that sends no cwd (activeCwd is still '' during detach / the first WS frame) now
//     anchors correctly instead of falling through to the tmux launch dir. Read from the same
//     TmuxState the display consumes, so the front/back views can't drift.
//  3. non-tmux standalone: the shell's live /proc cwd (correct — the shell IS the pane, and it
//     follows `cd`). For a tmux session step 2 always wins, so this is NEVER reached there —
//     which is what removes the old "/home/ubuntu" (tmux launch dir) degradation that silently
//     broke recent-files / overview / git-diff / paste-target alike.
//  4. the session's creation cwd (last resort).
//
// Trust model is local single-user: the caller already holds the auth code + full shell access,
// so honouring a real existing directory is no escalation; tree/raw still confine traversal
// within the resolved root.
func (s *Server) workbenchCWD(ctx context.Context, sessionID, cwdParam string) (string, bool) {
	cwd, source, ok := s.resolveWorkbenchCWD(ctx, sessionID, cwdParam)
	// One structured line makes "which dir did the workbench anchor to, and from which source"
	// auditable — the absence of exactly this was why the /home/ubuntu degradation stayed
	// invisible until an endpoint was hand-probed. Debug level: opt-in, never noisy.
	if ok {
		slog.Debug("workbench cwd resolved", "session", sessionID, "cwd", cwd, "source", source)
	}
	return cwd, ok
}

// resolveWorkbenchCWD is workbenchCWD's pure core: it returns the resolved dir plus the SOURCE
// it came from ("override" | "tmux-active" | "proc" | "session"), split out so the source is
// available for the log line above (and unit tests) without threading it through every caller.
func (s *Server) resolveWorkbenchCWD(ctx context.Context, sessionID, cwdParam string) (cwd, source string, ok bool) {
	if cwdParam != "" && filepath.IsAbs(cwdParam) {
		if info, err := os.Stat(cwdParam); err == nil && info.IsDir() {
			return cwdParam, "override", true
		}
	}
	sess, err := s.mgr.Get(sessionID)
	if err != nil {
		return "", "", false
	}
	if pc := s.activePaneCWD(ctx, sess.ShellPID()); pc != "" {
		return pc, "tmux-active", true
	}
	if live := liveShellCWD(sess); live != "" {
		return live, "proc", true
	}
	return sess.CWD, "session", true
}

// activePaneCWD returns the CWD of the active pane of the tmux session the given shell is
// attached to — read from the SAME TmuxState snapshot GET /tmux/state + the WS push serve, so
// the workbench anchors to exactly the pane the UI marks active (its pane_current_path, which
// the frontend's useTmuxState.activeCwd mirrors). Returns "" when the shell isn't attached
// inside tmux, no active pane carries a cwd, or the snapshot can't be read/parsed — callers
// then fall back to the shell's own /proc cwd. Parses the full snapshot for just the one field
// it needs; the provider already computes it on the ~1s WS budget.
func (s *Server) activePaneCWD(ctx context.Context, shellPID int) string {
	if shellPID <= 0 || s.tmuxProvider == nil {
		return ""
	}
	raw, err := s.tmuxProvider.TmuxState(ctx, shellPID)
	if err != nil {
		return ""
	}
	var st struct {
		AttachedSession string `json:"attachedSession"`
		Sessions        []struct {
			Name    string `json:"name"`
			Windows []struct {
				Active bool `json:"active"`
				Panes  []struct {
					Active bool   `json:"active"`
					CWD    string `json:"cwd"`
				} `json:"panes"`
			} `json:"windows"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return ""
	}
	// Scope to the session THIS shell is attached to (mirrors the frontend, which scopes its
	// windows by attachedSession). Not attached → no tmux anchor; caller falls back to /proc.
	if st.AttachedSession == "" {
		return ""
	}
	for _, sess := range st.Sessions {
		if sess.Name != st.AttachedSession {
			continue
		}
		for _, win := range sess.Windows {
			if !win.Active {
				continue
			}
			for _, pane := range win.Panes {
				if pane.Active && pane.CWD != "" {
					return pane.CWD
				}
			}
		}
	}
	return ""
}

// liveShellCWD returns the current working directory of the session's shell process via
// /proc/<pid>/cwd (Linux/WSL). Returns "" off-Linux, on error, or if the target isn't a
// directory, so callers fall back to the static session cwd. NOTE: for a tmux session this
// is tmux's own cwd, not the active pane's — callers must prefer the client-supplied pane
// cwd first (workbenchCWD does).
func liveShellCWD(sess *Session) string {
	pid := sess.ShellPID()
	if pid <= 0 {
		return ""
	}
	dir, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// safeResolve joins a client-supplied relative path onto the session cwd and proves
// the result stays within the cwd subtree. It defends against `..` escape, absolute
// paths, and symlinks that point outside the root:
//  1. Reject any path containing a `..` component outright (per the /files/* contract:
//     拒绝绝对路径 / `..` 逃逸) — we never silently clamp it.
//  2. Clean("/"+rel) anchors the path so an absolute rel can't escape the cwd.
//  3. Join onto cwd, then EvalSymlinks the deepest existing ancestor.
//  4. Verify the resolved path is cwd itself or a descendant of cwd (catches a
//     symlinked ancestor that points outside the tree).
//
// Returns an error (→ 403 at the handler) on any escape.
func safeResolve(cwd, rel string) (string, error) {
	// Explicitly reject `..` traversal rather than clamping it — the contract says
	// reject, and an explicit 403 is clearer than silently resolving to cwd.
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return "", errors.New("path contains ..")
		}
	}

	// A full absolute path that ALREADY sits inside cwd (the 最近文件 list carries
	// agent-attributed absolute paths) is taken as-is. Everything else — a relative 目录树
	// path, or an absolute path outside cwd like "/sub/f.txt" — is anchored under cwd so it
	// can only ever address something within the tree. `..` was already rejected above.
	var target string
	if filepath.IsAbs(rel) && pathUnder(cwd, rel) {
		target = filepath.Clean(rel)
	} else {
		target = filepath.Join(cwd, filepath.Clean("/"+rel))
	}

	// Resolve symlinks on the real cwd and on whatever part of target exists, so a
	// symlink pointing outside the tree is caught. Non-existent leaves are fine —
	// we resolve the nearest existing ancestor and re-append the missing tail.
	realCWD, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", err
	}
	realTarget, err := evalExistingPrefix(target)
	if err != nil {
		return "", err
	}

	if realTarget != realCWD && !strings.HasPrefix(realTarget, realCWD+string(os.PathSeparator)) {
		return "", errors.New("path escapes session root")
	}
	return realTarget, nil
}

// pathUnder reports whether the cleaned absolute path p is cwd itself or a descendant of
// cwd — a lexical pre-check that lets a genuine in-tree absolute path skip cwd-anchoring
// (the EvalSymlinks confinement check in safeResolve remains the real security gate).
func pathUnder(cwd, p string) bool {
	c := filepath.Clean(cwd)
	cp := filepath.Clean(p)
	return cp == c || strings.HasPrefix(cp, c+string(os.PathSeparator))
}

// evalExistingPrefix EvalSymlinks the longest existing prefix of p, then rejoins the
// non-existent tail. This lets us safety-check a path that doesn't fully exist yet
// while still catching a symlinked ancestor that escapes the tree.
func evalExistingPrefix(p string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", errors.New("cannot resolve path")
	}
	resolvedParent, err := evalExistingPrefix(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(p)), nil
}

// cleanRel returns the normalized, cwd-relative path for echoing back to the client
// (leading slash stripped; "." for the root).
func cleanRel(rel string) string {
	cleaned := filepath.Clean("/" + rel)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return "."
	}
	return cleaned
}

// isBinaryContent reports whether the sniffed head looks binary: a NUL byte in the
// first bytes, or a content-type that is neither text/* nor a known text-ish type.
// An empty/unknown content-type is treated as text (we already checked for NULs).
// isBinaryContent decides text-vs-binary from the CONTENT, not the extension/mime — a
// .sh is plain text even though its mime is application/x-sh, and an extension whitelist
// forever misses variants (.zsh/.conf/.env/Dockerfile/…). A NUL byte is the definitive
// binary signal; otherwise a high ratio of non-whitespace CONTROL bytes means binary.
// Printable ASCII and any byte ≥0x80 (UTF-8 lead/continuation) read as text, so source,
// scripts, configs and UTF-8 docs all preview.
func isBinaryContent(head []byte) bool {
	if len(head) == 0 {
		return false // empty file → previewable (blank), not binary
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	ctrl := 0
	for _, b := range head {
		switch {
		case b >= 0x20: // printable ASCII or UTF-8 (≥0x80)
		case b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v' || b == '\b' || b == 0x1b:
		default:
			ctrl++
		}
	}
	return float64(ctrl)/float64(len(head)) > 0.3
}

// imageContentType maps a lowercased file extension (with dot) to its image MIME type,
// or "" if it isn't a raster image the drawer previews inline. SVG is intentionally
// absent: it's XML (served + rendered as text), and an <img>-loaded SVG can smuggle
// markup. This is the backend SSOT for "served as an image"; the frontend groups files
// into the 图片 category by the matching extension list in FilesPanel.vue.
func imageContentType(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".avif":
		return "image/avif"
	default:
		return ""
	}
}
