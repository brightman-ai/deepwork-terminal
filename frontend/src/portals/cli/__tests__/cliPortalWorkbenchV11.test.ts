/**
 * Task Workbench v11 integration tests for CliPortal / CliTabBar / workbenchMerge:
 *  1. `profileId` field preservation across 3-way `mergeWorkbench` + `createTab`.
 *  2. `<CliTerminalView>` `v-show` survival invariant across `BotWorkspaceView`,
 *     `CenterFilePreview` (italic Preview Tab), and `SessionTranscriptView` (`💬 Chat`).
 *  3. `CliTabBar` v11 controls (`btn-nav-toggle`, `cli-preview-file-tab`,
 *     `vmode-transcript`, `vmode-terminal`, `btn-open-dispatch`, `toggle-right-drawer`).
 */
import { afterEach, beforeAll, describe, expect, it } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { computed, defineComponent, h, nextTick, ref, withDirectives, vShow, type Component } from 'vue'
import { installVueSfcLoader } from './support/sfcLoader'
import { byTestId, fire, findAll, isShown, mountView } from './support/testRenderer'
import { createTab, type WorkbenchConfig, type WorkbenchTab } from '../../../types/workbench'
import { mergeWorkbench } from '../../../composables/cli/workbenchMerge'
import { resolveCompanionDrawerInset, resolveWorkbenchCanvas } from '../workbenchCanvasState'
import type { SpaceKind, TabViewMode } from '../../../composables/cli/useTaskWorkbench'
import type { EffectiveStatus } from '../../../composables/cli/useAgentOverview'

installVueSfcLoader()

const g = globalThis as unknown as Record<string, unknown>
const SAVED = {
  window: g.window,
  document: g.document,
  localStorage: g.localStorage,
  ResizeObserver: g.ResizeObserver,
  requestAnimationFrame: g.requestAnimationFrame,
}
let cleanups: Array<() => void> = []

function setupBrowserEnv(width = 1440, height = 900, mobile = false): void {
  const store = new Map<string, string>()
  g.localStorage = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v) },
    removeItem: (k: string) => { store.delete(k) },
    clear: () => { store.clear() },
  }
  g.document = {
    visibilityState: 'visible',
    hasFocus: () => true,
    addEventListener: () => {},
    removeEventListener: () => {},
    querySelector: () => null,
    querySelectorAll: () => [],
  }
  g.window = {
    HTMLElement: class { focus(): void {} },
    HTMLInputElement: class { select(): void {} },
    HTMLTextAreaElement: class { select(): void {} },
    focus(): void {},
    performance,
    innerWidth: width,
    innerHeight: height,
    location: { search: '', pathname: '/portal/cli', hash: '', href: 'http://localhost/portal/cli' },
    history: { replaceState: () => {} },
    localStorage: g.localStorage,
    ...(mobile ? { ontouchstart: null } : {}),
    addEventListener: () => {},
    removeEventListener: () => {},
  }
  g.ResizeObserver = class {
    observe(): void {}
    disconnect(): void {}
  }
  g.requestAnimationFrame = (cb: (t: number) => void) => { cb(0); return 0 }
}

let CliTerminalView: Component
let CliTabBar: Component
let createTaskWorkbench: typeof import('../../../composables/cli/useTaskWorkbench').createTaskWorkbench
beforeAll(async () => {
  setupBrowserEnv(1440, 900, false)
  CliTerminalView = (await import('../adapters/CliTerminalView.vue')).default
  CliTabBar = (await import('../adapters/CliTabBar.vue')).default
  createTaskWorkbench = (await import('../../../composables/cli/useTaskWorkbench')).createTaskWorkbench
})

afterEach(() => {
  cleanups.forEach((fn) => fn())
  cleanups = []
  for (const [k, v] of Object.entries(SAVED)) {
    if (v === undefined) delete g[k]
    else g[k] = v
  }
})

function makeConfig(tabs: WorkbenchTab[], activeTabId = tabs[0]?.id ?? '', rev = 1): WorkbenchConfig {
  return {
    groups: [{ id: 'g1', name: '默认', tabs, collapsed: false }],
    activeGroupId: 'g1',
    activeTabId,
    lastSaved: '2026-11-21T00:00:00.000Z',
    rev,
  }
}

