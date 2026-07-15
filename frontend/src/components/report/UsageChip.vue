<script setup lang="ts">
/**
 * UsageChip — the CLI topbar's usage entry point. It keeps three bounded contexts apart:
 *
 *   「官方订阅」 — how much of my subscription quota is left, and when does it reset?
 *   「API 计费」 — how much have I actually spent per token?
 *   「Agent 效能」 — what work ran, what finished, and how strong the evidence is.
 *
 * The three tabs never collapse unlike grains. A subscription's "≈等价" (what those tokens would have cost at API prices)
 * is NOT a bill and never appears as one; an API spend is a real bill and never gets a
 * reset time it does not have.
 *
 * The pill is permanent application chrome. Provider/Agent discovery is asynchronous and may
 * legitimately return empty, stale, or failed; none of those data states may remove the user's
 * only route back into this UI. They degrade the content behind the entry, never the entry itself.
 *
 * All numbers come from the usage SSOT (useUsageQuota /usage/quota + useUsageReport
 * /usage/report, both via cliFetch(cliApi) so this works standalone AND pro-embedded) —
 * nothing is computed here.
 *
 * Host-agnostic: the optional "详细报表 ›" link is emitted as `detail` (shown only when the
 * host passes show-detail), so this shared component never depends on any one shell's router.
 */
import { nextTick, onMounted, onUnmounted, ref, computed, watch } from 'vue'
import { Gauge } from 'lucide-vue-next'
import { useUsageQuota, quotaGroupsFor, type QuotaGroup, type RuntimeQuota } from './useUsageQuota'
import { useUsageReport, type UsageProviderRow } from './useUsageReport'
import { useAgentReport } from './useAgentReport'
import AgentReportDetail from './AgentReportDetail.vue'
import { fmtTokens, fmtCost, fmtCredits } from './cost'
import Spark from './Spark.vue'
import { placeAnchoredPopover, type RectLike } from './popoverPlacement'
import { usageMoneyPresentation } from './usageBillingPresentation'

defineProps<{ showDetail?: boolean }>()
const emit = defineEmits<{ (e: 'detail'): void }>()

const {
  quotas, subscriptions, hasSubscription, hasApi, tightest,
  loaded, loading, fetchedAt, probeNote, load, probe,
} = useUsageQuota()

const open = ref(false)
const wrapRef = ref<HTMLElement | null>(null)
const popRef = ref<HTMLElement | null>(null)
const popPos = ref({ top: 0, left: 0, maxHeight: 560 })
let timer: ReturnType<typeof setInterval> | undefined
let placementFrame = 0
let placementObserver: ResizeObserver | null = null

const runtimeLabel = (r: string) => (r === 'claude' ? 'Claude' : r === 'codex' ? 'Codex' : r === 'gemini' ? 'Gemini' : r)
const kindLabel = (k: string) => (k === '5h' ? '5小时' : k === '7d' ? '7天' : k)

// ── 花费区: 4 windows prefetched in PARALLEL, each into its OWN useUsageReport() instance
// (isolated `report` ref) so switching windows is instant and concurrent fetches never race.
type ReportWindow = '24h' | '7d' | '14d' | '30d'
const WINDOWS: { key: ReportWindow; label: string }[] = [
  { key: '24h', label: '今日' },
  { key: '7d', label: '7天' },
  { key: '14d', label: '14天' },
  { key: '30d', label: '30天' },
]
const rep24h = useUsageReport()
const rep7d = useUsageReport()
const rep14d = useUsageReport()
const rep30d = useUsageReport()
const reportByWindow = { '24h': rep24h, '7d': rep7d, '14d': rep14d, '30d': rep30d } as const
const activeWindow = ref<ReportWindow>('24h')
const activeReport = computed(() => reportByWindow[activeWindow.value].report.value)
const activeLoading = computed(() => reportByWindow[activeWindow.value].loading.value)
// Agent windows use the same isolated-cache rule as cost windows. A single mutable
// report ref can briefly paint 24h data under a selected 7d label and lets late
// responses overwrite a newer selection.
const agent24h = useAgentReport()
const agent7d = useAgentReport()
const agent14d = useAgentReport()
const agent30d = useAgentReport()
const agentByWindow = { '24h': agent24h, '7d': agent7d, '14d': agent14d, '30d': agent30d } as const
const agentReport = computed(() => agentByWindow[activeWindow.value].report.value)
const agentLoading = computed(() => agentByWindow[activeWindow.value].loading.value)
const agentError = computed(() => agentByWindow[activeWindow.value].error.value)
const agentDetailOpen = ref(false)
// Honest: cost_complete===false (some model in the window had no price) → the number is an
// under-count; fmtCost prefixes «≈». A specific runtime's cost===null already renders «—».
const costApprox = computed(() => activeReport.value?.summary?.cost_complete === false)
const providersFor = (w: ReportWindow = activeWindow.value) => reportByWindow[w].report.value?.providers ?? []

// ── tabs ─────────────────────────────────────────────────────────────────────────────────
type Tab = 'sub' | 'api' | 'agent'
const tab = ref<Tab>('sub')
// Open on the tab that actually has something to say: an API-only user lands on API, everyone
// else lands on 订阅. Only auto-pick until the user touches the segments themselves.
const tabTouched = ref(false)
watch(loaded, (ok) => {
  if (!ok || tabTouched.value) return
  tab.value = !hasSubscription.value && hasApi.value ? 'api' : 'sub'
})
function pickTab(t: Tab) {
  tab.value = t
  tabTouched.value = true
  resetPopoverScroll()
  if (t === 'agent') void loadAgentWindow(activeWindow.value)
}

// Request billing remains the fact SSOT. Unknown is never API-paid; a runtime currently proven
// to be a subscription may show unknown historical rows only as API-equivalent value.
const currentSubscriptionRuntimes = computed<ReadonlySet<string>>(() => new Set(
  quotas.value.filter((quota) => quota.billing === 'subscription').map((quota) => quota.runtime),
))
const costProviders = computed<UsageProviderRow[]>(() => providersFor().filter((provider) =>
  usageMoneyPresentation(provider, currentSubscriptionRuntimes.value)?.tab === tab.value,
))
const costHeading = computed(() => (tab.value === 'sub' ? '用量 / ≈等价' : '用量 / 实付'))
// What KIND of money this row is. The badge is the whole point of the split, so it is rendered
// per row rather than implied by which tab you happen to be on.
function billingBadge(p: UsageProviderRow): { text: string; cls: string; title: string } {
  const placement = usageMoneyPresentation(p, currentSubscriptionRuntimes.value)
  if (placement?.semantics === 'api_paid') {
    return { text: '实付', cls: 'api', title: '按量付费 · 这是真实应付成本' }
  }
  return {
    text: '≈等价', cls: 'eq',
    title: placement?.evidence === 'current_subscription_fallback'
      ? '当前 runtime 为官方订阅；窗口用量按 API 价折算，仅作等价值，不是 API 账单，也不改写历史请求归属'
      : '包月实付；此为按 API 价折算的等价值，不是账单',
  }
}

