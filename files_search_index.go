package terminal

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Queries never own discovery. A cancelled keystroke must not discard work, and a
// refresh must not replace a usable snapshot with an empty, truncated prefix.
const fileSearchIndexTTL = 30 * time.Second
const fileSearchIndexRoots = 4
const fileSearchMaxBytes = 128 << 20

var fileSearchBuilders = make(chan struct{}, 2)
var fileSearchGeneration atomic.Uint64

type indexedFilename struct {
	name, rel           string
	lowerName, lowerRel string
	isDir               bool
}
type fileSearchSnapshot struct {
	entries    []indexedFilename // immutable after publication
	generation uint64
	complete   bool
	scanError  string
}
type fileSearchIndex struct {
	mu              sync.RWMutex
	ready           chan struct{}
	at, lastUsed    time.Time
	dirty, building bool
	cancel          context.CancelFunc
	view            fileSearchSnapshot
	matchesByQuery  map[string]fileSearchMatches
	directories     map[string]indexedDirectory
	reconciledAt    time.Time
}

func (i *fileSearchIndex) snapshot() (fileSearchSnapshot, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.view, i.building
}

// Mutations/browse mark stale without throwing away the previous usable view.
func (s *Server) invalidateFileSearchIndexes(dir string) {
	if canonical, err := filepath.EvalSymlinks(dir); err == nil {
		dir = canonical
	}
	s.fileSearchMu.Lock()
	defer s.fileSearchMu.Unlock()
	for root, index := range s.fileSearchIndexes {
		if root == dir || strings.HasPrefix(dir, strings.TrimRight(root, string(os.PathSeparator))+string(os.PathSeparator)) || strings.HasPrefix(root, strings.TrimRight(dir, string(os.PathSeparator))+string(os.PathSeparator)) {
			index.mu.Lock()
			index.dirty = true
			index.mu.Unlock()
		}
	}
}

func (s *Server) closeFileSearchIndexes() {
	s.fileSearchMu.Lock()
	defer s.fileSearchMu.Unlock()
	for _, index := range s.fileSearchIndexes {
		index.cancel()
	}
	s.fileSearchIndexes = nil
}

