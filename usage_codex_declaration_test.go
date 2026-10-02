package terminal

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestOfficialCodexProviderDeclaration_RequiresCompleteEvidence(t *testing.T) {
	for _, key := range []string{"OPENAI_BASE_URL", "OPENAI_API_BASE", "CHATGPT_BASE_URL", "CODEX_CHATGPT_BASE_URL", "OPENAI_API_KEY", "CODEX_HOME"} {
		t.Setenv(key, "")
	}
	home := t.TempDir()
	config := `model_provider = "my-official-alias"
[model_providers.my-official-alias]
name = "A custom display name"
wire_api = "responses"
requires_openai_auth = true
`
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600))
	require.Empty(t, officialCodexProviderDeclaration(home), "no authenticated account")
	require.NoError(t, os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"fixture-token"}}`), 0600))
	require.Equal(t, "my-official-alias", officialCodexProviderDeclaration(home))
	t.Setenv("OPENAI_BASE_URL", "https://relay.invalid")
	require.Empty(t, officialCodexProviderDeclaration(home), "an environment override changes the destination")
	t.Setenv("OPENAI_BASE_URL", "")
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.toml"), []byte(config+`base_url = "https://relay.invalid"`), 0600))
	require.Empty(t, officialCodexProviderDeclaration(home), "never infer ownership of a custom relay")
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"fixture-key"}`), 0600))
	require.Empty(t, officialCodexProviderDeclaration(home), "API billing is not a ChatGPT subscription")
}
