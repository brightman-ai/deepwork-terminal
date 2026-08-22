package terminal

// Viewport aggregation — the server's half of per-attachment geometry.
//
// ## The rule, and why it is this one
//
// A session's size is the component-wise MINIMUM over every window displaying it. That is
// tmux's rule and it has tmux's reason: a terminal cannot display what does not fit. If two
// people watch one shell and the grid is sized to the larger window, the smaller one shows a
// wrapped, truncated lie of a screen — and a TUI, which paints by absolute cursor address,
// does not degrade gracefully into "slightly wrong". It degrades into garbage that looks like
// a bug in the program the user is running.
//
// ## Why the minimum is taken TWICE
//
// The daemon owns the PTY and mins across its clients (this server, another host's server,
// a `dw-terminal attach` CLI). But ONE server can have many browsers open on the same
// session, and to the daemon they are a single attachment — the daemon cannot see them and
// has no business knowing they exist. So the server mins over its own browsers first and
// declares that one number as its attachment's size.
//
//	browsers ──min──> this server's attachment ──min──> the PTY
//	                  (here)                            (muxd/session.go)
//
// Two levels, one rule, applied where the knowledge is. Before this existed the server kept a
// single shared size and the last browser to resize simply overwrote it, so opening a phone
// on a session reflowed the desktop that was already watching it — and the frontend had grown
// a whole discipline (viewportDeclaration.ts) to decide who was allowed to be the last writer.
// That discipline was a workaround for shared mutable state; with per-owner state the rule
// collapses back into "every window tells the truth about itself".
//
// ## Withdrawal is half the design
//
// A size that can only ever be declared is a ratchet: the smallest window that ever looked at
// a session would constrain it forever, including after that window closed. So every viewer
// must be able to stop imposing a constraint — by closing its socket (the registry entry goes
// with it) or by explicitly declaring itself an observer (0×0), which is what a browser tab
// does when it goes to the background. An observer still receives output; it just no longer
// gets a vote on the size.

import "fmt"

// viewer is one WebSocket connection watching this session: where its bytes go, and how big
// the window it is drawn in is.
//
// cols/rows are 0 until the browser tells us — and 0 means OBSERVER, not "unknown default".
// A viewer that has not declared a size imposes no constraint, which is the only safe
// reading: guessing a size for a window we have never measured would let a tab that is not
// even visible shrink the session for everyone who can see it.
type viewer struct {
	ch         chan viewerFrame
	cols, rows int
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
	Resize *[2]int // the session's grid is now this big, as of this point in the stream
}

// Viewport returns the smallest window currently watching this session — the size this
// server declares on its attachment — or 0,0 when no viewer has declared one.
//
// Columns and rows are minimised independently, matching the daemon (effectiveSizeLocked).
// A 100×24 window and an 80×50 one therefore yield 80×24: the largest grid that fits in
// both. Picking one viewer's box wholesale would leave the other one overflowing on the
// axis it was smaller on, which is the case this whole mechanism exists to prevent.
func (s *Session) Viewport() (cols, rows int) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	return s.viewportLocked()
}

// viewportLocked requires subMu (read or write) to be held.
func (s *Session) viewportLocked() (cols, rows int) {
	for _, v := range s.viewers {
		if v.cols <= 0 || v.rows <= 0 {
			continue // an observer: watching, not constraining
		}
		if cols == 0 || v.cols < cols {
			cols = v.cols
		}
		if rows == 0 || v.rows < rows {
			rows = v.rows
		}
	}
	return cols, rows
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
		v.cols, v.rows = cols, rows
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
		if gc, gr := s.PTYSize(); gc != cols || gr != rows {
			s.tellViewer(subID, gc, gr)
		}
	}
	return nil
}

// tellViewer hands the current grid to ONE viewer, in order with its own output.
func (s *Session) tellViewer(subID string, cols, rows int) {
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	if v, ok := s.viewers[subID]; ok {
		pushViewerControl(v.ch, viewerFrame{Resize: &[2]int{cols, rows}})
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

	cols, rows := s.Viewport()
	if cols == s.declCols && rows == s.declRows {
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
	if err := stream.Resize(s.ID, uint16(cols), uint16(rows)); err != nil {
		return false, err
	}
	s.declCols, s.declRows = cols, rows
	return true, nil
}

// noteDeclared records a size that was declared out of band — by attach(), which passes the
// viewport in the attach request rather than paying a second round trip for it.
func (s *Session) noteDeclared(cols, rows int) {
	s.geomMu.Lock()
	s.declCols, s.declRows = cols, rows
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
func (s *Session) applyGrid(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.subMu.RLock()
	defer s.subMu.RUnlock()
	s.mu.Lock()
	s.ptyCols, s.ptyRows = cols, rows
	s.mu.Unlock()
	for _, v := range s.viewers {
		pushViewerControl(v.ch, viewerFrame{Resize: &[2]int{cols, rows}})
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
	room := cap(ch) - viewerControlReserve
	if room < 1 {
		// See admitOutput in the daemon: a queue smaller than the reserve must still carry
		// output, or the reserve turns into a silent mute.
		room = 1
	}
	return len(ch) < room
}

// pushViewerControl delivers a frame that must not be lost. The reserve above means this
// normally just sends; the eviction path survives only for a consumer stalled long enough
// to fill even the reserve, where the newest grid is the one worth keeping. It never
// blocks: a viewer whose reader has stopped entirely must not stall the output pump for
// everyone else.
func pushViewerControl(ch chan viewerFrame, f viewerFrame) {
	select {
	case ch <- f:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- f:
	default:
	}
}
