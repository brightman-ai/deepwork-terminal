package terminal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brightman-ai/kit/obs"
)

const (
	clipboardMaxUploadSize = 10 << 20 // 10 MB
	clipboardTmpDir        = "tmp/clipboard"
	clipboardTTL           = 24 * time.Hour
)

// clipboardSeqCounter is a per-session auto-increment image counter.
// Key: session ID, Value: next sequence number.
var clipboardSeqCounter sync.Map

// handleClipboardPasteUpload handles POST /sessions/{id}/paste-upload.
// Accepts multipart image upload, saves to temp dir, injects path into PTY.
func (s *Server) handleClipboardPasteUpload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logCtx := obs.WithStage(r.Context(), stgTerminalClipboard)
	id := r.PathValue("id")
	sess, err := s.mgr.Get(id)
	if err != nil {
		terminalClipboardUploadErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard upload rejected",
			"reason", "session_not_found",
			"session_id", id,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"error", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	// Limit upload size
	r.Body = http.MaxBytesReader(w, r.Body, clipboardMaxUploadSize)

	if err := r.ParseMultipartForm(clipboardMaxUploadSize); err != nil {
		terminalClipboardUploadErrors.Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart form: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		terminalClipboardUploadErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard upload rejected",
			"reason", "missing_file",
			"session_id", id,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing or invalid file: " + err.Error()})
		return
	}
	defer file.Close()

	// Validate MIME type
	mime := r.FormValue("mime")
	if mime == "" {
		mime = header.Header.Get("Content-Type")
	}
	if !isAllowedClipboardMIME(mime) {
		terminalClipboardUploadErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard upload rejected",
			"reason", "unsupported_mime",
			"session_id", id,
			"mime", mime,
			"filename", header.Filename,
			"elapsed_ms", time.Since(start).Milliseconds())
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported MIME type: " + mime})
		return
	}

	ext := extFromMIME(mime)
	isImage := strings.HasPrefix(strings.ToLower(mime), "image/")

	// Save under session CWD:
	// - Images: {cwd}/tmp/clip/{MM-dd-HH}/{HHmmSSS}-{hash}.{ext}
	// - Files:  {cwd}/tmp/files/{MM-dd-HH}/{original-name}
	now := time.Now()
	// Where the upload lands. Prefer the live active-pane cwd the client sends (the same
	// resolution the files drawer uses) so the file appears where the CLI actually is —
	// not the session's static launch dir. For a non-tmux terminal (no client cwd),
	// workbenchCWD probes the shell's live cwd; it only falls back to the launch dir as a
	// last resort. This is the fix for "图片传飞" (uploads saved under home, not the agent cwd).
	cwd, _ := s.workbenchCWD(id, r.FormValue("cwd"))
	if cwd == "" {
		cwd = sess.WorkingDir()
	}
	subDir := "clip"
	if !isImage {
		subDir = "files"
	}
	hourDir := filepath.Join(cwd, "tmp", subDir, now.Format("01-02-15"))
	sessionDir := hourDir
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		terminalClipboardUploadErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard upload failed",
			"reason", "mkdir_failed",
			"session_id", id,
			"dir", sessionDir,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create clipboard dir"})
		return
	}

	// Lazy cleanup: remove files older than TTL
	go cleanupOldClipboardFiles(sessionDir, clipboardTTL)

	// Read file into memory for hash dedup
	data, err := io.ReadAll(file)
	if err != nil {
		terminalClipboardUploadErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard upload failed",
			"reason", "read_failed",
			"session_id", id,
			"mime", mime,
			"filename", header.Filename,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}

	// Hash dedup: if identical to the most recent file, return existing path
	hash := sha256.Sum256(data)
	hashHex := hex.EncodeToString(hash[:8]) // short hash for comparison
	if existing := findDuplicateClipboard(sessionDir, hashHex, data); existing != "" {
		// Resolve the dedup path against the SAME base as the fresh-save branch below
		// (the live active-pane `cwd`), NOT the session's static launch dir. A PC paste
		// uploads the same bytes twice — once saved, once deduped — and if the two
		// branches use different roots the client gets two different strings for one image
		// (e.g. "tmp/clip/X.png" + "code/.../tmp/clip/X.png"). Same base → identical string
		// → uniqueOrderedPaths collapses them to one. Mobile uploads once, so it was unaffected.
		relPath, _ := filepath.Rel(cwd, existing)
		if relPath == "" {
			relPath = existing
		}
		// Record (idempotent: deduped by absPath) so the cross-session drawer
		// lists this file with the session's name even on the dedup path.
		s.recordUpload(sess, existing, isImage)
		terminalClipboardUploadsTotal.Inc()
		terminalClipboardUploadBytes.Add(uint64(len(data)))
		terminalClipboardUploadDuration.Observe(time.Since(start).Seconds())
		terminalLogger.Info(logCtx, "cli clipboard upload deduped",
			"session_id", id,
			"kind", clipboardKindLabel(isImage),
			"mime", mime,
			"size", len(data),
			"rel_path", relPath,
			"filename", filepath.Base(existing),
			"elapsed_ms", time.Since(start).Milliseconds())
		writeJSON(w, http.StatusOK, map[string]any{
			"path":     existing,
			"relPath":  relPath,
			"size":     len(data),
			"filename": filepath.Base(existing),
			"dedup":    true,
		})
		return
	}

	// Filename policy: preserve the ORIGINAL name whenever the user copied a real
	// file (image OR other) — the name carries meaning the agent and the human read
	// back off the injected @path. Only a NAMELESS bitmap (screenshot / "Copy Image",
	// whose name arrives as a browser placeholder like "image.png"/"clipboard.png")
	// falls through to the synthetic {HHmm}{seq}-{hash} name, since there is no
	// original to keep. Previously ALL images got the hash name, losing "foo.png".
	var filename string
	origName := sanitizeClipboardFilename(header.Filename)
	switch {
	case !isGenericClipboardName(origName):
		filename = uniqueClipboardFilename(sessionDir, origName, hashHex)
	case isImage:
		seq := nextClipboardSeq(id)
		filename = fmt.Sprintf("%s%03d-%s%s", now.Format("1504"), seq, hashHex, ext)
	default:
		filename = uniqueClipboardFilename(sessionDir, "upload"+ext, hashHex)
	}
	savePath := filepath.Join(sessionDir, filename)

	// Save atomically
	tmpPath := savePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		terminalClipboardUploadErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard upload failed",
			"reason", "write_failed",
			"session_id", id,
			"mime", mime,
			"filename", filename,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}
	if err := os.Rename(tmpPath, savePath); err != nil {
		os.Remove(tmpPath)
		terminalClipboardUploadErrors.Inc()
		terminalLogger.Warn(logCtx, "cli clipboard upload failed",
			"reason", "rename_failed",
			"session_id", id,
			"mime", mime,
			"filename", filename,
			"elapsed_ms", time.Since(start).Milliseconds(),
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rename failed"})
		return
	}
	written := int64(len(data))

	// Compute relative path from CWD for short @reference
	relPath, _ := filepath.Rel(cwd, savePath)
	if relPath == "" {
		relPath = savePath
	}

	// NOTE: Do NOT inject path into PTY here. The frontend resolver owns the
	// paste policy for browser, Wails, and mobile runtimes.

	// Record in the cross-session upload index (~/.dw-terminal/uploads.json) so
	// the resource drawer can list this file from ANY future session, annotated
	// with this session's name + cwd + time.
	s.recordUpload(sess, savePath, isImage)

	terminalClipboardUploadsTotal.Inc()
	terminalClipboardUploadBytes.Add(uint64(written))
	terminalClipboardUploadDuration.Observe(time.Since(start).Seconds())
	terminalLogger.Info(logCtx, "cli clipboard upload completed",
		"session_id", id,
		"kind", clipboardKindLabel(isImage),
		"mime", mime,
		"size", written,
		"rel_path", relPath,
		"filename", filename,
		"elapsed_ms", time.Since(start).Milliseconds())

	writeJSON(w, http.StatusOK, map[string]any{
		"path":     savePath,
		"relPath":  relPath,
		"size":     written,
		"filename": filename,
	})
}

