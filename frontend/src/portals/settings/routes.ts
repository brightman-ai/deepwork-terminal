import type { RouteRecordRaw } from 'vue-router'

export const settingsRoutes: RouteRecordRaw[] = [
  {
    path: '/portal/settings',
    name: 'portal-settings',
    component: () => import('./SettingsPortal.vue'),
    meta: { scrollMode: 'contained' },
  },
]
