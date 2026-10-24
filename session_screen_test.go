package terminal

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brightman-ai/deepwork-terminal/muxd"
)

// The restore point's promise (REQ-TS-12/13), as tests: a terminal brought to the present from a
// keyframe + window is indistinguishable from one that watched every byte — screen, scrollback,
// cursor, modes — however long ago the screen's content was drawn, wherever the keyframe's cut
// fell, and whatever the stream did while the browser was being synced.
//
// The oracle is muxd's own screen model fed the WHOLE stream. Comparing two runs of the same
// model tests the keyframe, the window and the serializer — exactly the code this change adds —
// independently of how faithfully that model imitates xterm (which the system check covers).

const replayReset = "\x1b[H\x1b[2J\x1b[3J" // what the frontend writes on replay_reset

type renderedState struct {
	screen, parked, scrollback []string
	cursor                     [2]int
	alt                        bool
	modes                      string
	pen                        muxd.Style
}

func renderLines(lines []muxd.Line, styles []muxd.Style) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		var b strings.Builder
		pos := 0
		for _, sp := range ln.Spans {
			b.WriteString(ln.Text[pos:sp.Start])
			st := muxd.DefaultStyle
			if int(sp.Style) < len(styles) {
				st = styles[sp.Style]
			}
			fmt.Fprintf(&b, "{%v}", st)
			pos = int(sp.Start)
		}
		b.WriteString(ln.Text[pos:])
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func observe(t *testing.T, h *muxd.History, scrollback int) renderedState {
	t.Helper()
	st, lines, ok := h.State(scrollback)
	require.True(t, ok, "model broke")
	rs := renderedState{
		screen:     renderLines(st.Lines, st.Styles),
		scrollback: renderLines(lines, st.Styles),
		cursor:     [2]int{st.CursorRow, st.CursorCol},
		alt:        st.Alt,
		modes:      fmt.Sprint(st.Modes),
		pen:        st.Pen,
	}
	if st.Parked != nil {
		rs.parked = renderLines(st.Parked.Lines, st.Styles)
	}
	return rs
}

func requireSameTerminal(t *testing.T, want, got renderedState, compareScrollback int) {
	t.Helper()
	require.Equal(t, strings.Join(want.screen, "\n"), strings.Join(got.screen, "\n"), "screen")
	require.Equal(t, strings.Join(want.parked, "\n"), strings.Join(got.parked, "\n"), "parked normal screen")
	require.Equal(t, want.cursor, got.cursor, "cursor")
	require.Equal(t, want.alt, got.alt, "alternate screen")
	require.Equal(t, want.modes, got.modes, "modes")
	require.Equal(t, want.pen, got.pen, "pen")
	tail := func(s []string) []string { return s[max(0, len(s)-compareScrollback):] }
	require.GreaterOrEqual(t, len(got.scrollback), min(len(want.scrollback), compareScrollback), "scrollback depth")
	require.Equal(t, strings.Join(tail(want.scrollback), "\n"), strings.Join(tail(got.scrollback), "\n"), "scrollback")
}

// keyframeSession is a session with a ring and a keyframe and no daemon: the pump's ingest path,
// driven directly.
func keyframeSession(g muxd.Grid, ring int) *Session {
	s := &Session{ID: "kf", Buffer: NewRingBuffer(ring), viewers: map[string]*viewer{}, pty: g}
	s.subMu.Lock()
	s.kfResetLocked(g)
	s.subMu.Unlock()
	return s
}

// ingest feeds a stream in randomly sized chunks, so keyframe cuts land mid-sequence and mid-rune.
func ingest(s *Session, rng *rand.Rand, stream string) {
	for len(stream) > 0 {
		n := min(len(stream), 1+rng.IntN(700))
		s.subMu.RLock()
		s.ingestLocked([]byte(stream[:n]))
		s.fanOutLocked([]byte(stream[:n]))
		s.subMu.RUnlock()
		stream = stream[n:]
	}
}

