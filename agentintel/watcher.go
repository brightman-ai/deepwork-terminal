package agentintel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultAgentStateDebounce      = 100 * time.Millisecond
	defaultAgentStateRediscover    = 30 * time.Second
	defaultAgentStateToolUseStale  = 3 * time.Second
	defaultAgentStatePTYIdleProbe  = 5 * time.Second
	defaultAgentStateSubscriberBuf = 8
)

// AgentStateWatcher tails one CLI session's JSONL transcript and fans out
// derived state changes to subscribers.
type AgentStateWatcher struct {
	sessionID string
	cwd       string
	tool      AgentTool
	locator   *ProjectLocator

	debounce      time.Duration
	rediscover    time.Duration
	toolUseStale  time.Duration
	ptyIdleProbe  time.Duration
	subscriberBuf int

	ptySource PTYIdleSource // optional; nil disables PTY idle probing

	mu          sync.RWMutex
	subscribers map[uint64]chan AgentIntelResponse
	nextSubID   uint64

	currentPath string
	watchedDir  string
	claude      *ClaudeDriver
	codex       *CodexDriver

	lastFingerprint string
	lastResponse    AgentIntelResponse

	cancel    context.CancelFunc
	done      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewAgentStateWatcher creates a watcher for one CLI session. Use Start before
// expecting fsnotify delivery; Subscribe can be called before or after Start.
func NewAgentStateWatcher(sessionID, cwd string, tool AgentTool, locator *ProjectLocator) *AgentStateWatcher {
	if locator == nil {
		locator = NewProjectLocator()
	}
	return &AgentStateWatcher{
		sessionID:       sessionID,
		cwd:             cwd,
		tool:            tool,
		locator:         locator,
		debounce:        defaultAgentStateDebounce,
		rediscover:      defaultAgentStateRediscover,
		toolUseStale:    defaultAgentStateToolUseStale,
		ptyIdleProbe:    defaultAgentStatePTYIdleProbe,
		subscriberBuf:   defaultAgentStateSubscriberBuf,
		subscribers:     make(map[uint64]chan AgentIntelResponse),
		done:            make(chan struct{}),
		lastResponse:    AgentIntelResponse{},
		lastFingerprint: fingerprintAgentIntelResponse(AgentIntelResponse{}),
	}
}

// SetPTYIdleSource injects a PTY activity source for idle detection.
// Must be called before Start. When set, the watcher probes terminal activity
// to override "running" state when the PTY has been silent.
func (w *AgentStateWatcher) SetPTYIdleSource(src PTYIdleSource) {
	w.ptySource = src
}

// Start begins the fsnotify loop.
func (w *AgentStateWatcher) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	w.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		w.cancel = cancel
		go w.run(ctx)
	})
}

