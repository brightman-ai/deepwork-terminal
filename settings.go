package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Auth-code generation/canonicalization/comparison now lives in the shared kit/authgate SSOT
// (github.com/brightman-ai/kit/authgate) so the standalone terminal, standalone teamworkbench and
// deepwork-pro all speak the same codes. Callers use authgate.Generate() directly.

// workbench persistence — stores tab layout as JSON file
var (
	workbenchMu   sync.Mutex
	workbenchData json.RawMessage
)

func (s *Server) handleGetWorkbench(w http.ResponseWriter, r *http.Request) {
	workbenchMu.Lock()
	data := workbenchData
	workbenchMu.Unlock()

	if data == nil {
		// Try loading from disk
		data = s.loadWorkbenchFromDisk()
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Explicit 200: same gin-NoRoute-404 embed trap as handleGetStore — a bare Write
	// would ship the workbench body with a 404 status under deepwork-pro, silently
	// dropping the saved per-pane workbench layout on 8087.
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck
}

func (s *Server) handleSaveWorkbench(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	workbenchMu.Lock()
	workbenchData = raw
	workbenchMu.Unlock()

	// Persist to disk
	s.saveWorkbenchToDisk(raw)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workbenchPath() string {
	dir := s.config.DataDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".dw-terminal")
	}
	return filepath.Join(dir, "workbench.json")
}

func (s *Server) loadWorkbenchFromDisk() json.RawMessage {
	data, err := os.ReadFile(s.workbenchPath())
	if err != nil {
		return nil
	}
	return json.RawMessage(data)
}

func (s *Server) saveWorkbenchToDisk(data json.RawMessage) {
	path := s.workbenchPath()
	os.MkdirAll(filepath.Dir(path), 0755) //nolint:errcheck
	os.WriteFile(path, data, 0644)        //nolint:errcheck
}

// store persistence — stores user input data (snippets, history) as JSON file
var (
	storeMu   sync.Mutex
	storeData json.RawMessage
)

func (s *Server) handleGetStore(w http.ResponseWriter, r *http.Request) {
	storeMu.Lock()
	if storeData == nil {
		storeData = s.loadStoreFromDisk() // hydrate the in-memory cache from disk once (e.g. post-restart)
	}
	data := storeData
	storeMu.Unlock()
	if data == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}")) //nolint:errcheck
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Explicit 200 is REQUIRED, not cosmetic: when embedded in deepwork-pro this
	// handler is reached via gin's NoRoute forward, and gin's serveError pre-sets the
	// response status to 404 before running the forwarded handler. A bare w.Write
	// (no WriteHeader) then flushes with that 404 — so the client got the store BODY
	// but a 404 STATUS, and fetchStore() discarded it as a failed load → remotePeers
	// came back empty → every remote tab showed "该远程配置已被删除". Standalone hid this
	// (its mux defaults to 200). See handleSaveStore, which already sets 204.
	w.WriteHeader(http.StatusOK)
	w.Write(data) //nolint:errcheck
}

func (s *Server) handleSaveStore(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	storeMu.Lock()
	base := storeData
	if base == nil {
		// After a restart the cache is empty — merge onto the ON-DISK store so a partial PUT
		// (a client that hasn't GET-hydrated, or whose local data is incomplete) can't clobber
		// keys it doesn't know about.
		base = s.loadStoreFromDisk()
	}
	merged := mergeStoreJSON(base, raw)
	storeData = merged
	storeMu.Unlock()
	s.saveStoreToDisk(merged)
	w.WriteHeader(http.StatusNoContent)
}

// mergeStoreJSON returns base with patch's top-level keys added/overwritten; keys present ONLY in
// base are PRESERVED. This makes PUT /store a per-key merge instead of a whole-object replace, so a
// client that (after a restart or a failed GET) holds only some keys can never wipe the others —
// the root of the "remotePeers and history take turns vanishing" data loss. Degrades to a plain
// replace if either side isn't a JSON object, so it never fails a save.
func mergeStoreJSON(base, patch json.RawMessage) json.RawMessage {
	var p map[string]json.RawMessage
	if err := json.Unmarshal(patch, &p); err != nil {
		return patch // not a JSON object — keep the old replace behaviour rather than fail the save
	}
	b := map[string]json.RawMessage{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &b) // best-effort; a corrupt/absent base just starts empty
	}
	for k, v := range p {
		b[k] = v
	}
	out, err := json.Marshal(b)
	if err != nil {
		return patch
	}
	return out
}

func (s *Server) storePath() string {
	dir := s.config.DataDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".dw-terminal")
	}
	return filepath.Join(dir, "store.json")
}

func (s *Server) loadStoreFromDisk() json.RawMessage {
	data, err := os.ReadFile(s.storePath())
	if err != nil {
		return nil
	}
	return json.RawMessage(data)
}

func (s *Server) saveStoreToDisk(data json.RawMessage) {
	path := s.storePath()
	os.MkdirAll(filepath.Dir(path), 0755) //nolint:errcheck
	os.WriteFile(path, data, 0644)        //nolint:errcheck
}

// handleSystem returns system info for the settings page + onboarding (the help
// center reads tmuxInstalled/os to decide whether to show the tmux install step
// and which command to display for the host OS).
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"port":          s.Port(),
		"pid":           os.Getpid(),
		"commit":        "dev",
		"os":            runtime.GOOS,
		"tmuxInstalled": s.tmuxInstalled(),
	})
}

// handleGetSettings returns current server settings.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	settings := map[string]any{
		"shell":       s.config.DefaultShell,
		"bufferSize":  s.config.BufferSize,
		"maxSessions": s.config.MaxSessions,
		"authCode":    s.config.AuthCode,
		"tunnel":      s.tunnel.Status(),
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, settings)
}

// handleTunnelStatus returns the full tunnel state including download progress.
func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.tunnel.Status())
}

// handleTunnelStart starts the cloudflare tunnel in the background.
func (s *Server) handleTunnelStart(w http.ResponseWriter, r *http.Request) {
	if s.tunnel.IsRunning() {
		writeJSON(w, http.StatusOK, map[string]any{
			"running":   true,
			"publicURL": s.tunnel.PublicURL(),
		})
		return
	}

	// Start in background; frontend polls /tunnel/status.
	go func() {
		localAddr := fmt.Sprintf("http://localhost:%d", s.Port())
		url, err := s.tunnel.Start(context.Background(), localAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tunnel error: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  Internet:  %s\n", url)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"running": false,
		"status":  "starting",
	})
}

// handleTunnelStop stops the tunnel.
func (s *Server) handleTunnelStop(w http.ResponseWriter, r *http.Request) {
	s.tunnel.Stop()
	writeJSON(w, http.StatusOK, map[string]any{"running": false})
}

// handleTunnelLogin runs `cloudflared tunnel login` and surfaces the Cloudflare auth URL via
// /tunnel/status. Synchronous so a launch failure is returned to the UI, not just logged.
func (s *Server) handleTunnelLogin(w http.ResponseWriter, r *http.Request) {
	if err := s.tunnel.Login(context.Background()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.tunnel.Status())
}

// handleTunnelNamed brings up a persistent named tunnel bound to the posted hostname. Synchronous
// so a failure (not logged in, DNS zone not on account, edge connect) reaches the UI verbatim.
func (s *Server) handleTunnelNamed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Hostname) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "hostname required"})
		return
	}
	localAddr := fmt.Sprintf("http://localhost:%d", s.Port())
	if _, err := s.tunnel.StartNamed(context.Background(), req.Hostname, localAddr); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.tunnel.Status())
}
