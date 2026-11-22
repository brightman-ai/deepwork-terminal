package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	toml "github.com/pelletier/go-toml/v2"
)

const profileProbeTimeout = 3 * time.Second

// ProfileItem is the wire representation of an isolated CLI profile.
// Security invariant: raw API keys MUST NEVER appear on this struct or any HTTP payload.
type ProfileItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	Kind         string `json:"kind"`
	Model        string `json:"model"`
	BaseURL      string `json:"baseUrl,omitempty"`
	APIKeyMasked string `json:"apiKeyMasked,omitempty"`
	HasAPIKey    bool   `json:"hasApiKey"`
	ConfigDir    string `json:"configDir"`
	IsDefault    bool   `json:"isDefault"`
	Badge        string `json:"badge"`
}

// storedProfile is the on-disk representation persisted in <dataDir>/profiles.json (0600).
type storedProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Kind      string `json:"kind"`
	Model     string `json:"model"`
	BaseURL   string `json:"baseUrl,omitempty"`
	APIKey    string `json:"apiKey,omitempty"`
	ConfigDir string `json:"configDir"`
	IsDefault bool   `json:"isDefault"`
	Badge     string `json:"badge"`
	SourceDir string `json:"sourceDir,omitempty"`
}

type profilesDiskDoc struct {
	DefaultProfileID string          `json:"defaultProfileId"`
	Profiles         []storedProfile `json:"profiles"`
}

type profileStoreLock struct {
	mu sync.Mutex
}

var (
	profileStoresByDir      sync.Map // map[string]*profileStoreLock
	worktreeProfileMuxBound sync.Map // map[*http.ServeMux]struct{}
)

// registerWorktreeAndProfileRoutes wires all Git worktree and isolated CLI profile HTTP routes
// onto s.mux without the /api prefix (matching Server.registerRoutes). Idempotent per ServeMux.
func (s *Server) registerWorktreeAndProfileRoutes(wrap func(http.HandlerFunc) http.HandlerFunc) {
	if s == nil || s.mux == nil {
		return
	}
	if wrap == nil {
		wrap = s.authWrap
	}
	if _, already := worktreeProfileMuxBound.LoadOrStore(s.mux, struct{}{}); already {
		return
	}

	s.mux.HandleFunc("GET /git/worktrees", wrap(s.handleListWorktrees))
	s.mux.HandleFunc("GET /git/worktrees/all", wrap(s.handleListAllWorktrees))
	s.mux.HandleFunc("POST /git/worktrees", wrap(s.handleCreateWorktree))
	s.mux.HandleFunc("POST /git/worktrees/merge", wrap(s.handleMergeWorktree))
	s.mux.HandleFunc("POST /git/worktrees/remove", wrap(s.handleRemoveWorktree))

	s.mux.HandleFunc("GET /profiles", wrap(s.handleListProfiles))
	s.mux.HandleFunc("POST /profiles", wrap(s.handleCreateProfile))
	s.mux.HandleFunc("PUT /profiles/{id}", wrap(s.handleUpdateProfile))
	s.mux.HandleFunc("DELETE /profiles/{id}", wrap(s.handleDeleteProfile))
	s.mux.HandleFunc("POST /profiles/scan", wrap(s.handleScanProfiles))
	s.mux.HandleFunc("POST /profiles/test", wrap(s.handleTestProfile))
}

func (s *Server) profileLock() *profileStoreLock {
	dir := filepath.Clean(s.dataDir())
	actual, _ := profileStoresByDir.LoadOrStore(dir, &profileStoreLock{})
	return actual.(*profileStoreLock)
}

func (s *Server) profilesFilePath() string {
	return filepath.Join(s.dataDir(), "profiles.json")
}

func (s *Server) profileIsolatedDir(id string) string {
	return filepath.Join(s.dataDir(), "profiles", id)
}

// ensureProfileDirs guarantees <dataDir>/profiles/<id>/{claude,codex} exist with strict 0700 permissions.
func (s *Server) ensureProfileDirs(id string) (string, error) {
	if !isSafeProfileID(id) {
		return "", errors.New("invalid profile id")
	}
	root := s.profileIsolatedDir(id)
	claudeDir := filepath.Join(root, "claude")
	codexDir := filepath.Join(root, "codex")
	for _, d := range []string{root, claudeDir, codexDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return "", err
		}
	}
	return root, nil
}

