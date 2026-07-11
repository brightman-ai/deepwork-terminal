/**
 * useTmuxState — reactive tmux topology for a terminal session.
 *
 * Source of truth lives on the backend (agentintel.TmuxStateService). We get an
 * initial snapshot via GET /tmux/state on init, then keep it live by consuming the
 * { type: "tmux_state", payload: TmuxState } WS control frame the server pushes on
 * ~1s diff. Wire handleWSMessage into the same control-message switch that dispatches
 * agent_state (see CliTerminalSurface).
 *
 * Per-session keyed singleton: each tab owns one WS / shellPID scope, so state is
 * keyed by sessionId rather than process-global. Re-using the same sessionId returns
 * the same reactive store (no duplicate fetch).
 *
 * The tmux prefix is dynamic — C-b (0x02) or C-a (0x01) or any user remap. prefixSeq()
 * builds prefix+suffix sequences (e.g. prefixSeq('c') = new-window) so callers never
 * hardcode \x02.
 */
import { ref, computed, type Ref, type ComputedRef } from 'vue'
import type { TmuxState, TmuxWindowState, TmuxPaneState, AgentTool } from '@terminal/types/terminal'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

const DEFAULT_PREFIX = new Uint8Array([0x02]) // C-b

/** Semantic copy-mode motions the UI can request without knowing keystrokes. */
export type CopyMotion = 'halfpage-up' | 'halfpage-down'

// ─── Stable per-pane identity (drawer-per-pane, 20260710-124400) ────────────────────────────
// `pane.index` is NOT stable — tmux recycles it when a pane closes (the next split reuses the
// freed index), so keying per-pane UI state on it would silently inherit a closed pane's state
// into whatever new pane happens to land on the same index. The stable identity is the pane's
// OWN window ("@N", survives window reorder) plus tmux's own stable pane id ("%N", survives
// index reuse) — both already carried on every TmuxWindowState/TmuxPaneState. Falls back to the
// (unstable) index only for a pre-upgrade host that hasn't started sending windowId/paneId yet,
// so callers degrade to "mostly works" instead of throwing.
export function paneStateKey(
  win: Pick<TmuxWindowState, 'windowId' | 'index'>,
  pane: Pick<TmuxPaneState, 'paneId' | 'index'>,
): string {
  const w = win.windowId || `w${win.index}`
  const p = pane.paneId || `p${pane.index}`
  return `${w}:${p}`
}

export interface TmuxStateStore {
  state: Ref<TmuxState | null>
  /** True once the first snapshot/push has arrived (state !== null). Until then the
   *  topology is UNKNOWN, distinct from "known to be empty/detached" — consumers must
   *  gate any tmux chrome on `ready` so an unknown first frame renders nothing (no
   *  attached/detached state guess) instead of flashing the detached layout. */
  ready: ComputedRef<boolean>
  installed: ComputedRef<boolean>
  serverRunning: ComputedRef<boolean>
  /** True iff THIS session's shell is inside a tmux client (per-shell, not global). */
  attached: ComputedRef<boolean>
  /** tmux session name THIS shell is attached to ('' when detached). */
  attachedSession: ComputedRef<string>
  /** Decoded tmux prefix control byte(s); falls back to 0x02 (C-b) until known. */
  prefixBytes: ComputedRef<Uint8Array>
  prefixDisplay: ComputedRef<string>
  /** Resolved global mode-keys ('vi' | 'emacs'); 'emacs' until known (tmux default). */
  modeKeys: ComputedRef<'vi' | 'emacs'>
  /** Windows of the session THIS shell is attached to ([] when detached). */
  windows: ComputedRef<TmuxWindowState[]>
  /** Live cwd of the ACTIVE pane (active window → active pane → cwd) of the attached
   *  session — '' when unknown/detached. Drives the workbench (files + overview) so it
   *  follows pane/window switches instead of being pinned to the session's creation cwd. */
  activeCwd: ComputedRef<string>
  /** Live agentTool of the ACTIVE pane (active window → active pane → agentTool) of the
   *  attached session — '' when unknown/detached/no agent. Lets the overview route to the
   *  codex vs claude metrics extractor for the pane the user is actually looking at. */
  activeTool: ComputedRef<AgentTool>
  /** prefix + suffix as a string, e.g. prefixSeq('c') for new-window. */
  prefixSeq: (suffix: string) => string
  /** Switch this client onto window `index` via POST /tmux/select-window — driven server-side so
   *  index ≥10 can't leak a `select-window -t N` burst into the focused pane. Best-effort. */
  selectWindow: (index: number) => Promise<void>
  /** Run a semantic copy-mode scroll motion via POST /tmux/copy-motion — the server
   *  drives it as a direct `send-keys -X <motion>` against the tmux socket. We do NOT
   *  inject keystrokes: the prefix `[` + command-prompt route silently no-ops for these
   *  motions, and a raw key depends on mode-keys. Best-effort; resolves once dispatched. */
  runCopyMotion: (motion: CopyMotion) => Promise<void>
  /** Force a server-side full redraw to this client (POST /tmux/refresh) to resync the web
   *  terminal's grid when xterm's buffer has diverged from tmux's model (ghosting). Best-effort. */
  runRefreshClient: () => Promise<void>
  /** Create a new tmux session and switch this client onto it via POST /tmux/new-session.
   *  Server-side (keystroke `new-session` is unreliable + refuses to nest in a client). */
  newSession: () => Promise<void>
  /** Tell the server the Agent Overview opened/closed → gates per-window tail capture. */
  setOverviewActive: (open: boolean) => Promise<void>
  /** Apply a pushed { type: "tmux_state" } WS frame payload. */
  handleWSMessage: (payload: unknown) => void
  /** One-shot GET snapshot — called on init. */
  fetchSnapshot: () => Promise<void>
}