describe('REQ-TW-01 · WorkbenchTab profileId 3-way merge preservation', () => {
  it('createTab sets optional profileId only when provided (zero breaking change)', () => {
    const plain = createTab({ groupId: 'g1', name: '终端 1', cwd: '~' })
    expect('profileId' in plain).toBe(false)

    const withProf = createTab({ groupId: 'g1', name: 'GLM', cwd: '/repo', profileId: 'prof-glm' })
    expect(withProf.profileId).toBe('prof-glm')
  })

  it('preserves local profileId change when server concurrently updates cwd', () => {
    const baseTab: WorkbenchTab = {
      id: 't1', groupId: 'g1', name: '主终端', cwd: '/repo', engine: 'claude', sessionId: 's1', profileId: 'prof-a',
    }
    const base = makeConfig([baseTab], 't1', 1)
    const local = makeConfig([{ ...baseTab, profileId: 'prof-b' }], 't1', 1)
    const server = makeConfig([{ ...baseTab, cwd: '/repo/sub' }], 't1', 2)

    const merged = mergeWorkbench(base, local, server)
    const tab = merged.groups[0]!.tabs[0]!
    expect(tab.profileId).toBe('prof-b')
    expect(tab.cwd).toBe('/repo/sub')
    expect(merged.rev).toBe(2)
  })

  it('preserves server profileId change when local only renames the tab', () => {
    const baseTab: WorkbenchTab = {
      id: 't1', groupId: 'g1', name: '主终端', cwd: '/repo', engine: 'claude', sessionId: 's1',
    }
    const base = makeConfig([baseTab], 't1', 1)
    const local = makeConfig([{ ...baseTab, name: '主卷 · 改名' }], 't1', 1)
    const server = makeConfig([{ ...baseTab, profileId: 'prof-codex' }], 't1', 2)

    const merged = mergeWorkbench(base, local, server)
    const tab = merged.groups[0]!.tabs[0]!
    expect(tab.name).toBe('主卷 · 改名')
    expect(tab.profileId).toBe('prof-codex')
  })

  it('preserves profileId on both locally-added and server-added tabs during conflict merge', () => {
    const shared: WorkbenchTab = {
      id: 't1', groupId: 'g1', name: 't1', cwd: '/repo', engine: 'shell', sessionId: 's1', profileId: 'prof-main',
    }
    const localNew: WorkbenchTab = {
      id: 't-local', groupId: 'g1', name: 'local', cwd: '/wt1', engine: 'claude', sessionId: 's2', profileId: 'prof-glm',
    }
    const serverNew: WorkbenchTab = {
      id: 't-server', groupId: 'g1', name: 'server', cwd: '/wt2', engine: 'codex', sessionId: 's3', profileId: 'prof-codex',
    }
    const merged = mergeWorkbench(
      makeConfig([shared], 't1', 1),
      makeConfig([shared, localNew], 't-local', 1),
      makeConfig([shared, serverNew], 't-server', 2),
    )
    const byId = new Map(merged.groups[0]!.tabs.map((t) => [t.id, t]))
    expect(byId.get('t1')?.profileId).toBe('prof-main')
    expect(byId.get('t-local')?.profileId).toBe('prof-glm')
    expect(byId.get('t-server')?.profileId).toBe('prof-codex')
  })
})

