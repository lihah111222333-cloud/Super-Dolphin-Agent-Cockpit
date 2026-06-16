# V2↔V3 P1 核心功能缺失修复计划

> 日期：2026-03-25
> 来源：docs/plans/迁移/v2v3-recheck-final.md §4
> 总项数：30 项 P1 + 12 项 P2 = 42 项（其中 P1-23 已确认落地，仅保留验收核对）
> 估计工作量：45-63 人日
> 执行方式：按根因聚合，Codex Agent 并行修复 + 互审 + golden contract 守卫

---

## 0.1 P1 覆盖对照表

| P1 | 缺口 | Agent 任务 | 批次 | 备注 |
|----|------|-----------|------|------|
| P1-01 | `thread/start` 契约恢复 | **F5-1** | 第四批 | 恢复启动参数面、provider fallback、返回 envelope；依赖 P0 thread-start guard/binding 修复 |
| P1-02 | `thread/resume` 契约恢复 | **F5-2** | 第四批 | 恢复 request/response shape 与 resume 路径，依赖 B3/B4 |
| P1-03 | `thread/recover` 契约恢复 | **F5-3** | 第四批 | 恢复 `recovered/mode` 结果面与 replay effect，依赖 B6 |
| P1-04 | `thread/fork` 契约恢复 | **F5-4** | 第四批 | 恢复 `thread{id,forkedFrom}` 响应与 fork 元数据 |
| P1-05 | `thread/config/get` 字段/语义 | **D1** | 第二批 | 恢复 `effective/override` 与 `effort` 等字段 |
| P1-06 | `thread/messages` cursor/before 契约 | **D2-1** | 第二批 | 恢复 V2 `before`/分页入参语义 |
| P1-07 | `thread/messages` 响应 envelope | **D2-2** | 第二批 | 恢复 `{messages,total/...}` 结果形状 |
| P1-08 | `thread/messages` compaction/hydration | **D2-3** | 第二批 | 恢复 provider history + runtime hydration 语义 |
| P1-09 | `thread/messages` 离线历史 | **D2-4** + **A1** | 第二/三批 | D2-4 先修返回结构，离线读取验证依赖 A1 |
| P1-10 | `turn/interrupt` 返回 envelope | **D3** | 第二批 | 恢复确认型返回体，不再固定 `{ok:true}` |
| P1-11 | `TurnInterrupted` 终态闭环 | **B1** | 第一批 | 独立 `handleTurnInterruptedEvent`，不再合成 `TurnCompleted` |
| P1-12 | approval live replay | **A3** | 第三批 | reconnect 后重放 pending approvals |
| P1-13 | `StopAllAgents/archive/delete` 统一停机 | **B2** | 第一批 | 同步修复 `stopReason=""` 清空 bug |
| P1-14 | DAG 锁 / wakeup fencing | **B6** | 第一批 | 独立 Agent，覆盖 wakeup claim/bind/recover fencing |
| P1-15 | execute-time ready wait | **F7** | 第四批 | 仅保留 ready wait；WSHandler 接线不再列为开发项 |
| P1-16 | approval 阻塞等待态 | **B5** | 第一批 | 先人工定策略，再由 Agent 落地 |
| P1-17 | workspace dry-run 生命周期 | **F6-1** | 第四批 | 恢复 `active -> merging -> active` 门闩 |
| P1-18 | workspace merge 补偿 / `delete_removed` | **F6-2** | 第四批 | 恢复删除语义、失败补偿与事件一致性 |
| P1-19 | dashboard `agentStatus` / status filter | **F1** | 第四批 | 独立读模型，不再并入总览 DTO |
| P1-20 | dashboard 日志 / DAG 面 | **F2** | 第四批 | 恢复 `auditLogs/aiLogs/busLogs/dags/dagDetail` 能力 |
| P1-21 | preferences 合同 | **A2** | 第三批 | 恢复 richer preferences/sidebar 结果层与副作用 |
| P1-22 | uistate 事件投影 | **A4** | 第三批 | 恢复 turn/thread/tool/workspace/UI 投影链 |
| P1-23 | WSHandler 接线 | **已完成，无新增 Agent** | 验收项 | `internal/platform/rpc/module.go:68-78` 已接线，只保留验收核对 |
| P1-24 | codex 进程生命周期 | **B4** | 第一批 | 进程管理落点在 `internal/provider/codexapp/transport.go` |
| P1-25 | claude reconnect / reinitialize | **B3** | 第一批 | `restartIfNeededLocked` 落点在 `internal/provider/claudecli/session.go` |
| P1-26 | claude turn finish payload | **D4** | 第二批 | 恢复 V2 终态 payload 合同 |
| P1-27 | store-db `thread/read` DTO | **D5-1** | 第二批 | 恢复 thread/read DTO 形状 |
| P1-28 | store-db workspace DTO | **D5-2** | 第二批 | 恢复 workspace DTO / tool DTO 形状 |
| P1-29 | wails desktop API | **F3** | 第四批 | `ui/log`、`windowBootstrap`、LSP helper 等 API 面 |
| P1-30 | wails desktop 兼容绑定 / 事件 | **F4** | 第四批 | `ui/openNewWindow`、`agent-event`、`files-dropped`、`group`、`SelectProjectDirs` |

