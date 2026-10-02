# deepwork-terminal 主旅程 FMEA

基线：2026-10-02。范围覆盖 README 中描述的浏览器/移动端远程终端、tmux/daemon 会话、Agent 状态与通知、文件抽屉、额度/Agent 报表、服务重启与远程访问。

## 评分口径

- **S 严重度**：1=外观瑕疵，10=工作丢失、敏感数据暴露或关键功能不可恢复。
- **O 发生度**：1=罕见，10=常见。没有生产遥测时按代码路径与历史回归做工程估计。
- **D 难检度**：1=现有自动检查稳定发现，10=通常只有用户现场才能发现。
- **RPN = S×O×D**。S≥9 或 RPN≥200 先处理；之后按 RPN 从高到低。分值用于排序，不代表生产实测概率。

## 风险清单

| ID | 主旅程 / 多轮交互 | 失效模式及影响 | S/O/D → RPN | 当前控制 / 证据 | 优先动作 |
|---|---|---|---:|---|---|
| F1 | 发现并连接终端；多 Server 启停、监听失败后重试 | 多实例切换时 quota credential source 残留，测试触达真实凭证，或 last-close 留下 typed-nil source 导致查询 panic | 9/3/8 → **216** | source owner 栈、Close/listen 失败注销、测试 home guard、nil-safe kit interface、反序注销后立即查额度回归 | **MITIGATED**：旧实例先关闭、新实例仍持有 source；最后实例关闭清空无 typed-nil；隔离测试与真实 quota query 契约通过 |
| F2 | 建立 Claude 会话；内外层 shell、不同 cwd/profile 来回切换 | Agent 被绑定到错误 transcript，状态/运行目录/通知错误 | 8/4/8 → **256** | Linux 读取进程 environ；Darwin 尝试 KERN_PROCARGS2；环境不可见时禁止 PID 绑定并回退 cwd；嵌套 shell 用例 | **MITIGATED / LIMITATION**：防止 host profile 旧 PID 记录冒认；macOS 不暴露子进程 env 时无法验证私有 profile，保持 unknown |
| F3 | WebSocket 输入、输出、断线重连、窗口 resize、server restart | 会话被误销毁、重放缺口拼成假屏幕、或重连后输入不再生效 | 10/3/6 → **180** | muxd detach/restore、非连续 replay 清空、restart E2E、真实 PTY TUI 与 peer PID 用例 | **MITIGATED**：restart E2E、muxd 与真实 TUI 相关用例通过 |
| F4 | 文件抽屉搜索；失败后立即重试、等待恢复、换 query/目录 | 索引失败吞掉 last-good 结果，或持久故障每 30 秒触发昂贵重扫 | 7/5/7 → **245** | 失败保留旧快照、立即重试；构建态 500ms、错误态 5min、正常态 30s 的轮询策略及单测 | **MITIGATED**：策略单测与全仓 Go 测试通过；真实权限故障注入仍待做 |
| F5 | 上传大文件；中断续传、重复 chunk、完成、同名冲突 | 文件损坏/覆盖，或返回错误 relPath 让客户端在错误位置继续操作 | 8/3/6 → **144** | 分块长度与 hash 校验、原子落盘、cwd symlink 规范化；RoundTrip/Resume/retry 用例 | **MITIGATED / P2 residual**：定向分块套件覆盖续传与重复 complete；跨进程持续并发压测仍待补 |
| F11 | 分块上传 complete 成功但响应丢失，客户端按“合并失败”重试 | 相同文件落成原文件 + hash 副本；并发完成可能覆盖同名不同内容 | 7/5/6 → **210** | content hash + requested-name dedupe；same-dir atomic no-replace hard link；重复完成回归 | **MITIGATED**：响应丢失重试返回同一 relPath，且不同内容用独立名 |
| F6 | 查额度、切换 provider、连续刷新或多进程并行使用 Codex | 自定义 endpoint 被误报为 OpenAI/订阅付款方，导致错误消费决策 | 8/4/7 → **224** | config/auth/provider 与近期 rollout 合并判断；无法归属时 unknown；Codex default-provider 与混合 endpoint 回归用例 | **MITIGATED**：旧 rollout + custom default 会清除 OpenAI claim；已知/未知并发不声称唯一付款方；配置热切换压力测试仍待做 |
| F7 | 看 Agent 报表；筛选、翻页、提交“有用/需返工”、刷新 | 完成被当验收，反馈未持久化或缓存仍显示旧汇总 | 7/5/6 → **210** | OutcomeEvidence 有来源/oracle/time/ref/confidence；幂等 JSONL；报告只失效依赖 outcome 的缓存；前端模板/保存/分页刷新契约回归 | **MITIGATED / UI E2E OPEN**：reporter 与 detail API 已验证 completed guard、权限、幂等、多页 cursor 回读、汇总/证据一致；源码契约验证入口门槛、重复提交禁用、保存证据与刷新/错误提示；真实浏览器点击路径仍待验 |
| F8 | 远程访问 / CORS / 鉴权；旋转 auth code 后旧页面继续请求 | 旧 token 仍可用，或私网请求被浏览器静默拦截 | 9/2/5 → **90** | auth throttle、rotate、CORS/PNA 与跨 Origin 多轮 API 测试；本机没有可控浏览器 surface | **PARTIAL / P1 by severity gate**：preflight→旧 token→rotate→旧 token 拒绝→新 token 成功已通过；真实远端 browser reconnect 保持 OPEN |
| F9 | daemon socket 初始化、升级后旧 daemon 占用、短时文件路径 | AF_UNIX 路径过长返回含糊错误；普通文件被误当活 socket 拒绝恢复 | 7/4/6 → **168** | 测试 fixture 使用短私有 socket；ENOTSOCK 归为无 listener；Darwin peer PID 回归；按 OS sun_path 限制 bind 前校验 | **MITIGATED**：Darwin/Linux 长路径诊断、ENOTSOCK 回收和 peer PID 测试通过 |
| F10 | 通知等待→通知→用户回复→下一轮完成；多个 pane 并行 | 重复通知、必要权限与返工混为一类，或用户回复后 cooldown 吞掉下一轮 | 8/4/7 → **224** | transition/cooldown/显式 signal 共用测试；phase 和 evidence 分层；关闭后立即重开串行等待旧 poller 完成持久化 | **MITIGATED / PARTIAL**：双 tab 同批完成与独立 cooldown、关闭→快速重开有 race 回归；coordinator 多启用渠道并行发送及单渠道 panic 隔离已验证；真实外部渠道投递仍待验 |

