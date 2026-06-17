# WS7 — PWA + Web Push 通知 + 平台感知安装引导

> 前端实现说明。后端按固定契约独立实现（VAPID + push 存储/发送）。

## 1. PWA shell

选型：**手写最小 `sw.js` + 静态 `manifest.webmanifest`（放 `public/`）+ 手动注册**，
不引入 `vite-plugin-pwa`/workbox。理由（Linus）：本工程是实时终端门户，离线缓存 shell 是
反模式且有风险；SW 唯一职责是收 push + 路由点击。零新构建依赖，完全自有 SW。

- `frontend/public/manifest.webmanifest`：name="Deepwork Terminal"、`display:standalone`、
  `scope/start_url:/`、`theme/background:#0d0d0f`、192/512 + maskable-512 图标。
- `frontend/public/sw.js`：`install→skipWaiting`、`activate→clients.claim`；
  `push`→`showNotification({title,body,tag,icon,badge,data})`（tag 缺省 `dw-agent-${sessionId}`，
  `renotify:true`）；`notificationclick`→优先 focus 已开窗口（支持则 `navigate(data.url)`）否则
  `openWindow(data.url)`。非 JSON payload 降级为通用提醒，不丢弃。
- `frontend/public/` 图标由 `frontend/scripts/gen-pwa-icons.mjs` 生成（纯 Node zlib PNG 编码器，
  无 sharp/canvas 依赖）：amber `>_` shell mark on dark rounded tile，匹配 v6 范式 palette。
  品牌变更后重跑 `node scripts/gen-pwa-icons.mjs`。
- `index.html`：`<link rel=manifest>` + `theme-color` + `apple-mobile-web-app-capable` +
  `apple-mobile-web-app-status-bar-style` + `apple-mobile-web-app-title` + `apple-touch-icon` +
  修复了原先指向不存在的 `/favicon.ico`（改 `/favicon-32.png`）。
- 注册：`main.ts` 在 mount 后 `usePushNotifications().ensureRegistration()`（幂等、特性守卫、
  无 SW 平台静默 no-op）。
- 服务路径：Go `internal/spa/embed.go` 对 dist 内存在的精确文件按原路径服务，未命中回退
  `index.html`。故 `/sw.js`、`/manifest.webmanifest`、`/pwa-*.png` 均在 root scope 提供，
  SW `scope:'/'` 合法。**无需改后端。**

## 2. usePushNotifications API（唯一事实源，module 级单例）

```
supported        ComputedRef<boolean>     SW+PushManager+Notification 三者都在
permission       Ref<'default'|'granted'|'denied'|'unsupported'>
subscribed       Ref<boolean>             本地有订阅且已 POST 后端
isStandalone     ComputedRef<boolean>     display-mode:standalone | iOS navigator.standalone
platform         ComputedRef<PushPlatform>  ios-safari|ios-other|chromium|desktop-safari|desktop-firefox|other
canPromptInstall Ref<boolean>             已捕获 beforeinstallprompt（Chromium/Android）
installed        Ref<boolean>             本会话内 appinstalled 触发
ensureRegistration() → Promise<ServiceWorkerRegistration|null>
promptInstall()  → 'accepted'|'dismissed'|'unavailable'
subscribe(sessionId) → boolean  请求权限→subscribe(userVisibleOnly,appServerKey)→POST /push/subscribe
unsubscribe()    → boolean       本地 unsub + POST /push/unsubscribe（best-effort）
refresh()        → 重读 permission/standalone + 同步既有订阅
```

- `urlBase64ToUint8Array(base64url)`：VAPID 公钥 → `applicationServerKey`。
- `arrayBufferToBase64Url`：`sub.getKey('p256dh'|'auth')` → base64url（契约要求）。
- 鲁棒性：每个 `PushManager`/`Notification`/`serviceWorker` 访问都特性守卫；iOS 非
  standalone（无 `window.PushManager`）下 `supported=false`，UI 退化为安装引导而非崩溃。
- 平台判定：iPadOS 13+ 伪装 Mac → 用 `maxTouchPoints` 辨别；iOS 上 `CriOS/FxiOS/EdgiOS/OPiOS/GSA/
  DuckDuckGo` → `ios-other`（无法订阅）。

