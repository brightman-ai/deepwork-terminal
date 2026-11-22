package terminal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func httpPutJSON(urlStr, body, authToken string) (*http.Response, error) {
	if authToken == "" {
		authToken = testAuthCode
	}
	req, err := http.NewRequest(http.MethodPut, urlStr, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("X-CLI-Auth", authToken)
	}
	return http.DefaultClient.Do(req)
}

func TestProfile_AutoSeedAndIsolatedDirs0700(t *testing.T) {
	server, _, srv := newWorktreeProfileTestServer(t)

	resp, err := httpGet(server.URL+"/profiles", "")
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Profiles         []ProfileItem `json:"profiles"`
		DefaultProfileID string        `json:"defaultProfileId"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Len(t, out.Profiles, 1)
	assert.Equal(t, "prof-default", out.DefaultProfileID)
	assert.Equal(t, "prof-default", out.Profiles[0].ID)
	assert.True(t, out.Profiles[0].IsDefault)
	assert.False(t, out.Profiles[0].HasAPIKey)

	// Verify <dataDir>/profiles/prof-default/{claude,codex} exist with 0700 permissions.
	for _, sub := range []string{"", "claude", "codex"} {
		dir := filepath.Join(srv.dataDir(), "profiles", "prof-default", sub)
		info, statErr := os.Stat(dir)
		require.NoError(t, statErr, "expected isolated dir %s", dir)
		assert.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "dir %s must be 0700", dir)
	}
}

func TestProfile_CRUDAndStrictAPIKeyRedaction(t *testing.T) {
	server, _, srv := newWorktreeProfileTestServer(t)

	const secretKey = "sk-ant-api03-ULTRA-SECRET-CREDENTIAL-99887766"

	// 1. POST /profiles with gateway key -> 201, wire payload MUST NOT contain raw secret!
	createPayload, _ := json.Marshal(map[string]any{
		"name":      "GLM-5.1 Coding Gateway",
		"engine":    "claude",
		"kind":      "gateway",
		"model":     "glm-5.1-coding",
		"baseUrl":   "https://open.bigmodel.cn/api/anthropic",
		"apiKey":    secretKey,
		"isDefault": true,
	})
	cResp, err := httpPost(server.URL+"/profiles", string(createPayload), "")
	require.NoError(t, err)
	rawCreated, err := io.ReadAll(cResp.Body)
	cResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, cResp.StatusCode)

	assert.NotContains(t, string(rawCreated), secretKey, "raw apiKey leaked in POST /profiles")
	assert.NotContains(t, string(rawCreated), "ULTRA-SECRET", "secret substring leaked")
	assert.NotContains(t, string(rawCreated), `"apiKey"`, "wire struct must never expose apiKey field")

	var createdWrap struct {
		Profile ProfileItem `json:"profile"`
	}
	require.NoError(t, json.Unmarshal(rawCreated, &createdWrap))
	gw := createdWrap.Profile
	require.NotEmpty(t, gw.ID)
	assert.True(t, gw.HasAPIKey)
	assert.Equal(t, "sk-a****7766", gw.APIKeyMasked)
	assert.True(t, gw.IsDefault)

	// Verify 0700 on new profile's isolated dirs and 0600 on profiles.json.
	for _, sub := range []string{"", "claude", "codex"} {
		info, statErr := os.Stat(filepath.Join(srv.dataDir(), "profiles", gw.ID, sub))
		require.NoError(t, statErr)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
	fileInfo, err := os.Stat(filepath.Join(srv.dataDir(), "profiles.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm(), "profiles.json must be 0600")

	// 2. GET /profiles also strictly redacts apiKey and reflects updated defaultProfileId.
	lResp, err := httpGet(server.URL+"/profiles", "")
	require.NoError(t, err)
	rawList, err := io.ReadAll(lResp.Body)
	lResp.Body.Close()
	require.NoError(t, err)
	assert.NotContains(t, string(rawList), secretKey)
	assert.NotContains(t, string(rawList), `"apiKey"`)

	var listOut struct {
		Profiles         []ProfileItem `json:"profiles"`
		DefaultProfileID string        `json:"defaultProfileId"`
	}
	require.NoError(t, json.Unmarshal(rawList, &listOut))
	assert.Equal(t, gw.ID, listOut.DefaultProfileID)

	// 3. PUT /profiles/{id} updating name/model (and even echoing masked key) preserves real key!
	updatePayload, _ := json.Marshal(map[string]any{
		"name":   "GLM-5.1 Pro",
		"model":  "glm-5.1-pro",
		"apiKey": gw.APIKeyMasked, // accidental echo of masked placeholder must not clobber real key
	})
	uResp, err := httpPutJSON(server.URL+"/profiles/"+gw.ID, string(updatePayload), "")
	require.NoError(t, err)
	rawUpdated, err := io.ReadAll(uResp.Body)
	uResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, uResp.StatusCode)
	assert.NotContains(t, string(rawUpdated), secretKey)

	var updatedWrap struct {
		Profile ProfileItem `json:"profile"`
	}
	require.NoError(t, json.Unmarshal(rawUpdated, &updatedWrap))
	assert.Equal(t, "GLM-5.1 Pro", updatedWrap.Profile.Name)
	assert.Equal(t, "glm-5.1-pro", updatedWrap.Profile.Model)
	assert.True(t, updatedWrap.Profile.HasAPIKey)

	// Verify stored secret key is intact via resolveProfileEnv.
	resolvedEnv := srv.resolveProfileEnv(gw.ID, []string{"PATH=/usr/bin"})
	assert.Contains(t, resolvedEnv, "ANTHROPIC_API_KEY="+secretKey)

	// 4. DELETE /profiles/{id} -> 204 No Content, cleans up isolated dir, reconciles default.
	dResp, err := httpDelete(server.URL+"/profiles/"+gw.ID, "")
	require.NoError(t, err)
	dResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, dResp.StatusCode)

	_, statErr := os.Stat(filepath.Join(srv.dataDir(), "profiles", gw.ID))
	assert.True(t, os.IsNotExist(statErr), "deleted profile isolated dir should be cleaned up")

	lResp2, err := httpGet(server.URL+"/profiles", "")
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(lResp2.Body).Decode(&listOut))
	lResp2.Body.Close()
	assert.Equal(t, "prof-default", listOut.DefaultProfileID)
}

func TestProfile_ScanHomeReadOnlyAndDeduplicated(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	claudeMain := filepath.Join(fakeHome, ".claude")
	claudeTeam := filepath.Join(fakeHome, ".claude-team")
	claudeGrouped := filepath.Join(fakeHome, ".claude-profiles", "research")
	codexDir := filepath.Join(fakeHome, ".codex")
	require.NoError(t, os.MkdirAll(claudeMain, 0o700))
	require.NoError(t, os.MkdirAll(claudeTeam, 0o700))
	require.NoError(t, os.MkdirAll(claudeGrouped, 0o700))
	require.NoError(t, os.MkdirAll(codexDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(fakeHome, ".claude.json"), []byte(`{"oauth":true}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(claudeGrouped), "notes.txt"), []byte("not a profile"), 0o600))

	const homeSecret = "sk-home-never-expose-me-11223344"
	require.NoError(t, os.WriteFile(filepath.Join(claudeTeam, "settings.json"), []byte(
		`{"model":"claude-opus-4-6","env":{"ANTHROPIC_API_KEY":"`+homeSecret+`","ANTHROPIC_BASE_URL":"https://proxy.team.example"}}`,
	), 0o600))
	groupedSettings := []byte(`{"model":"claude-research","env":{"ANTHROPIC_API_KEY":"` + homeSecret + `","ANTHROPIC_BASE_URL":"https://proxy.research.example"}}`)
	groupedSettingsPath := filepath.Join(claudeGrouped, "settings.json")
	require.NoError(t, os.WriteFile(groupedSettingsPath, groupedSettings, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(
		"model = \"gpt-5.3-codex-spark\"\n",
	), 0o600))

	server, _, srv := newWorktreeProfileTestServer(t)

	sResp, err := httpPost(server.URL+"/profiles/scan", `{}`, "")
	require.NoError(t, err)
	rawScan, err := io.ReadAll(sResp.Body)
	sResp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, sResp.StatusCode)
	assert.NotContains(t, string(rawScan), homeSecret, "scan must never leak keys from settings.json")

	var scanOut struct {
		Imported int           `json:"imported"`
		Profiles []ProfileItem `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(rawScan, &scanOut))
	assert.Equal(t, 4, scanOut.Imported)
	assert.Len(t, scanOut.Profiles, 5)

	// Second scan is idempotent: imported == 0.
	sResp2, err := httpPost(server.URL+"/profiles/scan", `{}`, "")
	require.NoError(t, err)
	var scanOut2 struct {
		Imported int           `json:"imported"`
		Profiles []ProfileItem `json:"profiles"`
	}
	require.NoError(t, json.NewDecoder(sResp2.Body).Decode(&scanOut2))
	sResp2.Body.Close()
	assert.Equal(t, 0, scanOut2.Imported)
	assert.Len(t, scanOut2.Profiles, 5)

	// Deleting an imported profile must NEVER delete the user's real ~/.claude-team directory!
	var teamID, groupedID string
	for _, p := range scanOut.Profiles {
		switch p.Badge {
		case "~/.claude":
			assert.Equal(t, "Claude · 本机默认", p.Name)
		case "~/.codex":
			assert.Equal(t, "Codex · 本机默认", p.Name)
		case "~/.claude-team":
			teamID = p.ID
			assert.Equal(t, "Claude · claude-team", p.Name)
			assert.Equal(t, "claude-opus-4-6", p.Model)
		case "~/.claude-profiles/research":
			groupedID = p.ID
			assert.Equal(t, "Claude · research", p.Name)
			assert.Equal(t, "claude-research", p.Model)
			assert.Equal(t, "https://proxy.research.example", p.BaseURL)
			assert.False(t, p.HasAPIKey)
		case "~/.claude-profiles":
			t.Error("profile group directory must not be imported as a profile")
		}
	}
	require.NotEmpty(t, teamID)
	require.NotEmpty(t, groupedID)

	// Verify resolveProfileEnv routes CLAUDE_CONFIG_DIR to ~/.claude-team for imported profile.
	env := srv.resolveProfileEnv(teamID, []string{"PATH=/usr/bin"})
	assert.Contains(t, env, "CLAUDE_CONFIG_DIR="+claudeTeam)
	assert.Contains(t, srv.resolveProfileEnv(groupedID, nil), "CLAUDE_CONFIG_DIR="+claudeGrouped)
	groupedAfterScan, err := os.ReadFile(groupedSettingsPath)
	require.NoError(t, err)
	assert.Equal(t, groupedSettings, groupedAfterScan)

	delResp, err := httpDelete(server.URL+"/profiles/"+teamID, "")
	require.NoError(t, err)
	delResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, delResp.StatusCode)

	_, statErr := os.Stat(filepath.Join(claudeTeam, "settings.json"))
	require.NoError(t, statErr, "user's ~/.claude-team must stay completely untouched on delete")
}

func TestProfile_ClaudeSwitchModelDiscoveryDoesNotExposeSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude-profiles", "glm")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	settings := filepath.Join(dir, "settings.json")
	const secret = "mock-key-that-must-not-leak"
	config := `{"model":"claude-fable-5-2","env":{"ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.3-flash[1m]","ANTHROPIC_AUTH_TOKEN":"` + secret + `","ANTHROPIC_BASE_URL":"https://glm.example.invalid/api?token=` + secret + `"}}`
	require.NoError(t, os.WriteFile(settings, []byte(config), 0o600))
	model, baseURL := inspectExternalAgentConfigReadOnly(dir, "claude")
	assert.Equal(t, "glm-5.3-flash[1m]", model)
	assert.Equal(t, "https://glm.example.invalid/api", baseURL)

	srv := newIsolatedBotTestServer(t)
	srv.registerWorktreeAndProfileRoutes(nil)
	res := doJSONReq(t, srv, http.MethodPost, "/profiles/scan", nil)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	assert.Contains(t, res.Body.String(), "glm-5.3-flash[1m]")
	assert.NotContains(t, res.Body.String(), secret)
	assert.NotContains(t, res.Body.String(), "ANTHROPIC_AUTH_TOKEN")
	onDisk, err := os.ReadFile(settings)
	require.NoError(t, err)
	assert.Equal(t, config, string(onDisk))

	fallbackDir := filepath.Join(home, ".claude", "profiles")
	require.NoError(t, os.MkdirAll(fallbackDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(fallbackDir, "glm.json"), []byte(config), 0o600))
	for _, tc := range []struct{ settings, want string }{
		{`{"model":"opus[1m]"}`, "glm-5.3-flash[1m]"},
		{`{"model":"claude-research"}`, "claude-research"},
		{`{"model":"opus","env":{"ANTHROPIC_MODEL":"explicit","ANTHROPIC_DEFAULT_OPUS_MODEL":"ignored"}}`, "explicit"},
		{`{"model":"haiku","env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"haiku-provider"}}`, "haiku-provider"},
	} {
		require.NoError(t, os.WriteFile(settings, []byte(tc.settings), 0o600))
		model, _ := inspectExternalAgentConfigReadOnly(dir, "claude")
		assert.Equal(t, tc.want, model)
	}
}

