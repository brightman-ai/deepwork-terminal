import type { QuotaGroup, QuotaWindow, RuntimeQuota } from './useUsageQuota'

/** Pure grouped-quota read model, kept free of browser/Vue/auth side effects for regression tests.
 *
 *  Values live HERE and are re-exported by useUsageQuota, never the other way round: importing a
 *  value from that module pulls in useCliAuth → `window`, and these files must stay runnable with
 *  no DOM. (Types are fine — they are erased.) */

/** The account's identity, and therefore its list key. Two codex rows (OpenAI + Kimi) coexist, so
 *  keying by runtime alone silently renders one of them. */
export function accountKey(q: { runtime: string; vendor?: string }): string {
  return `${q.runtime}:${q.vendor ?? ''}`
}
export function quotaGroupsFor(q: RuntimeQuota): QuotaGroup[] {
  if (q.quota_groups?.length) return q.quota_groups
  if (q.windows?.length || q.snapshot) {
    return [{ family: q.family, windows: q.windows, snapshot: q.snapshot }]
  }
  return []
}

/** The tightest window across accounts, carrying WHICH account it belongs to.
 *
 *  It names the account, not just the runtime: with two codex subscriptions on one host a bare
 *  「codex」 no longer identifies whose quota is nearly gone — and「0%」about the wrong account is
 *  worse than no number, because it is actionable and wrong. */
export function findTightestQuota(
  quotas: RuntimeQuota[],
): { runtime: string; account: string; display: string; window: QuotaWindow } | null {
  let best: { runtime: string; account: string; display: string; window: QuotaWindow } | null = null
  for (const q of quotas) {
    for (const group of quotaGroupsFor(q)) {
      if (group.snapshot?.stale) continue
      for (const w of group.windows ?? []) {
        if (best === null || w.remaining_percent < best.window.remaining_percent) {
          best = { runtime: q.runtime, account: accountKey(q), display: q.display ?? q.runtime, window: w }
        }
      }
    }
  }
  return best
}