// ── pill ─────────────────────────────────────────────────────────────────────────────────
const pct = computed(() => (tightest.value ? Math.round(tightest.value.window.remaining_percent) : null))
const level = computed(() => {
  const p = pct.value
  if (p === null) return 'none'
  if (p < 15) return 'crit'
  if (p < 40) return 'warn'
  return 'ok'
})
// API-only users have no quota % to show, so the pill carries today's REAL spend instead
// (「API ¥1.23」). Unknown cost → just 「API」. Never a fabricated percentage.
const todayApiCost = computed(() => {
  let total: number | null = null
  let currency = ''
  for (const provider of providersFor('24h')) {
    if (provider.billing_mode !== 'api' || typeof provider.cost !== 'number') continue
    if (currency && provider.currency !== currency) return null
    currency = provider.currency ?? ''
    total = (total ?? 0) + provider.cost
  }
  return total
})
const pillText = computed(() => {
  if (pct.value !== null) return `${pct.value}%`
  if (hasApi.value) {
    const c = todayApiCost.value
    const currency = providersFor('24h').find((p) => p.billing_mode === 'api' && typeof p.cost === 'number')?.currency
    return c === null ? 'API' : `API ${fmtCost(c, currency)}`
  }
  return '—' // present, but no reading we can stand behind — the popover explains why
})
const pillTitle = computed(() =>
  pct.value !== null ? '订阅额度剩余 · 点开明细' : hasApi.value ? 'API 计费 · 今日实付 · 点开明细' : '用量 · 点开明细',
)

// ── formatting ───────────────────────────────────────────────────────────────────────────
// EVERY time in this popover is an absolute wall clock. Never a countdown, never an age.
//
// A relative time ("27 分钟前", "2h 后重置") is a value that DECAYS: the moment it is painted
// it starts drifting from the truth, and keeping it honest costs a periodic re-render forever.
// We already shipped that bug once — the age was computed by the backend at fetch time and then
// frozen, so a tab left open kept insisting the reading was taken 「刚刚」. An instant cannot
// rot: "22:07" is as true an hour later as it was when painted, and it needs no clock at all.
//
// clockLabel renders one instant relative to today's date, so the common case stays short.
function clockLabel(at: Date, now: Date): string {
  const hhmm = `${String(at.getHours()).padStart(2, '0')}:${String(at.getMinutes()).padStart(2, '0')}`
  const days = Math.round(
    (new Date(at.getFullYear(), at.getMonth(), at.getDate()).getTime() -
      new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()) / 86_400_000,
  )
  if (days === 0) return hhmm
  if (days === 1) return `明天 ${hhmm}`
  if (days === -1) return `昨天 ${hhmm}`
  return `${at.getMonth() + 1}/${at.getDate()} ${hhmm}`
}

// When a quota window resets.
function fmtReset(iso?: string): string {
  if (!iso) return ''
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  const nowDate = new Date()
  if (at.getTime() < nowDate.getTime() - 120_000) return '已重置' // reading predates its own reset
  const label = clockLabel(at, nowDate)
  return label.length <= 5 ? `${label} 重置` : label // "23:08 重置" / "明天 02:20"
}

// When a reading was taken / when this client last got an answer. Both are instants, so both
// are shown as instants.
function fmtAt(iso?: string): string {
  if (!iso) return ''
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  return clockLabel(at, new Date())
}
// When THIS CLIENT last got an answer — a separate fact from when the runtime last reported.
const fetchedAtLabel = computed(() => (fetchedAt.value ? fmtAt(new Date(fetchedAt.value).toISOString()) : ''))
// Where a reading came from. Only worth naming when it changes what the user should EXPECT:
// a rollout reading moves only when the runtime writes one (so refreshing may well change
// nothing), whereas a probe is us having gone and asked. The hook is claude's only channel,
// so naming it would be noise.
function sourceLabel(source?: string): string {
  if (source === 'probe') return '实时查询'
  if (source === 'rollout') return '由 Codex 上报'
  return ''
}
// Spell out what the active family means, since its whole job is to explain a missing bar.
function familyHint(group: QuotaGroup): string {
  const kinds = (group.windows ?? []).map((w) => kindLabel(w.kind)).join(' + ')
  return kinds
    ? `独立额度组：${group.family}（${kinds}）。不同组分别计数，互不覆盖。`
    : `独立额度组：${group.family}`
}
// What the card says when it has no numbers — the honest alternative to disappearing.
function healthLabel(q: RuntimeQuota): string {
  if (q.health?.ok) return ''
  switch (q.health?.reason) {
    case 'not_executable': return 'CLI 不可用'
    case 'not_installed': return 'CLI 未安装'
    case 'version_check_failed': return 'CLI 无响应'
    default: return 'CLI 状态未知'
  }
}
function cacheHitRate(p: UsageProviderRow): string {
  const read = p.cache_read_tokens ?? 0
  const denom = read + (p.input_tokens ?? 0)
  if (denom <= 0) return '—'
  const raw = (read / denom) * 100
  // Show 100% ONLY when it is truly all-cache. With prompt caching, cache_read dwarfs fresh
  // input (3196M vs 1.79M → 99.94%) and Math.round would claim a perfection it doesn't have.
  const shown = raw >= 100 ? 100 : Math.min(99, Math.round(raw))
  return `${shown}%`
}

function fmtDuration(seconds?: number): string {
  if (typeof seconds !== 'number' || seconds < 0) return '—'
  const totalMinutes = Math.round(seconds / 60)
  if (totalMinutes < 1) return '<1m'
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  return hours ? `${hours}h${minutes ? `${minutes}m` : ''}` : `${minutes}m`
}

function fmtLatency(seconds?: number): string {
  if (typeof seconds !== 'number' || seconds < 0) return '—'
  if (seconds < 60) return `${seconds < 10 ? seconds.toFixed(1) : Math.round(seconds)}s`
  return fmtDuration(seconds)
}

function coverageText(key: string): string {
  const c = agentReport.value?.coverage?.[key]
  if (!c || c.state === 'missing') return key === 'outcome' ? '结果尚未验证' : '证据未采集'
  if (c.state === 'complete') return '证据完整'
  return typeof c.ratio === 'number' ? `覆盖 ${Math.round(c.ratio * 100)}%` : '证据部分覆盖'
}

const agentArtifactKnown = computed(() => {
  const c = agentReport.value?.coverage?.artifacts
  return c?.state === 'complete' || c?.state === 'partial'
})

watch(activeWindow, (window) => {
  if (tab.value === 'agent') void loadAgentWindow(window)
})

function resetPopoverScroll(): void {
  void nextTick(() => {
    if (popRef.value) popRef.value.scrollTop = 0
    schedulePlacement()
  })
}

