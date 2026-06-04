/**
 * useAgentIntel — Agent intelligence for terminal sessions.
 *
 * Receives agent state via WebSocket control messages (type: "agent_state")
 * pushed by the backend, eliminating the need for a separate SSE connection.
 * Falls back to a one-shot GET snapshot on mount for initial state.
 *
 * Connection budget: 0 additional connections (piggybacks on existing WS).
 *
 * [Ref: TH-0501-m9j, 铁律 v2.0 Rule 1+2]
 */
import { ref, onMounted, onUnmounted } from 'vue'
import type { AgentState, AgentIntelResponse } from '@/types/terminal'
import { useCliAuth } from '@/composables/cli/useCliAuth'
import { cliApi } from '@/composables/cli/useCliApiPrefix'

export function useAgentIntel(sessionId: () => string) {
  const agentState = ref<AgentState | null>(null)
  const notifications = ref<AgentState[]>([])
  const connected = ref(false)
  const { cliFetch } = useCliAuth()

  let lastHash = ''
  let stopped = false

  function apply(data: AgentIntelResponse) {
    const h = quickHash(data)
    if (h === lastHash) return
    lastHash = h
    agentState.value = data.current ?? null
    notifications.value = data.notifications ?? []
    connected.value = true
  }

  /** One-shot GET snapshot — used on mount for initial state. */
  async function fetchSnapshot() {
    const id = sessionId()
    if (!id) return
    try {
      const resp = await cliFetch(cliApi(`/sessions/${id}/agent-state`))
      if (resp.ok) {
        apply(await resp.json() as AgentIntelResponse)
      }
    } catch { /* endpoint may not exist yet */ }
  }

  /**
   * Handle a WebSocket control message. Called by the terminal page
   * when it receives a text frame with type "agent_state".
   */
  function handleWSMessage(payload: unknown) {
    if (stopped) return
    try {
      apply(payload as AgentIntelResponse)
    } catch { /* malformed payload */ }
  }

  function stop() {
    stopped = true
    connected.value = false
  }

  onMounted(() => {
    fetchSnapshot() // immediate snapshot while WS agent_state messages arrive
  })

  onUnmounted(stop)

  return { agentState, notifications, connected, handleWSMessage }
}

/** Quick hash of response data to detect changes without deep comparison. */
function quickHash(data: AgentIntelResponse): string {
  const c = data.current
  const nLen = data.notifications?.length ?? 0
  let h = `${c?.tool}|${c?.status}|${c?.waitReason}|${c?.totalTokens}|${c?.tmuxWindow}|${nLen}`
  if (data.notifications) {
    for (const n of data.notifications) {
      h += `|${n.tmuxWindow}:${n.status}`
    }
  }
  return h
}
