package terminal

// Viewport aggregation — the server's half of per-attachment geometry.
//
// ## Two levels, two different rules, because the two levels mean different things
//
//	browsers ──owner wins──> this server's attachment ──min──> the PTY
//	          (here)                                   (muxd/session.go)
//
// The daemon mins across ITS clients — this server, another host's server, a
// `dw-terminal attach` CLI — because those are genuinely simultaneous windows onto one shell,
// and a terminal cannot display what does not fit. That is tmux's rule and it keeps tmux's
// reason.
//
// Browsers on THIS server are not simultaneous. Attaching preempts (SessionManager.
// SetActiveConn): the previous connection is told it was taken over and closed. Minimising
// across a set whose extra members are, by construction, no longer being drawn into gives the
// vote to windows nobody is looking at — and because the preemption is asynchronous, the
// losing viewer stays registered for the length of its own teardown, which was long enough
// for every tab switch to drag the session down to the smaller of the two and back. So this
// level is exclusive: the owner's window is the size, and the daemon still gets exactly one
// number from us. See Viewport.
//
// Before any of this the server kept a single shared size and the last browser to resize
// simply overwrote it — the frontend had grown a whole discipline (viewportDeclaration.ts) to
// decide who was allowed to be the last writer. Per-viewer state plus explicit ownership
// replaces that: every window tells the truth about itself, and exactly one of them counts.
//
// ## Withdrawal is half the design
//
// A size that can only ever be declared is a ratchet: the smallest window that ever looked at
// a session would constrain it forever, including after that window closed. So every viewer
// must be able to stop imposing a constraint — by closing its socket (the registry entry goes
// with it) or by explicitly declaring itself an observer (0×0), which is what a browser tab
// does when it goes to the background. An observer still receives output; it just no longer
// gets a vote on the size.

