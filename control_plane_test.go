package terminal

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestControlHubPublishesBoundedFramesAndUnsubscribes(t *testing.T) {
	h := newControlHub()
	ch, unsubscribe := h.subscribe()
	h.publish([]byte(`one`))
	select {
	case got := <-ch:
		require.Equal(t, []byte(`one`), got)
	case <-time.After(time.Second):
		t.Fatal("control frame was not delivered")
	}
	unsubscribe()
	// A removed subscriber must not receive a later frame or panic the publisher.
	h.publish([]byte(`two`))
	require.Equal(t, uint64(0), h.droppedFrames())
}

func TestClipboardControlPublishCarriesEpochAndCursor(t *testing.T) {
	s := &Server{clipboard: newClipboardStore(), control: newControlHub()}
	ch, unsubscribe := s.control.subscribe()
	defer unsubscribe()
	s.addClipboardEntry(clipboardEntry{ID: "one", SessionID: "session", Text: "hello"})
	select {
	case frame := <-ch:
		var msg WSControlMessage
		require.NoError(t, json.Unmarshal(frame, &msg))
		require.Equal(t, MsgTypeClipboardDelta, msg.Type)
		var payload ClipboardDeltaPayload
		require.NoError(t, json.Unmarshal(msg.Payload, &payload))
		require.NotEmpty(t, payload.Epoch)
		require.Equal(t, uint64(1), payload.Cursor)
		require.Len(t, payload.Entries, 1)
		require.Equal(t, "one", payload.Entries[0].ID)
	case <-time.After(time.Second):
		t.Fatal("clipboard delta was not published")
	}
}

func TestClipboardControlCursorBelongsToPublishedEntry(t *testing.T) {
	s := &Server{clipboard: newClipboardStore(), control: newControlHub()}
	ch, unsubscribe := s.control.subscribe()
	defer unsubscribe()
	// Simulate two writers appending before the first writer gets to publish.
	first := s.clipboard.add(clipboardEntry{ID: "first", Text: "one"})
	second := s.clipboard.add(clipboardEntry{ID: "second", Text: "two"})
	for _, entry := range []clipboardEntry{first, second} {
		s.publishClipboardEntry(entry)
		var msg WSControlMessage
		require.NoError(t, json.Unmarshal(<-ch, &msg))
		var payload ClipboardDeltaPayload
		require.NoError(t, json.Unmarshal(msg.Payload, &payload))
		require.Equal(t, entry.Sequence, payload.Cursor, "must not acknowledge the other writer's undelivered entry")
	}
}

func TestClipboardTargetsSnapshotIsSharedAndRevisioned(t *testing.T) {
	s := &Server{clipboard: newClipboardStore()}
	first := s.clipboardTargetsSnapshot(context.Background())
	require.Equal(t, uint64(1), first.revision)
	require.NotEmpty(t, first.frame)
	second := s.clipboardTargetsSnapshot(context.Background())
	require.Equal(t, first.revision, second.revision)
	require.Equal(t, first.frame, second.frame)
	var msg WSControlMessage
	require.NoError(t, json.Unmarshal(first.frame, &msg))
	require.Equal(t, MsgTypeClipboardTargets, msg.Type)
	var payload ClipboardTargetsPayload
	require.NoError(t, json.Unmarshal(msg.Payload, &payload))
	require.Equal(t, first.revision, payload.Revision)
}
