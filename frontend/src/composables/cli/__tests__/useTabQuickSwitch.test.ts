import { afterEach, beforeEach, describe, expect, it } from 'bun:test'
import { defineComponent, ref } from 'vue'
import { mountView } from '../../../portals/cli/__tests__/support/testRenderer'
import { useTabQuickSwitch, rankQuickSwitchItems, type QuickSwitchItem, type QuickSwitchStatus } from '../useTabQuickSwitch'
import type { WorkbenchGroup } from '../../../types/workbench'
import { decodeQuickSwitchHistory, encodeQuickSwitchHistory } from '../quickSwitchRanking'
import { aggregatePaneStatus, type TabPaneStatus } from '../tabPanePresentation'

const item = (id: string, name: string, cwd: string, position?: number): QuickSwitchItem => ({ id, name, cwd, position, group: '研究', machine: '本机', engine: 'shell', collapsed: false, status: 'idle' })
describe('tab quick switching', () => {
  it('filters any pane, counts tabs once, and freezes membership while pane badges remain live', () => {
    const panes = ref(new Map<string, TabPaneStatus[]>([
      ['a', [{key: 'first', label: '', status: 'unknown'}]],
      ['b', [{key: 'first', label: '左', status: 'running'}, {key: 'second', label: '右', status: 'waiting'}]],
      ['c', [{key: 'first', label: '左', status: 'running'}, {key: 'second', label: '右', status: 'running'}]],
      ['d', [{key: 'first', label: '上', status: 'done-unseen'}, {key: 'second', label: '下', status: 'idle'}]],
    ]))
    let model!: ReturnType<typeof useTabQuickSwitch>
    const view = mountView(defineComponent({setup() {
      model = useTabQuickSwitch({groups: () => [{id: 'g', name: '工作', collapsed: false, tabs: [...panes.value.keys()].map(id => ({id, groupId: 'g', name: id, cwd: '/work', engine: 'shell'}))}],
        activeId: () => 'a', positions: () => new Map(), machine: () => '本机',
        panes: id => panes.value.get(id)!, status: id => aggregatePaneStatus(panes.value.get(id)!),
        select: () => {}, expand: () => {}, storage: null})
      return () => null
    }}), {})
    try {
      model.show()
      expect(model.filters.value.map(option => [option.id, option.count])).toEqual([['all', 4], ['waiting', 1], ['done-unseen', 1], ['running', 2], ['idle', 1]])
      model.setFilter('running'); expect(model.results.value.map(item => item.id)).toEqual(['b', 'c'])
      panes.value.set('b', [{key: 'first', label: '左', status: 'waiting'}, {key: 'second', label: '右', status: 'running'}])
      expect(model.filterStale.value).toBe(false)
      panes.value.set('b', [{key: 'first', label: '左', status: 'waiting'}, {key: 'second', label: '右', status: 'waiting'}])
      expect(model.filterStale.value).toBe(true)
      expect(model.results.value.map(item => item.id)).toEqual(['b', 'c'])
      expect(model.results.value[0]!.panes!.map(pane => pane.status)).toEqual(['waiting', 'waiting'])
      expect(model.filters.value.find(option => option.id === 'running')!.count).toBe(2)
      model.refreshFilter(); expect(model.results.value.map(item => item.id)).toEqual(['c'])
      panes.value.get('c')![1]!.status = 'disconnected'
      model.search('c')
      expect(model.results.value.map(item => item.id)).toEqual(['c'])
      expect(model.filters.value.find(option => option.id === 'disconnected')!.count).toBe(1)
      expect(model.filters.value.find(option => option.id === 'running')!.count).toBe(1)
      model.close(); model.show(); expect(model.filter.value).toBe('all'); expect(model.results.value).toHaveLength(4)
    } finally { view.unmount() }
  })
  let previousWindow: PropertyDescriptor | undefined, previousDocument: PropertyDescriptor | undefined
  beforeEach(() => {
    previousWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
    previousDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
    const events = new EventTarget()
    const listeners = { addEventListener: events.addEventListener.bind(events), removeEventListener: events.removeEventListener.bind(events) }
    Object.defineProperty(globalThis, 'window', { configurable: true, value: { ...listeners } })
    Object.defineProperty(globalThis, 'document', { configurable: true, value: { ...listeners, visibilityState: 'visible', hasFocus: () => true } })
  })
  afterEach(() => {
    if (previousWindow) Object.defineProperty(globalThis, 'window', previousWindow)
    else Reflect.deleteProperty(globalThis, 'window')
    if (previousDocument) Object.defineProperty(globalThis, 'document', previousDocument)
    else Reflect.deleteProperty(globalThis, 'document')
  })
  it('ranks original numeric position before date fragments; Chinese and directory terms distinguish duplicate names', () => {
    const items = [item('a', '安全治理', '/work/202612', 1), item('b', '安全治理', '/work/project', 12), item('c', 'Other', '/work/202612', 3)]
    expect(rankQuickSwitchItems(items, '12', ['a', 'c', 'b']).map(x => x.id)).toEqual(['b', 'a', 'c'])
    expect(rankQuickSwitchItems(items, '安全 project', ['a', 'b', 'c']).map(x => x.id)).toEqual(['b'])
    expect(rankQuickSwitchItems(items, 'OTHER', ['a', 'b', 'c']).map(x => x.id)).toEqual(['c'])
    expect(rankQuickSwitchItems(items, '不存在', ['a', 'b', 'c'])).toEqual([])
  })
  function setup() {
    const groups = ref<WorkbenchGroup[]>([{ id: 'g', name: '研究', collapsed: false, tabs: ['a', 'b', 'c'].map(id => ({ id, name: id, groupId: 'g', cwd: '/work', engine: 'shell' })) }, { id: 'h', name: '隐藏', collapsed: true, tabs: [{ id: 'd', name: '远端', groupId: 'h', cwd: '/peer', engine: 'codex' }] }])
    const active = ref('a')
    let model!: ReturnType<typeof useTabQuickSwitch>
    const selections: string[] = []
    const view = mountView(defineComponent({ setup() {
      model = useTabQuickSwitch({ groups: () => groups.value, activeId: () => active.value,
        positions: () => new Map([['a', 1], ['b', 2], ['c', 3]]), machine: tab => tab.id === 'd' ? 'peer.example' : '本机', status: () => 'idle',
        select: id => { selections.push(id); active.value = id }, expand: id => { groups.value.find(x => x.id === id)!.collapsed = false } })
      return () => null
    } }), {})
    return { model, groups, active, selections, dispose: view.unmount }
  }
  it('uses page MRU without mutating topbar order and remembers identity across reorder', () => {
    const h = setup()
    try {
      h.active.value = 'c'; h.active.value = 'b'
      h.model.show(); expect(h.model.results.value.map(x => x.id)).toEqual(['c', 'a', 'd', 'b'])
      h.groups.value[0]!.tabs.reverse()
      expect(h.model.selectedId.value).toBe('c')
      expect(h.model.select()).toBe(true); expect(h.selections).toEqual(['c'])
      expect(h.groups.value[0]!.tabs.map(t => t.id)).toEqual(['c', 'b', 'a'])
      h.model.show(); expect(h.model.selectedId.value).toBe('b')
      h.model.close(); expect(h.active.value).toBe('c')
    } finally { h.dispose() }
  })
  it('does not confirm a removed target by its old list position', () => {
    const h = setup()
    try {
      h.model.show(); expect(h.model.selectedId.value).toBe('b')
      h.groups.value[0]!.tabs = h.groups.value[0]!.tabs.filter(x => x.id !== 'b')
      expect(h.model.select()).toBe(false); expect(h.selections).toEqual([]); expect(h.model.open.value).toBe(true)
      h.model.move(1); expect(h.model.select()).toBe(true); expect(h.selections).toEqual(['c'])
    } finally { h.dispose() }
  })
  it('searches collapsed remote members and expands only on confirmation', () => {
    const h = setup()
    try {
      h.model.show(); h.model.search('peer.example')
      expect(h.model.results.value[0]!.position).toBeUndefined()
      expect(h.groups.value[1]!.collapsed).toBe(true)
      expect(h.model.select()).toBe(true); expect(h.groups.value[1]!.collapsed).toBe(false); expect(h.selections).toEqual(['d'])
    } finally { h.dispose() }
  })
  it('keeps completion, idle and disconnected distinct while updating an open result in place', () => {
    const statuses = ref(new Map<string, QuickSwitchStatus>([['a', 'running'], ['b', 'done-unseen'], ['c', 'idle'], ['d', 'disconnected'], ['e', 'terminal']]))
    let model!: ReturnType<typeof useTabQuickSwitch>
    const view = mountView(defineComponent({ setup() {
      model = useTabQuickSwitch({ groups: () => [{ id: 'g', name: '工作', collapsed: false, tabs: [...statuses.value.keys()].map(id => ({ id, groupId: 'g', name: id, cwd: '/work', engine: 'shell' })) }],
        activeId: () => 'a', positions: () => new Map(), machine: () => '本机', status: id => statuses.value.get(id)!,
        select: () => {}, expand: () => {}, storage: null })
      return () => null
    } }), {})
    try {
      model.show()
      const order = model.results.value.map(x => x.id), selected = model.selectedId.value
      expect(model.results.value.map(x => x.status)).toEqual(['done-unseen', 'idle', 'disconnected', 'terminal', 'running'])
      statuses.value.set('b', 'idle') // Shared seen state has acknowledged completion.
      statuses.value.set('d', 'waiting') // Reconnected with a real awaiting-input state.
      expect(model.results.value.find(x => x.id === 'b')!.status).toBe('idle')
      expect(model.results.value.find(x => x.id === 'd')!.status).toBe('waiting')
      expect(model.results.value.map(x => x.id)).toEqual(order)
      expect(model.selectedId.value).toBe(selected)
    } finally { view.unmount() }
  })
  it('freezes ranked identities/reasons while states change, then prioritizes fresh waiting on reopen', () => {
    const groups = ref<WorkbenchGroup[]>([{ id: 'g', name: '工作', collapsed: false, tabs: ['a', 'b', 'c'].map((id, i) => ({ id, groupId: 'g', name: i === 1 ? '精确目标' : id, cwd: '/work', engine: 'shell', sessionId: id })) }])
    const active = ref('a'), time = Date.UTC(2026, 9, 23)
    const states = ref(new Map(['a', 'b', 'c'].map(id => [id, [{ key: id, rawStatus: 'running' as 'running' | 'waiting', effectiveStatus: 'running' as 'running' | 'waiting', awaitingSince: 0, activityAt: 0 }]])))
    let model!: ReturnType<typeof useTabQuickSwitch>
    const view = mountView(defineComponent({ setup() {
      model = useTabQuickSwitch({ groups: () => groups.value, activeId: () => active.value, positions: () => new Map([['a', 1], ['b', 2], ['c', 3]]),
        machine: () => '本机', status: id => states.value.get(id)![0]!.effectiveStatus, signals: () => states.value, now: () => time, storage: null,
        select: id => { active.value = id; model.noteVisit(id) }, expand: () => {} })
      return () => null
    } }), {})
    try {
      model.show(); expect(model.selectedId.value).toBe('b')
      states.value.set('c', [{ key: 'c', rawStatus: 'waiting', effectiveStatus: 'waiting', awaitingSince: time - 100, activityAt: time }])
      expect(model.results.value.map(x => x.id)).toEqual(['b', 'c', 'a'])
      expect(model.results.value[1]!.status).toBe('waiting')
      expect(model.selectedId.value).toBe('b')
      model.close(); model.show(); expect(model.selectedId.value).toBe('c')
      expect(model.results.value[0]!.reason).toBe('等待你处理')
      states.value.set('c', [{ key: 'c', rawStatus: 'running', effectiveStatus: 'running', awaitingSince: 0, activityAt: time }])
      expect(model.results.value[0]!.id).toBe('c')
      expect(model.results.value[0]!.reason).toBeUndefined()
      model.search('精确目标'); expect(model.selectedId.value).toBe('b')
      expect(model.select()).toBe(true)
      model.show(); expect(model.results.value.find(x => x.id === 'b')?.reason).toBe('刚刚查看')
      expect(groups.value[0]!.tabs.map(x => x.id)).toEqual(['a', 'b', 'c'])
    } finally { view.unmount() }
  })
  it('combines snapshot status filters with search, counts matches, and resets on reopening', () => {
    const statuses = ref(new Map<string, QuickSwitchStatus>([['a', 'running'], ['b', 'waiting'], ['c', 'waiting'], ['d', 'done-unseen'], ['e', 'idle'], ['f', 'disconnected'], ['g', 'terminal']]))
    const names = new Map([['b', '会议答复'], ['c', '代码确认'], ['d', '会议总结']])
    let model!: ReturnType<typeof useTabQuickSwitch>
    const view = mountView(defineComponent({ setup() {
      model = useTabQuickSwitch({ groups: () => [{ id: 'g', name: '工作', collapsed: false, tabs: [...statuses.value.keys()].map(id => ({ id, groupId: 'g', name: names.get(id) ?? id, cwd: '/work', engine: 'shell' })) }],
        activeId: () => 'a', positions: () => new Map(), machine: () => '本机', status: id => statuses.value.get(id)!, select: () => {}, expand: () => {}, storage: null })
      return () => null
    } }), {})
    try {
      model.show()
      expect(model.filters.value.map(x => [x.id, x.count])).toEqual([['all', 7], ['waiting', 2], ['done-unseen', 1], ['running', 1], ['idle', 1], ['disconnected', 1]])
      model.setFilter('waiting'); expect(model.results.value.map(x => x.id)).toEqual(['b', 'c'])
      const selected = model.selectedId.value
      statuses.value.set('b', 'running')
      expect(model.filterStale.value).toBe(true)
      expect(model.results.value.map(x => [x.id, x.status])).toEqual([['b', 'running'], ['c', 'waiting']])
      expect(model.selectedId.value).toBe(selected)
      expect(model.filters.value.find(x => x.id === 'waiting')!.count).toBe(2)
      model.refreshFilter(); expect(model.filterStale.value).toBe(false)
      expect(model.results.value.map(x => x.id)).toEqual(['c']); expect(model.selectedId.value).toBe('c')
      model.search('会议'); expect(model.results.value).toEqual([])
      expect(model.filters.value.map(x => x.count)).toEqual([2, 0, 1, 1, 0, 0])
      model.setFilter('done-unseen'); expect(model.results.value.map(x => x.id)).toEqual(['d'])
      model.search('不存在'); expect(model.results.value).toEqual([]); expect(model.select()).toBe(false)
      model.close(); model.show()
      expect(model.filter.value).toBe('all'); expect(model.query.value).toBe(''); expect(model.results.value).toHaveLength(7)
      expect(model.filters.value.find(x => x.id === 'idle')!.count).toBe(1) // Unknown never counts as idle.
    } finally { view.unmount() }
  })
  it('never confirms a closed filtered target or substitutes its former row', () => {
    const groups = ref<WorkbenchGroup[]>([{ id: 'g', name: '工作', collapsed: false, tabs: ['a', 'b', 'c'].map(id => ({ id, groupId: 'g', name: id, cwd: '/work', engine: 'shell' })) }])
    const selections: string[] = []
    let model!: ReturnType<typeof useTabQuickSwitch>
    const view = mountView(defineComponent({ setup() {
      model = useTabQuickSwitch({ groups: () => groups.value, activeId: () => 'a', positions: () => new Map(), machine: () => '本机', status: () => 'idle', select: id => selections.push(id), expand: () => {}, storage: null })
      return () => null
    } }), {})
    try {
      model.show(); model.setFilter('idle'); expect(model.selectedId.value).toBe('b')
      groups.value[0]!.tabs = groups.value[0]!.tabs.filter(t => t.id !== 'b')
      expect(model.select('b')).toBe(false); expect(selections).toEqual([])
      model.move(1); expect(model.select()).toBe(true); expect(selections).toEqual(['c'])
    } finally { view.unmount() }
  })
  it('preserves refresh history during hydration and counts only an uninterrupted visible terminal visit', async () => {
    const time = Date.now(), groups = ref<WorkbenchGroup[]>([]), ready = ref(false), reading = ref(true), active = ref('a')
    let saved = encodeQuickSwitchHistory(new Map([['b', { enteredAt: time - 60000, countedAt: 0, days: [] }]]), time)
    const storage = { getItem: () => saved, setItem: (_: string, value: string) => { saved = value } }
    let model!: ReturnType<typeof useTabQuickSwitch>
    const view = mountView(defineComponent({ setup() {
      model = useTabQuickSwitch({ groups: () => groups.value, activeId: () => active.value, positions: () => new Map(), machine: () => '本机', status: () => 'idle',
        storage, ready: () => ready.value, isReading: () => reading.value,
        select: id => { active.value = id; model.noteVisit(id) }, expand: () => {} })
      return () => null
    } }), {})
    try {
      groups.value = [{ id: 'g', name: '工作', collapsed: false, tabs: ['a', 'b', 'c'].map(id => ({ id, groupId: 'g', name: id, engine: 'shell', cwd: '/work' })) }]
      ready.value = true
      model.show(); expect(model.selectedId.value).toBe('b')
      expect(decodeQuickSwitchHistory(saved, Date.now()).get('b')?.days).toEqual([])
      model.select('b')
      reading.value = false
      await new Promise(resolve => setTimeout(resolve, 2100))
      expect(decodeQuickSwitchHistory(saved, Date.now()).get('b')?.days).toEqual([])
      reading.value = true
      await new Promise(resolve => setTimeout(resolve, 2100))
      expect(decodeQuickSwitchHistory(saved, Date.now()).get('b')?.days).toEqual([[Math.floor(Date.now() / 86400000), 1]])
      groups.value[0]!.tabs = groups.value[0]!.tabs.filter(t => t.id !== 'b')
      expect(decodeQuickSwitchHistory(saved, Date.now()).has('b')).toBe(false)
    } finally { view.unmount() }
  })
  it('keeps switching usable when local storage throws', () => {
    let model!: ReturnType<typeof useTabQuickSwitch>, active = 'a'
    const view = mountView(defineComponent({ setup() {
      model = useTabQuickSwitch({ groups: () => [{ id: 'g', name: 'g', collapsed: false, tabs: ['a', 'b'].map(id => ({ id, name: id, groupId: 'g', cwd: '/work', engine: 'shell' })) }],
        activeId: () => active, positions: () => new Map(), machine: () => '本机', status: () => 'idle', storage: { getItem() { throw Error('denied') }, setItem() { throw Error('quota') } },
        select: id => { active = id; model.noteVisit(id) }, expand: () => {} })
      return () => null
    } }), {})
    try { model.show(); expect(model.select('b')).toBe(true); expect(active).toBe('b') } finally { view.unmount() }
  })
})
