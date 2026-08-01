<script setup lang="ts">
/**
 * VersionBadge — 顶栏那枚版本号，以及点开它之后的「关于」面板。
 *
 * ## 它为什么从一个 <span> 变成了一个按钮
 *
 * 上一版它是只读的 `<span :title="...">`。Human 实测原话："我把鼠标放到这个版本号上，它既不会
 * 弹出一些信息，也无法点击。我觉得用户看到应该是比较懵逼的。" 拆开看是三条，条条成立：
 *
 *   · **零意符**：`cursor: default`，没有 hover 反馈。原生 title 其实在（实测确认），但它要等
 *     浏览器那 1~2 秒延迟，而且**没有任何东西邀请你把鼠标放上去**。看不见的可供性不算可供性。
 *   · **手机上完全够不着**：触摸屏没有 hover。这是个移动优先的产品，而完整版本在手机上根本无法获得。
 *   · **点击直觉没被兑现**：`<span>` 不可点、不可聚焦、键盘也到不了。用户想点它是对的，是它没回应。
 *
 * 现在它是 `<button>`：可点、可聚焦、有 hover 反馈、手机可用。
 *
 * ## 面板里为什么是两块而不是一句「有更新」
 *
 * 见 versionPanel.ts 的文件头：「页面旧了」和「程序旧了」的紧迫度、动作、代价全不同，合并成一句
 * 话会让用户不知道自己该干嘛。两块各自一句话、各自一个动作。
 *
 * ## 组件而非两壳各写一份
 *
 * standalone 的 CliTabBar 和 pro 的 MainLayout 各自拥有自己的顶栏布局（这是刻意的），但**这枚
 * 徽标的行为**必须一样。所以这里连按钮带面板整个封装成一个组件，两壳只负责把它放进各自的 chrome
 * 行 —— 行为无处可漂移。
 */
import { computed, ref, onMounted, onUnmounted, nextTick } from 'vue'
import { Copy, Check, ExternalLink, RefreshCw } from 'lucide-vue-next'
import { useBuildVersionLabel } from '@terminal/composables/cli/useBuildVersionLabel'
import { useAppUpdate } from '@terminal/composables/cli/useAppUpdate'
import { pageLine, programLine, badgeNeedsAttention } from '@terminal/composables/cli/versionPanel'
import { useRenderHealth, rendererLine, metricsLine } from '@terminal/composables/cli/renderHealth'
import { copyTextToClipboard } from '@ce/utils/clipboard'

/** 面板标题里的产品名 —— 两壳各自传，其余一切共享。 */
const props = withDefaults(defineProps<{ appName?: string }>(), { appName: 'deepwork' })

const { short, full, releaseState, latest, latestUrl } = useBuildVersionLabel()
const { updateAvailable, applyUpdate } = useAppUpdate()

const open = ref(false)
const copied = ref(false)

// 面板 Teleport 到 body + 固定定位，而不是留在按钮旁边做 absolute 定位。
//
// 【为什么必须这样】standalone 的 `.cli-tab-bar` 是 `overflow-x:auto; overflow-y:hidden`（它要
// 横向滚标签），于是**任何超出这 36px 高的绝对定位子元素都会被裁掉** —— 夹具实测：面板只露出
// 紧贴徽标下方的一条边，正文全被裁没了。同一个坑还有第二层：顶栏自成层叠上下文，面板的 z-index
// 再高也压不过外面的浮层（首次进站时快捷键横幅正好盖在同一块像素上）。
//
// Teleport 到 body 一次解决两个：不再被裁，也不再受制于顶栏的层叠上下文。代价是位置要自己算，
// 所以有下面这段 anchor 逻辑。
const anchorEl = ref<HTMLElement>()
const panelPos = ref<{ top: number; right: number }>({ top: 0, right: 0 })

function placePanel(): void {
  const el = anchorEl.value
  if (!el) return
  const r = el.getBoundingClientRect()
  // 右对齐到徽标右缘（用 right 而不是 left：面板比徽标宽，靠右才不会溢出屏幕外）。
  panelPos.value = { top: Math.round(r.bottom + 6), right: Math.round(window.innerWidth - r.right) }
}

function toggle(): void {
  open.value = !open.value
  if (open.value) void nextTick(placePanel)
}

