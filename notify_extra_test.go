package terminal

import (
	"testing"
	"time"
)

func TestBootstrapTokenOneTimeAndTTL(t *testing.T) {
	b := newBootstrapStore()

	tok := b.issue()
	if tok == "" {
		t.Fatal("issue returned empty token")
	}
	// First consume succeeds; second is rejected (single-use).
	if !b.consume(tok) {
		t.Fatal("first consume should succeed")
	}
	if b.consume(tok) {
		t.Fatal("second consume must fail (token is single-use)")
	}
	// Unknown / empty tokens are rejected.
	if b.consume("nope") || b.consume("") {
		t.Fatal("unknown/empty tokens must be rejected")
	}

	// Expired token: issue at T, consume at T+TTL+1m → rejected, and the entry is
	// pruned (not leaked).
	base := time.Now()
	nowFunc = func() time.Time { return base }
	defer func() { nowFunc = time.Now }()
	expTok := b.issue()
	nowFunc = func() time.Time { return base.Add(bootstrapTTL + time.Minute) }
	if b.consume(expTok) {
		t.Fatal("expired token must be rejected")
	}
}

func TestNotifyMetricsRecord(t *testing.T) {
	m := newNotifyMetrics()
	m.record("ilink", false)
	m.record("webpush", true)  // ilink tried, fell back
	m.record("webpush", false) // direct web push (ilink not configured)
	m.record("none", true)     // ilink tried, nothing delivered

	v := m.view()
	if v.Events != 4 {
		t.Fatalf("events: want 4, got %d", v.Events)
	}
	if v.ViaIlink != 1 {
		t.Fatalf("viaIlink: want 1, got %d", v.ViaIlink)
	}
	if v.ViaWebPush != 2 {
		t.Fatalf("viaWebPush: want 2, got %d", v.ViaWebPush)
	}
	if v.FellBack != 2 {
		t.Fatalf("fellBack: want 2, got %d", v.FellBack)
	}
	if v.Undelivered != 1 {
		t.Fatalf("undelivered: want 1, got %d", v.Undelivered)
	}
	if v.LastChannel != "none" {
		t.Fatalf("lastChannel: want none, got %q", v.LastChannel)
	}
	if v.LastAtMs == 0 {
		t.Fatal("lastAtMs should be set after records")
	}
}
