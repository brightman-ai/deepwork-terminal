/** Keep the branching path segments that distinguish results, not identical heads/tails. */
export function searchPathContexts(paths: string[]): Map<string, string> {
  interface Branch { children: Map<string, Branch>; terminal: boolean }
  const root: Branch = { children: new Map(), terminal: false }
  const parents = new Map(paths.map(path => [path, path.split('/').slice(0, -1)]))
  for (const parts of parents.values()) {
    let node = root
    for (const part of parts) {
      if (!node.children.has(part)) node.children.set(part, { children: new Map(), terminal: false })
      node = node.children.get(part)!
    }
    node.terminal = true
  }
  const contexts = new Map<string, string>()
  for (const [path, parts] of parents) {
    let node = root
    const selected: number[] = []
    for (let i = 0; i < parts.length; i++) {
      if (node.children.size > 1 || node.terminal) selected.push(i)
      node = node.children.get(parts[i])!
    }
    // The final parent still orients a sole result; no root-directory fiction.
    if (!selected.length && parts.length) selected.push(parts.length - 1)
    contexts.set(path, selected.map((i, j) => (j && i > selected[j - 1] + 1 ? '… / ' : '') + parts[i]).join(' / '))
  }
  // Repeated segment names can make an ancestor and its child both read "work".
  // If compression collides, show complete parent paths for that basename family.
  const identities = new Set<string>()
  const ambiguousNames = new Set<string>()
  for (const [path, context] of contexts) {
    const name = path.split('/').pop()!
    const identity = JSON.stringify([name, context])
    if (identities.has(identity)) ambiguousNames.add(name)
    identities.add(identity)
  }
  for (const [path, parts] of parents) {
    if (ambiguousNames.has(path.split('/').pop()!)) contexts.set(path, parts.join(' / '))
  }
  return contexts
}
