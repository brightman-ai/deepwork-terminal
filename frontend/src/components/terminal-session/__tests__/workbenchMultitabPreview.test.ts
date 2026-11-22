import { afterEach, beforeAll, describe, expect, it } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { defineComponent, h, nextTick, type Component } from 'vue'
import { installVueSfcLoader } from '../../../portals/cli/__tests__/support/sfcLoader'
import { byTestId, fire, mountView, type TNode } from '../../../portals/cli/__tests__/support/testRenderer'

installVueSfcLoader()

const g = globalThis as unknown as Record<string, unknown>
const SAVED = {
  window: g.window,
  document: g.document,
  localStorage: g.localStorage,
  ResizeObserver: g.ResizeObserver,
  fetch: g.fetch,
}
let cleanups: Array<() => void> = []

function textOf(node: TNode): string {
  return node.kind === 'text' ? node.text : node.children.map(textOf).join('')
}

function setupBrowserEnv(): void {
  const store = new Map<string, string>()
  const media = {
    matches: false,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  }
  g.localStorage = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, v) },
    removeItem: (k: string) => { store.delete(k) },
    clear: () => { store.clear() },
  }
  g.document = {
    visibilityState: 'visible',
    addEventListener() {},
    removeEventListener() {},
    querySelector: () => null,
    querySelectorAll: () => [],
  }
  g.window = {
    innerWidth: 1440,
    innerHeight: 900,
    location: { search: '', pathname: '/portal/cli', hash: '', href: 'http://localhost/portal/cli' },
    localStorage: g.localStorage,
    matchMedia: () => media,
    addEventListener() {},
    removeEventListener() {},
  }
  g.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
}

let PreviewDocumentTabs: Component
let ReviewPanel: Component

beforeAll(async () => {
  setupBrowserEnv()
  PreviewDocumentTabs = (await import('../PreviewDocumentTabs.vue')).default
  ReviewPanel = (await import('../ReviewPanel.vue')).default
})

afterEach(() => {
  cleanups.forEach((fn) => fn())
  cleanups = []
  for (const [k, v] of Object.entries(SAVED)) {
    if (v === undefined) delete g[k]
    else g[k] = v
  }
})

describe('REQ-TW-05 · PreviewDocumentTabs multi-tab rendering, disambiguation & actions', () => {
  it('renders disambiguated labels, active class, middle-click close, and close-others / close-all actions', async () => {
    setupBrowserEnv()
    const selected: string[] = []
    const closed: string[] = []
    const closedOthers: string[] = []
    let closedAllCount = 0

    const tabs = [
      { key: 'k1', name: 'index.ts', absPath: '/repo/src/api/index.ts', loading: false },
      { key: 'k2', name: 'index.ts', absPath: '/repo/src/web/index.ts', loading: false },
      { key: 'k3', name: 'README.md', absPath: '/repo/README.md', loading: false },
    ]

    const m = mountView(PreviewDocumentTabs, {
      tabs: [tabs[0]!],
      activeKey: 'k1',
      onSelect: (k: string) => selected.push(k),
      onClose: (k: string) => closed.push(k),
      onCloseOthers: (k: string) => closedOthers.push(k),
      onCloseAll: () => { closedAllCount++ },
    })
    cleanups.push(m.unmount)

    // Single tab: no close-others / close-all buttons
    expect(byTestId(m.host, 'fp-preview-tabs-close-others')).toHaveLength(0)
    expect(byTestId(m.host, 'fp-preview-tabs-close-all')).toHaveLength(0)

    // Multiple tabs: disambiguate same-name files and expose close-others / close-all
    m.props.tabs = tabs
    m.props.activeKey = 'k2'
    await nextTick()

    const tab1 = byTestId(m.host, 'fp-preview-tab-k1')[0]!
    const tab2 = byTestId(m.host, 'fp-preview-tab-k2')[0]!
    const tab3 = byTestId(m.host, 'fp-preview-tab-k3')[0]!

    expect(textOf(tab1)).toContain('api/index.ts')
    expect(textOf(tab2)).toContain('web/index.ts')
    expect(textOf(tab3)).toContain('README.md')
    expect(String(tab2.props.class || '')).toContain('is-active')
    expect(String(tab1.props.class || '')).not.toContain('is-active')

    // Click select
    fire(tab1, 'onClick', {})
    expect(selected).toEqual(['k1'])

    // Middle-click close (auxclick with button === 1)
    fire(tab3, 'onAuxclick', { button: 1, preventDefault() {} })
    expect(closed).toEqual(['k3'])

    // Close others & close all
    const closeOthersBtn = byTestId(m.host, 'fp-preview-tabs-close-others')[0]!
    const closeAllBtn = byTestId(m.host, 'fp-preview-tabs-close-all')[0]!
    fire(closeOthersBtn, 'onClick', {})
    fire(closeAllBtn, 'onClick', {})
    expect(closedOthers).toEqual(['k2'])
    expect(closedAllCount).toBe(1)
  })
})

