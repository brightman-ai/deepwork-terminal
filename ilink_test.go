package terminal

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	ilinksdk "github.com/the-yex/wechat-ilink-sdk"
)

// newTestIlink builds an ilinkStore backed by a temp dir with a fixed key, with no
// real SDK client. Tests inject sendFn to exercise the state machine offline.
func newTestIlink(t *testing.T) *ilinkStore {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return &ilinkStore{dir: t.TempDir(), key: key}
}

func seedLoggedIn(s *ilinkStore, send func(ctx context.Context, to, text string) error) {
	s.state = ilinkState{LoggedIn: true, UserID: "u1", ContextToken: "ctx1", WindowStart: time.Now()}
	s.sendFn = send
}

func TestIlinkNotConfigured(t *testing.T) {
	s := newTestIlink(t)
	if got := s.trySend(context.Background(), "e1", "hi"); got != ilinkNotConfigured {
		t.Fatalf("fresh store: want ilinkNotConfigured, got %v", got)
	}
}

func TestIlinkNoInboundSeed(t *testing.T) {
	s := newTestIlink(t)
	s.state = ilinkState{LoggedIn: true, UserID: "u1"} // logged in but no context token
	s.sendFn = func(context.Context, string, string) error { return nil }
	if got := s.trySend(context.Background(), "e1", "hi"); got != ilinkDormant {
		t.Fatalf("no seed: want ilinkDormant, got %v", got)
	}
}

func TestIlinkCountingAndRenewalHint(t *testing.T) {
	s := newTestIlink(t)
	var sent []string
	seedLoggedIn(s, func(_ context.Context, _, text string) error {
		sent = append(sent, text)
		return nil
	})
	for i := 1; i <= ilinkMaxSends; i++ {
		if got := s.trySend(context.Background(), "e", "BODY"); got != ilinkSent {
			t.Fatalf("send %d: want ilinkSent, got %v", i, got)
		}
	}
	if s.state.SentCount != ilinkMaxSends {
		t.Fatalf("SentCount: want %d, got %d", ilinkMaxSends, s.state.SentCount)
	}
	// Sends 1..6 carry no renewal hint; 7..10 do.
	for i, text := range sent {
		seq := i + 1
		hasHint := strings.Contains(text, "回复任意字符可续订")
		switch {
		case seq < ilinkRenewalAt && hasHint:
			t.Fatalf("send %d should NOT have renewal hint: %q", seq, text)
		case seq >= ilinkRenewalAt && !hasHint:
			t.Fatalf("send %d SHOULD have renewal hint: %q", seq, text)
		}
	}
}

func TestIlinkQuotaExhaustedGoesDormant(t *testing.T) {
	s := newTestIlink(t)
	seedLoggedIn(s, func(context.Context, string, string) error { return nil })
	for i := 0; i < ilinkMaxSends; i++ {
		s.trySend(context.Background(), "e", "x")
	}
	if got := s.trySend(context.Background(), "e", "x"); got != ilinkDormant {
		t.Fatalf("11th send: want ilinkDormant, got %v", got)
	}
	if s.state.Dormant == "" {
		t.Fatal("expected Dormant reason to be set after quota exhaustion")
	}
}

func TestIlinkWindowExpiryGoesDormant(t *testing.T) {
	s := newTestIlink(t)
	seedLoggedIn(s, func(context.Context, string, string) error { return nil })
	s.state.WindowStart = time.Now().Add(-ilinkWindowTTL - time.Minute) // window lapsed
	if got := s.trySend(context.Background(), "e", "x"); got != ilinkDormant {
		t.Fatalf("expired window: want ilinkDormant, got %v", got)
	}
	if !strings.Contains(s.state.Dormant, "window expired") {
		t.Fatalf("want window-expired reason, got %q", s.state.Dormant)
	}
}

func TestIlinkInboundRefreshResetsAndRecovers(t *testing.T) {
	s := newTestIlink(t)
	seedLoggedIn(s, func(context.Context, string, string) error { return nil })
	for i := 0; i < ilinkMaxSends; i++ {
		s.trySend(context.Background(), "e", "x")
	}
	s.trySend(context.Background(), "e", "x") // → dormant
	if s.state.Dormant == "" {
		t.Fatal("precondition: expected dormant")
	}
	// A user reply re-seeds the window: counter back to 0, dormant cleared, sendable.
	s.inboundRefresh("u1", "ctx2")
	if s.state.SentCount != 0 || s.state.Dormant != "" || s.state.ContextToken != "ctx2" {
		t.Fatalf("inboundRefresh did not reset cleanly: %+v", s.state)
	}
	if got := s.trySend(context.Background(), "e", "x"); got != ilinkSent {
		t.Fatalf("after refresh: want ilinkSent, got %v", got)
	}
}

func TestIlinkSessionExpiredVsAmbiguous(t *testing.T) {
	// Definite session error → dormant + fall back to A.
	s := newTestIlink(t)
	seedLoggedIn(s, func(context.Context, string, string) error { return ilinksdk.ErrSessionExpired })
	if got := s.trySend(context.Background(), "e", "x"); got != ilinkDormant {
		t.Fatalf("session expired: want ilinkDormant, got %v", got)
	}
	if s.state.Dormant == "" {
		t.Fatal("session expired should mark dormant")
	}
	// Ambiguous (network) error → ilinkAmbiguous, NOT dormant (transient).
	s2 := newTestIlink(t)
	seedLoggedIn(s2, func(context.Context, string, string) error { return errors.New("connection reset") })
	if got := s2.trySend(context.Background(), "e", "x"); got != ilinkAmbiguous {
		t.Fatalf("ambiguous: want ilinkAmbiguous, got %v", got)
	}
	if s2.state.Dormant != "" {
		t.Fatalf("ambiguous must NOT mark dormant, got %q", s2.state.Dormant)
	}
}

func TestIlinkPersistRoundTripEncrypted(t *testing.T) {
	s := newTestIlink(t)
	seedLoggedIn(s, func(context.Context, string, string) error { return nil })
	s.state.SentCount = 3
	s.mu.Lock()
	s.persistLocked()
	s.mu.Unlock()

	// Raw file must NOT contain plaintext secrets.
	raw, err := os.ReadFile(s.statePath())
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if bytes.Contains(raw, []byte("ctx1")) || bytes.Contains(raw, []byte("u1")) {
		t.Fatal("state file is NOT encrypted (plaintext secret found)")
	}

	// A fresh store with the same dir+key recovers the state.
	s2 := &ilinkStore{dir: s.dir, key: s.key}
	s2.loadState()
	if !s2.state.LoggedIn || s2.state.UserID != "u1" || s2.state.ContextToken != "ctx1" || s2.state.SentCount != 3 {
		t.Fatalf("round-trip mismatch: %+v", s2.state)
	}
}
