<script setup lang="ts">
/**
 * SessionOverviewTab — terminal 会话总览 wrapper (CHG-016).
 *
 * The HOST half of the shared single-session overview: it owns the data fetch
 * (GET /sessions/{id}/overview via authed cliFetch) and feeds the metrics bag
 * to the pure, host-agnostic @ce canvas <SessionOverviewPane>. All rendering,
 * derived-metric and honesty logic lives in the @ce pane (SSOT) — this wrapper
 * adds ONLY fetch + ~3s poll + mount/unmount lifecycle.
 *
 * Empty/loading is handled gracefully: until the first response lands the pane
 * shows its loading affordance; a session with no transcript yet (turn_count 0)
 * still renders a valid (mostly「—」) overview rather than blanking.
 */
import { ref, watch, onMounted, onBeforeUnmount } from 'vue'
import SessionOverviewPane from '@ce/components/overview/SessionOverviewPane.vue'
import { sessionOverview } from '@terminal/api/overview'
import type { SessionDetail, TurnsSummary, Turn, UnitPrice } from '@ce/types/sessionMetrics'
import type { AgentTool } from '@terminal/types/terminal'

// cwd + tool are the drawer's EFFECTIVE pane working dir + agent, OWNED by ResourceDrawer: in
// FOLLOW mode they track the live active pane; once the user LOCKS the drawer they freeze, so a
// main-area pane switch no longer yanks the metrics the user is reading. The overview just
// consumes them as props + re-fetches when they change. active mirrors the drawer's `open`: the
// 3s poll runs only while the drawer is open (no wasted fetches behind a minimized drawer), but
// the already-fetched data is kept.
const props = defineProps<{ sessionId: string; cwd: string; tool: AgentTool; active: boolean }>()

const detail = ref<SessionDetail | null>(null)
const summary = ref<TurnsSummary | null>(null)
const turns = ref<Turn[]>([])
const price = ref<UnitPrice | null>(null)
// loading is true only until the FIRST response of the current session lands —
// the 3s poll refreshes silently so the pane never flashes its loading state.
const loading = ref(true)

let timer: ReturnType<typeof setInterval> | null = null
const POLL_MS = 3000
// Monotonic request id: only the LATEST refresh may apply its response, so an out-of-order
// landing (the active cwd/tool settles on mobile while several fetches are in flight) can't
// clobber newer state. Bumped on every refresh + on session switch (invalidates in-flight).
let seq = 0

function clearData(): void {
  detail.value = null
  summary.value = null
  turns.value = []
  price.value = null
}
// "Has real data": a non-empty transcript that resolved a model or any tokens.
function hasData(): boolean {
  const d = detail.value
  if (!d || (d.turn_count ?? 0) === 0) return false
  return !!d.model_id || ((summary.value?.input_tokens ?? 0) + (summary.value?.output_tokens ?? 0)) > 0
}
// An "uninformative" response: no turns, or a transcript with neither a model nor any
// tokens — what a transient wrong-cwd / empty-codex-rollout resolution returns.
function bagEmpty(bag: Awaited<ReturnType<typeof sessionOverview>>): boolean {
  const d = bag.detail
  if (!d || (d.turn_count ?? 0) === 0) return true
  return !d.model_id && ((bag.summary?.input_tokens ?? 0) + (bag.summary?.output_tokens ?? 0)) === 0
}

async function refresh(): Promise<void> {
  if (!props.sessionId) {
    clearData()
    loading.value = false
    return
  }
  const mine = ++seq
  // Pass the ANCHORED pane's cwd AND agentTool so the server routes to the codex-vs-claude
  // metrics extractor for the pane the drawer is anchored to (null-safe: '' → claude).
  const bag = await sessionOverview(props.sessionId, props.cwd, props.tool)
  if (mine !== seq) return // superseded by a newer refresh
  // Don't replace a data-rich overview with a transient empty one: while the tmux state
  // settles on mobile the active pane can momentarily resolve to a barely-used transcript
  // or the wrong (near-empty) codex rollout, which would flash the real numbers to 0. A
  // genuinely empty session starts empty (hasData() false) and shows the empty shape fine.
  if (bagEmpty(bag) && hasData()) {
    loading.value = false
    return
  }
  detail.value = bag.detail
  summary.value = bag.summary
  turns.value = bag.turns ?? []
  price.value = bag.price ?? null
  loading.value = false
}

// startPoll only arms the 3s timer when the drawer is ACTIVE (open). A minimized drawer
// keeps its last-fetched data but spends no network — re-opening re-arms the poll + does
// one immediate catch-up refresh.
function startPoll(): void {
  stopPoll()
  if (!props.active) return
  timer = setInterval(() => { void refresh() }, POLL_MS)
}
function stopPoll(): void {
  if (timer) { clearInterval(timer); timer = null }
}

// Session switch → drop the previous session's data up-front (so the skip-empty guard in
// refresh() can't preserve it for the new session), reset to loading, re-fetch, restart poll.
watch(() => props.sessionId, () => {
  seq++
  clearData()
  loading.value = true
  if (props.active) void refresh()
  startPoll()
})

// Effective cwd OR tool change (followed pane switched, or the user locked/unlocked) →
// re-fetch immediately so the overview tracks it, without waiting for the 3s poll. Only active.
watch(() => [props.cwd, props.tool], () => { if (props.active) void refresh() })

// Drawer minimize/restore → pause/resume the poll. On restore do one immediate catch-up so
// the user doesn't stare at stale numbers for up to 3s; on minimize just stop the timer
// (data stays). A toggle never clears the data, so re-opening shows the prior overview at once.
watch(() => props.active, (isActive) => {
  if (isActive) { void refresh(); startPoll() }
  else stopPoll()
})

onMounted(() => {
  if (props.active) void refresh()
  startPoll()
})
onBeforeUnmount(() => {
  stopPoll()
})
</script>

<template>
  <SessionOverviewPane
    :detail="detail"
    :summary="summary"
    :turns="turns"
    :price="price"
    :loading="loading"
  />
</template>
