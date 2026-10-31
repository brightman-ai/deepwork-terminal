import { expect, it } from 'bun:test'

it('migrates and persists shortcut settings with an isolated server-store singleton', async () => {
  // Store API mocks and hydration are process-wide; file ordering must not
  // substitute useServerStore.test's history seed for this suite's legacy seed.
  const child = Bun.spawn([process.execPath, 'test', import.meta.dir + '/useShortcutsConfig.fixture.ts'], {
    stdout: 'pipe', stderr: 'pipe',
  })
  const [stdout, stderr, code] = await Promise.all([
    new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited,
  ])
  expect({ code, output: code ? stdout + stderr : '' }).toEqual({ code: 0, output: '' })
  expect(Number(/(\d+) pass/.exec(stderr)?.[1])).toBeGreaterThan(0)
  expect(stderr).toContain('0 fail')
})
