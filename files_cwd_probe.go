package terminal

import (
	"context"
	"encoding/json"
	"time"
)

type workbenchCWDProbe struct {
	ready chan struct{}
	at    time.Time
	raw   json.RawMessage
	err   error
}

// File browsing cannot queue behind a slow global tmux topology probe. Share one
// in-flight lookup per shell, wait briefly, then use the live shell cwd. The
// provider owns completed-state freshness; cancellation never creates a
// new batch of goroutines behind the same provider lock.
func (s *Server) workbenchTmuxState(ctx context.Context, pid int) (json.RawMessage, error) {
	s.workbenchCWDMu.Lock()
	if s.workbenchCWDProbes == nil {
		s.workbenchCWDProbes = make(map[int]*workbenchCWDProbe)
	}
	probe := s.workbenchCWDProbes[pid]
	if probe != nil {
		select {
		case <-probe.ready:
			probe = nil
		default:
		}
	}
	if probe == nil {
		// Session ids/pids churn on long-lived servers. Completed probes need not be
		// retained forever; an in-flight probe remains shared until it has answered.
		for id, p := range s.workbenchCWDProbes {
			select {
			case <-p.ready:
				if time.Since(p.at) > time.Second {
					delete(s.workbenchCWDProbes, id)
				}
			default:
			}
		}
		probe = &workbenchCWDProbe{ready: make(chan struct{})}
		s.workbenchCWDProbes[pid] = probe
		go func(p *workbenchCWDProbe) {
			base := s.watchCtx
			if base == nil {
				base = context.Background()
			}
			probeCtx, cancel := context.WithTimeout(base, 200*time.Millisecond)
			defer cancel()
			p.raw, p.err = s.tmuxProvider.TmuxState(probeCtx, pid)
			p.at = time.Now()
			close(p.ready)
		}(probe)
	}
	s.workbenchCWDMu.Unlock()
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-probe.ready:
		return probe.raw, probe.err
	case <-timer.C:
		return nil, context.DeadlineExceeded
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