func TestProfile_TestEndpointProbe(t *testing.T) {
	server, _, _ := newWorktreeProfileTestServer(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-probe-key" || r.Header.Get("x-api-key") != "valid-probe-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	// 1. Valid gateway + key -> ok: true.
	okBody, _ := json.Marshal(map[string]string{
		"baseUrl": upstream.URL,
		"apiKey":  "valid-probe-key",
	})
	resp, err := httpPost(server.URL+"/profiles/test", string(okBody), "")
	require.NoError(t, err)
	var probeRes struct {
		OK        bool   `json:"ok"`
		LatencyMs int64  `json:"latencyMs"`
		Message   string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&probeRes))
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, probeRes.OK)
	assert.GreaterOrEqual(t, probeRes.LatencyMs, int64(0))

	// 2. Wrong key -> 401 -> ok: false, secret never echoed.
	badBody, _ := json.Marshal(map[string]string{
		"baseUrl": upstream.URL,
		"apiKey":  "wrong-secret-probe-key",
	})
	resp2, err := httpPost(server.URL+"/profiles/test", string(badBody), "")
	require.NoError(t, err)
	rawBad, err := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.NoError(t, err)
	assert.NotContains(t, string(rawBad), "wrong-secret-probe-key")
	require.NoError(t, json.Unmarshal(rawBad, &probeRes))
	assert.False(t, probeRes.OK)

	// 3. Non-HTTP scheme rejected cleanly without probing filesystem.
	fileBody, _ := json.Marshal(map[string]string{"baseUrl": "file:///etc/passwd"})
	resp3, err := httpPost(server.URL+"/profiles/test", string(fileBody), "")
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(resp3.Body).Decode(&probeRes))
	resp3.Body.Close()
	assert.False(t, probeRes.OK)
}