const stores = new Map<string, TmuxStateStore>()

function decodePrefix(b64: string | undefined): Uint8Array {
  if (!b64) return DEFAULT_PREFIX
  try {
    const bin = atob(b64)
    if (bin.length === 0) return DEFAULT_PREFIX
    const out = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
    return out
  } catch {
    return DEFAULT_PREFIX
  }
}

function bytesToString(bytes: Uint8Array): string {
  let s = ''
  for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i])
  return s
}

function createStore(sessionId: () => string): TmuxStateStore {
  const { cliFetch } = useCliAuth()
  const state = ref<TmuxState | null>(null)

  // null = topology UNKNOWN (snapshot not yet arrived); non-null = known. This闸
  // separates "未知" from "已知未 attach" so the bar never renders a guessed state on
  // the first frame (root cause of the 1.1~11.1 flash: ?? false collapsed both into false).
  const ready = computed(() => state.value !== null)
  const installed = computed(() => state.value?.installed ?? false)
  const serverRunning = computed(() => state.value?.serverRunning ?? false)
  const attached = computed(() => state.value?.attached ?? false)
  const attachedSession = computed(() => state.value?.attachedSession ?? '')
  const prefixBytes = computed(() => decodePrefix(state.value?.prefix?.bytes))
  const prefixDisplay = computed(() => state.value?.prefix?.display ?? 'C-b')
  const modeKeys = computed<'vi' | 'emacs'>(() => (state.value?.modeKeys === 'vi' ? 'vi' : 'emacs'))

  // Windows of the session THIS shell is attached to — scoped by attachedSession
  // name, NOT by any session that merely has a client. Detached → [] so the bar
  // self-hides for plain shells even while a tmux server runs for someone else.
  const windows = computed<TmuxWindowState[]>(() => {
    if (!attached.value) return []
    const sessions = state.value?.sessions ?? []
    if (sessions.length === 0) return []
    const name = attachedSession.value
    const s = (name && sessions.find(x => x.name === name)) || sessions.find(x => x.attached)
    return s?.windows ?? []
  })

  // Live cwd of the active pane within the attached session's active window. Falls back
  // through active→first window and active→first pane so a single-pane shell still resolves.
  const activeCwd = computed<string>(() => {
    const ws = windows.value
    if (ws.length === 0) return ''
    const win = ws.find(w => w.active) ?? ws[0]
    const panes = win?.panes ?? []
    if (panes.length === 0) return ''
    const pane = panes.find(p => p.active) ?? panes[0]
    return pane?.cwd ?? ''
  })

  // Live agentTool of the active pane (same active window → active pane resolution as
  // activeCwd). '' when no agent is detected on that pane. Drives the overview's codex-vs-
  // claude routing so it follows pane/window switches alongside activeCwd.
  const activeTool = computed<AgentTool>(() => {
    const ws = windows.value
    if (ws.length === 0) return ''
    const win = ws.find(w => w.active) ?? ws[0]
    const panes = win?.panes ?? []
    if (panes.length === 0) return ''
    const pane = panes.find(p => p.active) ?? panes[0]
    return pane?.agentTool ?? ''
  })

  function prefixSeq(suffix: string): string {
    return bytesToString(prefixBytes.value) + suffix
  }

  // Switch this client onto a window by index. Driven on the SERVER (POST /tmux/select-window),
  // not the PTY: tmux binds prefix+0..9 to select-window, but index ≥10 has no binding, so the
  // keystroke route must open the command prompt (prefix ':' select-window -t N ⏎) and the burst
  // races the prompt open — leaking the literal `select-window -t N` into the focused app. The
  // server drives it against the socket, scoped to this shell's session. SSOT for bar + overview.
  async function selectWindow(index: number): Promise<void> {
    const id = sessionId()
    if (!id) return
    try {
      await cliFetch(cliApi(`/tmux/select-window?session=${encodeURIComponent(id)}`), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ index }),
      })
    } catch { /* best-effort — a transient failure just means no switch this tap */ }
  }

  async function runCopyMotion(motion: CopyMotion): Promise<void> {
    // Drive the scroll on the SERVER via `send-keys -X <motion>` against the tmux socket.
    // The keystroke route (prefix `[`, then the command prompt) was proven to silently no-op
    // for these motions while in copy-mode, and a raw key depends on mode-keys. The server
    // enters copy-mode first only when the pane is not already in one (so a held scroll
    // position is preserved). attachedSession scopes the target to the right session's pane.
    try {
      await cliFetch(cliApi('/tmux/copy-motion'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session: attachedSession.value, motion }),
      })
    } catch { /* best-effort scroll — a transient failure just means no scroll this tap */ }
  }

  // Force a server-side full redraw to this session's tmux client. Resyncs the web terminal's
  // xterm grid when it has diverged from tmux's model (ghosting under fullscreen TUIs — stale
  // cells that a client-side term.refresh can't clear because the divergence is in xterm's
  // buffer). Best-effort: a transient failure just means the residue lingers until the next one.
  async function runRefreshClient(): Promise<void> {
    const id = sessionId()
    if (!id) return
    // A tmux redraw only means something for a shell INSIDE a tmux client. A plain
    // (non-tmux) shell has no client to refresh — the server returns 501 "refresh
    // unsupported". CliTerminalSurface calls this on every alt-screen render, so
    // without this gate a non-tmux session floods the server log with per-render
    // 501s. Gate on attached: tmux users still get the ghosting resync unchanged.
    if (!attached.value) return
    try {
      await cliFetch(cliApi(`/tmux/refresh?session=${encodeURIComponent(id)}`), { method: 'POST' })
    } catch { /* best-effort resync */ }
  }

  async function newSession(): Promise<void> {
    const id = sessionId()
    if (!id) return
    try {
      await cliFetch(cliApi(`/tmux/new-session?session=${encodeURIComponent(id)}`), { method: 'POST' })
    } catch { /* best-effort — the ~1s topology poll still surfaces a created session */ }
  }

  // Tell the server whether the Agent Overview is open, so it captures per-window tail only while
  // someone is viewing it (zero extra cost otherwise). Best-effort — a miss just means tail stays
  // at its last state until the next toggle.
  async function setOverviewActive(open: boolean): Promise<void> {
    try {
      await cliFetch(cliApi('/tmux/overview'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ open }),
      })
    } catch { /* best-effort */ }
  }

  function handleWSMessage(payload: unknown): void {
    if (payload && typeof payload === 'object') {
      state.value = payload as TmuxState
    }
  }

  async function fetchSnapshot(): Promise<void> {
    const id = sessionId()
    if (!id) return
    try {
      const resp = await cliFetch(cliApi('/tmux/state'))
      if (resp.ok) handleWSMessage(await resp.json())
    } catch { /* endpoint may be absent in older hosts — stay null, bar stays hidden */ }
  }

  return {
    state, ready, installed, serverRunning, attached, attachedSession, prefixBytes, prefixDisplay,
    modeKeys, windows, activeCwd, activeTool, prefixSeq, selectWindow, runCopyMotion, runRefreshClient, newSession, setOverviewActive, handleWSMessage, fetchSnapshot,
  }
}

export function useTmuxState(sessionId: () => string): TmuxStateStore {
  const id = sessionId()
  const existing = stores.get(id)
  if (existing) return existing
  const store = createStore(sessionId)
  stores.set(id, store)
  void store.fetchSnapshot()
  return store
}
