import { describe, it, expect } from 'bun:test'
import { searchPathContexts } from '../searchPathContext'

describe('search path identity', () => {
  it('retains differing middle segments when heads and names are identical', () => {
    const paths = ['docs/meetings/身份链讨论/work/final-v6', 'docs/meetings/架构讨论/work/final-v6']
    expect([...searchPathContexts(paths).values()]).toEqual(['身份链讨论', '架构讨论'])
  })
  it('keeps both topic and stage/direction distinctions among deeply copied results', () => {
    const paths = [
      'docs/meetings/身份链/work/final-v6',
      'docs/meetings/身份链/work/final-v6/traces/digest/artifacts/inputs/home/work/final-v6',
      'docs/meetings/身份链/work/final-v6/traces/digest/artifacts/outputs/home/work/final-v6',
      'docs/meetings/身份链/work/final-v6/traces/render/artifacts/outputs/home/work/final-v6',
      'docs/meetings/架构/work/final-v6',
    ]
    expect([...searchPathContexts(paths).values()]).toEqual([
      '身份链', '身份链 / … / final-v6 / … / digest / … / inputs',
      '身份链 / … / final-v6 / … / digest / … / outputs',
      '身份链 / … / final-v6 / … / render', '架构',
    ])
  })
  it('does not give ancestor and repeated-child paths the same visible identity', () => {
    const paths = ['docs/work/final-v6', 'docs/work/work/final-v6']
    const contexts = searchPathContexts(paths)
    expect(contexts.get(paths[0])).not.toBe(contexts.get(paths[1]))
  })

  it('supports single results, root results, ancestor parents and unicode segments', () => {
    expect(searchPathContexts(['deep/说明/final']).get('deep/说明/final')).toBe('说明')
    expect(searchPathContexts(['final']).get('final')).toBe('')
    const paths = ['a/b/final', 'a/b/c/final']
    expect(new Set(searchPathContexts(paths).values()).size).toBe(2)
  })
})
