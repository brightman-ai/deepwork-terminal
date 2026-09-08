/**
 * csvTable — csv/tsv 文本 → 表格行。纯函数，因为这里每一条规则都是被真实文件教出来的。
 *
 * ── 为什么不走服务端转换 ──────────────────────────────────────────────────────────────────────
 * xlsx 走 libreoffice（首次 1–3s）是值得的：那是个压缩包，客户端解不动。csv 已经是文本，
 * 为它付一次进程启动只为了得到一张表，是拿延迟换零收益。
 *
 * ── 真实文件教的三件事（`~/code/automation` 抽样实测）────────────────────────────────────────
 * 1. **BOM**：文件以 EF BB BF 开头。不剥掉，第一个表头会变成 `﻿字段名`，看起来只是"有点怪"，
 *    但按列名匹配的一切都会静默失配。
 * 2. **CRLF**：行尾是 \r\n。不处理，每行最后一个单元格都拖着一个 \r。
 * 3. **`### SHEET:` 分节**：用户的工具链把多张表塞进一个 csv，用
 *    `### SHEET: 攻防规则建模 (147 rows x 8 cols)` 分隔。把它当普通数据行渲染不算错，
 *    但既然那一行明说了"下面是另一张表"，就该真的分成另一张表 —— 信息本来就在文件里。
 *
 * 引号规则按 RFC 4180：字段可被 " 包裹，包裹内的 "" 是一个字面量 "，包裹内可以有分隔符和换行。
 */

export interface CsvSheet {
  /** 分节名；没有 `### SHEET:` 标记时为 ''（单表文件）。 */
  name: string
  rows: string[][]
}

/** `### SHEET: 名字 (147 rows x 8 cols)` —— 取名字，丢掉括号里的统计。 */
const SHEET_MARK = /^###\s*SHEET:\s*(.*?)\s*(?:\(\s*\d+\s*rows?\s*x\s*\d+\s*cols?\s*\))?\s*$/i

/**
 * 分隔符：显式给（tsv）或按首行猜。猜法只在**引号外**数分隔符，否则一个含逗号的引用字段
 * 能把 tsv 判成 csv。
 */
export function sniffDelimiter(text: string, ext?: string): string {
  if (ext === 'tsv') return '\t'
  const firstLine = text.slice(0, text.indexOf('\n') < 0 ? text.length : text.indexOf('\n'))
  let inQ = false, comma = 0, tab = 0, semi = 0
  for (const ch of firstLine) {
    if (ch === '"') inQ = !inQ
    else if (inQ) continue
    else if (ch === ',') comma++
    else if (ch === '\t') tab++
    else if (ch === ';') semi++
  }
  if (tab > comma && tab > semi) return '\t'
  if (semi > comma) return ';' // 部分地区 Excel 导出的方言
  return ','
}

/**
 * 解析成一张或多张表。`maxRows` 是护栏而不是优化：一个百万行的 csv 直接铺进 DOM 会把页面
 * 变成砖头，截断并**如实告知**比装作全渲染了要好（返回值里 truncated 说明这件事）。
 */
export function parseCsv(text: string, opts?: { ext?: string; maxRows?: number }): { sheets: CsvSheet[]; truncated: boolean } {
  const maxRows = opts?.maxRows ?? 5000
  const body = text.charCodeAt(0) === 0xfeff ? text.slice(1) : text // BOM
  const delim = sniffDelimiter(body, opts?.ext)

  const sheets: CsvSheet[] = []
  let cur: CsvSheet = { name: '', rows: [] }
  let row: string[] = []
  let field = ''
  let inQuotes = false
  let total = 0
  let truncated = false
  // 一行读完时的收尾：先看它是不是分节标记，再决定是开新表还是当数据行。
  const endRow = (): void => {
    row.push(field); field = ''
    const isBlank = row.length === 1 && row[0] === ''
    const mark = row.length === 1 ? SHEET_MARK.exec(row[0]) : null
    if (mark) {
      if (cur.rows.length) sheets.push(cur)
      cur = { name: mark[1] || '', rows: [] }
    } else if (!isBlank) {
      if (total < maxRows) { cur.rows.push(row); total++ } else truncated = true
    }
    row = []
  }

  for (let i = 0; i < body.length; i++) {
    const ch = body[i]
    if (inQuotes) {
      if (ch === '"') {
        if (body[i + 1] === '"') { field += '"'; i++ } // "" → 字面量引号
        else inQuotes = false
      } else field += ch
      continue
    }
    if (ch === '"' && field === '') { inQuotes = true; continue }
    if (ch === delim) { row.push(field); field = ''; continue }
    if (ch === '\r') continue // CRLF：\r 由后面的 \n 收尾，单独的 \r 也不该进单元格
    if (ch === '\n') { endRow(); continue }
    field += ch
  }
  if (field !== '' || row.length) endRow()
  if (cur.rows.length || !sheets.length) sheets.push(cur)
  return { sheets, truncated }
}
