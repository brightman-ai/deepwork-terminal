/**
 * composeLenses — Non-destructive stackable thinking lenses (`🟣 叠加透镜`),
 * cursor-range action templates (`🟢 动作模板`), `@file:line` reference chips,
 * and per-session 6000ms undo-clear backup shared across `>_ CLI` (`ComposeBarDesktop` /
 * `ComposeBar`) and `💬 Chat` (`SessionTranscriptView`).
 *
 * Core invariants (REQ-TW-06):
 *  1. Mounting or unmounting a `lens` NEVER mutates the user's typed draft text;
 *     lens prompts are appended only when assembling the outgoing payload.
 *  2. Inserting a `tpl` uses `[selectionStart, selectionEnd)` (`insertAtCursorRange`)
 *     and NEVER overwrites surrounding draft text.
 *  3. Clearing backs up `{ draft, lenses, refs }` per session for `6000ms` so a single
 *     tap on Undo restores both the draft text and all mounted lenses / `@file` chips.
 */
import { computed, reactive, ref } from 'vue'

export type PhraseKind = 'lens' | 'tpl'
export type PhraseFilter = 'all' | 'lens' | 'tpl'

export interface ComposePhrase {
  id: string
  kind: PhraseKind
  pinned: boolean
  short: string
  title: string
  desc: string
  prompt: string
}

export interface ComposeUndoBackup {
  draft: string
  lenses: string[]
  refs: string[]
  clearedAt: number
  expiresAt: number
}

export interface SessionComposeBucket {
  draft: string
  hydratedDraft: boolean
  activeLenses: string[]
  refChips: string[]
  undoBackup: ComposeUndoBackup | null
}

export const UNDO_CLEAR_TTL_MS = 6000
const DRAFT_STORAGE_PREFIX = 'cli-compose-draft'
const GLOBAL_SCOPE_KEY = '__global__'

export const BUILTIN_PHRASES: readonly ComposePhrase[] = [
  {
    id: 'lens-ssot',
    kind: 'lens',
    pinned: true,
    short: '🏛️ 状态·SSOT',
    title: '明确唯一状态源与不变量（禁投机抽象）',
    desc: '列出本次状态唯一写入者与核心不变量；无第二个现成用途时不建新抽象层。',
    prompt:
      '【架构约束】请先指明本次变更的唯一状态真相源（SSOT）、唯一写入入口及必须成立的不变量；若拟引入新抽象层，请给出今天已存在的第二个调用点，否则保持直接实现。',
  },
  {
    id: 'lens-2hyp',
    kind: 'lens',
    pinned: true,
    short: '🎯 双因排障',
    title: '现象与推断分离 · 先取证排除再改码',
    desc: '列出两个可区分成因并先取一条排除证据；禁靠判空/重试掩盖症状。',
    prompt:
      '【排障约束】请把可观察现象与推断分开：列出至少两个可区分的候选成因，先用日志或最小复现排除其一再改代码；禁止用外层捕获、盲目判空或加延时掩盖根因。',
  },
  {
    id: 'lens-journey',
    kind: 'lens',
    pinned: true,
    short: '🔬 真实旅程',
    title: '走通一次真实输入→终态（附失败路径）',
    desc: '沿真实调用边界核对输入与可观察终态，未运行步骤显式标未验。',
    prompt:
      '【验证约束】请沿真实用户动作 → 接口/状态机 → 最终可见结果走通完整闭环，同时验证一条异常/边界输入路径；任何未实际运行的环节请明确标为「未验」。',
  },
  {
    id: 'tpl-review-fix',
    kind: 'tpl',
    pinned: false,
    short: '🔄 逐项自审闭环',
    title: '对照意图与 git diff 逐项复核再交付',
    desc: '核查空值、并发竞态、回滚与相邻同类问题。',
    prompt:
      '完成编码后请对照需求意图与 git diff 逐项排查空值、并发顺序与异常回滚，修复后重跑测试并按【改动点 / 实测证据 / 未验边界】汇报。',
  },
  {
    id: 'tpl-counterex',
    kind: 'tpl',
    pinned: false,
    short: '🔍 专找反例',
    title: '只列触发条件、代码依据与可观察后果',
    desc: '用于方案/代码挑刺，无实锤问题则直说未发现。',
    prompt:
      '请仅针对当前方案/改动寻找可成立的反例：按影响程度列出【触发条件 → 定位行号 → 可观察后果】；无证据支持的问题直写「未发现实锤阻塞」。',
  },
]

