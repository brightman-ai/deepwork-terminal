/**
 * Overview API client — SESSION-SCOPED single-session metrics bag (CHG-016, SSOT).
 *
 *   GET /sessions/{id}/overview → { detail, summary, turns }
 *
 * The endpoint returns the SAME host-agnostic contract the shared @ce pane
 * consumes (see @ce/types/sessionMetrics). We keep the wire JSON's snake_case
 * field set verbatim — the typed bag IS the pane's prop bag, so there is NO
 * mapping layer between fetch and render.
 *
 * Auth: same authed cliFetch (X-CLI-Auth) the rest of the terminal API uses.
 */
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import type { SessionMetricsBag } from '@ce/types/sessionMetrics'

export type { SessionMetricsBag } from '@ce/types/sessionMetrics'

const EMPTY_OVERVIEW: SessionMetricsBag = { detail: null, summary: null, turns: [], price: null }
const OVERVIEW_CACHE_MS = 2_000
const OVERVIEW_CACHE_MAX = 64
interface OverviewCacheEntry { value: SessionMetricsBag; expiresAt: number }
interface OverviewPending {
  controller: AbortController
  promise: Promise<SessionMetricsBag>
  subscribers: number
  settled: boolean
}
const overviewCache = new Map<string, OverviewCacheEntry>()
const overviewPending = new Map<string, OverviewPending>()

function overviewKey(sessionId: string, cwd?: string, tool?: string): string {
  return `${sessionId}\u0000${cwd || ''}\u0000${tool || ''}`
}

function subscribeOverview(pending: OverviewPending, signal?: AbortSignal): Promise<SessionMetricsBag> {
  if (signal?.aborted) return Promise.resolve(EMPTY_OVERVIEW)
  pending.subscribers++
  return new Promise((resolve) => {
    let released = false
    const release = () => {
      if (released) return
      released = true
      signal?.removeEventListener('abort', onAbort)
      pending.subscribers--
      // A hidden tab should not keep a request alive after every consumer has left.
      if (!pending.settled && pending.subscribers === 0) pending.controller.abort()
    }
    const onAbort = () => { release(); resolve(EMPTY_OVERVIEW) }
    signal?.addEventListener('abort', onAbort, { once: true })
    pending.promise.then((value) => {
      if (released) return
      release()
      resolve(value)
    })
  })
}

/**
 * GET /sessions/{id}/overview — the single-session {detail, summary, turns} bag.
 * Returns nulls (not a throw) on any failure so the caller renders the pane's
 * empty/loading affordance instead of crashing the drawer.
 */
export async function sessionOverview(
  sessionId: string,
  cwd?: string,
  tool?: string,
  signal?: AbortSignal,
): Promise<SessionMetricsBag> {
  if (!sessionId || signal?.aborted) return EMPTY_OVERVIEW
  const key = overviewKey(sessionId, cwd, tool)
  const cached = overviewCache.get(key)
  if (cached && cached.expiresAt > Date.now()) return cached.value
  if (cached) overviewCache.delete(key)

  let pending = overviewPending.get(key)
  if (!pending) {
    const controller = new AbortController()
    const { cliFetch } = useCliAuth()
    const promise = (async (): Promise<SessionMetricsBag> => {
      try {
        // cwd = the effective pane directory; tool = the live runtime. Both are part of the
        // cache key so a pane switch can never paint one runtime's model under another.
        const params = new URLSearchParams()
        if (cwd) params.set('cwd', cwd)
        if (tool) params.set('tool', tool)
        let url = `/sessions/${encodeURIComponent(sessionId)}/overview`
        const qs = params.toString()
        if (qs) url += `?${qs}`
        const resp = await cliFetch(cliApi(url), { signal: controller.signal })
        if (!resp.ok) return EMPTY_OVERVIEW
        const data = (await resp.json()) as Partial<SessionMetricsBag>
        return {
          detail: data.detail ?? null,
          summary: data.summary ?? null,
          turns: data.turns ?? [],
          price: data.price ?? null,
        }
      } catch {
        return EMPTY_OVERVIEW
      }
    })()
    pending = { controller, promise, subscribers: 0, settled: false }
    overviewPending.set(key, pending)
    void promise.then((value) => {
      pending!.settled = true
      overviewPending.delete(key)
      if (!controller.signal.aborted) {
        if (overviewCache.size >= OVERVIEW_CACHE_MAX) {
          const oldest = overviewCache.keys().next().value
          if (oldest) overviewCache.delete(oldest)
        }
        overviewCache.set(key, { value, expiresAt: Date.now() + OVERVIEW_CACHE_MS })
      }
    })
  }
  return subscribeOverview(pending, signal)
}
