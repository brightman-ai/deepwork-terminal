package terminal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// This file adds a CHUNKED, RESUMABLE upload path on top of the single-shot
// clipboard/attachment upload in clipboard_paste.go. Why it exists:
//   - Cloudflare (and most reverse proxies fronting a tunnel) cap a single request BODY
//     at ~100 MB. A one-POST upload of a large 产物 (a video, a dataset, a build)
//     simply 413s at the edge before it ever reaches us. Slicing the file into ≤8 MiB
//     chunks keeps every request well under that ceiling.
//   - Mobile networks drop. A 90 MB single POST that dies at 80% restarts from zero;
//     chunks let a reconnecting client re-init, learn which parts already landed, and
//     send only the gap — the transfer resumes instead of restarting.
//
// State is ON DISK, not in memory, so a server restart mid-upload loses nothing: the
// staging dir <DataDir>/uploads-partial/<uploadId>/ holds meta.json plus one N.part
// file per received chunk, and THOSE FILES ARE THE STATE. A re-init recomputes the same
// deterministic uploadId (hash of cwd+dir+name+size) and rescans the parts already on
// disk — idempotent resume with no server-side session map to lose. A package mutex
// guards only the two sections that read-modify a whole staging dir (the stale sweep and
// the reassemble), since per-chunk writes go to distinct N.part files and never collide.
//
// Path safety and the landing directory reuse the SAME primitives as the read/write file
// handlers (workbenchCWD → safeResolve → uniqueClipboardFilename); the only new surface
// is the staging root, which lives under DataDir and never accepts a client-supplied path
// component (uploadId is validated as 16 hex chars, so it can't traverse).
//
// Every response goes through writeJSON (explicit WriteHeader) — never a bare w.Write —
// for the same reason files_crud.go documents: this repo hit a gin-NoRoute
// 404-with-200-body bug when a handler let the status fall through to the framework.

const (
	// chunkUploadChunkSize is the fixed slice size a client cuts the file into. 8 MiB keeps
	// each POST an order of magnitude under Cloudflare's ~100 MB body cap with headroom for
	// retries, while staying large enough that a 1 GB upload is ~128 requests, not thousands.
	chunkUploadChunkSize = 8 << 20 // 8 MiB
	// chunkStagingSubdir is the DataDir-relative root under which each in-flight upload gets
	// its own <uploadId>/ staging dir (meta.json + N.part files).
	chunkStagingSubdir = "uploads-partial"
	// chunkStagingTTL is how long an abandoned staging dir survives before init's lazy sweep
	// RemoveAll's it — measured by meta.json's mtime, so a still-progressing upload (whose
	// meta is rewritten on each re-init) is never swept out from under an active client.
	chunkStagingTTL = 24 * time.Hour
)

// chunkStoreMu guards the whole-directory read-modify sections (stale sweep, reassemble)
// so a concurrent complete/sweep on the same staging root can't race. Per-chunk writes
// target distinct N.part files and are intentionally left lock-free.
var chunkStoreMu sync.Mutex

// chunkMeta is the on-disk descriptor written to <staging>/meta.json at init and read back
// by chunk/complete/status/abort. It is the ONLY record of an upload's shape — there is no
// in-memory counterpart — so a server restart mid-upload reconstructs everything from it.
type chunkMeta struct {
	Name        string `json:"name"`        // sanitized single-segment target filename
	Dir         string `json:"dir"`         // target dir, RELATIVE to CWD (as the client sent it)
	CWD         string `json:"cwd"`         // resolved absolute workbench cwd the upload anchors to
	Size        int64  `json:"size"`        // total byte length of the finished file
	ChunkSize   int64  `json:"chunkSize"`   // fixed slice size (chunkUploadChunkSize)
	TotalChunks int    `json:"totalChunks"` // ceil(size/chunkSize), min 1 (a 0-byte file is one empty chunk)
}

// validUploadID reports whether id is exactly the 16 lowercase-hex chars init mints. This
// is the traversal guard for the staging root: a value that passes can only ever name a
// single dir under uploads-partial/, never "../" out of it or into an absolute path.
func validUploadID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// chunkStagingRoot is <DataDir>/uploads-partial — the parent of every per-upload staging dir.
func (s *Server) chunkStagingRoot() string {
	return filepath.Join(s.config.DataDir, chunkStagingSubdir)
}

// chunkStagingDir returns the staging dir for a (pre-validated) uploadId.
func (s *Server) chunkStagingDir(uploadID string) string {
	return filepath.Join(s.chunkStagingRoot(), uploadID)
}

