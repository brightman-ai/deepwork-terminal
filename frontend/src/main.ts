import { createApp } from 'vue'
import { createPinia } from 'pinia'
import router from './router'
import App from './App.vue'
import { configureRemoteSink, createLogger } from '@ce/utils/obs'
import { reportCliInputDiagnostic } from '@terminal/composables/cli/useCliInputDiagnostics'

// Import Tailwind CSS
import '@ce/assets/main.css'

// ── Portal registrations (side-effect imports — must run before app mount) ────
import '@terminal/portals/cli'
import '@terminal/portals/settings'

configureRemoteSink()
const log = createLogger('main')
const inputLog = createLogger('input-hit-test')

// ── Phase 3: Global error capture — window.onerror + unhandledrejection ────────
// Installed at the earliest possible moment so any error during Vue init is caught.
// These fire-and-forget sends to /api/telemetry/log provide iOS Safari observability.
window.onerror = (_msg, src, line, col, err) => {
  log.error('window.onerror', { src, line, col, stack: err?.stack })
}
window.addEventListener('unhandledrejection', (ev) => {
  log.error('window.unhandledrejection', { reason: String(ev.reason), stack: (ev.reason as Error)?.stack })
})

// ── Bug 3 fix: 阻止 Safari 恢复上次 scroll + zoom 状态 ─────────────────────────
// iOS Safari 从 iOS 10+ 起会在 F5 刷新后恢复用户上次的 scrollRestoration（含 zoom）。
// 将 scrollRestoration 设为 'manual' 告知浏览器不自动恢复，由我们自己控制。
if ('scrollRestoration' in history) {
  history.scrollRestoration = 'manual'
}

// ── Browser Portal / mobile Safari: authoritative visible viewport height ────
// CSS 100vh/h-screen uses the layout viewport on iOS Safari and can include
// browser chrome. The app shell is intentionally overflow:hidden, so using 100vh
// can clip fixed toolbars. visualViewport.height is the runtime truth for the
// currently visible area; CSS consumes it via --dw-app-viewport-height.
function syncAppVisualViewport(reason = 'event'): void {
  const vv = window.visualViewport
  const height = Math.max(1, Math.round(vv?.height ?? window.innerHeight))
  const offsetTop = Math.max(0, Math.round(vv?.offsetTop ?? 0))
  document.documentElement.style.setProperty('--dw-app-viewport-height', `${height}px`)
  document.documentElement.style.setProperty('--dw-app-viewport-offset-top', `${offsetTop}px`)
  reportCliInputDiagnostic('app.viewport.sync', { reason, height, offsetTop })
}

function syncAppVisualViewportSettled(reason = 'settled'): void {
  syncAppVisualViewport(`${reason}:now`)
  requestAnimationFrame(() => syncAppVisualViewport(`${reason}:raf`))
  setTimeout(() => syncAppVisualViewport(`${reason}:120`), 120)
  setTimeout(() => syncAppVisualViewport(`${reason}:360`), 360)
}

syncAppVisualViewport('boot')
window.addEventListener('resize', () => syncAppVisualViewport('window.resize'))
window.addEventListener('orientationchange', () => syncAppVisualViewportSettled('orientationchange'))
window.visualViewport?.addEventListener('resize', () => syncAppVisualViewport('visual.resize'))
window.visualViewport?.addEventListener('scroll', () => syncAppVisualViewport('visual.scroll'))
document.addEventListener('focusin', () => syncAppVisualViewportSettled('focusin'))
document.addEventListener('focusout', () => syncAppVisualViewportSettled('focusout'))

// ── TS-OBS: global input hit-test diagnostics ────────────────────────────────
// Logs only discrete control activations. It does not prevent/modify events, but
// gives enough geometry to diagnose "I clicked A, app triggered B" reports across
// Wails desktop, PC browser, and iOS Safari.
function cssPathFor(el: Element | null): string {
  if (!el) return ''
  const parts: string[] = []
  let node: Element | null = el
  while (node && parts.length < 5) {
    let part = node.tagName.toLowerCase()
    const testid = node.getAttribute('data-testid')
    const role = node.getAttribute('role')
    if (testid) part += `[data-testid="${testid}"]`
    else if (node.id) part += `#${node.id}`
    else if (role) part += `[role="${role}"]`
    const className = typeof node.className === 'string'
      ? node.className.trim().split(/\s+/).filter(Boolean).slice(0, 3).join('.')
      : ''
    if (!testid && !node.id && className) part += `.${className}`
    parts.unshift(part)
    node = node.parentElement
  }
  return parts.join(' > ')
}

function inputTargetSummary(target: EventTarget | null): Record<string, unknown> {
  const el = target instanceof Element ? target : null
  if (!el) return {}
  const control = el.closest('button,a,input,textarea,select,[role="button"],[data-testid]') as HTMLElement | null
  const rect = control?.getBoundingClientRect()
  return {
    target_path: cssPathFor(el),
    control_path: cssPathFor(control),
    control_text: control?.innerText?.trim().slice(0, 80) || control?.getAttribute('aria-label') || '',
    control_disabled: control instanceof HTMLButtonElement || control instanceof HTMLInputElement || control instanceof HTMLTextAreaElement || control instanceof HTMLSelectElement
      ? control.disabled
      : control?.getAttribute('aria-disabled') === 'true',
    control_rect: rect
      ? {
          x: Math.round(rect.x),
          y: Math.round(rect.y),
          w: Math.round(rect.width),
          h: Math.round(rect.height),
        }
      : undefined,
  }
}