func TestProfile_ProbeFailureRedactsURLUserinfo(test *testing.T) {
	server, _, _ := newWorktreeProfileTestServer(test)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "http://redirect-user:redirect-password@[invalid")
		writer.WriteHeader(http.StatusFound)
	}))
	test.Cleanup(upstream.Close)
	endpoint, err := url.Parse(upstream.URL)
	require.NoError(test, err)
	endpoint.User = url.UserPassword("probe-user", "probe-p@ssword")
	body, err := json.Marshal(map[string]string{"baseUrl": endpoint.String()})
	require.NoError(test, err)
	resp, err := httpPost(server.URL+"/profiles/test", string(body), "")
	require.NoError(test, err)
	defer resp.Body.Close()
	require.Equal(test, http.StatusOK, resp.StatusCode)
	var result struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	require.NoError(test, json.NewDecoder(resp.Body).Decode(&result))
	require.False(test, result.OK)
	require.Contains(test, result.Message, upstream.URL)
	for _, secret := range []string{"probe-user", "probe-p@ssword", "probe-p%40ssword", "redirect-user", "redirect-password"} {
		require.NotContains(test, result.Message, secret)
	}
}

func TestProfile_ResolveProfileEnvIsolation(t *testing.T) {
	server, _, srv := newWorktreeProfileTestServer(t)

	base := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"ANTHROPIC_BASE_URL=https://leaked-parent-gateway.invalid",
		"ANTHROPIC_API_KEY=leaked-parent-key",
		"OPENAI_BASE_URL=https://leaked-openai.invalid",
	}

	// Empty or unknown profileID returns base untouched.
	assert.Equal(t, base, srv.resolveProfileEnv("", base))
	assert.Equal(t, base, srv.resolveProfileEnv("does-not-exist", base))

	// Seeded OAuth profile ("prof-default") strips ambient gateway vars and injects isolated dirs.
	oauthEnv := srv.resolveProfileEnv("prof-default", base)
	expectedRoot := filepath.Join(srv.dataDir(), "profiles", "prof-default")
	assert.Contains(t, oauthEnv, "CLAUDE_CONFIG_DIR="+filepath.Join(expectedRoot, "claude"))
	assert.Contains(t, oauthEnv, "CODEX_HOME="+filepath.Join(expectedRoot, "codex"))
	assert.Contains(t, oauthEnv, "DW_PROFILE_ID=prof-default")
	for _, kv := range oauthEnv {
		assert.False(t, strings.HasPrefix(kv, "ANTHROPIC_BASE_URL="), "OAuth profile must strip leaked ANTHROPIC_BASE_URL")
		assert.False(t, strings.HasPrefix(kv, "ANTHROPIC_API_KEY="), "OAuth profile must strip leaked ANTHROPIC_API_KEY")
		assert.False(t, strings.HasPrefix(kv, "OPENAI_BASE_URL="), "OAuth profile must strip leaked OPENAI_BASE_URL")
	}

	// Codex gateway profile sets OPENAI_BASE_URL, OPENAI_API_KEY, CODEX_API_KEY, OPENAI_MODEL.
	codexBody, _ := json.Marshal(map[string]any{
		"name":    "Codex Gateway",
		"engine":  "codex",
		"kind":    "gateway",
		"model":   "gpt-5.3-codex",
		"baseUrl": "https://codex-gw.example/v1",
		"apiKey":  "sk-codex-gw-1234567890",
	})
	cResp, err := httpPost(server.URL+"/profiles", string(codexBody), "")
	require.NoError(t, err)
	var created struct {
		Profile ProfileItem `json:"profile"`
	}
	require.NoError(t, json.NewDecoder(cResp.Body).Decode(&created))
	cResp.Body.Close()
	require.Equal(t, http.StatusCreated, cResp.StatusCode)

	codexEnv := srv.resolveProfileEnv(created.Profile.ID, base)
	codexRoot := filepath.Join(srv.dataDir(), "profiles", created.Profile.ID)
	assert.Contains(t, codexEnv, "CLAUDE_CONFIG_DIR="+filepath.Join(codexRoot, "claude"))
	assert.Contains(t, codexEnv, "CODEX_HOME="+filepath.Join(codexRoot, "codex"))
	assert.Contains(t, codexEnv, "OPENAI_BASE_URL=https://codex-gw.example/v1")
	assert.Contains(t, codexEnv, "OPENAI_API_KEY=sk-codex-gw-1234567890")
	assert.Contains(t, codexEnv, "CODEX_API_KEY=sk-codex-gw-1234567890")
	assert.Contains(t, codexEnv, "OPENAI_MODEL=gpt-5.3-codex")
}

