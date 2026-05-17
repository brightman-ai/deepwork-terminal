function trimRightSlash(value: string): string {
  return value.replace(/\/+$/, '')
}

function trimLeftSlash(value: string): string {
  return value.replace(/^\/+/, '')
}

function joinBase(base: string, path: string): string {
  if (/^(https?:|wss?:|file:|data:|blob:)/i.test(path)) return path
  if (!base) return path
  return `${trimRightSlash(base)}/${trimLeftSlash(path)}`
}

export function apiUrl(path: string): string {
  const runtimeBase = window.__DW_API_BASE || import.meta.env.VITE_API_BASE_URL || ''
  return joinBase(runtimeBase, path)
}

export function wsUrl(path: string): string {
  const runtimeBase = window.__DW_WS_BASE || ''
  const fallbackBase = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}`
  return joinBase(runtimeBase || fallbackBase, path)
}