// 渲染：这个页面此刻怎么把字画到屏幕上。收录理由见 renderHealth.ts —— 界面上看不见、会改变你对
// "卡不卡"的解读、且一旦静默降级没有别的东西会告诉你。数据全部来自**已经在跑**的上报路径。
const { renderer, declineReason, contextLost, metrics } = useRenderHealth()
const render = computed(() => rendererLine(renderer.value, contextLost.value, declineReason.value))
const renderMetrics = computed(() => metricsLine(metrics.value))

const page = computed(() => pageLine(updateAvailable.value))
const program = computed(() => programLine(releaseState.value, latest.value, latestUrl.value))
// 小圆点：页面旧了 / 程序旧了 / GPU 掉了 —— 三者都是"需要你做点什么"。渲染降级同样一次刷新就能
// 修，把它排除在提醒之外，就等于让人继续用一个慢一半的终端而不自知。
const needsAttention = computed(
  () => badgeNeedsAttention(updateAvailable.value, releaseState.value) || contextLost.value,
)

async function copyVersion(): Promise<void> {
  if (await copyTextToClipboard(full.value)) {
    copied.value = true
    setTimeout(() => { copied.value = false }, 1600)
  }
}

function runAction(line: { action?: { kind: string; href?: string } }): void {
  const a = line.action
  if (!a) return
  if (a.kind === 'refresh') { applyUpdate(); return }
  // 普通重载即可拿回 GPU 上下文 —— 不必走 applyUpdate 那套清缓存的重活，那是给"页面旧了"用的。
  if (a.kind === 'reload') { window.location.reload(); return }
  if (a.kind === 'open' && a.href) window.open(a.href, '_blank', 'noopener,noreferrer')
}

// 点面板外面 / Esc 关闭 —— 一个小浮层该有的两条退路，缺一条都会变成"关不掉"。
const rootEl = ref<HTMLElement>()
const panelEl = ref<HTMLElement>()
function onDocPointerDown(e: PointerEvent): void {
  if (!open.value) return
  const t = e.target as Node
  // 面板已 Teleport 到 body，不再是 rootEl 的后代 —— 两处都要判，否则点面板自己也会把它关掉。
  if (rootEl.value?.contains(t) || panelEl.value?.contains(t)) return
  open.value = false
}
function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape' && open.value) open.value = false
}
onMounted(() => {
  document.addEventListener('pointerdown', onDocPointerDown, true)
  window.addEventListener('keydown', onKey)
  window.addEventListener('resize', placePanel)
})
onUnmounted(() => {
  document.removeEventListener('pointerdown', onDocPointerDown, true)
  window.removeEventListener('keydown', onKey)
  window.removeEventListener('resize', placePanel)
})
</script>

<template>
  <div v-if="short" ref="rootEl" class="vb-root">
    <span ref="anchorEl" class="vb-anchor">
    <button
      class="vb-badge"
      :class="{ 'is-open': open }"
      type="button"
      :title="`${props.appName} ${full} — 点击查看版本与更新`"
      :aria-expanded="open"
      aria-haspopup="dialog"
      data-testid="version-badge"
      @click="toggle"
    >
      <span class="vb-text">{{ short }}</span>
      <!-- 小圆点只在**需要用户做点什么**时出现（见 badgeNeedsAttention）。常亮的点会被学会忽略。 -->
      <span v-if="needsAttention" class="vb-dot" data-testid="version-badge-dot" />
    </button>
    </span>

    <Teleport to="body">
    <div
      v-if="open"
      ref="panelEl"
      class="vb-panel"
      :style="{ top: panelPos.top + 'px', right: panelPos.right + 'px' }"
      role="dialog"
      aria-label="版本与更新"
      data-testid="version-panel"
    >
      <div class="vb-row vb-head">
        <span class="vb-label">当前版本</span>
        <code class="vb-ver" data-testid="version-panel-full">{{ full }}</code>
        <button class="vb-copy" type="button" :title="copied ? '已复制' : '复制版本号'" data-testid="version-panel-copy" @click="copyVersion">
          <component :is="copied ? Check : Copy" :size="13" />
        </button>
      </div>

      <div class="vb-block">
        <div class="vb-block-title">浏览器页面</div>
        <div class="vb-row">
          <span class="vb-line" :class="`t-${page.tone}`" data-testid="version-panel-page">{{ page.text }}</span>
          <button v-if="page.action" class="vb-act" type="button" data-testid="version-panel-page-action" @click="runAction(page)">
            <RefreshCw :size="12" /><span>{{ page.action.label }}</span>
          </button>
        </div>
      </div>

      <div class="vb-block">
        <div class="vb-block-title">渲染</div>
        <div class="vb-row">
          <span class="vb-line" :class="`t-${render.tone}`" data-testid="version-panel-render">{{ render.text }}</span>
          <button v-if="render.action" class="vb-act" type="button" data-testid="version-panel-render-action" @click="runAction(render)">
            <RefreshCw :size="12" /><span>{{ render.action.label }}</span>
          </button>
        </div>
        <div v-if="render.detail" class="vb-sub" data-testid="version-panel-render-detail">{{ render.detail }}</div>
        <div v-if="renderMetrics" class="vb-sub" data-testid="version-panel-render-metrics">{{ renderMetrics }}</div>
      </div>

      <div class="vb-block">
        <div class="vb-block-title">服务端程序</div>
        <div class="vb-row">
          <span class="vb-line" :class="`t-${program.tone}`" data-testid="version-panel-program">{{ program.text }}</span>
          <button v-if="program.action" class="vb-act" type="button" data-testid="version-panel-program-action" @click="runAction(program)">
            <span>{{ program.action.label }}</span><ExternalLink :size="12" />
          </button>
        </div>
      </div>
    </div>
    </Teleport>
  </div>
