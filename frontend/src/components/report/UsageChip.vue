<script setup lang="ts">
/**
 * UsageChip — the CLI topbar's usage entry point. It answers two DIFFERENT questions about
 * money, and keeps them apart:
 *
 *   「官方订阅」 — how much of my subscription quota is left, and when does it reset?
 *   「API 计费」 — how much have I actually spent per token?
 *
 * Hence two tabs. A subscription's "≈等价" (what those tokens would have cost at API prices)
 * is NOT a bill and never appears as one; an API spend is a real bill and never gets a
 * reset time it does not have.
 *
 * The pill renders whenever ANY provider account is present — including an API-only user
 * with no quota window at all (who previously had no entry into this UI whatsoever), and
 * including a provider whose CLI is currently broken (whose card previously vanished).
 * Presence is an account fact; a failed probe or a stale reading DEGRADES the card, it never
 * deletes it.
 *
 * All numbers come from the usage SSOT (useUsageQuota /usage/quota + useUsageReport
 * /usage/report, both via cliFetch(cliApi) so this works standalone AND pro-embedded) —
 * nothing is computed here.
 *
 * Host-agnostic: the optional "详细报表 ›" link is emitted as `detail` (shown only when the
 * host passes show-detail), so this shared component never depends on any one shell's router.
 */
import { onMounted, onUnmounted, ref, computed, watch } from 'vue'
import { Gauge } from 'lucide-vue-next'
import { useUsageQuota, type RuntimeQuota } from './useUsageQuota'
import { useUsageReport, type UsageProviderRow } from './useUsageReport'
import { fmtTokens, fmtCost } from './cost'
import Spark from './Spark.vue'

defineProps<{ showDetail?: boolean }>()
const emit = defineEmits<{ (e: 'detail'): void }>()

const { quotas, subscriptions, apiRuntimes, hasSubscription, hasApi, tightest, loaded, loading, fetchedAt, load } = useUsageQuota()

const open = ref(false)
const wrapRef = ref<HTMLElement | null>(null)
// A ticking clock so every "更新于 …" recomputes on its own. Ages were rendered from a number
// the BACKEND computed at fetch time and then frozen, so a tab left open for ten minutes kept
// insisting the reading was taken 「刚刚」. The age is now derived from the reading's absolute
// timestamp against a clock that actually moves.
const now = ref(Date.now())
let clock: ReturnType<typeof setInterval> | undefined
// Fixed viewport coords for the teleported popover (see toggle). Left-anchored to the chip and
// clamped clear of the left nav rail + right edge, so it never hides under either.
const popPos = ref({ top: 0, left: 0 })
let timer: ReturnType<typeof setInterval> | undefined

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
// Honest: cost_complete===false (some model in the window had no price) → the number is an
// under-count; fmtCost prefixes «≈». A specific runtime's cost===null already renders «—».
const costApprox = computed(() => activeReport.value?.summary?.cost_complete === false)
const providerFor = (runtime: string, w: ReportWindow = activeWindow.value) =>
  reportByWindow[w].report.value?.providers?.find((p) => p.runtime === runtime)

// ── tabs ─────────────────────────────────────────────────────────────────────────────────
type Tab = 'sub' | 'api'
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
}

// API 计费 tab lists the runtimes whose spend is a REAL bill (billing==='api') plus the ones we
// cannot attribute (billing==='unknown' → 「未分类」). Subscription runtimes are deliberately
// absent HERE: their tokens are already paid for by the plan, so their spend must never sit in a
// column that reads as "what you owe".
//
// But they still get the SAME 今日/7天/14天/30天 breakdown on their own tab — labelled ≈等价.
// Splitting the tabs is about separating two kinds of MONEY, not about taking the 7/14/30-day
// view away from subscribers (which an earlier cut of this component did).
const apiTabRuntimes = computed<RuntimeQuota[]>(() =>
  quotas.value.filter((q) => q.billing === 'api' || q.billing === 'unknown'),
)
// The runtimes whose usage the CURRENT tab is accounting for.
const costRuntimes = computed<RuntimeQuota[]>(() => (tab.value === 'sub' ? subscriptions.value : apiTabRuntimes.value))
const costHeading = computed(() => (tab.value === 'sub' ? '用量 / ≈等价' : '用量 / 实付'))
// What KIND of money this row is. The badge is the whole point of the split, so it is rendered
// per row rather than implied by which tab you happen to be on.
function billingBadge(q: RuntimeQuota): { text: string; cls: string; title: string } {
  if (q.billing === 'subscription') {
    return { text: '≈等价', cls: 'eq', title: '包月实付；此为按 API 价折算的等价值，不是账单' }
  }
  if (q.billing === 'api') {
    return { text: '实付', cls: 'api', title: '按量付费 · 这是真实应付成本' }
  }
  return { text: '未分类', cls: 'unknown', title: '无法证明这段用量属于订阅还是 API，不猜' }
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
  for (const q of apiRuntimes.value) {
    const c = providerFor(q.runtime, '24h')?.cost
    if (typeof c === 'number') total = (total ?? 0) + c
  }
  return total
})
const pillText = computed(() => {
  if (pct.value !== null) return `${pct.value}%`
  if (hasApi.value) {
    const c = todayApiCost.value
    return c === null ? 'API' : `API ${fmtCost(c, providerFor(apiRuntimes.value[0]?.runtime ?? '', '24h')?.currency)}`
  }
  return '—' // present, but no reading we can stand behind — the popover explains why
})
const pillTitle = computed(() =>
  pct.value !== null ? '订阅额度剩余 · 点开明细' : hasApi.value ? 'API 计费 · 今日实付 · 点开明细' : '用量 · 点开明细',
)

