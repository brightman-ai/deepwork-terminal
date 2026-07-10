<script setup lang="ts">
/**
 * UsageChip — a glanceable codex/claude usage chip for the CLI topbar. It shows the single
 * TIGHTEST subscription window as a colored % (额度提醒: green ok / amber warn / red near-limit),
 * taking no layout space beyond a small pill. Tap → a popover with every runtime's 5h/7d remaining
 * + reset time AND a cost/usage section (今日·7天·14天·30天 window switcher, per-runtime
 * tokens/cost/cache-hit/model/spark).
 * Data is the usage SSOT (useUsageQuota /usage/quota + useUsageReport /usage/report, both via
 * cliFetch(cliApi) so it works standalone AND pro-embedded) — no numbers are computed here.
 * Renders nothing when no quota is available (e.g. the rate-limit hook is not installed yet).
 *
 * Host-agnostic: the optional "详细报表 ›" link is emitted as `detail` (shown only when the host
 * passes show-detail), so this shared component never depends on any one shell's router.
 */
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { Gauge } from 'lucide-vue-next'
import { useUsageQuota } from './useUsageQuota'
import { useUsageReport, type UsageProviderRow } from './useUsageReport'
import { fmtTokens, fmtCost } from './cost'
import Spark from './Spark.vue'

defineProps<{ showDetail?: boolean }>()
const emit = defineEmits<{ (e: 'detail'): void }>()

const { quotas, tightest, load } = useUsageQuota()

const open = ref(false)
const wrapRef = ref<HTMLElement | null>(null)
// Fixed viewport coords for the teleported popover (see toggle). Left-anchored to the chip and
// clamped clear of the left nav rail + right edge, so it never hides under either.
const popPos = ref({ top: 0, left: 0 })
let timer: ReturnType<typeof setInterval> | undefined

const pct = computed(() => (tightest.value ? Math.round(tightest.value.window.remaining_percent) : null))
const level = computed(() => {
  const p = pct.value
  if (p === null) return 'none'
  if (p < 15) return 'crit'
  if (p < 40) return 'warn'
  return 'ok'
})
const runtimeLabel = (r: string) => (r === 'claude' ? 'Claude' : r === 'codex' ? 'Codex' : r === 'gemini' ? 'Gemini' : r)
const kindLabel = (k: string) => (k === '5h' ? '5小时' : k === '7d' ? '7天' : k)

function fmtReset(iso?: string): string {
  if (!iso) return ''
  const mins = Math.round((new Date(iso).getTime() - Date.now()) / 60000)
  // A reset time already in the PAST means the reading itself is stale (the quota source hasn't
  // refreshed — e.g. no codex session today, or the claude statusline hook stopped). Say so
  // honestly rather than the misleading "即将重置".
  if (mins < -2) return '数据已过期'
  if (mins <= 2) return '即将重置'
  if (mins < 60) return `${mins}m 后重置`
  if (mins < 1440) return `${Math.round(mins / 60)}h 后重置`
  return `${Math.round(mins / 1440)}d 后重置`
}

// ── 花费区 (replaces the old single "今日花费" line): 4 windows prefetched in PARALLEL,
// each into its OWN useUsageReport() instance (isolated `report` ref) so switching windows
// is instant and there's no race between concurrent fetches sharing one ref.
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
// under-count; fmtCost prefixes «≈». A specific runtime's cost===null already renders «—» via
// fmtCost itself (no flag needed) — never fabricated either way.
const costApprox = computed(() => activeReport.value?.summary?.cost_complete === false)
const providers = computed<UsageProviderRow[]>(() => activeReport.value?.providers ?? [])
// 会话数/轮次 — NOT exposed by the report backend (providers[]/summary only carry tokens +
// cost, no turn/session counters). DEFER: intentionally not rendered here — do not fabricate.

// A runtime counts as "subscription" only if useUsageQuota actually says so (joined by the
// `runtime` string — same enum as providers[].runtime). Unmatched/unknown runtimes default to
// "not subscription" so we never CLAIM a fake 等价 equivalence for a runtime we know nothing about.
function billingFor(runtime: string): string | undefined {
  return quotas.value.find((q) => q.runtime === runtime)?.billing
}
// req 4: only split into per-runtime rows when at least one runtime is subscription-billed
// (that's the only case where "≈等价 vs 实际" distinction matters). All-api → a single total.
const hasSubscription = computed(() => quotas.value.some((q) => q.billing === 'subscription'))
function cacheHitRate(p: UsageProviderRow): string {
  const read = p.cache_read_tokens ?? 0
  const denom = read + (p.input_tokens ?? 0)
  if (denom <= 0) return '—'
  const raw = (read / denom) * 100
  // Honest rounding: show 100% ONLY when it is TRULY all-cache (zero fresh input).
  // With prompt caching, cache_read dwarfs fresh input (e.g. 3196M vs 1.79M → 99.94%),
  // and Math.round would render a misleading perfect "100%". Cap at 99% otherwise so
  // the number never claims a perfection it doesn't have (同 fmtCost 的 never-fabricated 原则).
  const shown = raw >= 100 ? 100 : Math.min(99, Math.round(raw))
  return `${shown}%`
}

