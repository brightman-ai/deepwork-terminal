package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/kit/agentanalytics"
	"github.com/brightman-ai/kit/transcript"
)

func TestProjectAgentFileCodexRequestJoinsWorkItem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	dir := filepath.Join(home, ".codex", "sessions", "2026", "07", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-test.jsonl")
	body := `{"timestamp":"2026-07-14T01:00:00Z","type":"session_meta","payload":{"id":"root-1","cwd":"/project","source":"cli"}}
{"timestamp":"2026-07-14T01:00:01Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-5.6-sol","service_tier":"priority","reasoning_effort":"xhigh"}}}
{"timestamp":"2026-07-14T01:00:02Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-07-14T01:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix"}]}}
{"timestamp":"2026-07-14T01:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}
{"timestamp":"2026-07-14T01:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":11790,"cached_input_tokens":9000,"output_tokens":210,"reasoning_output_tokens":40,"total_tokens":12000}}}}
{"timestamp":"2026-07-14T01:00:06Z","type":"event_msg","payload":{"type":"task_complete"}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dataset, err := projectAgentFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.WorkItems) != 1 || len(dataset.Requests) != 1 || dataset.Requests[0].WorkItemID != dataset.WorkItems[0].ID {
		t.Fatalf("projection did not join request to work item: work=%+v requests=%+v", dataset.WorkItems, dataset.Requests)
	}
	if dataset.Requests[0].APIEquivalent == nil || dataset.Requests[0].APIEquivalent.Amount != .0495 {
		t.Fatalf("request cost=%+v, want priority .0495", dataset.Requests[0].APIEquivalent)
	}
	if dataset.WorkItems[0].Outcome != agentanalytics.OutcomeCompletedUnverified {
		t.Fatalf("runtime completion became success: %+v", dataset.WorkItems[0])
	}
}

func TestAgentReporterRepricesUnchangedCachedFacts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(home, ".claude", "projects"))
	path := filepath.Join(home, ".codex", "sessions", "2026", "07", "15", "rollout-cached.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reporter := newAgentReporter()
	reporter.now = func() time.Time { return now }
	reporter.files[path] = agentFileProjection{
		size: info.Size(), modUnixNano: info.ModTime().UnixNano(), runtime: "codex", sessionID: "cached",
		dataset: agentanalytics.ActivityDataset{
			RequestFacts: []transcript.ModelRequestUsage{{
				ID: "cached-request", Runtime: "codex", Model: "gpt-5.5", ServiceTier: "priority", At: now,
				InputTokens: 1_000_000, CachedInputTokens: 1_000_000, OutputTokens: 1_000_000,
				Coverage: transcript.RequestCoverage{Tokens: transcript.CoverageComplete},
			}},
			// Simulate an index written before the exact Priority price existed.
			Requests: []agentanalytics.EconomicRequest{{ID: "cached-request", Runtime: "codex", Model: "gpt-5.5"}},
		},
	}
	dataset := reporter.Dataset(context.Background(), "24h")
	if len(dataset.Requests) != 1 || dataset.Requests[0].APIEquivalent == nil || dataset.Requests[0].APIEquivalent.Amount != 88.75 {
		t.Fatalf("unchanged cached fact was not repriced: %+v", dataset.Requests)
	}
}

func TestProjectAgentFileClaudeExtractsToolTimingAndArtifacts(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude", "projects")
	t.Setenv("DW_CLAUDE_PROJECTS", root)
	dir := filepath.Join(root, "-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "claude-root.jsonl")
	body := `{"type":"user","sessionId":"claude-root","cwd":"/project","timestamp":"2026-07-14T02:00:00Z","message":{"role":"user","content":"fix it"}}` + "\n" +
		`{"type":"assistant","sessionId":"claude-root","cwd":"/project","timestamp":"2026-07-14T02:00:01Z","message":{"id":"msg_1","role":"assistant","model":"claude-sonnet-5","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool_edit","name":"Edit","input":{"file_path":"/project/a.go","old_string":"old\n","new_string":"new\n"}}],"usage":{"input_tokens":10,"output_tokens":5}}}` + "\n" +
		`{"type":"user","sessionId":"claude-root","cwd":"/project","timestamp":"2026-07-14T02:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_edit","content":"The file /project/a.go has been updated successfully."}]}}` + "\n" +
		`{"type":"assistant","sessionId":"claude-root","cwd":"/project","timestamp":"2026-07-14T02:00:04Z","message":{"id":"msg_2","role":"assistant","model":"claude-sonnet-5","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool_write","name":"Write","input":{"file_path":"/project/new.md","content":"one\ntwo\n"}}],"usage":{"input_tokens":10,"output_tokens":4}}}` + "\n" +
		`{"type":"user","sessionId":"claude-root","cwd":"/project","timestamp":"2026-07-14T02:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_write","content":"File created successfully at: /project/new.md"}]}}` + "\n" +
		`{"type":"assistant","sessionId":"claude-root","cwd":"/project","timestamp":"2026-07-14T02:00:07Z","message":{"id":"msg_3","role":"assistant","model":"claude-sonnet-5","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"output_tokens":3}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	dataset, err := projectAgentFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.WorkItems) != 1 || len(dataset.Tools) != 2 || len(dataset.Artifacts) != 2 {
		t.Fatalf("projection work=%d tools=%+v artifacts=%+v", len(dataset.WorkItems), dataset.Tools, dataset.Artifacts)
	}
	if dataset.Tools[0].DurationSeconds == nil || *dataset.Tools[0].DurationSeconds != 2 || dataset.Tools[0].Status != agentanalytics.ToolExecutionCompleted {
		t.Fatalf("edit tool timing=%+v", dataset.Tools[0])
	}
	if edit := dataset.Artifacts[0]; edit.Kind != agentanalytics.ArtifactCode || edit.Additions != 1 || edit.Deletions != 1 || edit.ChangeCoverage != "complete" {
		t.Fatalf("edit artifact=%+v", edit)
	}
	if write := dataset.Artifacts[1]; write.Kind != agentanalytics.ArtifactDoc || write.Operation != "create" || write.Additions != 2 || write.WrittenLines != 2 {
		t.Fatalf("write artifact=%+v", write)
	}
}

