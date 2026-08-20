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
import { copyTextToClipboard } from '@ce/utils/clipboard'

const DRAFT_KEY = 'cli-compose-draft'
const HISTORY_MAX = 15
const UNDO_VISIBLE_MS = 6000

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
  /** Focus strategy. Mobile must dodge the soft-keyboard's viewport jump (see
   *  useVisualKeyboardInset); desktop has no such thing — a plain `el?.focus()` is correct there.
   *  Caller decides; this module has no opinion on what a device's keyboard does to the viewport. */
  focusEl: (el: HTMLTextAreaElement | null | undefined) => void
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

  // --- Draft persistence ---
  function saveDraft() {
    try { localStorage.setItem(DRAFT_KEY, text.value) } catch {}
  }
  function loadDraft() {
    try { text.value = localStorage.getItem(DRAFT_KEY) || '' } catch {}
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
  // Grow with content up to `opts.maxLines`, then scroll internally.
  function autoResize() {
    const ta = textareaRef.value
    if (!ta) return
    ta.style.height = 'auto'
    const cs = getComputedStyle(ta)
    const lineHeight = parseFloat(cs.lineHeight) || 21
    const vPad = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom) + 2 // padding + 1px borders
    const cap = lineHeight * opts.maxLines + vPad
    ta.style.height = Math.min(ta.scrollHeight, cap) + 'px'
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
    pushHistory(val)
    opts.emit.send(val)
    text.value = ''
    try { localStorage.removeItem(DRAFT_KEY) } catch {}
    nextTick(autoResize)
  }

  /** Convert composed text into WS-ready chunks (bracketed-paste for long/multi-line, char-by-char
   *  for a short single line) — the SAME encode a host uses when wiring the `send` emit through to
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
    const prev = text.value
    text.value = ''
    try { localStorage.removeItem(DRAFT_KEY) } catch {}
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
    loadDraft()
    const injected = opts.draft()
    if (injected != null) text.value = injected
    await serverStore.load()
    loadSnippets()
    loadHistory()
    await nextTick()
    focus()
    autoResize()
    cleanupInputDiagnostics = attachCliInputDiagnostics(textareaRef.value ?? null, opts.diagnosticSurface)
  })

  onUnmounted(() => {
    saveDraft()
    cleanupInputDiagnostics?.()
    cleanupInputDiagnostics = null
    if (copyFeedbackTimer) clearTimeout(copyFeedbackTimer)
    if (undoTimer) clearTimeout(undoTimer)
  })

  return {
    text, textareaRef,
    showSnippets, showHistory, snippets, history,
    copyFeedback, showUndo,
    toggleSnippets, toggleHistory, saveSnippet, insertSnippet, deleteSnippet, clearHistory, insertFromHistory,
    onInput, onTextareaFocus, autoResize,
    send, encode, copyAll, clearAll, undoClear, moveCursor,
  }
}
