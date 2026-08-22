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

/** One subscription this host actually holds, as reported by /usage/quota. */
export interface SubscriptionAccount {
  vendor: string
  /**
   * The endpoint ids this plan is spent through. EMPTY means no endpoint restriction — a
   * first-party plan (the CLI's own login), which covers that vendor's traffic wholesale.
   */
  endpoints?: readonly string[]
}

/**
 * Whether this row's spend is covered by a subscription you actually hold.
 *
 * Two things had to go into this, in order:
 *
 * 1. VENDOR, not (runtime, vendor). A subscription is bought from a vendor; the runtime is merely
 *    which CLI you spent it from, and the same Kimi plan is reachable from more than one. This
 *    replaced a hardcoded table of first-party pairings, which structurally barred any
 *    third-party plan from the subscription tab no matter what you had bought.
 *
 * 2. ENDPOINT, when the account declares one. Holding a Kimi PLAN and a Moonshot API KEY at the
 *    same time is ordinary, and vendor-matching alone files both under the subscription — because
 *    codex records a billing mode only for Fast turns and claude records none at all, so nearly
 *    every row arrives as「unknown」. The plan is reached through a declared endpoint
 *    ("mimo2codex-kimi-coding"); metered traffic is not. Matching the endpoint is evidence for
 *    which of the two this row was, where matching the vendor is a coin flip that always lands on
 *    "subscription".
 *
 * An account with no declared endpoints covers the vendor wholesale — that is the first-party
 * case, where the plan IS the CLI's login and there is no separate endpoint to name.
 */
export function subscriptionCovers(
  row: Pick<UsageProviderRow, 'vendor' | 'runtime_provider'>,
  accounts: readonly SubscriptionAccount[],
): boolean {
  if (!row.vendor) return false
  return accounts.some((account) => {
    if (account.vendor !== row.vendor) return false
    if (!account.endpoints?.length) return true
    return account.endpoints.includes(row.runtime_provider ?? '')
  })
}

/**
 * Place one usage row. The two tabs are 订阅 and 纯 API, and the split is EXHAUSTIVE and
 * DISJOINT — every row lands in exactly one, so no amount of money is ever shown twice.
 *
 * `accounts` is live account state, kept OUT of the request facts on purpose: what you are
 * subscribed to today must not rewrite what a request cost last week. It is used only to break
 * the tie that request evidence leaves open — codex records a billing mode only for Fast turns,
 * and claude records none at all, so「unknown」is the common case rather than the exotic one.
 *
 * Request evidence still wins where it exists: an explicit `api` row is metered spend and goes to
 * the API tab even when you hold that vendor's plan, which is exactly how a Kimi plan and a
 * Moonshot API key stay apart.
 */
export function usageMoneyPresentation(
  provider: UsageProviderRow,
  accounts: readonly SubscriptionAccount[],
): UsageMoneyPresentation {
  // `||`, not `??`. The model-bundle report path leaves billing_mode as an EMPTY STRING rather
  // than omitting it, and `??` passes '' straight through — so every one of its rows failed the
  // `=== 'unknown'` test and fell to the estimate branch, silently emptying the subscription tab
  // for any host on that path.
  const mode = provider.billing_mode || 'unknown'

  // Metered spend is metered spend. Checked BEFORE the subscription so a row the request itself
  // calls `api` can never be swallowed by a plan you happen to hold with the same vendor.
  if (mode === 'api') {
    return { tab: 'api', semantics: 'api_paid', evidence: 'request' }
  }
  const subscribed = subscriptionCovers(provider, accounts)
  if (subscribed && mode === 'subscription') {
    return { tab: 'sub', semantics: 'api_equivalent', evidence: 'request' }
  }
  if (subscribed) {
    return { tab: 'sub', semantics: 'api_equivalent', evidence: 'current_subscription_fallback' }
  }
  // Everything else is spend we cannot prove the shape of — including a `subscription`-flagged row
  // for a vendor no account here covers, which cannot be an official subscription no matter what
  // the flag says. It stays visible, in the tab where money that leaves your account belongs,
  // labelled as an estimate.
  return { tab: 'api', semantics: 'api_estimated', evidence: 'no_subscription_evidence' }
}

/** A short chip plus the sentence behind it. */
export interface FacadeNote {
  label: string
  title: string
}

type AttributionEvidence = Pick<
  UsageProviderRow,
  'attribution_basis' | 'runtime_provider' | 'top_model' | 'requests' | 'priced_requests'
>

/**
 * How much this row's heading can be trusted, when the answer is anything less than "fully".
 * Returns null for `confirmed` and `model`, which need no caveat.
 *
 * Two different admissions, deliberately not merged:
 *
 *   endpoint   — a REFUSAL to price. The endpoint contradicted the model id, so the id is a facade
 *                and cannot key a rate card. Real tokens beside a blank or short cost otherwise
 *                read as a broken panel, and the user goes hunting for a bug that isn't there.
 *   unverified — the row is priced and attributed from the model id alone, because nothing here
 *                knows whose endpoint it came from. This is the residual gap: on a host with no
 *                declaration for a relay, relayed traffic still lands under the vendor whose id the
 *                relay borrowed. Saying so is what makes it fixable — the caveat names the very
 *                endpoint the user would go and declare.
 *
 * WHOLE vs PART is a distinction real data forced. On this machine the relay served 2,318 turns of
 * which only 199 wore a borrowed id, so the row does have money, just not all of it. A chip reading
 *「门面 model」next to ¥1,950 would describe a row that does not exist.
 */
export function facadeNote(provider: AttributionEvidence): FacadeNote | null {
  if (provider.attribution_basis === 'unverified') {
    const endpoint = provider.runtime_provider
    if (!endpoint) return null
    return {
      label: '端点未登记',
      title: `这些请求发往「${endpoint}」，但本机没有登记它属于哪个厂商，`
        + `所以这一行的厂商只能按 model id 认——如果它是个中转，这个归属和金额就都可能是错的。`
        + `在订阅设置里把这个端点登记给对应厂商即可消除该不确定性。`,
    }
  }
  if (provider.attribution_basis !== 'endpoint') return null
  const requests = provider.requests ?? 0
  const priced = provider.priced_requests ?? 0
  const unpriced = Math.max(0, requests - priced)
  const partial = priced > 0 && unpriced > 0
  const endpoint = provider.runtime_provider
  const where = endpoint ? `「${endpoint}」` : '一个中转端点'
  const model = provider.top_model ? `「${provider.top_model}」` : '上游的 model id'
  const scope = partial && requests > 0
    ? `这一行 ${requests} 次请求里有 ${unpriced} 次`
    : '这些请求'
  return {
    label: partial ? '含门面 model' : '门面 model',
    title: `${scope}经 ${where} 中转，回显的 ${model} 是上游的门面，不是真正应答的模型。`
      + `厂商认得出、具体模型认不出，没有(厂商, 模型)就没有费率——所以这部分不估算金额，而不是估成 0。`
      + (partial ? '其余请求的 model id 是真的，已正常计价。' : ''),
  }
}
