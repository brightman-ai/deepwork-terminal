import type { UsageProviderRow } from './useUsageReport'

export type UsageMoneyTab = 'sub' | 'api'
export type UsageMoneySemantics = 'api_equivalent' | 'api_paid'

export interface UsageMoneyPresentation {
  tab: UsageMoneyTab
  semantics: UsageMoneySemantics
  evidence: 'request' | 'current_subscription_fallback'
}

/** The single placement policy for usage Money. Request facts remain unchanged. */
export function usageMoneyPresentation(
  provider: UsageProviderRow,
  currentSubscriptionRuntimes: ReadonlySet<string>,
): UsageMoneyPresentation | null {
  const mode = provider.billing_mode ?? 'unknown'
  if (mode === 'api') return { tab: 'api', semantics: 'api_paid', evidence: 'request' }
  if (mode === 'subscription') return { tab: 'sub', semantics: 'api_equivalent', evidence: 'request' }
  if (currentSubscriptionRuntimes.has(provider.runtime)) {
    return { tab: 'sub', semantics: 'api_equivalent', evidence: 'current_subscription_fallback' }
  }
  return null
}
