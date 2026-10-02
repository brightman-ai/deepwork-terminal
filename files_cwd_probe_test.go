package terminal

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type blockedCWDProvider struct {
	gate  chan struct{}
	calls atomic.Int32
}

func (p *blockedCWDProvider) TmuxState(_ context.Context, _ int) (json.RawMessage, error) {
	p.calls.Add(1)
	<-p.gate // Deliberately ignores context, as a mutex-bound provider can do.
	return json.RawMessage(`{"attachedSession":"s","sessions":[{"name":"s","windows":[{"active":true,"panes":[{"active":true,"cwd":"/real"}]}]}]}`), nil
}
func TestWorkbenchCWDProbe_SharedWhileBlockedAndCancellationReturns(t *testing.T) {
	p := &blockedCWDProvider{gate: make(chan struct{})}
	s := &Server{tmuxProvider: p}
	for n := 0; n < 2; n++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_, err := s.workbenchTmuxState(ctx, 42)
		cancel()
		require.ErrorIs(t, err, context.DeadlineExceeded)
	}
	require.Equal(t, int32(1), p.calls.Load(), "requests must share one blocked probe")
	s.workbenchCWDMu.Lock()
	ready := s.workbenchCWDProbes[42].ready
	s.workbenchCWDMu.Unlock()
	close(p.gate)
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("probe did not finish")
	}
	require.Equal(t, "/real", s.activePaneCWD(context.Background(), 42))
}
