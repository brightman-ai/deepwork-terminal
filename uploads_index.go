package terminal

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// uploadEntry is one persisted row of the cross-session upload index
// (~/.dw-terminal/uploads.json). It is the "中间表" (lightweight middle table)
// that lets the resource drawer list every upload across ALL sessions, past and
// present, annotated with the originating session's name + cwd + time.
//
// The index is the single source of truth for which files the raw endpoint is
// allowed to serve: an absPath that is not in the index is never servable. This
// makes path-traversal structurally impossible (no client-supplied path ever
// reaches the filesystem — only an opaque id that maps to a recorded absPath).
type uploadEntry struct {
	ID           string `json:"id"`           // stable hash of absPath (whitelist key)
	Kind         string `json:"kind"`         // "image" | "file"
	AbsPath      string `json:"absPath"`      // absolute path on disk (dedup key)
	Name         string `json:"name"`         // base filename
	Size         int64  `json:"size"`         // bytes (refreshed on read)
	MtimeMs      int64  `json:"mtimeMs"`      // file mtime (refreshed on read)
	SessionID    string `json:"sessionId"`    // originating session id
	SessionName  string `json:"sessionName"`  // originating session display name
	CWD          string `json:"cwd"`          // originating session working dir
	UploadedAtMs int64  `json:"uploadedAtMs"` // when first indexed (stable)
}

// uploadIndex is the thread-safe, disk-backed store of upload entries. It mirrors
// the persistence idiom used by the workbench/store handlers in handlers.go and
// the push store: an in-memory map guarded by a mutex, flushed to a single JSON
// file under the server DataDir. Dedup is by absPath.
type uploadIndex struct {
	mu      sync.Mutex
	path    string
	entries map[string]*uploadEntry // keyed by absPath
}

// newUploadIndex loads the index from disk (best-effort) and returns it ready to
// use. A missing or malformed file yields an empty index — never an error, so a
// fresh install just starts recording going forward.
func newUploadIndex(dataDir string) *uploadIndex {
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".dw-terminal")
	}
	idx := &uploadIndex{
		path:    filepath.Join(dataDir, "uploads.json"),
		entries: map[string]*uploadEntry{},
	}
	idx.load()
	return idx
}

// uploadID derives a stable, collision-resistant id for an absolute path. The id
// is the only token the client ever sends back to fetch raw bytes, so it must map
// 1:1 to a recorded absPath and reveal nothing about the filesystem layout.
func uploadID(absPath string) string {
	sum := sha1.Sum([]byte(absPath)) //nolint:gosec // id only, not security-sensitive
	return hex.EncodeToString(sum[:12])
}

// load reads the JSON file into the in-memory map. Caller need not hold the lock
// (only called from the constructor before the index is shared).
func (ix *uploadIndex) load() {
	data, err := os.ReadFile(ix.path)
	if err != nil {
		return
	}
	var rows []*uploadEntry
	if err := json.Unmarshal(data, &rows); err != nil {
		return
	}
	for _, e := range rows {
		if e == nil || e.AbsPath == "" {
			continue
		}
		if e.ID == "" {
			e.ID = uploadID(e.AbsPath)
		}
		ix.entries[e.AbsPath] = e
	}
}

// flushLocked writes the current map to disk. Caller MUST hold ix.mu.
func (ix *uploadIndex) flushLocked() {
	rows := make([]*uploadEntry, 0, len(ix.entries))
	for _, e := range ix.entries {
		rows = append(rows, e)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UploadedAtMs > rows[j].UploadedAtMs })
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(ix.path), 0o755)
	_ = os.WriteFile(ix.path, data, 0o644)
}

// put records (or refreshes) an entry, deduped by absPath, and persists. The
// UploadedAtMs of an existing entry is preserved so ordering stays stable; the
// session annotation is updated in case the same file is re-uploaded from a newer
// session.
func (ix *uploadIndex) put(e uploadEntry) {
	if e.AbsPath == "" {
		return
	}
	e.ID = uploadID(e.AbsPath)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if existing, ok := ix.entries[e.AbsPath]; ok {
		e.UploadedAtMs = existing.UploadedAtMs // keep original index time
		if e.SessionName == "" {
			e.SessionName = existing.SessionName
		}
	}
	if e.UploadedAtMs == 0 {
		e.UploadedAtMs = e.MtimeMs
	}
	stored := e
	ix.entries[e.AbsPath] = &stored
	ix.flushLocked()
}

// list returns a snapshot of all entries with each file re-stat'd. Entries whose
// underlying file no longer exists are PRUNED from the index (and the prune is
// persisted). The returned slice is newest-first by UploadedAtMs.
func (ix *uploadIndex) list() []uploadEntry {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	var out []uploadEntry
	var pruned bool
	for abs, e := range ix.entries {
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			delete(ix.entries, abs)
			pruned = true
			continue
		}
		e.Size = info.Size()
		e.MtimeMs = info.ModTime().UnixMilli()
		out = append(out, *e)
	}
	if pruned {
		ix.flushLocked()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UploadedAtMs > out[j].UploadedAtMs })
	return out
}

// get returns the entry for an id (whitelist lookup) and whether it exists.
func (ix *uploadIndex) get(id string) (uploadEntry, bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for _, e := range ix.entries {
		if e.ID == id {
			return *e, true
		}
	}
	return uploadEntry{}, false
}

// has reports whether an absPath is already indexed (used by backfill to skip).
func (ix *uploadIndex) has(absPath string) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	_, ok := ix.entries[absPath]
	return ok
}
