package terminal

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
)

// This file turns the SESSION file-upload cap from a compile-time constant
// (ClipboardMaxUploadSize, in clipboard_paste.go) into a RUNTIME-configurable,
// persisted value. ClipboardMaxUploadSize stays exactly as-is and remains the
// DEFAULT — deepwork-pro's parallel upload route imports that const today and must
// keep compiling unchanged; this SSOT sits ON TOP of it, not instead of it.
//
// Both size-enforcement points (clipboard_paste.go's MaxBytesReader/ParseMultipartForm
// and service_inproc.go's LimitReader) read the effective cap via UploadLimitBytes()
// instead of the const directly, so a runtime change takes effect immediately for
// both the HTTP and in-proc (Wails) upload paths.

const (
	uploadLimitFloorMB   = 1    // hard floor: never allow a cap below 1 MB
	uploadLimitCeilingMB = 1024 // hard ceiling: never allow an unbounded cap (1 GB)
	uploadLimitFileName  = "upload-limit.json"
)

// uploadLimitBytes is the current EFFECTIVE upload cap, in bytes. Initialized to
// ClipboardMaxUploadSize below so a build that never calls LoadUploadLimit/
// SetUploadLimitMB behaves exactly as before this file existed. atomic.Int64 (not a
// mutex) because UploadLimitBytes is read on every upload request's hot path and must
// never block on a writer.
var uploadLimitBytes atomic.Int64

func init() {
	uploadLimitBytes.Store(ClipboardMaxUploadSize)
}

// uploadLimitDir is the DataDir SetUploadLimitMB persists into, remembered from the
// last LoadUploadLimit call (server startup wires this to Config.DataDir — see
// NewServer). Guarded by its own mutex, separate from uploadLimitBytes, since it
// changes far less often and Set must read it without racing a concurrent Load.
var (
	uploadLimitDirMu sync.Mutex
	uploadLimitDir   string
)

// uploadLimitFile is the on-disk shape of <DataDir>/upload-limit.json.
type uploadLimitFile struct {
	MaxMB int `json:"maxMb"`
}

// UploadLimitBytes returns the current effective upload cap in bytes. Exported so
// deepwork-pro — which imports ClipboardMaxUploadSize today as its own default — can
// switch to this runtime-configurable SSOT later without duplicating the clamp/
// persist/load logic; terminal-only callers (clipboard_paste.go, service_inproc.go)
// use it directly in place of the const.
func UploadLimitBytes() int64 {
	return uploadLimitBytes.Load()
}

// clampUploadLimitMB clamps mb into [uploadLimitFloorMB, uploadLimitCeilingMB].
func clampUploadLimitMB(mb int) int {
	switch {
	case mb < uploadLimitFloorMB:
		return uploadLimitFloorMB
	case mb > uploadLimitCeilingMB:
		return uploadLimitCeilingMB
	default:
		return mb
	}
}

// SetUploadLimitMB clamps mb to [1, 1024] MB, applies it as the new effective upload
// cap (visible to UploadLimitBytes immediately), persists it to
// <DataDir>/upload-limit.json (the DataDir last supplied to LoadUploadLimit), and
// returns the actually-applied MB value. If no DataDir has ever been loaded (Set
// called before LoadUploadLimit, e.g. in a caller that skips startup wiring), the
// in-memory value still applies — persistence is best-effort, never a precondition
// for the limit taking effect. A write failure is returned but the in-memory value is
// NOT rolled back: the running server keeps honoring the new limit; only a future
// restart would miss it.
func SetUploadLimitMB(mb int) (int, error) {
	applied := clampUploadLimitMB(mb)
	uploadLimitBytes.Store(int64(applied) << 20)

	uploadLimitDirMu.Lock()
	dir := uploadLimitDir
	uploadLimitDirMu.Unlock()
	if dir == "" {
		return applied, nil
	}
	return applied, writeUploadLimitFile(dir, applied)
}

// LoadUploadLimit reads <dataDir>/upload-limit.json (written by a prior
// SetUploadLimitMB) and applies it as the effective upload cap. Call once at server
// startup with Config.DataDir (see NewServer) — a missing file is NOT an error, it
// just means the limit was never overridden, so the compile-time
// ClipboardMaxUploadSize default (already the package-init value) stands. Also
// remembers dataDir so a later SetUploadLimitMB call knows where to persist.
func LoadUploadLimit(dataDir string) error {
	uploadLimitDirMu.Lock()
	uploadLimitDir = dataDir
	uploadLimitDirMu.Unlock()

	if dataDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dataDir, uploadLimitFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var f uploadLimitFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if f.MaxMB <= 0 {
		return nil
	}
	uploadLimitBytes.Store(int64(clampUploadLimitMB(f.MaxMB)) << 20)
	return nil
}

// writeUploadLimitFile atomically persists the effective MB value to
// <dataDir>/upload-limit.json (temp file + rename — the same crash-safe pattern the
// clipboard upload save path uses) so a mid-write crash can never leave a corrupt or
// half-written config file behind.
func writeUploadLimitFile(dataDir string, mb int) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(uploadLimitFile{MaxMB: mb})
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, uploadLimitFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// handleUploadLimitGet handles GET /files/upload-limit: current effective cap plus
// the fixed default/floor/ceiling so the frontend can render a slider/input without
// hardcoding any of the three (SSOT stays server-side).
func (s *Server) handleUploadLimitGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"maxMb":     UploadLimitBytes() >> 20,
		"defaultMb": ClipboardMaxUploadSize >> 20,
		"ceilingMb": uploadLimitCeilingMB,
		"floorMb":   uploadLimitFloorMB,
	})
}

// handleUploadLimitSet handles PUT /files/upload-limit. Form: mb=<N>. A missing or
// non-numeric mb is a client error (400); an in-range or out-of-range numeric value is
// always accepted and clamped (never a 400) since clamping — not rejecting — is the
// documented contract.
func (s *Server) handleUploadLimitSet(w http.ResponseWriter, r *http.Request) {
	raw := r.FormValue("mb")
	mb, err := strconv.Atoi(raw)
	if raw == "" || err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mb must be an integer"})
		return
	}
	applied, err := SetUploadLimitMB(mb)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"maxMb": applied})
}
