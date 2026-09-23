/**
 * Resource-drawer API client — CROSS-SESSION uploads + transcript inputs.
 *
 * Uploads (images + files) are no longer session-scoped: GET /uploads returns
 * every clipboard image / uploaded file across ALL sessions, past and present,
 * each annotated with its originating session name + cwd. The raw bytes are
 * served by GET /uploads/raw?id=<id> — an opaque, index-whitelisted id (no path
 * ever leaves the server, so traversal is structurally impossible).
 *
 * Inputs (human prompts) come from GET /inputs, which parses the claude + codex
 * transcripts and surfaces only the prompts the user actually typed.
 *
 * Auth: list requests use cliFetch (X-CLI-Auth header). Raw URLs consumed by
 * <img>/<a> elements cannot carry headers, so rawUrl() appends ?auth=<code>,
 * which the backend authWrap also accepts.
 */
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { apiUrl } from '@ce/utils/runtimeBase'

export interface UploadItem {
  id: string
  kind: 'image' | 'file'
  name: string
  size: number
  mtimeMs: number
  sessionId: string
  sessionName: string
  cwd: string
  /** Request path (no /api prefix, no auth) as returned by the server. */
  url: string
  /** Absolute path on disk — the @-referenceable path for "插入对话" injection. */
  path: string
}

export interface UploadsResponse {
  items: UploadItem[]
  total: number
  counts: { images: number; files: number }
  sessions: string[]
  nextCursor?: string
}

export interface InputItem {
  text: string
  tsMs: number
  source: 'claude' | 'codex'
  cwd: string
  project: string
  sessionName?: string
}

export interface InputsResponse {
  items: InputItem[]
}

/** Filter the entire inventory on the server before returning one bounded page. */
export async function fetchUploadsPage(query: {
  kind: 'image' | 'file'; q: string; session: string; order: 'newest' | 'oldest'; cursor?: string
}, signal?: AbortSignal): Promise<UploadsResponse> {
  const { cliFetch } = useCliAuth()
  const params = new URLSearchParams({ ...query, limit: '24' })
  if (!query.cursor) params.delete('cursor')
  const resp = await cliFetch(cliApi(`/uploads?${params}`), { signal })
  if (!resp.ok) throw new Error(`加载失败 (${resp.status})`)
  return await resp.json() as UploadsResponse
}

/** GET /inputs — human prompts parsed from claude/codex transcripts, newest first. */
export async function fetchInputs(signal?: AbortSignal): Promise<InputItem[]> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/inputs'), { signal })
  if (!resp.ok) throw new Error(`加载失败 (${resp.status})`)
  const data = await resp.json() as Partial<InputsResponse>
  return data.items ?? []
}

/**
 * Build a fully-qualified, auth-bearing raw URL for an upload id, suitable for
 * <img src> / <a href> / fetch. The id is the opaque whitelist key; no path is
 * ever sent to the server.
 */
export function rawUrl(id: string): string {
  const { getAuthCode } = useCliAuth()
  const base = apiUrl(cliApi('/uploads/raw'))
  const sep = base.includes('?') ? '&' : '?'
  const auth = getAuthCode()
  const authQ = auth ? `&auth=${encodeURIComponent(auth)}` : ''
  return `${base}${sep}id=${encodeURIComponent(id)}${authQ}`
}

/**
 * Fetch a text-like upload's raw bytes as a string, for INLINE preview (no new
 * browser tab). Uses the same authed cliFetch path as the list endpoints. Returns
 * null on any failure so the caller can fall back to a download action.
 */
export async function fetchRawText(id: string): Promise<string | null> {
  const { cliFetch } = useCliAuth()
  try {
    const resp = await cliFetch(cliApi(`/uploads/raw?id=${encodeURIComponent(id)}`))
    if (!resp.ok) return null
    return await resp.text()
  } catch {
    return null
  }
}
