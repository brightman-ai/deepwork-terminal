package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

func generateAuthCode() string {
	b := make([]byte, 4)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b) // 8-char hex code like "a3f7b2c1"
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
		"tunnel": map[string]any{
			"running":   s.tunnel.IsRunning(),
			"publicURL": s.tunnel.PublicURL(),
		},
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, settings)
}

// handleTunnelStatus returns the current tunnel state.
func (s *Server) handleTunnelStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"running":   s.tunnel.IsRunning(),
		"publicURL": s.tunnel.PublicURL(),
	})
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
