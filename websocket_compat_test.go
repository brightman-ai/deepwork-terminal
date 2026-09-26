package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// Exercise the real upgrade and terminal replay, not just the UA classifier:
// Apple clients must not negotiate deflate even when they advertise it. Other
// browsers retain compression and both paths deliver a complete large replay.
func TestTerminalWSBrowserCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, ua   string
		compressed bool
	}{
		{"iphone_safari", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 Version/18.6 Mobile/15E148 Safari/604.1", false},
		{"ipad_desktop_safari", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) AppleWebKit/605.1.15 Version/18.6 Safari/605.1.15", false},
		{"iphone_chrome", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 CriOS/140.0 Mobile/15E148 Safari/604.1", false},
		{"ios_embedded", "Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148", false},
		{"desktop_chrome", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36", true},
		{"android_chrome", "Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 Chrome/140.0.0.0 Mobile Safari/537.36", true},
		{"firefox", "Mozilla/5.0 (X11; Linux x86_64; rv:142.0) Gecko/20100101 Firefox/142.0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, sm, _ := newDrawerTestServer(t)
			sm.bufferSize = 1 << 20
			sess, err := sm.Create("compat-replay")
			require.NoError(t, err)
			payload := bytes.Repeat([]byte("终端 replay 0123456789\r\n"), 12000)[:wsReplayMaxBytes]
			sess.Buffer.Write(payload)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, strings.Replace(server.URL, "http://", "ws://", 1)+"/sessions/"+sess.ID+"/ws?auth="+testAuthCode, &websocket.DialOptions{
				HTTPHeader:      http.Header{"User-Agent": []string{tc.ua}},
				CompressionMode: websocket.CompressionNoContextTakeover,
			})
			require.NoError(t, err)
			defer conn.CloseNow()
			conn.SetReadLimit(1 << 20)
			require.Equal(t, tc.compressed, strings.Contains(response.Header.Get("Sec-WebSocket-Extensions"), "permessage-deflate"))
			var replay []byte
			reset := false
			for len(replay) < len(payload) {
				kind, data, err := conn.Read(ctx)
				require.NoError(t, err)
				if kind == websocket.MessageBinary {
					require.True(t, reset, "replay reset must precede terminal bytes")
					replay = append(replay, data...)
				} else {
					var msg WSControlMessage
					require.NoError(t, json.Unmarshal(data, &msg))
					reset = reset || msg.Type == MsgTypeReplayReset
				}
			}
			require.Equal(t, payload, replay)
			// Bidirectional control still works after the full replay.
			require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat","payload":{"sentAt":12345}}`)))
			for {
				kind, data, err := conn.Read(ctx)
				require.NoError(t, err)
				if kind != websocket.MessageText {
					continue
				}
				var msg WSControlMessage
				require.NoError(t, json.Unmarshal(data, &msg))
				if msg.Type == MsgTypeHeartbeatAck {
					require.JSONEq(t, `{"sentAt":12345}`, string(msg.Payload))
					break
				}
			}
		})
	}
}
