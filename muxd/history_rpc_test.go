package muxd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The protocol half of scrollback: reading a page, searching, paging the style table, and — the
// one that decides whether this feature can ship at all — what happens when the daemon on the
// other end is older than the binary asking.

// historyPair starts a daemon with one pipe-backed session and returns a connected client, the
// session id, and the write end of the PTY so a test can produce output.
func historyPair(t *testing.T, opts ...func(*CreateReq)) (*Client, string, *os.File) {
	t.Helper()
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	var writeEnd *os.File
	d := NewDaemonWith(0, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, w, perr := os.Pipe()
		writeEnd = w
		return r, nil, perr
	})
	d.SetHistoryLines(5000)
	go func() { _ = d.Serve(context.Background(), ln) }()
	t.Cleanup(d.DestroyAll)
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	req := CreateReq{Argv: []string{"/bin/sh"}, Cols: 80, Rows: 5}
	for _, o := range opts {
		o(&req)
	}
	id, _, err := c.Create(req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = writeEnd.Close() })
	return c, id, writeEnd
}

// produce writes to the PTY and waits until the daemon has accounted for the lines, so the test
// never races the read loop.
func produce(t *testing.T, c *Client, id string, want int64) {
	t.Helper()
	waitFor(t, 3*time.Second, "the daemon to account for the output", func() bool {
		ack, err := c.History(HistoryReq{ID: id, From: 0, Count: 1})
		return err == nil && ack.Total >= want
	})
}

