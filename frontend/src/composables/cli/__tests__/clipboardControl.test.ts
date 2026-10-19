import { expect, it } from 'bun:test'

it('recovers clipboard gaps once and preserves cursor/target identity across socket duplication and app restart', async () => {
  const child = Bun.spawn([process.execPath, new URL('./clipboardControl.fixture.ts', import.meta.url).pathname], { stdout: 'pipe', stderr: 'pipe' })
  const [stdout, stderr, code] = await Promise.all([
    new Response(child.stdout).text(), new Response(child.stderr).text(), child.exited,
  ])
  expect({ code, stderr }).toEqual({ code: 0, stderr: '' })
  expect(stdout).toContain('clipboard control oracle passed')
})
