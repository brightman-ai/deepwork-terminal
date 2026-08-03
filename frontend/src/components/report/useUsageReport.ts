// Shared usage/cost report data layer (fleet + settings 同源).
//
// ONE fetch of GET /usage/report → ONE cost dataset, consumed by BOTH the fleet
// overview KPI/dashboard AND the settings 报表 (Linus: 一套 cost/report 数据, 不为
// settings 另起一份). The window is a param so each mount picks its own range; the
// backend (kit/usage) is the SINGLE cost CALCULATION source (tokens × 价表), this
// composable just transports it. Honest: cost==null ⇒「—」, never fabricated.
//
// Fetch goes through cliFetch(cliApi(...)) so the SAME data layer works standalone
// (/api/usage/report) AND pro-embedded (/api/cli/usage/report).

import { ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export interface UsageReportSummary {
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_create_tokens?: number
  total_tokens: number
  cost?: number | null
  currency?: string
  cost_complete?: boolean
}
export interface UsageReportRow {
  date: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_create_tokens?: number
  total_tokens: number
  cost?: number | null
  currency?: string
}
/**
 * One (vendor, caller, billing) slice of a window — kit's `usage.ProviderRow`.
 *
 * The two money questions live on separate fields because they have different answers:
 * `vendor` is WHO IS OWED (derived from the model id), `runtime` is WHO SPENT IT (the CLI).
 * They were one field until a codex pane talking to DeepSeek produced a row labelled OpenAI
 * that landed under an OpenAI subscription — an impossible bill.
 */
export interface UsageProviderRow {
  /** Canonical vendor id ('anthropic'|'openai'|'moonshot'|'deepseek'|…). '' = unresolved model id. */
  vendor: string
  /** The vendor's name as it appears on an invoice ('Kimi'). Empty exactly when `vendor` is. */
  vendor_display?: string
  /** The CLI that made the request ('claude'|'codex'|'whale'|…). Data-driven, never an enum. */
  runtime: string
  billing_mode?: 'subscription' | 'api' | 'unknown'
  billing_coverage?: 'complete' | 'partial' | 'missing'
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_create_tokens?: number
  total_tokens: number
  cost?: number | null
  currency?: string
  /** Lossless per-currency money. `cost` is populated only when there is exactly one currency. */
  costs?: Record<string, number>
  /** This row's OWN price completeness — so a「≈」here is never caused by a model in another tab. */
  requests?: number
  priced_requests?: number
  /** YYYY-MM-DD: the OLDEST price-rule verification date behind this row's money (staleness bound). */
  price_verified_at?: string
  top_model?: string
  /**
   * Published rate cards for `top_model`, the one the money was computed with first.
   *
   * A currency SYMBOL is not evidence. Moonshot sells kimi-k3 at both ¥20/M and $3.00/M — the
   * same price at its own 6.67 conversion — so a bare「$85.93」leaves no way to tell which list
   * produced it, while being wrong by 6.67× still looks entirely plausible. Showing the unit
   * price settles it without anyone holding an exchange rate.
   */
  unit_prices?: UsageRateCard[]
  spark?: number[]
}

/** One vendor-published rate card. Never a converted one — see `unit_prices`. */
export interface UsageRateCard {
  currency: string
  input_per_m: number
  output_per_m: number
  cache_read_per_m: number
  source_url?: string
  /** The card this row's money was actually computed with. Others are context. */
  primary?: boolean
}
export interface UsageReportData {
  window: string
  start_date?: string
  end_date?: string
  rows?: UsageReportRow[]
  summary?: UsageReportSummary
  providers?: UsageProviderRow[]
  available: boolean
  reason?: string
  data_source?: string
}

/**
 * useUsageReport fetches the usage/cost report for a backend window ('7d' | '30d').
 * Returns reactive { report, loading, error, load }. Caller owns when to load
 * (fleet on mount + 7d default; settings on its window toggle).
 */
export function useUsageReport() {
  const { cliFetch } = useCliAuth()
  const report = ref<UsageReportData | null>(null)
  const loading = ref(false)
  const error = ref('')

  async function load(window: '24h' | '7d' | '14d' | '30d' = '7d'): Promise<void> {
    loading.value = true
    error.value = ''
    try {
	  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
	  const params = new URLSearchParams({ window, timezone })
	  const res = await cliFetch(cliApi(`/usage/report?${params}`), {
        headers: { Accept: 'application/json' },
      })
      if (!res.ok) {
        error.value = `加载用量失败 (${res.status})`
        return
      }
      report.value = (await res.json()) as UsageReportData
    } catch {
      error.value = '加载用量失败'
    } finally {
      loading.value = false
    }
  }

  return { report, loading, error, load }
}
