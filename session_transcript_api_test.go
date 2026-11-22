package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/brightman-ai/kit/transcript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedSessionInManager(sm *SessionManager, id, cwd, engine string, buf *RingBuffer) *Session {
	sess := &Session{
		ID:     id,
		Name:   id,
		Title:  id,
		CWD:    cwd,
		Engine: engine,
		Buffer: buf,
		Status: StatusRunning,
	}
	sm.sessions.Store(id, sess)
	return sess
}

func TestSessionTranscript_ClaudeStructuredTurnsAndFiles(t *testing.T) {
	claudeConfig := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	t.Setenv("DW_CLAUDE_PROJECTS", "")
	t.Setenv("DW_CODEX_HOME", t.TempDir())

	projectDir := t.TempDir()
	realProjectDir, err := filepath.EvalSymlinks(projectDir)
	require.NoError(t, err)

	shardDir := filepath.Join(claudeConfig, "projects", transcript.EncodeProjectDir(realProjectDir))
	require.NoError(t, os.MkdirAll(shardDir, 0o755))

	jsonl := "" +
		`{"type":"user","uuid":"u1","userType":"external","timestamp":"2026-11-21T05:00:00Z","cwd":` + jsonQuote(realProjectDir) + `,"message":{"role":"user","content":"请检查 @src/pay.ts:14 按钮点击失灵"}}` + "\n" +
		`{"type":"user","uuid":"u-meta","isMeta":true,"userType":"external","timestamp":"2026-11-21T05:00:01Z","message":{"role":"user","content":"injected meta"}}` + "\n" +
		`{"type":"user","uuid":"u-tool","userType":"external","timestamp":"2026-11-21T05:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n" +
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-21T05:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":` + jsonQuote(filepath.Join(realProjectDir, "src/pay.ts")) + `}},{"type":"text","text":"已定位并修复，详见 docs/spec.md:43。"}]}}` + "\n" +
		`{"type":"user","uuid":"u2","userType":"external","timestamp":"2026-11-21T05:01:00Z","message":{"role":"user","content":"<command-name>/clear</command-name>"}}` + "\n" +
		`{"type":"user","uuid":"u3","userType":"external","timestamp":"2026-11-21T05:02:00Z","message":{"role":"user","content":"顺便跑一下单元测试"}}` + "\n" +
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-21T05:02:05Z","message":{"role":"assistant","content":[{"type":"text","text":"全部测试已通过。"}]}}` + "\n"

	require.NoError(t, os.WriteFile(filepath.Join(shardDir, "sess-claude.jsonl"), []byte(jsonl), 0o644))

	srv := newIsolatedBotTestServer(t)
	seedSessionInManager(srv.mgr, "sess-1", projectDir, "claude", nil)

	rec := doJSONReq(t, srv, http.MethodGet, "/sessions/sess-1/transcript", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp SessionTranscriptResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "sess-1", resp.SessionID)
	assert.Equal(t, "claude", resp.AgentTool)
	require.Len(t, resp.Turns, 4)

	assert.Equal(t, "user", resp.Turns[0].Role)
	assert.Contains(t, resp.Turns[0].Text, "src/pay.ts:14")
	assert.Contains(t, resp.Turns[0].Files, "src/pay.ts:14")

	assert.Equal(t, "assistant", resp.Turns[1].Role)
	assert.Contains(t, resp.Turns[1].Text, "已定位并修复")
	assert.Contains(t, resp.Turns[1].Files, "src/pay.ts")
	assert.Contains(t, resp.Turns[1].Files, "docs/spec.md:43")

	assert.Equal(t, "user", resp.Turns[2].Role)
	assert.Equal(t, "顺便跑一下单元测试", resp.Turns[2].Text)

	assert.Equal(t, "assistant", resp.Turns[3].Role)
	assert.Equal(t, "全部测试已通过。", resp.Turns[3].Text)
}

