package agentintel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The needs-you dot's reload-proof "seen" layer is a cross-language contract: the frontend
// (useAgentOverview) keys on the EXACT JSON field name `awaitingSince` and parses it as an ISO
// string, and it treats the Go zero time (`0001-01-01…`, emitted because encoding/json's omitempty
// does NOT apply to a time.Time struct) as "undated → never dismissable". Lock both so a backend
// rename or a switch to *time.Time can't silently break the frontend.
func TestAwaitingSince_JSONContract(t *testing.T) {
	ts, _ := time.Parse(time.RFC3339Nano, "2026-07-09T12:34:56Z")
	b, err := json.Marshal(TmuxPaneState{AwaitingUser: true, AwaitingSince: ts})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"awaitingSince":"2026-07-09T12:34:56Z"`) {
		t.Errorf("field name/format contract broke — frontend keys on `awaitingSince` (ISO): %s", b)
	}

	// A pane awaiting but with no dated completion (e.g. PTY-only permission prompt) emits the
	// zero time; the frontend's isDatedSince() guards exactly this `0001-01-01` prefix.
	z, err := json.Marshal(TmuxPaneState{AwaitingUser: true})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if !strings.Contains(string(z), `"awaitingSince":"0001-01-01T00:00:00Z"`) {
		t.Errorf("zero-time contract changed — frontend isDatedSince() expects the 0001-01-01 prefix: %s", z)
	}
}
