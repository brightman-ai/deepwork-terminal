// useUsageQuota — the subscription rate-limit (额度) side of the usage SSOT, alongside
// useUsageReport (cost/tokens). ONE fetch of GET /usage/quota → per-runtime windows
// (claude 5h/7d, codex 5h/7d) computed by kit/usage (claude statusline hook + codex
// rollout transcript). This composable only transports + derives the single tightest
// window (the number that actually says "am I about to hit a limit?"). Honest: unavailable
// runtimes are dropped, no window → no chip.
//
// Fetch goes through cliFetch(cliApi(...)) — the shared terminal API path — so the SAME
// chip works standalone (/api/usage/quota, X-CLI-Auth) AND pro-embedded (/api/cli/usage/quota).
import { ref, computed } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export interface QuotaWindow {
  kind: string // '5h' | '7d'
  window_minutes: number
  used_percent: number
  remaining_percent: number
  reset_at?: string
}
export interface RuntimeQuota {
  runtime: string // 'claude' | 'codex' | 'gemini'
  available: boolean
  plan?: string
  billing?: string // 'subscription' | 'api' — 'api' has no windows (usage-based)
  windows?: QuotaWindow[]
  note?: string
  reason?: string
}

export function useUsageQuota() {
  const { cliFetch } = useCliAuth()
  const quotas = ref<RuntimeQuota[]>([])
  const loading = ref(false)
  const error = ref('')

  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await cliFetch(cliApi('/usage/quota'), { headers: { Accept: 'application/json' } })
      if (!res.ok) { error.value = `额度加载失败 (${res.status})`; return }
      const d = (await res.json()) as { quotas?: RuntimeQuota[] }
      // Keep runtimes with real windows OR an explicit API-billing marker (the
      // latter has no windows but must still surface as 「API 计费」, not vanish).
      quotas.value = (d.quotas ?? []).filter(
        (q) => q.available && ((q.windows?.length ?? 0) > 0 || q.billing === 'api'),
      )
    } catch {
      error.value = '额度加载失败'
    } finally {
      loading.value = false
    }
  }

  // The single most-constraining window across every available runtime — the one that
  // decides whether you're near a wall. Drives the glanceable chip.
  const tightest = computed<{ runtime: string; window: QuotaWindow } | null>(() => {
    let best: { runtime: string; window: QuotaWindow } | null = null
    for (const q of quotas.value) {
      for (const w of q.windows ?? []) {
        if (best === null || w.remaining_percent < best.window.remaining_percent) {
          best = { runtime: q.runtime, window: w }
        }
      }
    }
    return best
  })

  return { quotas, tightest, loading, error, load }
}
