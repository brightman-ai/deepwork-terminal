package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brightman-ai/deepwork-terminal/agentintel"
)

// ── CP3: the wire contract, held by bytes rather than by care ─────────────────────────────────
//
// The two overview feeds describe the same kind of thing — a terminal surface unit that may be
// running an agent — and their payloads each hand-wrote the same field names, with a comment on
// each side asking the other to please stay in step ("Field names mirror the tmux pane/window
// payload"). A comment cannot hold two structs equal. The shared type in agentintel/surface_unit.go
// is what replaces that request with a fact.
//
// Making that change is only safe if the WIRE does not move, because the frontend that consumes
// both payloads is frozen this round. "Looks the same" is not the standard: these goldens are
// byte-for-byte, generated from the code as it stood BEFORE the shared type existed.
//
// Regenerating them is a wire-contract change and needs Human's decision, never a green build:
// DW_UPDATE_SURFACE_GOLDEN=1 exists so the initial capture is reproducible, not as an escape hatch.

const goldenEnv = "DW_UPDATE_SURFACE_GOLDEN"

func goldenBytes(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv(goldenEnv) == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote golden %s (%d bytes)", path, len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing: %v — the wire contract has nothing to be checked against", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("wire bytes moved, and the frontend reading them did not.\n want: %s\n  got: %s", want, got)
	}
}

// fixedOverviewEntries is the non-tmux payload's frozen input: one card with every unit field
// populated and one with none of them, because the two halves of the contract fail separately —
// the first pins names, order and types, the second pins which keys disappear when empty.
func fixedOverviewEntries() []SessionOverviewEntry {
	full := SessionOverviewEntry{
		ID:     "sess-1",
		Title:  "worker",
		CWD:    "/repo",
		Engine: "shell",
		Exited: false,
		SurfaceCard: agentintel.SurfaceCard{
			SurfaceUnit: agentintel.SurfaceUnit{
				AgentTool:       agentintel.ToolClaude,
				AgentStatus:     agentintel.StatusWaiting,
				AwaitingUser:    true,
				AwaitingSince:   time.Date(2026, 8, 8, 9, 0, 0, 123456789, time.UTC),
				EndedOnQuestion: true,
				StatusRule:      "screen.approval",
				StatusEvidence:  "Do you want to proceed?",
				ActivityAt:      time.Date(2026, 8, 8, 8, 59, 0, 0, time.UTC),
			},
			Tail: []string{"line one", "line two"},
		},
	}
	bare := SessionOverviewEntry{ID: "sess-2", Title: "quiet"}
	// A third, for the one key whose POSITION the two above cannot show: `exited` is omitempty and
	// false in both, so a reordering of it would slip past a golden made only of them. It moved when
	// the card became an embedded type (own fields first, shared last), and a key that moves without
	// a test noticing is the whole failure mode these bytes exist to prevent.
	dead := SessionOverviewEntry{ID: "sess-3", Title: "gone", Exited: true}
	return []SessionOverviewEntry{full, bare, dead}
}

// fixedTmuxState is the tmux payload's frozen input, same two halves for the same reason.
func fixedTmuxState() agentintel.TmuxState {
	full := agentintel.TmuxPaneState{
		Index:  0,
		Active: true,
		Title:  "pane title",
		PID:    4242,
		CWD:    "/repo",
		PaneID: "%7",
		SurfaceUnit: agentintel.SurfaceUnit{
			AgentTool:       agentintel.ToolClaude,
			AgentStatus:     agentintel.StatusWaiting,
			AwaitingUser:    true,
			AwaitingSince:   time.Date(2026, 8, 8, 9, 0, 0, 123456789, time.UTC),
			EndedOnQuestion: true,
			StatusRule:      "screen.approval",
			StatusEvidence:  "Do you want to proceed?",
			ActivityAt:      time.Date(2026, 8, 8, 8, 59, 0, 0, time.UTC),
		},
	}
	bare := agentintel.TmuxPaneState{Index: 1, PID: 4243, CWD: "/repo"}
	// The window's card facts come from RollUpPanes, not from a literal written next to the panes:
	// a golden whose card was hand-typed would keep passing while the roll-up that produces it in
	// production said something else — pinning the bytes of a fiction.
	win := agentintel.TmuxWindowState{
		Index:    0,
		Name:     "editor",
		WindowID: "@1",
		Active:   true,
		Panes:    []agentintel.TmuxPaneState{full, bare},
		SurfaceCard: agentintel.SurfaceCard{
			Tail: []string{"line one", "line two"},
		},
	}
	win.RollUpPanes()
	return agentintel.TmuxState{
		Installed:       true,
		ServerRunning:   true,
		Attached:        true,
		AttachedSession: "main",
		Prefix:          agentintel.TmuxPrefix{Display: "C-b", Bytes: []byte{0x02}},
		ModeKeys:        "emacs",
		Sessions: []agentintel.TmuxSessionState{{
			Name:     "main",
			Attached: true,
			Windows:  []agentintel.TmuxWindowState{win},
		}},
	}
}

