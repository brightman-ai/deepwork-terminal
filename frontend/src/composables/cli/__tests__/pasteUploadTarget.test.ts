import { describe, expect, test } from 'bun:test'
import { pasteUploadHeaders, resolvePasteUploadTarget } from '../pasteUploadTarget'
import { setCliApiPrefix } from '../useCliApiPrefix'

describe('resolvePasteUploadTarget — upload follows the terminal tab', () => {
  test('embedded local tab keeps its local mount and compatibility auth headers', () => {
    setCliApiPrefix('/cli')
    const target = resolvePasteUploadTarget({
      sessionId: 'local-session',
      isRemote: false,
      localAuth: 'LOCAL-CODE',
    })
    expect(target.url).toBe('/api/cli/sessions/local-session/paste-upload')
    expect(target.authHeaders).toEqual({
      'X-CLI-Auth': 'LOCAL-CODE',
      'X-Auth-Code': 'LOCAL-CODE',
    })
  })

  test('remote tab posts to the peer standalone API, never the embedded local mount', () => {
    setCliApiPrefix('/cli')
    const target = resolvePasteUploadTarget({
      sessionId: 'remote-session',
      isRemote: true,
      httpBase: 'http://stmac:8087/',
      localAuth: 'LOCAL-CODE',
      remoteAuth: 'PEER-CODE',
    })
    expect(target.url).toBe('http://stmac:8087/api/sessions/remote-session/paste-upload')
    expect(target.url).not.toContain('/api/cli/')
    expect(target.authHeaders).toEqual({ 'X-CLI-Auth': 'PEER-CODE' })
    expect(pasteUploadHeaders(target, { 'X-Trace-ID': 'host-trace' })).toEqual({
      'X-CLI-Auth': 'PEER-CODE',
    })
  })

  test('remote tab cannot silently fall back to same-origin when its peer base is missing', () => {
    expect(() => resolvePasteUploadTarget({
      sessionId: 'remote-session',
      isRemote: true,
      httpBase: '',
      remoteAuth: 'PEER-CODE',
    })).toThrow('remote upload target is unavailable')
  })
})
