import { describe, it, expect } from 'bun:test'
import { computeVisibleTabOrder } from '../useVisibleTabOrder'

describe('computeVisibleTabOrder', () => {
  it('assigns 1-based positions in the given order', () => {
    const order = computeVisibleTabOrder(['a', 'b', 'c'])
    expect(order.position.get('a')).toBe(1)
    expect(order.position.get('b')).toBe(2)
    expect(order.position.get('c')).toBe(3)
  })

  it('idAtPosition maps Alt+N to the Nth visible tab', () => {
    const order = computeVisibleTabOrder(['a', 'b', 'c'])
    expect(order.idAtPosition(1)).toBe('a')
    expect(order.idAtPosition(3)).toBe('c')
    expect(order.idAtPosition(4)).toBeUndefined()
    expect(order.idAtPosition(0)).toBeUndefined()
  })

  it('nextId advances and wraps at the end', () => {
    const order = computeVisibleTabOrder(['a', 'b', 'c'])
    expect(order.nextId('a')).toBe('b')
    expect(order.nextId('b')).toBe('c')
    expect(order.nextId('c')).toBe('a')
  })

  it('prevId retreats and wraps at the start', () => {
    const order = computeVisibleTabOrder(['a', 'b', 'c'])
    expect(order.prevId('c')).toBe('b')
    expect(order.prevId('b')).toBe('a')
    expect(order.prevId('a')).toBe('c')
  })

  it('an unknown current id falls back to the first (next) or last (prev) tab', () => {
    const order = computeVisibleTabOrder(['a', 'b', 'c'])
    expect(order.nextId('ghost')).toBe('a')
    expect(order.prevId('ghost')).toBe('c')
  })

  it('a single-tab list wraps to itself', () => {
    const order = computeVisibleTabOrder(['only'])
    expect(order.nextId('only')).toBe('only')
    expect(order.prevId('only')).toBe('only')
  })

  it('an empty list resolves nothing', () => {
    const order = computeVisibleTabOrder([])
    expect(order.nextId('a')).toBeUndefined()
    expect(order.prevId('a')).toBeUndefined()
    expect(order.idAtPosition(1)).toBeUndefined()
  })
})
