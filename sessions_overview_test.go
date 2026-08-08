package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/kit/obs"
)

// The non-tmux overview's whole value is that a card shows what the terminal is ACTUALLY doing.
// These lock in that the entry builder reads real ring content, strips the agent frame, and stays
// honest about sessions that have produced nothing.

func newOverviewTestServer(t *testing.T) (*Server, *SessionManager) {
	t.Helper()
	factory, writeEnds := pipePTYFactoryFunc()
	sm := NewSessionManagerWithFactory(4096, "/bin/sh", factory)
	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   4096,
		MaxSessions:  10,
		AuthCode:     testAuthCode,
	}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.mgr = sm
	t.Cleanup(func() {
		sm.DestroyAll()
		writeEnds.closeAll()
	})
	return srv, sm
}

func TestSessionsOverview_CardCarriesLiveTail(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate PTY output landing in the ring — this is the exact path the writer uses.
	sess.Buffer.Write([]byte("compiling module\nall checks passed\n"))

	entries := srv.sessionsOverview(context.Background())
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.ID != sess.ID {
		t.Fatalf("entry id = %q, want %q", e.ID, sess.ID)
	}
	if len(e.Tail) == 0 {
		t.Fatal("card has no tail — the overview would again be strictly worse than the tab strip")
	}
	joined := strings.Join(e.Tail, "\n")
	if !strings.Contains(joined, "all checks passed") {
		t.Fatalf("tail lost the most recent output: %q", e.Tail)
	}
}

// REGRESSION: the first cut read the ring buffer directly and handed raw PTY bytes to a line
// splitter. The card then rendered cursor-positioning soup ("\x1b[20;2H\x1b[0m\x1b[K…") instead of
// text — caught only on a real 8087 session, never by the pure chrome-stripping tests. The fix
// composes Session.TailOutput (which owns CSI/OSC decoding) before chrome stripping.
func TestSessionsOverview_TailIsDecodedNotRawEscapes(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("ansi")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Colored + cursor-positioned output, exactly what a real PTY writes.
	// CSI colour, an OSC title, and two-char escapes (ESC = keypad, ESC ( B charset) — a real zsh
	// prompt emits all three families. Both texts are written on ADJACENT rows: with a real screen
	// model, text positioned 20 rows apart genuinely would not share the card's last-N-line window
	// (that is correct terminal behavior, verified in screen_test.go, not something to assert here).
	sess.Buffer.Write([]byte("\x1b[32mbuild ok\x1b[0m\r\n\x1b]0;title\x07\x1b[Kdone\x1b=\x1b(B\r\n"))

	entries := srv.sessionsOverview(context.Background())
	joined := strings.Join(entries[0].Tail, "\n")
	if strings.Contains(joined, "\x1b") {
		t.Fatalf("escape sequences leaked into the card: %q", entries[0].Tail)
	}
	if !strings.Contains(joined, "build ok") || !strings.Contains(joined, "done") {
		t.Fatalf("decoding lost the actual text: %q", entries[0].Tail)
	}
}

