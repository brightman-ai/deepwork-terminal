package agentintel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// resetCodexIndexRegistry drops the in-memory shared indexes so a test can observe what a
// FRESH process would load from disk. Without it every driver on the same sessions root gets
// the same live index and the cache is never exercised.
func resetCodexIndexRegistry() {
	codexIndexRegistry.Lock()
	defer codexIndexRegistry.Unlock()
	codexIndexRegistry.byRoot = nil
}

func writeCodexFixture(t *testing.T, root string) (parent, child string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	parent = filepath.Join(root, "rollout-parent.jsonl")
	parentBody := `{"timestamp":"2026-08-05T01:00:00Z","type":"session_meta","payload":{"id":"parent-1","cwd":"/project"}}` + "\n" +
		`{"timestamp":"2026-08-05T01:00:01Z","type":"response_item","payload":{"type":"function_call","name":"spawn_agent","arguments":"{\"task_name\":\"reviewer\",\"message\":\"review the diff\"}"}}` + "\n" +
		`{"timestamp":"2026-08-05T01:00:02Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n"
	if err := os.WriteFile(parent, []byte(parentBody), 0o600); err != nil {
		t.Fatal(err)
	}
	child = filepath.Join(root, "rollout-child.jsonl")
	childBody := `{"timestamp":"2026-08-05T01:00:03Z","type":"session_meta","payload":{"id":"child-1","source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-1","depth":1,"agent_path":"/root/reviewer"}}}}}` + "\n" +
		`{"timestamp":"2026-08-05T01:00:04Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
		`{"timestamp":"2026-08-05T01:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":77}}}}` + "\n" +
		`{"timestamp":"2026-08-05T01:00:06Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	if err := os.WriteFile(child, []byte(childBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return parent, child
}

// The index exists so a restart does not re-derive the subagent tree from every rollout on
// disk. That claim is only true if the cursors come back too — a restored tree that still
// re-reads 1.9 GB to produce it has saved nothing.
func TestCodexAgentIndexResumesWhereItStopped(t *testing.T) {
	cache := t.TempDir()
	root := filepath.Join(t.TempDir(), "sessions")
	parent, child := writeCodexFixture(t, root)

	SetAgentIndexCacheDir(cache)
	t.Cleanup(func() { SetAgentIndexCacheDir("") })
	resetCodexIndexRegistry()
	t.Cleanup(resetCodexIndexRegistry)

	driver := NewCodexDriver(parent)
	if err := driver.Update(); err != nil {
		t.Fatal(err)
	}
	nodes := driver.AgentTree()
	if len(nodes) != 1 || nodes[0].Status != AgentDone || nodes[0].TokensDown != 77 {
		t.Fatalf("first pass tree=%+v", nodes)
	}
	if nodes[0].Description != "review the diff" {
		t.Fatalf("spawn description lost: %q", nodes[0].Description)
	}

	// A fresh process: same files on disk, nothing in memory.
	resetCodexIndexRegistry()
	reloaded := NewCodexDriver(parent)
	idx := reloaded.agentTree.index
	idx.mu.Lock()
	file, known := idx.files[child]
	var offset int64
	if known {
		offset = file.reader.Offset()
	}
	_, parentSeen := idx.seen[parent]
	idx.mu.Unlock()

	if !known {
		t.Fatal("restart lost the subagent rollout entirely")
	}
	info, err := os.Stat(child)
	if err != nil {
		t.Fatal(err)
	}
	if offset != info.Size() {
		t.Fatalf("restart re-reads the child rollout: cursor=%d, already %d bytes on disk", offset, info.Size())
	}
	if !parentSeen {
		t.Fatal("restart re-inspects rollouts it has already classified")
	}

	if err := reloaded.Update(); err != nil {
		t.Fatal(err)
	}
	nodes = reloaded.AgentTree()
	if len(nodes) != 1 || nodes[0].Status != AgentDone || nodes[0].TokensDown != 77 || nodes[0].Description != "review the diff" {
		t.Fatalf("restored tree=%+v", nodes)
	}

	// A resumed cursor still has to be a cursor: new events must land.
	f, err := os.OpenFile(child, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(f, `{"timestamp":"2026-08-05T01:01:00Z","type":"event_msg","payload":{"type":"turn_aborted"}}`); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Update(); err != nil {
		t.Fatal(err)
	}
	if nodes = reloaded.AgentTree(); len(nodes) != 1 || nodes[0].Status != AgentError {
		t.Fatalf("appended event was not seen after a resume: %+v", nodes)
	}
}

// A cache is only ever an accelerator. Anything it cannot vouch for must be re-derived, not
// half-trusted: a cursor restored under different parsing semantics, or against a different
// sessions root, describes bytes that are not the bytes it claims.
func TestCodexAgentIndexRejectsWhatItCannotVouchFor(t *testing.T) {
	cache := t.TempDir()
	root := filepath.Join(t.TempDir(), "sessions")
	parent, _ := writeCodexFixture(t, root)

	SetAgentIndexCacheDir(cache)
	t.Cleanup(func() { SetAgentIndexCacheDir("") })
	resetCodexIndexRegistry()
	t.Cleanup(resetCodexIndexRegistry)

	driver := NewCodexDriver(parent)
	if err := driver.Update(); err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(cache, "codex-agent-index."+hashPath(root)+".json")
	body, err := os.ReadFile(cached)
	if err != nil {
		t.Fatalf("index was never persisted: %v", err)
	}

	for name, corrupt := range map[string]string{
		"another schema": `{"schema":"codex-agent-index.vFUTURE","sessions_root":"` + root + `","files":[{"path":"/nope.jsonl","offset":9}]}`,
		"another root":   `{"schema":"` + codexAgentIndexSchema + `","sessions_root":"/somewhere/else","files":[{"path":"/nope.jsonl","offset":9}]}`,
		"not json":       string(body[:len(body)/2]),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(cached, []byte(corrupt), 0o600); err != nil {
				t.Fatal(err)
			}
			resetCodexIndexRegistry()
			fresh := NewCodexDriver(parent)
			idx := fresh.agentTree.index
			idx.mu.Lock()
			adopted := len(idx.files)
			idx.mu.Unlock()
			if adopted != 0 {
				t.Fatalf("adopted %d entries from an index it cannot vouch for", adopted)
			}
			// And it still works, the slow way.
			if err := fresh.Update(); err != nil {
				t.Fatal(err)
			}
			if nodes := fresh.AgentTree(); len(nodes) != 1 || nodes[0].Status != AgentDone {
				t.Fatalf("rebuild from scratch failed: %+v", nodes)
			}
		})
	}
}
