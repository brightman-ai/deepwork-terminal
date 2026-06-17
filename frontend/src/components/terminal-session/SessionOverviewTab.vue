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
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import type { SessionDetail, TurnsSummary, Turn } from '@ce/types/sessionMetrics'

const props = defineProps<{ sessionId: string }>()

// Live active-pane cwd so the overview follows tmux pane/window switches (server falls
// back to the session's creation cwd when this is empty).
const tmux = useTmuxState(() => props.sessionId)

const detail = ref<SessionDetail | null>(null)
const summary = ref<TurnsSummary | null>(null)
const turns = ref<Turn[]>([])
// loading is true only until the FIRST response of the current session lands —
// the 3s poll refreshes silently so the pane never flashes its loading state.
const loading = ref(true)

let timer: ReturnType<typeof setInterval> | null = null
const POLL_MS = 3000

async function refresh(): Promise<void> {
  if (!props.sessionId) {
    detail.value = null
    summary.value = null
    turns.value = []
    loading.value = false
    return
  }
  const bag = await sessionOverview(props.sessionId, tmux.activeCwd.value)
  detail.value = bag.detail
  summary.value = bag.summary
  turns.value = bag.turns ?? []
  loading.value = false
}

function startPoll(): void {
  stopPoll()
  timer = setInterval(() => { void refresh() }, POLL_MS)
}
function stopPoll(): void {
  if (timer) { clearInterval(timer); timer = null }
}

// Session switch → reset to loading, re-fetch, restart the poll clock.
watch(() => props.sessionId, () => {
  loading.value = true
  void refresh()
  startPoll()
})

// Active-pane cwd change (user switched tmux pane/window) → re-fetch immediately so the
// overview tracks what the user is now looking at, without waiting for the 3s poll.
watch(() => tmux.activeCwd.value, () => { void refresh() })

onMounted(() => {
  void refresh()
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
    :loading="loading"
  />
</template>