func TestSessionsOverview_SilentSessionHasEmptyTail(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("quiet"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	entries := srv.sessionsOverview(context.Background())
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	// Empty, not fabricated padding — the card renders "no recent output" from this.
	if len(entries[0].Tail) != 0 {
		t.Fatalf("a session that printed nothing must have an empty tail, got %q", entries[0].Tail)
	}
}

func TestSessionsOverview_CoversEverySessionNotJustActive(t *testing.T) {
	// The entire point: a non-tmux user only has a WS on the ACTIVE tab, so the frame must
	// describe the others too or the overview can never show them.
	srv, sm := newOverviewTestServer(t)
	for _, name := range []string{"a", "b", "c"} {
		if _, err := sm.Create(name); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	if got := len(srv.sessionsOverview(context.Background())); got != 3 {
		t.Fatalf("want all 3 sessions in the frame, got %d", got)
	}
}

func TestSessionsOverviewJSON_IsStableForDiffSuppression(t *testing.T) {
	// The 1s ticker only pushes when the bytes change; unstable marshalling would push every
	// second forever and defeat the whole "quiet machine pushes nothing" design.
	//
	// Marshals the builder directly rather than going through sessionsOverviewJSON, whose per-tick
	// cache would mask a genuine content change inside the TTL (that cache has its own test below).
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("steady state\r\n"))

	marshal := func() string {
		b, mErr := json.Marshal(srv.sessionsOverview(context.Background()))
		if mErr != nil {
			t.Fatalf("marshal: %v", mErr)
		}
		return string(b)
	}

	if first, second := marshal(), marshal(); first != second {
		t.Fatalf("unchanged state produced differing JSON:\n%s\n%s", first, second)
	}
	before := marshal()
	sess.Buffer.Write([]byte("new line appeared\r\n"))
	if after := marshal(); after == before {
		t.Fatal("new output did NOT change the payload — the card would never update")
	}
}

func TestSessionsOverviewJSON_SharesOneSnapshotPerTick(t *testing.T) {
	// The payload is global but every connected WS writer asks for it, so with one surface mounted
	// per terminal the same snapshot would be rebuilt N times a second. Within a tick, all callers
	// must get the SAME bytes off one computation.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("first\r\n"))

	first := string(srv.overviewSnapshot(context.Background()).json)

	// Content changes, but we are still inside the same tick → callers keep sharing the snapshot.
	sess.Buffer.Write([]byte("second\r\n"))
	if cached := string(srv.overviewSnapshot(context.Background()).json); cached != first {
		t.Fatal("a second caller within the same tick recomputed instead of sharing the snapshot")
	}

	// Past the TTL the next tick recomputes and the new output shows up.
	time.Sleep(sessionsOverviewCacheTTL + 150*time.Millisecond)
	fresh := string(srv.overviewSnapshot(context.Background()).json)
	if fresh == first {
		t.Fatal("cache never expired — the overview would freeze")
	}
	if !strings.Contains(fresh, "second") {
		t.Fatalf("refreshed snapshot lost the new output: %s", fresh)
	}
}

// ── I2「变化才有代价」 ────────────────────────────────────────────────────────────────────────
//
// The two halves of the invariant, one test each, because they fail independently: a revision that
// moves without a change makes every connection push an identical frame forever; a revision that
// does NOT move on a real change freezes the UI, which is far worse and completely silent.

func TestOverviewRevision_MovesOnlyWhenTheAnswerMoves(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("steady\r\n"))

	first := srv.overviewSnapshot(context.Background())
	if first.revision == 0 {
		t.Fatal("revision 0 is the never-seen sentinel — a real snapshot must not carry it")
	}

	// A REBUILD with no change. Expire the TTL so this is genuinely a second computation, not the
	// cache being served: the point is that recomputing an identical answer is not an event.
	time.Sleep(sessionsOverviewCacheTTL + 150*time.Millisecond)
	same := srv.overviewSnapshot(context.Background())
	if same.revision != first.revision {
		t.Fatalf("revision moved %d → %d with nothing changed — every connection would re-push an identical frame once a second",
			first.revision, same.revision)
	}

	sess.Buffer.Write([]byte("something happened\r\n"))
	time.Sleep(sessionsOverviewCacheTTL + 150*time.Millisecond)
	moved := srv.overviewSnapshot(context.Background())
	if moved.revision == first.revision {
		t.Fatal("revision did NOT move on real output — the card would never update again")
	}
	if moved.frame == nil || !strings.Contains(string(moved.frame), "something happened") {
		t.Fatalf("frame does not carry the new output: %s", moved.frame)
	}
	if !strings.Contains(string(moved.frame), MsgTypeSessionsOverview) {
		t.Fatalf("frame is not a %s control message: %s", MsgTypeSessionsOverview, moved.frame)
	}
}

func TestOverviewScreen_ReplaysOnlyWhenTheBufferMoved(t *testing.T) {
	// The expensive half of a card is replaying up to 128 KiB of ring onto a full grid. It used to
	// run for every session every tick regardless of whether that session had emitted a byte, so
	// the cost tracked wall clock and tab count rather than anything the user did.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("hello\r\n"))
	cols, rows := sess.PTYSize()

	first := srv.sessionScreen(sess.ID, sess.Buffer, cols, rows)
	again := srv.sessionScreen(sess.ID, sess.Buffer, cols, rows)
	// Identity, not equality: an equal-but-rebuilt grid is exactly the work being skipped, and
	// only pointer identity can tell the two apart.
	if len(first) == 0 || &first[0] != &again[0] {
		t.Fatal("an untouched buffer was replayed twice — the cost still tracks the clock, not the change")
	}

	sess.Buffer.Write([]byte("world\r\n"))
	after := srv.sessionScreen(sess.ID, sess.Buffer, cols, rows)
	if len(after) > 0 && len(first) > 0 && &after[0] == &first[0] {
		t.Fatal("new output served a stale screen — the card would show the past")
	}
	if !strings.Contains(strings.Join(after, "\n"), "world") {
		t.Fatalf("replayed screen lost the new output: %q", after)
	}

	// A resize repaints the SAME bytes onto a different grid, so the seq alone must not be taken
	// as permission to reuse.
	resized := srv.sessionScreen(sess.ID, sess.Buffer, cols/2, rows)
	if len(resized) > 0 && len(after) > 0 && &resized[0] == &after[0] {
		t.Fatal("a resized grid reused the old screen — the card would keep the old wrapping")
	}
}