function installInputHitTestDiagnostics(): void {
  let lastPointerAt = 0
  const shouldLog = (event: Event): boolean => {
    const target = event.target instanceof Element ? event.target : null
    if (!target) return false
    return !!target.closest('button,a,input,textarea,select,[role="button"],[data-testid]')
  }
  const logEvent = (event: PointerEvent | MouseEvent, phase: 'pointerdown' | 'click') => {
    if (!shouldLog(event)) return
    if (phase === 'pointerdown') lastPointerAt = performance.now()
    const vv = window.visualViewport
    const hit = document.elementFromPoint(event.clientX, event.clientY)
    const root = document.documentElement
    inputLog.info('control hit-test', {
      phase,
      route: window.location.pathname,
      x: Math.round(event.clientX),
      y: Math.round(event.clientY),
      button: 'button' in event ? event.button : undefined,
      pointer_type: 'pointerType' in event ? event.pointerType : undefined,
      pointer_to_click_ms: phase === 'click' && lastPointerAt > 0
        ? Math.round(performance.now() - lastPointerAt)
        : undefined,
      element_from_point: cssPathFor(hit),
      viewport: {
        inner_w: window.innerWidth,
        inner_h: window.innerHeight,
        dpr: window.devicePixelRatio,
        visual_w: Math.round(vv?.width ?? 0),
        visual_h: Math.round(vv?.height ?? 0),
        visual_offset_top: Math.round(vv?.offsetTop ?? 0),
        visual_offset_left: Math.round(vv?.offsetLeft ?? 0),
        visual_scale: Number((vv?.scale ?? 1).toFixed(3)),
        titlebar_inset: getComputedStyle(root).getPropertyValue('--dw-titlebar-inset').trim(),
        app_viewport_h: getComputedStyle(root).getPropertyValue('--dw-app-viewport-height').trim(),
      },
      ...inputTargetSummary(event.target),
    })
  }
  document.addEventListener('pointerdown', (event) => logEvent(event, 'pointerdown'), { capture: true })
  document.addEventListener('click', (event) => logEvent(event, 'click'), { capture: true })
}

installInputHitTestDiagnostics()

// ── Bug 3 fix: JS 强制重置 iOS Safari viewport scale ──────────────────────────
// iOS Safari 会忽略 maximum-scale=1.0 对用户手势缩放的限制（可访问性合规）。
// 但通过 JS 动态修改 viewport meta content 可以触发浏览器重新解析，强制回到 scale=1.0。
// 仅在 iOS 设备上执行，避免影响桌面端。
function resetViewportZoom(): void {
  const viewport = document.querySelector('meta[name=viewport]') as HTMLMetaElement | null
  if (!viewport) return
  // 先设置包含 maximum-scale=1.0 的严格值（触发浏览器重解析）
  viewport.content = 'width=device-width, initial-scale=1.0, minimum-scale=1.0, maximum-scale=1.0, user-scalable=no, viewport-fit=cover'
  // 下一帧恢复原始值（已经触发了 scale 重置效果）
  requestAnimationFrame(() => {
    viewport.content = 'width=device-width, initial-scale=1.0, minimum-scale=1.0, maximum-scale=1.0, user-scalable=no, viewport-fit=cover'
  })
}

if (typeof navigator !== 'undefined' && /iphone|ipad|ipod/i.test(navigator.userAgent)) {
  // 页面加载时立即重置
  resetViewportZoom()
  // 从后台切回前台时也重置（iOS Safari 可能在 visibilitychange 时恢复 zoom）
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      resetViewportZoom()
    }
  })
  // Bug 4 fix: Detect and auto-reset viewport zoom whenever visualViewport resizes.
  // When the user accidentally triggers pinch-zoom, scale becomes > 1.
  // Resetting the viewport meta content triggers Safari to re-parse and reset scale.
  window.visualViewport?.addEventListener('resize', () => {
    const scale = window.visualViewport?.scale ?? 1
    if (scale > 1.01) {
      resetViewportZoom()
      log.info('zoom detected and reset', { scale })
    }
  })
}

const app = createApp(App)
const pinia = createPinia()

// ── Phase 3: Vue error handler — catches errors in components/lifecycle hooks ──
app.config.errorHandler = (err, _vm, info) => {
  log.error('Vue.errorHandler', { info, error: String(err), stack: (err as Error)?.stack })
}

app.use(pinia)
app.use(router)

app.mount('#app')

// ── PWA: register the push service worker on load ─────────────────────────────
// usePushNotifications owns the registration (idempotent, feature-guarded). We
// nudge it here so the SW is live before the user opens the install guide; on
// platforms without serviceWorker this is a silent no-op.
import('@terminal/composables/cli/usePushNotifications')
  .then(({ usePushNotifications }) => usePushNotifications().ensureRegistration())
  .catch((err) => log.warn('pwa.sw.register failed', { error: String(err) }))