import (
	"fmt"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// viewer is one WebSocket connection watching this session: where its bytes go, and how big
// the window it is drawn in is.
//
// cols/rows are 0 until the browser tells us — and 0 means OBSERVER, not "unknown default".
// A viewer that has not declared a size imposes no constraint, which is the only safe
// reading: guessing a size for a window we have never measured would let a tab that is not
// even visible shrink the session for everyone who can see it.
type viewer struct {
	ch chan viewerFrame
	// size is the window this browser is displaying the session in; the zero Grid means
	// OBSERVER — still receiving output, no longer voting on the size.
	size muxd.Grid
}

// viewerFrame is one thing that happened on this session, carried to a viewer in the order
// it happened.
//
// Output and resizes share ONE channel for the same reason they do one layer down in the
// daemon (muxd.subFrame): a client cannot interpret a run of bytes without knowing which
// grid they were drawn for. Deliver the resize late and a full-screen repaint lands on the
// old geometry; deliver it early and the tail of the previous screen lands on the new one.
// Either way the user sees a corrupted picture and blames the program inside the terminal.
type viewerFrame struct {
	Data   []byte
	Resize *muxd.Grid // the session's grid is now this big, as of this point in the stream
}

// Viewport returns the size this server declares on its attachment: the window of the
// viewer that OWNS the session, or 0,0 when no browser owns it.
//
// ## Why ownership rather than the minimum
//
// This used to min over every viewer, on tmux's rule and tmux's reason. It does not any
// more, because this server is not tmux: a browser attaching here already PREEMPTS whoever
// held the session (SetActiveConn, and the "Session 已被其他设备接管" banner that goes with
// it). Only one browser can be receiving output at a time, so minimising across the others
// gave the vote to windows that were no longer being drawn into — and worse, the preemption
// is asynchronous: the losing connection unsubscribes from its own defer, so for the length
// of one handler teardown BOTH viewers were in the map and the session took the smaller.
//
// The result was a grid that oscillated on every tab switch. Each oscillation makes the
// program inside repaint at the new size, appending a screen's worth of bytes to the ring at
// a width the next replay will not be using — see gridSeq for what that does to the picture.
// A phone opening the same session reflowed the desktop watching it, then handed it back on
// disconnect, and neither window ever settled.
//
// So: exclusive. The owner's window is the session's size, and a viewer that has been
// preempted has no vote — it is not displaying the session any more, which is exactly the
// condition SetViewerSize's 0×0 already describes. The daemon still mins across its own
// clients (another host's server, a `dw-terminal attach` CLI): those genuinely ARE
// simultaneous, and smallest-wins remains right where windows really do coexist.
func (s *Session) Viewport() muxd.Grid {
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	return s.viewportLocked()
}

// viewportLocked requires subMu (read or write) to be held.
func (s *Session) viewportLocked() muxd.Grid {
	if s.activeViewer != "" {
		// The owner speaks alone — including when it has not spoken yet. Falling back to
		// another viewer's size here would reintroduce the oscillation through the back
		// door, in the exact window where it hurts: between the new browser subscribing and
		// its first resize, which is when the replay it is about to receive gets its grid.
		v, ok := s.viewers[s.activeViewer]
		if !ok || v.size.Zero() {
			return muxd.Grid{}
		}
		return v.size
	}
	// No browser owns this session. Something may still be watching it through the daemon,
	// and any viewer registered here without owning it (a preempted connection still tearing
	// down) is not displaying anything — so the old minimum survives only as the answer to
	// "what is the largest grid that fits everyone still registered", which with no owner is
	// the most honest thing we can say.
	var g muxd.Grid
	for _, v := range s.viewers {
		if v.size.Zero() {
			continue // an observer: watching, not constraining
		}
		if g.Cols == 0 || v.size.Cols < g.Cols {
			g.Cols = v.size.Cols
		}
		if g.Rows == 0 || v.size.Rows < g.Rows {
			g.Rows = v.size.Rows
		}
	}
	return g
}

// SetViewerOwner makes one viewer the session's sole owner of the grid, or clears ownership
// when subID is empty, and re-declares the resulting size.
//
// Called where preemption already happens, so that "who receives the output" and "whose
// window sizes the session" cannot answer differently — they are the same question asked
// twice, and the whole failure this replaces came from letting them drift.
func (s *Session) SetViewerOwner(subID string, epoch uint64) {
	s.subMu.Lock()
	if epoch < s.ownerEpoch {
		// A NEWER connection already took the grid. This one registered first, was preempted
		// while it was still subscribing, and is now claiming ownership it has already lost —
		// preemption, subscribing and this call are three steps, and two browsers arriving
		// together interleave across them. Without this the loser wins, and the session sizes
		// itself to a window whose socket is closing.
		s.subMu.Unlock()
		return
	}
	if subID != "" {
		if _, ok := s.viewers[subID]; !ok {
			// Ownership of a viewer that is not registered would freeze the session at 0×0
			// until the next subscribe.
			s.subMu.Unlock()
			return
		}
	}
	s.activeViewer, s.ownerEpoch = subID, epoch
	s.subMu.Unlock()

	if _, err := s.syncViewport(); err != nil {
		logger.Debug("could not re-declare the viewport after an ownership change",
			"id", s.ID, "sub_id", subID, "error", err)
	}
}

// ReleaseViewerOwner drops ownership if subID still holds it, and re-declares.
//
// Conditional because a departing viewer is routinely NOT the owner any more: preemption
// hands ownership to the new connection first, and the old one's teardown runs afterwards.
// An unconditional clear there would take the grid away from the browser that just won it.
func (s *Session) ReleaseViewerOwner(subID string) {
	s.subMu.Lock()
	if s.activeViewer != subID {
		s.subMu.Unlock()
		return
	}
	s.activeViewer = ""
	s.subMu.Unlock()

	if _, err := s.syncViewport(); err != nil {
		logger.Debug("could not re-declare the viewport after the owner left",
			"id", s.ID, "sub_id", subID, "error", err)
	}
}

// SetViewerSize records the size of ONE viewer's window and re-declares this server's
// aggregate to the daemon.
//
// 0×0 is a legitimate, meaningful argument: "I am still here, still receiving output, but I
// am not displaying this session any more — do not size the PTY for me." A backgrounded
// browser tab says exactly that. Rejecting it as out-of-bounds (the obvious reading of a
// zero size) would make withdrawal impossible and turn the minimum into a ratchet.
//
// A viewer that has already disconnected is not an error: the socket closing and the last
// resize crossing on the wire is an ordinary race, and the registry has already forgotten
// the constraint that resize was about to change.
func (s *Session) SetViewerSize(subID string, cols, rows int) error {
	if cols < 0 || rows < 0 {
		return fmt.Errorf("viewer size: cols/rows cannot be negative (%d×%d)", cols, rows)
	}
	if cols == 0 || rows == 0 {
		// Half a withdrawal is not a thing. One zero would otherwise be read as "constrain
		// the other axis only", which no window ever means.
		cols, rows = 0, 0
	}
	s.subMu.Lock()
	v, ok := s.viewers[subID]
	if ok {
		v.size = muxd.Grid{Cols: cols, Rows: rows}
	}
	s.subMu.Unlock()
	if !ok {
		return nil
	}

	pushed, err := s.syncViewport()
	if err != nil {
		return err
	}
	// ANSWER THE ASKER when nothing went to the daemon.
	//
	// A viewer that asks for a size it will not get — because a smaller window is also
	// watching — moves neither our aggregate nor the session, so no resize is broadcast and
	// nothing else will ever tell it. It has meanwhile fitted its own terminal to the size
	// it asked for, and from here on it paints every screen onto a grid the session never
	// entered. That is not a transient: nothing is scheduled to correct it.
	//
	// When we DID push, the daemon answers instead — including the case where it clamps us
	// against another host's smaller window. See the MsgResize handler in muxd/daemon.go.
	if !pushed && cols > 0 && rows > 0 {
		if got := s.PTYSize(); got != (muxd.Grid{Cols: cols, Rows: rows}) {
			s.tellViewer(subID, got)
		}
	}
	return nil
}

// tellViewer hands the current grid to ONE viewer, in order with its own output.
func (s *Session) tellViewer(subID string, g muxd.Grid) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	if v, ok := s.viewers[subID]; ok {
		pushViewerControl(v.ch, viewerFrame{Resize: &g})
	}
}