function prefetchAll(): void {
  // Fire all four in parallel; each composable instance keeps its own `report` ref so there's
  // no cross-window clobbering. Re-fetching on every open (rather than a load-once guard) keeps
  // the numbers fresh — and since `report.value` isn't cleared before the fetch resolves, the
  // switcher still shows the last-good cache instantly instead of flashing "loading" on reopen.
  void rep24h.load('24h')
  void rep7d.load('7d')
  void rep14d.load('14d')
  void rep30d.load('30d')
}

function gotoFullReport(): void {
  open.value = false
  emit('detail')
}

function toggle() {
  open.value = !open.value
  if (!open.value) return
  prefetchAll()
  // Position the (teleported, fixed) popover under the chip. Anchor its LEFT to the chip, clamped
  // to stay clear of the left nav rail (≥56) and the right edge — the old absolute+right:0 popover
  // was trapped in the topbar's stacking context and got covered by the left menu.
  const r = wrapRef.value?.getBoundingClientRect()
  if (r) {
    const POP_W = Math.min(320, window.innerWidth - 16)
    popPos.value = {
      top: r.bottom + 6,
      left: Math.max(56, Math.min(r.left, window.innerWidth - POP_W - 8)),
    }
  }
}

// The chip renders INLINE (no fixed overlay, no teleport) so its host decides placement — CliV2
// drops it into the terminal tab row (which itself teleports into the global topbar). Inline = it
// never floats over / blocks the right-panel controls (⤢ / ?), which the old position:fixed did.
onMounted(() => {
  void load()
  timer = setInterval(() => void load(), 60000) // quotas drift slowly; a 60s refresh is plenty
})
onUnmounted(() => clearInterval(timer))
</script>

<template>
  <div v-if="tightest" ref="wrapRef" class="uchip-wrap">
    <button class="uchip" :class="'lvl-' + level" type="button" title="订阅额度剩余 · 点开明细" @click.stop="toggle">
      <Gauge :size="12" class="uchip-ic" />
      <span class="uchip-pct">{{ pct }}%</span>
    </button>
  </div>
  <!-- Popover teleported to <body> + fixed-positioned (see toggle) so it escapes the topbar's
       stacking context — the old absolute + right:0 popover got covered by the left nav rail. -->
  <Teleport to="body">
    <template v-if="open">
      <div class="uchip-backdrop" @click="open = false" />
      <div class="uchip-pop" :style="{ top: popPos.top + 'px', left: popPos.left + 'px' }" @click.stop>
        <div class="uchip-pop-title">订阅额度剩余</div>
        <div v-for="q in quotas" :key="q.runtime" class="uchip-rt">
          <div class="uchip-rt-head">{{ runtimeLabel(q.runtime) }}<span v-if="q.plan" class="uchip-plan">{{ q.plan }}</span></div>
          <!-- API-key 计费：无订阅额度窗口，诚实标「按量付费」而非误导的额度条/过期 -->
          <div v-if="q.billing === 'api'" class="uchip-api">API 计费 · 按量付费（无订阅额度）</div>
          <div v-for="w in q.windows" :key="w.kind" class="uchip-win">
            <span class="uchip-win-k">{{ kindLabel(w.kind) }}</span>
            <span class="uchip-bar"><span class="uchip-bar-fill" :style="{ width: w.remaining_percent + '%' }" :class="'lvl-' + (w.remaining_percent < 15 ? 'crit' : w.remaining_percent < 40 ? 'warn' : 'ok')" /></span>
            <span class="uchip-win-p">{{ Math.round(w.remaining_percent) }}%</span>
            <span class="uchip-win-r">{{ fmtReset(w.reset_at) }}</span>
          </div>
        </div>
        <div class="uchip-sep" />
        <div class="uchip-costhead">
          <span>用量 / 花费</span>
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
          <template v-if="hasSubscription">
            <div v-for="p in providers" :key="p.runtime" class="uchip-prov">
              <div class="uchip-prov-head">
                <span class="uchip-prov-name">{{ runtimeLabel(p.runtime) }}</span>
                <span
                  v-if="billingFor(p.runtime) === 'subscription'"
                  class="uchip-eq"
                  title="包月实付、此为按 API 价的等价值"
                >≈等价</span>
              </div>
              <div class="uchip-prov-glance">
                <span class="uchip-prov-cost">{{ fmtCost(p.cost, p.currency, costApprox) }}</span>
                <span class="uchip-prov-tok">{{ fmtTokens(p.total_tokens) }} tok</span>
                <span class="uchip-prov-hit">缓存命中 {{ cacheHitRate(p) }}</span>
              </div>
              <div class="uchip-prov-bd">
                <span>↓{{ fmtTokens(p.input_tokens) }} / ↑{{ fmtTokens(p.output_tokens) }}</span>
                <span>缓存读{{ fmtTokens(p.cache_read_tokens) }} / 写{{ fmtTokens(p.cache_create_tokens) }}</span>
              </div>
              <div class="uchip-prov-ctx">
                <span class="uchip-prov-model">{{ p.top_model || '—' }}</span>
                <Spark :bars="p.spark ?? []" />
              </div>
            </div>
            <div v-if="!providers.length" class="uchip-dim">窗口内暂无用量</div>
          </template>
          <template v-else>
            <div class="uchip-cost">
              <span>总花费</span>
              <span>{{ fmtCost(activeReport.summary?.cost, activeReport.summary?.currency, costApprox) }} · {{ fmtTokens(activeReport.summary?.total_tokens) }} tok</span>
            </div>
          </template>
        </template>
        <div v-else class="uchip-dim uchip-loading">{{ activeReport?.reason || '用量数据不可用' }}</div>
      </div>
    </template>
  </Teleport>
