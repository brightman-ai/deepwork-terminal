/**
 * usePushNotifications — single source of truth for the PWA + Web Push lifecycle.
 *
 * Owns: service-worker registration, feature detection, platform classification,
 * Notification.permission, PushManager subscribe/unsubscribe against the FIXED
 * backend contract, and a captured `beforeinstallprompt` event for Chromium.
 *
 * Backend contract (called via cliApi()):
 *   GET  /push/vapid       → { publicKey }   (base64url VAPID public key)
 *   POST /push/subscribe   { endpoint, keys:{p256dh,auth}, sessionId }
 *   POST /push/unsubscribe { endpoint }
 *
 * Robustness law: NEVER assume Push/SW exist. iOS Safari outside standalone has
 * NO window.PushManager and a non-functional Notification — every access is
 * feature-guarded so the UI degrades to install-guidance instead of throwing.
 *
 * Module-level singleton (not per-session): there is exactly one SW registration
 * and one push subscription per origin. sessionId is passed at subscribe() time so
 * the backend can target the active tab, but the subscription itself is global.
 */
import { ref, computed, type Ref, type ComputedRef } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'

export type PushPlatform = 'ios-safari' | 'ios-other' | 'chromium' | 'desktop-safari' | 'desktop-firefox' | 'other'
export type PushPermission = 'default' | 'granted' | 'denied' | 'unsupported'

// Minimal shape of the Chromium-only beforeinstallprompt event (not in lib.dom).
interface BeforeInstallPromptEvent extends Event {
  readonly platforms: string[]
  prompt(): Promise<void>
  readonly userChoice: Promise<{ outcome: 'accepted' | 'dismissed'; platform: string }>
}

export interface PushNotificationsApi {
  /** SW + PushManager + Notification all present (platform CAN do Web Push). */
  supported: ComputedRef<boolean>
  /**
   * Running in a SECURE CONTEXT (HTTPS or localhost). Web Push / Service Workers
   * are gated on this by every browser, so over plain HTTP on a LAN/Tailscale IP
   * `navigator.serviceWorker` is absent and registration silently fails. When this
   * is false the UI must surface the real "needs HTTPS" reason, not a generic
   * "unsupported" — it is the dominant cause of "notifications don't work".
   */
  secureContext: boolean
  /** Notification.permission, or 'unsupported' when the API is absent. */
  permission: Ref<PushPermission>
  /** A live PushSubscription exists and has been POSTed to the backend. */
  subscribed: Ref<boolean>
  /** Running as an installed PWA (display-mode: standalone / iOS navigator.standalone). */
  isStandalone: ComputedRef<boolean>
  /** Coarse platform bucket that drives the install guide's branch. */
  platform: ComputedRef<PushPlatform>
  /** A beforeinstallprompt was captured and is still usable (Chromium/Android). */
  canPromptInstall: Ref<boolean>
  /** The app reported itself installed this session (appinstalled fired). */
  installed: Ref<boolean>
  /** Ensure the SW is registered; resolves to the registration or null. */
  ensureRegistration(): Promise<ServiceWorkerRegistration | null>
  /** Fire the captured native install prompt; returns the user's outcome. */
  promptInstall(): Promise<'accepted' | 'dismissed' | 'unavailable'>
  /**
   * Last subscribe() failure reason — drives a specific, actionable hint instead
   * of a generic "失败". 'ios-reopen' means the SW wasn't controlling the page and
   * the documented iOS fix (fully close + relaunch from the icon) is required.
   */
  lastError: Ref<'' | 'denied' | 'ios-reopen' | 'generic'>
  /** Opt-in: request permission, subscribe, POST to backend. Returns success. */
  subscribe(sessionId: string): Promise<boolean>
  /** Opt-out: unsubscribe locally + tell the backend to forget the endpoint. */
  unsubscribe(): Promise<boolean>
  /** Re-read permission + existing subscription (e.g. on visibility regain). */
  refresh(): Promise<void>
  /**
   * Fire an end-to-end test notification so the user can verify the full chain.
   * Returns the REAL outcome — never a false "sent". See PushTestResult.
   */
  sendTest(): Promise<PushTestResult>
}

/**
 * Honest result of sendTest(). The backend's /push/test reports per-attempt
 * delivery, so we no longer claim "已发送" when Apple rejected the token.
 *   'delivered' — backend confirmed ≥1 device got the push (2xx). `sent` = count.
 *   'rejected'  — backend tried but every attempt was refused (e.g. Apple 403
 *                 BadJwtToken). `rejected[0].status` is the HTTP status to show.
 *   'local'     — no backend subscription, but a local Notification() fired.
 *   'failed'    — nothing could be sent (no permission / request failed).
 */