func isSafeProfileID(id string) bool {
	if id == "" || len(id) > 96 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

func maskAPIKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) <= 4 {
		return "****"
	}
	if len(raw) <= 8 {
		return raw[:2] + "****"
	}
	return raw[:4] + "****" + raw[len(raw)-4:]
}

func toProfileItem(p storedProfile) ProfileItem {
	hasKey := strings.TrimSpace(p.APIKey) != ""
	var masked string
	if hasKey {
		masked = maskAPIKey(p.APIKey)
	}
	return ProfileItem{
		ID:           p.ID,
		Name:         p.Name,
		Engine:       p.Engine,
		Kind:         p.Kind,
		Model:        p.Model,
		BaseURL:      p.BaseURL,
		APIKeyMasked: masked,
		HasAPIKey:    hasKey,
		ConfigDir:    p.ConfigDir,
		IsDefault:    p.IsDefault,
		Badge:        p.Badge,
	}
}

func toProfileItems(list []storedProfile) []ProfileItem {
	out := make([]ProfileItem, 0, len(list))
	for _, p := range list {
		out = append(out, toProfileItem(p))
	}
	return out
}

func normalizeProfileEngine(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "claude", nil
	}
	switch v {
	case "claude", "codex", "shell":
		return v, nil
	default:
		return "", fmt.Errorf("invalid engine %q (want claude, codex, or shell)", raw)
	}
}

func normalizeProfileKind(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == "local" {
		return "oauth", nil
	}
	switch v {
	case "oauth", "gateway", "imported":
		return v, nil
	default:
		return "", fmt.Errorf("invalid kind %q (want oauth, gateway, or imported)", raw)
	}
}

func defaultBadgeForProfile(kind, engine string) string {
	switch kind {
	case "gateway":
		return "独立网关"
	case "imported":
		return "本机导入"
	default:
		if engine == "shell" {
			return "纯净 Shell"
		}
		return "独立 OAuth"
	}
}

func defaultModelForEngine(engine string) string {
	switch engine {
	case "codex":
		return "gpt-5.3-codex"
	case "shell":
		return ""
	default:
		return "claude-sonnet-4-6"
	}
}

// loadOrSeedProfilesLocked reads <dataDir>/profiles.json and auto-seeds a default system profile
// when none exists yet. Caller MUST hold s.profileLock().mu.
func (s *Server) loadOrSeedProfilesLocked() (*profilesDiskDoc, error) {
	path := s.profilesFilePath()
	var doc profilesDiskDoc
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if unmarshalErr := json.Unmarshal(data, &doc); unmarshalErr != nil {
			return nil, fmt.Errorf("corrupt profiles.json: %w", unmarshalErr)
		}
	}

	if len(doc.Profiles) == 0 {
		const seedID = "prof-default"
		cfgDir, err := s.ensureProfileDirs(seedID)
		if err != nil {
			return nil, err
		}
		doc = profilesDiskDoc{
			DefaultProfileID: seedID,
			Profiles: []storedProfile{
				{
					ID:        seedID,
					Name:      "Claude 默认配置",
					Engine:    "claude",
					Kind:      "oauth",
					Model:     "claude-sonnet-4-6",
					ConfigDir: cfgDir,
					IsDefault: true,
					Badge:     "默认隔离目录",
				},
			},
		}
		if err := s.saveProfilesLocked(&doc); err != nil {
			return nil, err
		}
		return &doc, nil
	}

	// Reconcile default flags + isolated directories for all persisted profiles.
	reconcileDefaultProfile(&doc)
	needsSave := false
	for i := range doc.Profiles {
		p := &doc.Profiles[i]
		if isSafeProfileID(p.ID) {
			if cfgDir, err := s.ensureProfileDirs(p.ID); err == nil && p.ConfigDir == "" {
				p.ConfigDir = cfgDir
			}
		}
		if p.Kind == "imported" {
			var legacyDir string
			engine := p.Engine
			switch {
			case strings.HasPrefix(p.Name, "Claude · ~/."):
				legacyDir = strings.TrimPrefix(p.Name, "Claude · ~/")
				if engine == "" {
					engine = "claude"
				}
			case strings.HasPrefix(p.Name, "Codex · ~/."):
				legacyDir = strings.TrimPrefix(p.Name, "Codex · ~/")
				if engine == "" {
					engine = "codex"
				}
			}
			if legacyDir != "" {
				if cleaned := formatScannedProfileName(engine, legacyDir); cleaned != p.Name {
					p.Name = cleaned
					needsSave = true
				}
			}
		}
	}
	if needsSave {
		if err := s.saveProfilesLocked(&doc); err != nil {
			return nil, err
		}
	}
	return &doc, nil
}

