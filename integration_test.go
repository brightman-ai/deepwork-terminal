package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-08-I-01: POST creates a session.
func TestIntegration_CreateSession(t *testing.T) {
	server, _, _ := NewTestCLIServer(t)

	resp, err := httpPost(formatURL(server, "/sessions"), `{"name":"test-create"}`, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.NotEmpty(t, result["id"])
	assert.Equal(t, "test-create", result["name"])
	assert.Equal(t, "running", result["status"])
}

// TC-08-I-02: GET lists sessions.
func TestIntegration_ListSessions(t *testing.T) {
	server, sm, _ := NewTestCLIServer(t)

	// Create 2 sessions directly.
	sm.Create("s1")
	sm.Create("s2")

	resp, err := httpGet(formatURL(server, "/sessions"), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var sessions []map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&sessions)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
}

// TC-08-I-03: DELETE destroys a session.
func TestIntegration_DestroySession(t *testing.T) {
	server, sm, _ := NewTestCLIServer(t)

	sess, err := sm.Create("to-delete")
	require.NoError(t, err)

	resp, err := httpDelete(formatURL(server, "/sessions/%s", sess.ID), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify it's gone.
	resp2, err := httpGet(formatURL(server, "/sessions/%s", sess.ID), "")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

// TC-08-I-04: WebSocket binary bidirectional transport.
func TestIntegration_WSBinaryIO(t *testing.T) {
	server, sm, writeEnds := NewTestCLIServer(t)

	sess, err := sm.Create("ws-test")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond) // Let readLoop start.

	ws := DialTestWS(t, server, sess.ID, "")

	// Inject data via pipe (simulates PTY output).
	writeEnds.Get(0).Write([]byte("hello from pty"))

	// Read binary message.
	data := WaitForBinaryContaining(t, ws, "hello from pty", 3*time.Second)
	assert.Contains(t, string(data), "hello from pty")

	// Send binary data (simulates terminal input).
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = ws.Write(ctx, websocket.MessageBinary, []byte("user input"))
	require.NoError(t, err)
}

// TC-08-I-05: WebSocket reconnection with replay.
func TestIntegration_WSReconnectReplay(t *testing.T) {
	server, sm, writeEnds := NewTestCLIServer(t)

	sess, err := sm.Create("replay-test")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Inject data before any WS connection.
	writeEnds.Get(0).Write([]byte("pre-connect data"))
	time.Sleep(100 * time.Millisecond) // Let readLoop process.

	// Connect WS — should receive replay buffer.
	ws := DialTestWS(t, server, sess.ID, "")
	data := WaitForBinaryContaining(t, ws, "pre-connect data", 3*time.Second)
	assert.Contains(t, string(data), "pre-connect data")
}

// TC-08-I-06: Shell exit sends shell_exit control message.
func TestIntegration_ShellExitMessage(t *testing.T) {
	server, sm, writeEnds := NewTestCLIServer(t)

	sess, err := sm.Create("exit-test")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	ws := DialTestWS(t, server, sess.ID, "")

	// Close the pipe write end to simulate shell exit.
	writeEnds.CloseAt(0)

	// Should receive shell_exit control message.
	msg := WaitForControlMessage(t, ws, MsgTypeShellExit, 5*time.Second)
	assert.Equal(t, MsgTypeShellExit, msg.Type)
}

// TC-08-I-07: resize control message changes PTY size.
func TestIntegration_ResizeControl(t *testing.T) {
	server, sm, _ := NewTestCLIServer(t)

	sess, err := sm.Create("resize-test")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	ws := DialTestWS(t, server, sess.ID, "")

	// Send resize control message.
	resizeMsg, _ := json.Marshal(WSControlMessage{
		Type:    MsgTypeResize,
		Payload: json.RawMessage(`{"cols":120,"rows":40}`),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = ws.Write(ctx, websocket.MessageText, resizeMsg)
	require.NoError(t, err)
	// No error means the message was processed.
}

// TC-08-I-08: heartbeat returns heartbeat_ack.
func TestIntegration_Heartbeat(t *testing.T) {
	server, sm, _ := NewTestCLIServer(t)

	sess, err := sm.Create("heartbeat-test")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	ws := DialTestWS(t, server, sess.ID, "")

	// Send heartbeat.
	hb, _ := json.Marshal(WSControlMessage{Type: MsgTypeHeartbeat})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = ws.Write(ctx, websocket.MessageText, hb)
	require.NoError(t, err)

	// Should receive heartbeat_ack.
	ack := WaitForControlMessage(t, ws, MsgTypeHeartbeatAck, 3*time.Second)
	assert.Equal(t, MsgTypeHeartbeatAck, ack.Type)
}

// TC-08-I-09: resize with extreme values is rejected.
func TestIntegration_ResizeExtremeValues(t *testing.T) {
	server, sm, _ := NewTestCLIServer(t)

	sess, err := sm.Create("resize-extreme")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	ws := DialTestWS(t, server, sess.ID, "")

	// Send invalid resize (cols=0).
	resizeMsg, _ := json.Marshal(WSControlMessage{
		Type:    MsgTypeResize,
		Payload: json.RawMessage(`{"cols":0,"rows":0}`),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = ws.Write(ctx, websocket.MessageText, resizeMsg)
	require.NoError(t, err)

	// Send valid resize to verify server is still responsive.
	hb, _ := json.Marshal(WSControlMessage{Type: MsgTypeHeartbeat})
	err = ws.Write(ctx, websocket.MessageText, hb)
	require.NoError(t, err)
	ack := WaitForControlMessage(t, ws, MsgTypeHeartbeatAck, 3*time.Second)
	assert.Equal(t, MsgTypeHeartbeatAck, ack.Type)
}

// TC-08-I-10: invalid session ID returns 404.
func TestIntegration_InvalidSessionID(t *testing.T) {
	server, _, _ := NewTestCLIServer(t)

	resp, err := httpGet(formatURL(server, "/sessions/nonexistent-id"), "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TC-08-I-11: authWrap requires auth for every request.
func TestIntegration_NoAuthToken(t *testing.T) {
	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     "secret123",
	}))
	require.NoError(t, err)

	// Simulate a non-localhost remote address.
	makeRemoteReq := func(path, token string) *http.Request {
		req, _ := http.NewRequest("GET", path, nil)
		req.RemoteAddr = "10.0.0.5:12345" // non-localhost
		if token != "" {
			req.Header.Set("X-CLI-Auth", token)
		}
		return req
	}

	handler := srv.authWrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No token → 401.
	w1 := httptest.NewRecorder()
	handler(w1, makeRemoteReq("/sessions", ""))
	assert.Equal(t, http.StatusUnauthorized, w1.Code)

	// Correct token → 200.
	w2 := httptest.NewRecorder()
	handler(w2, makeRemoteReq("/sessions", "secret123"))
	assert.Equal(t, http.StatusOK, w2.Code)

	// Localhost is not special-cased; the auth code remains the only gate.
	reqLocal, _ := http.NewRequest("GET", "/sessions", nil)
	reqLocal.RemoteAddr = "127.0.0.1:54321"
	w3 := httptest.NewRecorder()
	handler(w3, reqLocal)
	assert.Equal(t, http.StatusUnauthorized, w3.Code)
}

// Auth code matching is case- and separator-insensitive (normalizeAuthCode): the
// display form is XXXX-XXXX but a friend may type it lowercase or without the dash.
func TestIntegration_AuthCodeLenientInput(t *testing.T) {
	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     "AB2C-D3EF", // stored with the display dash
	}))
	require.NoError(t, err)

	handler := srv.authWrap(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := func(token string) int {
		r, _ := http.NewRequest("GET", "/sessions", nil)
		r.RemoteAddr = "10.0.0.5:12345"
		r.Header.Set("X-CLI-Auth", token)
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Code
	}

	for _, ok := range []string{"AB2C-D3EF", "ab2c-d3ef", "AB2CD3EF", "ab2cd3ef", "Ab2c-D3ef"} {
		assert.Equal(t, http.StatusOK, req(ok), "lenient form %q should authenticate", ok)
	}
	for _, bad := range []string{"AB2C-D3EE", "wrong", ""} {
		assert.Equal(t, http.StatusUnauthorized, req(bad), "wrong code %q must be rejected", bad)
	}
}

// Rotating the auth code invalidates the old one immediately (the recovery path
// when a shared ?auth= link leaks) and makes the new one authenticate.
func TestIntegration_RotateAuthCode(t *testing.T) {
	srv, err := NewServer(WithConfig(Config{
		Addr: ":0", DefaultShell: "/bin/sh", BufferSize: 4096, MaxSessions: 10,
		AuthCode: "OLD1-OLD2",
	}))
	require.NoError(t, err)

	handler := srv.authWrap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	auth := func(token string) int {
		r, _ := http.NewRequest("GET", "/sessions", nil)
		r.RemoteAddr = "10.0.0.5:12345"
		r.Header.Set("X-CLI-Auth", token)
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Code
	}
	assert.Equal(t, http.StatusOK, auth("OLD1-OLD2"), "old code works before rotate")

	// Rotate.
	rw := httptest.NewRecorder()
	rr, _ := http.NewRequest("POST", "/auth/rotate", nil)
	srv.handleRotateAuthCode(rw, rr)
	require.Equal(t, http.StatusOK, rw.Code)
	var body struct {
		AuthCode string `json:"authCode"`
	}
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &body))
	require.NotEmpty(t, body.AuthCode)
	assert.NotEqual(t, "OLD1-OLD2", body.AuthCode)

	assert.Equal(t, http.StatusUnauthorized, auth("OLD1-OLD2"), "old code rejected after rotate")
	assert.Equal(t, http.StatusOK, auth(body.AuthCode), "new code authenticates")
}

// Posting {"code":"..."} to /auth/rotate sets a user-supplied CUSTOM code (manual edit);
// an empty body rotates to random (covered by TestIntegration_RotateAuthCode).
func TestIntegration_SetCustomAuthCode(t *testing.T) {
	srv, err := NewServer(WithConfig(Config{
		Addr: ":0", DefaultShell: "/bin/sh", BufferSize: 4096, MaxSessions: 10,
		AuthCode: "OLD1-OLD2",
	}))
	require.NoError(t, err)
	handler := srv.authWrap(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	auth := func(token string) int {
		r, _ := http.NewRequest("GET", "/sessions", nil)
		r.RemoteAddr = "10.0.0.5:12345"
		r.Header.Set("X-CLI-Auth", token)
		w := httptest.NewRecorder()
		handler(w, r)
		return w.Code
	}

	// Set a custom code.
	rw := httptest.NewRecorder()
	rr, _ := http.NewRequest("POST", "/auth/rotate", strings.NewReader(`{"code":"MyTeam-2026"}`))
	srv.handleRotateAuthCode(rw, rr)
	require.Equal(t, http.StatusOK, rw.Code)
	var body struct {
		AuthCode string `json:"authCode"`
	}
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &body))
	assert.Equal(t, "MyTeam-2026", body.AuthCode) // stored verbatim (matching normalizes)

	assert.Equal(t, http.StatusUnauthorized, auth("OLD1-OLD2"), "old code rejected")
	assert.Equal(t, http.StatusOK, auth("MyTeam-2026"), "custom code works")
	assert.Equal(t, http.StatusOK, auth("myteam2026"), "custom code matches case/dash-insensitively")

	// A blank/whitespace-only custom code is rejected (400).
	bw := httptest.NewRecorder()
	br, _ := http.NewRequest("POST", "/auth/rotate", strings.NewReader(`{"code":"  -  "}`))
	srv.handleRotateAuthCode(bw, br)
	assert.Equal(t, http.StatusBadRequest, bw.Code)
}

