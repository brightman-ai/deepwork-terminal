import { describe, expect, test } from 'bun:test'
import { usageMoneyPresentation, subscriptionCovers, facadeNote } from '../usageBillingPresentation'
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
  // The vendors this host holds a subscription with — NOT the runtimes. That distinction is the
  // whole point: the same Kimi plan is spendable from more than one CLI.
  const subscribed = [{ vendor: 'anthropic' }, { vendor: 'openai' }]

  test('request evidence partitions subscription and API without duplication', () => {
    expect(usageMoneyPresentation(row('anthropic', 'claude', 'subscription'), subscribed))
      .toMatchObject({ tab: 'sub', semantics: 'api_equivalent', evidence: 'request' })
    expect(usageMoneyPresentation(row('anthropic', 'claude', 'api'), subscribed))
      .toMatchObject({ tab: 'api', semantics: 'api_paid', evidence: 'request' })
  })

  test('unknown billing on a subscribed vendor is equivalent value', () => {
    expect(usageMoneyPresentation(row('anthropic', 'claude'), subscribed)).toEqual({
      tab: 'sub', semantics: 'api_equivalent', evidence: 'current_subscription_fallback',
    })
  })

  // The defect the vendor condition exists to fix. Before it, "codex has a subscription" alone
  // sent this row to 官方订阅 — a DeepSeek request billed against OpenAI quota.
  test('a vendor with no account here can NEVER reach the subscription tab, whatever the CLI is', () => {
    for (const billing of ['unknown', 'subscription', 'api'] as const) {
      for (const runtime of ['codex', 'claude', 'whale']) {
        expect(usageMoneyPresentation(row('deepseek', runtime, billing), subscribed).tab).toBe('api')
      }
    }
  })

  // Even a row that CLAIMS subscription billing cannot be an official subscription when no account
  // here covers that vendor. The flag describes how a request was paid for, not by whom.
  test('a subscription flag from an unsubscribed vendor is downgraded, not believed', () => {
    expect(usageMoneyPresentation(row('moonshot', 'codex', 'subscription'), subscribed))
      .toMatchObject({ tab: 'api', semantics: 'api_estimated' })
  })

  // The regression the hardcoded FIRST_PARTY_RUNTIME table made structurally impossible: you can
  // buy Kimi For Coding and spend it through codex, and a GLM Coding Plan and spend it through
  // Claude Code. Neither is a "first-party pair", and both are real subscriptions.
  test('a third-party subscription reaches the subscription tab once its account exists', () => {
    const withKimiAndGLM = [{ vendor: 'anthropic' }, { vendor: 'openai' }, { vendor: 'moonshot' }, { vendor: 'zhipu' }]
    expect(usageMoneyPresentation(row('moonshot', 'codex'), withKimiAndGLM).tab).toBe('sub')
    expect(usageMoneyPresentation(row('zhipu', 'claude'), withKimiAndGLM).tab).toBe('sub')
    // …and the SAME plan spent from a different CLI stays with it. Pairing by (runtime, vendor)
    // would have exiled this row to a tab headed「无官方订阅的消费」.
    expect(usageMoneyPresentation(row('moonshot', 'claude'), withKimiAndGLM).tab).toBe('sub')
  })

  // Configuration, not code, is what moves a row. Same fact, two hosts, two answers.
  test('the same row lands differently depending on which accounts this host holds', () => {
    expect(usageMoneyPresentation(row('moonshot', 'codex'), [{ vendor: 'openai' }]).tab).toBe('api')
    expect(usageMoneyPresentation(row('moonshot', 'codex'), [{ vendor: 'moonshot' }]).tab).toBe('sub')
  })

  // Placement is TOTAL. The previous version returned null here, which deleted these tokens from
  // both tabs while leaving them in the window summary — money visible nowhere.
  test('unknown billing with no subscription evidence is still shown, as an estimate', () => {
    expect(usageMoneyPresentation(row('anthropic', 'claude'), [])).toEqual({
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

  // 不算重，说到底是一条算术性质：两个 tab 是同一批行的一个【划分】——不重不漏。
  // 逐行断言"落在某个 tab"还不够，那证明不了两边加起来正好等于总数。
  test('两个 tab 是划分：token 不重不漏，加起来正好等于总量', () => {
    const accounts = [
      { vendor: 'anthropic' },
      { vendor: 'openai' },
      { vendor: 'moonshot', endpoints: ['mimo2codex-kimi-coding'] },
    ]
    const rows: UsageProviderRow[] = [
      { ...row('anthropic', 'claude', 'unknown', 3_170_906_215) },
      { ...row('openai', 'codex', 'unknown', 1_235_812_209), runtime_provider: 'openai' },
      { ...row('moonshot', 'codex', 'unknown', 891_397_854), runtime_provider: 'mimo2codex-kimi-coding' },
      { ...row('moonshot', 'codex', 'api', 5_000_000), runtime_provider: 'moonshot' },
      { ...row('deepseek', 'codex', 'unknown', 1_000) },
    ]
    const sum = (t: 'sub' | 'api') => rows
      .filter((r) => usageMoneyPresentation(r, accounts).tab === t)
      .reduce((n, r) => n + (r.total_tokens ?? 0), 0)
    const total = rows.reduce((n, r) => n + (r.total_tokens ?? 0), 0)

    expect(sum('sub') + sum('api')).toBe(total)          // 不漏
    expect(sum('sub')).toBe(3_170_906_215 + 1_235_812_209 + 891_397_854)
    expect(sum('api')).toBe(5_000_000 + 1_000)           // 不重：订阅那三笔一个 token 都没进来
  })

  // 「同时把订阅和纯 API 分离」的硬情形：同一个厂商，既有套餐又有 API key。
  // 只按 vendor 判会把两笔都算成订阅——因为 billing_mode 绝大多数是 unknown，分不开。
  // 端点是分得开的证据：套餐走登记过的中转，按量付费不走。
  test('同一厂商的套餐与 API key 按端点分开，不会混进订阅', () => {
    const kimiPlan = [{ vendor: 'moonshot', endpoints: ['mimo2codex-kimi-coding'] }]
    const viaPlan: UsageProviderRow = { ...row('moonshot', 'codex'), runtime_provider: 'mimo2codex-kimi-coding' }
    const viaApiKey: UsageProviderRow = { ...row('moonshot', 'codex'), runtime_provider: 'moonshot' }

    expect(usageMoneyPresentation(viaPlan, kimiPlan).tab).toBe('sub')
    expect(usageMoneyPresentation(viaApiKey, kimiPlan).tab).toBe('api')
    // 没登记端点的账号 = 第一方套餐（登录本身就是套餐），整个厂商都算它的，不是"什么都不算"。
    expect(usageMoneyPresentation(viaApiKey, [{ vendor: 'moonshot' }]).tab).toBe('sub')
  })

  // 请求自己说了是按量付费，就不许被同厂商的套餐吞掉——顺序上 api 必须先判。
  test('显式 api 计费永远进 API tab，哪怕你持有该厂商的套餐', () => {
    const kimiPlan = [{ vendor: 'moonshot', endpoints: ['mimo2codex-kimi-coding'] }]
    const meteredOnPlanEndpoint: UsageProviderRow = {
      ...row('moonshot', 'codex', 'api'), runtime_provider: 'mimo2codex-kimi-coding',
    }
    expect(usageMoneyPresentation(meteredOnPlanEndpoint, kimiPlan))
      .toMatchObject({ tab: 'api', semantics: 'api_paid' })
  })

  test('an unnamed vendor is never subscribed, whatever the accounts contain', () => {
    expect(subscriptionCovers({ vendor: '' }, [{ vendor: 'anthropic' }, { vendor: '' }])).toBe(false)
    expect(subscriptionCovers({ vendor: 'anthropic' }, subscribed)).toBe(true)
    expect(subscriptionCovers({ vendor: 'moonshot' }, subscribed)).toBe(false)
  })
})

describe('why a row carries no money', () => {
  const relayed = (
    basis: UsageProviderRow['attribution_basis'],
    requests = 1,
    priced = 0,
  ): UsageProviderRow => ({
    ...row('moonshot', 'codex'),
    attribution_basis: basis, runtime_provider: 'mimo2codex-kimi-coding', top_model: 'gpt-5.6-sol',
    requests, priced_requests: priced,
  })

  // Only the endpoint basis is a REFUSAL to price. Everything else unpriced is an ordinary gap the
  // existing「无价表」already covers, and saying more would over-explain.
  test('only an endpoint contradiction gets its own explanation', () => {
    const note = facadeNote(relayed('endpoint'))
    expect(note?.title).toContain('mimo2codex-kimi-coding')
    expect(note?.title).toContain('gpt-5.6-sol')
    for (const basis of ['model', 'confirmed', undefined] as const) {
      expect(facadeNote(relayed(basis))).toBeNull()
    }
  })

  // The residual gap, made visible instead of silent. Without a declaration for the relay, this
  // row is attributed and PRICED from the borrowed model id — exactly the original defect. The
  // caveat names the endpoint the user would go and declare, which is what makes it fixable.
  test('an undeclared endpoint says so, and names itself', () => {
    const note = facadeNote(relayed('unverified'))
    expect(note?.label).toBe('端点未登记')
    expect(note?.title).toContain('mimo2codex-kimi-coding')
  })

  // Nothing to name, nothing to say: claude records no endpoint at all, and a caveat about an
  // endpoint that does not exist would send the reader looking for one.
  test('unverified with no endpoint recorded stays quiet', () => {
    expect(facadeNote({ attribution_basis: 'unverified' })).toBeNull()
  })

  // The distinction real data forced: on this machine the relay served 2,318 turns and only 199
  // wore a borrowed id, so the row DOES have money. A flat「门面 model」beside ¥1,950 would
  // describe a row that does not exist.
  test('partial and total refusals read differently', () => {
    expect(facadeNote(relayed('endpoint', 2318, 2119))?.label).toBe('含门面 model')
    expect(facadeNote(relayed('endpoint', 2318, 2119))?.title).toContain('199')
    expect(facadeNote(relayed('endpoint', 5, 0))?.label).toBe('门面 model')
  })

  // A blank cost with no sentence beside it reads as a broken panel, so the reason must survive
  // even when the fields it likes to quote are missing.
  test('the explanation still says something when the endpoint and model are absent', () => {
    const bare: UsageProviderRow = { ...row('moonshot', 'codex'), attribution_basis: 'endpoint' }
    expect(facadeNote(bare)?.title.length ?? 0).toBeGreaterThan(10)
  })
})
