/**
 * Resident Bots API client — First-class resident bots, read-only precheck
 * triggers, SOUL.md/MEMORY.md docs, and CAS worktree item spawning.
 *
 *   GET   /bots
 *   GET   /bots/{id}
 *   PUT   /bots/{id}/docs
 *   POST  /bots/{id}/chat
 *   POST  /bots/{id}/run-check
 *   PATCH /bots/{id}/items/{itemId}
 *
 * Auth: authed cliFetch (X-CLI-Auth / X-Auth-Code) + cliApi prefix.
 */
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export type BotItemStatus = 'needs_you' | 'working' | 'done'

export interface BotItem {
  id: string
  num: number
  status: BotItemStatus
  origin: string
  title: string
  question: string
  targetFile?: string
  branchSlug?: string
  sessionId?: string
  repoCwd?: string
  live?: boolean
  spawnedWorktreePath?: string
  spawnedTabId?: string
  createdAt: string
}

export interface BotTrigger {
  id: string
  name: string
  precheck: string
  mode: string
  stats: string
  skippedZeroToken: number
}

export interface BotMessage {
  id: string
  role: 'user' | 'bot'
  meta?: string
  text: string
  files?: string[]
  createdAt: string
}

export interface BotThread {
  id: string
  title: string
  kind: 'home' | 'alert'
  messages: BotMessage[]
}

export interface BotDetail {
  id: string
  slug: string
  name: string
  emoji: string
  roleTitle: string
  profileId: string
  repoCwd: string
  needsYouCount: number
  workingCount: number
  soulMd: string
  memoryMd: string
  triggers: BotTrigger[]
  items: BotItem[]
  threads: BotThread[]
}

export interface UpdateBotDocsRequest {
  soulMd: string
  memoryMd: string
}

export interface SendBotChatRequest {
  threadId?: string
  message: string
}

export interface SendBotChatResponse {
  bot: BotDetail
}

export interface SimulateBotAlertPayload {
  origin: string
  title: string
  question: string
  targetFile?: string
  branchSlug?: string
}

export interface RunBotCheckRequest {
  triggerId?: string
  precheckCmd?: string
  patrol?: boolean
  simulateAlert?: SimulateBotAlertPayload
}

export interface RunBotCheckResponse {
  skippedZeroToken?: boolean
  bot: BotDetail
}

export interface PatchBotItemRequest {
  status?: BotItemStatus
  spawnedWorktreePath?: string
  spawnedTabId?: string
}

export interface PatchBotItemResponse {
  item: BotItem
  alreadySpawned: boolean
  bot: BotDetail
}

export class BotApiError extends Error {
  status: number
  blocked?: boolean

  constructor(message: string, status: number, blocked?: boolean) {
    super(message)
    this.name = 'BotApiError'
    this.status = status
    this.blocked = blocked
  }
}

async function readBotError(resp: Response, fallback: string): Promise<BotApiError> {
  try {
    const body = (await resp.json()) as { error?: string; blocked?: boolean }
    return new BotApiError(body.error || fallback, resp.status, body.blocked)
  } catch {
    return new BotApiError(fallback, resp.status)
  }
}

export async function fetchBots(): Promise<BotDetail[]> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/bots'))
  if (resp.status === 401 || resp.status === 429) {
    return []
  }
  if (!resp.ok) {
    throw await readBotError(resp, `加载常驻幕僚列表失败: HTTP ${resp.status}`)
  }
  const data = (await resp.json()) as { bots?: BotDetail[] }
  return Array.isArray(data.bots) ? data.bots : []
}

export async function fetchBot(id: string): Promise<BotDetail> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi(`/bots/${encodeURIComponent(id)}`))
  if (!resp.ok) {
    throw await readBotError(resp, `加载幕僚详情失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as BotDetail
}

export async function updateBotDocs(
  id: string,
  req: UpdateBotDocsRequest,
): Promise<BotDetail> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi(`/bots/${encodeURIComponent(id)}/docs`), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readBotError(resp, `保存幕僚记忆文档失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as BotDetail
}

export async function sendBotChat(
  id: string,
  req: SendBotChatRequest,
): Promise<SendBotChatResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi(`/bots/${encodeURIComponent(id)}/chat`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readBotError(resp, `发送幕僚消息失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as SendBotChatResponse
}

export async function runBotCheck(
  id: string,
  req: RunBotCheckRequest,
): Promise<RunBotCheckResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi(`/bots/${encodeURIComponent(id)}/run-check`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readBotError(resp, `执行只读巡检失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as RunBotCheckResponse
}

export async function patchBotItem(
  id: string,
  itemId: string,
  req: PatchBotItemRequest,
): Promise<PatchBotItemResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(
    cliApi(`/bots/${encodeURIComponent(id)}/items/${encodeURIComponent(itemId)}`),
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    },
  )
  if (!resp.ok) {
    throw await readBotError(resp, `更新分诊事项失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as PatchBotItemResponse
}