// restoreOnto plays a viewer's restore onto a fresh model the way a browser plays it onto xterm.
func restoreOnto(t *testing.T, s *Session, subID string, g muxd.Grid) *muxd.History {
	t.Helper()
	vs, ok := s.syncViewer(subID)
	require.True(t, ok)
	require.NotNil(t, vs.rp, "expected a keyframe restore, got the ring fallback")
	body, _ := restoreBytes(vs, false, false)
	out := muxd.NewHistory(g, keyframeLines)
	out.Write([]byte(replayReset))
	out.Write(withoutPrelude(t, body))
	return out
}

// withoutPrelude checks the restore opens with the prelude and drops it: the oracle model starts
// blank already, and would otherwise record the prelude's autowrap reset as an observed mode.
func withoutPrelude(t *testing.T, body []byte) []byte {
	t.Helper()
	require.True(t, strings.HasPrefix(string(body), restorePrelude), "a restore must open with the prelude")
	return body[len(restorePrelude):]
}

// inlineTUI is the case that started all this: a transcript printed once, then hours of status
// ticks at absolute positions — far more bytes than any window — with modes set only at startup.
func inlineTUI(rows, ticks int) string {
	var b strings.Builder
	b.WriteString("\x1b[?2004h\x1b[?1h\x1b=\x1b[?25l\x1b[5 q")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "• Ran step %d \x1b[38;2;104;104;104m(details 中文 %d)\x1b[0m\r\n", i, i)
	}
	for i := 0; i < ticks; i++ {
		fmt.Fprintf(&b, "\x1b]0;⠇ task %d\a\x1b[?2026h\x1b[%d;1H\x1b[1m•\x1b[0m Working (%ds • esc to interrupt)\x1b[K\x1b[%d;3H\x1b[?2026l", i, rows-3, i, rows-1)
	}
	return b.String()
}

func TestKeyframe_RestoreMatchesAModelThatSawEverything(t *testing.T) {
	g := muxd.Grid{Cols: 100, Rows: 30}
	for _, tc := range []struct {
		name   string
		stream string
	}{
		{"inline TUI after its transcript left the window", inlineTUI(g.Rows, 12000)},
		{"scrolling shell, far deeper than the window", func() string {
			var b strings.Builder
			for i := 0; i < 20000; i++ {
				fmt.Fprintf(&b, "\x1b[3%dmline %05d\x1b[0m plain tail 宽\r\n", i%8, i)
			}
			return b.String()
		}()},
		{"full-screen TUI up over a shell", func() string {
			var b strings.Builder
			for i := 0; i < 4000; i++ {
				fmt.Fprintf(&b, "$ build %d ok\r\n", i)
			}
			b.WriteString("$ vim\x1b[?1049h\x1b[?1000h\x1b[?1006h\x1b[2;29r")
			for i := 0; i < 6000; i++ {
				fmt.Fprintf(&b, "\x1b[%d;1H\x1b[7m~ %d\x1b[0m\x1b[K", 2+i%27, i)
			}
			return b.String()
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(1, uint64(len(tc.stream))))
			s := keyframeSession(g, 1<<20)
			s.viewers["browser"] = &viewer{ch: make(chan viewerFrame, 1<<16)}
			ingest(s, rng, tc.stream)
			require.Less(t, s.Buffer.Seq()-s.kf.at, uint64(keyframeWindowBytes)+1, "window outgrew its bound")

			ref := muxd.NewHistory(g, 50_000)
			ref.Write([]byte(tc.stream))

			got := restoreOnto(t, s, "browser", g)
			requireSameTerminal(t, observe(t, ref, keyframeLines), observe(t, got, keyframeLines), 4000)

			// And the NEXT bytes land the same way on both — the restore is a state, not a picture.
			next := "\x1b[?2004l continuing\r\nmore\x1b[31m red"
			ref.Write([]byte(next))
			got.Write([]byte(next))
			requireSameTerminal(t, observe(t, ref, keyframeLines), observe(t, got, keyframeLines), 4000)
		})
	}
}

