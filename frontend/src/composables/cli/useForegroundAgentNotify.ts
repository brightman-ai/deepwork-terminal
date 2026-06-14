/**
 * useForegroundAgentNotify — the open-but-unfocused-tab half of the notify story.
 *
 * Backend Web Push covers the NO-tab case. This covers the case where a tab IS open
 * but the user is looking elsewhere (document.hidden): we watch the pushed tmux_state
 * frames for any pane whose agentStatus transitions INTO `waiting`, and — only then,
 * only if the document is hidden and Notification permission is granted — raise a
 * local Notification.
 *
 * Dedupe + no-double-fire discipline:
 *  - Per-pane edge detection: fire only on running/idle/… → waiting, never on a
 *    pane that was already waiting (prevents repeat-fire on every 1s diff frame).
 *  - tag === `dw-agent-${sessionId}` matches the SW push tag, so if the backend
 *    push lands too the OS collapses them onto one notification instead of two.
 */
import { watch, onUnmounted } from 'vue'
import type { TmuxState } from '@/types/terminal'
import { useTmuxState } from '@/composables/cli/useTmuxState'
import { usePushNotifications } from '@/composables/cli/usePushNotifications'

interface PaneKey { window: number; pane: number }
function keyOf(k: PaneKey): string { return `${k.window}:${k.pane}` }

export function useForegroundAgentNotify(sessionId: () => string): void {
  const tmux = useTmuxState(sessionId)
  const push = usePushNotifications()
  // Last-seen status per pane — the edge detector's memory.
  const lastStatus = new Map<string, string>()
  let primed = false

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
    if (push.permission.value !== 'granted') return
    if (typeof Notification === 'undefined') return
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
      // First frame just seeds the baseline so we don't fire for panes that were
      // ALREADY waiting when the tab connected (those are stale, not new edges).
      if (!primed) {
        for (const [k, v] of current) lastStatus.set(k, v.status)
        primed = true
        return
      }
      for (const [k, v] of current) {
        const prev = lastStatus.get(k)
        if (v.status === 'waiting' && prev !== 'waiting') {
          notifyWaiting(v.tool ? `${v.tool}（${v.title ?? ''}）`.trim() : (v.title ?? ''))
        }
        lastStatus.set(k, v.status)
      }
      // Forget panes that disappeared so a re-created pane re-arms cleanly.
      for (const k of [...lastStatus.keys()]) if (!current.has(k)) lastStatus.delete(k)
    },
    { deep: true },
  )

  onUnmounted(stop)
}