async function loadAgentWindow(window: ReportWindow, force = false): Promise<void> {
  // Reset both before and after replacement. The second reset defeats browser
  // scroll anchoring when a short loading body is replaced by a tall cached body.
  resetPopoverScroll()
  await agentByWindow[window].load(window, force)
  if (tab.value === 'agent' && activeWindow.value === window) resetPopoverScroll()
}

function prefetchAll(): void {
  // Fire all four in parallel; each composable instance keeps its own `report` ref so there's
  // no cross-window clobbering. `report.value` isn't cleared before the fetch resolves, so the
  // switcher shows the last-good cache instantly instead of flashing "loading" on reopen.
  void rep24h.load('24h')
  void rep7d.load('7d')
  void rep14d.load('14d')
  void rep30d.load('30d')
}

// Opening the popover re-reads what the runtimes have written. Cheap, no provider request.
function reload(): void {
  void load()
  prefetchAll()
  void loadAgentWindow(activeWindow.value)
}

function refreshAgent(): void {
  void loadAgentWindow(activeWindow.value, true)
}

// The ⟳ button goes further: it ASKS the provider. Re-reading the disk cannot always help —
// codex only records the limit family of the model it is currently running, so while a session
// works on a per-model plan the ACCOUNT limit stops being written, and its newest reading can
// be hours old no matter how often you poll. That is why 刷新 used to move nothing.
// One real provider request, only ever on this click.
function refresh(): void {
  void probe()
  prefetchAll()
}

function gotoFullReport(): void {
  open.value = false
  if (tab.value === 'agent') {
    agentDetailOpen.value = true
    return
  }
  emit('detail')
}

function viewportRect(): RectLike {
  const vv = window.visualViewport
  const left = vv?.offsetLeft ?? 0
  const top = vv?.offsetTop ?? 0
  const width = vv?.width ?? window.innerWidth
  const height = vv?.height ?? window.innerHeight
  return { left, top, width, height, right: left + width, bottom: top + height }
}

function updatePlacement(): void {
  placementFrame = 0
  if (!open.value) return
  const anchor = wrapRef.value?.getBoundingClientRect()
  const popover = popRef.value?.getBoundingClientRect()
  if (!anchor || !popover) return
  popPos.value = placeAnchoredPopover(anchor, popover, viewportRect())
}
function schedulePlacement(): void {
  if (placementFrame) return
  placementFrame = window.requestAnimationFrame(updatePlacement)
}
function installPlacementWatchers(): void {
  window.addEventListener('resize', schedulePlacement)
  window.addEventListener('scroll', schedulePlacement, true)
  window.visualViewport?.addEventListener('resize', schedulePlacement)
  window.visualViewport?.addEventListener('scroll', schedulePlacement)
  placementObserver = new ResizeObserver(schedulePlacement)
  if (wrapRef.value) placementObserver.observe(wrapRef.value)
  // Content growth must not move the shell. viewport/anchor changes still
  // schedule placement; the popover keeps one stable side while it is open.
}
function removePlacementWatchers(): void {
  window.removeEventListener('resize', schedulePlacement)
  window.removeEventListener('scroll', schedulePlacement, true)
  window.visualViewport?.removeEventListener('resize', schedulePlacement)
  window.visualViewport?.removeEventListener('scroll', schedulePlacement)
  placementObserver?.disconnect()
  placementObserver = null
  if (placementFrame) window.cancelAnimationFrame(placementFrame)
  placementFrame = 0
}

function toggle() {
  open.value = !open.value
  if (!open.value) return
  reload()
}

watch(open, async (isOpen) => {
  removePlacementWatchers()
  if (!isOpen) return
  await nextTick()
  installPlacementWatchers()
  schedulePlacement()
})

// Coming back to the page is the moment the numbers matter and the moment they are most likely
// stale: a backgrounded tab has its timers throttled (and on mobile, suspended outright), so the
// 60s poll simply does not run while you are away. Re-ask on the way back in.
function onVisible(): void {
  if (document.visibilityState !== 'visible') return
  void load()
  if (open.value) prefetchAll()
}

// The chip renders INLINE (no fixed overlay, no teleport) so its host decides placement — it
// never floats over / blocks the right-panel controls (⤢ / ?), which a position:fixed one did.
onMounted(() => {
  void load()
  // Independent prefetch: Agent activity must remain discoverable even when no
  // provider quota account is present, and cannot delay either money tab.
  void agent24h.load('24h')
  // The pill can show today's API spend, so an API-only user needs the report before opening.
  void rep24h.load('24h')
  // The ONLY periodic work left. Every rendered time is an absolute instant, so nothing decays
  // between polls and no second timer is needed to keep the text honest.
  timer = setInterval(() => void load(), 60000)
  document.addEventListener('visibilitychange', onVisible)
  window.addEventListener('focus', onVisible)
})
onUnmounted(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', onVisible)
  window.removeEventListener('focus', onVisible)
  removePlacementWatchers()
})
</script>

