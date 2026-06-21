package notify

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// TestCooldown rate-limits the manual "send test" PER provider (so testing WeChat
// doesn't block testing Feishu, and rapid taps can't drain a channel's quota).
const TestCooldown = 8 * time.Second

// Result is one provider's outcome for one event.
type Result struct {
	Provider string  `json:"provider"`
	Outcome  Outcome `json:"outcome"`
	Detail   string  `json:"detail,omitempty"` // optional ("429", "BadJwtToken", "dormant: quota")
}

// EventRecord is exactly one fanned-out event with each enabled provider's result.
// Replaces the old two-valued record(channel, fellBack): under fan-out there is no
// primary/fallback, so "fellBack" is gone.
type EventRecord struct {
	At      time.Time `json:"at"`
	Kind    Kind      `json:"kind"`
	Results []Result  `json:"results"`
}

// Coordinator is the one fan-out point and the one config-join point.
type Coordinator struct {
	store     Store
	order     []string            // stable kind order for display + iteration
	providers map[string]Provider // kind → provider
	metrics   *Metrics
	now       func() time.Time

	mu        sync.Mutex
	lastTest  map[string]time.Time // per-provider test cooldown
}

// NewCoordinator builds a coordinator. providers are registered in the given order
// (display + send order). now defaults to time.Now when nil (overridable in tests).
func NewCoordinator(store Store, now func() time.Time, providers ...Provider) *Coordinator {
	if now == nil {
		now = time.Now
	}
	c := &Coordinator{
		store:     store,
		providers: map[string]Provider{},
		metrics:   newMetrics(now),
		now:       now,
		lastTest:  map[string]time.Time{},
	}
	for _, p := range providers {
		c.order = append(c.order, p.Kind())
		c.providers[p.Kind()] = p
	}
	return c
}

