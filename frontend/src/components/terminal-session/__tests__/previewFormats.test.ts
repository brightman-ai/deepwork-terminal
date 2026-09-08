import { describe, it, expect } from 'bun:test'
import { readFileSync } from 'node:fs'
import { formatFor, catOf, extOf, isImageName, CAT_LABEL, CAT_ORDER } from '../previewFormats'
import { parseCsv, sniffDelimiter } from '../csvTable'

/**
 * 这两个模块是"统一识别"的承重件。它们坏掉的方式都不报错：
 * 角标说是文档、点开说打不开；表头带着看不见的 BOM；宽表少一列。所以断言写在这里。
 */

describe('格式表是唯一真相（REQ-fmt-ssot）', () => {
  it('每个声明了类别的扩展名都真的有渲染器 —— 不存在"角标承诺、预览打脸"', () => {
    // 这正是本轮要修的那个 bug：CAT_EXT.doc 里写着 ppt，预览分支却只认 pptx，
    // 于是一个 .ppt 顶着「文档」角标告诉你"二进制文件，无法预览"。
    for (const ext of ['pptx', 'ppt', 'docx', 'doc', 'pdf', 'xlsx', 'xls', 'csv', 'tsv', 'm4a', 'zip', 'xmind']) {
      const spec = formatFor(`x.${ext}`)
      expect(spec.kind).not.toBe('none')
      expect(spec.cat).not.toBe('other')
    }
  })

  it('老二进制按族归一到同族新格式的渲染器（而不是各写一种）', () => {
    expect(formatFor('a.doc').kind).toBe(formatFor('a.docx').kind)   // 都走 DocxPreview
    expect(formatFor('a.xls').kind).toBe(formatFor('a.xlsx').kind)   // 都走 SheetPreview
    expect(formatFor('a.ppt').kind).toBe(formatFor('a.pptx').kind)   // 都走 PdfPreview
  })

  it('要服务端转换的格式，取数方式必须是 convert:*（否则会去拿原始字节喂给渲染器）', () => {
    expect(formatFor('a.pptx').fetch).toBe('convert:pdf')
    expect(formatFor('a.ppt').fetch).toBe('convert:pdf')
    expect(formatFor('a.xlsx').fetch).toBe('convert:html')
    expect(formatFor('a.xls').fetch).toBe('convert:html')
    expect(formatFor('a.doc').fetch).toBe('convert:docx')
    // docx/pdf 是客户端直读，不该被误接到转换上
    expect(formatFor('a.docx').fetch).toBe('bytes')
    expect(formatFor('a.pdf').fetch).toBe('bytes')
  })

  it('前后端两张表必须一致：后端 convertTargetFor 认的，前端也得认', () => {
    // 前端说"转 html"、后端只认 pdf，症状是"请求了一个后端拒绝的转换"，且只在运行时暴露。
    const goSrc = readFileSync(new URL('../../../../../convert_office.go', import.meta.url), 'utf8')
    const table = goSrc.slice(goSrc.indexOf('func convertTargetFor'), goSrc.indexOf('// convertContentType'))
    for (const [ext, target] of [['.pptx', 'pdf'], ['.ppt', 'pdf'], ['.xlsx', 'html'], ['.xls', 'html'], ['.doc', 'docx']] as const) {
      expect(table).toContain(`"${ext}"`)
      expect(formatFor(`a${ext}`).fetch).toBe(`convert:${target}`)
    }
  })

  it('未知扩展名回落文本分支（后端按内容嗅探）—— "没列进表"不等于"打不开"', () => {
    const spec = formatFor('nginx.conf.bak')
    expect(spec.kind).toBe('text')
    expect(spec.fetch).toBe('text')
  })

  it('筛选 chip 的每个类别都有文案，顺序表里也在', () => {
    for (const ext of ['md', 'xlsx', 'go', 'json', 'css', 'png', 'm4a', 'zip']) {
      const cat = catOf(`x.${ext}`)
      expect(CAT_LABEL[cat]).toBeTruthy()
      expect(CAT_ORDER).toContain(cat)
    }
  })

  it('extOf / isImageName 的边界', () => {
    expect(extOf('a.tar.gz')).toBe('gz')
    expect(extOf('Makefile')).toBe('')      // 无扩展名不能把整个文件名当扩展名
    expect(extOf('.gitignore')).toBe('')    // 点开头的隐藏文件同理
    expect(isImageName('a.PNG')).toBe(true) // 大小写不敏感
    expect(isImageName('a.svg')).toBe(false) // svg 是 XML，按文本预览（与后端一致）
  })
})

describe('csv 解析（规则全部来自真实文件）', () => {
  it('剥 BOM —— 留着它第一个表头就变成 "\\uFEFF字段名"，按列名匹配的一切静默失配', () => {
    const { sheets } = parseCsv('﻿字段名,类型\nsrc_ip,string\n')
    expect(sheets[0].rows[0]).toEqual(['字段名', '类型'])
  })

  it('CRLF 不留 \\r 在最后一个单元格里', () => {
    const { sheets } = parseCsv('a,b\r\n1,2\r\n')
    expect(sheets[0].rows[1]).toEqual(['1', '2'])
  })

  it('引号内的分隔符和换行不断列', () => {
    const { sheets } = parseCsv('a,b\n"含,逗号","含\n换行"\n')
    expect(sheets[0].rows[1]).toEqual(['含,逗号', '含\n换行'])
  })

  it('"" 是一个字面量引号', () => {
    const { sheets } = parseCsv('a\n"他说""好"""\n')
    expect(sheets[0].rows[1][0]).toBe('他说"好"')
  })

  it('### SHEET: 标记切成多张表 —— 信息本来就在文件里，别把它当数据行', () => {
    // 用户工具链的真实产物：`### SHEET: 攻防规则建模 (147 rows x 8 cols)`
    const { sheets } = parseCsv('### SHEET: 攻防规则建模 (147 rows x 8 cols)\na,b\n1,2\n### SHEET: 第二张\nc\n3\n')
    expect(sheets.map((s) => s.name)).toEqual(['攻防规则建模', '第二张'])
    expect(sheets[0].rows).toEqual([['a', 'b'], ['1', '2']])
    expect(sheets[1].rows).toEqual([['c'], ['3']])
  })

  it('tsv 用制表符；含逗号的引用字段不能把 tsv 猜成 csv', () => {
    expect(sniffDelimiter('a\tb\tc', 'tsv')).toBe('\t')
    expect(sniffDelimiter('"x,y,z"\tb')).toBe('\t')
  })

  it('超过上限截断并如实告知（不装作全渲染了）', () => {
    const big = Array.from({ length: 50 }, (_, i) => `${i},x`).join('\n')
    const { sheets, truncated } = parseCsv(big, { maxRows: 10 })
    expect(sheets[0].rows.length).toBe(10)
    expect(truncated).toBe(true)
  })
})
