<script setup lang="ts">
/**
 * CopyModeView — 只读回看视口（`Ctrl+B` `[` 进入，`Esc` 退出）。
 *
 * ── 它为什么是一层覆盖，而不是"让 xterm 多滚一点" ──────────────────────────────────────────────
 * xterm 只能追加，没有 prepend。要往回滚到第 1 行，要么把整段历史重新灌进 xterm（正是「F5 不要刷
 * 20 万行」禁止的事，只是换了触发时机），要么另开一个视口看同一份历史。tmux 的 copy-mode 本来就是
 * 后者，使用者的肌肉记忆也长这样。
 *
 * 下面那个终端**原封不动地留在 DOM 里**：实时输出照收，滚动位置一格没动，Esc 一按就回到原样。
 *
 * ── 三条硬约束 ──────────────────────────────────────────────────────────────────────────────────
 * ① **一个按键都不许漏进 PTY。** 进了模式就全吞：漏一个字符进 shell，比这个功能不存在更糟。
 *    截获挂在**捕获阶段的 document 上**，先于 xterm 的隐藏 textarea 拿到事件。
 * ② **原生文本选择。** 这是 DOM 文本不是 canvas，所以拖选、双击选词、Cmd+C 全是浏览器原生行为 ——
 *    顺带治了"全屏 TUI 里选不动字"这个老毛病。所以容器**不能**设 user-select:none。
 * ③ **配色跟着终端走。** 调色板下标解析成 `--dw-ansi-N` 变量，值在这里按 xterm 的主题定义一次；
 *    绝不在数据层硬编码颜色（那会把今天的主题烤进比它活得久的历史里）。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  lineText,
  styleToCss,
  type HistoryMatch,
  type TerminalHistory,
} from '../../composables/cli/useTerminalHistory'

const props = defineProps<{
  history: TerminalHistory
  /** 标签名，印在标题栏上——多标签时它是唯一能说清"这是谁的历史"的东西。 */
  title?: string
}>()
const emit = defineEmits<{ (e: 'close'): void }>()

const scroller = ref<HTMLElement | null>(null)
const h = props.history

/**
 * ── 窗口化渲染 ──────────────────────────────────────────────────────────────────────────────────
 *
 * 只渲染视口里那几十行，其余用一个撑高的占位块表示。
 *
 * 这不是"锦上添花的优化"：初版对全部已载入行做 `v-for`，实测载入到约 2000 行时页面就开始卡到
 * 无法操作（一行几个 span，两千行就是上万个 DOM 节点）；而这个功能的默认深度是 **5 万行**。
 * 没有窗口化，它在自己的设计目标上必然废掉。
 *
 * 行高必须是**固定**的，才能由行号直接算出位置。所以 CSS 里写死 `line-height`，并在挂载后量一次
 * 真实行高校准（字体加载/缩放会让计算值和实际值差几个像素，累积到几千行就是几屏的偏移）。
 */
const ROW_H = ref(18)
const viewTop = ref(0)
const viewH = ref(600)
const OVERSCAN = 20

const startIndex = computed(() =>
  Math.max(0, Math.floor(viewTop.value / ROW_H.value) - OVERSCAN))
const endIndex = computed(() =>
  Math.min(h.lines.value.length, startIndex.value + Math.ceil(viewH.value / ROW_H.value) + OVERSCAN * 2))
/** 视口里真正要挂到 DOM 上的那几十行。 */
const windowLines = computed(() => h.lines.value.slice(startIndex.value, endIndex.value))
const totalHeight = computed(() => h.lines.value.length * ROW_H.value)
const offsetTop = computed(() => startIndex.value * ROW_H.value)

function syncViewport(): void {
  const el = scroller.value
  if (!el) return
  viewTop.value = el.scrollTop
  viewH.value = el.clientHeight
}

/** 量一次真实行高。算出来的和渲染出来的差几个像素，累到几千行就是几屏的偏移。 */
function measureRowHeight(): void {
  const row = scroller.value?.querySelector<HTMLElement>('.copy-mode__line')
  if (row) {
    const rect = row.getBoundingClientRect()
    if (rect.height > 4) ROW_H.value = rect.height
  }
}
const searchInput = ref<HTMLInputElement | null>(null)
const query = ref('')
const matches = ref<HistoryMatch[]>([])
const matchIndex = ref(-1)
const searching = ref(false)
const searchOpen = ref(false)

/** 历史到头了吗——"已经是最早的一行"和"更早的被淘汰了"是两回事，标题栏要说得出区别。 */
const atOldest = computed(() => h.lines.value.length > 0 && h.lines.value[0].n <= h.base.value)
const evicted = computed(() => h.base.value > 0)