## 3. 前台兜底 useForegroundAgentNotify

`watch(tmux.state)`（deep）逐 pane 检测 `agentStatus` 边沿 `*→waiting`；仅在
`document.hidden` 且 `permission==='granted'` 时 `new Notification(...)`。
首帧只播种 baseline（不为「连上时已 waiting」的旧态补发）。dedupe：per-pane 记忆上次 status，
仅边沿触发；`tag=dw-agent-${sessionId}` 与后端 push 同 tag → OS 合并不双弹。pane 消失即遗忘
（重建可重新触发）。`onUnmounted` 停 watch。后端 push 负责无 tab 场景，本兜底负责「开着但失焦」。

## 4. 平台矩阵（InstallGuideSheet）

| 平台 / 状态 | 安装区 | 通知区 |
|---|---|---|
| 已 standalone | 隐藏 | 通知开关（开启/关闭 + permission 提示；denied/unsupported 禁用） |
| chromium（未装） | 步骤1「安装应用」→`promptInstall()`（有 beforeinstallprompt 才显示按钮） | 步骤2「开启通知」→`subscribe()` |
| ios-safari | 图文步骤 分享→添加到主屏→从主屏开启通知（含分享/加号 inline glyph） | 标注 iOS 16.4+ 仅 standalone 生效 |
| ios-other | 警示「请用 Safari 打开」+ 灰化步骤 | — |
| desktop-safari/firefox/other | 可选安装提示 | supported 则直接开启；否则提示去 Chrome/iOS-PWA |

样式：v6 范式（`#141416/#1c1c1f` 表面、amber `#f08a3c` accent、mono 大写小标签、clean steps）；
overlay 沿用 TmuxStatusSheet 习语（Teleport+scrim，mobile 底部 sheet / desktop 右上 popover），
点遮罩或 × 关闭。

## 5. 入口图标 + badge/nudge

- **主入口** `InstallNotifyIcon.vue`：顶部标题/标签区常驻、平台感知 glyph（未装=下载箭头，
  已 standalone=铃铛）。`showNudge`（amber 脉冲点）：未 denied 且未订阅时提示——未装恒提示，
  已装则 supported 时提示。
  - TerminalPage：放 `.terminal-header`（HUD 与 SetupWizardIcon 之间）。
  - CliTerminalSurface：`.terminal-body` 右上 `position:absolute`（父 CliTabBar 才有标题行）。
- **次入口** TmuxPaneBar header 小铃铛（语境化，trailing edge）：`bellNudge`（amber 点）同口径；
  `is-alert`（amber）当有 pane waiting 且未订阅；首次某 pane 进入 waiting 且未订阅时，铃铛旁
  弹一次性 inline 提示「有 agent 在等待 — 开启通知？」6s 自动消失（`hintShown` 保证每 tab 一次）。
- 两入口都 `@open/@open-notify` → 打开同一 `InstallGuideSheet`。

## 6. 挂载点

- `pages/TerminalPage.vue`：header 主图标 + TmuxPaneBar 铃铛 + `InstallGuideSheet` +
  `useForegroundAgentNotify`。
- `components/terminal-session/CliTerminalSurface.vue`：surface 右上主图标 + TmuxPaneBar 铃铛 +
  `InstallGuideSheet` + `useForegroundAgentNotify`。
- 既有布局（ResourceDrawer/TmuxQuickBar/TmuxPaneBar/Toolbar/TmuxStatusSheet）均不受影响并存。

## 7. 验证（编译级；live 由 Coordinator 在 8087 + 真机补）

- `npm run type-check`：我的文件 0 error；仅 11 个既有 error（`plugin.ts`/`portals/settings`/
  `../../deepwork/frontend`），与任务预期一致。
- `npm run build`：exit 0。dist 产出 `sw.js`（与源字节一致，push+notificationclick 齐全）、
  `manifest.webmanifest`（standalone、3 图标含 maskable）、全部图标；`index.html` 含 manifest/
  apple-touch/apple-mobile-web-app/theme-color。
- 三场景推理：iOS-safari-未装→图文步骤；chromium→beforeinstallprompt 安装+订阅；
  already-standalone→通知开关。
