import { describe, it, expect } from 'bun:test'
import { useTabContextMenu, type TabMenuActions } from '../useTabContextMenu'

/** A MouseEvent stand-in that records whether the browser's own menu was suppressed. */
function fakeEvent(x = 120, y = 80) {
  const calls = { prevented: 0, stopped: 0 }
  const e = {
    clientX: x,
    clientY: y,
    preventDefault: () => { calls.prevented++ },
    stopPropagation: () => { calls.stopped++ },
  } as unknown as MouseEvent
  return { e, calls }
}

function harness(overrides: Partial<TabMenuActions> = {}, count = 3) {
  const log: string[] = []
  const actions: TabMenuActions = {
    rename: (id) => log.push(`rename:${id}`),
    close: (id) => log.push(`close:${id}`),
    closeOthers: (id) => log.push(`closeOthers:${id}`),
    create: () => log.push('create'),
    copy: (t) => { log.push(`copy:${t}`) },
    tabCount: () => count,
    ...overrides,
  }
  return { menu: useTabContextMenu(actions), log }
}

function item(menu: ReturnType<typeof useTabContextMenu>, key: string) {
  const found = menu.items.value.find((i) => i.key === key)
  if (!found) throw new Error(`no menu item ${key}; got ${menu.items.value.map((i) => i.key).join(',')}`)
  return found
}

describe('useTabContextMenu', () => {
  it('opens at the pointer and suppresses the browser menu', () => {
    const { menu } = harness()
    const { e, calls } = fakeEvent(200, 44)
    menu.openAt(e, { id: 't1', name: '终端 1', cwd: '/repo' })
    expect(menu.open.value).toBe(true)
    expect(menu.position.value).toEqual({ x: 200, y: 44 })
    expect(calls.prevented).toBe(1)
    // Without preventDefault the native menu covers ours; this is the whole reason the model owns
    // the event rather than each tab bar remembering to call it.
    expect(calls.stopped).toBe(1)
  })

  it('every entry runs its action AND closes the menu', () => {
    const { menu, log } = harness()
    for (const key of ['rename', 'new', 'close-others', 'close']) {
      menu.openAt(fakeEvent().e, { id: 't1', name: '终端 1', cwd: '/repo' })
      item(menu, key).run()
      expect(menu.open.value).toBe(false)
    }
    expect(log).toEqual(['rename:t1', 'create', 'closeOthers:t1', 'close:t1'])
  })

  it('copies the tab\'s cwd, and disables the entry when there is none', () => {
    const { menu, log } = harness()
    menu.openAt(fakeEvent().e, { id: 't1', name: '终端 1', cwd: '/home/me/repo' })
    expect(item(menu, 'copy-cwd').disabled).toBeFalsy()
    item(menu, 'copy-cwd').run()
    expect(log).toEqual(['copy:/home/me/repo'])

    // A tab that never connected has no live directory — better an obviously-dead entry than one
    // that silently copies an empty string.
    menu.openAt(fakeEvent().e, { id: 't2', name: '终端 2' })
    expect(item(menu, 'copy-cwd').disabled).toBe(true)
  })

  it('keeps 关闭其他 present but disabled with a single tab (stable positions)', () => {
    const { menu } = harness({}, 1)
    menu.openAt(fakeEvent().e, { id: 't1', name: '终端 1' })
    // Present: a menu whose entries appear and disappear between right-clicks has to be re-read
    // every time, which costs more than one greyed row.
    expect(item(menu, 'close-others').disabled).toBe(true)
    expect(menu.items.value.map((i) => i.key)).toEqual(['rename', 'copy-cwd', 'new', 'close-others', 'close'])
  })

  it('omits 关闭其他 entirely when the host cannot do it', () => {
    const { menu } = harness({ closeOthers: undefined })
    menu.openAt(fakeEvent().e, { id: 't1', name: '终端 1' })
    expect(menu.items.value.some((i) => i.key === 'close-others')).toBe(false)
    // A capability the shell genuinely lacks is absent, not permanently greyed — greying implies
    // "not right now", which would be a lie.
  })

  it('destructive entries sort last so 关闭 is never adjacent to a benign default', () => {
    const { menu } = harness()
    menu.openAt(fakeEvent().e, { id: 't1', name: '终端 1' })
    const firstDanger = menu.items.value.findIndex((i) => i.danger)
    expect(firstDanger).toBeGreaterThan(0)
    expect(menu.items.value.slice(firstDanger).every((i) => i.danger)).toBe(true)
  })

  it('has no items and runs nothing once closed', () => {
    const { menu, log } = harness()
    menu.openAt(fakeEvent().e, { id: 't1', name: '终端 1' })
    const close = item(menu, 'close')
    menu.close()
    expect(menu.items.value).toEqual([])
    close.run() // a stale handler from the closed menu must be inert
    expect(log).toEqual([])
  })
})
