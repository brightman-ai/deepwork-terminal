import { describe, it, expect } from 'bun:test'
import { accountKey, findTightestQuota } from '../usageQuotaGroups'
import type { RuntimeQuota } from '../useUsageQuota'

// 账号轴：一个 CLI 可以给两家付钱。这些用例钉的是「按 runtime 归一」会怎么静默出错。

const account = (over: Partial<RuntimeQuota> & { runtime: string }): RuntimeQuota => ({
  present: true, health: { ok: true }, ...over,
}) as RuntimeQuota

describe('accountKey', () => {
  it('同一个 runtime 的两个订阅必须是两个 key（否则列表渲染只出一行）', () => {
    const openai = account({ runtime: 'codex', vendor: 'openai' })
    const kimi = account({ runtime: 'codex', vendor: 'kimi' })
    expect(accountKey(openai)).not.toBe(accountKey(kimi))
  })
  it('没有 vendor 的老响应仍有稳定 key', () => {
    expect(accountKey({ runtime: 'claude' })).toBe('claude:')
  })
})

describe('findTightestQuota', () => {
  const win = (remaining: number) => ({ kind: '7d', window_minutes: 10080, used_percent: 100 - remaining, remaining_percent: remaining })

  it('报出的是【账号】而不只是 runtime —— 两个 codex 订阅时「codex」谁也没指认', () => {
    const quotas = [
      account({ runtime: 'codex', vendor: 'openai', display: 'Codex 官方', windows: [win(80)] }),
      account({ runtime: 'codex', vendor: 'kimi', display: 'Kimi For Coding', windows: [win(25)] }),
    ]
    const got = findTightestQuota(quotas)
    expect(got?.display).toBe('Kimi For Coding')
    expect(got?.account).toBe('codex:kimi')
    expect(got?.window.remaining_percent).toBe(25)
  })

  it('过期读数不参与——它描述的是一个已经翻篇的窗口', () => {
    const quotas = [
      account({ runtime: 'codex', vendor: 'openai', display: 'Codex 官方', windows: [win(3)], quota_groups: [{ family: 'codex', windows: [win(3)], snapshot: { age_seconds: 999, stale: true } }] }),
      account({ runtime: 'claude', vendor: 'anthropic', display: 'Claude 官方', windows: [win(48)] }),
    ]
    expect(findTightestQuota(quotas)?.display).toBe('Claude 官方')
  })
})
