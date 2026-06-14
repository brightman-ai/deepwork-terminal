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
  /** Opt-in: request permission, subscribe, POST to backend. Returns success. */
  subscribe(sessionId: string): Promise<boolean>
  /** Opt-out: unsubscribe locally + tell the backend to forget the endpoint. */
  unsubscribe(): Promise<boolean>
  /** Re-read permission + existing subscription (e.g. on visibility regain). */
  refresh(): Promise<void>
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
const _canPromptInstall = ref(false)
const _installed = ref(false)
const _standalone = ref(detectStandalone())
let _deferredPrompt: BeforeInstallPromptEvent | null = null
let _registration: ServiceWorkerRegistration | null = null
let _wiredGlobalListeners = false
let _singleton: PushNotificationsApi | null = null

const swSupported = typeof navigator !== 'undefined' && 'serviceWorker' in navigator
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
    if (!supported.value) return false
    const reg = await ensureRegistration()
    if (!reg) return false

    // Permission must be requested from a user gesture; callers wire this to a tap.
    let perm = readPermission()
    if (perm === 'default') {
      try { perm = (await Notification.requestPermission()) as PushPermission }
      catch { perm = readPermission() }
    }
    _permission.value = perm
    if (perm !== 'granted') return false

    try {
      // Fetch the VAPID public key, then subscribe (reuse an existing sub if present).
      const vapidResp = await cliFetch(cliApi('/push/vapid'))
      if (!vapidResp.ok) return false
      const { publicKey } = await vapidResp.json() as { publicKey: string }
      if (!publicKey) return false

      let sub = await reg.pushManager.getSubscription()
      if (!sub) {
        sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: urlBase64ToUint8Array(publicKey),
        })
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
      return resp.ok
    } catch {
      _subscribed.value = false
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
    await syncExistingSubscription()
  }

  // Kick off background registration + subscription sync (non-blocking).
  if (supported.value) void syncExistingSubscription()

  return {
    supported,
    permission: _permission,
    subscribed: _subscribed,
    isStandalone,
    platform,
    canPromptInstall: _canPromptInstall,
    installed: _installed,
    ensureRegistration,
    promptInstall,
    subscribe,
    unsubscribe,
    refresh,
  }
}

export function usePushNotifications(): PushNotificationsApi {
  if (!_singleton) _singleton = createApi()
  return _singleton
}
