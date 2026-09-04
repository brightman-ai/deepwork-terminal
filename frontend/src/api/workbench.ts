/**
 * Workbench API client — GET/PUT /api/cli/workbench
 * 使用 cliFetch 自动携带 X-CLI-Auth 头，并处理 401 未认证场景。
 *
 * # 这一层为什么要把"没问到"和"服务端确实是空的"分开
 *
 * 它们在网络层长得几乎一样（都是"我没拿到标签列表"），但对**能不能存盘**的含义完全相反：
 *
 *   404 = 服务端确实还没有这份文档（首次使用）→ 默认配置就是事实，必须允许存盘，否则永远存不上。
 *   401 / 429 / 5xx / 断网 = 没问到 → 默认配置是**编造**的，拿它去 PUT 就是用一份空列表覆盖掉
 *                            用户真实的标签（连同还活着的 session 的绑定）。
 *
 * 这个函数原来把两者一起塞进 `createDefaultWorkbenchConfig()` 返回，调用方无从分辨——上游
 * useWorkbench 于是对着一份编造的空配置照常存盘。所以这里返回 `hydrated`，而不是只返回 config。
 */
import type { WorkbenchConfig } from '@terminal/types/workbench'
import { createDefaultWorkbenchConfig } from '@terminal/types/workbench'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

function getCliFetch() {
  const { cliFetch } = useCliAuth()
  return cliFetch
}

export interface WorkbenchLoad {
  config: WorkbenchConfig
  /** `true` = 这份 config 代表服务端此刻的真实状态（包括"服务端确实还没有"）；可以作为存盘基准。
   *  `false` = 没问到，config 只是能让界面渲染的兜底值，**不得**用它覆盖服务端。 */
  hydrated: boolean
}

/**
 * 从后端加载 workbench 配置。
 * 若后端返回 404 (首次使用尚无配置), 返回默认配置（且 hydrated=true，见文件头）。
 */
export async function fetchWorkbenchConfig(): Promise<WorkbenchLoad> {
  const cliFetch = getCliFetch()
  const resp = await cliFetch(cliApi('/workbench'))
  if (resp.status === 404) {
    // 服务端确实没有这份文档 —— 这是一条真实的答案，不是一次失败。
    return { config: createDefaultWorkbenchConfig(), hydrated: true }
  }
  if (resp.status === 401 || resp.status === 429) {
    // Auth dialog will handle (401 = wrong code, 429 = throttled) — return defaults silently,
    // 但明确标成"没问到"，否则下一次改动就会把这份空配置 PUT 上去。
    return { config: createDefaultWorkbenchConfig(), hydrated: false }
  }
  if (!resp.ok) {
    throw new Error(`加载 workbench 配置失败: HTTP ${resp.status}`)
  }
  return { config: await resp.json() as WorkbenchConfig, hydrated: true }
}

/** 一次存盘的结果。`conflict` 不是错误，是"你的基准过期了，这是当前的，自己合并后再来"。 */
export type WorkbenchSaveOutcome =
  | { kind: 'saved'; rev?: number }
  | { kind: 'conflict'; current: WorkbenchConfig }
  | { kind: 'skipped' }
  | { kind: 'failed'; message: string }

/**
 * 保存 workbench 配置到后端。
 *
 * 带着 `config.rev`（服务端所有的版本号）过去；服务端发现基准已被别的设备推进就回 409 并附上当前
 * 文档，此时**不算失败**——调用方要三方合并后重试（见 workbenchMerge.ts）。
 */
export async function saveWorkbenchConfig(config: WorkbenchConfig): Promise<WorkbenchSaveOutcome> {
  const cliFetch = getCliFetch()
  const resp = await cliFetch(cliApi('/workbench'), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  })
  if (resp.status === 409) {
    try {
      return { kind: 'conflict', current: await resp.json() as WorkbenchConfig }
    } catch {
      return { kind: 'failed', message: '保存冲突且无法读取服务端当前配置' }
    }
  }
  if (resp.status === 401 || resp.status === 429) {
    // Auth dialog will handle (401 = wrong code, 429 = throttled) — silently skip.
    return { kind: 'skipped' }
  }
  if (!resp.ok) {
    return { kind: 'failed', message: `保存 workbench 配置失败: HTTP ${resp.status}` }
  }
  // 204（旧服务端）没有 body；200 带 {"rev":N}。两种都算成功。
  try {
    const data = await resp.json() as { rev?: number }
    return { kind: 'saved', rev: typeof data?.rev === 'number' ? data.rev : undefined }
  } catch {
    return { kind: 'saved' }
  }
}
