package terminal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/brightman-ai/kit/agentanalytics"
)

var (
	errOutcomeStoreUnavailable = errors.New("agent outcome store unavailable")
	errOutcomeWorkItemMissing  = errors.New("agent work item not found")
	errOutcomeWorkItemOpen     = errors.New("agent work item is not complete")
	errOutcomeStatusInvalid    = errors.New("unsupported human outcome")
)

var agentOutcomeStoreMu sync.Mutex

func agentOutcomeStorePath(dataDir string) string {
	// Outcome is user evidence shared by standalone and embedded shells. Tests keep their
	// default writes beside the temporary reporter unless they explicitly choose a shared home.
	if testRunWithoutIsolatedDeepworkHome() {
		return filepath.Join(dataDir, "agent-outcomes.jsonl")
	}
	return filepath.Join(deepworkHomeDir(), "agent-outcomes.jsonl")
}

func (a *agentReporter) outcomeEvidenceStamp() string {
	if a.outcomePath == "" {
		return ""
	}
	info, err := os.Stat(a.outcomePath)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// RecordHumanOutcome persists the two explicit, low-friction judgements the detail page asks
// for. These are outcome evidence, not a satisfaction score or an inferred attention event.
func (a *agentReporter) RecordHumanOutcome(ctx context.Context, workItemID string, status agentanalytics.OutcomeStatus) (agentanalytics.OutcomeEvidence, error) {
	if strings.TrimSpace(workItemID) == "" || (status != agentanalytics.OutcomeHumanAccepted && status != agentanalytics.OutcomeHumanRework) {
		return agentanalytics.OutcomeEvidence{}, errOutcomeStatusInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.outcomePath == "" {
		return agentanalytics.OutcomeEvidence{}, errOutcomeStoreUnavailable
	}
	dataset := a.refreshLocked(ctx, "30d")
	if err := ctx.Err(); err != nil {
		return agentanalytics.OutcomeEvidence{}, err
	}
	var selected *agentanalytics.ActivityWorkItem
	for i := range dataset.WorkItems {
		if dataset.WorkItems[i].ID == workItemID {
			selected = &dataset.WorkItems[i]
			break
		}
	}
	if selected == nil {
		return agentanalytics.OutcomeEvidence{}, errOutcomeWorkItemMissing
	}
	if selected.Status != agentanalytics.LifecycleCompleted {
		return agentanalytics.OutcomeEvidence{}, errOutcomeWorkItemOpen
	}
	evidence := agentanalytics.OutcomeEvidence{
		WorkItemID: workItemID,
		Source:     "human_feedback.v1",
		OracleKind: "human_acceptance",
		Status:     status,
		At:         a.now().UTC(),
		Ref:        selected.SourceRef,
		Confidence: 1,
	}
	evidence, appended, err := appendHumanOutcomeEvidence(a.outcomePath, evidence)
	if err != nil {
		return agentanalytics.OutcomeEvidence{}, err
	}
	if !appended {
		a.invalidateOutcomeReportsLocked()
		return evidence, nil
	}
	a.invalidateOutcomeReportsLocked()
	return evidence, nil
}

func (a *agentReporter) invalidateOutcomeReportsLocked() {
	// Outcome evidence is overlaid after the transcript dataset memo. Keep the source
	// revision and combined datasets intact; only reports that consumed the old overlay
	// need rebuilding. Usage reports do not depend on outcomes either.
	a.reports = make(map[string]agentReportCacheEntry)
}

func appendHumanOutcomeEvidence(path string, evidence agentanalytics.OutcomeEvidence) (agentanalytics.OutcomeEvidence, bool, error) {
	data, err := json.Marshal(evidence)
	if err != nil {
		return agentanalytics.OutcomeEvidence{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return agentanalytics.OutcomeEvidence{}, false, err
	}
	data = append(data, '\n')
	agentOutcomeStoreMu.Lock()
	defer agentOutcomeStoreMu.Unlock()
	prior, _ := readHumanOutcomeEvidenceLocked(path)
	for _, item := range prior {
		if item.WorkItemID == evidence.WorkItemID && item.Source == evidence.Source && item.Status == evidence.Status {
			return item, false, nil // retrying an acknowledged click must not duplicate evidence
		}
	}
	separator, err := humanOutcomeNeedsSeparator(path)
	if err != nil {
		return agentanalytics.OutcomeEvidence{}, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return agentanalytics.OutcomeEvidence{}, false, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return agentanalytics.OutcomeEvidence{}, false, err
	}
	if separator {
		if n, err := file.Write([]byte{'\n'}); err != nil || n != 1 {
			_ = file.Close()
			if err != nil {
				return agentanalytics.OutcomeEvidence{}, false, err
			}
			return agentanalytics.OutcomeEvidence{}, false, io.ErrShortWrite
		}
	}
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return agentanalytics.OutcomeEvidence{}, false, writeErr
	}
	if closeErr != nil {
		return agentanalytics.OutcomeEvidence{}, false, closeErr
	}
	return evidence, true, nil
}

func humanOutcomeNeedsSeparator(path string) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	var last [1]byte
	n, err := file.ReadAt(last[:], info.Size()-1)
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, io.ErrUnexpectedEOF
	}
	return last[0] != '\n', nil
}

func (a *agentReporter) withHumanOutcomesLocked(dataset agentanalytics.ActivityDataset) agentanalytics.ActivityDataset {
	dataset, _ = a.humanOutcomeSnapshotLocked(dataset)
	return dataset
}

