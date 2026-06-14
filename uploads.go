package terminal

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// uploadsImageDir / uploadsFileDir are the per-session subtrees (relative to the
// session CWD) that hold clipboard-pasted images and uploaded files. They mirror
// the paths written by handleClipboardPasteUpload and are scanned by the backfill.
const (
	uploadsImageDir = "tmp/clip"
	uploadsFileDir  = "tmp/files"
)

// uploadItem is one listed resource (image or file) in the CROSS-SESSION drawer.
// Unlike the old session-scoped variant, it carries the originating session's
// name/cwd + an opaque id so the raw endpoint can whitelist by id (raw bytes are
// fetched only via id — a client-supplied path never reaches the filesystem, so
// traversal stays impossible). Path is the server's OWN recorded absPath, exposed
// as read-only metadata so the drawer's "插入对话" can inject the same
// @-referenceable path the clipboard-paste flow injects post-upload.
type uploadItem struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // "image" | "file"
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	MtimeMs     int64  `json:"mtimeMs"`
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	CWD         string `json:"cwd"`
	URL         string `json:"url"`  // raw-serving URL (request path, no /api prefix)
	Path        string `json:"path"` // absolute path on disk — the @-referenceable path agents need
}

// uploadsResponse is the payload of GET /uploads.
type uploadsResponse struct {
	Items []uploadItem `json:"items"`
}

// handleUploadsList handles GET /uploads — the global, cross-session listing.
//
// It first backfills any on-disk uploads from ALIVE sessions that predate the
// index, then returns every indexed entry (newest first), re-stat'd and pruned of
// vanished files. Optional filters: ?kind=image|file and ?session=<name>.
func (s *Server) handleUploadsList(w http.ResponseWriter, r *http.Request) {
	s.backfillUploads()

	kindFilter := r.URL.Query().Get("kind")
	sessionFilter := r.URL.Query().Get("session")
	rawBase := strings.TrimSuffix(r.URL.Path, "/uploads") + "/uploads/raw"

	entries := s.uploads.list()
	items := make([]uploadItem, 0, len(entries))
	for _, e := range entries {
		if kindFilter != "" && e.Kind != kindFilter {
			continue
		}
		if sessionFilter != "" && e.SessionName != sessionFilter {
			continue
		}
		items = append(items, uploadItem{
			ID:          e.ID,
			Kind:        e.Kind,
			Name:        e.Name,
			Size:        e.Size,
			MtimeMs:     e.MtimeMs,
			SessionID:   e.SessionID,
			SessionName: e.SessionName,
			CWD:         e.CWD,
			URL:         rawBase + "?id=" + e.ID,
			Path:        e.AbsPath,
		})
	}
	writeJSON(w, http.StatusOK, uploadsResponse{Items: items})
}

// handleUploadsRaw handles GET /uploads/raw?id=<id>.
//
// SECURITY: the client supplies only an opaque id. The server looks the id up in
// the index whitelist and serves the recorded absPath — there is NO client-
// supplied path, so directory traversal is structurally impossible. An id that is
// not in the index (or whose file vanished) yields 404; there is no directory
// listing, only file bytes.
func (s *Server) handleUploadsRaw(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing id"})
		return
	}
	entry, ok := s.uploads.get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	info, err := os.Stat(entry.AbsPath)
	if err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	f, err := os.Open(entry.AbsPath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer f.Close()

	if ct := mime.TypeByExtension(filepath.Ext(entry.AbsPath)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// Uploads are immutable hashed/named artifacts: safe to cache aggressively.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(entry.AbsPath), info.ModTime(), f)
}

// backfillUploads is a best-effort scan of every ALIVE session's tmp/clip and
// tmp/files subtrees, adding any file not yet in the index (with the live
// session's name). It never fails on missing dirs and never errors out the
// request — it just enriches the index so files uploaded before the index existed
// (in still-running sessions) also appear cross-session.
func (s *Server) backfillUploads() {
	for _, sess := range s.mgr.List() {
		cwd := sess.WorkingDir()
		if cwd == "" {
			continue
		}
		s.backfillDir(cwd, uploadsImageDir, "image", sess.ID, sess.Name)
		s.backfillDir(cwd, uploadsFileDir, "file", sess.ID, sess.Name)
	}
}

func (s *Server) backfillDir(cwd, sub, kind, sessionID, sessionName string) {
	root := filepath.Join(cwd, sub)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return filepath.SkipDir // dir absent — nothing to backfill
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".tmp") {
			return nil
		}
		if s.uploads.has(path) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		s.uploads.put(uploadEntry{
			Kind:         kind,
			AbsPath:      path,
			Name:         name,
			Size:         info.Size(),
			MtimeMs:      info.ModTime().UnixMilli(),
			SessionID:    sessionID,
			SessionName:  sessionName,
			CWD:          cwd,
			UploadedAtMs: info.ModTime().UnixMilli(),
		})
		return nil
	})
}