<template>
  <!-- Navigation is stable chrome: async data may change its label/content, never its presence. -->
  <div ref="wrapRef" class="uchip-wrap">
    <button
      class="uchip"
      :class="'lvl-' + level"
      type="button"
      :title="pillTitle"
      aria-haspopup="dialog"
      :aria-expanded="open"
      @click.stop="toggle"
    >
      <Gauge :size="12" class="uchip-ic" />
      <span class="uchip-pct">{{ pillText }}</span>
    </button>
  </div>

  <Teleport to="body">
    <template v-if="open">
      <div class="uchip-backdrop" @click="open = false" />
      <div
        ref="popRef"
        class="uchip-pop"
        :style="{ top: popPos.top + 'px', left: popPos.left + 'px', maxHeight: popPos.maxHeight + 'px' }"
        @click.stop
      >
        <div class="uchip-tabs" role="tablist">
          <button type="button" role="tab" :aria-selected="tab === 'sub'" :class="{ on: tab === 'sub' }" @click="pickTab('sub')">官方订阅</button>
          <button type="button" role="tab" :aria-selected="tab === 'api'" :class="{ on: tab === 'api' }" @click="pickTab('api')">API 计费</button>
          <button type="button" role="tab" :aria-selected="tab === 'agent'" :class="{ on: tab === 'agent' }" @click="pickTab('agent')">Agent 效能</button>
        </div>

        <!-- ── 官方订阅 tab 独有：额度条 / 重置 / 新鲜度（API 计费没有额度窗口，不伪造）── -->
        <template v-if="tab === 'sub'">
          <div v-for="q in subscriptions" :key="q.runtime" class="uchip-rt">
            <div class="uchip-rt-head">
              <span>{{ runtimeLabel(q.runtime) }}</span>
              <span v-if="q.plan" class="uchip-plan">{{ q.plan }}</span>
              <span v-if="healthLabel(q)" class="uchip-badge warn" :title="q.health?.reason">{{ healthLabel(q) }}</span>
            </div>

            <div
              v-for="(group, groupIndex) in quotaGroupsFor(q)"
              :key="group.family || group.snapshot?.captured_at || groupIndex"
              class="uchip-group"
              :class="{ 'is-stale': group.snapshot?.stale }"
            >
              <div v-if="group.family || group.snapshot?.stale" class="uchip-group-head">
                <span v-if="group.family" class="uchip-plan uchip-family" :title="familyHint(group)">{{ group.family }}</span>
                <span v-if="group.snapshot?.stale" class="uchip-badge stale">数据已过期</span>
              </div>
              <!-- key by INDEX: duplicate kinds are a domain contradiction; the backend drops
                   them before they reach this list. -->
              <div v-for="(w, i) in group.windows" :key="i" class="uchip-win">
                <span class="uchip-win-k">{{ kindLabel(w.kind) }}</span>
                <span class="uchip-bar"><span class="uchip-bar-fill" :style="{ width: w.remaining_percent + '%' }" :class="'lvl-' + (w.remaining_percent < 15 ? 'crit' : w.remaining_percent < 40 ? 'warn' : 'ok')" /></span>
                <span class="uchip-win-p">{{ Math.round(w.remaining_percent) }}%</span>
                <span v-if="w.inferred" class="uchip-win-r uchip-inferred" title="窗口已重置，且此后运行时未上报任何用量 ⟹ 未使用。此值为推断，非实测。">已重置 · 推断</span>
                <span v-else class="uchip-win-r">{{ fmtReset(w.reset_at) }}</span>
              </div>
              <div v-if="group.snapshot" class="uchip-dim uchip-note">
                额度更新于 {{ fmtAt(group.snapshot.captured_at) }}
                <span v-if="sourceLabel(group.snapshot.source)" class="uchip-src">· {{ sourceLabel(group.snapshot.source) }}</span>
              </div>
            </div>

            <!-- No reading at all: say so plainly. Never a fabricated 0%/100% bar. -->
            <div v-if="!quotaGroupsFor(q).length" class="uchip-dim uchip-note">{{ q.note || '暂无额度数据' }}</div>
          </div>
          <div v-if="!subscriptions.length" class="uchip-dim uchip-empty">未检出官方订阅账号</div>
          <div class="uchip-sep" />
        </template>

        <!-- ── 用量 / 花费：订阅/API 共用数据，但钱的性质不同 ──── -->
        <template v-if="tab !== 'agent'">
        <div class="uchip-costhead">
          <span>{{ costHeading }}</span>
          <button v-if="showDetail" type="button" class="uchip-detail" @click="gotoFullReport">详细报表 ›</button>
        </div>
        <div class="uchip-winseg" role="tablist">
          <button
            v-for="w in WINDOWS"
            :key="w.key"
            type="button"
            role="tab"
            :aria-selected="w.key === activeWindow"
            :class="{ on: w.key === activeWindow }"
            @click="activeWindow = w.key"
          >{{ w.label }}</button>
        </div>

        <div v-if="activeLoading && !activeReport" class="uchip-dim uchip-loading">加载中…</div>
        <template v-else-if="activeReport?.available">
          <div v-for="provider in costProviders" :key="`${provider.runtime}:${provider.billing_mode}`" class="uchip-prov">
            <div class="uchip-prov-head">
              <span class="uchip-prov-name">{{ runtimeLabel(provider.runtime) }}</span>
              <span class="uchip-badge" :class="billingBadge(provider).cls" :title="billingBadge(provider).title">{{ billingBadge(provider).text }}</span>
            </div>
            <div class="uchip-prov-glance">
              <span class="uchip-prov-cost">{{ fmtCost(provider.cost, provider.currency, costApprox) }}</span>
              <span class="uchip-prov-tok">{{ fmtTokens(provider.total_tokens) }} tok</span>
              <span class="uchip-prov-hit">缓存命中 {{ cacheHitRate(provider) }}</span>
            </div>
            <div class="uchip-prov-bd">
              <span>↓{{ fmtTokens(provider.input_tokens) }} / ↑{{ fmtTokens(provider.output_tokens) }}</span>
              <span>缓存读{{ fmtTokens(provider.cache_read_tokens) }} / 写{{ fmtTokens(provider.cache_create_tokens) }}</span>
            </div>
            <div class="uchip-prov-ctx">
              <span class="uchip-prov-model">{{ provider.top_model || '—' }}</span>
              <Spark :bars="provider.spark ?? []" />
            </div>
          </div>
          <div v-if="!costProviders.length" class="uchip-dim uchip-empty">
            {{ tab === 'sub' ? '该窗口暂无订阅用量' : '窗口内无可证明的 API 实付费用' }}
          </div>
          <div v-else-if="tab === 'api'" class="uchip-foot">只统计逐请求明确为 API 的真实实付；订阅与未分类用量不计入。</div>
          <div v-else class="uchip-foot">按 API 价折算订阅窗口用量，仅表示等价值，不是 API 账单。</div>
        </template>
        <div v-else class="uchip-dim uchip-loading">{{ activeReport?.reason || '用量数据不可用' }}</div>
        </template>

        <!-- ── Agent 效能：结果门优先，活动量明确不冒充能力 ── -->
        <template v-else>
          <div class="uchip-costhead">
            <span>{{ activeWindow === '24h' ? '今日 Agent 效能' : `${WINDOWS.find(w => w.key === activeWindow)?.label} Agent 效能` }}</span>
            <button type="button" class="uchip-detail" @click="gotoFullReport">详细报表 ›</button>
          </div>
          <div class="uchip-winseg" role="tablist" aria-label="Agent 效能时间范围">
            <button v-for="w in WINDOWS" :key="w.key" type="button" role="tab" :aria-selected="w.key === activeWindow" :class="{ on: w.key === activeWindow }" @click="activeWindow = w.key">{{ w.label }}</button>
          </div>

          <div v-if="agentLoading && !agentReport" class="uchip-agent-skeleton" aria-label="Agent 效能加载中">
            <span /><span /><span />
          </div>
          <div v-else-if="agentError && !agentReport" class="uchip-agent-empty">{{ agentError }}</div>
          <template v-else-if="agentReport">
            <div class="uchip-health" :class="`is-${agentReport.health.state}`">
              <strong>{{ agentReport.health.label }}</strong>
              <span>{{ agentReport.health.headline }}</span>
            </div>
            <div class="uchip-health-axes" aria-label="Agent 健康维度">
              <div v-for="axis in agentReport.health.axes" :key="axis.key" :class="`is-${axis.state}`" :title="axis.headline">
                <i aria-hidden="true" />
                <span>{{ axis.label }}</span>
                <small>{{ axis.headline }}</small>
              </div>
            </div>
            <div class="uchip-agent-status" :class="{ warn: agentReport.summary.interrupted + agentReport.summary.errors > 0 }">
              <strong>{{ agentReport.summary.completed }}/{{ agentReport.summary.work_items }} 已完成</strong>
              <span v-if="agentReport.summary.interrupted">· {{ agentReport.summary.interrupted }} 中断</span>
              <span v-if="agentReport.summary.errors">· {{ agentReport.summary.errors }} 错误</span>
              <span v-if="agentReport.summary.never_started">· {{ agentReport.summary.never_started }} 未启动</span>
              <span v-if="agentReport.summary.open">· {{ agentReport.summary.open }} 未闭合</span>
            </div>
            <div class="uchip-agent-proof">
              <span v-if="agentReport.summary.verified_pass">{{ agentReport.summary.verified_pass }} 项已验证</span>
              <span v-else>完成 ≠ 验收 · {{ coverageText('outcome') }}</span>
            </div>

            <div class="uchip-agent-time">
              <div><span>墙钟耗时</span><strong>{{ fmtDuration(agentReport.summary.wall_seconds) }}</strong></div>
              <div><span>Agent 累计</span><strong>{{ fmtDuration(agentReport.summary.cumulative_seconds) }}</strong></div>
              <div><span>平均并发</span><strong>{{ agentReport.summary.average_concurrency?.toFixed(2) ?? '—' }}×</strong></div>
            </div>

            <div class="uchip-agent-counts" aria-label="不同统计口径">
              <span><b>{{ agentReport.summary.work_items }}</b> 任务</span>
              <span><b>{{ agentReport.summary.agent_instances }}</b> Agent</span>
              <span><b>{{ agentReport.summary.agent_assignments }}</b> 调度</span>
              <span><b>{{ agentReport.summary.model_requests }}</b> 模型请求</span>
            </div>
            <div v-if="agentReport.summary.delegated_lifecycle.submitted" class="uchip-agent-dispatch">
              子 Agent 调度 {{ agentReport.summary.delegated_lifecycle.submitted }} 次 · {{ agentReport.summary.delegated_lifecycle.started }} 启动 · {{ agentReport.summary.delegated_lifecycle.completed }} 完成
              <span v-if="agentReport.summary.delegated_lifecycle.interrupted">· {{ agentReport.summary.delegated_lifecycle.interrupted }} 中断</span>
              <span v-if="agentReport.summary.delegated_lifecycle.errors">· {{ agentReport.summary.delegated_lifecycle.errors }} 错误</span>
              <span v-if="agentReport.summary.delegated_lifecycle.never_started">· {{ agentReport.summary.delegated_lifecycle.never_started }} 未启动</span>
            </div>

            <div v-for="runtime in agentReport.runtime_profiles" :key="runtime.runtime" class="uchip-agent-runtime">
              <div class="uchip-agent-runtime-head">
                <strong>{{ runtimeLabel(runtime.runtime) }}</strong>
                <span>{{ runtime.completed }}/{{ runtime.work_items }} 完成</span>
                <span v-if="runtime.interrupted" class="is-bad">{{ runtime.interrupted }} 中断</span>
                <span v-if="runtime.errors" class="is-bad">{{ runtime.errors }} 错误</span>
              </div>
              <div class="uchip-agent-runtime-metrics">
                <span>{{ fmtDuration(runtime.active_seconds) }} active</span>
                <span>{{ fmtTokens(runtime.output_tokens) }} 输出 tok</span>
                <span>{{ runtime.observed_response_tokens_per_second ? `观察吞吐 ${runtime.observed_response_tokens_per_second.toFixed(1)} tok/s` : '观察吞吐证据不足' }}</span>
                <span v-if="runtime.generation_tokens_per_second">纯生成 {{ runtime.generation_tokens_per_second.toFixed(1) }} tok/s</span>
                <span :title="`transcript 因果输入→首个 assistant 事件；覆盖 ${runtime.first_response_coverage.observed_n}/${runtime.first_response_coverage.eligible_n}`">首响应 {{ fmtLatency(runtime.observed_first_response_median_seconds) }}</span>
                <span v-if="runtime.ttft_median_seconds !== undefined" :title="`provider 请求开始→首 token；覆盖 ${runtime.ttft_coverage.observed_n}/${runtime.ttft_coverage.eligible_n}`">TTFT {{ fmtLatency(runtime.ttft_median_seconds) }}</span>
                <span>工具 {{ runtime.tools.calls }} 次 · 均 {{ fmtLatency(runtime.tools.average_duration_seconds) }}<template v-if="runtime.tools.open + runtime.tools.unknown"> · {{ runtime.tools.open }} 运行中 / {{ runtime.tools.unknown }} 未知</template></span>
              </div>
              <div class="uchip-agent-runtime-output">
                <div class="uchip-agent-subtitle">可证产出 · 覆盖 {{ runtime.artifact_coverage.observed_n }}/{{ runtime.artifact_coverage.eligible_n }}</div>
                <template v-if="runtime.artifacts.events">
                  <span>代码 {{ runtime.artifacts.by_kind.code?.files ?? 0 }} 文件（新 {{ runtime.artifacts.by_kind.code?.created_files ?? 0 }} / 改 {{ runtime.artifacts.by_kind.code?.modified_files ?? 0 }}）· 写 {{ runtime.artifacts.by_kind.code?.written_lines ?? 0 }} 行 · +{{ runtime.artifacts.by_kind.code?.additions ?? 0 }}/−{{ runtime.artifacts.by_kind.code?.deletions ?? 0 }}</span>
                  <span>文档 {{ runtime.artifacts.by_kind.doc?.files ?? 0 }} 文件（新 {{ runtime.artifacts.by_kind.doc?.created_files ?? 0 }} / 改 {{ runtime.artifacts.by_kind.doc?.modified_files ?? 0 }}）· 写 {{ runtime.artifacts.by_kind.doc?.written_lines ?? 0 }} 行 · +{{ runtime.artifacts.by_kind.doc?.additions ?? 0 }}/−{{ runtime.artifacts.by_kind.doc?.deletions ?? 0 }}</span>
                </template>
                <span v-else class="uchip-dim">暂无可证 Edit/Write 产物，不从 worktree 总 diff 猜归属</span>
                <div class="uchip-agent-yield" title="同一 runtime、同一窗口的观察比率；用于资源审计，不是任务或模型能力分">
                  <span>模型输出 <b>{{ fmtTokens(runtime.resource_yield.request_output_tokens) }} tok</b> <small>覆盖 {{ runtime.resource_yield.token_coverage.observed_n }}/{{ runtime.resource_yield.token_coverage.eligible_n }}</small></span>
                  <span>产出密度 <b>{{ runtime.resource_yield.written_lines_per_thousand_output_tokens !== undefined ? `${runtime.resource_yield.token_coverage.state === 'complete' ? '' : '≈'}${runtime.resource_yield.written_lines_per_thousand_output_tokens.toFixed(1)} 行/1K tok` : '—' }}</b></span>
                  <span>产出节奏 <b>{{ runtime.resource_yield.written_lines_per_active_hour !== undefined ? `${runtime.resource_yield.written_lines_per_active_hour.toFixed(1)} 行/active h` : '—' }}</b></span>
                  <span>等价成本 <b v-if="runtime.resource_yield.api_equivalent_per_thousand_written_lines">{{ runtime.resource_yield.cost_coverage.state === 'complete' ? '' : '≥' }}{{ fmtCost(runtime.resource_yield.api_equivalent_per_thousand_written_lines.amount, runtime.resource_yield.api_equivalent_per_thousand_written_lines.currency) }}/千行</b><b v-else>价格覆盖 {{ runtime.resource_yield.cost_coverage.observed_n }}/{{ runtime.resource_yield.cost_coverage.eligible_n }}</b></span>
                </div>
              </div>
              <div v-if="agentReport.top_cost_models[runtime.runtime]?.length" class="uchip-agent-models">
                <div class="uchip-agent-subtitle">成本构成 · Top 3 已知模型 · 未知另列</div>
                <div v-for="model in agentReport.top_cost_models[runtime.runtime]" :key="model.model" class="uchip-agent-model">
                  <span class="model-name">{{ model.model || '未知模型' }}</span>
                  <span>{{ model.request_n }} 请求</span>
                  <span>{{ model.cost ? fmtCost(model.cost.amount, model.cost.currency) : '价格缺失' }}</span>
                  <small class="model-speed" :title="`同模型可用输出 ${fmtTokens(model.observed_response_output_tokens)} tok / ${model.observed_response_duration_seconds.toFixed(1)}s；覆盖 ${model.response_speed_coverage.observed_n}/${model.response_speed_coverage.eligible_n}`">
                    {{ model.observed_response_tokens_per_second !== undefined ? `观察吞吐 ${model.observed_response_tokens_per_second.toFixed(1)} tok/s` : '观察吞吐证据不足' }} · 覆盖 {{ model.response_speed_coverage.observed_n }}/{{ model.response_speed_coverage.eligible_n }}
                  </small>
                  <small>{{ [...(model.efforts ?? []), ...(model.service_tiers ?? [])].join(' · ') || '配置未采集' }}</small>
                  <small v-if="model.credits !== undefined">额度折算 {{ fmtCredits(model.credits) }} credits<span v-if="model.fast_multipliers?.length"> · Fast {{ model.fast_multipliers.map(value => `${value}×`).join('/') }}</span></small>
                </div>
              </div>
            </div>
            <div class="uchip-foot">首响应来自 transcript 观察；TTFT 仅在 provider 提供首 token 证据时显示，二者不混算。</div>

            <div class="uchip-agent-artifacts">
              <div class="uchip-agent-subtitle">窗口总产出</div>
              <template v-if="agentArtifactKnown">
                <span>代码 {{ agentReport.artifacts.by_kind.code?.files ?? 0 }} 文件 · 写 {{ agentReport.artifacts.by_kind.code?.written_lines ?? 0 }} 行 · +{{ agentReport.artifacts.by_kind.code?.additions ?? 0 }}/−{{ agentReport.artifacts.by_kind.code?.deletions ?? 0 }}</span>
                <span>文档 {{ agentReport.artifacts.by_kind.doc?.files ?? 0 }} 文件 · 写 {{ agentReport.artifacts.by_kind.doc?.written_lines ?? 0 }} 行 · +{{ agentReport.artifacts.by_kind.doc?.additions ?? 0 }}/−{{ agentReport.artifacts.by_kind.doc?.deletions ?? 0 }}</span>
                <span v-if="agentReport.artifacts.unattributed.files">未归因 {{ agentReport.artifacts.unattributed.files }} 文件</span>
              </template>
              <span v-else class="uchip-dim">产物归因未采集，不用 worktree 总 diff 猜功劳</span>
            </div>

            <div class="uchip-agent-comparison">
              <strong>Runtime 建议</strong>
              <span v-if="agentReport.comparisons.length">{{ agentReport.comparisons[0]?.recommendation || '存在权衡，展开查看证据' }}</span>
              <span v-else>任务结构或样本证据不足，暂不横比</span>
            </div>
            <div class="uchip-foot">产出量是可证活动，不等于能力；任务结构或验收不同不横比 · {{ agentReport.health.policy_version }} 透明规则</div>
          </template>
          <div v-else class="uchip-agent-empty">暂无可观测的 Agent 活动</div>
        </template>

        <div v-if="probeNote" class="uchip-probenote">{{ probeNote }}</div>
        <div class="uchip-refresh">
          <span class="uchip-dim">{{ tab === 'agent' ? (agentReport ? `生成于 ${fmtAt(agentReport.generated_at)}` : '') : (fetchedAtLabel ? `拉取于 ${fetchedAtLabel}` : '') }}</span>
          <button
            type="button"
            :disabled="tab === 'agent' ? agentLoading : loading"
            :title="tab === 'agent' ? '重新读取本地 Agent 事实' : '向 Codex 实时查询账号额度（一次真实请求）。Claude 的额度只能由它自己上报，无法主动查询。'"
            @click="tab === 'agent' ? refreshAgent() : refresh()"
          >{{ (tab === 'agent' ? agentLoading : loading) ? '查询中…' : '⟳ 刷新' }}</button>
        </div>
      </div>
    </template>
  </Teleport>
  <AgentReportDetail :open="agentDetailOpen" :initial-window="activeWindow" @close="agentDetailOpen = false" />
