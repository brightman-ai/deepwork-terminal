package terminal

// Subscription credentials for the quota domain, and the timer that keeps quota warm.
//
// # Why the host holds the key
//
// kit/usage knows WHICH vendors have a quota API; it deliberately does not know how this host
// stores their keys. So the key store lives here, sealed the same way every other secret in this
// process is (AES-GCM under the machine key that iLink and the notify config already share) —
// one encryption path, not a second one invented for this feature.
//
// The file is written out-of-band (settings UI, or an operator seeding it once); this half only
// reads it. Nothing here is ever returned to a client: the API surface reports quota, never
// credentials, and a key that reaches the browser is a key that has leaked.
//
// # Why there is a timer at all
//
// Asking a vendor for quota used to cost a real inference request, so it could only happen when
// a person pressed 刷新 — and the number was therefore stale exactly when it mattered. Both
// vendors now answer a plain read (see kit/usage/codex_api.go), so the honest design is the
// opposite: keep it warm on a timer and let the button mean "don't wait for the timer". GET
// /usage/quota stays a pure file read, so the UI still paints instantly and a slow vendor can
// never stall it.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/brightman-ai/kit/transcript"
	"github.com/brightman-ai/kit/usage"
)

// quotaWarmInterval is how often the warmer wakes up, and quotaMaxAge how stale a stored
// reading may get before it is re-fetched. Quota only moves when the user actually runs
// something, so minutes-fresh is fresh: asking harder would buy nothing and spend someone
// else's rate limit.
const (
	quotaWarmInterval = 5 * time.Minute
	quotaMaxAge       = 10 * time.Minute
)

// kit/usage owns one credential source per process. Keep the source used by the quota
// reconciliation shim in lockstep with it, and hold the read lock across kit calls so a
// second Server cannot swap the source midway through one response.
var (
	usageCredentialSourceMu sync.RWMutex
	usageCredentialSource   *credentialStore
	usageCredentialSources  []usageCredentialRegistration
)

type usageCredentialRegistration struct {
	owner  *Server
	source *credentialStore
}

func registerUsageCredentialSource(owner *Server, source *credentialStore) {
	usageCredentialSourceMu.Lock()
	defer usageCredentialSourceMu.Unlock()
	kept := usageCredentialSources[:0]
	for _, registration := range usageCredentialSources {
		if registration.owner != owner {
			kept = append(kept, registration)
		}
	}
	usageCredentialSources = append(kept, usageCredentialRegistration{owner: owner, source: source})
	usageCredentialSource = source
	useUsageCredentialSource(source)
}

func unregisterUsageCredentialSource(owner *Server) {
	usageCredentialSourceMu.Lock()
	defer usageCredentialSourceMu.Unlock()
	kept := usageCredentialSources[:0]
	for _, registration := range usageCredentialSources {
		if registration.owner != owner {
			kept = append(kept, registration)
		}
	}
	usageCredentialSources = kept
	usageCredentialSource = nil
	if n := len(usageCredentialSources); n > 0 {
		usageCredentialSource = usageCredentialSources[n-1].source
	}
	useUsageCredentialSource(usageCredentialSource)
}

func useUsageCredentialSource(source *credentialStore) {
	if source == nil {
		// A typed nil pointer converted to CredentialSource is a non-nil interface and
		// makes kit/usage call a method through nil on its next quota query.
		usage.UseCredentials(nil)
		return
	}
	usage.UseCredentials(source)
}

func testRunWithoutIsolatedDeepworkHome() bool {
	return strings.HasSuffix(os.Args[0], ".test") && os.Getenv("DEEPWORK_HOME") == ""
}

func deepworkHomeDir() string {
	if home := os.Getenv("DEEPWORK_HOME"); home != "" {
		return home
	}
	userHome, _ := os.UserHomeDir()
	return filepath.Join(userHome, ".deepwork")
}

// subscriptionCredentials is the on-disk shape of <DataDir>/usage-credentials.json.enc.
type subscriptionCredentials struct {
	Version       int                      `json:"version"`
	Subscriptions []subscriptionCredential `json:"subscriptions"`
}

