package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// SafeWriteEnds provides thread-safe access to pipe write ends.
type SafeWriteEnds struct {
	mu    sync.Mutex
	files []*os.File
}

func (s *SafeWriteEnds) append(f *os.File) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = append(s.files, f)
}

func (s *SafeWriteEnds) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.files {
		f.Close()
	}
}

// Get returns the write end at the given index (thread-safe).
func (s *SafeWriteEnds) Get(index int) *os.File {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index >= len(s.files) {
		return nil
	}
	return s.files[index]
}

// CloseAt closes the write end at the given index.
func (s *SafeWriteEnds) CloseAt(index int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < len(s.files) {
		s.files[index].Close()
	}
}

// pipePTYFactoryFunc returns a PTYFactory that uses pipes and stores the write end.
func pipePTYFactoryFunc() (PTYFactory, *SafeWriteEnds) {
	writeEnds := &SafeWriteEnds{}
	factory := func(_ PTYStartOptions) (*os.File, *exec.Cmd, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		writeEnds.append(w)
		return r, nil, nil
	}
	return factory, writeEnds
}

// NewTestCLIServer creates a test HTTP server with terminal routes (no auth).
// Returns the server, SessionManager, and write-end pipes for data injection.
func NewTestCLIServer(t *testing.T) (*httptest.Server, *SessionManager, *SafeWriteEnds) {
	t.Helper()

	factory, writeEnds := pipePTYFactoryFunc()
	sm := NewSessionManagerWithFactory(4096, "/bin/sh", factory)

	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     "test-no-auth-bypass",
	}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// Override the manager with the pipe-based one.
	srv.mgr = sm

	server := httptest.NewServer(srv.mux)
	t.Cleanup(func() {
		sm.DestroyAll()
		server.Close()
		writeEnds.closeAll()
	})

	return server, sm, writeEnds
}

// NewTestCLIServerWithAuth creates a test server with a specific auth code.
// Note: in tests, requests go through httptest which connects from 127.0.0.1,
// so auth is bypassed automatically for localhost connections.
func NewTestCLIServerWithAuth(t *testing.T, authCode string) (*httptest.Server, *SessionManager, *SafeWriteEnds) {
	t.Helper()

	factory, writeEnds := pipePTYFactoryFunc()
	sm := NewSessionManagerWithFactory(4096, "/bin/sh", factory)

	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     authCode,
	}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// Override the manager with the pipe-based one.
	srv.mgr = sm

	server := httptest.NewServer(srv.mux)
	t.Cleanup(func() {
		sm.DestroyAll()
		server.Close()
		writeEnds.closeAll()
	})

	return server, sm, writeEnds
}

// DialTestWS opens a WebSocket connection to the test server for the given session.
func DialTestWS(t *testing.T, server *httptest.Server, sessionID string, token string) *websocket.Conn {
	t.Helper()

	wsURL := strings.Replace(server.URL, "http://", "ws://", 1) +
		"/sessions/" + sessionID + "/ws"
	if token != "" {
		wsURL += "?auth=" + token
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial WS: %v", err)
	}
	t.Cleanup(func() {
		conn.CloseNow()
	})

	return conn
}

// WaitForBinaryContaining reads binary WS messages until one contains substr.
func WaitForBinaryContaining(t *testing.T, ws *websocket.Conn, substr string, timeout time.Duration) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		msgType, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("WS read error waiting for %q: %v", substr, err)
		}
		if msgType == websocket.MessageBinary && strings.Contains(string(data), substr) {
			return data
		}
	}
}

// WaitForControlMessage reads WS messages until a control message of the given type is found.
func WaitForControlMessage(t *testing.T, ws *websocket.Conn, msgType string, timeout time.Duration) WSControlMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for {
		wsMsgType, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("WS read error waiting for control message %q: %v", msgType, err)
		}
		if wsMsgType == websocket.MessageText {
			var ctrl WSControlMessage
			if err := json.Unmarshal(data, &ctrl); err == nil && ctrl.Type == msgType {
				return ctrl
			}
		}
	}
}

// WaitForPTYReady waits for the session to appear in the SessionManager.
func WaitForPTYReady(t *testing.T, sm *SessionManager, sessionID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := sm.Get(sessionID); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session %s not ready after %v", sessionID, timeout)
}

// httpGet performs a GET request with optional auth header.
func httpGet(url string, authToken string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if authToken != "" {
		req.Header.Set("X-CLI-Auth", authToken)
	}
	return http.DefaultClient.Do(req)
}

// httpPost performs a POST request with JSON body.
func httpPost(url string, body string, authToken string) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("X-CLI-Auth", authToken)
	}
	return http.DefaultClient.Do(req)
}

// httpDelete performs a DELETE request.
func httpDelete(url string, authToken string) (*http.Response, error) {
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return nil, err
	}
	if authToken != "" {
		req.Header.Set("X-CLI-Auth", authToken)
	}
	return http.DefaultClient.Do(req)
}

// formatURL constructs a full URL for the test server.
func formatURL(server *httptest.Server, path string, args ...any) string {
	return server.URL + fmt.Sprintf(path, args...)
}
