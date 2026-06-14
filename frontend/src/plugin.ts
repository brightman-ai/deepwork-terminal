/**
 * deepwork-terminal Vue Plugin
 *
 * Standalone usage: App.vue bootstraps directly via main.ts (no plugin needed).
 *
 * Embedded usage (CE App Shell / deepwork):
 *   import TerminalPlugin, { terminalPortalMeta } from 'deepwork-terminal/plugin'
 *   app.use(TerminalPlugin, { router, routePrefix: '/terminal' })
 *
 * The CE shell calls install(), which registers the terminal's routes into
 * the host router. terminalPortalMeta provides the label and icon for nav.
 */
import type { App } from 'vue'
import type { Router } from 'vue-router'

export interface TerminalPluginOptions {
  router: Router
  /** Route prefix for terminal pages. Defaults to '/terminal'. */
  routePrefix?: string
}

/** Metadata the CE App Shell uses to build navigation entries. */
export interface PortalMeta {
  id: string
  label: string
  routePrefix: string
  icon?: string
}

export const terminalPortalMeta: PortalMeta = {
  id: 'terminal',
  label: 'Terminal',
  routePrefix: '/terminal',
  icon: 'terminal',
}

const TerminalPlugin = {
  install(_app: App, options: TerminalPluginOptions) {
    const { router, routePrefix = '/terminal' } = options

    // The terminal UI is the canonical CliPortal (the SSOT host that mounts CliTabBar +
    // CliTerminalSurface, which now owns its own status row). The legacy TerminalListPage /
    // TerminalPage forks were removed; the embedded host mounts the same portal standalone uses.
    router.addRoute({
      path: routePrefix,
      component: () => import('./portals/cli/CliPortal.vue'),
    })
  },
}

export default TerminalPlugin
