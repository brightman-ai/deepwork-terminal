package agentintel

import (
	"context"
	"sync"
	"time"
)

const (
	defaultAgentSnapshotPoll        = 2 * time.Second
	defaultAgentSnapshotTimeout     = 5 * time.Second
	defaultAgentSnapshotSubscriberB = 8
)

// AgentSnapshotWatcher publishes the same tmux-aware state used by the HTTP
// snapshot endpoint. It is intentionally polling-based because tmux topology
// changes do not emit JSONL/fsnotify events.
type AgentSnapshotWatcher struct {
	sessionID string
	resolver  AgentStateResolver

	poll          time.Duration
	resolveTime   time.Duration
	subscriberBuf int

	mu          sync.RWMutex
	subscribers map[uint64]chan AgentIntelResponse
	nextSubID   uint64

	lastFingerprint string
	lastResponse    AgentIntelResponse

	cancel    context.CancelFunc
	done      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

func NewAgentSnapshotWatcher(sessionID string, resolver AgentStateResolver) *AgentSnapshotWatcher {
	return &AgentSnapshotWatcher{
		sessionID:     sessionID,
		resolver:      resolver,
		poll:          defaultAgentSnapshotPoll,
		resolveTime:   defaultAgentSnapshotTimeout,
		subscriberBuf: defaultAgentSnapshotSubscriberB,
		subscribers:   make(map[uint64]chan AgentIntelResponse),
		done:          make(chan struct{}),
	}
}

func (w *AgentSnapshotWatcher) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	w.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		w.cancel = cancel
		go w.run(ctx)
	})
}

func (w *AgentSnapshotWatcher) Stop() {
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
			<-w.done
		}
		w.mu.Lock()
		for id, ch := range w.subscribers {
			close(ch)
			delete(w.subscribers, id)
			AgentIntelSubscribersActive.Sub(1)
		}
		w.mu.Unlock()
	})
}

func (w *AgentSnapshotWatcher) subscribe() (<-chan AgentIntelResponse, func()) {
	ch := make(chan AgentIntelResponse, w.subscriberBuf)

	w.mu.Lock()
	w.nextSubID++
	id := w.nextSubID
	w.subscribers[id] = ch
	resp := cloneAgentIntelResponse(w.lastResponse)
	hasSnapshot := w.lastFingerprint != ""
	w.mu.Unlock()

	AgentIntelSubscribersActive.Add(1)

	if hasSnapshot {
		select {
		case ch <- resp:
		default:
		}
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			w.mu.Lock()
			if cur, ok := w.subscribers[id]; ok {
				close(cur)
				delete(w.subscribers, id)
				AgentIntelSubscribersActive.Sub(1)
			}
			w.mu.Unlock()
		})
	}
	return ch, release
}

func (w *AgentSnapshotWatcher) run(ctx context.Context) {
	defer close(w.done)

	w.publishIfChanged(ctx)

	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.publishIfChanged(ctx)
		}
	}
}

func (w *AgentSnapshotWatcher) publishIfChanged(ctx context.Context) {
	if w.resolver == nil {
		return
	}

	resolveCtx, cancel := context.WithTimeout(ctx, w.resolveTime)
	resp, err := w.resolver(resolveCtx, w.sessionID)
	cancel()
	if err != nil {
		AgentIntelWatcherErrors.Inc()
		Logger.Debug(ctx, "agent snapshot resolve failed", "session_id", w.sessionID, "error", err)
		return
	}

	fp := fingerprintAgentIntelResponse(resp)
	w.mu.Lock()
	if fp == w.lastFingerprint {
		w.mu.Unlock()
		return
	}
	w.lastFingerprint = fp
	w.lastResponse = cloneAgentIntelResponse(resp)
	subscriberCount := len(w.subscribers)
	for _, ch := range w.subscribers {
		select {
		case ch <- cloneAgentIntelResponse(resp):
			AgentIntelStatePushTotal.Inc()
		default:
			AgentIntelStateDropTotal.Inc()
		}
	}
	w.mu.Unlock()

	DetectTotal.Inc()
	Logger.Debug(ctx, "agent snapshot pushed",
		"session_id", w.sessionID, "subscribers", subscriberCount)
}
