/**
 * midTruncate — 长文件名"中段省略，保头保尾"的**纯切分规则**（2026-09-11，REQ-fp-midtrunc）。
 *
 * 为什么它必须存在：版本差异长在文件名**尾部**（…-v8.xlsx vs …-v9.xlsx），CSS 单行
 * `text-overflow: ellipsis` 是尾部省略，正好把差异吃掉——两行看起来一模一样（用户原话：
 * "看不到版本差异，也难以区分"）。CSS 做不到中段省略，`direction:rtl` 技巧会丢头。
 *
 * 规则（Human 拍定"中段省略，保头保尾"，VS Code 标签页同款）：
 *   tail = 名字**末尾**保住的一段（版本号/扩展名天然落在这里），head = 其余部分由 CSS 撑出
 *   "head…tail" 形态。宽度自适应交给 CSS（head flex-shrink 收缩 + tail 不收缩），这里只定
 *   tail 的**预算**：按显示宽度计（CJK/全角 = 2 个单位，其余 = 1），默认 20 单位 ≈ 最窄
 *   抽屉行也放得下，超出预算的 tail 从头截（保住的是"末段"，截的是它的更早部分）。
 *
 * 按字素切（Intl.Segmenter，退化到码点），emoji/ZWJ 序列不当半个字砍。
 */

/** 单个字素的显示宽度单位：CJK/全角按 2，其余按 1。emoji 序列整体按 2。 */
function unitWidth(grapheme: string): number {
  let width = 0
  for (const ch of grapheme) {
    width += ch.codePointAt(0)! > 0xff ? 2 : 1
  }
  return Math.max(width, 1)
}

/** 字素切分：优先 Intl.Segmenter（emoji/ZWJ 不劈开），环境没有就退化到码点。 */
function graphemes(name: string): string[] {
  const Seg = (typeof Intl !== 'undefined' ? (Intl as { Segmenter?: new(o: object) => { segment(s: string): Iterable<{ segment: string }> } }).Segmenter : undefined)
  if (Seg) {
    const out: string[] = []
    for (const part of new Seg({ granularity: 'grapheme' }).segment(name)) out.push(part.segment)
    return out
  }
  return Array.from(name)
}

/**
 * 切成 {head, tail}。名字整体不超过预算时 head 为空串（**不**人为加省略——空间够就显示全名，
 * 与 A3-2 对应）；超预算时 head 为前段、tail 为末段（head 溢出时由 CSS 显示成 "head…tail"）。
 *
 * 扩展名锚定：预算再小，最后一个 `.` 起的扩展名（含点）整体留在 tail——"…xlsx" 里丢掉点
 * 是可读性事故（A3-3"退化形态扩展名完整"的本意）。扩展名优先于预算，tail 因此可以略超。
 */
export function splitMidTruncate(name: string, tailUnits = 20): { head: string; tail: string } {
  const segs = graphemes(name)
  let acc = 0
  let i = segs.length
  while (i > 0) {
    const w = unitWidth(segs[i - 1])
    if (acc + w > tailUnits) break
    acc += w
    i--
  }
  // 扩展名锚定：tail 若把最后一个 '.' 切走了，就把 tail 起点退到那个 '.'（点必须是扩展名的）。
  for (let d = segs.length - 1; d > 0; d--) {
    if (segs[d] === '.') {
      if (i > d) i = d
      break
    }
  }
  return { head: segs.slice(0, i).join(''), tail: segs.slice(i).join('') }
}
