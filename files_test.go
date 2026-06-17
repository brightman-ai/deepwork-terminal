package terminal

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-FS-01: safeResolve rejects traversal / absolute / symlink-out escapes and
// accepts in-tree paths (including the root itself).
func TestSafeResolve(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "f.txt"), []byte("hi"), 0o644))

	// A symlink inside root pointing OUTSIDE root must be rejected.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))

	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	// Accepted (resolve to in-tree paths).
	for _, rel := range []string{"", ".", "/", "sub", "sub/f.txt", "/sub/f.txt", "missing.txt"} {
		got, err := safeResolve(root, rel)
		require.NoErrorf(t, err, "rel %q should be allowed", rel)
		assert.Truef(t, got == realRoot || pathWithin(realRoot, got),
			"rel %q resolved out of tree: %s", rel, got)
	}

	// Rejected (escape the tree).
	for _, rel := range []string{"..", "../", "../../etc/passwd", "sub/../../x", "escape/secret"} {
		_, err := safeResolve(root, rel)
		assert.Errorf(t, err, "rel %q must be rejected", rel)
	}
}

func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) &&
		!(len(rel) >= 2 && rel[:2] == "..")
}

// TC-FS-02: GET /files/tree?path=../ → 403 (traversal escape blocked at the API).
func TestFilesTree_TraversalEscape403(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "tree-esc", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "tree-esc")

	resp, err := httpGet(formatURL(server, "/files/tree?session=%s&path=%s", sess.ID, "../"), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Same guard on /files/raw.
	r2, err := httpGet(formatURL(server, "/files/raw?session=%s&path=%s", sess.ID, "../../etc/passwd"), "")
	require.NoError(t, err)
	defer r2.Body.Close()
	assert.Equal(t, http.StatusForbidden, r2.StatusCode)
}

// TC-FS-03: GET /files/tree lists a single directory level, dirs-first then name.
func TestFilesTree_ListsOneLevel(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "zdir"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "adir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "b.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("x"), 0o644))
	// A nested file must NOT appear (single level only).
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "zdir", "deep.txt"), []byte("deep"), 0o644))

	_, err := sm.CreateWithOptions(CreateOptions{Name: "tree-list", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "tree-list")

	resp, err := httpGet(formatURL(server, "/files/tree?session=%s&path=%s", sess.ID, ""), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out treeResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out.Entries, 4)

	// Dirs first (adir, zdir), then files (a.txt, b.txt).
	names := []string{out.Entries[0].Name, out.Entries[1].Name, out.Entries[2].Name, out.Entries[3].Name}
	assert.Equal(t, []string{"adir", "zdir", "a.txt", "b.txt"}, names)
	assert.True(t, out.Entries[0].IsDir)
	assert.False(t, out.Entries[2].IsDir)
	assert.Equal(t, int64(5), out.Entries[3].Size) // b.txt = "hello"
	assert.Equal(t, ".", out.Rel)

	// Nested level: path=zdir shows deep.txt only.
	r2, err := httpGet(formatURL(server, "/files/tree?session=%s&path=%s", sess.ID, "zdir"), "")
	require.NoError(t, err)
	defer r2.Body.Close()
	var out2 treeResponse
	require.NoError(t, json.NewDecoder(r2.Body).Decode(&out2))
	require.Len(t, out2.Entries, 1)
	assert.Equal(t, "deep.txt", out2.Entries[0].Name)
	assert.Equal(t, "zdir", out2.Rel)
}

// TC-FS-04: GET /files/raw streams text, flags binary, and bounds size.
func TestFilesRaw_TextBinaryAndSize(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "note.md"), []byte("# Title\nbody"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "bin.dat"), []byte{0x00, 0x01, 0x02, 'A', 'B'}, 0o644))
	big := make([]byte, rawPreviewMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "big.txt"), big, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "adir"), 0o755))

	_, err := sm.CreateWithOptions(CreateOptions{Name: "raw", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "raw")

	// Text file → streamed bytes, text content-type.
	resp, err := httpGet(formatURL(server, "/files/raw?session=%s&path=%s", sess.ID, "note.md"), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := make([]byte, 64)
	n, _ := resp.Body.Read(body)
	assert.Contains(t, string(body[:n]), "# Title")

	// Binary file → {binary:true,size}.
	rb, err := httpGet(formatURL(server, "/files/raw?session=%s&path=%s", sess.ID, "bin.dat"), "")
	require.NoError(t, err)
	defer rb.Body.Close()
	assert.Equal(t, http.StatusOK, rb.StatusCode)
	var binOut struct {
		Binary bool  `json:"binary"`
		Size   int64 `json:"size"`
	}
	require.NoError(t, json.NewDecoder(rb.Body).Decode(&binOut))
	assert.True(t, binOut.Binary)
	assert.Equal(t, int64(5), binOut.Size)

	// Oversize file → {tooLarge:true,size}.
	rt, err := httpGet(formatURL(server, "/files/raw?session=%s&path=%s", sess.ID, "big.txt"), "")
	require.NoError(t, err)
	defer rt.Body.Close()
	var tooOut struct {
		TooLarge bool  `json:"tooLarge"`
		Size     int64 `json:"size"`
	}
	require.NoError(t, json.NewDecoder(rt.Body).Decode(&tooOut))
	assert.True(t, tooOut.TooLarge)
	assert.Equal(t, int64(rawPreviewMaxBytes+1), tooOut.Size)

	// Directory → 400.
	rd, err := httpGet(formatURL(server, "/files/raw?session=%s&path=%s", sess.ID, "adir"), "")
	require.NoError(t, err)
	defer rd.Body.Close()
	assert.Equal(t, http.StatusBadRequest, rd.StatusCode)
}

// TC-FS-05: GET /files/recent stats agent-edited files; vanished ones list exists:false.
func TestFilesRecent_Shape(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "recent", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "recent")

	// No transcripts for this cwd → valid empty envelope (never 500).
	resp, err := httpGet(formatURL(server, "/files/recent?session=%s", sess.ID), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var out recentFilesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.NotNil(t, out.Items)

	// Unknown session → empty list, 200 (soft-fail like /inputs).
	r2, err := httpGet(formatURL(server, "/files/recent?session=%s", "nope"), "")
	require.NoError(t, err)
	defer r2.Body.Close()
	assert.Equal(t, http.StatusOK, r2.StatusCode)
}

func TestIsBinaryContent(t *testing.T) {
	assert.True(t, isBinaryContent([]byte{0x00, 'a'}, ""), "NUL byte → binary")
	assert.False(t, isBinaryContent([]byte("hello"), "text/plain; charset=utf-8"))
	assert.False(t, isBinaryContent([]byte("{}"), "application/json"))
	assert.True(t, isBinaryContent([]byte{1, 2, 3}, "image/png"))
	assert.False(t, isBinaryContent([]byte("plain"), ""), "no NUL, unknown type → text")
}

// sessionByName returns the live session whose Name matches.
func sessionByName(t *testing.T, sm *SessionManager, name string) *Session {
	t.Helper()
	for _, s := range sm.List() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("session %q not found", name)
	return nil
}
