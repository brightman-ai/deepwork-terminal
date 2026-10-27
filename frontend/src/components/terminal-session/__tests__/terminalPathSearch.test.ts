import { describe, expect, it } from 'bun:test'
import type { SearchResult } from '@terminal/api/files'
import { searchTerminalPathCandidates, terminalPathFilename } from '../terminalPathSearch'

const entry = (rel: string) => ({ name: rel.split('/').at(-1)!, rel, isDir: false, size: 42, mtimeMs: 0 })
const ready = (...paths: string[]): SearchResult => ({ entries: paths.map(entry), truncated: false, indexState: 'ready' })
const signal = () => new AbortController().signal

describe('terminal path recovery', () => {
  it('recovers commands/whiteboard-t3.md by filename but requires confirmation', async () => {
    const queries: string[] = []
    const found = await searchTerminalPathCandidates('commands/whiteboard-t3.md', '/project', async query => {
      queries.push(query)
      return query === 'commands/whiteboard-t3.md' ? ready() : ready('relocated/whiteboard-t3.md')
    }, signal())
    expect(queries).toEqual(['commands/whiteboard-t3.md', 'whiteboard-t3.md'])
    expect(found.filenameFallback).toBe(true)
    expect(found.candidates.map(c => [c.absPath, c.exact])).toEqual([['/project/relocated/whiteboard-t3.md', false]])
  })

  it('keeps the original suffix proof when the full path exists', async () => {
    const queries: string[] = []
    const found = await searchTerminalPathCandidates('commands/whiteboard-t3.md', '/project', async query => {
      queries.push(query);return ready('nested/commands/whiteboard-t3.md')
    }, signal())
    expect(queries).toEqual(['commands/whiteboard-t3.md'])
    expect(found.filenameFallback).toBe(false)
    expect(found.candidates[0].exact).toBe(true)
  })

  it('retains same-name ambiguity and never upgrades a different parent to exact', async () => {
    const found = await searchTerminalPathCandidates('old/共享.md', '/project', async query =>
      query.includes('/') ? ready() : ready('a/共享.md','b/共享.md','b/共享.md.bak'), signal())
    expect(found.candidates.map(c => [c.rel,c.exact])).toEqual([['a/共享.md',false],['b/共享.md',false]])
  })

  it('keeps errors distinct from missing paths and exposes the filename for manual recovery', async () => {
    const found = await searchTerminalPathCandidates('wrong/说明.md', '/project', async () => ({...ready(),error:'HTTP 500'}), signal())
    expect(found.filenameFallback).toBe(false)
    expect(found.result.error).toBe('HTTP 500')
    expect(terminalPathFilename('wrong/说明.md')).toBe('说明.md')
  })

  it('surfaces errors from the filename stage and preserves the incomplete warning', async () => {
    const found = await searchTerminalPathCandidates('old/doc.md', '/project', async query => query.includes('/')
      ? {...ready(),incomplete:true,indexState:'building'} : {...ready(),error:'HTTP 503'}, signal())
    expect(found.filenameFallback).toBe(true)
    expect(found.result.error).toBe('HTTP 503')
    expect(found.result.incomplete).toBe(true)
  })

  it('does not loop on a missing basename or invent an outside search root', async () => {
    const queries: string[]=[]
    const found=await searchTerminalPathCandidates('missing.md','/project',async query=>{queries.push(query);return ready()},signal())
    expect(queries).toEqual(['missing.md'])
    expect(found.candidates).toEqual([])
    const absolute=await searchTerminalPathCandidates('/other/project/说明.md','/project',async query=>query.includes('/')?ready():ready('说明.md'),signal())
    expect(absolute.candidates[0].absPath).toBe('/project/说明.md')
    expect(absolute.candidates[0].exact).toBe(false)
  })

  it('cancellation between stages prevents a second search even if transport ignores abort', async () => {
    const control=new AbortController();const queries:string[]=[]
    await expect(searchTerminalPathCandidates('old/doc.md','/project',async query=>{
      queries.push(query);control.abort();return ready()
    },control.signal)).rejects.toBeDefined()
    expect(queries).toEqual(['old/doc.md'])
  })

  it('rejects a late filename response after cancellation', async () => {
    const control=new AbortController()
    await expect(searchTerminalPathCandidates('old/doc.md','/project',async query=>{
      if(!query.includes('/'))control.abort()
      return ready()
    },control.signal)).rejects.toBeDefined()
  })

  it('recovers explicit dot paths, Windows separators and elided parents without discarding the reference', async () => {
    for(const path of ['./old/中文.md','../old/中文.md','C:\\old\\中文.md','old/.../中文.md']) {
      const found=await searchTerminalPathCandidates(path,'/project',async query=>query==='中文.md'?ready('new/中文.md'):ready(),signal())
      expect(found.query).toBe('中文.md')
      expect(found.filenameFallback).toBe(true)
      expect(found.candidates[0].exact).toBe(false)
    }
    expect(terminalPathFilename('old/')).toBe('')
    expect(terminalPathFilename('../..')).toBe('')
  })
})
