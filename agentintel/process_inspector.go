package agentintel

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcessInfo holds parsed fields from ps output.
type ProcessInfo struct {
	PID     int
	PPID    int
	PGID    int
	Command string
}

// ProcessInspector detects AI CLI tools in the process tree.
// Uses a shared snapshot cache (3s TTL) so multiple sidecars don't
// each fork their own ps process.
type ProcessInspector struct {
	mu       sync.Mutex
	cache    []ProcessInfo
	cacheAt  time.Time
	cacheTTL time.Duration
}

func NewProcessInspector() *ProcessInspector {
	return &ProcessInspector{cacheTTL: 3 * time.Second}
}

// SharedProcessInspector is a global singleton so all sidecars
// share one ps snapshot instead of each forking their own.
var SharedProcessInspector = NewProcessInspector()

// DetectTool finds the AI CLI tool running under shellPID.
// Returns ToolNone if no known tool is found.
// The context controls the subprocess timeout (ps command).
func (pi *ProcessInspector) DetectTool(shellPID int) AgentTool {
	return pi.DetectToolCtx(context.Background(), shellPID)
}

// DetectToolCtx is the context-aware variant of DetectTool.
func (pi *ProcessInspector) DetectToolCtx(ctx context.Context, shellPID int) AgentTool {
	procs := pi.processSnapshot(ctx)
	children := childTree(procs, shellPID)

	// Walk deepest first (most specific child process wins).
	var found AgentTool
	for _, p := range children {
		if t := toolFromCommand(p.Command); t != ToolNone {
			found = t
		}
	}
	return found
}

// HasActiveCommand checks if the shell has any non-shell child process running.
func (pi *ProcessInspector) HasActiveCommand(shellPID int) bool {
	procs := pi.processSnapshot(context.Background())
	children := childTree(procs, shellPID)
	return len(children) > 0
}

// processSnapshot returns a cached process list, refreshing at most once per cacheTTL.
// The context controls the subprocess timeout; on cancellation, returns stale cache.
func (pi *ProcessInspector) processSnapshot(ctx context.Context) []ProcessInfo {
	pi.mu.Lock()
	defer pi.mu.Unlock()

	if time.Since(pi.cacheAt) < pi.cacheTTL && pi.cache != nil {
		return pi.cache
	}

	// -wwaxo (NOT -ewwaxo): the `a`/`x` flags already select every process, while
	// the BSD `-e` flag additionally appends each process's full environment to the
	// command= column. That env pollution silently breaks command matching — e.g.
	// a `tmux` client carries TERM=tmux-256color/ZSH_TMUX_TERM in its environment,
	// which made isTmuxClient's "tmux-" guard reject a genuine client. Argv only.
	out, err := exec.CommandContext(ctx, "ps", "-wwaxo", "pid=,ppid=,pgid=,command=").Output()
	if err != nil {
		return pi.cache // stale is better than nil (includes context cancellation)
	}
	pi.cache = parsePS(out)
	pi.cacheAt = time.Now()
	return pi.cache
}

func parsePS(out []byte) []ProcessInfo {
	var procs []ProcessInfo
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		pgid, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		procs = append(procs, ProcessInfo{
			PID:     pid,
			PPID:    ppid,
			PGID:    pgid,
			Command: strings.Join(fields[3:], " "),
		})
	}
	return procs
}

// childTree returns all descendants of rootPID (BFS order, shallowest first).
func childTree(procs []ProcessInfo, rootPID int) []ProcessInfo {
	// Build parent→children index.
	byPPID := make(map[int][]ProcessInfo, len(procs))
	for _, p := range procs {
		byPPID[p.PPID] = append(byPPID[p.PPID], p)
	}

	var result []ProcessInfo
	queue := []int{rootPID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range byPPID[cur] {
			if child.PID == rootPID {
				continue // guard against cycles
			}
			result = append(result, child)
			queue = append(queue, child.PID)
		}
	}
	return result
}

// toolFromCommand returns the AgentTool for a process command string.
// Skips wrapper scripts (command contains "wrapper").
func toolFromCommand(cmd string) AgentTool {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "wrapper") {
		return ToolNone
	}

	// Check each known tool name against the command string.
	for _, candidate := range []struct {
		name string
		tool AgentTool
	}{
		{"claude", ToolClaude},
		{"codex", ToolCodex},
		{"gemini", ToolGemini},
		{"opencode", ToolOpenCode},
	} {
		if matchesToolName(lower, candidate.name) {
			return candidate.tool
		}
	}
	return ToolNone
}

// matchesToolName checks if cmd contains the tool name as a standalone word.
// Matches: "claude", "/usr/bin/claude", "claude --flag", "node /path/claude".
func matchesToolName(cmd, name string) bool {
	// Exact match: command IS the tool name.
	if cmd == name {
		return true
	}
	// Path suffix: /claude or /claude-code
	if strings.Contains(cmd, "/"+name) {
		return true
	}
	// Starts with tool name + space (e.g., "claude --help")
	if strings.HasPrefix(cmd, name+" ") {
		return true
	}
	// Contains tool name surrounded by spaces (e.g., "node claude --flag")
	if strings.Contains(cmd, " "+name+" ") || strings.HasSuffix(cmd, " "+name) {
		return true
	}
	return false
}