// ── formatting ───────────────────────────────────────────────────────────────────────────
// The reset time is shown as a WALL CLOCK, not a countdown ("23:08 重置", not "2h 后重置").
// A countdown forces the reader to do arithmetic against a number that is itself rounded,
// and it reads as an estimate; the runtime gives us an exact instant, so we show the instant.
function fmtReset(iso?: string): string {
  if (!iso) return ''
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  const now = new Date()
  if (at.getTime() < now.getTime() - 120_000) return '已重置' // reading predates its own reset
  const hhmm = `${String(at.getHours()).padStart(2, '0')}:${String(at.getMinutes()).padStart(2, '0')}`
  const days = Math.round(
    (new Date(at.getFullYear(), at.getMonth(), at.getDate()).getTime() -
      new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()) / 86_400_000,
  )
  if (days <= 0) return `${hhmm} 重置`
  if (days === 1) return `明天 ${hhmm}`
  return `${at.getMonth() + 1}/${at.getDate()} ${hhmm}`
}
// Age is measured from the reading's own absolute timestamp against a moving clock — NOT from
// the age the backend computed at fetch time (which freezes the moment the response lands, so a
// tab open for ten minutes keeps claiming 「刚刚」 forever).
function fmtAgeAt(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const sec = Math.max(0, Math.round((now.value - t) / 1000))
  if (sec < 90) return '刚刚'
  if (sec < 3600) return `${Math.round(sec / 60)} 分钟前`
  if (sec < 86400) return `${Math.round(sec / 3600)} 小时前`
  return `${Math.round(sec / 86400)} 天前`
}
// How long ago THIS CLIENT last got an answer — a separate fact from when the runtime last
// reported, and the one that was silently lying before.
const fetchedAgo = computed(() => (fetchedAt.value ? fmtAgeAt(new Date(fetchedAt.value).toISOString()) : ''))
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

function prefetchAll(): void {
  // Fire all four in parallel; each composable instance keeps its own `report` ref so there's
  // no cross-window clobbering. `report.value` isn't cleared before the fetch resolves, so the
  // switcher shows the last-good cache instantly instead of flashing "loading" on reopen.
  void rep24h.load('24h')
  void rep7d.load('7d')
  void rep14d.load('14d')
  void rep30d.load('30d')
}

// refresh pulls EVERYTHING the popover shows. The quota used to be refreshed only by the 60s
// background timer — so opening the popover showed you whatever that timer last saw, and the
// number you were staring at could be a minute (or, in a throttled background tab, many
// minutes) out of date. If you are looking at it, we go and ask.
function refresh(): void {
  now.value = Date.now()
  void load()
  prefetchAll()
}

function gotoFullReport(): void {
  open.value = false
  emit('detail')
}

function toggle() {
  open.value = !open.value
  if (!open.value) return
  refresh()
  // Position the (teleported, fixed) popover under the chip. Anchor its LEFT to the chip, then
  // clamp: clear of the left nav rail on desktop, and clear of the right edge everywhere — an
  // absolute+right:0 popover gets trapped in the topbar's stacking context and covered by the
  // left menu. The rail clamp must NOT apply on a phone (there is no rail there, and forcing
  // left≥56 on a 390px screen pushes the popover's right edge off-screen).
  const r = wrapRef.value?.getBoundingClientRect()
  if (r) {
    const POP_W = Math.min(340, window.innerWidth - 16)
    const minLeft = window.innerWidth < 520 ? 8 : 56
    const maxLeft = window.innerWidth - POP_W - 8
    popPos.value = {
      top: r.bottom + 6,
      left: Math.max(minLeft, Math.min(r.left, maxLeft)),
    }
  }
}

