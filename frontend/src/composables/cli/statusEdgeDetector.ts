/**
 * statusEdgeDetector — the ONE implementation of "per-key status transition" memory.
 *
 * Why this file exists (architecture, not convenience):
 * status-edge detection had grown two hand-rolled copies — the backend's
 * (`push_notifier.go:185`) and the frontend's (`useForegroundAgentNotify.ts`). Adding a third
 * for the attention HUD would have guaranteed drift: three places each remembering "the previous
 * status" with subtly different priming/pruning rules. The frontend's two consumers genuinely
 * observe DIFFERENT granularities (notify: pane × raw agentStatus; HUD: window × seen-aware
 * EffectiveStatus) so they cannot share one memory map — but they must share one definition of
 * what an edge IS. That definition lives here, and only here.
 *
 * Three rules are baked in so no consumer can re-decide them:
 *  1. PRIMING — the first frame only seeds the baseline. Keys already in a firing state when the
 *     tab connected are stale, not new edges; never announce them.
 *  2. EDGE — emit exactly when the remembered status differs from the incoming one. A key absent
 *     from memory (created after priming) emits with `from: undefined` — a genuinely new pane /
 *     window arriving already-firing IS an edge.
 *  3. PRUNING — keys missing from a frame are forgotten, so a re-created pane/window re-arms
 *     cleanly instead of inheriting a dead predecessor's status.
 *
 * `diff()` must be called on EVERY frame regardless of whether the consumer intends to act on the
 * result. Advancing the memory unconditionally is what makes "don't replay events that expired
 * while the user was away" structural rather than a runtime check (see ATT-8).
 */

export interface StatusEdge<S> {
  key: string
  /** `undefined` = the key is new since priming (freshly created pane/window). */
  from: S | undefined
  to: S
}

export interface StatusEdgeDetector<S> {
  /** Feed one frame; get the transitions it introduced. Always advances the memory. */
  diff(current: ReadonlyMap<string, S>): StatusEdge<S>[]
  /** False until the baseline frame has been seeded (the first `diff` returns no edges). */
  readonly primed: boolean
  /** The remembered status for a key — for consumers that need "what was it before?". */
  peek(key: string): S | undefined
}

export function createStatusEdgeDetector<S>(): StatusEdgeDetector<S> {
  const last = new Map<string, S>()
  let primed = false

  return {
    get primed(): boolean {
      return primed
    },
    peek(key: string): S | undefined {
      return last.get(key)
    },
    diff(current: ReadonlyMap<string, S>): StatusEdge<S>[] {
      if (!primed) {
        for (const [k, v] of current) last.set(k, v)
        primed = true
        return []
      }
      const edges: StatusEdge<S>[] = []
      for (const [k, to] of current) {
        const from = last.get(k)
        if (from !== to) edges.push({ key: k, from, to })
        last.set(k, to)
      }
      for (const k of [...last.keys()]) if (!current.has(k)) last.delete(k)
      return edges
    },
  }
}
