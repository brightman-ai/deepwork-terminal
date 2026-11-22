<template>
  <div
    v-if="open"
    class="nt-backdrop"
    data-testid="new-task-modal"
    @click.self="emit('close')"
    @keydown.escape.prevent="emit('close')"
  >
    <section
      class="nt-panel"
      role="dialog"
      aria-modal="true"
      aria-label="新建任务"
    >
      <div class="nt-head">
        <h2>＋ 新建任务</h2>
        <button
          type="button"
          class="nt-close-btn"
          data-testid="nt-close"
          title="关闭"
          @click="emit('close')"
        >
          ✕
        </button>
      </div>

      <form
        class="nt-body"
        @submit.prevent="handleSubmit"
        @keydown.meta.enter.prevent="handleSubmit"
        @keydown.ctrl.enter.prevent="handleSubmit"
      >
        <label class="nt-field">
          <span>任务目标 / 首条指令</span>
          <input
            v-model="prompt"
            type="text"
            class="nt-input"
            data-testid="nt-prompt-input"
            placeholder="输入任务目标、分支说明或 #Issue 编号…"
          />
        </label>

        <div v-if="mode === 'arena'" class="nt-grid-2">
          <label class="nt-field">
            <span>新分支名</span>
            <input
              v-model="branch"
              type="text"
              class="nt-input nt-mono"
              data-testid="nt-branch-input"
              placeholder="feat/task-slug"
              @input="branchEdited = true"
            />
          </label>
          <label class="nt-field">
            <span>基线分支 (baseRef)</span>
            <input
              v-model="baseRef"
              type="text"
              class="nt-input nt-mono"
              data-testid="nt-baseref-input"
              placeholder="main"
            />
          </label>
        </div>

        <div class="nt-mode-grid" role="radiogroup" aria-label="任务启动模式">
          <button
            type="button"
            class="nt-choice-card"
            role="radio"
            :aria-checked="mode === 'arena'"
            data-testid="nt-mode-arena"
            @click="mode = 'arena'"
          >
            <strong class="nt-c-gold">🌿 新建独立分支目录</strong>
            <span class="nt-choice-desc">
              基于基线分支拉出独立工作目录改代码，互不干扰，完成后可一键合入。
            </span>
          </button>
          <button
            type="button"
            class="nt-choice-card"
            role="radio"
            :aria-checked="mode === 'brainstorm'"
            data-testid="nt-mode-brainstorm"
            @click="mode = 'brainstorm'"
          >
            <strong class="nt-c-blue">💬 在当前目录直接开聊</strong>
            <span class="nt-choice-desc">
              不新建分支目录，直接在当前目录新开终端并发讨论或排查。
            </span>
          </button>
        </div>

        <div v-if="profiles.length > 0" class="nt-prof-section">
          <div class="nt-prof-label">选择并发账号 / 引擎配置</div>
          <div class="nt-prof-grid">
            <label
              v-for="prof in profiles"
              :key="prof.id"
              class="nt-prof-item"
              :class="{ checked: selectedProfileIds.includes(prof.id) }"
            >
              <span class="nt-prof-left">
                <input
                  type="checkbox"
                  :value="prof.id"
                  :checked="selectedProfileIds.includes(prof.id)"
                  :data-testid="`nt-profile-check-${prof.id}`"
                  @change="toggleProfile(prof.id)"
                />
                <strong>{{ prof.name }}</strong>
              </span>
              <span class="nt-prof-model">{{ prof.model || prof.engine }}</span>
            </label>
          </div>
        </div>

        <div v-if="errorMessage" class="nt-error" role="alert">
          ⚠️ {{ errorMessage }}
        </div>

        <div class="nt-foot">
          <span class="nt-budget-hint" data-testid="nt-budget-hint">
            {{ budgetHint }}
          </span>
          <div class="nt-foot-btns">
            <button
              type="button"
              class="nt-btn-ghost"
              data-testid="nt-cancel"
              @click="emit('close')"
            >
              取消
            </button>
            <button
              type="submit"
              class="nt-btn-white"
              data-testid="nt-submit"
              :disabled="busy || !canSubmit"
            >
              {{ busy ? '创建中…' : '创建并启动 ⌘↵' }}
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

export interface NewTaskSubmitPayload {
  mode: 'arena' | 'brainstorm'
  prompt: string
  branch: string
  baseRef: string
  profileIds: string[]
}

const props = withDefaults(
  defineProps<{
    open: boolean
    profiles: ProfileItem[]
    defaultProfileId?: string
    defaultBaseRef?: string
    busy?: boolean
    errorMessage?: string | null
  }>(),
  {
    defaultProfileId: '',
    defaultBaseRef: 'main',
    busy: false,
    errorMessage: null,
  },
)

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'submit', payload: NewTaskSubmitPayload): void
}>()

const prompt = ref<string>('')
const branch = ref<string>('')
const branchEdited = ref<boolean>(false)
const baseRef = ref<string>(props.defaultBaseRef)
const mode = ref<'arena' | 'brainstorm'>('arena')
const selectedProfileIds = ref<string[]>([])

function slugifyBranch(input: string): string {
  const ascii = input
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32)
  if (ascii) return `feat/${ascii}`
  return `feat/task-${Date.now().toString(36).slice(-4)}`
}

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return
    prompt.value = ''
    branch.value = `feat/task-${Date.now().toString(36).slice(-4)}`
    branchEdited.value = false
    baseRef.value = props.defaultBaseRef || 'main'
    mode.value = 'arena'
    const fallbackId =
      props.defaultProfileId ||
      props.profiles.find((p) => p.isDefault)?.id ||
      props.profiles[0]?.id
    selectedProfileIds.value = fallbackId ? [fallbackId] : []
  },
)

