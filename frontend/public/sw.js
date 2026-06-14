/* Deepwork Terminal — service worker (hand-rolled, push-only).
 *
 * Deliberately NOT a caching/offline SW: this is a live remote-terminal portal,
 * so stale-serving the shell would be a footgun. The SW exists solely to receive
 * Web Push while no tab is focused (or no tab is open) and to route a tap back to
 * the right session. Registered from the app at /sw.js with scope '/'.
 *
 * Backend push payload contract (fixed):
 *   { title, body, tag, data: { url, sessionId } }
 */

// Take control immediately on install/activate so the first subscribe works
// without a reload (no offline cache to migrate, so skipWaiting is safe).
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('push', (event) => {
  let payload = {}
  try {
    payload = event.data ? event.data.json() : {}
  } catch {
    // Non-JSON / opaque payload — degrade to a generic nudge rather than drop it.
    payload = { title: 'Deepwork Terminal', body: event.data ? event.data.text() : '' }
  }
  const title = payload.title || 'Deepwork Terminal'
  const data = payload.data || {}
  const options = {
    body: payload.body || '',
    // tag lets the foreground fallback and the backend push collapse onto the
    // same notification instead of double-stacking (renotify keeps it lively).
    tag: payload.tag || (data.sessionId ? `dw-agent-${data.sessionId}` : 'dw-agent'),
    renotify: true,
    icon: '/pwa-192.png',
    badge: '/pwa-192.png',
    data,
  }
  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const target = (event.notification.data && event.notification.data.url) || '/'
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      // Prefer focusing an already-open tab; navigate it to the target session if
      // the client supports it. Fall back to opening a fresh window.
      for (const client of clients) {
        if ('focus' in client) {
          client.focus()
          if (target && 'navigate' in client && client.url !== target) {
            try { client.navigate(target) } catch { /* cross-origin / unsupported — ignore */ }
          }
          return undefined
        }
      }
      if (self.clients.openWindow) return self.clients.openWindow(target)
      return undefined
    }),
  )
})
