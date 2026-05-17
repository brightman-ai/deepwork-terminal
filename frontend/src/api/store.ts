/**
 * Store API client — GET/PUT /api/store
 * 通用 KV 持久化，数据存在服务端文件系统，跨域名（trycloudflare 等）不丢失。
 */
import { useCliAuth } from '@/composables/cli/useCliAuth'

function getCliFetch() {
  const { cliFetch } = useCliAuth()
  return cliFetch
}

export async function fetchStore(): Promise<Record<string, unknown>> {
  const cliFetch = getCliFetch()
  const resp = await cliFetch('/api/store')
  if (!resp.ok) return {}
  return resp.json() as Promise<Record<string, unknown>>
}

export async function saveStore(data: Record<string, unknown>): Promise<void> {
  const cliFetch = getCliFetch()
  const resp = await cliFetch('/api/store', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  })
  if (resp.status === 401) return // auth dialog will handle
  if (!resp.ok) throw new Error(`保存 store 失败: HTTP ${resp.status}`)
}
