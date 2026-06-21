/**
 * useCliAuth — Manages terminal auth code storage and API request helpers.
 * Stores the auth code in localStorage and provides fetch wrappers that
 * automatically include the X-Auth-Code header used by the global WebUI auth
 * middleware. X-CLI-Auth is kept for backwards compatibility with older CLI
 * session endpoints.
 * [Ref: BUG-2 security fix]
 */
import { ref } from 'vue'
import { apiUrl } from '@ce/utils/runtimeBase'

const AUTH_STORAGE_KEY = 'cli_auth_code'

// Bootstrap: if ?auth= is in the URL, store it and clean the URL.
// This enables auth-included links (e.g. tunnel URLs shared with auth code).
const urlParams = new URLSearchParams(window.location.search)
const urlAuth = urlParams.get('auth')
if (urlAuth) {
  localStorage.setItem(AUTH_STORAGE_KEY, urlAuth)
  urlParams.delete('auth')
  const clean = urlParams.toString()
  const newURL = window.location.pathname + (clean ? '?' + clean : '') + window.location.hash
  window.history.replaceState({}, '', newURL)
}

// Shared reactive state across all component instances.
const authCode = ref(localStorage.getItem(AUTH_STORAGE_KEY) || '')
const showAuthDialog = ref(false)

// One-time bootstrap token from a push-notification deep link. It arrives in the
// URL FRAGMENT (#bootstrap=…) — a fragment is never sent to the server or in a
// Referer header, so the bearer token can't leak to tunnel/proxy logs. It is
// single-use + short-TTL server-side, so even a leaked URL string is worthless.
// Exchange it for the auth code once, then strip the fragment.
const bootstrapMatch = window.location.hash.match(/[#&]bootstrap=([^&]+)/)
if (bootstrapMatch && !urlAuth) {
  const bootstrapTok = decodeURIComponent(bootstrapMatch[1])
  window.history.replaceState({}, '', window.location.pathname + window.location.search)
  void (async () => {
    try {
      const resp = await fetch(apiUrl('/auth/bootstrap?token=' + encodeURIComponent(bootstrapTok)))
      if (resp.ok) {
        const { authCode: code } = await resp.json() as { authCode?: string }
        if (code) {
          localStorage.setItem(AUTH_STORAGE_KEY, code)
          authCode.value = code
        }
      }
    } catch { /* invalid/expired token → fall back to the manual auth dialog */ }
  })()
}

export function useCliAuth() {
  function getAuthCode(): string {
    return authCode.value
  }

  function setAuthCode(code: string) {
    authCode.value = code
    localStorage.setItem(AUTH_STORAGE_KEY, code)
  }

  function clearAuthCode() {
    authCode.value = ''
    localStorage.removeItem(AUTH_STORAGE_KEY)
  }

  /**
   * Wrapper around fetch that adds the X-CLI-Auth header.
   * If the response is 401, prompts for auth code via the dialog.
   */
  async function cliFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const headers = new Headers(init?.headers)
    if (authCode.value) {
      headers.set('X-CLI-Auth', authCode.value)
      headers.set('X-Auth-Code', authCode.value)
    }
    const target = typeof input === 'string' ? apiUrl(input) : input
    const resp = await fetch(target, { ...init, headers })
    if (resp.status === 401) {
      showAuthDialog.value = true
    }
    return resp
  }

  function promptAuth() {
    showAuthDialog.value = true
  }

  function dismissAuthDialog() {
    showAuthDialog.value = false
  }

  return {
    authCode,
    showAuthDialog,
    getAuthCode,
    setAuthCode,
    clearAuthCode,
    cliFetch,
    promptAuth,
    dismissAuthDialog,
  }
}
