package terminal

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilesTreeExplicitDirectoryNavigation(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	base := t.TempDir()
	project := filepath.Join(base, "project")
	memory := filepath.Join(base, "home", ".claude", "projects", "-project", "memory")
	sub := filepath.Join(project, "docs", "中文目录")
	for _, dir := range []string{project, memory, sub} {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(memory, "MEMORY.md"), []byte("selected memory"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "提纲.md"), []byte("project document"), 0o644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "explicit-directory", CWD: project})
	require.NoError(t, err)
	session := sessionByName(t, sm, "explicit-directory")
	t.Setenv("HOME", filepath.Join(base, "home"))
	for _, tc := range []struct {
		name, path, cwd, rel, file string
	}{
		{"outside absolute", memory + "/", memory, ".", "MEMORY.md"},
		{"home abbreviation", "~/.claude/projects/-project/memory/", memory, ".", "MEMORY.md"},
		{"parent relative", "../home/.claude/projects/-project/memory/", memory, ".", "MEMORY.md"},
		{"inside relative", "docs/中文目录/", project, "docs/中文目录", "提纲.md"},
		{"inside absolute", sub + "/", project, "docs/中文目录", "提纲.md"},
		{"same project root", project + "/", project, ".", "docs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"session": {session.ID}, "cwd": {project}, "anchor": {"explicit"}, "path": {tc.path}, "navigate": {"1"}}
			resp, err := httpGet(formatURL(server, "/files/tree?")+q.Encode(), "")
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var out treeResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
			assert.Equal(t, tc.cwd, out.CWD)
			assert.Equal(t, tc.rel, out.Rel)
			found := false
			for _, entry := range out.Entries {
				found = found || entry.Name == tc.file
			}
			assert.True(t, found, "must list the literal target, not a child invented beneath the previous root")
		})
	}
	// No user-navigation marker: preserve the ordinary confined tree contract.
	q := url.Values{"session": {session.ID}, "cwd": {project}, "anchor": {"explicit"}, "path": {"../home/"}}
	resp, err := httpGet(formatURL(server, "/files/tree?")+q.Encode(), "")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	for _, path := range []string{filepath.Join(base, "missing"), filepath.Join(memory, "MEMORY.md")} {
		q.Set("navigate", "1")
		q.Set("path", path)
		resp, err := httpGet(formatURL(server, "/files/tree?")+q.Encode(), "")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	}
	// Browsing a directory does not cd or replace the session's execution context.
	current, err := sm.Get(session.ID)
	require.NoError(t, err)
	assert.Equal(t, project, current.CWD)
}
