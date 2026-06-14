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
}

export interface UploadsResponse {
  items: UploadItem[]
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

/** GET /uploads — the global, cross-session list of images + files. */
export async function fetchUploads(): Promise<UploadItem[]> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/uploads'))
  if (!resp.ok) return []
  const data = await resp.json() as Partial<UploadsResponse>
  return data.items ?? []
}

/** GET /inputs — human prompts parsed from claude/codex transcripts, newest first. */
export async function fetchInputs(): Promise<InputItem[]> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/inputs'))
  if (!resp.ok) return []
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