func TestHistoryRPC_ReadsAPageWithStyles(t *testing.T) {
	c, id, w := historyPair(t)
	if _, err := w.Write([]byte("\x1b[31mred one\x1b[0m\r\nplain two\r\nthree\r\nfour\r\nfive\r\nsix\r\nseven\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	produce(t, c, id, 3)

	ack, err := c.History(HistoryReq{ID: id, From: 0, Count: 10})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if !ack.Enabled {
		t.Fatal("history reported disabled on a daemon that has it")
	}
	if len(ack.Lines) == 0 {
		t.Fatal("no lines came back")
	}
	if ack.Lines[0].Text != "red one" {
		t.Fatalf("first line = %q", ack.Lines[0].Text)
	}
	if len(ack.Lines[0].Spans) == 0 {
		t.Fatal("colour was lost crossing the protocol")
	}
	if ack.StylesTotal < 2 {
		t.Fatalf("style table has %d entries; the coloured run should have added one", ack.StylesTotal)
	}
	// The table came back because the client claimed to have none.
	if len(ack.Styles) != ack.StylesTotal {
		t.Fatalf("asked for the whole table, got %d of %d", len(ack.Styles), ack.StylesTotal)
	}
	if ack.Styles[ack.Lines[0].Spans[0].Style].FG != IndexedColor(1) {
		t.Fatal("the span does not resolve to red through the returned table")
	}
}

func TestHistoryRPC_StyleTableIsSentIncrementally(t *testing.T) {
	c, id, w := historyPair(t)
	_, _ = w.Write([]byte("\x1b[31ma\x1b[0m\r\n\x1b[32mb\x1b[0m\r\nc\r\nd\r\ne\r\nf\r\n"))
	produce(t, c, id, 2)

	full, err := c.History(HistoryReq{ID: id, From: 0, Count: 10})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// A client that already holds the whole table gets none of it back — ids are append-only, so
	// a prefix is always enough to resolve everything it has been sent.
	again, err := c.History(HistoryReq{ID: id, From: 0, Count: 10, StylesFrom: full.StylesTotal})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(again.Styles) != 0 {
		t.Fatalf("re-sent %d style entries the client already had", len(again.Styles))
	}
	if again.StylesTotal != full.StylesTotal {
		t.Fatalf("table length changed without new styles: %d → %d", full.StylesTotal, again.StylesTotal)
	}
}

func TestHistoryRPC_ScreenReturnsTheLiveGrid(t *testing.T) {
	c, id, w := historyPair(t)
	// More lines than the 5-row grid, so some genuinely scroll off and the screen's numbering has
	// a non-zero history to continue from — which is the thing being asserted.
	_, _ = w.Write([]byte("one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\nvisible"))
	produce(t, c, id, 2)

	ack, err := c.History(HistoryReq{ID: id, Screen: true})
	if err != nil {
		t.Fatalf("History(screen): %v", err)
	}
	joined := ""
	for _, l := range ack.Lines {
		joined += l.Text + "\n"
	}
	if !strings.Contains(joined, "visible") {
		t.Fatalf("the live screen does not contain the last line written: %q", joined)
	}
	if len(ack.Lines) > 0 && ack.Lines[0].N != ack.Total {
		t.Fatalf("screen starts at %d but history ends at %d — a viewer would render a gap",
			ack.Lines[0].N, ack.Total)
	}
}

func TestHistoryRPC_SearchRunsInTheDaemon(t *testing.T) {
	c, id, w := historyPair(t)
	var b strings.Builder
	for i := 0; i < 300; i++ {
		if i == 42 {
			b.WriteString("中文 NEEDLE here\r\n")
		} else {
			b.WriteString("haystack line\r\n")
		}
	}
	_, _ = w.Write([]byte(b.String()))
	produce(t, c, id, 200)

	ack, err := c.SearchHistory(HistorySearchReq{ID: id, Query: "NEEDLE", Limit: 10})
	if err != nil {
		t.Fatalf("SearchHistory: %v", err)
	}
	if len(ack.Matches) != 1 {
		t.Fatalf("got %d matches, want 1: %+v", len(ack.Matches), ack.Matches)
	}
	m := ack.Matches[0]
	if m.N != 42 {
		t.Fatalf("match on line %d, want 42", m.N)
	}
	// Byte offset, matching Span.Start — "中文 " is seven bytes.
	if m.Col != 7 {
		t.Fatalf("match at byte %d, want 7 (中文 is six bytes plus a space)", m.Col)
	}
	if !strings.Contains(m.Text, "NEEDLE") {
		t.Fatalf("the preview line does not contain the match: %q", m.Text)
	}
	// The whole point: the answer is small even though the history is not.
	if len(ack.Matches) > 10 {
		t.Fatal("limit was not honoured")
	}
}

func TestHistoryRPC_UnknownSessionIsAnError(t *testing.T) {
	c, _, _ := historyPair(t)
	if _, err := c.History(HistoryReq{ID: "nope", Count: 10}); err == nil {
		t.Fatal("reading history for a session that does not exist should fail")
	}
	if _, err := c.SearchHistory(HistorySearchReq{ID: "nope", Query: "x"}); err == nil {
		t.Fatal("searching a session that does not exist should fail")
	}
}

func TestHistoryRPC_DisabledSessionSaysSoInsteadOfLookingEmpty(t *testing.T) {
	path := isolatedSocket(t)
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	d := NewDaemonWith(0, -1, func(_ SpawnOptions) (*os.File, *exec.Cmd, error) {
		r, _, perr := os.Pipe()
		return r, nil, perr
	})
	d.SetHistoryLines(-1) // scrollback off
	go func() { _ = d.Serve(context.Background(), ln) }()
	defer d.DestroyAll()
	waitFor(t, 2*time.Second, "daemon to accept", func() bool { return SocketAlive(path) })

	c, err := Connect(path)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	id, _, err := c.Create(CreateReq{Argv: []string{"/bin/sh"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ack, err := c.History(HistoryReq{ID: id, Count: 10})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// "Disabled" and "empty" are different things, and a viewer that cannot tell them apart will
	// tell the user the wrong one.
	if ack.Enabled {
		t.Fatal("a session spawned with scrollback off reported it enabled")
	}
	if len(ack.Lines) != 0 {
		t.Fatal("a session with no history returned lines")
	}
}

// The case that decides whether this can ship without ending everyone's sessions: the daemon
// running on a machine is routinely OLDER than the binary talking to it, because that is the
// entire point of muxd. A client must not send a frame such a daemon cannot parse.
func TestHistoryRPC_AnOlderDaemonIsDetectedNotProbed(t *testing.T) {
	c, id, _ := historyPair(t)

	// Simulate the daemon that predates the feature: same protocol version, capability absent.
	c.mu.Lock()
	peer := c.peer
	peer.Features = []string{FeaturePerAttachmentGeometry}
	c.peer = peer
	c.mu.Unlock()

	_, err := c.History(HistoryReq{ID: id, Count: 10})
	if !errors.Is(err, ErrNoScrollback) {
		t.Fatalf("got %v, want ErrNoScrollback — the client must check the capability, not probe", err)
	}
	_, err = c.SearchHistory(HistorySearchReq{ID: id, Query: "x"})
	if !errors.Is(err, ErrNoScrollback) {
		t.Fatalf("got %v, want ErrNoScrollback", err)
	}
}

// Scrollback was added WITHOUT a protocol bump, on purpose: a bump makes the client refuse to
// talk to a mismatched daemon, which would end every live session on the machine to deliver a
// feature nothing depends on. If someone later bumps the version for this, this test says why not.
func TestScrollbackDidNotCostAProtocolBump(t *testing.T) {
	if ProtoVersion != 2 {
		t.Fatalf("ProtoVersion is %d; scrollback is meant to ride the capability mechanism, not a bump."+
			" If the bump is deliberate, it ends every live session on upgrade — say so where it happens.",
			ProtoVersion)
	}
	has := false
	for _, f := range DaemonFeatures {
		if f == FeatureScrollback {
			has = true
		}
	}
	if !has {
		t.Fatal("the daemon does not advertise scrollback, so no client will ever ask for it")
	}
	for _, f := range RequiredDaemonFeatures {
		if f == FeatureScrollback {
			t.Fatal("scrollback must not be REQUIRED: a daemon without it runs terminals fine," +
				" and requiring it prints an upgrade warning whose only cure ends every session")
		}
	}
}