export interface PushTestResult {
  kind: 'delivered' | 'rejected' | 'local' | 'failed'
  /** Devices the backend confirmed delivered to (kind === 'delivered'). */
  sent?: number
  /** Per-attempt refusals from the push service (kind === 'rejected'). */
  rejected?: Array<{ status: number; reason?: string }>
}

// ─── base64url → Uint8Array (applicationServerKey expects a raw byte array) ──────
function urlBase64ToUint8Array(base64url: string): Uint8Array {
  const padding = '='.repeat((4 - (base64url.length % 4)) % 4)
  const base64 = (base64url + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

function arrayBufferToBase64Url(buf: ArrayBuffer | null): string {
  if (!buf) return ''
  const bytes = new Uint8Array(buf)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

// ─── platform classification (UA-based, intentionally coarse) ────────────────────
function detectPlatform(): PushPlatform {
  const ua = navigator.userAgent
  const isIOS = /iPad|iPhone|iPod/.test(ua) ||
    // iPadOS 13+ reports as Mac; disambiguate by touch points.
    (navigator.platform === 'MacIntel' && (navigator.maxTouchPoints ?? 0) > 1)
  if (isIOS) {
    // On iOS every browser is WebKit; only Safari (and standalone) can do Web Push.
    // CriOS/FxiOS/EdgiOS/OPiOS markers ⇒ a non-Safari shell that can't subscribe.
    const isOtherShell = /CriOS|FxiOS|EdgiOS|OPiOS|GSA|DuckDuckGo/.test(ua)
    return isOtherShell ? 'ios-other' : 'ios-safari'
  }
  const isChromium = /Chrome|Chromium|Edg|OPR/.test(ua) && !/OPiOS/.test(ua)
  if (isChromium) return 'chromium'
  if (/Firefox/.test(ua)) return 'desktop-firefox'
  if (/Safari/.test(ua)) return 'desktop-safari'
  return 'other'
}

function detectStandalone(): boolean {
  if (typeof window === 'undefined') return false
  const mql = window.matchMedia?.('(display-mode: standalone)')?.matches
  // iOS Safari predates display-mode; it exposes navigator.standalone instead.
  const iosStandalone = (navigator as unknown as { standalone?: boolean }).standalone === true
  return Boolean(mql) || iosStandalone
}

// ─── module-level singleton state ────────────────────────────────────────────────
const _permission = ref<PushPermission>('default')
const _subscribed = ref(false)
const _lastError = ref<'' | 'denied' | 'ios-reopen' | 'generic'>('')
const _canPromptInstall = ref(false)
const _installed = ref(false)
const _standalone = ref(detectStandalone())
let _deferredPrompt: BeforeInstallPromptEvent | null = null
let _registration: ServiceWorkerRegistration | null = null
let _wiredGlobalListeners = false
let _singleton: PushNotificationsApi | null = null

// Secure context (HTTPS / localhost) is the hard prerequisite: without it the
// browser hides serviceWorker/PushManager entirely. Detect it FIRST so the UI can
// give the real reason ("needs HTTPS") instead of a generic "unsupported".
const secureContext = typeof window === 'undefined' || window.isSecureContext !== false
const swSupported = secureContext && typeof navigator !== 'undefined' && 'serviceWorker' in navigator
const pushSupported = typeof window !== 'undefined' && 'PushManager' in window
const notificationSupported = typeof window !== 'undefined' && 'Notification' in window

function readPermission(): PushPermission {
  if (!notificationSupported) return 'unsupported'
  return Notification.permission as PushPermission
}

function wireGlobalListeners(): void {
  if (_wiredGlobalListeners || typeof window === 'undefined') return
  _wiredGlobalListeners = true
  // Chromium/Android only: capture the install prompt so the guide can replay it
  // on a user gesture (the event is single-shot and tied to a gesture).
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault()
    _deferredPrompt = e as BeforeInstallPromptEvent
    _canPromptInstall.value = true
  })
  window.addEventListener('appinstalled', () => {
    _installed.value = true
    _deferredPrompt = null
    _canPromptInstall.value = false
  })
  // Reflect runtime display-mode flips (e.g. launched from home screen).
  window.matchMedia?.('(display-mode: standalone)')?.addEventListener?.('change', (ev) => {
    _standalone.value = ev.matches || detectStandalone()
  })
  // Re-evaluate standalone + permission + subscription whenever the user returns to
  // the page. This is the iOS-critical hook: a Safari tab cannot detect that the app
  // was installed, but LAUNCHING from the home-screen icon mounts a fresh standalone
  // context — and on desktop, focusing back after granting permission in the browser
  // chrome updates the sheet without a manual reload. Cheap + idempotent.
  const reEval = (): void => { void _singleton?.refresh() }
  document.addEventListener('visibilitychange', () => { if (!document.hidden) reEval() })
  window.addEventListener('focus', reEval)
}

function createApi(): PushNotificationsApi {
  const { cliFetch } = useCliAuth()
  _permission.value = readPermission()
  wireGlobalListeners()

  const supported = computed(() => swSupported && pushSupported && notificationSupported)
  const isStandalone = computed(() => _standalone.value)
  const platform = computed(() => detectPlatform())

  async function ensureRegistration(): Promise<ServiceWorkerRegistration | null> {
    if (!swSupported) return null
    if (_registration) return _registration
    try {
      // Reuse an existing registration before registering anew (idempotent on reload).
      _registration = (await navigator.serviceWorker.getRegistration('/')) ||
        (await navigator.serviceWorker.register('/sw.js', { scope: '/' }))
      return _registration
    } catch {
      return null
    }
  }

  /**
   * Ensure the SW is BOTH ready AND controlling the page before we subscribe.
   *
   * Documented iOS PWA bug: pushManager.subscribe() silently fails (or hangs) when
   * the service worker isn't yet the page's controller. On a first standalone launch
   * the SW is installed/activated but `navigator.serviceWorker.controller` is still
   * null until the next navigation — so we wait briefly for `controllerchange`, and
   * if it never fires we try a one-shot re-register to claim the page. Returns true
   * when the page is controlled; false means the caller should surface the iOS
   * "fully close + relaunch" hint rather than a generic failure.
   */
  async function ensureControlling(): Promise<boolean> {
    if (!swSupported) return false
    try { await navigator.serviceWorker.ready } catch { /* keep going */ }
    if (navigator.serviceWorker.controller) return true
    // Wait one tick for an activating SW to claim the page.
    const gotControl = await new Promise<boolean>((resolve) => {
      const t = setTimeout(() => resolve(Boolean(navigator.serviceWorker.controller)), 1500)
      navigator.serviceWorker.addEventListener(
        'controllerchange',
        () => { clearTimeout(t); resolve(true) },
        { once: true },
      )
    })
    if (gotControl) return true
    // Last resort: re-register so a clients.claim()-ing SW takes control.
    try {
      _registration = await navigator.serviceWorker.register('/sw.js', { scope: '/' })
      await navigator.serviceWorker.ready
    } catch { /* ignore — fall through to the controller check */ }
    return Boolean(navigator.serviceWorker.controller)
  }

  async function syncExistingSubscription(): Promise<void> {
    if (!supported.value) { _subscribed.value = false; return }
    const reg = await ensureRegistration()
    if (!reg) { _subscribed.value = false; return }
    try {
      const sub = await reg.pushManager.getSubscription()
      _subscribed.value = Boolean(sub)
    } catch {
      _subscribed.value = false
    }
  }

  async function promptInstall(): Promise<'accepted' | 'dismissed' | 'unavailable'> {
    if (!_deferredPrompt) return 'unavailable'
    try {
      await _deferredPrompt.prompt()
      const choice = await _deferredPrompt.userChoice
      _deferredPrompt = null
      _canPromptInstall.value = false
      if (choice.outcome === 'accepted') _installed.value = true
      return choice.outcome
    } catch {
      return 'unavailable'
    }
  }

  async function subscribe(sessionId: string): Promise<boolean> {
    _lastError.value = ''
    if (!supported.value) { _lastError.value = 'generic'; return false }
    const reg = await ensureRegistration()
    if (!reg) { _lastError.value = 'generic'; return false }

    // Permission must be requested from a user gesture; callers wire this to a tap.
    let perm = readPermission()
    if (perm === 'default') {
      try { perm = (await Notification.requestPermission()) as PushPermission }
      catch { perm = readPermission() }
    }
    _permission.value = perm
    if (perm !== 'granted') { _lastError.value = 'denied'; return false }

    // iOS PWA: subscribe() silently fails unless the SW controls the page. Gate on it.
    const controlling = await ensureControlling()
    if (!controlling) { _lastError.value = 'ios-reopen'; return false }

    try {
      // Fetch the VAPID public key, then subscribe (reuse an existing sub if present).
      const vapidResp = await cliFetch(cliApi('/push/vapid'))
      if (!vapidResp.ok) { _lastError.value = 'generic'; return false }
      const { publicKey } = await vapidResp.json() as { publicKey: string }
      if (!publicKey) { _lastError.value = 'generic'; return false }

      let sub = await reg.pushManager.getSubscription()
      if (!sub) {
        try {
          sub = await reg.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(publicKey),
          })
        } catch {
          // The classic standalone-iOS silent failure: granted, controlling, yet
          // subscribe() still throws/rejects. The documented remedy is a full relaunch.
          _lastError.value = _standalone.value ? 'ios-reopen' : 'generic'
          _subscribed.value = false
          return false
        }
      }

      const body = {
        endpoint: sub.endpoint,
        keys: {
          p256dh: arrayBufferToBase64Url(sub.getKey('p256dh')),
          auth: arrayBufferToBase64Url(sub.getKey('auth')),
        },
        sessionId,
      }
      const resp = await cliFetch(cliApi('/push/subscribe'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      _subscribed.value = resp.ok
      if (!resp.ok) _lastError.value = 'generic'
      return resp.ok
    } catch {
      _subscribed.value = false
      _lastError.value = 'generic'
      return false
    }
  }

  async function unsubscribe(): Promise<boolean> {
    const reg = await ensureRegistration()
    if (!reg) { _subscribed.value = false; return true }
    try {
      const sub = await reg.pushManager.getSubscription()
      if (sub) {
        const endpoint = sub.endpoint
        await sub.unsubscribe()
        // Best-effort backend cleanup; local state is already opted-out either way.
        try {
          await cliFetch(cliApi('/push/unsubscribe'), {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ endpoint }),
          })
        } catch { /* endpoint already gone server-side — ignore */ }
      }
      _subscribed.value = false
      return true
    } catch {
      return false
    }
  }

  async function refresh(): Promise<void> {
    _permission.value = readPermission()
    _standalone.value = detectStandalone()
    if (_permission.value !== 'denied') _lastError.value = ''
    await syncExistingSubscription()
  }

  async function sendTest(): Promise<PushTestResult> {
    // Preferred path: a real subscription exists → ask the backend to push, which
    // exercises the entire SW + OS chain exactly as an agent-waiting event would.
    // The backend reports per-attempt delivery so we surface the HONEST outcome:
    // a delivered count, or the rejection status (e.g. Apple 403 BadJwtToken).
    if (_subscribed.value) {
      try {
        const resp = await cliFetch(cliApi('/push/test'), { method: 'POST' })
        if (resp.ok) {
          const r = await resp.json().catch(() => null) as
            | { sent?: number; rejected?: Array<{ status: number; reason?: string }> }
            | null
          const sent = r?.sent ?? 0
          const rejected = r?.rejected ?? []
          if (sent > 0) return { kind: 'delivered', sent }
          if (rejected.length > 0) return { kind: 'rejected', rejected }
          // ok but nothing delivered and nothing rejected → fall through to local self-test.
        }
      } catch { /* fall through to local self-test */ }
    }
    // Fallback (e.g. desktop foreground-only, or backend test failed): if permission
    // is granted we can still prove the OS-notification leg with a local Notification.
    if (readPermission() === 'granted' && notificationSupported) {
      try {
        new Notification('✅ 测试通知', { body: 'Deepwork 推送已就绪（本地自检）', tag: 'dw-test', icon: '/pwa-192.png' })
        return { kind: 'local' }
      } catch { /* quota / revoked mid-call — fall through */ }
    }
    return { kind: 'failed' }
  }

  // Kick off background registration + subscription sync (non-blocking).
  if (supported.value) void syncExistingSubscription()

  return {
    supported,
    secureContext,
    permission: _permission,
    subscribed: _subscribed,
    isStandalone,
    platform,
    canPromptInstall: _canPromptInstall,
    installed: _installed,
    lastError: _lastError,
    ensureRegistration,
    promptInstall,
    subscribe,
    unsubscribe,
    refresh,
    sendTest,
  }
}

export function usePushNotifications(): PushNotificationsApi {
  if (!_singleton) _singleton = createApi()
  return _singleton
}
