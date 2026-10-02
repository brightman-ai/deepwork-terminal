package terminal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brightman-ai/kit/transcript"
	"github.com/brightman-ai/kit/usage"
	"github.com/pelletier/go-toml/v2"
)

func codexHomeMatchesEnvironment(home string) bool {
	configuredHome := os.Getenv("CODEX_HOME")
	return configuredHome == "" || filepath.Clean(configuredHome) == filepath.Clean(home)
}

type codexProviderSettings struct {
	BaseURL      string `toml:"base_url"`
	WireAPI      string `toml:"wire_api"`
	RequiresAuth bool   `toml:"requires_openai_auth"`
}

// codexConfigSnapshot keeps the provider id, its endpoint settings, and auth-file shape
// from one read of each file. The attribution rules below must not combine two config
// generations if the user edits config.toml while quota polling is in flight.
type codexConfigSnapshot struct {
	homeMatchesEnvironment bool
	targetEnvironmentSet   bool
	configMissing          bool
	configReadable         bool
	providerID             string
	providers              map[string]codexProviderSettings
	chatGPTAuth            bool
}

func readCodexConfigSnapshot(home string) codexConfigSnapshot {
	snapshot := codexConfigSnapshot{homeMatchesEnvironment: codexHomeMatchesEnvironment(home)}
	if !snapshot.homeMatchesEnvironment {
		return snapshot
	}
	for _, key := range []string{"OPENAI_BASE_URL", "OPENAI_API_BASE", "CHATGPT_BASE_URL", "CODEX_CHATGPT_BASE_URL", "OPENAI_API_KEY"} {
		if os.Getenv(key) != "" {
			snapshot.targetEnvironmentSet = true
			break
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if os.IsNotExist(err) {
		snapshot.configMissing = true
	} else if err != nil {
		return snapshot
	} else {
		var config struct {
			Provider  string                           `toml:"model_provider"`
			Providers map[string]codexProviderSettings `toml:"model_providers"`
		}
		if toml.Unmarshal(data, &config) != nil {
			return snapshot
		}
		snapshot.configReadable = true
		snapshot.providerID = config.Provider
		snapshot.providers = config.Providers
	}

	authData, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return snapshot
	}
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	snapshot.chatGPTAuth = json.Unmarshal(authData, &auth) == nil && auth.Mode == "chatgpt" && auth.Tokens.AccessToken != ""
	return snapshot
}

func (snapshot codexConfigSnapshot) officialProviderDeclaration() string {
	if !snapshot.homeMatchesEnvironment || snapshot.targetEnvironmentSet || !snapshot.configReadable || snapshot.providerID == "" || !snapshot.chatGPTAuth {
		return ""
	}
	provider, ok := snapshot.providers[snapshot.providerID]
	if !ok || !provider.RequiresAuth || provider.BaseURL != "" || provider.WireAPI != "responses" {
		return ""
	}
	return snapshot.providerID
}

func (snapshot codexConfigSnapshot) directOpenAIProviderIsDefault() bool {
	if !snapshot.homeMatchesEnvironment || snapshot.targetEnvironmentSet {
		return false
	}
	if snapshot.configMissing {
		return true // no override file: Codex's built-in `openai` provider is the default
	}
	if !snapshot.configReadable {
		return false
	}
	// An older rollout without session_meta can only inherit Codex's default
	// provider. If the current config selects a custom provider, its endpoint
	// cannot be reconstructed from that rollout, so do not call it OpenAI.
	if snapshot.providerID != "" && snapshot.providerID != usage.VendorOpenAI {
		return false
	}
	provider, exists := snapshot.providers[usage.VendorOpenAI]
	return !exists || provider.BaseURL == ""
}

// A custom provider NAME is not evidence. Only a configuration explicitly using
// OpenAI authentication, the default service URL and ChatGPT login declares an
// official subscription alias. Custom URLs/API-key overrides remain unverified.
func officialCodexProviderDeclaration(home string) string {
	return readCodexConfigSnapshot(home).officialProviderDeclaration()
}

// reconcileCodexAttribution keeps endpoint ownership correct with released kit versions
// that require API keys before accepting provider declarations. The subscription store may
// contain keyless endpoint metadata, and the built-in ChatGPT OAuth profile is also keyless.
// Use only explicit host mappings or the narrowly verified official OAuth profile; never infer
// a vendor from an arbitrary provider name.
func reconcileCodexAttribution(quotas []usage.QuotaInfo, credentials *credentialStore) []usage.QuotaInfo {
	codexHome := transcript.CodexHome()
	config := readCodexConfigSnapshot(codexHome)
	if !config.homeMatchesEnvironment {
		return quotas
	}
	explicit := map[string]string(nil)
	if credentials != nil {
		explicit = credentials.explicitProviderVendorsSnapshot()
	}
	officialProviderID := config.officialProviderDeclaration()
	directOpenAIAllowed := config.directOpenAIProviderIsDefault()
	if len(explicit) == 0 && officialProviderID == "" && directOpenAIAllowed {
		return quotas
	}

	providers := recentCodexProviderIDs(time.Now())
	if len(providers) == 0 {
		return quotas
	}
	activeVendors := make(map[string]struct{})
	unknown := false
	untrustedOpenAIDeclaration := false
	for providerID := range providers {
		if providerID == unknownCodexProvider {
			unknown = true
			continue
		}
		if vendor, declared := explicit[providerID]; declared {
			if vendor == "" {
				unknown = true // conflicting host declarations
			} else {
				activeVendors[vendor] = struct{}{}
			}
			continue
		}
		if (providerID == "" || providerID == usage.VendorOpenAI) && !directOpenAIAllowed {
			unknown = true
			untrustedOpenAIDeclaration = true
		} else if providerID == "" || providerID == usage.VendorOpenAI || (officialProviderID != "" && providerID == officialProviderID) {
			activeVendors[usage.VendorOpenAI] = struct{}{}
		} else {
			unknown = true
		}
	}
	if len(activeVendors) == 0 {
		if untrustedOpenAIDeclaration {
			// Released kit versions treat the reserved id as OpenAI even when the user
			// redirects it. With no explicit mapping, remove that unsupported claim.
			for i := range quotas {
				q := &quotas[i]
				if q.Runtime == "codex" {
					q.Attribution = nil
				}
			}
		}
		return quotas
	}

	if len(activeVendors) == 1 && !unknown {
		var vendor string
		for vendor = range activeVendors {
		}
		owner := usage.Account{Runtime: "codex", Vendor: vendor}
		for i := range quotas {
			q := &quotas[i]
			if q.Runtime != owner.Runtime {
				continue
			}
			q.Attribution = &usage.Attribution{
				Active:  q.Vendor == vendor,
				Vendor:  vendor,
				Display: owner.Display(),
			}
		}
		return quotas
	}

	// Concurrent or partly unknown traffic can prove several accounts active, but cannot name
	// one biller. Preserve the active bits of every account with explicit evidence and suppress
	// cross-vendor claims that would imply exclusivity.
	for i := range quotas {
		q := &quotas[i]
		if q.Runtime != "codex" {
			continue
		}
		if q.Attribution == nil {
			q.Attribution = &usage.Attribution{}
		}
		_, q.Attribution.Active = activeVendors[q.Vendor]
		q.Attribution.Vendor = ""
		q.Attribution.Display = ""
		q.Attribution.ProviderID = ""
	}
	return quotas
}

const (
	codexAttributionFiles  = 24
	codexAttributionWindow = 30 * time.Minute
	unknownCodexProvider   = "\x00unreadable"
)

// recentCodexProviderIDs mirrors the bounded activity window used by kit/usage. It is
// intentionally used only for the OAuth alias case above, where a released kit can erase the
// provider id while merging concurrent accounts. The first session_meta line is the endpoint
// declaration; absent ids retain Codex's legacy default marker, which the caller accepts as
// OpenAI only when the current config does not redirect that default.
func recentCodexProviderIDs(now time.Time) map[string]struct{} {
	providers := make(map[string]struct{})
	for _, path := range transcript.NewestFiles(transcript.CodexSessionsRoot(), transcript.RolloutPrefix, transcript.JSONLSuffix, codexAttributionFiles) {
		info, err := os.Stat(path)
		if err != nil || now.Sub(info.ModTime()) > codexAttributionWindow {
			continue
		}
		file, err := os.Open(path) //nolint:gosec — read-only runtime transcript scan
		if err != nil {
			providers[unknownCodexProvider] = struct{}{}
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		readable := false
		if scanner.Scan() {
			var meta struct {
				Type    string `json:"type"`
				Payload struct {
					ModelProvider string `json:"model_provider"`
				} `json:"payload"`
			}
			if json.Unmarshal(scanner.Bytes(), &meta) == nil && meta.Type == "session_meta" {
				readable = true
				provider := strings.TrimSpace(meta.Payload.ModelProvider)
				if provider == "" {
					provider = usage.VendorOpenAI
				}
				providers[provider] = struct{}{}
			}
		}
		_ = file.Close()
		if !readable {
			// Do not claim a sole biller if a recent rollout could not provide its endpoint.
			providers[unknownCodexProvider] = struct{}{}
		}
	}
	return providers
}
