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
	server, sm, _ := newDrawerTestServer(t)
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
	server, sm, _ := newDrawerTestServer(t)
	root := t.TempDir()
	for i := 0; i < 1300; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("a-widget-%04d.txt", i)), nil, 0644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "widget.txt"), nil, 0644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "ranking", CWD: root})
	require.NoError(t, err)
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
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "one.txt"), nil, 0644))
	first, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	second, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	require.Same(t, first, second)
	require.NoError(t, os.WriteFile(filepath.Join(root, "two.txt"), nil, 0644))
	first.at = time.Now().Add(-fileSearchIndexTTL)
	third, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	require.Len(t, third.entries, 2)
	s.invalidateFileSearchIndexes(root)
	fourth, err := s.fileSearchIndex(context.Background(), root)
	require.NoError(t, err)
	require.NotSame(t, third, fourth, "tree refresh must invalidate filename discovery")
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
	server, sm, _ := newDrawerTestServer(t)
	root := t.TempDir()
	for _, rel := range []string{"topic/final-v6/meeting.md", "topic/final-v6/notes.md", "topic/final-v5/meeting.md", "topic/final-v6/traces/copied/meeting.md"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(root, rel), nil, 0644))
	}
	_, err := sm.CreateWithOptions(CreateOptions{Name: "path-search", CWD: root})
	require.NoError(t, err)
	id := sessionByName(t, sm, "path-search").ID
	for _, query := range []string{"meeting%20final-v6", "FINAL-V6%20MEETING", "topic%2Ffinal-v6%20meeting"} {
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
	server, sm, _ := newDrawerTestServer(t)
	root := t.TempDir()
	base := filepath.Join(root, "meetings", "final-v6")
	require.NoError(t, os.MkdirAll(base, 0755))
	for i := 0; i < 250; i++ {
		require.NoError(t, os.Mkdir(filepath.Join(base, fmt.Sprintf("artifact-%03d", i)), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, "meeting.md"), nil, 0644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "path-page", CWD: root})
	require.NoError(t, err)
	resp, err := httpGet(formatURL(server, "/files/search?session=%s&q=meeting%%20final-v6", sessionByName(t, sm, "path-page").ID), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	var result searchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Equal(t, 252, result.TotalMatches)
	require.Equal(t, "meetings/final-v6/meeting.md", result.Entries[1].Rel)
	require.Equal(t, 200, result.NextOffset)
}