// syncViewport re-derives the smallest watching window and declares it on this server's
// attachment to the daemon. It reports whether anything was actually sent.
//
// Serialised by geomMu so that two racing resizes cannot be applied to the daemon in the
// wrong order — the last frame on the wire must carry the newest minimum, or the session
// settles at a size nobody is watching at.
func (s *Session) syncViewport() (pushed bool, err error) {
	s.geomMu.Lock()
	defer s.geomMu.Unlock()

	want := s.Viewport()
	if want == s.declared {
		// Nothing to say. The browser re-declares its size on every reconnect and on every
		// tab switch, and a resize the daemon already knows about is a frame that buys
		// nothing — while still costing a wakeup on every attached client, since the daemon
		// broadcasts size changes.
		return false, nil
	}

	s.mu.Lock()
	stream := s.stream
	s.mu.Unlock()
	if stream == nil {
		// Not attached: there is nothing to declare on. attach() declares the current
		// viewport as part of attaching, so the constraint is not lost — just deferred.
		return false, nil
	}

	// Declare it on THIS SERVER'S ATTACHMENT, not on the control connection.
	//
	// The distinction decides whether the browser's size counts at all. A session sizes
	// itself to fit everything attached to it, and a control-plane resize is only the
	// fallback for when nothing is — so sending a viewer's size there means it is silently
	// ignored the moment any client (including this server's own attachment) has declared
	// one. The server is a client, exactly as a tmux client is: this is its size.
	// Against a daemon OLDER than per-attachment geometry this is a one-way degradation, not
	// a corruption. Such a daemon routes any resize to its single shared size and rejects a
	// zero one outright, so a withdrawal is answered with an error frame the stream reader
	// ignores — the session simply keeps the size it had, which is exactly what that daemon
	// did before this feature existed. Recording the declaration anyway is right: the next
	// real size a viewer declares differs from it and is therefore sent, so a stale
	// constraint cannot outlive the next resize. Restarting the daemon ends the degradation.
	c, r := want.Wire()
	if err := stream.Resize(s.ID, c, r); err != nil {
		return false, err
	}
	s.declared = want
	return true, nil
}

// noteDeclared records a size that was declared out of band — by attach(), which passes the
// viewport in the attach request rather than paying a second round trip for it.
func (s *Session) noteDeclared(g muxd.Grid) {
	s.geomMu.Lock()
	s.declared = g
	s.geomMu.Unlock()
}

// fanOutData delivers output to every viewer.
//
// Output is expendable: a browser too slow to drain its channel misses live bytes and gets
// them back from the replay on its next attach. It is refused AT THE TAIL rather than
// evicted from the head — see viewerControlReserve for why the difference matters.
func (s *Session) fanOutData(data []byte) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	for _, v := range s.viewers {
		if !admitViewerOutput(v.ch) {
			continue
		}
		select {
		case v.ch <- viewerFrame{Data: data}:
		default:
		}
	}
}

// applyGrid records the session's new size and hands it to every viewer, as ONE step.
//
// The two halves must not be separable, because a browser subscribing in between would read
// the new size (and announce it to its terminal) while the frames still queued ahead of it
// were drawn at the old one — it would then paint old-grid bytes onto the new grid, which
// for a full-screen TUI is a scrambled screen, not a slightly-off one. Subscribe takes the
// same locks in the same order, so a viewer either joins before this and receives the
// resize frame, or joins after it and reads the new size. There is no third case.
func (s *Session) applyGrid(g muxd.Grid) {
	if g.Zero() {
		return
	}
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	// Through the one writer, so that the grid-epoch mark cannot be skipped by whichever
	// path happened to learn the new size first.
	s.setPTYSizeFromDaemon(g)
	// One pointer, shared by every viewer on purpose: a Grid is a value nobody mutates
	// after it is announced, and handing out N copies of the same two numbers only invites
	// someone to wonder whether they can differ.
	for _, v := range s.viewers {
		pushViewerControl(v.ch, viewerFrame{Resize: &g})
	}
}

// viewerControlReserve is how many slots at the top of a viewer's queue are held back for
// control frames. Same policy, same reason as controlReserve in the daemon: evicting the
// oldest frame to admit a newer control frame can discard an EARLIER control frame, which
// reorders the stream instead of merely thinning it — the bytes that were queued between
// the two then get painted onto a grid the viewer was never told about.
//
// Reserving instead of evicting means nothing already queued is ever removed.
const viewerControlReserve = 8

// admitViewerOutput reports whether there is room for one more expendable frame.
func admitViewerOutput(ch chan viewerFrame) bool {
	return muxd.AdmitExpendable(ch, viewerControlReserve)
}

// pushViewerControl delivers a frame that must not be lost. The reserve above means this
// normally just sends; the eviction path survives only for a consumer stalled long enough
// to fill even the reserve, where the newest grid is the one worth keeping. It never
// blocks: a viewer whose reader has stopped entirely must not stall the output pump for
// everyone else.
func pushViewerControl(ch chan viewerFrame, f viewerFrame) {
	muxd.PushNewestOnFull(ch, f)
}