func TestProjectCodexApplyPatchEmitsStableMultiFileArtifacts(t *testing.T) {
	now := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
	block := transcript.Block{
		Type: transcript.BlockTool, ToolName: "apply_patch", ToolUseID: "patch-1", ResultSeen: true, EndedAt: &now,
		ToolInput: map[string]interface{}{"input": "*** Begin Patch\n*** Update File: src/main.go\n@@\n-old\n+new\n+extra\n*** Add File: docs/readme.md\n+# title\n+body\n*** End Patch"},
	}
	deltas := projectArtifactDeltas("work-1", "tool-1", block)
	if len(deltas) != 2 {
		t.Fatalf("multi-file patch artifacts=%+v", deltas)
	}
	if got := deltas[0]; got.ID != "tool-1:artifact:0" || got.Path != "src/main.go" || got.Kind != agentanalytics.ArtifactCode || got.Operation != "modify" || got.Additions != 2 || got.Deletions != 1 || got.WrittenLines != 2 {
		t.Fatalf("update artifact=%+v", got)
	}
	if got := deltas[1]; got.ID != "tool-1:artifact:1" || got.Path != "docs/readme.md" || got.Kind != agentanalytics.ArtifactDoc || got.Operation != "create" || got.Additions != 2 || got.Deletions != 0 || got.WrittenLines != 2 {
		t.Fatalf("add artifact=%+v", got)
	}
	totals := agentanalytics.AggregateArtifacts(deltas)
	if totals.Events != 2 || totals.ByKind[agentanalytics.ArtifactCode].Files != 1 || totals.ByKind[agentanalytics.ArtifactDoc].Files != 1 {
		t.Fatalf("multi-file patch collapsed=%+v", totals)
	}
}