// subscriptionCredential is one vendor's entry. Vendor is kit/usage's vendor id ("kimi"), NOT
// the id of whatever proxy happens to route to it — those go in RuntimeProviderIDs, where they
// are data the user controls rather than a constant this code would have to guess.
type subscriptionCredential struct {
	Vendor             string   `json:"vendor"`
	APIKey             string   `json:"api_key"`
	BaseURL            string   `json:"base_url,omitempty"`
	RuntimeProviderIDs []string `json:"runtime_provider_ids,omitempty"`
}

// credentialStore implements usage.CredentialSource over the sealed file. It is read-through
// with a short cache so a warm-up pass does not decrypt once per vendor, and so an operator who
// edits the file does not have to restart the server to be believed.
//
// The file lives at the deepwork home (~/.deepwork, DEEPWORK_HOME-overridable) — the SAME
// home that hosts kit/usage's quota snapshots — because a vendor subscription belongs to the
// machine's user, not to whichever server instance happens to run. The per-DataDir location
// this store used first is how one shell (standalone, DataDir ~/.dw-terminal) came to show
// Kimi/GLM subscriptions that the other (pro-embedded, DataDir ~/.deepwork) had never heard
// of: same machine, same user, two different answers to "what am I subscribed to".
type credentialStore struct {
	path      string // canonical, shared
	key       []byte
	legacyDir string // pre-share location, consulted once for migration

	mu       sync.Mutex
	loadedAt time.Time
	byVendor map[string]usage.Credential
	// ExplicitProviderVendors records only ids read from the sealed user file. The
	// built-in official Codex fallback added by load() is deliberately excluded.
	explicitProviderVendors map[string]string
}

const credentialCacheTTL = 30 * time.Second

// credentialFileName is the sealed store's name in whichever directory holds it.
const credentialFileName = "usage-credentials.json.enc"

func newCredentialStore(dataDir string) *credentialStore {
	home := deepworkHomeDir()
	shared := filepath.Join(home, credentialFileName)
	return &credentialStore{
		path:      shared,
		key:       loadOrCreateIlinkKey(filepath.Dir(shared)), // one machine key for every secret in this process
		legacyDir: dataDir,
	}
}

// Credential implements usage.CredentialSource.
func (s *credentialStore) Credential(vendor string) (usage.Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	cred, ok := s.byVendor[vendor]
	return cred, ok
}

// explicitProviderVendorsSnapshot returns only mappings authored in the sealed file;
// the built-in OAuth fallback is intentionally absent. Explicit ownership outranks inference.
func (s *credentialStore) explicitProviderVendorsSnapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	out := make(map[string]string, len(s.explicitProviderVendors))
	for providerID, vendor := range s.explicitProviderVendors {
		out[providerID] = vendor
	}
	return out
}

func (s *credentialStore) ensureLoadedLocked() {
	if s.byVendor == nil || time.Since(s.loadedAt) > credentialCacheTTL {
		s.byVendor = s.load()
		s.loadedAt = time.Now()
	}
}

// load decrypts and parses the store. Every failure mode — absent, unreadable, corrupt — means
// the same thing to the domain: no subscriptions are configured here. That is a legitimate
// state (most hosts have none), so it is not an error and never blocks startup.
func (s *credentialStore) load() map[string]usage.Credential {
	out := map[string]usage.Credential{}
	s.explicitProviderVendors = map[string]string{}
	defer func() {
		id := officialCodexProviderDeclaration(transcript.CodexHome())
		if id == "" {
			return
		}
		// An explicit host declaration is stronger than the default-profile adapter.
		for _, cred := range out {
			for _, declared := range cred.RuntimeProviderIDs {
				if declared == id {
					return
				}
			}
		}
		cred := out["openai"]
		cred.RuntimeProviderIDs = append(cred.RuntimeProviderIDs, id)
		out["openai"] = cred
	}()
	sealed, err := os.ReadFile(s.path) //nolint:gosec — our own sealed store
	if err != nil {
		s.migrateLegacy()
		// Whatever migration produced is worth reading; nothing means "no subscriptions".
		if sealed, err = os.ReadFile(s.path); err != nil {
			return out
		}
	}
	plain, err := aesgcmOpen(s.key, sealed)
	if err != nil {
		logger.Warn("usage credentials decrypt failed", "error", err)
		return out
	}
	var file subscriptionCredentials
	if err := json.Unmarshal(plain, &file); err != nil {
		logger.Warn("usage credentials parse failed", "error", err)
		return out
	}
	for _, entry := range file.Subscriptions {
		if entry.Vendor == "" || (entry.APIKey == "" && len(entry.RuntimeProviderIDs) == 0) {
			continue
		}
		for _, providerID := range entry.RuntimeProviderIDs {
			if prior, exists := s.explicitProviderVendors[providerID]; exists && prior != entry.Vendor {
				s.explicitProviderVendors[providerID] = ""
			} else if !exists {
				s.explicitProviderVendors[providerID] = entry.Vendor
			}
		}
		out[entry.Vendor] = usage.Credential{
			APIKey:             entry.APIKey,
			BaseURL:            entry.BaseURL,
			RuntimeProviderIDs: entry.RuntimeProviderIDs,
		}
	}
	return out
}

