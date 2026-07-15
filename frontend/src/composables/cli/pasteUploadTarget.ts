/** Pure routing core for clipboard/file uploads. The terminal WS and this REST target
 * must be resolved from the same tab connection; keeping the decision here makes the
 * remote-vs-local boundary deterministic and unit-testable. [remote-terminal RT-10] */
import { cliApi, peerApi } from '@terminal/composables/cli/useCliApiPrefix'

export interface PasteUploadTargetInput {
  sessionId: string
  isRemote: boolean
  httpBase?: string
  localAuth?: string
  remoteAuth?: string
}

export interface PasteUploadTarget {
  url: string
  isRemote: boolean
  authHeaders: Record<string, string>
}

export function resolvePasteUploadTarget(input: PasteUploadTargetInput): PasteUploadTarget {
  const path = `/sessions/${input.sessionId}/paste-upload`
  if (!input.isRemote) {
    const authHeaders: Record<string, string> = {}
    if (input.localAuth) {
      authHeaders['X-CLI-Auth'] = input.localAuth
      authHeaders['X-Auth-Code'] = input.localAuth
    }
    return { url: cliApi(path), isRemote: false, authHeaders }
  }

  const base = (input.httpBase || '').trim().replace(/\/+$/, '')
  if (!base) throw new Error('remote upload target is unavailable')
  const authHeaders: Record<string, string> = {}
  // Cross-origin peer calls deliberately use the explicit header only (RT-8): no
  // local compatibility header and no cookie credential semantics.
  if (input.remoteAuth) authHeaders['X-CLI-Auth'] = input.remoteAuth
  return { url: base + peerApi(path), isRemote: true, authHeaders }
}

/** Remote peers intentionally get the RT-8 minimum header set so an observability header
 * added by a newer host cannot make an older peer reject the CORS preflight. */
export function pasteUploadHeaders(
  target: PasteUploadTarget,
  localTraceHeaders: Record<string, string>,
): Record<string, string> {
  return target.isRemote
    ? { ...target.authHeaders }
    : { ...localTraceHeaders, ...target.authHeaders }
}
