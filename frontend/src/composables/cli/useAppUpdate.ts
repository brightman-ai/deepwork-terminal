/**
 * useAppUpdate — detect a newer deployed build and force the client onto it.
 *
 * Why this exists: index.html is served no-cache and neither shell's service worker
 * caches assets, yet a long-lived browser tab keeps running the JS it booted with —
 * it never re-navigates, so it never notices a redeploy. Users then stare at a stale
 * UI and a plain reload (or even /refresh in another tab) leaves THIS tab untouched.
 *
 * Detection is endpoint-free: the entry bundle is content-hashed (…/assets/index-<hash>.js),
 * so we capture the hash THIS tab booted with, then poll the server's (no-cache) index.html
 * and compare. Different hash ⇒ a new build is live ⇒ surface an "update" affordance.
 *
 * applyUpdate() is the single clear-and-reload path shared by the auto-update pill
 * (CliTabBar), the manual entry (HelpCenter), and the PWA refresh button: drop Cache
 * Storage + unregister any service worker, then hard-navigate through /fresh (which
 * 302-redirects to /?t=<unixnano> — a unique URL no cache can satisfy).
 */
import { ref } from 'vue'

const updateAvailable = ref(false)
let bootEntry = ''
let started = false

/** The entry-bundle path (…/assets/index-<hash>.js) THIS tab is running. */
function loadedEntry(): string {
  if (typeof document === 'undefined') return ''
  const el = document.querySelector('script[type="module"][src*="/assets/"]') as HTMLScriptElement | null
  const src = el?.getAttribute('src') || ''
  const m = src.match(/\/assets\/[\w.-]+\.js/)
  return m ? m[0] : ''
}

/** The entry-bundle path the server's current index.html references. */
function serverEntry(html: string): string {
  const m = html.match(/<script[^>]*type="module"[^>]*src="(\/assets\/[\w.-]+\.js)"/)
  return m ? m[1] : ''
}

async function check(): Promise<void> {
  if (updateAvailable.value || !bootEntry) return
  try {
    // no-store + a cache-busting query so no intermediary can hand back a stale index.
    const res = await fetch('/?u=' + Date.now(), { cache: 'no-store', credentials: 'same-origin' })
    if (!res.ok) return
    const server = serverEntry(await res.text())
    if (server && server !== bootEntry) updateAvailable.value = true
  } catch { /* offline / transient — try again next tick */ }
}

/** Drop client caches, nudge the SW to update, then hard-reload onto the latest build.
 *  The load-bearing step is the /fresh hard-navigation; the cache drop is belt-and-
 *  suspenders. We update() the SW rather than unregister() so the push subscription
 *  survives (both shells' SW is push-only, so there's no stale asset cache to evict —
 *  killing it would only cost the user their notifications). */
export async function applyAppUpdate(): Promise<void> {
  try {
    if (typeof window !== 'undefined' && window.caches) {
      const keys = await caches.keys()
      await Promise.all(keys.map((k) => caches.delete(k)))
    }
    if (typeof navigator !== 'undefined' && navigator.serviceWorker?.getRegistrations) {
      const regs = await navigator.serviceWorker.getRegistrations()
      for (const r of regs) void r.update()
    }
  } catch { /* best-effort — never block the reload on a cleanup failure */ }
  if (typeof window !== 'undefined') {
    window.location.replace('/fresh' + window.location.search)
  }
}

export function useAppUpdate() {
  if (typeof window !== 'undefined' && !started) {
    started = true
    bootEntry = loadedEntry()
    const tick = () => { void check() }
    // Poll on a slow interval (a redeploy is rare) + whenever the tab regains focus.
    setInterval(tick, 90_000)
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') tick()
    })
    setTimeout(tick, 12_000) // first check after boot settles
  }
  return { updateAvailable, applyUpdate: applyAppUpdate }
}
