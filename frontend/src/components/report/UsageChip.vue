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
import { useUsageQuota, quotaGroupsFor, findTightestQuota, accountKey, type QuotaGroup, type RuntimeQuota } from './useUsageQuota'
import { useUsageReport, type UsageProviderRow } from './useUsageReport'
import { useAgentReport } from './useAgentReport'
import AgentReportDetail from './AgentReportDetail.vue'
import { fmtTokens, fmtCost, fmtCredits } from './cost'
import Spark from './Spark.vue'
import { placeAnchoredPopover, type RectLike } from './popoverPlacement'
import { usageMoneyPresentation, subscriptionCovers, facadeNote, type UsageMoneySemantics, type SubscriptionAccount } from './usageBillingPresentation'
import { groupByVendor, type UsageVendorGroup } from './usageVendorGroups'
import type { UsageRateCard } from './useUsageReport'
import { groupPresentation } from './quotaStaleness'

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

// The CALLER's name. Deliberately a lookup with a passthrough default, NOT an enum: a runtime we
// have never heard of (pro's whale, tomorrow's agent) renders under its own id rather than being
// dropped or shown as「其他」. The axis is data-driven; only the prettier spellings are listed.
const runtimeLabel = (r: string) => (r === 'claude' ? 'Claude' : r === 'codex' ? 'Codex' : r === 'gemini' ? 'Gemini' : r)
// The ACCOUNT's name, served by the backend. It is not derived from the runtime any more: the
// same CLI can bill two vendors, so「Codex」names a program while「Codex 官方」and「Kimi For
// Coding」name the two bills — and only the latter answers "whose money is this?".
const accountLabel = (q: RuntimeQuota) => q.display || `${runtimeLabel(q.runtime)} 官方`
// 用量/花费区的行名。这里曾经只能写「Codex 官方」这种 CLI 名，因为报表侧的 token 只带 model
// 不带 model_provider，认不出账号——那条限制已经解除（后端按 endpoint 交叉核对 model id 定 vendor），
// 所以这一区终于能和额度区叫同一个名字：你买的是「Kimi For Coding」，两处就都这么写。
//
// 取名顺序：本机账号的订阅产品名 → 厂商发票名 → CLI 名兜底。前两级都来自后端，新增一个厂商
// 不需要改这里。
const subscriptionNameByVendor = computed(() => {
  const names = new Map<string, string>()
  for (const q of quotas.value) {
    if (q.vendor && q.display) names.set(q.vendor, q.display)
  }
  return names
})
const accountRowLabel = (row: UsageProviderRow) =>
  subscriptionNameByVendor.value.get(row.vendor)
  || row.vendor_display
  || `${runtimeLabel(row.runtime)} 官方`
// 「经由谁」。同一个订阅可能既被直连花掉、也被某个中转端点花掉，那是两笔不同的开销，
// 后端已经分成两行；不写出端点，两行看起来就是重复的。
const viaLabel = (row: UsageProviderRow) => {
  const endpoint = row.runtime_provider
  if (!endpoint || endpoint === row.vendor) return ''
  return `${runtimeLabel(row.runtime)} · 经 ${endpoint}`
}
// 窗口长度的中文名。'30d' 是后来才有的——智谱把 MCP 工具配额按月给，而窗口标签由长度决定，
// 所以标签表必须跟着后端的 windowKind 一起长，否则月窗会顶着「7天」的名字出现。
const kindLabel = (k: string) => (k === '5h' ? '5小时' : k === '7d' ? '7天' : k === '30d' ? '30天' : k)

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
// 本机真正持有的订阅，连同它各自的端点一起交给归属规则。
//
// 按 VENDOR 不按 runtime：订阅是跟厂商买的，runtime 只是你从哪个 CLI 花它 —— 同一份
// Kimi For Coding 既能被 codex 花也能被 claude code 花。再按 ENDPOINT 收窄：同一个厂商
// 同时有套餐和 API key 是常事，端点才分得开哪笔是套餐（`billing_mode` 绝大多数是 unknown，
// 分不开）。present 是硬条件：没配 key 的厂商不算你有订阅。
const subscriptionAccounts = computed<SubscriptionAccount[]>(() =>
  quotas.value
    .filter((quota) => quota.present && quota.billing === 'subscription' && quota.vendor)
    .map((quota) => ({ vendor: quota.vendor as string, endpoints: quota.endpoints })),
)
const rowsInTab = (rows: UsageProviderRow[], which: 'sub' | 'api') =>
  rows.filter((row) => usageMoneyPresentation(row, subscriptionAccounts.value).tab === which)

// ── 官方订阅 tab: grain = the ACCOUNT (vendor × 端点) ──────────────────────────────────────────
// 能到这里的只有「本机确有订阅账号的厂商」——第三方厂商不是被硬编码挡在门外，而是没有账号就没有
// 门；配好一个 key，它自己就走进来了。
const subProviders = computed<UsageProviderRow[]>(() => rowsInTab(providersFor(), 'sub'))

// ── API 计费 tab: grain = the VENDOR, with the callers beneath ─────────────────────────────────
// 「我欠谁钱」is the main row; 「谁替我花的」is the detail. Grouping happens over the rows already
// placed in THIS tab, so subscription tokens can never leak into an API total.
const apiVendors = computed<UsageVendorGroup[]>(() => groupByVendor(rowsInTab(providersFor(), 'api')))

const costHeading = computed(() => (tab.value === 'sub' ? '用量 / ≈等价' : '用量 / 估算'))
const tabHasRows = computed(() => (tab.value === 'sub' ? subProviders.value.length > 0 : apiVendors.value.length > 0))

// Approximation is a property of what you are LOOKING AT, not of the window. Reading it off the
// window summary meant an unpriced model in the API tab put a「≈」on the subscription tab's number,
// which is a confession about the wrong number.
const rowIncomplete = (r: UsageProviderRow) => (r.requests ?? 0) > 0 && (r.priced_requests ?? 0) < (r.requests ?? 0)

