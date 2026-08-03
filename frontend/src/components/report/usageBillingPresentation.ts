import type { UsageProviderRow } from './useUsageReport'

// ── which tab a row belongs in, and what kind of money it is ──────────────────────────────────
//
// The single placement policy for usage Money. Request facts are never rewritten; this only
// decides where an already-computed row is SHOWN and what claim is made about it.
//
// It is deliberately TOTAL — every row lands in exactly one tab. The previous version returned
// null for "unknown billing, no subscription account", which silently deleted those tokens from
// both tabs while leaving them in the window summary. A number that appears in no tab is worse
// than a mislabelled one: nobody can even ask about it.

export type UsageMoneyTab = 'sub' | 'api'

/**
 * What KIND of money a row is. Three genuinely different claims, so three values:
 *
 *   api_equivalent — you did NOT pay this. It is what these tokens would have cost at API prices,
 *                    shown so a subscription's value is legible. Never a bill.
 *   api_paid       — a real, per-token bill, with request-level evidence saying so.
 *   api_estimated  — real spend, but the transcript never recorded the billing mode. Calling it
 *                    「实付」would claim evidence we lack; calling it「≈等价」would be false, since
 *                    there is no subscription here to be equivalent TO.
 */
export type UsageMoneySemantics = 'api_equivalent' | 'api_paid' | 'api_estimated'

export interface UsageMoneyPresentation {
  tab: UsageMoneyTab
  semantics: UsageMoneySemantics
  evidence: 'request' | 'current_subscription_fallback' | 'no_subscription_evidence'
}

/**
 * The first-party pairings: which CLI is the vendor's own front door.
 *
 * This enumeration is a statement about the world (Anthropic makes Claude Code), not a whitelist
 * of features, and it is the ONE place that knowledge lives. It is also the structural reason a
 * third-party vendor can never reach the subscription tab: no pairing, no path.
 *
 * Note this is the OPPOSITE of the caller axis, which must stay data-driven — any agent that
 * produces usage facts shows up under its vendor without a code change here.
 */
const FIRST_PARTY_RUNTIME: Readonly<Record<string, string>> = {
  anthropic: 'claude',
  openai: 'codex',
  google: 'gemini',
}

/** Whether this row is a first-party CLI talking to its own vendor. */
export function isFirstPartyPair(vendor: string, runtime: string): boolean {
  return !!vendor && FIRST_PARTY_RUNTIME[vendor] === runtime
}

/**
 * Place one usage row.
 *
 * `currentSubscriptionRuntimes` is live account state from /usage/quota, kept OUT of the request
 * facts on purpose: what you are subscribed to today must not rewrite what a request cost last
 * week. It is used only to break the tie that request evidence leaves open — codex records a
 * billing mode only for Fast turns, and claude records none at all, so「unknown」is the common
 * case rather than the exotic one.
 *
 * The vendor condition is what makes that tie-break safe. Before it, "this runtime has a
 * subscription" alone sent a DeepSeek row to the subscription tab.
 */
export function usageMoneyPresentation(
  provider: UsageProviderRow,
  currentSubscriptionRuntimes: ReadonlySet<string>,
): UsageMoneyPresentation {
  const mode = provider.billing_mode ?? 'unknown'
  const firstParty = isFirstPartyPair(provider.vendor, provider.runtime)

  if (firstParty && mode === 'subscription') {
    return { tab: 'sub', semantics: 'api_equivalent', evidence: 'request' }
  }
  if (firstParty && mode === 'unknown' && currentSubscriptionRuntimes.has(provider.runtime)) {
    return { tab: 'sub', semantics: 'api_equivalent', evidence: 'current_subscription_fallback' }
  }
  if (mode === 'api') {
    return { tab: 'api', semantics: 'api_paid', evidence: 'request' }
  }
  // Everything else is spend we cannot prove the shape of — including a `subscription`-flagged row
  // from a THIRD-PARTY vendor, which cannot be an official subscription no matter what the flag
  // says. It stays visible, in the tab where money that leaves your account belongs, labelled as
  // an estimate.
  return { tab: 'api', semantics: 'api_estimated', evidence: 'no_subscription_evidence' }
}