func TestProjectCodexApplyPatchAcceptsOnlyExactExecBridge(t *testing.T) {
	now := time.Date(2026, 7, 15, 2, 0, 0, 0, time.UTC)
	patch := "*** Begin Patch\n*** Add File: docs/from-bridge.md\n+hello\n*** End Patch"
	encoded, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	block := transcript.Block{
		Type: transcript.BlockTool, ToolName: "exec", ResultSeen: true, EndedAt: &now,
		ToolInput: map[string]interface{}{"input": "const patch = " + string(encoded) + ";\ntext(await tools.apply_patch(patch));"},
	}
	deltas := projectArtifactDeltas("work", "bridge-tool", block)
	if len(deltas) != 1 || deltas[0].Path != "docs/from-bridge.md" || deltas[0].Additions != 1 {
		t.Fatalf("exact bridge artifact=%+v", deltas)
	}
	block.ToolInput["input"] = "const patch = " + string(encoded) + ";\ntext(patch);"
	if got := projectArtifactDeltas("work", "not-executed", block); len(got) != 0 {
		t.Fatalf("non-executing JS was treated as artifact evidence: %+v", got)
	}
}

func TestMergeRequestFactHonorsTimingInvalidationTombstone(t *testing.T) {
	start := time.Date(2026, 7, 14, 2, 0, 0, 0, time.UTC)
	first, end := start.Add(time.Second), start.Add(2*time.Second)
	prior := transcript.ModelRequestUsage{
		ID: "claude:s:msg", StartedAt: &start, FirstObservedAt: &first, EndedAt: &end,
		Coverage: transcript.RequestCoverage{Timing: transcript.CoveragePartial},
	}
	next := transcript.ModelRequestUsage{
		ID: "claude:s:msg", TimingInvalidated: true,
		Coverage: transcript.RequestCoverage{Timing: transcript.CoverageMissing},
	}
	merged := mergeRequestFact(prior, next)
	if !merged.TimingInvalidated || merged.StartedAt != nil || merged.FirstTokenAt != nil || merged.Coverage.Timing != transcript.CoverageMissing {
		t.Fatalf("timing tombstone was not monotonic: %+v", merged)
	}
}

func TestToolProjectionDistinguishesOpenInterruptedAndUnknown(t *testing.T) {
	identity := agentFileIdentity{runtime: transcript.KindClaude, sessionID: "s"}
	block := transcript.Block{Type: transcript.BlockTool, ToolUseID: "tool", ToolName: "Edit"}
	open := projectToolExecution(identity, "source", "work", "tool", agentanalytics.LifecycleOpen, block)
	interrupted := projectToolExecution(identity, "source", "work", "tool", agentanalytics.LifecycleInterrupted, block)
	unknown := projectToolExecution(identity, "source", "work", "tool", agentanalytics.LifecycleCompleted, block)
	if open.Status != agentanalytics.ToolExecutionOpen || interrupted.Status != agentanalytics.ToolExecutionInterrupted || unknown.Status != agentanalytics.ToolExecutionUnknown {
		t.Fatalf("tool states open=%s interrupted=%s unknown=%s", open.Status, interrupted.Status, unknown.Status)
	}
}

func TestHandleAgentReportContractAndTimezoneValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(home, ".claude", "projects"))
	s := &Server{agentUsage: newAgentReporter()}
	req := httptest.NewRequest(http.MethodGet, "/usage/agent-report?window=24h&timezone=Asia%2FShanghai", nil)
	w := httptest.NewRecorder()
	s.handleAgentReport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var report agentanalytics.ActivityReport
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != "agent-report.v1" || report.Timezone != "Asia/Shanghai" {
		t.Fatalf("report=%+v", report)
	}

	bad := httptest.NewRequest(http.MethodGet, "/usage/agent-report?timezone=Moon%2FBase", nil)
	w = httptest.NewRecorder()
	s.handleAgentReport(w, bad)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid timezone status=%d", w.Code)
	}
}

func TestHandleAgentReportDetailContractAndFilters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(home, ".claude", "projects"))
	s := &Server{agentUsage: newAgentReporter()}
	req := httptest.NewRequest(http.MethodGet, "/usage/agent-report/detail?window=7d&timezone=Asia%2FShanghai&runtime=codex&limit=20", nil)
	w := httptest.NewRecorder()
	s.handleAgentReportDetail(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var detail agentanalytics.ActivityDetailReport
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.SchemaVersion != "agent-detail.v1" || detail.Report.Window != "7d" || len(detail.Metrics) == 0 {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestAgentReporterUnchangedFileUsesMaterializedProjection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(home, ".claude", "projects"))
	a := newAgentReporter()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.Local)
	a.now = func() time.Time { return now }
	// Empty roots still produce a deterministic last-good report and cache it.
	r1 := a.Report(context.Background(), "24h", "Asia/Shanghai")
	r2 := a.Report(context.Background(), "24h", "Asia/Shanghai")
	if !r1.GeneratedAt.Equal(r2.GeneratedAt) {
		t.Fatal("hot report bypassed cache")
	}
}

