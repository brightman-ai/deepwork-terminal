<script setup lang="ts">
/**
 * TerminalSearchBar — 终端内查找条(Ctrl/Cmd+F 等价物)。
 *
 * PC 用户对"终端"的基本预期之一就是能在几千行回滚里定位一条报错；此前只装了 addon-fit /
 * addon-web-links，没有查找能力，滚回去肉眼找是一票否决项。这个组件只管 UI + 交互，实际的
 * buffer 搜索能力在 XtermTerminal.vue（@xterm/addon-search 的薄封装：findNext/findPrevious/
 * clearSearch + searchResultIndex/searchResultCount）。
 *
 * 视觉语言参照 DrawerSearchBox.vue 的既有primitve（放大镜 + 输入框 + 清除×，同样的 size-3.5
 * 图标/gap-1.5 间距/text-xs 字号节奏），但配色改成这个目录下浮层的既有约定
 * (KeyCastrOverlay/UploadProgressFloat)：终端本身固定深色主题（XtermTerminal.vue 里 background
 * 硬编码 #1e1e1e，不跟随 app 的亮/暗色 token），浮在它上面的控件用固定深色调色板而不是
 * hsl(var(--foreground)) 这类随主题切换的 token —— 否则亮色模式下这条查找条会和恒暗的终端底色
 * 冲突到无法辨认。
 *
 * 实时增量搜索：每次 input 都会用 incremental:true 调 findNext（键入时随打随搜，符合
 * VS Code/浏览器地址栏查找的现代习惯）；Enter/Shift+Enter 是显式的"跳到下一个/上一个"；
 * Escape 关闭并把焦点还给终端 —— 这三个键位只在查找条本身聚焦时生效，从不拦截别处的按键。
 */
import { ref, nextTick, watch } from 'vue'
import { Search, X, ChevronUp, ChevronDown, CaseSensitive, WholeWord, Regex } from 'lucide-vue-next'
import type { TerminalFindOptions } from './terminalSearchOptions'

const props = defineProps<{
  /** 0-based index of the currently active match, -1 = none (see XtermTerminal's searchResultIndex). */
  resultIndex: number
  /** Total match count for the current query. */
  resultCount: number
}>()

const emit = defineEmits<{
  /** Fired on every keystroke (incremental) AND on a toggle flip — the parent owns calling into
   *  xterm's findNext. Every event carries the full term+options: this component is the sole owner
   *  of that state, so the host never has to track a duplicate copy of "what is being searched". */
  (e: 'search', term: string, options: TerminalFindOptions, incremental: boolean): void
  (e: 'next', term: string, options: TerminalFindOptions): void
  (e: 'previous', term: string, options: TerminalFindOptions): void
  (e: 'close'): void
}>()

const query = ref('')
const caseSensitive = ref(false)
const wholeWord = ref(false)
const regex = ref(false)
const inputEl = ref<HTMLInputElement>()

function options(): TerminalFindOptions {
  return { caseSensitive: caseSensitive.value, wholeWord: wholeWord.value, regex: regex.value }
}

// Autofocus the moment the bar mounts (it is v-if'd in by the host, so `onMounted`-time is
// "just opened"). nextTick because the <input> is only in the DOM after this component renders.
void nextTick(() => inputEl.value?.focus())

function onInput(): void {
  emit('search', query.value, options(), true)
}

/** Re-run the CURRENT query when a toggle (case/whole-word/regex) flips — otherwise flipping a
 *  toggle would silently leave stale results/highlights on screen until the next keystroke. */
function rerunSearch(): void {
  if (!query.value) return
  emit('search', query.value, options(), false)
}

function toggleCaseSensitive(): void { caseSensitive.value = !caseSensitive.value; rerunSearch() }
function toggleWholeWord(): void { wholeWord.value = !wholeWord.value; rerunSearch() }
function toggleRegex(): void { regex.value = !regex.value; rerunSearch() }

function clearQuery(): void {
  query.value = ''
  emit('search', '', options(), false)
  inputEl.value?.focus()
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter') {
    e.preventDefault()
    if (e.shiftKey) emit('previous', query.value, options())
    else emit('next', query.value, options())
    return
  }
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
  }
  // Every other key (including a bare Ctrl+F, should it reach here) is left completely alone —
  // this bar only ever intercepts Enter/Shift+Enter/Escape, never anything a text input needs.
}

// Whether to show a "no match" state — only once the user actually typed something, never on the
// still-empty query right after opening (that would read as a false "搜不到" before typing a key).
const showNoMatch = ref(false)
watch([() => props.resultCount, query], ([count]) => {
  showNoMatch.value = query.value.length > 0 && count === 0
})

defineExpose({
  /** 聚焦输入框。`selectAll` 时连带全选已有查询词 —— 搜索条已经开着时再按一次 Cmd+F，
   *  用户要的是"换个词重搜"，光把光标放回去还得自己先清空。 */
  focus: (selectAll = false) => {
    const el = inputEl.value
    if (!el) return
    el.focus()
    if (selectAll) el.select()
  },
})
</script>

