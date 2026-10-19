import { expect, it } from 'bun:test'

it('preserves snapshot ownership on cancellation, failure and auth rotation', async () => {
  // Bun module mocks are process-wide. Isolate boundary stubs from the rest of the suite.
  const child = Bun.spawn([process.execPath, new URL('./overview.fixture.ts', import.meta.url).pathname], { stdout: 'pipe', stderr: 'pipe' })
  const [stdout, stderr, code] = await Promise.all([
    new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited,
  ])
  expect({ code, stderr }).toEqual({ code: 0, stderr: '' })
  expect(stdout).toContain('overview lifecycle oracle passed')
})
