<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { X } from 'lucide-vue-next'
import { fmtCost, fmtCredits, fmtTokens } from './cost'
import { useAgentDetail, type AgentDetailFilter, type AgentDetailTask } from './useAgentDetail'
import type { AgentReportWindow } from './useAgentReport'

const props = defineProps<{ open: boolean; initialWindow: AgentReportWindow }>()
const emit = defineEmits<{ (event: 'close'): void }>()
type View = 'overview' | 'runtime' | 'models' | 'tasks'
const VIEWS: Array<{ key: View; label: string }> = [{ key: 'overview', label: '总览' }, { key: 'runtime', label: 'Runtime' }, { key: 'models', label: '模型' }, { key: 'tasks', label: '任务' }]
const view = ref<View>('overview')
const closeRef = ref<HTMLButtonElement | null>(null)
const expanded = ref<string>('')
const { detail, loading, error, load } = useAgentDetail()
const localTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const filter = reactive<AgentDetailFilter>({ window: props.initialWindow, timezone: localTimezone, limit: 30 })
const timezones = [...new Set([localTimezone, 'Asia/Shanghai', 'UTC'])]
const report = computed(() => detail.value?.report)

const runtimeLabel = (runtime: string) => runtime === 'claude' ? 'Claude' : runtime === 'codex' ? 'Codex' : runtime
const duration = (seconds?: number) => {
  if (typeof seconds !== 'number') return '—'
  const minutes = Math.round(seconds / 60)
  return minutes >= 60 ? `${Math.floor(minutes / 60)}h${minutes % 60 ? `${minutes % 60}m` : ''}` : `${minutes}m`
}
const latency = (seconds?: number) => typeof seconds !== 'number' ? '—' : seconds < 60 ? `${seconds < 10 ? seconds.toFixed(1) : Math.round(seconds)}s` : duration(seconds)
const dateTime = (value?: string) => value ? new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(value)) : '—'
const outcomeLabel = (value: string) => ({ verified_pass: '已验证', human_accepted: '已验收', completed_unverified: '完成·未验证', verified_fail: '验证失败', human_rework: '需返工', interrupted: '中断', error: '错误', open: '未闭合' }[value] ?? value)
const statusClass = (task: AgentDetailTask) => task.outcome === 'verified_pass' || task.outcome === 'human_accepted' ? 'ok' : task.status === 'error' || task.status === 'interrupted' ? 'bad' : 'unknown'
const healthRule = (reason: { comparator?: string; threshold?: number; minimum_n?: number; evidence_ref: string; coverage_ref?: string }) => {
  const comparators: Record<string, string> = { '>': '高于', '>=': '不少于', '<': '低于', '<=': '不高于', '==': '等于' }
  const evidence: Record<string, string> = {
    AgentRun: 'Agent 运行记录', OutcomeEvidence: '结果证据', TaskProfile: '任务画像',
    'summary.interrupted': '中断任务数', 'summary.completed_unverified': '完成但未验证的任务',
    comparisons: '同类任务比较', 'runtime_profiles.observed_response_tokens_per_second': 'Runtime 响应吞吐',
    'observability.projection': '数据投影完整性',
  }
  const coverage: Record<string, string> = {
    'coverage.outcome': '结果证据', 'coverage.response_speed': '响应时序',
    'observability.stages.comparison': '可比任务', 'observability.projection': '投影完整性',
  }
  const rule = reason.comparator && reason.threshold !== undefined ? `触发条件 ${comparators[reason.comparator] ?? reason.comparator} ${reason.threshold}` : ''
  const sample = reason.minimum_n ? `至少 ${reason.minimum_n} 个样本` : ''
  const source = evidence[reason.evidence_ref] ?? reason.evidence_ref
  const coverageLabel = reason.coverage_ref ? coverage[reason.coverage_ref] ?? reason.coverage_ref : ''
  return [`观察项 ${source}`, rule, sample, coverageLabel ? `覆盖 ${coverageLabel}` : ''].filter(Boolean).join(' · ')
}

