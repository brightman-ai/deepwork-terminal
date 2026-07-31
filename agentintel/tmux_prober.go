package agentintel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const tmuxFieldSep = "\t"

// tmuxClientCacheRetention drops a shell's cached client identity once nothing has asked about it
// for this long — the shell is gone, or is not a tmux one. Far above tmuxProbeTTL so it only ever
// evicts the truly idle; it exists to bound the map, not to control freshness.
const tmuxClientCacheRetention = 5 * time.Minute

// TmuxPane represents a single tmux pane with its process info.
type TmuxPane struct {
	SessionName    string
	SessionWindow  string // "session:window" for capture-pane target
	WindowIndex    int
	WindowName     string
	PaneIndex      int
	PanePID        int
	PaneCWD        string // pane's current working directory
	PaneID         string // stable tmux pane id ("%N") — survives index reuse / window reorder
	WindowID       string // stable tmux window id ("@N") — survives window index reuse / reorder
	Active         bool   // window is the active window in the session
	PaneActive     bool   // this pane is the active pane WITHIN its window (target of a bare session:window capture)
	LastActivityAt int64  // unix timestamp of last pane activity (from tmux)
}

// TmuxClient is the tmux client a shell is attached THROUGH — one identity, resolved once.
//
// It used to be three separate lookups (is there a client? what is its name? what session is it
// on?), each re-deriving the same answer from its own `list-clients` call. They are facets of one
// entity: the tty tmux is talking to on behalf of this shell. Modelling it as such is not tidiness
// — every caller below is on a latency path the user feels (window switch, copy motion, redraw),
// and each redundant lookup was another command queued on a tmux server that serves them one at a
// time.
type TmuxClient struct {
	// PID of the tmux client process found inside the shell's process tree. Zero means the shell
	// is not attached to tmux at all, which is the honest answer to all three questions at once.
	PID int
	// Name is the client's tty — the handle `-t` / `switch-client -c` expect.
	Name string
	// Session is the tmux session this client is currently attached to.
	Session string
}

// Attached reports whether the shell is inside tmux. Reading it off the resolved client keeps
// "is attached" and "which client" from ever disagreeing.
func (c TmuxClient) Attached() bool { return c.PID != 0 }

type clientCacheEntry struct {
	client TmuxClient
	at     time.Time
}

// TmuxProber performs zero-invasion tmux introspection via read-only commands.
type TmuxProber struct {
	inspector *ProcessInspector

	// clientMu guards the per-shell client cache. Client identity only changes when someone
	// attaches or detaches, so a sub-second TTL is generous — while the poll that reads it runs
	// once a second per connection.
	clientMu    sync.Mutex
	clientCache map[int]clientCacheEntry
}

func NewTmuxProber(inspector *ProcessInspector) *TmuxProber {
	return &TmuxProber{inspector: inspector, clientCache: map[int]clientCacheEntry{}}
}

// clientPIDFor finds the tmux client process inside shellPID's tree, off the shared ps snapshot.
//
// This stays SUBPROCESS-FREE on purpose, and the distinction is load-bearing: "is this shell in
// tmux" is asked constantly (every session's status, every signal qualification), while "what is
// that client called" is asked only when something is about to act on it. Answering the cheap
// question through the expensive path — one `tmux list-clients` per call — turned a pure in-memory
// lookup into a fork/exec storm that stalled the whole test suite. Cheap questions get cheap
// answers; see ClientFor for the other half.
func (tp *TmuxProber) clientPIDFor(ctx context.Context, shellPID int) int {
	if shellPID <= 0 {
		return 0
	}
	for _, p := range processTreeIncludingRoot(tp.inspector.processSnapshot(ctx), shellPID) {
		if isTmuxClient(p.Command) {
			return p.PID
		}
	}
	return 0
}

