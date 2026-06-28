package terminal

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// authCodeAlphabet is an unambiguous uppercase set (no 0/O, 1/I/L, U) so the printed code is easy
// to read off the console and re-type without confusion. 30 chars.
const authCodeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

func generateAuthCode() string {
	// Human-friendly default: 8 chars grouped 4-4 with a hyphen (e.g. E3X1-M6T2). Easy to read and
	// type for the common local/LAN case. crypto/rand + rejection sampling keeps each pick uniform.
	//
	// Tradeoff: ~39-bit entropy (30^8), far below the old 128-bit hex. The auth path has no rate
	// limit, so this is only safe for local/LAN reach. Exposing this instance publicly (cloudflare
	// tunnel) should pair with a strong operator-set -auth-code rather than this generated default.
	const n = 8
	// Reject bytes in the biased tail so modulo stays uniform over the alphabet.
	limit := byte(256 - (256 % len(authCodeAlphabet)))
	out := make([]byte, 0, n+1)
	buf := make([]byte, 1)
	for i := 0; i < n; i++ {
		if i == 4 {
			out = append(out, '-')
		}
		for {
			rand.Read(buf) //nolint:errcheck
			if buf[0] < limit {
				out = append(out, authCodeAlphabet[int(buf[0])%len(authCodeAlphabet)])
				break
			}
		}
	}
	return string(out)
}

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
	data := storeData
	storeMu.Unlock()
	if data == nil {
		data = s.loadStoreFromDisk()
	}
	if data == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}")) //nolint:errcheck
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data) //nolint:errcheck
}

func (s *Server) handleSaveStore(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	storeMu.Lock()
	storeData = raw
	storeMu.Unlock()
	s.saveStoreToDisk(raw)
	w.WriteHeader(http.StatusNoContent)
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

// handleSystem returns system info for the settings page.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"port":   s.Port(),
		"pid":    os.Getpid(),
		"commit": "dev",
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
