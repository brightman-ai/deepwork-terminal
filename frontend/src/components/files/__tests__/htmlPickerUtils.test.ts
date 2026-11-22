import { describe, expect, it } from 'bun:test'
import {
  buildElementSelector,
  formatElementDimensions,
  formatHtmlPickerDraft,
  formatHtmlPickerPayload,
  inspectPickedElement,
  isIgnorablePickerElement,
  locateElementSourceLine,
  resolvePickableElement,
  type PickableElementLike,
} from '../htmlPickerUtils'

function makeEl(
  tagName: string,
  opts: {
    id?: string
    className?: string
    textContent?: string
    outerHTML?: string
    attrs?: Record<string, string>
    rect?: { width: number; height: number }
    parentElement?: PickableElementLike | null
  } = {},
): PickableElementLike {
  return {
    tagName: tagName.toUpperCase(),
    id: opts.id ?? '',
    className: opts.className ?? '',
    textContent: opts.textContent ?? '',
    outerHTML: opts.outerHTML,
    getAttribute: (name: string) => opts.attrs?.[name] ?? null,
    getBoundingClientRect: () => opts.rect ?? { width: 0, height: 0 },
    parentElement: opts.parentElement ?? null,
  }
}

describe('htmlPickerUtils — selector, dimensions & source line resolution', () => {
  it('ignores document chrome elements and picker internal markers', () => {
    expect(isIgnorablePickerElement(null)).toBe(true)
    expect(isIgnorablePickerElement(makeEl('html'))).toBe(true)
    expect(isIgnorablePickerElement(makeEl('script'))).toBe(true)
    expect(
      isIgnorablePickerElement(makeEl('div', { attrs: { 'data-dw-picker-ignore': '1' } })),
    ).toBe(true)
    expect(isIgnorablePickerElement(makeEl('button', { className: 'pay-cta' }))).toBe(false)
  })

  it('builds concise CSS selectors prioritizing id, semantic classes, testid, and nth-of-type', () => {
    expect(buildElementSelector(makeEl('button', { id: 'submit-order' }))).toBe(
      'button#submit-order',
    )
    expect(
      buildElementSelector(
        makeEl('button', { className: 'dw-picker-hover pay-cta primary-btn extra-cls' }),
      ),
    ).toBe('button.pay-cta.primary-btn')
    expect(
      buildElementSelector(makeEl('input', { attrs: { 'data-testid': 'zip-field' } })),
    ).toBe('input[data-testid="zip-field"]')

    const btn1 = makeEl('button', { textContent: 'Cancel' })
    const btn2 = makeEl('button', { textContent: 'Confirm' })
    const footer: PickableElementLike = {
      tagName: 'FOOTER',
      children: [btn1, btn2],
    }
    btn1.parentElement = footer
    btn2.parentElement = footer
    expect(buildElementSelector(btn2)).toBe('footer > button:nth-of-type(2)')
  })

  it('formats rounded element dimensions cleanly', () => {
    expect(formatElementDimensions('button', { width: 259.7, height: 38.2 })).toEqual({
      width: 260,
      height: 38,
      dimensions: 'button 260×38',
    })
    expect(formatElementDimensions('section', null)).toEqual({
      width: 0,
      height: 0,
      dimensions: 'section',
    })
  })

  it('resolves cross-realm iframe elements and text nodes without relying on instanceof HTMLElement/Element', () => {
    class ParentElement {}
    class ParentHTMLElement extends ParentElement {}
    class IframeRealmElement {
      nodeType = 1
      tagName = 'BUTTON'
      className = 'pay-cta'
      textContent = 'Continue to payment'
      getAttribute = () => null
      getBoundingClientRect = () => ({ width: 220, height: 40 })
    }
    class IframeRealmTextNode {
      nodeType = 3
      parentElement: IframeRealmElement
      constructor(parent: IframeRealmElement) {
        this.parentElement = parent
      }
    }

    const g = globalThis as unknown as Record<string, unknown>
    const prevEl = g.Element
    const prevHtmlEl = g.HTMLElement
    g.Element = ParentElement
    g.HTMLElement = ParentHTMLElement
    try {
      const foreignBtn = new IframeRealmElement()
      const foreignText = new IframeRealmTextNode(foreignBtn)
      expect(foreignBtn instanceof ParentElement).toBe(false)
      expect(foreignBtn instanceof ParentHTMLElement).toBe(false)
      expect(resolvePickableElement(foreignBtn)).toBe(foreignBtn)
      expect(resolvePickableElement(foreignText)).toBe(foreignBtn)
      expect(resolvePickableElement({ nodeType: 9 })).toBeNull()
      expect(resolvePickableElement(null)).toBeNull()
    } finally {
      if (prevEl === undefined) delete g.Element
      else g.Element = prevEl
      if (prevHtmlEl === undefined) delete g.HTMLElement
      else g.HTMLElement = prevHtmlEl
    }
  })

  it('locates exact 1-based line numbers in raw HTML source', () => {
    const html = [
      '<!doctype html>',
      '<html>',
      '<body>',
      '  <div class="checkout-card">',
      '    <input id="zip-code" class="zip-input" value="94105" />',
      '    <button class="btn-ghost">Back</button>',
      '    <button class="pay-cta">',
      '      Continue to payment →',
      '    </button>',
      '  </div>',
      '</body>',
      '</html>',
    ].join('\n')

    expect(locateElementSourceLine(html, makeEl('input', { id: 'zip-code' }))).toBe(5)
    expect(
      locateElementSourceLine(
        html,
        makeEl('button', { className: 'pay-cta', textContent: 'Continue to payment →' }),
      ),
    ).toBe(7)
    expect(
      locateElementSourceLine(
        html,
        makeEl('div', { attrs: { 'data-pick-ln': '4' } }),
      ),
    ).toBe(4)
  })
})

