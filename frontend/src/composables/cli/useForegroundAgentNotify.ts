/**
 * useForegroundAgentNotify — the open-but-unfocused-tab half of the notify story.
 *
 * This covers the case where a tab IS open but the user is looking elsewhere
 * (document.hidden): we watch the pushed tmux_state frames for any pane whose
 * agentStatus transitions INTO `waiting`, and — only then, only if the document is
 * hidden and Notification permission is already granted — raise a local Notification.
 *
 * Self-contained: it depends only on the standard `Notification` API (checked inline),
 * NOT on any service-worker / Web-Push plumbing — the app raising a purely local
 * notification is orthogonal to whether the browser can do Web Push at all.
 *
 * Dedupe + no-double-fire discipline:
 *  - Per-pane edge detection: fire only on running/idle/… → waiting, never on a
 *    pane that was already waiting (prevents repeat-fire on every 1s diff frame).
 *    The priming/edge/pruning rules are NOT restated here — they come from the shared
 *    `statusEdgeDetector`, the same primitive the attention HUD observes windows with.
 *  - tag === `dw-agent-${sessionId}` coalesces repeat notifications for the same
 *    session onto one OS notification instead of stacking several.
 */
import { watch, onUnmounted } from 'vue'
import type { TmuxState } from '@terminal/types/terminal'
import { useTmuxState } from '@terminal/composables/cli/useTmuxState'
import { createStatusEdgeDetector } from '@terminal/composables/cli/statusEdgeDetector'

interface PaneKey { window: number; pane: number }
function keyOf(k: PaneKey): string { return `${k.window}:${k.pane}` }

export function useForegroundAgentNotify(sessionId: () => string): void {
  const tmux = useTmuxState(sessionId)
  // Per-pane status memory. Shared implementation (one definition of priming/edge/pruning);
  // this instance is ours alone, keyed on pane × raw agentStatus.
  const edges = createStatusEdgeDetector<string>()

  function collect(state: TmuxState | null): Map<string, { status: string; tool?: string; title?: string }> {
    const out = new Map<string, { status: string; tool?: string; title?: string }>()
    for (const s of state?.sessions ?? []) {
      for (const w of s.windows ?? []) {
        for (const p of w.panes ?? []) {
          out.set(keyOf({ window: w.index, pane: p.index }), {
            status: p.agentStatus ?? 'none',
            tool: p.agentTool,
            title: p.title || w.name,
          })
        }
      }
    }
    return out
  }

  function notifyWaiting(label: string): void {
    // Only the open-but-unfocused case is ours; a focused tab needs no nudge.
    if (typeof document === 'undefined' || !document.hidden) return
    // Inlined support + permission gate (decoupled from the removed push composable):
    // raise only when the Notification API exists and the user already granted permission.
    if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
    try {
      const sid = sessionId()
      const n = new Notification('Agent 等待输入', {
        body: label ? `${label} 需要你的确认` : '一个 agent 正在等待你的输入',
        tag: sid ? `dw-agent-${sid}` : 'dw-agent',
        // Local notifications can't carry SW routing data, but focusing the tab is
        // enough here (the tab is already on the right session).
        icon: '/pwa-192.png',
      })
      n.onclick = () => { window.focus(); n.close() }
    } catch { /* permission revoked mid-session / quota — ignore */ }
  }

  const stop = watch(
    () => tmux.state.value,
    (state) => {
      const current = collect(state)
      const statuses = new Map<string, string>()
      for (const [k, v] of current) statuses.set(k, v.status)
      // The detector seeds its baseline on the first frame, so panes that were ALREADY
      // waiting when the tab connected are stale rather than new edges, and it forgets
      // panes that disappeared so a re-created pane re-arms cleanly.
      for (const edge of edges.diff(statuses)) {
        if (edge.to !== 'waiting') continue
        const v = current.get(edge.key)
        if (!v) continue
        notifyWaiting(v.tool ? `${v.tool}（${v.title ?? ''}）`.trim() : (v.title ?? ''))
      }
    },
    { deep: true },
  )

  onUnmounted(stop)
}
