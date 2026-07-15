import { normalizeAgentActivityReport, type AgentActivityReport, type AgentReportWindow } from './agentReportContract'

export interface AgentDetailFilter {
  window: AgentReportWindow
  timezone: string
  project?: string
  task_class?: string
  risk?: string
  outcome?: string
  runtime?: string
  cursor?: string
  limit?: number
}

export interface AgentDetailTask {
  id: string; runtime: string; project?: string; source_ref?: string
  task_profile: { project?: string; task_class?: string; scope_band?: string; risk?: string; oracle?: string; source?: string; confidence?: number }
  status: string; outcome: string; started_at?: string; ended_at?: string
  output_tokens: number; tool_calls: number; agent_instances: number
  assignments: Array<{ id: string; agent_instance_id: string; attempt: number; status: string }>
  requests: Array<{
    id: string; model?: string; effort?: string; service_tier?: string; output_tokens: number
    api_equivalent?: { amount: number; currency: string }; cost_complete: boolean
  }>
  artifacts: Array<{ path: string; kind: string; additions: number; deletions: number; attribution: string; excluded: boolean }>
  diagnostics?: string[]
}

export interface AgentDetailReport {
  schema_version: string
  report: AgentActivityReport
  filters: { projects: string[]; task_classes: string[]; risks: string[]; outcomes: string[]; runtimes: string[] }
  tasks: AgentDetailTask[]
  next_cursor?: string
  metrics: Array<{ id: string; grain: string; formula: string; unit: string; provenance: string; coverage: string; comparison_policy: string }>
}

export function normalizeAgentDetailReport(value: unknown): AgentDetailReport {
  if (!value || typeof value !== 'object') throw new Error('invalid Agent detail report')
  const next = value as AgentDetailReport
  next.report = normalizeAgentActivityReport(next.report)
  next.filters = next.filters ?? { projects: [], task_classes: [], risks: [], outcomes: [], runtimes: [] }
  next.filters.projects = Array.isArray(next.filters.projects) ? next.filters.projects : []
  next.filters.task_classes = Array.isArray(next.filters.task_classes) ? next.filters.task_classes : []
  next.filters.risks = Array.isArray(next.filters.risks) ? next.filters.risks : []
  next.filters.outcomes = Array.isArray(next.filters.outcomes) ? next.filters.outcomes : []
  next.filters.runtimes = Array.isArray(next.filters.runtimes) ? next.filters.runtimes : []
  next.tasks = Array.isArray(next.tasks) ? next.tasks : []
  next.tasks.forEach((task) => {
    task.assignments = Array.isArray(task.assignments) ? task.assignments : []
    task.requests = Array.isArray(task.requests) ? task.requests : []
    task.artifacts = Array.isArray(task.artifacts) ? task.artifacts : []
  })
  next.metrics = Array.isArray(next.metrics) ? next.metrics : []
  return next
}
