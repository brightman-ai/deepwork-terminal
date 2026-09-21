import { onScopeDispose, watch } from 'vue'

const previews: Array<() => void> = []

function onKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape' || previews.length === 0) return
  // Window capture runs before the terminal/copy-mode document capture handlers.
  event.preventDefault()
  event.stopImmediatePropagation()
  previews[previews.length - 1]()
}

/** Only the most recently opened preview owns Escape; hidden/unmounted previews release it. */
export function usePreviewEscape(isOpen: () => boolean, close: () => void): void {
  function remove(): void {
    const index = previews.indexOf(close)
    if (index >= 0) previews.splice(index, 1)
    if (previews.length === 0) window.removeEventListener('keydown', onKeydown, true)
  }
  watch(isOpen, (open) => {
    remove()
    if (open) {
      if (previews.length === 0) window.addEventListener('keydown', onKeydown, true)
      previews.push(close)
    }
  }, { immediate: true, flush: 'sync' })
  onScopeDispose(remove)
}