func TestAgentReporterIncrementalAppendMatchesFullRebuild(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude", "projects")
	t.Setenv("DW_CLAUDE_PROJECTS", root)
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	dir := filepath.Join(root, "-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "incremental.jsonl")
	initial := `{"type":"user","sessionId":"incremental","cwd":"/project","timestamp":"2026-07-14T02:00:00Z","message":{"role":"user","content":"first"}}` + "\n" +
		`{"type":"assistant","sessionId":"incremental","cwd":"/project","timestamp":"2026-07-14T02:00:01Z","message":{"id":"msg_1","role":"assistant","model":"claude-sonnet-5","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"output_tokens":2}}}` + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.Local)
	incrementalReporter := newAgentReporter(t.TempDir())
	incrementalReporter.now = func() time.Time { return now }
	first := incrementalReporter.Report(context.Background(), "24h", "Asia/Shanghai", true)
	if first.Observability.Projection.Mode != "full_rebuild" {
		t.Fatalf("first projection mode=%s", first.Observability.Projection.Mode)
	}
	appendRequestFixture := func(value string) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.WriteString(value); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	appendRequestFixture(
		`{"type":"user","sessionId":"incremental","cwd":"/project","timestamp":"2026-07-14T02:00:02Z","message":{"role":"user","content":"second"}}` + "\n" +
			`{"type":"assistant","sessionId":"incremental","cwd":"/project","timestamp":"2026-07-14T02:00:04Z","message":{"id":"msg_2","role":"assistant","model":"claude-sonnet-5","stop_reason":"end_turn","content":[{"type":"text","text":"done again"}],"usage":{"input_tokens":11,"output_tokens":3}}}` + "\n")
	incrementalStarted := time.Now()
	incremental := incrementalReporter.Report(context.Background(), "24h", "Asia/Shanghai", true)
	incrementalElapsed := time.Since(incrementalStarted)
	fullReporter := newAgentReporter(t.TempDir())
	fullReporter.now = func() time.Time { return now }
	fullStarted := time.Now()
	full := fullReporter.Report(context.Background(), "24h", "Asia/Shanghai", true)
	fullElapsed := time.Since(fullStarted)
	if incremental.Observability.Projection.Mode != "incremental" || incremental.Observability.Projection.ChangedFiles != 1 {
		t.Fatalf("append was not incremental: %+v", incremental.Observability.Projection)
	}
	if incremental.Summary.WorkItems != full.Summary.WorkItems || incremental.Summary.ModelRequests != full.Summary.ModelRequests || incremental.Tools.Calls != full.Tools.Calls || incremental.Artifacts.Events != full.Artifacts.Events {
		t.Fatalf("append/full mismatch: incremental=%+v full=%+v", incremental.Summary, full.Summary)
	}
	if len(incremental.RuntimeProfiles) != 1 || len(full.RuntimeProfiles) != 1 || incremental.RuntimeProfiles[0].ObservedFirstResponseMedianSeconds == nil || full.RuntimeProfiles[0].ObservedFirstResponseMedianSeconds == nil || *incremental.RuntimeProfiles[0].ObservedFirstResponseMedianSeconds != *full.RuntimeProfiles[0].ObservedFirstResponseMedianSeconds {
		t.Fatalf("append/full latency mismatch: incremental=%+v full=%+v", incremental.RuntimeProfiles, full.RuntimeProfiles)
	}
	if incrementalElapsed > 500*time.Millisecond {
		t.Fatalf("incremental projection exceeded 500ms budget: %s", incrementalElapsed)
	}
	t.Logf("incremental=%s full=%s work=%d requests=%d first_response=%s", incrementalElapsed, fullElapsed, incremental.Summary.WorkItems, incremental.Summary.ModelRequests, time.Duration(*incremental.RuntimeProfiles[0].ObservedFirstResponseMedianSeconds*float64(time.Second)))
}

