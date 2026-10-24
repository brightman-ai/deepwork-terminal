package terminal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

func TestReturningReaderRetainsScrollbackButBottomViewerKeepsFastRestore(t *testing.T) {
	for _, reading := range []bool{false, true} {
		t.Run(map[bool]string{false: "following-bottom", true: "reading-history"}[reading], func(t *testing.T) {
			grid := muxd.Grid{Cols: 100, Rows: 30}
			v := &viewer{ch: make(chan viewerFrame, 10), visibleOnce: true}
			sess := &Session{ID: "reading", pty: grid, viewers: map[string]*viewer{"tab": v}}
			payload, err := json.Marshal(ResizePayload{Cols: 100, Rows: 30, PreserveScrollback: reading})
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{}
			server.handleControlMessage(context.Background(), nil, sess, "tab", WSControlMessage{Type: MsgTypeResize, Payload: payload})
			select {
			case frame := <-v.ch:
				if !frame.Resync || frame.Fast == reading {
					t.Fatalf("reading=%v got %+v", reading, frame)
				}
			default:
				t.Fatal("returning viewer was not restored")
			}
			if v.size != grid {
				t.Fatalf("reading intent changed geometry: %+v", v.size)
			}
		})
	}
}