// writeChunkMeta atomically persists meta to <stagingDir>/meta.json (tmp+rename, the same
// crash-safe pattern the rest of this package uses), creating the staging dir if needed.
func writeChunkMeta(stagingDir string, meta chunkMeta) error {
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	path := filepath.Join(stagingDir, "meta.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readChunkMeta loads <stagingDir>/meta.json. A missing/corrupt file returns an error →
// the caller answers 404 (unknown upload id).
func readChunkMeta(stagingDir string) (chunkMeta, error) {
	var meta chunkMeta
	data, err := os.ReadFile(filepath.Join(stagingDir, "meta.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

// expectedChunkSize is how many bytes chunk index MUST hold to count as received: the full
// chunkSize for every slice but the last, and the remainder for the last. A 0-byte upload
// has one chunk of size 0. Used by scanReceivedChunks so a truncated/short .part (a dropped
// connection mid-chunk) is NOT counted, forcing the client to resend exactly that slice.
func expectedChunkSize(meta chunkMeta, index int) int64 {
	if index == meta.TotalChunks-1 {
		return meta.Size - int64(meta.TotalChunks-1)*meta.ChunkSize
	}
	return meta.ChunkSize
}

// scanReceivedChunks returns the SORTED indices whose <index>.part exists on disk at its
// expected size. Because chunk writes are atomic (tmp+rename), presence normally implies a
// complete slice; the size check is the belt-and-suspenders that keeps a short/garbled part
// from masquerading as received across a resume. This — meta.json + the N.part files — is
// the entire persisted state of an in-flight upload.
func scanReceivedChunks(stagingDir string, meta chunkMeta) []int {
	received := make([]int, 0, meta.TotalChunks)
	for i := 0; i < meta.TotalChunks; i++ {
		info, err := os.Stat(filepath.Join(stagingDir, strconv.Itoa(i)+".part"))
		if err != nil || info.IsDir() {
			continue
		}
		if info.Size() == expectedChunkSize(meta, i) {
			received = append(received, i)
		}
	}
	return received
}

// missingChunks returns the indices in [0,totalChunks) that scanReceivedChunks did NOT find
// — the gap a resume must fill, and the 409 payload complete returns when asked to finish early.
func missingChunks(stagingDir string, meta chunkMeta) []int {
	have := make(map[int]bool, meta.TotalChunks)
	for _, i := range scanReceivedChunks(stagingDir, meta) {
		have[i] = true
	}
	missing := []int{}
	for i := 0; i < meta.TotalChunks; i++ {
		if !have[i] {
			missing = append(missing, i)
		}
	}
	return missing
}

// sweepStaleStaging RemoveAll's every staging dir whose meta.json mtime predates the TTL —
// the lazy GC for uploads a client started and abandoned (closed the tab, gave up on a
// flaky link). Called from init under chunkStoreMu so it can't race a concurrent reassemble.
// meta.json is (re)written on every init, so an upload the user is still resuming keeps a
// fresh mtime and is never collected.
func sweepStaleStaging(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return // no staging root yet (nothing uploaded) — nothing to sweep
	}
	cutoff := time.Now().Add(-chunkStagingTTL)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(root, e.Name(), "meta.json"))
		if err != nil || info.ModTime().Before(cutoff) {
			// Missing meta (a torn init) OR stale meta → reclaim the dir.
			os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// handleChunkUploadInit handles POST /files/upload/init.
// Form: session, cwd, dir (rel target dir), name, size (total bytes). Resolves the
// workbench cwd, validates the target dir + filename + size, mints a deterministic
// uploadId, writes meta.json, and reports which chunks (if any) are ALREADY on disk so a
// resuming client sends only the gap. Idempotent: re-init of the same file returns the same
// uploadId and the current received set.
func (s *Server) handleChunkUploadInit(w http.ResponseWriter, r *http.Request) {
	cwd, ok := s.workbenchCWD(r.Context(), r.FormValue("session"), r.FormValue("cwd"))
	if !ok || cwd == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	dir := r.FormValue("dir")
	// Validate the target dir up front (traversal / symlink-out → 403) so a client can't
	// even START an upload aimed outside the tree; complete re-checks on the resolved path.
	if _, err := safeResolve(cwd, dir); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "target dir not allowed"})
		return
	}
	name := sanitizeClipboardFilename(r.FormValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	rawSize := r.FormValue("size")
	size, err := strconv.ParseInt(rawSize, 10, 64)
	if rawSize == "" || err != nil || size < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "size must be a non-negative integer"})
		return
	}
	// Same cap as the single-shot path, read once from the runtime SSOT (upload_limit.go). A
	// deterministic 413 carrying limit_mb lets the client say "文件超过 N MB 上限" without
	// hardcoding the number — identical contract to handleClipboardPasteUpload.
	if limit := UploadLimitBytes(); size > limit {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error":    fmt.Sprintf("file exceeds the %d MB limit", limit>>20),
			"limit_mb": limit >> 20,
		})
		return
	}

	chunkSize := int64(chunkUploadChunkSize)
	totalChunks := int((size + chunkSize - 1) / chunkSize)
	if totalChunks == 0 {
		totalChunks = 1 // a 0-byte file is still one (empty) chunk
	}

	// Deterministic id: identical (cwd,dir,name,size) → identical uploadId, which is what
	// makes resume idempotent (the client need not persist a token). NUL separators keep the
	// four fields unambiguous so no concatenation collision is possible.
	sum := sha256.Sum256([]byte(cwd + "\x00" + dir + "\x00" + name + "\x00" + strconv.FormatInt(size, 10)))
	uploadID := hex.EncodeToString(sum[:])[:16]
	stagingDir := s.chunkStagingDir(uploadID)

	// Lazy GC of abandoned uploads — under the lock so it can't collide with a concurrent
	// reassemble, and BEFORE we (re)write this upload's meta so a fresh mtime protects it.
	chunkStoreMu.Lock()
	sweepStaleStaging(s.chunkStagingRoot())
	err = writeChunkMeta(stagingDir, chunkMeta{
		Name:        name,
		Dir:         dir,
		CWD:         cwd,
		Size:        size,
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
	})
	chunkStoreMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot stage upload"})
		return
	}

	meta, _ := readChunkMeta(stagingDir) // just wrote it; read back for the canonical shape
	writeJSON(w, http.StatusOK, map[string]any{
		"uploadId":    uploadID,
		"chunkSize":   chunkSize,
		"totalChunks": totalChunks,
		"received":    scanReceivedChunks(stagingDir, meta),
	})
}

