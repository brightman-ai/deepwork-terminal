package terminal

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/muxd"
	"github.com/brightman-ai/kit/authgate"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchV11_AuthenticatedRoutes(test *testing.T) {
	test.Setenv("DW_CLAUDE_PROJECTS", test.TempDir())
	test.Setenv("DW_CODEX_HOME", test.TempDir())
	test.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	test.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dataDir := test.TempDir()
	repo, err := filepath.EvalSymlinks(seedCleanWorktreeRepo(test))
	require.NoError(test, err)

	baseEnv := []string{
		"PATH=/usr/bin:/bin",
		"CLAUDE_CONFIG_DIR=/base/claude",
		"CODEX_HOME=/base/codex",
		"ANTHROPIC_BASE_URL=https://base.example.invalid",
	}
	pipeFactory, writeEnds := pipePTYFactoryFunc()
	spawnEnvs := make(chan []string, 2)
	manager := NewSessionManagerWithFactory(4096, "/bin/sh", func(options muxd.SpawnOptions) (*os.File, *exec.Cmd, error) {
		spawnEnvs <- slices.Clone(options.Env)
		return pipeFactory(options)
	})
	manager.EnvSource = func() []string { return baseEnv }
	test.Cleanup(writeEnds.closeAll)
	test.Cleanup(manager.DestroyAll)

	server := &Server{
		config:       Config{DataDir: dataDir, AuthCode: testAuthCode},
		mux:          http.NewServeMux(),
		mgr:          manager,
		authThrottle: authgate.NewThrottle(),
	}
	server.registerRoutes()

	request := func(method, path string, payload any, wantStatus int) []byte {
		test.Helper()
		var body bytes.Buffer
		if payload != nil {
			require.NoError(test, json.NewEncoder(&body).Encode(payload))
		}
		send := func(token string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, bytes.NewReader(body.Bytes()))
			if payload != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			if token != "" {
				req.Header.Set("X-CLI-Auth", token)
			}
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, req)
			return recorder
		}
		require.Equalf(test, http.StatusUnauthorized, send("").Code, "%s %s without auth", method, path)
		resp := send(testAuthCode)
		require.Equalf(test, wantStatus, resp.Code, "%s %s: %s", method, path, resp.Body)
		return resp.Body.Bytes()
	}

	var worktrees worktreeListResponse
	worktreeBody := request(http.MethodGet, "/git/worktrees?cwd="+url.QueryEscape(repo), nil, http.StatusOK)
	require.NoError(test, json.Unmarshal(worktreeBody, &worktrees))
	require.Equal(test, repo, worktrees.Root)
	require.Len(test, worktrees.Worktrees, 1)
	require.True(test, worktrees.Worktrees[0].IsPrimary)
	require.Equal(test, repo, worktrees.Worktrees[0].Path)

	const gatewayKey = "test-only-workbench-gateway-key"
	const gatewayURL = "https://gateway.example.invalid"
	const gatewayModel = "workbench-test-model"
	profileBody := request(http.MethodPost, "/profiles", map[string]string{
		"name": "Workbench Gateway", "engine": "claude", "kind": "gateway",
		"model": gatewayModel, "baseUrl": gatewayURL, "apiKey": gatewayKey,
	}, http.StatusCreated)
	require.NotContains(test, string(profileBody), gatewayKey)
	require.NotContains(test, string(profileBody), `"apiKey"`)
	var created struct {
		Profile ProfileItem `json:"profile"`
	}
	require.NoError(test, json.Unmarshal(profileBody, &created))
	require.NotEmpty(test, created.Profile.ID)
	require.True(test, created.Profile.HasAPIKey)
	require.NotEmpty(test, created.Profile.APIKeyMasked)

	profilesBody := request(http.MethodGet, "/profiles", nil, http.StatusOK)
	require.NotContains(test, string(profilesBody), gatewayKey)
	require.NotContains(test, string(profilesBody), `"apiKey"`)
	var listed struct {
		Profiles []ProfileItem `json:"profiles"`
	}
	require.NoError(test, json.Unmarshal(profilesBody, &listed))
	require.Contains(test, listed.Profiles, created.Profile)

	sessionBody := request(http.MethodPost, "/sessions", map[string]string{
		"name": "profiled", "engine": "claude", "cwd": repo, "profileId": created.Profile.ID,
	}, http.StatusCreated)
	require.NotContains(test, string(sessionBody), gatewayKey)
	var session struct {
		ID        string `json:"id"`
		ProfileID string `json:"profileId"`
	}
	require.NoError(test, json.Unmarshal(sessionBody, &session))
	require.NotEmpty(test, session.ID)
	require.Equal(test, created.Profile.ID, session.ProfileID)
	profileDir := filepath.Join(dataDir, "profiles", created.Profile.ID)
	require.Len(test, spawnEnvs, 1)
	require.ElementsMatch(test, []string{
		"PATH=/usr/bin:/bin",
		"CLAUDE_CONFIG_DIR=" + filepath.Join(profileDir, "claude"),
		"CODEX_HOME=" + filepath.Join(profileDir, "codex"),
		"ANTHROPIC_BASE_URL=" + gatewayURL,
		"ANTHROPIC_API_KEY=" + gatewayKey,
		"ANTHROPIC_AUTH_TOKEN=" + gatewayKey,
		"ANTHROPIC_MODEL=" + gatewayModel,
		"DW_PROFILE_ID=" + created.Profile.ID,
	}, <-spawnEnvs)

	plainBody := request(http.MethodPost, "/sessions", map[string]string{"cwd": repo}, http.StatusCreated)
	require.NotContains(test, string(plainBody), `"profileId"`)
	require.Len(test, spawnEnvs, 1)
	require.Equal(test, baseEnv, <-spawnEnvs)

	var bots struct {
		Bots []BotDetail `json:"bots"`
	}
	require.NoError(test, json.Unmarshal(request(http.MethodGet, "/bots", nil, http.StatusOK), &bots))
	require.True(test, slices.ContainsFunc(bots.Bots, func(bot BotDetail) bool { return bot.Slug == "scout" }))
	var check struct {
		Blocked bool   `json:"blocked"`
		Error   string `json:"error"`
	}
	simulatedCheck := map[string]any{"precheckCmd": "rm -rf /tmp/x", "simulateAlert": struct{}{}}
	checkBody := request(http.MethodPost, "/bots/scout/run-check", simulatedCheck, http.StatusBadRequest)
	require.NoError(test, json.Unmarshal(checkBody, &check))
	require.True(test, check.Blocked)
	require.NotEmpty(test, check.Error)

	const terminalOutput = "Workbench integration output"
	sess, err := manager.Get(session.ID)
	require.NoError(test, err)
	writer := writeEnds.Get(0)
	require.NotNil(test, writer)
	_, err = writer.WriteString(terminalOutput + "\r\n")
	require.NoError(test, err)
	require.Eventually(test, func() bool {
		return bytes.Contains(sess.Buffer.Read(), []byte(terminalOutput))
	}, 2*time.Second, 5*time.Millisecond)

	var transcript SessionTranscriptResponse
	transcriptBody := request(http.MethodGet, "/sessions/"+session.ID+"/transcript", nil, http.StatusOK)
	require.NoError(test, json.Unmarshal(transcriptBody, &transcript))
	require.Equal(test, session.ID, transcript.SessionID)
	require.Equal(test, repo, transcript.CWD)
	require.Len(test, transcript.Turns, 1)
	require.Equal(test, "assistant", transcript.Turns[0].Role)
	require.Contains(test, transcript.Turns[0].Text, terminalOutput)
}
