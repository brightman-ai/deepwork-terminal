# Portal Section Contribution Registry — 三仓 SSOT 终局设计

> 作者视角:Linus。一句话:`definePortal` 解决了"导航 → 独立 portal",本设计解决"portal **内部 section** 跨仓贡献/复用"。
> 三仓:`deepwork`(@ce, public 框架) · `deepwork-terminal`(public) · `deepwork-pro`(private 宿主)。源码级 Vite alias 依赖(`@ce`/`@terminal`),改 @ce 自动影响两下游。

---

## 1. 问题定义(第一面墙)

**现状能力**:`@ce/framework/portal` 的 `definePortal` + `portalRegistry` 支持"一个导航菜单项 → 一个独立 portal(整块路由+场景)"。✓

**缺口**:portal **内部的 section(板块)** 无法跨仓复用。具体:
- terminal 的 **Network/cloudflare 隧道**配置段,逻辑上属于 terminal 域,应同时出现在:
  - terminal-standalone 的 settings(已有);
  - pro 的 settings(**当前没有** → 在 pro 里配不了 terminal 的 cloudflare)。
- 但 terminal 与 pro 各自**硬编码**了一套 settings section 数组(terminal: System/Terminal/Network;pro v2: AI供应商/运行时/用量/...),零共享、必然分叉。

**证据**(调研):
- @ce `framework/portal/`:只有 portal 级注册,**无 section/slot/contribution 机制**;SlotGrid 是纯布局引擎(分割/折叠/tab),非内容贡献。无共享 SettingsPortal 壳。
- terminal `portals/settings/`:category 硬编码(`SettingsCategoryRail.vue:26`、`SettingsFormView.vue:21`),内容内联(`v-if field.id==='__network'`),但每段**逻辑自包含**(各自 `onMounted` 调 `/api/system`、`/api/settings`、`/api/tunnel/*`),拆分成本低。
- pro `v2/portals/settings/SettingsV2.vue`:独立 v2 设计,零 `@terminal` 复用,**无 Network/tunnel 段**。两仓都注册 `id='settings' route='/portal/settings'`,各 build 各一份 = 分叉。

---

## 2. 设计原则(Linus)

1. **SSOT**:每个 section 定义**一次**,在它的 owner repo;消费方零重复、零手工列表。
2. **适度抽象**:最小 registry(注册 + 取出渲染),**不是**全插件系统(无生命周期/消息总线)。
3. **扩展**:加一个 section = owner repo 一次 `definePortalSection`,所有消费该 portal 的壳**自动**显示。
4. **对称**:与现有 `definePortal` 同一心智模型(define + registry + 副作用 import 自注册)。
5. **不妥协不遗留**:彻底消除两仓 settings 分叉的**结构性根因**,而非补丁。

---

## 3. 核心原语:@ce Portal Section Registry

**新增** `deepwork/frontend/src/framework/portal/sections.ts`(与 `definePortal` 并列):

```ts
import type { Component } from 'vue'

export interface PortalSection {
  /** 贡献到哪个 portal(如 'settings');泛化 → 任何 portal 可接受 section */
  portalId: string
  /** 全局唯一,命名空间化避免撞名:'terminal.network' / 'pro.ai-providers' */
  id: string
  /** 可选分组(同 portal 内的大类);settings 里就是 category */
  group?: string
  label: string
  icon?: Component
  /** 组内排序;同分数按注册序 */
  order?: number
  /** 内容组件,支持懒加载(() => import) */
  component: Component | (() => Promise<Component>)
  /** 运行时门控:standalone-only / 嵌入态 / feature flag。缺省=显示 */
  enabled?: () => boolean
}

const registry = new Map<string, PortalSection[]>()  // by portalId

export function definePortalSection(s: PortalSection): void {
  const list = registry.get(s.portalId) ?? []
  if (list.some(x => x.id === s.id)) return       // 幂等(HMR/重复 import 安全)
  list.push(s)
  registry.set(s.portalId, list)
}

/** 取某 portal 的全部 section,enabled 过滤 + group/order 排序。壳只认这个。 */
export function getPortalSections(portalId: string): PortalSection[] {
  return (registry.get(portalId) ?? [])
    .filter(s => s.enabled?.() ?? true)
    .sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
}
```

