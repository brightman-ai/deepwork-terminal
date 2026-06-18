/**
 * useBuildVersion — pick up a new frontend build without a manual refresh.
 *
 * A long-open tab can keep running stale JS after a deploy. We identify the loaded build
 * by its entry-module content hash (assets/index-<hash>.js) and compare it to the server's
 * CURRENT index.html (fetched no-store) whenever the tab regains focus — the moment the
 * user has just returned and is NOT mid-keystroke. On a mismatch we reload: the terminal
 * session lives server-side (tmux/PTY), so the reload reconnects to the SAME session with
 * no loss. Pairs with the no-cache index.html (kit/webserve) which makes the refetch
 * authoritative. This is the going-forward fix; a tab opened BEFORE no-cache shipped still
 * needs one hard refresh to land on a build that carries this checker.
 */
import { onMounted, onUnmounted } from 'vue'

/** The content hash of the entry module this tab is running (null if not resolvable). */
function loadedBuildHash(): string | null {
  const el = document.querySelector(
    'script[type="module"][src*="/assets/index-"]',
  ) as HTMLScriptElement | null
  return el?.src.match(/index-([A-Za-z0-9_-]+)\.js/)?.[1] ?? null
}

export function useBuildVersion() {
  const loaded = loadedBuildHash()
  let reloading = false

  async function checkAndReload(): Promise<void> {
    if (reloading || !loaded || document.visibilityState !== 'visible') return
    try {
      const html = await (await fetch('/', { cache: 'no-store' })).text()
      const current = html.match(/assets\/index-([A-Za-z0-9_-]+)\.js/)?.[1]
      if (current && current !== loaded) {
        reloading = true
        window.location.reload()
      }
    } catch {
      /* offline / transient — retry on the next focus */
    }
  }

  function onVisible(): void {
    if (document.visibilityState === 'visible') void checkAndReload()
  }

  onMounted(() => document.addEventListener('visibilitychange', onVisible))
  onUnmounted(() => document.removeEventListener('visibilitychange', onVisible))

  return { checkAndReload }
}
