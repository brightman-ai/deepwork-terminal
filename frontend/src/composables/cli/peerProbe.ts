/**
 * Peer reachability/auth probe + URL helper — the pure, dependency-free core of the mesh
 * remote-terminal connection, split out of useRemotePeers so it carries NO Vue/window/store
 * imports and is unit-testable in isolation. [remote-terminal RT-2, RT-4]
 */
import { peerApi } from '@terminal/composables/cli/useCliApiPrefix'

/** Strip a trailing slash so base + path never doubles up. */
export function normalizeUrl(u: string): string {
  return u.trim().replace(/\/+$/, '')
}

/**
 * Auth-probe a peer before wiring a tab to it. Distinguishes a wrong code (401, deterministic)
 * from an unreachable address / TRANSIENT network blip (fetch throws / times out). DERP-relayed
 * tailscale links blip briefly, so a SINGLE probe failure would falsely block a peer that is
 * actually reachable (the user hits "连不上", then a plain retry connects) — so the TRANSIENT
 * class (thrown fetch / timeout) is retried a couple of times before we declare it unreachable.
 * A 401 NEVER retries (retrying would just mask a real auth error). Hits the SAME REST the
 * session-lifecycle uses, so a green probe means create-session will work. opts inject
 * fast/deterministic retries in tests.
 */
export async function probePeer(
  httpBase: string,
  code: string,
  opts: { attempts?: number; delayMs?: number } = {},
): Promise<{ ok: boolean; error?: string }> {
  const attempts = opts.attempts ?? 3
  const delayMs = opts.delayMs ?? 600
  const url = normalizeUrl(httpBase) + peerApi('/sessions')
  let lastErr = '连不上：地址不可达，或 HTTPS 页面连 HTTP 地址被浏览器拦截'
  for (let i = 1; i <= attempts; i++) {
    // 6s hard timeout per attempt: an unreachable host would otherwise leave the browser fetch
    // pending for the OS TCP timeout (~90s) with zero UI feedback — the silent hang RT-4 forbids.
    const ctrl = new AbortController()
    const timer = setTimeout(() => ctrl.abort(), 6000)
    try {
      const resp = await fetch(url, { headers: { 'X-CLI-Auth': code }, signal: ctrl.signal })
      if (resp.status === 401) return { ok: false, error: '认证码错误' } // deterministic → no retry
      if (!resp.ok) return { ok: false, error: `远程返回 HTTP ${resp.status}` } // deterministic → no retry
      return { ok: true }
    } catch (e) {
      // fetch threw = transient (blip / can't-route / timeout) → remember the reason + retry
      lastErr = (e as Error)?.name === 'AbortError'
        ? '连接超时：地址不可达（检查地址 / 网络 / 对端是否在线）'
        : '连不上：地址不可达，或 HTTPS 页面连 HTTP 地址被浏览器拦截'
    } finally {
      clearTimeout(timer)
    }
    if (i < attempts) await new Promise((r) => setTimeout(r, delayMs))
  }
  return { ok: false, error: lastErr }
}