## 分阶段验收与提交

1. **阶段 A — 会话/启动与平台稳定性**：F1、F2、F3、F9。重复创建、断开、关闭、重启、反序关闭 Server；Linux/macOS 真实 daemon 与 profile 场景。提交标题以 `test(session): ...` / `fix(session): ...` 清楚表达风险。
2. **阶段 B — 文件旅程**：F4、F5。搜索失败→立即重试→自动恢复；上传中断→resume→重复 chunk→complete→读取结果。提交标题以 `fix(files): ...`。
3. **阶段 C — Agent 价值与计费可信度**：F6、F7、F10。切换 provider、多 shell 刷新、反馈写入/重复提交、并行通知和用户回复。提交标题以 `fix(usage): ...` / `feat(agent-report): ...`。
4. **阶段 D — 远程连接验收**：F8。鉴权旋转、跨 Origin / 私网 browser 连接与恢复。若真实远端环境不可用，记明 L4-SKIP，不以 mock 冒充体验验收。

## 证据限制

当前 S/O/D 是代码审查和测试回归驱动的工程排序；没有生产失败率、远端 browser session 或用户遥测，因此不能声称是统计风险。每阶段完成时更新本表状态、测试命令和 commit SHA；未验证的远程体验保持 OPEN。

## 执行记录

