# P7w2 审查：Dashboard 模块实现

## 结论

`internal/module/dashboard/` 已经形成可编译、可注册、可接入的独立读模型模块：有清晰的 `Service` 契约、4 个 RPC handler、`fx` 注册、`app` 装配、DTO 定义，以及通过的编译守卫。

但它和 V2 的关系是“局部替代”，不是“等价迁移”。当前最明显的 3 个差距如下。

1. `ui/dashboard/get` 接受 `page` 参数，但当前 handler 明确忽略该参数，始终只返回 V3 的 `Dashboard` DTO；V2 则按 `page` 聚合 `agents/dags/taskAcks/taskTraces/skills/commandCards/prompts/memory`。证据：`internal/module/dashboard/rpc.go:12-14`、`internal/module/dashboard/rpc.go:40-41`、`go-agent-v2/internal/dashrpc/ui.go:34-38`、`go-agent-v2/internal/dashrpc/ui.go:61-77`。
2. V2 `dashrpc.Register` 暴露了 12 个 `dashboard/*` 方法，V3 当前只有 `dashboard/agent/detail`、`dashboard/system/info`、`dashboard/logs` 三个 `dashboard/*` 方法，覆盖面明显缩小。证据：`go-agent-v2/internal/dashrpc/register.go:89-103`、`internal/module/dashboard/rpc.go:38-52`。
3. `Dashboard.TokenUsage` 与 `AgentDetail.TurnHistory` 已出现在 DTO 中，但服务实现当前分别固定返回零值和空切片，尚未连接真实数据源。证据：`internal/module/dashboard/types.go:13-24`、`internal/module/dashboard/types.go:26-31`、`internal/module/dashboard/service.go:60-65`、`internal/module/dashboard/service.go:85-89`。

## 1. 文件清单与行数

架构守卫的文件上限是 `400` 行。证据：`internal/archtest/guardlib.go:17-24`。

| 文件 | 行数 | 结论 | 证据 |
| --- | ---: | --- | --- |
| `internal/module/dashboard/contract.go` | 11 | <=400 | `internal/module/dashboard/contract.go:1-11` |
| `internal/module/dashboard/logs.go` | 86 | <=400 | `internal/module/dashboard/logs.go:1-86` |
| `internal/module/dashboard/module.go` | 9 | <=400 | `internal/module/dashboard/module.go:1-9` |
| `internal/module/dashboard/rpc.go` | 73 | <=400 | `internal/module/dashboard/rpc.go:1-73` |
| `internal/module/dashboard/service.go` | 338 | <=400 | `internal/module/dashboard/service.go:1-338` |
| `internal/module/dashboard/types.go` | 86 | <=400 | `internal/module/dashboard/types.go:1-86` |

结论：范围内共 6 个 Go 文件，全部低于 `MaxFileLines=400`，其中最大的是 `service.go` 的 338 行。

## 2. Service 接口

`Service` 契约正好暴露了任务要求的 4 个方法。

- `GetDashboard(ctx)`，证据：`internal/module/dashboard/contract.go:5-10`、`internal/module/dashboard/service.go:55-66`
- `GetAgentDetail(ctx, agentID)`，证据：`internal/module/dashboard/contract.go:5-10`、`internal/module/dashboard/service.go:68-90`
- `GetSystemInfo(ctx)`，证据：`internal/module/dashboard/contract.go:5-10`、`internal/module/dashboard/service.go:92-103`
- `GetLogs(ctx, filter)`，证据：`internal/module/dashboard/contract.go:5-10`、`internal/module/dashboard/service.go:105-130`

实现侧还有编译期接口守卫：`var _ Service = (*service)(nil)`。证据：`internal/module/dashboard/service.go:24-29`、`internal/module/dashboard/service.go:40-40`。

## 3. 数据聚合

聚合来源很集中，没有引入 UI 状态层。

| 入口 | 数据来源 | 结论 | 证据 |
| --- | --- | --- | --- |
| `GetDashboard` | `orchestration.Service.ListAgents` + 进程 runtime/build 信息 | 聚合 agent 列表、system 信息、启动时长；`TokenUsage` 暂为零值 | `internal/module/dashboard/service.go:55-66`、`internal/module/dashboard/service.go:132-160` |
| `GetAgentDetail` | `orchestration.Service.Snapshot` + `orchestration.Service.GetReport` | 读取 agent 快照和最后报告；`TurnHistory` 目前固定空切片 | `internal/module/dashboard/service.go:68-90`、`internal/sidecar/orch/orchestration/contract.go:11-29` |
| `GetSystemInfo` | `orchestration.Service.ListAgents` + `runtime` + `debug.ReadBuildInfo` | 只取 agent 数量，其他为本进程 build/runtime 指标 | `internal/module/dashboard/service.go:92-103`、`internal/module/dashboard/service.go:143-213` |
| `GetLogs` | `systemlog.Store.List` + `ailog.Store.List` | 合并 system log 与 AI log，排序后裁剪 limit | `internal/module/dashboard/service.go:105-130`、`internal/module/dashboard/service.go:243-328`、`internal/store/systemlog/contract.go:9-50`、`internal/store/ailog/contract.go:9-34` |

