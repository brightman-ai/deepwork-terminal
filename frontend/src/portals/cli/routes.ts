export const cliRoutes = [
  {
    path: '/portal/cli',
    name: 'portal-cli',
    component: () => import('./CliPortal.vue'),
    meta: { scrollMode: 'contained' },
  },
]
