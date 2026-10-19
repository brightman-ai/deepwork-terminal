package terminal

// control_plane.go owns the server-side half of the browser control stream.  It is
// deliberately independent of muxd: muxd continues to carry terminal bytes and the
// application publishes the global clipboard/topology facts that belong to the HTTP
// service.  This keeps old daemons (and old browsers) wire compatible while giving new
// clients one bounded, diff-suppressed stream.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Targets are topology, not a keystroke feed.  One shared probe every two seconds is
	// enough for a pane/session selector and avoids making every WS writer shell out to
	// tmux independently.
	clipboardTargetsCacheTTL  = 1500 * time.Millisecond
	clipboardTargetsPollEvery = 2 * time.Second
	clipboardTargetBuildLimit = 5 * time.Second
)

// ClipboardDeltaPayload is full-state-safe even though Entries is normally one new item.
// A client may therefore drop a frame under back-pressure and recover with the epoch/cursor
// snapshot without replaying megabytes of history over the WS.
type ClipboardDeltaPayload struct {
	Epoch   string           `json:"epoch"`
	Cursor  uint64           `json:"cursor"`
	Entries []clipboardEntry `json:"entries,omitempty"`
}

// ClipboardTargetsPayload is a topology snapshot.  Revision is independent from clipboard
// cursor: a pane changing must never look like a new clipboard item, and vice versa.
type ClipboardTargetsPayload struct {
	Revision uint64            `json:"revision"`
	Host     string            `json:"host"`
	Targets  []clipboardTarget `json:"targets"`
}

// controlHub fans already-marshalled immutable frames to all attached terminal sockets.  Each
// subscriber gets a small bounded queue.  Full-state-safe frames may be dropped when a browser
// cannot keep up; the next frame carries a newer cursor/revision and the browser performs one
// HTTP snapshot recovery.  The hub never runs clipboardTargets or JSON encoding on a WS writer.
type controlHub struct {
	mu      sync.Mutex
	nextID  uint64
	subs    map[uint64]chan []byte
	dropped atomic.Uint64
}

func newControlHub() *controlHub { return &controlHub{subs: make(map[uint64]chan []byte)} }

func (h *controlHub) subscribe() (<-chan []byte, func()) {
	if h == nil {
		ch := make(chan []byte)
		return ch, func() {}
	}
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	ch := make(chan []byte, 8)
	h.subs[id] = ch
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if current, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(current)
			}
			h.mu.Unlock()
		})
	}
}

func (h *controlHub) publish(frame []byte) {
	if h == nil || len(frame) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- frame:
		default:
			h.dropped.Add(1)
		}
	}
}

func (h *controlHub) droppedFrames() uint64 {
	if h == nil {
		return 0
	}
	return h.dropped.Load()
}

func (h *controlHub) hasSubscribers() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

type clipboardTargetsSnapshot struct {
	revision uint64
	host     string
	targets  []clipboardTarget
	encoded  []byte // deterministic targets JSON used for change detection
	frame    []byte // complete WSControlMessage, shared by all writers
	at       time.Time
}

type clipboardTargetsBuild struct {
	done chan struct{}
	snap clipboardTargetsSnapshot
	ok   bool
}

func (s *Server) ensureControlHub() *controlHub {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.control == nil {
		s.control = newControlHub()
	}
	return s.control
}

// clipboardTargetsSnapshot returns one shared target answer.  The expensive tmux/provider
// inspection is performed at most once per TTL; concurrent callers wait on a promise and may
// abandon that wait with their own context.  A detached build keeps one short-lived request from
// cancelling the answer needed by every other connection.
func (s *Server) clipboardTargetsSnapshot(ctx context.Context) clipboardTargetsSnapshot {
	now := time.Now()
	s.controlMu.Lock()
	if s.targetsCache.at.Add(clipboardTargetsCacheTTL).After(now) && s.targetsCache.revision != 0 {
		snap := s.targetsCache
		s.controlMu.Unlock()
		return snap
	}
	lastGood := s.targetsCache
	if b := s.targetsBuild; b != nil {
		s.controlMu.Unlock()
		select {
		case <-b.done:
			if b.ok {
				return b.snap
			}
			return lastGood
		case <-ctx.Done():
			return lastGood
		}
	}
	b := &clipboardTargetsBuild{done: make(chan struct{})}
	s.targetsBuild = b
	s.controlMu.Unlock()
	go s.buildClipboardTargets(context.WithoutCancel(ctx), b)
	select {
	case <-b.done:
		if b.ok {
			return b.snap
		}
		return lastGood
	case <-ctx.Done():
		return lastGood
	}
}

