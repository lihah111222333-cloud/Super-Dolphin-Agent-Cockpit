# V2 vs V3 对照验证报告

> 验证日期：2026-03-20

## 1. 总体覆盖度

约 58%（按本次验证清单逐项加权估算）。

一句话评价：V3 的骨架、Store repo 分层、基础 RPC/FX/run/stateless 已落地，但 Bus 事件面、Runner 事件派发、Provider 统一和 P3 目标状态机仍明显未对齐迁移计划。

## 2. Bus 事件对照

补充说明：`go-agent-v2/internal/bus/types.go` 只做了 `type AgentEvent = agentcore.Event` 别名。为完成“V2 事件清单 vs V3 typed event”比对，本次补读了 `go-agent-v2/legacy-agentsdk/agentcore/types.go` 中的实际事件常量。

### V2 事件清单 vs V3 typed event 映射表

| V2 来源 | V2 topic / event family | V3 对应 | 结论 |
|---|---|---|---|
| `go-agent-v2/internal/bus/bus.go` | `dag.*` / `task.*` | `internal/dto/task/event.go` | 部分覆盖：DAG 创建、节点状态、wakeup 派发/完成已建模，但 V2 的 `dag.run_*`、`task.progress` 级别事件未见独立 typed event |
| `go-agent-v2/internal/bus/orchestration.go` | `BeginOrchestrationTaskState` / `UpdateOrchestrationTaskState` / `EndOrchestrationTaskState` | `internal/dto/task/event.go` + `internal/dto/workspace/event.go` | 部分覆盖：编排态拆入 task/workspace 事件，但 `binding_warning`、`reset`、snapshot 无直接事件模型 |
| `go-agent-v2/legacy-agentsdk/agentcore/types.go` | `EventTurnStarted` / `EventTurnComplete` / `EventTurnAborted` | `internal/dto/turn/event.go` | 部分覆盖：`TurnStarted`/`TurnCompleted` 存在；`TurnAborted` 仅近似映射到 `TurnInterrupted`，未见严格等价事件 |
| `go-agent-v2/legacy-agentsdk/agentcore/types.go` | `EventExecApprovalRequest` / `EventExecCommandBegin` / `EventExecCommandEnd` | `internal/dto/tool/event.go` | 部分覆盖：有 tool begin/end 与 approval request/resolved，但 orchestration 当前未实际发射这些事件 |
| `go-agent-v2/legacy-agentsdk/agentcore/types.go` | `EventAgentMessageDelta` / `EventReasoningDelta` / `EventPlanDelta` | `internal/dto/turn/event.go` 中 `TurnOutputDelta` | 部分覆盖：只看到通用 `TurnOutputDelta` DTO；reasoning/plan/message delta 没有细分 typed event，且当前未实际发布 |
| `go-agent-v2/legacy-agentsdk/agentcore/types.go` | `EventConnectionDead` / `EventStreamError` / `EventWarning` / `EventTokenCount` | 无 | 遗漏 |
| `go-agent-v2/internal/bus/bus.go` | `command_card.*` / `prompt.*` / `skill.*` / `lsp.*` | 无 | 遗漏 |
| `go-agent-v2/internal/bus/bus.go` | `approval.*` / `lock.*` / `heartbeat.*` / `budget.*` / `rollback.*` / `scheduler.*` | 无 | 遗漏 |
| `go-agent-v2/legacy-agentsdk/agentcore/types.go` | `EventPatchApply*` / `EventFileRead` / `EventFileUpdated` / `EventThreadNameUpdated` / `EventContextCompacted` | 无 | 遗漏 |
| V3 新增 | `internal/dto/ui/event.go` UI projection 事件 | V2 总线中无 typed 对应 | V3 新增能力，不构成回归 |

### 覆盖项

- V3 `internal/platform/bus/bus.go` 已覆盖 `Publish` / `Subscribe` / 取消订阅（返回 cancel func）三项基础能力。
- V3 `internal/platform/bus/subscription.go` 提供批量取消；`internal/platform/bus/router.go` 提供路由订阅生命周期包装。
- V3 事件定义已经收敛到 typed DTO：`agent` / `turn` / `tool` / `task` / `workspace` / `ui` 六类。
- V3 `internal/platform/bus/projection.go` 提供 typed projector 绑定方式，符合“总线 + 投影器”方向。

### 遗漏项

