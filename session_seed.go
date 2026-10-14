package terminal

import (
	"math/rand/v2"
	"time"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// session_seed.go — giving a freshly started keyframe what this server never saw.
//
// A server that has just (re)started rebuilds each session from the daemon's ring replay, which
// holds the last megabyte of output and nothing older. The daemon's own screen model holds the
// rest — it has been fed every byte since the session began — and the keyframe is seeded from it
// once the replay has been taken in. Two daemon generations, two ways:
//
//   - FeatureScreenSnap: the daemon hands over its complete terminal state together with the ring
//     offset it is true at. The seed is exact — the keyframe becomes that state at that offset,
//     and the ring after it is the window.
//
//   - FeatureScrollback only (a daemon that predates the snapshot, and therefore every daemon
//     that was already running when this was built — restarting one ends all its sessions): the
//     daemon can describe its normal screen and its scrollback, but not the cursor, the modes, or
//     the offset they are true at. The keyframe keeps everything it learned from the replay
//     (cursor, pen, modes, the alternate screen if one is up — all recent, all in the replay) and
//     takes the daemon's CONTENT for what the replay cannot reach. The offset is proven, not
//     assumed: the daemon's ring position is read before and after the content, and only an
//     unchanged position is accepted (see seedByGraft).
//
// Either way, every viewer is then re-synced, because the restore point it was drawn from has
// just gained the history it was missing.

// daemonSnapshots reports whether a daemon can answer MsgScreenSnap. A variable so tests can
// exercise the older daemon's path against an in-process daemon built from this source.
var daemonSnapshots = func(c *muxd.Client) bool { return c.Peer().Has(muxd.FeatureScreenSnap) }

const (
	// seedAttempts bounds the retries for a session that never holds still between two reads. Such
	// a session is redrawing continuously, so its replay already holds its screen; giving up costs
	// only scrollback depth.
	seedAttempts = 40
)

// streamBase maps the daemon's stream offsets to this server's ring positions for the current
// attachment: daemon offset `daemon` was written to the ring at position `ring`. Valid while the
// stream has no holes. Protected by subMu.
type streamBase struct {
	daemon int64
	ring   uint64
}

// restartLocalCopy throws this server's copy of the session away — ring and keyframe together —
// and tells every viewer that what it is showing no longer continues anything we hold.
func (s *Session) restartLocalCopy() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	s.Buffer.Reset()
	s.kf = keyframe{gen: s.kf.gen + 1}
	s.resyncAllViewersLocked()
}

// seedKeyframe runs once per fresh attach, after its replay has been taken in.
func (m *SessionManager) seedKeyframe(sess *Session, stream *muxd.Stream) {
	sess.mu.Lock()
	client := sess.mux
	sess.mu.Unlock()
	if client == nil {
		return
	}
	var err error
	how := "none"
	switch {
	case daemonSnapshots(client):
		how = "snapshot"
		err = seedFromSnapshot(sess, stream, client)
	case client.Peer().Has(muxd.FeatureScrollback):
		how = "graft"
		err = seedByGraft(sess, stream, client)
	default:
		return
	}
	if err != nil {
		logger.Info("keyframe not seeded from the daemon; restores reach back only as far as the replay",
			"id", sess.ID, "how", how, "reason", err)
		return
	}
	logger.Info("keyframe seeded from the daemon", "id", sess.ID, "how", how)
}

type seedError string

func (e seedError) Error() string { return string(e) }

