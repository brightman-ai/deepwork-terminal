import type { LayoutPolicyConfig } from '@ce/composables/layout/useLayoutPolicy'

export const cliLayoutPolicy: LayoutPolicyConfig = {
  effects: {
    // When an agent needs input, badge the tab (handled by CliTabBar directly via
    // tabRuntimes — no secondary slot to expand). No layout-level effect needed here
    // beyond the AgentStatusStrip visual cue.
    'agent.needs_input': (ctx) => {
      if (!ctx.userManuallyClosed('primary')) {
        return { slot: 'primary', action: 'expand' }
      }
      return null
    },
  },
}
