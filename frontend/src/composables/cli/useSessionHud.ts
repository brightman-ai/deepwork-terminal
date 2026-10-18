import { ref, watch, onBeforeUnmount } from 'vue'
import { sessionOverview } from '@terminal/api/overview'

export interface SessionHudTarget {
  sessionId: string
  cwd?: string
  tool?: string
  /** Remote peers have a different auth/base; the local overview endpoint must not be used. */
  remote?: boolean
}

export interface SessionHudSnapshot {
  sessionId: string
  model: string
  totalCost: number | null
  currency: string
  inputTokens: number | null
  outputTokens: number | null
  outputTps: number | null
  modelTps: number | null
  /** true when the session has a model but no price evidence yet. */
  costCoverage: 'known' | 'unpriced' | 'empty'
  loading: boolean
  updatedAt: number
}

const REFRESH_MS = 15_000
const MODEL_TPS_MAX = 600
function sumTurns(turns: Array<{ input_tokens?: number | null; output_tokens?: number | null }>, key: 'input_tokens' | 'output_tokens'): number | null {
  let total = 0; let seen = false
  for (const turn of turns) {
    const value = turn[key]
    if (typeof value === 'number') { total += value; seen = true }
  }
  return seen ? total : null
}
function deriveTps(bag: Awaited<ReturnType<typeof sessionOverview>>): { outputTps: number | null; modelTps: number | null } {
  let genMs = 0; let outTok = 0
  for (const turn of bag.turns ?? []) {
    if (typeof turn.duration_ms === 'number' && typeof turn.ttft_ms === 'number' && typeof turn.output_tokens === 'number' && turn.output_tokens > 0 && turn.duration_ms > turn.ttft_ms) {
      genMs += turn.duration_ms - turn.ttft_ms; outTok += turn.output_tokens
    }
  }
  const outputTps = genMs > 0 && outTok > 0 ? outTok / (genMs / 1000) : null
  const summary = bag.summary
  const modelTps = summary?.output_window_ms && summary.output_tokens && summary.output_window_ms > 0
    ? summary.output_tokens / (summary.output_window_ms / 1000)
    : null
  return { outputTps, modelTps: modelTps && modelTps > 0 && modelTps <= MODEL_TPS_MAX ? modelTps : null }
}

/**
 * One short-lived snapshot owner for the active terminal session.
 *
 * This is intentionally not a long poll: only the visible session owns one ordinary overview
 * request, while the active tab is switched or every 15s. The eventual request broker can share
 * this key with SessionOverviewTab; until then it already avoids one request per hidden tab.
 */
export function useSessionHud(target: () => SessionHudTarget | null) {
  const snapshot = ref<SessionHudSnapshot | null>(null)
  let timer: ReturnType<typeof setInterval> | null = null
  let request: AbortController | null = null
  let generation = 0

  function stop(): void {
    if (timer) { clearInterval(timer); timer = null }
    generation++
    request?.abort()
    request = null
  }

  function clear(): void {
    snapshot.value = null
  }

  async function refresh(current: SessionHudTarget): Promise<void> {
    const mine = ++generation
    request?.abort()
    const ctrl = new AbortController()
    request = ctrl
    const previous = snapshot.value
    snapshot.value = {
      sessionId: current.sessionId,
      model: previous?.sessionId === current.sessionId ? previous.model : '',
      totalCost: previous?.sessionId === current.sessionId ? previous.totalCost : null,
      currency: previous?.sessionId === current.sessionId ? previous.currency : '',
      inputTokens: previous?.sessionId === current.sessionId ? previous.inputTokens : null,
      outputTokens: previous?.sessionId === current.sessionId ? previous.outputTokens : null,
      outputTps: previous?.sessionId === current.sessionId ? previous.outputTps : null,
      modelTps: previous?.sessionId === current.sessionId ? previous.modelTps : null,
      costCoverage: previous?.sessionId === current.sessionId ? previous.costCoverage : 'empty',
      loading: true,
      updatedAt: previous?.sessionId === current.sessionId ? previous.updatedAt : 0,
    }
    try {
      const bag = await sessionOverview(current.sessionId, current.cwd, current.tool, ctrl.signal)
      if (mine !== generation || ctrl.signal.aborted) return
      const model = bag.detail?.model_id || ''
      const cost = bag.summary?.total_cost
      const currency = bag.summary?.currency || ''
      const inputTokens = bag.summary?.input_tokens ?? sumTurns(bag.turns ?? [], 'input_tokens')
      const outputTokens = bag.summary?.output_tokens ?? sumTurns(bag.turns ?? [], 'output_tokens')
      const tps = deriveTps(bag)
      snapshot.value = {
        sessionId: current.sessionId,
        model,
        totalCost: typeof cost === 'number' && Number.isFinite(cost) ? cost : null,
        currency,
        inputTokens: typeof inputTokens === 'number' ? inputTokens : null,
        outputTokens: typeof outputTokens === 'number' ? outputTokens : null,
        outputTps: tps.outputTps,
        modelTps: tps.modelTps,
        costCoverage: cost != null ? 'known' : model ? 'unpriced' : 'empty',
        loading: false,
        updatedAt: Date.now(),
      }
    } catch {
      if (mine !== generation || ctrl.signal.aborted) return
      snapshot.value = {
        ...(snapshot.value ?? { sessionId: current.sessionId, model: '', totalCost: null, currency: '', inputTokens: null, outputTokens: null, outputTps: null, modelTps: null, costCoverage: 'empty', updatedAt: 0 }),
        sessionId: current.sessionId,
        loading: false,
      }
    } finally {
      if (request === ctrl) request = null
    }
  }

  watch(
    () => {
      const t = target()
      return t && t.sessionId && !t.remote ? `${t.sessionId}\u0000${t.cwd || ''}\u0000${t.tool || ''}` : ''
    },
    () => {
      stop()
      const current = target()
      if (!current?.sessionId || current.remote) { clear(); return }
      void refresh(current)
      timer = setInterval(() => {
        const latest = target()
        if (latest?.sessionId && !latest.remote) void refresh(latest)
      }, REFRESH_MS)
    },
    { immediate: true },
  )

  onBeforeUnmount(stop)
  return snapshot
}
