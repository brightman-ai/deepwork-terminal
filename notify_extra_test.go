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