- V2 `AgentRouter` 的发现、委派、广播、AgentEvent 子路由在 V3 `internal/platform/bus/router.go` 中不存在；V3 Router 只管理订阅生命周期，不再承担跨 agent 路由。
- V2 `ResilientPublisher` 的 DB fallback、replay loop、health 状态、Publish panic 防护在 V3 未见等价实现。V3 仅有 `ResilientSubscribe`，而且只是订阅端 opt-in 的 handler panic 保护。
- V3 当前 DTO 事件面没有覆盖 V2 的 `command_card`、`prompt`、`skill`、`lsp`、`lock`、`heartbeat`、`budget`、`rollback`、`scheduler`、`token_count`、`context_compacted`、`thread_name_updated` 等事件族。
- `TurnStalled` 虽已定义在 `internal/dto/turn/event.go`，但 orchestration 层未见实际发布。

## 3. Runner/状态机对照

### V2 能力清单 vs V3 覆盖

| V2 能力 | V2 位置 | V3 对应 | 结论 |
|---|---|---|---|
| Launch | `manager_launch.go` | `internal/sidecar/orch/orchestration/service.go:LaunchAgent` | 部分覆盖：V3 只做 `command/env` 进程启动；V2 的 provider 解析、dynamic tools、resume session、transport fallback 未迁入 |
| Stop | `manager_lifecycle.go:Stop` | `StopAgent` / `StopAllAgents` | 基本覆盖：单 agent stop 存在；批量 stop 仅内部方法，不在 `Service` 契约中暴露 |
| Submit / deferred submit | `manager_submission.go` | `SubmitTurn` + `SubmissionQueue` | 部分覆盖：有排队，但没有 V2 的 metadata、queue position、active submission、progress 标记、replay 次数、dead client 同步恢复 |
| Recover | `manager_recover.go` | `Recover` | 部分覆盖：存在统一恢复入口，但无 V2 的 provider-aware resume 目标推导、submission replay 选项、early silence circuit breaker |
| Snapshot | `manager_lifecycle.go:Snapshot` | `Snapshot` | 部分覆盖：V3 返回单 agent snapshot；V2 还能保留运行时 client 快照用于 shutdown/kill 路径 |
| effectiveState 双真相消除 | `manager.go:effectiveState` | `service.agentRuntime.state` + `stateless` | 已覆盖：V3 全仓未见 `effectiveState` 命中，状态显示不再依赖第二套可变视图 |
| auto-recover（stall / connection dead / CLI dead） | `manager_recover.go` + `manager_event.go` | 无等价自动入口 | 遗漏 |
| provider registry | `provider_registry.go` | 无 | 遗漏：`internal/provider` 目录当前不存在，`LaunchRequest` 也无 provider 字段 |
| 事件归一化与派发 | `manager_event.go` | `events.go` | 遗漏：V2 处理大量 `agentcore.Event*` 并驱动状态；V3 只发布 `StateChanged` / `TurnStarted` / `TurnCompleted` |

### 覆盖项

- V3 已把状态机显式化：`internal/platform/statemachine/factory.go` + `github.com/qmuntal/stateless` 已接入。
- V3 `Snapshot` 能返回 `AllowedTriggers`，比 V2 的外部可见状态更适合直接做状态矩阵观测。
- V3 已把恢复入口收敛到 `internal/sidecar/orch/orchestration/recover.go`，方向符合 P3 “所有恢复入口统一经过 recovery”。

### 遗漏项

- `TriggerStall`、`TriggerMessageDelta`、`TriggerCommandBegin`、`TriggerCommandEnd` 在 `internal/dto/agent/state.go` 中定义了，但 orchestration 层没有触发点。
- `AgentLaunched`、`AgentStopped`、`AgentRecovering`、`AgentFailed` 在 `internal/dto/agent/event.go` 中定义了，但 orchestration 层未见发布代码。
- `TurnInterrupted`、`TurnResumed`、`TurnStalled`、`TurnOutputDelta`、`ToolCallBegin`、`ToolCallEnd`、`UIProjectionUpdated` 等 DTO 当前未见 orchestration 发布。
- V2 `SetOnEvent` 允许外部订阅统一 runtime 事件流；V3 `Service` 契约没有等价外部事件派发接口。

### 偏差项（实现与规划不一致）

