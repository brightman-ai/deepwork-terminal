/**
 * Give each source line one layout box, keeping highlight.js's cross-line span context.
 * Input is already escaped/highlighted code, never arbitrary document HTML. Numbers are CSS
 * generated from data-source-line so they cannot contaminate source offsets or document find.
 */
export function sourceLineHtml(html: string): string {
  const spans: string[] = []
  let line = 1
  const openLine = () => '<span class="fp-source-line" data-source-line="' + line + '"><span class="fp-source-text">' + spans.join('')
  const closeLine = () => '</span>'.repeat(spans.length) + '</span></span>'
  let result = openLine()
  let offset = 0
  for (const match of html.matchAll(/<span\b[^>]*>|<\/span>|\n/g)) {
    result += html.slice(offset, match.index)
    const token = match[0]
    if (token === '\n') {
      result += '\n' + closeLine()
      line++
      result += openLine()
    } else {
      result += token
      if (token === '</span>') spans.pop()
      else spans.push(token)
    }
    offset = (match.index ?? 0) + token.length
  }
  return result + html.slice(offset) + closeLine()
}
