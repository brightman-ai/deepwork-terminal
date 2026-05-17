<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { FormPane } from '@ce/components/pane'
import { apiUrl } from '@ce/utils/runtimeBase'
import { CheckCircle2, XCircle, AlertCircle, RefreshCw, PlayCircle } from 'lucide-vue-next'

interface StepSummary {
  id: string
  title: string
  description: string
  category: 'permission' | 'api_key' | 'feature'
  platform: string
  priority: number
  depends_on: string[]
  status: 'configured' | 'not_configured' | 'error' | 'skipped'
}

interface TestResult {
  success: boolean
  message: string
  preview?: string
}

const steps = ref<StepSummary[]>([])
const loading = ref(false)
const testingId = ref('')
const testingAll = ref(false)
const testResults = ref<Record<string, TestResult>>({})
const error = ref('')

const groupedSteps = computed(() => {
  const groups: Record<string, StepSummary[]> = {
    permission: [],
    api_key: [],
    feature: [],
  }
  for (const step of steps.value) {
    if (groups[step.category]) {
      groups[step.category].push(step)
    }
  }
  return groups
})

const categoryLabel: Record<string, string> = {
  permission: '权限',
  api_key: 'API 密钥',
  feature: '功能',
}

// Sentinel fields — one per category group
const formSections = computed(() => [
  { id: 'wizard-steps', title: '配置步骤', fields: [{ id: '__wizard_steps', type: 'text' as const, label: '' }] },
])

onMounted(loadSteps)

async function loadSteps(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch(apiUrl('/api/setup/steps'))
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    steps.value = (data.steps || []) as StepSummary[]
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载步骤失败'
  } finally {
    loading.value = false
  }
}

async function testStep(stepId: string): Promise<void> {
  testingId.value = stepId
  try {
    const res = await fetch(apiUrl(`/api/setup/steps/${encodeURIComponent(stepId)}/test`), {
      method: 'POST',
    })
    const data = (await res.json().catch(() => ({}))) as TestResult & { error?: string }
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`)
    testResults.value = { ...testResults.value, [stepId]: data }
  } catch (e) {
    testResults.value = {
      ...testResults.value,
      [stepId]: { success: false, message: e instanceof Error ? e.message : '测试失败' },
    }
  } finally {
    testingId.value = ''
  }
}

async function testAll(): Promise<void> {
  testingAll.value = true
  try {
    const res = await fetch(apiUrl('/api/setup/steps/test-all'), { method: 'POST' })
    const data = (await res.json().catch(() => ({}))) as {
      results?: Record<string, TestResult>
      error?: string
    }
    if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`)
    testResults.value = data.results ?? {}
  } catch (e) {
    error.value = e instanceof Error ? e.message : '全量测试失败'
  } finally {
    testingAll.value = false
  }
}

function statusIcon(status: string) {
  if (status === 'configured') return CheckCircle2
  if (status === 'error') return XCircle
  if (status === 'skipped') return AlertCircle
  return AlertCircle
}

function statusClass(status: string): string {
  if (status === 'configured') return 'step-status--ok'
  if (status === 'error') return 'step-status--error'
  if (status === 'skipped') return 'step-status--skip'
  return 'step-status--pending'
}
</script>

<template>
  <FormPane :sections="formSections" mode="page" class="h-full">
    <template #actions>
      <div class="flex items-center gap-2">
        <button class="wizard-btn wizard-btn--ghost" :disabled="loading" @click="loadSteps">
          <RefreshCw :size="13" />
          刷新
        </button>
        <button class="wizard-btn wizard-btn--primary" :disabled="testingAll || loading" @click="testAll">
          <PlayCircle :size="13" />
          {{ testingAll ? '测试中...' : '全量测试' }}
        </button>
        <span v-if="error" class="text-xs text-destructive">{{ error }}</span>
      </div>
    </template>

    <template #field="{ field }">
      <div v-if="field.id === '__wizard_steps'" class="space-y-5">
        <div v-if="loading" class="text-xs text-muted-foreground animate-pulse">加载步骤中...</div>
        <div v-else-if="!steps.length" class="text-xs text-muted-foreground">无可用步骤。</div>
        <template v-else>
          <div
            v-for="(groupSteps, category) in groupedSteps"
            v-show="groupSteps.length"
            :key="category"
            class="space-y-2"
          >
            <p class="text-[10px] font-semibold text-muted-foreground uppercase tracking-wide">
              {{ categoryLabel[category] || category }}
            </p>
            <div class="space-y-1.5">
              <div
                v-for="step in groupSteps"
                :key="step.id"
                class="step-card"
              >
                <div class="step-card__head">
                  <component
                    :is="statusIcon(step.status)"
                    :size="14"
                    class="step-status"
                    :class="statusClass(step.status)"
                  />
                  <div class="flex flex-col min-w-0 flex-1">
                    <span class="text-xs font-medium text-foreground truncate">{{ step.title }}</span>
                    <span class="text-[10px] text-muted-foreground truncate">{{ step.description }}</span>
                  </div>
                  <button
                    class="wizard-btn wizard-btn--sm"
                    :disabled="testingId === step.id"
                    @click="testStep(step.id)"
                  >
                    {{ testingId === step.id ? '测试中...' : '测试' }}
                  </button>
                </div>
                <div
                  v-if="testResults[step.id]"
                  class="step-card__result"
                  :class="testResults[step.id].success ? 'step-result--ok' : 'step-result--fail'"
                >
                  <span>{{ testResults[step.id].message }}</span>
                  <span v-if="testResults[step.id].preview" class="font-mono text-[9px] opacity-70">
                    {{ testResults[step.id].preview }}
                  </span>
                </div>
              </div>
            </div>
          </div>
        </template>
      </div>
    </template>
  </FormPane>
</template>

<style scoped>
.wizard-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  font-size: 11px;
  font-weight: 500;
  border: 1px solid hsl(var(--border));
  border-radius: 5px;
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  cursor: pointer;
  transition: background-color 0.1s;
}

.wizard-btn:hover:not(:disabled) { background: hsl(var(--muted)); }
.wizard-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.wizard-btn--primary { background: hsl(var(--primary)); color: hsl(var(--primary-foreground)); border-color: hsl(var(--primary)); }
.wizard-btn--primary:hover:not(:disabled) { background: hsl(var(--primary) / 0.9); }
.wizard-btn--ghost { background: transparent; border-color: transparent; }
.wizard-btn--ghost:hover:not(:disabled) { background: hsl(var(--muted)); }
.wizard-btn--sm { padding: 2px 6px; font-size: 10px; }

.step-card {
  border: 1px solid hsl(var(--border));
  border-radius: 6px;
  background: hsl(var(--background));
  overflow: hidden;
}

.step-card__head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 10px;
}

.step-card__result {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 5px 10px;
  font-size: 10px;
  border-top: 1px solid hsl(var(--border));
}

.step-result--ok { background: hsl(140 60% 95%); color: hsl(140 50% 25%); }
.step-result--fail { background: hsl(0 80% 97%); color: hsl(0 70% 35%); }

.step-status { flex-shrink: 0; }
.step-status--ok { color: hsl(140 50% 40%); }
.step-status--error { color: hsl(0 70% 50%); }
.step-status--skip { color: hsl(40 70% 50%); }
.step-status--pending { color: hsl(var(--muted-foreground)); }
</style>
