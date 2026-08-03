import { describe, expect, test } from 'bun:test'
import { usageMoneyPresentation, isFirstPartyPair } from '../usageBillingPresentation'
import type { UsageProviderRow } from '../useUsageReport'

const row = (
  vendor: string,
  runtime: string,
  billing_mode: UsageProviderRow['billing_mode'] = 'unknown',
  total_tokens = 2,
): UsageProviderRow => ({
  vendor, vendor_display: vendor, runtime, billing_mode,
  input_tokens: 1, output_tokens: 1, cache_read_tokens: 0, total_tokens,
  requests: 1, priced_requests: 1,
})

describe('usage Money placement', () => {
  const subscribed = new Set(['claude', 'codex'])

  test('request evidence partitions subscription and API without duplication', () => {
    expect(usageMoneyPresentation(row('anthropic', 'claude', 'subscription'), subscribed))
      .toMatchObject({ tab: 'sub', semantics: 'api_equivalent', evidence: 'request' })
    expect(usageMoneyPresentation(row('anthropic', 'claude', 'api'), subscribed))
      .toMatchObject({ tab: 'api', semantics: 'api_paid', evidence: 'request' })
  })

  test('unknown billing on a first-party pair with a live subscription is equivalent value', () => {
    expect(usageMoneyPresentation(row('anthropic', 'claude'), subscribed)).toEqual({
      tab: 'sub', semantics: 'api_equivalent', evidence: 'current_subscription_fallback',
    })
  })

  // The defect this redesign exists to fix. Before the vendor condition, "codex has a subscription"
  // alone sent this row to 官方订阅 — a DeepSeek request billed against OpenAI quota.
  test('a third-party vendor can NEVER reach the subscription tab, whatever the CLI is', () => {
    for (const billing of ['unknown', 'subscription', 'api'] as const) {
      for (const runtime of ['codex', 'claude', 'whale']) {
        const placement = usageMoneyPresentation(row('deepseek', runtime, billing), subscribed)
        expect(placement.tab).toBe('api')
      }
    }
  })

  // Even a row that CLAIMS subscription billing cannot be an official subscription if the vendor
  // is not the CLI's own. The flag describes how the request was paid for, not by whom.
  test('a subscription flag from a third-party vendor is downgraded, not believed', () => {
    expect(usageMoneyPresentation(row('moonshot', 'codex', 'subscription'), subscribed))
      .toMatchObject({ tab: 'api', semantics: 'api_estimated' })
  })

  // Placement is TOTAL. The previous version returned null here, which deleted these tokens from
  // both tabs while leaving them in the window summary — money visible nowhere.
  test('unknown billing with no subscription evidence is still shown, as an estimate', () => {
    expect(usageMoneyPresentation(row('anthropic', 'claude'), new Set())).toEqual({
      tab: 'api', semantics: 'api_estimated', evidence: 'no_subscription_evidence',
    })
  })

  test('every row lands in exactly one tab', () => {
    const rows = [
      row('anthropic', 'claude', 'subscription'), row('anthropic', 'claude'),
      row('openai', 'codex'), row('openai', 'codex', 'api'),
      row('deepseek', 'codex'), row('moonshot', 'codex'), row('moonshot', 'claude'),
      row('', 'codex'), row('', 'whale'),
    ]
    const placed = rows.map((r) => usageMoneyPresentation(r, subscribed).tab)
    expect(placed.filter((t) => t === 'sub').length + placed.filter((t) => t === 'api').length)
      .toBe(rows.length)
  })

  test('first-party pairing is about the pair, not either half alone', () => {
    expect(isFirstPartyPair('anthropic', 'claude')).toBe(true)
    expect(isFirstPartyPair('openai', 'codex')).toBe(true)
    expect(isFirstPartyPair('openai', 'claude')).toBe(false) // claude code talking to OpenAI
    expect(isFirstPartyPair('moonshot', 'codex')).toBe(false)
    expect(isFirstPartyPair('', 'codex')).toBe(false) // unknown vendor is never first-party
  })
})