func TestAgentReporterRealDataObservabilityProbe(t *testing.T) {
	if os.Getenv("DW_AGENT_REALDATA") != "1" {
		t.Skip("set DW_AGENT_REALDATA=1 to validate local provider transcripts")
	}
	reporter := newAgentReporter(t.TempDir())
	coldStarted := time.Now()
	report := reporter.Report(context.Background(), "7d", "Asia/Shanghai", true)
	coldElapsed := time.Since(coldStarted)
	hotStarted := time.Now()
	hot := reporter.Report(context.Background(), "7d", "Asia/Shanghai")
	hotElapsed := time.Since(hotStarted)
	var claude *agentanalytics.RuntimeProfile
	for i := range report.RuntimeProfiles {
		if report.RuntimeProfiles[i].Runtime == transcript.KindClaude {
			claude = &report.RuntimeProfiles[i]
			break
		}
	}
	if claude == nil || claude.ObservedResponseTokensPerSecond == nil || claude.ResponseSpeedCoverage.ObservedN == 0 {
		t.Fatalf("Claude response throughput missing: profiles=%+v", report.RuntimeProfiles)
	}
	if claude.ObservedFirstResponseMedianSeconds == nil || claude.FirstResponseCoverage.ObservedN == 0 {
		t.Fatalf("Claude transcript first-response evidence missing: %+v", claude)
	}
	if claude.TTFTMedianSeconds != nil || claude.TTFTCoverage.ObservedN != 0 {
		t.Fatalf("transcript observation was mislabeled provider TTFT: %+v", claude)
	}
	if report.Tools.Calls == 0 || report.Tools.TimingCoverage.ObservedN == 0 {
		t.Fatalf("tool timing missing: %+v", report.Tools)
	}
	if report.Artifacts.Events == 0 {
		dataset := reporter.Dataset(context.Background(), "7d")
		t.Fatalf("artifact extraction missing: report=%+v raw_artifacts=%d raw_tools=%d diagnostics=%v", report.Artifacts, len(dataset.Artifacts), len(dataset.Tools), dataset.IngestDiagnostics)
	}
	if report.Health.PolicyVersion != agentanalytics.AgentHealthPolicyVersion || len(report.Health.Axes) != 3 {
		t.Fatalf("health assessment missing: %+v", report.Health)
	}
	if !hot.GeneratedAt.Equal(report.GeneratedAt) {
		t.Fatal("hot path bypassed report cache")
	}
	t.Logf("real report: work=%d requests=%d tools=%d artifacts=%d claude_response=%.2f tok/s response_coverage=%d/%d first_response=%s first_response_coverage=%d/%d ttft_coverage=%d/%d health=%s cold=%s hot=%s mode=%s",
		report.Summary.WorkItems, report.Summary.ModelRequests, report.Tools.Calls, report.Artifacts.Events,
		*claude.ObservedResponseTokensPerSecond, claude.ResponseSpeedCoverage.ObservedN, claude.ResponseSpeedCoverage.EligibleN,
		time.Duration(*claude.ObservedFirstResponseMedianSeconds*float64(time.Second)), claude.FirstResponseCoverage.ObservedN, claude.FirstResponseCoverage.EligibleN,
		claude.TTFTCoverage.ObservedN, claude.TTFTCoverage.EligibleN,
		report.Health.State, coldElapsed, hotElapsed, report.Observability.Projection.Mode)
}

func TestAgentReporterIndexRoundTripAndSchemaGate(t *testing.T) {
	dir := t.TempDir()
	a := newAgentReporter(dir)
	a.files["/tmp/rollout.jsonl"] = agentFileProjection{
		size: 42, modUnixNano: 99, generation: "gen", runtime: "codex", sessionID: "s1",
		codexCursor: transcript.CodexRequestCursor{Offset: 42, SessionID: "s1", Model: "gpt-5.6-sol"},
		dataset:     agentanalytics.ActivityDataset{WorkItems: []agentanalytics.ActivityWorkItem{{ID: "w1", Runtime: "codex"}}},
	}
	a.mu.Lock()
	a.saveIndexLocked()
	a.mu.Unlock()

	reloaded := newAgentReporter(dir)
	got, ok := reloaded.files["/tmp/rollout.jsonl"]
	if !ok || got.size != 42 || got.generation != "gen" || got.codexCursor.Offset != 42 || got.codexCursor.Model != "gpt-5.6-sol" || len(got.dataset.WorkItems) != 1 || got.dataset.WorkItems[0].ID != "w1" {
		t.Fatalf("index round trip=%+v ok=%v", got, ok)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-report-index-v10.json"), []byte(`{"schema":"future","files":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if stale := newAgentReporter(dir); len(stale.files) != 0 {
		t.Fatalf("future schema was loaded: %+v", stale.files)
	}
}