function normalizeScopeKey(sessionId?: string): string {
  const trimmed = sessionId?.trim()
  return trimmed ? trimmed : GLOBAL_SCOPE_KEY
}

function draftStorageKey(sessionId?: string): string {
  const trimmed = sessionId?.trim()
  return trimmed ? `${DRAFT_STORAGE_PREFIX}:${trimmed}` : DRAFT_STORAGE_PREFIX
}

function readStoredDraft(sessionId?: string): string {
  try {
    if (typeof localStorage === 'undefined') return ''
    return localStorage.getItem(draftStorageKey(sessionId)) || ''
  } catch {
    return ''
  }
}

function writeStoredDraft(sessionId: string | undefined, value: string): void {
  try {
    if (typeof localStorage === 'undefined') return
    const key = draftStorageKey(sessionId)
    if (value) localStorage.setItem(key, value)
    else localStorage.removeItem(key)
  } catch {
    // Ignore storage quota / sandbox errors
  }
}

/**
 * Normalize a `@file` or `file:line` token into canonical `path` or `path:line` form
 * without leading `@` or surrounding whitespace.
 */
export function normalizeRefChip(raw: string): string {
  return (raw || '').trim().replace(/^@+/, '').trim()
}

/**
 * Insert `snippet` across `[start, end)` without overwriting surrounding draft text.
 * Clamps out-of-bounds indices safely and adds single-space separation only when
 * touching adjacent non-whitespace tokens.
 */
export function insertAtCursorRange(
  text: string,
  start: number,
  end: number,
  snippet: string,
): { value: string; caret: number; text: string; cursor: number } {
  const source = text ?? ''
  const s = Math.max(0, Math.min(Number.isFinite(start) ? start : source.length, source.length))
  const e = Math.max(s, Math.min(Number.isFinite(end) ? end : s, source.length))
  if (!snippet) {
    return { value: source, caret: s, text: source, cursor: s }
  }
  const before = source.slice(0, s)
  const after = source.slice(e)
  const lead = before.length > 0 && !/\s$/.test(before) && !/^\s/.test(snippet) ? ' ' : ''
  const trail = after.length > 0 && !/^\s/.test(after) && !/\s$/.test(snippet) ? ' ' : ''
  const next = before + lead + snippet + trail + after
  const caret = before.length + lead.length + snippet.length
  return { value: next, caret, text: next, cursor: caret }
}

/**
 * Detect `/` at start of input or immediately after whitespace (`/` or `... /`)
 * to summon the Phrase Arsenal without clobbering file paths like `a/b/c`.
 */
export function shouldOpenSlashArsenal(text: string): boolean {
  return text === '/' || /\s\/$/.test(text)
}

/**
 * Strip a trailing `/` slash-arsenal trigger right before inserting a template so
 * `/ ` + template doesn't leave a stray leading `/`.
 */
export function stripTrailingSlashTrigger(
  text: string,
  caret = text.length,
): { text: string; caret: number; stripped: boolean } {
  const safeCaret = Math.max(0, Math.min(caret, text.length))
  const head = text.slice(0, safeCaret)
  const tail = text.slice(safeCaret)
  if (head === '/') {
    return { text: tail, caret: 0, stripped: true }
  }
  if (/\s\/$/.test(head)) {
    const nextHead = head.slice(0, -1)
    return { text: nextHead + tail, caret: nextHead.length, stripped: true }
  }
  return { text, caret: safeCaret, stripped: false }
}

/**
 * Filter phrases by category (`all` | `lens` | `tpl`) and case-insensitive substring query.
 */
