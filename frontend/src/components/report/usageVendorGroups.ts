import type { UsageProviderRow, UsageRateCard } from './useUsageReport'

// ── rolling the caller rows up under the vendor they owe ──────────────────────────────────────
//
// The API tab reads「我欠谁钱」first and「谁替我花的」second, so its main row is the vendor and its
// children are the callers. kit deliberately ships the fine grain (vendor × caller × billing) and
// nothing coarser: a total can always be summed back up, and can never be split back apart.
//
// This module is the ONE place that summing happens. It is pure and tested, like its neighbours
// (usageQuotaGroups / usageBillingPresentation / quotaStaleness) — money arithmetic scattered
// through a template is money arithmetic nobody can check.
//
// It does NOT price anything. kit/usage remains the single cost CALCULATION source; adding
// already-priced rows together is a different act from turning tokens into money, and the honesty
// rules that survive it are made explicit below.

export interface UsageVendorGroup {
  /** Canonical vendor id; '' for "the model id named no vendor we know". */
  vendor: string
  /** Invoice name, e.g. 'Kimi'. Empty for the unknown vendor — the CALLER names that, with the
   *  raw model id beside it, because only the model id is actually information there. */
  display: string
  /** The caller rows underneath, biggest first. Never empty. */
  rows: UsageProviderRow[]
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheCreateTokens: number
  totalTokens: number
  /** Lossless per-currency money. */
  costs: Record<string, number>
  /** Scalar money — null unless there is EXACTLY one currency. Two currencies have no sum, and
   *  「—」 is the only honest rendering of a number that does not exist. */
  cost: number | null
  currency: string
  requests: number
  pricedRequests: number
  /** False when some request under this vendor had no price: the total UNDER-counts, so it is
   *  shown as「≈」. Derived from this vendor's own rows only — never inherited from the window. */
  costComplete: boolean
  /** YYYY-MM-DD, the OLDEST price verification behind this money. '' when nothing was priced. */
  priceVerifiedAt: string
  /** Published rate cards for `topModel` — taken from the biggest caller row, not merged, because
   *  a unit price belongs to a model and averaging two models' rates would describe neither. */
  unitPrices: UsageRateCard[]
  /** The biggest single contributor's top model — a label for 主要消耗, not a claim that every
   *  request here used it. */
  topModel: string
  /** Element-wise per-day totals across the callers. */
  spark: number[]
}

/** Element-wise add. Both series are oldest-first over the same window start, so index i is the
 *  same day in each; a short series is simply a row that ended early. */
function addSpark(into: number[], from?: number[]): number[] {
  if (!from?.length) return into
  const length = Math.max(into.length, from.length)
  const out = new Array<number>(length)
  for (let i = 0; i < length; i += 1) out[i] = (into[i] ?? 0) + (from[i] ?? 0)
  return out
}

/**
 * Group already-placed rows by vendor, largest spend first.
 *
 * Callers pass only the rows belonging to ONE tab. Grouping across tabs would put subscription
 * tokens into an API total, which is the double-count this whole redesign exists to prevent.
 */
export function groupByVendor(rows: UsageProviderRow[]): UsageVendorGroup[] {
  const byVendor = new Map<string, UsageVendorGroup>()

  for (const row of rows) {
    let group = byVendor.get(row.vendor)
    if (!group) {
      group = {
        vendor: row.vendor, display: row.vendor_display ?? '', rows: [],
        inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheCreateTokens: 0, totalTokens: 0,
        costs: {}, cost: null, currency: '', requests: 0, pricedRequests: 0,
        costComplete: true, priceVerifiedAt: '', topModel: '', unitPrices: [], spark: [],
      }
      byVendor.set(row.vendor, group)
    }
    group.rows.push(row)
    group.inputTokens += row.input_tokens ?? 0
    group.outputTokens += row.output_tokens ?? 0
    group.cacheReadTokens += row.cache_read_tokens ?? 0
    group.cacheCreateTokens += row.cache_create_tokens ?? 0
    group.totalTokens += row.total_tokens ?? 0
    group.requests += row.requests ?? 0
    group.pricedRequests += row.priced_requests ?? 0
    group.spark = addSpark(group.spark, row.spark)

    // Prefer the lossless `costs` map. Falling back to the scalar keeps older payloads working,
    // but never invents a currency: money with no currency code is money we cannot add.
    const costs = row.costs ?? (typeof row.cost === 'number' && row.currency ? { [row.currency]: row.cost } : {})
    for (const [currency, amount] of Object.entries(costs)) {
      group.costs[currency] = (group.costs[currency] ?? 0) + amount
    }
    // The staleness bound is the OLDEST date, not the newest: one freshly-checked model must not
    // make a row of half-year-old prices look current.
    if (row.price_verified_at && (!group.priceVerifiedAt || row.price_verified_at < group.priceVerifiedAt)) {
      group.priceVerifiedAt = row.price_verified_at
    }
  }

  for (const group of byVendor.values()) {
    group.rows.sort((a, b) => (b.total_tokens ?? 0) - (a.total_tokens ?? 0))
    group.topModel = group.rows[0]?.top_model ?? ''
    group.unitPrices = group.rows[0]?.unit_prices ?? []
    group.costComplete = group.requests > 0 && group.pricedRequests === group.requests
    const currencies = Object.keys(group.costs)
    if (currencies.length === 1) {
      group.cost = round4(group.costs[currencies[0]])
      group.currency = currencies[0]
    }
    for (const currency of currencies) group.costs[currency] = round4(group.costs[currency])
  }

  return [...byVendor.values()].sort((a, b) => {
    if (b.totalTokens !== a.totalTokens) return b.totalTokens - a.totalTokens
    return a.vendor < b.vendor ? -1 : a.vendor > b.vendor ? 1 : 0
  })
}

/** Match kit's rounding so a summed total and a backend total render identically. */
function round4(v: number): number {
  return Math.round(v * 10_000) / 10_000
}