func TestAgentReporterAppendProjectionKeepsIdentityAndScansOnlyNewRequests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	dir := filepath.Join(home, ".codex", "sessions", "2026", "07", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-append.jsonl")
	initial := `{"timestamp":"2026-07-14T01:00:00Z","type":"session_meta","payload":{"id":"append-1","cwd":"/project","source":"cli"}}` + "\n" +
		`{"timestamp":"2026-07-14T01:00:01Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-5.6-sol","service_tier":"standard","reasoning_effort":"high"}}}` + "\n" +
		`{"timestamp":"2026-07-14T01:00:02Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
		`{"timestamp":"2026-07-14T01:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}}` + "\n" +
		`{"timestamp":"2026-07-14T01:00:04Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}}}` + "\n" +
		`{"timestamp":"2026-07-14T01:00:05Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := inspectAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := projectAgentFileProjection(context.Background(), path, identity, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	first.size, first.modUnixNano = info.Size(), info.ModTime().UnixNano()
	if len(first.dataset.WorkItems) != 1 || len(first.dataset.RequestFacts) != 1 || first.codexCursor.Offset != info.Size() {
		t.Fatalf("initial projection=%+v", first)
	}
	stableID := first.dataset.WorkItems[0].ID
	firstRequestID := first.dataset.RequestFacts[0].ID

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	appendBody := `{"timestamp":"2026-07-14T01:01:00Z","type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-5.6-terra","service_tier":"priority","reasoning_effort":"xhigh"}}}` + "\n" +
		`{"timestamp":"2026-07-14T01:01:01Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n" +
		`{"timestamp":"2026-07-14T01:01:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]}}` + "\n" +
		`{"timestamp":"2026-07-14T01:01:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":80,"cached_input_tokens":20,"output_tokens":8}}}}` + "\n" +
		`{"timestamp":"2026-07-14T01:01:04Z","type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
	if _, err := f.WriteString(appendBody); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	second, reset, err := appendAgentFileProjection(context.Background(), path, identity, first, time.Time{})
	if err != nil || reset {
		t.Fatalf("append reset=%v err=%v", reset, err)
	}
	if len(second.dataset.WorkItems) != 2 || len(second.dataset.RequestFacts) != 2 || len(second.dataset.Requests) != 2 {
		t.Fatalf("append projection work=%+v facts=%+v", second.dataset.WorkItems, second.dataset.RequestFacts)
	}
	if second.dataset.WorkItems[0].ID != stableID || second.dataset.RequestFacts[0].ID != firstRequestID {
		t.Fatalf("stable facts changed: before=%s/%s after=%s/%s", stableID, firstRequestID, second.dataset.WorkItems[0].ID, second.dataset.RequestFacts[0].ID)
	}
	if second.dataset.RequestFacts[1].Model != "gpt-5.6-terra" || second.dataset.RequestFacts[1].Effort != "xhigh" {
		t.Fatalf("appended settings lost: %+v", second.dataset.RequestFacts[1])
	}
	if second.codexCursor.Offset <= first.codexCursor.Offset {
		t.Fatalf("cursor did not advance: before=%+v after=%+v", first.codexCursor, second.codexCursor)
	}
}

func TestInspectClaudeAgentFileIgnoresLeadingControlRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-a.jsonl")
	body := `{"type":"queue-operation","operation":"enqueue"}` + "\n" +
		`{"type":"assistant","sessionId":"parent-session","isSidechain":true,"agentId":"agent-a","cwd":"/project"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := inspectAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if identity.root || identity.runtime != transcript.KindClaude || identity.sessionID != "agent-a" || identity.project != "/project" {
		t.Fatalf("identity=%+v", identity)
	}
}

func TestCodexSubagentProjectionSkipsInheritedParentRequests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DW_CODEX_HOME", filepath.Join(home, ".codex"))
	dir := filepath.Join(home, ".codex", "sessions", "2026", "07", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-child.jsonl")
	body := `{"timestamp":"2026-07-14T03:08:30Z","type":"session_meta","payload":{"id":"019f5e98-f665-7af0-865a-a27435989655","cwd":"/project","source":{"subagent":{"thread_spawn":{"parent_thread_id":"019f59ec-f2c4-7bb3-8bf8-e3ce7cd4256d"}}}}}` + "\n" +
		`{"timestamp":"2026-07-14T03:08:30Z","type":"session_meta","payload":{"id":"019f59ec-f2c4-7bb3-8bf8-e3ce7cd4256d","cwd":"/project","source":"cli"}}` + "\n" +
		`{"timestamp":"2026-07-14T03:08:30Z","type":"event_msg","payload":{"type":"task_started","turn_id":"019f59ed-03b4-7ea2-8277-f50bdec3279d"}}` + "\n" +
		`{"timestamp":"2026-07-14T03:08:30Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":9000,"cached_input_tokens":8000,"output_tokens":900}}}}` + "\n" +
		`{"timestamp":"2026-07-14T03:08:31Z","type":"event_msg","payload":{"type":"task_started","turn_id":"019f5e98-f77c-7ba0-8ff1-6a40ac7794c9"}}` + "\n" +
		`{"timestamp":"2026-07-14T03:08:32Z","type":"turn_context","payload":{"turn_id":"019f5e98-f77c-7ba0-8ff1-6a40ac7794c9","model":"gpt-5.6-sol","effort":"high","service_tier":"standard"}}` + "\n" +
		`{"timestamp":"2026-07-14T03:08:33Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := inspectAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if identity.root {
		t.Fatalf("subagent classified as root: %+v", identity)
	}
	projection, err := projectAgentFileProjection(context.Background(), path, identity, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(projection.dataset.RequestFacts); got != 1 {
		t.Fatalf("inherited request counted: got=%d facts=%+v", got, projection.dataset.RequestFacts)
	}
	fact := projection.dataset.RequestFacts[0]
	if fact.InputTokens != 60 || fact.CachedInputTokens != 40 || fact.OutputTokens != 10 {
		t.Fatalf("child request=%+v", fact)
	}
	if projection.codexCursor.Offset != int64(len(body)) {
		t.Fatalf("cursor=%+v want offset=%d", projection.codexCursor, len(body))
	}
}

func TestProjectAgentTreeResumeAddsAssignmentNotInstance(t *testing.T) {
	start := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	dataset := agentanalytics.ActivityDataset{
		WorkItems:   []agentanalytics.ActivityWorkItem{{ID: "w", Runtime: "claude", StartedAt: &start, EndedAt: &end}},
		Instances:   []agentanalytics.AgentInstance{{ID: "claude:root", Runtime: "claude"}},
		Assignments: []agentanalytics.AgentAssignment{{ID: "w:root", WorkItemID: "w", AgentInstanceID: "claude:root"}},
	}
	projectAgentTree(&dataset, agentFileIdentity{runtime: "claude", sessionID: "root", root: true}, []agentintel.AgentNode{{
		ID: "child", Depth: 1, StartedAt: start.Add(time.Minute), Runtime: "claude",
		Attempts: []agentintel.AgentAttempt{
			{Sequence: 1, StartedAt: start.Add(time.Minute), EndedAt: start.Add(10 * time.Minute), Status: agentintel.AgentError},
			{Sequence: 2, StartedAt: start.Add(20 * time.Minute), EndedAt: start.Add(30 * time.Minute), Status: agentintel.AgentDone},
		},
	}})
	if len(dataset.Instances) != 2 || len(dataset.Assignments) != 3 {
		t.Fatalf("resume changed instance grain: instances=%+v assignments=%+v", dataset.Instances, dataset.Assignments)
	}
	if dataset.Assignments[1].AgentInstanceID != dataset.Assignments[2].AgentInstanceID || dataset.Assignments[1].Attempt != 1 || dataset.Assignments[2].Attempt != 2 {
		t.Fatalf("resume assignments=%+v", dataset.Assignments)
	}
}
