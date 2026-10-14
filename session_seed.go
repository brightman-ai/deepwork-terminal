package terminal

import (
	"time"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// session_seed.go — giving a freshly started keyframe what this server never saw.
//
// A server that has just (re)started rebuilds each session from the daemon's ring replay, which
// holds the last megabyte of output and nothing older. The daemon's own screen model holds the
// rest — it has been fed every byte since the session began — and the keyframe is seeded from it
// once the replay has been taken in. Only the newer daemon generation can be used:
//
//   - FeatureScreenSnap: the daemon hands over its complete terminal state together with the ring
//     offset it is true at. The seed is exact — the keyframe becomes that state at that offset,
//     and the ring after it is the window.
//
//   - A daemon that predates the snapshot (and therefore every daemon that was already running when
//     this was built — restarting one ends all its sessions) is NOT seeded. It can describe its
//     screen and scrollback but not where its cursor is, and a screen taken from it joined to a
//     cursor rebuilt from the replay is two different terminals stitched together: a program that
//     moves only relatively (an inline TUI redrawing "N lines up") then erases and redraws the
//     wrong rows, and the rows it meant to erase stay on screen — observed on 18074, 2026-10-14,
//     as Claude Code's old status lines left under its new output, surviving F5. The keyframe keeps
//     what the replay alone says: content and cursor from one stream, consistent with each other.
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
	default:
		err = seedError("the daemon cannot say where its cursor is (it predates ScreenSnap)")
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