// ClientFor resolves the tmux client shellPID is attached through — THE single lookup behind every
// question that needs the client's IDENTITY, not merely its existence (see TmuxClient).
//
// Two stages, cheapest first:
//  1. clientPIDFor above — no subprocess. No client → done, no tmux command at all;
//  2. ONE `list-clients` returning every field at once, matched by that PID. This replaces the
//     previous shape, where name and session were two separate invocations of the same command
//     that each re-derived the same client.
//
// Memoized per shell for tmuxProbeTTL: identity only changes on attach/detach, while callers ask
// once a second per connection. A result derived under a failing ctx is not cached — see
// topologySnapshot for why serving stale-empty is worse than re-probing.
func (tp *TmuxProber) ClientFor(ctx context.Context, shellPID int) TmuxClient {
	tmuxPID := tp.clientPIDFor(ctx, shellPID)
	if tmuxPID == 0 {
		return TmuxClient{}
	}
	tp.clientMu.Lock()
	if e, ok := tp.clientCache[shellPID]; ok && e.client.PID == tmuxPID && time.Since(e.at) < tmuxProbeTTL {
		tp.clientMu.Unlock()
		return e.client
	}
	tp.clientMu.Unlock()

	client := tp.resolveClientFields(ctx, tmuxPID)
	if ctx.Err() == nil {
		tp.clientMu.Lock()
		tp.clientCache[shellPID] = clientCacheEntry{client: client, at: time.Now()}
		// A workbench opens and closes shells all day; without this the map is a slow leak keyed
		// by dead PIDs. Bounded by "shells that asked recently", pruned on the same clock.
		for pid, e := range tp.clientCache {
			if time.Since(e.at) > tmuxClientCacheRetention {
				delete(tp.clientCache, pid)
			}
		}
		tp.clientMu.Unlock()
	}
	return client
}

func (tp *TmuxProber) resolveClientFields(ctx context.Context, tmuxPID int) TmuxClient {
	out, err := tmuxCommandContext(ctx, "list-clients",
		"-F", "#{client_pid}"+tmuxFieldSep+"#{client_name}"+tmuxFieldSep+"#{session_name}").Output()
	if err != nil {
		// The client process exists, so the shell IS in tmux; only the extra fields are unknown.
		// Saying otherwise would make Attached() flap on a transient command failure.
		return TmuxClient{PID: tmuxPID}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, tmuxFieldSep, 3)
		if len(fields) != 3 {
			continue
		}
		if pid, _ := strconv.Atoi(fields[0]); pid == tmuxPID {
			return TmuxClient{
				PID:     tmuxPID,
				Name:    strings.TrimSpace(fields[1]),
				Session: strings.TrimSpace(fields[2]),
			}
		}
	}
	return TmuxClient{PID: tmuxPID}
}

// DetectTmux checks if a tmux client is running as a child of shellPID. Subprocess-free.
func (tp *TmuxProber) DetectTmux(ctx context.Context, shellPID int) bool {
	return tp.clientPIDFor(ctx, shellPID) != 0
}

// FindClientSession finds which tmux session the CLI session's tmux client is attached to.
func (tp *TmuxProber) FindClientSession(ctx context.Context, shellPID int) string {
	return tp.ClientFor(ctx, shellPID).Session
}

// FindClientName returns the tmux client name (its tty, the handle `switch-client -c` wants)
// for the client in shellPID's child tree, "" when that shell is not attached to tmux.
func (tp *TmuxProber) FindClientName(ctx context.Context, shellPID int) string {
	return tp.ClientFor(ctx, shellPID).Name
}

func processTreeIncludingRoot(procs []ProcessInfo, rootPID int) []ProcessInfo {
	result := make([]ProcessInfo, 0, 1)
	for _, p := range procs {
		if p.PID == rootPID {
			result = append(result, p)
			break
		}
	}
	return append(result, childTree(procs, rootPID)...)
}

// ListPanesForSession returns all panes in the given tmux session.
func (tp *TmuxProber) ListPanesForSession(ctx context.Context, sessionName string) ([]TmuxPane, error) {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return tp.ListPanes(ctx)
	}
	all, err := tp.ListPanes(ctx)
	if err != nil {
		return nil, err
	}
	panes := make([]TmuxPane, 0, len(all))
	for _, pane := range all {
		if strings.TrimSpace(pane.SessionName) == sessionName {
			panes = append(panes, pane)
		}
	}
	return panes, nil
}

