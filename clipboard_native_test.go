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

// getClipboardFiles calls the zero-copy probe with optional extra headers.
func getClipboardFiles(t *testing.T, serverURL string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, serverURL+"/browser/clipboard/files", nil)
	require.NoError(t, err)
	req.Header.Set("X-CLI-Auth", testAuthCode)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestClipboardFilePaths_ServedNotFourOhFour is the regression gate for the actual defect:
// the shared paste resolver has always probed this URL, deepwork-pro has always served it,
// and standalone deepwork-terminal answered 404. Nothing was logged as an error — the
// resolver treats "not ok" as "no files here" and falls back to uploading — so a local
// paste of local files silently became a network upload and then a 413.
//
// A 404 here is therefore the failure this test exists to catch, which is why it asserts
// the shape of a served response rather than just "not 404".
func TestClipboardFilePaths_ServedNotFourOhFour(t *testing.T) {
	server, _, _ := newDrawerTestServer(t)

	status, body := getClipboardFiles(t, server.URL, nil)

	require.Equal(t, http.StatusOK, status, "the route must be served — a 404 is the bug this endpoint fixes")
	_, ok := body["paths"]
	assert.True(t, ok, "the response must carry a paths key, empty or not — that is the contract the resolver reads")
}

// TestClipboardFilePaths_RejectsAForwardedClient covers the case that makes a loopback
// address a lie: cloudflared runs ON this machine and dials the server from 127.0.0.1, so
// a public tunnel visitor is indistinguishable from a local one by address alone. This
// endpoint reads the SERVER OWNER's clipboard, so answering that visitor would hand a
// stranger the paths of whatever the owner last copied.
//
// 403 rather than an empty list because the two mean different things to the caller: the
// resolver counts a 403 as "this deployment cannot do native paste" and stops probing,
// where an empty list means "nothing is on the clipboard right now".
func TestClipboardFilePaths_RejectsAForwardedClient(t *testing.T) {
	server, _, _ := newDrawerTestServer(t)

	for _, header := range []string{"X-Forwarded-For", "CF-Connecting-IP", "X-Real-Ip"} {
		t.Run(header, func(t *testing.T) {
			status, _ := getClipboardFiles(t, server.URL, map[string]string{header: "203.0.113.7"})
			assert.Equal(t, http.StatusForbidden, status,
				"%s means the loopback address belongs to a proxy, not to the person at this keyboard", header)
		})
	}
}

// TestExistingPaths_DropsWhatIsNoLongerThere: a path is only useful because the agent will
// open it. Returning one that has since been deleted injects an `@reference` that fails far
// away from the paste, whereas dropping it lets the resolver upload the bytes the browser
// still holds — a worse outcome that at least works.
func TestExistingPaths_DropsWhatIsNoLongerThere(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "kept.m4a")
	require.NoError(t, os.WriteFile(real, []byte("x"), 0600))

	usable, dropped := existingPaths([]string{real, filepath.Join(dir, "gone.m4a"), ""})

	assert.Equal(t, []string{real}, usable)
	assert.Equal(t, 1, dropped, "the drop must be counted, not swallowed — it is the only signal that the reader returned something unexpected")
}
