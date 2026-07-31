package agentintel

import (
	"testing"
	"time"
)

// The probe and the user share one single-threaded tmux server. These cover the rule that decides
// who yields — and specifically the two ways such a rule goes wrong: never yielding (the original
// contention), and yielding forever (a dashboard frozen because someone is holding a key).
func TestProbeDeferredForInteraction(t *testing.T) {
	now := time.Now()
	built := now.Add(-time.Second)

	t.Run("no interaction ever recorded → never defers", func(t *testing.T) {
		lastInteraction.Store(0)
		if probeDeferredForInteraction(built, now) {
			t.Fatal("deferred with no interaction on record")
		}
	})

	t.Run("defers while the interaction is recent", func(t *testing.T) {
		lastInteraction.Store(now.UnixNano())
		if !probeDeferredForInteraction(built, now.Add(interactionQuiet/2)) {
			t.Fatal("did not defer during the quiet window")
		}
	})

	t.Run("stops deferring once the quiet window elapses", func(t *testing.T) {
		lastInteraction.Store(now.UnixNano())
		if probeDeferredForInteraction(built, now.Add(interactionQuiet)) {
			t.Fatal("still deferring past the quiet window")
		}
	})

	t.Run("never defers a snapshot that was never built", func(t *testing.T) {
		// A user who starts by typing would otherwise stare at an empty dashboard: the first
		// build is what populates it, so it is not deferrable.
		lastInteraction.Store(now.UnixNano())
		if probeDeferredForInteraction(time.Time{}, now.Add(time.Millisecond)) {
			t.Fatal("deferred the very first build")
		}
	})

	t.Run("held key cannot starve the probe past the staleness ceiling", func(t *testing.T) {
		start := now
		var deferredAt time.Duration
		starved := true
		// Simulate a key repeating every 50ms — the quiet window never lapses on its own.
		for elapsed := time.Duration(0); elapsed < 10*time.Second; elapsed += 50 * time.Millisecond {
			at := start.Add(elapsed)
			lastInteraction.Store(at.UnixNano())
			if !probeDeferredForInteraction(start, at) {
				deferredAt = elapsed
				starved = false
				break
			}
		}
		if starved {
			t.Fatal("probe starved: continuous input deferred it indefinitely")
		}
		if deferredAt > interactionMaxStale+100*time.Millisecond {
			t.Fatalf("deferred %v, past the %v ceiling", deferredAt, interactionMaxStale)
		}
	})
}