func isAllowedClipboardMIME(mime string) bool {
	lower := strings.ToLower(mime)
	// Allow images, documents, code, and common text formats
	for _, prefix := range []string{"image/", "text/", "application/pdf", "application/json"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// Allow common binary formats by extension check in MIME
	for _, allowed := range []string{
		"application/octet-stream", "application/zip", "application/gzip",
		"application/x-yaml", "application/toml", "application/xml",
		// Office documents — the deepwork side does ZERO text extraction; these
		// land in the session sandbox (tmp/files/) and the agent reads them via
		// the injected path. Allowing the MIME is purely "let the file reach disk".
		"application/msword",
		"application/vnd.ms-excel",
		"application/vnd.ms-powerpoint",
	} {
		if lower == allowed {
			return true
		}
	}
	// Modern OOXML office formats (wordprocessingml / spreadsheetml /
	// presentationml) all share the openxmlformats-officedocument namespace.
	if strings.HasPrefix(lower, "application/vnd.openxmlformats-officedocument.") {
		return true
	}
	return false
}

func extFromMIME(mime string) string {
	switch strings.ToLower(mime) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "application/pdf":
		return ".pdf"
	case "application/msword":
		return ".doc"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.ms-excel":
		return ".xls"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.ms-powerpoint":
		return ".ppt"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "application/zip":
		return ".zip"
	default:
		return ".bin"
	}
}