- 迁移文档 P3 目标状态应为 `Provisioning / Idle / TurnQueued / TurnStarting / TurnRunning / AwaitingUserInput / Recovering / Stopping / Stopped / Failed`；当前 V3 仍是 `idle / thinking / running / paused / stopped / error` 六态，明显偏向 V2 粗粒度状态，而非 P3 终态。
- `SubmissionQueue` 在 V3 已存在，但只实现了 FIFO 容器，没有把 “queued firing tests / deferred submit / replay active submission / stall monitor” 一并迁入。
- `runner_actor.go` 当前 `processTurnQueues` 直接把 turn 标记开始并立即完成，没有 V2 manager_event 里的真实事件驱动链路。

## 4. Store 层对照

### 抽查结果

| V2 Store | V2 公开方法 | V3 对应 | 结论 |
|---|---|---|---|
| `agent_thread.go` | `FindByPort` / `ListRunning` / `ListRunningFull` / `ListRecoverable` / `Delete` / `Upsert` / `UpdateStatus` / `ResetRunningToCreated` / `ExpireStaleAgents` / `ExistsRunning` / `ListCwdMap` / `ListCwdMapByCwd` | `thread.Store` | 部分覆盖：绝大多数已迁；`Delete` 缺失；`ListRunningFull` 被 `ListRunning` 吸收；`ListCwdMap*` 改为返回切片而非 map |
| `task_dag.go` + `task_dag_phase1.go` | DAG + Node + Wakeup + Lease + `WithDAGTx` + `*Tx` 方法 | `taskdag.Store` + `internal/platform/db/tx.go` | 部分覆盖：大部分业务方法已迁；`WithDAGTx` 和组合式 `GetDAGDetailForUpdateTx` 未保留在 Store 接口层 |
| `workspace_run.go` | `SaveRun` / `GetRun` / `ListRuns` / `UpdateRunStatus` / `TryTransitionRunStatus` / `SaveFile` / `GetFile` / `ListFiles` | `workspace.Store` | 已覆盖：仅做 Save→Upsert、TryTransition→Transition 命名调整 |
| `agent_provider_binding.go` | `Upsert` / `DeleteByAgentID` / `UpdateSessionUUID` / `FindByAgentID` | `binding.Store` | 部分覆盖：查询/更新接口齐全，但 V2 的 provider-thread 唯一约束冲突幂等处理未保留 |

### 覆盖项

- V3 `internal/store/*/store.go` 已是薄 repo 封装，业务条件基本下沉到 `internal/store/sqlc/query_*.go`。
- 线程存储的 upsert / running / recoverable / stale expire / cwd prefix 查询已覆盖。
- DAG/wakeup/worker lease 的 `FOR UPDATE`、`SKIP LOCKED`、lease fencing、状态条件更新在 V3 `query_task_dag*.go` 中基本存在。
- Workspace run/file 的 upsert、条件状态迁移、列表过滤已覆盖。
- 事务 helper 仍存在于 `internal/platform/db/tx.go`，`BEGIN/ROLLBACK/COMMIT` 语义未丢失。

### 遗漏项

- `thread.Store` 未保留 `Delete`，若仍需要物理删除线程记录，则存在接口回退。
- `taskdag.Store` 未暴露 V2 的 `WithDAGTx` / `GetDAGDetailForUpdateTx` 一站式事务装配能力；调用方需要自行组合 `db.WithTx` 与 repo。
- `binding.Store` 当前只做直接 `UpsertAgentProviderBinding`，未保留 V2 在 `(provider, provider_thread_id)` 唯一约束冲突时“同一逻辑绑定视为成功”的幂等修正逻辑。

## 5. 迁移文档符合性

### Done 标准达成情况

| 项目 | 结论 | 说明 |
|---|---|---|
| §2.8 `go-agent-v2/internal/runner -> internal/sidecar/orch/orchestration + internal/platform/statemachine + internal/platform/runner` | 部分达成 | 目录存在，基本骨架存在，但能力明显弱于 V2，且未达到 P3 目标状态机 |
| §2.8 `go-agent-v2/internal/store -> internal/platform/db + internal/store/sqlc + internal/store/*` | 基本达成 | 路径存在，repo + sqlc 风格查询已落地 |
| §2.8 `go-agent-v2/internal/bus -> internal/platform/bus` | 部分达成 | typed DTO 方向成立，但当前实现是自研 reflect bus，不是 P2 计划中的 `kelindar/event` |
| §2.8 `go-agent-v2/internal/apiserver -> internal/platform/rpc + internal/module/* + internal/provider/* + internal/ui/* + internal/app` | 部分达成 | `rpc/module/app` 存在，`internal/provider` / `internal/ui` 当前不存在 |
| §2.8 `go-agent-v2/pkg/toolsdk -> internal/tool/* + internal/mcpserver/* + internal/module/...` | 部分达成 | `internal/mcpserver/common` 与 `cmd/mcp-*` 存在，`internal/tool` 当前不存在 |
| P1 Done：V3 中不存在手写 SQL builder 式 Store | 基本达成 | `internal/store/*` 中未见 `NewQueryBuilder` 命中，repo 已是薄封装；但 `internal/store/sqlc/*` 是否由生成器落盘，本次未从文件头看到生成标记 |
| P2 Done：总线内部没有业务 `map[string]any` payload | 基本达成 | `internal/platform/bus/*` 未见 `map[string]any` / `json.RawMessage` 业务载荷；但事件面覆盖不足且未使用 `kelindar/event` |
| P3 Done：没有 `effectiveState` 并列可变状态字段 | 达成 | 全仓未见 `effectiveState` 命中，V3 仅保留单一 `state` 字段 |