func TestKeyframe_ResizeMovesTheKeyframeToTheBoundary(t *testing.T) {
	g := muxd.Grid{Cols: 80, Rows: 24}
	s := keyframeSession(g, 1<<20)
	s.viewers["browser"] = &viewer{ch: make(chan viewerFrame, 1<<16)}
	rng := rand.New(rand.NewPCG(7, 7))
	before := inlineTUI(g.Rows, 3000)
	ingest(s, rng, before)

	g2 := muxd.Grid{Cols: 120, Rows: 40}
	s.applyGrid(g2)
	require.Equal(t, s.Buffer.Seq(), s.kf.at, "a resize must leave no old-grid bytes in the window")
	after := "\x1b[2J\x1b[Hredrawn at 120x40\r\n"
	ingest(s, rng, after)

	ref := muxd.NewHistory(g, 50_000)
	ref.Write([]byte(before))
	ref.Resize(g2)
	ref.Write([]byte(after))
	got := restoreOnto(t, s, "browser", g2)
	requireSameTerminal(t, observe(t, ref, keyframeLines), observe(t, got, keyframeLines), 200)
}

// TestKeyframe_SyncSplitsALiveStreamExactly is the seam: a viewer synced while output is flowing
// sees each byte exactly once — in its restore point or in its queue.
func TestKeyframe_SyncSplitsALiveStreamExactly(t *testing.T) {
	g := muxd.Grid{Cols: 80, Rows: 24}
	for iter := 0; iter < 20; iter++ {
		s := keyframeSession(g, 1<<20)
		rng := rand.New(rand.NewPCG(uint64(iter), 99))
		var stream strings.Builder
		for i := 0; i < 3000; i++ {
			fmt.Fprintf(&stream, "\x1b[3%dmrow %d\x1b[0m\r\n", i%8, i)
		}
		full := stream.String()
		// A synchronized visible browser receives the post-snapshot stream.
		// A zero-sized observer intentionally drops it, making this depend on timing.
		s.viewers["browser"] = &viewer{ch: make(chan viewerFrame, 1<<16), size: g}

		pause := time.Duration(rng.IntN(3000)) * time.Microsecond
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			ingest(s, rng, full) // the only user of rng from here on
		}()
		time.Sleep(pause)
		vs, ok := s.syncViewer("browser")
		require.True(t, ok)
		wg.Wait()

		got := muxd.NewHistory(g, keyframeLines)
		got.Write([]byte(replayReset))
		body, _ := restoreBytes(vs, false, false)
		got.Write(withoutPrelude(t, body))
		for drained := false; !drained; {
			select {
			case f := <-s.viewers["browser"].ch:
				got.Write(f.Data)
			default:
				drained = true
			}
		}
		ref := muxd.NewHistory(g, 50_000)
		ref.Write([]byte(full))
		requireSameTerminal(t, observe(t, ref, keyframeLines), observe(t, got, keyframeLines), 2900)
	}
}

func (s *Session) streamBaseRing() uint64 {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	return s.streamBase.ring
}

func (s *Session) streamBaseDaemon() int64 {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	return s.streamBase.daemon
}

// newSeedTestManager is a manager on a pipe-backed in-process daemon with a SMALL ring, so a
// session's early output leaves both rings — the situation seeding exists for.
func newSeedTestManager(t *testing.T, ring int) (*SessionManager, func(string)) {
	t.Helper()
	var mu sync.Mutex
	var writeEnd *os.File
	factory := func(_ muxd.SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		mu.Lock()
		writeEnd = w
		mu.Unlock()
		return r, nil, nil
	}
	sm := NewSessionManagerWithFactory(ring, "/bin/sh", factory)
	sm.muxSocket = shortMuxdSocket(t)
	t.Cleanup(func() {
		sm.DestroyAll()
		mu.Lock()
		if writeEnd != nil {
			_ = writeEnd.Close()
		}
		mu.Unlock()
	})
	return sm, func(s string) {
		mu.Lock()
		w := writeEnd
		mu.Unlock()
		_, _ = w.Write([]byte(s))
	}
}

