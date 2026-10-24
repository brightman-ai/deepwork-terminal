/**
 * useComposeBox — device-agnostic state/behavior for a "compose then send" text box.
 *
 * Extracted from what was originally ComposeBar.vue's (mobile-only) script. The DOMAIN here —
 * draft persistence, snippets, send history, undo-clear, copy-all, cursor math, send encoding —
 * has nothing to do with touch vs. mouse+keyboard; it was mobile-only only because the FIRST
 * caller was. A desktop caller wants the exact same guarantees (nothing typed is ever silently
 * lost, snippets/history are the same server-backed list either device reaches for) with a
 * DIFFERENT skin: no on-screen cursor-nav buttons (a hardware keyboard already has arrow keys),
 * no soft-keyboard viewport dodging, a taller box.
 *
 * Same split this codebase already made for the tab strip (cli-tabs: one judgment SSOT, two
 * presentational components per shell) — one state/behavior SSOT here, ComposeBar.vue (mobile)
 * and ComposeBarDesktop.vue (desktop) are thin template+CSS shells over it.
 */
import { ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import {
  attachCliInputDiagnostics,
  reportCliInputDiagnostic,
  summarizeText,
} from '@terminal/composables/cli/useCliInputDiagnostics'
import { useServerStore } from '@terminal/composables/cli/useServerStore'
import { useComposeSendStrategy } from '@terminal/composables/cli/useComposeSendStrategy'
import * as typedTriggerOps from '@terminal/composables/cli/composeTypedTrigger'
import type { TypedTrigger } from '@terminal/composables/cli/composeTypedTrigger'
import { copyTextToClipboard } from '@ce/utils/clipboard'

const DRAFT_KEY = 'cli-compose-draft'
/** Manually-dragged textarea height (px) — a UI preference, not content, so it is intentionally
 *  ONE global key shared across every tab/session, same as "I like a taller box" would be if this
 *  were a settings toggle instead of a drag gesture. */
const HEIGHT_KEY = 'cli-compose-height'
const HISTORY_MAX = 15
const UNDO_VISIBLE_MS = 6000

/** 粘贴那一刻输入框的选区快照——上传是异步的，完成时用它把引用放回用户贴的那个位置。 */
export interface ComposeSelectionAnchor {
  start: number
  end: number
  /** 快照时的全文；完成时全文已变（用户继续打了字）就不信快照位置，改用当时的光标。 */
  value: string
}

/**
 * 把一段文件引用（`@./tmp/clip/x.png `）拼进输入框文本的 [start,end) 处，返回新文本和光标位置。
 * 两侧补空格只为"和相邻文字分开"：前面紧贴非空白字符就补一个，后面紧贴非空白就保留引用自带的
 * 尾随空格，否则去掉，避免出现双空格。
 */
export function spliceAtSelection(
  value: string,
  start: number,
  end: number,
  insert: string,
): { value: string; caret: number } {
  const s = Math.max(0, Math.min(start, value.length))
  const e = Math.max(s, Math.min(end, value.length))
  const before = value.slice(0, s)
  const after = value.slice(e)
  const core = insert.trim()
  if (!core) return { value, caret: s }
  const lead = before && !/\s$/.test(before) ? ' ' : ''
  const trail = after && /^\s/.test(after) ? '' : ' '
  const piece = lead + core + trail
  return { value: before + piece + after, caret: before.length + piece.length }
}

export interface UseComposeBoxOptions {
  /** Cap on visible lines before the box scrolls internally instead of growing further. Mobile's
   *  screen is scarce (~5); desktop has room to spare — the cap is a caller decision, not this
   *  module's. */
  maxLines: number
  /** Diagnostics tag for the textarea, e.g. 'compose-textarea-mobile' / '-desktop' — so compose
   *  telemetry from the two skins is distinguishable, not merged into one undifferentiated signal. */
  diagnosticSurface: string
  /** Host-supplied draft (e.g. ResourceDrawer's 重发 inserting a past prompt for editing).
   *  A getter, not a Ref, so each host's own `defineProps<{draft?: string}>()` stays the SSOT for
   *  its own prop — this module just reads it. */
  draft: () => string | undefined
  /** Which tab/session this draft belongs to — scopes the localStorage draft key so two tabs
   *  stop clobbering each other's in-progress text (previously ONE global key for every tab).
   *  A getter for the same reason `draft` is one: the host's own session id can change under an
   *  already-mounted instance (e.g. ResourceDrawer reusing one compose box across drawer opens).
   *  Omit it to keep the pre-existing single-global-draft behavior (host doesn't have a stable id
   *  to key on). */
  draftScope?: () => string | undefined
  /** Focus strategy. Mobile must dodge the soft-keyboard's viewport jump (see
   *  useVisualKeyboardInset); desktop has no such thing — a plain `el?.focus()` is correct there.
   *  Caller decides; this module has no opinion on what a device's keyboard does to the viewport. */
  focusEl: (el: HTMLTextAreaElement | null | undefined) => void
  /** Desktop's host focuses after its floating placement has settled. Mobile owns mount focus. */
  focusOnMount?: boolean
  /** Called right after a focus that might need viewport correction. No-op on desktop. */
  onFocusSideEffects?: () => void
  emit: {
    send: (text: string) => void
  }
}

export function useComposeBox(opts: UseComposeBoxOptions) {
  const serverStore = useServerStore()
  const composeSend = useComposeSendStrategy()

  const text = ref('')
  const textareaRef = ref<HTMLTextAreaElement>()
  const showSnippets = ref(false)
  const showHistory = ref(false)
  const snippets = ref<string[]>([])
  const history = ref<string[]>([])
  let cleanupInputDiagnostics: (() => void) | null = null
  let disposed = false

  // --- Copy-all feedback (icon flips to a checkmark briefly) ---
  const copyFeedback = ref(false)
  let copyFeedbackTimer: ReturnType<typeof setTimeout> | null = null

  // --- Undo-clear: 清空全部 is destructive on multi-line drafts, so the wiped text is kept
  // around for a short window as a one-shot restore, not a confirm-before-clear dialog (that
  // trades one friction for another). clearedBackup holds the pre-clear text; showUndo drives the
  // pill; the timer auto-dismisses so the pill never lingers and blocks the input.
  const clearedBackup = ref<string | null>(null)
  const showUndo = ref(false)
  let undoTimer: ReturnType<typeof setTimeout> | null = null

  function focus(): void {
    opts.focusEl(textareaRef.value)
  }

  // --- Draft persistence (per-tab: DRAFT_KEY suffixed with draftScope when the host has one) ---
  function draftKey(): string {
    const scope = opts.draftScope?.()
    return scope ? `${DRAFT_KEY}:${scope}` : DRAFT_KEY
  }
  function saveDraft() {
    try { localStorage.setItem(draftKey(), text.value) } catch {}
  }
  function loadDraft() {
    try { text.value = localStorage.getItem(draftKey()) || '' } catch {}
  }
  function removeDraft() {
    try { localStorage.removeItem(draftKey()) } catch {}
  }

  // --- Injected draft (host-supplied, e.g. ResourceDrawer 重发) ---
  function applyDraft(draft: string) {
    if (draft == null) return
    text.value = draft
    saveDraft()
    nextTick(() => {
      autoResize()
      const ta = textareaRef.value
      if (ta) {
        const end = ta.value.length
        try { ta.setSelectionRange(end, end) } catch {}
      }
      focus()
    })
  }
  watch(opts.draft, (d) => { if (d != null) applyDraft(d) })

  // --- Snippets (server-side, survives trycloudflare domain changes) ---
  function loadSnippets() {
    snippets.value = serverStore.get<string[]>('snippets', [])
  }
  function saveSnippetsToStorage() {
    serverStore.set('snippets', snippets.value)
  }
  function saveSnippet() {
    const t = text.value.trim()
    if (!t) return
    if (!snippets.value.includes(t)) {
      snippets.value.unshift(t)
      if (snippets.value.length > 20) snippets.value.pop()
      saveSnippetsToStorage()
    }
  }
  function insertSnippet(s: string) {
    text.value = s
    showSnippets.value = false
    nextTick(() => {
      autoResize()
      focus()
    })
  }
  function deleteSnippet(i: number) {
    snippets.value.splice(i, 1)
    saveSnippetsToStorage()
  }
  function toggleSnippets() {
    showSnippets.value = !showSnippets.value
    if (showSnippets.value) showHistory.value = false
  }

  // --- Send History (server-side, survives trycloudflare domain changes) ---
  function loadHistory() {
    history.value = serverStore.get<string[]>('history', [])
  }
  function saveHistoryToStorage() {
    serverStore.set('history', history.value)
  }
  function pushHistory(t: string) {
    const trimmed = t.trim()
    if (!trimmed) return
    const idx = history.value.indexOf(trimmed)
    if (idx !== -1) history.value.splice(idx, 1)
    history.value.unshift(trimmed)
    if (history.value.length > HISTORY_MAX) history.value.pop()
    saveHistoryToStorage()
  }
  function insertFromHistory(h: string) {
    text.value = h
    showHistory.value = false
    nextTick(() => {
      autoResize()
      focus()
    })
  }
  function clearHistory() {
    history.value = []
    serverStore.set('history', [])
  }
  function toggleHistory() {
    showHistory.value = !showHistory.value
    if (showHistory.value) showSnippets.value = false
  }

  // --- Auto-resize + input handler ---
  // Grow with content up to `opts.maxLines`, then scroll internally — UNLESS the user has
  // dragged a manual height (manualHeight below), which then replaces `opts.maxLines` as the
  // cap until dragged again. A manual drag is a stronger signal of intent than the content-driven
  // default, so it wins rather than being overwritten on the next keystroke.
  function autoResize() {
    const ta = textareaRef.value
    if (!ta) return
    if (manualHeight.value != null) {
      ta.style.height = manualHeight.value + 'px'
      return
    }
    ta.style.height = 'auto'
    const cs = getComputedStyle(ta)
    const lineHeight = parseFloat(cs.lineHeight) || 21
    const vPad = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom) + 2 // padding + 1px borders
    const cap = lineHeight * opts.maxLines + vPad
    ta.style.height = Math.min(ta.scrollHeight, cap) + 'px'
  }

  // --- Manual resize (drag handle, desktop-only UI but the state lives here so it's testable
  // without a DOM drag simulation) ---
  const manualHeight = ref<number | null>(null)
  function loadManualHeight() {
    try {
      const raw = localStorage.getItem(HEIGHT_KEY)
      const n = raw ? parseFloat(raw) : NaN
      manualHeight.value = Number.isFinite(n) ? n : null
    } catch {}
  }
  function saveManualHeight() {
    try {
      if (manualHeight.value != null) localStorage.setItem(HEIGHT_KEY, String(manualHeight.value))
    } catch {}
  }
  let resizeStartY = 0
  let resizeStartHeight = 0
  /** +1: the grip sits BELOW the textarea (floating desktop box), dragging DOWN grows it;
   *  -1: the grip sits ABOVE a bottom-anchored textarea (the original docked bar), dragging UP grows it. */
  let resizeSign: 1 | -1 = -1
  function onResizeMove(e: PointerEvent) {
    // Docked/bottom-anchored: the handle sits ABOVE the textarea, so dragging UP (decreasing
    // clientY) must grow the box — startY - currentY. A grip BELOW the textarea is the mirror image.
    const delta = resizeSign * (e.clientY - resizeStartY)
    const next = Math.min(Math.max(resizeStartHeight + delta, 40), window.innerHeight * 0.7)
    manualHeight.value = next
    const ta = textareaRef.value
    if (ta) ta.style.height = next + 'px'
  }
  function onResizeEnd() {
    window.removeEventListener('pointermove', onResizeMove)
    saveManualHeight()
  }
  function onResizeStart(e: PointerEvent, grip: 'above' | 'below' = 'above') {
    const ta = textareaRef.value
    if (!ta) return
    e.preventDefault()
    resizeSign = grip === 'below' ? 1 : -1
    resizeStartY = e.clientY
    resizeStartHeight = ta.offsetHeight
    window.addEventListener('pointermove', onResizeMove)
    window.addEventListener('pointerup', onResizeEnd, { once: true })
  }
  /**
   * 把文件引用插进输入框（输入框里贴图时用，见 CliTerminalSurface.onClipboardPaste）。anchor 是
   * 粘贴那一刻的选区；上传期间用户又改了文字就退回到此刻的光标处。插完光标落在引用后面。
   */
  function insertAtCursor(insert: string, anchor?: ComposeSelectionAnchor): void {
    const ta = textareaRef.value
    const cur = text.value
    let start = cur.length
    let end = cur.length
    if (anchor && anchor.value === cur) {
      start = anchor.start
      end = anchor.end
    } else if (ta) {
      start = ta.selectionStart
      end = ta.selectionEnd
    }
    const next = spliceAtSelection(cur, start, end, insert)
    text.value = next.value
    saveDraft()
    void nextTick(() => {
      autoResize()
      const el = textareaRef.value
      if (!el) return
      el.focus()
      el.selectionStart = el.selectionEnd = next.caret
    })
  }

  // --- Typed trigger (desktop "打字即成框", terminal-input REQ-TI-01/03) ---
  // The box was opened BY a keystroke the terminal never saw. The bookkeeping lives in
  // composeTypedTrigger.ts (pure, tested); this block only applies its results to the refs and the
  // per-tab draft, so Esc on an untouched box can hand the keystroke back to the terminal and
  // leave the draft as it was.
  let typedTrigger: TypedTrigger | null = null

  function placeCaretAtEnd(): void {
    const ta = textareaRef.value
    if (!ta) return
    const end = ta.value.length
    try { ta.setSelectionRange(end, end) } catch {}
  }

  /** Box opened by a visible character: append it to whatever draft was loaded, focus, caret last. */
  function beginTyped(typed: string): void {
    const begun = typedTriggerOps.beginTyped(text.value, typed)
    typedTrigger = begun.trigger
    text.value = begun.text
    saveDraft()
    focus() // synchronously — the next keystroke must find the box, not the terminal
    void nextTick(() => { autoResize(); placeCaretAtEnd() })
  }

  /** Box opened by an IME about to compose inside it. Focus must move NOW (same microtask checkpoint as the keydown). */
  function beginImeTyped(): void {
    typedTrigger = typedTriggerOps.beginIme(text.value)
    focus()
  }

  /** Text composed elsewhere (the terminal's own textarea, Safari's event order) handed over once committed. */
  function adoptCommitted(committed: string): void {
    if (!committed) return
    const adopted = typedTriggerOps.adoptCommitted(typedTrigger, text.value, committed)
    typedTrigger = adopted.trigger
    text.value = adopted.text
    saveDraft()
    void nextTick(() => { autoResize(); focus(); placeCaretAtEnd() })
  }

  /** compositionend inside the box: the first one after an IME-opened box defines "what the trigger was". */
  function noteCompositionEnd(): void {
    // After Vue's v-model has taken the committed text.
    void nextTick(() => { typedTrigger = typedTriggerOps.noteCompositionEnd(typedTrigger, text.value) })
  }

  /**
   * Esc on the box. If it still holds exactly what the triggering keystroke put there, take that
   * keystroke back: restore the draft as it was and return the text for the caller to re-send to the
   * terminal. Anything else (edited, or not opened by typing) → null, and the caller just closes.
   */
  function consumePristineTrigger(): string | null {
    const taken = typedTriggerOps.consumePristine(typedTrigger, text.value)
    typedTrigger = null
    if (!taken) return null
    text.value = taken.restoreText
    if (taken.restoreText) saveDraft(); else removeDraft()
    return taken.typed
  }

  function onInput(e: Event) {
    const ie = e as InputEvent
    reportCliInputDiagnostic('compose.input', {
      isComposing: ie.isComposing,
      inputType: ie.inputType,
      eventData: summarizeText(ie.data),
      modelValue: summarizeText(text.value),
    })
    autoResize()
    saveDraft()
  }

  // --- Focus handler ---
  function onTextareaFocus() {
    reportCliInputDiagnostic('compose.focus', { textLen: text.value.length })
    opts.onFocusSideEffects?.()
  }

  // --- Send ---
  function send() {
    const val = text.value
    if (!val) return
    reportCliInputDiagnostic('compose.send', { value: summarizeText(val) })
    typedTrigger = null
    pushHistory(val)
    opts.emit.send(val)
    text.value = ''
    removeDraft()
    nextTick(autoResize)
  }

  /** Convert assembled text into one WS-ready paste transaction — the SAME encode a host uses when wiring the `send` emit through to
   *  the PTY. Exposed here so both hosts call one implementation instead of importing the strategy
   *  composable separately and risking the two drifting. */
  const encode = composeSend.encode

  // --- Copy all ---
  async function copyAll() {
    const ok = await copyTextToClipboard(text.value)
    reportCliInputDiagnostic('compose.copy-all', { ok, len: text.value.length })
    if (!ok) return
    copyFeedback.value = true
    if (copyFeedbackTimer) clearTimeout(copyFeedbackTimer)
    copyFeedbackTimer = setTimeout(() => { copyFeedback.value = false }, 1400)
  }

  // --- Clear all (undoable) ---
  function clearAll() {
    typedTrigger = null
    const prev = text.value
    text.value = ''
    removeDraft()
    nextTick(() => {
      autoResize()
      focus()
    })
    if (undoTimer) { clearTimeout(undoTimer); undoTimer = null }
    if (!prev) {
      showUndo.value = false
      clearedBackup.value = null
      return
    }
    clearedBackup.value = prev
    showUndo.value = true
    undoTimer = setTimeout(() => {
      showUndo.value = false
      clearedBackup.value = null
      undoTimer = null
    }, UNDO_VISIBLE_MS)
  }

  function undoClear() {
    if (clearedBackup.value == null) return
    text.value = clearedBackup.value
    saveDraft()
    clearedBackup.value = null
    showUndo.value = false
    if (undoTimer) { clearTimeout(undoTimer); undoTimer = null }
    nextTick(() => {
      autoResize()
      const ta = textareaRef.value
      if (ta) {
        const end = ta.value.length
        try { ta.setSelectionRange(end, end) } catch {}
      }
      focus()
    })
  }

  // --- Cursor movement (used by hosts that render on-screen nav buttons, e.g. mobile) ---
  function moveCursor(dir: 'up' | 'down' | 'left' | 'right' | 'home' | 'end') {
    const ta = textareaRef.value
    if (!ta) return
    const pos = ta.selectionStart
    const val = ta.value
    switch (dir) {
      case 'left':
        ta.selectionStart = ta.selectionEnd = Math.max(0, pos - 1); break
      case 'right':
        ta.selectionStart = ta.selectionEnd = Math.min(val.length, pos + 1); break
      case 'home': {
        const ls = val.lastIndexOf('\n', pos - 1) + 1
        ta.selectionStart = ta.selectionEnd = ls; break
      }
      case 'end': {
        let le = val.indexOf('\n', pos)
        if (le === -1) le = val.length
        ta.selectionStart = ta.selectionEnd = le; break
      }
      case 'up': {
        const cls = val.lastIndexOf('\n', pos - 1) + 1
        const col = pos - cls
        const pls = val.lastIndexOf('\n', cls - 2) + 1
        ta.selectionStart = ta.selectionEnd = Math.min(pls + col, Math.max(0, cls - 1)); break
      }
      case 'down': {
        const csl = val.lastIndexOf('\n', pos - 1) + 1
        const cp = pos - csl
        let nls = val.indexOf('\n', pos)
        if (nls === -1) break
        nls += 1
        let nle = val.indexOf('\n', nls)
        if (nle === -1) nle = val.length
        ta.selectionStart = ta.selectionEnd = Math.min(nls + cp, nle); break
      }
    }
    focus()
  }

  onMounted(async () => {
    loadManualHeight()
    loadDraft()
    const injected = opts.draft()
    if (injected != null) text.value = injected
    await nextTick()
    autoResize()
    if (opts.focusOnMount !== false) focus()
    cleanupInputDiagnostics = attachCliInputDiagnostics(textareaRef.value ?? null, opts.diagnosticSurface)
    // Remote snippets/history must not decide when the local editor receives focus. A late
    // response used to refocus the box after the user had already started editing elsewhere
    // (or begun an IME composition). Only hydrate the lists when it arrives.
    await serverStore.load()
    if (disposed) return
    loadSnippets()
    loadHistory()
  })

  onUnmounted(() => {
    disposed = true
    saveDraft()
    cleanupInputDiagnostics?.()
    cleanupInputDiagnostics = null
    if (copyFeedbackTimer) clearTimeout(copyFeedbackTimer)
    if (undoTimer) clearTimeout(undoTimer)
    // In-flight drag at unmount (host closed the panel mid-resize) — drop the window listener,
    // it would otherwise keep firing against a detached textareaRef.
    window.removeEventListener('pointermove', onResizeMove)
  })

  return {
    text, textareaRef,
    showSnippets, showHistory, snippets, history,
    copyFeedback, showUndo,
    toggleSnippets, toggleHistory, saveSnippet, insertSnippet, deleteSnippet, clearHistory, insertFromHistory,
    onInput, onTextareaFocus, autoResize, onResizeStart,
    send, encode, copyAll, clearAll, undoClear, moveCursor, insertAtCursor,
    beginTyped, beginImeTyped, adoptCommitted, noteCompositionEnd, consumePristineTrigger,
  }
}
