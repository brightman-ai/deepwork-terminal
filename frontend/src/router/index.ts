import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";

// ── Portal 注册 (静态 import 保证在 portalRegistry.getAll() 前执行) ─────────────
import "@terminal/portals/cli";
import "@terminal/portals/settings";

import { portalRegistry, isPortalEnabled } from "@ce/framework/portal";

// ── 默认 portal 路由 (动态: 取第一个已启用 portal，standalone 模式下自动指向 cli) ──
const defaultPortal = portalRegistry.getAll().find((p) => isPortalEnabled(p.id))
const defaultPortalPath = defaultPortal ? `/portal/${defaultPortal.id}` : '/portal/cli'

// ── 非 portal 路由 (layout 子路由) ─────────────────────────────────────────────
const staticChildren: RouteRecordRaw[] = [
  {
    path: "",
    redirect: defaultPortalPath,
  },
  { path: '/settings', redirect: '/portal/settings' },
];

// ── 从 registry 生成 portal 路由 (portal 的 index.ts 是唯一事实源) ───────────────
const portalRoutes: RouteRecordRaw[] = portalRegistry
  .getAll()
  .filter((descriptor) => isPortalEnabled(descriptor.id))
  .flatMap((descriptor) => descriptor.routes);

const routes: RouteRecordRaw[] = [
  {
    path: "/",
    component: () => import("@ce/layouts/MainLayout.vue"),
    children: [...staticChildren, ...portalRoutes],
  },
  {
    path: "/:catchAll(.*)*",
    component: () => import("@terminal/pages/ErrorNotFound.vue"),
  },
];

export default createRouter({
  history: createWebHistory(),
  routes,
});
