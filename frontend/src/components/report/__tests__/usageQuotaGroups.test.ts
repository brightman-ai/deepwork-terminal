import { describe, expect, test } from 'bun:test'
import { findTightestQuota, quotaGroupsFor } from '../usageQuotaGroups'
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