| 阶段 | 范围 | 验收证据 | Commit |
|---|---|---|---|
| Baseline | 主旅程与 S/O/D 风险排序 | 代码/README/现有测试审阅；评分均标为工程估计 | `65488e3` |
| Phase A | F1–F7、F9 高风险处理与平台稳定性 | `go test ./... -count=1`、Go builds、`go vet ./...`、前端 type-check/build、search poll 单测、共享前端契约 | `e12ecb6` |
| Phase B | F4 扫描错误 last-good/恢复故障注入 | `go test . -run '^TestFilesSearch_FailedRefreshPreservesLastGoodAndRecovers$' -count=1` | `c1b7f2d` |
| Phase B2 | F11 complete 响应丢失后的幂等重试 | `go test . -run '^TestChunkUploadCompleteRetryAfterLostResponseReusesSameFile$' -count=1` | `569c53a` |
| Phase C | F7 人工 outcome 持久化闭环 | `go test . -run '^TestHumanOutcomeRoundTripRequiresCompletedWorkAndIsIdempotent$' -count=1` | `8a9ad32` |
| Phase C2 | F6 Codex custom default、旧 rollout 与混合 endpoint | `go test . -run '^TestReconcileCodexAttribution' -count=1` | `68929de` |
| Phase C3 | F7 outcome API 跨页反馈与汇总回读 | `go test . -run '^TestAgentOutcomeHTTPRoundTripKeepsPagedDetailAndSummaryConsistent$' -count=1` | `46ea018` |
| Phase E | F10 双 tab 并行完成与独立 cooldown | `go test . -run '^TestNotifierSessionSource_MultipleSessionsKeepCooldownIndependent$' -count=1` | `e422749` |
| Phase A2 | F1 多 Server credential source 反序关闭 | `go test . -run '^TestUsageCredentialSourcesRestoreNewestOwnerAfterOutOfOrderClose$' -count=1` | `cd3b741` |
| Phase A3 | F1 最后 source 注销后 quota query 不 panic | `go test . -run '^(TestUsageCredentialSourcesRestoreNewestOwnerAfterOutOfOrderClose|TestHandleUsageQuota)$' -count=1` | `a67b56c` |
| Phase A4 | F9 超长 AF_UNIX 路径 bind 前诊断 | `go test ./muxd -run '^(TestListenRejectsOverlongSocketPathWithActionableError|TestProtoListenReclaimsStaleSocket)$' -count=1` | `c1b3afb` |
| Phase F | F8 跨 Origin 鉴权旋转序列 | `go test . -run '^TestRemoteAuthJourney_RotateRevokesTheOldCode$' -count=1` | `2847cd9` |
| Phase E2 | F10 最后渠道关闭后立即重开，确认旧 poller 落盘并退出后才可启动新 poller | `go test . -run '^(TestNotifierRestartWaitsForPreviousPollerToPersist|TestEnsureNotifierWithoutTmux|TestNotifierSessionSource_MultipleSessionsKeepCooldownIndependent)$' -count=1 -timeout=2m`；新生命周期回归另经 `-race` 单测 | pending |
| Phase C4 | F7 前端 outcome 反馈入口、持久化调用、证据显示及分页刷新接线 | `cd frontend && bun test src/components/report/__tests__/agentOutcomeFeedback.test.ts src/components/report/__tests__/templateBindings.test.ts && bun run type-check` | pending |
| Phase E3 | F10 多个启用渠道并发 fan-out；单渠道 panic 不阻断其它渠道 | `go test ./notify -run '^(TestCoordinatorFanoutSkipsDisabled|TestCoordinatorFanoutDeliversToEveryEnabledProviderDespitePanic)$' -count=5 -race -timeout=2m` | pending |

### 全量验收快照

- 代码基线：`bafe213`（工作区无未提交改动）。
- `go test ./... -count=1 -timeout=2m`：PASS。
- `go build ./...`、`GOWORK=off go build ./...`、`go vet ./...`、`git diff --check`：PASS。
- 前端类型检查/正式构建、搜索轮询单测、共享前端契约：PASS。
- 没有可控浏览器 surface，F8 的真实远端 browser reconnect 仍记为 L4-SKIP/OPEN；API 序列测试没有冒充真实浏览器验收。