func TestKeyframe_RestartedServerIsSeededWithWhatItsReplayCannotReach(t *testing.T) {
	for _, tier := range []struct {
		name      string
		snapshots bool
	}{
		{"daemon with snapshots (exact)", true},
	} {
		t.Run(tier.name, func(t *testing.T) {
			old := daemonSnapshots
			daemonSnapshots = func(c *muxd.Client) bool { return tier.snapshots && old(c) }
			t.Cleanup(func() { daemonSnapshots = old })

			const ring = 16 << 10
			sm, write := newSeedTestManager(t, ring)
			sess, err := sm.Create("seeded")
			require.NoError(t, err)
			g := sess.PTYSize()

			// Everything the PTY ever printed, in order — the oracle is fed exactly this.
			var all strings.Builder
			emit := func(s string) { all.WriteString(s); write(s) }

			// Early content, then ticks until both rings have long forgotten it.
			var early strings.Builder
			for i := 0; i < 200; i++ {
				fmt.Fprintf(&early, "EARLY-%03d\r\n", i)
			}
			emit(early.String())
			emit("\x1b[?2004h\x1b[?1h") // modes, set once, long before the replay window
			var ticks strings.Builder
			for i := 0; i < 2000; i++ {
				fmt.Fprintf(&ticks, "\x1b[%d;1Htick %d\x1b[K", g.Rows, i)
			}
			emit(ticks.String())
			total := uint64(all.Len())
			require.Eventually(t, func() bool { return sess.Buffer.Seq() >= total }, 5*time.Second, 10*time.Millisecond)
			require.NotContains(t, string(sess.Buffer.Read()), "EARLY-199", "the test needs the early screen to have left the ring")

			// Restart the server: a new manager adopts the session from the same daemon — while
			// the program keeps SCROLLING. A seed applied at the wrong offset re-applies or skips
			// some of these lines, which is a duplicated or missing line the comparison below sees.
			require.NoError(t, sm.CloseAll())
			restored := NewSessionManager(ring, "/bin/sh")
			restored.muxSocket = sm.muxSocket
			t.Cleanup(func() { _ = restored.CloseAll() })
			done := make(chan struct{})
			go func() {
				defer close(done)
				for burst := 0; burst < 60; burst++ {
					var b strings.Builder
					for i := 0; i < 4; i++ {
						fmt.Fprintf(&b, "\r\nlive %02d.%d", burst, i)
					}
					emit(b.String())
					time.Sleep(time.Duration(5+burst%4*10) * time.Millisecond)
				}
			}()
			require.NoError(t, restored.Restore())
			again, err := restored.Get(sess.ID)
			require.NoError(t, err)
			<-done
			total = uint64(all.Len())

			// The seed lands once the replay is in and the daemon has held still between reads.
			require.Eventually(t, func() bool {
				again.subMu.Lock()
				defer again.subMu.Unlock()
				if again.kf.model == nil {
					return false
				}
				st, lines, _ := again.kf.model.State(keyframeLines)
				return len(lines) > 0 && strings.Contains(strings.Join(renderLines(lines, st.Styles), "\n"), "EARLY-000")
			}, 10*time.Second, 20*time.Millisecond, "the keyframe never learned the scrollback older than the replay")

			// Exactness: once every byte has arrived, a browser restored from this server shows what
			// a terminal that saw the whole stream shows — screen, cursor, and scrollback.
			require.Eventually(t, func() bool {
				return again.Buffer.Seq()-again.streamBaseRing() >= total-uint64(again.streamBaseDaemon())
			}, 5*time.Second, 10*time.Millisecond)
			again.subMu.Lock()
			again.viewers["browser"] = &viewer{ch: make(chan viewerFrame, 1<<10)}
			again.subMu.Unlock()
			got := restoreOnto(t, again, "browser", g)
			ref := muxd.NewHistory(g, 50_000)
			ref.Write([]byte(all.String()))
			want, have := observe(t, ref, keyframeLines), observe(t, got, keyframeLines)
			requireSameTerminal(t, want, have, 400)
		})
	}
}

