package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// A custom provider NAME is not evidence. Only a configuration explicitly using
// OpenAI authentication, the default service URL and ChatGPT login declares an
// official subscription alias. Custom URLs/API-key overrides remain unverified.
func officialCodexProviderDeclaration(home string) string {
	for _, key := range []string{"OPENAI_BASE_URL", "OPENAI_API_BASE", "CHATGPT_BASE_URL", "CODEX_CHATGPT_BASE_URL", "OPENAI_API_KEY"} {
		if os.Getenv(key) != "" {
			return ""
		}
	}
	if configuredHome := os.Getenv("CODEX_HOME"); configuredHome != "" && filepath.Clean(configuredHome) != filepath.Clean(home) {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		return ""
	}
	var config struct {
		Provider  string `toml:"model_provider"`
		Providers map[string]struct {
			BaseURL      string `toml:"base_url"`
			WireAPI      string `toml:"wire_api"`
			RequiresAuth bool   `toml:"requires_openai_auth"`
		} `toml:"model_providers"`
	}
	if toml.Unmarshal(data, &config) != nil || config.Provider == "" {
		return ""
	}
	provider, ok := config.Providers[config.Provider]
	if !ok || !provider.RequiresAuth || provider.BaseURL != "" || provider.WireAPI != "responses" {
		return ""
	}
	data, err = os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return ""
	}
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.Mode != "chatgpt" || auth.Tokens.AccessToken == "" {
		return ""
	}
	return config.Provider
}
