package agentintel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeProjectDir_encoding(t *testing.T) {
	pl := NewProjectLocator()
	home, _ := os.UserHomeDir()

	cases := []struct {
		projectPath string
		wantSuffix  string
	}{
		{
			projectPath: "/Users/foo/proj",
			wantSuffix:  "/.claude/projects/-Users-foo-proj",
		},
		{
			projectPath: "/home/user/code/my.project",
			wantSuffix:  "/.claude/projects/-home-user-code-my-project",
		},
		{
			projectPath: "/Users/anthony/code/deepwork",
			wantSuffix:  "/.claude/projects/-Users-anthony-code-deepwork",
		},
	}

	for _, tc := range cases {
		got := pl.ClaudeProjectDir(tc.projectPath)
		want := home + tc.wantSuffix
		if got != want {
			t.Errorf("ClaudeProjectDir(%q)\n got  %q\n want %q", tc.projectPath, got, want)
		}
	}
}

func TestSelectCodexRootRollout_ProcessIdentityBeatsSameCWDNewest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex", "sessions", "2026", "07", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	pane2 := write("rollout-pane2.jsonl", `{"type":"session_meta","payload":{"id":"pane2","cwd":"/same/project","source":"cli"}}`)
	pane5 := write("rollout-pane5.jsonl", `{"type":"session_meta","payload":{"id":"pane5","cwd":"/same/project","source":"cli"}}`)
	child := write("rollout-child.jsonl", `{"type":"session_meta","payload":{"id":"child","cwd":"/same/project","source":{"subagent":{"thread_spawn":{"parent_thread_id":"pane2"}}}}}`)

	// These paths model pane2's concrete process FDs. pane5 is newer globally,
	// but is not open by pane2 and therefore cannot contaminate its state.
	got, err := selectCodexRootRollout([]string{pane2, child}, "/same/project")
	if err != nil || got != pane2 {
		t.Fatalf("got (%q,%v), want pane2 root %q", got, err, pane2)
	}
	if got == pane5 {
		t.Fatal("same-cwd newest rollout contaminated another process")
	}
}

func TestCodexLatestSessionFiltersCWDAndSubagents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex", "sessions", "2026", "07", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "rollout-root.jsonl")
	other := filepath.Join(dir, "rollout-other.jsonl")
	child := filepath.Join(dir, "rollout-child.jsonl")
	fixtures := map[string]string{
		root:  `{"type":"session_meta","payload":{"id":"root","cwd":"/wanted","source":"cli"}}`,
		other: `{"type":"session_meta","payload":{"id":"other","cwd":"/other","source":"cli"}}`,
		child: `{"type":"session_meta","payload":{"id":"child","cwd":"/wanted","source":{"subagent":{"thread_spawn":{"parent_thread_id":"root"}}}}}`,
	}
	for path, body := range fixtures {
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Make the wrong-cwd and child files newer; neither may win.
	now := time.Now()
	_ = os.Chtimes(other, now, now)
	_ = os.Chtimes(child, now.Add(time.Second), now.Add(time.Second))
	_ = os.Chtimes(root, now.Add(-time.Second), now.Add(-time.Second))
	got, err := NewProjectLocator().CodexLatestSession("/wanted")
	if err != nil || got != root {
		t.Fatalf("got (%q,%v), want root %q", got, err, root)
	}
}

func TestClaudeProjectDir_noDoubleSlash(t *testing.T) {
	pl := NewProjectLocator()
	dir := pl.ClaudeProjectDir("/Users/foo/bar")
	if strings.Contains(dir, "//") {
		t.Errorf("ClaudeProjectDir produced double slash: %q", dir)
	}
}

func TestCodexSessionDir(t *testing.T) {
	pl := NewProjectLocator()
	home, _ := os.UserHomeDir()
	got := pl.CodexSessionDir()
	want := home + "/.codex/sessions"
	if got != want {
		t.Errorf("CodexSessionDir() = %q, want %q", got, want)
	}
}