describe('REQ-TW-01/05 · v-show terminal survival & canvas state transitions', () => {
  it('resolveWorkbenchCanvas computes mutually-exclusive primary views and PC split-file pane', () => {
    const base = {
      activeSpaceKind: 'task' as SpaceKind,
      hasDetachedCard: false,
      hasCenterPreview: false,
      centerPreviewActive: false,
      hasSplitPreview: false,
      isMobile: false,
      activeTabViewMode: 'terminal' as TabViewMode,
      hasActiveSession: true,
    }

    // 1. Default CLI terminal mode
    expect(resolveWorkbenchCanvas(base)).toEqual({
      showTerminalView: true,
      showDetachedCard: false,
      showTranscriptView: false,
      showCenterPreview: false,
      showBotWorkspace: false,
      showSplitPreview: false,
    })

    // 2. Switch to Resident Bot Workspace
    expect(resolveWorkbenchCanvas({ ...base, activeSpaceKind: 'bot', hasSplitPreview: true })).toEqual({
      showTerminalView: false,
      showDetachedCard: false,
      showTranscriptView: false,
      showCenterPreview: false,
      showBotWorkspace: true,
      showSplitPreview: false,
    })

    // 3. Open italic Center Preview Tab on PC
    expect(resolveWorkbenchCanvas({ ...base, hasCenterPreview: true, centerPreviewActive: true })).toEqual({
      showTerminalView: false,
      showDetachedCard: false,
      showTranscriptView: false,
      showCenterPreview: true,
      showBotWorkspace: false,
      showSplitPreview: false,
    })

    // 4. Switch to 💬 Chat transcript mode
    expect(resolveWorkbenchCanvas({ ...base, activeTabViewMode: 'transcript' })).toEqual({
      showTerminalView: false,
      showDetachedCard: false,
      showTranscriptView: true,
      showCenterPreview: false,
      showBotWorkspace: false,
      showSplitPreview: false,
    })

    // 5. PC side-by-side split-file preview keeps terminal visible alongside split pane
    expect(resolveWorkbenchCanvas({ ...base, hasSplitPreview: true })).toEqual({
      showTerminalView: true,
      showDetachedCard: false,
      showTranscriptView: false,
      showCenterPreview: false,
      showBotWorkspace: false,
      showSplitPreview: true,
    })

    // 6. Mobile never shows PC side-by-side split-file pane
    expect(resolveWorkbenchCanvas({ ...base, isMobile: true, hasSplitPreview: true }).showSplitPreview).toBe(false)
  })

  it('resolveCompanionDrawerInset yields right/left inset for CenterFilePreview & Chat while docked split drawer is open (R2-01)', () => {
    const previewCanvas = resolveWorkbenchCanvas({
      activeSpaceKind: 'task',
      hasDetachedCard: false,
      hasCenterPreview: true,
      centerPreviewActive: true,
      hasSplitPreview: false,
      isMobile: false,
      activeTabViewMode: 'terminal',
      hasActiveSession: true,
    })
    expect(resolveCompanionDrawerInset(previewCanvas, 300, 'right')).toEqual({ paddingRight: '300px' })
    expect(resolveCompanionDrawerInset(previewCanvas, 360, 'left')).toEqual({ paddingLeft: '360px' })
    expect(resolveCompanionDrawerInset(previewCanvas, 0, 'right')).toEqual({})

    const chatCanvas = resolveWorkbenchCanvas({
      activeSpaceKind: 'task',
      hasDetachedCard: false,
      hasCenterPreview: false,
      centerPreviewActive: false,
      hasSplitPreview: false,
      isMobile: false,
      activeTabViewMode: 'transcript',
      hasActiveSession: true,
    })
    expect(resolveCompanionDrawerInset(chatCanvas, 300, 'right')).toEqual({ paddingRight: '300px' })

    // Terminal view squeezes itself inside CliTerminalSurface -> no double inset
    const termCanvas = resolveWorkbenchCanvas({
      activeSpaceKind: 'task',
      hasDetachedCard: false,
      hasCenterPreview: false,
      centerPreviewActive: false,
      hasSplitPreview: false,
      isMobile: false,
      activeTabViewMode: 'terminal',
      hasActiveSession: true,
    })
    expect(resolveCompanionDrawerInset(termCanvas, 300, 'right')).toEqual({})
  })

  it('keeps the exact mounted <CliTerminalView> instance alive via v-show across Bot, Preview Tab, and Chat transitions', async () => {
    setupBrowserEnv(1440, 900, false)
    const wb = createTaskWorkbench()
    const centerPreviewActive = ref(false)

    const Shell = defineComponent({
      setup() {
        const canvas = computed(() =>
          resolveWorkbenchCanvas({
            activeSpaceKind: wb.activeSpaceKind.value,
            hasDetachedCard: false,
            hasCenterPreview: Boolean(wb.centerPreviewFile.value),
            centerPreviewActive: centerPreviewActive.value,
            hasSplitPreview: false,
            isMobile: false,
            activeTabViewMode: wb.getTabViewMode('t1'),
            hasActiveSession: true,
          }),
        )
        return () =>
          h('div', { class: 'test-portal-shell' }, [
            withDirectives(
              h(CliTerminalView, {
                loading: false,
                error: null,
                activeTabId: 't1',
                tabsWithSession: [{ id: 't1', name: '主终端', sessionId: 'S1' }],
                splitAllowed: true,
              }),
              [[vShow, canvas.value.showTerminalView]],
            ),
            canvas.value.showBotWorkspace ? h('div', { 'data-testid': 'bot-workspace-view' }) : null,
            canvas.value.showCenterPreview ? h('div', { 'data-testid': 'center-file-preview' }) : null,
            canvas.value.showTranscriptView ? h('div', { 'data-testid': 'session-transcript-view' }) : null,
          ])
      },
    })

    const m = mountView(Shell, {})
    cleanups.push(m.unmount)

    const initialTermView = byTestId(m.host, 'cli-portal-terminal-view')[0]!
    const initialSurface = findAll(m.host, (n) => n.props['data-sid'] === 'S1')[0]!
    expect(isShown(initialTermView)).toBe(true)

    // Transition 1: CLI → BotWorkspaceView
    wb.selectBot('scout')
    await nextTick()
    expect(byTestId(m.host, 'bot-workspace-view')).toHaveLength(1)
    expect(byTestId(m.host, 'cli-portal-terminal-view')[0]).toBe(initialTermView)
    expect(findAll(m.host, (n) => n.props['data-sid'] === 'S1')[0]).toBe(initialSurface)
    expect(isShown(initialTermView)).toBe(false)

    // Transition 2: BotWorkspaceView → CenterFilePreview (italic Preview Tab)
    wb.openCenterPreview('/repo/docs/spec.md')
    centerPreviewActive.value = true
    await nextTick()
    expect(byTestId(m.host, 'center-file-preview')).toHaveLength(1)
    expect(byTestId(m.host, 'cli-portal-terminal-view')[0]).toBe(initialTermView)
    expect(isShown(initialTermView)).toBe(false)

    // Transition 3: CenterFilePreview → 💬 Chat (SessionTranscriptView)
    wb.closeCenterPreview()
    centerPreviewActive.value = false
    wb.setTabViewMode('t1', 'transcript')
    await nextTick()
    expect(byTestId(m.host, 'session-transcript-view')).toHaveLength(1)
    expect(byTestId(m.host, 'cli-portal-terminal-view')[0]).toBe(initialTermView)
    expect(isShown(initialTermView)).toBe(false)

    // Transition 4: 💬 Chat → >_ CLI restores the exact same mounted terminal surface
    wb.setTabViewMode('t1', 'terminal')
    await nextTick()
    expect(byTestId(m.host, 'cli-portal-terminal-view')[0]).toBe(initialTermView)
    expect(findAll(m.host, (n) => n.props['data-sid'] === 'S1')[0]).toBe(initialSurface)
    expect(isShown(initialTermView)).toBe(true)
  })

  it('CliPortal.vue template enforces v-show (never v-if) on <CliTerminalView>', () => {
    const source = readFileSync(join(import.meta.dir, '..', 'CliPortal.vue'), 'utf8')
    const match = /<CliTerminalView[\s\S]*?\/>/.exec(source)
    expect(match).not.toBeNull()
    const tag = match![0]
    expect(tag).toContain('v-show="canvas.showTerminalView"')
    expect(tag).not.toContain('v-if=')
  })
})