// Stop terminates the watcher and closes all subscriber channels.
func (w *AgentStateWatcher) Stop() {
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

func (w *AgentStateWatcher) subscribe() (<-chan AgentIntelResponse, func()) {
	ch := make(chan AgentIntelResponse, w.subscriberBuf)

	w.mu.Lock()
	w.nextSubID++
	id := w.nextSubID
	w.subscribers[id] = ch
	resp := w.lastResponse
	hasSnapshot := w.lastFingerprint != ""
	w.mu.Unlock()

	AgentIntelSubscribersActive.Add(1)

	if hasSnapshot {
		select {
		case ch <- cloneAgentIntelResponse(resp):
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

func (w *AgentStateWatcher) run(ctx context.Context) {
	defer close(w.done)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		AgentIntelWatcherErrors.Inc()
		Logger.Error(ctx, "agent-state fsnotify init failed", "session_id", w.sessionID, "error", err)
		return
	}
	defer fsw.Close()

	w.refresh(ctx, fsw)

	checkTicker := time.NewTicker(w.rediscover)
	defer checkTicker.Stop()

	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	var toolUseTimer *time.Timer
	var toolUseC <-chan time.Time
	var ptyIdleTimer *time.Timer
	var ptyIdleC <-chan time.Time
	resetDebounce := func() {
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(w.debounce)
		} else {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(w.debounce)
		}
		debounceC = debounceTimer.C
	}
	stopDebounce := func() {
		if debounceTimer != nil {
			if !debounceTimer.Stop() {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
		}
		debounceC = nil
	}
	resetToolUseTimer := func(wait time.Duration) {
		if wait <= 0 {
			wait = time.Millisecond
		}
		if toolUseTimer == nil {
			toolUseTimer = time.NewTimer(wait)
		} else {
			if !toolUseTimer.Stop() {
				select {
				case <-toolUseTimer.C:
				default:
				}
			}
			toolUseTimer.Reset(wait)
		}
		toolUseC = toolUseTimer.C
	}
	stopToolUseTimer := func() {
		if toolUseTimer != nil {
			if !toolUseTimer.Stop() {
				select {
				case <-toolUseTimer.C:
				default:
				}
			}
		}
		toolUseC = nil
	}
	scheduleToolUse := func() {
		wait, ok := w.toolUseWait()
		if ok {
			resetToolUseTimer(wait)
			return
		}
		stopToolUseTimer()
	}
	scheduleToolUse()

	// PTY idle probe: only active when ptySource is set AND last state is "running".
	resetPTYIdleTimer := func() {
		if w.ptySource == nil {
			return
		}
		if ptyIdleTimer == nil {
			ptyIdleTimer = time.NewTimer(w.ptyIdleProbe)
		} else {
			if !ptyIdleTimer.Stop() {
				select {
				case <-ptyIdleTimer.C:
				default:
				}
			}
			ptyIdleTimer.Reset(w.ptyIdleProbe)
		}
		ptyIdleC = ptyIdleTimer.C
	}
	stopPTYIdleTimer := func() {
		if ptyIdleTimer != nil {
			if !ptyIdleTimer.Stop() {
				select {
				case <-ptyIdleTimer.C:
				default:
				}
			}
		}
		ptyIdleC = nil
	}
	schedulePTYIdle := func() {
		if w.ptySource == nil {
			return
		}
		if w.lastStateIsRunning() {
			resetPTYIdleTimer()
		} else {
			stopPTYIdleTimer()
		}
	}
	schedulePTYIdle()

	for {
		select {
		case <-ctx.Done():
			stopDebounce()
			stopToolUseTimer()
			stopPTYIdleTimer()
			return
		case event, ok := <-fsw.Events:
			if !ok {
				return
			}
			if w.shouldHandleEvent(event) {
				resetDebounce()
			}
		case err, ok := <-fsw.Errors:
			if !ok {
				return
			}
			AgentIntelWatcherErrors.Inc()
			Logger.Warn(ctx, "agent-state fsnotify error", "session_id", w.sessionID, "error", err)
		case <-checkTicker.C:
			if w.needsRediscovery() {
				w.refresh(ctx, fsw)
				scheduleToolUse()
			}
			// Always re-evaluate state on the 30s tick to catch staleness
			// transitions (e.g., running/waiting → done after JSONL goes cold).
			w.publishIfChanged(ctx)
			schedulePTYIdle()
		case <-debounceC:
			debounceC = nil
			w.refresh(ctx, fsw)
			scheduleToolUse()
			schedulePTYIdle()
		case <-toolUseC:
			toolUseC = nil
			w.publishIfChanged(ctx)
			scheduleToolUse()
			schedulePTYIdle()
		case <-ptyIdleC:
			ptyIdleC = nil
			w.publishIfChanged(ctx)
			schedulePTYIdle()
		}
	}
}

func (w *AgentStateWatcher) refresh(ctx context.Context, fsw *fsnotify.Watcher) {
	path, tool, err := w.discoverJSONL()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		AgentIntelWatcherErrors.Inc()
		Logger.Debug(ctx, "agent-state JSONL discovery failed",
			"session_id", w.sessionID, "tool", string(w.tool), "error", err)
	}
	if path == "" {
		w.clearCurrentPath()
		w.publishIfChanged(ctx)
		return
	}

	w.ensurePath(path, tool)
	w.ensureWatchDir(ctx, fsw, filepath.Dir(path))
	if err := w.updateDriver(); err != nil {
		AgentIntelWatcherErrors.Inc()
		Logger.Warn(ctx, "agent-state JSONL update failed",
			"session_id", w.sessionID, "path", path, "error", err)
		return
	}
	w.publishIfChanged(ctx)
}

func (w *AgentStateWatcher) discoverJSONL() (string, AgentTool, error) {
	switch w.tool {
	case ToolClaude:
		path, err := w.latestClaudeJSONL()
		return path, ToolClaude, err
	case ToolCodex:
		path, err := w.locator.CodexLatestSession(w.cwd)
		return path, ToolCodex, err
	default:
		path, err := w.latestClaudeJSONL()
		if path != "" || err == nil {
			return path, ToolClaude, err
		}
		path, cerr := w.locator.CodexLatestSession(w.cwd)
		return path, ToolCodex, cerr
	}
}

func (w *AgentStateWatcher) latestClaudeJSONL() (string, error) {
	files, err := w.locator.ClaudeSessionFiles(w.cwd)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", os.ErrNotExist
	}
	return files[0], nil
}

func (w *AgentStateWatcher) ensurePath(path string, tool AgentTool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if path == w.currentPath && tool == w.tool {
		return
	}
	w.currentPath = path
	w.tool = tool
	w.claude = nil
	w.codex = nil
	switch tool {
	case ToolClaude:
		w.claude = NewClaudeDriver(path, w.sessionID)
	case ToolCodex:
		w.codex = NewCodexDriver(path)
	}
}

func (w *AgentStateWatcher) clearCurrentPath() {
	w.mu.Lock()
	w.currentPath = ""
	w.claude = nil
	w.codex = nil
	w.mu.Unlock()
}

func (w *AgentStateWatcher) ensureWatchDir(ctx context.Context, fsw *fsnotify.Watcher, dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if dir == "" || dir == w.watchedDir {
		return
	}
	if w.watchedDir != "" {
		_ = fsw.Remove(w.watchedDir)
	}
	if err := fsw.Add(dir); err != nil {
		AgentIntelWatcherErrors.Inc()
		Logger.Warn(ctx, "agent-state fsnotify watch failed",
			"session_id", w.sessionID, "dir", dir, "error", err)
		return
	}
	w.watchedDir = dir
}

func (w *AgentStateWatcher) updateDriver() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.tool {
	case ToolClaude:
		if w.claude == nil && w.currentPath != "" {
			w.claude = NewClaudeDriver(w.currentPath, w.sessionID)
		}
		if w.claude != nil {
			return w.claude.Update()
		}
	case ToolCodex:
		if w.codex == nil && w.currentPath != "" {
			w.codex = NewCodexDriver(w.currentPath)
		}
		if w.codex != nil {
			return w.codex.Update()
		}
	}
	return nil
}

