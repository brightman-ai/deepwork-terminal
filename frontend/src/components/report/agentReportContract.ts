export type AgentReportWindow = '24h' | '7d' | '14d' | '30d'

export interface ReportCoverage {
  state: 'complete' | 'partial' | 'missing'
  observed_n: number
  eligible_n: number
  ratio?: number
  provenance: string[]
  diagnostics?: string[]
}

export interface AgentActivitySummary {
  work_items: number; submitted: number; started: number; completed: number
  interrupted: number; errors: number; never_started: number; open: number
  verified_pass: number; verified_fail: number; human_rework: number; completed_unverified: number
  wall_seconds: number; cumulative_seconds: number; average_concurrency?: number
  agent_instances: number; agent_assignments: number; model_requests: number; tool_calls: number
  assignment_lifecycle: { submitted: number; started: number; completed: number; interrupted: number; errors: number; never_started: number; open: number }
  delegated_lifecycle: { submitted: number; started: number; completed: number; interrupted: number; errors: number; never_started: number; open: number }
}

export interface AgentRuntimeProfile {
  runtime: string; work_items: number; completed: number; interrupted: number; errors: number
  active_seconds: number; output_tokens: number; model_requests: number
  agent_instances: number; tool_calls: number; generation_tokens_per_second?: number
  observed_response_tokens_per_second?: number
  ttft_median_seconds?: number; observed_first_response_median_seconds?: number
  generation_speed_coverage: ReportCoverage; response_speed_coverage: ReportCoverage; speed_coverage: ReportCoverage
  ttft_coverage: ReportCoverage; first_response_coverage: ReportCoverage
  artifacts: ArtifactTotals; artifact_coverage: ReportCoverage; resource_yield: RuntimeResourceYield
  tools: ToolExecutionSummary
}

export interface ArtifactLineTotals {
  additions: number; deletions: number; written_lines: number; files: number; created_files: number; modified_files: number
}

export interface ArtifactTotals {
  by_kind: Record<string, ArtifactLineTotals>
  unattributed: ArtifactLineTotals
  excluded_files: number; events: number
}

export interface RuntimeResourceYield {
  formula_version: string
  reviewable_written_lines: number
  request_output_tokens: number
  api_equivalent?: { amount: number; currency: string }
  cost_coverage: ReportCoverage
  token_coverage: ReportCoverage
  written_lines_per_thousand_output_tokens?: number
  written_lines_per_active_hour?: number
  api_equivalent_per_thousand_written_lines?: { amount: number; currency: string }
  diagnostics: string[]
}

export interface ToolExecutionSummary {
  calls: number; completed: number; errors: number; interrupted: number
  open: number; unknown: number
  total_duration_seconds: number; average_duration_seconds?: number; timing_coverage: ReportCoverage
}

export type AgentHealthState = 'healthy' | 'attention' | 'critical' | 'insufficient_evidence'
export interface AgentHealthReason {
  code: string; state: AgentHealthState; message: string; observed?: number; unit?: string
  comparator?: string; threshold?: number; minimum_n?: number; evidence_ref: string; coverage_ref?: string
}
export interface AgentHealthAxis {
  key: 'execution' | 'delivery' | 'efficiency'; label: string; state: AgentHealthState
  headline: string; reasons: AgentHealthReason[]
}
export interface AgentHealthAssessment {
  policy_version: string; state: AgentHealthState; label: string; headline: string
  axes: AgentHealthAxis[]; reasons: AgentHealthReason[]
}

export interface ModelCostRow {
  runtime: string; model: string; request_n: number; efforts?: string[]; service_tiers?: string[]
  cost?: { amount: number; currency: string }; cost_coverage: ReportCoverage
  credits?: number; credit_coverage: ReportCoverage; fast_multipliers?: number[]
  observed_response_tokens_per_second?: number
  observed_response_output_tokens: number; observed_response_duration_seconds: number
  response_speed_coverage: ReportCoverage
}

export interface AgentActivityReport {
  schema_version: string; window: AgentReportWindow; timezone: string
  start: string; end: string; generated_at: string; summary: AgentActivitySummary
  runtime_profiles: AgentRuntimeProfile[]; top_cost_models: Record<string, ModelCostRow[]>
  artifacts: ArtifactTotals
  tools: ToolExecutionSummary
  health: AgentHealthAssessment
  coverage: Record<string, ReportCoverage>
  observability: {
    projection: {
      state: 'complete' | 'partial' | 'missing'; mode: 'full_rebuild' | 'incremental' | 'cached' | 'in_memory'
      refreshed_at: string; source_high_watermark?: string; source_files: number; changed_files: number
      index_schema?: string; diagnostics?: string[]
    }
    stages: Record<string, ReportCoverage>
  }
  comparisons: Array<{ status: string; reason: string; recommendation?: string }>
}

