/** xterm can discard its hovered link during resize/render while retaining the
 * last pointer cell. A second click on that unchanged cell then has no native
 * activation. Recover only an unclaimed, stationary mouse click; touch has its
 * own selection/tap controller and native activations must never be doubled. */
export function createFileLinkClickGate(activate: (x: number, y: number) => boolean) {
  let down: { x: number; y: number } | undefined
  let claimed = false
  return {
    begin(event: { button: number; clientX: number; clientY: number; pointerType?: string }) {
      claimed = false
      down = event.button === 0 && (!event.pointerType || event.pointerType === 'mouse')
        ? { x: event.clientX, y: event.clientY } : undefined
    },
    claim() { claimed = true },
    complete(event: { button: number; clientX: number; clientY: number; detail: number }) {
      const start = down
      down = undefined
      if (!start || claimed || event.button !== 0 || event.detail > 1 ||
        Math.hypot(event.clientX - start.x, event.clientY - start.y) > 5) return false
      return activate(event.clientX, event.clientY)
    },
  }
}
