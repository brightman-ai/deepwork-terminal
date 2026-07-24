package terminal

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// httpPostForm posts url-encoded form values — the same shape a real client sends for
// the /files/{mkdir,create,rename,delete} write endpoints, which read session/cwd/
// operands via r.FormValue.
func httpPostForm(rawURL string, form url.Values, authToken string) (*http.Response, error) {
	if authToken == "" {
		authToken = testAuthCode
	}
	req, err := http.NewRequest("POST", rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authToken != "" {
		req.Header.Set("X-CLI-Auth", authToken)
	}
	return http.DefaultClient.Do(req)
}

// TC-FSW-01: POST /files/mkdir creates exactly one directory level, 409s on a
// duplicate, 403s on a `..` escape, and 400s on an empty path.
func TestFilesMkdir(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "mkdir", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "mkdir")
	mkdirURL := formatURL(server, "/files/mkdir")

	// Success: new dir appears on disk.
	resp, err := httpPostForm(mkdirURL, url.Values{"session": {sess.ID}, "path": {"newdir"}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	info, statErr := os.Stat(filepath.Join(cwd, "newdir"))
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())

	// Duplicate → 409, directory untouched.
	resp2, err := httpPostForm(mkdirURL, url.Values{"session": {sess.ID}, "path": {"newdir"}}, "")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)

	// `..` escape → 403, nothing created outside cwd.
	resp3, err := httpPostForm(mkdirURL, url.Values{"session": {sess.ID}, "path": {"../escape"}}, "")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp3.StatusCode)
	_, escErr := os.Stat(filepath.Join(filepath.Dir(cwd), "escape"))
	assert.True(t, os.IsNotExist(escErr), "escape must not have been created")

	// Empty path → 400.
	resp4, err := httpPostForm(mkdirURL, url.Values{"session": {sess.ID}, "path": {""}}, "")
	require.NoError(t, err)
	defer resp4.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp4.StatusCode)

	// Missing parent (non-recursive Mkdir) → not 200/409 (must actually fail).
	resp5, err := httpPostForm(mkdirURL, url.Values{"session": {sess.ID}, "path": {"nope/deep"}}, "")
	require.NoError(t, err)
	defer resp5.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp5.StatusCode)
}

// TC-FSW-02: POST /files/create makes an empty file (O_EXCL — never truncates an
// existing one), 409s on a duplicate, 403s on a `..` escape, 400s on an empty path.
func TestFilesCreate(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "create", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "create")
	createURL := formatURL(server, "/files/create")

	// Success: new empty file appears on disk.
	resp, err := httpPostForm(createURL, url.Values{"session": {sess.ID}, "path": {"note.txt"}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	info, statErr := os.Stat(filepath.Join(cwd, "note.txt"))
	require.NoError(t, statErr)
	assert.False(t, info.IsDir())
	assert.Equal(t, int64(0), info.Size())

	// Pre-existing file with real content must survive a duplicate create (no truncation).
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "existing.txt"), []byte("keep me"), 0o644))
	resp2, err := httpPostForm(createURL, url.Values{"session": {sess.ID}, "path": {"existing.txt"}}, "")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
	body, rerr := os.ReadFile(filepath.Join(cwd, "existing.txt"))
	require.NoError(t, rerr)
	assert.Equal(t, "keep me", string(body))

	// `..` escape → 403.
	resp3, err := httpPostForm(createURL, url.Values{"session": {sess.ID}, "path": {"../escape.txt"}}, "")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp3.StatusCode)

	// Empty path → 400.
	resp4, err := httpPostForm(createURL, url.Values{"session": {sess.ID}, "path": {""}}, "")
	require.NoError(t, err)
	defer resp4.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp4.StatusCode)
}