func clipboardKindLabel(isImage bool) string {
	if isImage {
		return "image"
	}
	return "file"
}

func sanitizeClipboardFilename(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	return name
}

// isGenericClipboardName reports whether a pasted filename is a browser-generated
// placeholder for a NAMELESS clipboard bitmap (a screenshot or "Copy Image") rather
// than a real file the user copied. A real name is preserved as-is; a generic one
// lets an image fall through to the synthetic {time}{seq}-{hash} name. Recognized
// placeholders: empty, "blob", and any "image.*" / "clipboard.*" (Chrome pastes raw
// bitmaps as "image.png"; our own web client sends "clipboard.<ext>"). "screenshot.png"
// et al. are treated as REAL names — a user may legitimately copy such a file.
func isGenericClipboardName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || lower == "blob" {
		return true
	}
	stem := strings.TrimSuffix(lower, filepath.Ext(lower))
	return stem == "image" || stem == "clipboard"
}

func uniqueClipboardFilename(dir, name, hashHex string) string {
	if name == "" {
		return ""
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem = "upload"
	}
	return fmt.Sprintf("%s-%s%s", stem, hashHex, ext)
}

// nextClipboardSeq returns the next auto-increment sequence number for a session.
func nextClipboardSeq(sessionID string) int64 {
	val, _ := clipboardSeqCounter.LoadOrStore(sessionID, &atomic.Int64{})
	counter := val.(*atomic.Int64)
	return counter.Add(1)
}

// findDuplicateClipboard checks if a file with the same content already exists in
// the directory, returning its path (or "" if none). Content-based, NOT purely
// filename-based: a synthetic image name embeds the hash ({HHmm}{seq}-{hash8}.{ext})
// so a filename match is the fast path, but a PRESERVED original name ("foo.png")
// does not — so we fall back to comparing by size then full content hash. Without
// this, a single PC paste (which uploads the same bytes twice) would fail to dedup a
// named file and inject two @references for one image.
func findDuplicateClipboard(dir string, hashHex string, data []byte) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	// Check most recent files first (reverse order)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			continue
		}
		name := entries[i].Name()
		if strings.HasSuffix(name, ".tmp") {
			continue
		}
		// Fast path: synthetic names carry the hash in the filename.
		if strings.Contains(name, "-"+hashHex+".") {
			return filepath.Join(dir, name)
		}
		// Content path (preserved original names): size pre-filter, then hash.
		info, err := entries[i].Info()
		if err != nil || info.Size() != int64(len(data)) {
			continue
		}
		existing, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		h := sha256.Sum256(existing)
		if hex.EncodeToString(h[:8]) == hashHex {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// recordUpload writes an entry into the cross-session upload index for a freshly
// saved (or deduped) upload. The session annotation (name + cwd) is taken from
// the live session that produced it; absPath dedups so re-uploads are idempotent.
func (s *Server) recordUpload(sess *Session, absPath string, isImage bool) {
	if s.uploads == nil || absPath == "" {
		return
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return
	}
	s.uploads.put(uploadEntry{
		Kind:         clipboardKindLabel(isImage),
		AbsPath:      absPath,
		Name:         filepath.Base(absPath),
		Size:         info.Size(),
		MtimeMs:      info.ModTime().UnixMilli(),
		SessionID:    sess.ID,
		SessionName:  sess.Name,
		CWD:          sess.WorkingDir(),
		UploadedAtMs: time.Now().UnixMilli(),
	})
}

func cleanupOldClipboardFiles(dir string, ttl time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-ttl)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
