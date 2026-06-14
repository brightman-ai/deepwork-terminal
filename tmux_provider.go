package terminal

import (
	"context"
	"encoding/json"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// TmuxStateProvider yields a JSON-encoded tmux topology snapshot for the host.
//
// The terminal ships a default, in-process provider (defaultTmuxProvider) so the
// standalone build gets tmux topology/prefix/agent-status without any host wiring.
// A host (e.g. the pro repo) may inject a richer provider via WithTmuxProvider;
// this is purely additive and never required.
//
// shellPID is the calling session's shell PID (0 when unknown); it lets the
// provider compute whether that specific shell is attached inside tmux.
type TmuxStateProvider interface {
	TmuxState(ctx context.Context, shellPID int) (json.RawMessage, error)
}

// defaultTmuxProvider is the terminal-owned provider backed by agentintel.
// It caches prefix + installed and uses a single batched tmux query for topology,
// so a ~1s WS poll stays cheap.
type defaultTmuxProvider struct {
	svc *agentintel.TmuxStateService
}

func newDefaultTmuxProvider() *defaultTmuxProvider {
	return &defaultTmuxProvider{svc: agentintel.NewTmuxStateService()}
}

func (p *defaultTmuxProvider) TmuxState(ctx context.Context, shellPID int) (json.RawMessage, error) {
	st := p.svc.State(ctx, shellPID)
	return json.Marshal(st)
}

// WithTmuxProvider overrides the default in-process tmux provider.
// Hosts use this to supply a richer snapshot; standalone needs nothing.
func WithTmuxProvider(p TmuxStateProvider) Option {
	return func(s *Server) { s.tmuxProvider = p }
}
