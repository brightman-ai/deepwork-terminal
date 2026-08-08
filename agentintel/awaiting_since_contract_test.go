package agentintel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The needs-you dot's reload-proof "seen" layer is a cross-language contract: the frontend
// (useAgentOverview) keys on the EXACT JSON field name `awaitingSince` and parses it as an ISO
// string, and it treats an UNDATED completion as "never dismissable". Lock both so a backend
// rename, or a switch to *time.Time, can't silently break the frontend.
//
// ── 2026-08-08: what "undated" looks like on the wire changed, deliberately ───────────────────
// This test used to assert the opposite of its second half: that an undated pane emits the literal
// `"awaitingSince":"0001-01-01T00:00:00Z"`. That sentinel was never a design — it was what
// `omitempty` DOES to a struct, which is nothing, and the test had pinned the accident. With
// `omitzero` (Go 1.24) the key is simply absent, which is how the非 tmux card had always said it,
// so both feeds now say "undated" the same way: by not saying anything.
//
// This is safe to change because the frontend's `isDatedSince` predicate is
// `!!since && !since.startsWith('0001-01-01')` — an absent value and the sentinel were ALREADY
// indistinguishable to it. The assertion below is therefore not relaxed: it is inverted to match
// the intent, and it still fails if the field silently starts carrying a placeholder again.
func TestAwaitingSince_JSONContract(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2026-07-09T12:34:56Z")
	b, err := json.Marshal(TmuxPaneState{SurfaceUnit: SurfaceUnit{AwaitingUser: true, AwaitingSince: ts}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"awaitingSince":"2026-07-09T12:34:56Z"`) {
		t.Errorf("field name/format contract broke — frontend keys on `awaitingSince` (ISO): %s", b)
	}

	// A pane awaiting with no dated completion (e.g. a PTY-only permission prompt) carries NO
	// awaitingSince key at all. Asserted both ways round: the key must be gone, and in particular
	// the old zero-time placeholder must not come back — a value the frontend would have to know a
	// magic prefix to recognise as "no value".
	z, err := json.Marshal(TmuxPaneState{SurfaceUnit: SurfaceUnit{AwaitingUser: true}})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if strings.Contains(string(z), "awaitingSince") {
		t.Errorf("an undated wait still ships an awaitingSince key — undated must be the ABSENCE of a value, not a placeholder standing in for one: %s", z)
	}
	if strings.Contains(string(z), "0001-01-01") {
		t.Errorf("the zero-time sentinel is back on the wire: %s", z)
	}
}

// The same contract for the other timestamp on the unit. It regressed independently once already
// (`activityAt` existed on the pane for weeks while the session card had no freshness at all), so
// it gets its own assertion rather than riding on the one above.
func TestActivityAt_JSONContract(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2026-07-09T12:34:56Z")
	b, err := json.Marshal(TmuxPaneState{SurfaceUnit: SurfaceUnit{ActivityAt: ts}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"activityAt":"2026-07-09T12:34:56Z"`) {
		t.Errorf("activityAt name/format contract broke: %s", b)
	}

	z, err := json.Marshal(TmuxPaneState{})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if strings.Contains(string(z), "activityAt") {
		t.Errorf("a surface with no known transcript write still ships an activityAt key — "+
			"«我不知道它多新» must not be encoded as «它是公元一年»: %s", z)
	}
}
