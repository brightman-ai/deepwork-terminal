import type { LayoutPolicyConfig } from '@ce/composables/layout/useLayoutPolicy'

export const settingsLayoutPolicy: LayoutPolicyConfig = {
  effects: {
    'provider.auth_needed': (ctx) => {
      // WebChat provider needs login → expand secondary browser overlay
      if (!ctx.userManuallyClosed('secondary')) {
        return { slot: 'secondary', action: 'expand' }
      }
      return { slot: 'secondary', action: 'badge', badgeCount: 1 }
    },
  },
}
