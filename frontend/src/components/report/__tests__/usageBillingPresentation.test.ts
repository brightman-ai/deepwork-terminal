import { describe, expect, test } from 'bun:test'
import { usageMoneyPresentation } from '../usageBillingPresentation'
import type { UsageProviderRow } from '../useUsageReport'

const row = (billing_mode: UsageProviderRow['billing_mode'], runtime = 'claude'): UsageProviderRow => ({
  provider: runtime, runtime, billing_mode,
  input_tokens: 1, output_tokens: 1, cache_read_tokens: 0, total_tokens: 2,
})

describe('usage Money placement', () => {
  test('request evidence partitions subscription and API without duplication', () => {
    const subscriptions = new Set(['claude'])
    expect(usageMoneyPresentation(row('subscription'), subscriptions)).toMatchObject({ tab: 'sub', semantics: 'api_equivalent', evidence: 'request' })
    expect(usageMoneyPresentation(row('api'), subscriptions)).toMatchObject({ tab: 'api', semantics: 'api_paid', evidence: 'request' })
  })

  test('unknown billing on a current subscription is equivalent value, never API paid', () => {
    expect(usageMoneyPresentation(row('unknown'), new Set(['claude']))).toEqual({
      tab: 'sub', semantics: 'api_equivalent', evidence: 'current_subscription_fallback',
    })
  })

  test('unknown billing without subscription evidence is not presented as money', () => {
    expect(usageMoneyPresentation(row('unknown'), new Set())).toBeNull()
  })
})