func (s *Server) fileSearchIndex(ctx context.Context, root string) (*fileSearchIndex, error) {
	if canonical, err := filepath.EvalSymlinks(root); err == nil {
		root = canonical
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.fileSearchMu.Lock()
	if s.fileSearchIndexes == nil {
		s.fileSearchIndexes = make(map[string]*fileSearchIndex)
	}
	index := s.fileSearchIndexes[root]
	if index == nil {
		if len(s.fileSearchIndexes) >= fileSearchIndexRoots {
			var oldest string
			var at time.Time
			for key, candidate := range s.fileSearchIndexes {
				if at.IsZero() || candidate.lastUsed.Before(at) {
					oldest, at = key, candidate.lastUsed
				}
			}
			s.fileSearchIndexes[oldest].cancel()
			delete(s.fileSearchIndexes, oldest)
		}
		index = &fileSearchIndex{}
		s.fileSearchIndexes[root] = index
	}
	index.lastUsed = time.Now()
	index.mu.Lock()
	if !index.building && (index.at.IsZero() || index.dirty || time.Since(index.at) >= fileSearchIndexTTL) {
		buildCtx, cancel := context.WithCancel(context.Background())
		index.cancel = cancel
		index.ready = make(chan struct{})
		index.building, index.dirty = true, false
		go index.build(buildCtx, root, index.ready)
	}
	ready, hasView := index.ready, index.view.generation != 0
	index.mu.Unlock()
	s.fileSearchMu.Unlock()
	// Tiny trees finish in this grace period. Large trees return partial progress
	// promptly rather than holding the HTTP request for a full filesystem scan.
	if !hasView {
		timer := time.NewTimer(40 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ready:
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return index, ctx.Err()
}

// Directory mtimes describe changes to names, which is all this index owns.
// File contents/metadata remain live reads. Unchanged directories reuse their
// filename lists; periodic full reconciliation covers missed/coarse timestamps.
type indexedDirectory struct {
	mtime   int64
	entries []indexedFilename
}

const fileSearchReconcileInterval = 10 * time.Minute

func (i *fileSearchIndex) build(ctx context.Context, root string, ready chan struct{}) {
	defer close(ready)
	select {
	case fileSearchBuilders <- struct{}{}:
		defer func() { <-fileSearchBuilders }()
	case <-ctx.Done():
		return
	}
	i.mu.RLock()
	initial, previous, reconciledAt := i.view, i.directories, i.reconciledAt
	i.mu.RUnlock()
	full := !initial.complete || previous == nil || time.Since(reconciledAt) >= fileSearchReconcileInterval
	changed := full
	entries := make([]indexedFilename, 0, len(initial.entries))
	directories := make(map[string]indexedDirectory)
	queue := []string{""}
	bytes := 0
	scanError := ""
	lastPublish := time.Now()
	publish := func(complete bool) {
		if !complete && initial.generation != 0 {
			return
		}
		// Appends never modify the published prefix; capping the view prevents a
		// consumer from appending into the builder's tail, without quadratic copies.
		view := entries[:len(entries):len(entries)]
		viewComplete := complete && scanError == ""
		if complete && scanError != "" && initial.generation != 0 {
			// A failed refresh must not turn a previously complete index into a
			// partial prefix. Keep the last usable names and report the failed scan
			// alongside them so callers can show both results and the warning.
			view = initial.entries
		}
		i.mu.Lock()
		i.matchesByQuery = nil
		i.view = fileSearchSnapshot{entries: view, generation: fileSearchGeneration.Add(1), complete: viewComplete, scanError: scanError}
		if viewComplete {
			i.directories = directories
			if full {
				i.reconciledAt = time.Now()
			}
		}
		i.mu.Unlock()
		lastPublish = time.Now()
	}
	add := func(item indexedFilename) bool {
		bytes += 72 + 2*len(item.rel) + 2*len(item.name)
		if bytes > fileSearchMaxBytes {
			scanError = "目录索引超出内存预算，请缩小搜索范围"
			return false
		}
		entries = append(entries, item)
		if item.isDir {
			queue = append(queue, item.rel)
		}
		return true
	}
	// Breadth-first discovery prevents a deep artifact subtree starving siblings.
scan:
	for head := 0; head < len(queue); head++ {
		if ctx.Err() != nil {
			break
		}
		relDir := queue[head]
		queue[head] = ""
		abs := filepath.Join(root, relDir)
		info, err := os.Lstat(abs)
		if err != nil || !info.IsDir() {
			scanError = "部分目录无法读取"
			changed = true
			continue
		}
		old, known := previous[relDir]
		if !full && known && old.mtime == info.ModTime().UnixNano() {
			directories[relDir] = old
			for _, item := range old.entries {
				if !add(item) {
					break scan
				}
			}
			continue
		}
		changed = true
		dir, err := os.Open(abs)
		if err != nil {
			scanError = "部分目录无法读取"
			continue
		}
		discovered := indexedDirectory{mtime: info.ModTime().UnixNano()}
		for {
			if ctx.Err() != nil {
				dir.Close()
				break scan
			}
			batch, err := dir.ReadDir(256)
			for _, d := range batch {
				if d.IsDir() && searchSkipDirs[d.Name()] {
					continue
				}
				rel := filepath.ToSlash(filepath.Join(relDir, d.Name()))
				lowerRel := strings.ToLower(rel)
				// Names are views into the path strings, not duplicate allocations.
				item := indexedFilename{name: rel[strings.LastIndexByte(rel, '/')+1:], rel: rel, lowerName: lowerRel[strings.LastIndexByte(lowerRel, '/')+1:], lowerRel: lowerRel, isDir: d.IsDir()}
				if !add(item) {
					dir.Close()
					break scan
				}
				discovered.entries = append(discovered.entries, item)
			}
			if time.Since(lastPublish) >= 100*time.Millisecond {
				publish(false)
			}
			if err != nil {
				if err != io.EOF {
					scanError = "部分目录无法读取"
				}
				break
			}
		}
		dir.Close()
		directories[relDir] = discovered
	}
	if ctx.Err() != nil {
		return
	}
	if changed || scanError != "" {
		publish(true)
	}
	i.mu.Lock()
	i.building = false
	i.at = time.Now()
	i.mu.Unlock()
}

// Query caching is bound to an immutable generation, not a timeout. Metadata is
// still read for matches on each request so deletion/recency do not go stale.
type fileSearchMatches struct {
	generation uint64
	entries    []searchEntry
}

func (i *fileSearchIndex) matches(ctx context.Context, view fileSearchSnapshot, prefix, lowerPrefix string, terms []string, pathQuery bool) ([]searchEntry, error) {
	key := prefix + "\x00" + strings.Join(terms, "\x00")
	i.mu.RLock()
	cached, ok := i.matchesByQuery[key]
	i.mu.RUnlock()
	if ok && cached.generation == view.generation {
		return cached.entries, ctx.Err()
	}
	out := make([]searchEntry, 0, 64)
	for n := range view.entries {
		if n%1024 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		item := &view.entries[n]
		candidate := item.lowerName
		if pathQuery {
			candidate = item.lowerRel
			if lowerPrefix != "" {
				candidate = lowerPrefix + candidate
			}
		}
		matched := true
		for _, term := range terms {
			if !strings.Contains(candidate, term) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, searchEntry{Name: item.name, Rel: prefix + item.rel, IsDir: item.isDir})
		}
	}
	if len(out) <= 10000 {
		i.mu.Lock()
		if i.view.generation == view.generation {
			if i.matchesByQuery == nil || len(i.matchesByQuery) >= 16 {
				i.matchesByQuery = make(map[string]fileSearchMatches)
			}
			i.matchesByQuery[key] = fileSearchMatches{generation: view.generation, entries: out}
		}
		i.mu.Unlock()
	}
	return out, ctx.Err()
}
