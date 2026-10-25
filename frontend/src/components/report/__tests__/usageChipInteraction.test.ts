import { expect, it } from 'bun:test'

it('real UsageChip time-window buttons request, recover and retain their own report', async () => {
  const child = Bun.spawn([process.execPath, 'run', new URL('./usageChipInteraction.fixture.ts', import.meta.url).pathname], {stdout:'pipe',stderr:'pipe'})
  const [status, stdout, stderr] = await Promise.all([child.exited,new Response(child.stdout).text(),new Response(child.stderr).text()])
  expect({status,stderr}).toEqual({status:0,stderr:''})
  const evidence = JSON.parse(stdout.trim())
  expect(evidence.passed).toBe(true)
  expect(evidence.selection_requests).toContain('7d')
  expect(evidence.selection_requests).toContain('14d')
  expect(evidence.selection_requests).toContain('30d')
  expect(evidence.cold_recovery && evidence.error_retry && evidence.late_window_isolation).toBe(true)
  expect(evidence.escape_closes).toBe(true)
})
