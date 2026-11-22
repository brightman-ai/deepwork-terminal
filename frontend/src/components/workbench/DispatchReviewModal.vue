<template>
  <div
    v-if="open"
    class="dr-backdrop"
    data-testid="dispatch-modal"
    @click.self="emit('close')"
    @keydown.escape.prevent="emit('close')"
  >
    <section
      class="dr-panel"
      role="dialog"
      aria-modal="true"
      aria-label="并发派审"
    >
      <div class="dr-head">
        <div class="dr-head-text">
          <h2>⚡ 多模型并发会审</h2>
          <span class="dr-head-sub">共享当前只读目录 · 不新建分支目录</span>
        </div>
        <button
          type="button"
          class="dr-close-btn"
          data-testid="dr-close"
          title="关闭"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>

      <form
        class="dr-body"
        @submit.prevent="handleSubmit"
        @keydown.meta.enter.prevent="handleSubmit"
        @keydown.ctrl.enter.prevent="handleSubmit"
      >
        <div class="dr-mode-grid" role="radiogroup" aria-label="会审模式">
          <button
            v-for="m in MODES"
            :key="m.key"
            type="button"
            class="dr-choice-card"
            role="radio"
            :aria-checked="mode === m.key"
            :data-testid="`dr-mode-${m.key}`"
            @click="mode = m.key"
          >
            <strong :style="{ color: m.color }">{{ m.label }}</strong>
            <span class="dr-choice-desc">{{ m.desc }}</span>
          </button>
        </div>

        <div v-if="profiles.length > 0" class="dr-prof-section">
          <div class="dr-prof-label">选择并发评审账号 / 引擎配置</div>
          <div class="dr-prof-grid">
            <label
              v-for="prof in profiles"
              :key="prof.id"
              class="dr-prof-item"
              :class="{ checked: selectedProfileIds.includes(prof.id) }"
            >
              <span class="dr-prof-left">
                <input
                  type="checkbox"
                  :value="prof.id"
                  :checked="selectedProfileIds.includes(prof.id)"
                  :data-testid="`dr-profile-check-${prof.id}`"
                  @change="toggleProfile(prof.id)"
                />
                <strong>{{ prof.name }}</strong>
              </span>
              <span class="dr-prof-model">{{ prof.model || prof.engine }}</span>
            </label>
          </div>
        </div>
        <div v-else class="dr-empty" role="note">
          暂无可用评审配置 — 请先在底部遥测栏打开 Profile 配置中心创建账号。
        </div>

        <label class="dr-field">
          <span>会审上下文（当前模式：{{ MODES.find((m) => m.key === mode)?.label }}）</span>
          <textarea
            v-model="contextDrafts[mode]"
            class="dr-textarea"
            data-testid="dr-context-text"
            :rows="4"
            :placeholder="MODE_TEMPLATES[mode]"
          />
        </label>

        <div v-if="errorMessage" class="dr-error" role="alert">
          ⚠️ {{ errorMessage }}
        </div>

        <div class="dr-foot">
          <span class="dr-hint" data-testid="dr-hint">
            结论以回执条呈现，可一键汇总填入当前会话输入框
          </span>
          <div class="dr-foot-btns">
            <button
              type="button"
              class="dr-btn-ghost"
              data-testid="dr-cancel"
              @click="emit('close')"
            >
              取消
            </button>
            <button
              type="submit"
              class="dr-btn-white"
              data-testid="dr-submit"
              :disabled="busy || !canSubmit"
            >
              {{ busy ? '派发中…' : '并发拉起评审 ↵' }}
            </button>
          </div>
        </div>
      </form>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ProfileItem } from '@terminal/api/profiles'

export type DispatchReviewMode = 'blind' | 'critique' | 'diff'

export interface DispatchReviewPayload {
  mode: DispatchReviewMode
  profileIds: string[]
  context: string
}

const props = withDefaults(
  defineProps<{
    open: boolean
    profiles: ProfileItem[]
    defaultProfileId?: string
    busy?: boolean
    errorMessage?: string | null
  }>(),
  {
    defaultProfileId: '',
    busy: false,
    errorMessage: null,
  },
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'dispatch', payload: DispatchReviewPayload): void
}>()

const MODES: Array<{ key: DispatchReviewMode; label: string; color: string; desc: string }> = [
  {
    key: 'blind',
    label: '🙈 盲审求方案',
    color: '#e2d0ff',
    desc: '只给问题与约束，隐藏当前方案防锚定',
  },
  {
    key: 'critique',
    label: '🔍 带方案挑刺',
    color: '#9ec5fe',
    desc: '附上拟定方案，专找反例与边界漏洞',
  },
  {
    key: 'diff',
    label: '🧬 审查当前改动 (Diff)',
    color: '#8ce8b5',
    desc: '直接基于当前工作区代码改动做独立审查',
  },
]

const MODE_TEMPLATES: Record<DispatchReviewMode, string> = {
  blind: '[盲审求方案 · 隐藏当前方案防锚定]\n问题与约束：\n请独立给出方案，并附 2 个边界反例（只读勿改文件）。',
  critique: '[带方案挑刺 · 专找反例与边界漏洞]\n拟定方案与改动：\n请只列能推翻该实现的触发条件、行号依据与可观察后果；无实锤则写未发现。',
  diff: '[审查当前改动 · Diff]\n请直接基于当前工作区代码改动做独立审查，重点核查边界条件与回归风险。',
}

