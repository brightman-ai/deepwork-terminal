package terminal

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClipboardChunksPreserveAttachmentPolicy(t *testing.T) {
	server, sm, sut := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "attachment-chunks", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "attachment-chunks")
	payload := makeChunkPayload(25 << 10)
	initialize := func(name, mime string) chunkInitResp {
		form := url.Values{"name": {name}, "mime": {mime}, "size": {strconv.Itoa(len(payload))}, "chunkSize": {"8192"}}
		resp, err := httpPostForm(formatURL(server, "/sessions/%s/paste-upload/init", sess.ID), form, "")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var init chunkInitResp
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&init))
		require.Equal(t, int64(8192), init.ChunkSize)
		require.Equal(t, 4, init.TotalChunks)
		return init
	}
	finish := func(init chunkInitResp) map[string]any {
		sendAllChunks(t, server, init, payload)
		stagingDir := sut.chunkStagingDir(init.UploadID)
		info, err := os.Stat(stagingDir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), info.Mode().Perm())
		for _, name := range []string{"meta.json", "0.part"} {
			info, err := os.Stat(filepath.Join(stagingDir, name))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		}
		resp, err := httpPostForm(formatURL(server, "/files/upload/complete"), url.Values{"uploadId": {init.UploadID}}, "")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var done map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&done))
		path := done["path"].(string)
		actual, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, payload, actual)
		info, err = os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		return done
	}
	// Overlapping same-name/size selections must never share staging identities.
	first := initialize("示例 录音.m4a", "audio/mp4")
	second := initialize("示例 录音.m4a", "audio/mp4")
	require.NotEqual(t, first.UploadID, second.UploadID)
	one := finish(first)
	two := finish(second)
	require.Equal(t, "示例 录音.m4a", one["filename"])
	require.True(t, strings.HasPrefix(one["relPath"].(string), "tmp/files/"))
	require.Equal(t, one["path"], two["path"])
	require.Equal(t, true, two["dedup"])
	image := finish(initialize("diagram.png", "image/png"))
	require.True(t, strings.HasPrefix(image["relPath"].(string), "tmp/clip/"))
	require.Equal(t, "diagram.png", filepath.Base(image["path"].(string)))
}

func TestChunkUploadNegotiatedSizeCannotReuseOldResume(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "chunk-size", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "chunk-size")
	legacy := chunkInit(t, server, sess.ID, cwd, ".", "sample.bin", 25000)
	form := url.Values{"session": {sess.ID}, "name": {"sample.bin"}, "size": {"25000"}, "chunkSize": {"8192"}}
	resp, err := httpPostForm(formatURL(server, "/files/upload/init"), form, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var smaller chunkInitResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&smaller))
	require.NotEqual(t, legacy.UploadID, smaller.UploadID)
	require.Equal(t, 4, smaller.TotalChunks)
	// A subsequent estimate changes, but the negotiated transfer's ACKed parts
	// and boundaries survive a retry or browser reload.
	sendChunk(t, server, smaller.UploadID, 0, makeChunkPayload(8192))
	form.Set("chunkSize", "65536")
	resumed, err := httpPostForm(formatURL(server, "/files/upload/init"), form, "")
	require.NoError(t, err)
	defer resumed.Body.Close()
	var resume chunkInitResp
	require.NoError(t, json.NewDecoder(resumed.Body).Decode(&resume))
	require.Equal(t, smaller.UploadID, resume.UploadID)
	require.Equal(t, int64(8192), resume.ChunkSize)
	require.Equal(t, []int{0}, resume.Received)
}

func TestClipboardChunkResumeKeepsPartsAndBindsSession(t *testing.T) {
	server, sm, _ := newDrawerTestServer(t)
	cwd := t.TempDir()
	_, err := sm.CreateWithOptions(CreateOptions{Name: "resume-owner", CWD: cwd})
	require.NoError(t, err)
	sess := sessionByName(t, sm, "resume-owner")
	form := url.Values{"name": {"recording.m4a"}, "mime": {"audio/mp4"}, "size": {"25000"}, "chunkSize": {"8192"}}
	initURL := formatURL(server, "/sessions/%s/paste-upload/init", sess.ID)
	resp, err := httpPostForm(initURL, form, "")
	require.NoError(t, err)
	var init chunkInitResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&init))
	resp.Body.Close()
	payload := makeChunkPayload(25000)
	sendChunk(t, server, init.UploadID, 0, payload[:8192])
	form.Set("resumeId", init.UploadID)
	form.Set("chunkSize", "65536")
	resp, err = httpPostForm(initURL, form, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var resumed chunkInitResp
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&resumed))
	resp.Body.Close()
	require.Equal(t, init.UploadID, resumed.UploadID)
	require.Equal(t, int64(8192), resumed.ChunkSize)
	require.Equal(t, []int{0}, resumed.Received)
	form.Set("size", "25001")
	resp, err = httpPostForm(initURL, form, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
	form.Set("size", "25000")
	_, err = sm.CreateWithOptions(CreateOptions{Name: "other-owner", CWD: cwd})
	require.NoError(t, err)
	other := sessionByName(t, sm, "other-owner")
	resp, err = httpPostForm(formatURL(server, "/sessions/%s/paste-upload/init", other.ID), form, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
	// The refused attempt neither deletes nor corrupts the original valid upload.
	for i := 1; i < init.TotalChunks; i++ {
		start, end := i*8192, min((i+1)*8192, len(payload))
		sendChunk(t, server, init.UploadID, i, payload[start:end])
	}
	resp, err = httpPostForm(formatURL(server, "/files/upload/complete"), url.Values{"uploadId": {init.UploadID}}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var done struct{ Path string }
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&done))
	data, err := os.ReadFile(done.Path)
	require.NoError(t, err)
	require.Equal(t, payload, data)
}