export function filterPhrases(
  phrases: readonly ComposePhrase[],
  filter: PhraseFilter = 'all',
  query = '',
): ComposePhrase[] {
  const q = query.trim().toLowerCase()
  return phrases.filter((p) => {
    if (filter !== 'all' && p.kind !== filter) return false
    if (!q) return true
    const haystack = `${p.short} ${p.title} ${p.desc} ${p.prompt}`.toLowerCase()
    return haystack.includes(q)
  })
}

/**
 * Non-destructively assemble the outgoing payload from `draft`, stacked `lenses`,
 * and `@file:line` `refs`. The caller's `draft` string itself is never mutated.
 */
export function composePayloadWithLenses(
  draft: string,
  lenses: readonly string[] = [],
  refs: readonly string[] = [],
  phrases: readonly ComposePhrase[] = BUILTIN_PHRASES,
): string {
  const trimmedDraft = (draft ?? '').trim()
  const seenRefs = new Set<string>()
  const refTokens: string[] = []
  for (const raw of refs) {
    const clean = normalizeRefChip(raw)
    if (!clean || seenRefs.has(clean)) continue
    seenRefs.add(clean)
    const token = `@${clean}`
    if (!trimmedDraft.includes(token)) {
      refTokens.push(token)
    }
  }

  const seenLenses = new Set<string>()
  const lensPrompts: string[] = []
  for (const lensId of lenses) {
    if (!lensId || seenLenses.has(lensId)) continue
    seenLenses.add(lensId)
    const found = phrases.find((p) => p.kind === 'lens' && p.id === lensId)
    if (found?.prompt) {
      lensPrompts.push(found.prompt)
    }
  }

  const head = [refTokens.join(' '), trimmedDraft].filter(Boolean).join(' ')
  return [head, ...lensPrompts].filter(Boolean).join('\n\n')
}

// ── Per-session reactive state registry ─────────────────────────────────────

const sessionBuckets = reactive<Record<string, SessionComposeBucket>>({})
const undoTimers = new Map<string, ReturnType<typeof setTimeout>>()

export function ensureSessionComposeState(sessionId?: string): SessionComposeBucket {
  const key = normalizeScopeKey(sessionId)
  let bucket = sessionBuckets[key]
  if (!bucket) {
    bucket = reactive<SessionComposeBucket>({
      draft: readStoredDraft(sessionId),
      hydratedDraft: true,
      activeLenses: [],
      refChips: [],
      undoBackup: null,
    })
    sessionBuckets[key] = bucket
  } else if (!bucket.hydratedDraft) {
    bucket.draft = readStoredDraft(sessionId)
    bucket.hydratedDraft = true
  }
  return bucket
}

export function getSessionDraft(sessionId?: string): string {
  return ensureSessionComposeState(sessionId).draft
}

export function setSessionDraft(
  sessionId: string | undefined,
  draft: string,
  persist = true,
): void {
  const bucket = ensureSessionComposeState(sessionId)
  bucket.draft = draft
  if (persist) {
    writeStoredDraft(sessionId, draft)
  }
}

export function toggleSessionLens(sessionId: string | undefined, lensId: string): boolean {
  const phrase = BUILTIN_PHRASES.find((p) => p.id === lensId && p.kind === 'lens')
  if (!phrase) return false
  const bucket = ensureSessionComposeState(sessionId)
  const idx = bucket.activeLenses.indexOf(lensId)
  if (idx >= 0) {
    bucket.activeLenses.splice(idx, 1)
    return false
  }
  bucket.activeLenses.push(lensId)
  return true
}

export function addSessionRefChip(sessionId: string | undefined, rawRef: string): string | null {
  const clean = normalizeRefChip(rawRef)
  if (!clean) return null
  const bucket = ensureSessionComposeState(sessionId)
  if (!bucket.refChips.includes(clean)) {
    bucket.refChips.push(clean)
  }
  return clean
}

export function removeSessionRefChip(sessionId: string | undefined, rawRef: string): void {
  const clean = normalizeRefChip(rawRef)
  const bucket = ensureSessionComposeState(sessionId)
  const idx = bucket.refChips.indexOf(clean)
  if (idx >= 0) {
    bucket.refChips.splice(idx, 1)
  }
}

