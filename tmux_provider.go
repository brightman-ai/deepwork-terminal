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
//
// *agentintel.TmuxStateService is embedded (not held as a named field) so every
// capability it exposes — CopyMotion, NewSession, SelectWindow, SetOverviewActive,
// RefreshClient, TmuxInstalled, ... — is promoted onto defaultTmuxProvider for
// free via Go method promotion. This is deliberate: handleTmux* discovers optional
// capabilities via type assertion (s.tmuxProvider.(TmuxRefresher), etc.), and a
// hand-written one-line forwarder per method is a silent trap — the day
// TmuxStateService gained RefreshClient, defaultTmuxProvider did NOT get a matching
// forwarder, the type assertion quietly failed, and handleTmuxRefresh 501'd in
// production on every deployment for as long as nobody re-derived it. Embedding
// makes that whole bug class structurally impossible: a new exported method on the
// service is automatically satisfied here, with zero lines to remember to write.
// TmuxState below is the one deliberate exception — it adapts State's return type
// (renames + JSON-encodes it), so it cannot be promotion-only and stays hand-written.
type defaultTmuxProvider struct {
	*agentintel.TmuxStateService
}

func newDefaultTmuxProvider() *defaultTmuxProvider {
	return &defaultTmuxProvider{TmuxStateService: agentintel.NewTmuxStateService()}
}

// TmuxState satisfies TmuxStateProvider. Kept as an explicit adapter (not promoted)
// because it renames State()'s return value and JSON-encodes it for the wire.
func (p *defaultTmuxProvider) TmuxState(ctx context.Context, shellPID int) (json.RawMessage, error) {
	st := p.State(ctx, shellPID)
	return json.Marshal(st)
}

// WithTmuxProvider overrides the default in-process tmux provider.
// Hosts use this to supply a richer snapshot; standalone needs nothing.
func WithTmuxProvider(p TmuxStateProvider) Option {
	return func(s *Server) { s.tmuxProvider = p }
}

// Compile-time capability contract — the SSOT for what defaultTmuxProvider must keep
// satisfying. Any capability interface handleTmux* type-asserts against belongs in this
// list; a future one added here without a matching promoted/adapted method fails the
// build immediately instead of 501ing silently at runtime (see the doc comment above).
var (
	_ TmuxStateProvider   = (*defaultTmuxProvider)(nil)
	_ TmuxCopyMotioner    = (*defaultTmuxProvider)(nil)
	_ TmuxSessionMaker    = (*defaultTmuxProvider)(nil)
	_ TmuxWindowSelector  = (*defaultTmuxProvider)(nil)
	_ TmuxOverviewToggler = (*defaultTmuxProvider)(nil)
	_ TmuxRefresher       = (*defaultTmuxProvider)(nil)
)