### 偏差项

- 6 个框架中，`fx`、`run`、`stateless`、`jrpc2` 已实际使用；`kelindar/event` 在代码中未见导入命中；`sqlc` 以 `internal/store/sqlc` 风格 API 形式存在，但本次未直接看到生成标记。
- P2 文档要求使用 `github.com/kelindar/event`；当前 `internal/platform/bus/bus.go` 仍是自研 reflect 总线。
- P3 文档要求显式状态 `TurnQueued / TurnStarting / TurnRunning / Recovering / Stopping / Failed` 等；当前 V3 状态表未达到该粒度。
- P4 路径规划中的 `internal/provider/*` 尚未出现；Provider 统一层未落地。
- `internal/ui/*` 与 `internal/tool/*` 尚未出现，说明 §2.8 的多处归宿仍停留在部分骨架阶段。

## 6. 能力偏差总表

| V2 能力 | V3 状态 | 偏差类型 | 优先级 |
|---|---|---|---|
| Bus DB fallback + replay (`ResilientPublisher`) | 遗漏 | 功能/架构 | P1 |
| AgentRouter 发现/广播/委派/AgentEvent 子路由 | 遗漏 | 功能 | P1 |
| `command_card` / `prompt` / `skill` / `lsp` / `lock` / `heartbeat` / `budget` / `rollback` / `scheduler` 事件族 | 遗漏 | 功能 | P1 |
| Runner auto-recover（stall / connection dead / CLI dead） | 遗漏 | 功能 | P0 |
| Provider registry / 多 provider 切换 | 遗漏 | 架构 | P0 |
| manager_event 事件归一化与状态驱动 | 遗漏 | 功能 | P0 |
| P3 目标细粒度状态机 | 部分覆盖 | 架构 | P1 |
| deferred submit 全语义（metadata / replay / progress / queue position） | 部分覆盖 | 功能 | P1 |
| `effectiveState` 双真相消除 | 已覆盖 | 架构 | P2 |
| task_dag 事务装配 API (`WithDAGTx`) | 部分覆盖 | 架构 | P1 |
| binding 唯一约束冲突幂等处理 | 遗漏 | 功能 | P0 |
| Workspace run/file repo 能力 | 已覆盖 | 功能 | P2 |
| Thread repo 基础查询/状态迁移 | 已覆盖 | 功能 | P2 |
| P2 计划指定 `kelindar/event` 实现 | 遗漏 | 架构 | P1 |

## 7. 行动建议

1. 先补齐 P0/P1 级缺口：`binding.Store.Upsert` 的 provider-thread 幂等冲突处理、Runner auto-recover、Provider registry/统一层。
2. 把 orchestration 的事件发射补到 DTO 面：至少补 `AgentLaunched`、`AgentStopped`、`AgentRecovering`、`AgentFailed`、`TurnInterrupted`、`TurnResumed`、`TurnStalled`、`TurnOutputDelta`、`ToolCallBegin`、`ToolCallEnd`。
3. 决定 P2 是否坚持文档方案：若坚持，替换当前 reflect bus 为 `kelindar/event`；若不坚持，需同步修订 `v3-migration-plan.md` 的 P2 框架与验收口径。
4. 将 P3 状态从当前 6 态升级到文档目标态，明确 `queued / starting / running / awaiting input / recovering / stopping / failed`，并让 `runner_actor` 真正消费进程/turn 事件而不是“立即完成”。
5. 在 `taskdag` 层恢复组合式事务 API，至少提供基于 `db.WithTx` 的 store 级 helper，避免调用方手工拼装锁定顺序。
6. 若 `internal/store/sqlc/*` 确为生成代码，建议补充生成标记或生成入口文档；否则应明确这是手写查询层，避免与 P1 文档表述冲突。