func TestProfile_AutoUpgradesLegacyScannedNames(t *testing.T) {
	server, _, srv := newWorktreeProfileTestServer(t)

	legacyDoc := profilesDiskDoc{
		DefaultProfileID: "prof-default",
		Profiles: []storedProfile{
			{
				ID:        "prof-default",
				Name:      "Claude 默认配置",
				Engine:    "claude",
				Kind:      "oauth",
				Model:     "claude-sonnet-4-6",
				IsDefault: true,
				Badge:     "默认隔离目录",
			},
			{
				ID:     "prof-scan-glm",
				Name:   "Claude · ~/.claude-profiles/glm",
				Engine: "claude",
				Kind:   "imported",
				Badge:  "~/.claude-profiles/glm",
			},
			{
				ID:     "prof-scan-claude",
				Name:   "Claude · ~/.claude",
				Engine: "claude",
				Kind:   "imported",
				Badge:  "~/.claude",
			},
			{
				ID:     "prof-scan-codex",
				Name:   "Codex · ~/.codex",
				Engine: "codex",
				Kind:   "imported",
				Badge:  "~/.codex",
			},
			{
				ID:     "prof-scan-team",
				Name:   "Claude · ~/.claude-team",
				Engine: "claude",
				Kind:   "imported",
				Badge:  "~/.claude-team",
			},
			{
				ID:     "prof-scan-custom",
				Name:   "自定义名字",
				Engine: "claude",
				Kind:   "imported",
				Badge:  "~/.claude-profiles/custom",
			},
		},
	}
	raw, err := json.MarshalIndent(legacyDoc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(srv.profilesFilePath(), raw, 0o600))

	resp, err := httpGet(server.URL+"/profiles", "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Profiles []ProfileItem `json:"profiles"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))

	gotNames := make(map[string]string, len(out.Profiles))
	for _, p := range out.Profiles {
		gotNames[p.ID] = p.Name
	}
	assert.Equal(t, "Claude · glm", gotNames["prof-scan-glm"])
	assert.Equal(t, "Claude · 本机默认", gotNames["prof-scan-claude"])
	assert.Equal(t, "Codex · 本机默认", gotNames["prof-scan-codex"])
	assert.Equal(t, "Claude · claude-team", gotNames["prof-scan-team"])
	assert.Equal(t, "自定义名字", gotNames["prof-scan-custom"])

	// Verify upgraded names were persisted once to profiles.json.
	diskRaw, err := os.ReadFile(srv.profilesFilePath())
	require.NoError(t, err)
	assert.Contains(t, string(diskRaw), "Claude · glm")
	assert.Contains(t, string(diskRaw), "Claude · 本机默认")
	assert.Contains(t, string(diskRaw), "Codex · 本机默认")
	assert.NotContains(t, string(diskRaw), "Claude · ~/.claude-profiles/glm")
}
