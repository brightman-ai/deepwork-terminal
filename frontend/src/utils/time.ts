/**
 * Format a relative time string from an ISO 8601 timestamp.
 * Reference: Discourse-style relative time display.
 * Rules:
 *   < 1 min  → "Just now"
 *   < 60 min → "{N}m ago"
 *   < 24 h   → "{N}h ago"
 *   < 30 d   → "{N}d ago"
 *   >= 30 d  → localized date string (zh-CN)
 *
 * NOTE: "Just now" is only returned for timestamps within the last minute.
 *       Fixtures data with past timestamps will always show relative/date forms.
 */
export function formatRelativeTime(isoString: string): string {
  if (!isoString) return ''
  const ts = new Date(isoString).getTime()
  if (Number.isNaN(ts)) return ''

  const now = Date.now()
  const diffMs = now - ts
  if (diffMs < 0) return 'Just now'

  const diffMin = Math.floor(diffMs / 60_000)
  const diffHour = Math.floor(diffMs / 3_600_000)
  const diffDay = Math.floor(diffMs / 86_400_000)

  if (diffMin < 1) return 'Just now'
  if (diffMin < 60) return `${diffMin}m ago`
  if (diffHour < 24) return `${diffHour}h ago`
  if (diffDay < 30) return `${diffDay}d ago`
  return new Date(isoString).toLocaleDateString('zh-CN')
}

/**
 * Chinese relative time from an epoch-ms timestamp, for the notify health surfaces.
 *   0 / falsy → "—" (never)
 *   < 60s     → "N秒前" (≥1, never "0秒前")
 *   < 60m     → "N分钟前"
 *   < 24h     → "N小时前"
 *   else      → "N天前"
 */
export function relativeFromMs(atMs: number): string {
  if (!atMs) return '—'
  const sec = Math.max(1, Math.round((Date.now() - atMs) / 1000))
  if (sec < 60) return `${sec}秒前`
  const min = Math.round(sec / 60)
  if (min < 60) return `${min}分钟前`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr}小时前`
  return `${Math.round(hr / 24)}天前`
}

/**
 * Return the exact ISO string in a human-readable local format, for tooltip display.
 */
export function formatExactTime(isoString: string): string {
  if (!isoString) return ''
  const d = new Date(isoString)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString('zh-CN')
}
