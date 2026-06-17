package terminal

import (
	"bytes"
	"errors"
	"io"
	"mime"
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

// handleFilesRecent handles GET /files/recent?session=<id>.
//
// It resolves the session's cwd, asks agentintel for the files agents recently
// wrote/edited (transcript tool_use signal, newest-first, ≤30), then stats each for
// size + existence. A vanished file is still listed with exists:false — it may have
// been deleted and the历史信号 is still useful. A bad/absent session → 200 with an
// empty list (the drawer just shows nothing), matching the soft-fail style of /inputs.
func (s *Server) handleFilesRecent(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.sessionCWD(r.URL.Query().Get("session"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusOK, recentFilesResponse{Items: []recentFileItem{}})
		return
	}

	pl := agentintel.NewProjectLocator()
	files := agentintel.RecentEditedFiles(pl, cwd)

	items := make([]recentFileItem, 0, len(files))
	for _, f := range files {
		item := recentFileItem{
			Path: f.Path,
			Name: f.Name,
			Dir:  f.Dir,
			Tool: f.Tool,
			TsMs: f.TsMs,
		}
		if info, err := os.Stat(f.Path); err == nil && !info.IsDir() {
			item.Size = info.Size()
			item.Exists = true
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, recentFilesResponse{Items: items})
}

// handleFilesTree handles GET /files/tree?session=<id>&path=<rel>.
//
// Lists ONE directory level under the session cwd. Path safety: the rel path is
// cleaned, joined onto cwd, then symlink-resolved and verified to stay within the
// cwd subtree — `..` escape / absolute / symlink-out all yield 403.
func (s *Server) handleFilesTree(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.sessionCWD(r.URL.Query().Get("session"))
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

// handleFilesRaw handles GET /files/raw?session=<id>&path=<rel>.
//
// Same path safety as /files/tree. A directory → 400. A file >1MiB →
// {tooLarge:true,size} (200). A binary file (NUL byte in the first 8KiB, or a
// non-text content-type) → {binary:true,size} (200). Otherwise the text bytes are
// streamed with a Content-Type derived from the extension and a no-cache header (the
// file on disk is mutable — agents may rewrite it between previews).
func (s *Server) handleFilesRaw(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.sessionCWD(r.URL.Query().Get("session"))
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

	info, err := os.Stat(target)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "is a directory"})
		return
	}
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

	ct := mime.TypeByExtension(filepath.Ext(target))
	if isBinaryContent(head, ct) {
		writeJSON(w, http.StatusOK, map[string]any{"binary": true, "size": info.Size()})
		return
	}

	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// File is mutable (an agent may rewrite it) — never cache the preview.
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(head)
	_, _ = io.Copy(w, f) // stream the remainder past the sniffed head
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

	// Anchor: treat rel as rooted so an absolute `/etc/...` collapses into the cwd.
	cleaned := filepath.Clean("/" + rel)
	target := filepath.Join(cwd, cleaned)

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
func isBinaryContent(head []byte, contentType string) bool {
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	if contentType == "" {
		return false
	}
	base := contentType
	if i := strings.IndexByte(base, ';'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimSpace(strings.ToLower(base))
	if strings.HasPrefix(base, "text/") {
		return false
	}
	// A handful of structured-text types that mime reports as application/*.
	switch base {
	case "application/json", "application/xml", "application/javascript",
		"application/x-yaml", "application/x-sh", "application/toml":
		return false
	}
	return true
}
