import { expect, it } from 'bun:test'
it('reloads the real FilesPanel through terminal clicks and refresh controls', () => {
  const result=Bun.spawnSync([process.execPath,'run',import.meta.dir+'/previewRefresh.fixture.ts'],{cwd:process.cwd(),stdout:'pipe',stderr:'pipe'})
  expect(new TextDecoder().decode(result.stderr)).not.toContain('error:')
  expect(result.exitCode).toBe(0)
  expect(JSON.parse(new TextDecoder().decode(result.stdout).trim()).passed).toBe(true)
})