// A vendor with no name is not「其他」— it is a specific gap, and which gap it is decides whether
// the user can do anything about it. Observed on a live machine, both kinds exist at once:
//
//   model id present but unrecognised → actionable: the id can be added to the vendor table.
//   model id absent from the transcript → not actionable here: the CLI never recorded it (a
//                                         resumed codex session whose turn_context scrolled past).
//
// Collapsing them into one「未知厂商」sends the user looking for a model id that was never written.
const vendorLabel = (g: UsageVendorGroup) => g.display || '未知厂商'
const vendorTitle = (g: UsageVendorGroup) => {
  if (g.display) return `厂商：${g.display}`
  return g.topModel
    ? `model「${g.topModel}」不在内置厂商表里，因此无法判断计费主体，也不估算金额。`
    : '这些请求的 transcript 没有记录 model id（多见于续跑的会话），因此既认不出厂商也无法定价。token 用量仍是实测的。'
}

// What KIND of money this is. The badge is the whole point of the split, so it is rendered per row
// rather than implied by whichever tab you happen to be on.
const BADGES: Record<UsageMoneySemantics, { text: string; cls: string; title: string }> = {
  api_paid: { text: '实付', cls: 'api', title: '逐请求证据表明这是按量付费 · 真实应付成本' },
  api_equivalent: {
    text: '≈等价', cls: 'eq',
    title: '包月已付；此为按 API 价折算的等价值，不是账单，也不改写历史请求归属',
  },
  api_estimated: {
    text: '估算', cls: 'est',
    title: 'transcript 未记录计费方式。这不是官方订阅，所以按厂商标价估算——既不能称「实付」，也不是「等价」',
  },
}
function semanticsOf(row: UsageProviderRow): UsageMoneySemantics {
  return usageMoneyPresentation(row, subscriptionAccounts.value).semantics
}
// The unit price, spelled out. A currency SYMBOL is not evidence: Moonshot sells k3 at both ¥20/M
// and $3.00/M — the same price at its own 6.67 conversion — so「$85.93」alone cannot be checked, and
// being wrong by 6.67× looks entirely plausible. Printing the rate the money was computed FROM is
// what makes the total falsifiable.
//
// Both cards are vendor-published numbers. Nothing here converts between them; this codebase holds
// no exchange rate, because an FX rate is a third fact with its own source and its own staleness.
const CURRENCY_SYMBOL: Record<string, string> = { USD: '$', CNY: '¥' }
const rateSymbol = (c: string) => CURRENCY_SYMBOL[c] ?? `${c} `
function rateLabel(card: UsageRateCard): string {
  const s = rateSymbol(card.currency)
  return `${s}${card.input_per_m}/${s}${card.output_per_m} 每 1M`
}
// 分档的单价必须**整套**说出来，不能只报最便宜那一档。
//
// 这里曾经只印第一张卡：gpt-5.6-sol 写着 $5/$30，而编码会话里占绝大多数的长上下文请求
// 实收 $10/$45 —— 读者拿这个单价怎么算都对不上总额，而且看不出为什么。单价这一行存在的
// 唯一理由就是让总额可核对，报错档等于把它反过来用。
const THRESHOLD_TEXT = (n: number) => (n >= 1000 ? `${Math.round(n / 1000)}k` : `${n}`)
const bandLabel = (c: UsageRateCard): string => {
  if (c.band === 'long_context') return `上下文≥${THRESHOLD_TEXT(c.threshold ?? 0)}`
  if (c.band === 'long_output') return `回答≥${THRESHOLD_TEXT(c.threshold ?? 0)}`
  return c.band === 'base' ? '基础' : ''
}
const shortRate = (c: UsageRateCard) => {
  const s = rateSymbol(c.currency)
  return `${s}${c.input_per_m}/${s}${c.output_per_m}`
}
function unitPriceLine(cards: UsageRateCard[]): string {
  const mine = cards.filter((c) => c.primary !== false)
  const others = cards.filter((c) => c.primary === false)
  if (!mine.length) return ''
  // 单一价格：照旧一行说完。分档：每档带上它的条件，读者才知道总额是几档的混合。
  const head = mine.length === 1 && !mine[0].band
    ? rateLabel(mine[0])
    : mine.map((c) => `${bandLabel(c)} ${shortRate(c)}`).join(' · ') + ` 每 1M`
  if (!others.length) return head
  return `${head}（另一官方价 ${others.map(shortRate).join(' · ')}）`
}
function unitPriceTitle(topModel: string, cards: UsageRateCard[]): string {
  const banded = cards.some((c) => !!c.band)
  const lines = cards.map((c) => {
    const s = rateSymbol(c.currency)
    const which = c.primary === false
      ? '厂商另一平台的官方价，仅供对照'
      : (c.band ? `${bandLabel(c)} 时按此价` : '本行金额按此价计算')
    return `${c.currency}：输入 ${s}${c.input_per_m} / 输出 ${s}${c.output_per_m} / 缓存读 ${s}${c.cache_read_per_m} 每 1M —— ${which}`
  })
  if (banded) {
    lines.push('这个模型按长度分档计价，所以本行总额是几档的混合，不能用任何单独一档去乘总 token 数。')
  }
  lines.push('列出的都是厂商公布的标价，不是汇率换算——本系统不持有任何汇率。')
  return `${topModel}\n${lines.join('\n')}`
}

// A vendor group inherits its callers' semantics only when they agree; a mix means the honest
// label is the weaker one (估算), since part of the number is unproven.
function vendorSemantics(g: UsageVendorGroup): UsageMoneySemantics {
  const kinds = new Set(g.rows.map(semanticsOf))
  return kinds.size === 1 ? [...kinds][0] : 'api_estimated'
}
// 一个厂商组里只要有一行是「门面 model」，这个组的金额就注定不全 —— 把那一行的原话拿上来，
// 别让用户对着一个缺口自己猜。多行都缺时按整组的计价笔数重算措辞（部分 vs 全部）。
const groupFacadeNote = (g: UsageVendorGroup) => {
  const hit = g.rows.find((row) => row.attribution_basis === 'endpoint')
    ?? g.rows.find((row) => facadeNote(row))
  if (!hit) return null
  if (hit.attribution_basis !== 'endpoint') return facadeNote(hit)
  return facadeNote({
    attribution_basis: 'endpoint',
    runtime_provider: hit.runtime_provider,
    top_model: hit.top_model,
    requests: g.requests,
    priced_requests: g.pricedRequests,
  })
}

