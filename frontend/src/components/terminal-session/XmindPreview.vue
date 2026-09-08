<script setup lang="ts">
/**
 * XmindPreview — 思维导图。**不画布局，取它自己带的两样东西。**
 *
 * 2026-09-08 解包实测：一个 .xmind 就是个 zip，里面有
 *   `content.json`（42KB 的树：`[{rootTopic:{title, children:{attached:[…]}}}]`，递归）
 *   `Thumbnails/thumbnail.png`（461KB，**XMind 自己渲染好的导图图片**）
 * 所以两条横切要求各有着落，一行布局算法都不用写：
 *   「内容可复制」→ 大纲树（纯文本，原生可选中）
 *   「图片可放大」→ 缩略图交给共用的 ImageZoomViewer
 *
 * 默认给大纲：它可搜索、可复制、在手机上也读得下去；图是"看整体"时才切过去的。
 */
import { computed, ref } from 'vue'
import ImageZoomViewer from '@terminal/components/terminal-session/ImageZoomViewer.vue'

const props = defineProps<{
  name: string
  /** content.json 原文。 */
  contentJson: string
  /** Thumbnails/thumbnail.png 的取址；没有缩略图时不传。 */
  thumbUrl?: string
}>()

interface Node { title: string; depth: number }

const errorText = ref('')
const mode = ref<'outline' | 'image'>('outline')
const zoomSrc = ref('')

/** 递归摊平成缩进大纲。attached 是 XMind 的常规子节点键（detached 是浮动主题，不进大纲）。 */
function flatten(topic: unknown, depth: number, out: Node[]): void {
  if (!topic || typeof topic !== 'object') return
  const t = topic as { title?: string; children?: { attached?: unknown[] } }
  out.push({ title: (t.title || '').trim() || '（无标题）', depth })
  for (const child of t.children?.attached ?? []) flatten(child, depth + 1, out)
}

const nodes = computed<Node[]>(() => {
  errorText.value = ''
  try {
    const parsed: unknown = JSON.parse(props.contentJson)
    const sheets = Array.isArray(parsed) ? parsed : [parsed]
    const out: Node[] = []
    for (const sheet of sheets) {
      const s = sheet as { rootTopic?: unknown; title?: string }
      if (sheets.length > 1 && s.title) out.push({ title: `【${s.title}】`, depth: 0 })
      flatten(s.rootTopic, sheets.length > 1 && s.title ? 1 : 0, out)
    }
    if (!out.length) errorText.value = '这个导图里没有可读的节点'
    return out
  } catch (err) {
    // 老版 .xmind 用 content.xml 而不是 content.json —— 说清楚是哪种情况，别只说"解析失败"
    errorText.value = `无法解析 content.json（旧版 XMind 可能用的是 content.xml）：${err instanceof Error ? err.message : String(err)}`
    return []
  }
})

/** 复制整份大纲：导图的价值常常就是那个结构，能整份拿走比逐行选省事。 */
const outlineText = computed(() => nodes.value.map((n) => `${'  '.repeat(n.depth)}- ${n.title}`).join('\n'))
const copied = ref(false)
async function copyOutline(): Promise<void> {
  try {
    await navigator.clipboard.writeText(outlineText.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 1500)
  } catch { /* 剪贴板被拒时用户仍可手动选中复制 */ }
}
</script>

<template>
  <div class="flex h-full flex-col" data-testid="fp-preview-xmind">
    <div class="flex flex-none items-center gap-2 border-b border-border px-3 py-1.5">
      <button
        type="button" class="xm-tab" :class="{ 'xm-tab--on': mode === 'outline' }"
        data-testid="fp-xmind-tab-outline" @click="mode = 'outline'"
      >大纲</button>
      <button
        v-if="thumbUrl" type="button" class="xm-tab" :class="{ 'xm-tab--on': mode === 'image' }"
        data-testid="fp-xmind-tab-image" @click="mode = 'image'"
      >导图</button>
      <span class="flex-1" />
      <button
        v-if="mode === 'outline' && nodes.length" type="button" class="xm-tab"
        data-testid="fp-xmind-copy" @click="copyOutline"
      >{{ copied ? '已复制' : '复制大纲' }}</button>
    </div>

    <div v-if="errorText && mode === 'outline'" class="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center">
      <p class="text-xs text-muted-foreground">导图大纲解析失败</p>
      <p class="max-w-full text-[0.62rem] text-muted-foreground/70 break-all">{{ errorText }}</p>
      <p v-if="thumbUrl" class="text-[0.62rem] text-muted-foreground/70">可切到「导图」看渲染好的图</p>
    </div>

    <div v-else-if="mode === 'outline'" class="xm-outline min-h-0 flex-1 overflow-auto px-3 py-2">
      <div
        v-for="(n, i) in nodes" :key="i"
        class="xm-row"
        :style="{ paddingLeft: `${n.depth * 14}px` }"
        :data-testid="`fp-xmind-node-${i}`"
      >
        <span class="xm-bullet" :class="`xm-bullet--d${Math.min(n.depth, 3)}`" />{{ n.title }}
      </div>
    </div>

    <div v-else class="min-h-0 flex-1 overflow-auto p-3">
      <img
        :src="thumbUrl" alt="导图缩略图" class="mx-auto max-w-full cursor-zoom-in rounded"
        data-testid="fp-xmind-thumb" title="点击放大" @click="zoomSrc = thumbUrl || ''"
      />
    </div>

    <ImageZoomViewer v-if="zoomSrc" :src="zoomSrc" @close="zoomSrc = ''" />
  </div>
</template>

<style scoped>
.xm-tab {
  /* 触控目标：实测 393px 下原来只有 21px 高，手指点不准（移动端一等公民，见 spec §7）。
     34px + 容器 padding ≈ 可点区 46px，与放大查看器工具条同一档。 */
  min-height: 34px;
  display: inline-flex;
  align-items: center;
  padding: 2px 9px;
  border-radius: 999px;
  font-size: 0.68rem;
  color: var(--muted-foreground, #9aa);
  background: rgba(255, 255, 255, 0.05);
}
.xm-tab--on { color: #fff; background: rgba(140, 100, 255, 0.35); }
/* 抽屉面板整体 user-select:none —— 大纲必须能选中复制（这就是它存在的理由）。 */
.xm-outline { user-select: text; -webkit-user-select: text; }
.xm-row {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding-top: 2px;
  padding-bottom: 2px;
  font-size: 0.72rem;
  line-height: 1.5;
  color: var(--foreground, #ddd);
  word-break: break-word;
}
.xm-bullet {
  flex: 0 0 auto;
  width: 5px;
  height: 5px;
  border-radius: 50%;
  transform: translateY(-2px);
}
.xm-bullet--d0 { background: #c080ff; width: 7px; height: 7px; }
.xm-bullet--d1 { background: #7aa2ff; }
.xm-bullet--d2 { background: #5ec8a0; }
.xm-bullet--d3 { background: #8b949e; }
</style>
