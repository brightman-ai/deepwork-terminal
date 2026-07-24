/**
 * useDrawerDock — the single source of truth for which side the workbench drawer docks on
 * (left | right). GLOBAL, not per-pane: the dock side is a user preference that should be
 * consistent across every pane's drawer, so the state lives at module scope (one ref shared by
 * all callers) and persists to localStorage. Default 'right' — the historical position, so an
 * existing user sees no change until they flip it.
 *
 * This is deliberately a tiny orthogonal axis: it does NOT know about split/overlay/fullscreen
 * (those stay in the host's dual-mode layout SSOT). Consumers read `dock` and mirror their own
 * direction-dependent geometry off it — the drawer's flex order in split, the edge-drag resize
 * handle side, the overlay anchor. One flag in, every mirror derives.
 */
import { ref } from 'vue'

export type DockSide = 'left' | 'right'
const STORAGE_KEY = 'dw.drawer.dock'

function load(): DockSide {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'left' ? 'left' : 'right'
  } catch {
    return 'right'
  }
}

// Module-scoped singleton: every useDrawerDock() call shares this one ref, so flipping the dock
// in one pane's chrome moves all of them and the choice survives a reload.
const dock = ref<DockSide>(load())

function persist(v: DockSide): void {
  dock.value = v
  try {
    localStorage.setItem(STORAGE_KEY, v)
  } catch {
    /* private mode / storage disabled — in-memory state still works for the session */
  }
}

export function useDrawerDock() {
  return {
    dock,
    toggle: () => persist(dock.value === 'right' ? 'left' : 'right'),
    set: (v: DockSide) => persist(v),
  }
}
