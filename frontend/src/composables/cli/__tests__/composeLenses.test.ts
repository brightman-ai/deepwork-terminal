import { beforeEach, describe, expect, it } from 'bun:test'
import { ref } from 'vue'
import {
  BUILTIN_PHRASES,
  UNDO_CLEAR_TTL_MS,
  appendToSessionDraft,
  composePayloadWithLenses,
  filterPhrases,
  insertAtCursorRange,
  normalizeRefChip,
  resetComposeLensesStoreForTests,
  shouldOpenSlashArsenal,
  useComposeLenses,
} from '../composeLenses'

describe('composeLenses — BUILTIN_PHRASES & filtering', () => {
  it('ships both lens (叠加透镜) and tpl (动作模板) builtin phrases', () => {
    const lenses = BUILTIN_PHRASES.filter((p) => p.kind === 'lens')
    const templates = BUILTIN_PHRASES.filter((p) => p.kind === 'tpl')
    expect(lenses.length).toBeGreaterThanOrEqual(3)
    expect(templates.length).toBeGreaterThanOrEqual(2)
    expect(lenses.every((p) => p.pinned)).toBe(true)
    expect(lenses.map((p) => p.id)).toEqual(['lens-ssot', 'lens-2hyp', 'lens-journey'])
  })

  it('filters phrases by category and case-insensitive search query', () => {
    expect(filterPhrases(BUILTIN_PHRASES, 'lens').every((p) => p.kind === 'lens')).toBe(true)
    expect(filterPhrases(BUILTIN_PHRASES, 'tpl').every((p) => p.kind === 'tpl')).toBe(true)
    const ssotHits = filterPhrases(BUILTIN_PHRASES, 'all', 'ssot')
    expect(ssotHits.map((p) => p.id)).toEqual(['lens-ssot'])
  })
})

describe('composeLenses — cursor-range template insertion & slash trigger', () => {
  it('inserts template at cursor range without overwriting surrounding draft', () => {
    const original = '先检查上传限额逻辑，再补单测'
    const pos = original.indexOf('，')
    const res = insertAtCursorRange(original, pos, pos, '【核对点】')
    expect(res.text).toBe('先检查上传限额逻辑 【核对点】 ，再补单测')
    expect(res.text.slice(0, res.cursor)).toBe('先检查上传限额逻辑 【核对点】')
  })

  it('replaces only the selected [start, end) range without double spaces', () => {
    const original = '请针对 [TODO] 输出结论'
    const start = original.indexOf('[TODO]')
    const end = start + '[TODO]'.length
    const res = insertAtCursorRange(original, start, end, '并发竞态边界')
    expect(res.text).toBe('请针对 并发竞态边界 输出结论')
    expect(res.text).not.toContain('  ')
  })

  it('clamps out-of-bounds cursor indices safely', () => {
    const res = insertAtCursorRange('abc', -10, 999, '替换段')
    expect(res.text).toBe('abc' ? '替换段' : '')
    const appendRes = insertAtCursorRange('abc', 50, 99, '尾部')
    expect(appendRes.text).toBe('abc 尾部')
  })

  it('detects standalone or post-whitespace / trigger without false-firing on file paths', () => {
    expect(shouldOpenSlashArsenal('/')).toBe(true)
    expect(shouldOpenSlashArsenal('请重构 /')).toBe(true)
    expect(shouldOpenSlashArsenal('第一行\n/')).toBe(true)
    expect(shouldOpenSlashArsenal('frontend/src/App.vue')).toBe(false)
    expect(shouldOpenSlashArsenal('/api/files/tree')).toBe(false)
  })
})

