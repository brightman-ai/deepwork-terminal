import { describe, expect, test } from 'bun:test'
import { groupByVendor } from '../usageVendorGroups'
import type { UsageProviderRow } from '../useUsageReport'

const row = (p: Partial<UsageProviderRow> & Pick<UsageProviderRow, 'vendor' | 'runtime'>): UsageProviderRow => ({
  vendor_display: p.vendor ? p.vendor.toUpperCase() : '',
  billing_mode: 'unknown',
  input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_create_tokens: 0, total_tokens: 0,
  requests: 1, priced_requests: 1,
  ...p,
})

describe('vendor grouping', () => {
  test('callers roll up under the vendor they owe, biggest vendor first', () => {
    const groups = groupByVendor([
      row({ vendor: 'moonshot', runtime: 'codex', total_tokens: 100, costs: { USD: 1 } }),
      row({ vendor: 'moonshot', runtime: 'claude', total_tokens: 300, costs: { USD: 3 } }),
      row({ vendor: 'deepseek', runtime: 'codex', total_tokens: 50, costs: { USD: 0.5 } }),
    ])
    expect(groups.map((g) => g.vendor)).toEqual(['moonshot', 'deepseek'])
    expect(groups[0].totalTokens).toBe(400)
    expect(groups[0].cost).toBe(4)
    expect(groups[0].currency).toBe('USD')
    // Callers ordered by spend, so the biggest contributor is the one you read first.
    expect(groups[0].rows.map((r) => r.runtime)).toEqual(['claude', 'codex'])
  })

  // The invariant that makes the vendor axis worth having: a vendor bills in ONE currency, so a
  // vendor row has a computable total. If two ever collide, the total does not exist and「—」is the
  // only honest rendering — never a sum of dollars and yuan.
  test('two currencies under one vendor produce no scalar total', () => {
    const [group] = groupByVendor([
      row({ vendor: 'x', runtime: 'codex', total_tokens: 10, costs: { USD: 1 } }),
      row({ vendor: 'x', runtime: 'claude', total_tokens: 10, costs: { CNY: 7 } }),
    ])
    expect(group.cost).toBeNull()
    expect(group.currency).toBe('')
    expect(group.costs).toEqual({ USD: 1, CNY: 7 })
  })

  test('a vendor with any unpriced request is incomplete, and one with none is not', () => {
    const [mixed] = groupByVendor([
      row({ vendor: 'moonshot', runtime: 'codex', total_tokens: 10, requests: 3, priced_requests: 3, costs: { USD: 1 } }),
      row({ vendor: 'moonshot', runtime: 'claude', total_tokens: 10, requests: 2, priced_requests: 0 }),
    ])
    expect(mixed.costComplete).toBe(false)
    expect(mixed.requests).toBe(5)
    expect(mixed.pricedRequests).toBe(3)

    const [clean] = groupByVendor([
      row({ vendor: 'openai', runtime: 'codex', total_tokens: 10, requests: 2, priced_requests: 2, costs: { USD: 1 } }),
    ])
    expect(clean.costComplete).toBe(true)
  })

  // Oldest, not newest: one freshly-checked model must not make a row of stale prices look current.
  test('price age is the oldest verification behind the money', () => {
    const [group] = groupByVendor([
      row({ vendor: 'openai', runtime: 'codex', total_tokens: 10, price_verified_at: '2026-08-03' }),
      row({ vendor: 'openai', runtime: 'claude', total_tokens: 10, price_verified_at: '2026-01-15' }),
    ])
    expect(group.priceVerifiedAt).toBe('2026-01-15')
  })

  test('an unpriced vendor states no price age at all', () => {
    const [group] = groupByVendor([row({ vendor: 'moonshot', runtime: 'codex', total_tokens: 10, requests: 1, priced_requests: 0 })])
    expect(group.priceVerifiedAt).toBe('')
    expect(group.cost).toBeNull()
  })

  // The unknown vendor is its own group and keeps its model id, which is the only actionable thing
  // in it. Merging it into a named vendor would be a fabricated attribution.
  test('the unknown vendor is a group of its own', () => {
    const groups = groupByVendor([
      row({ vendor: '', vendor_display: '', runtime: 'codex', total_tokens: 500, top_model: 'zzz-unknown-1' }),
      row({ vendor: 'openai', runtime: 'codex', total_tokens: 10 }),
    ])
    expect(groups[0].vendor).toBe('')
    expect(groups[0].display).toBe('')
    expect(groups[0].topModel).toBe('zzz-unknown-1')
    expect(groups).toHaveLength(2)
  })

  test('sparks add element-wise across callers', () => {
    const [group] = groupByVendor([
      row({ vendor: 'moonshot', runtime: 'codex', total_tokens: 10, spark: [1, 2, 3] }),
      row({ vendor: 'moonshot', runtime: 'claude', total_tokens: 10, spark: [10, 0, 5] }),
    ])
    expect(group.spark).toEqual([11, 2, 8])
  })

  // Older payloads carried only the scalar. Accept it, but never invent a currency for money that
  // arrived without one — unlabelled money cannot be added to anything.
  test('a scalar cost is used only when it carries a currency', () => {
    const [withCurrency] = groupByVendor([row({ vendor: 'openai', runtime: 'codex', total_tokens: 10, cost: 2.5, currency: 'USD' })])
    expect(withCurrency.cost).toBe(2.5)
    const [without] = groupByVendor([row({ vendor: 'openai', runtime: 'codex', total_tokens: 10, cost: 2.5 })])
    expect(without.cost).toBeNull()
  })

  test('grouping is total — no row is dropped', () => {
    const rows = [
      row({ vendor: 'a', runtime: 'codex', total_tokens: 1 }),
      row({ vendor: 'a', runtime: 'claude', total_tokens: 2 }),
      row({ vendor: 'b', runtime: 'whale', total_tokens: 3 }),
      row({ vendor: '', runtime: 'codex', total_tokens: 4 }),
    ]
    const groups = groupByVendor(rows)
    expect(groups.reduce((n, g) => n + g.rows.length, 0)).toBe(rows.length)
    expect(groups.reduce((n, g) => n + g.totalTokens, 0)).toBe(10)
  })
})
