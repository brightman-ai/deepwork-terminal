import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'

describe('UsageChip entry presence', () => {
  test('the topbar entry is not conditional on asynchronous usage data', () => {
    const source = readFileSync(new URL('../UsageChip.vue', import.meta.url), 'utf8')
    const entry = source.match(/<div\b[^>]*class="uchip-wrap"[^>]*>/)?.[0]

    expect(entry).toBeDefined()
    expect(entry).not.toContain('v-if')
    expect(source).toContain(':aria-expanded="open"')
  })

  test('Agent lifecycle views never merge interruption and error into one anomaly count', () => {
    const compact = readFileSync(new URL('../UsageChip.vue', import.meta.url), 'utf8')
    const detail = readFileSync(new URL('../AgentReportDetail.vue', import.meta.url), 'utf8')

    for (const source of [compact, detail]) {
      expect(source).not.toMatch(/interrupted\s*\+\s*[^}\n]*errors[^\n]*异常/)
    }
    expect(detail).toContain('{{ report.summary.interrupted }} 中断 · {{ report.summary.errors }} 错误')
    expect(detail).toContain('{{ report.summary.delegated_lifecycle.interrupted }} 中断 · {{ report.summary.delegated_lifecycle.errors }} 错误')
  })
})