describe('htmlPickerUtils — full element inspection & structured annotations', () => {
  const sampleHtml = [
    '<div class="card">',
    '  <label>ZIP</label>',
    '  <input class="zip-input" placeholder="94105" />',
    '  <button class="pay-cta">Continue to payment →</button>',
    '</div>',
  ].join('\n')

  it('inspects element metadata and builds draft + direct-send structured payloads', () => {
    const el = makeEl('button', {
      className: 'pay-cta dw-picker-active',
      textContent: 'Continue to payment →',
      outerHTML:
        '<button class="pay-cta" data-dw-picker-active="1">Continue to payment →</button>',
      rect: { width: 260, height: 38 },
    })

    const picked = inspectPickedElement(el, {
      file: '/repo/tmp/ui-checkout-card.html',
      cwd: '/repo',
      htmlSource: sampleHtml,
      computedStyle: {
        backgroundColor: 'rgb(29, 78, 216)',
        borderRadius: '8px',
      },
    })

    expect(picked.file).toBe('tmp/ui-checkout-card.html')
    expect(picked.line).toBe(4)
    expect(picked.selector).toBe('button.pay-cta')
    expect(picked.dimensions).toBe('button 260×38')
    expect(picked.domSnippet).toBe('<button class="pay-cta">Continue to payment →</button>')
    expect(picked.cssSummary).toContain('background-color: rgb(29, 78, 216)')

    const draft = formatHtmlPickerDraft({
      pick: picked,
      note: '圆角改成 10px 并加深底色对比度',
      includeDom: true,
    })
    expect(draft.refChip).toBe('tmp/ui-checkout-card.html:4')
    expect(draft.draftText).toContain('[🎯 <button.pay-cta> (button 260×38)]')
    expect(draft.draftText).toContain('圆角改成 10px 并加深底色对比度')

    const directPayload = formatHtmlPickerPayload({
      pick: picked,
      note: '圆角改成 10px 并加深底色对比度',
      includeDom: true,
      includeShot: true,
    })
    expect(directPayload).toContain('🎯 **网页拾取批注**')
    expect(directPayload).toContain('`@tmp/ui-checkout-card.html:4`')
    expect(directPayload).toContain('`<button.pay-cta> (button 260×38)`')
    expect(directPayload).toContain('圆角改成 10px 并加深底色对比度')
    expect(directPayload).toContain('DOM: `<button class="pay-cta">Continue to payment →</button>`')
    expect(directPayload).toContain('[视口尺寸参考: 260×38px]')
  })

  it('mounts CenterFilePreview.vue & ReviewPanel.vue and drives picker popover + inline diff note send', () => {
    const script = `
      import assert from 'node:assert/strict'
      import { plugin } from 'bun'
      import { defineComponent, h, nextTick } from 'vue'
      import { installVueSfcLoader } from './src/portals/cli/__tests__/support/sfcLoader'
      import { byTestId, fire, mountView } from './src/portals/cli/__tests__/support/testRenderer'

      const events = new EventTarget()
      const media = { matches: false, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }
      Object.assign(globalThis, {
        window: { location: { search: '', hash: '', pathname: '/' }, innerWidth: 1280, innerHeight: 900, matchMedia: () => media, addEventListener: events.addEventListener.bind(events), removeEventListener: events.removeEventListener.bind(events) },
        document: { visibilityState: 'visible', documentElement: { clientWidth: 1280, clientHeight: 900 }, body: { offsetHeight: 0 }, addEventListener() {}, removeEventListener() {} },
        localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
        ResizeObserver: class { observe() {} unobserve() {} disconnect() {} },
      })

      const postedInputs = []
      const htmlBody = [
        '<!doctype html>',
        '<html><body>',
        '  <input class="zip-input" value="94105" />',
        '  <button class="pay-cta">Continue to payment</button>',
        '</body></html>',
      ].join('\\n')

      globalThis.fetch = async (input, opts) => {
        const url = new URL(String(input), 'http://fixture')
        if (url.pathname.endsWith('/files/raw')) {
          return new Response(htmlBody, { headers: { 'Content-Type': 'text/plain' } })
        }
        if (url.pathname.endsWith('/git/diff')) {
          return Response.json({
            root: '/repo',
            notGit: false,
            noCwd: false,
            truncated: false,
            files: [{
              status: 'M',
              path: 'src/pay.ts',
              added: 1,
              deleted: 0,
              diff: '@@ -10,2 +10,3 @@\\n const x = 1\\n+const cta = "pay"\\n return x',
            }],
          })
        }
        if (url.pathname.includes('/sessions/') && url.pathname.endsWith('/input')) {
          postedInputs.push({ url: url.pathname, body: String(opts?.body ?? '') })
          return new Response(null, { status: 204 })
        }
        return Response.json({})
      }

      plugin({
        name: 'stub-file-preview',
        setup(build) {
          build.onLoad({ filter: new RegExp('/FilePreview\\\\.vue$') }, () => ({
            contents: 'import { defineComponent, h } from "vue"; export default defineComponent({ setup: () => () => h("div") })',
            loader: 'ts',
          }))
        },
      })
      installVueSfcLoader()

      const { default: CenterFilePreview } = await import('./src/components/files/CenterFilePreview.vue')
      const { default: ReviewPanel } = await import('./src/components/terminal-session/ReviewPanel.vue')

      let previewInst = null
      const splits = []
      const sentAnnotations = []
      const previewView = mountView(defineComponent({
        setup: () => () => h(CenterFilePreview, {
          ref: (v) => { previewInst = v },
          path: '/repo/tmp/ui-checkout-card.html',
          sessionId: 'sess-main',
          cwd: '/repo',
          onSplitFile: (p) => splits.push(p),
          onSendAnnotation: (ev) => sentAnnotations.push(ev),
        }),
      }), {})

      const wait = async (fn, label = 'step') => {
        const deadline = Date.now() + 1200
        while (Date.now() < deadline) {
          await nextTick()
          if (fn()) return
          await new Promise((r) => setTimeout(r, 4))
        }
        throw new Error('timeout waiting at: ' + label)
      }

      await wait(() => byTestId(previewView.host, 'btn-toggle-html-picker').length === 1, 'picker-btn')
      await wait(() => byTestId(previewView.host, 'center-preview-html-frame').length === 1, 'html-frame')
      fire(byTestId(previewView.host, 'btn-split-file-preview')[0], 'onClick', {})
      assert.deepEqual(splits, ['/repo/tmp/ui-checkout-card.html'])

      fire(byTestId(previewView.host, 'btn-toggle-html-picker')[0], 'onClick', {})
      await nextTick()
      const frame = byTestId(previewView.host, 'center-preview-html-frame')[0]
      assert.equal(frame.props.sandbox, 'allow-same-origin')

      // Simulate a real cross-realm iframe document where elements do NOT inherit from globalThis.Element
      class ParentElement {}
      class ParentHTMLElement extends ParentElement {}
      globalThis.Element = ParentElement
      globalThis.HTMLElement = ParentHTMLElement

      const docListeners = {}
      const attrs = new Map()
      const foreignBtn = {
        nodeType: 1,
        tagName: 'BUTTON',
        className: 'pay-cta',
        textContent: 'Continue to payment',
        outerHTML: '<button class="pay-cta">Continue to payment</button>',
        getAttribute: (k) => attrs.get(k) ?? null,
        setAttribute: (k, v) => attrs.set(k, String(v)),
        removeAttribute: (k) => attrs.delete(k),
        getBoundingClientRect: () => ({ width: 260, height: 38 }),
      }
      assert.equal(foreignBtn instanceof globalThis.Element, false)
      assert.equal(foreignBtn instanceof globalThis.HTMLElement, false)

      const fakeIframeDoc = {
        readyState: 'complete',
        getElementById: () => null,
        createElement: () => ({ setAttribute() {}, textContent: '' }),
        head: { appendChild() {} },
        defaultView: { getComputedStyle: () => ({ backgroundColor: 'rgb(29, 78, 216)' }) },
        addEventListener: (type, fn) => { docListeners[type] = fn },
        removeEventListener: (type, fn) => { if (docListeners[type] === fn) delete docListeners[type] },
      }
      frame.contentDocument = fakeIframeDoc
      fire(frame, 'onLoad', {})
      await nextTick()

      // Hover on cross-realm text node inside foreignBtn -> highlights foreignBtn
      docListeners.mousemove({ target: { nodeType: 3, parentElement: foreignBtn } })
      assert.equal(attrs.get('data-dw-picker-hover'), '1')

      // Click on cross-realm foreignBtn -> sets active attr & opens inline popover
      let prevented = false
      let stopped = false
      docListeners.click({
        target: foreignBtn,
        preventDefault() { prevented = true },
        stopPropagation() { stopped = true },
      })
      assert.equal(prevented, true)
      assert.equal(stopped, true)
      assert.equal(attrs.get('data-dw-picker-active'), '1')

      await wait(() => byTestId(previewView.host, 'design-pick-popover').length === 1, 'popover-open')
      const noteInput = byTestId(previewView.host, 'dpp-note-input')[0]
      if (noteInput.props['onUpdate:modelValue']) {
        fire(noteInput, 'onUpdate:modelValue', '请改成深色圆角按钮')
      } else {
        noteInput.value = '请改成深色圆角按钮'
        fire(noteInput, 'onInput', { target: noteInput })
      }
      await nextTick()
      fire(byTestId(previewView.host, 'dpp-send-now')[0], 'onClick', {})
      await wait(() => sentAnnotations.length === 1, 'send-annotation')
      assert(sentAnnotations[0].text.includes('@tmp/ui-checkout-card.html:4'))
      assert(sentAnnotations[0].text.includes('<button.pay-cta>'))
      assert(sentAnnotations[0].text.includes('请改成深色圆角按钮'))
      assert(postedInputs.some((p) => p.url.includes('/sessions/sess-main/input')))

      // Re-click to verify ↳ 入框 appends draft ONCE (F1 guard)
      const { useComposeLenses } = await import('./src/composables/cli/composeLenses')
      const sessLenses = useComposeLenses(() => 'sess-main')
      sessLenses.sharedDraft.value = ''
      docListeners.click({ target: foreignBtn, preventDefault() {}, stopPropagation() {} })
      await wait(() => byTestId(previewView.host, 'dpp-to-draft').length === 1, 'to-draft-btn')
      fire(byTestId(previewView.host, 'dpp-to-draft')[0], 'onClick', {})
      await nextTick()
      const occurrences = sessLenses.sharedDraft.value.split('[🎯 <button.pay-cta>').length - 1
      assert.equal(occurrences, 1)
      previewView.unmount()

      // Mount ReviewPanel and verify inline diff note editor + ✨ 发送批注
      const sentNotes = []
      const rpView = mountView(defineComponent({
        setup: () => () => h(ReviewPanel, {
          sessionId: 'sess-main',
          cwd: '/repo',
          active: true,
          onSendNotes: (ev) => sentNotes.push(ev),
        }),
      }), {})

      await wait(() => byTestId(rpView.host, 'rp-file-src/pay.ts').length === 1, 'rp-file')
      const fileHeaderBtn = byTestId(rpView.host, 'rp-file-src/pay.ts')[0].children[0].children[0]
      fire(fileHeaderBtn, 'onClick', {})
      await wait(() => byTestId(rpView.host, 'rp-diff-line-src/pay.ts-2').length === 1, 'rp-diff-line')
      fire(byTestId(rpView.host, 'rp-diff-line-src/pay.ts-2')[0], 'onClick', {})
      await wait(() => byTestId(rpView.host, 'rp-inline-note-editor').length === 1, 'rp-note-editor')
      const rpNoteInput = byTestId(rpView.host, 'rp-note-input')[0]
      if (rpNoteInput.props['onUpdate:modelValue']) {
        fire(rpNoteInput, 'onUpdate:modelValue', '提取为常量避免硬编码')
      } else {
        rpNoteInput.value = '提取为常量避免硬编码'
        fire(rpNoteInput, 'onInput', { target: rpNoteInput })
      }
      await nextTick()
      fire(byTestId(rpView.host, 'rp-note-save')[0], 'onClick', {})
      await wait(() => byTestId(rpView.host, 'rp-send-notes').length === 1, 'rp-send-notes-btn')
      fire(byTestId(rpView.host, 'rp-send-notes')[0], 'onClick', {})
      await wait(() => sentNotes.length === 1, 'rp-sent')
      assert(sentNotes[0].text.includes('@src/pay.ts:11'))
      assert(sentNotes[0].text.includes('提取为常量避免硬编码'))
      rpView.unmount()

      console.log(JSON.stringify({ ok: true }))
      process.exit(0)
    `
    const proc = Bun.spawnSync([process.execPath, '-e', script], {
      cwd: process.cwd(),
      stdout: 'pipe',
      stderr: 'pipe',
    })
    const stderr = new TextDecoder().decode(proc.stderr)
    expect(stderr).toBe('')
    expect(proc.exitCode).toBe(0)
    expect(JSON.parse(new TextDecoder().decode(proc.stdout).trim())).toEqual({ ok: true })
  })
})