const mode = ref<DispatchReviewMode>('blind')
const contextDrafts = ref<Record<DispatchReviewMode, string>>({
  blind: '',
  critique: '',
  diff: '',
})
const selectedProfileIds = ref<string[]>([])

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return
    mode.value = 'blind'
    for (const key of Object.keys(contextDrafts.value) as DispatchReviewMode[]) {
      if (!contextDrafts.value[key].trim()) {
        contextDrafts.value[key] = MODE_TEMPLATES[key]
      }
    }
    const fallbackId =
      props.defaultProfileId ||
      props.profiles.find((p) => p.isDefault)?.id ||
      props.profiles[0]?.id
    selectedProfileIds.value = fallbackId ? [fallbackId] : []
  },
)

function toggleProfile(id: string): void {
  if (selectedProfileIds.value.includes(id)) {
    selectedProfileIds.value = selectedProfileIds.value.filter((x) => x !== id)
  } else {
    selectedProfileIds.value = [...selectedProfileIds.value, id]
  }
}

const canSubmit = computed(
  () => selectedProfileIds.value.length > 0 && Boolean(contextDrafts.value[mode.value].trim()),
)

function handleSubmit(): void {
  if (!canSubmit.value || props.busy) return
  emit('dispatch', {
    mode: mode.value,
    profileIds: selectedProfileIds.value.slice(),
    context: contextDrafts.value[mode.value].trim(),
  })
}
</script>

<style scoped>
.dr-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(5, 7, 12, 0.74);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 42px 12px 16px;
  z-index: 2200;
}
.dr-panel {
  width: min(640px, 100%);
  max-height: calc(100svh - 56px);
  background: var(--bg-surface, #161a22);
  color: var(--text, #eef1f6);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  border-radius: 12px;
  box-shadow: 0 24px 72px rgba(0, 0, 0, 0.75);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.dr-head {
  padding: 11px 15px;
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  gap: 8px;
  background: #131720;
}
.dr-head-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.dr-head h2 {
  margin: 0;
  font-size: 13.5px;
  font-weight: 600;
}
.dr-head-sub {
  font-size: 10.5px;
  color: var(--text-muted, #6c7688);
}
.dr-close-btn {
  margin-left: auto;
  width: 26px;
  height: 26px;
  border-radius: 5px;
  background: transparent;
  border: 0;
  color: var(--text-secondary, #a2acc0);
  cursor: pointer;
}
.dr-close-btn:hover {
  background: var(--bg-hover, #222937);
  color: #fff;
}
.dr-body {
  padding: 13px 15px 15px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.dr-mode-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
}
.dr-choice-card {
  text-align: left;
  padding: 9px 10px;
  border-radius: 8px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-canvas, #0c0e12);
  display: flex;
  flex-direction: column;
  gap: 3px;
  cursor: pointer;
}
.dr-choice-card:hover {
  border-color: rgba(180, 142, 255, 0.45);
}
.dr-choice-card[aria-checked='true'] {
  border-color: var(--purple, #b48eff);
  background: rgba(180, 142, 255, 0.12);
}
.dr-choice-card strong {
  font-size: 12px;
}
.dr-choice-desc {
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
  line-height: 1.45;
}
.dr-prof-section {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.dr-prof-label {
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
}
.dr-prof-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 6px;
  max-height: 172px;
  overflow-y: auto;
  padding-right: 4px;
}
.dr-prof-item {
  padding: 7px 9px;
  border-radius: 7px;
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-canvas, #0c0e12);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  font-size: 12px;
  cursor: pointer;
}
.dr-prof-item.checked {
  border-color: #6ea8fe;
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
}
.dr-prof-left {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.dr-prof-model {
  font-size: 10.5px;
  color: var(--text-muted, #6c7688);
  font-family: var(--font-mono, ui-monospace, monospace);
}
.dr-empty {
  padding: 8px 10px;
  border-radius: 6px;
  border: 1px dashed var(--line-strong, rgba(255, 255, 255, 0.145));
  font-size: 11.5px;
  color: var(--text-muted, #6c7688);
}
.dr-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
}
.dr-textarea {
  width: 100%;
  min-height: 66px;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--bg-canvas, #0c0e12);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  font-size: 12px;
  line-height: 1.5;
  color: var(--text, #eef1f6);
  resize: vertical;
  font-family: inherit;
}
.dr-error {
  padding: 6px 10px;
  border-radius: 6px;
  background: rgba(255, 107, 107, 0.14);
  border: 1px solid rgba(255, 107, 107, 0.45);
  color: #ffd0d0;
  font-size: 11.5px;
}
.dr-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.dr-hint {
  font-size: 11px;
  color: var(--text-muted, #6c7688);
}
.dr-foot-btns {
  display: flex;
  gap: 8px;
}
.dr-btn-ghost {
  height: 30px;
  padding: 0 11px;
  border-radius: 6px;
  color: var(--text-secondary, #a2acc0);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-surface, #161a22);
  font-size: 11.5px;
  cursor: pointer;
}
.dr-btn-ghost:hover {
  background: var(--bg-hover, #222937);
  color: #fff;
}
.dr-btn-white {
  height: 30px;
  padding: 0 13px;
  border-radius: 6px;
  background: var(--cta-white, #f2f4f8);
  color: var(--cta-white-fg, #0d1017);
  border: 0;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}
.dr-btn-white:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
@media (max-width: 640px) {
  .dr-mode-grid,
  .dr-prof-grid {
    grid-template-columns: 1fr;
  }
}
</style>