describe('REQ-TW-05 · ReviewPanel opens files inside workbench multi-tab reader', () => {
  it('emits open-file (not open-center-preview) when clicking 📄 and keeps ◫ split-file', async () => {
    setupBrowserEnv()
    g.fetch = (async () =>
      Response.json({
        root: '/repo',
        files: [
          {
            path: 'src/app.ts',
            status: 'M',
            added: 4,
            deleted: 1,
            binary: false,
            diff: '@@ -1,2 +1,3 @@\n+const ok = true',
          },
        ],
        truncated: false,
      })) as unknown as typeof fetch

    const openedFiles: string[] = []
    const openedCenter: string[] = []
    const splitFiles: string[] = []

    const m = mountView(
      defineComponent({
        setup: () => () =>
          h(ReviewPanel, {
            sessionId: 'sess-1',
            cwd: '/repo',
            active: true,
            onOpenFile: (p: string) => openedFiles.push(p),
            onOpenCenterPreview: (p: string) => openedCenter.push(p),
            onSplitFile: (p: string) => splitFiles.push(p),
          }),
      }),
      {},
    )
    cleanups.push(m.unmount)

    const end = Date.now() + 1500
    while (Date.now() < end && byTestId(m.host, 'rp-open-preview-src/app.ts').length === 0) {
      await nextTick()
      await new Promise((r) => setTimeout(r, 5))
    }

    const openBtn = byTestId(m.host, 'rp-open-preview-src/app.ts')[0]!
    const splitBtn = byTestId(m.host, 'rp-split-file-src/app.ts')[0]!
    expect(openBtn.props.title).toBe('在工作台多标签打开文件')

    fire(openBtn, 'onClick', { stopPropagation() {} })
    fire(splitBtn, 'onClick', { stopPropagation() {} })

    expect(openedFiles).toEqual(['/repo/src/app.ts'])
    expect(openedCenter).toEqual([])
    expect(splitFiles).toEqual(['/repo/src/app.ts'])
  })
})

