// useUsageQuota — the account/额度 side of the usage SSOT, alongside useUsageReport
// (tokens/cost). ONE fetch of GET /usage/quota → per-runtime state computed by kit/usage.
//
// The backend reports FOUR orthogonal facts per runtime, and this layer keeps them apart:
//
//   present  — the account exists on this host (credentials / history / a past reading).
//              This is a USER fact and the ONLY thing allowed to hide a provider.
//   billing  — 'subscription' (5h/7d windows) | 'api' (pay-per-token) | 'unknown'
//   snapshot — the last-known reading + whether it is stale
//   health   — can the CLI be executed right now (an ENVIRONMENT fact)
//
// Collapsing these into one `available` boolean is what made a logged-in Claude account
// vanish the moment a self-update dropped its executable bit. A broken CLI or a stale
// reading now DEGRADES the card; it never deletes it.
//
// Fetch goes through cliFetch(cliApi(...)) — the shared terminal API path — so the SAME
// chip works standalone (/api/usage/quota, X-CLI-Auth) AND pro-embedded (/api/cli/usage/quota).
import { ref, computed } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { findTightestQuota } from './usageQuotaGroups'
export { quotaGroupsFor, findTightestQuota, accountKey } from './usageQuotaGroups'

export type Billing = 'subscription' | 'api' | 'unknown'

export interface QuotaWindow {
  kind: string // '5h' | '7d'
  window_minutes: number
  used_percent: number
  remaining_percent: number
  reset_at?: string
  // The window's reset had passed by the time we looked: the counter rolled over.
  expired?: boolean
  // The value was DERIVED, not observed: the window rolled and nothing has reported since, so
  // nothing was used. Shown as such — never passed off as a reading.
  inferred?: boolean
}
export interface SnapshotMeta {
  captured_at?: string
  age_seconds: number
  // Where the reading came from. This is what answers "why did refreshing change nothing?" —
  // a 'rollout' reading only moves when the runtime chooses to write one; a 'probe' is us
  // going and asking.
  source?: 'hook' | 'rollout' | 'probe'
  stale: boolean
  stale_reason?: 'window_rolled' | 'too_old'
}
export interface QuotaGroup {
  family?: string
  windows?: QuotaWindow[]
  snapshot?: SnapshotMeta
}
export interface ProbeResult {
  runtime: string
  vendor?: string
  display?: string
  status: 'ok' | 'failed' | 'not_supported'
  reason?: string
}
/** What was actually SPENT this window, for vendors that meter that way (OpenAI does; Kimi
 *  meters in window percentage and sends nothing here). Always read from the vendor — a local
 *  rate-card computation measured 2.5× low every day, because the Fast multiplier is not in the
 *  transcript. `total` is absent when the window is too early to divide by. */
export interface QuotaCredits {
  used: number
  /**
   * What the PREVIOUS window consumed, end to end — a plain sum of the vendor's daily ledger.
   *
   * HISTORY, not this window's budget, and it must never be rendered as one. There is no budget
   * field because there is no budget to be had: it used to be reverse-derived as spend ÷ used-%,
   * and two measured windows put that ratio at 630.2 and 166.9 credits per point (3.78× apart),
   * which produced「剩 4,716」on an account whose previous week had run to 63,025. "How much is
   * left" is answered exactly by the percentage bar; restating it in credits required a
   * denominator nobody publishes.
   */
  prior_window?: number
  /** When that previous window opened (ISO-8601), so the figure can be labelled with real dates. */
  prior_window_start?: string
  source: 'api'
  window_start?: string
  days?: number
  /** false ⟹ the window opened mid-day and `used` therefore over-counts the first day. */
  whole_days?: boolean
}
/** Is the traffic being produced RIGHT NOW billed to this account? Absent ⟹ unknowable, which is
 *  never rendered as「no」. */
export interface QuotaAttribution {
  active: boolean
  vendor?: string
  display?: string
  /** The raw runtime-side provider id, when no vendor could be named for it. */
  provider_id?: string
}
export interface RuntimeHealth {
  ok: boolean
  reason?: string // 'not_installed' | 'not_executable' | 'version_check_failed'
  version?: string
}
export interface RuntimeQuota {
  runtime: string // 'claude' | 'codex' | 'gemini' — which CLI produced the traffic
  /** Who is BILLED. runtime+vendor is the account identity: one CLI can bill two vendors, so
   *  keying a list or a lookup by runtime alone silently drops one of them. */
  vendor?: string
  /** The vendor's invoice name, served by the backend so a new vendor needs no frontend change. */
  display?: string
  present: boolean
  evidence?: string[] // 'credentials' | 'snapshot' | 'sessions'
  billing?: Billing
  /**
   * The endpoint ids this SUBSCRIPTION is spent through (kit: Credential.RuntimeProviderIDs).
   *
   * Present so the money tabs can tell one vendor's plan from that same vendor's pay-per-token
   * traffic — holding a Kimi plan and a Moonshot API key at once is ordinary. EMPTY means no
   * endpoint restriction (a first-party plan is the CLI's own login), NOT "covers nothing".
   */
  endpoints?: string[]
  plan?: string
  // Family for the top-level compatibility-projection windows. New consumers use quota_groups
  // because independent Codex families can coexist and must not overwrite one another.
  family?: string
  windows?: QuotaWindow[]
  snapshot?: SnapshotMeta // absent ⟹ no reading has ever been captured
  // Lossless view: each family owns its windows + provenance. Top-level fields above remain
  // the newest compatibility projection for servers/consumers predating UB-16.
  quota_groups?: QuotaGroup[]
  credits?: QuotaCredits
  health: RuntimeHealth
  attribution?: QuotaAttribution
  note?: string
}


