<script setup lang="ts">
/**
 * CenterFilePreview — Full-width / full-height center file reader for the single reused
 * italic Preview Tab (`*📄 filename ✕*`) and split-file comparison pane (REQ-TW-05).
 *
 * Features:
 *  - `@ 引用到对话` (`data-testid="btn-cite-file-preview"`): attaches `@file:line` chip to composer
 *  - `◫ 分屏边聊边看` (`data-testid="btn-split-file-preview"`): emits `split-file` so parent opens
 *    side-by-side comparison and collapses the right file drawer (`≤3` columns invariant)
 *  - `.html` / `.htm` render mode + `🎯 开启拾取` (`data-testid="btn-toggle-html-picker"`):
 *    switches iframe to `sandbox="allow-same-origin"` (without `allow-scripts`) so untrusted JS
 *    cannot execute while parent inspects real DOM elements and pops up inline `design-pick-popover`
 *    (`data-testid="design-pick-popover"`, `↳ 入框`, `直接发送 ↵` `data-testid="dpp-send-now"`).
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { RefreshCw, ExternalLink } from 'lucide-vue-next'
import {
  filesRaw,
  filesRawRenderUrl,
  filesDownloadUrl,
  type RawResult,
} from '@terminal/api/files'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { appendToSessionDraft } from '@terminal/composables/cli/composeLenses'
import FilePreview from '@terminal/components/terminal-session/FilePreview.vue'
import {
  splitLocalHtmlReference,
  appendHtmlPreviewSuffix,
  htmlPreviewRevision,
} from '@terminal/components/terminal-session/localHtmlReference'
import {
  formatHtmlPickerDraft,
  formatHtmlPickerPayload,
  inspectPickedElement,
  isIgnorablePickerElement,
  normalizeDisplayFilePath,
  resolvePickableElement,
  type PickableElementLike,
  type PickedElementInfo,
} from './htmlPickerUtils'

export interface CenterPreviewTargetSession {
  id: string
  sessionId: string
  title: string
  isPrimary?: boolean
  httpBase?: string
  authToken?: string
  isRemote?: boolean
}

const props = withDefaults(
  defineProps<{
    path: string
    sessionId: string
    cwd?: string
    line?: number
    mode?: 'preview-tab' | 'split-pane' | 'mobile-full'
    targetSessions?: CenterPreviewTargetSession[]
    defaultTargetSessionId?: string
    initialText?: string
    active?: boolean
    autoPostInput?: boolean
  }>(),
  {
    cwd: '',
    mode: 'preview-tab',
    targetSessions: () => [],
    active: true,
    autoPostInput: true,
  },
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'split-file', path: string): void
  (e: 'promote-full', path: string): void
  (e: 'cite-file', payload: { path: string; line: number; refChip: string; targetSessionId: string }): void
  (
    e: 'draft-annotation',
    payload: { targetSessionId: string; draftText: string; refChip: string; pick: PickedElementInfo },
  ): void
  (
    e: 'send-annotation',
    payload: { targetSessionId: string; text: string; pick: PickedElementInfo },
  ): void
  (e: 'navigate', absPath: string): void
}>()

const loading = ref(false)
const revision = ref(0)
const rawResult = ref<RawResult>({ kind: 'text', text: props.initialText ?? '' })
const citedLine = ref<number>(props.line && props.line >= 1 ? props.line : 1)
const htmlRenderMode = ref<'render' | 'source'>('render')
const pickerEnabled = ref(false)
const hoveredLabel = ref('')
const activePick = ref<PickedElementInfo | null>(null)
const pickNote = ref('')
const includeDom = ref(true)
const includeShot = ref(true)
const selectedTargetSessionId = ref('')
const sendingNow = ref(false)
const toastText = ref('')
const htmlIframeRef = ref<HTMLIFrameElement | null>(null)
const dppNoteRef = ref<HTMLTextAreaElement | null>(null)

let fetchAbort: AbortController | null = null
let detachIframeListeners: (() => void) | null = null
let toastTimer: ReturnType<typeof setTimeout> | null = null

const parsedRef = computed(() => splitLocalHtmlReference(props.path || ''))
const cleanPath = computed(() => parsedRef.value.path)
const htmlSuffix = computed(() => parsedRef.value.suffix)
const fileName = computed(() => cleanPath.value.split('/').pop() || cleanPath.value || 'preview')
const displayPath = computed(() => normalizeDisplayFilePath(cleanPath.value, props.cwd))
const isHtmlFile = computed(() => /\.html?$/i.test(fileName.value))

const rawText = computed(() => (rawResult.value.kind === 'text' ? rawResult.value.text : ''))

const targetOptions = computed<CenterPreviewTargetSession[]>(() => {
  if (props.targetSessions.length > 0) return props.targetSessions
  if (!props.sessionId) return []
  return [
    {
      id: props.sessionId,
      sessionId: props.sessionId,
      title: '当前终端会话',
      isPrimary: true,
    },
  ]
})

const resolvedTargetSessionId = computed(() => {
  if (
    selectedTargetSessionId.value &&
    targetOptions.value.some((t) => t.sessionId === selectedTargetSessionId.value)
  ) {
    return selectedTargetSessionId.value
  }
  return (
    props.defaultTargetSessionId ||
    targetOptions.value[0]?.sessionId ||
    props.sessionId ||
    ''
  )
})

watch(
  [() => props.defaultTargetSessionId, () => props.sessionId, targetOptions],
  () => {
    if (!selectedTargetSessionId.value && resolvedTargetSessionId.value) {
      selectedTargetSessionId.value = resolvedTargetSessionId.value
    }
  },
  { immediate: true },
)

const htmlFrameSrc = computed(() => {
  if (!isHtmlFile.value || !props.sessionId) return ''
  const base = filesRawRenderUrl(props.sessionId, cleanPath.value, props.cwd || undefined)
  if (!base) return ''
  const withSuffix = appendHtmlPreviewSuffix(base, htmlSuffix.value)
  return htmlPreviewRevision(withSuffix, revision.value)
})

// Defense-in-depth fallback only (must NOT be relied on without sandbox):
// actual script isolation is enforced by `sandbox="allow-same-origin"` (omitting `allow-scripts`).
const staticSafeSrcdoc = computed(() => {
  if (!rawText.value) return ''
  return rawText.value.replace(/<script\b[\s\S]*?<\/script>/gi, '')
})

function showToast(msg: string): void {
  toastText.value = msg
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    toastText.value = ''
  }, 2400)
}

function releaseObjectUrl(res: RawResult): void {
  if (res.kind === 'image') {
    try {
      URL.revokeObjectURL(res.url)
    } catch {}
  }
}

async function loadFile(): Promise<void> {
  if (props.initialText !== undefined && !props.sessionId) {
    rawResult.value = { kind: 'text', text: props.initialText }
    return
  }
  if (!cleanPath.value || !props.sessionId) return

  fetchAbort?.abort()
  const controller = new AbortController()
  fetchAbort = controller
  loading.value = true
  try {
    const res = await filesRaw(
      props.sessionId,
      cleanPath.value,
      props.cwd || undefined,
      controller.signal,
    )
    if (controller.signal.aborted || fetchAbort !== controller) {
      releaseObjectUrl(res)
      return
    }
    releaseObjectUrl(rawResult.value)
    rawResult.value = res
    revision.value += 1
  } finally {
    if (fetchAbort === controller) {
      fetchAbort = null
      loading.value = false
    }
  }
}

watch(
  [cleanPath, () => props.sessionId, () => props.cwd],
  () => {
    citedLine.value = props.line && props.line >= 1 ? props.line : 1
    activePick.value = null
    hoveredLabel.value = ''
    pickerEnabled.value = false
    htmlRenderMode.value = isHtmlFile.value ? 'render' : 'source'
    void loadFile()
  },
  { immediate: true },
)

watch(
  () => props.line,
  (ln) => {
    if (ln && ln >= 1) citedLine.value = ln
  },
)

function cleanupIframePicker(): void {
  detachIframeListeners?.()
  detachIframeListeners = null
  hoveredLabel.value = ''
}

function toggleHtmlRenderMode(): void {
  if (htmlRenderMode.value === 'render') {
    htmlRenderMode.value = 'source'
    pickerEnabled.value = false
    activePick.value = null
    cleanupIframePicker()
  } else {
    htmlRenderMode.value = 'render'
  }
}

function toggleHtmlPicker(): void {
  if (!isHtmlFile.value) return
  if (htmlRenderMode.value !== 'render') {
    htmlRenderMode.value = 'render'
    pickerEnabled.value = true
    return
  }
  pickerEnabled.value = !pickerEnabled.value
  if (!pickerEnabled.value) {
    activePick.value = null
    cleanupIframePicker()
  }
}

function attachIframePicker(): void {
  cleanupIframePicker()
  if (!pickerEnabled.value) return
  const frame = htmlIframeRef.value
  let doc: Document | null = null
  try {
    doc = frame?.contentDocument ?? null
  } catch {
    doc = null
  }
  if (!doc) return

  // Inject lightweight hover/active visual affordance inside same-origin static preview doc
  try {
    if (!doc.getElementById('dw-html-picker-style')) {
      const styleEl = doc.createElement('style')
      styleEl.id = 'dw-html-picker-style'
      styleEl.setAttribute('data-dw-picker-ignore', '1')
      styleEl.textContent = `
        * { cursor: crosshair !important; }
        [data-dw-picker-hover="1"] {
          outline: 2px dashed #38bdf8 !important;
          outline-offset: 2px !important;
        }
        [data-dw-picker-active="1"] {
          outline: 2px solid #a855f7 !important;
          outline-offset: 2px !important;
          box-shadow: 0 0 0 4px rgba(168, 85, 247, 0.2) !important;
        }
      `
      ;(doc.head || doc.documentElement)?.appendChild(styleEl)
    }
  } catch {
    // Ignore style injection errors on restricted documents
  }

  let currentHoverEl: Element | null = null
  let currentActiveEl: Element | null = null

  const clearHover = () => {
    if (currentHoverEl) {
      currentHoverEl.removeAttribute('data-dw-picker-hover')
      currentHoverEl = null
    }
    hoveredLabel.value = ''
  }

  const onMouseMove = (ev: MouseEvent) => {
    const target = resolvePickableElement(ev.target) as (Element & PickableElementLike) | null
    if (!target || isIgnorablePickerElement(target)) {
      clearHover()
      return
    }
    if (target !== currentHoverEl) {
      clearHover()
      currentHoverEl = target
      currentHoverEl.setAttribute('data-dw-picker-hover', '1')
      const info = inspectPickedElement(target, {
        file: cleanPath.value,
        cwd: props.cwd,
        htmlSource: rawText.value,
      })
      hoveredLabel.value = `<${info.selector}> · ${info.dimensions} · @${info.file}:${info.line}`
    }
  }

  const onMouseLeave = () => {
    clearHover()
  }

  const onClick = (ev: MouseEvent) => {
    const target = resolvePickableElement(ev.target) as (Element & PickableElementLike) | null
    if (!target || isIgnorablePickerElement(target)) return
    ev.preventDefault()
    ev.stopPropagation()

    if (currentActiveEl) {
      currentActiveEl.removeAttribute('data-dw-picker-active')
    }
    currentActiveEl = target
    currentActiveEl.setAttribute('data-dw-picker-active', '1')

    let computedStyle: CSSStyleDeclaration | null = null
    try {
      computedStyle = doc?.defaultView?.getComputedStyle(target) ?? null
    } catch {
      computedStyle = null
    }

    const picked = inspectPickedElement(target, {
      file: cleanPath.value,
      cwd: props.cwd,
      htmlSource: rawText.value,
      computedStyle,
    })

    activePick.value = picked
    citedLine.value = picked.line
    pickNote.value = ''
    selectedTargetSessionId.value = resolvedTargetSessionId.value
    void nextTick(() => {
      dppNoteRef.value?.focus?.()
    })
  }

  doc.addEventListener('mousemove', onMouseMove, true)
  doc.addEventListener('mouseleave', onMouseLeave, true)
  doc.addEventListener('click', onClick, true)

  detachIframeListeners = () => {
    clearHover()
    if (currentActiveEl) {
      currentActiveEl.removeAttribute('data-dw-picker-active')
      currentActiveEl = null
    }
    doc?.removeEventListener('mousemove', onMouseMove, true)
    doc?.removeEventListener('mouseleave', onMouseLeave, true)
    doc?.removeEventListener('click', onClick, true)
  }
}

watch(pickerEnabled, (on) => {
  if (!on) {
    cleanupIframePicker()
    return
  }
  void nextTick(() => {
    try {
      if (htmlIframeRef.value?.contentDocument?.readyState === 'complete') {
        attachIframePicker()
      }
    } catch {
      // Wait for @load if iframe is not yet accessible
    }
  })
})

function onReaderClickCapture(ev: MouseEvent): void {
  const target = resolvePickableElement(ev.target) as (Element & { closest?: (s: string) => Element | null }) | null
  const lineRow = target?.closest?.('[data-source-line]')
  if (lineRow) {
    const ln = Number(lineRow.getAttribute('data-source-line'))
    if (Number.isSafeInteger(ln) && ln >= 1) {
      citedLine.value = ln
    }
  }
}

function citeCurrentFile(): void {
  const targetSid = resolvedTargetSessionId.value
  const refChip = `${displayPath.value}:${citedLine.value}`
  appendToSessionDraft(targetSid, '', refChip)
  emit('cite-file', {
    path: displayPath.value,
    line: citedLine.value,
    refChip,
    targetSessionId: targetSid,
  })
  showToast(`已挂载引用胶囊 @${refChip}`)
}

function handlePickToDraft(): void {
  const pick = activePick.value
  if (!pick) return
  const targetSid = resolvedTargetSessionId.value
  const { draftText, refChip } = formatHtmlPickerDraft({
    pick,
    note: pickNote.value,
    includeDom: includeDom.value,
    includeShot: includeShot.value,
  })
  appendToSessionDraft(targetSid, draftText, refChip)
  emit('draft-annotation', {
    targetSessionId: targetSid,
    draftText,
    refChip,
    pick,
  })
  activePick.value = null
  showToast(`↳ 已填入输入框并挂载 @${refChip}`)
}

async function postInputToSession(sessionId: string, text: string): Promise<boolean> {
  if (!sessionId || !text) return false
  const target = targetOptions.value.find((t) => t.sessionId === sessionId)
  const path = cliApi(`/sessions/${encodeURIComponent(sessionId)}/input`)
  const sendChunk = async (body: string): Promise<Response> => {
    const headers: Record<string, string> = { 'Content-Type': 'text/plain; charset=utf-8' }
    if (target?.isRemote) {
      if (!target.httpBase) throw new Error('remote peer unreachable')
      if (target.authToken) headers['X-CLI-Auth'] = target.authToken
      return fetch(target.httpBase + path, { method: 'POST', headers, body })
    }
    const { cliFetch } = useCliAuth()
    return cliFetch(path, { method: 'POST', headers, body })
  }
  try {
    const isMultiline = text.includes('\n')
    const pasteBody = isMultiline ? `\x1b[200~${text}\x1b[201~` : text
    const r1 = await sendChunk(pasteBody)
    if (!r1.ok) return false
    const r2 = await sendChunk('\r')
    return r2.ok
  } catch {
    return false
  }
}

async function handlePickSendNow(): Promise<void> {
  const pick = activePick.value
  if (!pick || sendingNow.value) return
  const targetSid = resolvedTargetSessionId.value
  const payload = formatHtmlPickerPayload({
    pick,
    note: pickNote.value,
    includeDom: includeDom.value,
    includeShot: includeShot.value,
  })

  sendingNow.value = true
  try {
    let ok = true
    if (props.autoPostInput && targetSid) {
      ok = await postInputToSession(targetSid, payload)
    }
    if (!ok) {
      showToast('发送失败：无法投递到目标会话，请检查连接后重试')
      return
    }
    emit('send-annotation', {
      targetSessionId: targetSid,
      text: payload,
      pick,
    })
    activePick.value = null
    showToast(`🚀 已直发结构化批注 (@${pick.file}:${pick.line})`)
  } finally {
    sendingNow.value = false
  }
}

function openExternalHtml(): void {
  if (!htmlFrameSrc.value) return
  window.open(htmlFrameSrc.value, '_blank', 'noopener,noreferrer')
}

function downloadRawFile(): void {
  const url = filesDownloadUrl(props.sessionId, cleanPath.value, props.cwd || undefined)
  if (!url) return
  const a = document.createElement('a')
  a.href = url
  a.download = fileName.value
  document.body.appendChild(a)
  a.click()
  a.remove()
}

onBeforeUnmount(() => {
  fetchAbort?.abort()
  cleanupIframePicker()
  releaseObjectUrl(rawResult.value)
  if (toastTimer) clearTimeout(toastTimer)
})

defineExpose({
  reload: loadFile,
  toggleHtmlPicker,
  inspectElementForTest: (el: PickableElementLike) => {
    const picked = inspectPickedElement(el, {
      file: cleanPath.value,
      cwd: props.cwd,
      htmlSource: rawText.value,
    })
    activePick.value = picked
    return picked
  },
})
</script>

<template>
  <div
    class="cfp-root"
    data-testid="center-file-preview"
    @click.capture="onReaderClickCapture"
  >
    <!-- Reader Header Bar -->
    <div class="cfp-toolbar" data-testid="center-preview-toolbar">
      <button
        v-if="mode === 'mobile-full'"
        type="button"
        class="cfp-btn cfp-btn--back"
        data-testid="btn-mobile-back-tree"
        @click="$emit('close')"
      >‹ 目录树</button>

      <div class="cfp-title-wrap" :title="cleanPath" data-testid="center-preview-title">
        <span class="cfp-title-italic">📄 {{ displayPath }}</span>
        <span class="cfp-line-pill">:{{ citedLine }}</span>
      </div>

      <div class="cfp-actions">
        <button
          v-if="isHtmlFile"
          type="button"
          class="cfp-btn"
          data-testid="btn-toggle-html-render"
          @click="toggleHtmlRenderMode"
        >{{ htmlRenderMode === 'render' ? '看源码' : '看渲染' }}</button>

        <button
          v-if="isHtmlFile"
          type="button"
          class="cfp-btn"
          :class="{ 'cfp-btn--picker-on': pickerEnabled }"
          data-testid="btn-toggle-html-picker"
          @click="toggleHtmlPicker"
        >{{ pickerEnabled ? '🎯 拾取中' : '🎯 开启拾取' }}</button>

        <button
          v-if="isHtmlFile && htmlFrameSrc"
          type="button"
          class="cfp-btn cfp-btn--icon"
          title="独立窗口打开网页"
          data-testid="btn-open-html-external"
          @click="openExternalHtml"
        ><ExternalLink class="size-3.5" /></button>

        <button
          type="button"
          class="cfp-btn cfp-btn--primary"
          data-testid="btn-cite-file-preview"
          :title="`挂载 @${displayPath}:${citedLine} 到输入框`"
          @click="citeCurrentFile"
        >@ 引用到对话</button>

        <button
          v-if="mode === 'preview-tab'"
          type="button"
          class="cfp-btn"
          data-testid="btn-split-file-preview"
          title="左会话 + 右文件并排对照（自动收起右侧目录树防四栏挤压）"
          @click="$emit('split-file', cleanPath)"
        >◫ 分屏边聊边看</button>

        <button
          v-if="mode === 'split-pane'"
          type="button"
          class="cfp-btn"
          data-testid="btn-promote-full-preview"
          @click="$emit('promote-full', cleanPath)"
        >⛶ 切回全宽预览 Tab</button>

        <button
          type="button"
          class="cfp-btn cfp-btn--icon"
          title="刷新文件内容"
          data-testid="btn-refresh-center-preview"
          :disabled="loading"
          @click="loadFile"
        ><RefreshCw class="size-3.5" :class="{ 'animate-spin': loading }" /></button>

        <button
          v-if="mode !== 'mobile-full'"
          type="button"
          class="cfp-btn cfp-btn--close"
          title="关闭预览标签页"
          data-testid="btn-close-center-preview"
          @click="$emit('close')"
        >✕</button>
      </div>
    </div>

    <!-- Body -->
    <div class="cfp-body">
      <div v-if="loading && !rawText && rawResult.kind === 'text'" class="cfp-empty">
        正在读取文件…
      </div>

      <!-- HTML Sandbox Render + Element Picker -->
      <div
        v-else-if="isHtmlFile && htmlRenderMode === 'render' && rawResult.kind === 'text'"
        class="cfp-html-stage"
      >
        <div class="cfp-html-banner">
          <span class="cfp-html-status">
            {{
              pickerEnabled
                ? '🎯 拾取已开启：点击网页中任意元素弹出就地批注框 → 一键入框或直发终端'
                : '沙盒安全预览（开启右上「🎯 开启拾取」可点选元素直发修改意见）'
            }}
          </span>
          <span v-if="hoveredLabel" class="cfp-hover-chip">{{ hoveredLabel }}</span>
        </div>

        <div class="cfp-iframe-wrap" :class="{ 'is-picking': pickerEnabled }">
          <iframe
            v-if="!pickerEnabled"
            key="sandbox-run"
            ref="htmlIframeRef"
            class="cfp-iframe"
            :src="htmlFrameSrc || undefined"
            :srcdoc="!htmlFrameSrc ? rawText : undefined"
            sandbox="allow-scripts"
            referrerpolicy="no-referrer"
            :title="fileName"
            data-testid="center-preview-html-frame"
          />
          <!-- Picker mode: allow-same-origin WITHOUT allow-scripts keeps untrusted JS inert
               while enabling accurate DOM inspection + bounding box + @file:line resolution -->
          <iframe
            v-else
            key="sandbox-pick"
            ref="htmlIframeRef"
            class="cfp-iframe"
            :src="htmlFrameSrc || undefined"
            :srcdoc="!htmlFrameSrc ? staticSafeSrcdoc : undefined"
            sandbox="allow-same-origin"
            referrerpolicy="no-referrer"
            :title="fileName"
            data-testid="center-preview-html-frame"
            @load="attachIframePicker"
          />

          <!-- Inline Design Pick Popover -->
          <div
            v-if="activePick"
            class="design-pick-popover"
            data-testid="design-pick-popover"
          >
            <div class="dpp-head">
              <span class="dpp-el-chip" data-testid="dpp-picked-selector">
                🎯 &lt;{{ activePick.selector }}&gt; · {{ activePick.dimensions }}
              </span>
              <span class="dpp-loc" data-testid="dpp-picked-location">
                @{{ activePick.file }}:{{ activePick.line }}
              </span>
              <div class="dpp-checks">
                <label class="dpp-chk">
                  <input v-model="includeDom" type="checkbox" data-testid="dpp-chk-dom" />
                  DOM/CSS
                </label>
                <label class="dpp-chk">
                  <input v-model="includeShot" type="checkbox" data-testid="dpp-chk-shot" />
                  尺寸上下文
                </label>
              </div>
            </div>

            <textarea
              ref="dppNoteRef"
              v-model="pickNote"
              class="dpp-textarea"
              data-testid="dpp-note-input"
              placeholder="直接输入对该元素的修改要求（Ctrl/⌘+Enter 直接发送）…"
              @keydown.ctrl.enter.prevent="handlePickSendNow"
              @keydown.meta.enter.prevent="handlePickSendNow"
              @keydown.esc.stop.prevent="activePick = null"
            />

            <div class="dpp-foot">
              <div class="dpp-target">
                <span>发至：</span>
                <select
                  :value="selectedTargetSessionId"
                  class="dpp-select"
                  data-testid="dpp-target-select"
                  @change="selectedTargetSessionId = ($event.target as HTMLSelectElement).value"
                >
                  <option
                    v-for="opt in targetOptions"
                    :key="opt.id"
                    :value="opt.sessionId"
                  >{{ opt.isPrimary ? '★ ' : '' }}{{ opt.title }}</option>
                </select>
              </div>

              <div class="dpp-btns">
                <button
                  type="button"
                  class="cfp-btn"
                  data-testid="dpp-cancel"
                  @click="activePick = null"
                >取消</button>
                <button
                  type="button"
                  class="cfp-btn cfp-btn--primary"
                  data-testid="dpp-to-draft"
                  @click="handlePickToDraft"
                >↳ 入框</button>
                <button
                  type="button"
                  class="cfp-btn cfp-btn--cta"
                  data-testid="dpp-send-now"
                  :disabled="sendingNow"
                  @click="handlePickSendNow"
                >{{ sendingNow ? '发送中…' : '直接发送 ↵' }}</button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Rich Markdown / Highlighted Source Reader -->
      <FilePreview
        v-else-if="rawResult.kind === 'text'"
        :name="fileName"
        :text="rawResult.text"
        :path="cleanPath"
        :render-src="htmlFrameSrc || undefined"
        :content-version="revision"
        :session-id="sessionId"
        :cwd="cwd || undefined"
        :line-start="line"
        :active="active"
        @navigate="$emit('navigate', $event)"
        @toast="showToast"
      />

      <!-- Raster image -->
      <div v-else-if="rawResult.kind === 'image'" class="cfp-image-stage">
        <img :src="rawResult.url" :alt="fileName" class="cfp-image" />
      </div>

      <!-- Binary / Too large / Error -->
      <div v-else-if="rawResult.kind === 'binary' || rawResult.kind === 'tooLarge'" class="cfp-empty">
        <p>{{ rawResult.kind === 'binary' ? '二进制文件，暂不支持内联文本渲染' : '文件较大（>1MB），建议下载查看' }}</p>
        <div class="cfp-empty-actions">
          <button type="button" class="cfp-btn cfp-btn--primary" @click="downloadRawFile">下载文件</button>
          <button type="button" class="cfp-btn" @click="citeCurrentFile">@ 引用路径到对话</button>
        </div>
      </div>

      <div v-else class="cfp-empty">
        <p>读取失败：{{ rawResult.kind === 'error' ? (rawResult.reason || `HTTP ${rawResult.status || ''}`) : '无法预览该文件' }}</p>
        <button type="button" class="cfp-btn" @click="loadFile">重试</button>
      </div>
    </div>

    <div v-if="toastText" class="cfp-toast" data-testid="center-preview-toast">
      {{ toastText }}
    </div>
  </div>
</template>

<style scoped>
.cfp-root {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-height: 0;
  background: #0d1017;
  color: #e4eaf5;
  overflow: hidden;
}

.cfp-toolbar {
  flex-shrink: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  background: #131722;
  border-bottom: 1px solid #262d40;
}

.cfp-title-wrap {
  min-width: 0;
  display: inline-flex;
  align-items: baseline;
  gap: 2px;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-size: 0.77rem;
  overflow: hidden;
}
.cfp-title-italic {
  font-style: italic;
  color: #dce6f8;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cfp-line-pill {
  flex-shrink: 0;
  color: #7ea6f8;
  font-size: 0.72rem;
}

.cfp-actions {
  margin-left: auto;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.cfp-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  height: 26px;
  padding: 0 9px;
  border-radius: 6px;
  border: 1px solid #313a52;
  background: #1b2130;
  color: #cbd5e8;
  font-size: 0.73rem;
  font-weight: 500;
  cursor: pointer;
  white-space: nowrap;
  transition: all 0.12s ease;
}
.cfp-btn:hover:not(:disabled) {
  background: #252e44;
  color: #f0f4fc;
}
.cfp-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.cfp-btn--icon {
  padding: 0 7px;
}
.cfp-btn--back {
  border-color: #3f5278;
  background: #1b2842;
  color: #b7d2ff;
  font-weight: 600;
}
.cfp-btn--primary {
  border-color: rgba(96, 152, 255, 0.5);
  background: rgba(54, 108, 220, 0.22);
  color: #c8deff;
}
.cfp-btn--primary:hover:not(:disabled) {
  background: rgba(54, 108, 220, 0.36);
}
.cfp-btn--picker-on {
  border-color: #c084fc;
  background: rgba(147, 51, 234, 0.28);
  color: #f3e8ff;
  box-shadow: 0 0 0 1px rgba(192, 132, 252, 0.25);
}
.cfp-btn--cta {
  border-color: #f8fafc;
  background: #f8fafc;
  color: #090d16;
  font-weight: 600;
}
.cfp-btn--cta:hover:not(:disabled) {
  background: #e2e8f0;
  color: #090d16;
}
.cfp-btn--close {
  border-color: transparent;
  background: transparent;
  color: #8c96ae;
  padding: 0 6px;
}
.cfp-btn--close:hover {
  background: rgba(248, 113, 113, 0.16);
  color: #fca5a5;
}

.cfp-body {
  flex: 1;
  min-height: 0;
  position: relative;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.cfp-html-stage {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.cfp-html-banner {
  flex-shrink: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 5px 12px;
  background: #111827;
  border-bottom: 1px solid #243049;
  font-size: 0.72rem;
  color: #adc4f0;
}
.cfp-hover-chip {
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-size: 0.69rem;
  padding: 1px 7px;
  border-radius: 999px;
  background: rgba(56, 189, 248, 0.14);
  border: 1px solid rgba(56, 189, 248, 0.4);
  color: #bae6fd;
}

.cfp-iframe-wrap {
  position: relative;
  flex: 1;
  min-height: 0;
  background: #ffffff;
}
.cfp-iframe-wrap.is-picking {
  box-shadow: inset 0 0 0 2px #a855f7;
}
.cfp-iframe {
  width: 100%;
  height: 100%;
  border: 0;
  display: block;
}

.design-pick-popover {
  position: absolute;
  right: 16px;
  bottom: 16px;
  width: min(520px, calc(100% - 32px));
  z-index: 30;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 11px 12px;
  border-radius: 10px;
  background: #141824;
  border: 1px solid #6b4cb6;
  box-shadow: 0 14px 40px rgba(0, 0, 0, 0.65);
  color: #eef2ff;
}
.dpp-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 0.72rem;
}
.dpp-el-chip {
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(168, 85, 247, 0.22);
  border: 1px solid rgba(192, 132, 252, 0.5);
  color: #f3e8ff;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
  font-weight: 600;
}
.dpp-loc {
  color: #94a3b8;
  font-family: 'SF Mono', 'Menlo', 'Consolas', monospace;
}
.dpp-checks {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 10px;
}
.dpp-chk {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 0.7rem;
  color: #cbd5e1;
  cursor: pointer;
}
.dpp-textarea {
  width: 100%;
  min-height: 62px;
  max-height: 140px;
  padding: 8px 10px;
  border-radius: 7px;
  border: 1px solid #334155;
  background: #0b0f19;
  color: #f1f5f9;
  font-size: 0.8rem;
  line-height: 1.45;
  resize: vertical;
  outline: none;
}
.dpp-textarea:focus {
  border-color: #818cf8;
  box-shadow: 0 0 0 2px rgba(129, 140, 248, 0.22);
}
.dpp-foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.dpp-target {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.72rem;
  color: #94a3b8;
}
.dpp-select {
  height: 26px;
  padding: 0 8px;
  border-radius: 6px;
  border: 1px solid #334155;
  background: #1e2436;
  color: #e2e8f0;
  font-size: 0.73rem;
}
.dpp-btns {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.cfp-image-stage {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: auto;
  padding: 16px;
  background: #090c12;
}
.cfp-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.cfp-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 24px;
  text-align: center;
  font-size: 0.8rem;
  color: #8c96ae;
}
.cfp-empty-actions {
  display: inline-flex;
  gap: 8px;
}

.cfp-toast {
  position: absolute;
  left: 50%;
  bottom: 18px;
  transform: translateX(-50%);
  z-index: 40;
  padding: 6px 14px;
  border-radius: 999px;
  background: rgba(15, 23, 42, 0.94);
  border: 1px solid rgba(96, 165, 250, 0.45);
  color: #e0f2fe;
  font-size: 0.74rem;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.45);
  pointer-events: none;
}
</style>
