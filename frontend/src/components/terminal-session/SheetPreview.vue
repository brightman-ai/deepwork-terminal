<script setup lang="ts">
/**
 * SheetPreview — 表格族（xlsx / xls / csv / tsv）的预览。**一个组件两种来源，一种观感。**
 *
 * ── 为什么 xlsx 走服务端 HTML 而不是转 pdf ────────────────────────────────────────────────────
 * 「预览要支持内容复制」这条要求直接排除了 pdf：canvas 上一个字也选不中。2026-09-08 实测
 * libreoffice 的 HTML 导出：4.3MB 的表转出 410KB html，**3 个 `<table>` 就是 3 个工作表**，
 * 合并单元格（colspan/rowspan）保住，工作表名带在锚点里，产物里 `<script>`/`on*` 事件为 0。
 * 文本是真 HTML —— 拖选、Ctrl+C、粘到别处保行列结构，全是浏览器原生行为，我们一行都不用写。
 * 代价写在规格的非目标里：图表/条件格式不保证还原。要看原版式：下载。
 *
 * ── 为什么 csv 不走同一条路 ───────────────────────────────────────────────────────────────────
 * csv 已经是文本，为它付一次 libreoffice 进程启动（~2s）只为得到一张表，是拿延迟换零收益。
 * 客户端解析（csvTable.ts）即时出表，**渲染成同一套 DOM/样式** —— 两种来源，一种观感。
 *
 * ── 安全 ──────────────────────────────────────────────────────────────────────────────────────
 * 产物虽由本机 libreoffice 生成，仍**一律过 DOMPurify** 再进 DOM：它的输入是用户的 xlsx，
 * 而 xlsx 里可以塞任何东西。解析用 DOMParser（惰性文档，脚本不执行）先取结构，再逐段消毒。
 */
import { computed, ref, watch, nextTick } from 'vue'
import DOMPurify from 'dompurify'
import ImageZoomViewer from '@terminal/components/terminal-session/ImageZoomViewer.vue'
import { wireImageZoom } from '@terminal/components/terminal-session/imageZoom'
import { parseCsv, type CsvSheet } from '@terminal/components/terminal-session/csvTable'

const props = defineProps<{
  name: string
  /** libreoffice 转出的 HTML（xlsx/xls）。与 csvText 二选一。 */
  html?: string
  /** csv/tsv 原文。与 html 二选一。 */
  csvText?: string
  /** 内嵌图片（xlsx 里的图表截图等）的取址函数：产物内的相对文件名 → 可请求的 URL。 */
  assetUrl?: (name: string) => string
}>()

interface Sheet { name: string; html: string; rows?: string[][] }

const sheets = ref<Sheet[]>([])
const active = ref(0)
const errorText = ref('')
const truncated = ref(false)
const bodyEl = ref<HTMLElement>()
const zoomSrc = ref('')

/**
 * 从 libreoffice 的 HTML 里切出每个工作表。
 *
 * 它的产物形状（实测）：开头一段 `<h1>概览</h1>` 的目录，然后每个工作表一个
 * `<A NAME="tableN"><h1>工作表 N: <em>表名</em></h1></A>` 锚点 + 一个 `<table>`，
 * 浮动图片跟在所属工作表的表格后面。所以按锚点切段，段内的东西都归那个工作表 —— 这样
 * 图片不会掉队，而开头那段目录（第一个锚点之前）整段丢掉：它在标签页里是重复信息。
 */
