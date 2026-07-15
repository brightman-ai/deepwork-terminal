import type { QuotaGroup, QuotaWindow, RuntimeQuota } from './useUsageQuota'

/** Pure grouped-quota read model, kept free of browser/Vue/auth side effects for regression tests. */
export function quotaGroupsFor(q: RuntimeQuota): QuotaGroup[] {
  if (q.quota_groups?.length) return q.quota_groups
  if (q.windows?.length || q.snapshot) {
    return [{ family: q.family, windows: q.windows, snapshot: q.snapshot }]
  }
  return []
}

export function findTightestQuota(quotas: RuntimeQuota[]): { runtime: string; window: QuotaWindow } | null {
  let best: { runtime: string; window: QuotaWindow } | null = null
  for (const q of quotas) {
    for (const group of quotaGroupsFor(q)) {
      if (group.snapshot?.stale) continue
      for (const w of group.windows ?? []) {
        if (best === null || w.remaining_percent < best.window.remaining_percent) {
          best = { runtime: q.runtime, window: w }
        }
      }
    }
  }
  return best
}
