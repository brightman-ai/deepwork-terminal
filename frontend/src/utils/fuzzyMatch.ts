/**
 * fuzzyMatch — gap-tolerant filter matching shared by every drawer search/filter box.
 *
 * The query is split on whitespace into terms; a candidate matches when EVERY term is a
 * case-insensitive substring of it (order-independent). So "test iso" matches
 * "tmux-test-isolation" because both "test" and "iso" appear — the space is the gap. An
 * empty / whitespace-only query matches everything.
 *
 * SSOT for the client side; the backend recursive file search (files.go matchesFuzzy)
 * mirrors the exact same rule so目录树 + 最近文件 + 历史输入 behave identically.
 */
export function fuzzyMatch(query: string, text: string): boolean {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean)
  if (terms.length === 0) return true
  const hay = text.toLowerCase()
  return terms.every((t) => hay.includes(t))
}