</template>

<style scoped>
/* Inline in the terminal tab row (host places it) — flows with the tabs, never floats over or
   blocks the right-panel controls (the old position:fixed overlay did). Popover anchors to it. */
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
.uchip-pct { font-weight: 600; }

.uchip-backdrop { position: fixed; inset: 0; z-index: 3000; }
.uchip-pop {
  position: fixed; z-index: 3001;
  width: min(320px, calc(100vw - 16px)); max-height: min(72vh, 560px); overflow-y: auto;
  box-sizing: border-box; padding: 10px 12px; border-radius: 10px;
  background: #16181d; border: 1px solid #2a2d35; box-shadow: 0 10px 30px rgba(0,0,0,0.5); color: #e6e8ec;
}
.uchip-pop-title { font-size: 12px; color: #9aa0aa; margin-bottom: 8px; }
.uchip-rt { margin-bottom: 8px; }
.uchip-rt-head { font-size: 12px; font-weight: 600; margin-bottom: 3px; }
.uchip-plan { margin-left: 6px; font-size: 10px; color: #8b909a; font-weight: 400; }
.uchip-win { display: flex; align-items: center; gap: 6px; font-size: 11px; color: #c9cdd5; margin: 2px 0; }
.uchip-win-k { width: 40px; flex-shrink: 0; color: #8b909a; }
.uchip-bar { flex: 1; height: 5px; border-radius: 3px; background: #262a32; overflow: hidden; }
.uchip-bar-fill { display: block; height: 100%; border-radius: 3px; }
.uchip-bar-fill.lvl-ok { background: #22c55e; }
.uchip-bar-fill.lvl-warn { background: #f59e0b; }
.uchip-bar-fill.lvl-crit { background: #ef4444; }
.uchip-win-p { width: 34px; text-align: right; font-variant-numeric: tabular-nums; }
.uchip-win-r { width: 74px; text-align: right; color: #7f858f; font-size: 10px; }
.uchip-api { font-size: 11px; color: #8b909a; margin: 2px 0; }
.uchip-sep { height: 1px; background: #23262d; margin: 8px 0; }
.uchip-cost { display: flex; justify-content: space-between; font-size: 11px; color: #c9cdd5; }
.uchip-cost > span:first-child { color: #8b909a; }
.uchip-dim { color: #7f858f; }
.uchip-loading { font-size: 11px; padding: 4px 0; }

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

/* ── per-runtime provider row: glance (big) → breakdown (compact) → context (small) ─── */
.uchip-prov { padding: 7px 0; border-top: 1px solid #21242b; }
.uchip-prov:first-of-type { border-top: none; }
.uchip-prov-head { display: flex; align-items: center; gap: 6px; margin-bottom: 3px; }
.uchip-prov-name { font-size: 11.5px; font-weight: 600; color: #e6e8ec; }
.uchip-eq {
  font-size: 9.5px; color: #f59e0b; background: rgba(245,158,11,0.12);
  border-radius: 4px; padding: 1px 5px; cursor: help;
}
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