/**
 * Programmatically append text and/or attach a `@file:line` chip to a session's composer
 * (used by `@ 引用到对话`, HTML Picker `↳ 入框`, and Diff `✨ 发送批注`).
 */
export function appendToSessionDraft(
  sessionId: string | undefined,
  snippet = '',
  refChip = '',
): string {
  const bucket = ensureSessionComposeState(sessionId)
  if (refChip) {
    addSessionRefChip(sessionId, refChip)
  }
  if (snippet.trim()) {
    const res = insertAtCursorRange(
      bucket.draft,
      bucket.draft.length,
      bucket.draft.length,
      snippet.trim(),
    )
    bucket.draft = res.text
    writeStoredDraft(sessionId, bucket.draft)
  }
  return bucket.draft
}

export function clearSessionComposeOnSend(sessionId?: string): void {
  const bucket = ensureSessionComposeState(sessionId)
  bucket.draft = ''
  bucket.activeLenses = []
  bucket.refChips = []
  writeStoredDraft(sessionId, '')
}

function cancelUndoTimer(key: string): void {
  const existing = undoTimers.get(key)
  if (existing) {
    clearTimeout(existing)
    undoTimers.delete(key)
  }
}

/**
 * Clear a session's draft, active lenses, and `@file:line` chips while storing a
 * `6000ms` per-session backup so one click on Undo restores the entire state.
 */
export function clearSessionComposeWithUndo(
  sessionId?: string,
  currentDraft?: string,
  ttlMs = UNDO_CLEAR_TTL_MS,
): ComposeUndoBackup | null {
  const key = normalizeScopeKey(sessionId)
  const bucket = ensureSessionComposeState(sessionId)
  const draftToSave = currentDraft !== undefined ? currentDraft : bucket.draft
  const hasContent =
    draftToSave.length > 0 || bucket.activeLenses.length > 0 || bucket.refChips.length > 0

  cancelUndoTimer(key)

  if (!hasContent) {
    bucket.undoBackup = null
    return null
  }

  const now = Date.now()
  const backup: ComposeUndoBackup = {
    draft: draftToSave,
    lenses: [...bucket.activeLenses],
    refs: [...bucket.refChips],
    clearedAt: now,
    expiresAt: now + ttlMs,
  }

  bucket.draft = ''
  bucket.activeLenses = []
  bucket.refChips = []
  bucket.undoBackup = backup
  writeStoredDraft(sessionId, '')

  const timer = setTimeout(() => {
    const target = sessionBuckets[key]
    if (target && target.undoBackup?.clearedAt === backup.clearedAt) {
      target.undoBackup = null
    }
    undoTimers.delete(key)
  }, ttlMs)
  undoTimers.set(key, timer)

  return backup
}

/**
 * Restore the 6000ms backup for `sessionId` if still within TTL.
 */
export function restoreSessionComposeUndo(sessionId?: string): ComposeUndoBackup | null {
  const key = normalizeScopeKey(sessionId)
  const bucket = ensureSessionComposeState(sessionId)
  const backup = bucket.undoBackup
  cancelUndoTimer(key)
  if (!backup) return null
  if (Date.now() > backup.expiresAt) {
    bucket.undoBackup = null
    return null
  }

  bucket.draft = backup.draft
  bucket.activeLenses = [...backup.lenses]
  bucket.refChips = [...backup.refs]
  bucket.undoBackup = null
  writeStoredDraft(sessionId, backup.draft)
  return backup
}

export function resetComposeLensesStoreForTests(): void {
  for (const timer of undoTimers.values()) {
    clearTimeout(timer)
  }
  undoTimers.clear()
  for (const key of Object.keys(sessionBuckets)) {
    delete sessionBuckets[key]
  }
}