func reconcileDefaultProfile(doc *profilesDiskDoc) {
	if len(doc.Profiles) == 0 {
		doc.DefaultProfileID = ""
		return
	}
	chosenIdx := -1
	if doc.DefaultProfileID != "" {
		for i := range doc.Profiles {
			if doc.Profiles[i].ID == doc.DefaultProfileID {
				chosenIdx = i
				break
			}
		}
	}
	if chosenIdx < 0 {
		for i := range doc.Profiles {
			if doc.Profiles[i].IsDefault {
				chosenIdx = i
				break
			}
		}
	}
	if chosenIdx < 0 {
		chosenIdx = 0
	}
	doc.DefaultProfileID = doc.Profiles[chosenIdx].ID
	for i := range doc.Profiles {
		doc.Profiles[i].IsDefault = (i == chosenIdx)
	}
}

func (s *Server) saveProfilesLocked(doc *profilesDiskDoc) error {
	reconcileDefaultProfile(doc)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	path := s.profilesFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmpPath := path + ".tmp." + uuid.NewString()[:8]
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// handleListProfiles handles GET /profiles.
func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	lock := s.profileLock()
	lock.mu.Lock()
	doc, err := s.loadOrSeedProfilesLocked()
	lock.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"profiles":         toProfileItems(doc.Profiles),
		"defaultProfileId": doc.DefaultProfileID,
	})
}

type createProfileRequest struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	Kind      string `json:"kind"`
	Model     string `json:"model"`
	BaseURL   string `json:"baseUrl"`
	APIKey    string `json:"apiKey"`
	IsDefault bool   `json:"isDefault"`
	Badge     string `json:"badge"`
}