// Coming back to the page is the moment the numbers matter and the moment they are most likely
// stale: a backgrounded tab has its timers throttled (and on mobile, suspended outright), so the
// 60s poll simply does not run while you are away. Re-ask on the way back in.
function onVisible(): void {
  if (document.visibilityState !== 'visible') return
  now.value = Date.now()
  void load()
  if (open.value) prefetchAll()
}

// The chip renders INLINE (no fixed overlay, no teleport) so its host decides placement — it
// never floats over / blocks the right-panel controls (⤢ / ?), which a position:fixed one did.
onMounted(() => {
  void load()
  // The pill can show today's API spend, so an API-only user needs the report before opening.
  void rep24h.load('24h')
  timer = setInterval(() => void load(), 60000) // background poll; the real freshness comes from the events above
  clock = setInterval(() => (now.value = Date.now()), 30000) // keeps every 「更新于 …」 honest
  document.addEventListener('visibilitychange', onVisible)
  window.addEventListener('focus', onVisible)
})
onUnmounted(() => {
  clearInterval(timer)
  clearInterval(clock)
  document.removeEventListener('visibilitychange', onVisible)
  window.removeEventListener('focus', onVisible)
})
</script>

<template>
  <!-- Presence — not health, not freshness — decides whether the entry exists at all. -->
  <div v-if="quotas.length" ref="wrapRef" class="uchip-wrap">
    <button class="uchip" :class="'lvl-' + level" type="button" :title="pillTitle" @click.stop="toggle">
      <Gauge :size="12" class="uchip-ic" />
      <span class="uchip-pct">{{ pillText }}</span>
    </button>
  </div>

  <Teleport to="body">
    <template v-if="open">
      <div class="uchip-backdrop" @click="open = false" />
      <div class="uchip-pop" :style="{ top: popPos.top + 'px', left: popPos.left + 'px' }" @click.stop>
        <div class="uchip-tabs" role="tablist">
          <button type="button" role="tab" :aria-selected="tab === 'sub'" :class="{ on: tab === 'sub' }" @click="pickTab('sub')">官方订阅</button>
          <button type="button" role="tab" :aria-selected="tab === 'api'" :class="{ on: tab === 'api' }" @click="pickTab('api')">API 计费</button>
        </div>

        <!-- ── 官方订阅 tab 独有：额度条 / 重置 / 新鲜度（API 计费没有额度窗口，不伪造）── -->
        <template v-if="tab === 'sub'">
          <div v-for="q in subscriptions" :key="q.runtime" class="uchip-rt">
            <div class="uchip-rt-head">
              <span>{{ runtimeLabel(q.runtime) }}</span>
              <span v-if="q.plan" class="uchip-plan">{{ q.plan }}</span>
              <span v-if="healthLabel(q)" class="uchip-badge warn" :title="q.health?.reason">{{ healthLabel(q) }}</span>
              <span v-if="q.snapshot?.stale" class="uchip-badge stale">数据已过期</span>
            </div>

            <div v-for="w in q.windows" :key="w.kind" class="uchip-win" :class="{ dim: q.snapshot?.stale }">
              <span class="uchip-win-k">{{ kindLabel(w.kind) }}</span>
              <span class="uchip-bar"><span class="uchip-bar-fill" :style="{ width: w.remaining_percent + '%' }" :class="'lvl-' + (w.remaining_percent < 15 ? 'crit' : w.remaining_percent < 40 ? 'warn' : 'ok')" /></span>
              <span class="uchip-win-p">{{ Math.round(w.remaining_percent) }}%</span>
              <span class="uchip-win-r">{{ fmtReset(w.reset_at) }}</span>
            </div>

            <!-- No reading at all: say so plainly. Never a fabricated 0%/100% bar. -->
            <div v-if="!q.windows?.length" class="uchip-dim uchip-note">{{ q.note || '暂无额度数据' }}</div>
            <div v-if="q.snapshot" class="uchip-dim uchip-note">额度更新于 {{ fmtAgeAt(q.snapshot.captured_at) }}</div>
          </div>
          <div v-if="!subscriptions.length" class="uchip-dim uchip-empty">未检出官方订阅账号</div>
          <div class="uchip-sep" />
        </template>

        <!-- ── 用量 / 花费：两个 tab 都有 今日·7天·14天·30天，只是这笔钱的性质不同 ──── -->
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
          <div v-for="q in costRuntimes" :key="q.runtime" class="uchip-prov">
            <div class="uchip-prov-head">
              <span class="uchip-prov-name">{{ runtimeLabel(q.runtime) }}</span>
              <span class="uchip-badge" :class="billingBadge(q).cls" :title="billingBadge(q).title">{{ billingBadge(q).text }}</span>
            </div>
            <template v-if="providerFor(q.runtime)">
              <div class="uchip-prov-glance">
                <span class="uchip-prov-cost">{{ fmtCost(providerFor(q.runtime)?.cost, providerFor(q.runtime)?.currency, costApprox) }}</span>
                <span class="uchip-prov-tok">{{ fmtTokens(providerFor(q.runtime)?.total_tokens) }} tok</span>
                <span class="uchip-prov-hit">缓存命中 {{ cacheHitRate(providerFor(q.runtime)!) }}</span>
              </div>
              <div class="uchip-prov-bd">
                <span>↓{{ fmtTokens(providerFor(q.runtime)?.input_tokens) }} / ↑{{ fmtTokens(providerFor(q.runtime)?.output_tokens) }}</span>
                <span>缓存读{{ fmtTokens(providerFor(q.runtime)?.cache_read_tokens) }} / 写{{ fmtTokens(providerFor(q.runtime)?.cache_create_tokens) }}</span>
              </div>
              <div class="uchip-prov-ctx">
                <span class="uchip-prov-model">{{ providerFor(q.runtime)?.top_model || '—' }}</span>
                <Spark :bars="providerFor(q.runtime)?.spark ?? []" />
              </div>
            </template>
            <div v-else class="uchip-dim uchip-note">窗口内暂无用量</div>
          </div>
          <div v-if="!costRuntimes.length" class="uchip-dim uchip-empty">
            {{ tab === 'sub' ? '无订阅用量' : '无 API 计费用量' }}
          </div>
          <div v-else-if="tab === 'api'" class="uchip-foot">按各 runtime 的当前计费模式归类；窗口内若曾切换计费方式，归类可能不准。</div>
        </template>
        <div v-else class="uchip-dim uchip-loading">{{ activeReport?.reason || '用量数据不可用' }}</div>

        <div class="uchip-refresh">
          <span class="uchip-dim">{{ fetchedAgo ? `拉取于 ${fetchedAgo}` : '' }}</span>
          <button type="button" :disabled="loading" @click="refresh">{{ loading ? '刷新中…' : '⟳ 刷新' }}</button>
        </div>
      </div>
    </template>
  </Teleport>
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
  box-sizing: border-box; padding: 10px 12px; border-radius: 10px;
  background: #16181d; border: 1px solid #2a2d35; box-shadow: 0 10px 30px rgba(0,0,0,0.5); color: #e6e8ec;
}