// One transport boundary owns compatibility with older hosts. Components consume the
// canonical shape and never scatter `?.length` workarounds across the view hierarchy.
export function normalizeAgentActivityReport(value: unknown): AgentActivityReport {
  if (!value || typeof value !== 'object') throw new Error('invalid Agent activity report')
  const report = value as AgentActivityReport
  report.runtime_profiles = Array.isArray(report.runtime_profiles) ? report.runtime_profiles : []
  report.comparisons = Array.isArray(report.comparisons) ? report.comparisons : []
  const missingCoverage: ReportCoverage = { state: 'missing', observed_n: 0, eligible_n: 0, provenance: [], diagnostics: ['host_contract_missing'] }
  const emptyLineTotals = (): ArtifactLineTotals => ({ additions: 0, deletions: 0, written_lines: 0, files: 0, created_files: 0, modified_files: 0 })
  const emptyArtifacts = (): ArtifactTotals => ({ by_kind: {}, unattributed: emptyLineTotals(), excluded_files: 0, events: 0 })
  const emptyResourceYield = (): RuntimeResourceYield => ({
    formula_version: 'host-contract-missing', reviewable_written_lines: 0, request_output_tokens: 0,
    cost_coverage: missingCoverage, token_coverage: missingCoverage, diagnostics: ['host_contract_missing'],
  })
  report.top_cost_models = report.top_cost_models && typeof report.top_cost_models === 'object'
    ? report.top_cost_models
    : {}
  for (const [runtime, rows] of Object.entries(report.top_cost_models)) {
    if (!Array.isArray(rows)) {
      report.top_cost_models[runtime] = []
      continue
    }
    for (const row of rows) {
      row.observed_response_output_tokens ??= 0
      row.observed_response_duration_seconds ??= 0
      row.response_speed_coverage ??= missingCoverage
    }
  }
  const emptyTools: ToolExecutionSummary = { calls: 0, completed: 0, errors: 0, interrupted: 0, open: 0, unknown: 0, total_duration_seconds: 0, timing_coverage: missingCoverage }
  report.tools = report.tools && typeof report.tools === 'object' ? report.tools : emptyTools
  report.tools.open ??= 0
  report.tools.unknown ??= 0
  report.artifacts = report.artifacts && typeof report.artifacts === 'object' ? report.artifacts : emptyArtifacts()
  report.artifacts.by_kind = report.artifacts.by_kind && typeof report.artifacts.by_kind === 'object' ? report.artifacts.by_kind : {}
  report.artifacts.unattributed ??= emptyLineTotals()
  const emptyLifecycle = { submitted: 0, started: 0, completed: 0, interrupted: 0, errors: 0, never_started: 0, open: 0 }
  if (report.summary) {
    report.summary.assignment_lifecycle ??= { ...emptyLifecycle }
    report.summary.delegated_lifecycle ??= { ...emptyLifecycle }
  }
  for (const runtime of report.runtime_profiles) {
    runtime.tools = runtime.tools && typeof runtime.tools === 'object' ? runtime.tools : emptyTools
    runtime.tools.open ??= 0
    runtime.tools.unknown ??= 0
    runtime.generation_speed_coverage = runtime.generation_speed_coverage ?? runtime.speed_coverage ?? missingCoverage
    runtime.response_speed_coverage = runtime.response_speed_coverage ?? missingCoverage
    runtime.ttft_coverage = runtime.ttft_coverage ?? missingCoverage
    runtime.first_response_coverage = runtime.first_response_coverage ?? missingCoverage
    runtime.artifacts = runtime.artifacts && typeof runtime.artifacts === 'object' ? runtime.artifacts : emptyArtifacts()
    runtime.artifacts.by_kind = runtime.artifacts.by_kind && typeof runtime.artifacts.by_kind === 'object' ? runtime.artifacts.by_kind : {}
    runtime.artifacts.unattributed ??= emptyLineTotals()
    runtime.artifact_coverage = runtime.artifact_coverage ?? missingCoverage
    runtime.resource_yield = runtime.resource_yield && typeof runtime.resource_yield === 'object' ? runtime.resource_yield : emptyResourceYield()
    runtime.resource_yield.cost_coverage ??= missingCoverage
    runtime.resource_yield.token_coverage ??= missingCoverage
    runtime.resource_yield.diagnostics = Array.isArray(runtime.resource_yield.diagnostics) ? runtime.resource_yield.diagnostics : []
  }
  report.health = report.health && typeof report.health === 'object' ? report.health : {
    policy_version: 'host-contract-missing', state: 'insufficient_evidence', label: '证据不足',
    headline: '当前 Host 未返回健康判定，请刷新或升级后重试', axes: [], reasons: [],
  }
  report.health.axes = Array.isArray(report.health.axes) ? report.health.axes : []
  report.health.reasons = Array.isArray(report.health.reasons) ? report.health.reasons : []
  return report
}