// handleChunkUploadChunk handles POST /files/upload/chunk?uploadId=&index=.
// The request BODY is the raw chunk bytes (not multipart — the client streams the slice
// directly). Caps the read at chunkSize+slack via MaxBytesReader (413 on overflow) and
// writes the slice atomically to <staging>/<index>.part.
func (s *Server) handleChunkUploadChunk(w http.ResponseWriter, r *http.Request) {
	uploadID := r.URL.Query().Get("uploadId")
	if !validUploadID(uploadID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown upload id"})
		return
	}
	stagingDir := s.chunkStagingDir(uploadID)
	meta, err := readChunkMeta(stagingDir)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown upload id"})
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil || index < 0 || index >= meta.TotalChunks {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "index out of range"})
		return
	}

	// Bound the body at one chunk plus a little slack — a single slice can never legitimately
	// exceed chunkSize, so anything larger is a malformed/hostile request, not a valid chunk.
	r.Body = http.MaxBytesReader(w, r.Body, meta.ChunkSize+4096)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
				"error":    "chunk exceeds the chunk size",
				"limit_mb": meta.ChunkSize >> 20,
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot read chunk body"})
		return
	}

	// Atomic write: a dropped connection can leave a *.part.tmp behind but never a
	// half-written N.part, so scanReceivedChunks only ever sees complete slices.
	partPath := filepath.Join(stagingDir, strconv.Itoa(index)+".part")
	tmpPath := partPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot write chunk"})
		return
	}
	if err := os.Rename(tmpPath, partPath); err != nil {
		os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot commit chunk"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"received": len(scanReceivedChunks(stagingDir, meta)),
		"total":    meta.TotalChunks,
	})
}