## 0.2 执行原则

1. **按根因聚合**，不逐项零散修复；每个 Agent 负责一个可独立验收的闭环。
2. **覆盖表先于编码**：新建/调整 Agent 任务前，必须先在本表补上对应的 P1 编号与依赖。
3. **每批修复后 1:N 互审 + archtest/golden 全绿**，方可进入下一批。
4. **Agent 拉起规范**：通过 `orchestration_launch_agent(provider="codex")`，初始 prompt 必含 `prompts/lsp-mandatory-prefix.md` 与 `prompts/lsp-advanced-guide.md`。
5. **V2 参考路径冻结**（统一按真实源码路径引用，不再使用旧错链）：
   - B1：`/Users/mima0000/Desktop/wj/go-agent-v2/internal/runner/manager_lifecycle.go` + `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/service/tracker/turn_tracker_rules_core.go`
   - B2：`/Users/mima0000/Desktop/wj/go-agent-v2/internal/runner/manager_lifecycle.go`
   - B3：`/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/claude/client.go`
   - B4：`/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/codex/client_appserver_transport.go`
6. **golden 测试按业务域分布**，首批样例分散到 `orchestration`、`transport`、`archtest`，不集中堆在 `rpc/`。
7. **守卫标准**：文件 ≤ 400 行，函数 ≤ 80 行，CC ≤ 10，包非测试文件 ≤ 15。

---

## 1. 第一批：根因 B + C + DAG fencing（终态闭环 + provider 进程 + wakeup 锁）— ✅ 已完成

> 完成日期：2026-03-25
> 审查流程：6 Agent 实施 → 6 Agent 初审 → 1:5 互审 → 修复 3 阻塞 → 再互审 6 方确认 → 收口

| Agent | 任务 | 状态 | 备注 |
|-------|------|------|------|
| **B1** | `TurnInterrupted` 独立终态闭环 | ✅ 已交付 | handleTurnInterruptedEvent 独立路径，不合成 TurnCompleted |
| **B2** | `StopAllAgents/archive/delete` 统一停机 | ✅ 已交付 | runner shutdown 补 handleProcessExit；**已知残留：waitForProcessExit 无超时的理论竞态窗口，放第二批修** |
| **B3** | claude reconnect / reinitialize | ✅ 已交付 | threadReady/threadReadyOnce 重建 + 等新 transport ready，-race 通过 |
| **B4** | codex transport 进程契约 | ✅ 已交付 | Setpgid + readiness probe + stderr collector + orphan cleanup + SIGTERM→grace→SIGKILL + atomic.Bool 修竞态，-race 通过 |
| **B5** | approval 阻塞等待态策略落地 | ✅ 方案 B 确认 | R1 已实现 Kind 扩展匹配（`tool`+`request_user_input`），UI 按 Kind 区分审批/输入，无需新增状态 |
| **B6** | DAG 锁 / wakeup fencing | ✅ 已交付 | ClaimedBy/LeaseExpiresAt 补传 + active_wakeup_id 贯穿 + 正向 fence 测试 + CC 降到≤10 |
| **D0** | golden test 框架 | ✅ 已交付 | helper + 3 个示例 case 按业务域分布（orchestration/transport/archtest） |

### 第一批已知残留（放第二批）

| # | 问题 | 归因 | 说明 |
|---|------|------|------|
| R1 | B2 waitForProcessExit 无超时理论竞态窗口 | B2 | 修复前 100% 卡死，修复后仅极小窗口。第二批加超时彻底封死 |
| R2 | B5 approval 等待态策略已决 | B5 | ✅ 方案 B 确认，R1 已落地 |
| R3 | SqlcBoundary 4 项违规（dashboard/uistate） | 预存 | 非本批引入，记录待修 |

### B1: `TurnInterrupted` 独立终态闭环

**问题**：orchestration 当前只在 `internal/sidecar/orch/orchestration/service.go` 的 `registerTurnLifecycle` 中订阅 `TurnStarted/TurnCompleted`；`TurnInterrupted` 没有独立消费路径。现有 `CompleteTurn(ctx, agentID, turnID, success bool, errMsg string)` 是二态接口，无法表达 interrupted 第三态。

**V2 参考**：
- `internal/runner/manager_lifecycle.go`：停机/中断链路会发出终态事件
- legacy tracker rules：区分 `turn_aborted`、`turn_complete`、`idle` 等终态

**修复方案**：
1. 在 `internal/sidecar/orch/orchestration/service.go` 的 `registerTurnLifecycle` 中显式订阅 `turndto.TurnInterrupted`。
2. 新增独立 `handleTurnInterruptedEvent(...)`，不再合成 `TurnCompleted`。
3. interrupted 路径单独完成：
   - 清理 `activeTurnID`
   - 驱动 interrupted 专用状态迁移
   - 保留 `reason`
   - 保证不复用 `success=false` 冒充 interrupted
4. 补测试：覆盖 `TurnInterrupted` 正常闭环、重复事件幂等、interrupt 后再收到 `TurnCompleted` 的收敛规则。

**验证**：`go test ./internal/sidecar/orch/orchestration/...`

### B2: `StopAllAgents/archive/delete` 统一停机

