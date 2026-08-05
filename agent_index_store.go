package terminal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// agentIndexStore persists the agent materialized view ONE SHARD PER TRANSCRIPT FILE.
//
// The projection of a transcript changes when that transcript changes — nothing else does
// it. A single-document index violated that: recording a one-line append meant marshalling
// and rewriting every projection ever made (149 MB, 352 ms), on the request path, under the
// report lock, several times a minute while you work. Sharding makes the write proportional
// to the change, which is what the invariant said all along. Loading gets the same deal in
// reverse — 457 independent documents decode in parallel instead of one 840 ms blocking one.
//
// The schema lives in the DIRECTORY name, so a parser-semantics bump is a new directory and
// the previous generation is deleted wholesale rather than being carried forever as dead
// weight (two abandoned single-file indexes were occupying 320 MB when this was written).
type agentIndexStore struct {
	dir string
}

const agentIndexDirPrefix = "agent-index."

// newAgentIndexStore prepares <dataDir>/agent-index.<version>/ and evicts every superseded
// generation of the index — older shard directories AND the abandoned single-file format.
// A cache that outlives the code that can read it is not a cache, it is litter.
func newAgentIndexStore(dataDir, version string) *agentIndexStore {
	dir := filepath.Join(dataDir, agentIndexDirPrefix+version)
	if os.MkdirAll(dir, 0o700) != nil {
		return nil
	}
	store := &agentIndexStore{dir: dir}
	entries, err := os.ReadDir(dataDir)
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			switch {
			case entry.IsDir() && strings.HasPrefix(name, agentIndexDirPrefix) && name != filepath.Base(dir):
				_ = os.RemoveAll(filepath.Join(dataDir, name))
			case !entry.IsDir() && strings.HasPrefix(name, legacyAgentIndexPrefix) && strings.Contains(name, ".json"):
				legacy := filepath.Join(dataDir, name)
				store.adoptLegacyIndex(legacy, agentReportIndexSchema)
				_ = os.Remove(legacy)
			}
		}
	}
	return store
}

const legacyAgentIndexPrefix = "agent-report-index-"

// legacyAgentIndex is the superseded single-document format. Kept ONLY to convert an
// existing one into shards on first start: the projections in it are still valid under the
// same schema, and discarding them would make the first launch re-parse every transcript on
// the request path — minutes of a hung report to save a few lines here. Delete this and
// adoptLegacyIndex once no installed version writes the old file.
type legacyAgentIndex struct {
	Schema string                                  `json:"schema"`
	Files  map[string]persistedAgentFileProjection `json:"files"`
}

func (s *agentIndexStore) adoptLegacyIndex(path, schema string) {
	if entries, err := os.ReadDir(s.dir); err != nil || len(entries) > 0 {
		return // already sharded; the legacy file is stale by definition
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var legacy legacyAgentIndex
	if json.Unmarshal(body, &legacy) != nil || legacy.Schema != schema {
		return
	}
	for transcriptPath, file := range legacy.Files {
		s.save(transcriptPath, agentFileProjection{
			size: file.Size, modUnixNano: file.ModUnixNano, pricedWith: file.PricedWith,
			generation: file.Generation, runtime: file.Runtime, sessionID: file.SessionID,
			codexCursor: file.CodexCursor, claudeCursor: file.ClaudeCursor,
			deepworkCursor: file.DeepworkCursor, dataset: file.Dataset,
		})
	}
}

// shardFile is a projection plus the transcript path it belongs to, so the map can be
// reconstructed from the shards alone without a manifest to keep in sync.
type shardFile struct {
	Path       string                       `json:"path"`
	Projection persistedAgentFileProjection `json:"projection"`
}

func (s *agentIndexStore) shardPath(transcriptPath string) string {
	sum := sha256.Sum256([]byte(transcriptPath))
	return filepath.Join(s.dir, hex.EncodeToString(sum[:16])+".json")
}

// load reads every shard. Decoding is the expensive part and the shards are independent,
// so it fans out across cores. An unreadable or malformed shard is dropped, not fatal: the
// transcript it describes is simply reparsed, which is the whole point of a rebuildable view.
func (s *agentIndexStore) load() map[string]agentFileProjection {
	out := make(map[string]agentFileProjection)
	if s == nil {
		return out
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return out
	}
	work := make(chan string, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			work <- filepath.Join(s.dir, entry.Name())
		}
	}
	close(work)

	var mu sync.Mutex
	var wg sync.WaitGroup
	workers := min(runtime.NumCPU(), 8)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range work {
				body, readErr := os.ReadFile(path)
				if readErr != nil {
					continue
				}
				var shard shardFile
				if json.Unmarshal(body, &shard) != nil || shard.Path == "" {
					continue
				}
				if transcriptGone(shard.Path) {
					_ = os.Remove(path)
					continue
				}
				projection := shard.Projection
				mu.Lock()
				out[shard.Path] = agentFileProjection{
					size: projection.Size, modUnixNano: projection.ModUnixNano,
					pricedWith: projection.PricedWith, generation: projection.Generation,
					runtime: projection.Runtime, sessionID: projection.SessionID,
					codexCursor: projection.CodexCursor, claudeCursor: projection.ClaudeCursor,
					deepworkCursor: projection.DeepworkCursor, dataset: projection.Dataset,
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// transcriptGone reports that a transcript was DELETED, as opposed to merely unreachable.
// A shard for a deleted transcript describes nothing and can never be refreshed, so left
// alone it accumulates forever. But "the file is missing" and "its whole tree is missing"
// are different facts: an unmounted home or a revoked permission makes every path look
// deleted, and pruning on that would throw away an index that is about to be correct again.
// Requiring the containing directory to still be there separates the two.
func transcriptGone(path string) bool {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return false
	}
	_, err := os.Stat(filepath.Dir(path))
	return err == nil
}

// save writes one shard. Best-effort by design: a failed write costs a reparse next start,
// never a wrong number, so it must not fail a refresh.
func (s *agentIndexStore) save(transcriptPath string, file agentFileProjection) {
	if s == nil {
		return
	}
	body, err := json.Marshal(shardFile{
		Path: transcriptPath,
		Projection: persistedAgentFileProjection{
			Size: file.size, ModUnixNano: file.modUnixNano, PricedWith: file.pricedWith,
			Generation: file.generation, Runtime: file.runtime, SessionID: file.sessionID,
			CodexCursor: file.codexCursor, ClaudeCursor: file.claudeCursor,
			DeepworkCursor: file.deepworkCursor, Dataset: file.dataset,
		},
	})
	if err != nil {
		return
	}
	target := s.shardPath(transcriptPath)
	tmp := target + ".tmp"
	if os.WriteFile(tmp, body, 0o600) != nil {
		return
	}
	if os.Rename(tmp, target) != nil {
		_ = os.Remove(tmp)
	}
}