describe('REQ-TW-05/06 · CliTabBar v11 controls (nav toggle, italic preview tab, view mode, dispatch, drawer)', () => {
  const emptyRollup: Record<EffectiveStatus, number> = {
    waiting: 0,
    running: 0,
    'done-unseen': 0,
    idle: 1,
  }
  const sampleGroups = [
    {
      id: 'g1',
      name: '默认',
      collapsed: false,
      tabs: [{ id: 't1', groupId: 'g1', name: '主终端', cwd: '/repo', engine: 'claude', sessionId: 'S1' }],
    },
  ]

  it('renders btn-nav-toggle when sidebar closed or mobile, italic preview tab on PC, and emits top-right tool events', async () => {
    setupBrowserEnv(1440, 900, false)
    const events: string[] = []
    const m = mountView(CliTabBar, {
      groups: sampleGroups,
      activeTabId: 't1',
      showGroupHeaders: false,
      renamingTabId: null,
      renameValue: '',
      tabPositions: new Map([['t1', 1]]),
      tabStatuses: new Map(),
      rollup: emptyRollup,
      sidebarOpen: true,
      isMobile: false,
      activeSpaceKind: 'task',
      centerPreviewFile: null as { path: string; line?: number } | null,
      centerPreviewActive: false,
      viewMode: 'terminal',
      drawerOpen: false,
      onToggleSidebar: () => events.push('toggle-sidebar'),
      onSelectPreview: () => events.push('select-preview'),
      onClosePreview: () => events.push('close-preview'),
      'onUpdate:viewMode': (mode: string) => events.push(`view-mode:${mode}`),
      onOpenDispatch: () => events.push('open-dispatch'),
      onToggleRightDrawer: () => events.push('toggle-right-drawer'),
    })
    cleanups.push(m.unmount)

    // Sidebar open on PC → btn-nav-toggle hidden
    expect(byTestId(m.host, 'btn-nav-toggle')).toHaveLength(0)
    expect(byTestId(m.host, 'cli-preview-file-tab')).toHaveLength(0)

    // Collapse sidebar → btn-nav-toggle appears and emits toggle-sidebar
    m.props.sidebarOpen = false
    await nextTick()
    expect(byTestId(m.host, 'btn-nav-toggle')).toHaveLength(1)
    fire(byTestId(m.host, 'btn-nav-toggle')[0]!, 'onClick')
    expect(events).toContain('toggle-sidebar')

    // Set centerPreviewFile on PC → italic Preview Tab renders
    m.props.centerPreviewFile = { path: '/repo/docs/preview.html' }
    m.props.centerPreviewActive = true
    await nextTick()
    expect(byTestId(m.host, 'cli-preview-file-tab')).toHaveLength(1)
    fire(byTestId(m.host, 'cli-preview-file-tab')[0]!, 'onClick')
    fire(byTestId(m.host, 'cli-preview-file-tab-close')[0]!, 'onClick', { stopPropagation() {} })
    expect(events).toContain('select-preview')
    expect(events).toContain('close-preview')

    // Top-right tools: [💬 Chat | >_ CLI], [⚡ 派审], [◨ 边栏]
    fire(byTestId(m.host, 'vmode-transcript')[0]!, 'onClick')
    fire(byTestId(m.host, 'vmode-terminal')[0]!, 'onClick')
    fire(byTestId(m.host, 'btn-open-dispatch')[0]!, 'onClick')
    fire(byTestId(m.host, 'toggle-right-drawer')[0]!, 'onClick')
    expect(events).toEqual([
      'toggle-sidebar',
      'select-preview',
      'close-preview',
      'view-mode:transcript',
      'view-mode:terminal',
      'open-dispatch',
      'toggle-right-drawer',
    ])

    // Mobile: btn-nav-toggle always visible even if sidebarOpen=true; italic Preview Tab hidden; compact 💬/>_ labels
    m.props.sidebarOpen = true
    m.props.isMobile = true
    await nextTick()
    expect(byTestId(m.host, 'btn-nav-toggle')).toHaveLength(1)
    expect(byTestId(m.host, 'cli-preview-file-tab')).toHaveLength(0)
    const trText = byTestId(m.host, 'vmode-transcript')[0]!.children.map((c) => c.text).join('').trim()
    const cliText = byTestId(m.host, 'vmode-terminal')[0]!.children.map((c) => c.text).join('').trim()
    expect(trText).toBe('💬')
    expect(cliText).toBe('>_')
  })
})