// handleCreateProfile handles POST /profiles.
func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req createProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	engine, err := normalizeProfileEngine(req.Engine)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	kind, err := normalizeProfileKind(req.Kind)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL != "" {
		if err := validateHTTPBaseURL(baseURL); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	lock := s.profileLock()
	lock.mu.Lock()
	defer lock.mu.Unlock()

	doc, err := s.loadOrSeedProfilesLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	slug := sanitizeSlugToken(name)
	if slug == "" {
		slug = engine
	}
	if len(slug) > 24 {
		slug = strings.Trim(slug[:24], "-_")
	}
	id := fmt.Sprintf("prof-%s-%s", slug, uuid.NewString()[:6])

	cfgDir, err := s.ensureProfileDirs(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = defaultModelForEngine(engine)
	}
	badge := strings.TrimSpace(req.Badge)
	if badge == "" {
		badge = defaultBadgeForProfile(kind, engine)
	}

	prof := storedProfile{
		ID:        id,
		Name:      name,
		Engine:    engine,
		Kind:      kind,
		Model:     model,
		BaseURL:   baseURL,
		APIKey:    strings.TrimSpace(req.APIKey),
		ConfigDir: cfgDir,
		IsDefault: req.IsDefault,
		Badge:     badge,
	}

	if req.IsDefault {
		for i := range doc.Profiles {
			doc.Profiles[i].IsDefault = false
		}
		doc.DefaultProfileID = id
	}
	doc.Profiles = append(doc.Profiles, prof)

	if err := s.saveProfilesLocked(doc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Re-fetch after default reconciliation so IsDefault reflects truth.
	created := doc.Profiles[len(doc.Profiles)-1]
	writeJSON(w, http.StatusCreated, map[string]any{
		"profile": toProfileItem(created),
	})
}

type updateProfileRequest struct {
	Name        *string `json:"name"`
	Engine      *string `json:"engine"`
	Kind        *string `json:"kind"`
	Model       *string `json:"model"`
	BaseURL     *string `json:"baseUrl"`
	APIKey      *string `json:"apiKey"`
	ClearAPIKey *bool   `json:"clearApiKey"`
	IsDefault   *bool   `json:"isDefault"`
	Badge       *string `json:"badge"`
}

// handleUpdateProfile handles PUT /profiles/{id}.
func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !isSafeProfileID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid profile id"})
		return
	}

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	lock := s.profileLock()
	lock.mu.Lock()
	defer lock.mu.Unlock()

	doc, err := s.loadOrSeedProfilesLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	idx := -1
	for i := range doc.Profiles {
		if doc.Profiles[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "profile not found"})
		return
	}

	target := &doc.Profiles[idx]
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name cannot be empty"})
			return
		}
		target.Name = n
	}
	if req.Engine != nil {
		eng, engErr := normalizeProfileEngine(*req.Engine)
		if engErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": engErr.Error()})
			return
		}
		target.Engine = eng
	}
	if req.Kind != nil {
		k, kindErr := normalizeProfileKind(*req.Kind)
		if kindErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": kindErr.Error()})
			return
		}
		target.Kind = k
	}
	if req.Model != nil {
		target.Model = strings.TrimSpace(*req.Model)
	}
	if req.BaseURL != nil {
		u := strings.TrimSpace(*req.BaseURL)
		if u != "" {
			if urlErr := validateHTTPBaseURL(u); urlErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": urlErr.Error()})
				return
			}
		}
		target.BaseURL = u
	}
	if req.ClearAPIKey != nil && *req.ClearAPIKey {
		target.APIKey = ""
	} else if req.APIKey != nil {
		trimmed := strings.TrimSpace(*req.APIKey)
		// Guard against clients accidentally echoing back the masked placeholder.
		if trimmed != "" && trimmed != maskAPIKey(target.APIKey) {
			target.APIKey = trimmed
		}
	}
	if req.Badge != nil {
		if b := strings.TrimSpace(*req.Badge); b != "" {
			target.Badge = b
		}
	}
	if req.IsDefault != nil && *req.IsDefault {
		for i := range doc.Profiles {
			doc.Profiles[i].IsDefault = (i == idx)
		}
		doc.DefaultProfileID = target.ID
	}

	if _, err := s.ensureProfileDirs(target.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.saveProfilesLocked(doc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"profile": toProfileItem(doc.Profiles[idx]),
	})
}

