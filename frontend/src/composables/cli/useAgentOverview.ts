/**
 * useAgentOverview — the Agent Overview's derived state.
 *
 * The single place the seen-state STATE MACHINE lives; the pane bar renders a compact roll-up of
 * it and the overview grid the full view. Built from the pushed tmux_state (per-window
 * panes/agentStatus/tail) plus a small client-local "seen" layer.
 *
 * Status model (the herdr-inspired `seen` dimension):
 *   waiting      — a pane needs your input (highest signal)          → red,  top
 *   running      — an agent is actively working                      → green
 *   done-unseen  — went idle AFTER you last looked (finished, unread)→ teal
 *   idle         — idle and you've seen it since (or never active)   → grey
 *
 * "Seen" = you were looking at that window's real terminal: while the overview is CLOSED, the
 * tmux-active window is continuously marked viewed; closing the overview re-marks it too.
 *
 * Identity: seen-state is keyed on the tmux STABLE window id (`@N`), never the reusable window
 * index — so a closed window's index being reused can't make a fresh window inherit "done-unseen".
 * State for vanished windows is pruned each push (no leak, and a reused id starts clean).
 */
import { computed, ref, watch, type Ref } from 'vue'
import type { TmuxWindowState } from '@terminal/types/terminal'

export type EffectiveStatus = 'waiting' | 'running' | 'done-unseen' | 'idle'

/** Raw per-window status from its panes: any waiting → waiting; any running → running; else idle. */
export function windowRawStatus(w: TmuxWindowState): 'waiting' | 'running' | 'idle' {
  const panes = w.panes ?? []
  if (panes.some((p) => p.agentStatus === 'waiting')) return 'waiting'
  if (panes.some((p) => p.agentStatus === 'running')) return 'running'
  return 'idle'
}

/** Backend "needs-you": any pane finished a turn / is blocked and hasn't been responded to.
 *  This is the durable, reload-proof signal (transcript-derived) that replaces the old
 *  "witness the running→idle transition" heuristic — a pane already done at page load counts. */
export function windowAwaiting(w: TmuxWindowState): boolean {
  return (w.panes ?? []).some((p) => p.awaitingUser)
}

/** The window's active-pane cwd (what the overview card shows). */
export function windowCwd(w: TmuxWindowState): string {
  const panes = w.panes ?? []
  return (panes.find((p) => p.active) ?? panes[0])?.cwd ?? ''
}

/** The window's active agent tool, if any (claude/codex badge). */
export function windowTool(w: TmuxWindowState): string {
  const panes = w.panes ?? []
  return (panes.find((p) => p.agentTool) ?? panes[0])?.agentTool ?? ''
}

/** Stable seen-state key: tmux window id (`@N`), falling back to index if a backend omits it. */
function windowKey(w: TmuxWindowState): string {
  return w.windowId || `#${w.index}`
}

export interface OverviewGroup {
  status: EffectiveStatus
  windows: TmuxWindowState[]
}

/**
 * PC 概览活跃大卡的每行列数（布局规则 SSOT，纯函数可单测）：
 * 每行 ≤3；恰好 4 个走 2×2 田字格；n≤3 就 n 列；更多则 3 列换行。
 * 空闲卡不走此处（收成 chip 条），只算活跃(等你/运行/完成待看)数。
 */
export function overviewColumns(activeCount: number): number {
  if (activeCount <= 3) return Math.max(1, activeCount)
  if (activeCount === 4) return 2
  return 3
}

export function useAgentOverview(windows: Ref<TmuxWindowState[]>, overviewOpen: Ref<boolean>) {
  // Client-local "seen" layer over the backend's durable "needs-you" (awaitingUser). A finished
  // window keeps its dot until you've SEEN it — where "seen" = the window became ACTIVE (you
  // switched to it). It keys on the pushed `active` flag, so a native tmux `ctrl+b N` switch clears
  // it exactly like a pane-bar tap does — same outcome regardless of how you switch. A new turn
  // (→ running = you responded) re-arms it so the NEXT completion shows again.
  const dismissed = ref<Record<string, boolean>>({})
  const prevRaw = ref<Record<string, string>>({})

  watch(
    windows,
    (wins) => {
      const live = new Set<string>()
      for (const w of wins) {
        const k = windowKey(w)
        live.add(k)
        const raw = windowRawStatus(w)
        // A new turn started (→ running = you responded) re-arms the dot for the next completion.
        if (raw === 'running' && prevRaw.value[k] !== 'running') delete dismissed.value[k]
        // You're viewing a finished window (it's active; overview closed so the terminal is what
        // you see) → seen, clear its dot. `active` comes from the topology push, so this fires for
        // a ctrl+b switch too, not just a mouse tap on the bar/overview.
        if (w.active && !overviewOpen.value && windowAwaiting(w)) dismissed.value[k] = true
        prevRaw.value[k] = raw
      }
      // Prune vanished windows — no leak; a reused id starts clean.
      for (const map of [dismissed.value, prevRaw.value]) {
        for (const k of Object.keys(map)) if (!live.has(k)) delete map[k]
      }
    },
    { immediate: true, deep: true },
  )

  /** Explicit "handled — hide it" for a window (e.g. tapping its overview card); re-armed on its
   *  next run. Switching to a window also clears it via the active-flag rule in the watcher. */
  function dismiss(w: TmuxWindowState): void {
    dismissed.value[windowKey(w)] = true
  }

  function effectiveStatus(w: TmuxWindowState): EffectiveStatus {
    const raw = windowRawStatus(w)
    if (raw !== 'idle') return raw // waiting (red) / running (green) come straight from the backend
    // Idle: "needs-you" (finished a turn, not yet responded) unless you've dismissed it.
    return windowAwaiting(w) && !dismissed.value[windowKey(w)] ? 'done-unseen' : 'idle'
  }

  /** Windows grouped by effective status, groups ordered by urgency, windows by index within. */
  const groups = computed<OverviewGroup[]>(() => {
    const buckets = new Map<EffectiveStatus, TmuxWindowState[]>()
    for (const w of windows.value) {
      const s = effectiveStatus(w)
      if (!buckets.has(s)) buckets.set(s, [])
      buckets.get(s)!.push(w)
    }
    const order: EffectiveStatus[] = ['waiting', 'running', 'done-unseen', 'idle']
    return order
      .filter((s) => buckets.has(s))
      .map((s) => ({
        status: s,
        windows: buckets.get(s)!.slice().sort((a, b) => a.index - b.index),
      }))
  })

  /** Global counts for the roll-up summary line. */
  const rollup = computed(() => {
    const c: Record<EffectiveStatus, number> = { waiting: 0, running: 0, 'done-unseen': 0, idle: 0 }
    for (const w of windows.value) c[effectiveStatus(w)]++
    return c
  })

  /** Reactive index→effectiveStatus map so the always-on tmux bar can dot each window with the
   *  SAME seen-aware status the overview uses (incl. done-unseen) — one source, no recompute. */
  const statusByIndex = computed(() => {
    const m: Record<number, EffectiveStatus> = {}
    for (const w of windows.value) m[w.index] = effectiveStatus(w)
    return m
  })

  return { effectiveStatus, groups, rollup, statusByIndex, dismiss }
}
