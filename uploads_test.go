package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDrawerTestServer builds a CLI server with an ISOLATED DataDir (so the
// cross-session upload index never touches the real ~/.dw-terminal) and a
// pipe-backed session manager. It returns the live test server, the manager, and
// the underlying *Server (whose .uploads index the tests drive directly).
func newDrawerTestServer(t *testing.T) (*httptest.Server, *SessionManager, *Server) {
	t.Helper()

	factory, writeEnds := pipePTYFactoryFunc()
	sm := NewSessionManagerWithFactory(4096, "/bin/sh", factory)

	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     testAuthCode,
		DataDir:      t.TempDir(), // isolate the persisted index per-test
	}))
	require.NoError(t, err)
	srv.mgr = sm

	server := httptest.NewServer(srv.mux)
	t.Cleanup(func() {
		sm.DestroyAll()
		server.Close()
		writeEnds.closeAll()
	})
	return server, sm, srv
}

// seedUpload writes a file under {cwd}/{sub}/{hourDir}/{name}, bumps its mtime for
// deterministic ordering, and returns its absolute path.
func seedUpload(t *testing.T, cwd, sub, name string, body []byte, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(cwd, filepath.FromSlash(sub), "06-14-09")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, body, 0o600))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
	return p
}

// TC-UP-01: GET /uploads lists images + files across sessions, newest-first, with
// opaque id-based raw URLs. The backfill picks up on-disk files of ALIVE sessions
// (and skips .tmp partials).
func TestIntegration_UploadsList(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)

	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "uploads-list", CWD: cwd})
	require.NoError(t, err)

	now := time.Now()
	seedUpload(t, cwd, uploadsImageDir, "old.png", []byte("PNGOLD"), now.Add(-2*time.Minute))
	seedUpload(t, cwd, uploadsImageDir, "new.png", []byte("PNGNEW"), now)
	seedUpload(t, cwd, uploadsFileDir, "notes.txt", []byte("hello"), now.Add(-1*time.Minute))
	// A .tmp partial must be ignored by the backfill.
	seedUpload(t, cwd, uploadsImageDir, "partial.png.tmp", []byte("X"), now)

	resp, err := httpGet(formatURL(server, "/uploads"), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out uploadsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))

	var images, files []uploadItem
	for _, it := range out.Items {
		switch it.Kind {
		case "image":
			images = append(images, it)
		case "file":
			files = append(files, it)
		}
	}
	require.Len(t, images, 2, ".tmp must be skipped")
	require.Len(t, files, 1)

	// Every item carries its originating session name + an id-based raw URL.
	assert.Equal(t, "uploads-list", images[0].SessionName)
	assert.NotEmpty(t, images[0].ID)
	assert.Contains(t, images[0].URL, "/uploads/raw?id=")
	assert.Equal(t, "notes.txt", files[0].Name)

	// ?kind filter narrows to one category.
	r2, err := httpGet(formatURL(server, "/uploads?kind=file"), "")
	require.NoError(t, err)
	defer r2.Body.Close()
	var out2 uploadsResponse
	require.NoError(t, json.NewDecoder(r2.Body).Decode(&out2))
	require.Len(t, out2.Items, 1)
	assert.Equal(t, "file", out2.Items[0].Kind)
}

// TC-UP-02: GET /uploads with nothing indexed returns an empty list (not 500).
func TestIntegration_UploadsListEmpty(t *testing.T) {
	server, _, _ := newDrawerTestServer(t)

	resp, err := httpGet(formatURL(server, "/uploads"), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out uploadsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Empty(t, out.Items)
}

// TC-UP-03: GET /uploads/raw?id= serves the recorded file's bytes with a type.
func TestIntegration_UploadsRawServes(t *testing.T) {
	server, _, srv := newDrawerTestServer(t)

	cwd := t.TempDir()
	abs := seedUpload(t, cwd, uploadsFileDir, "doc.txt", []byte("the body"), time.Now())
	srv.uploads.put(uploadEntry{Kind: "file", AbsPath: abs, Name: "doc.txt", SessionName: "s1", CWD: cwd})
	id := uploadID(abs)

	resp, err := httpGet(formatURL(server, "/uploads/raw?id=%s", id), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

	var buf [64]byte
	n, _ := resp.Body.Read(buf[:])
	assert.Equal(t, "the body", string(buf[:n]))
}

// TC-UP-04: GET /uploads/raw rejects anything not in the index whitelist with 404.
// There is NO client-supplied path — an opaque id is the only token — so traversal
// is structurally impossible; an unknown/forged id simply finds no entry.
func TestIntegration_UploadsRawRejectsUnknownID(t *testing.T) {
	server, _, _ := newDrawerTestServer(t)

	for _, id := range []string{"", "deadbeef", uploadID("/etc/passwd")} {
		resp, err := httpGet(formatURL(server, "/uploads/raw?id=%s", id), "")
		require.NoError(t, err)
		resp.Body.Close()
		if id == "" {
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		} else {
			assert.Equalf(t, http.StatusNotFound, resp.StatusCode, "forged id %q must 404", id)
		}
	}
}

// TC-IN-01: GET /inputs always returns a valid {items:[...]} envelope — human
// prompts when transcripts exist, an empty array otherwise — never an error.
func TestIntegration_InputsShape(t *testing.T) {
	server, _, _ := newDrawerTestServer(t)

	resp, err := httpGet(formatURL(server, "/inputs"), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out inputsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	for _, it := range out.Items {
		assert.Contains(t, []string{"claude", "codex"}, it.Source)
		assert.NotEmpty(t, it.Text)
	}
}
