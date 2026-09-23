package terminal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadsPagesSearchAndStableCursor(t *testing.T) {
	server, _, srv := newDrawerTestServer(t)
	cwd := t.TempDir()
	now := time.Now().Truncate(time.Second)
	for i := 0; i < 55; i++ {
		name := fmt.Sprintf("会议基线-%02d.md", i)
		path := seedUpload(t, cwd, uploadsFileDir, name, []byte("meeting notes"), now.Add(time.Duration(i/2)*time.Second))
		srv.uploads.put(uploadEntry{Kind: "file", AbsPath: path, Name: name, SessionName: "设计评审", CWD: cwd})
	}
	path := seedUpload(t, cwd, uploadsImageDir, "会议截图.png", []byte("image"), now)
	srv.uploads.put(uploadEntry{Kind: "image", AbsPath: path, Name: "会议截图.png", SessionName: "其他会话", CWD: cwd})
	read := func(query string) uploadsResponse {
		t.Helper()
		resp, err := httpGet(formatURL(server, "/uploads?%s", query), "")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var result uploadsResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		return result
	}
	first := read("kind=file&limit=24")
	require.Len(t, first.Items, 24)
	assert.Equal(t, 55, first.Total)
	assert.Equal(t, uploadCounts{Images: 1, Files: 55}, first.Counts)
	assert.ElementsMatch(t, []string{"设计评审", "其他会话"}, first.Sessions)
	require.NotEmpty(t, first.NextCursor)
	// A new upload inserted ahead of the cursor cannot duplicate the boundary row.
	added := seedUpload(t, cwd, uploadsFileDir, "later.md", []byte("new"), now.Add(time.Hour))
	srv.uploads.put(uploadEntry{Kind: "file", AbsPath: added, Name: "later.md", CWD: cwd})
	seen := map[string]bool{}
	for _, it := range first.Items {
		seen[it.ID] = true
	}
	cursor := first.NextCursor
	for cursor != "" {
		page := read("kind=file&limit=24&cursor=" + url.QueryEscape(cursor))
		require.LessOrEqual(t, len(page.Items), 24)
		for _, it := range page.Items {
			require.False(t, seen[it.ID], "duplicate item %s", it.ID)
			seen[it.ID] = true
		}
		cursor = page.NextCursor
	}
	assert.Len(t, seen, 55, "all original files, including equal timestamps, must remain reachable")
	assert.False(t, seen[uploadID(added)])
	// This file is older than page 1; search is over the inventory, not loaded rows.
	result := read("kind=file&limit=24&q=" + url.QueryEscape("评审 基线-00.md") + "&session=" + url.QueryEscape("设计评审"))
	require.Len(t, result.Items, 1)
	assert.Equal(t, "会议基线-00.md", result.Items[0].Name)
	assert.Equal(t, 1, result.Total)
	assert.Empty(t, result.NextCursor)
	oldest := read("kind=file&limit=1&order=oldest")
	require.Len(t, oldest.Items, 1)
	assert.Equal(t, now.UnixMilli(), oldest.Items[0].MtimeMs)
	next := read("kind=file&limit=1&order=oldest&cursor=" + url.QueryEscape(oldest.NextCursor))
	require.Len(t, next.Items, 1)
	assert.NotEqual(t, oldest.Items[0].ID, next.Items[0].ID)
	assert.Equal(t, now.UnixMilli(), next.Items[0].MtimeMs)
	empty := read("kind=file&limit=24&q=missing-filename")
	assert.Empty(t, empty.Items)
	assert.Equal(t, 0, empty.Total)
	assert.Empty(t, empty.NextCursor)
}

func TestUploadsPageRejectsInvalidParameters(t *testing.T) {
	server, _, _ := newDrawerTestServer(t)
	for _, q := range []string{"limit=0", "limit=-1", "limit=lots", "limit=24&cursor=invalid"} {
		resp, err := httpGet(formatURL(server, "/uploads?%s", q), "")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
}
