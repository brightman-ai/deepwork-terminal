package terminal

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
	"github.com/brightman-ai/kit/agentanalytics"
	"github.com/brightman-ai/kit/transcript"
	kitusage "github.com/brightman-ai/kit/usage"
)

const agentReportCacheTTL = 60 * time.Second
const agentReportIndexSchema = "agent-report-index.v10"

type agentFileProjection struct {
	size         int64
	modUnixNano  int64
	generation   string
	runtime      string
	sessionID    string
	codexCursor  transcript.CodexRequestCursor
	claudeCursor transcript.ClaudeRequestCursor
	dataset      agentanalytics.ActivityDataset
}

// agentReporter is a rebuildable materialized view over provider transcripts.
// Unchanged files are never reparsed. A malformed file contributes a stable
// diagnostic without deleting the last-good facts from other runtimes.
type agentReporter struct {
	mu        sync.Mutex
	files     map[string]agentFileProjection
	reports   map[string]agentReportCacheEntry
	now       func() time.Time
	indexPath string
	drivers   map[string]reportAgentTreeDriver
}

type reportAgentTreeDriver interface {
	Update() error
	AgentTree() []agentintel.AgentNode
}

type agentReportCacheEntry struct {
	report  agentanalytics.ActivityReport
	builtAt time.Time
}

func newAgentReporter(dataDir ...string) *agentReporter {
	a := &agentReporter{files: make(map[string]agentFileProjection), reports: make(map[string]agentReportCacheEntry), drivers: make(map[string]reportAgentTreeDriver), now: time.Now}
	if len(dataDir) > 0 && strings.TrimSpace(dataDir[0]) != "" {
		a.indexPath = filepath.Join(dataDir[0], "agent-report-index-v10.json")
		a.loadIndex()
	}
	return a
}

func (a *agentReporter) Report(ctx context.Context, window, timezone string, force ...bool) agentanalytics.ActivityReport {
	key := window + "|" + timezone
	a.mu.Lock()
	defer a.mu.Unlock()
	forced := len(force) > 0 && force[0]
	if cached, ok := a.reports[key]; !forced && ok && a.now().Sub(cached.builtAt) < agentReportCacheTTL {
		return cached.report
	}
	dataset := a.refreshLocked(ctx, reportCutoff(a.now(), window))
	report := agentanalytics.BuildActivityReport(dataset, window, timezone, a.now())
	a.reports[key] = agentReportCacheEntry{report: report, builtAt: a.now()}
	return report
}

func (a *agentReporter) Dataset(ctx context.Context, window string) agentanalytics.ActivityDataset {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshLocked(ctx, reportCutoff(a.now(), window))
}

func (a *agentReporter) Detail(ctx context.Context, window, timezone string, filter agentanalytics.DetailFilter) agentanalytics.ActivityDetailReport {
	a.mu.Lock()
	defer a.mu.Unlock()
	dataset := a.refreshLocked(ctx, reportCutoff(a.now(), window))
	return agentanalytics.BuildActivityDetail(dataset, window, timezone, a.now(), filter)
}

func (a *agentReporter) refreshLocked(ctx context.Context, cutoff time.Time) agentanalytics.ActivityDataset {
	paths := recentAgentTranscriptFiles(cutoff)
	live := make(map[string]struct{}, len(paths))
	hadProjection := len(a.files) > 0
	changedFiles := 0
	var sourceHighWatermark time.Time
	changed := false
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			break
		}
		live[path] = struct{}{}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().After(sourceHighWatermark) {
			sourceHighWatermark = info.ModTime()
		}
		cached, ok := a.files[path]
		if ok && cached.size == info.Size() && cached.modUnixNano == info.ModTime().UnixNano() {
			continue
		}
		projection, parseErr := a.projectChangedFile(ctx, path, cutoff, cached, ok)
		if parseErr != nil {
			if ok {
				// Preserve the prior projection; one partial append/malformed line must
				// not erase last-good history.
				cached.dataset.IngestDiagnostics = appendUniqueString(cached.dataset.IngestDiagnostics, "transcript_parse_failed:"+filepath.Base(path))
				a.files[path] = cached
				continue
			}
			projection.dataset.IngestDiagnostics = append(projection.dataset.IngestDiagnostics, "transcript_parse_failed:"+filepath.Base(path))
		}
		projection.size = info.Size()
		projection.modUnixNano = info.ModTime().UnixNano()
		a.files[path] = projection
		changed = true
		changedFiles++
	}
	if changed {
		a.saveIndexLocked()
	}
	var combined agentanalytics.ActivityDataset
	for path := range live {
		cached, ok := a.files[path]
		if !ok {
			continue
		}
		// RequestFacts are the durable evidence. Economic Requests are a
		// catalog-versioned projection and must be rebuilt even when the source
		// transcript is unchanged; otherwise a newly published exact price stays
		// invisible until that transcript happens to be appended or the whole
		// materialized index is deleted.
		rebuildEconomicRequests(&cached.dataset)
		a.files[path] = cached
		combined.WorkItems = append(combined.WorkItems, cached.dataset.WorkItems...)
		combined.Assignments = append(combined.Assignments, cached.dataset.Assignments...)
		combined.Instances = append(combined.Instances, cached.dataset.Instances...)
		combined.Requests = append(combined.Requests, cached.dataset.Requests...)
		combined.RequestFacts = append(combined.RequestFacts, cached.dataset.RequestFacts...)
		combined.Artifacts = append(combined.Artifacts, cached.dataset.Artifacts...)
		combined.Tools = append(combined.Tools, cached.dataset.Tools...)
		combined.IngestDiagnostics = append(combined.IngestDiagnostics, cached.dataset.IngestDiagnostics...)
	}
	sort.Strings(combined.IngestDiagnostics)
	mode := "cached"
	if changedFiles > 0 {
		mode = "incremental"
		if !hadProjection {
			mode = "full_rebuild"
		}
	}
	projectionState := "complete"
	if len(combined.IngestDiagnostics) > 0 {
		projectionState = "partial"
	}
	combined.Projection = agentanalytics.ProjectionObservability{
		State: projectionState, Mode: mode, RefreshedAt: a.now(), SourceFiles: len(live), ChangedFiles: changedFiles,
		Diagnostics: append([]string(nil), combined.IngestDiagnostics...),
	}
	if !sourceHighWatermark.IsZero() {
		combined.Projection.SourceHighWatermark = &sourceHighWatermark
	}
	if a.indexPath != "" {
		combined.Projection.IndexSchema = agentReportIndexSchema
	}
	return combined
}