// ListPanes returns panes from the tmux server visible to this process.
func (tp *TmuxProber) ListPanes(ctx context.Context) ([]TmuxPane, error) {
	out, err := tmuxCommandContext(ctx,
		"list-panes", "-s",
		"-F", tmuxPaneFormat(),
	).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	return parseTmuxPanes(string(out))
}

// CapturePane reads the last n visible lines of a tmux pane (zero-invasion, read-only). The agent's
// permission / selection / input PROMPT lives in the terminal, not its JSONL transcript, so this is
// the ground-truth source for "blocked waiting for the user" — gated on transcript inactivity so it
// is read only for panes that have stopped producing output (see PaneAgentMonitor).
func (tp *TmuxProber) CapturePane(ctx context.Context, sessionWindow string, paneIdx, lines int) ([]string, error) {
	target := fmt.Sprintf("%s.%d", sessionWindow, paneIdx)
	out, err := tmuxCommandContext(ctx,
		"capture-pane", "-t", target, "-p", "-S", fmt.Sprintf("-%d", lines),
	).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane %s: %w", target, err)
	}
	raw := strings.Split(string(out), "\n")
	for len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	return raw, nil
}

// CaptureWindowTail captures a WINDOW's active-pane live tail for the Agent Overview: the last
// `lines` lines of REAL output after `tool`'s persistent bottom chrome (input box / status /
// token counter, etc.) has been stripped. Unlike CapturePane it takes no pane index — tmux
// resolves a bare "session:window" target to that window's active pane, which is exactly what
// the overview card represents. Works on NON-attached / background windows too (no switch).
//
// It captures the whole VISIBLE screen (no -S), not just N lines: an agent's chrome is ~12
// lines pinned to the bottom, so the content worth showing sits above it and would be lost if we
// grabbed only the last N raw lines first. Stripping (per `tool`) then capping keeps the pushed
// payload — and the diff that decides whether to push at all — as small as the const promises.
func (tp *TmuxProber) CaptureWindowTail(ctx context.Context, sessionWindow string, tool AgentTool, lines int) ([]string, error) {
	out, err := tmuxCommandContext(ctx,
		"capture-pane", "-t", sessionWindow, "-p",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane %s: %w", sessionWindow, err)
	}
	raw := strings.Split(string(out), "\n")
	// Strip the agent's bottom chrome by tool (bare shell / unknown → left as-is), then keep the
	// last N content lines. stripAgentChrome already trims trailing blank padding.
	content := stripAgentChrome(raw, tool)
	if len(content) > lines {
		content = content[len(content)-lines:]
	}
	return content, nil
}

// DetectAgentsInPanes returns a map of pane PID → AgentTool for panes with AI tools.
func (tp *TmuxProber) DetectAgentsInPanes(ctx context.Context, panes []TmuxPane) map[int]AgentTool {
	result := make(map[int]AgentTool)
	for panePID, agent := range tp.DetectAgentProcessesInPanes(ctx, panes) {
		result[panePID] = agent.Tool
	}
	return result
}

// DetectAgentProcessesInPanes preserves the concrete runtime process identity
// needed to bind each pane to its own transcript.
func (tp *TmuxProber) DetectAgentProcessesInPanes(ctx context.Context, panes []TmuxPane) map[int]DetectedAgent {
	result := make(map[int]DetectedAgent)
	for _, pane := range panes {
		if agent := tp.inspector.DetectAgentCtx(ctx, pane.PanePID); agent.Tool != ToolNone {
			result[pane.PanePID] = agent
		}
	}
	return result
}

func isTmuxClient(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "tmux") &&
		!strings.Contains(lower, "tmux-") && // not tmux-related tool
		!strings.Contains(lower, "server") // not tmux server
}