// handleDeleteProfile handles DELETE /profiles/{id}.
func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !isSafeProfileID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid profile id"})
		return
	}

	lock := s.profileLock()
	lock.mu.Lock()
	defer lock.mu.Unlock()

	doc, err := s.loadOrSeedProfilesLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	idx := -1
	for i := range doc.Profiles {
		if doc.Profiles[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "profile not found"})
		return
	}

	doc.Profiles = append(doc.Profiles[:idx], doc.Profiles[idx+1:]...)
	if doc.DefaultProfileID == id {
		doc.DefaultProfileID = ""
	}

	// Clean up only our isolated <dataDir>/profiles/<id> directory — never touch external scanned paths.
	profilesRoot := filepath.Join(s.dataDir(), "profiles")
	isolatedDir := s.profileIsolatedDir(id)
	if isStrictChildPath(isolatedDir, profilesRoot) {
		_ = os.RemoveAll(isolatedDir)
	}

	if err := s.saveProfilesLocked(doc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleScanProfiles handles POST /profiles/scan.
// Scans ~/.claude* and ~/.codex* strictly READ-ONLY and imports non-duplicate profiles.
func (s *Server) handleScanProfiles(w http.ResponseWriter, r *http.Request) {
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot resolve user home directory"})
		return
	}

	entries, err := os.ReadDir(homeDir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"imported": 0,
			"profiles": []ProfileItem{},
		})
		return
	}

	type candidate struct {
		dirName string
		absPath string
		engine  string
		model   string
		baseURL string
	}
	var discovered []candidate
	addCandidate := func(name, engine string) {
		absPath := filepath.Join(homeDir, name)
		info, statErr := os.Stat(absPath)
		if statErr != nil || !info.IsDir() {
			return
		}
		model, baseURL := inspectExternalAgentConfigReadOnly(absPath, engine)
		discovered = append(discovered, candidate{
			dirName: name,
			absPath: filepath.Clean(absPath),
			engine:  engine,
			model:   model,
			baseURL: baseURL,
		})
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case name == ".claude-profiles":
			children, _ := os.ReadDir(filepath.Join(homeDir, name))
			for _, child := range children {
				addCandidate(filepath.Join(name, child.Name()), "claude")
			}
		case strings.HasPrefix(name, ".claude"):
			addCandidate(name, "claude")
		case strings.HasPrefix(name, ".codex"):
			addCandidate(name, "codex")
		}
	}

	lock := s.profileLock()
	lock.mu.Lock()
	defer lock.mu.Unlock()

	doc, err := s.loadOrSeedProfilesLocked()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	imported := 0
	for _, cand := range discovered {
		alreadyExists := false
		for _, existing := range doc.Profiles {
			if (existing.SourceDir != "" && samePath(existing.SourceDir, cand.absPath)) ||
				(existing.ConfigDir != "" && samePath(existing.ConfigDir, cand.absPath)) {
				alreadyExists = true
				break
			}
		}
		if alreadyExists {
			continue
		}

		slug := sanitizeSlugToken(strings.TrimPrefix(cand.dirName, "."))
		if slug == "" {
			slug = cand.engine
		}
		id := "prof-scan-" + slug
		if profileIDExists(doc.Profiles, id) {
			id = fmt.Sprintf("prof-scan-%s-%s", slug, uuid.NewString()[:6])
		}

		cfgDir, dirErr := s.ensureProfileDirs(id)
		if dirErr != nil {
			continue
		}

		doc.Profiles = append(doc.Profiles, storedProfile{
			ID:        id,
			Name:      formatScannedProfileName(cand.engine, cand.dirName),
			Engine:    cand.engine,
			Kind:      "imported",
			Model:     cand.model,
			BaseURL:   cand.baseURL,
			ConfigDir: cfgDir,
			IsDefault: false,
			Badge:     "~/" + cand.dirName,
			SourceDir: cand.absPath,
		})
		imported++
	}

	if imported > 0 {
		if err := s.saveProfilesLocked(doc); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported,
		"profiles": toProfileItems(doc.Profiles),
	})
}

func formatScannedProfileName(engine, dirName string) string {
	displayEngine := "Claude"
	if engine == "codex" {
		displayEngine = "Codex"
	}
	dirName = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(dirName), "~/"))
	switch {
	case strings.HasPrefix(dirName, ".claude-profiles/"):
		return fmt.Sprintf("Claude · %s", filepath.Base(dirName))
	case dirName == ".claude":
		return "Claude · 本机默认"
	case dirName == ".codex":
		return "Codex · 本机默认"
	default:
		return fmt.Sprintf("%s · %s", displayEngine, strings.TrimPrefix(dirName, "."))
	}
}

func profileIDExists(list []storedProfile, id string) bool {
	for _, p := range list {
		if p.ID == id {
			return true
		}
	}
	return false
}

