import { onMounted, onUnmounted, ref } from 'vue'
import { detectCliRuntimeProfile } from '@terminal/composables/cli/useCliRuntimeMode'

interface KeyboardInsetInput {
  innerHeight: number
  visualHeight?: number | null
  offsetTop?: number | null
}

const MIN_KEYBOARD_INSET = 8

export function computeVisualKeyboardInset(input: KeyboardInsetInput): number {
  const innerHeight = Math.max(0, input.innerHeight)
  const visualHeight = Math.max(0, input.visualHeight ?? innerHeight)
  const offsetTop = Math.max(0, input.offsetTop ?? 0)
  const inset = Math.max(0, Math.round(innerHeight - visualHeight - offsetTop))
  return inset < MIN_KEYBOARD_INSET ? 0 : inset
}

export function readVisualKeyboardInset(enabled = true): number {
  if (!enabled) return 0
  const profile = detectCliRuntimeProfile()
  if (!profile.usesVisualKeyboardInset) return 0
  const vv = window.visualViewport
  return computeVisualKeyboardInset({
    innerHeight: window.innerHeight,
    visualHeight: vv?.height,
    offsetTop: vv?.offsetTop,
  })
}

export function resetViewportScroll(): void {
  requestAnimationFrame(() => {
    window.scrollTo(0, 0)
    document.documentElement.scrollTop = 0
    document.body.scrollTop = 0
  })
}

export function focusWithoutViewportScroll(element: HTMLElement | null | undefined): void {
  if (!element) return
  try {
    element.focus({ preventScroll: true })
  } catch {
    element.focus()
  }
}

export function useVisualKeyboardInset(options?: { enabled?: () => boolean }) {
  const keyboardInset = ref(0)

  function syncKeyboardInset(): void {
    keyboardInset.value = readVisualKeyboardInset(options?.enabled?.() ?? true)
  }

  onMounted(() => {
    window.visualViewport?.addEventListener('resize', syncKeyboardInset)
    window.visualViewport?.addEventListener('scroll', syncKeyboardInset)
    syncKeyboardInset()
  })

  onUnmounted(() => {
    window.visualViewport?.removeEventListener('resize', syncKeyboardInset)
    window.visualViewport?.removeEventListener('scroll', syncKeyboardInset)
  })

  return { keyboardInset, syncKeyboardInset }
}
