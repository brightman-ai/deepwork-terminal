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
import type { TmuxState, TmuxWindowState } from '@terminal/types/terminal'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

const DEFAULT_PREFIX = new Uint8Array([0x02]) // C-b

/** Semantic copy-mode motions the UI can request without knowing keystrokes. */
export type CopyMotion = 'halfpage-up' | 'halfpage-down'

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
  /** prefix + suffix as a string, e.g. prefixSeq('c') for new-window. */
  prefixSeq: (suffix: string) => string
  /** Run a semantic copy-mode scroll motion via POST /tmux/copy-motion — the server
   *  drives it as a direct `send-keys -X <motion>` against the tmux socket. We do NOT
   *  inject keystrokes: the prefix `[` + command-prompt route silently no-ops for these
   *  motions, and a raw key depends on mode-keys. Best-effort; resolves once dispatched. */
  runCopyMotion: (motion: CopyMotion) => Promise<void>
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

  function prefixSeq(suffix: string): string {
    return bytesToString(prefixBytes.value) + suffix
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
    modeKeys, windows, prefixSeq, runCopyMotion, handleWSMessage, fetchSnapshot,
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
