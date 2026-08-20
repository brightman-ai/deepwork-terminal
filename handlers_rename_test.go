package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A renamed tab used to keep showing its pre-rename name EVERYWHERE downstream —
// GET /sessions, the notifier's identityTag title, the Agent Overview card —
// because renaming was purely a frontend `tab.name = x` mutation (useWorkbench.ts)
// that never told the backend Session anything. These lock in the missing write
// side: POST /sessions/{id}/rename, and that sessionTitle()/the overview entry
// (the exact value the notifier's identityTag consumes as `location`) reflect it
// immediately afterward — not just that the endpoint returns 204.
func TestRenameSessionUpdatesTitleEverywhere(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("stwork")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := sessionTitle(sess); got != "stwork" {
		t.Fatalf("pre-rename title = %q, want %q", got, "stwork")
	}
	entries := srv.sessionsOverview(context.Background())
	if len(entries) != 1 || entries[0].Title != "stwork" {
		t.Fatalf("pre-rename overview entry: %+v", entries)
	}

	body, _ := json.Marshal(map[string]string{"name": "agent-memory"})
	req := httptest.NewRequest(http.MethodPost, "/sessions/"+sess.ID+"/rename", bytes.NewReader(body))
	req.SetPathValue("id", sess.ID)
	w := httptest.NewRecorder()
	srv.handleRenameSession(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("rename status = %d, body = %s", w.Code, w.Body.String())
	}

	if got := sessionTitle(sess); got != "agent-memory" {
		t.Fatalf("post-rename title = %q, want %q — the exact bug: renamed but the old name kept showing", got, "agent-memory")
	}
	entries = srv.sessionsOverview(context.Background())
	if len(entries) != 1 || entries[0].Title != "agent-memory" {
		t.Fatalf("post-rename overview entry (what the notifier's identityTag reads as location): %+v", entries)
	}
}

func TestRenameSessionRejectsEmptyName(t *testing.T) {
	srv, sm := newOverviewTestServer(t)
	sess, err := sm.Create("original")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"name": "   "}) // sanitizeFieldMax trims to empty
	req := httptest.NewRequest(http.MethodPost, "/sessions/"+sess.ID+"/rename", bytes.NewReader(body))
	req.SetPathValue("id", sess.ID)
	w := httptest.NewRecorder()
	srv.handleRenameSession(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty name should be rejected, got status %d", w.Code)
	}
	if got := sessionTitle(sess); got != "original" {
		t.Fatalf("rejected rename must not have mutated the session, got %q", got)
	}
}

func TestRenameSessionUnknownID(t *testing.T) {
	srv, _ := newOverviewTestServer(t)
	body, _ := json.Marshal(map[string]string{"name": "x"})
	req := httptest.NewRequest(http.MethodPost, "/sessions/does-not-exist/rename", bytes.NewReader(body))
	req.SetPathValue("id", "does-not-exist")
	w := httptest.NewRecorder()
	srv.handleRenameSession(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown session id should 404, got %d", w.Code)
	}
}