func (w *AgentStateWatcher) publishIfChanged(ctx context.Context) {
	resp := w.currentResponse()
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
	Logger.Debug(ctx, "agent-state pushed",
		"session_id", w.sessionID, "tool", string(w.tool), "subscribers", subscriberCount)
}

func (w *AgentStateWatcher) currentResponse() AgentIntelResponse {
	// Copy state under lock, then release before I/O (os.Stat, PTY probe).
	// Avoids nested RLock deadlock (isJSONLFresh also needs the path).
	w.mu.RLock()
	currentPath := w.currentPath
	tool := w.tool
	var state AgentState
	switch tool {
	case ToolClaude:
		if w.claude != nil {
			state = w.claude.AgentState()
		}
	case ToolCodex:
		if w.codex != nil {
			state = w.codex.AgentState()
		}
	}
	w.mu.RUnlock()

	if currentPath == "" || (state.Tool == "" && state.Status == "") {
		return AgentIntelResponse{}
	}

	state.SignalSource = "jsonl"
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}

	// Staleness gate: if JSONL file hasn't been WRITTEN in >2 minutes, the agent
	// is very likely not running. Use file modtime (not state.UpdatedAt, which
	// drivers may set to time.Now() during parsing).
	// Only override "running" — preserve "waiting" (user may have left a [Y/n]
	// prompt open for >2min, that's still meaningful).
	jsonlFresh := jsonlFileIsFresh(currentPath)
	if !jsonlFresh && state.Status == StatusRunning {
		state.Status = StatusDone
		state.WaitReason = WaitNone
		state.SignalSource = "stale"
	}

	// PTY idle enrichment: only for fresh JSONL (active agent).
	// Same override logic as the HTTP snapshot handler.
	if w.ptySource != nil && jsonlFresh {
		// Output analysis: detect prompts/permission from terminal lines.
		if lines := w.ptySource.TailLines(8); len(lines) > 0 {
			switch AnalyzeOutput(lines) {
			case PromptNeedsPermission:
				state.Status = StatusWaiting
				state.WaitReason = WaitPermission
				state.SignalSource = "output"
			case PromptIdle, PromptLikelyIdle:
				if state.Status == StatusRunning {
					state.Status = StatusIdle
					state.WaitReason = WaitPrompt
					state.SignalSource = "output"
				}
			}
		}

		// PTY idle override: JSONL says running but terminal silent >5s → waiting.
		if state.Status == StatusRunning {
			if lastActive := w.ptySource.LastActivity(); !lastActive.IsZero() {
				if time.Since(lastActive) > defaultPTYIdleThreshold {
					state.Status = StatusWaiting
					state.WaitReason = WaitPrompt
					state.SignalSource = "pty_idle"
				}
			}
		}
	}

	current := state
	return AgentIntelResponse{
		Current:       &current,
		Notifications: []AgentState{state},
	}
}

