package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFilesSearch_PagesAndScope(t *testing.T) {
	server, sm, fileServer := newDrawerTestServer(t)
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "chosen"), 0755))
	for i := 0; i < 225; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, "chosen", fmt.Sprintf("hit-%03d.txt", i)), nil, 0644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "hit-outside.txt"), nil, 0644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "pages", CWD: root})
	require.NoError(t, err)
	id := sessionByName(t, sm, "pages").ID
	seen := map[string]bool{}
	for _, offset := range []int{0, 200} {
		awaitFileSearchIndex(t, fileServer, filepath.Join(root, "chosen"))
		resp, err := httpGet(formatURL(server, "/files/search?session=%s&path=chosen&q=hit&offset=%d", id, offset), "")
		require.NoError(t, err)
		var result searchResponse
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, 225, result.TotalMatches)
		require.False(t, result.Incomplete)
		if offset == 0 {
			require.Equal(t, 200, result.NextOffset)
		} else {
			require.Zero(t, result.NextOffset)
		}
		for _, entry := range result.Entries {
			require.Contains(t, entry.Rel, "chosen/")
			require.False(t, seen[entry.Rel], "duplicate between pages")
			seen[entry.Rel] = true
		}
	}
	require.Len(t, seen, 225)
	resp, err := httpGet(formatURL(server, "/files/search?session=%s&path=..&q=hit", id), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestFilesSearch_LateExactMatchSurvivesCommonMatches(t *testing.T) {
	server, sm, fileServer := newDrawerTestServer(t)
	root := t.TempDir()
	for i := 0; i < 1300; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("a-widget-%04d.txt", i)), nil, 0644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "widget.txt"), nil, 0644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "ranking", CWD: root})
	require.NoError(t, err)
	awaitFileSearchIndex(t, fileServer, root)
	resp, err := httpGet(formatURL(server, "/files/search?session=%s&q=widget", sessionByName(t, sm, "ranking").ID), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	var result searchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, "widget.txt", result.Entries[0].Name)
	require.Equal(t, 1301, result.TotalMatches)
}

func TestFileSearchIndex_ReuseExpiryCancellationAndBound(t *testing.T) {
	s := &Server{}
	t.Cleanup(s.closeFileSearchIndexes)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "one.txt"), nil, 0644))
	first, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	<-first.ready
	old, _ := first.snapshot()
	second, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	require.Same(t, first, second)
	require.NoError(t, os.WriteFile(filepath.Join(root, "two.txt"), nil, 0644))
	first.mu.Lock()
	first.at = time.Now().Add(-fileSearchIndexTTL)
	first.mu.Unlock()
	third, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	<-third.ready
	view, _ := third.snapshot()
	require.Len(t, view.entries, 2)
	require.Greater(t, view.generation, old.generation)
	// Invalidating retains the complete old view, then replaces it when ready.
	s.invalidateFileSearchIndexes(root)
	before, _ := third.snapshot()
	require.Len(t, before.entries, 2)
	require.NoError(t, os.WriteFile(filepath.Join(root, "three.txt"), nil, 0644))
	fourth, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	<-fourth.ready
	view, _ = fourth.snapshot()
	require.Len(t, view.entries, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.fileSearchIndex(ctx, t.TempDir())
	require.ErrorIs(t, err, context.Canceled)
	for i := 0; i < 6; i++ {
		_, err = s.fileSearchIndex(context.Background(), t.TempDir())
		require.NoError(t, err)
	}
	require.LessOrEqual(t, len(s.fileSearchIndexes), fileSearchIndexRoots)
}

func TestFilesSearch_PathTermsAndNearbyResults(t *testing.T) {
	server, sm, fileServer := newDrawerTestServer(t)
	root := t.TempDir()
	for _, rel := range []string{"topic/final-v6/meeting.md", "topic/final-v6/notes.md", "topic/final-v5/meeting.md", "topic/final-v6/traces/copied/meeting.md"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), nil, 0644))
	}
	_, err := sm.CreateWithOptions(CreateOptions{Name: "path-search", CWD: root})
	require.NoError(t, err)
	id := sessionByName(t, sm, "path-search").ID
	for _, query := range []string{"meeting%20final-v6", "FINAL-V6%20MEETING", "topic%2Ffinal-v6%20meeting"} {
		awaitFileSearchIndex(t, fileServer, root)
		resp, err := httpGet(formatURL(server, "/files/search?session=%s&q=%s", id, query), "")
		require.NoError(t, err)
		var result searchResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		resp.Body.Close()
		require.Equal(t, 2, result.TotalMatches)
		require.Equal(t, "topic/final-v6/meeting.md", result.Entries[0].Rel)
		require.Equal(t, "topic/final-v6/traces/copied/meeting.md", result.Entries[1].Rel)
	}
	// Single-name search keeps its established semantics instead of returning every descendant.
	resp, err := httpGet(formatURL(server, "/files/search?session=%s&q=final-v6", id), "")
	require.NoError(t, err)
	var result searchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	require.Equal(t, 1, result.TotalMatches)
	require.Equal(t, "topic/final-v6", result.Entries[0].Rel)
	// Scoped search still matches terms from its ancestor path.
	resp, err = httpGet(formatURL(server, "/files/search?session=%s&path=topic/final-v6&q=meeting%%20final-v6", id), "")
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	require.Equal(t, 2, result.TotalMatches)
}

