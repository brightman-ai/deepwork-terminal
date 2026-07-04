package terminal

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/brightman-ai/deepwork-terminal/authgate"
)

// The auth code is mutable at runtime (handleRotateAuthCode) and authWrap reads it
// on every request, so both sides go through these locked accessors — a plain
// field read racing a rotation would tear the string header.

// authCode returns the current auth code under lock.
func (s *Server) authCode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.AuthCode
}

// SetAuthCode replaces the current auth code under lock. Exported so an embedding
// host (deepwork-pro) can keep this embedded terminal's gate in sync when it
// rotates its own auth code — the code pro injects via withTerminalHostAuth must
// match what this terminal compares against, or every forwarded request 401s.
func (s *Server) SetAuthCode(code string) {
	s.mu.Lock()
	s.config.AuthCode = code
	s.mu.Unlock()
}

// handleRotateAuthCode SETS the auth code and returns the new value. ONE endpoint,
// two modes (SSOT): an optional {"code":"..."} body sets a user-supplied CUSTOM code
// (manual edit); an empty/absent body rotates to a fresh RANDOM code.
//
// Either way this immediately invalidates any previously shared link or QR (an old
// ?auth=OLD stops authenticating) — the recovery path when a share link leaks. The
// change is in-memory and effective at once; like the generated default it is
// intentionally NOT persisted, so a restart reapplies the operator's -auth-code (or
// generates a fresh one), matching the existing ephemeral model.
//
// Note: when this terminal is EMBEDDED in deepwork-pro, the code that actually gates
// access is pro's App.AuthCode, injected into every forwarded request by
// withTerminalHostAuth. Pro owns its own route (rotating App.AuthCode + re-syncing
// this injection); this handler serves the STANDALONE terminal.
func (s *Server) handleRotateAuthCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body) // absent/invalid body → random
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		code = generateAuthCode()
	} else if authgate.NormalizeCode(code) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code must contain letters or digits"})
		return
	}
	s.SetAuthCode(code)
	writeJSON(w, http.StatusOK, map[string]string{"authCode": code})
}