function requestFilter(cursor = ''): AgentDetailFilter {
  return { ...filter, cursor }
}
function refresh() { void load(requestFilter()) }
function loadMore() {
  if (detail.value?.next_cursor) void load(requestFilter(detail.value.next_cursor), true)
}
function close() { emit('close') }
function onKeydown(event: KeyboardEvent) { if (props.open && event.key === 'Escape') close() }

watch(() => props.open, async (open) => {
  if (!open) return
	const windowChanged = filter.window !== props.initialWindow
  filter.window = props.initialWindow
	if (!windowChanged) refresh()
  await nextTick()
  closeRef.value?.focus()
})
watch(() => [filter.window, filter.timezone, filter.project, filter.task_class, filter.risk, filter.outcome, filter.runtime], () => {
  if (props.open) refresh()
})
onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="ard-backdrop" @click.self="close">
      <section class="ard-panel" role="dialog" aria-modal="true" aria-labelledby="ard-title">
        <header class="ard-head">
          <div><span class="ard-eyebrow">透明健康判定 · 无综合分</span><h2 id="ard-title">Agent 效能报表</h2></div>
          <button ref="closeRef" type="button" class="ard-close" aria-label="关闭 Agent 效能报表" @click="close"><X :size="18" /></button>
        </header>

        <div class="ard-filters" aria-label="报表筛选">
          <label class="ard-filter"><span>时间</span><select v-model="filter.window" aria-label="时间范围"><option value="24h">今日</option><option value="7d">7天</option><option value="14d">14天</option><option value="30d">30天</option></select></label>
          <label class="ard-filter"><span>时区</span><select v-model="filter.timezone" aria-label="时区"><option v-for="item in timezones" :key="item" :value="item">{{ item }}</option></select></label>
          <label class="ard-filter"><span>项目</span><select v-model="filter.project" aria-label="项目"><option value="">全部项目</option><option v-for="item in detail?.filters.projects ?? []" :key="item" :value="item">{{ item }}</option></select></label>
          <label class="ard-filter"><span>任务类型</span><select v-model="filter.task_class" aria-label="任务类型" :disabled="!(detail?.filters.task_classes.length)"><option value="">{{ detail?.filters.task_classes.length ? '全部任务类型' : '未采集' }}</option><option v-for="item in detail?.filters.task_classes ?? []" :key="item" :value="item">{{ item }}</option></select></label>
          <label class="ard-filter"><span>风险</span><select v-model="filter.risk" aria-label="风险" :disabled="!(detail?.filters.risks.length)"><option value="">{{ detail?.filters.risks.length ? '全部风险' : '未采集' }}</option><option v-for="item in detail?.filters.risks ?? []" :key="item" :value="item">{{ item }}</option></select></label>
          <label class="ard-filter"><span>结果</span><select v-model="filter.outcome" aria-label="结果"><option value="">全部结果</option><option v-for="item in detail?.filters.outcomes ?? []" :key="item" :value="item">{{ outcomeLabel(item) }}</option></select></label>
          <label class="ard-filter"><span>Runtime</span><select v-model="filter.runtime" aria-label="Runtime"><option value="">全部 Runtime</option><option v-for="item in detail?.filters.runtimes ?? []" :key="item" :value="item">{{ runtimeLabel(item) }}</option></select></label>
        </div>

        <nav class="ard-tabs" role="tablist" aria-label="Agent 报表视图">
          <button v-for="item in VIEWS" :key="item.key" type="button" role="tab" :aria-selected="view === item.key" :class="{ on: view === item.key }" @click="view = item.key">{{ item.label }}</button>
        </nav>

        <div v-if="loading && !detail" class="ard-state">正在建立可追溯视图…</div>
        <div v-else-if="error && !detail" class="ard-state bad">{{ error }} <button type="button" @click="refresh">重试</button></div>
        <main v-else-if="detail && report" class="ard-content" :aria-busy="loading">
          <template v-if="view === 'overview'">
            <section class="ard-health" :class="`is-${report.health.state}`">
              <div><strong>{{ report.health.label }}</strong><span>{{ report.health.headline }}</span><small>{{ report.health.policy_version }}</small></div>
              <div class="ard-health-axes"><article v-for="axis in report.health.axes" :key="axis.key" :class="`is-${axis.state}`"><i /><b>{{ axis.label }}</b><span>{{ axis.headline }}</span></article></div>
              <div v-if="report.health.reasons.length" class="ard-health-reasons"><span v-for="reason in report.health.reasons" :key="reason.code"><b>{{ reason.message }}</b><small>{{ healthRule(reason) }}</small></span></div>
            </section>
            <div class="ard-kpis">
              <article><span>任务</span><strong>{{ report.summary.work_items }}</strong><small>{{ report.summary.completed }} 完成 · {{ report.summary.interrupted }} 中断 · {{ report.summary.errors }} 错误</small></article>
              <article><span>墙钟耗时</span><strong>{{ duration(report.summary.wall_seconds) }}</strong><small>活跃区间并集</small></article>
              <article><span>Agent 累计</span><strong>{{ duration(report.summary.cumulative_seconds) }}</strong><small>区间求和 · 并发 {{ report.summary.average_concurrency?.toFixed(2) ?? '—' }}×</small></article>
              <article><span>结果证据</span><strong>{{ report.summary.verified_pass }}</strong><small>已验证；{{ report.summary.completed_unverified }} 完成未验证</small></article>
            </div>
            <section class="ard-card"><h3>子 Agent 调度 <small>根任务生命周期已去重，不重复算一次调度健康</small></h3><p>{{ report.summary.delegated_lifecycle.submitted }} 提交 · {{ report.summary.delegated_lifecycle.started }} 启动 · {{ report.summary.delegated_lifecycle.completed }} 完成 · {{ report.summary.delegated_lifecycle.interrupted }} 中断 · {{ report.summary.delegated_lifecycle.errors }} 错误 · {{ report.summary.delegated_lifecycle.never_started }} 未启动</p></section>
            <section class="ard-card"><h3>工具执行 <small>中断、运行中、未知均不混入平均耗时</small></h3><p>{{ report.tools.calls }} 次 · {{ report.tools.completed }} 完成 · {{ report.tools.errors }} 错误 · {{ report.tools.interrupted }} 中断 · {{ report.tools.open }} 运行中 · {{ report.tools.unknown }} 未知 · 平均 {{ latency(report.tools.average_duration_seconds) }}</p><small>耗时覆盖 {{ report.tools.timing_coverage.observed_n }}/{{ report.tools.timing_coverage.eligible_n }}</small></section>
            <section class="ard-card"><h3>数据健康 <small>投影与指标分开观测</small></h3><p><b :class="report.observability.projection.state">{{ report.observability.projection.state }}</b> · {{ report.observability.projection.mode }} · {{ report.observability.projection.source_files }} 源文件 / {{ report.observability.projection.changed_files }} 变更</p><small>{{ report.observability.projection.index_schema || '内存视图' }} · 刷新于 {{ dateTime(report.observability.projection.refreshed_at) }}</small><div v-for="(coverage, key) in report.observability.stages" :key="key" class="ard-coverage"><span>{{ key }}</span><b :class="coverage.state">{{ coverage.state }}</b><span>{{ coverage.observed_n }}/{{ coverage.eligible_n }}</span><small>{{ coverage.diagnostics?.join(' · ') || coverage.provenance.join(' · ') }}</small></div></section>
            <section class="ard-card"><h3>证据覆盖</h3><div v-for="(coverage, key) in report.coverage" :key="key" class="ard-coverage"><span>{{ key }}</span><b :class="coverage.state">{{ coverage.state }}</b><span>{{ coverage.observed_n }}/{{ coverage.eligible_n }}</span><small>{{ coverage.diagnostics?.join(' · ') || coverage.provenance.join(' · ') }}</small></div></section>
            <section class="ard-card"><h3>产出事实</h3><p>代码 {{ report.artifacts.by_kind.code?.files ?? 0 }} 文件 / 写 {{ report.artifacts.by_kind.code?.written_lines ?? 0 }} 行 / +{{ report.artifacts.by_kind.code?.additions ?? 0 }}/−{{ report.artifacts.by_kind.code?.deletions ?? 0 }} · 文档 {{ report.artifacts.by_kind.doc?.files ?? 0 }} 文件 / 写 {{ report.artifacts.by_kind.doc?.written_lines ?? 0 }} 行 / +{{ report.artifacts.by_kind.doc?.additions ?? 0 }}/−{{ report.artifacts.by_kind.doc?.deletions ?? 0 }}</p><small>写入行与可证增删分开；generated/vendor/build 已排除，同一文件多次 Edit 只算一个文件。</small></section>
          </template>

          <template v-else-if="view === 'runtime'">
            <section v-for="runtime in report.runtime_profiles" :key="runtime.runtime" class="ard-card ard-runtime">
              <h3>{{ runtimeLabel(runtime.runtime) }} <small>{{ runtime.completed }}/{{ runtime.work_items }} 完成</small></h3>
              <div class="ard-runtime-grid"><span>active <b>{{ duration(runtime.active_seconds) }}</b></span><span>模型请求 <b>{{ runtime.model_requests }}</b></span><span>Agent <b>{{ runtime.agent_instances }}</b></span><span>工具调用 <b>{{ runtime.tools.calls }} · 均 {{ latency(runtime.tools.average_duration_seconds) }}</b></span><span>输出 <b>{{ fmtTokens(runtime.output_tokens) }} tok</b></span><span>观察吞吐 <b>{{ runtime.observed_response_tokens_per_second?.toFixed(1) ?? '证据不足' }}<small v-if="runtime.observed_response_tokens_per_second"> tok/s</small></b></span><span v-if="runtime.generation_tokens_per_second">纯生成 <b>{{ runtime.generation_tokens_per_second.toFixed(1) }} tok/s</b></span><span title="transcript 因果输入到首个 assistant 事件，不等同于 provider 首 token">首响应 <b>{{ latency(runtime.observed_first_response_median_seconds) }}</b></span><span v-if="runtime.ttft_median_seconds !== undefined" title="provider 请求开始到首 token 的精确证据">TTFT <b>{{ latency(runtime.ttft_median_seconds) }}</b></span></div>
              <small>首响应覆盖 {{ runtime.first_response_coverage.observed_n }}/{{ runtime.first_response_coverage.eligible_n }}；TTFT 覆盖 {{ runtime.ttft_coverage.observed_n }}/{{ runtime.ttft_coverage.eligible_n }}。同一接口、不同证据等级，不混名。</small>
              <div class="ard-runtime-output">
                <h4>可证产出 <small>覆盖 {{ runtime.artifact_coverage.observed_n }}/{{ runtime.artifact_coverage.eligible_n }} 任务</small></h4>
                <p>代码 {{ runtime.artifacts.by_kind.code?.files ?? 0 }} 文件（新 {{ runtime.artifacts.by_kind.code?.created_files ?? 0 }} / 改 {{ runtime.artifacts.by_kind.code?.modified_files ?? 0 }}）· 写 {{ runtime.artifacts.by_kind.code?.written_lines ?? 0 }} 行 · +{{ runtime.artifacts.by_kind.code?.additions ?? 0 }}/−{{ runtime.artifacts.by_kind.code?.deletions ?? 0 }}</p>
                <p>文档 {{ runtime.artifacts.by_kind.doc?.files ?? 0 }} 文件（新 {{ runtime.artifacts.by_kind.doc?.created_files ?? 0 }} / 改 {{ runtime.artifacts.by_kind.doc?.modified_files ?? 0 }}）· 写 {{ runtime.artifacts.by_kind.doc?.written_lines ?? 0 }} 行 · +{{ runtime.artifacts.by_kind.doc?.additions ?? 0 }}/−{{ runtime.artifacts.by_kind.doc?.deletions ?? 0 }}</p>
                <div class="ard-yield-grid">
                  <span>模型输出 <b>{{ fmtTokens(runtime.resource_yield.request_output_tokens) }} tok</b><small>token 覆盖 {{ runtime.resource_yield.token_coverage.observed_n }}/{{ runtime.resource_yield.token_coverage.eligible_n }}</small></span>
                  <span>产出密度 <b>{{ runtime.resource_yield.written_lines_per_thousand_output_tokens !== undefined ? `${runtime.resource_yield.token_coverage.state === 'complete' ? '' : '≈'}${runtime.resource_yield.written_lines_per_thousand_output_tokens.toFixed(1)} 行/1K tok` : '—' }}</b></span>
                  <span>产出节奏 <b>{{ runtime.resource_yield.written_lines_per_active_hour !== undefined ? `${runtime.resource_yield.written_lines_per_active_hour.toFixed(1)} 行/active h` : '—' }}</b></span>
                  <span>API 等价/千行 <b v-if="runtime.resource_yield.api_equivalent_per_thousand_written_lines">{{ runtime.resource_yield.cost_coverage.state === 'complete' ? '' : '≥' }}{{ fmtCost(runtime.resource_yield.api_equivalent_per_thousand_written_lines.amount, runtime.resource_yield.api_equivalent_per_thousand_written_lines.currency) }}</b><b v-else>—</b><small>价格覆盖 {{ runtime.resource_yield.cost_coverage.observed_n }}/{{ runtime.resource_yield.cost_coverage.eligible_n }}</small></span>
                </div>
                <small>只汇总该 runtime 的 provider Edit/Write 证据；不按共享 worktree 总 diff 猜归属，也不把行数当能力分。</small>
              </div>
            </section>
            <section class="ard-card"><h3>公平比较</h3><div v-if="report.comparisons.length"><p v-for="(comparison, index) in report.comparisons" :key="index">{{ comparison.recommendation || comparison.reason }}</p></div><p v-else>任务画像或结果样本不足，暂不横比。需要相同 project / task class / scope / risk / oracle 的前置画像与结果证据。</p></section>
          </template>

          <template v-else-if="view === 'models'">
            <section class="ard-card"><h3>生产请求表现 <small>Top 3 已知模型，未知证据桶另列；资源构成，不是能力榜</small></h3><template v-for="runtime in report.runtime_profiles" :key="runtime.runtime"><h4>{{ runtimeLabel(runtime.runtime) }}</h4><div v-for="model in report.top_cost_models[runtime.runtime] ?? []" :key="model.model" class="ard-model"><b>{{ model.model || '未知模型' }}</b><span>{{ model.request_n }} 请求</span><span>{{ model.cost ? fmtCost(model.cost.amount, model.cost.currency) : '价格证据不足' }}</span><small class="ard-model-speed" :title="`同模型可用输出 ${fmtTokens(model.observed_response_output_tokens)} tok / ${model.observed_response_duration_seconds.toFixed(1)}s`">{{ model.observed_response_tokens_per_second !== undefined ? `观察吞吐 ${model.observed_response_tokens_per_second.toFixed(1)} tok/s` : '观察吞吐证据不足' }} · 覆盖 {{ model.response_speed_coverage.observed_n }}/{{ model.response_speed_coverage.eligible_n }}</small><small>{{ [...(model.efforts ?? []), ...(model.service_tiers ?? [])].join(' · ') || 'effort/tier 未采集' }} · 成本覆盖 {{ model.cost_coverage.observed_n }}/{{ model.cost_coverage.eligible_n }}</small><small v-if="model.credits !== undefined">官方额度折算 {{ fmtCredits(model.credits) }} credits<span v-if="model.fast_multipliers?.length"> · Fast {{ model.fast_multipliers.map(value => `${value}×`).join('/') }}</span> · 覆盖 {{ model.credit_coverage.observed_n }}/{{ model.credit_coverage.eligible_n }}</small></div></template></section>
            <section class="ard-card"><h3>受控能力实验</h3><p>暂无 versioned paired experiment 结果。</p><small>生产观察不能证明 model/effort 的因果能力；mixed-model 任务也不会把最终成功重复记给每个模型。</small></section>
          </template>

          <template v-else>
            <section v-for="task in detail.tasks" :key="task.id" class="ard-task" :class="statusClass(task)">
              <button type="button" class="ard-task-head" :aria-expanded="expanded === task.id" @click="expanded = expanded === task.id ? '' : task.id">
                <span><b>{{ runtimeLabel(task.runtime) }}</b> · {{ outcomeLabel(task.outcome) }}</span><span>{{ dateTime(task.ended_at || task.started_at) }}</span><small>{{ task.project || '项目未采集' }} · {{ task.agent_instances }} Agent · {{ task.requests.length }} 请求 · {{ task.tool_calls }} 工具</small>
              </button>
              <div v-if="expanded === task.id" class="ard-trace">
                <p><b>TaskProfile</b> {{ task.task_profile.task_class || '未采集' }} / {{ task.task_profile.risk || '风险未采集' }} / {{ task.task_profile.oracle || 'oracle 未采集' }}</p>
                <p><b>Outcome evidence</b> {{ task.diagnostics?.join(' · ') || outcomeLabel(task.outcome) }}</p>
                <p><b>Transcript</b> <code>{{ task.source_ref || '未关联' }}</code></p>
                <div><b>Assignments</b><span v-for="assignment in task.assignments" :key="assignment.id">#{{ assignment.attempt }} {{ assignment.agent_instance_id }} · {{ assignment.status }}</span></div>
                <div><b>Model requests</b><span v-for="request in task.requests" :key="request.id">{{ request.model || '未知模型' }} · {{ request.effort || 'effort?' }} · {{ request.service_tier || 'tier?' }} · {{ request.api_equivalent ? fmtCost(request.api_equivalent.amount, request.api_equivalent.currency) : '价格缺失' }}</span></div>
              </div>
            </section>
            <div v-if="!detail.tasks.length" class="ard-state">该筛选下没有任务</div>
            <button v-if="detail.next_cursor" type="button" class="ard-more" :disabled="loading" @click="loadMore">{{ loading ? '加载中…' : '再显示 30 个' }}</button>
          </template>
        </main>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.ard-backdrop { position: fixed; inset: 0; z-index: 4100; display: grid; place-items: center; padding: 18px; background: rgba(4,6,10,.72); backdrop-filter: blur(3px); }