func TestFilesSearch_PathQueryDoesNotBuryNamedFileBehindAncestorMatches(t *testing.T) {
	server, sm, fileServer := newDrawerTestServer(t)
	root := t.TempDir()
	base := filepath.Join(root, "meetings", "final-v6")
	require.NoError(t, os.MkdirAll(base, 0755))
	for i := 0; i < 250; i++ {
		require.NoError(t, os.Mkdir(filepath.Join(base, fmt.Sprintf("artifact-%03d", i)), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "meeting.md"), nil, 0644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "path-page", CWD: root})
	require.NoError(t, err)
	awaitFileSearchIndex(t, fileServer, root)
	resp, err := httpGet(formatURL(server, "/files/search?session=%s&q=meeting%%20final-v6", sessionByName(t, sm, "path-page").ID), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	var result searchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, 252, result.TotalMatches)
	require.Equal(t, "meetings/final-v6/meeting.md", result.Entries[1].Rel)
	require.Equal(t, 200, result.NextOffset)
}

func TestFileSearchIndex_CancelledQueryKeepsDiscoveryAndOldSnapshot(t *testing.T) {
	s := &Server{}
	t.Cleanup(s.closeFileSearchIndexes)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "one.md"), nil, 0644))
	index, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	<-index.ready
	original, _ := index.snapshot()
	// Occupy both discovery workers: the next build is genuinely pending while
	// HTTP cancellation and concurrent queries exercise the same shared index.
	for i := 0; i < cap(fileSearchBuilders); i++ {
		fileSearchBuilders <- struct{}{}
	}
	release := func() {
		for i := 0; i < cap(fileSearchBuilders); i++ {
			<-fileSearchBuilders
		}
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	s.invalidateFileSearchIndexes(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "two.md"), nil, 0644))
	refreshed, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	old, building := refreshed.snapshot()
	require.True(t, building)
	require.Equal(t, original.generation, old.generation)
	require.True(t, old.complete)
	require.Len(t, old.entries, 1)
	coldRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(coldRoot, "OSMO改进方案.md"), nil, 0644))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err = s.fileSearchIndex(ctx, coldRoot)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	s.fileSearchMu.Lock()
	canonicalCold, canonicalErr := filepath.EvalSymlinks(coldRoot)
	require.NoError(t, canonicalErr)
	cold := s.fileSearchIndexes[canonicalCold]
	s.fileSearchMu.Unlock()
	require.NotNil(t, cold)
	release()
	released = true
	select {
	case <-cold.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery was cancelled with the query")
	}
	view, _ := cold.snapshot()
	require.True(t, view.complete)
	require.Equal(t, "OSMO改进方案.md", view.entries[0].name)
	<-refreshed.ready
	view, _ = refreshed.snapshot()
	require.Len(t, view.entries, 2)
	require.Greater(t, view.generation, original.generation)
}

func TestFilesSearch_GenerationChangeRestartsPagination(t *testing.T) {
	server, sm, s := newDrawerTestServer(t)
	root := t.TempDir()
	for i := 0; i < 225; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("hit-%03d.txt", i)), nil, 0644))
	}
	_, err := sm.CreateWithOptions(CreateOptions{Name: "generations", CWD: root})
	require.NoError(t, err)
	id := sessionByName(t, sm, "generations").ID
	read := func(query string) searchResponse {
		resp, err := httpGet(formatURL(server, "/files/search?session=%s&%s", id, query), "")
		require.NoError(t, err)
		defer resp.Body.Close()
		var result searchResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		return result
	}
	first := read("q=hit")
	require.NotZero(t, first.Generation)
	require.NoError(t, os.WriteFile(filepath.Join(root, "zzz-hit-new.txt"), nil, 0644))
	s.invalidateFileSearchIndexes(root)
	canonical, err := safeResolve(root, "")
	require.NoError(t, err)
	index, err := s.fileSearchIndex(context.Background(), canonical)
	require.NoError(t, err)
	<-index.ready
	page := read(fmt.Sprintf("q=hit&offset=200&generation=%d", first.Generation))
	require.True(t, page.Reset)
	require.Len(t, page.Entries, 200)
	require.Equal(t, first.Entries[0].Rel, page.Entries[0].Rel)
	require.Greater(t, page.Generation, first.Generation)
}

