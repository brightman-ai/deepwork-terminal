import { describe, expect, it } from 'bun:test'
import type { WorkbenchTab } from '../../../types/workbench'
import { aggregatePaneStatus, presentTabPanes, type PaneSessionFact } from '../tabPanePresentation'

const tab: WorkbenchTab = {id: 'tab', groupId: 'g', name: '工作', cwd: '/work', engine: 'shell', sessionId: 'left', split: {orientation: 'row', ratio: .5, secondSessionId: 'right'}}
const facts = (...rows: [string, PaneSessionFact][]) => new Map(rows)

describe('pane status presentation', () => {
  it('keeps spatial identity through mixed-state transitions and an orientation change', () => {
    const source = facts(['left', {status: 'running'}], ['right', {status: 'waiting'}])
    expect(presentTabPanes(tab, source).map(p => [p.key, p.label, p.status])).toEqual([['first', '左', 'running'], ['second', '右', 'waiting']])
    source.set('left', {status: 'waiting'}); source.set('right', {status: 'running'})
    const panes = presentTabPanes({...tab, split: {...tab.split!, orientation: 'column'}}, source)
    expect(panes.map(p => [p.sessionId, p.label, p.status])).toEqual([['left', '上', 'waiting'], ['right', '下', 'running']])
    expect(aggregatePaneStatus(panes)).toBe('waiting')
  })
  it('never copies the primary state into a pending or late secondary', () => {
    const source = facts(['left', {status: 'running'}])
    for (const secondSessionId of [undefined, 'right']) {
      expect(presentTabPanes({...tab, split: {...tab.split!, secondSessionId}}, source).map(p => p.status)).toEqual(['running', 'unknown'])
    }
    source.set('right', {status: 'done-unseen'})
    expect(presentTabPanes(tab, source).map(p => p.status)).toEqual(['running', 'done-unseen'])
  })
  it('separates one exited process from its live partner and peer failure', () => {
    const source = facts(['left', {status: 'running'}], ['right', {status: 'running', exited: true}])
    expect(presentTabPanes(tab, source).map(p => p.status)).toEqual(['running', 'disconnected'])
    source.set('right', {status: 'running'})
    expect(presentTabPanes(tab, source, 'detached').map(p => p.status)).toEqual(['disconnected', 'running'])
    expect(aggregatePaneStatus(presentTabPanes(tab, source, 'detached'))).toBe('running')
    expect(presentTabPanes(tab, source, 'unreachable').map(p => p.status)).toEqual(['disconnected', 'disconnected'])
  })
  it('retains one slot for an unsplit tab and two slots even when both are idle', () => {
    const source = facts(['left', {status: 'idle'}], ['right', {status: 'idle'}])
    expect(presentTabPanes({...tab, split: undefined}, source)).toEqual([{key: 'first', sessionId: 'left', label: '', status: 'idle'}])
    expect(presentTabPanes(tab, source).map(p => p.status)).toEqual(['idle', 'idle'])
    expect(aggregatePaneStatus(presentTabPanes(tab, new Map()))).toBe('unknown')
  })
})
