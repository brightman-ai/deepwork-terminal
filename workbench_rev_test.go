package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The workbench document is read ONCE per page load and written back whole. Two devices therefore
// hold two independently evolving copies, and before `rev` existed the later writer silently
// deleted whatever it had never seen — including a tab whose PTY was still running, which is how a
// live claude session became unreachable on 2026-09-03 while the process itself never died.
//
// These lock in the server's half of the fix: it refuses a write built on a base that has moved,
// and hands back the current document so the client can three-way merge (workbenchMerge.ts).

func resetWorkbench(t *testing.T) {
	t.Helper()
	workbenchMu.Lock()
	workbenchData = nil
	workbenchMu.Unlock()
	t.Cleanup(func() { workbenchMu.Lock(); workbenchData = nil; workbenchMu.Unlock() })
}

func putWorkbench(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleSaveWorkbench(rec, httptest.NewRequest(http.MethodPut, "/workbench", strings.NewReader(body)))
	return rec
}

func getWorkbench(t *testing.T, s *Server) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleGetWorkbench(rec, httptest.NewRequest(http.MethodGet, "/workbench", nil))
	return rec
}

func revOf(t *testing.T, body string) int64 {
	t.Helper()
	rev, ok := workbenchRev(json.RawMessage(body))
	if !ok {
		t.Fatalf("no rev in %s", body)
	}
	return rev
}

func TestWorkbenchSave_StaleRevIsRejectedAndCurrentReturned(t *testing.T) {
	s := &Server{config: Config{DataDir: t.TempDir()}}
	resetWorkbench(t)

	// Device A and device B both load rev 1.
	if rec := putWorkbench(t, s, `{"rev":0,"tabs":["t1"]}`); rec.Code != http.StatusOK {
		t.Fatalf("first save: got %d, want 200", rec.Code)
	}
	loaded := getWorkbench(t, s).Body.String()
	rev := revOf(t, loaded)

	// A saves, adding a tab. Accepted; rev moves.
	if rec := putWorkbench(t, s, `{"rev":`+revStr(rev)+`,"tabs":["t1","t2"]}`); rec.Code != http.StatusOK {
		t.Fatalf("device A save: got %d, want 200", rec.Code)
	}

	// B still holds the OLD rev and writes a document without t2 — the incident's final step.
	rec := putWorkbench(t, s, `{"rev":`+revStr(rev)+`,"tabs":["t1"]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save: got %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "t2") {
		t.Errorf("409 must carry the CURRENT document so the client can merge; got %s", rec.Body.String())
	}

	// And the stored document must be untouched by the rejected write.
	if got := getWorkbench(t, s).Body.String(); !strings.Contains(got, "t2") {
		t.Errorf("a rejected write must not mutate the stored doc; got %s", got)
	}
}

func TestWorkbenchSave_MatchingRevAccepted(t *testing.T) {
	s := &Server{config: Config{DataDir: t.TempDir()}}
	resetWorkbench(t)

	putWorkbench(t, s, `{"rev":0,"tabs":["t1"]}`)
	rev := revOf(t, getWorkbench(t, s).Body.String())

	rec := putWorkbench(t, s, `{"rev":`+revStr(rev)+`,"tabs":["t1","t2"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("in-sync save: got %d, want 200", rec.Code)
	}
	var resp struct{ Rev int64 }
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if resp.Rev != rev+1 {
		t.Errorf("rev must advance by one: got %d, want %d", resp.Rev, rev+1)
	}
}

// A page that loaded the pre-rev bundle sends no `rev` at all. Rejecting it would brick that
// page's saves entirely, so it is accepted as a plain replace — the old behaviour, scoped to old
// clients only, and it expires when the asset-hash updater reloads them.
func TestWorkbenchSave_PreRevClientStillAccepted(t *testing.T) {
	s := &Server{config: Config{DataDir: t.TempDir()}}
	resetWorkbench(t)

	putWorkbench(t, s, `{"rev":0,"tabs":["t1"]}`)
	if rec := putWorkbench(t, s, `{"tabs":["only-this"]}`); rec.Code != http.StatusOK {
		t.Fatalf("pre-rev client save: got %d, want 200", rec.Code)
	}
	if got := getWorkbench(t, s).Body.String(); !strings.Contains(got, "only-this") {
		t.Errorf("pre-rev save should have replaced the doc; got %s", got)
	}
}

// After a restart the in-memory cache is empty. The rev compare must be made against the ON-DISK
// document, not against "nothing" — otherwise every first write after a restart silently wins and
// the guard is off exactly when two devices are most likely to be out of sync.
func TestWorkbenchSave_RevComparedAgainstDiskAfterRestart(t *testing.T) {
	s := &Server{config: Config{DataDir: t.TempDir()}}
	resetWorkbench(t)
	s.saveWorkbenchToDisk(json.RawMessage(`{"rev":7,"tabs":["t1","t2"]}`))

	if rec := putWorkbench(t, s, `{"rev":3,"tabs":["t1"]}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale save against on-disk rev: got %d, want 409", rec.Code)
	}
	if rec := putWorkbench(t, s, `{"rev":7,"tabs":["t1"]}`); rec.Code != http.StatusOK {
		t.Fatalf("in-sync save against on-disk rev: got %d, want 200", rec.Code)
	}
}

func TestWithWorkbenchRev_PreservesEveryOtherKey(t *testing.T) {
	out := withWorkbenchRev(json.RawMessage(`{"groups":[1],"activeTabId":"x"}`), 9)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"groups", "activeTabId", "rev"} {
		if _, ok := m[k]; !ok {
			t.Errorf("key %q lost while stamping rev: %s", k, out)
		}
	}
	if string(m["rev"]) != "9" {
		t.Errorf("rev = %s, want 9", m["rev"])
	}
}

// revStr — screen_test.go already owns the name `itoa` in this package.
func revStr(n int64) string { return strconv.FormatInt(n, 10) }