</template>

<style scoped>
.vb-root { position: relative; display: inline-flex; align-items: center; flex-shrink: 0; }

/* 徽标：静止时依旧克制（和过去那个 span 的观感一致），但 hover/focus 有明确反馈 —— 这就是
   过去缺的那个意符。 */
.vb-badge {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 2px 5px; border: none; border-radius: 5px;
  background: transparent; cursor: pointer;
  font-size: 0.62rem; letter-spacing: 0.3px; line-height: 1;
  color: hsl(var(--muted-foreground)); opacity: 0.55; white-space: nowrap;
}
.vb-badge:hover, .vb-badge:focus-visible, .vb-badge.is-open {
  opacity: 1; background: hsl(var(--accent)); color: hsl(var(--foreground));
}
.vb-dot {
  width: 5px; height: 5px; border-radius: 50%; background: #f59e0b; flex-shrink: 0;
}

.vb-anchor { display: inline-flex; align-items: center; }
.vb-panel {
  position: fixed; z-index: 2700;
  width: 268px; padding: 10px 12px; border-radius: 10px;
  background: hsl(var(--card)); border: 1px solid hsl(var(--border));
  box-shadow: 0 10px 34px rgba(0,0,0,0.45);
  font-size: 12px; color: hsl(var(--foreground)); text-align: left;
  cursor: default;
}
.vb-row { display: flex; align-items: center; gap: 6px; }
.vb-head { padding-bottom: 8px; border-bottom: 1px solid hsl(var(--border)); }
.vb-label { color: hsl(var(--muted-foreground)); flex-shrink: 0; }
.vb-ver { flex: 1; min-width: 0; font-family: monospace; font-size: 11.5px; overflow: hidden; text-overflow: ellipsis; user-select: text; }
.vb-copy, .vb-act {
  display: inline-flex; align-items: center; gap: 4px; flex-shrink: 0;
  padding: 3px 7px; border-radius: 5px; font-size: 11.5px; cursor: pointer;
  border: 1px solid hsl(var(--border)); background: hsl(var(--background)); color: hsl(var(--foreground));
}
.vb-copy { padding: 3px 5px; }
.vb-copy:hover, .vb-act:hover { background: hsl(var(--accent)); }

.vb-block { padding-top: 9px; }
.vb-block-title { color: hsl(var(--muted-foreground)); font-size: 11px; margin-bottom: 4px; }
.vb-line { flex: 1; min-width: 0; }
/* 补充说明 / 指标：比正文更轻，属于"想深究时才读"的层级，不与主结论争注意力。 */
.vb-sub { margin-top: 2px; font-size: 11px; line-height: 1.45; color: hsl(var(--muted-foreground)); }
/* 三档语气：确认 / 要你做点什么 / 无从判断。unknown 刻意不是绿色 —— 它不是一句好消息。 */
.t-ok { color: #4ade80; }
.t-warn { color: #f59e0b; }
.t-muted { color: hsl(var(--muted-foreground)); }
</style>
