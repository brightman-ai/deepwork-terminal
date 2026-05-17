/**
 * frontendLogger.ts — compatibility wrapper over obs.ts.
 *
 * 新代码应直接使用 `@/utils/obs`。本文件仅保留旧调用点兼容层与 HUD eventQueue。
 *
 * 使用方式：
 *   import { flog } from '@/utils/frontendLogger'
 *   flog('CommandDrawer', 'mounted', { isOpen: true, height: 220 })
 *
 * 日志开关：
 *   - 默认开启（DEBUG_LOG_ENABLED = true）
 *   - 可通过 localStorage.setItem('debugLog', '0') 关闭
 *
 * eventQueue 继续保留给旧 HUD 使用。
 */

import { createLogger } from '@/utils/obs'

const DEBUG_LOG_ENABLED =
  typeof localStorage !== 'undefined'
    ? localStorage.getItem('debugLog') !== '0'
    : true

// ── 需求 4A: 全局事件队列（最多 10 条，供 HUD 读取）─────────────────────────
const MAX_QUEUE_SIZE = 10
export const eventQueue: string[] = []

function formatTime(): string {
  const d = new Date()
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  const ss = String(d.getSeconds()).padStart(2, '0')
  const ms = String(d.getMilliseconds()).padStart(3, '0')
  return `${hh}:${mm}:${ss}.${ms}`
}

function pushEvent(component: string, msg: string): void {
  const entry = `[${formatTime()}] [${component}] ${msg}`
  eventQueue.push(entry)
  if (eventQueue.length > MAX_QUEUE_SIZE) {
    eventQueue.shift()
  }
}

function writeCompat(
  level: 'info' | 'warn' | 'error',
  component: string,
  msg: string,
  data: Record<string, unknown>,
): void {
  pushEvent(component, msg)
  const logger = createLogger(component)
  switch (level) {
    case 'error':
      logger.error(msg, data)
      break
    case 'warn':
      logger.warn(msg, data)
      break
    default:
      logger.info(msg, data)
  }
}

/**
 * flog — 前端结构化日志，同时写入 console 和服务端日志接收端点。
 * fire-and-forget：不阻塞调用方，网络错误静默忽略。
 */
export function flog(
  component: string,
  msg: string,
  data?: Record<string, unknown>,
): void {
  if (!DEBUG_LOG_ENABLED) return
  writeCompat('info', component, msg, data ?? {})
}

/**
 * fwarn — 前端 warn 级别日志。
 */
export function fwarn(
  component: string,
  msg: string,
  data?: Record<string, unknown>,
): void {
  if (!DEBUG_LOG_ENABLED) return
  writeCompat('warn', component, msg, data ?? {})
}

/**
 * ferror — 前端 error 级别日志。
 */
export function ferror(
  component: string,
  msg: string,
  data?: Record<string, unknown>,
): void {
  if (!DEBUG_LOG_ENABLED) return
  writeCompat('error', component, msg, data ?? {})
}