// migrateLegacy carries a pre-share store into the shared location, exactly once: it only acts
// when the shared file is absent and the legacy pair (store + the key that sealed it) is not.
// The legacy file is left in place — nothing here is destructive, and a host rolling back to an
// older binary keeps working off its old copy. A legacy file that cannot be decrypted under its
// own key is logged and skipped: guessing at it would be worse than ignoring it.
//
// Test binaries never migrate (same discipline as muxd's socket guard): a NewServer test
// resolves the REAL ~/.dw-terminal as DataDir, and without this guard one `go test ./...` run
// migrated the developer's actual store — observed 2026-09-30. Tests that want migration set
// DEEPWORK_HOME explicitly.
func (s *credentialStore) migrateLegacy() {
	if testRunWithoutIsolatedDeepworkHome() {
		return
	}
	legacyPath := filepath.Join(s.legacyDir, credentialFileName)
	if s.legacyDir == "" || s.path == legacyPath {
		return
	}
	sealed, err := os.ReadFile(legacyPath) //nolint:gosec — read-only migration source
	if err != nil {
		return // nothing to migrate
	}
	legacyKey, err := os.ReadFile(filepath.Join(s.legacyDir, "ilink.key"))
	if err != nil || len(legacyKey) != 32 {
		return
	}
	plain, err := aesgcmOpen(legacyKey, sealed)
	if err != nil {
		logger.Warn("usage credentials legacy migration skipped: decrypt failed", "error", err)
		return
	}
	resealed, err := aesgcmSeal(s.key, plain)
	if err != nil {
		logger.Warn("usage credentials migration skipped: seal failed", "error", err)
		return
	}
	if err := ilinkAtomicWrite(s.path, resealed, 0o600); err != nil {
		logger.Warn("usage credentials migration write failed", "error", err)
		return
	}
	logger.Info("usage credentials migrated to shared deepwork home", "from", legacyPath, "to", s.path)
}

// startQuotaWarmer installs the credential store and keeps every free-to-ask account's reading
// fresh. It returns immediately; the loop exits with ctx.
func (s *Server) startQuotaWarmer(ctx context.Context) {
	if testRunWithoutIsolatedDeepworkHome() {
		return // never probe or register credentials from a developer's real home in go test
	}
	registerUsageCredentialSource(s, newCredentialStore(s.config.DataDir))
	go func() {
		// One pass at startup: a server that has just come up should not serve a day-old
		// number for the ten minutes before the first tick.
		warmQuota(ctx)
		ticker := time.NewTicker(quotaWarmInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				warmQuota(ctx)
			}
		}
	}()
}

// warmQuota refreshes the accounts whose stored reading has aged out, and logs only what a
// person could act on. A failure is expected and survivable — an expired login, a laptop
// offline — and the last-known reading stays on screen either way.
func warmQuota(ctx context.Context) {
	usageCredentialSourceMu.RLock()
	defer usageCredentialSourceMu.RUnlock()
	for _, result := range usage.RefreshStale(ctx, quotaMaxAge) {
		if result.Status == usage.ProbeFailed {
			logger.Info("quota refresh failed", "account", result.Display, "reason", result.Reason)
		}
	}
}
