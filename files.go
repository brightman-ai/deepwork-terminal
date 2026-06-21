package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// rawPreviewMaxBytes caps how large a file /files/raw will stream as text. Larger
// files return {tooLarge:true,size} metadata instead — the drawer's preview is for
// glancing at agent产物 (md/code), not downloading blobs.
const rawPreviewMaxBytes = 1 << 20 // 1 MiB

// imagePreviewMaxBytes caps how large an image /files/raw streams inline. Images get a
// larger budget than text (rawPreviewMaxBytes) because screenshots routinely exceed
// 1 MiB; past this they return {tooLarge} like any other oversized file.
const imagePreviewMaxBytes = 10 << 20 // 10 MiB

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
}

// searchMaxResults caps how many hits /files/search returns — a quick-open list, not a
// full index dump. searchMaxScan caps how many tree entries we walk before stopping, so
// a giant subtree can't hang the request.
const (
	searchMaxResults = 200
	searchMaxScan    = 20000
)

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

// handleFilesRecent handles GET /files/recent?session=<id>.
//
// It resolves the session's cwd, asks agentintel for the files agents recently
// wrote/edited (transcript tool_use signal, newest-first, ≤30), then stats each for
// size + existence. A vanished file is still listed with exists:false — it may have
// been deleted and the历史信号 is still useful. A bad/absent session → 200 with an
// empty list (the drawer just shows nothing), matching the soft-fail style of /inputs.
func (s *Server) handleFilesRecent(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
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
	cwd, ok := s.workbenchCWD(r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
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
	cwd, ok := s.workbenchCWD(r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
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
	// WalkDir does NOT follow symlinks, so traversal stays within the real subtree.
	_ = filepath.WalkDir(cwd, func(path string, d os.DirEntry, err error) error {
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
		if scanned > searchMaxScan || len(out) >= searchMaxResults {
			return filepath.SkipDir // stop descending; WalkDir keeps the walk bounded
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

	// Cap (the SkipDir guard stops descent, but a wide single level can still overshoot).
	if len(out) > searchMaxResults {
		out = out[:searchMaxResults]
	}
	// Directories first, then by rel path — a stable, scannable quick-open order.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Rel < out[j].Rel
	})

	writeJSON(w, http.StatusOK, searchResponse{Entries: out})
}

// handleFilesRaw handles GET /files/raw?session=<id>&path=<rel>.
//
// Same path safety as /files/tree. A directory → 400. An oversized file →
// {tooLarge:true,size} (200) (1MiB for text, 10MiB for images). A raster image with a
// known extension is streamed with its image/* Content-Type so the client renders it
// inline (<img>). Any other binary (NUL byte in the first 8KiB, or a non-text
// content-type) → {binary:true,size} (200). Otherwise the text bytes are streamed as
// text/plain with a no-cache header (the file on disk is mutable — agents may rewrite
// it between previews).
func (s *Server) handleFilesRaw(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.URL.Query().Get("session"), r.URL.Query().Get("cwd"))
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
	imgCT := imageContentType(strings.ToLower(filepath.Ext(target)))

	// Images get a larger inline budget than text (screenshots often exceed 1 MiB).
	sizeCap := int64(rawPreviewMaxBytes)
	if imgCT != "" {
		sizeCap = imagePreviewMaxBytes
	}
	if info.Size() > sizeCap {
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

// sessionCWD resolves a session id to its working directory. ok=false when the id
// is empty or the session is unknown.
func (s *Server) sessionCWD(sessionID string) (cwd string, ok bool) {
	if sessionID == "" {
		return "", false
	}
	sess, err := s.mgr.Get(sessionID)
	if err != nil {
		return "", false
	}
	return sess.CWD, true
}

// workbenchCWD resolves the working directory for a workbench request (files + overview).
// It PREFERS an explicit `cwd` — the frontend supplies the LIVE active tmux pane's cwd so
// the workbench follows pane/window switches — when that cwd is an absolute, existing
// directory; otherwise it falls back to the session's creation cwd. Trust model is local
// single-user: the caller already holds the auth code and full shell access, so honouring
// a real existing directory is no escalation, and tree/raw still confine traversal within
// the resolved root.
func (s *Server) workbenchCWD(sessionID, cwdParam string) (string, bool) {
	if cwdParam != "" && filepath.IsAbs(cwdParam) {
		if info, err := os.Stat(cwdParam); err == nil && info.IsDir() {
			return cwdParam, true
		}
	}
	// No usable client cwd. For tmux the client always sends the active pane's cwd
	// (pane_current_path); a non-tmux terminal has no such signal, so resolve the shell's
	// LIVE cwd from /proc — uploads + browsing then follow `cd` instead of the static launch
	// dir. Falls back to the session's creation cwd off-Linux or when the link can't be read.
	if sess, err := s.mgr.Get(sessionID); err == nil {
		if live := liveShellCWD(sess); live != "" {
			return live, true
		}
		return sess.CWD, true
	}
	return "", false
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
