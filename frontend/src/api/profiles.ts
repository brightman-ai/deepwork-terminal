/**
 * Isolated CLI Profiles API client.
 *
 *   GET    /profiles
 *   POST   /profiles
 *   PUT    /profiles/{id}
 *   DELETE /profiles/{id}
 *   POST   /profiles/scan
 *   POST   /profiles/test
 *
 * Auth: authed cliFetch (X-CLI-Auth / X-Auth-Code) + cliApi prefix.
 * Note: server ALWAYS masks API keys (`apiKeyMasked`), never exposing raw secrets.
 */
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export type ProfileEngine = 'claude' | 'codex' | 'shell'
export type ProfileKind = 'oauth' | 'gateway' | 'imported'

export interface ProfileItem {
  id: string
  name: string
  engine: ProfileEngine
  kind: ProfileKind
  model: string
  baseUrl?: string
  apiKeyMasked?: string
  hasApiKey: boolean
  configDir: string
  isDefault: boolean
  badge: string
}

export interface ProfileListResponse {
  profiles: ProfileItem[]
  defaultProfileId: string
}

export interface CreateProfileRequest {
  name: string
  engine: ProfileEngine
  kind: ProfileKind
  model?: string
  baseUrl?: string
  apiKey?: string
  isDefault?: boolean
}

export type UpdateProfileRequest = Partial<CreateProfileRequest> & {
  clearApiKey?: boolean
}

export interface ScanProfilesResponse {
  imported: number
  profiles: ProfileItem[]
}

export interface TestProfileRequest {
  baseUrl: string
  apiKey?: string
}

export interface TestProfileResponse {
  ok: boolean
  latencyMs: number
  message: string
}

export class ProfileApiError extends Error {
  status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'ProfileApiError'
    this.status = status
  }
}

async function readProfileError(resp: Response, fallback: string): Promise<ProfileApiError> {
  try {
    const body = (await resp.json()) as { error?: string }
    return new ProfileApiError(body.error || fallback, resp.status)
  } catch {
    return new ProfileApiError(fallback, resp.status)
  }
}

export async function fetchProfiles(): Promise<ProfileListResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/profiles'))
  if (resp.status === 401 || resp.status === 429) {
    return { profiles: [], defaultProfileId: '' }
  }
  if (!resp.ok) {
    throw await readProfileError(resp, `加载配置列表失败: HTTP ${resp.status}`)
  }
  const data = (await resp.json()) as Partial<ProfileListResponse>
  return {
    profiles: Array.isArray(data.profiles) ? data.profiles : [],
    defaultProfileId: data.defaultProfileId ?? '',
  }
}

export async function createProfile(req: CreateProfileRequest): Promise<ProfileItem> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/profiles'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readProfileError(resp, `创建配置失败: HTTP ${resp.status}`)
  }
  const data = (await resp.json()) as { profile: ProfileItem }
  return data.profile
}

export async function updateProfile(
  id: string,
  req: UpdateProfileRequest,
): Promise<ProfileItem> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi(`/profiles/${encodeURIComponent(id)}`), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readProfileError(resp, `更新配置失败: HTTP ${resp.status}`)
  }
  const data = (await resp.json()) as { profile: ProfileItem }
  return data.profile
}

export async function deleteProfile(id: string): Promise<void> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi(`/profiles/${encodeURIComponent(id)}`), {
    method: 'DELETE',
  })
  if (!resp.ok) {
    throw await readProfileError(resp, `删除配置失败: HTTP ${resp.status}`)
  }
}

export async function scanLocalProfiles(): Promise<ScanProfilesResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/profiles/scan'), {
    method: 'POST',
  })
  if (!resp.ok) {
    throw await readProfileError(resp, `扫描本机配置失败: HTTP ${resp.status}`)
  }
  const data = (await resp.json()) as Partial<ScanProfilesResponse>
  return {
    imported: typeof data.imported === 'number' ? data.imported : 0,
    profiles: Array.isArray(data.profiles) ? data.profiles : [],
  }
}

export async function testProfileGateway(
  req: TestProfileRequest,
): Promise<TestProfileResponse> {
  const { cliFetch } = useCliAuth()
  const resp = await cliFetch(cliApi('/profiles/test'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!resp.ok) {
    throw await readProfileError(resp, `网关连通性自检失败: HTTP ${resp.status}`)
  }
  return (await resp.json()) as TestProfileResponse
}