<template>
  <div class="tsb-root" data-testid="terminal-search-bar" @keydown="onKeydown">
    <Search class="tsb-icon" aria-hidden="true" />
    <input
      ref="inputEl"
      v-model="query"
      type="text"
      class="tsb-input"
      data-testid="terminal-search-input"
      placeholder="在终端中搜索…"
      autocomplete="off"
      autocorrect="off"
      autocapitalize="off"
      spellcheck="false"
      aria-label="在终端中搜索"
      @input="onInput"
    />
    <button
      v-if="query"
      class="tsb-btn tsb-clear"
      type="button"
      title="清除"
      aria-label="清除"
      data-testid="terminal-search-clear"
      @click="clearQuery"
    ><X class="tsb-icon-sm" /></button>

    <!-- 匹配数：无匹配时也要可见，不能静默失败。 -->
    <span
      class="tsb-count"
      :class="{ 'tsb-count--empty': showNoMatch }"
      data-testid="terminal-search-count"
    >{{ query ? (showNoMatch ? '无匹配' : `${resultIndex + 1}/${resultCount}`) : '' }}</span>

    <span class="tsb-sep" aria-hidden="true" />

    <button
      class="tsb-btn tsb-toggle"
      :class="{ on: caseSensitive }"
      type="button"
      title="区分大小写"
      aria-label="区分大小写"
      :aria-pressed="caseSensitive"
      data-testid="terminal-search-case"
      @click="toggleCaseSensitive"
    ><CaseSensitive class="tsb-icon-sm" /></button>
    <button
      class="tsb-btn tsb-toggle"
      :class="{ on: wholeWord }"
      type="button"
      title="全词匹配"
      aria-label="全词匹配"
      :aria-pressed="wholeWord"
      data-testid="terminal-search-whole-word"
      @click="toggleWholeWord"
    ><WholeWord class="tsb-icon-sm" /></button>
    <button
      class="tsb-btn tsb-toggle"
      :class="{ on: regex }"
      type="button"
      title="正则表达式"
      aria-label="正则表达式"
      :aria-pressed="regex"
      data-testid="terminal-search-regex"
      @click="toggleRegex"
    ><Regex class="tsb-icon-sm" /></button>

    <span class="tsb-sep" aria-hidden="true" />

    <button
      class="tsb-btn"
      type="button"
      title="上一个 (Shift+Enter)"
      aria-label="上一个匹配"
      :disabled="!query"
      data-testid="terminal-search-prev"
      @click="$emit('previous', query, options())"
    ><ChevronUp class="tsb-icon-sm" /></button>
    <button
      class="tsb-btn"
      type="button"
      title="下一个 (Enter)"
      aria-label="下一个匹配"
      :disabled="!query"
      data-testid="terminal-search-next"
      @click="$emit('next', query, options())"
    ><ChevronDown class="tsb-icon-sm" /></button>

    <button
      class="tsb-btn tsb-close"
      type="button"
      title="关闭 (Esc)"
      aria-label="关闭查找"
      data-testid="terminal-search-close"
      @click="$emit('close')"
    ><X class="tsb-icon-sm" /></button>
  </div>
</template>

<style scoped>
/* Docked along the BOTTOM edge of .terminal-body (its parent, CliTerminalSurface's
   `.terminal-body`, is already `position: relative`) — deliberately NOT top-right, which is
   already claimed by the notify/install icon row (top:4/z40) and the upload-progress pill
   (top:40/z45); anchoring here means this bar can never fight either of them for the same pixels.
   Fixed dark palette (not the app's hsl(var(--foreground)) theme tokens) to match the terminal's
   own always-dark background — see KeyCastrOverlay.vue / UploadProgressFloat.vue for the same
   convention on this file's siblings. */
.tsb-root {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 60;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  background: rgba(20, 20, 28, 0.96);
  border-top: 1px solid #2a2a45;
  box-shadow: 0 -2px 10px rgba(0, 0, 0, 0.35);
  backdrop-filter: blur(4px);
  font-size: 0.8rem;
}

.tsb-icon {
  flex-shrink: 0;
  width: 14px;
  height: 14px;
  color: #8a8ab0;
}
.tsb-icon-sm {
  width: 13px;
  height: 13px;
}

.tsb-input {
  flex: 1;
  min-width: 0;
  background: transparent;
  border: none;
  outline: none;
  color: #e8e8ee;
  font-size: 0.8rem;
  font-family: ui-monospace, 'Cascadia Code', Menlo, monospace;
}
.tsb-input::placeholder {
  color: #6a6a86;
}

.tsb-count {
  flex-shrink: 0;
  min-width: 3.5em;
  text-align: right;
  color: #8a8ab0;
  font-variant-numeric: tabular-nums;
  font-size: 0.72rem;
}
.tsb-count--empty {
  color: #ef8a8a;
}

.tsb-sep {
  flex-shrink: 0;
  width: 1px;
  height: 16px;
  background: #33334a;
}

.tsb-btn {
  flex-shrink: 0;
  display: inline-grid;
  place-items: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: 5px;
  background: transparent;
  color: #b8b8cc;
  cursor: pointer;
  touch-action: manipulation;
}
.tsb-btn:hover {
  background: rgba(255, 255, 255, 0.08);
  color: #e8e8ee;
}
.tsb-btn:active {
  transform: scale(0.94);
}
.tsb-btn:disabled {
  color: #55556a;
  cursor: default;
}
.tsb-btn:disabled:hover {
  background: transparent;
}
.tsb-btn:disabled:active {
  transform: none;
}

.tsb-toggle.on {
  border-color: #f5b342;
  color: #f5b342;
  background: rgba(245, 179, 66, 0.12);
}

.tsb-close {
  color: #b8b8cc;
}
</style>
