package agentintel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const tmuxFieldSep = "\t"

// TmuxPane represents a single tmux pane with its process info.
type TmuxPane struct {
	SessionName    string
	SessionWindow  string // "session:window" for capture-pane target
	WindowIndex    int
	WindowName     string
	PaneIndex      int
	PanePID        int
	PaneCWD        string // pane's current working directory
	Active         bool   // window is the active window in the session
	LastActivityAt int64  // unix timestamp of last pane activity (from tmux)
}

// TmuxProber performs zero-invasion tmux introspection via read-only commands.
type TmuxProber struct {
	inspector *ProcessInspector
}

func NewTmuxProber(inspector *ProcessInspector) *TmuxProber {
	return &TmuxProber{inspector: inspector}
}

// DetectTmux checks if tmux client is running as a child of shellPID.
func (tp *TmuxProber) DetectTmux(ctx context.Context, shellPID int) bool {
	procs := tp.inspector.processSnapshot(ctx)
	for _, p := range processTreeIncludingRoot(procs, shellPID) {
		if isTmuxClient(p.Command) {
			return true
		}
	}
	return false
}

// FindClientSession finds which tmux session the CLI session's tmux client is attached to.
func (tp *TmuxProber) FindClientSession(ctx context.Context, shellPID int) string {
	return tp.clientField(ctx, shellPID, "#{session_name}")
}

// FindClientName returns the tmux client name (its tty, the handle `switch-client -c` wants)
// for the client in shellPID's child tree, "" when that shell is not attached to tmux.
func (tp *TmuxProber) FindClientName(ctx context.Context, shellPID int) string {
	return tp.clientField(ctx, shellPID, "#{client_name}")
}

// clientPIDForShell finds the tmux client process PID inside shellPID's child tree (0 if none).
func (tp *TmuxProber) clientPIDForShell(ctx context.Context, shellPID int) int {
	procs := tp.inspector.processSnapshot(ctx)
	for _, p := range processTreeIncludingRoot(procs, shellPID) {
		if isTmuxClient(p.Command) {
			return p.PID
		}
	}
	return 0
}

// clientField resolves one tmux client format field for the client shellPID is attached
// through, by matching the in-tree client PID against tmux list-clients.
func (tp *TmuxProber) clientField(ctx context.Context, shellPID int, field string) string {
	tmuxPID := tp.clientPIDForShell(ctx, shellPID)
	if tmuxPID == 0 {
		return ""
	}
	out, err := tmuxCommandContext(ctx, "list-clients", "-F", "#{client_pid}"+tmuxFieldSep+field).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, tmuxFieldSep, 2)
		if len(fields) != 2 {
			continue
		}
		if pid, _ := strconv.Atoi(fields[0]); pid == tmuxPID {
			return strings.TrimSpace(fields[1])
		}
	}
	return ""
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

// DetectAgentsInPanes returns a map of pane PID → AgentTool for panes with AI tools.
func (tp *TmuxProber) DetectAgentsInPanes(ctx context.Context, panes []TmuxPane) map[int]AgentTool {
	result := make(map[int]AgentTool)
	for _, pane := range panes {
		if t := tp.inspector.DetectToolCtx(ctx, pane.PanePID); t != ToolNone {
			result[pane.PanePID] = t
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
		var sessionName, sessionWindow, windowName, paneCWD string
		var windowIdx, paneIdx, panePID int
		var active bool
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
			Active:         active,
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
