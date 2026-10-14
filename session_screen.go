package terminal

import (
	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// session_screen.go — how a browser terminal is brought to the present (REQ-TS-12/13).
//
// ── What was wrong with replaying bytes ─────────────────────────────────────────────────────────
// A byte stream is a process, not a state. Replaying the last N bytes of it rebuilds exactly what
// those N bytes drew and nothing older: an inline TUI (codex) whose transcript was printed hours ago
// and which has only been ticking its status line since comes back as a blank screen with a status
// line at the bottom — measured on a real session: 4 of 60 rows after a full 1 MiB replay, against
// 47 of 60 in a model that had seen everything. Scrollback is lost the same way. No replay length
// fixes this; a longer one only moves where it stops.
//
// ── The keyframe ────────────────────────────────────────────────────────────────────────────────
// So the server keeps, per session, a screen model that has consumed the output stream up to ring
// position `at` — a KEYFRAME, in the video sense — and the restore point it offers a browser is
//
//	keyframe state (scrollback + both screens + cursor, pen, modes)  ‖  ring[at:] verbatim
//
// The model only has to be right about what is OLDER than the window. Everything inside it reaches
// the browser as the original bytes, executed by xterm itself, so the fidelity of recent output is
// exactly what it was with a plain replay (hyperlinks, combining marks and all — the model keeps
// neither). The window is kept between keyframeWindowBytes/2 and keyframeWindowBytes by advancing the
// model in bulk, so the model costs one parse of the stream and a restore costs a bounded replay.
//
// `at` can fall inside an escape sequence. That is not a cut to be cleaned up: the model holds the
// first half as Pending, and the restore emits it immediately before the window, which supplies the
// second half. The browser parses one whole sequence, exactly as it would have the first time.
//
// ── Locking ─────────────────────────────────────────────────────────────────────────────────────
// The keyframe and the ring are written only by the stream pump, inside subMu's READ lock together
// with the fan-out of the same bytes; everything else that touches the keyframe takes subMu's WRITE
// lock. A restore point taken under the write lock therefore splits the stream cleanly: every byte
// is either in the restore point or queued to the viewer after it — never both, never neither.

const (
	// keyframeWindowBytes bounds the verbatim tail of a restore. It is the old replay cap: recent
	// output reaches the browser byte-for-byte exactly as it always has, and the model supplies
	// only what is older — so a reconnect costs what it used to, plus the keyframe.
	keyframeWindowBytes = 256 << 10

	// keyframeLines is the scrollback a restore carries: the frontend's xterm scrollback
	// (XtermTerminal.vue). More would be evicted on arrival; fewer would make a refreshed page
	// shallower than one that never refreshed (REQ-TS-13).
	keyframeLines = 5000
)

// keyframe is this server's restore point for one session. Protected as described above.
type keyframe struct {
	model *muxd.History // nil: no restore point; viewers get the ring replay instead
	at    uint64        // ring position the model has consumed through
	// gen counts the grid changes and restarts the keyframe has been through. A resize does not
	// move any byte offset, so an offset alone cannot tell a seed taken before one from a seed
	// taken after it; gen can.
	gen uint64
}

// kfResetLocked starts the keyframe over at the ring's present, on grid g. Called when the ring is
// started over (attach with a non-contiguous replay, a holed stream) — the old model described
// bytes this server no longer holds a continuation of.
func (s *Session) kfResetLocked(g muxd.Grid) {
	if g.Zero() {
		g = muxd.Grid{Cols: int(spawnCols), Rows: int(spawnRows)}
	}
	// The browser's terminal model, not the daemon's archive model: a restore must re-create what
	// xterm would hold, including what a resize does to the scrollback.
	s.kf = keyframe{model: muxd.NewTerminalModel(g, keyframeLines), at: s.Buffer.Seq(), gen: s.kf.gen + 1}
}

// kfAdvanceLocked feeds the model the stream up to `target`. Bytes before the ring's end come
// from the ring; bytes after it come from `incoming`, the chunk about to be written (which starts
// exactly at the ring's end).
func (s *Session) kfAdvanceLocked(target uint64, incoming []byte) {
	kf := &s.kf
	if kf.model == nil || target <= kf.at {
		return
	}
	end := s.Buffer.Seq()
	if kf.at < end {
		upTo := min(target, end)
		held, _, ok := s.Buffer.ReadFrom(kf.at)
		if !ok {
			// The window outran the ring, which only a single write larger than the ring can do.
			// The model cannot be fed bytes nobody kept; restart it rather than let it describe a
			// stream with a hole in it.
			s.kfResetLocked(s.ptyGrid())
			return
		}
		kf.model.Write(held[:upTo-kf.at])
		kf.at = upTo
	}
	// The rest comes from the chunk being admitted, which starts at the ring's end. kf.at may
	// already be inside it (an earlier advance of this same admission took part of it), so the
	// offset into it is kf.at-end, not zero.
	if target > kf.at && kf.at >= end {
		off := kf.at - end
		if off >= uint64(len(incoming)) {
			return
		}
		n := min(target-kf.at, uint64(len(incoming))-off)
		kf.model.Write(incoming[off : off+n])
		kf.at += n
	}
}

// kfAdmitLocked runs BEFORE `incoming` is written to the ring: if the window would outgrow its
// bound, the model takes the oldest half of it.
//
// If the model had to give up on an over-long sequence while advancing (an unterminated string
// longer than the model buffers), the point it stopped at is inside garbage and cannot be a
// keyframe; it is moved to the end of the chunk, where the browser will only ever see the model's
// rendering of it. Rare by construction, and bounded to one restore's worth of fidelity.
func (s *Session) kfAdmitLocked(incoming []byte) {
	kf := &s.kf
	if kf.model == nil {
		return
	}
	end := s.Buffer.Seq() + uint64(len(incoming))
	window := s.kfWindowLimit()
	if end-kf.at <= window {
		return
	}
	dropped := kf.model.Stats().DroppedBytes
	s.kfAdvanceLocked(end-window/2, incoming)
	if s.kf.model != nil && s.kf.model.Stats().DroppedBytes != dropped {
		s.kfAdvanceLocked(end, incoming)
	}
}

// ingestLocked takes one chunk of the session's output into this server's copy: the keyframe
// first (it may need the chunk to advance), then the ring. Caller holds subMu — the pump, the read
// lock, with the fan-out of the same chunk.
func (s *Session) ingestLocked(data []byte) {
	s.kfAdmitLocked(data)
	_, _ = s.Buffer.Write(data)
}

// kfResizeLocked moves the keyframe to the present and resizes the model. Bytes before a resize
// were drawn for the old grid; re-executing them on the new one is the stacked-copy corruption the
// grid epoch (Session.gridSeq) exists to prevent — the model takes them at the old size instead,
// and the window restarts at the boundary.
func (s *Session) kfResizeLocked(g muxd.Grid) {
	if s.kf.model == nil || g.Zero() {
		return
	}
	s.kfAdvanceLocked(s.Buffer.Seq(), nil)
	if s.kf.model != nil {
		s.kf.model.Resize(g)
	}
	s.kf.gen++
}

// kfWindowLimit is the largest window the keyframe lets accumulate: keyframeWindowBytes, or half
// the ring if the ring is smaller than twice that — the window must still be IN the ring when the
// model comes to take it, and the second half is the margin a single large write needs.
func (s *Session) kfWindowLimit() uint64 {
	return min(uint64(keyframeWindowBytes), uint64(s.Buffer.Cap()/2))
}

// restorePoint is everything a viewer needs to be brought to the present, taken in one instant.
type restorePoint struct {
	state      muxd.TermState
	scrollback []muxd.Line
	window     []byte // ring[at:], verbatim
}

// restorePointLocked reads the keyframe and the window. ok=false means there is no usable model
// (never created, or broken — see muxd.History.guard) and the caller must fall back to a replay.
func (s *Session) restorePointLocked() (rp restorePoint, ok bool) {
	kf := &s.kf
	if kf.model == nil {
		return rp, false
	}
	st, lines, ok := kf.model.State(keyframeLines)
	if !ok {
		return rp, false
	}
	window, _, ok := s.Buffer.ReadFrom(kf.at)
	if !ok {
		return rp, false
	}
	return restorePoint{state: st, scrollback: lines, window: window}, true
}

// ptyGrid is the session's current grid, or the spawn size before one is known.
func (s *Session) ptyGrid() muxd.Grid {
	s.mu.Lock()
	g := s.pty
	s.mu.Unlock()
	if g.Zero() {
		g = muxd.Grid{Cols: int(spawnCols), Rows: int(spawnRows)}
	}
	return g
}

// viewerSync is what syncViewer hands the connection: the grid to announce and the bytes that
// draw the present on a cleared terminal. Exactly one of rp / ring is meaningful.
type viewerSync struct {
	grid muxd.Grid
	rp   *restorePoint
	ring []byte // fallback replay, already cut at the grid epoch
	// truncated reports, for the fallback, that the ring held more than the replay sends.
	truncated bool
}

// syncViewer brings one viewer to the present. Under the write lock — so the stream is paused at
// a byte boundary — it discards every frame still queued for the viewer (all of them describe
// moments the restore point already contains), clears its stale mark, and reads the restore point.
// Everything the pump fans out after the lock is released is, by construction, the continuation.
//
// This is the ONLY way a viewer is brought up to date: on connect, and again whenever it has fallen
// behind far enough to lose output (see markStaleLocked). One mechanism, so the "joined late" and
// "fell behind" cases cannot drift apart.
func (s *Session) syncViewer(subID string) (vs viewerSync, ok bool) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	v, ok := s.viewers[subID]
	if !ok {
		return vs, false
	}
	for drained := false; !drained; {
		select {
		case <-v.ch:
		default:
			drained = true
		}
	}
	v.stale.Store(false)
	vs.grid = s.ptyGrid()
	if rp, ok := s.restorePointLocked(); ok {
		vs.rp = &rp
		vs.grid = muxd.Grid{Cols: rp.state.Cols, Rows: rp.state.Rows}
		return vs, true
	}
	full, keep := s.ReplayTail(wsReplayMaxBytes)
	vs.ring = full[len(full)-keep:]
	vs.truncated = s.Buffer.Len() > len(vs.ring)
	return vs, true
}

// markStaleLocked records that a viewer lost output and must be re-synced, and wakes its writer.
// Caller holds subMu (read or write). From here until the sync, the viewer is sent no output:
// anything sent would continue a stream the viewer no longer has an unbroken copy of.
func markStaleLocked(v *viewer) {
	if v.stale.Swap(true) {
		return
	}
	pushViewerControl(v.ch, viewerFrame{Resync: true})
}

// resyncAllViewersLocked marks every viewer stale — for an event that changed the restore point
// out from under all of them (a seeded keyframe, a ring started over).
func (s *Session) resyncAllViewersLocked() {
	for _, v := range s.viewers {
		markStaleLocked(v)
	}
}