// An older daemon (no ScreenSnap) is never grafted. It can hand over its screen but not its cursor,
// and its screen joined to a cursor rebuilt from the replay is two terminals stitched together: an
// inline TUI that redraws "N lines up" then erases the wrong rows and leaves its old status lines on
// screen, through every F5 (18074, 2026-10-14). The keyframe must be exactly what the bytes since
// the attach say — content and cursor from one stream — while the program keeps redrawing
// relatively, the way Claude Code's footer does.
func TestKeyframe_AnOlderDaemonIsNeverGrafted(t *testing.T) {
	old := daemonSnapshots
	daemonSnapshots = func(*muxd.Client) bool { return false }
	t.Cleanup(func() { daemonSnapshots = old })

	const ring = 16 << 10
	sm, write := newSeedTestManager(t, ring)
	sess, err := sm.Create("inline-tui")
	require.NoError(t, err)

	var all strings.Builder
	emit := func(s string) { all.WriteString(s); write(s) }
	// An inline UI: a transcript that scrolls, and a two-line footer redrawn in place by moving up
	// two lines and erasing them — no absolute addressing anywhere.
	footer := func(i int) string {
		return fmt.Sprintf("\x1b[2A\r\x1b[2Kstatus %d tokens\r\n\x1b[2Khint %d · ← for agents\r\n", i, i)
	}
	emit("\r\n\r\n")
	for i := 0; i < 400; i++ {
		if i%7 == 0 {
			emit(fmt.Sprintf("\x1b[2A\r\x1b[Jtranscript line %d\r\n\r\n\r\n", i))
		}
		emit(footer(i))
	}
	total := uint64(all.Len())
	require.Eventually(t, func() bool { return sess.Buffer.Seq() >= total }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, sm.CloseAll())
	restored := NewSessionManager(ring, "/bin/sh")
	restored.muxSocket = sm.muxSocket
	t.Cleanup(func() { _ = restored.CloseAll() })
	require.NoError(t, restored.Restore())
	again, err := restored.Get(sess.ID)
	require.NoError(t, err)
	for i := 400; i < 460; i++ { // the program keeps redrawing after the restart
		emit(footer(i))
	}
	total = uint64(all.Len())
	require.Eventually(t, func() bool {
		return again.Buffer.Seq()-again.streamBaseRing() >= total-uint64(again.streamBaseDaemon())
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond) // give a (wrong) seed every chance to land

	again.subMu.Lock()
	again.viewers["browser"] = &viewer{ch: make(chan viewerFrame, 1<<10)}
	again.subMu.Unlock()
	got := restoreOnto(t, again, "browser", sess.PTYSize())
	ref := muxd.NewTerminalModel(sess.PTYSize(), keyframeLines)
	ref.Write([]byte(all.String()[again.streamBaseDaemon():]))
	requireSameTerminal(t, observe(t, ref, keyframeLines), observe(t, got, keyframeLines), 400)
}

// TestKeyframe_RestoreDrawsTheSameIntoATerminalLeftInAnyState is the reviewer's first finding: a
// re-synced browser keeps its terminal, and that terminal can be inside a TUI's alternate screen,
// under its scrolling region. replay_reset only clears the active screen; the prelude has to put
// the terminal back on the normal screen with the whole screen scrolling, or the keyframe's
// scrollback is pushed through a three-row window into the wrong screen.
func TestKeyframe_RestoreDrawsTheSameIntoATerminalLeftInAnyState(t *testing.T) {
	g := muxd.Grid{Cols: 60, Rows: 20}
	s := keyframeSession(g, 1<<20)
	s.viewers["browser"] = &viewer{ch: make(chan viewerFrame, 1<<16)}
	stream := inlineTUI(g.Rows, 4000)
	ingest(s, rand.New(rand.NewPCG(3, 3)), stream)
	ref := muxd.NewHistory(g, 50_000)
	ref.Write([]byte(stream))

	vs, ok := s.syncViewer("browser")
	require.True(t, ok)
	body, _ := restoreBytes(vs, false, false)
	browser := muxd.NewHistory(g, keyframeLines)
	browser.Write([]byte("\x1b[?1049h\x1b[2;4rstale TUI frame\x1b[3")) // alt screen, region, mid-sequence
	browser.Write([]byte(replayReset))
	browser.Write(body)

	want, got := observe(t, ref, keyframeLines), observe(t, browser, keyframeLines)
	// The prelude's autowrap reset is the power-on value; a model records it as an observation.
	got.modes = strings.Replace(strings.Replace(got.modes, "{7 true} ", "", 1), " {7 true}", "", 1)
	requireSameTerminal(t, want, got, 4000)
}