// inspectExternalAgentConfigReadOnly reads non-secret model/baseUrl hints from ~/.claude* or ~/.codex*
// without ever writing to or mutating the user's home directories.
func inspectExternalAgentConfigReadOnly(dir, engine string) (model, baseURL string) {
	const maxConfigBytes = 64 << 10
	if engine == "claude" {
		type claudeConfig struct {
			Model string `json:"model"`
			Env   struct {
				Model   string `json:"ANTHROPIC_MODEL"`
				Opus    string `json:"ANTHROPIC_DEFAULT_OPUS_MODEL"`
				Sonnet  string `json:"ANTHROPIC_DEFAULT_SONNET_MODEL"`
				Haiku   string `json:"ANTHROPIC_DEFAULT_HAIKU_MODEL"`
				BaseURL string `json:"ANTHROPIC_BASE_URL"`
			} `json:"env"`
		}
		readConfig := func(path string) claudeConfig {
			file, err := os.Open(path)
			if err != nil {
				return claudeConfig{}
			}
			defer file.Close()
			var parsed claudeConfig
			raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
			if err != nil || len(raw) > maxConfigBytes || json.Unmarshal(raw, &parsed) != nil {
				return claudeConfig{}
			}
			return parsed
		}
		envModel := func(cfg claudeConfig) string {
			return firstTrimmedNonempty(cfg.Env.Model, cfg.Env.Opus, cfg.Env.Sonnet, cfg.Env.Haiku)
		}
		dir = filepath.Clean(dir)
		settings := readConfig(filepath.Join(dir, "settings.json"))
		model = envModel(settings)
		baseURL = strings.TrimSpace(settings.Env.BaseURL)
		parent := filepath.Dir(dir)
		if filepath.Base(parent) == ".claude-profiles" {
			alias := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(settings.Model)), "[1m]")
			fallbackModel := model == "" && (alias == "" || alias == "opus" || alias == "sonnet" || alias == "haiku" || alias == "claude-fable-5-2")
			if fallbackModel || baseURL == "" {
				profile := readConfig(filepath.Join(filepath.Dir(parent), ".claude", "profiles", filepath.Base(dir)+".json"))
				if fallbackModel {
					model = firstTrimmedNonempty(envModel(profile), profile.Model)
				}
				baseURL = firstTrimmedNonempty(baseURL, profile.Env.BaseURL)
			}
		}
		return firstTrimmedNonempty(model, settings.Model, "claude-sonnet-4-6"), sanitizeScannedBaseURL(baseURL)
	}

	model = "gpt-5.3-codex"
	f, err := os.Open(filepath.Join(dir, "config.toml"))
	if err != nil {
		return model, ""
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes))
	if err != nil {
		return model, ""
	}
	var parsed struct {
		Model   string `toml:"model"`
		BaseURL string `toml:"base_url"`
	}
	if toml.Unmarshal(raw, &parsed) == nil {
		if m := strings.TrimSpace(parsed.Model); m != "" {
			model = m
		}
		if u := strings.TrimSpace(parsed.BaseURL); u != "" {
			baseURL = sanitizeScannedBaseURL(u)
		}
	}
	return model, baseURL
}

func sanitizeScannedBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsedURL, err := url.Parse(raw)
	if err != nil || parsedURL.User != nil || validateHTTPBaseURL(raw) != nil {
		return ""
	}
	parsedURL.RawQuery, parsedURL.Fragment, parsedURL.RawFragment, parsedURL.ForceQuery = "", "", "", false
	return parsedURL.String()
}

func firstTrimmedNonempty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

type testProfileRequest struct {
	BaseURL   string `json:"baseUrl"`
	APIKey    string `json:"apiKey"`
	ProfileID string `json:"profileId"`
}

// handleTestProfile handles POST /profiles/test with a strict 3s HTTP probe timeout.
func (s *Server) handleTestProfile(w http.ResponseWriter, r *http.Request) {
	var req testProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)

	if req.ProfileID != "" && (baseURL == "" || apiKey == "") {
		lock := s.profileLock()
		lock.mu.Lock()
		doc, err := s.loadOrSeedProfilesLocked()
		lock.mu.Unlock()
		if err == nil {
			for _, p := range doc.Profiles {
				if p.ID == req.ProfileID {
					if baseURL == "" {
						baseURL = p.BaseURL
					}
					if apiKey == "" {
						apiKey = p.APIKey
					}
					break
				}
			}
		}
	}

	if err := validateHTTPBaseURL(baseURL); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"latencyMs": int64(0),
			"message":   err.Error(),
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), profileProbeTimeout)
	defer cancel()

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"latencyMs": int64(0),
			"message":   "invalid probe request",
		})
		return
	}
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		httpReq.Header.Set("x-api-key", apiKey)
	}

	client := &http.Client{Timeout: profileProbeTimeout}
	resp, err := client.Do(httpReq)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		safeURL := *httpReq.URL
		safeURL.User = nil
		msg := "probe failed: " + safeURL.String()
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			msg += ": request timed out"
		case errors.Is(err, context.Canceled):
			msg += ": request canceled"
		}
		if apiKey != "" {
			msg = strings.ReplaceAll(msg, apiKey, "[REDACTED]")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        false,
			"latencyMs": latencyMs,
			"message":   msg,
		})
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()

	ok := resp.StatusCode >= 200 && resp.StatusCode < 400
	var msg string
	switch {
	case ok:
		msg = fmt.Sprintf("连接成功 (HTTP %d)", resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		msg = fmt.Sprintf("鉴权失败 (HTTP %d)", resp.StatusCode)
	default:
		msg = fmt.Sprintf("网关返回 HTTP %d", resp.StatusCode)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        ok,
		"latencyMs": latencyMs,
		"message":   msg,
	})
}