// handleChunkUploadComplete handles POST /files/upload/complete.
// Form: uploadId, session (optional — for the cross-session upload index). Verifies every
// chunk is present (else 409 with the missing set), reassembles the slices in order into a
// tmp file in the target dir, verifies the byte count, then renames it to a
// clobber-safe final name — returning the SAME success shape as the single-shot upload.
func (s *Server) handleChunkUploadComplete(w http.ResponseWriter, r *http.Request) {
	uploadID := r.FormValue("uploadId")
	if !validUploadID(uploadID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown upload id"})
		return
	}
	stagingDir := s.chunkStagingDir(uploadID)

	// Hold the store lock across the whole verify→reassemble→rename→cleanup so two concurrent
	// completes (double-tap) can't both reassemble, and the sweep can't reclaim mid-assembly.
	chunkStoreMu.Lock()
	defer chunkStoreMu.Unlock()

	meta, err := readChunkMeta(stagingDir)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown upload id"})
		return
	}
	if missing := missingChunks(stagingDir, meta); len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "upload incomplete",
			"missing": missing,
		})
		return
	}

	// Resolve the target dir the reassembled file lands in (safeResolve confines it to the
	// tree, re-checked here — the dir could have changed since init). MkdirAll is a no-op when
	// it already exists (the common case: a browsed 目录树 dir), harmless otherwise.
	targetDir, err := safeResolve(meta.CWD, meta.Dir)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "target dir not allowed"})
		return
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create target dir"})
		return
	}

	// Reassemble into a tmp file first, then atomically rename — the final path never appears
	// until it holds the complete, verified bytes.
	tmpFile, err := os.CreateTemp(targetDir, ".dwupload-*.tmp")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create temp file"})
		return
	}
	tmpPath := tmpFile.Name()
	// Hash the assembled bytes as we stream them, so a name COLLISION in the target dir is
	// disambiguated by CONTENT (like the clipboard path) — identical bytes reuse one name
	// (idempotent), different bytes get a different name. Without a content-derived suffix,
	// uniqueClipboardFilename can hand back a suffixed name it never re-checks for existence,
	// which would let a third same-named upload overwrite the second.
	hasher := sha256.New()
	sink := io.MultiWriter(tmpFile, hasher)
	var written int64
	for i := 0; i < meta.TotalChunks; i++ {
		part, oerr := os.Open(filepath.Join(stagingDir, strconv.Itoa(i)+".part"))
		if oerr != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read chunk"})
			return
		}
		n, cerr := io.Copy(sink, part)
		part.Close()
		if cerr != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot assemble chunks"})
			return
		}
		written += n
	}
	// fsync then close so the bytes are durable before we hand back a success (a rename of an
	// unsynced file can survive while its contents don't after a crash).
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot flush file"})
		return
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot close file"})
		return
	}
	// The assembled size MUST equal what init was told, or the chunks were wrong/short — refuse
	// to publish a corrupt file, and clean up the reassembly artifact we just made.
	if written != meta.Size {
		os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "assembled size mismatch"})
		return
	}

	// Clobber-safe final name (never overwrite an existing DIFFERENT file in the target dir), the
	// suffix content-addressed so a re-upload of identical bytes collapses to one name. Then
	// re-confine the full rel path under cwd before the rename — belt to safeResolve's brace.
	hashHex := hex.EncodeToString(hasher.Sum(nil)[:8])
	finalName := uniqueClipboardFilename(targetDir, meta.Name, hashHex)
	if finalName == "" {
		finalName = meta.Name
	}
	target, err := safeResolve(meta.CWD, filepath.Join(meta.Dir, finalName))
	if err != nil {
		os.Remove(tmpPath)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "path not allowed"})
		return
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot finalize file"})
		return
	}

	// Record in the cross-session upload index so the resource drawer lists this file from any
	// future session — best-effort: a missing/expired session just skips the annotation, the
	// file is already on disk. Not an image (chunked path is for large binaries/documents).
	if sess, gerr := s.mgr.Get(r.FormValue("session")); gerr == nil {
		s.recordUpload(sess, target, false)
	}

	// The staging dir has done its job — reclaim it now rather than waiting for the TTL sweep.
	os.RemoveAll(stagingDir)

	relPath, _ := filepath.Rel(meta.CWD, target)
	if relPath == "" {
		relPath = target
	}
	// SAME success shape as handleClipboardPasteUpload so the client's upload-result handling
	// is one code path regardless of which route (single-shot vs chunked) produced the file.
	writeJSON(w, http.StatusOK, map[string]any{
		"path":     target,
		"relPath":  relPath,
		"size":     written,
		"filename": finalName,
	})
}

// handleChunkUploadStatus handles GET /files/upload/status?uploadId=.
// Reports the upload's shape plus which chunks are already on disk — the endpoint a
// reconnecting client polls before deciding what to resend. 404 for an unknown/GC'd id.
func (s *Server) handleChunkUploadStatus(w http.ResponseWriter, r *http.Request) {
	uploadID := r.URL.Query().Get("uploadId")
	if !validUploadID(uploadID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown upload id"})
		return
	}
	stagingDir := s.chunkStagingDir(uploadID)
	meta, err := readChunkMeta(stagingDir)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown upload id"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uploadId":    uploadID,
		"name":        meta.Name,
		"size":        meta.Size,
		"chunkSize":   meta.ChunkSize,
		"totalChunks": meta.TotalChunks,
		"received":    scanReceivedChunks(stagingDir, meta),
	})
}

// handleChunkUploadAbort handles POST /files/upload/abort.
// Form: uploadId → RemoveAll the staging dir (client cancelled). Idempotent: aborting an
// already-gone upload still returns ok (RemoveAll of a missing path is a no-op).
func (s *Server) handleChunkUploadAbort(w http.ResponseWriter, r *http.Request) {
	uploadID := r.FormValue("uploadId")
	if !validUploadID(uploadID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown upload id"})
		return
	}
	chunkStoreMu.Lock()
	os.RemoveAll(s.chunkStagingDir(uploadID))
	chunkStoreMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