**问题**：`StopAllAgents` 仍存在“先 `removeSession/publishAgentStopped` 再等真实退出”的时序；`archive/delete` 也没有统一走 `StopAgent()/waitForProcessExit()`；此外 `StopAllAgents` 当前还会把 `stopReason=""` 提前清空。

**V2 参考**：`internal/runner/manager_lifecycle.go` 的 `Stop/StopAll` 统一经停机链路收敛，`Shutdown/Kill` 完成后才删除运行态。

**修复方案**：
1. 以 `internal/sidecar/orch/orchestration/service.go` 的 `StopAgent/StopAllAgents` 为入口，抽出统一 stop helper。
2. `StopAllAgents` 改为逐个走 `StopAgent()` + `waitForProcessExit()`，不再直接 `removeSession/publishAgentStopped`。
3. `archive/delete` 在变更 thread/session/store 前必须先复用同一停机 helper。
4. 同步修复 `StopAllAgents` 中 `stopReason=""` 清空 bug，保证 `publishAgentStopped` 使用真实原因。
5. 补测试：stop-all、archive、delete、重复 stop、失败回滚。

**验证**：`go test ./internal/sidecar/orch/orchestration/... ./internal/module/thread/...`

### B3: claude reconnect / reinitialize

**问题**：`restartIfNeededLocked` 实际位于 `internal/provider/claudecli/session.go`，当前只替换 transport/read loop，没有重建 `threadReady/threadReadyOnce`，也没有重新等待新的 ready 信号。

**V2 参考**：legacy claude client 在重启后会重置 ready 通道，并等待 `session_configured` / ready 事件重新建立 session 上下文。

**修复方案**：
1. 读取 V2 legacy claude client 的 restart/ready 链路。
2. 在 V3 `internal/provider/claudecli/session.go` 的 `restartIfNeededLocked()` 中：
   - 重建 `threadReady` / `threadReadyOnce`
   - 重建 restart 期间的 turn/session 协调状态
   - 等待新 transport 的 ready / configured 事件后再继续
3. 明确 restart 前后 session/thread ID 的保持规则。
4. 补测试：restart 后 ready channel 重建、早到事件不丢、并发 submit 不越过 ready。

**验证**：`go test ./internal/provider/claudecli/...`

### B4: codex transport 进程契约

**问题**：codex 进程管理相关逻辑实际集中在 `internal/provider/codexapp/transport.go`，不是 `driver.go`。当前仍缺 readiness probe、`Setpgid`、stderr collector、orphan cleanup、分阶段 stop。

**V2 参考**：legacy codex app-server transport 提供完整 transport 管理，包括 `Setpgid`、初始化、重连、孤儿清理等。

**修复方案**：
1. 以 `internal/provider/codexapp/transport.go` 为主改点，补齐：
   - `cmd.SysProcAttr.Setpgid = true`
   - 启动后的 readiness / initialize barrier
   - stderr collector
   - local process orphan cleanup
   - stop 顺序 `SIGTERM -> grace period -> SIGKILL`
2. 将 transport 层契约显式化，不把进程管理散落回 `driver.go`。
3. 补测试：spawn-local、reconnect、graceful stop、orphan cleanup。

**验证**：`go test ./internal/provider/codexapp/...`

### B5: approval 阻塞等待态策略落地

**问题**：当前只有 `kind==request_user_input` 才进入 `awaiting_user_input`；普通 tool approval 事件无法稳定驱动等待态。

**必须先人工定策略**：
1. **方案 A**：新增 `awaiting_tool_approval` 状态。
2. **方案 B**：扩大 `isRequestUserInputEvent` 的 Kind 匹配，把 tool approval 归入现有等待态。

**约束**：Agent 不自行决定 A/B，只能在人工定案后按既定方案实现。

**修复方案**：
1. 对照 V2 approval 生命周期与当前状态机触发点。
2. 在人工定案后，统一修改状态机判定、恢复路径与测试基线。
3. 保证与 B1 的 interrupted 终态处理能正确配合，不产生等待态泄漏。

**验证**：`go test ./internal/platform/statemachine/... ./internal/sidecar/orch/orchestration/...`

### B6: DAG 锁 / wakeup fencing

**问题**：当前 wakeup claim/bind/recover 相关逻辑分散在 `internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql`、`internal/sidecar/orch/sql/queries/task_dag_node_runtime.sql`、`internal/sidecar/orch/store/taskdag/store_wakeup.go`、`internal/sidecar/orch/orchestration/recover.go`，但计划里还没有独立 Agent 负责把 `claimed_at/claimed_by/lease_expires_at/active_wakeup_id` 做成完整 fencing。

**修复方案**：
1. 明确 wakeup claim fence：`claimed_at + claimed_by + lease_expires_at` 必须参与 sent/retry/fail 的幂等条件。
2. 明确 node bind fence：`active_wakeup_id` 必须贯穿 `BindRunningTaskDagNodeTurn`、recover replay、completion 清理。
3. 补 stale dispatch reclaim 与 recover replay 的冲突测试，防止旧 wakeup 绑定新 turn。
4. 补 DAG 级回归测试：重复 claim、stale reclaim、recover replay、双 worker 并发。

**验证**：`go test ./internal/sidecar/orch/orchestration/... ./internal/sidecar/orch/store/taskdag/...`

### D0: golden test 框架

