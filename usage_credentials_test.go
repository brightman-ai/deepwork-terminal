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
