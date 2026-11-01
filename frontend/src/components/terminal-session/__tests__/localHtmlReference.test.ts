// Synthetic width-preserving grammar fixtures; names and prose are invented.
import { describe, expect, it } from 'bun:test'
import { findTerminalFileLinks, findWrappedTerminalFileLinks, terminalFileLinksAtRow, terminalFileLinkAtCell } from '../terminalFileLinks'
import { splitLocalHtmlReference, appendHtmlPreviewSuffix, htmlPreviewRevision } from '../localHtmlReference'
import { resolveDocumentPath } from '../terminalPathResolver'
import { cliHardWrap, transcriptCellBuffer } from './fixtures/transcriptPathLayouts'

const path = '/home/tester/work/example-workspace/topics/EX-0001-abc-APP-X-DemoX文文文文与场文文文/文文/S1文文-文文case文对文文文文文/无文文文文文文UI/ui/prototype/report-chain.html'
const address = path + '?set=bank&all=1'
describe('local HTML addresses, from terminal through rendering', () => {
  it('recognizes the synthetic address without accepting a glob as a file', () => {
    expect(findTerminalFileLinks('  ' + address)).toEqual([{path: address, start: 2, end: address.length + 1}])
    for (const raw of ['docs/*.html?set=bank', 'docs/report.md?set=bank', '${ROOT}/report.html?set=bank', 'https://example.org/report.html?set=bank']) expect(findTerminalFileLinks(raw)).toEqual([])
  })
  it('keeps labels, surrounding prose and source annotations out of HTML navigation', () => {
    for (const text of [`文件：${address}`, `页文(${address})已保存。`, `[文行文文](${address})`, `\`${address}\``, `● Read(${address})`]) {
      expect(findTerminalFileLinks(text).map(l => l.path)).toEqual([address])
    }
    expect(findTerminalFileLinks('docs/report.html#L20-L40')[0]).toMatchObject({path:'docs/report.html',lineStart:20,lineEnd:40})
    expect(findTerminalFileLinks('docs/report.html?return=part:42#L20')[0]?.path).toBe('docs/report.html?return=part:42#L20')
  })
  it('maps every soft-wrap fragment and click cell to the same full address', () => {
    for (let at=1;at<address.length;at++) {
      const rows=[{text:address.slice(0,at),isWrapped:false},{text:address.slice(at),isWrapped:true}]
      for(const row of [0,1])expect(findWrappedTerminalFileLinks(rows,row).map(l=>l.path)).toEqual([address])
    }
    for(const columns of [28,40,56,80,120,240]) {
      const buffer=transcriptCellBuffer(['  '+address],columns)
      const links=Array.from({length:buffer.length},(_,row)=>terminalFileLinksAtRow(buffer,row)).flat()
      expect([...new Set(links.map(l=>l.path))]).toEqual([address])
      for(const link of links)for(let col=link.cellStart-1;col<link.cellEnd;col++)expect(terminalFileLinkAtCell(buffer,link.bufferRow,col)?.path).toBe(address)
    }
  })
  it('keeps hard-wrap ownership and completes a filename even before its parameters', () => {
    for(const width of [28,40,56,80]) {
      for(const raw of ['● 页文('+address+')已保存。','● 文文结果：'+address+'。','● 文文结果：'+address]) {
        const buffer=transcriptCellBuffer(cliHardWrap(raw,width,2),40)
        const found=Array.from({length:buffer.length},(_,row)=>terminalFileLinksAtRow(buffer,row)).flat().map(l=>l.path)
        expect([...new Set(found)]).toEqual([address])
      }
    }
  })
  it('does not absorb an independent file after an HTML address', () => {
    const original = '● 页文：/work/report.html?set=bank&all=1'
    for (const next of ['docs/next.md', '/other/report.html', '另一个文件：docs/next.md']) {
      const buffer=transcriptCellBuffer([original, '  '+next], 80)
      const paths=[...new Set(Array.from({length:buffer.length},(_,row)=>terminalFileLinksAtRow(buffer,row)).flat().map(l=>l.path))]
      expect(paths).toContain('/work/report.html?set=bank&all=1')
      expect(paths.some(path=>path.includes('all=1docs/') || path.includes('all=1/other/'))).toBe(false)
    }
  })
  it('separates disk identity and browser query without decoding filename bytes', () => {
    expect(resolveDocumentPath('/work/docs/design.md', '../report.html?return=../../other#case101')).toBe('/work/report.html?return=../../other#case101')
    expect(splitLocalHtmlReference(address)).toEqual({path,suffix:'?set=bank&all=1'})
    expect(splitLocalHtmlReference('docs/report.htm#case101')).toEqual({path:'docs/report.htm',suffix:'#case101'})
    expect(splitLocalHtmlReference('docs/report.html#L20')).toEqual({path:'docs/report.html#L20',suffix:''})
    expect(splitLocalHtmlReference('docs/query?.txt')).toEqual({path:'docs/query?.txt',suffix:''})
  })
  it('preserves page parameters and anchors while keeping read/auth controls owned by the transport', () => {
    const src='/api/files/raw?session=s1&path=docs%2Freport.html&render=1&auth=secret'
    const result=new URL(appendHtmlPreviewSuffix(src,'?set=bank&all=1&tag=a&tag=b&path=other&download=1&auth=other#case101'),'http://localhost')
    expect(result.searchParams.get('path')).toBe('docs/report.html')
    expect(result.searchParams.get('auth')).toBe('secret')
    expect(result.searchParams.has('download')).toBe(false)
    expect(result.searchParams.get('set')).toBe('bank')
    expect(result.searchParams.getAll('tag')).toEqual(['a','b'])
    expect(result.hash).toBe('#case101')
    const revised=new URL(htmlPreviewRevision(result.pathname+result.search+result.hash,7),'http://localhost')
    expect(revised.searchParams.get('preview')).toBe('7')
    expect(revised.hash).toBe('#case101')
  })
})
