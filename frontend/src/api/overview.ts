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

/**
 * GET /sessions/{id}/overview — the single-session {detail, summary, turns} bag.
 * Returns nulls (not a throw) on any failure so the caller renders the pane's
 * empty/loading affordance instead of crashing the drawer.
 */
export async function sessionOverview(sessionId: string, cwd?: string): Promise<SessionMetricsBag> {
  const empty: SessionMetricsBag = { detail: null, summary: null, turns: [] }
  if (!sessionId) return empty
  const { cliFetch } = useCliAuth()
  try {
    // cwd = live active tmux pane dir → overview follows pane switches (server falls back
    // to the session's creation cwd when absent/invalid).
    let url = `/sessions/${encodeURIComponent(sessionId)}/overview`
    if (cwd) url += `?cwd=${encodeURIComponent(cwd)}`
    const resp = await cliFetch(cliApi(url))
    if (!resp.ok) return empty
    const data = (await resp.json()) as Partial<SessionMetricsBag>
    return {
      detail: data.detail ?? null,
      summary: data.summary ?? null,
      turns: data.turns ?? [],
    }
  } catch {
    return empty
  }
}
