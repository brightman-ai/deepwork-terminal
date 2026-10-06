package terminal

// The credential store's SHARED location and one-time legacy migration.
//
// Why this test exists (2026-09-30): the store used to live per-DataDir, so the standalone
// shell (~/.dw-terminal) showed Kimi/GLM subscriptions the pro shell (~/.deepwork) had never
// heard of — same machine, same user, two different answers. The store now lives at the
// deepwork home (DEEPWORK_HOME-overridable, same place quota snapshots already live), and a
// legacy store migrates itself forward exactly once.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brightman-ai/kit/usage"
)

func TestCredentialStore_SharedAcrossDataDirs(t *testing.T) {
	deepwork := t.TempDir()
	t.Setenv("DEEPWORK_HOME", deepwork)

	// Seeded once by an operator (or migrated) — visible from ANY server's DataDir.
	shared := newCredentialStore(filepath.Join(t.TempDir(), "server-a"))
	seed(t, shared, `{"version":1,"subscriptions":[{"vendor":"zhipu","api_key":"k"}]}`)

	other := newCredentialStore(filepath.Join(t.TempDir(), "server-b"))
	if _, ok := other.Credential("zhipu"); !ok {
		t.Fatal("a second server on the same host must see the same subscriptions")
	}
}

func TestCredentialStore_MigratesLegacyStoreOnce(t *testing.T) {
	deepwork := t.TempDir()
	t.Setenv("DEEPWORK_HOME", deepwork)

	// A pre-share server: its own DataDir held both the store and the key that sealed it.
	legacyDir := t.TempDir()
	legacyKey := loadOrCreateIlinkKey(legacyDir)
	sealed, err := aesgcmSeal(legacyKey, []byte(`{"version":1,"subscriptions":[{"vendor":"moonshot","api_key":"k"}]}`))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, credentialFileName), sealed, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	store := newCredentialStore(legacyDir)
	if _, ok := store.Credential("moonshot"); !ok {
		t.Fatal("the legacy subscription must be readable through the shared path after migration")
	}
	if _, err := os.Stat(filepath.Join(deepwork, credentialFileName)); err != nil {
		t.Fatalf("migration must write the shared store: %v", err)
	}
	// The legacy copy stays — rollback to an older binary keeps working off it.
	if _, err := os.Stat(filepath.Join(legacyDir, credentialFileName)); err != nil {
		t.Fatalf("migration must not delete the legacy store: %v", err)
	}

	// A SECOND server (different DataDir, nothing legacy of its own) sees the migrated store.
	fresh := newCredentialStore(filepath.Join(t.TempDir(), "server-b"))
	if _, ok := fresh.Credential("moonshot"); !ok {
		t.Fatal("the migrated shared store must serve every server on the host")
	}
}

func TestCredentialStore_NoLegacy_NoShared_EmptyIsLegitimate(t *testing.T) {
	t.Setenv("DEEPWORK_HOME", t.TempDir())
	store := newCredentialStore(t.TempDir())
	if _, ok := store.Credential("zhipu"); ok {
		t.Fatal("no store anywhere means no subscriptions — a legitimate state, not an error")
	}
}

