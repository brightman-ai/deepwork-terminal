package terminal

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ── What a card shows must be what the terminal showed ───────────────────────────────────────
//
// An independent Witness reported the overview cards drawing mangled text: `prompt ok` appearing
// as `promptrok`, `build rc=0` as `buildtrc=0`, `core rc=0` as `coredrc=0`, and
// `bypass permissions on (shift+tab to cycle)` as `▶▶Obypass|permissions on (shift+tab—tofcycle)`.
//
// Every one of those is a SPACE that turned into a consonant, plus some invented leading glyphs —
// which is what OCR does to small monospace text in a downscaled screenshot, and is not a thing any
// code in this path can do (the card renders `{{ line }}` inside `white-space: pre`; there is no
// transform between the wire and the glyph). The finding was reported from screenshots, so the
// reading itself was never verified against the bytes.
//
// This is that verification, and it is a TEST rather than a one-off check because the honest answer
// to "is the text mangled?" should not have to be re-derived from a screenshot next time. It runs
// the real path — ring buffer → renderScreen replay → TailFromLines → SessionOverviewEntry.Tail —
// and asserts the lines arrive byte-for-byte, spaces included.
//
// If this ever goes red, the Witness was right and the bug is in the replay. While it is green, a
// mangled card is a rendering or reading artifact and must be chased in the browser, not here.
func TestOverviewTail_IsByteForByteWhatThePTYWrote(t *testing.T) {
	srv, sm, writeEnds := newTailFidelityServer(t)
	if _, err := sm.Create("worker"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The exact lines from the report. Spaces inside and between words are the whole point.
	lines := []string{
		"prompt ok",
		"build rc=0",
		"core rc=0",
		"bypass permissions on (shift+tab to cycle)",
	}
	if _, err := writeEnds.Get(0).Write([]byte(strings.Join(lines, "\r\n") + "\r\n")); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	entry := waitForTail(t, srv, len(lines))
	got := strings.Join(entry.Tail, "\n")
	for _, want := range lines {
		if !containsLine(entry.Tail, want) {
			t.Errorf("the card's tail does not contain %q verbatim.\n full tail:\n%s", want, got)
		}
	}
	// And the specific corruptions, named, so a failure says which one came back rather than
	// leaving the next reader to diff two blocks of text by eye.
	for _, corrupt := range []string{"promptrok", "buildtrc=0", "coredrc=0", "tofcycle"} {
		if strings.Contains(got, corrupt) {
			t.Errorf("the tail contains %q — the replay IS mangling spaces after all.\n full tail:\n%s", corrupt, got)
		}
	}
}

func containsLine(tail []string, want string) bool {
	for _, l := range tail {
		if strings.TrimRight(l, " ") == want {
			return true
		}
	}
	return false
}

// waitForTail polls the overview until the session's card carries at least n tail lines. The PTY
// read loop is asynchronous, so a single immediate call races it — and a race that usually passes
// is worse than no test at all.
func waitForTail(t *testing.T, srv *Server, n int) SessionOverviewEntry {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last SessionOverviewEntry
	for time.Now().Before(deadline) {
		entries := srv.sessionsOverview(context.Background())
		if len(entries) > 0 {
			last = entries[0]
			if len(last.Tail) >= n {
				return last
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the card never carried %d tail lines; last had %d: %q", n, len(last.Tail), last.Tail)
	return last
}

// newTailFidelityServer is newOverviewTestServer plus the write ends, which this file needs and
// the others do not.
func newTailFidelityServer(t *testing.T) (*Server, *SessionManager, *SafeWriteEnds) {
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
	return srv, sm, writeEnds
}
