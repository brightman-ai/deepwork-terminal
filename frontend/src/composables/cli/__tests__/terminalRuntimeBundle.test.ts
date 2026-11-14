import { afterAll, expect, test } from 'bun:test'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { build, loadConfigFromFile } from 'vite'
import type { Terminal } from '@xterm/xterm'

// Importing xterm directly in Bun selects its working CommonJS entry and misses this bug.
// Exercise the minified browser runtime with the app's actual resolution configuration.
const dir = mkdtempSync(fileURLToPath(new URL('../../../../node_modules/.terminal-runtime-', import.meta.url)))
afterAll(() => rmSync(dir, { recursive: true, force: true }))

test('production terminal answers capability queries and keeps rendering subsequent output', async () => {
  const entry = join(dir, 'entry.js')
  writeFileSync(entry, "import '@xterm/xterm/css/xterm.css'; export { Terminal } from '@xterm/xterm'")
  const loaded = await loadConfigFromFile({ command: 'build', mode: 'production' }, fileURLToPath(new URL('../../../../vite.config.ts', import.meta.url)))
  if (!loaded) throw new Error('Missing application build configuration')
  const result = await build({
    configFile: false,
    logLevel: 'silent',
    resolve: loaded.config.resolve,
    build: {
      write: false,
      minify: 'esbuild',
      rollupOptions: { input: entry, preserveEntrySignatures: 'strict' },
    },
  })
  if (Array.isArray(result) || !('output' in result)) throw new Error('Expected one browser bundle')
  const chunk = result.output.find(part => part.type === 'chunk' && part.isEntry)
  if (!chunk || chunk.type !== 'chunk') throw new Error('Missing terminal runtime')
  const bundle = join(dir, 'runtime.mjs')
  writeFileSync(bundle, chunk.code)
  const runtime = await import(bundle) as { Terminal: new (options: object) => Terminal }
  const term = new runtime.Terminal({ cols: 80, rows: 6, allowProposedApi: true })
  let replies = ''
  term.onData(data => { replies += data })
  const write = (data: string) => new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('Terminal parser stopped consuming output')), 1000)
    term.write(data, () => { clearTimeout(timeout); resolve() })
  })
  try {
    // agy's first query, split across transport chunks, followed by an unknown capability.
    await write('\x1b[?202')
    await write('6$p\x1b[?2027$p\x1b[?2004$p')
    expect(replies).toBe('\x1b[?2026;2$y\x1b[?2027;0$y\x1b[?2004;2$y')
    replies = ''
    await write('\x1b[?2026h\x1b[?2026$p\x1b[?2026l\x1b[?2026$p\x1b[4h\x1b[4$p\x1b[4l\x1b[4$p')
    expect(replies).toBe('\x1b[?2026;1$y\x1b[?2026;2$y\x1b[4;1$y\x1b[4;2$y')
    await write('\x1b[?1049h\x1b[H\x1b[2JWelcome\r\nSelect login method:\r\n> 1. Example provider')
    expect(term.buffer.active.type).toBe('alternate')
    expect(term.buffer.active.getLine(1)?.translateToString(true)).toBe('Select login method:')
    await write('\r\nStill accepting output')
    expect(term.buffer.active.getLine(3)?.translateToString(true)).toBe('Still accepting output')
  } finally {
    term.dispose()
  }
})
