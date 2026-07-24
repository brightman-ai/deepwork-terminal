import { describe, expect, test } from 'bun:test'
import { shouldProjectKeystroke, type KeystrokeLike } from '../useKeyCastrHud'

/**
 * Projection criterion (request.md §8.3 SSOT): a key projects into the KeyCastr HUD only if
 * "the terminal alone doesn't show you pressed it." KC-1/KC-2 pin this down per-key so the
 * behavior can't silently regress back toward "shows everything" (the bug this whole change
 * exists to kill) or overshoot into "shows nothing" (which would defeat KC-2).
 */
function key(k: string, mods: Partial<KeystrokeLike> = {}): KeystrokeLike {
  return { key: k, ctrlKey: false, altKey: false, metaKey: false, shiftKey: false, isComposing: false, ...mods }
}

describe('shouldProjectKeystroke — projects only what the terminal itself does not reveal', () => {
  describe('projects (invisible on the terminal)', () => {
    test('Ctrl-C — modifier chord', () => {
      expect(shouldProjectKeystroke(key('c', { ctrlKey: true }))).toBe(true)
    })
    test('tmux prefix chord (Ctrl-B)', () => {
      expect(shouldProjectKeystroke(key('b', { ctrlKey: true }))).toBe(true)
    })
    test('Alt/Option combo', () => {
      expect(shouldProjectKeystroke(key('b', { altKey: true }))).toBe(true)
    })
    test('Cmd/Meta combo', () => {
      expect(shouldProjectKeystroke(key('k', { metaKey: true }))).toBe(true)
    })
    test('Escape', () => {
      expect(shouldProjectKeystroke(key('Escape'))).toBe(true)
    })
    test('Tab', () => {
      expect(shouldProjectKeystroke(key('Tab'))).toBe(true)
    })
    for (const k of ['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight']) {
      test(`arrow key ${k}`, () => {
        expect(shouldProjectKeystroke(key(k))).toBe(true)
      })
    }
    for (const k of ['Home', 'End', 'PageUp', 'PageDown']) {
      test(k, () => {
        expect(shouldProjectKeystroke(key(k))).toBe(true)
      })
    }
    for (const k of ['F1', 'F5', 'F12']) {
      test(`function key ${k}`, () => {
        expect(shouldProjectKeystroke(key(k))).toBe(true)
      })
    }
    test('Insert', () => {
      expect(shouldProjectKeystroke(key('Insert'))).toBe(true)
    })
    test('Delete', () => {
      expect(shouldProjectKeystroke(key('Delete'))).toBe(true)
    })
    test('unmapped named key falls back to projecting (unknown visible effect, err toward showing)', () => {
      expect(shouldProjectKeystroke(key('CapsLock'))).toBe(true)
    })
  })

  describe('does NOT project (terminal already shows the effect)', () => {
    test('plain printable letter', () => {
      expect(shouldProjectKeystroke(key('a'))).toBe(false)
    })
    test('printable digit', () => {
      expect(shouldProjectKeystroke(key('7'))).toBe(false)
    })
    test('shifted printable char (capital letter) — still just a visible char', () => {
      expect(shouldProjectKeystroke(key('A', { shiftKey: true }))).toBe(false)
    })
    test('Space', () => {
      expect(shouldProjectKeystroke(key(' '))).toBe(false)
    })
    test('Enter', () => {
      expect(shouldProjectKeystroke(key('Enter'))).toBe(false)
    })
    test('Backspace', () => {
      expect(shouldProjectKeystroke(key('Backspace'))).toBe(false)
    })
    test('a lone Shift press (no other key yet)', () => {
      expect(shouldProjectKeystroke(key('Shift'))).toBe(false)
    })
    test('a lone Control press (no other key yet)', () => {
      expect(shouldProjectKeystroke(key('Control'))).toBe(false)
    })
    test('IME composition in progress', () => {
      expect(shouldProjectKeystroke(key('a', { isComposing: true }))).toBe(false)
    })
    test('IME composition sentinel key (Process)', () => {
      expect(shouldProjectKeystroke(key('Process'))).toBe(false)
    })
    test('IME committed result (a CJK char lands after composition ends)', () => {
      expect(shouldProjectKeystroke(key('中'))).toBe(false)
    })
  })

  /**
   * The rule-6 "err toward showing it" fallback is right for a key we can NAME but whose terminal
   * effect we don't know (CapsLock). It is wrong for a key the engine itself could not name: the
   * pill would read `UNIDENTIFIED`, which identifies nothing. The SSOT criterion is "project what
   * the user cannot SEE" — a pill that cannot say WHICH key was pressed carries zero information
   * and is pure occlusion, i.e. exactly the cost R2 set out to remove.
   */
  describe('unidentifiable keys — zero information, so never projected', () => {
    test('Unidentified (Android soft keyboard / IME path)', () => {
      expect(shouldProjectKeystroke(key('Unidentified'))).toBe(false)
    })
    test('Unidentified stays silent even as a modifier chord (⌃UNIDENTIFIED names no key either)', () => {
      expect(shouldProjectKeystroke(key('Unidentified', { ctrlKey: true }))).toBe(false)
      expect(shouldProjectKeystroke(key('Unidentified', { altKey: true }))).toBe(false)
      expect(shouldProjectKeystroke(key('Unidentified', { metaKey: true }))).toBe(false)
    })
    test('empty key string (would render an empty pill)', () => {
      expect(shouldProjectKeystroke(key(''))).toBe(false)
      expect(shouldProjectKeystroke(key('', { ctrlKey: true }))).toBe(false)
    })
    test('a NAMED unknown key is still projected — the fallback is intact, not blanket-disabled', () => {
      expect(shouldProjectKeystroke(key('CapsLock'))).toBe(true)
      expect(shouldProjectKeystroke(key('ContextMenu'))).toBe(true)
    })
  })
})