func TestSurfaceWire_SessionsOverviewBytesAreFrozen(t *testing.T) {
	got, err := json.Marshal(fixedOverviewEntries())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	goldenBytes(t, "sessions_overview.json", got)
}

func TestSurfaceWire_TmuxStateBytesAreFrozen(t *testing.T) {
	got, err := json.Marshal(fixedTmuxState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	goldenBytes(t, "tmux_state.json", got)
}

// ── "It must not be possible to add only half of a fact" ──────────────────────────────────────
//
// The goldens above prove the wire did not move. They cannot prove the drift is over: a payload
// that embeds SurfaceUnit today can still grow a private `awaitingKind` tomorrow, on one side
// only, and every golden stays green because the golden's fixed input never sets it. That is the
// SAME failure the shared type was built to end, one round later.
//
// So the payloads' own fields are frozen too. Everything a surface payload declares OUTSIDE the
// shared unit is listed here, once, and anything new must either join these lists — a deliberate,
// reviewed statement that the fact belongs to one source's structure and not to the other's — or
// go into SurfaceUnit, where both feeds get it or neither does.

// jsonName is the wire name a struct field marshals under.
func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	if tag == "" {
		return f.Name
	}
	return tag
}

// splitSurface separates a payload's OWN wire fields from those promoted out of the named embedded
// shared type. A payload that does not embed it at all fails here rather than silently reporting
// every field as its own — that shape is precisely the pre-shared-type world.
//
// `embed` is a parameter because there are now TWO layers to hold still: units embed SurfaceUnit,
// cards embed SurfaceCard. They are checked the same way on purpose — the mismatch that produced
// this round was a shared UNIT sitting under two unshared CARDS, so a rule that only ever looked at
// one depth would have been blind to it.
func splitSurface(t *testing.T, typ reflect.Type, embed string) (own, shared []string) {
	t.Helper()
	embedded := false
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous && f.Type.Name() == embed {
			embedded = true
			shared = append(shared, flattenJSONNames(f.Type)...)
			continue
		}
		own = append(own, jsonName(f))
	}
	if !embedded {
		t.Fatalf("%s no longer embeds agentintel.%s — the surface payloads are back to "+
			"hand-written field lists that only a comment asks to stay in step", typ, embed)
	}
	return own, shared
}

// flattenJSONNames is the wire names a struct contributes, following embedded structs the way
// encoding/json promotes them — SurfaceCard's own contribution is SurfaceUnit's fields plus `tail`,
// and the assertions below compare that flattened list, not the two-level Go shape.
func flattenJSONNames(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, flattenJSONNames(f.Type)...)
			continue
		}
		out = append(out, jsonName(f))
	}
	return out
}

// The fields each payload legitimately owns, because they describe that source's STRUCTURE rather
// than the surface unit's semantics: a session id and its exit state belong to a PTY session, a
// pane index and its tmux pane id belong to a pane.
//
// `activityAt` used to be on this list, as tmux-only. It is not any more — it moved into
// SurfaceUnit, where it always belonged: "how old is the evidence behind this status" is a fact
// about a surface, not about tmux, and a non-tmux card could tell exactly the same ten-hour lie
// with nothing on it to catch the difference. Its absence from this list is the assertion.
//
// `tail` used to be on the CARD lists for the same "it's just this source's structure" reason, on
// both of them, separately. It is not any more: a card's last lines are a card fact on either feed,
// they were already required to share a length (OverviewTailLines), and two declarations sharing a
// constant is precisely the arrangement that let one of them sit at 8 while its comment claimed it
// matched the other's 40.
var (
	overviewOwnFields   = []string{"id", "title", "cwd", "engine", "exited"}
	tmuxWindowOwnFields = []string{"index", "name", "windowId", "active", "cwd", "panes"}
	tmuxPaneOwnFields   = []string{"index", "active", "title", "pid", "cwd", "paneId"}
)