补充观察：

- `service` struct 只持有 `orchestration.Service`、`systemlog.Store`、`ailog.Store` 三类依赖。证据：`internal/module/dashboard/service.go:24-29`。
- `GetLogs` 先用 `resolveLogSource` 决定是否查 system/ai，再分别调用 `appendSystemLogs` 与 `appendAILogs`，最后统一排序。证据：`internal/module/dashboard/service.go:105-130`、`internal/module/dashboard/service.go:230-288`、`internal/module/dashboard/logs.go:75-85`。

## 4. RPC handler

四个要求的 RPC 路由均已注册在 `NewDashboardHandlers` 中。

| RPC 方法 | 参数 | 服务调用 | 证据 |
| --- | --- | --- | --- |
| `ui/dashboard/get` | `uiDashboardGetParams{page}` | `svc.GetDashboard(ctx)` | `internal/module/dashboard/rpc.go:12-14`、`internal/module/dashboard/rpc.go:38-42` |
| `dashboard/agent/detail` | `agentId` / `agent_id` | `svc.GetAgentDetail(ctx, p.agentID())` | `internal/module/dashboard/rpc.go:16-19`、`internal/module/dashboard/rpc.go:43-45`、`internal/module/dashboard/rpc.go:55-57` |
| `dashboard/system/info` | 空对象 | `svc.GetSystemInfo(ctx)` | `internal/module/dashboard/rpc.go:46-48` |
| `dashboard/logs` | `source/keyword/level/logger/component/agentId|agent_id/threadId|thread_id/eventType|event_type/toolName|tool_name/limit` | `svc.GetLogs(ctx, p.filter())` | `internal/module/dashboard/rpc.go:21-36`、`internal/module/dashboard/rpc.go:49-50`、`internal/module/dashboard/rpc.go:59-72` |

审查结论：

- 路由名完整，且都通过 `rpc.StrictHandler` 暴露。证据：`internal/module/dashboard/rpc.go:38-52`。
- `ui/dashboard/get` 的 `page` 参数目前未参与任何分支逻辑，因为 handler 直接把参数命名为 `_` 后丢弃。证据：`internal/module/dashboard/rpc.go:40-41`。

## 5. fx 注册

`dashboard.Module` 已按要求提供 `Service` 和 handler map。

- `fx.Provide(NewService)` 暴露服务实现。证据：`internal/module/dashboard/module.go:5-8`、`internal/module/dashboard/service.go:42-53`
- `fx.Provide(NewDashboardHandlers)` 暴露 RPC handler map。证据：`internal/module/dashboard/module.go:5-8`、`internal/module/dashboard/rpc.go:38-53`
- `NewDashboardHandlers` 返回的是 `rpc.HandlerMapResult`。证据：`internal/module/dashboard/rpc.go:38-39`
- `rpc.HandlerMapResult` 是 `fx.Out`，并写入 `group:"rpc_handlers"`。证据：`internal/platform/rpc/module.go:36-40`
- `rpc.registerAllHandlers` 会把该 group 注册进服务器。证据：`internal/platform/rpc/module.go:47-52`

结论：`fx` 装配链条完整，满足“提供 Service + HandlerMapResult”的要求。

## 6. app 接入

`internal/app/modules.go` 已引入并装配 `dashboard.Module`。

- import 已存在。证据：`internal/app/modules.go:3-23`
- `dashboard.Module` 已进入 `app.Module` 选项列表。证据：`internal/app/modules.go:25-48`

结论：dashboard 模块已接入应用主装配。

## 7. 类型定义

响应 DTO 的 JSON tag 基本满足 snake_case 约束；单词字段使用小写单词，复合字段使用 snake_case。

