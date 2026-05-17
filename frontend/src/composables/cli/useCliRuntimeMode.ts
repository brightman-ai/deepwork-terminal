import { ref, onMounted, onUnmounted } from 'vue'

export type CliRuntimeMode = 'pc-browser' | 'wails-pc' | 'ios-safari'

export interface CliRuntimeProfile {
  mode: CliRuntimeMode
  isWailsDesktop: boolean
  isIOSWebKit: boolean
  hasTouch: boolean
  isNarrowTouch: boolean
  usesMobileOverlay: boolean
  usesVisualKeyboardInset: boolean
  focusRequiresPreventScroll: boolean
}

export interface CliRuntimeEnvironment {
  userAgent: string
  platform: string
  maxTouchPoints: number
  innerWidth: number
  hasTouchEvent: boolean
  hasWailsRuntime: boolean
}

function browserEnvironment(): CliRuntimeEnvironment | null {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') {
    return null
  }

  const w = window as Window & {
    __wails?: unknown
    __wails_invoke__?: unknown
    wails?: unknown
  }
  return {
    userAgent: navigator.userAgent || '',
    platform: navigator.platform || '',
    maxTouchPoints: navigator.maxTouchPoints || 0,
    innerWidth: window.innerWidth || 1024,
    hasTouchEvent: 'ontouchstart' in window,
    hasWailsRuntime: !!(w.__wails || w.__wails_invoke__ || w.wails),
  }
}

export function computeCliRuntimeProfile(env: CliRuntimeEnvironment | null): CliRuntimeProfile {
  if (!env) {
    return {
      mode: 'pc-browser',
      isWailsDesktop: false,
      isIOSWebKit: false,
      hasTouch: false,
      isNarrowTouch: false,
      usesMobileOverlay: false,
      usesVisualKeyboardInset: false,
      focusRequiresPreventScroll: false,
    }
  }

  const isWailsDesktop = env.hasWailsRuntime
  const ua = env.userAgent
  const platform = env.platform
  const iOSDevice = /iPhone|iPad|iPod/i.test(ua)
    || (platform === 'MacIntel' && env.maxTouchPoints > 1)
  const appleWebKit = /AppleWebKit/i.test(ua)
  const isIOSWebKit = iOSDevice && appleWebKit
  const hasTouch = env.hasTouchEvent || env.maxTouchPoints > 0
  const isNarrowTouch = hasTouch && env.innerWidth < 1024
  const mode: CliRuntimeMode = isWailsDesktop
    ? 'wails-pc'
    : isIOSWebKit
      ? 'ios-safari'
      : 'pc-browser'
  const usesMobileOverlay = !isWailsDesktop && (isIOSWebKit || isNarrowTouch)

  return {
    mode,
    isWailsDesktop,
    isIOSWebKit,
    hasTouch,
    isNarrowTouch,
    usesMobileOverlay,
    usesVisualKeyboardInset: usesMobileOverlay,
    focusRequiresPreventScroll: isIOSWebKit || usesMobileOverlay,
  }
}

export function detectCliRuntimeProfile(): CliRuntimeProfile {
  return computeCliRuntimeProfile(browserEnvironment())
}

export function useCliRuntimeMode() {
  const profile = ref<CliRuntimeProfile>(detectCliRuntimeProfile())

  function refresh(): void {
    profile.value = detectCliRuntimeProfile()
  }

  onMounted(() => {
    refresh()
    window.addEventListener('resize', refresh)
    window.addEventListener('orientationchange', refresh)
  })

  onUnmounted(() => {
    window.removeEventListener('resize', refresh)
    window.removeEventListener('orientationchange', refresh)
  })

  return { profile, refresh }
}