func TestOverviewScreenCache_DropsClosedSessions(t *testing.T) {
	// The cached grid is the largest thing this file holds; keeping one per session that ever
	// existed is a leak that only shows up on a long-lived server.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("output\r\n"))
	srv.sessionsOverview(context.Background())

	srv.screenCacheMu.Lock()
	held := len(srv.screenCache)
	srv.screenCacheMu.Unlock()
	if held == 0 {
		t.Fatal("nothing cached — the reuse path is not being exercised at all")
	}

	sm.DestroyAll()
	srv.sessionsOverview(context.Background())

	srv.screenCacheMu.Lock()
	defer srv.screenCacheMu.Unlock()
	if len(srv.screenCache) != 0 {
		t.Fatalf("closed session's screen still cached: %d entries", len(srv.screenCache))
	}
}

func TestOverviewSnapshot_CallersCancellationDoesNotPoisonTheSharedRebuild(t *testing.T) {
	// The snapshot describes EVERY session and is served to EVERY caller, but it is built by
	// whichever connection ticks first. When that connection's context was the rebuild's context,
	// closing one tab poisoned everyone's answer — `ps` fails under a dead context, every agent
	// reads as ToolNone, and a well-formed payload claims no session is running an agent. Same lie
	// as an empty pane bar, different door.
	//
	// The dead caller itself is entitled to leave immediately (that is a separate invariant, pinned
	// below). What must NOT happen is the build it kicked off producing a degraded answer for the
	// clients that are still here.
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("worker")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sess.Buffer.Write([]byte("still here\r\n"))

	dead, cancel := context.WithCancel(context.Background())
	cancel() // the caller is already gone before the rebuild starts
	srv.overviewSnapshot(dead)

	// Whatever that dead caller got, the build it started must land intact for everyone else.
	deadline := time.Now().Add(5 * time.Second)
	var snap overviewSnapshot
	for time.Now().Before(deadline) {
		srv.overviewCacheMu.Lock()
		snap = srv.overviewCache
		srv.overviewCacheMu.Unlock()
		if snap.revision != 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(snap.entries) != 1 {
		t.Fatalf("the rebuild a dead caller started published %d entries, want 1 — it rode that caller's lifetime",
			len(snap.entries))
	}
	if snap.frame == nil || !strings.Contains(string(snap.frame), "still here") {
		t.Fatalf("frame lost the session's output: %s", snap.frame)
	}
}

func TestOverviewSnapshot_AWaiterLeavesWhenItsOwnCallerGivesUp(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("worker"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Stand in for a rebuild that has wedged inside an unpreemptable read: an in-flight promise
	// that never completes. This is the state the whole design has to survive.
	stuck := &overviewBuild{done: make(chan struct{})}
	srv.overviewCacheMu.Lock()
	srv.overviewInFlight = stuck
	srv.overviewCacheAt = time.Time{} // expired, so callers take the miss path
	srv.overviewCacheMu.Unlock()
	defer close(stuck.done)

	gone, cancel := context.WithCancel(context.Background())
	done := make(chan overviewSnapshot, 1)
	go func() { done <- srv.overviewSnapshot(gone) }()

	// The caller's connection drops while the rebuild is still stuck.
	cancel()

	select {
	case <-done:
		// Left as soon as its own context ended, which is the entire point.
	case <-time.After(2 * time.Second):
		t.Fatal("a caller whose context was cancelled could not abandon a wedged rebuild — " +
			"this is the goroutine leak a mutex makes unavoidable")
	}
}

// A rebuild that keeps failing must retry at the healthy cadence, not continuously. Without an
// ATTEMPT stamp, a failure never refreshes overviewCacheAt, so the next caller starts another
// rebuild instantly — turning "slow but eventually right" into "permanently empty, at full CPU".
func TestOverviewSnapshot_AFailedRebuildDoesNotSpin(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("worker"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A build attempt just happened and published nothing (the failure shape).
	srv.overviewCacheMu.Lock()
	srv.overviewAttemptAt = time.Now()
	srv.overviewCacheAt = time.Time{}
	srv.overviewCacheMu.Unlock()

	srv.overviewSnapshot(context.Background())

	srv.overviewCacheMu.Lock()
	spinning := srv.overviewInFlight != nil
	srv.overviewCacheMu.Unlock()
	if spinning {
		t.Fatal("a caller started a fresh rebuild immediately after a failed one — the server would " +
			"do nothing but retry for as long as the condition lasts")
	}

	// …and it does resume once the cadence allows it.
	srv.overviewCacheMu.Lock()
	srv.overviewAttemptAt = time.Now().Add(-2 * sessionsOverviewCacheTTL)
	srv.overviewCacheMu.Unlock()
	if snap := srv.overviewSnapshot(context.Background()); snap.revision == 0 {
		t.Fatal("the retry never produced a snapshot — the overview would stay empty for good")
	}
}

// ── CP1: the eye this rebuild did not have ───────────────────────────────────────────────────
//
// The tmux probe has published its own duration ever since its months-old lag was finally
// measured (agentintel.TmuxProbeDuration). The non-tmux rebuild — the structural twin, on the
// same 1s ticker — published only WHICH path ran and whether it gave up, never how long it took.
// So "is the rebuild eating the tick?" had no answer but a guess, which is exactly the mistake
// this feature already shipped once ("the ticker is already the clock, so this is a pure
// optimisation"). The first test pins the instrument; the second one produces the number.

// histogramCount reads a histogram's observation count out of the metric registry. The registry
// exposes histograms only through WritePrometheus, so this parses the exposition text — which is
// also precisely what a real scrape would see, i.e. it fails if the metric is unreachable rather
// than merely unset.
func histogramCount(t *testing.T, name string) uint64 {
	t.Helper()
	var buf bytes.Buffer
	obs.WritePrometheus(&buf)
	prefix := name + "_count "
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 10, 64)
		if err != nil {
			t.Fatalf("unparsable %s line %q: %v", name, line, err)
		}
		return n
	}
	t.Fatalf("%s is not registered — the overview rebuild has no duration instrument at all", name)
	return 0
}

const overviewRebuildDurationMetric = "terminal_overview_rebuild_duration_seconds"

func TestOverviewRebuild_IsTimed(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	if _, err := sm.Create("worker"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	before := histogramCount(t, overviewRebuildDurationMetric)
	b := &overviewBuild{done: make(chan struct{})}
	srv.buildOverview(context.Background(), b)
	after := histogramCount(t, overviewRebuildDurationMetric)

	// THIS rebuild's own cost, read off the build rather than inferred from a process-wide
	// counter. Rebuilds are detached from their callers by design, so any other one running
	// concurrently makes a global delta a statement about the process, not about this build —
	// which is how this assertion first failed: "recorded 2 observations" during a full run,
	// green in isolation. An intermittently-wrong assertion is worse than none.
	if b.elapsed <= 0 {
		t.Fatal("the rebuild did not time itself — an untimed rebuild is one nobody can find out about")
	}
	// And the instrument is actually wired to the registry: without this the field above could be
	// set while nothing ever reaches a scrape. `>= 1` rather than `== 1` because the count is
	// process-wide and a detached rebuild may legitimately land inside this window; the exactness
	// that matters is asserted above, where it belongs.
	if delta := int64(after) - int64(before); delta < 1 {
		t.Fatalf("a completed rebuild reached the duration histogram %d times — the field is set but "+
			"the metric is not connected to anything", delta)
	}
}

// claudeTranscriptBody builds a syntactically real Claude JSONL transcript: `turns` complete
// user → tool_use → tool_result → end_turn cycles, padded to the order of magnitude a transcript
// that has been open for a while actually reaches. Size is the point — the incremental driver's
// FIRST pass over a session reads the whole file, and that first pass is what a freshly started
// server pays for every terminal at once.
func claudeTranscriptBody(sessionID, cwd string, turns int) string {
	var sb strings.Builder
	pad := strings.Repeat("context line that stands in for a real message body; ", 4)
	base := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	for i := 0; i < turns; i++ {
		at := base.Add(time.Duration(i) * 10 * time.Second)
		ts := func(off int) string { return at.Add(time.Duration(off) * time.Second).Format(time.RFC3339) }
		tool := fmt.Sprintf("tool_%d", i)
		fmt.Fprintf(&sb, `{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":"%s%d"}}`+"\n",
			sessionID, cwd, ts(0), pad, i)
		fmt.Fprintf(&sb, `{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"id":"msg_%d_a","role":"assistant","model":"claude-sonnet-5","stop_reason":"tool_use","content":[{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":"%s/file_%d.go"}}],"usage":{"input_tokens":1200,"output_tokens":48}}}`+"\n",
			sessionID, cwd, ts(1), i, tool, cwd, i)
		fmt.Fprintf(&sb, `{"type":"user","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"%s"}]}}`+"\n",
			sessionID, cwd, ts(2), tool, pad)
		fmt.Fprintf(&sb, `{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":%q,"message":{"id":"msg_%d_b","role":"assistant","model":"claude-sonnet-5","stop_reason":"end_turn","content":[{"type":"text","text":"%s"}],"usage":{"input_tokens":1300,"output_tokens":96}}}`+"\n",
			sessionID, cwd, ts(3), i, pad)
	}
	return sb.String()
}

// nearestRank is the plain nearest-rank percentile. Deliberately not an interpolating one: with
// 20 samples an interpolated P95 is a number no single rebuild ever took.
func nearestRank(sorted []time.Duration, pct int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (pct*len(sorted)+99)/100 - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func summarize(t *testing.T, label string, samples []time.Duration) time.Duration {
	t.Helper()
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p50, p95 := nearestRank(sorted, 50), nearestRank(sorted, 95)
	t.Logf("%-24s n=%d  min=%v  P50=%v  P95=%v  max=%v",
		label, len(sorted), sorted[0], p50, p95, sorted[len(sorted)-1])
	return p95
}

// TestOverviewRebuild_RealMachineCost is the measurement CP1 exists to produce.
//
// Everything known about this rebuild's cost came from a fixture with NO agent process and NO
// transcript — it isolated the screen replay and nothing else, which is the cheap half. This runs
// the whole path on real machinery: real PTYs, real child processes the detector must find in a
// real `ps` snapshot, and real transcripts on disk that the incremental driver actually parses.
//
// It asserts only that the fixture is genuinely exercising that path (five detected agents, five
// timed rebuilds per round). The DURATION is reported, never asserted — a threshold here would be
// a machine-speed test, and the number's job is to decide whether CP2 is worth doing at all, not
// to pass or fail.
func TestOverviewRebuild_RealMachineCost(t *testing.T) {
	const sessionCount = 5
	const rounds = 20

	base := t.TempDir()

	// A process the detector will recognise. Detection matches the BASENAME TOKEN of a process's
	// argv as `ps` reports it, so a long-lived script named "claude" is an agent as far as every
	// layer under test is concerned ("/bin/sh …/bin/claude" → token base "claude").
	//
	// A script rather than a copy of some system binary: macOS refuses to execute a copied
	// platform binary at all (SIGKILL, exit 137 — verified), so that route measures nothing.
	binDir := filepath.Join(base, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeClaude := filepath.Join(binDir, "claude")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Hermetic transcript roots: DW_CLAUDE_PROJECTS decides where shards are read from, and
	// CLAUDE_CONFIG_DIR keeps the PID→session lookup off the real ~/.claude.
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(base, "claude-projects"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(base, "claude-home"))
	locator := agentintel.NewProjectLocator()

	sm := NewSessionManager(1<<20, "/bin/sh")
	t.Cleanup(sm.DestroyAll)
	srv, err := NewServer(WithConfig(Config{
		Addr:         ":0",
		DefaultShell: "/bin/sh",
		BufferSize:   1 << 20,
		MaxSessions:  16,
		AuthCode:     testAuthCode,
	}))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.mgr = sm

	type fixture struct {
		sess       *Session
		transcript string
	}
	fixtures := make([]fixture, 0, sessionCount)
	for i := 0; i < sessionCount; i++ {
		cwd := filepath.Join(base, "proj", strconv.Itoa(i))
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		shard := locator.ClaudeProjectDir(cwd)
		if err := os.MkdirAll(shard, 0o755); err != nil {
			t.Fatal(err)
		}
		sessionID := fmt.Sprintf("fixture-session-%d", i)
		path := filepath.Join(shard, sessionID+".jsonl")
		// EvalSymlinks because the locator resolves the cwd the same way (macOS /var → /private/var);
		// the transcript has to name the path the driver will see.
		realCWD, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(claudeTranscriptBody(sessionID, realCWD, 400)), 0o600); err != nil {
			t.Fatal(err)
		}

		sess, err := sm.CreateWithOptions(CreateOptions{Name: fmt.Sprintf("agent-%d", i), Shell: "/bin/sh", CWD: cwd})
		if err != nil {
			t.Fatalf("CreateWithOptions: %v", err)
		}
		// A real desktop grid, not the default — the replay cost scales with it.
		if err := sess.SetPTYSize(200, 50); err != nil {
			t.Fatalf("SetPTYSize: %v", err)
		}
		// Start the agent as a CHILD of this session's shell: detection walks descendants and
		// never looks at the shell itself.
		if _, err := sess.PTY.Write([]byte(fakeClaude + "\n")); err != nil {
			t.Fatalf("write to pty: %v", err)
		}
		fixtures = append(fixtures, fixture{sess: sess, transcript: path})
	}

	if info, err := os.Stat(fixtures[0].transcript); err == nil {
		t.Logf("fixture: %d sessions, %d KiB transcript each, 200x50 grid",
			sessionCount, info.Size()/1024)
	}

	// Wait for the process table to show every fake agent. The inspector caches `ps` for 3s, so
	// this is a poll, not a sleep.
	deadline := time.Now().Add(30 * time.Second)
	var detected int
	var rules []string
	for time.Now().Before(deadline) {
		detected = 0
		rules = rules[:0]
		for _, e := range srv.sessionsOverview(context.Background()) {
			if e.AgentTool != "" {
				detected++
				rules = append(rules, e.StatusRule)
			}
		}
		if detected == sessionCount {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if detected != sessionCount {
		t.Fatalf("only %d/%d sessions have a detected agent — the fixture is measuring the cheap path, "+
			"which is the very mistake this measurement exists to correct", detected, sessionCount)
	}
	// Detected is not the same as PARSED. `transcript.unlocatable` means the driver never found a
	// file and the expensive half never ran, which would make every number below a measurement of
	// the wrong thing — and it would look exactly like a fast machine.
	for i, rule := range rules {
		if !strings.HasPrefix(rule, "transcript.") || rule == string(agentintel.RuleTranscriptUnlocatable) {
			t.Fatalf("session %d resolved via %q — its transcript was never parsed, so this fixture "+
				"measures the cheap path", i, rule)
		}
	}

	rebuild := func() time.Duration {
		b := &overviewBuild{done: make(chan struct{})}
		start := time.Now()
		srv.buildOverview(context.Background(), b)
		return time.Since(start)
	}

	// The cold pass — every transcript parsed end to end, every screen replayed from scratch: what
	// a server pays in its first second with agents already running.
	//
	// It has to be MADE cold. The detection wait above already drove several rebuilds, so the
	// drivers are bound and incremental by now; measuring "the first rebuild" at this point would
	// quietly report a warm number under a cold label. Dropping the tracker and the screen cache
	// puts the server back in the state it boots into.
	srv.sessionAgent = newSessionAgentTracker()
	srv.screenCacheMu.Lock()
	srv.screenCache = nil
	srv.screenCacheMu.Unlock()
	t.Logf("%-24s %v", "cold first rebuild", rebuild())

	// The quiet shape: nothing moved. This is the one CP2 would optimise, so it is the one its
	// trigger reads.
	quiet := make([]time.Duration, 0, rounds)
	for i := 0; i < rounds; i++ {
		quiet = append(quiet, rebuild())
	}
	quietP95 := summarize(t, "quiet (nothing moved)", quiet)

	// The busy shape: every session emitted output AND its agent appended a turn, so no screen is
	// reused and every driver has new bytes to parse. This is the worst honest tick.
	busy := make([]time.Duration, 0, rounds)
	noise := []byte(strings.Repeat("build output line that repaints the card\r\n", 48))
	for i := 0; i < rounds; i++ {
		for j, f := range fixtures {
			f.sess.Buffer.Write(noise)
			fh, err := os.OpenFile(f.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fh.WriteString(claudeTranscriptBody(fmt.Sprintf("fixture-session-%d", j), base, 1))
			fh.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		busy = append(busy, rebuild())
	}
	busyP95 := summarize(t, "busy (every session moved)", busy)

	t.Logf("CP2 trigger reads the QUIET P95 (%v) against 20ms; busy P95 was %v", quietP95, busyP95)
}
