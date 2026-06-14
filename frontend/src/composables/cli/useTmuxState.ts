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
import type { TmuxState, TmuxWindowState } from '@/types/terminal'
import { useCliAuth } from '@/composables/cli/useCliAuth'
import { cliApi } from '@/composables/cli/useCliApiPrefix'

const DEFAULT_PREFIX = new Uint8Array([0x02]) // C-b

export interface TmuxStateStore {
  state: Ref<TmuxState | null>
  installed: ComputedRef<boolean>
  serverRunning: ComputedRef<boolean>
  attached: ComputedRef<boolean>
  /** Decoded tmux prefix control byte(s); falls back to 0x02 (C-b) until known. */
  prefixBytes: ComputedRef<Uint8Array>
  prefixDisplay: ComputedRef<string>
  /** Windows of the attached session (else the first session, else []). */
  windows: ComputedRef<TmuxWindowState[]>
  /** prefix + suffix as a string, e.g. prefixSeq('c') for new-window. */
  prefixSeq: (suffix: string) => string
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

  const installed = computed(() => state.value?.installed ?? false)
  const serverRunning = computed(() => state.value?.serverRunning ?? false)
  const attached = computed(() => state.value?.attached ?? false)
  const prefixBytes = computed(() => decodePrefix(state.value?.prefix?.bytes))
  const prefixDisplay = computed(() => state.value?.prefix?.display ?? 'C-b')

  const windows = computed<TmuxWindowState[]>(() => {
    const sessions = state.value?.sessions ?? []
    if (sessions.length === 0) return []
    const s = sessions.find(x => x.attached) ?? sessions[0]
    return s.windows ?? []
  })

  function prefixSeq(suffix: string): string {
    return bytesToString(prefixBytes.value) + suffix
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
    state, installed, serverRunning, attached, prefixBytes, prefixDisplay,
    windows, prefixSeq, handleWSMessage, fetchSnapshot,
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
