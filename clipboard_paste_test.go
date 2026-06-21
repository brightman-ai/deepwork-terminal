package terminal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postPasteUpload POSTs a multipart paste-upload with an explicit active-pane `cwd`
// form field (what the browser sends as pane_current_path) and returns the JSON body.
func postPasteUpload(t *testing.T, serverURL, sessID, cwd string, data []byte) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "shot.png")
	require.NoError(t, err)
	_, err = fw.Write(data)
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("mime", "image/png"))
	require.NoError(t, mw.WriteField("cwd", cwd))
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/sessions/%s/paste-upload", serverURL, sessID), &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CLI-Auth", testAuthCode)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// TC-CLIP-DEDUP: a PC paste uploads the SAME bytes twice — once fresh-saved, once
// hash-deduped. Both responses must return the SAME relPath, resolved against the live
// active-pane cwd the client sent — NOT the session's static launch dir. Regression for
// the bug where the dedup branch used sess.WorkingDir() (e.g. $HOME), so the client got
// two DIFFERENT strings for one image ("tmp/clip/X.png" + "code/.../tmp/clip/X.png").
func TestClipboardPaste_DedupUsesActivePaneCwd(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)

	// Session launch dir (sess.CWD) is the PARENT; the active pane has cd'd into a child.
	// The dedup-branch bug used sess.CWD → a parent-relative path; the fix uses the child.
	launchDir := t.TempDir()
	paneDir := filepath.Join(launchDir, "pane")
	require.NoError(t, os.MkdirAll(paneDir, 0o755))

	_, err := sm.CreateWithOptions(CreateOptions{Name: "clip", CWD: launchDir})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "clip")

	data := []byte("fake-png-bytes-for-hash-dedup")

	first := postPasteUpload(t, server.URL, sess.ID, paneDir, data)
	second := postPasteUpload(t, server.URL, sess.ID, paneDir, data)

	rel1, _ := first["relPath"].(string)
	rel2, _ := second["relPath"].(string)

	// The second upload took the dedup branch.
	assert.Equal(t, true, second["dedup"], "second identical upload should be deduped")
	// Both paths resolve against the active-pane cwd → identical, and pane-relative.
	assert.Equal(t, rel1, rel2, "dedup path must match the freshly-saved path (same base dir)")
	assert.True(t, strings.HasPrefix(rel1, "tmp/clip/"), "path is relative to the active-pane cwd, got %q", rel1)
	assert.NotContains(t, rel2, "..", "dedup path must not escape the active-pane cwd")
	// The file actually lives under the active-pane dir at that rel path.
	assert.FileExists(t, filepath.Join(paneDir, rel1))
}