watch(prompt, (val) => {
  if (!branchEdited.value && val.trim()) {
    branch.value = slugifyBranch(val)
  }
})

function toggleProfile(id: string): void {
  if (selectedProfileIds.value.includes(id)) {
    selectedProfileIds.value = selectedProfileIds.value.filter((x) => x !== id)
  } else {
    selectedProfileIds.value = [...selectedProfileIds.value, id]
  }
}

const budgetHint = computed(() => {
  const cnt = Math.max(1, selectedProfileIds.value.length)
  if (mode.value === 'arena') {
    return `将新建 ${cnt} 个独立分支目录与终端`
  }
  return `将在当前目录新开 ${cnt} 个终端会话`
})

const canSubmit = computed(() => {
  if (mode.value === 'arena') {
    return Boolean(branch.value.trim())
  }
  return Boolean(prompt.value.trim() || branch.value.trim())
})

function handleSubmit(): void {
  if (!canSubmit.value || props.busy) return
  const resolvedBranch = branch.value.trim() || slugifyBranch(prompt.value)
  const fallbackId =
    props.defaultProfileId || props.profiles[0]?.id || ''
  const profileIds = selectedProfileIds.value.length
    ? selectedProfileIds.value.slice()
    : fallbackId
      ? [fallbackId]
      : []

  emit('submit', {
    mode: mode.value,
    prompt: prompt.value.trim(),
    branch: resolvedBranch,
    baseRef: baseRef.value.trim() || 'main',
    profileIds,
  })
}
</script>

<style scoped>
.nt-backdrop {
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
.nt-panel {
  width: min(600px, 100%);
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
.nt-head {
  padding: 11px 15px;
  border-bottom: 1px solid var(--line, rgba(255, 255, 255, 0.075));
  display: flex;
  align-items: center;
  gap: 8px;
  background: #131720;
}
.nt-head h2 {
  margin: 0;
  font-size: 13.5px;
  font-weight: 600;
}
.nt-close-btn {
  margin-left: auto;
  width: 26px;
  height: 26px;
  border-radius: 5px;
  background: transparent;
  border: 0;
  color: var(--text-secondary, #a2acc0);
  cursor: pointer;
}
.nt-close-btn:hover {
  background: var(--bg-hover, #222937);
  color: #fff;
}
.nt-body {
  padding: 13px 15px 15px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.nt-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
}
.nt-input {
  height: 32px;
  padding: 0 10px;
  border-radius: 6px;
  background: var(--bg-canvas, #0c0e12);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  font-size: 12px;
  color: var(--text, #eef1f6);
  width: 100%;
}
.nt-mono {
  font-family: var(--font-mono, ui-monospace, monospace);
}
.nt-grid-2 {
  display: grid;
  grid-template-columns: 2fr 1fr;
  gap: 8px;
}
.nt-mode-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}
.nt-choice-card {
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
.nt-choice-card:hover {
  border-color: rgba(180, 142, 255, 0.45);
}
.nt-choice-card[aria-checked='true'] {
  border-color: var(--purple, #b48eff);
  background: rgba(180, 142, 255, 0.12);
}
.nt-c-gold {
  color: #f5c96b;
  font-size: 12px;
}
.nt-c-blue {
  color: #9ec5fe;
  font-size: 12px;
}
.nt-choice-desc {
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
  line-height: 1.45;
}
.nt-prof-section {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.nt-prof-label {
  font-size: 11px;
  color: var(--text-secondary, #a2acc0);
}
.nt-prof-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 6px;
  max-height: 172px;
  overflow-y: auto;
  padding-right: 4px;
}
.nt-prof-item {
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
.nt-prof-item.checked {
  border-color: #6ea8fe;
  background: var(--accent-bg, rgba(110, 168, 254, 0.15));
}
.nt-prof-left {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.nt-prof-model {
  font-size: 10.5px;
  color: var(--text-muted, #6c7688);
  font-family: var(--font-mono, ui-monospace, monospace);
}
.nt-error {
  padding: 6px 10px;
  border-radius: 6px;
  background: rgba(255, 107, 107, 0.14);
  border: 1px solid rgba(255, 107, 107, 0.45);
  color: #ffd0d0;
  font-size: 11.5px;
}
.nt-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.nt-budget-hint {
  font-size: 11px;
  color: var(--text-muted, #6c7688);
}
.nt-foot-btns {
  display: flex;
  gap: 8px;
}
.nt-btn-ghost {
  height: 30px;
  padding: 0 11px;
  border-radius: 6px;
  color: var(--text-secondary, #a2acc0);
  border: 1px solid var(--line-strong, rgba(255, 255, 255, 0.145));
  background: var(--bg-surface, #161a22);
  font-size: 11.5px;
  cursor: pointer;
}
.nt-btn-ghost:hover {
  background: var(--bg-hover, #222937);
  color: #fff;
}
.nt-btn-white {
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
.nt-btn-white:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
@media (max-width: 640px) {
  .nt-grid-2,
  .nt-mode-grid,
  .nt-prof-grid {
    grid-template-columns: 1fr;
  }
}
</style>