---

## 8. 修复后复查（2026-03-20 第二轮）

### 编译与守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过

### 修复项验证

| # | 修复 | 状态 | 备注 |
|---|---|---|---|
| 1 | 封装 | 已覆盖 | [runner_actor.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/runner_actor.go) 中 `stopAll` 仅调用 `service.StopAllAgents()`；未发现 `service.mu` 直接访问 |
| 2 | 事件发布 | 已覆盖 | [events.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/events.go) 已有 `publishAgentLaunched` / `publishAgentStopped` / `publishAgentRecovering` / `publishAgentFailed`；header 构造复用 `agentSessionHeader` / `turnHeader` / `agentHeader` |
| 3 | auto-recover | 部分覆盖 | [recover.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/recover.go) 有 `StallDetector`，且 [runner_actor.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/runner_actor.go) 在 ticker 中调用 `recoverStalledAgents`；仅覆盖 stall 检测，未见 connection-dead / CLI-dead 等其他 V2 自动恢复路径 |
| 4 | binding 幂等 | 已覆盖 | [binding/store.go](/Volumes/bot/super-agent-v3/internal/store/binding/store.go) `Upsert` 通过 [errors.go](/Volumes/bot/super-agent-v3/internal/platform/db/errors.go) `IsUniqueViolation` 检测唯一约束冲突，并在同 `provider + providerThreadID + agentID` 时视为成功 |
| 5 | taskdag 事务 | 已覆盖 | [taskdag/store.go](/Volumes/bot/super-agent-v3/internal/store/taskdag/store.go) 已提供 `WithTx`；[db.go](/Volumes/bot/super-agent-v3/internal/store/sqlc/db.go) 已提供 `NewWithTx` 和 `Queries.WithTx` |
| 6 | 去 stub | 已覆盖 | [runner_actor.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/runner_actor.go) `processTurnQueues` 不再立即完成 turn；[contract.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/contract.go) 已暴露 `CompleteTurn` |
| 7 | helpers 拆分 | 已覆盖 | [service.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/service.go) 现为 312 行，低于 380 行阈值；辅助逻辑已拆至 [helpers.go](/Volumes/bot/super-agent-v3/internal/sidecar/orch/orchestration/helpers.go) |

### 旧偏差项状态更新

| V2 能力 | 修复前状态 | 修复后状态 |
|---|---|---|
| auto-recover | 遗漏 | 部分覆盖 |
| binding 幂等 | 遗漏 | 已覆盖 |
| 事件归一化 | 遗漏 | 遗漏 |
| processTurnQueues stub | 遗漏 | 已覆盖 |
| taskdag WithTx | 部分覆盖 | 已覆盖 |
| Blocker 2 封装 | 未确认 | 已覆盖 |

补充变化：

- [state.go](/Volumes/bot/super-agent-v3/internal/dto/agent/state.go) 已切换到 `Provisioning / Idle / TurnQueued / TurnStarting / TurnRunning / AwaitingUserInput / Recovering / Stopping / Stopped / Failed`，P3 目标状态集现在与迁移文档一致。
- 事件发布面比上一轮更完整，但仍未达到 V2 `manager_event.go` 的原始事件归一化覆盖度。

### 覆盖度更新

修复前：58%

修复后：68%

说明：本次上调主要来自 7 项修复中 5 项达到“已覆盖”，1 项达到“部分覆盖”，且 P3 目标状态集已对齐迁移文档；但 Bus P2 实现、Provider 统一层和完整 runtime 事件归一化仍未闭合，因此未上调到 70% 以上。

### 剩余缺口（P4+ 范围）

严格复查后，剩余缺口不只 P4+，仍有未闭合的 P2/P3 项：

- P2 残留：`internal/platform/bus` 仍是自研 reflect bus，未切换到文档规划的 `github.com/kelindar/event`
- P2 残留：V2 `ResilientPublisher` 的 DB fallback / replay、`AgentRouter` 的广播/委派/发现能力、以及 `command_card` / `prompt` / `skill` / `lsp` / `lock` / `heartbeat` / `budget` / `rollback` / `scheduler` 等事件族仍未迁入 V3 typed event 面
- P3 残留：runtime 事件归一化仍缺失；当前新增的是发布函数，不是 V2 `manager_event.go` 等价的事件驱动归一化链路
- P3 残留：auto-recover 仅覆盖 stall 检测；未见 V2 的 connection-dead / queued-submit-dead / submit-cli-dead 等恢复矩阵
- P3 残留：deferred submit 仍只覆盖基础排队；metadata、queue position、submission replay、progress 标记等 V2 语义仍未完全迁移
- P4 残留：`internal/provider/*` 仍未落地，多 provider 统一层和 registry 仍缺
- P4/P5 残留：`internal/ui/*`、`internal/tool/*` 仍未出现，`pkg/toolsdk` / UI 归宿尚未完成