- `Dashboard`：`agents`、`system`、`token_usage`、`uptime`。证据：`internal/module/dashboard/types.go:13-18`
- `AgentDetail`：`snapshot`、`turn_history`、`last_report`。证据：`internal/module/dashboard/types.go:20-24`
- `SystemInfo`：`started_at`、`build_version`、`build_commit`、`build_time`、`go_version`、`memory_alloc_bytes`、`agent_count` 等。证据：`internal/module/dashboard/types.go:33-46`
- `LogEntry`：`agent_id`、`thread_id`、`trace_id`、`event_type`、`tool_name`、`duration_ms` 等。证据：`internal/module/dashboard/types.go:69-85`
- `LogFilter`、`TurnRef` 也遵循同样风格。证据：`internal/module/dashboard/types.go:48-67`

需要单独说明的点：

- `AgentSnapshot`/`AgentOverview` 并未在 dashboard 包内重定义，而是直接 alias 到 `orchestration.AgentSnapshot`。证据：`internal/module/dashboard/types.go:10-11`
- `orchestration.AgentSnapshot` 的 tag 也是 snake_case：`parent_id`、`thread_id`、`last_report` 等。证据：`internal/sidecar/orch/orchestration/contract.go:52-62`

结论：DTO tag 风格统一，没有发现响应对象里的 camelCase tag。

## 8. V2 对照

### V2 能力面

V2 `dashrpc.Register` 一次注册了 12 个 `dashboard/*` 方法。

- `dashboard/agentStatus`
- `dashboard/dags`
- `dashboard/taskAcks`
- `dashboard/taskTraces`
- `dashboard/commandCards`
- `dashboard/prompts`
- `dashboard/sharedFiles`
- `dashboard/auditLogs`
- `dashboard/aiLogs`
- `dashboard/busLogs`
- `dashboard/skills`
- `dashboard/dagDetail`

证据：`go-agent-v2/internal/dashrpc/register.go:89-103`。

同时，V2 `ui/dashboard/get` 是一个“按页聚合壳”，会根据 `page` 选择不同的 dashboard 数据块；默认结果形状固定包含 `agents/dags/taskAcks/taskTraces/skills/commandCards/prompts/memory` 八类字段。证据：`go-agent-v2/internal/dashrpc/ui.go:34-38`、`go-agent-v2/internal/dashrpc/ui.go:48-58`、`go-agent-v2/internal/dashrpc/ui.go:61-77`。

### V3 覆盖情况

| V2 能力 | V3 状态 | 证据 |
| --- | --- | --- |
| `ui/dashboard/get` 分页聚合 | 形状已变化，且 `page` 被忽略 | V2：`go-agent-v2/internal/dashrpc/ui.go:34-38`、`go-agent-v2/internal/dashrpc/ui.go:61-77`；V3：`internal/module/dashboard/rpc.go:12-14`、`internal/module/dashboard/rpc.go:40-41`、`internal/module/dashboard/service.go:55-66`、`internal/module/dashboard/types.go:13-18` |
| `dashboard/agentStatus` | 无同名接口；agent 列表能力被并入 `GetDashboard` | V2：`go-agent-v2/internal/dashrpc/register.go:91-91`；V3：`internal/module/dashboard/service.go:55-66`、`internal/module/dashboard/service.go:132-141` |
| `dashboard/dags` / `taskAcks` / `taskTraces` / `commandCards` / `prompts` / `sharedFiles` / `skills` / `dagDetail` | dashboard 模块内均未覆盖 | V2：`go-agent-v2/internal/dashrpc/register.go:92-103`；V3：`internal/module/dashboard/rpc.go:38-52` |
| `dashboard/auditLogs` / `aiLogs` / `busLogs` | 被压缩为单一 `dashboard/logs`；数据源只有 `systemlog.Store` 与 `ailog.Store`，不再保留 V2 的 3 个独立方法名 | V2：`go-agent-v2/internal/dashrpc/register.go:98-100`；V3：`internal/module/dashboard/rpc.go:49-50`、`internal/module/dashboard/service.go:105-130`、`internal/module/dashboard/service.go:243-288` |
| `dashboard/agent/detail` | V3 新增；V2 暴露的 dashboard 方法清单中不包含该方法 | V3：`internal/module/dashboard/rpc.go:43-45`；V2：`go-agent-v2/internal/dashrpc/register.go:89-103` |
| `dashboard/system/info` | V3 新增；V2 暴露的 dashboard 方法清单中不包含该方法 | V3：`internal/module/dashboard/rpc.go:46-48`；V2：`go-agent-v2/internal/dashrpc/register.go:89-103` |

补充观察：