function splitLibreOfficeSheets(raw: string): Sheet[] {
  const doc = new DOMParser().parseFromString(raw, 'text/html')
  const anchors = Array.from(doc.body.querySelectorAll<HTMLAnchorElement>('a[name^="table"]'))
  const clean = (frag: string): string => DOMPurify.sanitize(frag, { FORBID_TAGS: ['style', 'script'], FORBID_ATTR: ['style'] })
  if (!anchors.length) {
    // 认不出分节就整体渲染 —— 少一个标签栏，总好过"什么都不显示"
    return [{ name: '', html: clean(doc.body.innerHTML) }]
  }
  const out: Sheet[] = []
  for (let i = 0; i < anchors.length; i++) {
    const start = anchors[i]
    const stop = anchors[i + 1] ?? null
    // 锚点自己带着 <h1>工作表 N: <em>名</em></h1>；名字取 em，取不到就退回整段文本
    const label = start.querySelector('em')?.textContent?.trim()
      || start.textContent?.replace(/^工作表\s*\d+\s*[:：]\s*/, '').trim()
      || `工作表 ${i + 1}`
    const parts: string[] = []
    // 从锚点的顶层祖先开始，逐个兄弟收集到下一个锚点为止
    let node: Node | null = topLevelOf(start, doc.body)
    const stopTop = stop ? topLevelOf(stop, doc.body) : null
    while (node && node !== stopTop) {
      if (node.nodeType === 1) {
        const el = node as HTMLElement
        if (el !== start && !el.contains(start)) parts.push(el.outerHTML)
        else if (el.contains(start) && el !== start) parts.push(el.outerHTML)
      }
      node = node.nextSibling
    }
    out.push({ name: label, html: clean(parts.join('\n')) })
  }
  return out
}

/** 某个节点在 body 下的那个顶层祖先（切段要按顶层兄弟走）。 */
function topLevelOf(node: Node, body: HTMLElement): Node {
  let n: Node = node
  while (n.parentNode && n.parentNode !== body) n = n.parentNode
  return n
}

function build(): void {
  errorText.value = ''
  truncated.value = false
  active.value = 0
  try {
    if (props.html !== undefined) {
      sheets.value = splitLibreOfficeSheets(props.html)
    } else if (props.csvText !== undefined) {
      const ext = props.name.toLowerCase().endsWith('.tsv') ? 'tsv' : 'csv'
      const parsed = parseCsv(props.csvText, { ext })
      truncated.value = parsed.truncated
      sheets.value = parsed.sheets.map((s: CsvSheet, i) => ({
        name: s.name || (parsed.sheets.length > 1 ? `表 ${i + 1}` : ''),
        html: '',
        rows: s.rows,
      }))
    } else {
      sheets.value = []
    }
    if (!sheets.value.length) errorText.value = '这个表格里没有可显示的内容'
  } catch (err) {
    errorText.value = err instanceof Error ? err.message : String(err)
  }
  void nextTick(() => afterRender())
}

/**
 * 渲染后：把内嵌图片的相对 src 改写成可请求的 URL，并接上放大查看器。
 * libreoffice 把 xlsx 里的图导出成产物目录下的兄弟文件，`<img src="xxx_html_43d2.png">` ——
 * 那个名字对浏览器毫无意义，必须换成带 session/path/asset 的地址。
 */