const statusText = computed(() => {
  if (!h.enabled.value) {
    return h.reason.value === 'daemon-too-old'
      ? '这个会话还没有历史：常驻进程（muxd）比当前程序旧，需要重启它才会开始记录'
      : '这个会话没有开启历史'
  }
  if (h.broken.value) return '历史记录中途出错已停止增长（终端本身不受影响）'
  const held = h.total.value - h.base.value
  const shown = h.lines.value.length
  // shown 会比 held 略多，因为它还含当前可见屏（那部分还没滚出去、不算历史）。说清楚，别让
  // 「已载入 3206 / 3143 行历史」看起来像个 bug。
  return `${held} 行历史 · 已载入 ${shown}（含当前屏）${evicted.value ? ` · 更早的 ${h.base.value} 行已被淘汰` : ''}`
})

const currentMatch = computed(() =>
  matchIndex.value >= 0 && matchIndex.value < matches.value.length ? matches.value[matchIndex.value] : null,
)

/**
 * 跳到某一行。**按下标算位置**，不用 scrollIntoView —— 窗口化之后目标行多半根本不在 DOM 里，
 * `querySelector` 会返回 null，跳转会安静地什么都不做。
 */
function scrollToLine(n: number): void {
  nextTick(() => {
    const el = scroller.value
    if (!el) return
    const idx = h.lines.value.findIndex((l) => l.n === n)
    if (idx < 0) return
    el.scrollTop = Math.max(0, idx * ROW_H.value - el.clientHeight / 2)
    syncViewport()
  })
}

/**
 * 搜索。**默认往回找（更早的）**，Shift+Enter 才往后。
 *
 * 因为打开搜索框时人在最新处：从这里"向后"找必然是零结果 —— 初版就是这么写的，实测输入一个确实
 * 存在的词，界面显示"无匹配"。人在这个位置想找的永远是「最近一次出现的 X」，那是向**更早**扫。
 *
 * 锚点取**当前视口顶端那一行**，不是 total：「下一个匹配」是相对你现在在看哪儿说的，不是相对
 * 会话的末尾。
 */
async function runSearch(backward: boolean): Promise<void> {
  const q = query.value.trim()
  if (!q) {
    matches.value = []
    matchIndex.value = -1
    return
  }
  searching.value = true
  try {
    // 一次**全新**的搜索，锚点取已载入的**最新**一行，不是视口顶端那一行。
    //
    // 真机上撞到的：视口停在底部时，"视口顶端"仍然在最后几十行之上；向回搜从那里起步，会把它下方
    // （更新的）那些行整段跳过 —— 搜一个明明就在屏幕上的词，结果是"无匹配"。
    // 「下一个匹配」（已有 currentMatch）才是相对你现在停在哪儿说的。
    const newest = h.lines.value[h.lines.value.length - 1]?.n ?? h.total.value
    const anchor = currentMatch.value?.n ?? newest
    const from = backward ? anchor : anchor + 1
    const found = await h.search(q, from, backward)
    if (found.length > 0) {
      matches.value = found
      matchIndex.value = 0
      await h.ensureVisible(found[0].n)
      scrollToLine(found[0].n)
    } else {
      matches.value = []
      matchIndex.value = -1
    }
  } finally {
    searching.value = false
  }
}

function stepMatch(delta: number): void {
  if (matches.value.length === 0) return
  matchIndex.value = (matchIndex.value + delta + matches.value.length) % matches.value.length
  const m = matches.value[matchIndex.value]
  void h.ensureVisible(m.n).then(() => scrollToLine(m.n))
}

/** 滚到顶就向更早补一页，并**保住视觉锚点**：不这么做，新行插进来会把你正在看的那行顶走。 */
async function onScroll(): Promise<void> {
  const el = scroller.value
  if (!el) return
  syncViewport()
  if (h.loading.value) return
  if (el.scrollTop > 80) return
  const before = el.scrollTop
  const added = await h.loadOlder()
  if (added > 0) {
    await nextTick()
    // 视觉锚点：新行插在前面会把你正在看的那行顶走。窗口化之后高度是算出来的，所以直接按
    // 新增行数补，而不是量 scrollHeight（量到的可能还是上一帧的）。
    el.scrollTop = before + added * ROW_H.value
    syncViewport()
  }
}

function pageBy(fraction: number): void {
  const el = scroller.value
  if (!el) return
  el.scrollTop += el.clientHeight * fraction
  syncViewport()
  void onScroll()
}

/**
 * 键盘：**全部吞掉**。
 *
 * 挂在 document 的捕获阶段，因为 xterm 的隐藏 textarea 才是终端的输入通道 —— 冒泡阶段拦已经晚了，
 * 那时字符已经在去 PTY 的路上。这是约束 ①，不是优化。
 */