func reportCutoff(now time.Time, window string) time.Time {
	days := 2
	switch window {
	case "7d":
		days = 8
	case "14d":
		days = 15
	case "30d":
		days = 31
	}
	return now.AddDate(0, 0, -days)
}

type persistedAgentIndex struct {
	Schema string                                  `json:"schema"`
	Files  map[string]persistedAgentFileProjection `json:"files"`
}

type persistedAgentFileProjection struct {
	Size         int64                          `json:"size"`
	ModUnixNano  int64                          `json:"mod_unix_nano"`
	Generation   string                         `json:"generation"`
	Runtime      string                         `json:"runtime"`
	SessionID    string                         `json:"session_id"`
	CodexCursor  transcript.CodexRequestCursor  `json:"codex_cursor"`
	ClaudeCursor transcript.ClaudeRequestCursor `json:"claude_cursor"`
	Dataset      agentanalytics.ActivityDataset `json:"dataset"`
}

func (a *agentReporter) loadIndex() {
	body, err := os.ReadFile(a.indexPath)
	if err != nil {
		return
	}
	var persisted persistedAgentIndex
	if json.Unmarshal(body, &persisted) != nil || persisted.Schema != agentReportIndexSchema {
		return
	}
	for path, file := range persisted.Files {
		a.files[path] = agentFileProjection{
			size: file.Size, modUnixNano: file.ModUnixNano, generation: file.Generation,
			runtime: file.Runtime, sessionID: file.SessionID, codexCursor: file.CodexCursor,
			claudeCursor: file.ClaudeCursor, dataset: file.Dataset,
		}
	}
}

func (a *agentReporter) saveIndexLocked() {
	if a.indexPath == "" {
		return
	}
	persisted := persistedAgentIndex{Schema: agentReportIndexSchema, Files: make(map[string]persistedAgentFileProjection, len(a.files))}
	for path, file := range a.files {
		persisted.Files[path] = persistedAgentFileProjection{
			Size: file.size, ModUnixNano: file.modUnixNano, Generation: file.generation,
			Runtime: file.runtime, SessionID: file.sessionID, CodexCursor: file.codexCursor,
			ClaudeCursor: file.claudeCursor, Dataset: file.dataset,
		}
	}
	body, err := json.Marshal(persisted)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(a.indexPath), 0o700) != nil {
		return
	}
	tmp := a.indexPath + ".tmp"
	if os.WriteFile(tmp, body, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, a.indexPath)
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func recentAgentTranscriptFiles(cutoff time.Time) []string {
	roots := []string{transcript.ClaudeProjectsRoot(), transcript.CodexSessionsRoot()}
	var paths []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() || filepath.Ext(path) != transcript.JSONLSuffix {
				return nil
			}
			if info, statErr := entry.Info(); statErr == nil && !info.ModTime().Before(cutoff) {
				paths = append(paths, path)
			}
			return nil
		})
	}
	sort.Strings(paths)
	return paths
}

type agentFileIdentity struct {
	runtime   string
	sessionID string
	project   string
	agentID   string
	root      bool
}

