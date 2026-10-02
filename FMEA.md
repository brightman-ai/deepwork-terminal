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
| F1 | 发现并连接终端；多 Server 启停、监听失败后重试 | 多实例切换时 quota credential source 残留，或测试启动触达真实用户凭证并发起请求 | 9/3/8 → **216** | source 有 owner 栈并在 Close/listen 失败注销；默认 go test 且未设隔离 home 时 warmer 不注册；有回归用例 | P1：验证重复 Close、反序关闭多实例与 Listen 失败恢复 |
| F2 | 建立 Claude 会话；内外层 shell、不同 cwd/profile 来回切换 | Agent 被绑定到错误 transcript，状态/运行目录/通知错误 | 8/4/8 → **256** | Linux 读取进程 environ；Darwin 读取 KERN_PROCARGS2；平台不暴露时回退，不伪称读取成功；嵌套 shell 用例 | P1：profile 不可见时明确验证保守回退，不让 host profile 的旧 PID 记录冒认 |
| F3 | WebSocket 输入、输出、断线重连、窗口 resize、server restart | 会话被误销毁、重放缺口拼成假屏幕、或重连后输入不再生效 | 10/3/6 → **180** | muxd detach/restore、非连续 replay 清空、restart E2E、真实 PTY TUI 与 peer PID 用例 | P1：把 reconnect→多次输入→再次断开→重启纳入循环验收 |
| F4 | 文件抽屉搜索；失败后立即重试、等待恢复、换 query/目录 | 索引失败吞掉 last-good 结果，或持久故障每 30 秒触发昂贵重扫 | 7/5/7 → **245** | 失败保留旧快照、立即重试；构建态 500ms、错误态 5min、正常态 30s 的轮询策略及单测 | P1：注入可恢复/持续失败目录，验旧结果、警告清除和扫描节奏 |
| F5 | 上传大文件；中断续传、重复 chunk、完成、同名冲突 | 文件损坏/覆盖，或返回错误 relPath 让客户端在错误位置继续操作 | 8/3/6 → **144** | 分块长度与 hash 校验、原子落盘、cwd symlink 规范化；ChunkUploadRoundTrip/Resume 用例 | P1：追加 symlink cwd + 重复 complete/abort 的多轮场景 |
| F6 | 查额度、切换 provider、连续刷新或多进程并行使用 Codex | 自定义 endpoint 被误报为 OpenAI/订阅付款方，导致错误消费决策 | 8/4/7 → **224** | config/auth/provider 与近期 rollout 合并判断；无法归属时 unknown；Codex default-provider 回归用例 | P1：覆盖配置热切换、并发 provider 与旧 rollout 的保守归属 |
| F7 | 看 Agent 报表；筛选、翻页、提交“有用/需返工”、刷新 | 完成被当验收，反馈未持久化或缓存仍显示旧汇总 | 7/5/6 → **210** | OutcomeEvidence 有来源/oracle/time/ref/confidence；幂等 JSONL；报告只失效依赖 outcome 的缓存 | P1：多页筛选反馈后验证汇总、证据详情与跨 shell 刷新一致 |
| F8 | 远程访问 / CORS / 鉴权；旋转 auth code 后旧页面继续请求 | 旧 token 仍可用，或私网请求被浏览器静默拦截 | 9/2/5 → **90** | auth throttle、rotate、CORS/PNA 测试；E2E 只测本机入口 | P2：远端 browser journey 验 rotate 后重连及多 Origin 失败文案 |
| F9 | daemon socket 初始化、升级后旧 daemon 占用、短时文件路径 | AF_UNIX 路径过长返回含糊错误；普通文件被误当活 socket 拒绝恢复 | 7/4/6 → **168** | 测试 fixture 使用短私有 socket；ENOTSOCK 归为无 listener；Darwin peer PID 回归 | P2：生产自定义长 socket 路径给出启动前可行动诊断 |
| F10 | 通知等待→通知→用户回复→下一轮完成；多个 pane 并行 | 重复通知、必要权限与返工混为一类，或用户回复后 cooldown 吞掉下一轮 | 8/4/7 → **224** | transition/cooldown/显式 signal 共用测试；phase 和 evidence 分层 | P2：多 pane 同时完成、关闭通知、回复后再完成的端到端序列 |

## 分阶段验收与提交

1. **阶段 A — 会话/启动与平台稳定性**：F1、F2、F3、F9。重复创建、断开、关闭、重启、反序关闭 Server；Linux/macOS 真实 daemon 与 profile 场景。提交标题以 `test(session): ...` / `fix(session): ...` 清楚表达风险。
2. **阶段 B — 文件旅程**：F4、F5。搜索失败→立即重试→自动恢复；上传中断→resume→重复 chunk→complete→读取结果。提交标题以 `fix(files): ...`。
3. **阶段 C — Agent 价值与计费可信度**：F6、F7、F10。切换 provider、多 shell 刷新、反馈写入/重复提交、并行通知和用户回复。提交标题以 `fix(usage): ...` / `feat(agent-report): ...`。
4. **阶段 D — 远程连接验收**：F8。鉴权旋转、跨 Origin / 私网 browser 连接与恢复。若真实远端环境不可用，记明 L4-SKIP，不以 mock 冒充体验验收。

## 证据限制

当前 S/O/D 是代码审查和测试回归驱动的工程排序；没有生产失败率、远端 browser session 或用户遥测，因此不能声称是统计风险。每阶段完成时更新本表状态、测试命令和 commit SHA；未验证的远程体验保持 OPEN。