func (a *agentReporter) humanOutcomeSnapshotLocked(dataset agentanalytics.ActivityDataset) (agentanalytics.ActivityDataset, []agentanalytics.OutcomeEvidence) {
	if a.outcomePath == "" {
		return dataset, nil
	}
	evidence, diagnostics := readHumanOutcomeEvidence(a.outcomePath)
	if len(diagnostics) > 0 {
		dataset.IngestDiagnostics = append([]string(nil), dataset.IngestDiagnostics...)
		for _, diagnostic := range diagnostics {
			dataset.IngestDiagnostics = appendUniqueString(dataset.IngestDiagnostics, diagnostic)
		}
		dataset.Projection.State = "partial"
		dataset.Projection.Diagnostics = append([]string(nil), dataset.IngestDiagnostics...)
	}
	if len(evidence) == 0 {
		return dataset, evidence
	}
	// refreshLocked may return a memoized slice. Copy only the work items we project so
	// outcome resolution never mutates the raw transcript memo.
	dataset.WorkItems = append([]agentanalytics.ActivityWorkItem(nil), dataset.WorkItems...)
	byWorkItem := make(map[string][]agentanalytics.OutcomeEvidence)
	for _, item := range evidence {
		byWorkItem[item.WorkItemID] = append(byWorkItem[item.WorkItemID], item)
	}
	for i := range dataset.WorkItems {
		work := &dataset.WorkItems[i]
		if items := byWorkItem[work.ID]; len(items) > 0 {
			work.Outcome = agentanalytics.ResolveOutcome(work.Status, work.TaskProfile, items).Status
		}
	}
	return dataset, evidence
}

func (a *agentReporter) DetailWithOutcomeEvidence(ctx context.Context, window, timezone string, filter agentanalytics.DetailFilter) (agentanalytics.ActivityDetailReport, map[string][]agentanalytics.OutcomeEvidence) {
	a.mu.Lock()
	defer a.mu.Unlock()
	dataset, evidence := a.humanOutcomeSnapshotLocked(a.refreshLocked(ctx, window))
	detail := agentanalytics.BuildActivityDetail(dataset, window, timezone, a.now(), filter)
	wanted := make(map[string]struct{}, len(detail.Tasks))
	for _, task := range detail.Tasks {
		wanted[task.ID] = struct{}{}
	}
	byWorkItem := make(map[string][]agentanalytics.OutcomeEvidence)
	for _, item := range evidence {
		if _, ok := wanted[item.WorkItemID]; ok {
			byWorkItem[item.WorkItemID] = append(byWorkItem[item.WorkItemID], item)
		}
	}
	return detail, byWorkItem
}

type agentDetailWithOutcomeEvidence struct {
	agentanalytics.ActivityDetailReport
	OutcomeEvidence map[string][]agentanalytics.OutcomeEvidence `json:"outcome_evidence,omitempty"`
}

func readHumanOutcomeEvidence(path string) ([]agentanalytics.OutcomeEvidence, []string) {
	agentOutcomeStoreMu.Lock()
	defer agentOutcomeStoreMu.Unlock()
	return readHumanOutcomeEvidenceLocked(path)
}

func readHumanOutcomeEvidenceLocked(path string) ([]agentanalytics.OutcomeEvidence, []string) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{"outcome_evidence_read_failed"}
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var evidence []agentanalytics.OutcomeEvidence
	var diagnostics []string
	seen := make(map[string]struct{})
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(string(scanner.Bytes())) == "" {
			continue // concurrent recovery can leave an extra separator; it is not bad evidence
		}
		var item agentanalytics.OutcomeEvidence
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil || !validHumanOutcomeEvidence(item) {
			diagnostics = append(diagnostics, "outcome_evidence_invalid_line:"+strconv.Itoa(line))
			continue
		}
		key := item.WorkItemID + "\x00" + item.Source + "\x00" + string(item.Status)
		if _, duplicate := seen[key]; duplicate {
			continue // another shell may append the same explicit click at the same time
		}
		seen[key] = struct{}{}
		evidence = append(evidence, item)
	}
	if scanner.Err() != nil {
		diagnostics = append(diagnostics, "outcome_evidence_read_truncated")
	}
	sort.SliceStable(evidence, func(i, j int) bool { return evidence[i].At.Before(evidence[j].At) })
	return evidence, diagnostics
}

func validHumanOutcomeEvidence(item agentanalytics.OutcomeEvidence) bool {
	return item.WorkItemID != "" && item.Source == "human_feedback.v1" &&
		item.OracleKind == "human_acceptance" &&
		(item.Status == agentanalytics.OutcomeHumanAccepted || item.Status == agentanalytics.OutcomeHumanRework) &&
		!item.At.IsZero() && item.Confidence == 1
}

type humanOutcomeRequest struct {
	WorkItemID string                       `json:"work_item_id"`
	Outcome    agentanalytics.OutcomeStatus `json:"outcome"`
}

func (s *Server) handleAgentOutcome(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request humanOutcomeRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_outcome_request"})
		return
	}
	s.mu.Lock()
	reporter := s.agentUsage
	if reporter == nil {
		reporter = newAgentReporter(s.config.DataDir)
		s.agentUsage = reporter
	}
	s.mu.Unlock()
	evidence, err := reporter.RecordHumanOutcome(r.Context(), request.WorkItemID, request.Outcome)
	switch {
	case errors.Is(err, errOutcomeStatusInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, errOutcomeWorkItemMissing):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, errOutcomeWorkItemOpen):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, errOutcomeStoreUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case err != nil:
		logger.Warn("could not persist human outcome evidence", "work_item_id", request.WorkItemID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "outcome_persist_failed"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"evidence": evidence})
	}
}
