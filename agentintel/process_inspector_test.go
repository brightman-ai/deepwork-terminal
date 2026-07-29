package agentintel

import (
	"testing"
)

func TestToolFromCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want AgentTool
	}{
		{"/usr/local/bin/claude", ToolClaude},
		{"claude --dangerously-skip-permissions", ToolClaude},
		{"/home/user/.nvm/bin/codex", ToolCodex},
		{"codex run", ToolCodex},
		{"/opt/codex-code-mode-host", ToolNone},
		{"/tmp/codex-helper", ToolNone},
		{"/tmp/claude-1001/project/worker", ToolNone},
		{"/usr/bin/gemini", ToolGemini},
		{"gemini --model pro", ToolGemini},
		{"/usr/local/bin/opencode", ToolOpenCode},
		{"opencode --dir /tmp", ToolOpenCode},
		{"/bin/bash tool-wrapper.sh claude", ToolNone}, // wrapper skipped
		{"/bin/zsh", ToolNone},
		{"node /home/user/.nvm/bin/claude-wrapper.sh", ToolNone}, // wrapper skipped
		{"python3 script.py", ToolNone},
	}

	for _, tc := range cases {
		got := toolFromCommand(tc.cmd)
		if got != tc.want {
			t.Errorf("toolFromCommand(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

// TestDetectAgentInTreeIgnoresSelfUpdater is a REAL case, not a hypothetical: `bun install
// -g @openai/codex` is the exact command Codex's own binary runs to update itself (the
// string is compiled into it, and it is what the user saw on screen the night a
// self-updating pane was misread). filepath.Base of that package spec is "codex", so the
// installer used to be detected as the agent — and being DEEPER in the tree it displaced the
// real codex process, which unbinds the pane's transcript for the whole update.
func TestDetectAgentInTreeIgnoresSelfUpdater(t *testing.T) {
	procs := []ProcessInfo{
		{PID: 100, PPID: 1, Command: "-zsh"},
		{PID: 200, PPID: 100, Command: "bun /home/user/.bun/bin/codex --yolo"},
		{PID: 300, PPID: 200, Command: "bun install -g @openai/codex"},
	}
	got := detectAgentInTree(procs, 100)
	if got.Tool != ToolCodex || got.ProcessPID != 200 {
		t.Fatalf("detected agent = %+v, want the real codex pid 200 (not its installer)", got)
	}
	// The installer's own command line, standing alone, is not an agent either.
	if tool := toolFromCommand("bun install -g @openai/codex"); tool != ToolNone {
		t.Fatalf("a package install line was detected as %q", tool)
	}
	if tool := toolFromCommand("npm install -g @anthropic-ai/claude-code"); tool != ToolNone {
		t.Fatalf("a package install line was detected as %q", tool)
	}
}

func TestDetectAgentInTreeIgnoresCodexHelperProcess(t *testing.T) {
	procs := []ProcessInfo{
		{PID: 100, PPID: 1, Command: "zsh"},
		{PID: 200, PPID: 100, Command: "/home/user/.bun/bin/bun /home/user/.bun/bin/codex --yolo"},
		{PID: 201, PPID: 200, Command: "/opt/openai/bin/codex --yolo"},
		{PID: 202, PPID: 201, Command: "/opt/openai/bin/codex-code-mode-host"},
	}

	got := detectAgentInTree(procs, 100)
	if got.Tool != ToolCodex || got.ProcessPID != 201 {
		t.Fatalf("detected agent = %+v, want native Codex pid 201", got)
	}
}

func TestChildTree(t *testing.T) {
	procs := []ProcessInfo{
		{PID: 1, PPID: 0, Command: "init"},
		{PID: 100, PPID: 1, Command: "bash"},
		{PID: 101, PPID: 100, Command: "claude --dangerously-skip-permissions"},
		{PID: 102, PPID: 101, Command: "node worker"},
	}

	children := childTree(procs, 100)
	if len(children) != 2 {
		t.Fatalf("expected 2 children of pid 100, got %d", len(children))
	}
	if children[0].PID != 101 {
		t.Errorf("expected first child PID 101, got %d", children[0].PID)
	}
	if children[1].PID != 102 {
		t.Errorf("expected second child PID 102, got %d", children[1].PID)
	}
}

func TestDetectTool_mockSnapshot(t *testing.T) {
	procs := []ProcessInfo{
		{PID: 200, PPID: 0, Command: "bash"},
		{PID: 201, PPID: 200, Command: "/usr/local/bin/claude"},
	}

	pi := &ProcessInspector{}

	// Override processSnapshot via helper (uses childTree + toolFromCommand directly).
	children := childTree(procs, 200)
	var found AgentTool
	for _, p := range children {
		if t2 := toolFromCommand(p.Command); t2 != ToolNone {
			found = t2
		}
	}
	_ = pi // ensure pi is used (satisfies compiler)

	if found != ToolClaude {
		t.Errorf("expected ToolClaude, got %q", found)
	}
}

func TestParsePS(t *testing.T) {
	raw := []byte(
		"  1     0     0 /sbin/init\n" +
			"100     1     1 bash\n" +
			"101   100   100 claude --dangerously-skip-permissions\n",
	)
	procs := parsePS(raw)
	if len(procs) != 3 {
		t.Fatalf("expected 3 processes, got %d", len(procs))
	}
	if procs[2].Command != "claude --dangerously-skip-permissions" {
		t.Errorf("unexpected command: %q", procs[2].Command)
	}
}
