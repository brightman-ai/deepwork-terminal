import { describe, expect, test } from 'bun:test'

const localStorageStub = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
}

Object.defineProperty(globalThis, 'localStorage', {
  value: localStorageStub,
  configurable: true,
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
