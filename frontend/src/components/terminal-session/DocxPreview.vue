<script setup lang="ts">
/**
 * DocxPreview — 讲解 docx 的客户端 reflow 渲染（docx-preview 库）。
 *
 * 形态决策（brainstorm 2026-09-06 拍定）：全端单一 reflow —— ignoreWidth/ignoreHeight 丢弃
 * Word 固定页宽，内容按预览容器宽重排（手机 ~390px 直接可读）；breakPages=false 连续流，
 * 不留分页空白。要看版式保真（分页/页宽）走「下载」——那是既有出路。
 * 失败路径：损坏 zip / 非 docx 内容 → renderAsync 抛错 → 内联原因；预览头部的「下载」按钮
 * 始终是逃生口。库（~160KB）仅在真打开 docx 时动态 import。
 */
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import ImageZoomViewer from '@terminal/components/terminal-session/ImageZoomViewer.vue'
import { wireImageZoom } from '@terminal/components/terminal-session/imageZoom'
import { armRenderWatchdog, type RenderWatchdog } from '@terminal/components/terminal-session/previewWatchdog'

// 渲染 watchdog（REQ-fp-a2-0）：renderAsync 抛错有下面的 catch，但永不结算（promise 永远
// pending）会让人对着黑面板猜。超时把失败讲出来；settle 时机 = 内容真落进 body。
let watchdog: RenderWatchdog | null = null

const props = defineProps<{
  name: string
  data: ArrayBuffer
}>()

const bodyEl = ref<HTMLElement>()
const styleEl = ref<HTMLElement>()
const rendering = ref(true)
const errorText = ref('')

async function render(): Promise<void> {
  const body = bodyEl.value
  if (!body) return
  rendering.value = true
  errorText.value = ''
  body.innerHTML = ''
  watchdog?.dispose()
  watchdog = armRenderWatchdog('docx', (msg) => {
    errorText.value = msg
    rendering.value = false
  })
  try {
    const { renderAsync } = await import('docx-preview')
    await renderAsync(props.data, body, styleEl.value, {
      ignoreWidth: true, // reflow：丢 Word 固定页宽，按容器宽重排
      ignoreHeight: true,
      ignoreFonts: false,
      breakPages: false, // 连续流，无分页空白
      experimental: false,
    })
    if (!body.childElementCount) {
      // 渲染"成功"却一个节点都没落（空/怪结构）——按失败讲，不给空白页。
      watchdog?.dispose()
      errorText.value = '文档没有可渲染的内容'
    } else {
      watchdog?.settle()
      wireImages(body)
    }
  } catch (err) {
    watchdog?.dispose()
    errorText.value = err instanceof Error ? err.message : String(err)
  } finally {
    rendering.value = false
  }
}

// 图片缩放：共用 ImageZoomViewer（横切要求，见该组件头部）。这里只管"点了哪张"。
const zoomSrc = ref('')
function wireImages(root: HTMLElement): void { wireImageZoom(root, (src) => { zoomSrc.value = src }) }

onMounted(() => void render())
watch(() => props.data, () => void render())
onBeforeUnmount(() => { watchdog?.dispose(); watchdog = null })
</script>

<template>
  <div class="relative h-full overflow-auto" data-testid="fp-preview-docx">
    <!-- docx-preview 把 <style> 注进这里；容器 display:none，样式仍全局生效 -->
    <div ref="styleEl" aria-hidden="true" style="display: none"></div>
    <div v-if="rendering && !errorText" class="absolute inset-0 flex items-center justify-center text-xs text-muted-foreground animate-pulse">docx 渲染中…</div>
    <div v-else-if="errorText" class="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
      <p class="text-xs text-muted-foreground">docx 渲染失败：文件可能已损坏或不是真正的 docx 内容</p>
      <p class="max-w-full text-[0.62rem] text-muted-foreground/70 break-all">{{ errorText }}</p>
      <p class="text-[0.62rem] text-muted-foreground/70">可用上方「下载」在本地应用中打开</p>
    </div>
    <!-- 纸面卡：docx run 颜色多为 auto（黑），深色面板上必须给恒定浅色阅读面 -->
    <div v-show="!errorText" ref="bodyEl" class="docx-body m-3 rounded-lg border border-border bg-[#fbfaf8] text-[#1a1a1a] shadow-sm"></div>

    <ImageZoomViewer v-if="zoomSrc" :src="zoomSrc" @close="zoomSrc = ''" />
  </div>
</template>

<style scoped>
/* docx-preview 默认把 wrapper 刷成灰底+30px 内边距、section 固定页宽 —— reflow 形态全部覆盖掉。 */
.docx-body :deep(.docx-wrapper) {
  background: transparent;
  padding: 0;
}
/* 抽屉面板整体 user-select:none（rd-panel）——正文必须显式开文本选择（2026-09-07「docx 不能
   复制」），与 md 阅读器（FilePreview .fp）同法。图片保留 zoom 光标不受影响。 */
.docx-body {
  user-select: text;
  -webkit-user-select: text;
}
.docx-body :deep(.docx-wrapper > section.docx) {
  width: auto;
  min-height: auto;
  padding: 14px 16px 20px;
  background: transparent;
  box-shadow: none;
}
/* 手机 ~390px：正文默认 11-12pt 偏小但可读；图片必须收缩进容器宽，否则 reflow 被一张图撑破。
   docx-preview 把图包在 width:<n>pt 的 inline-block div 里（实测 420pt）——只约束 img 没用
   （max-width:100% 按父盒算），必须连包裹盒一起封顶；固定 pt 高的包裹盒在变窄后留白，
   用 :has(> img) 把图盒的定高也放开。 */
.docx-body :deep(*) {
  max-width: 100%;
}
.docx-body :deep(img) {
  height: auto;
}
.docx-body :deep(div:has(> img)) {
  height: auto;
}
.docx-body :deep(table) {
  max-width: 100%;
}
/* 图片可点开放大（wireImages 加 title 提示） */
.docx-body :deep(img) {
  cursor: zoom-in;
}

/* ── 全屏缩放查看器 ── */
</style>
