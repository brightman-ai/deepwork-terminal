package authgate

import (
	"testing"
	"time"
)

// newAt builds a Throttle with a controllable clock so decay is deterministic.
func newAt(t *time.Time) *Throttle {
	return &Throttle{now: func() time.Time { return *t }}
}

func TestThrottle_FreeBurstThenGrowingDelay(t *testing.T) {
	clk := time.Unix(1_700_000_000, 0)
	th := newAt(&clk)

	for i := 0; i < int(freeBurst); i++ {
		if d := th.Penalty(); d != 0 {
			t.Fatalf("failure %d should be inside the free burst, got %v", i+1, d)
		}
	}
	if d := th.Penalty(); d != stepDelay {
		t.Fatalf("first failure past the burst → one step, got %v want %v", d, stepDelay)
	}
	if d := th.Penalty(); d != 2*stepDelay {
		t.Fatalf("second past the burst → two steps, got %v want %v", d, 2*stepDelay)
	}
}

func TestThrottle_DecaysDuringQuiet(t *testing.T) {
	clk := time.Unix(1_700_000_000, 0)
	th := newAt(&clk)
	for i := 0; i < 8; i++ {
		th.Penalty() // build the score above the burst
	}
	clk = clk.Add(10 * halfLife) // long quiet
	if d := th.Penalty(); d != 0 {
		t.Fatalf("score should have decayed below the burst after a long quiet, got %v", d)
	}
}

func TestThrottle_CapsDelay(t *testing.T) {
	clk := time.Unix(1_700_000_000, 0)
	th := newAt(&clk)
	var d time.Duration
	for i := 0; i < 1000; i++ {
		d = th.Penalty()
	}
	if d > maxDelay {
		t.Fatalf("delay must be capped at %v, got %v", maxDelay, d)
	}
}

func TestThrottle_ResetClearsScore(t *testing.T) {
	clk := time.Unix(1_700_000_000, 0)
	th := newAt(&clk)
	for i := 0; i < 20; i++ {
		th.Penalty()
	}
	if th.Penalty() == 0 {
		t.Fatal("score should be built up before reset")
	}
	th.Reset()
	if d := th.Penalty(); d != 0 {
		t.Fatalf("reset clears the score → free burst restored, got %v", d)
	}
}