func TestIntegration_CORSPreflightAllowsRemoteSessionAuth(t *testing.T) {
	req, _ := http.NewRequest(http.MethodOptions, "/api/sessions", nil)
	req.Header.Set("Origin", "http://stmac:8087")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "X-CLI-Auth, Content-Type")

	w := httptest.NewRecorder()
	corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("preflight should be answered before auth/session handlers")
	})).ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "http://stmac:8087", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "X-CLI-Auth")
	assert.Contains(t, w.Header().Get("Access-Control-Allow-Methods"), "POST")
}

// Chrome Private Network Access: a page fetching a LAN/tailscale (private/local) peer sends
// Access-Control-Request-Private-Network on the preflight. Without the ack the browser blocks the
// mesh fetch with an opaque "Failed to fetch" — the reported remote-terminal "地址不可达" even
// though the ordinary CORS headers are all present. The middleware must echo the ack, but only
// when asked (don't advertise PNA gratuitously).
func TestIntegration_CORSPreflightAcksPrivateNetwork(t *testing.T) {
	req, _ := http.NewRequest(http.MethodOptions, "/api/sessions", nil)
	req.Header.Set("Origin", "http://stmac:8087")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Private-Network"))

	req2, _ := http.NewRequest(http.MethodOptions, "/api/sessions", nil)
	req2.Header.Set("Origin", "http://stmac:8087")
	req2.Header.Set("Access-Control-Request-Method", "GET")
	w2 := httptest.NewRecorder()
	corsMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(w2, req2)
	assert.Empty(t, w2.Header().Get("Access-Control-Allow-Private-Network"))
}

