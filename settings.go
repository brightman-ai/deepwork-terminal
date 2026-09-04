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
//
// # Why this one is rev-guarded and the store next door is merged
//
// Both files had the same wound — a whole-document PUT from a client holding a stale copy
// silently deleted everything it did not know about. For the store a per-key merge was enough,
// because its top-level keys (history / remotePeers / snippets) are independent and a client
// that knows nothing about a key has no opinion about it.
//
// That does not transfer here. The workbench's entire payload hangs off ONE key, `groups`, and
// the thing being lost is a tab INSIDE it. Merging `groups` wholesale is the same replace under
// a different name; unioning tabs by id is worse than the bug, because it resurrects tabs the
// user deliberately closed. "Absent" is genuinely ambiguous at tab granularity — deleted here,
// or never seen here? — and no server-side rule can tell the two apart.
//
// So the server stops guessing and starts refusing: every document carries a server-owned `rev`,
// a PUT must name the rev it edited, and a PUT built on a rev that has moved is rejected with the
// current document attached. The client is the only party that knows what it changed, so it is the
// only party that can resolve the conflict — it three-way merges against the base it loaded and
// retries (see workbenchMerge.ts). Deleted-here and never-seen-here are distinguishable there,
// and nowhere else.
//
// A PUT with NO rev field is a pre-rev client (a page that loaded the old bundle before this
// change). It is accepted as a plain replace, because rejecting it would brick that page's saves
// entirely; such a page keeps the old clobbering behaviour until it reloads, which the asset-hash
// updater makes it do on its own.
var (
	workbenchMu   sync.Mutex
	workbenchData json.RawMessage
)

func (s *Server) handleGetWorkbench(w http.ResponseWriter, r *http.Request) {
	workbenchMu.Lock()
	if workbenchData == nil {
		// Hydrate the in-memory cache from disk ONCE, and keep it — the old code read the file
		// and dropped it on the floor, so every GET re-read the disk and, worse, the rev compare
		// in handleSaveWorkbench would have had nothing to compare against after a restart.
		workbenchData = s.loadWorkbenchFromDisk()
	}
	data := workbenchData
	workbenchMu.Unlock()

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
	if workbenchData == nil {
		workbenchData = s.loadWorkbenchFromDisk()
	}
	current := workbenchData
	claimed, claimedOK := workbenchRev(raw)
	if claimedOK {
		if have, _ := workbenchRev(current); claimed != have {
			// Stale base. Hand back what is actually stored (rev included) so the client can
			// three-way merge onto it instead of guessing, and do NOT touch the stored doc.
			workbenchMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			if current == nil {
				w.Write([]byte("{}")) //nolint:errcheck
			} else {
				w.Write(current) //nolint:errcheck
			}
			return
		}
	}
	have, _ := workbenchRev(current)
	next := have + 1
	stamped := withWorkbenchRev(raw, next)
	workbenchData = stamped
	workbenchMu.Unlock()

	// Persist to disk
	s.saveWorkbenchToDisk(stamped)
	writeJSON(w, http.StatusOK, map[string]any{"rev": next})
}

// workbenchRev reads the server-owned revision out of a workbench document. The second return
// distinguishes "this document declares a rev" from "it does not" — the caller needs that to tell
// a pre-rev client (no opinion, accept its write) from a client claiming rev 0 (a document that
// has never been saved, which must still be compared).
func workbenchRev(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var probe struct {
		Rev *int64 `json:"rev"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil || probe.Rev == nil {
		return 0, false
	}
	return *probe.Rev, true
}

// withWorkbenchRev returns doc with `rev` set to n, preserving every other key verbatim. Falls
// back to the document unchanged if it is not a JSON object, so a malformed body can never fail
// a save in a way that loses the user's tabs.
func withWorkbenchRev(doc json.RawMessage, n int64) json.RawMessage {
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(doc, &m); err != nil {
		return doc
	}
	stamped, err := json.Marshal(n)
	if err != nil {
		return doc
	}
	m["rev"] = stamped
	out, err := json.Marshal(m)
	if err != nil {
		return doc
	}
	return out
}

// dataDir is where this deployment's own files live. It doubles as this deployment's IDENTITY
// (see sessionMeta.Origin): standalone and the pro embed share one per-user daemon but keep
// separate data dirs, and the data dir is exactly what makes their tab lists separate — so it is
// the honest answer to "whose tab list owns this session".
func (s *Server) dataDir() string {
	if dir := s.config.DataDir; dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dw-terminal")
}

func (s *Server) workbenchPath() string {
	return filepath.Join(s.dataDir(), "workbench.json")
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
	return filepath.Join(s.dataDir(), "store.json")
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
		// The sessions do not live in this process any more, so "is the server up" stopped
		// being the whole health story. If the daemon is unreachable the tab strip is empty
		// and every keystroke goes nowhere, and nothing else the product exposes would say
		// why. This is the one place that can.
		"daemon": s.mgr.DaemonHealth(),
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