**为什么够用、不过度**:registry 只做"注册 + 排序取出"。section 的渲染、状态、API 全由 section 组件自己负责(它本就自包含)。壳不需要知道 section 内部。

---

## 4. SSOT 归属表(谁定义、出现在哪)

| section id | owner repo | 内容 | terminal-standalone | pro |
|---|---|---|---|---|
| `terminal.system` | terminal | 端口/进程/版本 (`/api/system`) | ✓ | ✓ |
| `terminal.shell` | terminal | Shell/缓冲区/会话 (`/api/settings`) | ✓ | ✓ |
| `terminal.network` | terminal | 认证码 + Cloudflare 隧道 (`/api/tunnel/*`) | ✓ | ✓ |
| `ce.appearance` (可选) | @ce | 主题/外观 | ✓ | ✓ |
| `pro.ai-providers` | pro | AI 供应商 | — | ✓ |
| `pro.agent-runtimes` | pro | CLI 运行时检测 | — | ✓ |
| `pro.usage` / `pro.setup-wizard` / ... | pro | pro 专属 | — | ✓ |

**关键**:`terminal.network`(cloudflare)只在 **terminal** 定义一次,pro 通过 side-effect import 获得 → pro 里能配 terminal 隧道。**分叉消除**。

---

## 5. 消费方式:注册即贡献(side-effect import)

section 模块在 import 时自注册,与 portal 完全同构:

```ts
// @terminal/portals/settings/sections/index.ts  (terminal 拥有)
import { definePortalSection } from '@ce/framework/portal/sections'
import { Info, Terminal, Globe } from 'lucide-vue-next'

definePortalSection({ portalId:'settings', id:'terminal.system', group:'terminal',
  label:'System', icon:Info, order:10, component:()=>import('./SystemSection.vue') })
definePortalSection({ portalId:'settings', id:'terminal.shell', group:'terminal',
  label:'Terminal', icon:Terminal, order:20, component:()=>import('./ShellSection.vue') })
definePortalSection({ portalId:'settings', id:'terminal.network', group:'terminal',
  label:'Network', icon:Globe, order:30, component:()=>import('./NetworkSection.vue') })
```

- **terminal-standalone**:其 router 已 `import '@terminal/portals/settings'` → 连带 import sections → 自注册。
- **pro**:在 pro 的 settings 注册处加一行 side-effect import:
  ```ts
  import '@terminal/portals/settings/sections'  // 贡献 terminal 的 System/Shell/Network 到 pro settings
  ```
  pro 自己的 section 同样 `definePortalSection`。pro 的 settings 壳渲染 `getPortalSections('settings')` → **union**。

---

## 6. 渲染:壳消费 registry(不强制统一壳)

**适度抽象的关键取舍**:section 复用(真需求)不要求 shell 统一(另有复杂度)。两仓壳各自调 `getPortalSections('settings')` 渲染即可:

- terminal `SettingsFormView` / pro `SettingsV2`:把硬编码 category 数组 → `getPortalSections('settings')`;desktop rail / mobile tabs 都从这个列表生成;选中项渲染 `<component :is="section.component">`。
- (可选 P4 终局)抽 `@ce/components/PortalSectionHost.vue` 统一 desktop-rail + mobile-tabs 壳,两仓共用,**彻底**消除壳重复。非阻塞。

---

## 7. route/id 冲突的彻底解决

两仓现都注册 `definePortal id='settings' route='/portal/settings'`(各 build 各一份)。终局应**把 settings portal descriptor 上移 @ce**(`@ce` 定义 `id='settings'` + 路由指向 @ce 的 PortalSectionHost),两仓只 `definePortalSection` 贡献内容,不再各自 `definePortal('settings')`。→ 同 id/route 分叉**结构性消除**。(P4,与统一壳一起做。)

---

## 8. 为什么不是别的方案(穿墙 vs 绕墙)

| 备选 | 否决理由 |
|---|---|
| 每个 host 显式 import + 摆放 section | host 维护 section 列表 = 重复 + 漂移(正是当前病根)。registry 自注册免维护。 |
| 把 cloudflare 段塞进 @ce | @ce 不该知道 terminal 业务域。owner=terminal,贡献到 settings。 |
| 复用整个 settings portal | portal 是路由级、两仓 portal 集不同;复用粒度应是 **section**。 |
| 用 SlotGrid 承载 | SlotGrid 是布局引擎(分割/tab),非"按 portal 注册内容块";语义不符,过重。 |

