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

export type Billing = 'subscription' | 'api' | 'unknown'

export interface QuotaWindow {
  kind: string // '5h' | '7d'
  window_minutes: number
  used_percent: number
  remaining_percent: number
  reset_at?: string
  // The window's reset has PASSED since this reading was taken: the counter has rolled over,
  // so used/remaining are not merely old — they are wrong, and must not be painted as a
  // quantity. Per-window, because a 5h window can roll while the 7d one stays valid.
  expired?: boolean
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
export interface ProbeResult {
  runtime: string
  status: 'ok' | 'failed' | 'not_supported'
  reason?: string
}
export interface RuntimeHealth {
  ok: boolean
  reason?: string // 'not_installed' | 'not_executable' | 'version_check_failed'
  version?: string
}
export interface RuntimeQuota {
  runtime: string // 'claude' | 'codex' | 'gemini'
  present: boolean
  evidence?: string[] // 'credentials' | 'snapshot' | 'sessions'
  billing?: Billing
  plan?: string
  windows?: QuotaWindow[]
  snapshot?: SnapshotMeta // absent ⟹ no reading has ever been captured
  health: RuntimeHealth
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
  // Drawn ONLY from readings we can stand behind: an expired window's used% is not old, it is
  // WRONG, and putting it on the always-visible pill would be exactly the "expired data
  // presented as live" the spec forbids. Nothing left ⟹ null ⟹ the pill says 「—」 and sends
  // you to the popover, which explains why.
  const tightest = computed<{ runtime: string; window: QuotaWindow } | null>(() => {
    let best: { runtime: string; window: QuotaWindow } | null = null
    for (const q of quotas.value) {
      if (q.snapshot?.stale) continue
      for (const w of q.windows ?? []) {
        if (w.expired) continue
        if (best === null || w.remaining_percent < best.window.remaining_percent) {
          best = { runtime: q.runtime, window: w }
        }
      }
    }
    return best
  })

  function billingFor(runtime: string): Billing | undefined {
    return quotas.value.find((q) => q.runtime === runtime)?.billing
  }

  return {
    quotas, subscriptions, apiRuntimes,
    hasSubscription, hasApi, tightest, billingFor,
    loading, loaded, error, fetchedAt, probeNote, load, probe,
  }
}