func (s *Server) buildClipboardTargets(parent context.Context, b *clipboardTargetsBuild) {
	ctx, cancel := context.WithTimeout(parent, clipboardTargetBuildLimit)
	defer cancel()
	targets, err := s.clipboardTargets(ctx, "")
	if err != nil {
		s.controlMu.Lock()
		s.targetsBuild = nil
		s.controlMu.Unlock()
		b.ok = false
		close(b.done)
		return
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "远端"
	}
	// clipboardTargets is assembled in SessionManager creation order.  Copy the slice so no
	// future caller can mutate the immutable frame shared by all WS writers.
	copyTargets := append([]clipboardTarget(nil), targets...)
	encoded, _ := json.Marshal(struct {
		Host    string            `json:"host"`
		Targets []clipboardTarget `json:"targets"`
	}{Host: host, Targets: copyTargets})

	s.controlMu.Lock()
	prev := s.targetsCache
	snap := clipboardTargetsSnapshot{revision: prev.revision, host: host, targets: copyTargets, encoded: encoded}
	if prev.revision == 0 || !bytes.Equal(prev.encoded, encoded) {
		snap.revision++
	}
	payload, _ := json.Marshal(ClipboardTargetsPayload{Revision: snap.revision, Host: host, Targets: copyTargets})
	snap.frame, _ = json.Marshal(WSControlMessage{Type: MsgTypeClipboardTargets, Payload: payload})
	snap.at = time.Now()
	s.targetsCache = snap
	s.targetsBuild = nil
	b.snap, b.ok = snap, true
	s.controlMu.Unlock()
	close(b.done)
}

// cachedClipboardTargetsFrame is the non-blocking WS handshake path. A cold target probe may
// need tmux/process inspection and must never delay terminal replay or keystrokes; the server
// publisher will deliver the first completed snapshot to this socket. HTTP snapshot callers use
// clipboardTargetsSnapshot and may wait on its shared promise instead.
func (s *Server) cachedClipboardTargetsFrame() []byte {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.targetsCache.revision == 0 {
		return nil
	}
	return s.targetsCache.frame
}

// publishClipboardEntry emits one delta after a successful store mutation.  New connections
// receive metadata only (not the 20 MiB history); their broker uses epoch/cursor to decide whether
// one ordinary HTTP snapshot is needed.
func (s *Server) publishClipboardEntry(e clipboardEntry) {
	if s == nil || s.clipboard == nil {
		return
	}
	epoch, cursor := s.clipboard.metadata()
	payload, _ := json.Marshal(ClipboardDeltaPayload{Epoch: epoch, Cursor: cursor, Entries: []clipboardEntry{e}})
	frame, _ := json.Marshal(WSControlMessage{Type: MsgTypeClipboardDelta, Payload: payload})
	if h := s.ensureControlHub(); h != nil {
		h.publish(frame)
	}
}

func (s *Server) clipboardMetadataFrame() []byte {
	if s == nil || s.clipboard == nil {
		return nil
	}
	epoch, cursor := s.clipboard.metadata()
	payload, _ := json.Marshal(ClipboardDeltaPayload{Epoch: epoch, Cursor: cursor})
	frame, _ := json.Marshal(WSControlMessage{Type: MsgTypeClipboardDelta, Payload: payload})
	return frame
}

// startControlPublisher is one server-level topology watcher.  It is intentionally independent
// of muxd and survives an app-only restart: the next app instance rebuilds its own cache from the
// existing SessionManager view, while an old muxd keeps serving terminal bytes unchanged.
func (s *Server) startControlPublisher(ctx context.Context) {
	if s == nil {
		return
	}
	h := s.ensureControlHub()
	go func() {
		t := time.NewTicker(clipboardTargetsPollEvery)
		defer t.Stop()
		var lastRevision uint64
		for {
			select {
			case <-t.C:
				// No browser owns the live control stream. Avoid probing tmux/process topology
				// merely to keep an unused cache warm; the next subscriber gets a shared build.
				if !h.hasSubscribers() {
					continue
				}
				snap := s.clipboardTargetsSnapshot(ctx)
				if snap.revision != 0 && snap.revision != lastRevision {
					h.publish(snap.frame)
					lastRevision = snap.revision
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}
