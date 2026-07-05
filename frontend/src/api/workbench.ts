/**
 * Workbench API client — GET/PUT /api/cli/workbench
 * 使用 cliFetch 自动携带 X-CLI-Auth 头，并处理 401 未认证场景。
 */
import type { WorkbenchConfig } from '@terminal/types/workbench'
import { createDefaultWorkbenchConfig } from '@terminal/types/workbench'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

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
  if (resp.status === 401 || resp.status === 429) {
    // Auth dialog will handle (401 = wrong code, 429 = throttled) — return defaults silently.
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
  if (resp.status === 401 || resp.status === 429) {
    // Auth dialog will handle (401 = wrong code, 429 = throttled) — silently skip.
    return
  }
  if (!resp.ok) {
    throw new Error(`保存 workbench 配置失败: HTTP ${resp.status}`)
  }
}
