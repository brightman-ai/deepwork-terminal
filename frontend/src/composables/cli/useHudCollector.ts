/**
 * useHudCollector — Collects diagnostic events for the HUD panel.
 * Maintains a 200-event ring buffer with category-based sampling.
 * [Ref: CAP-hud-diagnostics S3, DDC-14]
 */
import { ref, reactive, readonly } from 'vue'
import { apiUrl } from '@/utils/runtimeBase'

export type HudEventType = 'focus' | 'keyboard' | 'touch' | 'ws' | 'state' | 'resize' | 'error'

export interface HudEvent {
  timestamp: number
  type: HudEventType
  detail: string
}

export interface HudSnapshot {
  focus: string
  keyboard: string
  ws: string
  pty: string
  anchor: string
  mode: string
}

const MAX_EVENTS = 200

export function useHudCollector() {
  const events = ref<HudEvent[]>([])
  const snapshot = reactive<HudSnapshot>({
    focus: 'IDLE',
    keyboard: '-',
    ws: 'disconnected',
    pty: '-',
    anchor: 'IDLE',
    mode: 'normal',
  })
  const enabled = ref(true)

  function record(type: HudEventType, detail: string) {
    if (!enabled.value) return
    const event: HudEvent = {
      timestamp: Date.now(),
      type,
      detail,
    }
    events.value.push(event)
    // Ring buffer: keep last MAX_EVENTS.
    if (events.value.length > MAX_EVENTS) {
      events.value = events.value.slice(-MAX_EVENTS)
    }
  }

  function updateSnapshot(partial: Partial<HudSnapshot>) {
    Object.assign(snapshot, partial)
  }

  function clear() {
    events.value = []
  }

  function toggle() {
    enabled.value = !enabled.value
  }

  async function upload(sessionId: string) {
    const body = {
      sessionId,
      timestamp: new Date().toISOString(),
      userAgent: navigator.userAgent,
      screen: {
        width: screen.width,
        height: screen.height,
        orientation: screen.orientation?.type ?? 'unknown',
      },
      events: events.value,
      snapshot: { ...snapshot },
    }

    try {
      await fetch(apiUrl('/api/debug/logs'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      return true
    } catch {
      return false
    }
  }

  return {
    events: readonly(events),
    snapshot: readonly(snapshot),
    enabled: readonly(enabled),
    record,
    updateSnapshot,
    clear,
    toggle,
    upload,
  }
}