// ── pill ─────────────────────────────────────────────────────────────────────────────────
// ── which subscription the pill speaks for ────────────────────────────────────────────────────
//
// The pill used to headline the TIGHTEST quota across every subscription, which is safety-first
// and, with more than one, wrong about the question being asked. Observed here: a codex premium
// window at 0%, a claude window at 98%, and the chrome reading「0%」all day — while the codex CLI
// was in fact busy sending every one of its tokens to Moonshot and DeepSeek. Not one of them
// touched the OpenAI subscription the 0% was about.
//
// So relevance is spend, not membership: a subscription speaks for the pill when there is recent
// usage BILLED TO ITS VENDOR. subscriptionCovers is the same predicate that decides tab
// placement, so「what counts as spending against this subscription」has one definition.
//
// Nothing is hidden by this. A quota that loses the headline keeps its bar, its percentage and its
// reset time in 官方订阅, and the tooltip below names it whenever it is the tighter one — an
// exhausted window you are not using today is still exhausted tomorrow, and must stay one glance
// away rather than one discovery away.
const spendingSubscriptions = computed<ReadonlySet<string>>(() => new Set(
  providersFor('24h')
    .filter((row) => subscriptionCovers(row, subscriptionAccounts.value) && (row.total_tokens ?? 0) > 0)
    .map((row) => row.vendor),
))
// Falls back to every subscription when none has first-party spend today — a quiet day should show
// the same reading it always did, not go blank.
//
// 归属是更强的信号，且是第一手的：后端能说出「最近一次会话记在谁头上」时，一个**明确没在计费**的
// 账号就不该替这颗药丸说话——那正是截图里的处境（官方额度早已耗尽，人已改用 Kimi，而 chrome 还在
// 用官方那条读数当头条）。但「不知道」绝不当「没在用」：claude 压根没有归属这一说，必须留下。
const headline = computed(() => {
  const relevant = quotas.value.filter((q) => q.attribution?.active !== false)
  const pool = relevant.length ? relevant : quotas.value
  const spending = pool.filter((q) => !!q.vendor && spendingSubscriptions.value.has(q.vendor))
  return findTightestQuota(spending.length ? spending : pool)
})
// The tightest overall, kept only to warn about a subscription the headline is not speaking for.
const overshadowed = computed(() => {
  const h = headline.value
  const t = tightest.value
  if (!h || !t || t.account === h.account) return null
  return t.window.remaining_percent < h.window.remaining_percent ? t : null
})
const pct = computed(() => (headline.value ? Math.round(headline.value.window.remaining_percent) : null))
const level = computed(() => {
  const p = pct.value
  if (p === null) return 'none'
  if (p < 15) return 'crit'
  if (p < 40) return 'warn'
  return 'ok'
})
// API-only users have no quota % to show, so the pill carries today's spend instead («API $1.23»).
// It sums the SAME rows the API tab shows — pill and tab reading different sets is how a user
// learns to distrust both. Mixed currencies have no sum, so the pill drops to a bare「API」rather
// than adding dollars to yuan.
const todayApi = computed<{ cost: number | null; currency: string }>(() => {
  let total: number | null = null
  let currency = ''
  for (const row of rowsInTab(providersFor('24h'), 'api')) {
    if (typeof row.cost !== 'number') continue
    if (currency && row.currency !== currency) return { cost: null, currency: '' }
    currency = row.currency ?? ''
    total = (total ?? 0) + row.cost
  }
  return { cost: total, currency }
})
// A percentage needs a SUBJECT. The pill shows the tightest quota across runtimes, so with more
// than one subscription a bare「0%」names nothing — and 0% is exactly the reading that makes
// someone stop what they are doing. Observed: a codex premium window ran out, the user moved to
// another vendor entirely, and the chrome kept headlining 0% for the subscription they had
// deliberately stopped using, with nothing on screen to say which one it was.
//
// Naming it costs four characters and turns「0%」into a fact you can act on or dismiss. The
// SELECTION rule is untouched: the tightest quota is still the one worth surfacing, because an
// exhausted window you are not using today is still exhausted tomorrow.
const pillText = computed(() => {
  if (pct.value !== null) {
    const owner = subscriptions.value.length > 1 && headline.value
      ? `${headline.value.display} ` : ''
    return `${owner}${pct.value}%`
  }
  if (hasApi.value) {
    const { cost, currency } = todayApi.value
    return cost === null ? 'API' : `API ${fmtCost(cost, currency)}`
  }
  return '—' // present, but no reading we can stand behind — the popover explains why
})
const pillTitle = computed(() => {
  if (pct.value === null) return hasApi.value ? 'API 计费 · 今日实付 · 点开明细' : '用量 · 点开明细'
  const h = headline.value
  if (!h) return '订阅额度剩余 · 点开明细'
  // Say whose window this is and which window, so the number can be checked rather than trusted.
  let text = `${h.display} ${kindLabel(h.window.kind)}额度剩余 ${pct.value}%`
  if (spendingSubscriptions.value.has(h.runtime)) text += '（近 24h 你在花的就是它）'
  // The louder number never disappears — it just stops being the headline for something you are
  // not spending against.
  const other = overshadowed.value
  if (other) {
    text += ` · 另有 ${other.display} ${kindLabel(other.window.kind)}仅剩 `
      + `${Math.round(other.window.remaining_percent)}%，但近 24h 没有走它的用量`
  }
  return `${text} · 点开明细`
})

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
/**
 * 这一组读数「有多可信」+ 据此怎么呈现。判定全在 quotaStaleness.ts（纯函数、带测试），这里只把
 * 组件手上的现状喂进去。
 *
 * canProbe 只对 codex 为真：probe 通道（拿 ~/.codex/auth.json 直接问账号，和 `codex /status`
 * 同源）目前只有 codex 有。claude 的读数来自它自己写的快照，没有等价的"就地问一次"，所以那边
 * 只降权、不给一个点了没反应的按钮。
 */