**目标**：只搭基础设施，不做实际 RPC 修复。

**范围**：
1. 从 V2 提取关键 schema / payload 样本。
2. 建共享 test helper，支持 golden 更新、差异对比、domain 分类。
3. 先落 2-3 个示例 case，按业务域分散到：
   - `orchestration`
   - `transport`
   - `archtest`

**要求**：
- 不把所有 golden 都堆到 `rpc/`
- 为第二批返回体修复提供回归基线

**验证**：`go test ./internal/sidecar/orch/orchestration/... ./internal/provider/... ./internal/archtest/...`

---

## 2. 第二批：根因 D（RPC 返回体系统迁移）

### 预计：9 项 P1，10-15 人日，5 个 Agent 并行

| Agent | 任务 | 涉及模块 | 工作量 | 依赖 |
|-------|------|---------|--------|------|
| **D1** | `thread/config/get` 字段与语义恢复 | thread-config | 中 | D0 |
| **D2** | `thread/messages` 四项恢复（分页 / envelope / hydration / 离线） | thread-messages | 大 | D0；离线子项依赖 A1 |
| **D3** | `turn/interrupt` 返回 envelope | turn-lifecycle | 小 | D0 |
| **D4** | claude turn finish payload | provider-claude | 中 | D0 |
| **D5** | store-db DTO（`thread/read` + workspace） | store-db | 中 | D0 |

### 第二批统一方法论

1. 所有返回体修复以 D0 的 golden 框架为门槛。
2. 修复顺序固定为：contract/type -> handler -> provider/store -> golden -> archtest。
3. 只要修改 JSON 结果面，就必须新增或更新 golden。

### D1: `thread/config/get` 字段与语义恢复

**目标**：恢复 `effective/override` 结果层，补齐 `model/effort/supportsThreadOverride` 等字段，不再只返回“形似但值不对”的壳。

**重点**：
1. 对齐 V2 `threadConfigGet` 合同。
2. 补 `effort` 与 override/effective 的真实来源。
3. 补 golden 与 contract test。

**验证**：`go test ./internal/module/thread/...`

### D2: `thread/messages` 四项恢复

**拆分**：
1. **D2-1**：恢复 `before` / cursor / limit 入参契约。
2. **D2-2**：恢复响应 envelope（`messages/total/...`），不再直接返回裸数组。
3. **D2-3**：恢复 provider history + runtime hydration / compaction 语义。
4. **D2-4**：恢复离线历史读取结果层。

**依赖声明**：
- 离线历史读取子项依赖 **A1**
- 本批先修返回结构与在线路径
- 离线场景留到 A1 完成后补验证

**验证**：`go test ./internal/module/thread/...`

### D3: `turn/interrupt` 返回 envelope

**目标**：恢复 V2 的确认型 envelope，不再固定返回 `{ok:true}`。

**重点**：
1. handler 不再吞掉 provider 返回信息。
2. codex/claude 两条 provider 路径统一成相同结果面。
3. 与 B1 的 interrupted 终态闭环保持一致。

**验证**：`go test ./internal/module/turn/...`

### D4: claude turn finish payload

**目标**：恢复 turn finish payload 的 `summary/result/message/stop_reason` 合同，避免只剩缩水结果。

**验证**：`go test ./internal/provider/claudecli/...`

### D5: store-db DTO（`thread/read` + workspace）

**拆分**：
1. **D5-1**：恢复 `thread/read` DTO 与读取结果层。
2. **D5-2**：恢复 workspace DTO / tool DTO 形状。

**验证**：`go test ./internal/store/... ./internal/module/workspace/...`

---

## 3. 第三批：根因 A 残项 + E 残项（session 解耦 + approval replay）— ✅ 已完成

> 完成日期：2026-03-26
> 审查流程：5 Agent 实施 → 1:4 互审 → 修复 11 项问题 → 再互审 4 方确认 → 收口

| Agent | 任务 | 状态 | 备注 |
|-------|------|------|------|
| **A1** | session 解耦 | ✅ 已交付 | store 写穿+resume 合并+config 优先级链；**已知残留：Claude resume threadID 耦合，放第四批** |
| **A2** | preferences delta | ✅ 已交付 | 校验先于存储，非法值拒绝写入 |
| **A3** | approval replay+peer_kind | ✅ 已交付 | UI-only replay+TTL 刷新+peer_kind gating |
| **A4-α** | 深度计数器+Status | ✅ 已交付 | nil check+idle-preserve 修正+CC≤10 |
| **A4-β** | Overlay 覆盖层 | ✅ 已交付 | Sidebar/patch 一致性；**已知限制：terminal_wait 无生产 producer** |
| **A4-γ** | Timeline 投影 | ⏸️ 可选 | 顺延到第四批 |

### 第三批已知残留（放第四批）

| # | 问题 | 归因 | 说明 |
|---|------|------|------|
| R1 | Claude resume 路径 resolveResumeRequest 把 threadID 改写成 ProviderThreadID | A1 | 事件流 threadID 仍可能落到 provider id，影响 uistate 归属 |
| R2 | terminal_wait overlay 无生产 producer | A4-β | 只有 mcp_startup 有 live producer，terminal_wait 等后续事件接入 |
| R3 | A4-γ Timeline 投影未实现 | A4-γ | ~400-500行，高耦合，可选推迟 |

