/** Preserve the request's permission root; only navigate the tree inside it. */
export function treeRelativePath(cwd: string, path: string): string | null {
  // Tree paths use a confined namespace. Never let the tree API reinterpret an
  // outside absolute path as a similarly named child of the current project.
  const clean = (value: string): string | null => {
    const parts: string[] = []
    for (const part of value.split('/')) {
      if (!part || part === '.') continue
      if (part === '..') {
        if (!parts.length) return null
        parts.pop()
      } else parts.push(part)
    }
    return (value.startsWith('/') ? '/' : '') + parts.join('/')
  }
  if (path.startsWith('~/')) return null // automatic location never guesses the server's HOME
  const target = clean(path)
  if (target === null) return null
  if (!target.startsWith('/')) return target
  const base = clean(cwd)
  if (!base?.startsWith('/')) return null
  if (target === base) return ''
  if (base === '/') return target.slice(1)
  return target.startsWith(base + '/') ? target.slice(base.length + 1) : null
}

export function filePreviewLocation(cwd: string, absolutePath: string, requestPath: string): { rel: string; parent: string } | null {
  for (const raw of [absolutePath, requestPath]) {
    const value = raw.trim().replace(/^@/, '')
    if (!value || value.startsWith('~/')) continue
    const rel = treeRelativePath(cwd, value)
    // An outside absolute identity cannot fall back to a misleading basename.
    if (rel === null && value.startsWith('/')) return null
    if (!rel) continue
    return { rel, parent: rel.slice(0, Math.max(0, rel.lastIndexOf('/'))) }
  }
  return null
}