describe('composeLenses — non-destructive lenses, @file:line refChips & 6000ms undoClear', () => {
  beforeEach(() => {
    resetComposeLensesStoreForTests()
  })

  it('mounts and unmounts stackable lenses without mutating user draft text', () => {
    const sid = ref('sess-alpha')
    const lenses = useComposeLenses(() => sid.value)
    lenses.sharedDraft.value = '修复分屏切换时的焦点闪烁'

    lenses.toggleLens('lens-ssot')
    lenses.toggleLens('lens-2hyp')

    // Draft stays 100% clean
    expect(lenses.sharedDraft.value).toBe('修复分屏切换时的焦点闪烁')
    expect(lenses.activeLenses.value).toEqual(['lens-ssot', 'lens-2hyp'])

    const payload = lenses.assemblePayload()
    expect(payload).toContain('修复分屏切换时的焦点闪烁')
    expect(payload).toContain('【架构约束】')
    expect(payload).toContain('【排障约束】')
    expect(payload.indexOf('【架构约束】')).toBeLessThan(payload.indexOf('【排障约束】'))

    // Unmounting removes the lens prompt while keeping draft intact
    lenses.toggleLens('lens-ssot')
    expect(lenses.sharedDraft.value).toBe('修复分屏切换时的焦点闪烁')
    expect(lenses.assemblePayload()).not.toContain('【架构约束】')
    expect(lenses.assemblePayload()).toContain('【排障约束】')
  })

  it('manages @file:line refChips with deduplication and strips trailing / when inserting tpl', () => {
    const lenses = useComposeLenses(() => 'sess-beta')
    expect(normalizeRefChip('@@src/App.vue:42 ')).toBe('src/App.vue:42')

    lenses.addRefChip('@src/App.vue:42')
    lenses.addRefChip('src/App.vue:42')
    lenses.addRefChip('internal/server.go:88')
    expect(lenses.refChips.value).toEqual(['src/App.vue:42', 'internal/server.go:88'])

    const tpl = BUILTIN_PHRASES.find((p) => p.id === 'tpl-counterex')!
    const inserted = lenses.insertTemplateAtRange('请检查 /', 5, 5, tpl)
    expect(inserted.text).not.toContain(' / ')
    expect(inserted.text).toContain('请检查')
    expect(inserted.text).toContain(tpl.prompt)

    const payload = composePayloadWithLenses(
      '重点核查渲染分支 @src/App.vue:42',
      [],
      lenses.refChips.value,
    )
    // @src/App.vue:42 is already in draft so only @internal/server.go:88 is prefixed
    expect(payload.match(/@src\/App\.vue:42/g)?.length).toBe(1)
    expect(payload).toContain('@internal/server.go:88')

    lenses.removeRefChip('internal/server.go:88')
    expect(lenses.refChips.value).toEqual(['src/App.vue:42'])
  })

  it('isolates state per session and restores draft + lenses + refChips within 6000ms undo window', async () => {
    expect(UNDO_CLEAR_TTL_MS).toBe(6000)

    const s1 = useComposeLenses(() => 'sess-1')
    const s2 = useComposeLenses(() => 'sess-2')

    appendToSessionDraft('sess-1', '重构工作树删除门控', 'worktree.go:31')
    s1.toggleLens('lens-journey')
    appendToSessionDraft('sess-2', '另一会话草稿', 'bots.go:12')

    expect(s1.sharedDraft.value).toBe('重构工作树删除门控')
    expect(s2.sharedDraft.value).toBe('另一会话草稿')

    const backup = s1.clearWithUndo()
    expect(backup).not.toBeNull()
    expect(s1.sharedDraft.value).toBe('')
    expect(s1.activeLenses.value).toEqual([])
    expect(s1.refChips.value).toEqual([])
    expect(s1.hasUndoBackup.value).toBe(true)
    // sess-2 is untouched
    expect(s2.sharedDraft.value).toBe('另一会话草稿')
    expect(s2.refChips.value).toEqual(['bots.go:12'])

    const restored = s1.undoClear()
    expect(restored?.draft).toBe('重构工作树删除门控')
    expect(s1.sharedDraft.value).toBe('重构工作树删除门控')
    expect(s1.activeLenses.value).toEqual(['lens-journey'])
    expect(s1.refChips.value).toEqual(['worktree.go:31'])
    expect(s1.hasUndoBackup.value).toBe(false)

    // Verify TTL auto-expiry with short custom TTL
    s1.clearWithUndo(undefined, 25)
    expect(s1.hasUndoBackup.value).toBe(true)
    await new Promise((resolve) => setTimeout(resolve, 40))
    expect(s1.hasUndoBackup.value).toBe(false)
    expect(s1.undoClear()).toBeNull()
  })

  it('mounts SessionTranscriptView.vue & ComposeBarDesktop.vue sharing lenses, draft, 6s undo, and transcript API', () => {
    const script = `
      import assert from 'node:assert/strict'
      import { plugin } from 'bun'
      import { defineComponent, h, nextTick } from 'vue'
      import { installVueSfcLoader } from './src/portals/cli/__tests__/support/sfcLoader'
      import { byTestId, findAll, fire, mountView } from './src/portals/cli/__tests__/support/testRenderer'

      plugin({
        name: 'stub-terminal-focus',
        setup(build) {
          build.onLoad({ filter: /terminalFocus\.ts$/ }, () => ({
            contents: 'export const guardTerminalElementFocus = () => () => {}',
            loader: 'ts',
          }))
        },
      })

      const store = new Map()
      const events = new EventTarget()
      Object.assign(globalThis, {
        window: { location: { search: '', hash: '', pathname: '/' }, innerWidth: 1280, innerHeight: 900, addEventListener: events.addEventListener.bind(events), removeEventListener: events.removeEventListener.bind(events) },
        document: { visibilityState: 'visible', activeElement: null, hasFocus: () => true, documentElement: { clientWidth: 1280, clientHeight: 900 }, body: { offsetHeight: 0 }, addEventListener() {}, removeEventListener() {} },
        localStorage: {
          getItem: (k) => store.get(k) ?? null,
          setItem: (k, v) => store.set(k, String(v)),
          removeItem: (k) => store.delete(k),
        },
        ResizeObserver: class { observe() {} unobserve() {} disconnect() {} },
        requestAnimationFrame: (f) => setTimeout(f, 0),
        cancelAnimationFrame: clearTimeout,
        getComputedStyle: () => ({ lineHeight: '20px', paddingTop: '8px', paddingBottom: '8px', transitionDelay: '0s', transitionDuration: '0s', animationDelay: '0s', animationDuration: '0s' }),
      })
      window.getComputedStyle = globalThis.getComputedStyle

      const posted = []
      globalThis.fetch = async (input, opts) => {
        const url = new URL(String(input), 'http://fixture')
        if (url.pathname.endsWith('/sessions/sess-chat/transcript')) {
          return Response.json({
            sessionId: 'sess-chat',
            cwd: '/repo',
            agentTool: 'claude',
            turns: [
              { id: 't1', role: 'user', text: '排查上传断流' },
              { id: 't2', role: 'assistant', text: '定位到分片重试节流逻辑', files: ['upload_pacing.go:24'] },
            ],
          })
        }
        if (url.pathname.endsWith('/sessions/sess-chat/input')) {
          posted.push(String(opts?.body ?? ''))
          return new Response(null, { status: 204 })
        }
        return Response.json({})
      }

      installVueSfcLoader()
      const { default: SessionTranscriptView } = await import('./src/components/cli/SessionTranscriptView.vue')
      const { default: ComposeBarDesktop } = await import('./src/components/terminal-session/ComposeBarDesktop.vue')
      const { appendToSessionDraft } = await import('./src/composables/cli/composeLenses')

      const openedFiles = []
      const chatSent = []
      const cliSent = []

      const chatView = mountView(defineComponent({
        setup: () => () => h(SessionTranscriptView, {
          sessionId: 'sess-chat',
          tabTitle: 'Main Claude',
          pollIntervalMs: 0,
          onOpenFile: (f) => openedFiles.push(f),
          onSend: (t) => chatSent.push(t),
        }),
      }), {})

      const cliView = mountView(defineComponent({
        setup: () => () => h(ComposeBarDesktop, {
          sessionId: 'sess-chat',
          onSend: (t) => cliSent.push(t),
        }),
      }), {})
      findAll(cliView.host, (n) => n.tag === 'textarea').forEach((n) => {
        n.focus = () => {}
        n.setSelectionRange = () => {}
        n.value = ''
      })
      findAll(chatView.host, (n) => n.tag === 'textarea').forEach((n) => {
        n.focus = () => {}
        n.setSelectionRange = () => {}
      })

      const wait = async (fn) => {
        const deadline = Date.now() + 1500
        while (Date.now() < deadline) {
          await nextTick()
          if (fn()) return
          await new Promise((r) => setTimeout(r, 4))
        }
        throw new Error('timeout waiting in transcript/compose fixture')
      }

      await wait(() => byTestId(chatView.host, 'transcript-file-pill-upload_pacing.go:24').length === 1)
      fire(byTestId(chatView.host, 'transcript-file-pill-upload_pacing.go:24')[0], 'onClick', {})
      assert.deepEqual(openedFiles, [{ path: 'upload_pacing.go', line: 24 }])

      // Mount lens in ComposeBarDesktop -> immediately reflected in SessionTranscriptView
      fire(byTestId(cliView.host, 'compose-lens-pill-lens-ssot')[0], 'onClick', {})
      appendToSessionDraft('sess-chat', '请补齐边界单测', 'upload_pacing.go:24')
      await wait(() => byTestId(chatView.host, 'transcript-active-lens-lens-ssot').length === 1)
      assert.equal(byTestId(cliView.host, 'compose-ref-chip-upload_pacing.go:24').length, 1)

      // Clear from ComposeBarDesktop -> 6s undo bar appears -> undo restores both draft & lens
      fire(byTestId(cliView.host, 'compose-desktop-clear-all')[0], 'onClick', {})
      await wait(() => byTestId(cliView.host, 'compose-desktop-undo-clear-bar').length === 1)
      fire(byTestId(cliView.host, 'compose-desktop-undo-clear')[0], 'onClick', {})
      await wait(() => byTestId(cliView.host, 'compose-active-lens-lens-ssot').length === 1)

      // Send from SessionTranscriptView -> sends assembled payload to /sessions/sess-chat/input
      fire(byTestId(chatView.host, 'transcript-send-btn')[0], 'onClick', {})
      await wait(() => chatSent.length === 1)
      assert(chatSent[0].includes('@upload_pacing.go:24'))
      assert(chatSent[0].includes('请补齐边界单测'))
      assert(chatSent[0].includes('【架构约束】'))
      assert(posted.some((b) => b.includes('【架构约束】')))

      chatView.unmount()
      cliView.unmount()
      console.log(JSON.stringify({ ok: true }))
      process.exit(0)
    `
    const proc = Bun.spawnSync([process.execPath, '-e', script], {
      cwd: process.cwd(),
      stdout: 'pipe',
      stderr: 'pipe',
    })
    const stderr = new TextDecoder().decode(proc.stderr)
    expect(stderr).not.toContain('Error:')
    expect(proc.exitCode).toBe(0)
    expect(JSON.parse(new TextDecoder().decode(proc.stdout).trim())).toEqual({ ok: true })
  })
})
