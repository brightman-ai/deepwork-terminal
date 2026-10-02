import { ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { normalizeAgentDetailReport, type AgentDetailFilter, type AgentDetailReport, type AgentOutcomeEvidence } from './agentDetailContract'

export * from './agentDetailContract'

export type HumanOutcome = 'human_accepted' | 'human_rework'

export function useAgentDetail() {
  const { cliFetch } = useCliAuth()
  const detail = ref<AgentDetailReport | null>(null)
  const loading = ref(false)
  const error = ref('')
  let generation = 0
  let loadedPages = 0

  async function load(filter: AgentDetailFilter, append = false): Promise<boolean> {
    const requestGeneration = ++generation
    loading.value = true
    error.value = ''
    const params = new URLSearchParams()
    for (const [key, value] of Object.entries(filter)) {
      if (value !== undefined && value !== '') params.set(key, String(value))
    }
    try {
      const response = await cliFetch(cliApi(`/usage/agent-report/detail?${params}`), { headers: { Accept: 'application/json' } })
      if (!response.ok) {
        if (requestGeneration === generation) error.value = `详细报表加载失败 (${response.status})`
        return false
      }
      const next = normalizeAgentDetailReport(await response.json())
      if (requestGeneration !== generation) return false
      if (append && detail.value) next.tasks = [...detail.value.tasks, ...next.tasks]
      detail.value = next
      loadedPages = append ? loadedPages + 1 : 1
      return true
    } catch {
      if (requestGeneration === generation) error.value = '详细报表加载失败'
      return false
    } finally {
      if (requestGeneration === generation) loading.value = false
    }
  }

  async function reloadLoadedPages(filter: AgentDetailFilter): Promise<boolean> {
    const pageCount = Math.max(1, loadedPages)
    if (!await load(filter)) return false
    for (let page = 1; page < pageCount; page++) {
      const cursor = detail.value?.next_cursor
      if (!cursor) break
      if (!await load({ ...filter, cursor }, true)) return false
    }
    return true
  }

  async function recordOutcome(workItemId: string, outcome: HumanOutcome): Promise<{ evidence: AgentOutcomeEvidence } | { error: string }> {
    try {
      const response = await cliFetch(cliApi('/usage/agent-report/outcome'), {
        method: 'POST',
        headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify({ work_item_id: workItemId, outcome }),
      })
      if (response.ok) {
        const result = await response.json() as { evidence?: AgentOutcomeEvidence }
        if (result.evidence) return { evidence: result.evidence }
        return { error: '结果已提交，但服务端未返回证据；刷新详情确认状态' }
      }
      if (response.status === 404) return { error: '任务记录已不可用，请刷新列表' }
      if (response.status === 409) return { error: '只有已完成任务可以标记结果' }
      return { error: `结果写入失败 (${response.status})` }
    } catch {
      return { error: '结果写入失败，请重试' }
    }
  }

  return { detail, loading, error, load, reloadLoadedPages, recordOutcome }
}