function staleOf(q: RuntimeQuota, group: QuotaGroup) {
  return groupPresentation({
    stale: !!group.snapshot?.stale,
    ageSeconds: group.snapshot?.age_seconds ?? 0,
    allInferred: (group.windows || []).length > 0 && (group.windows || []).every((w) => !!w.inferred),
    runtime: accountLabel(q),
    // Served by the domain now. Hardcoding「codex 可以问」was already wrong the moment a second
    // vendor appeared behind that same CLI, and it is the kind of wrong that shows a button
    // which does nothing.
    canProbe: !!q.can_probe,
    // q.family = 最新那条账号读数的 family = 当前生效的家族。与它不一致的分组是历史。
    groupFamily: group.family || '',
    activeFamily: q.family || '',
    // 说真正的原因（"你在用 Kimi"）而不是症状（"6 天没有上报"）。
    billedToDisplay: billedElsewhere(q),
  })
}

// ── 账号归属：这笔钱现在记在谁头上 ────────────────────────────────────────────────────────────
//
// 后端看的是【最近 30 分钟内】的会话证据（第一手），前端只做呈现。这解决的是截图里那个说不出口的
// 状态：官方那行显示着一周前的读数，而用户这周的每一次调用其实都记在别人账上——页面既没说它旧、
// 更没说钱去哪了，于是那一行看起来像坏了。
//
// 三种呈现，对应三种事实：
//   窗口内只有本厂商的流量        →「当前计费」
//   窗口内只有别家厂商的流量      →「记在 X 名下」——唯一一种点名是信息而非猜测的情形
//   窗口内多家并发（含本厂商）    → 各自挂「当前计费」，谁也不指认谁；实测一台机器一小时里
//                                   131 条 GLM 消息与 2 条 Anthropic 消息同时流淌，宣称唯一
//                                   付款方是每秒都可能翻面的假话
// 窗口外无流量 / 后端拿不到归属 → 不挂徽标。「不知道」永远不渲染成「不是」。
const billedElsewhere = (q: RuntimeQuota): string | undefined => {
  const a = q.attribution
  if (!a || a.active) return undefined
  return a.display || a.provider_id || undefined
}
interface AccountChip { text: string; cls: string; title: string }
function accountChip(q: RuntimeQuota): AccountChip | null {
  const a = q.attribution
  if (!a) return null
  if (a.active) {
    return { text: '当前计费', cls: 'live', title: '最近 30 分钟内有会话的用量记在这个账号上（并发时多个账号可同时成立）' }
  }
  const who = billedElsewhere(q)
  return who
    ? { text: `记在 ${who}`, cls: 'idle', title: `最近 30 分钟的会话经 ${who} 计费，不消耗本账号额度` }
    : null
}

// 子限额降噪：账号池永远显示；per-model 子限额（GPT-5.3-Codex-Spark 之类）只有真的用掉了才占一行。
// 判据是「这一组有话要说吗」——全 0% 的子限额没有。账号池即使 0% 也必须在，因为它是这一行的主语。
function visibleGroups(q: RuntimeQuota): QuotaGroup[] {
  const groups = quotaGroupsFor(q)
  return groups.filter((group, i) => i === 0 || (group.windows ?? []).some((w) => w.used_percent > 0))
}
const familyLabelOf = (group: QuotaGroup) => group.family_label || group.family || ''

// ── credits：厂商自己的消耗单位 ──────────────────────────────────────────────────────────────
//
// 只有官方接口给的数字才会到这里（kit/usage 拒绝本地折算——实测本地按费率卡算恒定低 2.5 倍，
// 因为 Fast 倍率不在 transcript 里）。
//
// 这里【不再显示总额度】。它曾经由「已耗 ÷ 已用%」反推，而那个除法被两个完整窗口的实测证伪：
// 上一周期跑满 100% 花了 63,025，每百分点 630.2；本周期 6% 花了 1,001，每百分点 166.9 ——
// 差 3.78 倍。于是面板上出现过「剩 4,716」，而这个账号上一周烧掉了 63,025。
// 「还剩多少」本来就有精确答案，就是上面那根百分比条；用 credits 再说一遍需要一个测不出来的
// 分母，那不是更清楚，是多编一个数。
function fmtCredits(n: number): string {
  // 「0.0」看起来像坏了；「0」是个干净的事实（这个周期还没花钱）。小数只在真的有小数时才出现。
  if (n === 0) return '0'
  if (n >= 1000) return Math.round(n).toLocaleString('en-US')
  return n >= 10 ? Math.round(n).toString() : n.toFixed(1)
}
/**
 * 本周期的已耗到底能不能给一个数。
 *
 * 厂商的日账是【按自然日结算】的，而当天那一行它还在写。窗口只有一天时（刚重置/刚开周期），
 * `used` 就 100% 由未结清的数据组成 —— 实测 2026-09-05：一个一天大的窗口报 1,153.45 credits、
 * 0 turns，同期仪表从 65% 爬到 74%。那个 1,153 不是「花得少」，是【账本还没写】。
 *
 * 给一个这样的数，比不给更糟：它带着小数点，看起来是个答案。
 */
function creditsSettlement(c: NonNullable<RuntimeQuota['credits']>): 'none' | 'partial' | 'full' {
  const days = c.days ?? 0
  const unsettled = c.unsettled_days ?? 0
  if (days > 0 && unsettled >= days) return 'none'
  return unsettled > 0 ? 'partial' : 'full'
}

/**
 * 「上一周期」还是「过去 N 天」。
 *
 * 两者数值可能一样，含义完全不同：只有观测到边界过去，那段区间才真的是一个计费周期。
 * 提前重置（codex 重置卡）会把上一周期截短，而按固定跨度往回数会伸进【再上一个】周期里 ——
 * 那不是算错了一个数，是给一段区间贴了它不配的标签。
 */
function priorLabel(c: NonNullable<RuntimeQuota['credits']>): string {
  if (c.prior_is_cycle) return '上一周期'
  // 区间长度直接由两个起点相减得出——不猜「大概是一周」，说出它实际覆盖了几天。
  const from = c.prior_window_start ? Date.parse(c.prior_window_start) : NaN
  const to = c.window_start ? Date.parse(c.window_start) : NaN
  const days = Number.isFinite(from) && Number.isFinite(to)
    ? Math.max(1, Math.round((to - from) / 86400000))
    : 0
  return days > 0 ? `此前 ${days} 天` : '此前一段'
}

