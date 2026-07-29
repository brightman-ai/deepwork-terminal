<script setup lang="ts">
/**
 * DetachedTerminalCard — 一个标签背后没有活着的进程时，占据终端区域的那张卡。
 *
 * 它替代的是本次改动删掉的谎：以前服务重启后，每个孤儿标签会被悄悄 POST /sessions 换成一个全新的
 * 空 PTY，屏幕上与「恢复成功」完全一致。现在那一步没有了，这张卡就是取而代之的**如实告知**：
 * 说清进程已经结束、原来在哪个目录、以及点哪个按钮会发生什么。
 *
 * 意符（《设计心理学》）：一枚静止的中性灰点 + 灰色描边卡片 = 在场但不活着；主按钮是唯一实心的
 * 东西，且它的文案自己说清后果（「在此目录新建终端」而不是「恢复」——我们恢复不了那个进程，
 * 不能用一个承诺不了的动词）。按钮下方那行小字把后果再讲一遍（会新建、不会接回旧进程）。
 *
 * 文案/颜色全部来自 tabLiveness.ts（SSOT），本组件不自己造词、不自己敲 hex。
 */
import { computed, ref } from 'vue'
import {
  LIVENESS_COLOR,
  LIVENESS_LABEL,
  livenessCopy,
  type TabNotLive,
} from '@terminal/composables/cli/tabLiveness'

const props = defineProps<{
  /** 为什么没有活着的进程：确认结束 / 问不到。两种处境的文案与主按钮都不同。 */
  liveness: TabNotLive
  /** 标签显示名（调用方已算好「终端N」这类位置名，本组件不重算）。 */
  name: string
  /** 这个标签原来的工作目录。空 = 不知道（例如 pro 没有持久化每个终端的目录）。 */
  cwd?: string
  /** 远程标签：进程原本长在别的机器上。 */
  remote?: boolean
  /** 远程标签的机器名（本机标签留空）。 */
  machineLabel?: string
  /** 主按钮真正要做的事，由宿主注入（detached→在原目录新建；unreachable→重新问一次）。
   *  返回 {ok,error}，和 RemoteTermDialog 的 on-connect 是同一个约定：失败要能说出原因，
   *  而不是让按钮默默无事发生。 */
  action: () => Promise<{ ok: boolean; error?: string }>
}>()

const emit = defineEmits<{
  /** 次按钮：关掉这个标签。 */
  (e: 'close'): void
}>()

const copy = computed(() =>
  livenessCopy(props.liveness, { remote: props.remote, machineLabel: props.machineLabel, cwd: props.cwd }),
)
const dotColor = computed(() => LIVENESS_COLOR[props.liveness])
const stateLabel = computed(() => LIVENESS_LABEL[props.liveness])

/** 主按钮点下去要发网络请求，期间禁用并改文案——否则用户会连点出好几个终端。 */
const busy = ref(false)
const failure = ref('')

