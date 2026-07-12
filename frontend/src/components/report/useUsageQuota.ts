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
}
export interface SnapshotMeta {
  captured_at?: string
  age_seconds: number
  stale: boolean
  stale_reason?: string // 'window_rolled' | 'too_old'
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

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await cliFetch(cliApi('/usage/quota'), { headers: { Accept: 'application/json' } })
      if (!res.ok) { error.value = `额度加载失败 (${res.status})`; return }
      const d = (await res.json()) as { quotas?: RuntimeQuota[] }
      // Presence is the ONLY filter. Not health, not freshness, not "does it have windows".
      quotas.value = (d.quotas ?? []).filter((q) => q.present)
      loaded.value = true
      fetchedAt.value = Date.now()
    } catch {
      error.value = '额度加载失败'
    } finally {
      loading.value = false
    }
  }

  const subscriptions = computed(() => quotas.value.filter((q) => q.billing !== 'api'))
  const apiRuntimes = computed(() => quotas.value.filter((q) => q.billing === 'api'))
  const hasSubscription = computed(() => quotas.value.some((q) => q.billing === 'subscription'))
  const hasApi = computed(() => apiRuntimes.value.length > 0)

  // The single most-constraining window — the number that says "am I about to hit a wall".
  // Drawn ONLY from fresh readings: a stale used% is a number we can no longer stand behind,
  // and putting it on the always-visible pill would be exactly the "expired data presented as
  // live" the spec forbids. All-stale ⟹ null ⟹ the pill says 「—」 and sends you to the popover.
  const tightest = computed<{ runtime: string; window: QuotaWindow } | null>(() => {
    let best: { runtime: string; window: QuotaWindow } | null = null
    for (const q of quotas.value) {
      if (q.snapshot?.stale) continue
      for (const w of q.windows ?? []) {
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
    loading, loaded, error, fetchedAt, load,
  }
}