// Send fans e out to every ENABLED provider CONCURRENTLY and records one
// EventRecord. Disabled providers are skipped (invariant §8.2: "关了不推" is enforced
// here, not in docs). Each provider runs isolated (safeSend): one slow or panicking
// channel never blocks the others past its own timeout, nor kills the caller (the
// notifier goroutine). Wall time ≈ the slowest single provider, not the sum.
func (c *Coordinator) Send(ctx context.Context, e Event) EventRecord {
	cfg, _ := c.store.Load(ctx)
	rec := EventRecord{At: c.now(), Kind: e.Kind}

	var enabled []ProviderConfig
	for _, kind := range c.order {
		if pc, ok := cfg.Get(kind); ok && pc.Enabled {
			enabled = append(enabled, pc)
		}
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, pc := range enabled {
		wg.Add(1)
		go func(pc ProviderConfig) {
			defer wg.Done()
			out, detail := c.safeSend(ctx, pc.Kind, e, pc)
			mu.Lock()
			rec.Results = append(rec.Results, Result{Provider: pc.Kind, Outcome: out, Detail: detail})
			mu.Unlock()
		}(pc)
	}
	wg.Wait()
	c.metrics.record(rec)
	return rec
}

// safeSend isolates one provider call: a panic becomes OutcomeFailed so one bad
// channel can never crash the fan-out or the notifier goroutine that drives it. It
// returns the outcome + a short troubleshooting detail (the failure reason).
func (c *Coordinator) safeSend(ctx context.Context, kind string, e Event, pc ProviderConfig) (out Outcome, detail string) {
	defer func() {
		if r := recover(); r != nil {
			out, detail = OutcomeFailed, "panic"
		}
	}()
	p := c.providers[kind]
	if p == nil {
		return OutcomeNotConfigured, ""
	}
	return p.Send(ctx, e, pc)
}

// Test sends a single KindTest event to one provider (manual UI "发测试"). It is
// per-provider cooldown-gated and records to metrics like a real event. Returns the
// honest Result (never a fake "sent"). cooldown reports false when gated.
func (c *Coordinator) Test(ctx context.Context, kind string, e Event) (res Result, allowed bool) {
	p := c.providers[kind]
	if p == nil {
		return Result{Provider: kind, Outcome: OutcomeNotConfigured}, true
	}
	c.mu.Lock()
	now := c.now()
	if last, ok := c.lastTest[kind]; ok && now.Sub(last) < TestCooldown {
		c.mu.Unlock()
		return Result{}, false
	}
	c.lastTest[kind] = now
	c.mu.Unlock()

	cfg, _ := c.store.Load(ctx)
	pc, _ := cfg.Get(kind)
	e.Kind = KindTest
	out, detail := c.safeSend(ctx, kind, e, pc)
	res = Result{Provider: kind, Outcome: out, Detail: detail}
	c.metrics.record(EventRecord{At: now, Kind: KindTest, Results: []Result{res}})
	return res, true
}

// Statuses is the SINGLE join point: provider runtime Status ⨝ Config.Enabled ⨝
// metrics TodaySent. Nothing else joins these sources (§12.3).
func (c *Coordinator) Statuses(ctx context.Context) []Status {
	cfg, _ := c.store.Load(ctx)
	today := c.metrics.todayByProvider()
	out := make([]Status, 0, len(c.order))
	for _, kind := range c.order {
		pc, _ := cfg.Get(kind)
		st := c.providers[kind].Status(ctx, pc)
		st.Kind = kind
		st.Enabled = pc.Enabled // join: config owns Enabled
		st.TodaySent = today[kind]
		out = append(out, st)
	}
	return out
}

// Config returns the current config with schema defaults applied.
func (c *Coordinator) Config(ctx context.Context) (Config, error) {
	cfg, err := c.store.Load(ctx)
	return cfg.withDefaults(), err
}

// SetEnabled flips one provider on/off and persists (the UI toggle path).
func (c *Coordinator) SetEnabled(ctx context.Context, kind string, enabled bool) error {
	cfg, _ := c.store.Load(ctx)
	cfg = cfg.withDefaults()
	found := false
	for i := range cfg.Providers {
		if cfg.Providers[i].Kind == kind {
			cfg.Providers[i].Enabled = enabled
			found = true
		}
	}
	if !found {
		cfg.Providers = append(cfg.Providers, ProviderConfig{Kind: kind, Enabled: enabled})
	}
	return c.store.Save(ctx, cfg)
}

// SetSettings replaces one provider's per-kind Settings (webhook url/secret) and
// persists. Settings are write-only from the UI's perspective (GET redacts them).
func (c *Coordinator) SetSettings(ctx context.Context, kind string, settings json.RawMessage) error {
	cfg, _ := c.store.Load(ctx)
	cfg = cfg.withDefaults()
	found := false
	for i := range cfg.Providers {
		if cfg.Providers[i].Kind == kind {
			cfg.Providers[i].Settings = settings
			found = true
		}
	}
	if !found {
		cfg.Providers = append(cfg.Providers, ProviderConfig{Kind: kind, Settings: settings})
	}
	return c.store.Save(ctx, cfg)
}

// Metrics returns the metrics view for the status API.
func (c *Coordinator) Metrics() MetricsView { return c.metrics.view() }

// MetricsSnapshot exports the metrics state for the host to persist (the package
// stays disk-agnostic). RestoreMetrics imports it on startup so send history /
// counters survive a restart (the "可排障" intent: see WHY a channel failed last).
func (c *Coordinator) MetricsSnapshot() MetricsState { return c.metrics.snapshot() }
func (c *Coordinator) RestoreMetrics(s MetricsState) { c.metrics.restore(s) }

// Knownkinds returns the registered provider kinds in display order.
func (c *Coordinator) Kinds() []string { return append([]string(nil), c.order...) }

// ── Metrics ──────────────────────────────────────────────────────────────────

// RecentSend is one historical send outcome — the recent-3 troubleshooting trail.
type RecentSend struct {
	AtMs    int64   `json:"atMs"`
	Outcome Outcome `json:"outcome"`
	Detail  string  `json:"detail,omitempty"` // failure reason ("订阅失效(410)", "BadJwtToken"…)
}

// recentCap bounds the per-provider recent-outcome trail.
const recentCap = 3

// Metrics is the per-provider delivery tally (one record per fanned-out event).
// SSOT for "what was notified and how" — no second tally to drift against.
type Metrics struct {
	mu     sync.Mutex
	now    func() time.Time
	events int
	last   time.Time
	// per-provider lifetime + today counters
	sent     map[string]int
	dormant  map[string]int
	failed   map[string]int
	today    map[string]int
	todayDay int // year*1000+yearday of the `today` window; rolls over at day change
	// per-provider troubleshooting signals
	lastSuccess map[string]time.Time    // last OutcomeSent
	recent      map[string][]RecentSend // last recentCap attempts (newest last)
}

func newMetrics(now func() time.Time) *Metrics {
	return &Metrics{
		now: now, sent: map[string]int{}, dormant: map[string]int{}, failed: map[string]int{}, today: map[string]int{},
		lastSuccess: map[string]time.Time{}, recent: map[string][]RecentSend{},
	}
}

func dayKey(t time.Time) int { y, yd := t.Year(), t.YearDay(); return y*1000 + yd }

func (m *Metrics) record(rec EventRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if dk := dayKey(now); dk != m.todayDay {
		m.todayDay = dk
		m.today = map[string]int{} // new day → reset today counters
	}
	m.events++
	m.last = now
	for _, r := range rec.Results {
		switch r.Outcome {
		case OutcomeSent:
			m.sent[r.Provider]++
			m.today[r.Provider]++
			m.lastSuccess[r.Provider] = now
		case OutcomeDormant:
			m.dormant[r.Provider]++
		case OutcomeFailed:
			m.failed[r.Provider]++
		}
		// recent trail records EVERY attempt (incl. failures + reason) for troubleshooting.
		rs := append(m.recent[r.Provider], RecentSend{AtMs: now.UnixMilli(), Outcome: r.Outcome, Detail: r.Detail})
		if len(rs) > recentCap {
			rs = rs[len(rs)-recentCap:]
		}
		m.recent[r.Provider] = rs
	}
}

func (m *Metrics) todayByProvider() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dk := dayKey(m.now()); dk != m.todayDay {
		return map[string]int{} // stale day → today is zero
	}
	out := make(map[string]int, len(m.today))
	for k, v := range m.today {
		out[k] = v
	}
	return out
}