### 原始计划：3 项 P1 + 1 项前置架构（A4 分 3 阶段执行），12-16 人日，4 个 Agent 并行

`注`：D1/D2 在线合同已在第二批处理，第三批只补无 session 语义。
`注`：A2 soft-depends-on A1。

| Agent | 任务 | 涉及模块 | 工作量 | 备注 |
|-------|------|---------|--------|------|
| **A1** | thread-scoped config/state resolver + override 持久化 | thread-config + thread-messages + uistate-config | 中~大 | 架构前置；补 D1/D2 的无 session 语义 |
| **A2** | 剩余 preferences 合同与副作用 delta | uistate | 中 | 对应 P1-21；soft-depends-on A1 |
| **A3** | approval live replay + `peer_kind` gating | rpc + approval | 中 | 对应 P1-12；不再依赖 B5 |
| **A4-α** | 深度计数器 + Status Derive 增强 | uistate | 小~中 | 对应 P1-22；必做，低耦合 |
| **A4-β** | Overlay 覆盖层 | uistate | 中 | 对应 P1-22；必做，中耦合 |
| **A4-γ** | Timeline 投影 | uistate | 大 | 对应 P1-22；可选，高耦合，可推迟 |

### A1: thread-scoped config/state resolver

**目标**：把 `thread/config/get`、`thread/messages`、`ui/config/get` 的“无 active session”读取统一到 thread-scoped resolver，不再把 online session 当成唯一真相源。

**边界说明**：
1. 第二批 D1/D2 已恢复在线合同与返回结构；第三批只补“session 缺失时”的读取语义。
2. resolver 要同时覆盖 config、runtime config、messages、sidebar 所需的 thread 元数据读取。

**文件落点（已核对）**：
1. `internal/module/thread/service.go` `resolveSession`
2. `internal/module/thread/command.go` `GetConfig`
3. `internal/module/thread/history.go` `ReadMessages` / `ReadRuntimeConfig`
4. `internal/module/uistate/config_rpc.go` `applyRuntimeConfigOverrides`

**config 真相源优先级链**：
```text
1. active session runtime 值（session 存在时）
2. thread-scoped store override（SetConfig 写入，持久化到 threadStore）
3. binding metadata（provider / cwd）
4. provider default（hardcoded）
```

**交付物**：
1. `buildOfflineConfig()`
2. `SetConfig` override 持久化到 `threadStore`
3. D1 `toolRouting` 在无 session 时有明确 fallback，而不是隐式丢空
4. `ReadMessages` / `ReadRuntimeConfig` / `ui/config/get` 共享同一套 resolver 语义

### A2: 剩余 preferences 合同与副作用 delta

**目标**：不重做 `ui/preferences` 主合同，只补当前实现相对 V2 仍缺的 side-effects delta 和结果层尾差。

**当前已具备**：
1. `ui/preferences/get`
2. `ui/preferences/getAll`
3. `ui/preferences/set`
4. 结构化 `Preferences` 结果层（`cwd/values/activeThreadId/...`）
5. 3 个 runtime 副作用：`activeThreadId`、`activeCmdThreadId`、`mainAgentId`

**剩余 delta**：
1. 补齐 V2 `stallThresholdSec` 的运行时副作用与校验语义
2. 补齐 V2 `settings.showInjectedPromptInChat` 的运行时副作用
3. 盘点其他仍留在 V2 `PreferenceSideEffects` / UI state 结果层里的尾差，避免只改 key 存储不改行为

**文件落点**：
1. `internal/module/uistate/service.go` `SetPreference`
2. `internal/module/uistate/patch.go` `SetPreference` side-effect path / `applyRuntimePreferenceLocked`

**依赖**：soft-depends-on A1

### A3: approval live replay

**目标**：恢复 reconnect 后 pending approvals 的重放，但只对 UI peer 生效，不再把 tool peer 当成 replay 目标。

**前置改动**：
1. 去掉对 B5 的依赖声明；B5 `Kind` 已在 R1 用方案 B 修完。
2. `rpc.Server` 引入 `peer_kind`。
3. `serveConn` 注入 `peer_kind`（`ui` 或 `tool`）。
4. `RestorePending` 只对 `peer_kind=ui` 的连接触发。

**竞态修复**：
1. replay 成功后刷新 pending TTL 基准，避免 reconnect 后立即被 cleanup 线程清掉。
2. `OnConnect` replay 与 cleanup timeout 的窗口要有显式顺序保证。

**文件落点**：
1. `internal/platform/rpc/module.go` `OnConnect`
2. `internal/platform/rpc/approval.go` `RestorePending`

**验证**：`go test ./internal/platform/rpc/... ./internal/module/turn/...`

### A4: uistate 事件投影链恢复（分阶段）

**目标**：把 P1-22 从“一次性大改”改成 staged delivery；第三批先完成必做的状态与 overlay 恢复，timeline 投影允许顺延。

**V2 对照文件（已核对）**：
1. `/Users/mima0000/Desktop/wj/go-agent-v2/internal/uistate/event_normalizer.go`
2. `/Users/mima0000/Desktop/wj/go-agent-v2/internal/uistate/event_dispatch.go`
3. `/Users/mima0000/Desktop/wj/go-agent-v2/internal/uistate/runtime_state.go`

