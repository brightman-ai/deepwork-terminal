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

/** One account's most-constraining CREDIBLE window — the sort key for card order (最紧优先).
 *
 *  Stale groups do not vote (an old number does not get to look urgent), and an account with no
 *  credible window sinks (it is comparable with nothing). This exists because the registry order
 *  once puts an EXHAUSTED subscription last, under the popover's fold, where the user reads it as
 *  「没有显示」— the tightest card is exactly the one that must be first (Human 2026-09-30). */
export function accountTightestRemaining(q: RuntimeQuota): number {
  let best: number | null = null
  for (const group of quotaGroupsFor(q)) {
    if (group.snapshot?.stale) continue
    for (const w of group.windows ?? []) {
      if (best === null || w.remaining_percent < best) best = w.remaining_percent
    }
  }
  return best === null ? Number.POSITIVE_INFINITY : best
}