func TestSurfaceUnit_NeitherPayloadCanGrowHalfAFact(t *testing.T) {
	entryOwn, entryCard := splitSurface(t, reflect.TypeOf(SessionOverviewEntry{}), "SurfaceCard")
	winOwn, winCard := splitSurface(t, reflect.TypeOf(agentintel.TmuxWindowState{}), "SurfaceCard")
	paneOwn, paneUnit := splitSurface(t, reflect.TypeOf(agentintel.TmuxPaneState{}), "SurfaceUnit")

	// One declaration, so one field list — in the same order, since order is wire-visible.
	if !reflect.DeepEqual(entryCard, winCard) {
		t.Fatalf("the two CARD payloads no longer share one field list:\n session: %v\n  window: %v", entryCard, winCard)
	}
	if len(paneUnit) == 0 {
		t.Fatal("the shared unit contributes no fields at all — the embed is decorative")
	}
	// A card says everything a unit says, plus its tail. Stated as an equation rather than a second
	// literal list: the point is that the card layer cannot quietly stop carrying a unit fact (or
	// start carrying one the unit lacks), which no amount of frozen lists would catch on its own.
	if want := append(append([]string{}, paneUnit...), "tail"); !reflect.DeepEqual(entryCard, want) {
		t.Errorf("a card no longer carries exactly the unit's facts plus its tail.\n  have: %v\n  want: %v\n"+
			"A card IS a surface (SurfaceCard embeds SurfaceUnit); if this drifted, one of the two "+
			"layers grew a fact the other cannot express.", entryCard, want)
	}

	for _, tc := range []struct {
		name  string
		got   []string
		want  []string
		other string
	}{
		{"SessionOverviewEntry", entryOwn, overviewOwnFields, "TmuxWindowState"},
		{"TmuxWindowState", winOwn, tmuxWindowOwnFields, "SessionOverviewEntry"},
		{"TmuxPaneState", paneOwn, tmuxPaneOwnFields, "SessionOverviewEntry"},
	} {
		if reflect.DeepEqual(tc.got, tc.want) {
			continue
		}
		t.Errorf("%s's own wire fields changed.\n  have: %v\n  want: %v\n"+
			"If the new field is a SURFACE FACT (something %s should report too), it belongs in "+
			"agentintel.SurfaceUnit — or in SurfaceCard if it belongs to the card rather than to one "+
			"of its units — so both feeds get it. If it genuinely describes only this source's "+
			"structure, add it to the frozen list above and say why. Identity is the one thing that "+
			"legitimately lives here: see SurfaceCard on why index/active cannot be shared.",
			tc.name, tc.got, tc.want, tc.other)
	}
}

// TestSurfaceUnit_EveryUnitFieldReachesBothWires closes the loop between the type and the bytes:
// the assertions above are about declarations, and a field that is declared but never marshalled
// (an untagged field, a `json:"-"`) would satisfy them while reaching nobody.
func TestSurfaceUnit_EveryUnitFieldReachesBothWires(t *testing.T) {
	_, unit := splitSurface(t, reflect.TypeOf(agentintel.TmuxPaneState{}), "SurfaceUnit")

	card, err := json.Marshal(fixedOverviewEntries())
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	pane, err := json.Marshal(fixedTmuxState())
	if err != nil {
		t.Fatalf("marshal pane: %v", err)
	}

	for _, name := range unit {
		key := `"` + name + `":`
		if !strings.Contains(string(card), key) {
			t.Errorf("unit field %q never reaches the sessions_overview wire", name)
		}
		if !strings.Contains(string(pane), key) {
			t.Errorf("unit field %q never reaches the tmux_state wire", name)
		}
	}
}