.ard-panel { width: min(980px, 100%); height: min(820px, calc(100vh - 36px)); display: flex; flex-direction: column; overflow: hidden; border: 1px solid #30343d; border-radius: 14px; background: #15171c; color: #e8eaf0; box-shadow: 0 28px 80px rgba(0,0,0,.55); }
.ard-head { display: flex; align-items: center; justify-content: space-between; padding: 16px 18px 10px; }.ard-head h2 { margin: 2px 0 0; font-size: 20px; }.ard-eyebrow { color: #7892c2; font-size: 10px; }.ard-close { display: grid; place-items: center; width: 34px; height: 34px; border: 1px solid #30343d; border-radius: 8px; background: transparent; color: #aeb3bd; cursor: pointer; }
.ard-filters { display: grid; grid-template-columns: repeat(7, minmax(0,1fr)); gap: 6px; padding: 0 18px 10px; }.ard-filter { display: grid; min-width: 0; gap: 3px; }.ard-filter > span { color: #7f8793; font-size: 9px; }.ard-filters select { width: 100%; min-width: 0; padding: 6px 7px; border: 1px solid #2b2f38; border-radius: 7px; background: #1c1f26; color: #cbd0d8; font: inherit; font-size: 10px; }.ard-filters select:disabled { opacity: .65; }
.ard-tabs { display: flex; gap: 3px; margin: 0 18px; padding: 3px; border: 1px solid #292d35; border-radius: 8px; background: #1b1e24; }.ard-tabs button { flex: 1; padding: 6px; border: 0; border-radius: 6px; background: transparent; color: #858b96; cursor: pointer; }.ard-tabs button.on { background: #30343d; color: #f1f2f5; font-weight: 600; }
.ard-content { min-height: 0; flex: 1; overflow: auto; padding: 14px 18px 22px; }.ard-content[aria-busy=true] { opacity: .72; }.ard-state { margin: auto; padding: 40px; color: #89909b; text-align: center; }.ard-state.bad { color: #f08b8b; }
.ard-health { display: grid; grid-template-columns: minmax(180px,.8fr) minmax(0,2fr); gap: 12px; margin-bottom: 11px; padding: 13px; border: 1px solid #353a44; border-radius: 10px; background: #1a1d23; }.ard-health > div:first-child { display: grid; align-content: center; gap: 5px; }.ard-health > div:first-child strong { width: max-content; padding: 3px 9px; border-radius: 999px; background: rgba(148,163,184,.12); color: #cbd5e1; font-size: 15px; }.ard-health > div:first-child span { color: #b8bdc6; font-size: 11px; line-height: 1.4; }.ard-health > div:first-child small { color: #666e79; font-size: 9px; }.ard-health.is-healthy { border-color: rgba(74,222,128,.28); }.ard-health.is-healthy > div:first-child strong { color: #4ade80; }.ard-health.is-attention { border-color: rgba(251,191,36,.32); }.ard-health.is-attention > div:first-child strong { color: #fbbf24; }.ard-health.is-critical { border-color: rgba(248,113,113,.36); }.ard-health.is-critical > div:first-child strong { color: #f87171; }.ard-health-axes { display: grid; gap: 5px; }.ard-health-axes article { display: grid; grid-template-columns: 7px 38px minmax(0,1fr); align-items: center; gap: 7px; padding: 6px 8px; border-radius: 7px; background: #20242b; font-size: 10px; }.ard-health-axes i { width: 7px; height: 7px; border-radius: 50%; background: #6b7280; }.ard-health-axes b { color: #d2d5da; }.ard-health-axes span { overflow: hidden; color: #858c97; text-overflow: ellipsis; white-space: nowrap; }.ard-health-axes .is-healthy i { background: #4ade80; }.ard-health-axes .is-attention i { background: #fbbf24; }.ard-health-axes .is-critical i { background: #f87171; }
.ard-health-reasons { grid-column: 1/-1; display: grid; gap: 7px; padding-top: 8px; border-top: 1px solid #2a2f37; }.ard-health-reasons > span { display: grid; gap: 3px; color: #bdc2ca; font-size: 10.5px; line-height: 1.4; }.ard-health-reasons b { font-weight: 500; }.ard-health-reasons small { color: #858d99; font-size: 9.5px; overflow-wrap: anywhere; }
.ard-kpis { display: grid; grid-template-columns: repeat(4,1fr); gap: 9px; }.ard-kpis article,.ard-card,.ard-task { border: 1px solid #282c34; border-radius: 9px; background: #1a1d23; }.ard-kpis article { padding: 12px; }.ard-kpis span,.ard-kpis small { display: block; color: #7f8691; font-size: 10px; }.ard-kpis strong { display: block; margin: 5px 0; font-size: 21px; font-variant-numeric: tabular-nums; }
.ard-card { margin-top: 11px; padding: 13px; }.ard-card h3 { margin: 0 0 10px; font-size: 13px; }.ard-card h3 small { margin-left: 6px; color: #747b87; font-weight: 400; }.ard-card h4 { margin: 12px 0 5px; color: #8ca9e4; font-size: 11px; }.ard-card p { color: #aab0ba; font-size: 11px; line-height: 1.55; }.ard-card > small { color: #707782; font-size: 10px; }
.ard-coverage { display: grid; grid-template-columns: 110px 65px 55px 1fr; gap: 8px; padding: 5px 0; border-top: 1px solid #242830; font-size: 10px; }.ard-coverage b.complete { color: #4ade80; }.ard-coverage b.partial { color: #fbbf24; }.ard-coverage b.missing { color: #8b909a; }.ard-coverage small { color: #717782; }
.ard-runtime-grid { display: grid; grid-template-columns: repeat(3,1fr); gap: 7px; }.ard-runtime-grid span { padding: 8px; border-radius: 7px; background: #20242b; color: #7f8690; font-size: 10px; }.ard-runtime-grid b { display: block; margin-top: 3px; color: #d8dbe1; font-size: 12px; }.ard-runtime h3 small { float: right; }
.ard-runtime-output { margin-top: 10px; padding: 9px 10px; border-radius: 8px; background: #20242b; }.ard-runtime-output h4 { margin: 0 0 5px; color: #d8dbe1; }.ard-runtime-output h4 small { margin-left: 6px; color: #7f8690; font-weight: 400; }.ard-runtime-output p { margin: 3px 0; color: #a9afb8; font-size: 11px; }
.ard-yield-grid { display: grid; grid-template-columns: repeat(4,minmax(0,1fr)); gap: 7px; margin: 8px 0; }.ard-yield-grid span { padding: 7px; border: 1px solid #303540; border-radius: 7px; color: #7f8793; font-size: 9px; }.ard-yield-grid b,.ard-yield-grid small { display: block; margin-top: 3px; }.ard-yield-grid b { color: #c5cee1; font-size: 11px; }.ard-yield-grid small { color: #666e79; }
.ard-model { display: grid; grid-template-columns: minmax(0,1fr) auto auto; gap: 5px 12px; padding: 8px 0; border-top: 1px solid #252932; font-size: 10px; }.ard-model small { grid-column: 1/-1; color: #717782; }
.ard-model .ard-model-speed { color: #8eabe7; font-variant-numeric: tabular-nums; }
.ard-task { margin-bottom: 7px; overflow: hidden; border-left: 3px solid #737985; }.ard-task.ok { border-left-color: #4ade80; }.ard-task.bad { border-left-color: #f87171; }.ard-task-head { width: 100%; display: grid; grid-template-columns: 1fr auto; gap: 4px 12px; padding: 10px 12px; border: 0; background: transparent; color: #cfd3da; text-align: left; cursor: pointer; }.ard-task-head small { grid-column: 1/-1; color: #777e89; }.ard-trace { padding: 3px 12px 12px; border-top: 1px solid #272b33; color: #949ba6; font-size: 10px; }.ard-trace p { overflow-wrap: anywhere; }.ard-trace > div { display: flex; flex-direction: column; gap: 3px; margin-top: 9px; }.ard-trace code { color: #829bd0; }.ard-more { width: 100%; padding: 8px; border: 1px solid #303540; border-radius: 8px; background: #20242b; color: #aeb5c0; cursor: pointer; }
@media (max-width: 760px) { .ard-backdrop { padding: 0; place-items: stretch; }.ard-panel { width: 100%; height: 100%; max-height: none; border-radius: 0; }.ard-filters { grid-template-columns: repeat(2,minmax(0,1fr)); }.ard-health { grid-template-columns: 1fr; }.ard-kpis { grid-template-columns: repeat(2,1fr); }.ard-runtime-grid,.ard-yield-grid { grid-template-columns: repeat(2,1fr); }.ard-coverage { grid-template-columns: 90px 55px 40px; }.ard-coverage small { grid-column: 1/-1; }.ard-model { grid-template-columns: minmax(0,1fr) auto; }.ard-model > span:nth-of-type(2) { grid-column: 2; }.ard-head { padding-top: max(14px, env(safe-area-inset-top)); } }
</style>
