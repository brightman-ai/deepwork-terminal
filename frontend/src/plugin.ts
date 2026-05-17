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
  install(app: App, options: TerminalPluginOptions) {
    const { router, routePrefix = '/terminal' } = options

    router.addRoute({
      path: routePrefix,
      component: () => import('./pages/TerminalListPage.vue'),
    })

    router.addRoute({
      path: `${routePrefix}/:id`,
      component: () => import('./pages/TerminalPage.vue'),
    })
  },
}

export default TerminalPlugin
