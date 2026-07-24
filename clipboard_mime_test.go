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

// postPasteUploadRaw is postPasteUploadNamed WITHOUT the 200 assertion, for the paths that
// are supposed to fail — so a test can pin the status code (413 vs 400) rather than just
// "not OK". Returns the status and the decoded body.
func postPasteUploadRaw(t *testing.T, serverURL, sessID, cwd, filename, mime string, data []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = fw.Write(data)
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("mime", mime))
	require.NoError(t, mw.WriteField("cwd", cwd))
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/sessions/%s/paste-upload", serverURL, sessID), &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CLI-Auth", testAuthCode)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestClipboardUpload_NoMIMEGate is the regression gate for a bug that cost a user a real
// file: a .drawio (application/vnd.jgraph.mxfile) was rejected with 400 "unsupported MIME
// type", twice, because an allowlist stood in front of the upload.
//
// That allowlist protected nothing. It already permitted "application/octet-stream" — what a
// browser sends for any binary it cannot name — so every unknown format passed, and only
// formats the browser DID recognize could be blocked. Reverse selection.
//
// deepwork does no parsing and never executes what it stores; the real boundaries are size,
// path containment, and the sandbox dir, all tested elsewhere in this file. So: whatever the
// user hands us, it reaches disk. Each case below was rejected by the old gate.
func TestClipboardUpload_NoMIMEGate(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	dir := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "clip", CWD: dir})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "clip")

	cases := []struct {
		name     string
		filename string
		mime     string
	}{
		// The reported file, verbatim from the server log that caught it.
		{"drawio", "研判 3.0 架构及功能梳理（含问题分析）.drawio", "application/vnd.jgraph.mxfile"},
		{"excalidraw", "sketch.excalidraw", "application/json"},
		{"keynote", "deck.key", "application/vnd.apple.keynote"},
		{"video", "screen-recording.mp4", "video/mp4"},
		{"audio", "voice-memo.m4a", "audio/mp4"},
		// A file picker that cannot name the type sends nothing at all. The old gate
		// rejected "" outright, which is how a plain "Files app" share could fail.
		{"empty mime", "notes.fountain", ""},
		{"unknown vendor mime", "board.miro", "application/vnd.miro.board"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte("payload-for-" + tc.name)
			out := postPasteUploadNamed(t, server.URL, sess.ID, dir, tc.filename, tc.mime, body)

			// It landed, under its own name — the extension is the user's, not extFromMIME's.
			saved, _ := out["path"].(string)
			require.NotEmpty(t, saved, "upload must return the saved path")
			assert.Equal(t, tc.filename, filepath.Base(saved), "original filename must be preserved")

			onDisk, err := os.ReadFile(saved)
			require.NoError(t, err, "the file must actually exist on disk")
			assert.Equal(t, body, onDisk, "bytes must round-trip unmodified")

			// Non-images go to tmp/files/, and stay inside the session sandbox.
			assert.True(t, strings.HasPrefix(saved, dir), "upload must stay under the session cwd")
			assert.Contains(t, saved, filepath.Join("tmp", "files"), "a non-image belongs in tmp/files/")
		})
	}
}

// TestClipboardUpload_TooLargeIs413 pins the ONE boundary that still rejects an upload, and
// pins it to a status the client can act on. 413 (not 400) is what tells the frontend the
// failure is deterministic — i.e. hide 重试, since replaying the same bytes cannot help —
// and limit_mb travels with it so the client never hardcodes the number.
func TestClipboardUpload_TooLargeIs413(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	dir := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "clip", CWD: dir})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "clip")

	oversized := bytes.Repeat([]byte("x"), ClipboardMaxUploadSize+1024)
	status, body := postPasteUploadRaw(t, server.URL, sess.ID, dir, "huge.bin", "application/octet-stream", oversized)

	assert.Equal(t, http.StatusRequestEntityTooLarge, status, "an oversized upload must be 413, not a generic 400")
	assert.EqualValues(t, ClipboardMaxUploadSize>>20, body["limit_mb"], "the limit must travel with the error")
}

// TestSanitizeClipboardFilename_RejectsTraversal: with the MIME gate gone, name sanitation is
// one of the boundaries actually holding the line. filepath.Base("..") is ".." — joining that
// onto the sandbox dir walks OUT of it — so traversal segments must resolve to "" (which makes
// the handler synthesize a name instead).
func TestSanitizeClipboardFilename_RejectsTraversal(t *testing.T) {
	for _, name := range []string{"..", ".", "/", "../../etc/passwd", "  ..  "} {
		got := sanitizeClipboardFilename(name)
		assert.NotContains(t, got, "..", "%q must not sanitize into a traversal segment (got %q)", name, got)
		assert.NotContains(t, got, "/", "%q must sanitize to a single path segment (got %q)", name, got)
	}
	// A legitimate name with dots in it is NOT traversal and must survive intact.
	assert.Equal(t, "v1.2.report.pdf", sanitizeClipboardFilename("v1.2.report.pdf"))
	assert.Equal(t, "arch.drawio", sanitizeClipboardFilename("/tmp/nested/arch.drawio"))
}

// TestExtFromMIMEOfficeDocuments ensures the MIME→ext fallback (used ONLY when the upload has
// no usable original filename, e.g. a clipboard bitmap) yields the right extension instead of
// the generic .bin. Formats absent from this map (.drawio, .excalidraw) need no entry: they
// arrive with a real filename, which is preserved as-is.
func TestExtFromMIMEOfficeDocuments(t *testing.T) {
	cases := map[string]string{
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
		"application/msword":            ".doc",
		"application/vnd.ms-excel":      ".xls",
		"application/vnd.ms-powerpoint": ".ppt",
		"application/zip":               ".zip",
		"image/png":                     ".png",
		"application/octet-stream":      ".bin",
	}
	for mime, want := range cases {
		if got := extFromMIME(mime); got != want {
			t.Errorf("extFromMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}
