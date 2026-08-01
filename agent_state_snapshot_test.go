package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pushStub builds an AgentStatePushFunc that yields the given frames and reports whether
// its release was called — the leak that matters here, since every subscription holds a
// watcher alive by refcount and a missed release keeps it running after the request ends.
func pushStub(frames ...string) (AgentStatePushFunc, *bool) {
	released := false
	fn := func(_ context.Context, _ string) (<-chan json.RawMessage, func(), error) {
		ch := make(chan json.RawMessage, len(frames))
		for _, f := range frames {
			ch <- json.RawMessage(f)
		}
		return ch, func() { released = true }, nil
	}
	return fn, &released
}

func getAgentState(t *testing.T, srv *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/sessions/"+id+"/agent-state", nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	srv.handleAgentStateSnapshot(w, r)
	return w
}

// TestAgentStateSnapshot_ReturnsTheCurrentState covers the mount-time fetch the shared
// frontend has always made and this server never answered. The response is written through
// verbatim rather than re-marshalled: it is the SAME payload the WS pushes, so the client
// can apply both through one code path, and re-encoding it here would create a second place
// for the shape to drift.
func TestAgentStateSnapshot_ReturnsTheCurrentState(t *testing.T) {
	push, released := pushStub(`{"current":{"status":"working"},"notifications":[]}`)
	srv := &Server{}
	srv.hooks.AgentStatePush = push

	w := getAgentState(t, srv, "sess-1")

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"current":{"status":"working"},"notifications":[]}`, w.Body.String())
	assert.True(t, *released, "the subscription must be released before the handler returns")
}

// TestAgentStateSnapshot_NoStateYetIsNotAnError: a session whose agent has not spoken has
// nothing to report, and that is a normal answer. Returning an error instead would put a
// red herring in the log of every freshly opened terminal.
func TestAgentStateSnapshot_NoStateYetIsNotAnError(t *testing.T) {
	push, released := pushStub() // a live subscription that has produced nothing
	srv := &Server{}
	srv.hooks.AgentStatePush = push

	w := getAgentState(t, srv, "sess-1")

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"current":null,"notifications":[]}`, w.Body.String(),
		"the empty answer must have the same shape as a real one — one client code path, not two")
	assert.True(t, *released)
}

// TestAgentStateSnapshot_UnknownSession: the provider is the authority on whether a session
// exists, so its refusal becomes the status. 404 is what the caller can act on.
func TestAgentStateSnapshot_UnknownSession(t *testing.T) {
	srv := &Server{}
	srv.hooks.AgentStatePush = func(context.Context, string) (<-chan json.RawMessage, func(), error) {
		return nil, nil, fmt.Errorf("session %q not found", "ghost")
	}

	w := getAgentState(t, srv, "ghost")

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestAgentStateSnapshot_NoProvider: an embedding host that wires no agent-state source at
// all is a configuration fact, not a missing session — 503 says "this deployment cannot
// answer", where a 404 would send the reader looking for a session that is right there.
func TestAgentStateSnapshot_NoProvider(t *testing.T) {
	srv := &Server{}

	w := getAgentState(t, srv, "sess-1")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