export function useUsageQuota() {
  const { cliFetch } = useCliAuth()
  const quotas = ref<RuntimeQuota[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref('')
  // When this client last SUCCEEDED in fetching. Distinct from snapshot.captured_at (when the
  // runtime last reported): a backgrounded tab can hold a perfectly fresh-looking snapshot
  // that it fetched ten minutes ago, and the user is looking at both numbers, not just one.
  const fetchedAt = ref<number>(0)
  // Why a refresh may not have moved the numbers (e.g. the live probe failed).
  const probeNote = ref('')

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await cliFetch(cliApi('/usage/quota'), { headers: { Accept: 'application/json' } })
      if (!res.ok) { error.value = `额度加载失败 (${res.status})`; return }
      apply((await res.json()) as { quotas?: RuntimeQuota[] })
    } catch {
      error.value = '额度加载失败'
    } finally {
      loading.value = false
    }
  }

  /**
   * probe — the USER-INITIATED refresh. It asks the provider for the account's current quota
   * instead of re-reading a file that may hold nothing new.
   *
   * Re-reading the disk cannot always help: codex records only the rate-limit family of the
   * model it is currently running, so while a session works on a per-model plan the ACCOUNT
   * limit stops being written at all — its newest reading can be hours old and polling will
   * never improve it. That is why pressing 刷新 used to move nothing.
   *
   * It costs one real provider request, so it happens ONLY when the user asks: never on the
   * background poll. A failed probe is not an error to show — the response still carries the
   * offline quotas, and `probeNote` explains why the numbers may not have moved.
   */
  async function probe(): Promise<void> {
    loading.value = true
    error.value = ''
    probeNote.value = ''
    try {
      const res = await cliFetch(cliApi('/usage/quota/refresh'), {
        method: 'POST',
        headers: { Accept: 'application/json' },
      })
      // A host that predates the probe endpoint simply has nothing to ask with. Fall back to
      // re-reading what the runtimes wrote — the same thing the poll does — rather than
      // reporting a failure the user can do nothing about.
      if (res.status === 404 || res.status === 405) {
        await load()
        probeNote.value = '此服务端不支持实时查询，显示的是运行时最后一次上报的读数'
        return
      }
      if (!res.ok) { error.value = `刷新失败 (${res.status})`; return }
      const d = (await res.json()) as { quotas?: RuntimeQuota[]; probe?: ProbeResult[] }
      apply(d)
      // Only a FAILED probe is worth a word. "not_supported" is normal (claude cannot be
      // asked — its usage arrives only when claude itself renders), and saying so on every
      // refresh would be noise.
      const failed = (d.probe ?? []).filter((p) => p.status === 'failed').map((p) => p.runtime)
      if (failed.length) probeNote.value = `${failed.join('、')} 实时查询失败，显示的是最后一次读数`
    } catch {
      error.value = '刷新失败'
    } finally {
      loading.value = false
    }
  }

  function apply(d: { quotas?: RuntimeQuota[] }): void {
    // Presence is the ONLY filter. Not health, not freshness, not "does it have windows".
    quotas.value = (d.quotas ?? []).filter((q) => q.present)
    loaded.value = true
    fetchedAt.value = Date.now()
  }

  const subscriptions = computed(() => quotas.value.filter((q) => q.billing !== 'api'))
  const apiRuntimes = computed(() => quotas.value.filter((q) => q.billing === 'api'))
  const hasSubscription = computed(() => quotas.value.some((q) => q.billing === 'subscription'))
  const hasApi = computed(() => apiRuntimes.value.length > 0)

  // The single most-constraining window — the number that says "am I about to hit a wall".
  //
  // An expired window is INCLUDED: the backend rolled it forward and derived its usage (the
  // window reset and nothing has reported since, so nothing was used). That derived 100% is
  // the honest current state, and dropping it would blank the pill for a runtime whose quota
  // is in fact wide open.
  const tightest = computed(() => findTightestQuota(quotas.value))

  function billingFor(runtime: string): Billing | undefined {
    return quotas.value.find((q) => q.runtime === runtime)?.billing
  }

  // The account currently being billed, if any account could tell us. This is a FIRST-HAND fact
  // (the newest session's provider), which is why it beats inferring relevance from spend rows.
  const activeAccount = computed(() => quotas.value.find((q) => q.attribution?.active) ?? null)

  return {
    quotas, subscriptions, apiRuntimes,
    hasSubscription, hasApi, tightest, billingFor, activeAccount,
    loading, loaded, error, fetchedAt, probeNote, load, probe,
  }
}