// fetchScrollback pages the daemon's scrollback lines [from, to) — at most keyframeLines of the
// newest — and the style table they resolve against, starting from a table prefix `styles`
// already held. ok=false if the pages do not join up (history moved underneath, or was evicted).
func fetchScrollback(sess *Session, to int64, styles []muxd.Style) ([]muxd.Line, []muxd.Style, bool) {
	from := to - keyframeLines
	if from < 0 {
		from = 0
	}
	var lines []muxd.Line
	for next := from; next < to; {
		page, err := sess.History(muxd.HistoryReq{From: next, Count: int(to - next), StylesFrom: len(styles)})
		if err != nil || !page.Enabled {
			return nil, nil, false
		}
		styles = append(styles, page.Styles...)
		if len(page.Lines) == 0 {
			if len(lines) == 0 && page.Base >= to {
				return nil, styles, true // nothing older than the screen is held
			}
			return nil, nil, false
		}
		if len(lines) == 0 && page.Lines[0].N > next {
			next = page.Lines[0].N // the oldest wanted lines were evicted: start where history does
		}
		for _, ln := range page.Lines {
			if ln.N != next {
				return nil, nil, false
			}
			if ln.N >= to {
				break
			}
			lines = append(lines, ln)
			next++
		}
	}
	return lines, styles, true
}

// errSeedMoved means the answer in hand no longer describes a point this server can install at —
// the grid changed, or the keyframe moved past it — and must be asked for again.
var errSeedMoved = seedError("the session moved while the seed was being read")

