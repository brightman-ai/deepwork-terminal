import { describe, expect, it } from 'bun:test'
import { terminalFileLinksAtRow, terminalFileLinkAtCell } from '../terminalFileLinks'
import { cliHardWrap, transcriptCellBuffer } from './fixtures/transcriptPathLayouts'

const counted='/work/other-project/skills/example-tool/design/普通命令输出-中文路径报告.md'
function inspect(rows:string[],columns=0){
 const buffer=transcriptCellBuffer(rows,columns)
 const links=Array.from({length:buffer.length},(_,row)=>terminalFileLinksAtRow(buffer,row)).flat()
 return {buffer,links,paths:[...new Set(links.map(link=>link.path))]}
}
describe('shell output owns its hanging path fields',()=>{
 it('retains numeric-output prefixes and three hard path rows without exposing filename tails',()=>{
  const rows=['  ⎿ \u00a015398 /work/other-project/skills/examp',
   '     le-tool/design/普通命令输出-中文路径',
   '     报告.md',
   '     5 /work/demo/results/first.json',
   '     8 /work/demo/results/second.json']
  for(const columns of [0,24,32,40,54,80,120]){
   const {buffer,links,paths}=inspect(rows,columns)
   expect(paths).toEqual([counted,'/work/demo/results/first.json','/work/demo/results/second.json'])
   for(const link of links)for(let col=link.cellStart-1;col<link.cellEnd;col++)expect(terminalFileLinkAtCell(buffer,link.bufferRow,col)?.path).toBe(link.path)
  }
 })
 it('uses the output gutter on later records and preserves quoted literal spaces',()=>{
  for(const marker of ['  ⎿ \u00a0','  └ ','└ '])for(const width of [24,32,40,54,80]){
   const rows=[marker+'output',...cliHardWrap(' '.repeat(marker.length)+'15398 '+counted,width,marker.length),
    ...cliHardWrap(' '.repeat(marker.length)+'"/work/demo/my  report.md"',width,marker.length)]
   expect(inspect(rows,32).paths).toEqual([counted,'/work/demo/my  report.md'])
  }
 })
 it('does not carry an output gutter across assistant paragraphs or join a new numbered record',()=>{
  expect(inspect(['  ⎿  output','','  /work/demo/partial-','     report.md']).paths).not.toContain('/work/demo/partial-report.md')
  expect(inspect(['  ⎿  12 /work/demo/partial-','     20 /work/demo/other.md']).paths).not.toContain('/work/demo/partial-20')
  expect(inspect(['  ⎿  12 /work/demo/complete.md','     20 /work/demo/other.md']).paths).toEqual(['/work/demo/complete.md','/work/demo/other.md'])
 })
})

describe('Git short-status references retain their command directory',()=>{
 const encoded='docs/\\344\\270\\255\\346\\226\\207 \\346\\212\\245\\345\\221\\212.md'
 const path='docs/中文 报告.md'
 it('opens the command repository file from every decoded, wrapped physical fragment',()=>{
  for(const width of [24,32,40,54,80]){
   const rows=[...cliHardWrap('● Bash(cd /work/current && git -C /work/other status --short docs | head)',width,6),'  ⎿ \u00a0ok',...cliHardWrap('     ?? "'+encoded+'"',width,5)]
   const {buffer,links,paths}=inspect(rows,32)
   expect(paths.filter(p=>p.endsWith('中文 报告.md'))).toEqual(['/work/other/docs/中文 报告.md'])
   const status=links.filter(link=>link.path.endsWith('中文 报告.md'))
   expect(status.length).toBeGreaterThan(0)
   for(const link of status)for(let col=link.cellStart-1;col<link.cellEnd;col++)expect(terminalFileLinkAtCell(buffer,link.bufferRow,col)?.path).toBe('/work/other/'+path)
  }
 })
 it('retains the original namespace when the root is missing, ambiguous or quoted as an example',()=>{
  for(const command of ['git status --short','echo "git -C /work/wrong status --short"','git -C /work/one status --short; git -C /work/two status --short']){
   const rows=['● Bash('+command+')','  ⎿ \u00a0ok','     ?? "'+encoded+'"']
   expect(inspect(rows,32).paths.filter(p=>p.endsWith('中文 报告.md'))).toEqual([path])
  }
 })
 it('does not apply Git namespace to other output or cross a new assistant message',()=>{
  const header=['● Bash(git -C /work/other status --short)','  ⎿ \u00a0ok']
  expect(inspect([...header,'     "'+encoded+'"']).paths.filter(p=>p.endsWith('中文 报告.md'))).toEqual([path])
  expect(inspect([...header,'● 新消息','     ?? "'+encoded+'"']).paths.filter(p=>p.endsWith('中文 报告.md'))).toEqual([path])
 })
})
