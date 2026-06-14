/**
 * useFocusStateMachine — Three-state focus management for mobile CLI overlay.
 * States: IDLE / TERMINAL / COMPOSE
 * Only one focusable element at a time (IR-05: DOM hard isolation).
 * [Ref: T5-B4.M3, CAP-terminal-interaction S2, DDC-05]
 */
import { ref, readonly } from 'vue'
import type { FocusState } from '@terminal/types/terminal'

export interface FocusStateMachine {
  /** Current focus state */
  state: ReturnType<typeof readonly<ReturnType<typeof ref<FocusState>>>>
  /** Transition to TERMINAL state (user taps terminal area) */
  focusTerminal: () => void
  /** Transition to COMPOSE state (user opens compose bar) */
  focusCompose: () => void
  /** Transition to IDLE state (user closes compose bar / blur) */
  blur: () => void
  /** Reset to IDLE */
  reset: () => void
}

export function useFocusStateMachine(): FocusStateMachine {
  const state = ref<FocusState>('IDLE')

  function focusTerminal() {
    state.value = 'TERMINAL'
  }

  function focusCompose() {
    state.value = 'COMPOSE'
  }

  function blur() {
    state.value = 'IDLE'
  }

  function reset() {
    state.value = 'IDLE'
  }

  return {
    state: readonly(state),
    focusTerminal,
    focusCompose,
    blur,
    reset,
  }
}