describe('REQ-TW-05 · FilesPanel default inline multi-tab behavior, capacity & compact guard', () => {
  it('opens multiple files in workbench tabs without emitting open-center-preview, supports close-others/close-all, quick exits & narrow panel compact mode', () => {
    const script = `
      import assert from 'node:assert/strict'
      import { plugin } from 'bun'
      import { defineComponent, h, nextTick } from 'vue'
      import { installVueSfcLoader } from './src/portals/cli/__tests__/support/sfcLoader'
      import { byTestId, findAll, fire, mountView } from './src/portals/cli/__tests__/support/testRenderer'

      const media = { matches: false, addEventListener(){}, removeEventListener(){}, addListener(){}, removeListener(){} }
      const resizeObservers = []
      Object.assign(globalThis, {
        window: {
          location: { search: '', hash: '', pathname: '/' },
          innerWidth: 1440,
          innerHeight: 900,
          matchMedia: () => media,
          addEventListener() {},
          removeEventListener() {},
        },
        document: {
          visibilityState: 'visible',
          documentElement: { clientWidth: 1440, clientHeight: 900 },
          body: { offsetHeight: 0 },
          addEventListener() {},
          removeEventListener() {},
        },
        localStorage: { getItem() { return null }, setItem() {} },
        ResizeObserver: class {
          constructor(cb) { this.cb = cb; this.targets = [] }
          observe(el) { this.targets.push(el); resizeObservers.push({ el, cb: this.cb }) }
          unobserve() {}
          disconnect() {}
        },
        requestAnimationFrame: (f) => setTimeout(f, 0),
        cancelAnimationFrame: clearTimeout,
        getComputedStyle: () => ({ transitionDelay: '0s', transitionDuration: '0s', animationDelay: '0s', animationDuration: '0s' }),
      })
      window.getComputedStyle = getComputedStyle

      const treeEntries = Array.from({ length: 12 }, (_, i) => ({
        name: \`file-\${i + 1}.ts\`,
        isDir: false,
        size: 120 + i,
        mtimeMs: 1700000000000 + i * 1000,
      }))

      globalThis.fetch = async (input) => {
        const url = new URL(String(input), 'http://fixture')
        if (url.pathname.endsWith('/files/tree')) {
          return Response.json({ cwd: '/project', rel: '.', entries: treeEntries, truncated: false })
        }
        if (url.pathname.endsWith('/files/raw')) {
          const p = url.searchParams.get('path') || ''
          return new Response('content-of:' + p, { headers: { 'Content-Type': 'text/plain' } })
        }
        if (url.pathname.endsWith('/files/recent')) {
          return Response.json({
            items: [{ name: 'recent.md', path: '/project/recent.md', dir: '/project', exists: true, size: 42, tsMs: Date.now() }],
          })
        }
        return Response.json({ entries: [], items: [], maxBytes: 1000000 })
      }

      plugin({
        name: 'preview-renderer-stubs',
        setup(build) {
          build.onResolve(
            { filter: /(FilePreview|DocxPreview|PdfPreview|SheetPreview|AudioPreview|ArchivePreview|XmindPreview)\\.vue$/ },
            () => ({ path: import.meta.dir + '/src/components/terminal-session/__tests__/previewReaderStub.ts' }),
          )
        },
      })
      installVueSfcLoader()

      const { default: FilesPanel } = await import('./src/components/terminal-session/FilesPanel.vue')
      const centerEvents = []
      const splitEvents = []
      const injectEvents = []
      const countEvents = []
      const openEvents = []

      const view = mountView(defineComponent({
        setup: () => () => h(FilesPanel, {
          sessionId: 'sess-1',
          cwd: '/project',
          mode: 'tree',
          active: true,
          onOpenCenterPreview: (p) => centerEvents.push(p),
          onSplitFile: (p) => splitEvents.push(p),
          onInject: (p) => injectEvents.push(p),
          onPreviewCountChange: (c) => countEvents.push(c),
          onPreviewOpenChange: (o) => openEvents.push(o),
        }),
      }), {})

      const textOf = (n) => n.kind === 'text' ? n.text : n.children.map(textOf).join('')
      const shown = () => textOf(view.host)
      const wait = async (check, label) => {
        const end = Date.now() + 2000
        while (Date.now() < end) {
          await nextTick()
          if (check()) return
          await new Promise((r) => setTimeout(r, 5))
        }
        throw new Error('Timeout waiting for: ' + label)
      }

      const triggerTreeMeasure = () => {
        const treeEl = byTestId(view.host, 'fp-tree')[0]
        if (treeEl) {
          treeEl.clientHeight = 700
          treeEl.scrollTop = 0
          treeEl.focus = () => {}
          treeEl.querySelectorAll = () => []
          for (const obs of resizeObservers) {
            if (obs.el === treeEl) obs.cb([{ contentRect: { width: 320, height: 700 } }])
          }
          fire(treeEl, 'onScroll', {})
        }
      }
      await wait(() => {
        triggerTreeMeasure()
        return byTestId(view.host, 'fp-tree-row-file-1.ts').length === 1
      }, 'tree loaded')

      // Click 12 files -> accumulate 12 tabs (MAX_OPEN_PREVIEWS >= 12) and emit ZERO open-center-preview
      for (let i = 1; i <= 12; i++) {
        fire(byTestId(view.host, \`fp-tree-row-file-\${i}.ts\`)[0], 'onClick', {})
        await wait(() => shown().includes(\`content-of:file-\${i}.ts\`), \`file-\${i} loaded\`)
      }
      assert.equal(centerEvents.length, 0, 'default preferCenterPreview=false must never emit open-center-preview on tree click')
      const tabButtons = findAll(view.host, (n) => String(n.props['data-testid'] || '').startsWith('fp-preview-tab-') && !String(n.props['data-testid']).startsWith('fp-preview-tab-close-') && !String(n.props['data-testid']).startsWith('fp-preview-tabs'))
      assert.equal(tabButtons.length, 12, 'must support at least 12 concurrent workbench tabs')
      assert.equal(openEvents[0], true)
      assert.equal(countEvents.at(-1), 12)

      // Quick exits in header & file actions menu
      fire(byTestId(view.host, 'fp-preview-cite')[0], 'onClick', {})
      fire(byTestId(view.host, 'fp-preview-split')[0], 'onClick', {})
      fire(byTestId(view.host, 'fp-preview-open-center')[0], 'onClick', {})
      assert.deepEqual(injectEvents, ['/project/file-12.ts'])
      assert.deepEqual(splitEvents, ['/project/file-12.ts'])
      assert.deepEqual(centerEvents, ['/project/file-12.ts'])

      // Narrow container (<520px) switches .fp-workspace to is-compact
      const fpObs = resizeObservers.find((obs) => obs.el?.props?.['data-testid'] === 'files-panel-tree')
      assert.ok(fpObs, 'ResizeObserver callback registered on fpRootEl')
      fpObs.cb([{ contentRect: { width: 380 } }])
      await nextTick()
      const workspaceNode = findAll(view.host, (n) => String(n.props.class || '').includes('fp-workspace'))[0]
      assert.ok(String(workspaceNode.props.class || '').includes('is-compact'), 'narrow panel must switch to is-compact')
      fpObs.cb([{ contentRect: { width: 780 } }])
      await nextTick()
      assert.ok(!String(workspaceNode.props.class || '').includes('is-compact'), 'widened panel exits is-compact')

      // Close others -> leaves only active tab (file-12.ts)
      fire(byTestId(view.host, 'fp-preview-tabs-close-others')[0], 'onClick', {})
      await nextTick()
      const afterCloseOthers = findAll(view.host, (n) => String(n.props['data-testid'] || '').startsWith('fp-preview-tab-') && !String(n.props['data-testid']).startsWith('fp-preview-tab-close-') && !String(n.props['data-testid']).startsWith('fp-preview-tabs'))
      assert.equal(afterCloseOthers.length, 1)

      // Open another tab then click 清空 (close-all)
      fire(byTestId(view.host, 'fp-tree-row-file-2.ts')[0], 'onClick', {})
      await wait(() => shown().includes('content-of:file-2.ts'), 'file-2 loaded')
      fire(byTestId(view.host, 'fp-preview-tabs-close-all')[0], 'onClick', {})
      await wait(() => byTestId(view.host, 'fp-preview').length === 0, 'fp-preview closed')
      assert.equal(countEvents.at(-1), 0)
      assert.equal(openEvents.at(-1), false)

      view.unmount()

      // Verify mode="recent" also emits open-file instead of open-center-preview by default
      const recentOpenFiles = []
      const recentCenterEvents = []
      const recentView = mountView(defineComponent({
        setup: () => () => h(FilesPanel, {
          sessionId: 'sess-1',
          cwd: '/project',
          mode: 'recent',
          active: true,
          onOpenFile: (p) => recentOpenFiles.push(p),
          onOpenCenterPreview: (p) => recentCenterEvents.push(p),
        }),
      }), {})
      await wait(() => byTestId(recentView.host, 'fp-recent-recent.md').length === 1, 'recent loaded')
      const recentRowBtn = findAll(byTestId(recentView.host, 'fp-recent-recent.md')[0], (n) => n.tag === 'button' && n.props.title === '/project/recent.md')[0]
      fire(recentRowBtn, 'onClick', {})
      assert.deepEqual(recentOpenFiles, ['/project/recent.md'])
      assert.deepEqual(recentCenterEvents, [])
      recentView.unmount()

      console.log(JSON.stringify({ ok: true }))
      process.exit(0)
    `
    const proc = Bun.spawnSync([process.execPath, '-e', script], {
      cwd: process.cwd(),
      stdout: 'pipe',
      stderr: 'pipe',
    })
    const stderr = new TextDecoder().decode(proc.stderr)
    const stdout = new TextDecoder().decode(proc.stdout)
    expect(stderr).toBe('')
    expect(proc.exitCode).toBe(0)
    expect(JSON.parse(stdout.trim()).ok).toBe(true)
  })
})

