import { ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import {
  normalizeAgentActivityReport,
  type AgentActivityReport,
  type AgentReportWindow,
} from './agentReportContract'

export * from './agentReportContract'

export function useAgentReport() {
  const { cliFetch } = useCliAuth()
  const report = ref<AgentActivityReport | null>(null)
  const loading = ref(false)
  const error = ref('')

  async function load(window: AgentReportWindow = '24h', force = false): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
      const params = new URLSearchParams({ window, timezone })
      if (force) params.set('refresh', '1')
      const res = await cliFetch(cliApi(`/usage/agent-report?${params}`), { headers: { Accept: 'application/json' } })
      if (!res.ok) { error.value = `Agent 效能加载失败 (${res.status})`; return }
      report.value = normalizeAgentActivityReport(await res.json())
    } catch {
      error.value = 'Agent 效能加载失败'
    } finally {
      loading.value = false
    }
  }

  return { report, loading, error, load }
}
