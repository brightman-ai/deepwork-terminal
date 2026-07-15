import { describe, expect, test } from 'bun:test'
import { normalizeAgentActivityReport } from '../agentReportContract'
import { normalizeAgentDetailReport } from '../agentDetailContract'

describe('Agent report transport normalization', () => {
  test('legacy null collections become the canonical empty shape', () => {
    const report = normalizeAgentActivityReport({
      runtime_profiles: null,
      top_cost_models: { codex: null },
      comparisons: null,
    })

    expect(report.runtime_profiles).toEqual([])
    expect(report.top_cost_models).toEqual({ codex: [] })
    expect(report.comparisons).toEqual([])
    expect(report.health.state).toBe('insufficient_evidence')
    expect(report.health.axes).toEqual([])
    expect(report.tools.calls).toBe(0)
    expect(report.artifacts.by_kind).toEqual({})
  })

  test('legacy model rows gain explicit missing throughput evidence', () => {
    const report = normalizeAgentActivityReport({
      runtime_profiles: [], comparisons: [],
      top_cost_models: { claude: [{ runtime: 'claude', model: 'opus', request_n: 2 }] },
    })

    const row = report.top_cost_models.claude?.[0]
    expect(row?.observed_response_output_tokens).toBe(0)
    expect(row?.observed_response_duration_seconds).toBe(0)
    expect(row?.response_speed_coverage.state).toBe('missing')
  })

  test('legacy runtime rows gain explicit empty artifact evidence', () => {
    const report = normalizeAgentActivityReport({
      runtime_profiles: [{ runtime: 'claude', tools: null }],
      top_cost_models: {}, comparisons: [],
    })

    expect(report.runtime_profiles[0]?.artifacts.events).toBe(0)
    expect(report.runtime_profiles[0]?.artifacts.by_kind).toEqual({})
    expect(report.runtime_profiles[0]?.artifact_coverage.state).toBe('missing')
    expect(report.runtime_profiles[0]?.resource_yield.formula_version).toBe('host-contract-missing')
    expect(report.runtime_profiles[0]?.resource_yield.cost_coverage.state).toBe('missing')
  })

  test('detail rows never expose nullable collections to the view', () => {
    const detail = normalizeAgentDetailReport({
      report: { runtime_profiles: null, top_cost_models: null, comparisons: null },
      filters: null,
      tasks: [{ assignments: null, requests: null, artifacts: null }],
      metrics: null,
    })

    expect(detail.filters.runtimes).toEqual([])
    expect(detail.tasks[0]?.assignments).toEqual([])
    expect(detail.tasks[0]?.requests).toEqual([])
    expect(detail.tasks[0]?.artifacts).toEqual([])
    expect(detail.metrics).toEqual([])
  })
})