function onKeydown(e: KeyboardEvent): void {
  // 搜索框里只放行编辑用的键，其余照样吞。
  const inSearchBox = e.target === searchInput.value
  if (inSearchBox) {
    if (e.key === 'Escape') {
      e.preventDefault(); e.stopPropagation()
      closeSearch()
      return
    }
    if (e.key === 'Enter') {
      e.preventDefault(); e.stopPropagation()
      void runSearch(!e.shiftKey)
      return
    }
    e.stopPropagation() // 不 preventDefault：让输入框正常收字
    return
  }

  e.preventDefault()
  e.stopPropagation()
  switch (e.key) {
    case 'Escape':
    case 'q':
      emit('close'); break
    case 'PageUp': pageBy(-0.9); break
    case 'PageDown': pageBy(0.9); break
    case 'ArrowUp': case 'k': pageBy(-0.06); break
    case 'ArrowDown': case 'j': pageBy(0.06); break
    case 'Home': case 'g':
      // 到当前已载入的最早一行；再往上靠触顶补页继续。
      scroller.value?.scrollTo({ top: 0 })
      void onScroll()
      break
    case 'End': case 'G':
      if (scroller.value) {
        scroller.value.scrollTop = totalHeight.value
        syncViewport()
      }
      break
    case '/':
      openSearch(); break
    case 'n': stepMatch(1); break
    case 'N': stepMatch(-1); break
    default:
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') openSearch()
      // 其余一律吞掉（约束 ①）。Cmd/Ctrl+C 是例外：浏览器的复制走的是 copy 事件，
      // 不依赖这里放行 keydown，所以吞掉它不影响复制。
  }
}

function openSearch(): void {
  const wasOpen = searchOpen.value
  searchOpen.value = true
  nextTick(() => {
    // 首次打开时清空：开框的那个 `/` 不该成为查询词的一部分（less/vim/tmux 都不会）。
    // 真键盘上它本来也进不来（keydown 被 preventDefault，而且那一刻输入框还没拿到焦点），
    // 但别的输入路径不产生可被 preventDefault 的 keydown —— 与按键不许漏进 PTY 是同一类问题，
    // 所以这里同样不能只靠 keydown 那一层。
    if (!wasOpen) query.value = ''
    searchInput.value?.focus()
    searchInput.value?.select()
  })
}

function closeSearch(): void {
  searchOpen.value = false
  matches.value = []
  matchIndex.value = -1
  query.value = ''
}

onMounted(async () => {
  document.addEventListener('keydown', onKeydown, true)
  await h.open()
  await nextTick()
  syncViewport()
  measureRowHeight()
  await nextTick()
  // 打开时停在最新处：使用者是从实时终端切过来的，接着往回翻才自然。
  if (scroller.value) {
    scroller.value.scrollTop = totalHeight.value
    syncViewport()
  }
  window.addEventListener('resize', syncViewport)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKeydown, true)
  window.removeEventListener('resize', syncViewport)
})

watch(() => h.lines.value.length, () => { nextTick(syncViewport) })
</script>

<template>
  <div class="copy-mode" data-testid="copy-mode">
    <div class="copy-mode__bar">
      <span class="copy-mode__badge">回看历史</span>
      <span v-if="title" class="copy-mode__title">{{ title }}</span>
      <span class="copy-mode__status" data-testid="copy-mode-status">{{ statusText }}</span>
      <span class="copy-mode__spacer" />
      <span v-if="atOldest" class="copy-mode__hint">已到最早</span>
      <button class="copy-mode__btn" data-testid="copy-mode-search" @click="openSearch">搜索 /</button>
      <button class="copy-mode__btn" data-testid="copy-mode-close" @click="emit('close')">退出 Esc</button>
    </div>

    <div v-if="searchOpen" class="copy-mode__search">
      <input
        ref="searchInput"
        v-model="query"
        class="copy-mode__input"
        data-testid="copy-mode-search-input"
        placeholder="在这个终端的历史里查找…"
        @keydown.enter.prevent="runSearch(true)"
      >
      <span class="copy-mode__count">
        {{ searching ? '搜索中…' : matches.length ? `${matchIndex + 1}/${matches.length}` : query ? '无匹配' : '' }}
      </span>
      <button class="copy-mode__btn" data-testid="copy-mode-prev" @click="stepMatch(-1)">↑</button>
      <button class="copy-mode__btn" data-testid="copy-mode-next" @click="stepMatch(1)">↓</button>
      <button class="copy-mode__btn" @click="closeSearch">关闭</button>
    </div>

    <div
      ref="scroller"
      class="copy-mode__scroll"
      data-testid="copy-mode-scroll"
      @scroll.passive="onScroll"
    >
      <div v-if="h.loading.value && h.lines.value.length === 0" class="copy-mode__empty">载入中…</div>
      <div v-else-if="!h.enabled.value" class="copy-mode__empty">{{ statusText }}</div>
      <div v-else-if="h.lines.value.length === 0" class="copy-mode__empty">
        这个终端还没有滚出去的历史。
      </div>
      <!-- 撑高块 + 绝对定位的可视窗口：DOM 里始终只有几十行，与历史深度无关。 -->
      <div v-else class="copy-mode__sizer" :style="{ height: totalHeight + 'px' }">
        <div class="copy-mode__window" :style="{ transform: `translateY(${offsetTop}px)` }">
          <div
            v-for="line in windowLines"
            :key="line.n"
            class="copy-mode__line"
            :class="{ 'copy-mode__line--hit': currentMatch && currentMatch.n === line.n }"
            :data-line="line.n"
            :style="{ height: ROW_H + 'px' }"
          >
            <span class="copy-mode__num">{{ line.n }}</span>
            <span class="copy-mode__text"><span
              v-for="(seg, i) in line.seg"
              :key="i"
              :style="styleToCss(h.styles.value[seg.s])"
            >{{ seg.t }}</span><span v-if="line.seg.length === 0">&nbsp;</span></span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 调色板变量在这里定义一次，值与 XtermTerminal 的主题同源。数据层只给下标，绝不给颜色。 */