function creditsTitle(c: NonNullable<RuntimeQuota['credits']>): string {
  const parts = ['已耗来自账号官方用量接口，不是本地按费率卡的估算。']
  if (c.whole_days === false) {
    parts.push('本周期从当天中途开始，而接口按自然日汇总，首日含上一周期用量——已耗偏大。')
  }
  // 当日账本会滞后结算（实测：同一批活动 17:31 报 301、17:44 报 1,001）。不说的话，用户会以为
  // 是自己看错了，或者以为面板在乱跳。
  parts.push('当日数字由厂商按自然日结算，最近几十分钟的消耗可能还没并进来。')
  const settle = creditsSettlement(c)
  if (settle === 'none') {
    parts.push('本周期的每一天厂商都还没结清，所以这里不给数字——'
      + '实测过一次一天大的窗口报 1,153 credits / 0 turns，同期仪表从 65% 涨到 74%：'
      + '那不是花得少，是账本还没写。')
  } else if (settle === 'partial') {
    parts.push(`本周期 ${c.days ?? 0} 天里有 ${c.unsettled_days} 天厂商还没结清，已耗会继续往上走。`)
  }
  if (c.prior_window) {
    const cycle = c.prior_is_cycle
    parts.push(`${priorLabel(c)}共 ${fmtCredits(c.prior_window)} credits——这是历史用量，不是本周期的额度：`
      + '厂商从不陈述额度，而 credits 与上面的百分比是两个独立计量器（实测每百分点相差 630/167/16 倍不等），'
      + '不能相除得出。'
      + (cycle ? '' : '（这段区间是按窗口长度往回数的，不一定正好是上一个计费周期——'
        + '提前重置会把周期截短，而我们没有观测到那次边界。）'))
  }
  return parts.join(' ')
}