/* ── two tabs = two kinds of money, never mixed ─────────────────────────────── */
.uchip-tabs {
  display: flex; width: 100%; background: #1b1e24; border: 1px solid #2a2d35;
  border-radius: 7px; padding: 2px; gap: 2px; margin-bottom: 10px;
}
.uchip-tabs button {
  flex: 1; padding: 4px 0; border-radius: 5px; border: none; background: none;
  color: #8b909a; font-size: 11px; cursor: pointer; font-family: inherit;
}
.uchip-tabs button.on { background: #2a2d35; color: #e6e8ec; font-weight: 600; }

.uchip-rt { margin-bottom: 10px; }
.uchip-rt-head { display: flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 600; margin-bottom: 4px; }
.uchip-plan { font-size: 10px; color: #8b909a; font-weight: 400; }
.uchip-badge { font-size: 9.5px; font-weight: 500; border-radius: 4px; padding: 1px 5px; margin-left: auto; }
.uchip-badge + .uchip-badge { margin-left: 4px; }
/* Amber, never red: red reads as "quota exhausted", which is a different (and false) claim. */
.uchip-badge.warn { color: #f59e0b; background: rgba(245,158,11,0.12); }
.uchip-badge.stale { color: #9aa0aa; background: rgba(154,160,170,0.12); }
.uchip-badge.api { color: #4ade80; background: rgba(74,222,128,0.12); }
.uchip-badge.unknown { color: #9aa0aa; background: rgba(154,160,170,0.12); }

.uchip-win { display: flex; align-items: center; gap: 6px; font-size: 11px; color: #c9cdd5; margin: 2px 0; }
.uchip-win.dim { opacity: 0.55; } /* stale values stay visible but stop looking authoritative */
.uchip-win-k { width: 40px; flex-shrink: 0; color: #8b909a; }
.uchip-bar { flex: 1; height: 5px; border-radius: 3px; background: #262a32; overflow: hidden; }
.uchip-bar-fill { display: block; height: 100%; border-radius: 3px; }
.uchip-bar-fill.lvl-ok { background: #22c55e; }
.uchip-bar-fill.lvl-warn { background: #f59e0b; }
.uchip-bar-fill.lvl-crit { background: #ef4444; }
.uchip-win-p { width: 34px; text-align: right; font-variant-numeric: tabular-nums; }
.uchip-win-r { width: 84px; text-align: right; color: #7f858f; font-size: 10px; white-space: nowrap; }

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
</style>