.copy-mode {
  --dw-term-bg: #1e1e1e;
  --dw-term-fg: #d4d4d4;
  --dw-ansi-0: #000000;  --dw-ansi-1: #cd3131;  --dw-ansi-2: #0dbc79;  --dw-ansi-3: #e5e510;
  --dw-ansi-4: #2472c8;  --dw-ansi-5: #bc3fbc;  --dw-ansi-6: #11a8cd;  --dw-ansi-7: #e5e5e5;
  --dw-ansi-8: #666666;  --dw-ansi-9: #f14c4c;  --dw-ansi-10: #23d18b; --dw-ansi-11: #f5f543;
  --dw-ansi-12: #3b8eea; --dw-ansi-13: #d670d6; --dw-ansi-14: #29b8db; --dw-ansi-15: #ffffff;

  position: absolute;
  inset: 0;
  z-index: 30;
  display: flex;
  flex-direction: column;
  background: var(--dw-term-bg);
  color: var(--dw-term-fg);
}

.copy-mode__bar,
.copy-mode__search {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  background: #252526;
  border-bottom: 1px solid #333;
  font-size: 12px;
  flex: 0 0 auto;
}

.copy-mode__badge {
  padding: 1px 6px;
  border-radius: 3px;
  background: #3b8eea;
  color: #fff;
  font-weight: 600;
}

.copy-mode__title { color: #d4d4d4; font-weight: 600; }
.copy-mode__status { color: #8b949e; }
.copy-mode__spacer { flex: 1 1 auto; }
.copy-mode__hint { color: #8b949e; }

.copy-mode__btn {
  padding: 2px 8px;
  border: 1px solid #3c3c3c;
  border-radius: 3px;
  background: #2d2d2d;
  color: #d4d4d4;
  font-size: 12px;
  cursor: pointer;
}
.copy-mode__btn:hover { background: #3a3a3a; }

.copy-mode__input {
  flex: 1 1 auto;
  min-width: 0;
  padding: 3px 8px;
  border: 1px solid #3c3c3c;
  border-radius: 3px;
  background: #1e1e1e;
  color: #d4d4d4;
  font-size: 12px;
}
.copy-mode__count { color: #8b949e; min-width: 64px; }

.copy-mode__scroll {
  flex: 1 1 auto;
  overflow-y: auto;
  overflow-x: auto;
  padding: 4px 0 12px;
  /* 原生选择——约束 ②。这是 DOM 文本，所以拖选/双击选词/Cmd+C 全都是浏览器自己的行为。 */
  user-select: text;
  -webkit-user-select: text;
}

.copy-mode__sizer { position: relative; }
.copy-mode__window { position: absolute; inset: 0 0 auto 0; will-change: transform; }

.copy-mode__line {
  display: flex;
  align-items: flex-start;
  font-family: 'Cascadia Code', 'Fira Code', 'Source Code Pro', Menlo, Monaco, monospace;
  font-size: 13px;
  /* 固定行高：位置是按行号算出来的，行高一浮动，几千行之后就会差出几屏。 */
  line-height: 18px;
  height: 18px;
  white-space: pre;
}
.copy-mode__line--hit { background: rgba(59, 142, 234, 0.18); }

.copy-mode__num {
  flex: 0 0 auto;
  width: 6ch;
  padding-right: 10px;
  text-align: right;
  color: #565656;
  /* 行号是导航用的装饰，复制整段时不该混进去。 */
  user-select: none;
  -webkit-user-select: none;
}

.copy-mode__text { flex: 1 1 auto; }

.copy-mode__empty {
  padding: 24px;
  color: #8b949e;
  font-size: 13px;
}
</style>
