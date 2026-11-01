/** Local HTML addresses have a disk identity plus browser navigation. Keep the
 * latter out of file reads/searches without loosening the filesystem grammar. */
export function splitLocalHtmlReference(address: string): { path: string; suffix: string } {
  const match = /^([^?#\r\n]+\.html?)([?#][^\r\n]*)$/i.exec(address)
  // Existing source-line links retain their meaning, including HTML source.
  if (!match || /^#L\d+(?:-L?\d+)?$/i.test(match[2]!)) return { path: address, suffix: '' }
  return { path: match[1]!, suffix: match[2]! }
}

/** Page parameters cannot replace the transport's path, auth or conversion
 * controls. Appending them still makes ordinary URLSearchParams work in HTML. */
const TRANSPORT_KEYS = new Set(['session', 'cwd', 'path', 'render', 'auth', 'preview', 'convert', 'pdf', 'asset', 'download', 'zip', 'entry', 'navigate', 'refresh'])
export function appendHtmlPreviewSuffix(renderUrl: string, suffix: string): string {
  if (!renderUrl || !suffix) return renderUrl
  const hashAt = suffix.indexOf('#')
  const hash = hashAt < 0 ? '' : suffix.slice(hashAt)
  const query = suffix.startsWith('?') ? suffix.slice(1, hashAt < 0 ? undefined : hashAt) : ''
  const params = new URLSearchParams()
  for (const [key, value] of new URLSearchParams(query)) {
    if (!TRANSPORT_KEYS.has(key)) params.append(key, value)
  }
  const extra = params.toString()
  return renderUrl.split('#')[0] + (extra ? (renderUrl.includes('?') ? '&' : '?') + extra : '') + hash
}

export function htmlPreviewRevision(url: string, revision: number): string {
  const at = url.indexOf('#'), base = at < 0 ? url : url.slice(0, at), hash = at < 0 ? '' : url.slice(at)
  return base + (base.includes('?') ? '&' : '?') + 'preview=' + revision + hash
}