async function onPrimary(): Promise<void> {
  if (busy.value) return
  busy.value = true
  failure.value = ''
  try {
    const r = await props.action()
    // 成功时宿主会把这个标签重新标成 live，本卡片随之被卸载；失败就把真实原因说出来。
    if (!r.ok) failure.value = r.error || '没能完成，请再试一次'
  } catch (e) {
    failure.value = e instanceof Error ? e.message : '没能完成，请再试一次'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="dtc" data-testid="detached-terminal-card">
    <div class="dtc-card">
      <div class="dtc-head">
        <span class="dtc-dot" />
        <span class="dtc-name">{{ name }}</span>
        <span class="dtc-state" :data-testid="`detached-state-${liveness}`">{{ stateLabel }}</span>
      </div>

      <h2 class="dtc-headline">{{ copy.headline }}</h2>
      <p class="dtc-body">{{ copy.body }}</p>

      <dl v-if="remote || cwd" class="dtc-facts">
        <div v-if="remote" class="dtc-fact">
          <dt>机器</dt>
          <dd>{{ machineLabel || '远端机器' }}</dd>
        </div>
        <div v-if="cwd" class="dtc-fact">
          <dt>原目录</dt>
          <dd class="dtc-path" data-testid="detached-cwd">{{ cwd }}</dd>
        </div>
      </dl>

      <div class="dtc-actions">
        <button
          type="button"
          class="dtc-btn dtc-btn--primary"
          :disabled="busy"
          data-testid="detached-primary"
          @click="onPrimary"
        >{{ busy ? '正在处理…' : copy.primary }}</button>
        <button
          type="button"
          class="dtc-btn"
          data-testid="detached-close"
          @click="emit('close')"
        >{{ copy.secondary }}</button>
      </div>
      <p class="dtc-hint">{{ copy.primaryHint }}</p>
      <p v-if="failure" class="dtc-failure" data-testid="detached-failure">{{ failure }}</p>
    </div>
  </div>
</template>

<style scoped>
.dtc {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  overflow-y: auto;
  background: var(--workbench-bg, #1e1e1e);
}

/* 中性灰描边 + 无任何强调色：这不是错误页，别用红/黄边把它推成故障。 */
.dtc-card {
  width: 100%;
  max-width: 520px;
  padding: 22px 24px 20px;
  border: 1px solid hsl(var(--border));
  border-radius: 12px;
  background: hsl(var(--card));
  color: hsl(var(--foreground));
}

.dtc-head {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 0.8125rem;
  color: hsl(var(--muted-foreground));
}
/* 颜色从 SSOT 绑过来；静止（LIVENESS_MOTION 两档都是 null——已结束的东西不表达存活）。 */
.dtc-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
  background: v-bind(dotColor);
}
.dtc-name {
  font-weight: 600;
  color: hsl(var(--foreground));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dtc-state {
  margin-left: auto;
  flex-shrink: 0;
  padding: 1px 7px;
  border-radius: 999px;
  border: 1px solid hsl(var(--border));
  font-size: 0.6875rem;
}

.dtc-headline {
  margin: 14px 0 6px;
  font-size: 1rem;
  font-weight: 600;
  line-height: 1.4;
}
.dtc-body {
  margin: 0;
  font-size: 0.8125rem;
  line-height: 1.65;
  color: hsl(var(--muted-foreground));
}

.dtc-facts {
  margin: 14px 0 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: hsl(var(--muted) / 0.35);
  font-size: 0.75rem;
}
.dtc-fact {
  display: flex;
  gap: 10px;
  align-items: baseline;
}
.dtc-fact + .dtc-fact { margin-top: 5px; }
.dtc-fact dt {
  flex-shrink: 0;
  min-width: 44px;
  color: hsl(var(--muted-foreground));
}
.dtc-fact dd {
  margin: 0;
  min-width: 0;
  word-break: break-all;
}
.dtc-path { font-family: var(--dw-mono, ui-monospace, SFMono-Regular, Menlo, monospace); }

.dtc-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;
}
.dtc-btn {
  padding: 7px 14px;
  border-radius: 7px;
  border: 1px solid hsl(var(--border));
  background: transparent;
  color: hsl(var(--foreground));
  font-size: 0.8125rem;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s, opacity 0.12s;
}
.dtc-btn:hover { background: hsl(var(--accent)); }
.dtc-btn:active { transform: scale(0.985); }
/* 唯一实心的东西 = 唯一被推荐的动作。 */
.dtc-btn--primary {
  border-color: #4a9eff;
  background: #4a9eff;
  color: #fff;
  font-weight: 600;
}
.dtc-btn--primary:hover { background: #3d8ce0; }
.dtc-btn--primary:disabled { opacity: 0.6; cursor: default; }

.dtc-hint {
  margin: 8px 0 0;
  font-size: 0.6875rem;
  line-height: 1.6;
  color: hsl(var(--muted-foreground));
}
.dtc-failure {
  margin: 8px 0 0;
  font-size: 0.75rem;
  color: #f87171;
}

@media (prefers-reduced-motion: reduce) {
  .dtc-btn { transition: none; }
  .dtc-btn:active { transform: none; }
}
</style>