- V2 token usage 不在 `ui/dashboard/get`，而是在 `ui/state/get` 的 `tokenUsageByThread`。证据：`go-agent-v2/internal/dashboard/state_service.go:165-176`
- V3 虽然在 `Dashboard` DTO 中加入了 `TokenUsage`，但当前 `GetDashboard` 固定返回零值。证据：`internal/module/dashboard/types.go:13-18`、`internal/module/dashboard/types.go:26-31`、`internal/module/dashboard/service.go:60-64`

结论：V3 当前是新投影，不是 V2 `dashrpc + dashboard/` 的等价搬迁。

## 9. import 方向

范围内文件的 import 方向干净，没有 `provider/` 依赖。

- `contract.go` 只依赖 `context`。证据：`internal/module/dashboard/contract.go:3-3`
- `logs.go` 只依赖标准库 `sort`、`strings`。证据：`internal/module/dashboard/logs.go:3-6`
- `module.go` 只依赖 `go.uber.org/fx`。证据：`internal/module/dashboard/module.go:3-3`
- `rpc.go` 依赖 `context`、`strings`、`jrpc2/handler`、`internal/platform/rpc`。证据：`internal/module/dashboard/rpc.go:3-10`
- `service.go` 依赖 `internal/sidecar/orch/orchestration`、`internal/store/ailog`、`internal/store/systemlog`，没有 `provider/`。证据：`internal/module/dashboard/service.go:3-14`
- `types.go` 依赖 `internal/sidecar/orch/orchestration`，没有 `provider/`。证据：`internal/module/dashboard/types.go:3-8`

结论：dashboard 模块遵守“模块聚合 store/service，不直接 import provider/”的方向要求。

## 10. 与 UI State 关系

当前 dashboard 模块是独立聚合，不依赖 `uistate.Service`。

- 在 `internal/module/dashboard/` 内没有任何 `uistate` import；其 import block 只指向 `orchestration`、`store` 和 `platform/rpc`。证据：`internal/module/dashboard/rpc.go:3-10`、`internal/module/dashboard/service.go:3-14`、`internal/module/dashboard/types.go:3-8`
- `service` 构造函数也只注入 `orchestration.Service`、`systemlog.Store`、`ailog.Store`。证据：`internal/module/dashboard/service.go:24-29`、`internal/module/dashboard/service.go:42-53`

这带来两个直接结果：

1. `Dashboard.TokenUsage` 目前没有来自 UI state 的支撑，返回固定零值。证据：`internal/module/dashboard/service.go:60-64`
2. `AgentDetail.TurnHistory` 目前没有从 thread/turn/uistate 投影补齐，返回固定空切片。证据：`internal/module/dashboard/service.go:85-89`

对照 V2，可确认 token usage 的确属于 UI state 侧，而不是 V2 dashboard 聚合页本身。证据：`go-agent-v2/internal/dashboard/state_service.go:165-176`。

结论：当前实现是“独立 dashboard 聚合”，不是“依附 uistate 的 dashboard 投影”。

## 11. 函数复杂度

按源码跨度统计，范围内 top 3 最长函数如下。

| 排名 | 函数 | 行号 | 跨度 |
| --- | --- | --- | ---: |
| 1 | `(*service).GetLogs` | `internal/module/dashboard/service.go:105-130` | 26 |
| 1 | `(*service).appendSystemLogs` | `internal/module/dashboard/service.go:243-268` | 26 |
| 3 | `matchKeyword` | `internal/module/dashboard/logs.go:46-69` | 24 |

紧随其后的是：

- `(*service).GetAgentDetail`，`internal/module/dashboard/service.go:68-90`，23 行
- `loadBuildMetadata`，`internal/module/dashboard/service.go:162-183`，22 行

对照守卫阈值 `MaxFuncLines=80`，当前 top 3 都明显低于阈值。证据：`internal/archtest/guardlib.go:17-24`。

## 12. 编译守卫

本次执行结果如下。

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过，输出 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.341s`

守卫入口证据：

- 代码体量守卫：`internal/archtest/code_size_guard_test.go:10-24`
- 依赖方向守卫：`internal/archtest/dependency_direction_test.go:32-177`

## 总评

当前 `dashboard` 模块在 V3 中已经“接得上、跑得通、守得住”，但它的功能边界更接近一个新的轻量 read-model，而不是 V2 dashboard 体系的等价迁移。若目标是“P7w2 完成 dashboard 迁移”，当前最关键的审查结论不是编译或注册问题，而是产品/契约层覆盖仍明显不足，尤其是：

1. `ui/dashboard/get` 的 `page` 语义已经丢失。
2. V2 多数 `dashboard/*` 能力尚未迁入。
3. DTO 中已有的 `TokenUsage`、`TurnHistory` 还没有真实投影来源。