---

## 9. 迁移计划(分阶段,每阶段可独立验收)

- **P1 @ce**(additive,零破坏):新增 `framework/portal/sections.ts`。导出 `definePortalSection`/`getPortalSections`。
- **P2 terminal**:把 SettingsFormView 的 System/Terminal/Network 抽成 3 个自包含 `sections/*.vue`(各自 API);新增 `sections/index.ts` 注册;`SettingsFormView`/`CategoryRail` 改为消费 `getPortalSections('settings')`。standalone 验收三段照常。
- **P3 pro**(在 worktree `deepwork-pro-sections-wt`):pro 的 settings section 改为 `definePortalSection`;加 `import '@terminal/portals/settings/sections'`;`SettingsV2` 渲染 `getPortalSections`。验收:pro settings 出现 Network 段且能起隧道。
- **P4(可选终局)**:settings portal descriptor + PortalSectionHost 壳上移 @ce,消除同 route 分叉 + 壳重复。

---

## 10. 验收锚

- ■ terminal-standalone settings:System/Terminal/Network 三段,来自 registry。
- ■ pro settings:pro 各段 + terminal 的 Network 段(同一份代码),cloudflare 可配。
- ■ 加新 section:只在 owner repo 一次 `definePortalSection`,两仓壳自动显示,零 host 改动。
- ■ `terminal.network` 全仓**唯一定义**(grep 只一处)。

---

# 增补(终局 = P4-direct):多仓集成 + 多 portal/region 泛化

> 决策:直接做 P4(共享壳 + descriptor 上移 @ce),不留两套壳。同时把机制从"settings 专用"泛化为"任意 portal 的任意 region 可贡献",并显式设计**多仓编译集成**边界。重点仍是 settings。

## 11. 泛化:slot = 扩展点(支持多 portal + 多 region)

把"贡献到哪"的键从 `portalId` 升级为 **`slot: string`**(扩展点 id),约定:
- `'<portal>'` —— portal 的默认 section 列表(如 `'settings'`)。
- `'<portal>.<region>'` —— portal 内的具名子区(如 `'settings.advanced'`、未来 `'cli.toolbar'`、`'dashboard.widgets'`)。

```ts
export interface PortalSection {
  slot: string                 // 扩展点:'settings' | 'settings.advanced' | 'cli.toolbar' | ...
  id: string                   // 全局唯一,命名空间化:'terminal.network'
  group?: string; label: string; icon?: Component; order?: number
  component: Component | (() => Promise<Component>)
  enabled?: () => boolean
}
export function definePortalSection(s: PortalSection): void
export function getPortalSections(slot: string): PortalSection[]   // enabled 过滤 + order 排序
```

- **多 portal**:不同 portal 用不同 slot(`'settings'` vs `'dashboard'`)。
- **多 region(选区)**:同 portal 多个具名 slot(`'settings'` + `'settings.advanced'`)。
- **PortalSectionHost** 接 `slot` prop,任何 section-可组合的 portal 复用同一个壳。
- 单一 `Map<slot, PortalSection[]>`,语义统一,**零额外抽象**承载多 portal/region。focus settings 时只用 `slot='settings'`。

## 12. 多仓编译集成边界(SSOT 的关键)

**心智模型 = Composition Root(组合根)**:每个 feature repo 暴露**一个副作用入口**注册它的全部贡献(portals + sections);**host(被编译的 app)** 通过 import 决定纳入哪些 repo。host 的 import 图**就是**清单(显式、可 tree-shake),无中心 manifest。

```
@terminal/contributions.ts   // terminal 的贡献入口(side-effect)
  import './portals/cli'                 // cli portal
  import './portals/settings/sections'   // System/Shell/Network sections
@pro 自己的 contributions(各 portal + sections)
@ce 提供:registry + PortalSectionHost + settings portal descriptor(共享壳)
```