func inspectAgentFile(path string) (agentFileIdentity, error) {
	f, err := os.Open(path)
	if err != nil {
		return agentFileIdentity{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	if !sc.Scan() {
		return agentFileIdentity{}, sc.Err()
	}
	if pathWithinRoot(path, transcript.CodexSessionsRoot()) {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				ID     string          `json:"id"`
				CWD    string          `json:"cwd"`
				Source json.RawMessage `json:"source"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil || row.Type != "session_meta" || row.Payload.ID == "" {
			return agentFileIdentity{}, os.ErrInvalid
		}
		root := true
		var source map[string]json.RawMessage
		if json.Unmarshal(row.Payload.Source, &source) == nil {
			_, isSubagent := source["subagent"]
			root = !isSubagent
		}
		return agentFileIdentity{runtime: transcript.KindCodex, sessionID: row.Payload.ID, project: row.Payload.CWD, root: root}, nil
	}
	var row struct {
		SessionID   string `json:"sessionId"`
		IsSidechain bool   `json:"isSidechain"`
		AgentID     string `json:"agentId"`
		CWD         string `json:"cwd"`
	}
	// Claude can prepend queue/control rows before the first conversational
	// event. Inspect a bounded prefix and merge identity fields instead of
	// letting an incidental first row decide root/sidechain ownership.
	for i := 0; i < 32; i++ {
		var candidate struct {
			SessionID   string `json:"sessionId"`
			IsSidechain bool   `json:"isSidechain"`
			AgentID     string `json:"agentId"`
			CWD         string `json:"cwd"`
		}
		if json.Unmarshal(sc.Bytes(), &candidate) == nil {
			if row.SessionID == "" {
				row.SessionID = candidate.SessionID
			}
			if row.AgentID == "" {
				row.AgentID = candidate.AgentID
			}
			if row.CWD == "" {
				row.CWD = candidate.CWD
			}
			row.IsSidechain = row.IsSidechain || candidate.IsSidechain
		}
		if !sc.Scan() {
			break
		}
	}
	id := row.SessionID
	if row.IsSidechain && row.AgentID != "" {
		id = row.AgentID
	}
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return agentFileIdentity{runtime: transcript.KindClaude, sessionID: id, project: row.CWD, agentID: row.AgentID, root: !row.IsSidechain}, nil
}

func (a *agentReporter) projectChangedFile(ctx context.Context, path string, cutoff time.Time, cached agentFileProjection, exists bool) (agentFileProjection, error) {
	identity, err := inspectAgentFile(path)
	if err != nil {
		return agentFileProjection{}, err
	}
	if exists && cached.runtime == identity.runtime && cached.sessionID == identity.sessionID && cached.generation != "" {
		if info, statErr := os.Stat(path); statErr == nil && info.Size() >= cached.size {
			updated, reset, appendErr := appendAgentFileProjection(ctx, path, identity, cached, cutoff)
			if appendErr != nil {
				return agentFileProjection{}, appendErr
			}
			if !reset {
				a.applyAgentTree(path, identity, &updated.dataset)
				return updated, nil
			}
		}
	}
	projection, err := projectAgentFileProjection(ctx, path, identity, cutoff)
	if err == nil {
		a.applyAgentTree(path, identity, &projection.dataset)
	}
	return projection, err
}

func (a *agentReporter) applyAgentTree(path string, identity agentFileIdentity, dataset *agentanalytics.ActivityDataset) {
	if !identity.root || dataset == nil {
		return
	}
	driver := a.drivers[path]
	if driver == nil {
		switch identity.runtime {
		case transcript.KindCodex:
			driver = agentintel.NewCodexDriver(path)
		case transcript.KindClaude:
			driver = agentintel.NewClaudeDriver(path, identity.sessionID)
		}
		if driver != nil {
			a.drivers[path] = driver
		}
	}
	if driver == nil {
		return
	}
	if err := driver.Update(); err != nil {
		dataset.IngestDiagnostics = appendUniqueString(dataset.IngestDiagnostics, "agent_tree_parse_failed:"+filepath.Base(path))
		return
	}
	projectAgentTree(dataset, identity, driver.AgentTree())
}

func projectAgentTree(dataset *agentanalytics.ActivityDataset, identity agentFileIdentity, nodes []agentintel.AgentNode) {
	rootID := identity.runtime + ":" + identity.sessionID
	dataset.Assignments = filterAssignments(dataset.Assignments, func(assignment agentanalytics.AgentAssignment) bool {
		return assignment.AgentInstanceID == rootID
	})
	dataset.Instances = filterInstances(dataset.Instances, func(instance agentanalytics.AgentInstance) bool {
		return instance.ID == rootID
	})
	for _, node := range nodes {
		instanceID := rootID + ":" + node.ID
		parentID := rootID
		if node.ParentID != "" {
			parentID = rootID + ":" + node.ParentID
		}
		dataset.Instances = append(dataset.Instances, agentanalytics.AgentInstance{
			ID: instanceID, Runtime: identity.runtime, ThreadID: node.ID, ParentInstanceID: parentID,
			Depth: node.Depth, SourceRef: node.SourceRef,
		})
		if len(node.Attempts) == 0 {
			workID := workItemAt(dataset.WorkItems, node.StartedAt)
			if workID == "" {
				dataset.IngestDiagnostics = appendUniqueString(dataset.IngestDiagnostics, "agent_assignment_unattributed:"+node.ID)
				continue
			}
			dataset.Assignments = append(dataset.Assignments, agentanalytics.AgentAssignment{
				ID: instanceID + ":attempt:0", WorkItemID: workID, AgentInstanceID: instanceID,
				Status: agentanalytics.LifecycleNeverStarted, SubmittedAt: node.StartedAt,
			})
			continue
		}
		for _, attempt := range node.Attempts {
			workID := workItemAt(dataset.WorkItems, attempt.StartedAt)
			if workID == "" {
				dataset.IngestDiagnostics = appendUniqueString(dataset.IngestDiagnostics, "agent_assignment_unattributed:"+node.ID)
				continue
			}
			started := attempt.StartedAt
			var ended *time.Time
			if !attempt.EndedAt.IsZero() {
				value := attempt.EndedAt
				ended = &value
			}
			dataset.Assignments = append(dataset.Assignments, agentanalytics.AgentAssignment{
				ID: instanceID + ":attempt:" + strconv.Itoa(attempt.Sequence), WorkItemID: workID,
				AgentInstanceID: instanceID, Attempt: attempt.Sequence, Status: analyticsAgentNodeStatus(attempt.Status),
				SubmittedAt: node.StartedAt, StartedAt: &started, EndedAt: ended,
			})
		}
	}
}

func analyticsAgentNodeStatus(status agentintel.AgentNodeStatus) agentanalytics.LifecycleStatus {
	switch status {
	case agentintel.AgentDone:
		return agentanalytics.LifecycleCompleted
	case agentintel.AgentError:
		return agentanalytics.LifecycleError
	case agentintel.AgentRunning:
		return agentanalytics.LifecycleStarted
	default:
		return agentanalytics.LifecycleOpen
	}
}

func filterAssignments(values []agentanalytics.AgentAssignment, keep func(agentanalytics.AgentAssignment) bool) []agentanalytics.AgentAssignment {
	out := values[:0]
	for _, value := range values {
		if keep(value) {
			out = append(out, value)
		}
	}
	return out
}

func filterInstances(values []agentanalytics.AgentInstance, keep func(agentanalytics.AgentInstance) bool) []agentanalytics.AgentInstance {
	out := values[:0]
	for _, value := range values {
		if keep(value) {
			out = append(out, value)
		}
	}
	return out
}

// projectAgentFile remains the small, test-friendly full projection boundary.
// Production refreshes use projectChangedFile so large append-only rollouts only
// parse newly appended request rows and a bounded run tail.
func projectAgentFile(ctx context.Context, path string) (agentanalytics.ActivityDataset, error) {
	identity, err := inspectAgentFile(path)
	if err != nil || !identity.root {
		return agentanalytics.ActivityDataset{}, err
	}
	projection, err := projectAgentFileProjection(ctx, path, identity, time.Time{})
	return projection.dataset, err
}

func projectAgentFileProjection(ctx context.Context, path string, identity agentFileIdentity, cutoff time.Time) (agentFileProjection, error) {
	var runs []transcript.AgentRun
	generation := identity.sessionID
	var err error
	if identity.root {
		runs, generation, _, err = loadAgentRunTail(ctx, path, identity, cutoff, nil, "")
		if err != nil {
			return agentFileProjection{}, err
		}
	}
	projection := agentFileProjection{generation: generation, runtime: identity.runtime, sessionID: identity.sessionID}
	var facts []transcript.ModelRequestUsage
	switch identity.runtime {
	case transcript.KindCodex:
		cursor := transcript.CodexRequestCursor{}
		if !identity.root {
			// Codex may materialize a fork by copying the complete parent rollout
			// after the child's session_meta. Those rows are context, not new child
			// requests. Start at the child's own task_started event so parent token
			// facts are never charged twice.
			cursor.Offset, err = codexSubagentOwnStartOffset(path, identity.sessionID)
			if err != nil {
				return agentFileProjection{}, err
			}
			cursor.SessionID = identity.sessionID
			cursor.Provider = "openai"
		}
		facts, projection.codexCursor, err = transcript.ScanCodexRequestUsageIncremental(path, cursor)
	case transcript.KindClaude:
		facts, projection.claudeCursor, err = transcript.ScanClaudeRequestUsageIncremental(path, transcript.ClaudeRequestCursor{})
	}
	if err != nil {
		return agentFileProjection{}, err
	}
	projection.dataset = projectAgentDataset(identity, path, runs)
	projection.dataset.RequestFacts = facts
	if projection.codexCursor.MalformedLines > 0 || projection.claudeCursor.MalformedLines > 0 {
		projection.dataset.IngestDiagnostics = append(projection.dataset.IngestDiagnostics, "transcript_malformed:"+filepath.Base(path))
	}
	rebuildEconomicRequests(&projection.dataset)
	return projection, nil
}

// codexSubagentOwnStartOffset returns the first byte owned by the child
// session. A direct child rollout has no embedded session_meta and starts at
// byte zero. A fork snapshot has a second session_meta followed by inherited
// parent events; the child's own UUIDv7 task is the authoritative boundary.
func codexSubagentOwnStartOffset(path, sessionID string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sessionMillis, validSessionID := uuidV7Millis(sessionID)
	reader := bufio.NewReader(f)
	var offset int64
	inherited := false
	for {
		lineStart := offset
		line, readErr := reader.ReadBytes('\n')
		offset += int64(len(line))
		if len(line) > 0 {
			var row struct {
				Type    string `json:"type"`
				Payload struct {
					Type   string `json:"type"`
					ID     string `json:"id"`
					TurnID string `json:"turn_id"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &row) == nil {
				if row.Type == "session_meta" && row.Payload.ID != "" && row.Payload.ID != sessionID {
					inherited = true
				}
				if inherited && row.Type == "event_msg" && row.Payload.Type == "task_started" && validSessionID {
					if taskMillis, ok := uuidV7Millis(row.Payload.TurnID); ok && taskMillis >= sessionMillis && taskMillis-sessionMillis <= int64((10*time.Minute)/time.Millisecond) {
						return lineStart, nil
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return 0, readErr
		}
	}
	if !inherited {
		return 0, nil
	}
	return 0, fmt.Errorf("codex child boundary not found: %s", filepath.Base(path))
}

func uuidV7Millis(value string) (int64, bool) {
	hex := strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	if len(hex) < 12 {
		return 0, false
	}
	millis, err := strconv.ParseInt(hex[:12], 16, 64)
	return millis, err == nil
}

func appendAgentFileProjection(ctx context.Context, path string, identity agentFileIdentity, cached agentFileProjection, cutoff time.Time) (agentFileProjection, bool, error) {
	stopIDs := make(map[string]struct{}, len(cached.dataset.WorkItems))
	for _, work := range cached.dataset.WorkItems {
		stopIDs[work.ID] = struct{}{}
	}
	var runs []transcript.AgentRun
	generation, reset := cached.generation, false
	var err error
	if identity.root {
		runs, generation, reset, err = loadAgentRunTail(ctx, path, identity, cutoff, stopIDs, cached.generation)
		if err != nil || reset {
			return agentFileProjection{}, reset, err
		}
	}

	updated := cached
	updated.generation = generation
	var appended []transcript.ModelRequestUsage
	switch identity.runtime {
	case transcript.KindCodex:
		appended, updated.codexCursor, err = transcript.ScanCodexRequestUsageIncremental(path, cached.codexCursor)
	case transcript.KindClaude:
		appended, updated.claudeCursor, err = transcript.ScanClaudeRequestUsageIncremental(path, cached.claudeCursor)
	}
	if err != nil {
		return agentFileProjection{}, false, err
	}
	tail := projectAgentDataset(identity, path, runs)
	updated.dataset = mergeAgentRunDataset(cached.dataset, tail)
	updated.dataset.RequestFacts = mergeRequestFacts(cached.dataset.RequestFacts, appended)
	if updated.codexCursor.MalformedLines > cached.codexCursor.MalformedLines || updated.claudeCursor.MalformedLines > cached.claudeCursor.MalformedLines {
		updated.dataset.IngestDiagnostics = appendUniqueString(updated.dataset.IngestDiagnostics, "transcript_malformed:"+filepath.Base(path))
	}
	rebuildEconomicRequests(&updated.dataset)
	return updated, false, nil
}

func loadAgentRunTail(ctx context.Context, path string, identity agentFileIdentity, cutoff time.Time, stopIDs map[string]struct{}, generation string) ([]transcript.AgentRun, string, bool, error) {
	var before *int64
	byID := make(map[string]transcript.AgentRun)
	currentGeneration := generation
	reset := false
	for {
		req := transcript.WindowRequest{Before: before, Limit: 50, Generation: currentGeneration}
		var window *transcript.WindowResult
		var err error
		switch identity.runtime {
		case transcript.KindCodex:
			window, err = transcript.NewCodexSource().LoadTranscriptFileWindow(ctx, path, identity.sessionID, req)
		case transcript.KindClaude:
			window, err = transcript.NewClaudeSource().LoadTranscriptFileWindow(ctx, path, identity.sessionID, req)
		default:
			return nil, "", false, os.ErrInvalid
		}
		if err != nil {
			return nil, "", false, err
		}
		if before == nil {
			reset = window.Reset
			currentGeneration = window.Generation
		}
		overlapped, reachedCutoff := false, false
		if window.Transcript != nil {
			for _, run := range window.Transcript.Runs {
				byID[run.ID] = run
				if _, ok := stopIDs[activityWorkItemID(identity, run)]; ok {
					overlapped = true
				}
				at := run.StartedAt
				if at == nil {
					at = run.EndedAt
				}
				if !cutoff.IsZero() && at != nil && at.Before(cutoff) {
					reachedCutoff = true
				}
			}
		}
		if overlapped || reachedCutoff || !window.HasMore {
			break
		}
		if before != nil && *before == window.Before {
			break
		}
		next := window.Before
		before = &next
	}
	runs := make([]transcript.AgentRun, 0, len(byID))
	for _, run := range byID {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		ai, aj := agentRunTime(runs[i]), agentRunTime(runs[j])
		if ai.Equal(aj) {
			return runs[i].ID < runs[j].ID
		}
		return ai.Before(aj)
	})
	return runs, currentGeneration, reset, nil
}

func agentRunTime(run transcript.AgentRun) time.Time {
	if run.StartedAt != nil {
		return *run.StartedAt
	}
	if run.EndedAt != nil {
		return *run.EndedAt
	}
	return time.Time{}
}

func projectAgentDataset(identity agentFileIdentity, path string, runs []transcript.AgentRun) agentanalytics.ActivityDataset {
	dataset := agentanalytics.ActivityDataset{}
	if !identity.root {
		return dataset
	}
	rootInstanceID := identity.runtime + ":" + identity.sessionID
	dataset.Instances = append(dataset.Instances, agentanalytics.AgentInstance{ID: rootInstanceID, Runtime: identity.runtime, ThreadID: identity.sessionID, SourceRef: path})
	for _, run := range runs {
		status := analyticsLifecycle(run.Status)
		intent := ""
		if run.UserIntent != nil {
			intent = run.UserIntent.Text
		}
		profile := agentanalytics.ProfileOpeningIntent(identity.project, intent)
		outcome := agentanalytics.ResolveOutcome(status, profile, nil).Status
		work := agentanalytics.ActivityWorkItem{
			ID: activityWorkItemID(identity, run), Runtime: identity.runtime, Project: identity.project,
			TaskProfile: profile,
			SourceRef:   path, Status: status, StartedAt: run.StartedAt, EndedAt: run.EndedAt, Outcome: outcome,
		}
		if run.Usage != nil {
			work.OutputTokens = int64(run.Usage.OutputTokens)
		}
		assignment := agentanalytics.AgentAssignment{ID: work.ID + ":root", WorkItemID: work.ID, AgentInstanceID: rootInstanceID, Root: true, Attempt: 1, Status: status, StartedAt: run.StartedAt, EndedAt: run.EndedAt}
		if run.StartedAt != nil {
			assignment.SubmittedAt = *run.StartedAt
		}
		dataset.Assignments = append(dataset.Assignments, assignment)
		seenTools := make(map[string]struct{})
		for blockIndex, block := range run.Segments {
			if block.Type == transcript.BlockTool || block.Type == transcript.BlockAgent {
				toolID := agentToolID(identity, block, blockIndex)
				if _, duplicate := seenTools[toolID]; duplicate {
					continue
				}
				seenTools[toolID] = struct{}{}
				work.ToolCalls++
				dataset.Tools = append(dataset.Tools, projectToolExecution(identity, path, work.ID, toolID, status, block))
				dataset.Artifacts = append(dataset.Artifacts, projectArtifactDeltas(work.ID, toolID, block)...)
			}
			if block.Type == transcript.BlockAgent {
				instanceID := rootInstanceID + ":" + firstAgentBlockID(block)
				dataset.Instances = append(dataset.Instances, agentanalytics.AgentInstance{ID: instanceID, Runtime: identity.runtime, ThreadID: instanceID, ParentInstanceID: rootInstanceID, Depth: 1, SourceRef: path})
				dataset.Assignments = append(dataset.Assignments, agentanalytics.AgentAssignment{ID: work.ID + ":" + firstAgentBlockID(block), WorkItemID: work.ID, AgentInstanceID: instanceID, Attempt: 1, Status: analyticsAgentAssignmentStatus(block, status)})
			}
		}
		dataset.WorkItems = append(dataset.WorkItems, work)
	}
	return dataset
}

func projectToolExecution(identity agentFileIdentity, sourceRef, workItemID, id string, runStatus agentanalytics.LifecycleStatus, block transcript.Block) agentanalytics.ToolExecution {
	tool := agentanalytics.ToolExecution{
		ID:         id,
		WorkItemID: workItemID, Runtime: identity.runtime, Name: block.ToolName,
		Status: analyticsToolStatus(block, runStatus), StartedAt: block.StartedAt, EndedAt: block.EndedAt, SourceRef: sourceRef,
	}
	if (tool.Status == agentanalytics.ToolExecutionCompleted || tool.Status == agentanalytics.ToolExecutionError) && block.DurationMs > 0 {
		seconds := float64(block.DurationMs) / 1000
		tool.DurationSeconds = &seconds
	} else if (tool.Status == agentanalytics.ToolExecutionCompleted || tool.Status == agentanalytics.ToolExecutionError) && block.StartedAt != nil && block.EndedAt != nil && block.EndedAt.After(*block.StartedAt) {
		seconds := block.EndedAt.Sub(*block.StartedAt).Seconds()
		tool.DurationSeconds = &seconds
	}
	return tool
}

func projectArtifactDeltas(workItemID, sourceToolID string, block transcript.Block) []agentanalytics.ArtifactDelta {
	if !block.ResultSeen || block.IsError {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(block.ToolName))
	if name == "apply_patch" {
		raw, _ := block.ToolInput["input"].(string)
		return projectApplyPatchArtifacts(workItemID, sourceToolID, raw, toolBlockTime(block))
	}
	if name == "exec" {
		raw, _ := block.ToolInput["input"].(string)
		if patch, ok := codexApplyPatchBridgeInput(raw); ok {
			return projectApplyPatchArtifacts(workItemID, sourceToolID, patch, toolBlockTime(block))
		}
	}
	path := firstToolInputString(block.ToolInput, "file_path", "path")
	if path == "" {
		return nil
	}
	kind, excluded, excludeReason := agentanalytics.ClassifyArtifact(path)
	at := toolBlockTime(block)
	delta := agentanalytics.ArtifactDelta{
		ID: sourceToolID, SourceToolID: sourceToolID, WorkItemID: workItemID, Path: path, Kind: kind, Attribution: agentanalytics.AttributionProviderPatch,
		At: at, Excluded: excluded, ExcludeReason: excludeReason,
	}
	switch name {
	case "edit", "editfile", "edit_file":
		oldText, oldOK := block.ToolInput["old_string"].(string)
		newText, newOK := block.ToolInput["new_string"].(string)
		if !oldOK || !newOK || oldText == "" {
			return nil
		}
		delta.Operation = "modify"
		delta.Additions, delta.Deletions = agentanalytics.ChangedLineCounts(oldText, newText)
		delta.WrittenLines = agentanalytics.ContentLineCount(newText)
		delta.ChangeCoverage = "complete"
		replaceAll, _ := block.ToolInput["replace_all"].(bool)
		if replaceAll {
			delta.ChangeCoverage = "partial"
			delta.Diagnostics = []string{"edit_replace_all_count_unknown"}
		}
		return []agentanalytics.ArtifactDelta{delta}
	case "write", "writefile", "write_file":
		content, ok := block.ToolInput["content"].(string)
		if !ok {
			return nil
		}
		delta.WrittenLines = agentanalytics.ContentLineCount(content)
		result := strings.ToLower(block.ToolResult)
		if strings.Contains(result, "file created successfully") {
			delta.Operation = "create"
			delta.Additions = delta.WrittenLines
			delta.ChangeCoverage = "complete"
		} else {
			delta.Operation = "modify"
			delta.ChangeCoverage = "missing"
			delta.Diagnostics = []string{"write_previous_content_unknown"}
		}
		return []agentanalytics.ArtifactDelta{delta}
	default:
		return nil
	}
}

// codexApplyPatchBridgeInput recognizes the exact generated JS bridge used by
// the Codex tool transport when apply_patch is nested under exec. It decodes a
// JSON string literal but never evaluates JavaScript and never parses generic
// exec/exec_command/Bash text as file evidence.
func codexApplyPatchBridgeInput(raw string) (string, bool) {
	const prefix = "const patch = "
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(raw, prefix)
	decoder := json.NewDecoder(strings.NewReader(rest))
	var patch string
	if err := decoder.Decode(&patch); err != nil {
		return "", false
	}
	tail := strings.TrimSpace(rest[decoder.InputOffset():])
	tail = strings.TrimSpace(strings.TrimPrefix(tail, ";"))
	if tail != "text(await tools.apply_patch(patch));" {
		return "", false
	}
	if !strings.HasPrefix(strings.TrimSpace(patch), "*** Begin Patch") || !strings.HasSuffix(strings.TrimSpace(patch), "*** End Patch") {
		return "", false
	}
	return patch, true
}

func projectApplyPatchArtifacts(workItemID, sourceToolID, patch string, at time.Time) []agentanalytics.ArtifactDelta {
	type patchDelta struct {
		path, operation      string
		additions, deletions int64
	}
	var current *patchDelta
	parsed := make([]patchDelta, 0, 2)
	flush := func() {
		if current != nil && strings.TrimSpace(current.path) != "" {
			parsed = append(parsed, *current)
		}
		current = nil
	}
	for _, raw := range strings.Split(patch, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "*** Add File:"):
			flush()
			current = &patchDelta{path: strings.TrimSpace(strings.TrimPrefix(line, "*** Add File:")), operation: "create"}
		case strings.HasPrefix(line, "*** Update File:"):
			flush()
			current = &patchDelta{path: strings.TrimSpace(strings.TrimPrefix(line, "*** Update File:")), operation: "modify"}
		case strings.HasPrefix(line, "*** Delete File:"):
			flush()
			current = &patchDelta{path: strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File:")), operation: "delete"}
		case strings.HasPrefix(line, "*** Move to:"):
			if current != nil {
				current.path = strings.TrimSpace(strings.TrimPrefix(line, "*** Move to:"))
			}
		case current != nil && strings.HasPrefix(raw, "+"):
			current.additions++
		case current != nil && strings.HasPrefix(raw, "-"):
			current.deletions++
		}
	}
	flush()
	out := make([]agentanalytics.ArtifactDelta, 0, len(parsed))
	for i, change := range parsed {
		kind, excluded, excludeReason := agentanalytics.ClassifyArtifact(change.path)
		coverage := "complete"
		diagnostics := []string(nil)
		if change.operation == "delete" && change.deletions == 0 {
			coverage = "missing"
			diagnostics = []string{"apply_patch_deleted_content_not_embedded"}
		}
		out = append(out, agentanalytics.ArtifactDelta{
			ID: fmt.Sprintf("%s:artifact:%d", sourceToolID, i), SourceToolID: sourceToolID,
			WorkItemID: workItemID, Path: change.path, Kind: kind, Operation: change.operation,
			Additions: change.additions, Deletions: change.deletions, WrittenLines: change.additions,
			ChangeCoverage: coverage, Attribution: agentanalytics.AttributionProviderPatch, At: at,
			Excluded: excluded, ExcludeReason: excludeReason, Diagnostics: diagnostics,
		})
	}
	return out
}

func firstToolInputString(input map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func toolBlockTime(block transcript.Block) time.Time {
	if block.EndedAt != nil {
		return *block.EndedAt
	}
	if block.StartedAt != nil {
		return *block.StartedAt
	}
	return time.Time{}
}

// activityWorkItemID is independent of a paged projector's local ordinal. The
// same human intent therefore keeps one identity whether loaded from the full
// transcript, a bounded tail, or after an append.
func activityWorkItemID(identity agentFileIdentity, run transcript.AgentRun) string {
	at := agentRunTime(run)
	intent := "system"
	if run.UserIntent != nil && strings.TrimSpace(run.UserIntent.Text) != "" {
		intent = strings.TrimSpace(run.UserIntent.Text)
	}
	sum := sha256.Sum256([]byte(intent))
	if at.IsZero() {
		return fmt.Sprintf("%s:%s:work:%s:%x", identity.runtime, identity.sessionID, run.ID, sum[:6])
	}
	return fmt.Sprintf("%s:%s:work:%d:%x", identity.runtime, identity.sessionID, at.UnixNano(), sum[:6])
}

func rebuildEconomicRequests(dataset *agentanalytics.ActivityDataset) {
	dataset.Requests = dataset.Requests[:0]
	for _, fact := range dataset.RequestFacts {
		projection := kitusage.ProjectRequestCost(fact)
		request := agentanalytics.EconomicRequest{ID: fact.ID, Runtime: fact.Runtime, Model: fact.Model, Effort: fact.Effort, ServiceTier: fact.ServiceTier, At: fact.At, OutputTokens: fact.OutputTokens, TokenComplete: fact.Coverage.Tokens == transcript.CoverageComplete, CostComplete: projection.Complete}
		request.WorkItemID = workItemAt(dataset.WorkItems, fact.At)
		if projection.APIEquivalent != nil {
			request.APIEquivalent = &agentanalytics.Money{Amount: *projection.APIEquivalent, Currency: projection.Currency}
		}
		if projection.Credits != nil {
			value := *projection.Credits
			request.Credits, request.CreditComplete = &value, true
		}
		if projection.FastMultiplier != nil {
			value := *projection.FastMultiplier
			request.FastMultiplier = &value
		}
		request.GenerationRate, request.GenerationDurationSeconds = agentanalytics.TokensPerSecond(fact.OutputTokens, fact.FirstTokenAt, fact.EndedAt)
		request.ObservedResponseRate, request.ObservedResponseDurationSeconds = agentanalytics.TokensPerSecond(fact.OutputTokens, fact.StartedAt, fact.EndedAt)
		request.TTFTSeconds = agentanalytics.ElapsedSeconds(fact.StartedAt, fact.FirstTokenAt)
		request.ObservedFirstResponseSeconds = agentanalytics.ElapsedSeconds(fact.StartedAt, fact.FirstObservedAt)
		dataset.Requests = append(dataset.Requests, request)
	}
}

func mergeAgentRunDataset(base, tail agentanalytics.ActivityDataset) agentanalytics.ActivityDataset {
	updatedWork := make(map[string]struct{}, len(tail.WorkItems))
	for _, work := range tail.WorkItems {
		updatedWork[work.ID] = struct{}{}
	}
	merged := base
	merged.WorkItems = merged.WorkItems[:0]
	for _, work := range base.WorkItems {
		if _, replace := updatedWork[work.ID]; !replace {
			merged.WorkItems = append(merged.WorkItems, work)
		}
	}
	merged.WorkItems = append(merged.WorkItems, tail.WorkItems...)
	sort.Slice(merged.WorkItems, func(i, j int) bool {
		ai := activityWorkTime(merged.WorkItems[i])
		aj := activityWorkTime(merged.WorkItems[j])
		if ai.Equal(aj) {
			return merged.WorkItems[i].ID < merged.WorkItems[j].ID
		}
		return ai.Before(aj)
	})

	merged.Assignments = merged.Assignments[:0]
	for _, assignment := range base.Assignments {
		if _, replace := updatedWork[assignment.WorkItemID]; !replace {
			merged.Assignments = append(merged.Assignments, assignment)
		}
	}
	merged.Assignments = append(merged.Assignments, tail.Assignments...)
	instances := make(map[string]agentanalytics.AgentInstance, len(base.Instances)+len(tail.Instances))
	for _, instance := range base.Instances {
		instances[instance.ID] = instance
	}
	for _, instance := range tail.Instances {
		instances[instance.ID] = instance
	}
	merged.Instances = merged.Instances[:0]
	for _, instance := range instances {
		merged.Instances = append(merged.Instances, instance)
	}
	sort.Slice(merged.Instances, func(i, j int) bool { return merged.Instances[i].ID < merged.Instances[j].ID })
	merged.Artifacts = replaceWorkItemArtifacts(base.Artifacts, tail.Artifacts, updatedWork)
	merged.Tools = replaceWorkItemTools(base.Tools, tail.Tools, updatedWork)
	for _, diagnostic := range tail.IngestDiagnostics {
		merged.IngestDiagnostics = appendUniqueString(merged.IngestDiagnostics, diagnostic)
	}
	return merged
}

func replaceWorkItemArtifacts(base, tail []agentanalytics.ArtifactDelta, updated map[string]struct{}) []agentanalytics.ArtifactDelta {
	out := make([]agentanalytics.ArtifactDelta, 0, len(base)+len(tail))
	for _, value := range base {
		if _, replace := updated[value.WorkItemID]; !replace {
			out = append(out, value)
		}
	}
	return append(out, tail...)
}

func replaceWorkItemTools(base, tail []agentanalytics.ToolExecution, updated map[string]struct{}) []agentanalytics.ToolExecution {
	out := make([]agentanalytics.ToolExecution, 0, len(base)+len(tail))
	for _, value := range base {
		if _, replace := updated[value.WorkItemID]; !replace {
			out = append(out, value)
		}
	}
	return append(out, tail...)
}

func activityWorkTime(work agentanalytics.ActivityWorkItem) time.Time {
	if work.StartedAt != nil {
		return *work.StartedAt
	}
	if work.EndedAt != nil {
		return *work.EndedAt
	}
	return time.Time{}
}

func mergeRequestFacts(base, appended []transcript.ModelRequestUsage) []transcript.ModelRequestUsage {
	byID := make(map[string]transcript.ModelRequestUsage, len(base)+len(appended))
	for _, fact := range base {
		byID[fact.ID] = fact
	}
	for _, fact := range appended {
		if prior, ok := byID[fact.ID]; ok {
			byID[fact.ID] = mergeRequestFact(prior, fact)
		} else {
			byID[fact.ID] = fact
		}
	}
	merged := make([]transcript.ModelRequestUsage, 0, len(byID))
	for _, fact := range byID {
		merged = append(merged, fact)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].At.Equal(merged[j].At) {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].At.Before(merged[j].At)
	})
	return merged
}