#### A4-α（必做）：深度计数器 + Status Derive 增强

- 规模：约 200 行，低耦合
- 目标：补齐 turn/tool/workspace 事件深度计数、状态派生与 status text derive
- 结果：先把 sidebar/state 的状态正确性拉回 V2 水位

#### A4-β（必做）：Overlay 覆盖层

- 规模：约 150 行，中耦合
- 目标：恢复 lifecycle / snapshot / partial patch 之间的 overlay 合成
- 结果：解决“状态有了但 UI patch 仍不完整”的半恢复状态

#### A4-γ（可选）：Timeline 投影

- 规模：约 400-500 行，高耦合
- 目标：恢复 thread timeline / diff / workspace 投影链
- 说明：若第三批压力过高，可顺延到第四批前半，但不得阻塞 α / β 落地

**验证**：`go test ./internal/module/uistate/...`

---

## 4. 第四批：thread 能力面 + workspace + dashboard + wails 收尾

### 预计：11 项 P1 + 2 项第三批残留 + 1 项已完成校核，14-22 人日，8-10 个 Agent 并行

| Agent | 任务 | 工作量 | 依赖 |
|-------|------|--------|------|
| **F1** | `dashboard/agentStatus` 独立读模型 + status filter | 中 | 无 |
| **F2** | dashboard 日志面（`auditLogs/aiLogs/busLogs`）+ DAG 面（`dags/dagDetail`） | 大 | 无 |
| **F3** | wails desktop API：`ui/log`、`windowBootstrap`、LSP helper | 中 | 无 |
| **F4** | wails desktop 兼容绑定/事件：`ui/openNewWindow`、`agent-event`、`files-dropped`、`GetGroup`、`SelectProjectDirs` | 中 | 无 |
| **F5-1** | `thread/start` 契约恢复 | 中 | P0 thread-start guard/binding |
| **F5-2** | `thread/resume` 契约恢复（含 R1 threadID 修复） | 中 | B3/B4 + A1-R1 + P0-2 |
| **F5-3** | `thread/recover` 契约恢复 | 中 | B6 |
| **F5-4** | `thread/fork` 契约恢复 | 小~中 | 无 |
| **F6-1** | 验证+清理：workspace dry-run 生命周期 | 小 | 无 |
| **F6-2** | 验证+清理：workspace merge 补偿 + `delete_removed` | 小 | F6-1 |
| **F7** | execute-time ready wait | 小 | B3/B4 建底 |
| **F8** | Claude resume public/provider thread ID 边界修复 | 小 | F5-2 |
| **F9** | codexapp `terminal_wait` overlay producer 补齐 | 小 | A4-β 已交付 |

### F1: `dashboard/agentStatus` 独立读模型 + status filter

**目标**：恢复 V2 `dashboard/agentStatus` 的独立接口与过滤能力，不再完全并入 `ui/dashboard/get`。

**目标文件链**：
1. `go-agent-v2/internal/dashrpc/register.go`
2. `go-agent-v2/internal/apiserver/dashboard_bindings.go`
3. `internal/module/dashboard/rpc.go`
4. `internal/module/dashboard/contract.go`
5. `internal/module/dashboard/ui_page.go`

**至少恢复**：
1. `dashboard/agentStatus` 接收 `status` 参数并独立返回 `agents` envelope
2. 不再依赖 `ui/dashboard/get` 全量 page 聚合来做状态过滤
3. 补 schema / contract / guard 测试，覆盖 `status=running` 过滤

**验证**：`go test ./internal/module/dashboard/... ./internal/apiserver/... ./internal/guards/...`

### F2: dashboard 日志面 + DAG 面

**目标**：恢复 `dashboard/auditLogs`、`dashboard/aiLogs`、`dashboard/busLogs`、`dashboard/dags`、`dashboard/dagDetail` 等细粒度能力。

**目标文件链**：
1. `go-agent-v2/internal/dashrpc/register.go`
2. `go-agent-v2/internal/apiserver/dashboard_bindings.go`
3. `internal/module/dashboard/rpc.go`
4. `internal/module/dashboard/service.go`
5. `internal/module/dashboard/contract.go`

**至少恢复**：
1. `dashboard/auditLogs` / `dashboard/busLogs` 与 V2 同名 surface
2. `dashboard/dags` / `dashboard/dagDetail` 细粒度 DAG 查询，不再只靠总览页
3. `dashboard/aiLogs` 与现有 `dashboard/aiLogs/recent` / `stats` 共存，不做回退覆盖
4. 实施时按 logs / DAG 两条子链拆提交，避免单文件过热

**验证**：`go test ./internal/module/dashboard/... ./internal/apiserver/... ./internal/guards/...`

### F3: wails desktop API

**目标**：补齐桌面 API 面，至少覆盖：
1. `ui/log`
2. `windowBootstrap`
3. LSP helper / diagnostics helper

**目标文件链**：
1. `go-agent-v2/cmd/agent-terminal/app_handlers.go`
2. `internal/ui/wails/binding.go`
3. `internal/ui/wails/binding_native.go`
4. `internal/ui/wails/rpc.go`

