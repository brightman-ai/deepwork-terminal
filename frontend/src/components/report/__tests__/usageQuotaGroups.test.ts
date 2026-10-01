import { describe, expect, test } from 'bun:test'
import { findTightestQuota, quotaGroupsFor, accountTightestRemaining } from '../usageQuotaGroups'
import type { RuntimeQuota } from '../useUsageQuota'

const health = { ok: true }

describe('usage quota groups', () => {
  test('different families remain visible while stale groups cannot mask a fresh pill value', () => {
    const q: RuntimeQuota = {
      runtime: 'codex', present: true, billing: 'subscription', health,
      family: 'codex',
      windows: [{ kind: '7d', window_minutes: 10080, used_percent: 17, remaining_percent: 83 }],
      snapshot: { age_seconds: 60, source: 'rollout', stale: false },
      quota_groups: [
        {
          family: 'codex',
          windows: [{ kind: '7d', window_minutes: 10080, used_percent: 17, remaining_percent: 83 }],
          snapshot: { age_seconds: 60, source: 'rollout', stale: false },
        },
        {
          family: 'premium',
          windows: [{ kind: '7d', window_minutes: 10080, used_percent: 4, remaining_percent: 96 }],
          snapshot: { age_seconds: 50_000, source: 'probe', stale: true, stale_reason: 'too_old' },
        },
      ],
    }

    expect(quotaGroupsFor(q).map(g => g.family)).toEqual(['codex', 'premium'])
    expect(findTightestQuota([q])?.window.remaining_percent).toBe(83)
  })

  test('old server top-level fields still render as one compatibility group', () => {
    const q: RuntimeQuota = {
      runtime: 'claude', present: true, billing: 'subscription', health,
      windows: [{ kind: '5h', window_minutes: 300, used_percent: 30, remaining_percent: 70 }],
      snapshot: { age_seconds: 30, source: 'hook', stale: false },
    }
    expect(quotaGroupsFor(q)).toHaveLength(1)
    expect(findTightestQuota([q])?.window.remaining_percent).toBe(70)
  })
})

// ── 卡序键：最紧优先（Human 2026-09-30「最紧优先 + 一屏全见」）──────────────────────────
// 事故形状：注册表序把 5h 耗尽（剩 0%）的 Kimi 排在第 4 位，弹层限高把它裁在折叠线下，
// 用户读作「Kimi 没有显示」。排序键必须让最紧的卡浮到最前。
describe('accountTightestRemaining', () => {
  test('取账号内可信窗口的最小剩余；耗尽的账号键值为 0（排最前）', () => {
    const kimi: RuntimeQuota = {
      runtime: 'codex', vendor: 'moonshot', present: true, billing: 'subscription', health,
      quota_groups: [{
        family: 'plan', account_wide: true,
        windows: [{ kind: '5h', window_minutes: 300, used_percent: 100, remaining_percent: 0 }],
        snapshot: { age_seconds: 60, source: 'probe', stale: false },
      }],
    }
    const codex: RuntimeQuota = {
      runtime: 'codex', vendor: 'openai', present: true, billing: 'subscription', health,
      quota_groups: [{
        family: 'codex', account_wide: true,
        windows: [{ kind: '7d', window_minutes: 10080, used_percent: 88, remaining_percent: 12 }],
        snapshot: { age_seconds: 60, source: 'probe', stale: false },
      }],
    }
    expect(accountTightestRemaining(kimi)).toBe(0)
    expect(accountTightestRemaining(codex)).toBe(12)
    expect([kimi, codex].sort((a, b) => accountTightestRemaining(a) - accountTightestRemaining(b)).map(q => q.vendor))
      .toEqual(['moonshot', 'openai'])
  })

  test('stale 组不参与比紧（旧数说了不算）；无任何可信窗口 → 沉底', () => {
    const onlyStale: RuntimeQuota = {
      runtime: 'codex', vendor: 'openai', present: true, billing: 'subscription', health,
      quota_groups: [{
        family: 'premium',
        windows: [{ kind: '7d', window_minutes: 10080, used_percent: 96, remaining_percent: 4 }],
        snapshot: { age_seconds: 90_000, source: 'probe', stale: true, stale_reason: 'too_old' },
      }],
    }
    const waiting: RuntimeQuota = {
      runtime: 'codex', vendor: 'moonshot', present: true, billing: 'subscription', health,
    }
    expect(accountTightestRemaining(onlyStale)).toBe(Number.POSITIVE_INFINITY)
    expect(accountTightestRemaining(waiting)).toBe(Number.POSITIVE_INFINITY)
  })
})
