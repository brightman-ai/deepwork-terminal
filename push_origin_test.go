package terminal

import (
	"encoding/json"
	"os"
	"testing"
)

func mkSub(ep, origin string) pushSubscription {
	var s pushSubscription
	s.Endpoint = ep
	s.Keys.P256dh = "p256"
	s.Keys.Auth = "auth"
	s.Origin = origin
	return s
}

// TestReconcileOrigin covers REQ-PUSH-02/03/05/07: subscriptions bound to a stale
// (dead/changed) tunnel origin are unregistered; matching-origin subs are kept; an
// unknown current origin is a no-op; legacy subs without an origin are dropped once.
func TestReconcileOrigin(t *testing.T) {
	dir := t.TempDir()
	ps := newPushStore(dir, "mailto:test@example.com")
	ps.add(mkSub("e1", "https://live.trycloudflare.com"))
	ps.add(mkSub("e2", "https://old-dead.trycloudflare.com")) // stale tunnel origin
	ps.add(mkSub("e3", ""))                                   // legacy, no origin (REQ-PUSH-07)
	ps.add(mkSub("e4", "https://live.trycloudflare.com/x"))   // same origin, different path

	// REQ-PUSH-05: unknown current origin → never drop anything.
	if n := ps.reconcileOrigin(""); n != 0 || ps.count() != 4 {
		t.Fatalf("empty tunnel URL must be a no-op: dropped=%d count=%d", n, ps.count())
	}

	// REQ-PUSH-02/03/07: current = live origin → drop e2 (stale) + e3 (legacy); keep e1,e4.
	if n := ps.reconcileOrigin("https://live.trycloudflare.com"); n != 2 {
		t.Fatalf("want 2 unregistered, got %d", n)
	}
	if ps.count() != 2 {
		t.Fatalf("want 2 remaining, got %d", ps.count())
	}
	for _, ep := range []string{"e1", "e4"} {
		ps.mu.Lock()
		_, ok := ps.subs[ep]
		ps.mu.Unlock()
		if !ok {
			t.Fatalf("%s should have been kept (origin matches)", ep)
		}
	}

	// Persisted to disk (survives a restart).
	data, err := os.ReadFile(ps.subsPath())
	if err != nil {
		t.Fatalf("read subs: %v", err)
	}
	var list []pushSubscription
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal subs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("persisted %d subs, want 2", len(list))
	}

	// Idempotent: a second reconcile against the same origin drops nothing more.
	if n := ps.reconcileOrigin("https://live.trycloudflare.com"); n != 0 {
		t.Fatalf("second reconcile should drop 0, got %d", n)
	}
}

func TestOriginOf(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"https://a.trycloudflare.com":         "https://a.trycloudflare.com",
		"https://a.trycloudflare.com/":        "https://a.trycloudflare.com",
		"https://a.trycloudflare.com/?x=1#f":  "https://a.trycloudflare.com",
		"http://stwork:8087":                  "http://stwork:8087",
		"not a url":                           "",
		"/relative/only":                      "",
	}
	for in, want := range cases {
		if got := originOf(in); got != want {
			t.Errorf("originOf(%q) = %q, want %q", in, got, want)
		}
	}
}
