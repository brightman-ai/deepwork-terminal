import { describe, expect, it } from 'bun:test'
import { findTerminalFileLinks, terminalFileLinksAtRow, terminalFileLinkAtCell } from '../terminalFileLinks'
import { cliHardWrap, transcriptCellBuffer } from './fixtures/transcriptPathLayouts'

const reference='../claude_remote/skills/whiteboard-t3/design/同题打样-三道题对比Codex-20261030.md'
const collect=(rows:string[],columns=0)=>{
 const buffer=transcriptCellBuffer(rows,columns)
 return [...new Set(Array.from({length:buffer.length},(_,r)=>terminalFileLinksAtRow(buffer,r)).flat().map(l=>l.path))]
}
describe('Claude compacted Referenced file records',()=>{
 it('recovers the reported mid-directory break at every physical fragment and click cell',()=>{
  const rows=['  └ Referenced file ../claude_remote/skills/whiteboard-t','    3/design/同题打样-三道题对比Codex-20261030.md']
  for(const columns of [32,40,56,100]) {
   const buffer=transcriptCellBuffer(rows,columns)
   const links=Array.from({length:buffer.length},(_,r)=>terminalFileLinksAtRow(buffer,r)).flat()
   expect([...new Set(links.map(l=>l.path))]).toEqual([reference])
   for(const link of links)for(let col=link.cellStart-1;col<link.cellEnd;col++)expect(terminalFileLinkAtCell(buffer,link.bufferRow,col)?.path).toBe(reference)
  }
 })
 it('handles parent traversals, slash/extension splits, long Chinese names and tree gutters',()=>{
  const paths=[reference,'../../../../../../tmp/claude-1001/-home-ubuntu-code-automation-design/session/scratchpad/检视-A2-S1业务基线与UEBA评审.md']
  for(const prefix of ['└ ','  └ ','  ├ ','  │ '])for(const width of [28,36,40,54,64,78,100])for(const path of paths) {
   expect(collect(cliHardWrap(prefix+'Referenced file '+path,width,prefix.length),40)).toEqual([path])
  }
 })
 it('keeps spaces and parentheses inside the owned file field',()=>{
  for(const path of ['docs/my  report(final).md','docs/中文  目录/报告（终稿）.md']) {
   expect(collect(cliHardWrap('  └ Referenced file '+path,36,4))).toEqual([path])
   expect(collect(cliHardWrap('  └ Referenced file "'+path+'"',36,4))).toEqual([path])
  }
 })
 it('does not join adjacent items, completed files, or a following independent short root',()=>{
  expect(collect(['  └ Referenced file docs/a.md','  └ Referenced file docs/b.md'])).toEqual(['docs/a.md','docs/b.md'])
  expect(collect(['  └ Referenced file docs/a.md','    docs/b.md'])).toEqual(['docs/a.md','docs/b.md'])
  expect(collect(['  └ Referenced file a/','    /other/b.md'])).not.toContain('a//other/b.md')
 })
 it('does not expose local-looking tails of opaque URLs or templates',()=>{
  for(const path of ['https://example.com/very/long/path/报告.md','${ROOT}/very/long/path/报告.md'])expect(collect(cliHardWrap('  └ Referenced file '+path,36,4))).toEqual([])
 })
})

describe('file-tool source ranges stay outside the filename',()=>{
 for(const space of ['', ' ', '\u00a0', '\u2009','\u202f'])it(`separates middle-dot lines annotation with ${JSON.stringify(space)} spacing`,()=>{
  const raw=`● Read(missing/研判链范式.md ·${space}lines 240-539)`
  expect(findTerminalFileLinks(raw).map(l=>[l.path,l.lineStart,l.lineEnd])).toEqual([['missing/研判链范式.md',240,539]])
  const buffer=transcriptCellBuffer(cliHardWrap(raw,32,6),40)
  const links=Array.from({length:buffer.length},(_,r)=>terminalFileLinksAtRow(buffer,r)).flat()
  expect(links.length).toBeGreaterThan(0)
  expect(links.every(l=>l.path==='missing/研判链范式.md' && l.lineStart===240 && l.lineEnd===539)).toBe(true)
 })
 it('retains literal annotation-like characters in quoted filenames',()=>{
  expect(findTerminalFileLinks('Read("docs/研判链范式.md ·lines 240-539.txt")').map(l=>l.path)).toEqual(['docs/研判链范式.md ·lines 240-539.txt'])
  expect(findTerminalFileLinks('Read("docs/a ·lines 2-4.md" ·lines 8-12)').map(l=>[l.path,l.lineStart,l.lineEnd])).toEqual([['docs/a ·lines 2-4.md',8,12]])
 })
})