**至少恢复**：
1. `ui/log` RPC surface 与桌面日志桥接
2. `ui/windowBootstrap/get` 的一次性读取行为
3. `GetLSPDiagnostics` / `GetLSPStatus` 或同等 desktop helper 兼容面

**验证**：`go test ./internal/ui/wails/... ./internal/app/...`

### F4: wails desktop 兼容绑定 / 事件

**说明**：原 `F4 "默认 middleware 链恢复"` 缺少明确文件、链路和验收定义，现重定义为可执行的桌面兼容任务。

**目标文件链**：
1. `internal/ui/wails/binding.go`
2. `internal/ui/wails/binding_native.go`
3. `internal/ui/wails/bridge.go`
4. `internal/ui/wails/window.go`

**至少恢复**：
1. `ui/openNewWindow`
2. `agent-event`
3. `files-dropped`
4. `GetGroup`
5. `SelectProjectDirs`

**验证**：`go test ./internal/ui/wails/... ./internal/app/...`

### F5: thread 模块四项契约恢复

> ⚠️ F5-1~F5-4 共享 `launchAgent`/`persistThreadState`/`bindSessionGeneration` 等核心内部方法。
> 建议由同一 Agent 或最多 2 个 Agent 执行，禁止 4 个 Agent 并行改同一组方法。
> 如果多 Agent 执行，必须遵守约束：共享方法签名不可变更，只能扩展返回体。

**目标文件链**：
1. `internal/module/thread/rpc.go`
2. `internal/module/thread/contract.go`
3. `internal/module/thread/lifecycle.go`
4. `internal/module/thread/start_session.go`
5. `internal/provider/codexapp/driver.go`
6. `internal/provider/codexapp/session.go`

#### F5-1: `thread/start`

**目标**：恢复 V2 `thread/start` 的参数面、provider 选择 fallback 和返回 envelope。

**重点**：
1. 参数：`modelProvider/baseInstructions/developerInstructions/sandbox/summary` 等
2. provider 选择：不再强制用户每次显式给 `provider`
3. 返回：恢复 `thread{id,status}`、effective model/provider/cwd/approvalPolicy

#### F5-2: `thread/resume`

**目标**：恢复 `threadId/path/cwd/model` 请求面与对象型返回，避免继续返回 `null`。

**含 R1 子任务**：修复 `resolveResumeRequest`（`start_session.go:179-202`）把 `req.ThreadID` 改写成 `ProviderThreadID` 的问题。Resume 后事件流 threadID 必须始终是 public thread id，provider 内部自行解析 provider-side ID。此项与 F8 共同收口 R1 残留。

#### F5-3: `thread/recover`

**目标**：恢复 `recovered/mode` 结果面与 recover replay 语义，不再只返回 effect。

#### F5-4: `thread/fork`

**目标**：恢复 `thread{id,forkedFrom}` 返回 envelope，补齐 fork 元数据，不再只返回 `newThreadID`。

**验证**：`go test ./internal/module/thread/... ./internal/provider/codexapp/...`

### F6: workspace dry-run + merge 补偿

**目标文件链**：
1. `internal/module/workspace/service.go`
2. `internal/module/workspace/service_merge.go`
3. `internal/module/workspace/service_delete_removed.go`
4. `internal/module/workspace/rpc.go`
5. `internal/module/workspace/service_test.go`

#### F6-1: dry-run 生命周期（验证+清理）

**目标**：dry-run 生命周期已实现且有测试，验证 `active → merging → active` 门闩完整性，清理 `service_helpers.go:159` 过时 TODO。

#### F6-2: merge 补偿 + `delete_removed`（验证+清理）

**目标**：merge 补偿 + `delete_removed` 已实现且有测试，验证事件一致性，清理死代码 `trackedRunFilePaths`。

**验证**：`go test ./internal/module/workspace/...`

### F7: execute-time ready wait

**说明**：`WSHandler` 已经在 `internal/platform/rpc/module.go` 接线，本任务只保留 execute-time ready wait。

**目标文件链**：
1. `internal/sidecar/orch/orchestration/helpers.go`
2. `internal/sidecar/orch/orchestration/service.go`
3. `internal/sidecar/orch/orchestration/execution_test.go`

**目标**：
1. 统一 submit / execute 前的 session ready 等待
2. 以 `internal/sidecar/orch/orchestration/helpers.go` 的 `waitForSubmitSessionReady` 为收敛点
3. 覆盖 ready race、超时、重试

**验证**：`go test ./internal/sidecar/orch/orchestration/...`

### F8: Claude resume public/provider thread ID 边界修复

**来源**：第三批残留 R1-Claude。

**目标文件链**：
1. `internal/module/thread/start_session.go`
2. `internal/module/thread/lifecycle.go`
3. `internal/provider/codexapp/driver.go`
4. `internal/module/thread/binding_registration.go`

**修复目标**：
1. `resolveResumeRequest` 保持 public `threadId` 透传到 provider 调用边界
2. provider 内部自行解析 `ProviderThreadID`，禁止在 module 层把 `req.ThreadID` 改写成 provider id
3. 事件流 / uistate / binding 继续以 public thread id 归属

**验证**：`go test ./internal/module/thread/... ./internal/provider/codexapp/...`

### F9: codexapp `terminal_wait` overlay producer 补齐

**来源**：第三批残留 R2-TerminalWait。