func TestTestRunWithoutIsolatedHomeDoesNotStartQuotaWarmer(t *testing.T) {
	t.Setenv("DEEPWORK_HOME", "")
	if !testRunWithoutIsolatedDeepworkHome() {
		t.Fatal("the test binary without an explicit home should be recognized as unisolated")
	}

	owner := &Server{config: Config{DataDir: t.TempDir()}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	usageCredentialSourceMu.RLock()
	registrationsBefore := len(usageCredentialSources)
	usageCredentialSourceMu.RUnlock()

	owner.startQuotaWarmer(ctx)

	usageCredentialSourceMu.RLock()
	defer usageCredentialSourceMu.RUnlock()
	if len(usageCredentialSources) != registrationsBefore {
		t.Fatal("go test without DEEPWORK_HOME registered credentials from the developer's real home")
	}
	for _, registration := range usageCredentialSources {
		if registration.owner == owner {
			t.Fatal("go test without DEEPWORK_HOME started a quota warmer")
		}
	}
}

func TestUsageCredentialSourcesRestoreNewestOwnerAfterOutOfOrderClose(t *testing.T) {
	t.Setenv("DEEPWORK_HOME", t.TempDir())
	t.Setenv("DW_CODEX_HOME", filepath.Join(t.TempDir(), "codex"))
	t.Setenv("DW_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "claude", "projects"))
	usageCredentialSourceMu.Lock()
	previousSources := append([]usageCredentialRegistration(nil), usageCredentialSources...)
	previousSource := usageCredentialSource
	usageCredentialSourceMu.Unlock()
	t.Cleanup(func() {
		usageCredentialSourceMu.Lock()
		usageCredentialSources = previousSources
		usageCredentialSource = previousSource
		useUsageCredentialSource(previousSource)
		usageCredentialSourceMu.Unlock()
	})

	older, newer := &Server{}, &Server{}
	first, second := &credentialStore{}, &credentialStore{}
	registerUsageCredentialSource(older, first)
	registerUsageCredentialSource(newer, second)
	unregisterUsageCredentialSource(older) // the older instance closes first

	usageCredentialSourceMu.RLock()
	active, registrations := usageCredentialSource, append([]usageCredentialRegistration(nil), usageCredentialSources...)
	usageCredentialSourceMu.RUnlock()
	if active != second || len(registrations) != len(previousSources)+1 || registrations[len(registrations)-1].owner != newer {
		t.Fatalf("closing an older server replaced the active source: source=%p registrations=%+v", active, registrations)
	}

	unregisterUsageCredentialSource(newer)
	usageCredentialSourceMu.RLock()
	active = usageCredentialSource
	usageCredentialSourceMu.RUnlock()
	if active != previousSource {
		t.Fatalf("closing the last server did not restore the previous source: got %p want %p", active, previousSource)
	}
	if active == nil {
		// Reproduces the real call chain that used to panic: typed nil had made it
		// through kit's CredentialSource interface until this first quota query.
		_ = usage.QueryAllQuotas()
	}
}

func TestExplicitDeepworkHomeIsNotTreatedAsUnisolatedTestRun(t *testing.T) {
	t.Setenv("DEEPWORK_HOME", t.TempDir())
	if testRunWithoutIsolatedDeepworkHome() {
		t.Fatal("an explicit temporary DEEPWORK_HOME should permit isolated quota tests")
	}
}

// seed writes a plaintext store through the canonical path, the way an operator would.
func seed(t *testing.T, store *credentialStore, plain string) {
	t.Helper()
	resealed, err := aesgcmSeal(store.key, []byte(plain))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := ilinkAtomicWrite(store.path, resealed, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// ── derived credentials (provider profiles) ──────────────────────────────────
//
// A claude-switch profile already holds the (routing base URL, key) pair for the very
// vendors whose quota APIs kit/usage can ask. Deriving the subscription from it is the SSOT
// fix for 2026-10-06: the user "provides an API key" once, in the profile they already
// maintain, and the quota domain must never require typing it into a second store.

func writeProfile(t *testing.T, root, name, settingsJSON string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir profile %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settingsJSON), 0o600); err != nil {
		t.Fatalf("write profile %s: %v", name, err)
	}
}

func TestCredentialStore_DerivesSubscriptionFromProfile(t *testing.T) {
	t.Setenv("DEEPWORK_HOME", t.TempDir())
	// Hermetic against the host too: an empty codex home keeps the official-codex
	// declaration from appending a machine-dependent "openai" entry (it reads the real
	// ~/.codex otherwise — the same never-touch-the-developer's-home discipline).
	t.Setenv("DW_CODEX_HOME", t.TempDir())
	profiles := t.TempDir()
	t.Setenv("DW_CLAUDE_PROFILES", profiles)

	writeProfile(t, profiles, "glm", `{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic","ANTHROPIC_AUTH_TOKEN":"glm-key"}}`)
	writeProfile(t, profiles, "kimi", `{"env":{"ANTHROPIC_BASE_URL":"https://api.kimi.com/coding/","ANTHROPIC_AUTH_TOKEN":"kimi-key"}}`)
	// The absence fixtures: a profile with no provider env, and one routed at a host this
	// domain cannot ask — both must derive nothing rather than guess.
	writeProfile(t, profiles, "minimax", `{"env":{}}`)
	writeProfile(t, profiles, "other", `{"env":{"ANTHROPIC_BASE_URL":"https://api.example.com/v1","ANTHROPIC_AUTH_TOKEN":"x"}}`)

	store := newCredentialStore(filepath.Join(t.TempDir(), "data"))
	if cred, ok := store.Credential("zhipu"); !ok || cred.APIKey != "glm-key" {
		t.Fatalf("glm profile must derive the zhipu subscription, got (%q, %v)", cred.APIKey, ok)
	}
	if cred, ok := store.Credential("moonshot"); !ok || cred.APIKey != "kimi-key" {
		t.Fatalf("kimi profile must derive the moonshot subscription, got (%q, %v)", cred.APIKey, ok)
	}
	if cred, _ := store.Credential("zhipu"); cred.BaseURL != "" {
		t.Fatalf("a derived credential carries the key only — the quota endpoint is the domain's, got %q", cred.BaseURL)
	}
	if _, ok := store.Credential("openai"); ok {
		t.Fatal("profiles that derive nothing must not conjure vendors this store never heard of")
	}
}

func TestCredentialStore_DerivationIsDeterministic(t *testing.T) {
	t.Setenv("DEEPWORK_HOME", t.TempDir())
	profiles := t.TempDir()
	t.Setenv("DW_CLAUDE_PROFILES", profiles)

	// Two profiles claim zhipu; directory order (sorted) decides, so the same fixture set
	// always yields the same answer on every machine and every run.
	writeProfile(t, profiles, "a-glm", `{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic","ANTHROPIC_AUTH_TOKEN":"first"}}`)
	writeProfile(t, profiles, "b-glm", `{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic","ANTHROPIC_AUTH_TOKEN":"second"}}`)

	store := newCredentialStore(filepath.Join(t.TempDir(), "data"))
	if cred, ok := store.Credential("zhipu"); !ok || cred.APIKey != "first" {
		t.Fatalf("the sorted-first profile must win, got (%q, %v)", cred.APIKey, ok)
	}
}

func TestCredentialStore_SealedEntryOverridesDerivedProfile(t *testing.T) {
	deepwork := t.TempDir()
	t.Setenv("DEEPWORK_HOME", deepwork)
	profiles := t.TempDir()
	t.Setenv("DW_CLAUDE_PROFILES", profiles)

	writeProfile(t, profiles, "glm", `{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic","ANTHROPIC_AUTH_TOKEN":"derived-key"}}`)
	writeProfile(t, profiles, "kimi", `{"env":{"ANTHROPIC_BASE_URL":"https://api.kimi.com/coding/","ANTHROPIC_AUTH_TOKEN":"kimi-key"}}`)

	store := newCredentialStore(filepath.Join(t.TempDir(), "data"))
	seed(t, store, `{"version":1,"subscriptions":[{"vendor":"zhipu","api_key":"explicit-key"}]}`)

	if cred, ok := store.Credential("zhipu"); !ok || cred.APIKey != "explicit-key" {
		t.Fatalf("explicit authorship must outrank derivation, got (%q, %v)", cred.APIKey, ok)
	}
	if cred, ok := store.Credential("moonshot"); !ok || cred.APIKey != "kimi-key" {
		t.Fatal("an explicit entry for one vendor must not disturb the derived layer of another")
	}
}

func TestCredentialStore_SealedRemovalFallsBackToDerived(t *testing.T) {
	t.Setenv("DEEPWORK_HOME", t.TempDir())
	profiles := t.TempDir()
	t.Setenv("DW_CLAUDE_PROFILES", profiles)
	writeProfile(t, profiles, "glm", `{"env":{"ANTHROPIC_BASE_URL":"https://open.bigmodel.cn/api/anthropic","ANTHROPIC_AUTH_TOKEN":"derived-key"}}`)

	store := newCredentialStore(filepath.Join(t.TempDir(), "data"))
	seed(t, store, `{"version":1,"subscriptions":[{"vendor":"zhipu","api_key":"explicit-key"}]}`)
	if cred, _ := store.Credential("zhipu"); cred.APIKey != "explicit-key" {
		t.Fatal("sealed entry must win while present")
	}

	// The operator removes the explicit entry (rewrites the sealed store); the derived
	// subscription resurfaces within the cache TTL — no restart, no re-entry.
	seed(t, store, `{"version":1,"subscriptions":[]}`)
	if err := os.Setenv("DW_CLAUDE_PROFILES", profiles); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	store.mu.Lock()
	store.loadedAt = time.Now().Add(-credentialCacheTTL - time.Second) // force a reload past the TTL
	creds := store.load()
	store.mu.Unlock()
	if cred, ok := creds["zhipu"]; !ok || cred.APIKey != "derived-key" {
		t.Fatalf("removing the explicit entry must let the derived subscription resurface, got (%q, %v)", cred.APIKey, ok)
	}
}