func TestFileSearchIndex_UnavailableRootReportsError(t *testing.T) {
	s := &Server{}
	t.Cleanup(s.closeFileSearchIndexes)
	index, err := s.fileSearchIndex(context.Background(), filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	<-index.ready
	view, building := index.snapshot()
	require.False(t, building)
	require.False(t, view.complete)
	require.NotEmpty(t, view.scanError)
}

func TestFilesSearch_FailedRefreshPreservesLastGoodAndRecovers(t *testing.T) {
	server, sm, fileServer := newDrawerTestServer(t)
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	require.NoError(t, os.Mkdir(blocked, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "stable.md"), nil, 0o600))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "last-good", CWD: root})
	require.NoError(t, err)
	sessionID := sessionByName(t, sm, "last-good").ID

	awaitFileSearchIndex(t, fileServer, root)
	query := func(q string) searchResponse {
		t.Helper()
		resp, err := httpGet(formatURL(server, "/files/search?session=%s&q=%s", sessionID, q), "")
		require.NoError(t, err)
		defer resp.Body.Close()
		var result searchResponse
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		return result
	}
	initial := query("stable")
	require.False(t, initial.Incomplete)
	require.Len(t, initial.Entries, 1)

	// Make a changed subtree unreadable after the last-good snapshot exists. chmod is
	// restored on every exit so TempDir cleanup remains reliable.
	require.NoError(t, os.WriteFile(filepath.Join(blocked, "recovered.md"), nil, 0o600))
	require.NoError(t, os.Chmod(blocked, 0))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
	fileServer.invalidateFileSearchIndexes(root)
	index, err := fileServer.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	select {
	case <-index.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("failed refresh did not finish")
	}

	failed, building := index.snapshot()
	require.False(t, building)
	require.False(t, failed.complete)
	require.NotEmpty(t, failed.scanError)
	require.Greater(t, failed.generation, initial.Generation)
	staleHit := query("stable")
	require.True(t, staleHit.Incomplete)
	require.NotEmpty(t, staleHit.ScanError)
	require.Len(t, staleHit.Entries, 1)
	require.Equal(t, "stable.md", staleHit.Entries[0].Name)

	require.NoError(t, os.Chmod(blocked, 0o700))
	fileServer.invalidateFileSearchIndexes(root)
	awaitFileSearchIndex(t, fileServer, root)
	recovered := query("recovered")
	require.False(t, recovered.Incomplete)
	require.Empty(t, recovered.ScanError)
	require.Len(t, recovered.Entries, 1)
	require.Equal(t, "blocked/recovered.md", recovered.Entries[0].Rel)
}

func awaitFileSearchIndex(t *testing.T, s *Server, root string) {
	t.Helper()
	index, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	select {
	case <-index.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture index did not finish")
	}
	view, _ := index.snapshot()
	require.True(t, view.complete)
}

func TestFileSearchIndex_UnchangedRefreshKeepsGeneration(t *testing.T) {
	s := &Server{}
	t.Cleanup(s.closeFileSearchIndexes)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "unchanged.md"), nil, 0644))
	awaitFileSearchIndex(t, s, root)
	index, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	before, _ := index.snapshot()
	s.invalidateFileSearchIndexes(root)
	index, err = s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	<-index.ready
	after, _ := index.snapshot()
	require.Equal(t, before.generation, after.generation)
	require.True(t, after.complete)
}

func TestFileSearchIndex_IncrementalRenameDeleteAndNewDirectory(t *testing.T) {
	s := &Server{}
	t.Cleanup(s.closeFileSearchIndexes)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs", "topic"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "docs", "topic", "old.md"), nil, 0644))
	awaitFileSearchIndex(t, s, root)
	require.NoError(t, os.Rename(filepath.Join(root, "docs", "topic", "old.md"), filepath.Join(root, "docs", "topic", "OSMO改进方案.md")))
	s.invalidateFileSearchIndexes(root)
	awaitFileSearchIndex(t, s, root)
	index, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	view, _ := index.snapshot()
	names := map[string]bool{}
	for _, item := range view.entries {
		names[item.rel] = true
	}
	require.True(t, names["docs/topic/OSMO改进方案.md"])
	require.False(t, names["docs/topic/old.md"])
	require.NoError(t, os.RemoveAll(filepath.Join(root, "docs", "topic")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "new"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "new", "new.md"), nil, 0644))
	s.invalidateFileSearchIndexes(root)
	awaitFileSearchIndex(t, s, root)
	view, _ = index.snapshot()
	names = map[string]bool{}
	for _, item := range view.entries {
		names[item.rel] = true
	}
	require.True(t, names["new/new.md"])
	require.False(t, names["docs/topic"])
	require.True(t, view.complete)
}
