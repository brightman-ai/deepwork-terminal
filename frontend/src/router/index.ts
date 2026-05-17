import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";

// ── Portal 注册 (静态 import 保证在 portalRegistry.getAll() 前执行) ─────────────
import "@/portals/cli";
import "@/portals/settings";

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
  {
    path: "/cli",
    name: "cli-workbench",
    component: () => import("@/pages/TerminalWorkbenchPage.vue"),
    meta: { scrollMode: "contained" },
  },
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
    path: "/cli/:id",
    name: "cli-terminal",
    component: () => import("@/pages/TerminalPage.vue"),
    props: true,
  },
  {
    path: "/:catchAll(.*)*",
    component: () => import("@/pages/ErrorNotFound.vue"),
  },
];

export default createRouter({
  history: createWebHistory(),
  routes,
});
