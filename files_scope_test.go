package terminal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilesExplicitScopeStaysWithDisplayedDirectory(t *testing.T) {
	server, _, srv := newDrawerTestServer(t)
	sm := newRealPTYManager(t, 4096, "/bin/sh")
	srv.mgr = sm
	live, displayed := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(live, "same.md"), []byte("LIVE DIRECTORY"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(displayed, "same.md"), []byte("DISPLAYED DIRECTORY"), 0644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "scope", CWD: live})
	require.NoError(t, err)
	id := sessionByName(t, sm, "scope").ID
	scope := url.Values{"session": {id}, "cwd": {displayed}, "anchor": {"explicit"}}
	get := func(endpoint string, q url.Values) *http.Response {
		resp, err := httpGet(formatURL(server, "%s?%s", endpoint, q.Encode()), "")
		require.NoError(t, err)
		return resp
	}
	resp := get("/files/tree", scope)
	var tree treeResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tree))
	resp.Body.Close()
	require.Equal(t, displayed, tree.CWD, "a displayed/locked pane is an explicit target, not a stale cwd hint")
	scope.Set("path", "same.md")
	resp = get("/files/raw", scope)
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "DISPLAYED DIRECTORY", string(data))
	scope.Del("path")
	scope.Set("q", "same")
	resp = get("/files/search", scope)
	var result searchResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	require.Len(t, result.Entries, 1)
	require.Equal(t, int64(len("DISPLAYED DIRECTORY")), result.Entries[0].Size)
	scope.Del("q")
	scope.Set("path", "created.txt")
	resp, err = httpPostForm(formatURL(server, "/files/create"), scope, "")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, err = os.Stat(filepath.Join(displayed, "created.txt"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(live, "created.txt"))
	require.True(t, os.IsNotExist(err))
	// Upload completion must retain the explicit destination captured at init.
	resp, err = httpPostForm(formatURL(server, "/files/upload/init"), url.Values{
		"session": {id}, "cwd": {displayed}, "anchor": {"explicit"}, "name": {"upload.txt"}, "size": {"5"},
	}, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var upload chunkInitResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&upload))
	resp.Body.Close()
	sendAllChunks(t, server, upload, []byte("hello"))
	resp, err = httpPostForm(formatURL(server, "/files/upload/complete"), url.Values{"uploadId": {upload.UploadID}, "session": {id}}, "")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	data, err = os.ReadFile(filepath.Join(displayed, "upload.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))
	_, err = os.Stat(filepath.Join(live, "upload.txt"))
	require.True(t, os.IsNotExist(err))
	// The safety boundary remains relative to the selected root.
	scope.Set("path", "../escape.txt")
	resp = get("/files/raw", scope)
	resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	// Automatic callers (terminal paste and older clients) retain live-process semantics.
	scope.Del("path")
	scope.Del("anchor")
	resp = get("/files/raw", url.Values{"session": {id}, "cwd": {displayed}, "path": {"same.md"}})
	data, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "LIVE DIRECTORY", string(data))
	// Explicit missing targets fail rather than silently opening/writing to the active directory.
	scope.Set("anchor", "explicit")
	scope.Set("cwd", filepath.Join(displayed, "removed"))
	for _, endpoint := range []string{"/files/tree", "/files/search", "/files/raw"} {
		resp = get(endpoint, scope)
		resp.Body.Close()
		require.Equal(t, http.StatusNotFound, resp.StatusCode, endpoint)
	}
	scope.Set("cwd", "relative/dir")
	resp = get("/files/tree", scope)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	scope.Set("cwd", displayed)
	scope.Set("session", "unknown")
	resp = get("/files/tree", scope)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
