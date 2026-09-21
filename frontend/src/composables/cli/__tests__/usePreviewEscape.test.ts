import { afterEach, describe, expect, it } from 'bun:test'
import { effectScope, ref, type EffectScope } from 'vue'
import { usePreviewEscape } from '../usePreviewEscape'

const originalWindow = globalThis.window
const scopes: EffectScope[] = []
afterEach(() => {
  scopes.splice(0).forEach(scope => scope.stop())
  globalThis.window = originalWindow
})

function setup() {
  const target = new EventTarget()
  globalThis.window = target as unknown as Window & typeof globalThis
  function key(key: string) {
    const event = new Event('keydown', { cancelable: true })
    Object.defineProperty(event, 'key', { value: key })
    target.dispatchEvent(event)
    return event
  }
  function preview() {
    const open = ref(false)
    const scope = effectScope()
    scopes.push(scope)
    scope.run(() => usePreviewEscape(() => open.value, () => { open.value = false }))
    return { open, scope }
  }
  return { key, preview, target }
}

describe('preview Escape ownership', () => {
  it('closes just the latest opened preview and consumes Escape', () => {
    const { key, preview } = setup()
    const first = preview(), second = preview()
    first.open.value = true; second.open.value = true
    expect(key('Escape').defaultPrevented).toBe(true)
    expect(second.open.value).toBe(false)
    expect(first.open.value).toBe(true)
    key('Escape')
    expect(first.open.value).toBe(false)
    expect(key('Escape').defaultPrevented).toBe(false)
  })
  it('does not leak Escape to later terminal handlers and leaves other keys alone', () => {
    const { key, preview, target } = setup()
    const p = preview(); p.open.value = true
    const received: string[] = []
    target.addEventListener('keydown', e => received.push((e as KeyboardEvent).key))
    key('Escape'); key('a')
    expect(received).toEqual(['a'])
  })
  it('unmount and hidden state release ownership; reopening takes the top position', () => {
    const { key, preview } = setup()
    const first = preview(), second = preview()
    first.open.value = true; second.open.value = true
    first.open.value = false; first.open.value = true
    key('Escape')
    expect(first.open.value).toBe(false)
    expect(second.open.value).toBe(true)
    second.scope.stop()
    expect(key('Escape').defaultPrevented).toBe(false)
  })
})
