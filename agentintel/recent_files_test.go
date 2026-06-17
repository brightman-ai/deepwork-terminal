package agentintel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSyntheticClaudeTranscript writes a minimal Claude project transcript under a
// fake HOME so RecentEditedFiles can resolve it from a cwd. It encodes the project
// dir the same way ClaudeProjectDir does and returns the cwd to query with.
func writeSyntheticClaudeTranscript(t *testing.T, home, cwd, jsonl string) {
	t.Helper()
	encoded := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	dir := filepath.Join(home, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestRecentEditedFiles_ExtractsEditToolsExcludesBash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// cwd must be a real, symlink-resolvable dir so canonicalCWD / ClaudeProjectDir
	// agree. Use a temp dir as the project root.
	cwd := t.TempDir()

	// Rows (newest timestamps later): Write, Edit, MultiEdit, NotebookEdit should be
	// surfaced; Bash and Read must be excluded. Write to /a.txt appears twice — the
	// newer timestamp must win.
	jsonl := strings.Join([]string{
		`{"type":"assistant","timestamp":"2026-06-10T10:00:00.000Z","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"` + cwd + `/a.txt"}}]}}`,
		`{"type":"assistant","timestamp":"2026-06-10T10:01:00.000Z","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"rm -rf /tmp/x"}}]}}`,
		`{"type":"assistant","timestamp":"2026-06-10T10:02:00.000Z","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"` + cwd + `/b.go"}}]}}`,
		`{"type":"assistant","timestamp":"2026-06-10T10:03:00.000Z","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"` + cwd + `/c.md"}}]}}`,
		`{"type":"assistant","timestamp":"2026-06-10T10:04:00.000Z","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","name":"MultiEdit","input":{"file_path":"` + cwd + `/d.py"}}]}}`,
		`{"type":"assistant","timestamp":"2026-06-10T10:05:00.000Z","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","name":"NotebookEdit","input":{"file_path":"` + cwd + `/e.ipynb"}}]}}`,
		// Newer Write to a.txt — should override the 10:00 timestamp on dedup.
		`{"type":"assistant","timestamp":"2026-06-10T11:00:00.000Z","cwd":"` + cwd + `","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"` + cwd + `/a.txt"}}]}}`,
		"",
	}, "\n")
	writeSyntheticClaudeTranscript(t, home, cwd, jsonl)

	pl := NewProjectLocator()
	got := RecentEditedFiles(pl, cwd)

	paths := map[string]RecentFile{}
	for _, f := range got {
		paths[f.Path] = f
	}

	wantTools := map[string]string{
		cwd + "/a.txt":   "Write",
		cwd + "/b.go":    "Edit",
		cwd + "/d.py":    "MultiEdit",
		cwd + "/e.ipynb": "NotebookEdit",
	}
	for p, tool := range wantTools {
		f, ok := paths[p]
		if !ok {
			t.Errorf("expected %s to be surfaced, missing", p)
			continue
		}
		if f.Tool != tool {
			t.Errorf("%s: tool = %q, want %q", p, f.Tool, tool)
		}
		if f.Name != filepath.Base(p) {
			t.Errorf("%s: name = %q, want %q", p, f.Name, filepath.Base(p))
		}
	}

	// Bash (rm) and Read (c.md) must NOT appear.
	if _, ok := paths[cwd+"/c.md"]; ok {
		t.Errorf("Read file c.md should be excluded but was surfaced")
	}
	for _, f := range got {
		if f.Tool == "Bash" {
			t.Errorf("Bash tool_use must never be surfaced: %+v", f)
		}
	}

	// Dedup: a.txt once, with the NEWER (11:00) timestamp.
	a := paths[cwd+"/a.txt"]
	if a.TsMs == 0 {
		t.Errorf("a.txt has zero tsMs (timestamp parse failed)")
	}
	// 11:00 epoch ms must be greater than 10:00 — sanity that newest won.
	if a.TsMs < parseRowTimestampMs("2026-06-10T10:30:00.000Z") {
		t.Errorf("a.txt dedup kept the OLDER timestamp: %d", a.TsMs)
	}

	// Result is sorted newest-first.
	for i := 1; i < len(got); i++ {
		if got[i-1].TsMs < got[i].TsMs {
			t.Errorf("not sorted newest-first at %d: %d < %d", i, got[i-1].TsMs, got[i].TsMs)
		}
	}
}

func TestRecentEditedFiles_EmptyWhenNoTranscripts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pl := NewProjectLocator()
	got := RecentEditedFiles(pl, t.TempDir())
	if len(got) != 0 {
		t.Errorf("expected empty result for project with no transcripts, got %d", len(got))
	}
}
