/**
 * Worktrees API client — Git CoW worktree lifecycle endpoints.
 *
 *   GET  /git/worktrees?session=<id>&cwd=<dir>
 *   GET  /git/worktrees/all?cwd=<dir>
 *   POST /git/worktrees
 *   POST /git/worktrees/merge
 *   POST /git/worktrees/remove
 *
 * Auth: authed cliFetch (X-CLI-Auth / X-Auth-Code) + cliApi prefix.
 */
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export interface WorktreeInfo {
  path: string
  branch: string
  head: string
  isPrimary: boolean
  dirty: boolean
  merged: boolean
  cacheMB: number
}

export interface RepoWorktreeGroup {
  root: string
  name: string
  worktrees: WorktreeInfo[]
  sessionCount: number
}

export interface AllWorktreesResponse {
  repos: RepoWorktreeGroup[]
}

export interface WorktreeListResponse {
  root: string
  notGit?: boolean
  noCwd?: boolean
  worktrees: WorktreeInfo[]
}

export interface CreateWorktreeRequest {
  session?: string
  cwd?: string
  branch: string
  baseRef?: string
}

export interface CreateWorktreeResponse {
  path: string
  branch: string
  baseRef: string
  isPrimary: boolean
}

export interface MergeWorktreeRequest {
  cwd: string
  targetBranch?: string
}

export interface MergeWorktreeResponse {
  ok: true
  mergedBranch: string
  targetBranch: string
}

export interface RemoveWorktreeRequest {
  path: string
  force?: boolean
}

export interface RemoveWorktreeResponse {
  ok: true
}

export type WorktreeGuardCode =
  | 'primary_protected'
  | 'dirty_worktree'
  | 'active_session'
  | string

export class WorktreeApiError extends Error {
  status: number
  code?: WorktreeGuardCode

  constructor(message: string, status: number, code?: WorktreeGuardCode) {
    super(message)
    this.name = 'WorktreeApiError'
    this.status = status
    this.code = code
  }
}

async function readErrorPayload(resp: Response, fallback: string): Promise<WorktreeApiError> {
  try {
    const body = (await resp.json()) as { error?: string; code?: WorktreeGuardCode }
    return new WorktreeApiError(body.error || fallback, resp.status, body.code)
  } catch {
    return new WorktreeApiError(fallback, resp.status)
  }
}

export async function fetchWorktrees(opts?: {
  session?: string
  cwd?: string
}): Promise<WorktreeListResponse> {
  const { cliFetch } = useCliAuth()
  const params = new URLSearchParams()
  if (opts?.session) params.set('session', opts.session)
  if (opts?.cwd) params.set('cwd', opts.cwd)
  const qs = params.toString()
  const url = qs ? `/git/worktrees?${qs}` : '/git/worktrees'

  const resp = await cliFetch(cliApi(url))
  if (resp.status === 401 || resp.status === 429) {
    return { root: '', worktrees: [], noCwd: true }
  }
  if (!resp.ok) {
    throw await readErrorPayload(resp, `加载工作树列表失败: HTTP ${resp.status}`)
  }
  const data = (await resp.json()) as Partial<WorktreeListResponse>
  return {
    root: data.root ?? '',
    notGit: data.notGit,
    noCwd: data.noCwd,
    worktrees: Array.isArray(data.worktrees) ? data.worktrees : [],
  }
}

export async function fetchAllWorktrees(opts?: {
  cwd?: string
}): Promise<AllWorktreesResponse> {
  const { cliFetch } = useCliAuth()
  const params = new URLSearchParams()
  if (opts?.cwd) params.set('cwd', opts.cwd)
  const qs = params.toString()
  const url = qs ? `/git/worktrees/all?${qs}` : '/git/worktrees/all'

  const resp = await cliFetch(cliApi(url))
  if (resp.status === 401 || resp.status === 404 || resp.status === 429) {
    return { repos: [] }
  }
  if (!resp.ok) {
    throw await readErrorPayload(resp, `加载全仓库工作树列表失败: HTTP ${resp.status}`)
  }
  const data = (await resp.json()) as Partial<AllWorktreesResponse>
  return {
    repos: Array.isArray(data.repos) ? data.repos : [],
  }
}

export async function createWorktree(
  req: CreateWorktreeRequest,
): Promise<CreateWorktreeResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/git/worktrees'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readErrorPayload(resp, `创建工作树失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as CreateWorktreeResponse
}

export async function mergeWorktree(
  req: MergeWorktreeRequest,
): Promise<MergeWorktreeResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/git/worktrees/merge'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readErrorPayload(resp, `合入工作树失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as MergeWorktreeResponse
}

export async function removeWorktree(
  req: RemoveWorktreeRequest,
): Promise<RemoveWorktreeResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/git/worktrees/remove'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readErrorPayload(resp, `移除工作树失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as RemoveWorktreeResponse
}