// ProviderMetric is one provider's tally + troubleshooting trail for the status view.
// Today's count is NOT here — it lives once on Status.TodaySent (single source); this
// carries the lifetime tally + last-success + the recent-3 trail.
type ProviderMetric struct {
	Provider        string       `json:"provider"`
	Sent            int          `json:"sent"`
	Dormant         int          `json:"dormant"`
	Failed          int          `json:"failed"`
	LastSuccessAtMs int64        `json:"lastSuccessAtMs"` // 0 = never succeeded
	Recent          []RecentSend `json:"recent"`          // last recentCap attempts (newest last)
}

// MetricsView is the JSON-friendly metrics snapshot for the status API.
type MetricsView struct {
	Events      int              `json:"events"`
	LastAtMs    int64            `json:"lastAtMs"` // 0 = never
	PerProvider []ProviderMetric `json:"perProvider"`
}

func (m *Metrics) view() MetricsView {
	m.mu.Lock()
	defer m.mu.Unlock()
	var lastMs int64
	if !m.last.IsZero() {
		lastMs = m.last.UnixMilli()
	}
	kinds := map[string]struct{}{}
	for _, mp := range []map[string]int{m.sent, m.dormant, m.failed} {
		for k := range mp {
			kinds[k] = struct{}{}
		}
	}
	for k := range m.recent {
		kinds[k] = struct{}{}
	}
	per := make([]ProviderMetric, 0, len(kinds))
	for k := range kinds {
		var lsMs int64
		if t, ok := m.lastSuccess[k]; ok && !t.IsZero() {
			lsMs = t.UnixMilli()
		}
		per = append(per, ProviderMetric{
			Provider: k, Sent: m.sent[k], Dormant: m.dormant[k], Failed: m.failed[k],
			LastSuccessAtMs: lsMs, Recent: append([]RecentSend(nil), m.recent[k]...),
		})
	}
	sort.Slice(per, func(i, j int) bool { return per[i].Provider < per[j].Provider })
	return MetricsView{Events: m.events, LastAtMs: lastMs, PerProvider: per}
}

// MetricsState is the serializable metrics snapshot the host persists to disk.
type MetricsState struct {
	Events      int                     `json:"events"`
	LastMs      int64                   `json:"lastMs"`
	TodayDay    int                     `json:"todayDay"`
	Sent        map[string]int          `json:"sent"`
	Dormant     map[string]int          `json:"dormant"`
	Failed      map[string]int          `json:"failed"`
	Today       map[string]int          `json:"today"`
	LastSuccess map[string]int64        `json:"lastSuccess"` // provider → unix ms
	Recent      map[string][]RecentSend `json:"recent"`
}

func (m *Metrics) snapshot() MetricsState {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls := make(map[string]int64, len(m.lastSuccess))
	for k, t := range m.lastSuccess {
		if !t.IsZero() {
			ls[k] = t.UnixMilli()
		}
	}
	cp := func(src map[string]int) map[string]int {
		d := make(map[string]int, len(src))
		for k, v := range src {
			d[k] = v
		}
		return d
	}
	rec := make(map[string][]RecentSend, len(m.recent))
	for k, v := range m.recent {
		rec[k] = append([]RecentSend(nil), v...)
	}
	var lastMs int64
	if !m.last.IsZero() {
		lastMs = m.last.UnixMilli()
	}
	return MetricsState{
		Events: m.events, LastMs: lastMs, TodayDay: m.todayDay,
		Sent: cp(m.sent), Dormant: cp(m.dormant), Failed: cp(m.failed), Today: cp(m.today),
		LastSuccess: ls, Recent: rec,
	}
}

func (m *Metrics) restore(s MetricsState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	nz := func(src map[string]int) map[string]int {
		if src == nil {
			return map[string]int{}
		}
		return src
	}
	m.events = s.Events
	if s.LastMs > 0 {
		m.last = time.UnixMilli(s.LastMs)
	}
	m.todayDay = s.TodayDay
	m.sent, m.dormant, m.failed, m.today = nz(s.Sent), nz(s.Dormant), nz(s.Failed), nz(s.Today)
	m.lastSuccess = map[string]time.Time{}
	for k, ms := range s.LastSuccess {
		if ms > 0 {
			m.lastSuccess[k] = time.UnixMilli(ms)
		}
	}
	if s.Recent != nil {
		m.recent = s.Recent
	} else {
		m.recent = map[string][]RecentSend{}
	}
}