describe('REQ-TW-05 · Static guardrails (CliPortal defaults, v-show invariant, no bare var(--accent))', () => {
  it('CliPortal sets prefer-center-preview=false, preserves v-show on CliTerminalView, and collapses occluding drawer in onOpenCenterPreview', () => {
    const cliPortalSrc = readFileSync(join(import.meta.dir, '../../../portals/cli/CliPortal.vue'), 'utf8')
    const termTag = /<CliTerminalView[\s\S]*?\/>/.exec(cliPortalSrc)?.[0] ?? ''
    expect(termTag).toContain('v-show="canvas.showTerminalView"')
    expect(termTag).toContain(':prefer-center-preview="false"')
    expect(cliPortalSrc).toContain('if (activeDrawerOpen.value && (activeDrawerSqueezePx.value === 0 || breakpoint.isMobile.value))')
    expect(cliPortalSrc).toContain('setActiveDrawerOpen(false)')
  })

  it('modified workbench files contain no bare var(--accent, ...) CSS tokens', () => {
    const files = [
      join(import.meta.dir, '../PreviewDocumentTabs.vue'),
      join(import.meta.dir, '../FilesPanel.vue'),
      join(import.meta.dir, '../ResourceDrawer.vue'),
      join(import.meta.dir, '../ReviewPanel.vue'),
      join(import.meta.dir, '../../../portals/cli/CliPortal.vue'),
    ]
    for (const file of files) {
      const src = readFileSync(file, 'utf8')
      expect(src).not.toMatch(/(?<!hsl\()var\(--accent\b/)
    }
  })
})
