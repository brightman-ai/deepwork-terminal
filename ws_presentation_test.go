package terminal

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// Hidden tabs keep their PTY attachment but must not multiply the global feed.
// Opening the overview restores tails on the same connection, without a replay.
func TestWSPresentationKeepsIOAndSubscribesToVisibleData(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	srv.tmuxProvider = nil
	sess, err := sm.Create("presentation")
	require.NoError(t, err)
	sess.Buffer.Write([]byte("visible fixture tail\r\n"))
	server := httptest.NewServer(srv.mux)
	defer server.Close()
	ws := DialTestWS(t, server, sess.ID, "")
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	messages := make(chan WSControlMessage, 32)
	go func() {
		defer close(messages)
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var msg WSControlMessage
			if json.Unmarshal(data, &msg) == nil {
				select {
				case messages <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	send := func(s string) { require.NoError(t, ws.Write(ctx, websocket.MessageText, []byte(s))) }
	await := func(kind string) WSControlMessage {
		t.Helper()
		for {
			select {
			case msg, ok := <-messages:
				require.True(t, ok, "connection should stay open")
				if msg.Type == kind {
					return msg
				}
			case <-ctx.Done():
				t.Fatal("timed out awaiting " + kind)
			}
		}
	}
	send(`{"type":"presentation","payload":{"active":false,"overview":false}}`)
	send(`{"type":"heartbeat","payload":{"sentAt":123}}`)
	require.JSONEq(t, `{"sentAt":123}`, string(await(MsgTypeHeartbeatAck).Payload))
	quiet := time.NewTimer(1200 * time.Millisecond)
	defer quiet.Stop()
quietLoop:
	for {
		select {
		case msg := <-messages:
			require.NotEqual(t, MsgTypeSessionsOverview, msg.Type, "hidden tab duplicates the host feed")
		case <-quiet.C:
			break quietLoop
		case <-ctx.Done():
			t.Fatal("hidden tab lost its connection")
		}
	}
	send(`{"type":"presentation","payload":{"active":true,"overview":false}}`)
	compact := await(MsgTypeSessionsOverview)
	require.NotContains(t, string(compact.Payload), `"tail"`)
	require.Contains(t, string(compact.Payload), sess.ID)
	send(`{"type":"presentation","payload":{"active":true,"overview":true}}`)
	full := await(MsgTypeSessionsOverview)
	require.Contains(t, string(full.Payload), "visible fixture tail")
	// Switching presentation must never clear/replay the terminal.
	for len(messages) > 0 {
		require.NotEqual(t, MsgTypeReplayReset, (<-messages).Type)
	}
}

func TestOverviewCompactFrameDoesNotChangeWithTerminalTail(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("compact")
	require.NoError(t, err)
	sess.Buffer.Write([]byte(strings.Repeat("first tail line\r\n", 40)))
	first := srv.overviewSnapshot(context.Background())
	require.NotEmpty(t, first.statusFrame)
	sess.Buffer.Write([]byte("different output, same agent facts\r\n"))
	srv.overviewCacheMu.Lock()
	srv.overviewCacheAt = time.Time{}
	srv.overviewAttemptAt = time.Time{}
	srv.overviewCacheMu.Unlock()
	second := srv.overviewSnapshot(context.Background())
	require.NotEqual(t, string(first.frame), string(second.frame))
	require.Equal(t, string(first.statusFrame), string(second.statusFrame))
	require.Less(t, len(second.statusFrame), len(second.frame))
}
