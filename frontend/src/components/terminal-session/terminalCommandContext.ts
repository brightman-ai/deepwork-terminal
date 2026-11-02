/** Read explicit command context; never execute or expand shell input. A CLI
 * hard break may remove a word separator or split a word, so both meanings stay
 * possible until the fixed `git -C <literal root> status --short` grammar proves
 * the boundary. Ambiguous, dynamic and unsupported contexts remain unresolved. */
function outsideQuotes(text: string): { mask: boolean[]; balanced: boolean } {
  const mask = Array<boolean>(text.length).fill(false)
  let quote = '', substitution = 0
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (substitution) { if (c === '(') substitution++; if (c === ')') substitution--; continue }
    if (quote) {
      if (c === '\\' && quote !== "'") { i++; continue }
      if (c === quote) quote = ''
      continue
    }
    if (c === '\\') { i++; continue }
    if (c === '$' && text[i + 1] === '(') { substitution = 1; i++; continue }
    if (/['"`]/u.test(c)) { quote = c; continue }
    mask[i] = true
  }
  return { mask, balanced: !quote && !substitution }
}

export function gitStatusWorkingDirectory(chunks: readonly string[]): string | undefined {
  if (chunks.join('').length > 32_000 || chunks.length > 128) return undefined
  let text = '', heredoc = ''
  const cuts = new Set<number>()
  for (const chunk of chunks) {
    if (heredoc) {
      if (chunk.trim() === heredoc) { heredoc = ''; text += ';' }
      continue
    }
    cuts.add(text.length)
    text += chunk
    const marker = /<<-?\s*(['"]?)([A-Za-z_]\w*)\1\s*$/u.exec(text)
    if (marker && outsideQuotes(text).mask[marker.index]) heredoc = marker[2]
  }
  if (heredoc) return undefined
  const { mask, balanced } = outsideQuotes(text)
  if (!balanced || text.split('').some((c, i) => c === '#' && mask[i])) return undefined
  const separator = (i: number): boolean => i === text.length || /[\s;&|()<>]/u.test(text[i]) || cuts.has(i)
  const skip = (i: number): number => { while (/\s/u.test(text[i] ?? '') && i < text.length) i++; return i }
  const word = (value: string, i: number): number | undefined => text.startsWith(value, i) && separator(i + value.length) ? i + value.length : undefined
  const roots = new Set<string>()
  let unresolved = false
  for (const program of text.matchAll(/(?:^|[;&|])\s*(?:command\s+)?git/gu)) {
    const start = program.index! + program[0].lastIndexOf('git')
    if (/[;&|]/u.test(text[program.index!] ?? '') && !mask[program.index!]) continue
    if (!mask[start] || !separator(start + 3)) continue
    const option = skip(start + 3), optionEnd = word('-C', option)
    if (optionEnd === undefined) {
      if (word('status', option) !== undefined) unresolved = true
      continue
    }
    const argument = skip(optionEnd)
    let matched = false
    const accept = (end: number, value: string): void => {
      if (!separator(end)) return
      const status = word('status', skip(end))
      if (status === undefined) return
      const flag = skip(status), flagEnd = word('--short', flag) ?? word('-s', flag)
      if (flagEnd === undefined) return
      const tail = text.slice(flagEnd).split(/[;&|]/u, 1)[0]
      if (/--porcelain|(?:^|\s)-z(?:\s|$)/u.test(tail)) return
      matched = true
      if (!value.startsWith('/') || /[$`\u0000\r\n]/u.test(value)) unresolved = true
      else roots.add(value.replace(/\/+$/u, '') || '/')
    }
    const quote = /['"]/u.test(text[argument] ?? '') ? text[argument] : ''
    let value = ''
    if (quote) {
      for (let i = argument + 1; i < text.length; i++) {
        if (text[i] === quote) { accept(i + 1, value); break }
        if (text[i] === '\\' && quote === '"' && /["\\$`]/u.test(text[i + 1] ?? '')) value += text[++i]
        else value += text[i]
      }
    } else {
      for (let i = argument; i <= text.length; i++) {
        if (i > argument && separator(i)) accept(i, value)
        if (i === text.length || /[\s;&|()<>]/u.test(text[i])) break
        if (text[i] === '\\' && i + 1 < text.length) value += text[++i]
        else value += text[i]
      }
    }
    if (!matched) unresolved = true
  }
  return !unresolved && roots.size === 1 ? [...roots][0] : undefined
}