// TC-08-I-12: HUD log upload.
func TestIntegration_HudLogUpload(t *testing.T) {
	server, _, _ := NewTestCLIServer(t)

	body := `{
		"sessionId": "test-session",
		"timestamp": "2026-03-21T12:00:00Z",
		"userAgent": "test-ua",
		"screen": {},
		"events": [],
		"snapshot": {}
	}`
	resp, err := httpPost(formatURL(server, "/debug/logs"), body, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

// TC-08-I-13: PTY process group independence — WS disconnect doesn't kill PTY.
func TestIntegration_PTYIndependence(t *testing.T) {
	server, sm, writeEnds := NewTestCLIServer(t)

	sess, err := sm.Create("pty-independence")
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Connect and then disconnect WS.
	ws := DialTestWS(t, server, sess.ID, "")
	ws.Close(websocket.StatusNormalClosure, "disconnect test")
	time.Sleep(100 * time.Millisecond)

	// Session should still be running.
	got, err := sm.Get(sess.ID)
	require.NoError(t, err)
	got.mu.Lock()
	status := got.Status
	got.mu.Unlock()
	assert.Equal(t, StatusRunning, status, "session should still be running after WS disconnect")

	// PTY should still accept data.
	_, err = writeEnds.Get(0).Write([]byte("still alive"))
	assert.NoError(t, err)

	// Can reconnect.
	ws2 := DialTestWS(t, server, sess.ID, "")
	data := WaitForBinaryContaining(t, ws2, "still alive", 3*time.Second)
	assert.Contains(t, string(data), "still alive")
}
