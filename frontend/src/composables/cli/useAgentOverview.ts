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

/** The transcript time of the awaiting pane's last completion — the reload-proof key the
 *  seen-layer dismisses against. '' when no pane is awaiting or the time is undated. */
export function windowAwaitingSince(w: TmuxWindowState): string {
  const p = (w.panes ?? []).find((p) => p.awaitingUser && isDatedSince(p.awaitingSince))
  return p?.awaitingSince ?? ''
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

// ─── persisted "seen" layer (localStorage, device-local) ─────────────────────────────
// The needs-you dot's dismissal is keyed on the backend's reload-proof AwaitingSince
// timestamp, so "I've seen this completion" survives F5 — yet a NEW turn (new timestamp)
// re-shows the dot on its own (no running→re-arm heuristic needed). Module-level singleton
// + localStorage → one seen-state across every component instance AND across reloads.
// Device-local by design (needs-you = "seen on THIS device"); never touches the server store
// (avoids the cross-device merge + the server-store overwrite hazard).
const SEEN_STORAGE_KEY = 'needsYouSeen'

function loadSeen(): Record<string, string> {
  try {
    const raw = JSON.parse(localStorage.getItem(SEEN_STORAGE_KEY) || '{}')
    return raw && typeof raw === 'object' ? (raw as Record<string, string>) : {}
  } catch {
    return {}
  }
}

function saveSeen(map: Record<string, string>): void {
  try {
    localStorage.setItem(SEEN_STORAGE_KEY, JSON.stringify(map))
  } catch {
    /* private-mode / quota — in-memory seen still works for this session */
  }
}

/** A dated (non-zero) AwaitingSince. A tmux "zero" time (`0001-01-01…`, from omitempty not
 *  applying to time.Time) or an absent value means the wait couldn't be dated (e.g. a PTY-only
 *  permission prompt) → treated as "unknown completion": never persist-dismissable, so such a
 *  high-signal wait keeps showing (incl. across F5) rather than being wrongly muted. */
function isDatedSince(since: string | undefined): since is string {
  return !!since && !since.startsWith('0001-01-01')
}

export function useAgentOverview(windows: Ref<TmuxWindowState[]>, overviewOpen: Ref<boolean>) {
  // Device-local "seen" layer over the backend's reload-proof "needs-you" (awaitingUser +
  // awaitingSince). A finished window keeps its dot until you've SEEN this completion — "seen" =
  // the window became ACTIVE (you switched to it), keyed on the pushed `active` flag so a native
  // ctrl+b N switch clears it exactly like a pane-bar tap. Persisted to localStorage keyed on the
  // completion's AwaitingSince, so it survives F5 yet a later turn's newer timestamp re-shows it —
  // no running-transition witness needed. Single call site → this ref is the shared seen-state.
  const seen = ref<Record<string, string>>(loadSeen())
  function persistSeen(): void {
    saveSeen(seen.value)
  }

  watch(
    windows,
    (wins) => {
      const live = new Set<string>()
      let changed = false
      for (const w of wins) {
        live.add(windowKey(w))
        // You're viewing a finished window (it's active; overview closed so the terminal is what
        // you see) → seen: remember THIS completion's timestamp. `active` comes from the topology
        // push, so a native ctrl+b switch clears it exactly like a pane-bar tap. No running→re-arm
        // needed: a new turn carries a newer AwaitingSince, so the stored one stops matching and
        // the dot returns on its own — reload-proof, because both sides are transcript-derived.
        if (w.active && !overviewOpen.value && windowAwaiting(w)) {
          const k = windowKey(w)
          const since = windowAwaitingSince(w)
          if (since && seen.value[k] !== since) {
            seen.value[k] = since
            changed = true
          }
        }
      }
      // Prune vanished windows — no leak; a reused id starts clean.
      for (const k of Object.keys(seen.value)) {
        if (!live.has(k)) {
          delete seen.value[k]
          changed = true
        }
      }
      if (changed) persistSeen()
    },
    { immediate: true, deep: true },
  )

  /** Explicit "handled — hide it" for a window (e.g. tapping its overview card). No re-arm needed:
   *  its next turn's newer AwaitingSince won't match the stored one, so the dot returns by itself. */
  function dismiss(w: TmuxWindowState): void {
    const since = windowAwaitingSince(w)
    if (!since || seen.value[windowKey(w)] === since) return
    seen.value[windowKey(w)] = since
    persistSeen()
  }

  function effectiveStatus(w: TmuxWindowState): EffectiveStatus {
    const raw = windowRawStatus(w)
    if (raw !== 'idle') return raw // waiting (red) / running (green) come straight from the backend
    // Idle: "needs-you" (finished a turn, not yet responded) unless you've SEEN this exact
    // completion — stored AwaitingSince equals the current one. A later turn's newer timestamp
    // won't match → dot re-appears; an undated wait never matches → stays shown (not muted).
    const since = windowAwaitingSince(w)
    return windowAwaiting(w) && !(isDatedSince(since) && seen.value[windowKey(w)] === since)
      ? 'done-unseen'
      : 'idle'
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