// jsonlFileIsFresh returns true if the given file was modified within the last 2 minutes.
func jsonlFileIsFresh(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < 2*time.Minute
}

// lastStateIsRunning returns true if the most recently published state was "running".
func (w *AgentStateWatcher) lastStateIsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.lastResponse.Current == nil {
		return false
	}
	return w.lastResponse.Current.Status == StatusRunning
}

func (w *AgentStateWatcher) shouldHandleEvent(event fsnotify.Event) bool {
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove|fsnotify.Chmod) == 0 {
		return false
	}
	name := filepath.Clean(event.Name)

	w.mu.RLock()
	currentPath := filepath.Clean(w.currentPath)
	watchedDir := filepath.Clean(w.watchedDir)
	w.mu.RUnlock()

	if currentPath != "" && samePath(name, currentPath) {
		return true
	}
	return watchedDir != "" &&
		samePath(filepath.Dir(name), watchedDir) &&
		strings.HasSuffix(strings.ToLower(name), ".jsonl")
}

func (w *AgentStateWatcher) needsRediscovery() bool {
	w.mu.RLock()
	path := w.currentPath
	w.mu.RUnlock()
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return err != nil
}

func (w *AgentStateWatcher) toolUseWait() (time.Duration, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.tool != ToolClaude || w.claude == nil {
		return 0, false
	}
	s := w.claude.State()
	if s.StopReason != "tool_use" || s.UpdatedAt.IsZero() {
		return 0, false
	}
	if s.Status == StatusWaiting && s.WaitReason == WaitPermission {
		return 0, false
	}
	return w.toolUseStale - time.Since(s.UpdatedAt), true
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	return strings.EqualFold(a, b)
}

func fingerprintAgentIntelResponse(resp AgentIntelResponse) string {
	type stateFP struct {
		Tool              AgentTool   `json:"tool,omitempty"`
		Status            AgentStatus `json:"status,omitempty"`
		WaitReason        WaitReason  `json:"waitReason,omitempty"`
		Model             string      `json:"model,omitempty"`
		InputTokens       int         `json:"inputTokens,omitempty"`
		OutputTokens      int         `json:"outputTokens,omitempty"`
		CacheReadTokens   int         `json:"cacheReadTokens,omitempty"`
		CacheCreateTokens int         `json:"cacheCreateTokens,omitempty"`
		TotalTokens       int         `json:"totalTokens,omitempty"`
		TmuxWindow        *int        `json:"tmuxWindow,omitempty"`
		TmuxPane          *int        `json:"tmuxPane,omitempty"`
	}
	type responseFP struct {
		Current       *stateFP  `json:"current,omitempty"`
		Notifications []stateFP `json:"notifications,omitempty"`
	}
	convert := func(s AgentState) stateFP {
		return stateFP{
			Tool:              s.Tool,
			Status:            s.Status,
			WaitReason:        s.WaitReason,
			Model:             s.Model,
			InputTokens:       s.InputTokens,
			OutputTokens:      s.OutputTokens,
			CacheReadTokens:   s.CacheReadTokens,
			CacheCreateTokens: s.CacheCreateTokens,
			TotalTokens:       s.TotalTokens,
			TmuxWindow:        s.TmuxWindow,
			TmuxPane:          s.TmuxPane,
		}
	}
	fp := responseFP{}
	if resp.Current != nil {
		cur := convert(*resp.Current)
		fp.Current = &cur
	}
	if len(resp.Notifications) > 0 {
		fp.Notifications = make([]stateFP, 0, len(resp.Notifications))
		for _, s := range resp.Notifications {
			fp.Notifications = append(fp.Notifications, convert(s))
		}
	}
	b, _ := json.Marshal(fp)
	return string(b)
}

func cloneAgentIntelResponse(resp AgentIntelResponse) AgentIntelResponse {
	out := AgentIntelResponse{
		Notifications: append([]AgentState(nil), resp.Notifications...),
	}
	if resp.Current != nil {
		cur := *resp.Current
		out.Current = &cur
	}
	return out
}