func TestSessionTranscript_CodexStructuredTurnsAndFiles(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("DW_CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("DW_CLAUDE_PROJECTS", "")

	projectDir := t.TempDir()
	realProjectDir, err := filepath.EvalSymlinks(projectDir)
	require.NoError(t, err)

	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "11", "21")
	require.NoError(t, os.MkdirAll(rolloutDir, 0o755))

	rollout := "" +
		`{"type":"session_meta","timestamp":"2026-11-21T06:00:00Z","payload":{"id":"cx-1","source":"cli","cwd":` + jsonQuote(realProjectDir) + `}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-11-21T06:00:01Z","payload":{"type":"user_message","message":"审查一下 bots_guard.go 的重定向拦截"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-11-21T06:00:03Z","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Update File: bots_guard.go\n@@\n*** End Patch"}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-11-21T06:00:05Z","payload":{"type":"agent_message","message":"已核查并补充测试用例 bots_test.go:12。"}}` + "\n"

	require.NoError(t, os.WriteFile(filepath.Join(rolloutDir, "rollout-2026-11-21.jsonl"), []byte(rollout), 0o644))

	srv := newIsolatedBotTestServer(t)
	seedSessionInManager(srv.mgr, "sess-cx", projectDir, "", nil)

	// Auto-detects codex when Claude has no transcript for this cwd.
	rec := doJSONReq(t, srv, http.MethodGet, "/sessions/sess-cx/transcript", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp SessionTranscriptResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "sess-cx", resp.SessionID)
	assert.Equal(t, "codex", resp.AgentTool)
	require.Len(t, resp.Turns, 2)
	assert.Equal(t, "user", resp.Turns[0].Role)
	assert.Contains(t, resp.Turns[0].Files, "bots_guard.go")
	assert.Equal(t, "assistant", resp.Turns[1].Role)
	assert.Contains(t, resp.Turns[1].Text, "已核查并补充测试用例")
	assert.Contains(t, resp.Turns[1].Files, "bots_guard.go")
	assert.Contains(t, resp.Turns[1].Files, "bots_test.go:12")
}

func TestSessionTranscript_ScreenTailFallback(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("DW_CLAUDE_PROJECTS", "")
	t.Setenv("DW_CODEX_HOME", t.TempDir())

	buf := NewRingBuffer(4096)
	_, _ = buf.Write([]byte("$ go test ./...\r\nFAIL bots_guard.go:42: unexpected pass\r\n"))

	srv := newIsolatedBotTestServer(t)
	seedSessionInManager(srv.mgr, "sess-sh", t.TempDir(), "shell", buf)

	rec := doJSONReq(t, srv, http.MethodGet, "/sessions/sess-sh/transcript", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp SessionTranscriptResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "sess-sh", resp.SessionID)
	require.Len(t, resp.Turns, 1)
	assert.Equal(t, "screen-tail-1", resp.Turns[0].ID)
	assert.Equal(t, "assistant", resp.Turns[0].Role)
	assert.Contains(t, resp.Turns[0].Text, "bots_guard.go:42")
	assert.Contains(t, resp.Turns[0].Files, "bots_guard.go:42")
}

func TestSessionTranscript_UnknownSessionAndAuthWrap(t *testing.T) {
	wrappedCalls := 0
	wrap := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			wrappedCalls++
			if r.Header.Get("X-Test-Auth") != "ok" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next(w, r)
		}
	}

	srv := &Server{
		config: Config{DataDir: t.TempDir()},
		mux:    http.NewServeMux(),
		mgr:    &SessionManager{},
	}
	srv.registerBotAndTranscriptRoutes(wrap)

	// Unauthenticated request rejected by wrap.
	unauthReq := httptest.NewRequest(http.MethodGet, "/bots", nil)
	unauthRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(unauthRec, unauthReq)
	assert.Equal(t, http.StatusUnauthorized, unauthRec.Code)

	// Authenticated request for unknown session → 404.
	req := httptest.NewRequest(http.MethodGet, "/sessions/missing-id/transcript", nil)
	req.Header.Set("X-Test-Auth", "ok")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, 2, wrappedCalls)
}

func TestSessionTranscript_DefaultAuthWrap(test *testing.T) {
	server := newIsolatedBotTestServer(test)
	for _, path := range []string{"/bots", "/sessions/missing-id/transcript"} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(test, http.StatusUnauthorized, recorder.Code, path)
	}
	resp := doJSONReq(test, server, http.MethodGet, "/sessions/missing-id/transcript", nil)
	require.Equal(test, http.StatusNotFound, resp.Code)
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
