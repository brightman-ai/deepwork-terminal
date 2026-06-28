// Package authgate holds framework-agnostic building blocks shared by every deepwork auth gate
// (the standalone terminal's net/http authWrap and deepwork-pro's gin middleware). It deliberately
// knows nothing about HTTP, gin, IPs or cookies — it is pure policy, so a single implementation
// serves both call sites with no duplication (the SSOT for auth-failure throttling).
package authgate

import (
	"math"
	"sync"
	"time"
)

// Throttle is a process-wide (GLOBAL, not per-IP) brake on FAILED auth attempts.
//
// Why global, not per-IP: the auto-generated auth code is short + human-friendly (~39-bit), and a
// public cloudflare tunnel reaches the server over loopback (cloudflared → 127.0.0.1), collapsing
// every visitor to one source address. Per-IP limiting is therefore both useless (all traffic looks
// identical) and forgeable (X-Forwarded-For is attacker-controlled), so only a single shared failure
// budget actually bounds a network brute-force.
//
// Two invariants, both intentional:
//   - Only FAILED attempts are charged. A correct code is NEVER delayed — call Reset on success and
//     Penalty only on a failed compare. Honest logins stay instant even while a flood is in flight.
//   - It never hard-locks. It adds increasing delay (and the caller surfaces Retry-After); it never
//     refuses a correct code. A legitimate user is at worst slowed if THEY mistype — never locked out
//     by someone else's guessing.
//
// The failure score decays exponentially, so a few human typos cost nothing (they sit under the free
// burst and evaporate during the quiet that follows) while a sustained guessing flood is throttled to
// an infeasible steady-state rate.
type Throttle struct {
	mu    sync.Mutex
	score float64          // decaying count of recent failures
	last  time.Time        // when score was last updated (zero = never)
	now   func() time.Time // clock seam; overridden in tests
}

const (
	// freeBurst failures cost no delay — fat-finger headroom for a human typing a code.
	freeBurst = 5.0
	// halfLife is how fast the failure score decays: it halves every 30s of quiet.
	halfLife = 30 * time.Second
	// stepDelay is the base delay added per failure beyond the burst (grows linearly in the overage).
	stepDelay = 400 * time.Millisecond
	// maxDelay caps a single parked attempt so a request never hangs forever and self-inflicted
	// resource use stays bounded.
	maxDelay = 5 * time.Second
)

// NewThrottle returns a ready-to-use Throttle backed by the real clock.
func NewThrottle() *Throttle {
	return &Throttle{now: time.Now}
}

// Penalty decays the running failure score, charges one failure, and returns how long THIS failed
// attempt's response should be delayed. Call it ONLY after a failed compare. A zero return means the
// attempt is still within the free burst (the caller should answer with a normal 401, no throttle).
func (t *Throttle) Penalty() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if !t.last.IsZero() {
		// score *= 0.5 ^ (elapsed / halfLife)
		elapsed := now.Sub(t.last)
		t.score *= math.Pow(0.5, float64(elapsed)/float64(halfLife))
	}
	t.last = now
	t.score++
	over := t.score - freeBurst
	if over <= 0 {
		return 0
	}
	d := time.Duration(over * float64(stepDelay))
	if d > maxDelay {
		d = maxDelay
	}
	return d
}

// Reset clears the failure score. A legitimate login calls this so any penalty built up by an
// attacker (or the user's own earlier typos) doesn't linger and slow the next honest mistake.
func (t *Throttle) Reset() {
	t.mu.Lock()
	t.score = 0
	t.last = time.Time{}
	t.mu.Unlock()
}