// TC-FSW-03: POST /files/rename moves a file/dir, 409s when `to` already exists, 404s
// when `from` doesn't, 403s when either side escapes the cwd, 400s on an empty operand.
func TestFilesRename(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("hi"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "b.txt"), []byte("bye"), 0o644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "rename", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "rename")
	renameURL := formatURL(server, "/files/rename")

	// Success: from is gone, to holds the original content.
	resp, err := httpPostForm(renameURL, url.Values{"session": {sess.ID}, "from": {"a.txt"}, "to": {"a2.txt"}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_, fromErr := os.Stat(filepath.Join(cwd, "a.txt"))
	assert.True(t, os.IsNotExist(fromErr))
	content, rerr := os.ReadFile(filepath.Join(cwd, "a2.txt"))
	require.NoError(t, rerr)
	assert.Equal(t, "hi", string(content))

	// to already exists → 409, both files left untouched.
	resp2, err := httpPostForm(renameURL, url.Values{"session": {sess.ID}, "from": {"a2.txt"}, "to": {"b.txt"}}, "")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
	_, stillErr := os.Stat(filepath.Join(cwd, "a2.txt"))
	assert.NoError(t, stillErr, "from must survive a conflicted rename")

	// from doesn't exist → 404.
	resp3, err := httpPostForm(renameURL, url.Values{"session": {sess.ID}, "from": {"missing.txt"}, "to": {"c.txt"}}, "")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp3.StatusCode)

	// `..` escape on either side → 403.
	resp4, err := httpPostForm(renameURL, url.Values{"session": {sess.ID}, "from": {"../etc"}, "to": {"c.txt"}}, "")
	require.NoError(t, err)
	defer resp4.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp4.StatusCode)

	resp5, err := httpPostForm(renameURL, url.Values{"session": {sess.ID}, "from": {"b.txt"}, "to": {"../escape.txt"}}, "")
	require.NoError(t, err)
	defer resp5.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp5.StatusCode)

	// Empty operand → 400.
	resp6, err := httpPostForm(renameURL, url.Values{"session": {sess.ID}, "from": {""}, "to": {"c.txt"}}, "")
	require.NoError(t, err)
	defer resp6.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp6.StatusCode)
}

// TC-FSW-04: POST /files/delete removes a file or a directory tree, 404s on a missing
// path, 403s on a `..` escape, and refuses to delete the cwd root itself under any
// spelling ("" or ".") with 400.
func TestFilesDelete(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "gone.txt"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "tree", "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "tree", "nested", "deep.txt"), []byte("y"), 0o644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "delete", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "delete")
	deleteURL := formatURL(server, "/files/delete")

	// Single file removed.
	resp, err := httpPostForm(deleteURL, url.Values{"session": {sess.ID}, "path": {"gone.txt"}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_, statErr := os.Stat(filepath.Join(cwd, "gone.txt"))
	assert.True(t, os.IsNotExist(statErr))

	// Directory removed recursively.
	resp2, err := httpPostForm(deleteURL, url.Values{"session": {sess.ID}, "path": {"tree"}}, "")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	_, treeErr := os.Stat(filepath.Join(cwd, "tree"))
	assert.True(t, os.IsNotExist(treeErr))
	_, nestedErr := os.Stat(filepath.Join(cwd, "tree", "nested", "deep.txt"))
	assert.True(t, os.IsNotExist(nestedErr))

	// Not found → 404.
	resp3, err := httpPostForm(deleteURL, url.Values{"session": {sess.ID}, "path": {"never-existed"}}, "")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp3.StatusCode)

	// `..` escape → 403.
	resp4, err := httpPostForm(deleteURL, url.Values{"session": {sess.ID}, "path": {"../../etc/passwd"}}, "")
	require.NoError(t, err)
	defer resp4.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp4.StatusCode)

	// Deleting the cwd root itself — "" and "." both refused with 400; cwd survives.
	resp5, err := httpPostForm(deleteURL, url.Values{"session": {sess.ID}, "path": {""}}, "")
	require.NoError(t, err)
	defer resp5.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp5.StatusCode)

	resp6, err := httpPostForm(deleteURL, url.Values{"session": {sess.ID}, "path": {"."}}, "")
	require.NoError(t, err)
	defer resp6.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp6.StatusCode)

	_, cwdErr := os.Stat(cwd)
	assert.NoError(t, cwdErr, "cwd root must survive both root-deletion attempts")
}
