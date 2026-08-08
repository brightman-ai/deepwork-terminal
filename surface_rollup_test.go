package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// ── The cross-language contract for one card ─────────────────────────────────────────────────
//
// Aggregation now happens in exactly one place (agentintel.RollUp) and the frontend just reads the
// card. That is the improvement; it is also a new way to be wrong, because "the frontend reads the
// card" is a claim about a seam no compiler crosses. Two suites, one file of vectors:
//
//	this one:                    units + active   --RollUp-->    card
//	surfaceRollupVectors.test.ts   card + identity --cardToUnit--> overview unit
//
// Same examples, opposite halves. Neither side can change its behaviour without the other's
// assertions failing, which is the only form of SSOT available across a language boundary — you
// cannot share the code, so you share the examples.

type rollupVector struct {
	Name string `json:"name"`
	// Active is the index of the focused unit, -1 for none.
	Active int `json:"active"`
	// Units nil marks a card no RollUp can produce (a frame from an older server). Those exist for
	// the TS half's back-compat clauses and are skipped here rather than silently passing as empty.
	Units *[]agentintel.SurfaceUnit `json:"units"`
	Card  agentintel.SurfaceUnit    `json:"card"`
}

func loadRollupVectors(t *testing.T) []rollupVector {
	t.Helper()
	path := filepath.Join("testdata", "surface_rollup_vectors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var file struct {
		Cases []rollupVector `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s has no cases — the contract exists but pins nothing", path)
	}
	return file.Cases
}

func TestRollUp_MatchesTheCrossLanguageVectors(t *testing.T) {
	skipped := 0
	for _, v := range loadRollupVectors(t) {
		if v.Units == nil {
			skipped++
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			got := agentintel.RollUp(*v.Units, v.Active)
			// Compared as WIRE bytes, not as structs: what the TS half receives is the JSON, so an
			// omitzero/omitempty mistake that leaves the Go values equal is exactly the failure this
			// has to catch. Both sides go through Marshal so key order and formatting cannot differ.
			gotJSON, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal got: %v", err)
			}
			wantJSON, err := json.Marshal(v.Card)
			if err != nil {
				t.Fatalf("marshal want: %v", err)
			}
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("roll-up disagrees with the shared vector.\n want: %s\n  got: %s", wantJSON, gotJSON)
			}
		})
	}
	// A vectors file that silently degraded to "everything is skipped" would be green and worthless.
	if skipped == len(loadRollupVectors(t)) {
		t.Fatal("every vector was skipped — no roll-up was actually exercised")
	}
}

// TestRollUp_OneUnitIsIdentity is the load-bearing claim of the whole card layer: a session card
// and a tmux window card can be read by the same code because N=1 is the PLAIN case, not a
// special one. sessions_overview.go relies on it literally — it calls RollUp with one element
// rather than assigning around it — so if this ever stops holding, the non-tmux feed starts lying
// in whatever way the roll-up rounds off.
//
// Combinations rather than one happy example, because the ways identity can break are all edges:
// a unit with a rule and no status (an explicit BEL on a bare shell), an undated wait, a card with
// no agent at all.
func TestRollUp_OneUnitIsIdentity(t *testing.T) {
	dated := time.Date(2026, 8, 8, 9, 0, 0, 0, time.UTC)
	activity := time.Date(2026, 8, 8, 8, 30, 0, 0, time.UTC)

	units := []agentintel.SurfaceUnit{
		{},
		{AgentTool: agentintel.ToolClaude},
		{AgentTool: agentintel.ToolClaude, AgentStatus: agentintel.StatusIdle, StatusRule: "transcript.idle"},
		{AgentTool: agentintel.ToolCodex, AgentStatus: agentintel.StatusRunning, StatusRule: "transcript.running", ActivityAt: activity},
		{AgentTool: agentintel.ToolClaude, AgentStatus: agentintel.StatusWaiting, AwaitingUser: true,
			AwaitingSince: dated, EndedOnQuestion: true, StatusRule: "screen.approval", StatusEvidence: "Allow?"},
		{AgentTool: agentintel.ToolCodex, AgentStatus: agentintel.StatusIdle, AwaitingUser: true, StatusRule: "transcript.turn_end"},
		// No tool, no status — only a rule. The explicit-signal path produces exactly this on a
		// bare shell, and an earlier cut of RollUp dropped the rule here because it took the
		// provenance from a "decider" that did not exist.
		{AwaitingUser: true, StatusRule: "signal.bell", StatusEvidence: "任务已完成"},
		// Activity with no agent: vacuous in production, and the roll-up must not quietly filter it
		// out — a filter that is a no-op in practice is a rule nobody can check.
		{ActivityAt: activity},
	}
	for i, u := range units {
		for _, active := range []int{0, -1} {
			got := agentintel.RollUp([]agentintel.SurfaceUnit{u}, active)
			if !reflect.DeepEqual(got, u) {
				t.Errorf("unit %d (active=%d): a one-unit roll-up changed the unit.\n want: %+v\n  got: %+v", i, active, u, got)
			}
		}
	}
}

// An out-of-range `active` must degrade to "no focus", not panic and not silently index. Callers
// compute it by searching for a flag that can legitimately be absent (a window whose panes all
// report pane_active=0 has been observed), so this is a reachable state, not defensive padding.
func TestRollUp_FocusOutOfRangeIsNoFocus(t *testing.T) {
	units := []agentintel.SurfaceUnit{
		{AgentTool: agentintel.ToolClaude, AgentStatus: agentintel.StatusIdle, StatusRule: "transcript.idle"},
		{AgentTool: agentintel.ToolCodex, AgentStatus: agentintel.StatusIdle, StatusRule: "transcript.turn_end"},
	}
	want := agentintel.RollUp(units, -1)
	for _, active := range []int{-5, 2, 99} {
		if got := agentintel.RollUp(units, active); !reflect.DeepEqual(got, want) {
			t.Errorf("active=%d did not degrade to no-focus.\n want: %+v\n  got: %+v", active, want, got)
		}
	}
}

// The window's card is derived from its panes and from nothing else — the one invariant that keeps
// "the card disagrees with what is in it" from being expressible.
func TestRollUpPanes_CardFollowsThePanes(t *testing.T) {
	w := agentintel.TmuxWindowState{
		Index: 3,
		Name:  "editor",
		Panes: []agentintel.TmuxPaneState{
			{Index: 0, Active: false, CWD: "/bg", SurfaceUnit: agentintel.SurfaceUnit{
				AgentTool: agentintel.ToolClaude, AgentStatus: agentintel.StatusWaiting,
				AwaitingUser: true, StatusRule: "screen.approval"}},
			{Index: 1, Active: true, CWD: "/fg", SurfaceUnit: agentintel.SurfaceUnit{
				AgentTool: agentintel.ToolCodex, AgentStatus: agentintel.StatusRunning, StatusRule: "transcript.running"}},
		},
	}
	w.RollUpPanes()

	if w.AgentStatus != agentintel.StatusWaiting {
		t.Errorf("a waiting background pane did not turn the card red: %q", w.AgentStatus)
	}
	if w.StatusRule != "screen.approval" {
		t.Errorf("the card's rule came from a bystander pane: %q", w.StatusRule)
	}
	if w.AgentTool != agentintel.ToolCodex {
		t.Errorf("the badge did not follow the focused pane: %q", w.AgentTool)
	}
	// The card's cwd is the focused pane's — this is what the overview card's footer, the title
	// fallback and the workbench's stored path all read.
	if w.CWD != "/fg" {
		t.Errorf("card cwd = %q, want the active pane's /fg", w.CWD)
	}

	// With nothing focused it falls back to the first pane, and still says everything else the
	// same way. A window with no active pane is not a window with no cwd.
	w.Panes[1].Active = false
	w.RollUpPanes()
	if w.CWD != "/bg" {
		t.Errorf("with no focused pane, card cwd = %q, want the first pane's /bg", w.CWD)
	}

	w.Panes = nil
	w.RollUpPanes()
	if (w.SurfaceUnit != agentintel.SurfaceUnit{}) || w.CWD != "" {
		t.Errorf("a window whose panes all went away still claims %+v / cwd %q", w.SurfaceUnit, w.CWD)
	}
}
