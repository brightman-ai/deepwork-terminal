import type { SearchEntry } from '@terminal/api/files'

export interface SearchResultRow extends SearchEntry {
  label: string
  grouped: boolean
}

/** A path is one result, including when pages overlap or a directory also matches. */
export function mergeSearchEntries(previous: SearchEntry[], incoming: SearchEntry[]): SearchEntry[] {
  const entries = new Map(previous.map(entry => [entry.rel, entry]))
  for (const entry of incoming) entries.set(entry.rel, entry)
  return [...entries.values()]
}

/** Flat parent-directory groups: path depth never adds rows or indentation. */
export function groupedSearchRows(entries: SearchEntry[], rootRel = '', sort: 'rank' | 'time' | 'name' = 'rank'): SearchResultRow[] {
  interface Group {
    directory?: SearchEntry
    files: SearchEntry[]
  }
  const scope = rootRel.replace(/^\/+|\/+$/g, '')
  const prefix = scope ? scope + '/' : ''
  const groups = new Map<string, Group>()
  for (const entry of mergeSearchEntries([], entries)) {
    if (!entry.rel.startsWith(prefix) || entry.rel === scope) continue
    const parent = entry.rel.slice(0, Math.max(0, entry.rel.lastIndexOf('/')))
    const key = entry.isDir ? entry.rel : parent
    let group = groups.get(key)
    if (!group) {
      group = { files: [] }
      if (key !== scope) {
        group.directory = { name: key.split('/').pop()!, rel: key, isDir: true, size: 0, mtimeMs: 0 }
      }
      groups.set(key, group)
    }
    if (entry.isDir) group.directory = entry
    else group.files.push(entry)
  }
  const ordered = [...groups.entries()]
  if (sort === 'name') ordered.sort(([a], [b]) => a.localeCompare(b))
  const rows: SearchResultRow[] = []
  for (const [, group] of ordered) {
    if (group.directory) rows.push({ ...group.directory, label: group.directory.rel.slice(prefix.length), grouped: false })
    for (const file of group.files) rows.push({ ...file, label: file.name, grouped: !!group.directory })
  }
  return rows
}