func validateHTTPBaseURL(raw string) error {
	if raw == "" {
		return errors.New("baseUrl is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("invalid baseUrl")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("baseUrl scheme must be http or https")
	}
	return nil
}

// resolveProfileEnv overlays isolated directory variables (CLAUDE_CONFIG_DIR, CODEX_HOME) and
// profile-specific gateway/model credentials onto base (KEY=VALUE strings) without mutating
// any running shell or global server environment.
func (s *Server) resolveProfileEnv(profileID string, base []string) []string {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" || s == nil {
		return base
	}

	lock := s.profileLock()
	lock.mu.Lock()
	doc, err := s.loadOrSeedProfilesLocked()
	lock.mu.Unlock()
	if err != nil || doc == nil {
		return base
	}

	var found *storedProfile
	for i := range doc.Profiles {
		if doc.Profiles[i].ID == profileID {
			found = &doc.Profiles[i]
			break
		}
	}
	if found == nil {
		return base
	}

	profileRoot, err := s.ensureProfileDirs(found.ID)
	if err != nil {
		return base
	}

	claudeDir := filepath.Join(profileRoot, "claude")
	codexDir := filepath.Join(profileRoot, "codex")
	if found.Kind == "imported" && found.SourceDir != "" {
		switch found.Engine {
		case "claude":
			claudeDir = found.SourceDir
		case "codex":
			codexDir = found.SourceDir
		}
	}

	ov := &envOverlay{
		Set: map[string]string{
			"DW_PROFILE_ID":     found.ID,
			"CLAUDE_CONFIG_DIR": claudeDir,
			"CODEX_HOME":        codexDir,
		},
	}

	if found.Kind == "gateway" {
		if found.Engine == "codex" {
			if found.BaseURL != "" {
				ov.Set["OPENAI_BASE_URL"] = found.BaseURL
			}
			if found.APIKey != "" {
				ov.Set["OPENAI_API_KEY"] = found.APIKey
				ov.Set["CODEX_API_KEY"] = found.APIKey
			}
			if found.Model != "" {
				ov.Set["OPENAI_MODEL"] = found.Model
			}
		} else {
			if found.BaseURL != "" {
				ov.Set["ANTHROPIC_BASE_URL"] = found.BaseURL
			}
			if found.APIKey != "" {
				ov.Set["ANTHROPIC_API_KEY"] = found.APIKey
				ov.Set["ANTHROPIC_AUTH_TOKEN"] = found.APIKey
			}
			if found.Model != "" {
				ov.Set["ANTHROPIC_MODEL"] = found.Model
			}
		}
	} else {
		// OAuth / imported non-gateway profiles strip ambient gateway overrides from base so an
		// isolated OAuth session never silently routes through a leftover parent gateway URL.
		ov.Unset = []string{
			"ANTHROPIC_BASE_URL",
			"ANTHROPIC_API_KEY",
			"ANTHROPIC_AUTH_TOKEN",
			"OPENAI_BASE_URL",
			"OPENAI_API_KEY",
		}
		if found.BaseURL != "" {
			if found.Engine == "codex" {
				ov.Set["OPENAI_BASE_URL"] = found.BaseURL
			} else {
				ov.Set["ANTHROPIC_BASE_URL"] = found.BaseURL
			}
		}
		if found.Model != "" {
			if found.Engine == "codex" {
				ov.Set["OPENAI_MODEL"] = found.Model
			} else if found.Engine == "claude" {
				ov.Set["ANTHROPIC_MODEL"] = found.Model
			}
		}
	}

	return applyEnvOverlay(base, ov)
}
