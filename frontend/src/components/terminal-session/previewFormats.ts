/**
 * previewFormats — 文件面板认识哪些格式、每种怎么取、怎么渲染。**一张表，一个真相。**
 *
 * ── 为什么它必须存在 ──────────────────────────────────────────────────────────────────────────
 * 在这之前有两张表：`CAT_EXT`（决定搜索行的类别角标和筛选 chip）和预览分支里的
 * `if (ext === 'docx' || ext === 'pdf' || ext === 'pptx')`（决定点开会怎样）。两张表必然漂移，
 * 而且已经漂了 —— `CAT_EXT.doc` 里写着 `ppt`，于是一个 .ppt 顶着"文档"角标，点开却说
 * "二进制文件，无法预览"。角标是承诺，预览是兑现，让它们出自同一处才不会互相打脸。
 *
 * 所以这里同时定义三件事，缺一都会让某个入口重新长出自己的判断：
 *   cat    — 类别（角标 / 筛选 chip / 图标着色）
 *   kind   — 用哪个渲染器
 *   fetch  — 怎么把字节弄来（各渲染器要的形状不同：文本 / URL / ArrayBuffer / JSON）
 *
 * 后端有一份对应的 SSOT：`convert_office.go` 的 `convertTargetFor`（哪些格式走 libreoffice、
 * 转成什么）与 `archive_preview.go` 的 `isZipFamilyExt`/`audioContentType`。两边必须一致 ——
 * 这里改了那边不改，症状是"前端请求了一个后端拒绝的转换"。
 */

/** 渲染器。'none' = 我们不预览它（给下载出路，并说清楚为什么）。 */
export type PreviewKind =
  | 'markdown' | 'text' | 'image' | 'html'
  | 'docx' | 'pdf' | 'sheet' | 'csv'
  | 'audio' | 'zip' | 'xmind'
  | 'none'

/**
 * 取数方式。渲染器要的形状不同，取错了就是"渲染器拿到一坨它不认识的东西"：
 *   text        — GET /files/raw 的文本分支（既有）
 *   imageUrl    — 直接给 <img> 一个 URL（既有）
 *   bytes       — ArrayBuffer（docx-preview / pdfjs 要的）
 *   convert:*   — 服务端 libreoffice 产物；冒号后是目标格式，与后端 convertTargetFor 对齐
 *   zipList     — GET …&zip=1 的条目清单 JSON
 *   mediaUrl    — 直接给 <audio> 一个可 Range 的 URL
 */
export type FetchMode = 'text' | 'imageUrl' | 'bytes' | 'convert:pdf' | 'convert:html' | 'convert:docx' | 'zipList' | 'mediaUrl'

export interface FormatSpec {
  cat: string
  kind: PreviewKind
  fetch: FetchMode
}

/** 类别标签（筛选 chip 的文案）。'other' 是兜底。 */
export const CAT_LABEL: Record<string, string> = {
  all: '全部', doc: '文档', sheet: '表格', code: '代码', config: '配置',
  style: '样式', image: '图片', media: '媒体', archive: '归档', other: '其他',
}

/** 类别配色：图标 + 扩展名角标同源，行不可能声称一个 chip 不认的类别。 */
export const CAT_TINT: Record<string, string> = {
  doc: 'text-sky-500 bg-sky-500/10',
  sheet: 'text-teal-500 bg-teal-500/10',
  code: 'text-emerald-500 bg-emerald-500/10',
  config: 'text-amber-500 bg-amber-500/10',
  style: 'text-pink-500 bg-pink-500/10',
  image: 'text-violet-500 bg-violet-500/10',
  media: 'text-orange-500 bg-orange-500/10',
  archive: 'text-rose-500 bg-rose-500/10',
  other: 'text-muted-foreground bg-muted',
}

/** 文本类共用的一条 spec，省得每个扩展名重复写。 */
const TXT = (cat: string): FormatSpec => ({ cat, kind: 'text', fetch: 'text' })

/**
 * 扩展名 → spec。**新增格式只改这里**，角标/筛选/路由/取数一起跟着走。
 *
 * 关于"老二进制按族归一"：.doc→docx、.xls→html、.ppt→pdf 都在服务端转好再交给**同族新格式
 * 已有的渲染器**，所以一个 .doc 和一个 .docx 长得一模一样。这比"给老格式另写一种渲染"省，
 * 也比"老格式一律转 pdf"诚实 —— 转 pdf 的那份文字是选不中的。
 */
