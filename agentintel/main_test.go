package agentintel

import (
	"os"
	"testing"
)

// TestMain isolates this binary from any real tmux server before a single test runs.
//
// Two of this package's tests build a TmuxStateService and probe topology; that opens a control
// connection to whatever socket the environment names, and with TMUX unset that is the
// developer's own live server. A test binary that exits — or is killed — then drops that client
// without a detach handshake, which is what crashed a real server twice on 2026-08-08. See
// IsolateTmuxForTests for the full chain.
func TestMain(m *testing.M) {
	IsolateTmuxForTests()
	os.Exit(m.Run())
}

// The isolation must itself be checkable, or it is one more guarantee held by a comment.
//
// Asserted at the STRUCTURAL level (no connection object exists) rather than behaviourally
// (nothing connected this time): a control client that is merely pointed somewhere harmless is
// still a client that can die badly, and "it didn't connect during this run" is exactly the kind
// of evidence that stays true right up until the run where it doesn't.
func TestTmuxIsolation_NoControlConnectionCanExistInTests(t *testing.T) {
	if c := newTmuxControl(); c != nil {
		t.Fatal("a tmux control connection was created inside a test binary — it will attach to " +
			"whatever socket the environment names, and when this process exits (or is killed) " +
			"the client vanishes without a detach handshake. That crashed a real tmux server " +
			"twice on 2026-08-08. See IsolateTmuxForTests.")
	}
	if p := NewTmuxProber(SharedProcessInspector); p.control != nil {
		t.Fatal("TmuxProber built a control connection despite the test isolation — the guard is " +
			"being bypassed somewhere between newTmuxControl and the prober")
	}
	if got := os.Getenv("TMUX"); got == "" {
		t.Fatal("TMUX is unset inside a test binary, so every fork/exec fallback targets the " +
			"DEFAULT socket — the developer's own live server")
	}
}
