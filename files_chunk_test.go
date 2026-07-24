package terminal

import (
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chunkInitResp mirrors the POST /files/upload/init 200 body.
type chunkInitResp struct {
	UploadID    string `json:"uploadId"`
	ChunkSize   int64  `json:"chunkSize"`
	TotalChunks int    `json:"totalChunks"`
	Received    []int  `json:"received"`
}

// makeChunkPayload returns n deterministic pseudo-random bytes. A fixed seed makes any
// misassembly (a swapped/duplicated/truncated chunk) fail the exact-content assertion —
// unlike a short repeating pattern, which an 8-MiB-aligned chunk swap would hide.
func makeChunkPayload(n int) []byte {
	data := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(data)
	return data
}

// httpPostRaw posts a RAW (non-multipart) body — the chunk endpoint reads r.Body directly.
func httpPostRaw(rawURL string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-CLI-Auth", testAuthCode)
	return http.DefaultClient.Do(req)
}

// httpGetAuth performs an authenticated GET (status endpoint).
func httpGetAuth(rawURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CLI-Auth", testAuthCode)
	return http.DefaultClient.Do(req)
}

// chunkInit POSTs /files/upload/init and returns the decoded 200 body.
func chunkInit(t *testing.T, server *httptest.Server, session, cwd, dir, name string, size int) chunkInitResp {
	t.Helper()
	form := url.Values{
		"session": {session}, "cwd": {cwd}, "dir": {dir},
		"name": {name}, "size": {strconv.Itoa(size)},
	}
	resp, err := httpPostForm(formatURL(server, "/files/upload/init"), form, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out chunkInitResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// sendChunk POSTs one raw slice to /files/upload/chunk and asserts 200.
func sendChunk(t *testing.T, server *httptest.Server, uploadID string, index int, data []byte) {
	t.Helper()
	u := formatURL(server, "/files/upload/chunk") + "?uploadId=" + uploadID + "&index=" + strconv.Itoa(index)
	resp, err := httpPostRaw(u, data)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// sendAllChunks slices payload by init.ChunkSize and uploads every chunk in order.
func sendAllChunks(t *testing.T, server *httptest.Server, init chunkInitResp, payload []byte) {
	t.Helper()
	cs := int(init.ChunkSize)
	for i := 0; i < init.TotalChunks; i++ {
		start := i * cs
		end := start + cs
		if end > len(payload) {
			end = len(payload)
		}
		sendChunk(t, server, init.UploadID, i, payload[start:end])
	}
}

// TC-CHK-01: init → chunk(all) → complete writes the exact bytes to the target path, under
// a target sub-dir that did not exist yet (complete MkdirAll's it), and returns the shared
// success shape.
func TestChunkUploadRoundTrip(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "chunk-rt", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "chunk-rt")

	// 9 MiB spans two chunks (8 MiB + 1 MiB) so the multi-part reassembly AND the smaller
	// last-chunk sizing are both exercised, while staying under the 10 MiB default cap.
	payload := makeChunkPayload(9 << 20)

	init := chunkInit(t, server, sess.ID, cwd, "sub/dir", "big.bin", len(payload))
	require.Equal(t, 2, init.TotalChunks)
	require.Empty(t, init.Received)
	require.Equal(t, int64(8<<20), init.ChunkSize)

	sendAllChunks(t, server, init, payload)

	// complete
	resp, err := httpPostForm(formatURL(server, "/files/upload/complete"),
		url.Values{"uploadId": {init.UploadID}, "session": {sess.ID}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var done struct {
		Path     string `json:"path"`
		RelPath  string `json:"relPath"`
		Size     int64  `json:"size"`
		Filename string `json:"filename"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&done))

	assert.Equal(t, "big.bin", done.Filename)
	assert.Equal(t, int64(len(payload)), done.Size)
	assert.Equal(t, filepath.Join("sub", "dir", "big.bin"), done.RelPath)

	// The assembled file exists at the target and its bytes match the source exactly.
	got, rerr := os.ReadFile(filepath.Join(cwd, "sub", "dir", "big.bin"))
	require.NoError(t, rerr)
	assert.True(t, bytes.Equal(payload, got), "reassembled bytes must equal source")
}

// TC-CHK-02: a partial upload resumes — re-init reports the chunks already on disk, status
// agrees, completing early 409s with the missing set, and finishing the gap succeeds.
func TestChunkUploadResume(t *testing.T) {
	server, sm, srv := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "chunk-resume", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "chunk-resume")

	payload := makeChunkPayload(9 << 20)
	cs := 8 << 20

	init := chunkInit(t, server, sess.ID, cwd, "", "resume.bin", len(payload))
	require.Equal(t, 2, init.TotalChunks)

	// Send only chunk 0 (simulating a dropped connection before the last chunk).
	sendChunk(t, server, init.UploadID, 0, payload[:cs])

	// Re-init (same file → same uploadId) reports chunk 0 already received.
	reinit := chunkInit(t, server, sess.ID, cwd, "", "resume.bin", len(payload))
	assert.Equal(t, init.UploadID, reinit.UploadID)
	assert.Equal(t, []int{0}, reinit.Received)

	// GET status agrees with the re-init view.
	statusResp, err := httpGetAuth(formatURL(server, "/files/upload/status") + "?uploadId=" + init.UploadID)
	require.NoError(t, err)
	defer statusResp.Body.Close()
	require.Equal(t, http.StatusOK, statusResp.StatusCode)
	var st struct {
		Name        string `json:"name"`
		Size        int64  `json:"size"`
		TotalChunks int    `json:"totalChunks"`
		Received    []int  `json:"received"`
	}
	require.NoError(t, json.NewDecoder(statusResp.Body).Decode(&st))
	assert.Equal(t, "resume.bin", st.Name)
	assert.Equal(t, int64(len(payload)), st.Size)
	assert.Equal(t, []int{0}, st.Received)

	// Completing now (chunk 1 still missing) → 409 with missing:[1].
	early, err := httpPostForm(formatURL(server, "/files/upload/complete"),
		url.Values{"uploadId": {init.UploadID}, "session": {sess.ID}}, "")
	require.NoError(t, err)
	defer early.Body.Close()
	require.Equal(t, http.StatusConflict, early.StatusCode)
	var conflict struct {
		Missing []int `json:"missing"`
	}
	require.NoError(t, json.NewDecoder(early.Body).Decode(&conflict))
	assert.Equal(t, []int{1}, conflict.Missing)

	// Send the missing tail chunk, then complete succeeds with correct bytes.
	sendChunk(t, server, init.UploadID, 1, payload[cs:])
	done, err := httpPostForm(formatURL(server, "/files/upload/complete"),
		url.Values{"uploadId": {init.UploadID}, "session": {sess.ID}}, "")
	require.NoError(t, err)
	defer done.Body.Close()
	require.Equal(t, http.StatusOK, done.StatusCode)

	got, rerr := os.ReadFile(filepath.Join(cwd, "resume.bin"))
	require.NoError(t, rerr)
	assert.True(t, bytes.Equal(payload, got))

	// Staging dir is gone after completion.
	assert.NoDirExists(t, srv.chunkStagingDir(init.UploadID))
}

// TC-CHK-03: init with size over the effective cap → 413 carrying limit_mb (default 10).
func TestChunkUploadOversizeInit(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "chunk-big", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "chunk-big")

	over := int(UploadLimitBytes()) + 1
	resp, err := httpPostForm(formatURL(server, "/files/upload/init"),
		url.Values{"session": {sess.ID}, "cwd": {cwd}, "name": {"huge.bin"}, "size": {strconv.Itoa(over)}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	var body struct {
		LimitMB int64 `json:"limit_mb"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, UploadLimitBytes()>>20, body.LimitMB)
}

// TC-CHK-04: complete before every chunk arrives → 409 with the missing set, no target written.
func TestChunkUploadMissingChunkComplete(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "chunk-missing", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "chunk-missing")

	payload := makeChunkPayload(9 << 20)
	init := chunkInit(t, server, sess.ID, cwd, "", "partial.bin", len(payload))
	require.Equal(t, 2, init.TotalChunks)

	// Send only chunk 1, leaving chunk 0 missing.
	sendChunk(t, server, init.UploadID, 1, payload[8<<20:])

	resp, err := httpPostForm(formatURL(server, "/files/upload/complete"),
		url.Values{"uploadId": {init.UploadID}, "session": {sess.ID}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	var conflict struct {
		Missing []int `json:"missing"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&conflict))
	assert.Equal(t, []int{0}, conflict.Missing)

	// Nothing was published to the target on a failed complete.
	_, statErr := os.Stat(filepath.Join(cwd, "partial.bin"))
	assert.True(t, os.IsNotExist(statErr))
}

// TC-CHK-05: abort removes the staging dir.
func TestChunkUploadAbort(t *testing.T) {
	server, sm, srv := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "chunk-abort", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "chunk-abort")

	payload := makeChunkPayload(9 << 20)
	init := chunkInit(t, server, sess.ID, cwd, "", "abort.bin", len(payload))
	sendChunk(t, server, init.UploadID, 0, payload[:8<<20])

	staging := srv.chunkStagingDir(init.UploadID)
	require.DirExists(t, staging)

	resp, err := httpPostForm(formatURL(server, "/files/upload/abort"),
		url.Values{"uploadId": {init.UploadID}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.NoDirExists(t, staging)
}

// TC-CHK-06: GET /files/raw?download=1 is now ranged/resumable (http.ServeContent). A full
// request advertises Accept-Ranges and streams the whole attachment; a Range request returns
// 206 Partial Content with exactly the requested byte window — the download-side companion to
// the chunked upload (a big download that drops resumes instead of restarting).
func TestFilesDownloadRange(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	body := makeChunkPayload(4096)
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "blob.bin"), body, 0o644))
	_, err := sm.CreateWithOptions(CreateOptions{Name: "dl", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "dl")
	dlURL := formatURL(server, "/files/raw?session=%s&path=blob.bin&download=1", sess.ID)

	// Full download: 200, attachment disposition, Accept-Ranges advertised, exact bytes.
	full, err := httpGetAuth(dlURL)
	require.NoError(t, err)
	defer full.Body.Close()
	require.Equal(t, http.StatusOK, full.StatusCode)
	assert.Equal(t, "bytes", full.Header.Get("Accept-Ranges"))
	assert.Contains(t, full.Header.Get("Content-Disposition"), "attachment")
	assert.Equal(t, "application/octet-stream", full.Header.Get("Content-Type"))
	gotFull, _ := io.ReadAll(full.Body)
	assert.True(t, bytes.Equal(body, gotFull))

	// Ranged download: 206 Partial Content with exactly bytes[100:200].
	req, err := http.NewRequest("GET", dlURL, nil)
	require.NoError(t, err)
	req.Header.Set("X-CLI-Auth", testAuthCode)
	req.Header.Set("Range", "bytes=100-199")
	part, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer part.Body.Close()
	require.Equal(t, http.StatusPartialContent, part.StatusCode)
	gotPart, _ := io.ReadAll(part.Body)
	assert.Equal(t, 100, len(gotPart))
	assert.True(t, bytes.Equal(body[100:200], gotPart))
}