const FORMATS: Record<string, FormatSpec> = {
  // 文档
  md: { cat: 'doc', kind: 'markdown', fetch: 'text' },
  markdown: { cat: 'doc', kind: 'markdown', fetch: 'text' },
  mdx: { cat: 'doc', kind: 'markdown', fetch: 'text' },
  txt: TXT('doc'), rst: TXT('doc'), adoc: TXT('doc'), org: TXT('doc'),
  pdf: { cat: 'doc', kind: 'pdf', fetch: 'bytes' },
  docx: { cat: 'doc', kind: 'docx', fetch: 'bytes' },
  doc: { cat: 'doc', kind: 'docx', fetch: 'convert:docx' },
  pptx: { cat: 'doc', kind: 'pdf', fetch: 'convert:pdf' },
  ppt: { cat: 'doc', kind: 'pdf', fetch: 'convert:pdf' },

  // 表格（自成一类：它们的阅读方式和"文档"是两回事）
  xlsx: { cat: 'sheet', kind: 'sheet', fetch: 'convert:html' },
  xls: { cat: 'sheet', kind: 'sheet', fetch: 'convert:html' },
  csv: { cat: 'sheet', kind: 'csv', fetch: 'text' },
  tsv: { cat: 'sheet', kind: 'csv', fetch: 'text' },

  // 代码
  ...Object.fromEntries(['go', 'ts', 'tsx', 'js', 'jsx', 'mjs', 'cjs', 'vue', 'py', 'rs', 'rb', 'java',
    'kt', 'swift', 'c', 'h', 'cpp', 'cc', 'hpp', 'cs', 'php', 'sh', 'bash', 'zsh', 'lua', 'sql', 'proto',
  ].map((e) => [e, TXT('code')])),

  // 配置
  ...Object.fromEntries(['json', 'yaml', 'yml', 'toml', 'ini', 'conf', 'env', 'xml', 'lock', 'dockerfile',
  ].map((e) => [e, TXT('config')])),

  // 样式 / 网页
  ...Object.fromEntries(['css', 'scss', 'less', 'svg'].map((e) => [e, TXT('style')])),
  html: { cat: 'style', kind: 'html', fetch: 'text' },
  htm: { cat: 'style', kind: 'html', fetch: 'text' },

  // 图片
  ...Object.fromEntries(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico', 'avif',
  ].map((e) => [e, { cat: 'image', kind: 'image', fetch: 'imageUrl' } as FormatSpec])),

  // 音频（视频不在范围内：仓里没有，且播放器/编码是另一摊事）
  ...Object.fromEntries(['m4a', 'mp3', 'wav', 'ogg', 'oga', 'flac', 'opus', 'aac',
  ].map((e) => [e, { cat: 'media', kind: 'audio', fetch: 'mediaUrl' } as FormatSpec])),

  // 归档 / 思维导图（xmind 本身就是个 zip）
  zip: { cat: 'archive', kind: 'zip', fetch: 'zipList' },
  xmind: { cat: 'archive', kind: 'xmind', fetch: 'zipList' },
}

/** 小写扩展名（不带点）。没有扩展名时返回 ''。 */
export function extOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i > 0 ? name.slice(i + 1).toLowerCase() : ''
}

/**
 * 这个文件名对应的 spec。未知扩展名 → 走文本分支：**后端按内容嗅探**（前 8KiB 有没有 NUL），
 * 真是文本就显示，真是二进制才回 {binary} 哨兵。所以"没列进表里"不等于"打不开" ——
 * 一个 .conf.bak 照样能看。
 */
export function formatFor(name: string): FormatSpec {
  return FORMATS[extOf(name)] ?? { cat: 'other', kind: 'text', fetch: 'text' }
}

/** 类别（角标 / 筛选 chip / 图标着色的唯一来源）。 */
export function catOf(name: string): string {
  return formatFor(name).cat
}

/** 该文件是否按图片直出（<img> + 点击放大）。 */
export function isImageName(name: string): boolean {
  return formatFor(name).kind === 'image'
}

/** 筛选 chip 的顺序：先给"人产出的东西"，再给工程文件。 */
export const CAT_ORDER = ['all', 'doc', 'sheet', 'image', 'media', 'code', 'config', 'style', 'archive', 'other']
