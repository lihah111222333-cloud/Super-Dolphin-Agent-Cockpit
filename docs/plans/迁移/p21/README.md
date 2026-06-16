# P21 架构演进与能力补齐总览

基于 Hermes Agent 源码审查，为 `super-agent-v3` 补齐能力差距的整体路线图。
本轮为第 2 轮文档修订版；口径已按主 agent BLOCK 结论、`§10.27` 默认值安全原则、`§10.30` fx / bus / run.Group 三层分工和当前仓库事实重新收口。

## 当前基线约束

- 宿主 UI / 管理 API 仍优先落在 `internal/module/*`，并通过 `rpc.HandlerMapResult` 暴露；agent-visible tool、DAG、workspace、长期编排默认仍属于 `cmd/mcp-orch` 的 MCP 工具执行面。
- 持久化调度类能力默认落在 core；若未来要让模型直接操作，再由 `cmd/mcp-orch` 追加 agent-visible tool 包装。
- `internal/mcpserver/common/bootstrap/*` 仍是当前合法的工具侧 lifecycle client；禁止把业务能力继续堆到 common 旁路。
- 关系型持久化统一走 `migrations/` + `sql/queries/` + sqlc。新增表/查询只维护**实际消费侧**的 `sqlc.yaml`：Cron v1 与 Session Insights v1 都是 **core-only**，因此只改根 `sqlc.yaml`，不改 `internal/sidecar/orch/sqlc.yaml`。
- 内容型数据仍允许文件持久化例外；不要为了“统一”把 `SKILL.md` 等内容强塞回 Postgres。
- `fx.Module` / `BusModule` / `RunnerModule` 必须按三层分工落地：`fx.Module` 只做 constructor + 资源 open/close；`fx.Invoke` 只把订阅器注入 `bus.subscribers`；所有长跑 worker 都实现 `Runner.Run(ctx)` 并进入 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`）。长循环、drain、重试都不放进 `fx` 生命周期。
- core Fx 与 `cmd/mcp-orch` Fx 是**双树同构**：两边各有一根 bus / run.Group；平台库可共享（首选 `internal/module/<domain>/shared/*` 或 `internal/platform/*` 白名单内包），业务 module 分别落在 `internal/module/*` 与 `cmd/mcp-orch/*`。共享库位置与业务 module 归属不是互斥选择。**archtest 白名单是枚举式**：`internal/archtest/dependency_direction_mcp_orch_test.go:23-29` 放行 `internal/platform/{config,db,bus,runner,rpc,runtimesafe,shared,statemachine,eventsurface,rlimit}` 十个子包，**不是前缀通配**；新建顶层 `internal/platform/<X>` 需同步改护栏，否则将 orch 层共享代码放在 `internal/module/<X>/shared/*` 以命中 `internal/module` 白名单。
- Provider 自定义配置优先复用 `thread/start` 已有 `config` 透传链路；实例 identity 只能由专用 key 承载，且必须持久化到恢复链路，不允许再靠 top-level `modelProvider` 或 legacy home 推断。

## 默认值安全原则

- identity / cwd / 信任域 / 权限相关参数缺失时，默认行为必须是 sentinel error + fail fast，不做 silent fallback。
- `codexHome` / `codexInstanceKey` / `codexModelProvider`、Cron `cwd`、`notify_channel` 这类隔离关键字段不进入默认选择链；legacy fallback 只能通过显式 feature flag / env opt-in 打开。
- service 层负责第一时间返回 `Err<Name>Required` 或 `ErrMissingCWD`；RPC 层再统一映射 `jrpc2.InvalidParams`。

## core ↔ orch 事件链路的真实入口

- **core terminal turn 到 orch 的全部通道是 hook consumer**，不是跨 Fx 桥接 core bus。实际入口链：`cmd/mcp-orch/runtime.go:216-219` 的 `subscribeOrchestrationHooks` → `cmd/mcp-orch/hook_subscription.go:13-40` 订 `agent.turn.after / failed / progress` hook → `internal/sidecar/orch/orchestration/hook_consumer.go:96-220` 分流处理 `TurnCompleted` 与 `ItemCompleted(final_answer)`。
- orch 侧 P2 / P1b / 任何需要观察 core terminal turn 的业务模块，必须在 hook consumer 处理链上装 tap，而不是向 `cmd/mcp-orch` 本地 bus 上寻找重发后的 core 事件——该重发流在该返回 0 命中。
- orch 本地 bus（`internal/sidecar/orch/orchestration/events.go`）只承载 orch 自己产生的事件（DAG / task / wakeup），不双向桥接 core。

## 阶段 0：前置冻结（已落地）

阶段 0 的共享契约已经冻结，后续 track 必须复用，不允许绕过：

1. **migration 编号校准**：当前实际 skill candidate migration 为 `0064_skill_candidates.sql`；本轮后续预占：
   - `0065_skill_candidates_promotion.sql`（promotion retry 字段）
   - `0066_skill_candidates_repo_fp_widen.sql`（repo_fingerprint 列宽）
   - `0067_cron_jobs_approval_policy.sql`（cron approval_policy_json）
   - `0068_skill_candidates_audit_retention.sql`（审计保留）
2. **Canonical Turn Observation Contract 冻结**：observation 已拆分 `ObservationReader` / `ObservationWriter`；trajectory collector 只依赖 Reader，并由 `internal/archtest/trajectory_readonly_test.go` 阻断写方法回归。
3. **ResolveCodexIdentity 冻结**：`internal/provider/shared/codex_identity.go` 的 `ResolveCodexIdentity(config) (CodexIdentity, error)` 是 Phase 0 共享契约。T-00 之后任何破坏 input keys、canonicalization、sentinel errors 或 output fields 的修改必须先走 ADR。

## 实施路线图

| 优先级 | 特性 | 描述 | 当前状态 |
|---|---|---|---|
| **[P0](P0_SelfLearningSkill.md)** | 自学习 Skill 闭环 | P0a 先交付 host-side create；P0b 负责共享 observation 层与自动提炼闭环 | ⏳ observation owner |
| **[P1a](P1a_MultiProviderCodex.md)** | 多 Provider Codex 实例 | 以 `codexHome/codexInstanceKey/codexModelProvider` 作为实例 identity，并落到 binding 恢复面 | ✅ 已实现 |
| **[P1b](P1b_CronScheduledTasks.md)** | Cron 定时任务 | DDL / RPC / 状态机 / 事件桥 / TurnServiceAdapter (T-12) 均已落地；剩 approval_policy allow-list (T-13) | ⚠️ ~98% |
| **[P1b-UI](P1b_CronUI.md)** | Cron 定时任务前端 | 任务页子 tab 下的列表 / 表单 / 详情 / wails 事件订阅 / 立即触发均已落地；复用 8 个 `cronjob/*` RPC 和 `cron/job/runStateChanged` 事件桥 | ✅ 已实现 |
| **[P2](P2_MultiPlatformNotifications.md)** | 多平台通知 | webhook 平台适配器与 SSRF 防护已落；redirect 重校验已补 | ⚠️ ~40%+ |
| **[P3](P3_SessionInsights.md)** | Session Insights 遥测 | store/flusher/dashboard insights route 已落地 | ⚠️ ~70% |

## 依赖拓扑

```
阶段 0：migration 编号校准 ─┬─► P1b (0045_cron_jobs)
                           └─► P3  (0046_session_insights)

阶段 0：ResolveCodexIdentity ─┬─► P1a binding + pool
                              └─► P1b cronjob/create.config 校验

阶段 0：Observation Contract ─┬─► P0b extractor / candidate
                              └─► P3  collector (严格消费，不再自建 turn 归因)

P0a  ────► 独立（最小风险）
P1a  ────► P1b 的 `provider=codex` 白名单依赖 identity 冻结
P1b  ────► P2 的 Cron 触发源（job.notify_channel）
P0b  ────► P3 collector 与 skill_candidate 审批
```

强依赖遵循上表；不在上表内的 track 可以并行。

## 落地顺序建议

1. 先做 `P0a`：补 host-side create / scope-safe 写入，把 project-scope 自学习入口钉死到 `CreateSkill` / `WriteLocal(..., scope=project)`。
2. `P1a` 与 `P1b` 可以并行设计，但 `P1a` 必须先冻结 session identity 三元组与 binding 持久化口径，否则 Cron 的 provider 选择与恢复链会失焦。
3. `P0b` 必须作为前置交付先把 observation 层落地；`P3` 只消费这层输出，不能并行发明第二套 turn 归因规则。
4. `P1b` / `P2` / `P3` 都必须遵守 `fx` / `bus` / `Runner` 三层分工：bus callback 只做 state merge / enqueue，长跑 loop / lease / observe / flush worker 等长跑部件一律进 `runner.actors`（historical role naming；active Fx tag: `group:"runners"`）。
5. signed skill 验签**延后到 P22**；P21 只定义自学习写入、observation 与 insights 契约，不在本期追加 verifier。

## 收口口径

### Canonical Turn Observation Contract

Canonical Turn Observation Contract：共享 observation 层统一产出 local turn id ↔ provider turn id、`call_id -> turn_id`、`skills_selected`、token snapshot、terminal precedence 与 raw/typed 去重事实；P0b 是 owner，P3 只消费这层输出。

- 必须维护 `local turn id ↔ provider turn id` 映射表，供 turn 终态、tool 事件与 provider raw event 对齐。
- 必须维护 `call_id -> turn_id` 映射；`internal/dto/tool/event.go:46-55` 的 `ToolDiffUpdated` 只有 `ThreadID/AgentID/CallID`，**没有 `turn_id`**，归因不能跳过这张表。
- `skills_selected` 只表示 resolver 在 `PrepareTurn` 选中并准备注入的 skill 集合，**不等于模型实际使用**。
- token snapshot 要做归一：保留旧的非零 token 计数，不被 zero-event 覆盖；Claude path 的 `UITokensUpdated` 经 `internal/provider/unified/ui_tokens.go:58-75` 固定 `Projection="thread"`，且可能不带 `turn_id`，不能直接当 per-turn 权威值。
- terminal precedence 必须固定：`interrupted/aborted` 一旦成立，不能被 late `completed` 覆盖；`internal/dto/turn/event.go:11-21` 的 `TurnCompleted.Success` 是非指针 `bool`，缺字段时有默认 true 陷阱。
- `dto.BusRawProviderEvent` 与 typed event 必须在 observation 层统一去重，只允许按 `call_id`、raw event id 或等价 key 合并一次；collector / trajectory 不得 raw + typed 双算。
- observation 层为 P0b 前置交付；P3 作为 consumer 依赖这层事实，不再自建第二套 turn 归因逻辑。

- `P1a` 与 `P1b` 必须共享 session identity config keys：`codexHome/codexInstanceKey/codexModelProvider` 共同构成 codex instance identity；`codexHome` 解析必须复用 P1a 的 canonicalize 流水线（先做 home/env 展开，再 `filepath.Clean` + `filepath.EvalSymlinks`），并以 canonicalized realpath 持久化到 binding；本期选择 **binding 持久化为主**，不把 resume 扩成 generic config 透传。
- `provider` 字段语义仍冻结为 `codex|claude`；实例差异只走 identity 三元组，不把 `provider` 扩成 `codex:qwen` / `codex-glm` 这种混合值。
- `approvalPolicy=never` 不是“全部自动通过”。`internal/platform/rpc/approval_support.go:204-209` 当前只会自动放行 `request_user_input`；后台任务与多 provider 实例都必须白名单 provider / sandbox / tool 组合。
- P0 的 system-scope 自学习写入必须走显式 review gate：未获批不得写；审批/缓存键必须至少带 `skill slug + content hash + repo fingerprint(project_root/cwd)`，防止跨项目复用旧审批。
- signed skill 验签在 P22 前都只能视为“待验证态”；P21 不允许因为 frontmatter 写了 `trust: signed` 就跳过审批、脱敏或人工 review。
- `P2` 的 alias 解析不能落入默认链：`NOTIFY_DEFAULT_CHANNEL` 只允许显式 opt-in，默认缺失时应 `drop/error`，不做 silent fallback。
- 文档若未明确 UI 交付，默认按 API-only 解释；若未来要做 UI，必须显式补 `DashboardPage`、`ui/dashboard/get?page=...` 与前端导航 / 页面接线。**P1b 的 UI 交付已显式拆出为 [P1b-UI](P1b_CronUI.md)**：定时任务不独立一栏，作为 `任务` 页的子 tab（`tasksSubTab='cron'`）渲染 `pages/CronPanel.js`；并补一条 wails 事件 `cron/job/runStateChanged`，复用 `internal/ui/wails/bridge.EventBridge.publish` 透传。

## 修复清单

- [fix01.md](./fix01.md)
- [fix01.v2.md](./fix01.v2.md)