**目标文件链**：
1. `internal/module/uistate/projector_handlers.go`
2. `internal/module/uistate/sidebar_compat.go`
3. `internal/provider/codexapp/session_approval.go`
4. `internal/platform/rpc/approval_events.go`

**修复目标**：
1. 在 codexapp 的终端等待 / `request_user_input` 事件中补 `setThreadOverlayLocked` 调用
2. 在输入恢复或 turn 完成后清理 `terminal_wait` overlay，避免悬挂
3. 补 live projector 测试，确保 overlay 与 sidebar snapshot/patch 一致

**验证**：`go test ./internal/module/uistate/... ./internal/provider/codexapp/... ./internal/platform/rpc/...`

### 第四批统一验收

⚠️ 守卫红线：单文件≤400行，单函数≤80行，CC≤10，包文件数≤15，包总行数≤4500
完成后必须跑 `go test -run TestCodeSizeGuard ./internal/archtest/...`

**批次验收**：
1. `go test ./internal/module/dashboard/... ./internal/ui/wails/... ./internal/module/thread/... ./internal/module/workspace/...`
2. `go test ./internal/sidecar/orch/orchestration/... ./internal/provider/codexapp/...`

### 第四批后仍未闭环项（需第五批或人工决策）

| # | 问题 | 状态 | 说明 |
|---|------|------|------|
| 1 | P1-16 (B5) approval 阻塞等待态 | ✅ 方案 B 确认 | R1 已实现 Kind 扩展匹配，UI 按 Kind 区分，2026-03-27 人工确认关闭 |
| 2 | A4-γ Timeline 投影 | ⏸️ 可选推迟 | ~400-500行高耦合，第三批标记为放第四批但未列入 |
| 3 | D1 config/read 完整离线 merge | ⏸️ | A1 基础已建，完整 V2 runtime merge 待补 |

---

## 5. P2 协议收缩（12 项，可与 P1 同批修复）

多数 P2 项与对应 P1 项同源，修 P1 时顺带对齐：

| P2 | 关联 P1 | 同批修复 |
|----|---------|---------|
| thread/start envelope | P1-01 `thread/start` 契约恢复 | 第四批 F5-1 |
| config 语义变更 | P1-05 `thread/config/get` | 第二批 D1 |
| messages before 类型 | P1-06 `thread/messages` cursor/before 契约 | 第二批 D2-1 |
| turn payload 缩水 | P1-10 `turn/interrupt` envelope | 第二批 D3 |
| dashboard 收窄 | P1-19~20 dashboard 补全 | 第四批 F1 + F2 |
| uistate 缩水 | P1-21~22 preferences / projection | 第三批 A2 + A4 |
| manifest env | P1-15 execute-time ready wait + P1-23 已完成 WSHandler 接线 | 第四批 F7 / 验收核对 |
| claude translator | P1-26 claude turn finish payload | 第二批 D4 |
| rpc-push method | 独立补 | 第二批附带 |
| raw passthrough | 评估是否恢复 | 第二批附带 |
| bus naming | 兼容桥 | 第二批附带 |
| workspace tool DTO | P1-28 workspace DTO | 第二批 D5-2 |

---

## 6. 验收标准

每批修复完成后必须满足：

1. `go build ./internal/... ./cmd/mcp-orch/...` ✅
2. 相关包测试全绿 ✅
3. `go test -run "TestCodeSizeGuard|TestDependencyDirection|TestTimeoutLocality" ./internal/archtest/...` ✅
4. 所有修改过 contract/result 的 P1 项 golden 测试全绿 ✅
5. 1:N 互审通过（每项至少 2 个 Agent 交叉审查） ✅
6. 涉及 MCP lifecycle/control plane 的改动必须额外满足契约合规：
   - 方法命名统一为 `ctl/*`，`mcp/*` 仅保留兼容别名
   - 活体寻址统一按 `LeaseKey{instance_id, generation}`，不得回退裸 `instance_id`
   - pending approval restore / UI 定向投递只面向 `peer_kind=ui`

---

## 7. 时间线与工作量

| 周 | 批次 | 根因 | 项数 | Agent 数 |
|----|------|------|------|---------|
| 第 2 周 | 第一批 | B + C + DAG fencing | 6 项 P1 + D0 | 7 |
| 第 3 周 | 第二批 | D | 9 项 P1 | 5 |
| 第 3-4 周 | 第三批 | A 残项 + E 残项 | 3 项 P1 + 1 项前置（A4 分阶段） | 4 |
| 第 4-5 周 | 第四批 | thread/workspace/dashboard/wails | 11 项 P1 + 2 项残留 + 1 项已完成校核 | 8-10 |
| **合计** | | | **30 项 P1（含 1 项已完成校核） + 2 项残留 + 1 项使能 + 1 项前置** | **~24-26** |

工作量汇总：
- 第一批：15-20 人日
- 第二批：10-15 人日
- 第三批：12-16 人日
- 第四批：14-22 人日
- **总计：51-73 人日**

说明：
- P1-23（WSHandler 接线）不再作为新增开发项，只保留验收核对。
- D0 是使能项，不计入 30 项 P1。
- P2 项随对应 P1 同批消化，不单独排期。
- P3 设计演进差异继续记录备案，不纳入本轮修复范围。