| 场景 | host | import 组合 | settings 呈现 |
|---|---|---|---|
| terminal 单独编译 | terminal | `@ce` + `@terminal/contributions` | @ce壳 + terminal三段 |
| pro 集成编译 | pro | `@ce` + pro贡献 + `@terminal/contributions` | @ce壳 + pro段 + terminal三段(union) |
| 未来:host 集成 N 个 public repo | pro/新host | `@ce` + repoA + repoB + ... | @ce壳 + 各 repo 贡献 union |

**保证 SSOT 的四条编译边界铁律**:
1. **单 registry 实例**:`@ce` 经 Vite alias 去重 → 每个 build 只有一份 `Map`;`@terminal` 的 section 副作用 import 解析到**同一** `@ce` 实例 → 注册进同一表。(若 @ce 变版本化 npm 多实例会破,**故保持源码 alias**。)
2. **命名空间 id**:`terminal.network`/`pro.ai-providers` —— 跨 repo 不撞。`definePortalSection` 幂等(同 id 忽略),HMR/重复 import 安全。
3. **enabled() 能力门控**:section 可按运行时能力自门(如 `terminal.network` 仅当 `/api/tunnel` 可用)。→ pro 纳入 terminal 段不会强显不适用项;未来 host 纳入任意 repo 都安全。
4. **host 级可选过滤(扩展点,先不建)**:`getPortalSections(slot, { filter })` 给 host 最终裁决权(对称现有 `NavigationSidebar.portalFilter`)。当前不需要 → 不建,留接口语义。

## 13. P4 终局结构(取代 §6/§7/§9 的分阶段)

```
@ce (deepwork, public):
  framework/portal/sections.ts            ← registry(§11 API)
  components/PortalSectionHost.vue         ← 共享壳:props{slot}; desktop-rail + mobile-tabs;
                                              渲染 getPortalSections(slot) 的 lazy <component>
  portals/settings/index.ts                ← definePortal{id:'settings',route:'/portal/settings',
                                              → PortalSectionHost slot='settings'}  (descriptor 上移,消除两仓同 route 分叉)
terminal (public):
  portals/settings/sections/{System,Shell,Network}Section.vue  ← 自包含(各自 API)
  portals/settings/sections/index.ts       ← definePortalSection ×3 (slot:'settings')
  contributions.ts                         ← import './portals/cli' + './portals/settings/sections'
  router:  import '@ce/portals/settings' + '@terminal/contributions'   (不再自注册 settings portal)
pro (private, worktree):
  portals/settings/sections/*              ← pro 段 definePortalSection
  router:  import '@ce/portals/settings' + pro sections + '@terminal/portals/settings/sections'
                                            (删 pro 自有 SettingsV2 portal 注册)
```

**消除的冗余/分叉**:两仓各自的 settings portal descriptor、各自的 settings 壳、各自硬编码 category 数组 —— 全部收敛为 @ce 一份壳 + 各 owner 一份 section。`terminal.network` 全局唯一。

## 14. P4 执行顺序(单管线,逐步可验)

1. **@ce**:`sections.ts` + `PortalSectionHost.vue` + `portals/settings/index.ts`(definePortal → host)。
2. **terminal**:抽 3 段为自包含 `.vue`(从 `SettingsFormView` 搬运,各带自己 API);`sections/index.ts` 注册;`contributions.ts`;router 改 import `@ce/portals/settings` + contributions;删 terminal 旧 settings portal descriptor + SettingsFormView/CategoryRail 壳。build standalone 验收三段。
3. **pro(worktree)**:pro 段改 `definePortalSection`;router import `@ce/portals/settings` + pro sections + `@terminal/portals/settings/sections`;删 pro SettingsV2 portal 注册。build 验收 pro settings = pro段 + Network段、可起隧道。
4. **三仓 build 全绿 + 双端 dw-browser 验收**(terminal:8087;pro:8088)。

## 15. 反对意见预答(不妥协)

- "@ce 不该拥有具体 settings portal":settings-as-section-host 是**框架级组合点**(谁都能贡献的设置页),非业务;业务 section 仍在 owner。@ce 拥有"空壳 + slot"是正确归属。
- "副作用 import 隐式":host 的 router 显式 import contributions = 组合根**显式可见**;比中心 manifest 更易读、可 tree-shake。
- "slot 泛化过度":一个 `Map<string,_>` + 一个 `slot` 字段承载多 portal/region,**零额外结构**;不用就退化为 `slot='settings'`。适度。
