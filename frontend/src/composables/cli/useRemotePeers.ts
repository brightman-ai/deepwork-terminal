/**
 * useRemotePeers — registry + connection resolution for REMOTE terminal tabs (mesh
 * direct-connect). A "peer" is another deepwork-terminal instance the BROWSER reaches
 * directly (the local server never proxies). The registry of addresses lives server-side
 * (the /store KV, so it is shared across the user's devices); each peer's auth code stays
 * in this browser's localStorage only (typed once, never sent to the server).
 *
 * resolveTabConnection() is the SINGLE source for "where does this tab connect" — the WS
 * client, the session-lifecycle REST calls, and the machine chip ALL derive from it, so the
 * remote-vs-local routing decision is never re-derived (and never drifts) across the app.
 * [Ref: remote-terminal spec RT-1..RT-9; design-review F1/F2]
 */
import { ref } from 'vue'
import type { WorkbenchTab } from '@terminal/types/workbench'
import { useServerStore } from '@terminal/composables/cli/useServerStore'
import { normalizeUrl, probePeer } from '@terminal/composables/cli/peerProbe'

const PEERS_KEY = 'remotePeers'
const LOCAL_AUTH_KEY = 'cli_auth_code'
const peerAuthKey = (id: string) => `cli_remote_auth_${id}`

export interface RemotePeer {
  id: string
  name: string
  tailscaleUrl?: string // http://host:port  — LAN / tailscale, plain http
  cloudflareUrl?: string // https://…         — public tunnel, https
}

export interface TabConnection {
  isRemote: boolean
  /** '' = same-origin; else absolute origin e.g. 'http://stwork:8087'. */
  httpBase: string
  /** '' = same-origin default; else 'ws://…' / 'wss://…' matching httpBase. */
  wsBase: string
  /** Local code, or the peer's code; '' when remote and no code stored yet. */
  authToken: string
  /** Chip label: '本机' for local, '<name> · <host>' for remote. */
  machineLabel: string
  peer?: RemotePeer
  /** Set when a remote tab cannot be reached from the CURRENT page (scheme/missing addr). */
  error?: string
  /** True when remote but no auth code stored yet (needs one-time entry). */
  needsAuth?: boolean
}

// Module-level singleton — one registry shared by every component instance.
const peers = ref<RemotePeer[]>([])
let hydrated = false

function genPeerId(): string {
  return `p-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`
}

/** http→ws, https→wss (keeps host/port). */
function httpToWs(httpBase: string): string {
  return httpBase.replace(/^http/i, (m) => (m.toLowerCase() === 'https' ? 'wss' : 'ws'))
}

function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

export function useRemotePeers() {
  const store = useServerStore()

  async function loadPeers(): Promise<void> {
    await store.load()
    const raw = store.get<RemotePeer[]>(PEERS_KEY, [])
    peers.value = Array.isArray(raw) ? raw : []
    hydrated = true
  }

  function persist(): void {
    store.set(PEERS_KEY, peers.value)
  }

  function addPeer(input: { name: string; tailscaleUrl?: string; cloudflareUrl?: string }): RemotePeer {
    const peer: RemotePeer = {
      id: genPeerId(),
      name: input.name.trim() || hostOf(input.tailscaleUrl || input.cloudflareUrl || '远程'),
      tailscaleUrl: input.tailscaleUrl ? normalizeUrl(input.tailscaleUrl) : undefined,
      cloudflareUrl: input.cloudflareUrl ? normalizeUrl(input.cloudflareUrl) : undefined,
    }
    peers.value = [...peers.value, peer]
    persist()
    return peer
  }

  function updatePeer(id: string, patch: Partial<Omit<RemotePeer, 'id'>>): void {
    peers.value = peers.value.map((p) =>
      p.id === id
        ? {
            ...p,
            ...patch,
            tailscaleUrl: patch.tailscaleUrl !== undefined ? (patch.tailscaleUrl ? normalizeUrl(patch.tailscaleUrl) : undefined) : p.tailscaleUrl,
            cloudflareUrl: patch.cloudflareUrl !== undefined ? (patch.cloudflareUrl ? normalizeUrl(patch.cloudflareUrl) : undefined) : p.cloudflareUrl,
          }
        : p,
    )
    persist()
  }

  function removePeer(id: string): void {
    peers.value = peers.value.filter((p) => p.id !== id)
    persist()
    try { localStorage.removeItem(peerAuthKey(id)) } catch { /* ignore */ }
  }

  function getPeer(id: string): RemotePeer | undefined {
    return peers.value.find((p) => p.id === id)
  }

  // ─── auth code (browser-local, never server-stored) ───────────────────────────
  function getPeerAuth(id: string): string {
    try { return localStorage.getItem(peerAuthKey(id)) || '' } catch { return '' }
  }
  function setPeerAuth(id: string, code: string): void {
    try { localStorage.setItem(peerAuthKey(id), code) } catch { /* ignore */ }
  }

  /**
   * THE single source for a tab's connection target. Local tabs resolve to same-origin
   * (empty bases → existing helpers handle them); remote tabs pick the endpoint that the
   * CURRENT page can actually reach: an https page can only open wss:// (mixed-content rule),
   * so it MUST use the peer's cloudflare https address; an http page can reach either.
   */
  function resolveTabConnection(tab: Pick<WorkbenchTab, 'remotePeerId'>): TabConnection {
    if (!tab.remotePeerId) {
      let local = ''
      try { local = localStorage.getItem(LOCAL_AUTH_KEY) || '' } catch { /* ignore */ }
      return { isRemote: false, httpBase: '', wsBase: '', authToken: local, machineLabel: '本机' }
    }
    const peer = getPeer(tab.remotePeerId)
    if (!peer) {
      return { isRemote: true, httpBase: '', wsBase: '', authToken: '', machineLabel: '远程(已失效)', error: '该远程配置已被删除' }
    }
    const isHttps = typeof location !== 'undefined' && location.protocol === 'https:'
    let chosen: string | undefined
    if (isHttps) {
      // https page → only wss:// is allowed (browser blocks ws:// from a secure page), so a
      // plain-http tailscale address is unreachable here. Must use the cloudflare https one.
      chosen = peer.cloudflareUrl
      if (!chosen) {
        return mkErr(peer, '当前是 HTTPS 页面，该远程未配置 HTTPS(cloudflare) 地址，无法直连（混合内容限制）')
      }
    } else {
      // http page can open both ws:// and wss:// — prefer the LAN/tailscale address, fall back
      // to the public cloudflare one.
      chosen = peer.tailscaleUrl || peer.cloudflareUrl
      if (!chosen) {
        return mkErr(peer, '该远程未配置可用地址')
      }
    }
    const httpBase = normalizeUrl(chosen)
    const authToken = getPeerAuth(peer.id)
    return {
      isRemote: true,
      httpBase,
      wsBase: httpToWs(httpBase),
      authToken,
      machineLabel: `${peer.name} · ${hostOf(httpBase)}`,
      peer,
      needsAuth: !authToken,
    }
  }

  function mkErr(peer: RemotePeer, error: string): TabConnection {
    return { isRemote: true, httpBase: '', wsBase: '', authToken: '', machineLabel: `${peer.name}(不可达)`, peer, error }
  }

  return {
    peers,
    hydrated: () => hydrated,
    loadPeers,
    addPeer,
    updatePeer,
    removePeer,
    getPeer,
    getPeerAuth,
    setPeerAuth,
    resolveTabConnection,
    probePeer,
    httpToWs,
    hostOf,
  }
}