---

## 8. 修复后复查（2026-03-20）

### 编译与守卫

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过

### 逐条对照结果

| # | 对照项 | V2 参考 | V3 文件 | 状态 | 备注 |
|---|---|---|---|---|---|
| A | auto-recover | `go-agent-v2/internal/runner/manager_recover.go` | `internal/sidecar/orch/orchestration/recover.go`, `internal/sidecar/orch/orchestration/runner_actor.go` | ✅ | V3 已有 `StallDetector`，且 `runner_actor.Run` 的 ticker 分支调用 `recoverStalledAgents`；但仍仅覆盖 stall 检测，不等于 V2 全量 recover matrix |
| B | 事件发布 | `go-agent-v2/internal/runner/manager_event.go` | `internal/sidecar/orch/orchestration/events.go` | ✅ | 已发布 `publishAgentLaunched` / `publishAgentStopped` / `publishAgentRecovering` / `publishAgentFailed`；header 通过 `agentSessionHeader` / `turnHeader` / `agentHeader` 工厂复用 |
| C | binding 幂等 | `go-agent-v2/internal/store/agent_provider_binding.go` | `internal/store/binding/store.go`, `internal/platform/db/errors.go` | ✅ | V3 `Upsert` 已在唯一约束冲突后回查 `(provider, provider_thread_id)` 并在同 agent 时视为成功；`IsUniqueViolation` 已落地 |
| D | taskdag 事务 | `go-agent-v2/internal/store/task_dag_phase1.go` | `internal/store/taskdag/store.go`, `internal/store/sqlc/db.go` | ✅ | V2 `WithDAGTx` 的意图在 V3 由 `taskdag.Store.WithTx` + `sqlc.NewWithTx` / `Queries.WithTx` 承接 |
| E | turn 生命周期 | `go-agent-v2/internal/runner/manager_submission.go` | `internal/sidecar/orch/orchestration/runner_actor.go`, `internal/sidecar/orch/orchestration/contract.go` | ✅ | `processTurnQueues` 已去掉“立即完成” stub，只做 `startTurnExecution + publishTurnStarted`；`CompleteTurn` 已进入 `Service` 契约 |
| F | 状态表 | `go-agent-v2/internal/runner/manager.go` | `internal/dto/agent/state.go` | ✅ | V3 现有 10 个状态、11 个触发器；不再停留在 V2 `idle/thinking/running/stopped/error` 粗粒度状态 |

### 覆盖度更新

修复前：58%

修复后：68%

说明：本轮是基于 V2 源码的重新核对，不是新一轮功能提交。A-F 六项修复均已落地，但它们没有消除上一轮已识别的 Bus P2 缺口、Provider 统一层缺口与 runtime 事件归一化缺口，因此总体覆盖度维持 `68%`。

### 剩余缺口（仅 P4+ 范围）

按当前代码，剩余缺口无法收敛到“仅 P4+ 范围”，仍有未闭合的 P2/P3 项；因此这里先列真实剩余项：

- P2：`internal/platform/bus` 仍是自研 reflect bus，未切换到迁移文档指定的 `github.com/kelindar/event`
- P2：V2 `ResilientPublisher` 的 DB fallback / replay、`AgentRouter` 的广播/委派/发现能力、以及多组 bus 事件族仍未完成 typed event 迁移
- P3：虽然事件发布函数已补齐，但尚未形成 V2 `manager_event.go` 等价的 runtime 事件归一化与状态驱动链路
- P3：auto-recover 仅覆盖 stall 检测；未覆盖 connection-dead / queued-submit-dead / submit-cli-dead 等 V2 恢复入口
- P3：deferred submit 仍缺 metadata、queue position、submission replay、progress 标记等 V2 完整语义
- P4：`internal/provider/*` 尚未落地，多 provider 统一层与 registry 仍缺
- P4/P5：`internal/ui/*` 与 `internal/tool/*` 尚未落地，`pkg/toolsdk` / UI 归宿仍未完成
