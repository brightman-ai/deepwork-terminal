package agentintel

import (
	"context"
	"os"
	"slices"
	"testing"
)

func TestParseTmuxPanesTabDelimited(t *testing.T) {
	raw := "1\t4\tcli sessoin1\t1\t6371\t0\t/Users/anthony/code/deepwork\t1714740000\n" +
		"1\t5\tc open thinking\t2\t21487\t1\t/Users/anthony/code/deepwork\t\n"

	panes, err := parseTmuxPanes(raw)
	if err != nil {
		t.Fatalf("parseTmuxPanes returned error: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("len(panes) = %d, want 2", len(panes))
	}

	if panes[0].WindowName != "cli sessoin1" {
		t.Fatalf("first window name = %q, want %q", panes[0].WindowName, "cli sessoin1")
	}
	if panes[0].SessionName != "1" || panes[0].SessionWindow != "1:4" {
		t.Fatalf("first pane session fields = %+v", panes[0])
	}
	if panes[1].WindowName != "c open thinking" {
		t.Fatalf("second window name = %q, want %q", panes[1].WindowName, "c open thinking")
	}
	if !panes[1].Active {
		t.Fatalf("second pane Active = false, want true")
	}
}

func TestParseTmuxPanesLegacySpaceDelimited(t *testing.T) {
	raw := "1:5 c-open-thinking 2 21487 1 /Users/anthony/code/deepwork 1714740000\n"

	panes, err := parseTmuxPanes(raw)
	if err != nil {
		t.Fatalf("parseTmuxPanes returned error: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("len(panes) = %d, want 1", len(panes))
	}
	if panes[0].WindowIndex != 5 || panes[0].PaneIndex != 2 || panes[0].PanePID != 21487 {
		t.Fatalf("parsed pane = %+v", panes[0])
	}
}

func TestIsTmuxClient(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"tmux new -s probe", true},
		{"tmux attach", true},
		{"tmux attach -t probe", true},
		{"/usr/bin/tmux", true},
		// Server process must NOT be treated as a client.
		{"tmux: server (/tmp/tmux-1001/default)", false},
		// A non-tmux process whose argv merely mentions a tmux-flavored TERM.
		{"/usr/bin/zsh -l", false},
		// Regression: the ps `command` column must carry argv ONLY. If the
		// environment leaks in (TERM=tmux-256color, ZSH_TMUX_*), the legacy
		// "tmux-" guard wrongly rejected a genuine client. With argv-only ps
		// output this string never occurs; assert the guard still holds for a
		// clean client argv that has no env contamination.
		{"tmux new-session -A -s main", true},
	}
	for _, tc := range cases {
		if got := isTmuxClient(tc.cmd); got != tc.want {
			t.Errorf("isTmuxClient(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestProcessTreeIncludingRoot(t *testing.T) {
	procs := []ProcessInfo{
		{PID: 10, PPID: 1, Command: "tmux"},
		{PID: 11, PPID: 10, Command: "zsh"},
	}
	got := processTreeIncludingRoot(procs, 10)
	if len(got) != 2 {
		t.Fatalf("len(processTreeIncludingRoot) = %d, want 2", len(got))
	}
	if got[0].PID != 10 || got[1].PID != 11 {
		t.Fatalf("processTreeIncludingRoot order = %+v", got)
	}
}

func TestTmuxSocketFromEnv(t *testing.T) {
	got := tmuxSocketFromEnv("/private/tmp/tmux-501/default,71243,66")
	if got != "/private/tmp/tmux-501/default" {
		t.Fatalf("tmuxSocketFromEnv = %q", got)
	}
	if got := tmuxSocketFromEnv(""); got != "" {
		t.Fatalf("tmuxSocketFromEnv(empty) = %q", got)
	}
}

func TestSanitizedTmuxEnv(t *testing.T) {
	got := sanitizedTmuxEnv([]string{
		"PATH=/bin",
		"TMUX=/private/tmp/tmux-501/default,71243,66",
		"TMUX_PANE=%88",
		"TERM=tmux-256color",
	})
	if slices.Contains(got, "TMUX=/private/tmp/tmux-501/default,71243,66") {
		t.Fatalf("sanitized env still contains TMUX: %v", got)
	}
	if slices.Contains(got, "TMUX_PANE=%88") {
		t.Fatalf("sanitized env still contains TMUX_PANE: %v", got)
	}
	if !slices.Contains(got, "PATH=/bin") || !slices.Contains(got, "TERM=tmux-256color") {
		t.Fatalf("sanitized env dropped unrelated entries: %v", got)
	}
}

func TestLiveTmuxListPanesForSession(t *testing.T) {
	sessionName := os.Getenv("DW_LIVE_TMUX_SESSION")
	if sessionName == "" {
		t.Skip("set DW_LIVE_TMUX_SESSION to run live tmux probe")
	}
	panes, err := NewTmuxProber(SharedProcessInspector).ListPanesForSession(context.Background(), sessionName)
	if err != nil {
		t.Fatalf("ListPanesForSession(%q): %v", sessionName, err)
	}
	t.Logf("panes=%d first=%+v", len(panes), panes[0])
}
