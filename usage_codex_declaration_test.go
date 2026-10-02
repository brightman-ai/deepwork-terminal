package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/brightman-ai/kit/usage"
	"github.com/stretchr/testify/require"
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

func TestDirectOpenAIProviderIsDefault_RespectsConfiguredDefaultProvider(t *testing.T) {
	for _, tc := range []struct {
		name       string
		providerID string
		want       bool
	}{
		{name: "built-in default", want: true},
		{name: "explicit OpenAI default", providerID: "openai", want: true},
		{name: "custom default", providerID: "my-relay", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := codexConfigSnapshot{
				homeMatchesEnvironment: true,
				configReadable:         true,
				providerID:             tc.providerID,
				providers:              map[string]codexProviderSettings{},
			}
			require.Equal(t, tc.want, snapshot.directOpenAIProviderIsDefault())
		})
	}

	t.Run("OpenAI redirected to custom endpoint", func(t *testing.T) {
		snapshot := codexConfigSnapshot{
			homeMatchesEnvironment: true,
			configReadable:         true,
			providerID:             "openai",
			providers: map[string]codexProviderSettings{
				"openai": {BaseURL: "https://relay.invalid"},
			},
		}
		require.False(t, snapshot.directOpenAIProviderIsDefault())
	})
}

func TestReconcileCodexAttributionClearsStaleOpenAIClaimForCustomDefault(t *testing.T) {
	home := t.TempDir()
	configureCodexAttributionTest(t, home, "custom-relay")
	writeCodexProviderRollout(t, home, "old-default", "") // legacy session_meta omits provider

	quotas := []usage.QuotaInfo{
		{Runtime: "codex", Vendor: usage.VendorOpenAI, Attribution: &usage.Attribution{Active: true, Vendor: usage.VendorOpenAI, Display: "OpenAI"}},
		{Runtime: "claude", Vendor: "anthropic", Attribution: &usage.Attribution{Active: true, Vendor: "anthropic"}},
	}
	got := reconcileCodexAttribution(quotas, nil)
	require.Nil(t, got[0].Attribution, "an old provider-less rollout cannot inherit OpenAI attribution after the default changed")
	require.True(t, got[1].Attribution.Active, "Codex reconciliation must not rewrite other runtimes")
}

func TestReconcileCodexAttributionTracksRepeatedDefaultProviderSwitches(t *testing.T) {
	home := t.TempDir()
	configureCodexAttributionTest(t, home, "custom-relay")
	writeCodexProviderRollout(t, home, "legacy", "") // no provider in this older rollout

	for i := 0; i < 100; i++ {
		provider, wantAttribution := "custom-relay", false
		if i%2 == 0 {
			provider, wantAttribution = "openai", true
		}
		configPath := filepath.Join(home, "config.toml")
		tmpPath := filepath.Join(home, "config-next-"+strconv.Itoa(i)+".toml")
		config := "model_provider = \"" + provider + "\"\n"
		require.NoError(t, os.WriteFile(tmpPath, []byte(config), 0o600))
		// Codex writes its active config atomically. The reader must pick up the new default
		// every time rather than cache a previous attribution decision for the old rollout.
		require.NoError(t, os.Rename(tmpPath, configPath))

		quotas := []usage.QuotaInfo{{
			Runtime: "codex", Vendor: usage.VendorOpenAI,
			Attribution: &usage.Attribution{Active: true, Vendor: usage.VendorOpenAI, Display: "OpenAI"},
		}}
		got := reconcileCodexAttribution(quotas, nil)
		if wantAttribution {
			require.NotNil(t, got[0].Attribution, "iteration %d: built-in OpenAI default should retain its declaration", i)
		} else {
			require.Nil(t, got[0].Attribution, "iteration %d: custom default must clear the stale OpenAI claim", i)
		}
	}
}

func TestReconcileCodexAttributionMarksConcurrentKnownAndUnknownProviders(t *testing.T) {
	home := t.TempDir()
	configureCodexAttributionTest(t, home, "custom-default")
	writeCodexProviderRollout(t, home, "known", "kimi-codex")
	writeCodexProviderRollout(t, home, "unknown", "another-relay")

	deepworkHome := t.TempDir()
	t.Setenv("DEEPWORK_HOME", deepworkHome)
	credentials := newCredentialStore(t.TempDir())
	seed(t, credentials, `{"version":1,"subscriptions":[{"vendor":"moonshot","api_key":"fixture-key","runtime_provider_ids":["kimi-codex"]}]}`)
	quotas := []usage.QuotaInfo{
		{Runtime: "codex", Vendor: usage.VendorOpenAI, Attribution: &usage.Attribution{Active: true, Vendor: usage.VendorOpenAI, Display: "OpenAI", ProviderID: "old"}},
		{Runtime: "codex", Vendor: usage.VendorMoonshot, Attribution: &usage.Attribution{Active: false, Vendor: usage.VendorMoonshot, Display: "Moonshot", ProviderID: "kimi-codex"}},
	}
	got := reconcileCodexAttribution(quotas, credentials)
	require.False(t, got[0].Attribution.Active, "an undeclared concurrent endpoint must not inherit another vendor's active claim")
	require.True(t, got[1].Attribution.Active, "the explicitly mapped endpoint remains known to be active")
	require.Empty(t, got[1].Attribution.Vendor, "mixed known/unknown traffic must not claim an exclusive biller")
	require.Empty(t, got[1].Attribution.Display)
}

func configureCodexAttributionTest(t *testing.T, home, provider string) {
	t.Helper()
	t.Setenv("DW_CODEX_HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("DEEPWORK_HOME", t.TempDir())
	for _, key := range []string{"OPENAI_BASE_URL", "OPENAI_API_BASE", "CHATGPT_BASE_URL", "CODEX_CHATGPT_BASE_URL", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
	config := "model_provider = \"" + provider + "\"\n"
	config += "[model_providers." + provider + "]\nname = \"fixture\"\nbase_url = \"https://relay.invalid\"\nwire_api = \"responses\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600))
}

func writeCodexProviderRollout(t *testing.T, home, name, provider string) {
	t.Helper()
	now := time.Now().UTC()
	dir := filepath.Join(home, "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	meta, err := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"model_provider": provider}})
	require.NoError(t, err)
	path := filepath.Join(dir, "rollout-"+name+".jsonl")
	require.NoError(t, os.WriteFile(path, append(meta, '\n'), 0o600))
	require.NoError(t, os.Chtimes(path, now, now))
}
