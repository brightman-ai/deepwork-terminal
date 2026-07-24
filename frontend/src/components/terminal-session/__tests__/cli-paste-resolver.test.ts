import { afterAll, describe, expect, test } from 'bun:test'

const localStorageStub = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
}

// Snapshot whatever this process had before we stub globals here, so we can
// put things back exactly as we found them once this file's tests are done.
// Without this, `Object.defineProperty` (unlike a plain assignment) defaults
// to `writable: false`, so any later test file in the same bun process that
// does `globalThis.window = ...` (a plain assignment) throws
// "Attempted to assign to readonly property" — this file's stub otherwise
// leaks across test files.
const originalWindow = (globalThis as any).window
const originalLocalStorage = (globalThis as any).localStorage

Object.defineProperty(globalThis, 'localStorage', {
  value: localStorageStub,
  configurable: true,
})

// The resolver module transitively imports useCliAuth, which reads
// window.location at module-load time. Stub a minimal window so the import
// graph evaluates under bun's DOM-less test runtime.
Object.defineProperty(globalThis, 'window', {
  value: {
    location: { search: '', pathname: '/', hash: '' },
    history: { replaceState: () => {} },
    localStorage: localStorageStub,
  },
  configurable: true,
})

afterAll(() => {
  // `delete` (not reassignment) is required to drop the non-writable
  // property descriptor installed above before restoring the prior value.
  delete (globalThis as any).window
  delete (globalThis as any).localStorage
  if (originalWindow !== undefined) (globalThis as any).window = originalWindow
  if (originalLocalStorage !== undefined) (globalThis as any).localStorage = originalLocalStorage
})

type ClipboardSnapshot = import('../../../composables/cli/useCliPasteResolver').ClipboardSnapshot
type PasteEnvironment = import('../../../composables/cli/useCliPasteResolver').PasteEnvironment

async function helpers() {
  return import('../../../composables/cli/useCliPasteResolver')
}

function snapshot(partial: Partial<ClipboardSnapshot>): ClipboardSnapshot {
  return {
    types: [],
    files: [],
    items: [],
    stringReads: [],
    hasPlainText: false,
    hasUriList: false,
    hasFileHint: false,
    hasImageHint: false,
    ...partial,
  }
}

function env(partial: Partial<PasteEnvironment>): PasteEnvironment {
  return {
    runtimeMode: 'pc-browser',
    isWailsDesktop: false,
    canUseServerNativeClipboard: false,
    isLoopbackHost: false,
    hostname: 'remote.local',
    protocol: 'http:',
    isSecureContext: false,
    supportsAsyncClipboardRead: false,
    ...partial,
  }
}

describe('CliPasteResolver pure helpers', () => {
  test('shell-quotes file paths only when needed and appends a terminal separator', async () => {
    const { formatPathsForPty } = await helpers()
    expect(formatPathsForPty(['tmp/clip/a.png'])).toBe('tmp/clip/a.png ')
    expect(formatPathsForPty(['/Users/me/My File.png'])).toBe("'/Users/me/My File.png' ")
    expect(formatPathsForPty(["/tmp/it's.png"])).toBe("'/tmp/it'\\''s.png' ")
  })

  test('formats duplicate injected paths once while preserving order', async () => {
    const { formatPathsForPty, uniqueOrderedPaths } = await helpers()
    const paths = ['tmp/clip/a.png', 'tmp/clip/a.png', 'tmp/clip/b.png', 'tmp/clip/a.png']
    expect(uniqueOrderedPaths(paths)).toEqual(['tmp/clip/a.png', 'tmp/clip/b.png'])
    expect(formatPathsForPty(paths)).toBe('tmp/clip/a.png tmp/clip/b.png ')
  })

  test('prefixes injected paths with @ so CLIs treat them as file references', async () => {
    const { withReferencePrefix } = await helpers()
    // Bare paths gain an @ prefix.
    expect(withReferencePrefix(['tmp/clip/07-04-20/xxx.png'])).toEqual(['@tmp/clip/07-04-20/xxx.png'])
    expect(withReferencePrefix(['tmp/clip/a.png', 'tmp/clip/b.png'])).toEqual([
      '@tmp/clip/a.png',
      '@tmp/clip/b.png',
    ])
    // Idempotent: an already-prefixed path is not double-prefixed.
    expect(withReferencePrefix(['@tmp/clip/a.png', 'tmp/clip/b.png'])).toEqual([
      '@tmp/clip/a.png',
      '@tmp/clip/b.png',
    ])
  })

  test('end-to-end injection prefixes @ then keeps quoting + trailing space', async () => {
    const { formatPathsForPty, withReferencePrefix, uniqueOrderedPaths } = await helpers()
    // Mirrors injectPaths: dedupe -> @-prefix -> format for PTY.
    const injected = (paths: string[]) => formatPathsForPty(withReferencePrefix(uniqueOrderedPaths(paths)))
    expect(injected(['tmp/clip/07-04-20/xxx.png'])).toBe('@tmp/clip/07-04-20/xxx.png ')
    expect(injected(['tmp/clip/a.png', 'tmp/clip/a.png', 'tmp/clip/b.png'])).toBe('@tmp/clip/a.png @tmp/clip/b.png ')
    // Paths needing shell-quoting keep the @ inside the quotes.
    expect(injected(['/Users/me/My File.png'])).toBe("'@/Users/me/My File.png' ")
  })

  test('probes native clipboard only when server-local runtime also has a file hint', async () => {
    const { shouldPreferNativeClipboardPaths, shouldProbeNativeClipboard } = await helpers()
    const fileHint = snapshot({ hasFileHint: true })
    const fileObjectHint = snapshot({
      hasFileHint: true,
      files: [new File(['same host file'], 'note.txt', { type: 'text/plain' })],
    })
    expect(shouldProbeNativeClipboard(fileHint, env({ canUseServerNativeClipboard: true }))).toBe(true)
    expect(shouldPreferNativeClipboardPaths(fileObjectHint, env({ canUseServerNativeClipboard: true }))).toBe(true)
    expect(shouldProbeNativeClipboard(fileHint, env({ canUseServerNativeClipboard: false }))).toBe(false)
    expect(shouldPreferNativeClipboardPaths(fileObjectHint, env({ canUseServerNativeClipboard: false }))).toBe(false)
    expect(shouldProbeNativeClipboard(snapshot({ hasPlainText: true }), env({ canUseServerNativeClipboard: true }))).toBe(false)
  })
})