func parseTmuxPanes(out string) ([]TmuxPane, error) {
	var panes []TmuxPane
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, tmuxFieldSep)
		if len(fields) < 6 {
			fields = strings.Fields(line)
		}
		if len(fields) < 6 {
			continue
		}
		var sessionName, sessionWindow, windowName, paneCWD, paneID, windowID string
		var windowIdx, paneIdx, panePID int
		var active, paneActive bool
		var err1, err2, err3 error
		var lastActivity int64

		if strings.Contains(line, tmuxFieldSep) {
			if len(fields) < 7 {
				continue
			}
			sessionName = fields[0]
			windowIdx, err1 = strconv.Atoi(fields[1])
			sessionWindow = fmt.Sprintf("%s:%d", sessionName, windowIdx)
			windowName = fields[2]
			paneIdx, err2 = strconv.Atoi(fields[3])
			panePID, err3 = strconv.Atoi(fields[4])
			active = fields[5] == "1"
			paneCWD = fields[6]
			if len(fields) >= 8 {
				lastActivity, _ = strconv.ParseInt(fields[7], 10, 64)
			}
			if len(fields) >= 9 {
				paneID = fields[8]
			}
			if len(fields) >= 10 {
				windowID = fields[9]
			}
			if len(fields) >= 11 {
				paneActive = fields[10] == "1"
			}
		} else {
			sessionWindow = fields[0]
			sessionName = sessionWindow
			windowStr := sessionWindow
			if idx := strings.LastIndex(sessionWindow, ":"); idx >= 0 {
				sessionName = sessionWindow[:idx]
				windowStr = sessionWindow[idx+1:]
			}
			windowIdx, err1 = strconv.Atoi(windowStr)
			windowName = fields[1]
			paneIdx, err2 = strconv.Atoi(fields[2])
			panePID, err3 = strconv.Atoi(fields[3])
			active = fields[4] == "1"
			paneCWD = fields[5]
			if len(fields) >= 7 {
				lastActivity, _ = strconv.ParseInt(fields[6], 10, 64)
			}
		}
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		panes = append(panes, TmuxPane{
			SessionName:    sessionName,
			SessionWindow:  sessionWindow,
			WindowIndex:    windowIdx,
			WindowName:     windowName,
			PaneIndex:      paneIdx,
			PanePID:        panePID,
			PaneCWD:        paneCWD,
			PaneID:         paneID,
			WindowID:       windowID,
			Active:         active,
			PaneActive:     paneActive,
			LastActivityAt: lastActivity,
		})
	}
	return panes, nil
}

func tmuxPaneFormat() string {
	fields := []string{
		"#{session_name}",
		"#{window_index}",
		"#{window_name}",
		"#{pane_index}",
		"#{pane_pid}",
		"#{window_active}",
		"#{pane_current_path}",
		"#{pane_last_activity}",
		"#{pane_id}",     // stable per-pane id ("%N") — survives index reuse/window reorder
		"#{window_id}",   // stable per-window id ("@N") — the Agent Overview keys seen-state on it
		"#{pane_active}", // this pane is active WITHIN its window — the pane a bare session:window tail targets
	}
	return strings.Join(fields, tmuxFieldSep)
}

// tmuxCommandContext preserves the server socket while dropping current-client
// context, so a portal running inside tmux does not scope probes to itself.
func tmuxCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "tmux", tmuxServerArgs(args...)...)
	cmd.Env = sanitizedTmuxEnv(os.Environ())
	return cmd
}

func tmuxServerArgs(args ...string) []string {
	socket := tmuxSocketFromEnv(os.Getenv("TMUX"))
	if socket == "" {
		return args
	}
	result := make([]string, 0, len(args)+2)
	result = append(result, "-S", socket)
	result = append(result, args...)
	return result
}

func tmuxSocketFromEnv(value string) string {
	socket, _, _ := strings.Cut(value, ",")
	return strings.TrimSpace(socket)
}

func sanitizedTmuxEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "TMUX_PANE=") {
			continue
		}
		result = append(result, entry)
	}
	return result
}
