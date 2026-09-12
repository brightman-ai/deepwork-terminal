/**
 * treeSort — 目录树的排序规则（2026-09-12，REQ-fp-tree-sort）。
 *
 * 目录树默认按名字排，但两种真实场景它覆盖不了：找"刚生成的产出"要按时间，找"占空间的
 * 大文件"要按大小。接口本来就带 mtimeMs / size，所以这是纯前端的一个比较器，零协议变化。
 *
 * 语义（与搜索"目录组置顶"同一立意）：**目录恒在前**，组内才按所选键排——
 *   · name：名字升序（现状）
 *   · time：文件按修改时间**降序**（新→旧；找产出物的方向）
 *   · size：文件按大小**降序**（大→小；找占空间者的方向）
 *   · 目录组内一律名字升序：目录的 mtime 会随内容增删跳动，按它排会来回跳；目录是"路标"，
 *     路标保持稳定的字母序。
 *   · 同键同值 → 名字升序兜底，保证稳定不抖动。
 */

export type TreeSortMode = 'name' | 'time' | 'size'

export const TREE_SORT_CYCLE: readonly TreeSortMode[] = ['name', 'time', 'size']

export function nextTreeSort(mode: TreeSortMode): TreeSortMode {
  const i = TREE_SORT_CYCLE.indexOf(mode)
  return TREE_SORT_CYCLE[(i + 1) % TREE_SORT_CYCLE.length]
}

export interface TreeSortEntry {
  name: string
  isDir: boolean
  size: number
  mtimeMs: number
}

/** 键值：time/size 用各自字段，name 用名字本身。都取"降序友好"的数值语义。 */
function keyValue(e: TreeSortEntry, mode: TreeSortMode): number {
  if (mode === 'time') return e.mtimeMs
  if (mode === 'size') return e.size
  return 0
}

/** 单层比较器：目录恒在前；组内按所选键降序（name 为升序）；平局名字升序。 */
export function compareTreeEntries(a: TreeSortEntry, b: TreeSortEntry, mode: TreeSortMode): number {
  if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
  if (a.isDir && b.isDir) return a.name.localeCompare(b.name)
  if (mode !== 'name') {
    const ka = keyValue(a, mode)
    const kb = keyValue(b, mode)
    if (ka !== kb) return kb - ka
  } else {
    const c = a.name.localeCompare(b.name)
    if (c !== 0) return c
  }
  return a.name.localeCompare(b.name)
}

/** 排一个已加载层级（不修改原数组——flatten 每次渲染对拷贝排序，排序切换即时生效）。 */
export function sortTreeLevel<T extends TreeSortEntry>(nodes: readonly T[], mode: TreeSortMode): T[] {
  return [...nodes].sort((a, b) => compareTreeEntries(a, b, mode))
}

/** 带投影的版本：调用方的节点形状自带 entry（如 FilesPanel 的 TreeNode），投影出排序键。 */
export function sortTreeBy<T>(nodes: readonly T[], mode: TreeSortMode, keyOf: (n: T) => TreeSortEntry): T[] {
  return [...nodes].sort((a, b) => compareTreeEntries(keyOf(a), keyOf(b), mode))
}
