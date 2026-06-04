/**
 * Workbench API client — GET/PUT /api/cli/workbench
 * 使用 cliFetch 自动携带 X-CLI-Auth 头，并处理 401 未认证场景。
 */
import type { WorkbenchConfig } from '@/types/workbench'
import { createDefaultWorkbenchConfig } from '@/types/workbench'
import { useCliAuth } from '@/composables/cli/useCliAuth'
import { cliApi } from '@/composables/cli/useCliApiPrefix'

function getCliFetch() {
  const { cliFetch } = useCliAuth()
  return cliFetch
}

/**
 * 从后端加载 workbench 配置。
 * 若后端返回 404 (首次使用尚无配置), 返回默认配置。
 */
export async function fetchWorkbenchConfig(): Promise<WorkbenchConfig> {
  const cliFetch = getCliFetch()
  const resp = await cliFetch(cliApi('/workbench'))
  if (resp.status === 404) {
    return createDefaultWorkbenchConfig()
  }
  if (resp.status === 401) {
    // Auth dialog will handle this — return defaults silently, reload will retry.
    return createDefaultWorkbenchConfig()
  }
  if (!resp.ok) {
    throw new Error(`加载 workbench 配置失败: HTTP ${resp.status}`)
  }
  return resp.json() as Promise<WorkbenchConfig>
}

/**
 * 保存 workbench 配置到后端。
 */
export async function saveWorkbenchConfig(config: WorkbenchConfig): Promise<void> {
  const cliFetch = getCliFetch()
  const resp = await cliFetch(cliApi('/workbench'), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  })
  if (resp.status === 401) {
    // Auth dialog will handle — silently skip, reload will retry.
    return
  }
  if (!resp.ok) {
    throw new Error(`保存 workbench 配置失败: HTTP ${resp.status}`)
  }
}
