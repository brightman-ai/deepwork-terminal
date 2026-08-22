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
	"sync"
	"time"

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
type credentialStore struct {
	path string
	key  []byte

	mu       sync.Mutex
	loadedAt time.Time
	byVendor map[string]usage.Credential
}

const credentialCacheTTL = 30 * time.Second

func newCredentialStore(dataDir string) *credentialStore {
	return &credentialStore{
		path: filepath.Join(dataDir, "usage-credentials.json.enc"),
		key:  loadOrCreateIlinkKey(dataDir), // one machine key for every secret in this process
	}
}

// Credential implements usage.CredentialSource.
func (s *credentialStore) Credential(vendor string) (usage.Credential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byVendor == nil || time.Since(s.loadedAt) > credentialCacheTTL {
		s.byVendor = s.load()
		s.loadedAt = time.Now()
	}
	cred, ok := s.byVendor[vendor]
	return cred, ok
}

// load decrypts and parses the store. Every failure mode — absent, unreadable, corrupt — means
// the same thing to the domain: no subscriptions are configured here. That is a legitimate
// state (most hosts have none), so it is not an error and never blocks startup.
func (s *credentialStore) load() map[string]usage.Credential {
	out := map[string]usage.Credential{}
	sealed, err := os.ReadFile(s.path) //nolint:gosec — our own sealed store
	if err != nil {
		return out
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
		if entry.Vendor == "" || entry.APIKey == "" {
			continue
		}
		out[entry.Vendor] = usage.Credential{
			APIKey:             entry.APIKey,
			BaseURL:            entry.BaseURL,
			RuntimeProviderIDs: entry.RuntimeProviderIDs,
		}
	}
	return out
}

// startQuotaWarmer installs the credential store and keeps every free-to-ask account's reading
// fresh. It returns immediately; the loop exits with ctx.
func (s *Server) startQuotaWarmer(ctx context.Context) {
	usage.UseCredentials(newCredentialStore(s.config.DataDir))
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
	for _, result := range usage.RefreshStale(ctx, quotaMaxAge) {
		if result.Status == usage.ProbeFailed {
			logger.Info("quota refresh failed", "account", result.Display, "reason", result.Reason)
		}
	}
}