// installAt applies a seed that is true at daemon offset `offset`, once this server's stream has
// taken in every byte up to it — the seed's bytes may still be in flight. gen is the keyframe
// generation the seed was read under: a resize since then (which moves no offset) voids it.
func installAt(sess *Session, stream *muxd.Stream, gen uint64, offset int64, apply func(pos uint64) error) error {
	for wait := 0; wait < 400; wait++ {
		sess.subMu.Lock()
		if stream.Holed() {
			sess.subMu.Unlock()
			return seedError("the stream lost bytes; its offsets no longer map onto the ring")
		}
		if sess.kf.gen != gen {
			sess.subMu.Unlock()
			return errSeedMoved
		}
		base := sess.streamBase
		if offset < base.daemon {
			sess.subMu.Unlock()
			return seedError("the seed predates this attachment's replay")
		}
		pos := base.ring + uint64(offset-base.daemon)
		if pos <= sess.Buffer.Seq() {
			if _, _, held := sess.Buffer.ReadFrom(pos); !held {
				sess.subMu.Unlock()
				return errSeedMoved
			}
			err := apply(pos)
			if err == nil {
				sess.resyncAllViewersLocked()
			}
			sess.subMu.Unlock()
			return err
		}
		sess.subMu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return errSeedMoved
}

func (s *Session) kfGen() uint64 {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	return s.kf.gen
}

// seedFromSnapshot: the exact seed. The daemon hands over its full terminal state and the offset
// it is true at; the keyframe becomes that state there, and the ring after it is the window.
func seedFromSnapshot(sess *Session, stream *muxd.Stream, client *muxd.Client) error {
	for attempt := 0; attempt < seedAttempts; attempt++ {
		gen := sess.kfGen()
		snap, err := client.ScreenSnap(sess.ID)
		if err != nil {
			return err
		}
		if !snap.Enabled || snap.Broken {
			return seedError("the daemon keeps no screen model for this session")
		}
		lines, styles, ok := fetchScrollback(sess, snap.HistoryTotal, nil)
		if !ok {
			continue // history moved underneath the paging; read it all again
		}
		// Scrollback spans resolve against the table the pages brought; the snapshot's own table
		// is a prefix of it (append-only), so one table serves both.
		if len(styles) < len(snap.Styles) {
			styles = snap.Styles
		}
		snap.Styles = styles
		err = installAt(sess, stream, gen, snap.Offset, func(pos uint64) error {
			model := muxd.NewTerminalModel(muxd.Grid{Cols: snap.Cols, Rows: snap.Rows}, keyframeLines)
			model.Restore(snap.TermState, lines)
			sess.kf.model, sess.kf.at = model, pos
			return nil
		})
		if err != errSeedMoved {
			return err
		}
	}
	return seedError("the session kept moving; no consistent snapshot")
}

// daemonOffset reads the daemon's ring position for a session: the offset an Attach acknowledges,
// which the daemon reads under the same lock its output loop holds while writing the ring AND its
// screen model. A throwaway observer attachment — it declares no size, and resumes from where our
// own stream already is, so the replay it carries is at most a few bytes.
func daemonOffset(client *muxd.Client, id string, since int64) (int64, error) {
	st, err := client.Attach(id, muxd.AttachOptions{Since: &since})
	if err != nil {
		return 0, err
	}
	off := st.Offset
	_ = st.Close()
	return off, nil
}

// seedByGraft: the older daemon's seed. It has no snapshot message, so the offset the content is
// true at has to be PROVEN rather than assumed: the daemon's ring position is read before and
// after reading its screen and scrollback, and only an unchanged position is accepted. The daemon
// writes its ring and its screen model under one lock, and the position is read under that lock;
// if no byte was written between the two reads, nothing was drawn either, so the content describes
// exactly that offset. (A quiet period on THIS side proves nothing: bytes the daemon already drew
// can still be in flight to us.)
func seedByGraft(sess *Session, stream *muxd.Stream, client *muxd.Client) error {
	for attempt := 0; attempt < seedAttempts; attempt++ {
		gen := sess.kfGen()
		before, err := daemonOffset(client, sess.ID, stream.Consumed())
		if err != nil {
			return err
		}
		screen, err := sess.History(muxd.HistoryReq{Screen: true})
		if err != nil || !screen.Enabled || screen.Broken {
			return seedError("the daemon keeps no usable history for this session")
		}
		lines, styles, ok := fetchScrollback(sess, screen.Total, screen.Styles)
		after, err := daemonOffset(client, sess.ID, stream.Consumed())
		if err != nil {
			return err
		}
		if ok && before == after {
			err := installAt(sess, stream, gen, before, func(pos uint64) error {
				return graftLocked(sess, pos, screen.Lines, lines, styles)
			})
			if err != errSeedMoved {
				return err
			}
		}
		// Output landed between the reads: the content may describe any point between them.
		// Ask again; a session that ticks a few times a second is idle for most of each second.
		time.Sleep(time.Duration(20+rand.IntN(120)) * time.Millisecond)
	}
	return seedError("the session never held still long enough to read a consistent screen")
}

// graftLocked puts the daemon's normal screen and scrollback into the keyframe at ring position
// pos, keeping everything else the keyframe learned from the replay (cursor, pen, modes, the
// alternate screen). Caller holds subMu (write). The model must be exactly at pos — it cannot be
// rewound — so it is advanced there first, and an already-later model voids the graft.
func graftLocked(sess *Session, pos uint64, screen, scrollback []muxd.Line, styles []muxd.Style) error {
	kf := &sess.kf
	if kf.model == nil {
		return seedError("no keyframe to graft onto")
	}
	if kf.at > pos {
		return errSeedMoved
	}
	sess.kfAdvanceLocked(pos, nil)
	if kf.model == nil || kf.at != pos {
		return errSeedMoved
	}
	st, _, ok := kf.model.State(0)
	if !ok {
		return seedError("the keyframe's model has broken")
	}
	if len(screen) > st.Rows {
		return errSeedMoved
	}
	// Move the daemon's lines into the keyframe's style space: its table is appended after ours
	// and every span id shifted by our table's length.
	shift := muxd.StyleID(len(st.Styles))
	rebase := func(in []muxd.Line) []muxd.Line {
		out := make([]muxd.Line, len(in))
		for i, ln := range in {
			out[i] = ln
			if len(ln.Spans) > 0 {
				out[i].Spans = make([]muxd.Span, len(ln.Spans))
				for j, sp := range ln.Spans {
					out[i].Spans[j] = muxd.Span{Start: sp.Start, Style: sp.Style + shift}
				}
			}
		}
		return out
	}
	st.Styles = append(st.Styles, styles...)
	// History(Screen) is the NORMAL screen — parked, if a full-screen program is up — which is
	// exactly the one the replay may have reached only partly.
	if st.Alt && st.Parked != nil {
		st.Parked.Lines = rebase(screen)
	} else {
		st.Lines = rebase(screen)
	}
	kf.model.Restore(st, rebase(scrollback))
	return nil
}
