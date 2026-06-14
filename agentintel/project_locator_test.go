package agentintel

import (
	"os"
	"strings"
	"testing"
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