export function useComposeLenses(sessionScope?: () => string | undefined) {
  const arsenalOpen = ref(false)
  const phraseFilter = ref<PhraseFilter>('all')
  const phraseQuery = ref('')

  const bucket = computed(() => ensureSessionComposeState(sessionScope?.()))
  const activeLenses = computed(() => bucket.value.activeLenses)
  const refChips = computed(() => bucket.value.refChips)
  const sharedDraft = computed({
    get: () => bucket.value.draft,
    set: (val: string) => setSessionDraft(sessionScope?.(), val),
  })
  const undoBackup = computed(() => bucket.value.undoBackup)
  const hasUndoBackup = computed(() => bucket.value.undoBackup !== null)

  const pinnedLenses = computed(() => BUILTIN_PHRASES.filter((p) => p.kind === 'lens' && p.pinned))
  const activeLensObjects = computed(() =>
    bucket.value.activeLenses
      .map((id) => BUILTIN_PHRASES.find((p) => p.kind === 'lens' && p.id === id))
      .filter((p): p is ComposePhrase => Boolean(p)),
  )
  const filteredPhrases = computed(() =>
    filterPhrases(BUILTIN_PHRASES, phraseFilter.value, phraseQuery.value),
  )
  const hasContextChips = computed(
    () => bucket.value.activeLenses.length > 0 || bucket.value.refChips.length > 0,
  )

  function isLensActive(lensId: string): boolean {
    return bucket.value.activeLenses.includes(lensId)
  }

  function toggleLens(lensId: string): boolean {
    return toggleSessionLens(sessionScope?.(), lensId)
  }

  function addRefChip(rawRef: string): string | null {
    return addSessionRefChip(sessionScope?.(), rawRef)
  }

  function removeRefChip(rawRef: string): void {
    removeSessionRefChip(sessionScope?.(), rawRef)
  }

  function toggleArsenal(): void {
    arsenalOpen.value = !arsenalOpen.value
  }

  function closeArsenal(): void {
    arsenalOpen.value = false
  }

  function checkSlashTrigger(currentText: string): boolean {
    if (shouldOpenSlashArsenal(currentText)) {
      arsenalOpen.value = true
      return true
    }
    return false
  }

  function insertTemplateAtRange(
    currentText: string,
    start: number,
    end: number,
    phraseOrSnippet: ComposePhrase | string,
  ): { text: string; cursor: number } {
    const snippet =
      typeof phraseOrSnippet === 'string' ? phraseOrSnippet : phraseOrSnippet.prompt
    const stripped = stripTrailingSlashTrigger(currentText, start)
    const adjStart = stripped.stripped ? stripped.caret : start
    const adjEnd = stripped.stripped ? Math.max(adjStart, end - (currentText.length - stripped.text.length)) : end
    const res = insertAtCursorRange(stripped.text, adjStart, adjEnd, snippet)
    setSessionDraft(sessionScope?.(), res.text)
    arsenalOpen.value = false
    return { text: res.text, cursor: res.cursor }
  }

  function assemblePayload(currentDraft?: string): string {
    const draft = currentDraft !== undefined ? currentDraft : bucket.value.draft
    return composePayloadWithLenses(draft, bucket.value.activeLenses, bucket.value.refChips)
  }

  function clearAfterSend(): void {
    clearSessionComposeOnSend(sessionScope?.())
    arsenalOpen.value = false
  }

  function clearWithUndo(currentDraft?: string, ttlMs = UNDO_CLEAR_TTL_MS): ComposeUndoBackup | null {
    return clearSessionComposeWithUndo(sessionScope?.(), currentDraft, ttlMs)
  }

  function undoClear(): ComposeUndoBackup | null {
    return restoreSessionComposeUndo(sessionScope?.())
  }

  return {
    phrases: BUILTIN_PHRASES,
    pinnedLenses,
    activeLenses,
    activeLensObjects,
    refChips,
    sharedDraft,
    undoBackup,
    hasUndoBackup,
    hasContextChips,
    arsenalOpen,
    phraseFilter,
    phraseQuery,
    filteredPhrases,
    isLensActive,
    toggleLens,
    addRefChip,
    removeRefChip,
    toggleArsenal,
    closeArsenal,
    checkSlashTrigger,
    insertTemplateAtRange,
    assemblePayload,
    clearAfterSend,
    clearWithUndo,
    undoClear,
  }
}
