package agentintel

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// A probe that ran out of time did not observe "no panes" — it observed nothing. Publishing
// the two as the same empty topology is what made the pane bar blink out and back: the empty
// answer was cached for a full TTL, then a good probe restored it, then another timed out.
//
// The guard for this was already written and already commented; it just watched the PARENT
// context, while the timeout that fires lives on the per-command child. The parent stays
// healthy, so it never triggered once in the case it was written for.
func TestTopologySnapshotKeepsLastGoodWhenTheProbeRunsOutOfTime(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	s := NewTmuxStateService()
	if !s.TmuxInstalled() {
		t.Skip("no tmux")
	}
	known := TmuxState{
		Installed: true, ServerRunning: true,
		Sessions: []TmuxSessionState{{Name: "known", Windows: []TmuxWindowState{{Index: 1}}}},
	}
	s.topologyMu.Lock()
	s.topology, s.topologyRead, s.topologyAt = known, true, time.Now().Add(-time.Hour)
	s.topologyMu.Unlock()

	// A budget nothing can meet: every command's context is already expired when it starts,
	// while the caller's own context is perfectly healthy — the exact shape of the bug.
	s.cmdTimeout = time.Nanosecond

	got := s.topologySnapshot(context.Background())
	if len(got.Sessions) != 1 || got.Sessions[0].Name != "known" {
		t.Fatalf("a probe that could not answer replaced what we knew: %+v", got.Sessions)
	}
	s.topologyMu.Lock()
	cached := s.topology
	s.topologyMu.Unlock()
	if len(cached.Sessions) != 1 || cached.Sessions[0].Name != "known" {
		t.Fatalf("the unanswered probe was cached, pinning it for a whole TTL: %+v", cached.Sessions)
	}
}

// The converse, so the fix cannot become "never update": a probe that DOES answer replaces the
// cache, including when its honest answer is that the tmux server is gone.
func TestTopologySnapshotPublishesAnAnswerEvenWhenItIsEmpty(t *testing.T) {
	s := NewTmuxStateService()
	s.mu.Lock()
	s.installed, s.installedAt = false, time.Now() // "tmux is not installed" IS an answer
	s.mu.Unlock()
	s.topologyMu.Lock()
	s.topology = TmuxState{Sessions: []TmuxSessionState{{Name: "stale"}}}
	s.topologyRead, s.topologyAt = true, time.Now().Add(-time.Hour)
	s.topologyMu.Unlock()

	if got := s.topologySnapshot(context.Background()); len(got.Sessions) != 0 {
		t.Fatalf("an answered probe did not replace a stale topology: %+v", got.Sessions)
	}
}
