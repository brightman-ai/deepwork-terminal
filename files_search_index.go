package terminal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A short-lived filename snapshot is shared by successive keystrokes. It contains no
// file contents, never follows symlinks, and is bounded across roots as well as per root.
const fileSearchIndexTTL = 10 * time.Second
const fileSearchIndexRoots = 4

type indexedFilename struct {
	name, rel string
	isDir     bool
}

type fileSearchIndex struct {
	ready      chan struct{}
	at         time.Time
	entries    []indexedFilename
	incomplete bool
	err        error
}

// Browsing/refreshing a directory also refreshes filename discovery. This covers
// the tree refresh after create/rename/delete/upload without keeping a second mutation list.
func (s *Server) invalidateFileSearchIndexes(dir string) {
	s.fileSearchMu.Lock()
	defer s.fileSearchMu.Unlock()
	for root := range s.fileSearchIndexes {
		if root == dir || strings.HasPrefix(dir, strings.TrimRight(root, string(os.PathSeparator))+string(os.PathSeparator)) || strings.HasPrefix(root, strings.TrimRight(dir, string(os.PathSeparator))+string(os.PathSeparator)) {
			delete(s.fileSearchIndexes, root)
		}
	}
}

func (s *Server) fileSearchIndex(ctx context.Context, root string) (*fileSearchIndex, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.fileSearchMu.Lock()
	if s.fileSearchIndexes == nil {
		s.fileSearchIndexes = make(map[string]*fileSearchIndex)
	}
	if cached := s.fileSearchIndexes[root]; cached != nil && time.Since(cached.at) < fileSearchIndexTTL {
		s.fileSearchMu.Unlock()
		select {
		case <-cached.ready:
			// A previous keystroke may have been cancelled while this query waited.
			if cached.err != nil && ctx.Err() == nil {
				return s.fileSearchIndex(ctx, root)
			}
			return cached, cached.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(s.fileSearchIndexes) >= fileSearchIndexRoots {
		oldest := root
		var at time.Time
		for key, index := range s.fileSearchIndexes {
			if at.IsZero() || index.at.Before(at) {
				oldest, at = key, index.at
			}
		}
		delete(s.fileSearchIndexes, oldest)
	}
	index := &fileSearchIndex{ready: make(chan struct{}), at: time.Now()}
	s.fileSearchIndexes[root] = index
	s.fileSearchMu.Unlock()
	deadline := time.Now().Add(searchTimeBudget)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			index.incomplete = true
			return nil
		}
		if path == root {
			return nil
		}
		if d.IsDir() && searchSkipDirs[d.Name()] {
			return filepath.SkipDir
		}
		if len(index.entries) >= searchMaxScan || time.Now().After(deadline) {
			index.incomplete = true
			return errSearchBudget
		}
		rel, err := filepath.Rel(root, path)
		if err == nil {
			index.entries = append(index.entries, indexedFilename{d.Name(), rel, d.IsDir()})
		}
		return nil
	})
	if err != nil {
		index.incomplete = true
	}
	index.err = ctx.Err()
	if index.err != nil {
		s.fileSearchMu.Lock()
		if s.fileSearchIndexes[root] == index {
			delete(s.fileSearchIndexes, root)
		}
		s.fileSearchMu.Unlock()
	}
	close(index.ready)
	return index, index.err
}
