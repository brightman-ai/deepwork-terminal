// CHG-014 R3 — shared cost/token display helpers (SINGLE formatting source).
//
// Backend (internal/llm/usage/cost.go) is the ONE cost CALCULATION source (tokens ×
// 价表). This module is the ONE cost/token DISPLAY source: fleet KPI + settings 报表
// 共用同一格式化 (¥/$ 符号 · k/M 缩写 · 缺价/缺值诚实「—」). No second formatter.

/** Format a token count: k/M abbreviated, null/undefined ⇒「—」(honest, no fake). */
export function fmtTokens(n?: number | null): string {
  if (n === undefined || n === null) return '—'
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

/** Currency symbol for a backend currency code. */
export function currencySymbol(currency?: string): string {
  switch (currency) {
    case 'CNY': return '¥'
    case 'USD': return '$'
    default: return currency ? `${currency} ` : ''
  }
}

/**
 * Format an estimated cost. RED LINE: cost==null/undefined ⇒「—」(缺价不蒙).
 * `approx` (cost under-counts because a model had no price) → prefix「≈」honestly.
 */
export function fmtCost(cost?: number | null, currency?: string, approx = false): string {
  if (cost === undefined || cost === null) return '—'
  const sym = currencySymbol(currency)
  const v = cost < 0.01 && cost > 0 ? cost.toFixed(4) : cost.toFixed(2)
  return `${approx ? '≈' : ''}${sym}${v}`
}

/** Official subscription-credit projection; null remains explicit unknown. */
export function fmtCredits(credits?: number | null): string {
  if (credits === undefined || credits === null) return '—'
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(credits)
}
