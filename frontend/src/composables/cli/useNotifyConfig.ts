/**
 * useNotifyConfig — single SSOT view over the notify provider config API
 * (`/api/notify/config`). Both the full settings section
 * (portals/settings/sections/NotificationsSection.vue) and the quick sheet
 * (components/terminal-session/NotifyQuickSheet.vue) read/mutate through this,
 * so toggling/testing in one surface is immediately reflected in the other.
 *
 * State is module-level (shared across all callers) so the two surfaces stay in
 * lock-step without prop drilling. All requests carry cli-auth (X-CLI-Auth /
 * X-Auth-Code from localStorage `cli_auth_code`) via useCliAuth().cliFetch — the
 * same authed fetch the rest of the terminal settings use.
 */
import { ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'

export type NotifyKind = 'ilink' | 'webpush' | 'feishu' | 'dingtalk' | 'wecom' | 'slack'

export interface NotifyQuota {
  used: number
  max: number
}

/**
 * One historical send outcome — the recent-3 troubleshooting trail.
 * `outcome`: 0=not-configured · 1=sent · 2=dormant · 3=failed (mirrors notify.Outcome).
 * `detail`: failure reason for troubleshooting ("订阅失效(410)", "BadJwtToken"…).
 */
export type NotifyOutcome = 0 | 1 | 2 | 3

export interface NotifyRecentSend {
  atMs: number
  outcome: NotifyOutcome
  detail?: string
}

export interface NotifyProvider {
  kind: NotifyKind | string
  name: string
  enabled: boolean
  configured: boolean
  healthy: boolean
  quota: NotifyQuota | null
  todaySent: number
  activationHint: string
  /** Redacted webhook settings with creds masked. `secret` is present (as "••••")
   *  only when a signing secret is configured — used to show the signing state. */
  settings: { url?: string; secret?: string } | null
  // ── merged from metrics.perProvider (by kind) so a row has everything it needs ──
  /** 0 = never succeeded. */
  lastSuccessAtMs: number
  /** Last ≤3 attempts, newest last. */
  recent: NotifyRecentSend[]
  /** Lifetime failed count (for the red light: enabled but last send failed). */
  failedCount: number
  /** Lifetime sent count. */
  sentCount: number
}

export interface NotifyProviderMetric {
  provider: string
  sent: number
  dormant: number
  failed: number
  lastSuccessAtMs: number
  recent: NotifyRecentSend[]
}

export interface NotifyMetrics {
  events: number
  lastAtMs: number
  perProvider: NotifyProviderMetric[]
}

export interface NotifyConfig {
  providers: NotifyProvider[]
  metrics: NotifyMetrics
}

/** Honest test outcome echoed straight from the backend. */
export type TestResult = 'sent' | 'dormant' | 'failed' | 'not-configured' | 'cooldown' | 'error'

// ── module-level shared state (one instance for the whole app) ────────────────
const providers = ref<NotifyProvider[]>([])
const metrics = ref<NotifyMetrics>({ events: 0, lastAtMs: 0, perProvider: [] })
const loading = ref(false)
const loaded = ref(false)
const error = ref('')

// The webhook IM channels that accept a url + secret via the settings endpoint.
// (Slack uses only the url — the webhook URL is its credential — but shares the same
// settings form/flow for UI consistency; its build closure ignores the secret.)
export const WEBHOOK_KINDS: NotifyKind[] = ['feishu', 'dingtalk', 'wecom', 'slack']
export function isWebhookKind(kind: string): boolean {
  return (WEBHOOK_KINDS as string[]).includes(kind)
}

export function useNotifyConfig() {
  const { cliFetch } = useCliAuth()

  function apply(cfg: NotifyConfig | null): void {
    if (!cfg) return
    const m = cfg.metrics ?? { events: 0, lastAtMs: 0, perProvider: [] }
    metrics.value = m
    // Merge metrics.perProvider into each provider by kind so every ProviderHealthRow
    // has its own troubleshooting trail (last-success + recent-3) without a second lookup.
    const byKind = new Map<string, NotifyProviderMetric>()
    for (const pm of Array.isArray(m.perProvider) ? m.perProvider : []) byKind.set(pm.provider, pm)
    const raw = Array.isArray(cfg.providers) ? cfg.providers : []
    providers.value = raw.map((p) => {
      const pm = byKind.get(p.kind)
      return {
        ...p,
        lastSuccessAtMs: pm?.lastSuccessAtMs ?? 0,
        recent: Array.isArray(pm?.recent) ? pm!.recent : [],
        failedCount: pm?.failed ?? 0,
        sentCount: pm?.sent ?? 0,
      }
    })
    loaded.value = true
  }

  /** GET /api/notify/config — refresh the shared view. */
  async function refresh(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const r = await cliFetch('/api/notify/config')
      if (r.ok) apply(await r.json())
      else error.value = `加载失败 (${r.status})`
    } catch {
      error.value = '加载失败，请检查网络后重试'
    } finally {
      loading.value = false
    }
  }

  /** PUT /api/notify/config — flip one provider's on/off; response refreshes all. */
  async function setEnabled(kind: string, enabled: boolean): Promise<boolean> {
    error.value = ''
    try {
      const r = await cliFetch('/api/notify/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ providers: [{ kind, enabled }] }),
      })
      if (r.ok) { apply(await r.json()); return true }
      error.value = `保存失败 (${r.status})`
      return false
    } catch {
      error.value = '保存失败，请稍后重试'
      return false
    }
  }

  /**
   * PUT /api/notify/providers/{kind}/settings — set the webhook url, and the secret
   * only when intended. `secret` semantics: undefined = leave the existing secret
   * untouched (editing the URL won't wipe it); '' = clear it (signing off);
   * a value = set/replace it. Matches the backend's pointer-based merge.
   */
  async function setSettings(kind: string, url: string, secret?: string): Promise<boolean> {
    error.value = ''
    try {
      const body: { url: string; secret?: string } = { url }
      if (secret !== undefined) body.secret = secret
      const r = await cliFetch(`/api/notify/providers/${kind}/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (r.ok) { apply(await r.json()); return true }
      error.value = `保存失败 (${r.status})`
      return false
    } catch {
      error.value = '保存失败，请稍后重试'
      return false
    }
  }

  /**
   * POST /api/notify/providers/{kind}/test — send a real test to ONE provider.
   * Returns the honest backend outcome; 429 surfaces as 'cooldown' (per-provider
   * 8s cooldown). Refreshes config afterward so todaySent/metrics update.
   */
  async function test(kind: string): Promise<TestResult> {
    try {
      const r = await cliFetch(`/api/notify/providers/${kind}/test`, { method: 'POST' })
      if (r.status === 429) return 'cooldown'
      if (!r.ok) return 'error'
      const d = await r.json() as { result?: string }
      void refresh() // todaySent / metrics moved — keep both surfaces honest
      return (d.result as TestResult) ?? 'error'
    } catch {
      return 'error'
    }
  }

  return {
    providers,
    metrics,
    loading,
    loaded,
    error,
    refresh,
    setEnabled,
    setSettings,
    test,
  }
}