</template>

<style scoped>
/* Inline in the terminal tab row (host places it) — flows with the tabs, never floats over or
   blocks the right-panel controls. Popover anchors to it. */
.uchip-wrap { display: inline-flex; position: relative; flex-shrink: 0; }
.uchip {
  display: inline-flex; align-items: center; gap: 4px; padding: 2px 7px; border-radius: 9px;
  border: 1px solid var(--border, #2a2d35); background: rgba(255,255,255,0.04);
  color: var(--fg, #cbd0d8); cursor: pointer; font-size: 11px; font-variant-numeric: tabular-nums;
}
.uchip:hover { background: rgba(255,255,255,0.08); }
.uchip-ic { flex-shrink: 0; }
.uchip.lvl-ok .uchip-ic { color: #22c55e; }
.uchip.lvl-warn .uchip-ic { color: #f59e0b; }
.uchip.lvl-crit .uchip-ic { color: #ef4444; }
.uchip.lvl-crit { color: #f4b0b0; border-color: #5a2a2a; }
.uchip.lvl-none .uchip-ic { color: #8b909a; }
.uchip-pct { font-weight: 600; }

.uchip-backdrop { position: fixed; inset: 0; z-index: 3000; }
.uchip-pop {
  position: fixed; z-index: 3001;
  width: min(340px, calc(100vw - 16px)); max-height: min(72vh, 560px); overflow-y: auto;
  overflow-anchor: none;
  box-sizing: border-box; padding: 10px 12px; border-radius: 10px;
  background: #16181d; border: 1px solid #2a2d35; box-shadow: 0 10px 30px rgba(0,0,0,0.5); color: #e6e8ec;
}

/* ── subscription, API money, and Agent evidence are separate contexts ─────── */
.uchip-tabs {
  display: flex; width: 100%; background: #1b1e24; border: 1px solid #2a2d35;
  position: sticky; top: -10px; z-index: 2;
  border-radius: 7px; padding: 2px; gap: 2px; margin-bottom: 10px;
  box-shadow: 0 8px 10px #16181d;
}
.uchip-tabs button {
  flex: 1; padding: 4px 0; border-radius: 5px; border: none; background: none;
  color: #8b909a; font-size: 11px; cursor: pointer; font-family: inherit;
}
.uchip-tabs button.on { background: #2a2d35; color: #e6e8ec; font-weight: 600; }

.uchip-rt { margin-bottom: 10px; }
.uchip-rt-head { display: flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 600; margin-bottom: 4px; }
.uchip-group { padding: 4px 0 5px; }
.uchip-group + .uchip-group { border-top: 1px dashed #2a2d35; }
.uchip-group.is-stale { opacity: 0.72; }
.uchip-group-head { display: flex; align-items: center; gap: 6px; min-height: 17px; margin-bottom: 1px; }
.uchip-plan { font-size: 10px; color: #8b909a; font-weight: 400; }
.uchip-badge { font-size: 9.5px; font-weight: 500; border-radius: 4px; padding: 1px 5px; margin-left: auto; }
.uchip-badge + .uchip-badge { margin-left: 4px; }
/* Amber, never red: red reads as "quota exhausted", which is a different (and false) claim. */
.uchip-badge.warn { color: #f59e0b; background: rgba(245,158,11,0.12); }
.uchip-badge.stale { color: #9aa0aa; background: rgba(154,160,170,0.12); }
.uchip-badge.api { color: #4ade80; background: rgba(74,222,128,0.12); }
.uchip-badge.unknown { color: #9aa0aa; background: rgba(154,160,170,0.12); }

.uchip-win { display: flex; align-items: center; gap: 6px; font-size: 11px; color: #c9cdd5; margin: 2px 0; }
.uchip-win-k { width: 40px; flex-shrink: 0; color: #8b909a; }
.uchip-src { color: #6f757f; }
.uchip-bar { flex: 1; height: 5px; border-radius: 3px; background: #262a32; overflow: hidden; }
.uchip-bar-fill { display: block; height: 100%; border-radius: 3px; }
.uchip-bar-fill.lvl-ok { background: #22c55e; }
.uchip-bar-fill.lvl-warn { background: #f59e0b; }
.uchip-bar-fill.lvl-crit { background: #ef4444; }
.uchip-win-p { width: 34px; text-align: right; font-variant-numeric: tabular-nums; }
.uchip-win-r { width: 84px; text-align: right; color: #7f858f; font-size: 10px; white-space: nowrap; }
/* An expired window shows no quantity at all — a bar would be a claim we cannot make. */
.uchip-inferred { color: #6f757f; font-style: italic; }
.uchip-family { color: #7aa2f7; opacity: 0.85; cursor: help; }
.uchip-probenote { margin-top: 8px; font-size: 10px; color: #f59e0b; }

.uchip-note { font-size: 10.5px; margin-top: 3px; }
.uchip-empty { font-size: 11px; padding: 6px 0; }
.uchip-dim { color: #7f858f; }
.uchip-loading { font-size: 11px; padding: 4px 0; }
.uchip-eqline { display: flex; align-items: center; gap: 6px; font-size: 10.5px; color: #c9cdd5; margin-top: 5px; }
.uchip-eq {
  font-size: 9.5px; color: #f59e0b; background: rgba(245,158,11,0.12);
  border-radius: 4px; padding: 1px 5px; cursor: help;
}
.uchip-foot { margin-top: 8px; font-size: 9.5px; color: #6f757f; line-height: 1.4; }
.uchip-sep { height: 1px; background: #23262d; margin: 8px 0; }

/* Refresh row: says WHEN this client last got an answer (a different fact from when the runtime
   last reported) and lets you go ask again. */
.uchip-refresh {
  display: flex; align-items: center; justify-content: space-between;
  margin-top: 10px; padding-top: 8px; border-top: 1px solid #21242b; font-size: 10px;
}
.uchip-refresh button {
  background: none; border: 1px solid #2a2d35; border-radius: 6px; padding: 3px 8px;
  color: #9aa0aa; font-size: 10px; cursor: pointer; font-family: inherit;
}
.uchip-refresh button:hover:not(:disabled) { color: #e6e8ec; border-color: #3a3f49; }
.uchip-refresh button:disabled { opacity: 0.5; cursor: default; }

/* ── 花费区 header + window switcher ─────────────────────────────────────────── */
.uchip-costhead { display: flex; align-items: center; justify-content: space-between; margin-bottom: 6px; }
.uchip-costhead > span:first-child { font-size: 12px; color: #9aa0aa; }
.uchip-detail {
  background: none; border: none; padding: 0; cursor: pointer;
  font-size: 10.5px; color: #7f9cf5; font-family: inherit;
}
.uchip-detail:hover { color: #a5b8ff; text-decoration: underline; }
.uchip-winseg {
  display: inline-flex; width: 100%; background: #1b1e24; border: 1px solid #2a2d35;
  border-radius: 7px; padding: 2px; gap: 2px; margin-bottom: 8px;
}
.uchip-winseg button {
  flex: 1; padding: 3px 0; border-radius: 5px; border: none; background: none;
  color: #8b909a; font-size: 10.5px; cursor: pointer; font-family: inherit;
}
.uchip-winseg button.on { background: #2a2d35; color: #e6e8ec; font-weight: 600; }

/* ── per-runtime row: glance (big) → breakdown (compact) → context (small) ───── */
.uchip-prov { padding: 7px 0; border-top: 1px solid #21242b; }
.uchip-prov:first-of-type { border-top: none; }
.uchip-prov-head { display: flex; align-items: center; gap: 6px; margin-bottom: 3px; }
.uchip-prov-name { font-size: 11.5px; font-weight: 600; color: #e6e8ec; }
.uchip-prov-glance { display: flex; align-items: baseline; gap: 8px; margin-bottom: 3px; }
.uchip-prov-cost { font-size: 14px; font-weight: 700; color: #e6e8ec; font-variant-numeric: tabular-nums; }
.uchip-prov-tok { font-size: 10.5px; color: #9aa0aa; }
.uchip-prov-hit { font-size: 10.5px; color: #4ade80; margin-left: auto; }
.uchip-prov-bd {
  display: flex; justify-content: space-between; font-size: 10px; color: #8b909a;
  font-variant-numeric: tabular-nums; margin-bottom: 4px;
}
.uchip-prov-ctx { display: flex; align-items: center; justify-content: space-between; gap: 6px; }
.uchip-prov-model {
  font-size: 9.5px; color: #7f858f; overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; max-width: 140px;
}

/* ── Agent 效能: status → time → runtime → evidence, no decorative score ── */
.uchip-agent-status { font-size: 12px; color: #cbd5e1; padding: 2px 0 1px; }
.uchip-agent-status strong { color: #4ade80; }
.uchip-agent-status.warn strong { color: #fbbf24; }
.uchip-agent-proof { font-size: 10px; color: #7f858f; margin-bottom: 8px; }
.uchip-health { display: grid; grid-template-columns: auto minmax(0,1fr); align-items: center; gap: 7px; margin: 2px 0 7px; padding: 8px 9px; border: 1px solid #343842; border-radius: 8px; background: #1d2027; }
.uchip-health strong { padding: 2px 7px; border-radius: 999px; background: rgba(148,163,184,.12); color: #cbd5e1; font-size: 12px; white-space: nowrap; }
.uchip-health span { color: #aeb4bf; font-size: 10px; line-height: 1.35; }
.uchip-health.is-healthy { border-color: rgba(74,222,128,.28); }.uchip-health.is-healthy strong { color: #4ade80; background: rgba(74,222,128,.10); }
.uchip-health.is-attention { border-color: rgba(251,191,36,.32); }.uchip-health.is-attention strong { color: #fbbf24; background: rgba(251,191,36,.10); }
.uchip-health.is-critical { border-color: rgba(248,113,113,.36); }.uchip-health.is-critical strong { color: #f87171; background: rgba(248,113,113,.11); }
.uchip-health.is-insufficient_evidence strong { color: #9ca3af; }
.uchip-health-axes { display: grid; gap: 3px; margin-bottom: 8px; }
.uchip-health-axes > div { display: grid; grid-template-columns: 8px 32px minmax(0,1fr); align-items: center; gap: 5px; min-width: 0; font-size: 9.5px; }
.uchip-health-axes i { width: 6px; height: 6px; border-radius: 50%; background: #6b7280; }
.uchip-health-axes span { color: #c4c8d0; }.uchip-health-axes small { overflow: hidden; color: #777e89; text-overflow: ellipsis; white-space: nowrap; }
.uchip-health-axes .is-healthy i { background: #4ade80; }.uchip-health-axes .is-attention i { background: #fbbf24; }.uchip-health-axes .is-critical i { background: #f87171; }
.uchip-agent-time {
  display: grid; grid-template-columns: repeat(3, 1fr); gap: 5px; margin-bottom: 7px;
}
.uchip-agent-time > div {
  min-width: 0; padding: 7px 6px; border: 1px solid #262a32; border-radius: 7px; background: #1b1e24;
}
.uchip-agent-time span { display: block; font-size: 9px; color: #7f858f; white-space: nowrap; }
.uchip-agent-time strong { display: block; margin-top: 2px; font-size: 13px; font-variant-numeric: tabular-nums; }
.uchip-agent-counts {
  display: grid; grid-template-columns: repeat(4, 1fr); gap: 3px; padding: 5px 0 7px;
  border-bottom: 1px solid #21242b; color: #7f858f; font-size: 9px; text-align: center;
}
.uchip-agent-counts b { display: block; color: #c9cdd5; font-size: 11px; font-variant-numeric: tabular-nums; }
.uchip-agent-dispatch { padding: 5px 0; color: #707782; font-size: 9px; border-bottom: 1px solid #21242b; }
.uchip-agent-runtime { padding: 8px 0; border-bottom: 1px solid #21242b; }
.uchip-agent-runtime-head { display: flex; align-items: baseline; gap: 7px; font-size: 10px; color: #8b909a; }
.uchip-agent-runtime-head strong { color: #e6e8ec; font-size: 11.5px; }
.uchip-agent-runtime-head .is-bad { margin-left: auto; color: #f87171; }
.uchip-agent-runtime-metrics { display: flex; flex-wrap: wrap; gap: 5px 10px; margin-top: 3px; font-size: 9.5px; color: #8b909a; }
.uchip-agent-runtime-output { display: flex; flex-direction: column; gap: 2px; margin-top: 6px; padding: 6px 7px; border-radius: 6px; background: #1b1e24; font-size: 9.5px; color: #a6abb4; }
.uchip-agent-yield { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 4px 8px; margin-top: 5px; padding-top: 5px; border-top: 1px dashed #30343d; color: #777f8b; }
.uchip-agent-yield span { min-width: 0; }.uchip-agent-yield b { display: block; margin-top: 1px; color: #b9c6e2; font-weight: 500; font-variant-numeric: tabular-nums; }.uchip-agent-yield small { color: #656c77; }
.uchip-agent-models { margin-top: 6px; }
.uchip-agent-subtitle { color: #6f96e8; font-size: 9px; margin-bottom: 3px; }
.uchip-agent-model {
  display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: 3px 7px;
  align-items: baseline; font-size: 9px; color: #8b909a; padding: 2px 0;
}
.uchip-agent-model .model-name { color: #c9cdd5; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.uchip-agent-model small { grid-column: 1 / -1; color: #656b75; overflow-wrap: anywhere; }
.uchip-agent-model .model-speed { color: #8eabe7; font-variant-numeric: tabular-nums; }
.uchip-agent-artifacts { display: flex; flex-wrap: wrap; gap: 3px 10px; padding: 8px 0; font-size: 9.5px; color: #9aa0aa; border-bottom: 1px solid #21242b; }
.uchip-agent-artifacts .uchip-agent-subtitle { flex-basis: 100%; }
.uchip-agent-comparison { display: flex; flex-direction: column; gap: 2px; padding-top: 8px; font-size: 9.5px; color: #7f858f; }
.uchip-agent-comparison strong { color: #c9cdd5; }
.uchip-agent-empty { padding: 12px 4px; color: #8b909a; font-size: 11px; text-align: center; }
.uchip-agent-skeleton { display: grid; gap: 6px; padding: 4px 0; }
.uchip-agent-skeleton span { height: 24px; border-radius: 6px; background: linear-gradient(90deg, #1c2027, #272c35, #1c2027); background-size: 200% 100%; animation: agent-shimmer 1.2s infinite; }
@keyframes agent-shimmer { to { background-position: -200% 0; } }

@media (max-width: 380px) {
  .uchip-pop { width: calc(100vw - 12px); padding: 9px; }
  .uchip-tabs { top: -9px; }
  .uchip-agent-time { gap: 3px; }
  .uchip-agent-time > div { padding-inline: 4px; }
}
</style>