func mergeRequestFact(prior, next transcript.ModelRequestUsage) transcript.ModelRequestUsage {
	out := prior
	if out.Model == "" {
		out.Model = next.Model
	}
	if out.Effort == "" {
		out.Effort = next.Effort
	}
	if out.ServiceTier == "" {
		out.ServiceTier = next.ServiceTier
	}
	if out.BillingMode == "" {
		out.BillingMode = next.BillingMode
	}
	if out.AuthMode == "" {
		out.AuthMode = next.AuthMode
	}
	if out.InferenceGeo == "" {
		out.InferenceGeo = next.InferenceGeo
	}
	if out.Speed == "" {
		out.Speed = next.Speed
	}
	out.InputTokens = maxAgentInt64(out.InputTokens, next.InputTokens)
	out.CachedInputTokens = maxAgentInt64(out.CachedInputTokens, next.CachedInputTokens)
	out.CacheWrite5mTokens = maxAgentInt64(out.CacheWrite5mTokens, next.CacheWrite5mTokens)
	out.CacheWrite1hTokens = maxAgentInt64(out.CacheWrite1hTokens, next.CacheWrite1hTokens)
	out.CacheWriteUnknownTokens = maxAgentInt64(out.CacheWriteUnknownTokens, next.CacheWriteUnknownTokens)
	out.OutputTokens = maxAgentInt64(out.OutputTokens, next.OutputTokens)
	out.ReasoningOutputTokens = maxAgentInt64(out.ReasoningOutputTokens, next.ReasoningOutputTokens)
	out.RawInputTokens = maxAgentInt64(out.RawInputTokens, next.RawInputTokens)
	if out.At.IsZero() || (!next.At.IsZero() && next.At.Before(out.At)) {
		out.At = next.At
	}
	if next.EndedAt != nil && (out.EndedAt == nil || next.EndedAt.After(*out.EndedAt)) {
		out.EndedAt = next.EndedAt
	}
	if next.StartedAt != nil && (out.StartedAt == nil || next.StartedAt.Before(*out.StartedAt)) {
		out.StartedAt = next.StartedAt
	}
	if next.FirstObservedAt != nil && (out.FirstObservedAt == nil || next.FirstObservedAt.Before(*out.FirstObservedAt)) {
		out.FirstObservedAt = next.FirstObservedAt
	}
	if next.FirstTokenAt != nil && (out.FirstTokenAt == nil || next.FirstTokenAt.Before(*out.FirstTokenAt)) {
		out.FirstTokenAt = next.FirstTokenAt
	}
	out.Coverage.Identity = strongerCoverage(out.Coverage.Identity, next.Coverage.Identity)
	out.Coverage.Model = strongerCoverage(out.Coverage.Model, next.Coverage.Model)
	out.Coverage.Tokens = strongerCoverage(out.Coverage.Tokens, next.Coverage.Tokens)
	out.Coverage.Effort = strongerCoverage(out.Coverage.Effort, next.Coverage.Effort)
	out.Coverage.Tier = strongerCoverage(out.Coverage.Tier, next.Coverage.Tier)
	out.Coverage.Billing = strongerCoverage(out.Coverage.Billing, next.Coverage.Billing)
	out.Coverage.Timing = strongerCoverage(out.Coverage.Timing, next.Coverage.Timing)
	out.Coverage.CacheTTL = strongerCoverage(out.Coverage.CacheTTL, next.Coverage.CacheTTL)
	for _, diagnostic := range next.Diagnostics {
		out.Diagnostics = appendUniqueString(out.Diagnostics, diagnostic)
	}
	// Invalidation is a monotonic tombstone: a later split row can prove the
	// interval crossed an interrupt and must revoke timing emitted by an earlier
	// incremental batch.
	if out.TimingInvalidated || next.TimingInvalidated {
		out.TimingInvalidated = true
		out.StartedAt = nil
		out.FirstTokenAt = nil
		out.Coverage.Timing = transcript.CoverageMissing
	}
	return out
}

func maxAgentInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func strongerCoverage(a, b transcript.CoverageState) transcript.CoverageState {
	rank := func(v transcript.CoverageState) int {
		switch v {
		case transcript.CoverageComplete:
			return 3
		case transcript.CoveragePartial:
			return 2
		case transcript.CoverageMissing:
			return 1
		default:
			return 0
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func analyticsLifecycle(status string) agentanalytics.LifecycleStatus {
	switch status {
	case transcript.RunCompleted:
		return agentanalytics.LifecycleCompleted
	case transcript.RunInterrupted:
		return agentanalytics.LifecycleInterrupted
	case transcript.RunError:
		return agentanalytics.LifecycleError
	default:
		return agentanalytics.LifecycleOpen
	}
}

func analyticsToolStatus(block transcript.Block, runStatus agentanalytics.LifecycleStatus) agentanalytics.ToolExecutionStatus {
	if block.ResultSeen {
		if block.IsError {
			return agentanalytics.ToolExecutionError
		}
		return agentanalytics.ToolExecutionCompleted
	}
	switch runStatus {
	case agentanalytics.LifecycleInterrupted:
		return agentanalytics.ToolExecutionInterrupted
	case agentanalytics.LifecycleOpen, agentanalytics.LifecycleStarted:
		return agentanalytics.ToolExecutionOpen
	default:
		return agentanalytics.ToolExecutionUnknown
	}
}

func analyticsAgentAssignmentStatus(block transcript.Block, runStatus agentanalytics.LifecycleStatus) agentanalytics.LifecycleStatus {
	if block.ResultSeen {
		if block.IsError {
			return agentanalytics.LifecycleError
		}
		return agentanalytics.LifecycleCompleted
	}
	if runStatus == agentanalytics.LifecycleInterrupted {
		return agentanalytics.LifecycleInterrupted
	}
	if runStatus == agentanalytics.LifecycleError {
		return agentanalytics.LifecycleError
	}
	return agentanalytics.LifecycleOpen
}

func agentToolID(identity agentFileIdentity, block transcript.Block, index int) string {
	id := block.ToolUseID
	if id == "" {
		id = block.EventID
	}
	if id == "" {
		id = fmt.Sprintf("segment-%d", index)
	}
	return identity.runtime + ":" + identity.sessionID + ":tool:" + id
}

func firstAgentBlockID(block transcript.Block) string {
	if block.EventID != "" {
		return block.EventID
	}
	if block.ToolUseID != "" {
		return block.ToolUseID
	}
	if block.TaskID != "" {
		return block.TaskID
	}
	return "unknown"
}

func workItemAt(items []agentanalytics.ActivityWorkItem, at time.Time) string {
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if item.StartedAt == nil || at.Before(*item.StartedAt) {
			continue
		}
		if item.EndedAt == nil || !at.After(*item.EndedAt) {
			return item.ID
		}
	}
	return ""
}

func pathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) handleAgentReport(w http.ResponseWriter, r *http.Request) {
	if s.agentUsage == nil {
		s.mu.Lock()
		if s.agentUsage == nil {
			s.agentUsage = newAgentReporter()
		}
		s.mu.Unlock()
	}
	window := r.URL.Query().Get("window")
	if window != "7d" && window != "14d" && window != "30d" {
		window = "24h"
	}
	timezone := strings.TrimSpace(r.URL.Query().Get("timezone"))
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_timezone"})
		return
	}
	writeJSON(w, http.StatusOK, s.agentUsage.Report(r.Context(), window, timezone, r.URL.Query().Get("refresh") == "1"))
}

func (s *Server) handleAgentReportDetail(w http.ResponseWriter, r *http.Request) {
	if s.agentUsage == nil {
		s.mu.Lock()
		if s.agentUsage == nil {
			s.agentUsage = newAgentReporter()
		}
		s.mu.Unlock()
	}
	window := r.URL.Query().Get("window")
	if window != "7d" && window != "14d" && window != "30d" {
		window = "24h"
	}
	timezone := strings.TrimSpace(r.URL.Query().Get("timezone"))
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_timezone"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	filter := agentanalytics.DetailFilter{
		Project: r.URL.Query().Get("project"), TaskClass: r.URL.Query().Get("task_class"),
		Risk: r.URL.Query().Get("risk"), Outcome: r.URL.Query().Get("outcome"),
		Runtime: r.URL.Query().Get("runtime"), Cursor: r.URL.Query().Get("cursor"), Limit: limit,
	}
	writeJSON(w, http.StatusOK, s.agentUsage.Detail(r.Context(), window, timezone, filter))
}
