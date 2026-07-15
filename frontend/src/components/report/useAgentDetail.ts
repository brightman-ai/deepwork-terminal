import { ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { normalizeAgentDetailReport, type AgentDetailFilter, type AgentDetailReport } from './agentDetailContract'

export * from './agentDetailContract'

export function useAgentDetail() {
  const { cliFetch } = useCliAuth()
  const detail = ref<AgentDetailReport | null>(null)
  const loading = ref(false)
  const error = ref('')
  let generation = 0

  async function load(filter: AgentDetailFilter, append = false): Promise<void> {
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
        return
      }
      const next = normalizeAgentDetailReport(await response.json())
      if (requestGeneration !== generation) return
      if (append && detail.value) next.tasks = [...detail.value.tasks, ...next.tasks]
      detail.value = next
    } catch {
      if (requestGeneration === generation) error.value = '详细报表加载失败'
    } finally {
      if (requestGeneration === generation) loading.value = false
    }
  }

  return { detail, loading, error, load }
}