function familyHint(group: QuotaGroup): string {
  const kinds = (group.windows ?? []).map((w) => kindLabel(w.kind)).join(' + ')
  return kinds
    ? `独立额度组：${group.family}（${kinds}）。不同组分别计数，互不覆盖。`
    : `独立额度组：${group.family}`
}
// CLI 健康属于 RUNTIME，不属于账号：同一个 codex 二进制坏了，两个订阅行会同时挂上同一句
// 「CLI 无响应」。同一句警告喊两遍不会让它更真，只会让人以为坏了两样东西。所以每个 runtime
// 只由一行来承载它——正在计费的那一行优先（那是你此刻真会受影响的地方），否则第一行。
const healthOwner = computed(() => {
  const owner = new Map<string, string>()
  for (const q of subscriptions.value) {
    const key = accountKey(q)
    if (!owner.has(q.runtime) || q.attribution?.active) owner.set(q.runtime, key)
  }
  return owner
})
const showsHealth = (q: RuntimeQuota) => healthOwner.value.get(q.runtime) === accountKey(q)

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
// Takes the two counts rather than a row, so one caller row and a whole vendor group are measured
// by the same function — a second copy for the grouped case is a second chance to get it wrong.
function cacheHitRate(cacheRead?: number, freshInput?: number): string {
  const read = cacheRead ?? 0
  const denom = read + (freshInput ?? 0)
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

// The ⟳ button asks every account that can be asked, right now. Re-reading the disk cannot always
// help — codex only writes the limit family of the model it is running, and a proxied session
// writes no limits at all — so polling alone can be arbitrarily far behind.
//
// It no longer COSTS anything: the probe went from a real inference request to a plain read, which
// is also why a background timer now keeps these numbers warm (usage_credentials.go). So this
// button means "don't wait for the timer", not "spend some quota to find out".
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
          <div
            v-for="q in subscriptions"
            :key="accountKey(q)"
            class="uchip-rt"
            :class="{ 'is-billing': q.attribution?.active, 'is-idle': !!billedElsewhere(q) }"
          >
            <div class="uchip-rt-head">
              <span class="uchip-rt-name">{{ accountLabel(q) }}</span>
              <span v-if="q.plan" class="uchip-plan">{{ q.plan }}</span>
              <!-- 一眼看出这笔钱现在记在谁头上。这是整页最贵的一个事实：截图里官方那行挂着
                   一周前的读数，而本周每一次调用其实都记在另一个账号上，页面从来没说过。 -->
              <span
                v-if="accountChip(q)"
                class="uchip-acct"
                :class="accountChip(q)!.cls"
                :title="accountChip(q)!.title"
                :data-testid="`uchip-acct-${accountKey(q)}`"
              >{{ accountChip(q)!.text }}</span>
              <span
                v-if="healthLabel(q) && showsHealth(q)"
                class="uchip-badge warn"
                :title="q.health?.reason"
              >{{ healthLabel(q) }}</span>
            </div>

            <div
              v-for="(group, groupIndex) in visibleGroups(q)"
              :key="group.family || group.snapshot?.captured_at || groupIndex"
              class="uchip-group"
              :class="{ 'is-stale': staleOf(q, group).dim }"
            >
              <!-- 主组（账号池）不再挂 family 标签：它就是这一行的主语，重复一遍只是噪音。
                   子限额才需要自报家门，且用厂商给的名字（GPT-5.3-Codex-Spark），不是合并用的 id。 -->
              <div v-if="(groupIndex > 0 && familyLabelOf(group)) || staleOf(q, group).badge" class="uchip-group-head">
                <span v-if="groupIndex > 0" class="uchip-plan uchip-family" :title="familyHint(group)">{{ familyLabelOf(group) }}</span>
                <!-- 「数据已过期」本身就是动作：点它直接向账号查询（probe，与 codex /status 同源）。
                     只说问题不给出路，等于把诊断丢回给用户。 -->
                <button
                  v-if="staleOf(q, group).badge"
                  type="button"
                  class="uchip-badge stale is-action"
                  :class="{ 'is-quiet': !!billedElsewhere(q) }"
                  :title="staleOf(q, group).hint"
                  :data-testid="`uchip-stale-${accountKey(q)}`"
                  :disabled="loading"
                  @click.stop="refresh"
                >{{ staleOf(q, group).badge }}<span class="uchip-stale-go">↻</span></button>
              </div>
              <!-- 折叠档：既过期、值又全是"推断"（窗口早已重置、此后零上报）——那个 100% 是缺省
                   猜测而非事实，不配占一整行绿条。 -->
              <div
                v-if="staleOf(q, group).collapse"
                class="uchip-dim uchip-note"
                :data-testid="`uchip-collapsed-${accountKey(q)}`"
              >{{ staleOf(q, group).note }}</div>
              <!-- key by INDEX: duplicate kinds are a domain contradiction; the backend drops
                   them before they reach this list. -->
              <div v-for="(w, i) in (staleOf(q, group).collapse ? [] : group.windows)" :key="i" class="uchip-win">
                <span class="uchip-win-k">{{ kindLabel(w.kind) }}</span>
                <span class="uchip-bar"><span class="uchip-bar-fill" :style="{ width: w.remaining_percent + '%' }" :class="'lvl-' + (w.remaining_percent < 15 ? 'crit' : w.remaining_percent < 40 ? 'warn' : 'ok')" /></span>
                <span class="uchip-win-p">{{ Math.round(w.remaining_percent) }}%</span>
                <span v-if="w.inferred" class="uchip-win-r uchip-inferred" title="窗口已重置，且此后运行时未上报任何用量 ⟹ 未使用。此值为推断，非实测。">已重置 · 推断</span>
                <span v-else class="uchip-win-r">{{ fmtReset(w.reset_at) }}</span>
              </div>
              <div v-if="group.snapshot && !staleOf(q, group).collapse" class="uchip-dim uchip-note">
                额度更新于 {{ fmtAt(group.snapshot.captured_at) }}
                <span v-if="sourceLabel(group.snapshot.source)" class="uchip-src">· {{ sourceLabel(group.snapshot.source) }}</span>
              </div>
            </div>

            <!-- 厂商自己的消耗单位。百分比回答「还剩多少」，credits 回答「花了多少」——两个不同的
                 问题，所以两行。只有厂商真有这个单位时才出现（Kimi 按窗口百分比计，就没有这一行，
                 硬造一个才是把订阅和 API 计费搅在一起）。 -->
            <div v-if="q.credits" class="uchip-credits" :title="creditsTitle(q.credits)">
              <span class="uchip-credits-k">本周期已耗</span>
              <!-- 一天都没结清 ⟹ 不给数字。给一个带小数点的 1,153 比空着更糟：它看起来是个答案，
                   而实测那正是「账本还没写」的样子（1,153 credits / 0 turns，同期仪表 65%→74%）。 -->
              <span v-if="creditsSettlement(q.credits) === 'none'" class="uchip-credits-pending">
                账本尚未结算
              </span>
              <span v-else class="uchip-credits-v">
                {{ fmtCredits(q.credits.used) }}<span class="uchip-credits-u"> credits</span>
                <span v-if="q.credits.whole_days === false" class="uchip-credits-approx" title="窗口从当天中途开始，接口按自然日汇总，首日含上一周期用量">≈</span>
                <span
                  v-if="creditsSettlement(q.credits) === 'partial'"
                  class="uchip-credits-approx"
                  :title="`本周期 ${q.credits.days} 天里有 ${q.credits.unsettled_days} 天厂商还没结清，这个数还会往上走`"
                >+</span>
              </span>
              <!-- 「上一周期」下沉成缩进的二级行。它此前与本周期同单位、同一行、右对齐——
                   版式本身就在说「1.2k / 134.4k」，而这两个数一个未结清、一个已结清，
                   甚至可能不是同一个计费周期。tooltip 赢不了版式，所以改版式。 -->
            </div>
            <div v-if="q.credits && q.credits.prior_window" class="uchip-credits-prior" :title="creditsTitle(q.credits)">
              <span class="uchip-credits-prior-k">└ {{ priorLabel(q.credits) }}</span>
              <span class="uchip-credits-prior-v">{{ fmtCredits(q.credits.prior_window) }} credits</span>
              <span v-if="!q.credits.prior_is_cycle" class="uchip-credits-prior-note">按窗口长度回溯，未必是完整周期</span>
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
          <!-- ── 官方订阅：一行一个账号（厂商 × 端点）。名字与上面的额度区同源，两处不该各叫各的 ── -->
          <template v-if="tab === 'sub'">
            <div v-for="row in subProviders" :key="`${row.vendor}:${row.runtime}:${row.runtime_provider ?? ''}:${row.billing_mode}`" class="uchip-prov">
              <div class="uchip-prov-head">
                <span class="uchip-prov-name">{{ accountRowLabel(row) }}</span>
                <span v-if="viaLabel(row)" class="uchip-prov-via">{{ viaLabel(row) }}</span>
                <span class="uchip-badge" :class="BADGES[semanticsOf(row)].cls" :title="BADGES[semanticsOf(row)].title">{{ BADGES[semanticsOf(row)].text }}</span>
              </div>
              <div class="uchip-prov-glance">
                <span class="uchip-prov-cost">{{ fmtCost(row.cost, row.currency, rowIncomplete(row)) }}</span>
                <!-- 「—」旁边必须有一句解释，否则用户只会当成面板坏了。 -->
                <span v-if="facadeNote(row)" class="uchip-prov-noprice" :title="facadeNote(row)!.title">{{ facadeNote(row)!.label }}</span>
                <span class="uchip-prov-tok">{{ fmtTokens(row.total_tokens) }} tok</span>
                <span class="uchip-prov-hit">缓存命中 {{ cacheHitRate(row.cache_read_tokens, row.input_tokens) }}</span>
              </div>
              <div class="uchip-prov-bd">
                <span>↓{{ fmtTokens(row.input_tokens) }} / ↑{{ fmtTokens(row.output_tokens) }}</span>
                <span>缓存读{{ fmtTokens(row.cache_read_tokens) }} / 写{{ fmtTokens(row.cache_create_tokens) }}</span>
              </div>
              <div class="uchip-prov-ctx">
                <span class="uchip-prov-model">{{ row.top_model || '—' }}</span>
                <Spark :bars="row.spark ?? []" />
              </div>
              <!-- 订阅这一区同样是钱（按 API 价折算的等价值），同样要能被核对。此前单价只印在
                   API tab，而这台机器的 API tab 是空的——于是最大的几笔金额旁边一个单价都没有。 -->
              <div v-if="unitPriceLine(row.unit_prices ?? [])" class="uchip-dim uchip-unitprice"
                   :title="unitPriceTitle(row.top_model ?? '', row.unit_prices ?? [])">
                {{ unitPriceLine(row.unit_prices ?? []) }}
              </div>
            </div>
          </template>

          <!-- ── API 计费：主行=厂商（欠谁钱），子行=调用方（谁替我花的）── -->
          <template v-else>
            <div v-for="group in apiVendors" :key="group.vendor || '__unknown__'" class="uchip-prov">
              <div class="uchip-prov-head">
                <span class="uchip-prov-name" :class="{ 'is-unknown': !group.vendor }" :title="vendorTitle(group)">{{ vendorLabel(group) }}</span>
                <!-- 单一调用方时就地说明「经由谁」；多个才值得展开成子行（展开一行等于把主行抄一遍）。 -->
                <span v-if="group.rows.length === 1" class="uchip-prov-via">经由 {{ viaLabel(group.rows[0]) || runtimeLabel(group.rows[0].runtime) }}</span>
                <!-- 钱的「种类」徽标只在真有钱时才成立。一行金额是「—」却挂着「估算」，是在声称一个
                     并不存在的估算——旁边的「无价表」已经把这件事说完了。一个事实一个信号。 -->
                <span v-if="group.pricedRequests > 0" class="uchip-badge" :class="BADGES[vendorSemantics(group)].cls" :title="BADGES[vendorSemantics(group)].title">{{ BADGES[vendorSemantics(group)].text }}</span>
              </div>
              <div class="uchip-prov-glance">
                <span class="uchip-prov-cost">{{ fmtCost(group.cost, group.currency, !group.costComplete) }}</span>
                <!-- 「—」旁边必须有一句解释。不解释的空值，用户只会当成坏了。 -->
                <span v-if="groupFacadeNote(group)" class="uchip-prov-noprice" :title="groupFacadeNote(group)!.title">{{ groupFacadeNote(group)!.label }}</span>
                <span v-else-if="group.pricedRequests === 0" class="uchip-prov-noprice" title="这些 model 不在内置价表里。宁可不给数字，也不拿同厂商的近似单价蒙一个——静默错数比空值糟。">无价表</span>
                <span class="uchip-prov-tok">{{ fmtTokens(group.totalTokens) }} tok</span>
                <span class="uchip-prov-hit">缓存命中 {{ cacheHitRate(group.cacheReadTokens, group.inputTokens) }}</span>
              </div>
              <div class="uchip-prov-bd">
                <span>↓{{ fmtTokens(group.inputTokens) }} / ↑{{ fmtTokens(group.outputTokens) }}</span>
                <span>缓存读{{ fmtTokens(group.cacheReadTokens) }} / 写{{ fmtTokens(group.cacheCreateTokens) }}</span>
              </div>
              <div v-if="group.rows.length > 1" class="uchip-callers">
                <div v-for="row in group.rows" :key="`${row.runtime}:${row.billing_mode}`" class="uchip-caller">
                  <span class="uchip-caller-name">{{ runtimeLabel(row.runtime) }}</span>
                  <span class="uchip-caller-tok">{{ fmtTokens(row.total_tokens) }} tok</span>
                  <span class="uchip-caller-cost">{{ fmtCost(row.cost, row.currency, rowIncomplete(row)) }}</span>
                </div>
              </div>
              <div class="uchip-prov-ctx">
                <!-- 认不出厂商时，这一格是用户唯一能动手的地方：要么给出 model id（可以去查、可以
                     加进厂商表），要么说清 model id 压根没被记下来。一个「—」两种含义，等于没说。 -->
                <span class="uchip-prov-model" :class="{ 'is-note': !group.topModel }">{{ group.topModel || 'model id 未记录' }}</span>
                <Spark :bars="group.spark" />
              </div>
              <!-- 价格会变，内置表不会自己知道。所以它公布自己的年龄——这是读的人唯一的防线。 -->
              <!-- 单价写出来，金额才可被核对。厂商有两套官方价时并列，绝不做汇率换算。 -->
              <div v-if="unitPriceLine(group.unitPrices)" class="uchip-dim uchip-unitprice" :title="unitPriceTitle(group.topModel, group.unitPrices)">
                {{ unitPriceLine(group.unitPrices) }}
              </div>
              <div v-if="group.priceVerifiedAt" class="uchip-dim uchip-priceage" title="内置价表最后一次与厂商价目页核对的日期（取本行最旧的一条）。此后厂商若调价，这个数字会偏。">
                价表核对于 {{ group.priceVerifiedAt }}
              </div>
            </div>
          </template>

          <div v-if="!tabHasRows" class="uchip-dim uchip-empty">
            {{ tab === 'sub' ? '该窗口暂无官方订阅用量' : '本窗口没有 API 计费用量' }}
          </div>
          <div v-else-if="tab === 'api'" class="uchip-foot">按内置价表估算，不是厂商账单。经中转/合约价使用时，实际单价可能与厂商标价不同。</div>
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
/* 未在计费的账号整行降一档。它不是坏了——只是现在不是它在花钱。降权而不是隐藏：正因为你在
   别处花钱，这条「还剩多少、何时重置」才是你决定什么时候切回来的依据。 */
.uchip-rt.is-idle { opacity: 0.82; }
.uchip-rt-head { display: flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 600; margin-bottom: 4px; }
.uchip-rt-name { color: #e6e8ec; }
/* 归属 chip 紧跟账号名，不靠右——它是名字的一部分（"哪个账号，以及它现在算不算数"），
   不是行尾的状态角标。绿=正在发生；中性灰=陈述，不是警告（那一行没有任何东西需要你处理）。 */
.uchip-acct { font-size: 9.5px; font-weight: 500; border-radius: 4px; padding: 1px 5px; cursor: help; }
.uchip-acct.live { color: #4ade80; background: rgba(74,222,128,0.12); }
.uchip-acct.idle { color: #9aa0aa; background: rgba(154,160,170,0.12); }

/* credits：厂商自己的消耗单位。百分比答「还剩多少」，这一行答「花了多少」——把它排在额度条
   下面、用同一套右对齐数字，两个问题一次读完，不必切页。 */
.uchip-credits { display: flex; align-items: baseline; gap: 6px; font-size: 11px; margin-top: 4px; cursor: help; }
.uchip-credits-k { color: #8b909a; font-size: 10.5px; flex-shrink: 0; }
.uchip-credits-v { color: #e6e8ec; font-weight: 600; font-variant-numeric: tabular-nums; }
.uchip-credits-u { color: #8b909a; font-weight: 400; font-size: 10px; }
.uchip-credits-approx { color: #f59e0b; margin-left: 2px; }
.uchip-credits-of { color: #7f858f; font-size: 10.5px; font-variant-numeric: tabular-nums; margin-left: auto; }

/* 未结清：不是一个数，所以不用数字的排版（无 tabular-nums、非加粗），别让它读起来像个值。 */
.uchip-credits-pending { color: #8b909a; font-size: 11px; font-style: italic; }

/* 「上一周期/此前 N 天」自己一行、缩进、更暗。
   它此前与本周期同行同单位右对齐，版式本身在说「1.2k / 134.4k」——而那两个数一个未结清、
   一个已结清，甚至可能不属于同一个计费周期。这里靠层级而不是靠 tooltip 来阻止那个读法。 */
.uchip-credits-prior {
  display: flex; align-items: baseline; gap: 6px;
  font-size: 10.5px; margin-top: 2px; padding-left: 10px; cursor: help;
}
.uchip-credits-prior-k { color: #6f757f; flex-shrink: 0; }
.uchip-credits-prior-v { color: #9aa0aa; font-variant-numeric: tabular-nums; }
.uchip-credits-prior-note { color: #6f757f; margin-left: auto; }
.uchip-group { padding: 4px 0 5px; }
.uchip-group + .uchip-group { border-top: 1px dashed #2a2d35; }
/* 过期读数必须**看起来就是旧的**。此前只降了一点整体不透明度（0.72），主体仍是满格实心绿条 +
   大号百分比 —— 视觉权重和新鲜读数几乎没差别，于是角落那句"已过期"没人看见（Human 实测：
   "这个额度一直不对"，而它其实一直如实标着过期）。
   现在：实心条改成**描边空心**（一眼看出这不是实测量），数字转中性灰。数字仍然显示——它曾经
   是真的，只是老了，藏起来反而是另一种不诚实。 */
.uchip-group.is-stale { opacity: 0.72; }
.uchip-group.is-stale .uchip-bar-fill {
  background: transparent !important;
  border: 1px dashed currentColor;
  opacity: 0.65;
}
.uchip-group.is-stale .uchip-win-p { color: hsl(var(--muted-foreground)); font-weight: 500; }

/* 「数据已过期」是按钮，不是标签：给它按钮该有的意符。 */
.uchip-badge.stale.is-action { border: none; cursor: pointer; display: inline-flex; align-items: center; gap: 3px; }
.uchip-badge.stale.is-action:hover:not(:disabled) { color: #e5e7eb; background: rgba(154,160,170,0.24); }
.uchip-badge.stale.is-action:disabled { cursor: progress; opacity: 0.6; }
/* 已经知道原因（你在用别的账号）时，这枚徽标只是个「想看新数就点」的入口，不该有提示的分量。 */
.uchip-badge.stale.is-action.is-quiet { opacity: 0.7; font-weight: 400; }
.uchip-stale-go { font-size: 10px; line-height: 1; }
.uchip-group-head { display: flex; align-items: center; gap: 6px; min-height: 17px; margin-bottom: 1px; }
.uchip-plan { font-size: 10px; color: #8b909a; font-weight: 400; }
.uchip-badge { font-size: 9.5px; font-weight: 500; border-radius: 4px; padding: 1px 5px; margin-left: auto; }
.uchip-badge + .uchip-badge { margin-left: 4px; }
/* Amber, never red: red reads as "quota exhausted", which is a different (and false) claim. */
.uchip-badge.warn { color: #f59e0b; background: rgba(245,158,11,0.12); }
.uchip-badge.stale { color: #9aa0aa; background: rgba(154,160,170,0.12); }
.uchip-badge.api { color: #4ade80; background: rgba(74,222,128,0.12); }
/* 「估算」= 这笔钱真的花了，但 transcript 没记计费方式。它比「实付」弱、比「≈等价」强，所以既不能
   借绿色（那是「已证实付」），也不能借琥珀色（那在这套配色里是「需要你处理」）。中性灰=「读数本身
   有保留」，与 .stale 同一族语义，正确。 */
.uchip-badge.est { color: #9aa0aa; background: rgba(154,160,170,0.12); }
.uchip-badge.eq { color: #a5b4fc; background: rgba(165,180,252,0.12); }
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

/* ── 厂商主行 / 调用方子行 ─────────────────────────────────────────────────────
   子行的字号与灰度**必须**低于主行：视线该先落在「欠谁钱」，再往下看「谁花的」。
   把它做得跟主行一样重，等于又把两个轴压回一个平面——正是这次要修的东西。 */
.uchip-prov-via { font-size: 9.5px; color: #7f858f; }
/* 未知厂商不是错误，是一个待回答的问题：中性色 + 说明性 tooltip，不用红。 */
.uchip-prov-name.is-unknown { color: #9aa0aa; font-weight: 500; cursor: help; }
/* 「无价表」紧贴在「—」右边——解释必须挨着被解释的东西，隔一段就没人把它们连起来读。 */
/* 「model id 未记录」是一句说明，不是一个 model 名——排版上要看得出区别，否则会被当成模型叫这个。 */
.uchip-prov-model.is-note { font-style: italic; color: #6f757f; }
.uchip-prov-noprice {
  font-size: 9.5px; color: #8b909a; background: rgba(154,160,170,0.12);
  border-radius: 4px; padding: 1px 5px; cursor: help; align-self: center;
}
.uchip-callers {
  display: grid; gap: 2px; margin: 1px 0 5px; padding-left: 8px;
  border-left: 1px solid #262a32;
}
.uchip-caller {
  display: grid; grid-template-columns: minmax(0,1fr) auto auto; gap: 8px;
  align-items: baseline; font-size: 10px; color: #8b909a; font-variant-numeric: tabular-nums;
}
.uchip-caller-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.uchip-caller-cost { color: #c9cdd5; min-width: 52px; text-align: right; }
.uchip-priceage { font-size: 9px; margin-top: 3px; cursor: help; }
/* 单价比「价表核对于」重要一档——它是让上面那个金额可被核对的东西，所以给它稍高的对比度。 */
.uchip-unitprice { font-size: 9.5px; margin-top: 4px; color: #8b909a; cursor: help; font-variant-numeric: tabular-nums; }

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