function afterRender(): void {
  const root = bodyEl.value
  if (!root) return
  if (props.assetUrl) {
    root.querySelectorAll<HTMLImageElement>('img').forEach((img) => {
      const raw = img.getAttribute('src') || ''
      if (!raw || /^(https?:|data:|blob:)/i.test(raw)) return
      // libreoffice 把中文文件名写成**百分号编码**（实测 `%E5%A4%9A…png`）。不先解码就再
      // encodeURIComponent 一次 = 双重编码，服务端去找一个名字里真的带 % 的文件 → 404，
      // 表面症状是"图静默不显示"。
      let file = raw.replace(/^\.\//, '')
      try { file = decodeURIComponent(file) } catch { /* 不是合法编码就按原样 */ }
      img.src = props.assetUrl!(file)
    })
  }
  wireImageZoom(root, (src) => { zoomSrc.value = src })
}

watch(() => [props.html, props.csvText, props.name], build, { immediate: true })

const current = computed(() => sheets.value[active.value])
const multi = computed(() => sheets.value.length > 1)
function pick(i: number): void { active.value = i; void nextTick(() => afterRender()) }
</script>

<template>
  <div class="relative flex h-full flex-col" data-testid="fp-preview-sheet">
    <div v-if="errorText" class="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
      <p class="text-xs text-muted-foreground">表格预览失败</p>
      <p class="max-w-full text-[0.62rem] text-muted-foreground/70 break-all">{{ errorText }}</p>
      <p class="text-[0.62rem] text-muted-foreground/70">可用上方「下载」在本地应用中打开</p>
    </div>

    <template v-else>
      <!-- 工作表标签：只有多表时才占那一行高度 -->
      <div v-if="multi" class="sheet-tabs" data-testid="fp-sheet-tabs">
        <button
          v-for="(s, i) in sheets"
          :key="i"
          type="button"
          class="sheet-tab"
          :class="{ 'sheet-tab--on': i === active }"
          :data-testid="`fp-sheet-tab-${i}`"
          :title="s.name"
          @click="pick(i)"
        >{{ s.name || `表 ${i + 1}` }}</button>
      </div>

      <div ref="bodyEl" class="sheet-body" data-testid="fp-sheet-body">
        <!-- xlsx/xls：消毒后的 libreoffice 产物（表格 + 内嵌图） -->
        <div v-if="current && !current.rows" v-html="current.html"></div>
        <!-- csv/tsv：本地解析出的行，渲染成同一套表格 -->
        <table v-else-if="current && current.rows">
          <tbody>
            <tr v-for="(r, ri) in current.rows" :key="ri">
              <td v-for="(c, ci) in r" :key="ci">{{ c }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <p v-if="truncated" class="sheet-note" data-testid="fp-sheet-truncated">
        行数过多，只显示了前面一部分——完整内容请用上方「下载」。
      </p>
    </template>

    <ImageZoomViewer v-if="zoomSrc" :src="zoomSrc" @close="zoomSrc = ''" />
  </div>
</template>

<style scoped>
.sheet-tabs {
  display: flex;
  gap: 4px;
  overflow-x: auto;
  padding: 6px 8px;
  border-bottom: 1px solid var(--border, #2a2a2a);
  flex: 0 0 auto;
}
.sheet-tab {
  /* 触控目标：实测 393px 下原来只有 21px 高，手指点不准（移动端一等公民，见 spec §7）。
     34px + 容器 padding ≈ 可点区 46px，与放大查看器工具条同一档。 */
  min-height: 34px;
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  max-width: 40vw;
  padding: 3px 10px;
  border-radius: 999px;
  font-size: 0.7rem;
  color: var(--muted-foreground, #9aa);
  background: rgba(255, 255, 255, 0.05);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.sheet-tab--on { color: #fff; background: rgba(140, 100, 255, 0.35); }

/* 表体：横向滚动**关在容器里**（宽表是常态，页面本身绝不能横滚）。 */
.sheet-body {
  flex: 1 1 auto;
  min-height: 0;
  overflow: auto;
  padding: 10px;
  /* 抽屉面板整体 user-select:none —— 表格正文必须显式开，否则"能看见但拿不走"，
     而"拿得走"正是这一轮的核心要求（2026-09-08）。 */
  user-select: text;
  -webkit-user-select: text;
}

/* 表格观感：与 md 阅读器里的 table 同一套（边框/斑马纹/内边距），不另造一种。 */
.sheet-body :deep(table) {
  border-collapse: collapse;
  /* 自然列宽 + 容器横滚。不给 max-content，表会被压进面板宽度，中文列会塌成"一字一行"
     （2026-09-08 截图实测：340px 面板下「场景」两个字竖排）——那是能看见但读不了。 */
  width: max-content;
  max-width: none;
  font-size: 0.72rem;
  background: #fbfaf8;
  color: #1a1a1a;
  border-radius: 6px;
  overflow: hidden;
}
.sheet-body :deep(td),
.sheet-body :deep(th) {
  border: 1px solid #d8d4cc;
  padding: 3px 7px;
  vertical-align: top;
  white-space: pre-wrap;
  word-break: break-word;
  min-width: 4.5em;  /* 窄列也留出能读的宽度，别塌成竖排 */
  max-width: 40ch;   /* 长文本单元格再长也不把整表撑成一条线 */
}
.sheet-body :deep(th) { background: #efece6; font-weight: 600; }
.sheet-body :deep(tr:nth-child(even) td) { background: #f5f2ee; }
.sheet-body :deep(img) { max-width: 100%; height: auto; display: block; margin: 8px 0; }
.sheet-body :deep(h1) { font-size: 0.78rem; font-weight: 600; margin: 6px 0; color: var(--foreground, #ddd); }
.sheet-body :deep(hr) { display: none; } /* libreoffice 在每个表之间插分隔线，标签页里是噪音 */

.sheet-note {
  flex: 0 0 auto;
  padding: 5px 10px;
  font-size: 0.62rem;
  color: var(--muted-foreground, #9aa);
  border-top: 1px solid var(--border, #2a2a2a);
}
</style>
