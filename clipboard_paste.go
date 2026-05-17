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
	cwd := sess.WorkingDir()
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
	if existing := findDuplicateClipboard(sessionDir, hashHex); existing != "" {
		cwd2 := ""
		if s2, err2 := s.mgr.Get(id); err2 == nil {
			cwd2 = s2.WorkingDir()
		}
		relPath, _ := filepath.Rel(cwd2, existing)
		if relPath == "" {
			relPath = existing
		}
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

	// Filename: images get seq+hash, files keep original name
	var filename string
	if isImage {
		seq := nextClipboardSeq(id)
		filename = fmt.Sprintf("%s%03d-%s%s", now.Format("1504"), seq, hashHex, ext)
	} else {
		// Preserve original filename for uploaded files
		origName := sanitizeClipboardFilename(header.Filename)
		if origName == "" || origName == "blob" {
			origName = "upload" + ext
		}
		filename = uniqueClipboardFilename(sessionDir, origName, hashHex)
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
	} {
		if lower == allowed {
			return true
		}
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

// findDuplicateClipboard checks if a file with the same hash already exists in the directory.
// Filename format includes hash: {HHmm}{seq}-{hash8}.{ext}
func findDuplicateClipboard(dir string, hashHex string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	// Check most recent files first (reverse order)
	for i := len(entries) - 1; i >= 0; i-- {
		name := entries[i].Name()
		if strings.Contains(name, "-"+hashHex+".") {
			return filepath.Join(dir, name)
		}
	}
	return ""
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
