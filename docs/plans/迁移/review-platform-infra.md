# Platform 基础设施总审查

审查范围:
- `internal/platform/bus/`
- `internal/platform/statemachine/`
- `internal/platform/runner/`
- `internal/platform/config/`
- `internal/platform/db/`
- `internal/platform/shared/`

审查方式:
- 只读审查
- 使用 LSP `text_search / workspace_symbol / references(compact) / call_hierarchy / read_file / document_symbol`

## 结论摘要

1. `config` 当前只有“环境变量 + 默认值”，没有“文件”来源；因此实际优先级不是“环境变量 > 文件 > 默认值”，而是“环境变量 > 默认值”。证据: `internal/platform/config/config.go:15-40`。
2. bus 发布/订阅框架已经成形，但真正有业务消费者的事件很少。运行时有非日志消费方的只有 `agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted`；其余大多只有 `LogSink` 在听。证据: `internal/platform/bus/sink.go:43-87`、`internal/sidecar/orch/orchestration/module.go:25-53`、`internal/platform/rpc/push.go:75-92`。
3. 严格意义上的“听了没人发”事件存在: `TurnStalled`、`TurnResumed`、全部 `Task*`、全部 `UI*`。这些类型在 `LogSink` 有订阅，但在 `internal/*` 没有发布源。证据: `internal/platform/bus/sink.go:51-87`，以及全仓 `text_search` 无对应构造/发布命中。
4. 状态机已经去掉 force fallback；`fireOrForceLocked` 只做 `FireCtx`，失败时返回 `illegal state transition`，不再直接改状态。证据: `internal/sidecar/orch/orchestration/service.go:257-279`。
5. 状态机声明表包含 10 状态、11 触发器、32 条转换，但运行时只实际 fire 了其中 9 个触发器；`TriggerUserInputRequested` / `TriggerUserInputResolved` 没有任何 fire 点，`StateAwaitingUserInput` 目前不可达。证据: `internal/dto/agent/state.go:51-112`，`internal/sidecar/orch/orchestration/**/*.go` 对 user_input 触发器全仓无命中。
6. `db.WithTx` 被 `sqlc -> store -> module service` 正确串起来了，但连接池配置非常薄，只设置了 `MaxConns=4` 并在 `OnStart` 做一次 `Ping`，没有把 `timeouts.go` 里的 DB/health 配置接进去。证据: `internal/platform/db/module.go:19-39`、`internal/platform/config/timeouts.go:8-17`。
7. `platform/shared` 里 `Retry` 有真实调用，但 `RequireNonEmpty` 当前零引用；`idgen.go` 又与 `internal/dto/shared/ids.go` 存在重复实现，ID 生成权威被拆成两份。证据: `internal/platform/shared/*.go`、`internal/dto/shared/ids.go:1-14`。

## 15 个维度总表

| # | 维度 | 结论 |
| --- | --- | --- |
| 1 | 文件清单与行数 | 通过。26 个文件全部 `<=400` 行，最大文件 `bus_test.go=199` 行。 |
| 2 | bus 泛型 API | 部分通过。`Route[T]` / `ResilientSubscribe[T]` 正常；`Projector[S,E]` 主路径正确，但 `State()` nil 不安全，且整套 API 运行时使用面很薄。 |
| 3 | bus 事件发布面 | 部分通过。发布源清晰，但多数事件只有日志消费。 |
| 4 | bus 事件订阅面 | 部分通过。订阅面集中在 `LogSink`、orchestration、rpc push；另有未接入的泛型桥接 API。 |
| 5 | bus 孤儿检测 | 不通过。存在明确“听了没人发”的事件族。 |
| 6 | EventHeader 嵌入链 | 通过。各嵌入链内无重复字段；但 header 结构是分叉树，不是单一 9 层线性链。 |
| 7 | statemachine 状态表 | 部分通过。声明表完整列出了 10 状态/11 触发器，但 `awaiting_user_input` 链路未接入运行时。 |
| 8 | statemachine 严格性 | 通过。force fallback 已移除。 |
| 9 | runner RunGroup | 部分通过。oklog/run 模式基本正确，但 runner 若意外返回 `nil`，可能静默拖停整组。 |
| 10 | config 来源 | 不通过。没有文件层；`LogLevel` 也未被消费。 |
| 11 | db pool | 部分通过。能建池/能健康探测，但超时、健康检查周期、生命周期配置缺失。 |
| 12 | db tx | 通过。store 使用方式正确；仅 API 本身缺少 panic-safe rollback。 |
| 13 | shared 工具 | 部分通过。`Retry` 可用但极简；`validation` 未接入；`idgen` 重复。 |
| 14 | fx 注册 | 部分通过。`bus/config/db` 完整；`runner/statemachine` 是空壳 module；`shared` 无 module。 |
| 15 | import 方向 | 通过。审查范围内只看到 `db -> config`，无反向/循环依赖。 |

## 1. 文件清单与行数

### `internal/platform/bus/` 总计 12 文件 / 705 行

| 文件 | 行数 |
| --- | ---: |
| `bus.go` | 24 |
| `bus_test.go` | 199 |
| `emitters.go` | 65 |
| `module.go` | 36 |
| `projection.go` | 44 |
| `resilient.go` | 30 |
| `router.go` | 38 |
| `sink.go` | 107 |
| `subscription.go` | 22 |
| `subscription_test.go` | 38 |
| `typed.go` | 31 |
| `typed_test.go` | 71 |

### `internal/platform/statemachine/` 总计 2 文件 / 87 行

| 文件 | 行数 |
| --- | ---: |
| `factory.go` | 81 |
| `module.go` | 6 |

### `internal/platform/runner/` 总计 2 文件 / 66 行

| 文件 | 行数 |
| --- | ---: |
| `group.go` | 60 |
| `module.go` | 6 |

### `internal/platform/config/` 总计 3 文件 / 69 行

| 文件 | 行数 |
| --- | ---: |
| `config.go` | 41 |
| `module.go` | 6 |
| `timeouts.go` | 22 |

### `internal/platform/db/` 总计 4 文件 / 82 行

| 文件 | 行数 |
| --- | ---: |
| `errors.go` | 13 |
| `module.go` | 41 |
| `pool.go` | 6 |
| `tx.go` | 22 |

### `internal/platform/shared/` 总计 3 文件 / 59 行

| 文件 | 行数 |
| --- | ---: |
| `idgen.go` | 15 |
| `retry.go` | 29 |
| `validation.go` | 15 |

结论:
- 全部文件均未超过 400 行。
- 范围内最大的文件是 `internal/platform/bus/bus_test.go`，199 行。

## 2. bus 泛型 API

### `Route[T]`

位置: `internal/platform/bus/router.go:18-23`

结论:
- 实现就是对 `event.Subscribe(dispatcher, handler)` 的 nil-safe 薄封装，语义正确。
- `Router` 本身不做 event type 路由判定，只是收集 `CancelFunc`。注释和实现一致，但命名偏“重”。
- `NewRouter(_ *event.Dispatcher)` 根本没有使用传入的 dispatcher。证据: `internal/platform/bus/router.go:14-16`。
- `Route()` 的唯一引用是 `Projector.Bind()`。证据: `references(compact)` 只命中 `internal/platform/bus/projection.go:42`。

### `ResilientSubscribe[T]`

位置: `internal/platform/bus/resilient.go:10-29`

结论:
- 语义正确，但它是“panic 屏蔽订阅”，不是“重试订阅”。
- `recoverCall()` 的写法能正确拿到 panic 值并写日志，但不附带 stack trace。
- 真实运行时调用点只有两处:
  - `internal/sidecar/orch/orchestration/module.go:33-44`
  - `internal/platform/rpc/push.go:82-90`

### `Projector[S,E]`

位置: `internal/platform/bus/projection.go:10-43`

结论:
- `Apply()` 读写锁和 reduce 逻辑正确，`reduce == nil` 时会退化为 identity reducer。
- `Bind()` 正确通过 `Route(dispatcher, p.Apply)` 订阅事件。
- 唯一明显问题是 `State()` 没有 nil guard，和 `Apply()` / `Bind()` 的 nil-safe 风格不一致。证据: `internal/platform/bus/projection.go:32-36`。
- `NewProjector` 在运行时代码没有命中，当前更像备用工具而非实际基础设施。

### 额外观察

- `TypedEmitter[T]` 的 `Emit()` / `On()` 实现正常，nil-safe 处理完整。证据: `internal/platform/bus/typed.go:18-30`。
- 但 `NewTypedEmitter` 的运行时调用点为 0；只有测试使用。证据: 全仓 `text_search("NewTypedEmitter[")` 仅命中 `typed_test.go`。

## 3. bus 事件发布面

### 直接/间接发布入口

1. `internal/sidecar/orch/orchestration/events.go:13-64`
   - 直接 `event.Publish(...)`
   - 发布: `StateChanged`、`AgentLaunched`、`AgentStopped`、`AgentRecovering`、`AgentFailed`

2. `internal/platform/rpc/approval_events.go:15-35`
   - 直接 `event.Publish(...)`
   - 发布: `ToolApprovalRequested`、`ToolApprovalResolved`

3. `internal/provider/unified/event_map.go:43-64`
   - 所有 provider translator 返回的 typed event 最终都在这里统一 `event.Publish(d.bus, typedEv)`
   - translator 来源:
     - `internal/provider/claudecli/event_map.go:35-102`
     - `internal/provider/codexapp/event_map.go:39-147`

4. `internal/module/workspace/service.go:43-53` + `internal/module/workspace/service_helpers.go:220-284`
   - 通过 `bus.NewEmitter[T](dispatcher)` 创建闭包，再在 helper 中实际 emit
   - 发布: `WorkspaceRunCreated`、`WorkspaceRunStatusChanged`、`WorkspaceRunMerged`、`WorkspaceRunMergeError`、`WorkspaceRunAborted`

### 按事件族归类

#### 有发布且有非日志消费方

- `agentdto.StateChanged`
  - 发布: `internal/sidecar/orch/orchestration/events.go:13-23`、`internal/provider/codexapp/event_map.go:47-53`
  - 订阅: `internal/platform/rpc/push.go:82-84`、`internal/platform/bus/sink.go:44`

- `turndto.TurnStarted`
  - 发布: `internal/provider/claudecli/event_map.go:57-58`、`internal/provider/codexapp/event_map.go:77-79`
  - 订阅: `internal/sidecar/orch/orchestration/module.go:33-37`、`internal/platform/rpc/push.go:85-87`、`internal/platform/bus/sink.go:52`

- `turndto.TurnCompleted`
  - 发布: `internal/provider/claudecli/event_map.go:76-81`、`internal/provider/codexapp/event_map.go:80-85`
  - 订阅: `internal/sidecar/orch/orchestration/module.go:38-44`、`internal/platform/rpc/push.go:88-90`、`internal/platform/bus/sink.go:53`

#### 有发布，但只有 `LogSink` 在消费

- agent: `AgentLaunched`、`AgentStopped`、`AgentRecovering`、`AgentFailed`
- turn: `TurnInterrupted`、`TurnInputReceived`、`TurnOutputDelta`
- tool: `ToolCallBegin`、`ToolCallEnd`、`ToolApprovalRequested`、`ToolApprovalResolved`
- workspace: `WorkspaceRunCreated`、`WorkspaceRunStatusChanged`、`WorkspaceRunMerged`、`WorkspaceRunAborted`、`WorkspaceRunMergeError`

说明:
- 这些事件不是“完全没人听”，因为 `LogSink` 统一订阅了它们。
- 但如果把“日志镜像”排除掉，它们没有业务消费者。

### 多发布源事件

以下 event type 存在多个生产方，消费者看不出“来源层”:
- `StateChanged`: orchestration 内部状态机 + codexapp raw event translator
- `AgentLaunched` / `AgentStopped` / `AgentFailed`: orchestration + provider translator
- `AgentRecovering`: orchestration + codexapp translator
- `ToolApprovalRequested` / `ToolApprovalResolved`: rpc approval manager + codexapp translator

风险:
- 同一 DTO 类型混合“内部状态变化”和“provider 原始事件翻译”两类语义，后续消费者若默认“一种事件类型只有一个权威生产者”，会难以去重或排序。

## 4. bus 事件订阅面

### 真实运行时订阅点

1. `internal/platform/bus/sink.go:43-87`
   - `LogSink` 订阅 28 个 typed event
   - 本质是“总线镜像到日志”

2. `internal/sidecar/orch/orchestration/module.go:25-53`
   - `TurnStarted -> BindActiveTurnID`
   - `TurnCompleted -> CompleteTurn`

3. `internal/platform/rpc/push.go:75-92`
   - `StateChanged`、`TurnStarted`、`TurnCompleted` 推给所有 rpc 客户端

### 已定义但未接入的订阅 API

- `internal/platform/rpc/push.go:60-73`
  - `BindEventToNotify[T]` 有实现，但运行时代码没有任何调用点。

- `internal/platform/bus/router.go:18-23`
  - `Route[T]` 只有 `Projector.Bind()` 一处引用；`Projector` 又没有运行时调用点。

## 5. bus 孤儿检测

### 严格意义上的“发了没人听”

- 没有发现完全无人订阅的发布源。
- 原因不是消费面充分，而是 `LogSink` 覆盖了几乎全部已知 event type。

### 严格意义上的“听了没人发”

下列事件在 `internal/platform/bus/sink.go` 有订阅，但在 `internal/*` 没有任何发布源:

- `turndto.TurnStalled`
- `turndto.TurnResumed`
- `taskdto.TaskDagCreated`
- `taskdto.TaskNodeStatusChanged`
- `taskdto.TaskWakeupDispatched`
- `taskdto.TaskWakeupCompleted`
- `uidto.UIProjectionUpdated`
- `uidto.UITimelineAppended`
- `uidto.UITokensUpdated`

证据:
- 订阅位置: `internal/platform/bus/sink.go:54-56,69-72,84-86`
- 全仓对这些类型的构造搜索均无命中。

### “只有日志消费”的弱 orphan

如果把 `LogSink` 排除，下列事件实际没有业务消费方:

- `AgentLaunched`
- `AgentStopped`
- `AgentRecovering`
- `AgentFailed`
- `TurnInterrupted`
- `TurnInputReceived`
- `TurnOutputDelta`
- `ToolCallBegin`
- `ToolCallEnd`
- `ToolApprovalRequested`
- `ToolApprovalResolved`
- 全部 `WorkspaceRun*`

## 6. EventHeader 嵌入链

位置: `internal/dto/shared/event.go:41-114`

### 结构事实

代码里不是单一“9 层线性 header 链”，而是多条分支链:

- agent 链: `EventHeader -> AgentHeader -> AgentSessionHeader`
- turn 链: `EventHeader -> AgentHeader -> TurnHeader`
- tool 链: `EventHeader -> AgentHeader -> TurnHeader -> ToolCallHeader -> ToolApprovalHeader`
- task 链: `EventHeader -> TaskDAGHeader -> TaskNodeHeader -> TaskWakeupHeader`
- workspace 链: `EventHeader -> WorkspaceRunHeader`
- ui 链: `EventHeader -> UIProjectionHeader -> UITurnHeader`

### 重复字段检查

结论:
- 每一条嵌入链内部都没有重复字段名。
- 存在跨分支的同名语义字段，但不发生在同一链内:
  - `ThreadID`: `AgentHeader` 与 `UIProjectionHeader`
  - `DagKey`: `TaskDAGHeader` 与 `WorkspaceRunHeader`

### 额外观察

- `TurnHeader` 嵌入的是 `AgentHeader`，不是 `AgentSessionHeader`；所以 turn/tool 事件天然不带 `SessionID`。
- 这不构成“重复字段”问题，但会让 turn/tool 事件无法仅靠 header 关联 agent session。

## 7. statemachine 状态表

### 声明面

位置:
- 状态/触发器/转换定义: `internal/dto/agent/state.go:8-112`
- 从 DTO 定义生成 `StateConfig`: `internal/sidecar/orch/orchestration/helpers.go:16-32`
- 装配到 `machineCfg`: `internal/sidecar/orch/orchestration/service.go:87-90`

统计:
- 状态 10 个
- 触发器 11 个
- 转换 32 条

### 运行时触发面

`call_hierarchy(incoming)` 显示 `fireOrForceLocked` 的调用来源有 9 个:
- `reconcileReadyStateLocked`
- `startTurnExecution`
- `normalizeRecoveryState`
- `stopAgentLocked`
- `SubmitTurn`
- `startProcessLocked`
- `claimTurnWork`
- `CompleteTurn`
- `handleProcessExitTransition`

### 覆盖结论

1. 对“运行时实际 fire 的触发器”来说，声明表是覆盖的:
   - `launch_succeeded`
   - `launch_failed`
   - `turn_enqueued`
   - `turn_accepted`
   - `turn_completed`
   - `turn_aborted`
   - `recover_requested`
   - `stop_requested`
   - `process_exited`

2. 对“声明存在的全部触发器”来说，不完整:
   - `TriggerUserInputRequested`
   - `TriggerUserInputResolved`
   在 orchestration 运行时代码没有任何 fire 点。

3. 因为 2)，`StateAwaitingUserInput` 目前只是声明态，不是运行态。
   - 该状态只在 `internal/platform/rpc/approval_support.go:27` 被当作 approval payload 默认值字符串使用，不会驱动状态机进入该状态。

结论:
- 状态表在“声明层面”完整。
- 状态表在“运行时闭环”层面部分缺口，最明显的是 approval/user-input 状态链路未接入。

## 8. statemachine 严格性

### `fireOrForceLocked` 是否已去掉 force fallback

位置: `internal/sidecar/orch/orchestration/service.go:257-279`

结论:
- 已去掉。
- 当前逻辑:
  - 调 `fireAndPublishLocked()`
  - 若 `FireCtx` 报错，则读取 `AllowedTriggers()` 拼出错误并直接返回
  - 不再有 `forceStateLocked()` 之类的旁路状态覆写

这意味着:
- 声明式状态表重新成为运行时唯一权威。
- 非法转换不再“悄悄成功”，而是显式失败。

## 9. runner RunGroup

位置:
- 实现: `internal/platform/runner/group.go:18-59`
- 装配: `internal/app/runner.go:26-60`

### 正确点

- 使用 `oklog/run.Group` 的 execute/interrupt 双函数模式是对的。
- 有三个 actor 类别:
  - root context actor
  - OS signal actor
  - 每个注入的 `Runner`

- interrupt 统一只做 `cancel()`，符合 `run.Group` 的常见用法。

### 风险点

- `RunGroup()` 返回的是“第一个退出 actor 的返回值”。
- `BindRuntime()` 只有在 `err != nil && err != context.Canceled` 时才触发 `Shutdowner.Shutdown()`。证据: `internal/app/runner.go:35-43`。
- 这意味着如果某个 runner 在未收到 cancel 的情况下“正常返回 nil”，整组会被取消，但 app 不会主动 shutdown，可能出现“后台 runner 已全停，fx 进程还活着”的静默半死状态。

结论:
- execute/interrupt 模式本身正确。
- 进程级生命周期语义仍偏脆，尤其是“runner 提前 nil 返回”的场景。

## 10. config 来源

位置: `internal/platform/config/config.go:15-40`

### 实际优先级

- `DATABASE_URL`: `envOr("DATABASE_URL", <hardcoded default>)`
- `RPC_ADDR`: `envOr("RPC_ADDR", "127.0.0.1:8080")`
- `LOG_LEVEL`: `envOr("LOG_LEVEL", "info")`
- `PROJECT_ROOT`: `PROJECT_ROOT env`，否则 `os.Getwd()`

### 结论

- 没有文件加载逻辑。
- 没有优先级合并逻辑。
- 没有配置文件路径、解析器、反序列化、override 层。

因此:
- 实际上是“环境变量 > 默认值”
- `PROJECT_ROOT` 是“环境变量 > 当前工作目录”
- 用户要求的“环境变量 / 文件 / 默认值优先级”在代码中并不存在

### timeouts.go 接入情况

位置: `internal/platform/config/timeouts.go:8-21`

定义的常量:
- `TurnTimeout`
- `LaunchTimeout`
- `ShutdownTimeout`
- `HealthCheckPeriod`
- `StallDetectDelay`
- `DBQueryTimeout`
- `RPCRequestTimeout`
- `InterruptSettleTimeout`

真实命中:
- `WithRPCRequestTimeout()` 被 `internal/module/skill/exec.go:57-59` 使用
- `InterruptSettleTimeout` 被 `internal/module/turn/service.go:190-205` 使用

其余常量当前没有运行时引用。

额外观察:
- `Config.LogLevel` 只有定义，没有消费方。

## 11. db pool

位置: `internal/platform/db/module.go:19-39`

### 当前实现

- `pgxpool.ParseConfig(cfg.DatabaseURL)`
- 强行设置 `poolCfg.MaxConns = 4`
- `pgxpool.NewWithConfig(context.Background(), poolCfg)`
- `OnStart` 做一次 `pool.Ping(ctx)`
- `OnStop` 做 `pool.Close()`

### 结论

优点:
- 最小闭环成立，Fx 生命周期也补上了健康检查和关闭。

缺口:
- 没有 `MinConns`
- 没有 `MaxConnLifetime`
- 没有 `MaxConnIdleTime`
- 没有 `HealthCheckPeriod`
- 没有把 `DBQueryTimeout` 统一下沉到 query 层
- 没有任何从 `timeouts.go` 注入 pgxpool 的逻辑

因此:
- “能用”
- 但还不能算“配置、超时、健康检查已完整建模”

## 12. db tx

位置:
- tx API: `internal/platform/db/tx.go:11-21`
- sqlc 适配: `internal/store/sqlc/db.go:51-58`
- store 适配:
  - `internal/store/taskdag/store.go:15-19`
  - `internal/store/workspace/store.go:15-19`
- 业务调用:
  - `internal/sidecar/orch/orchestration/dag.go:20-34`
  - `internal/module/workspace/service_helpers.go:166-179`

### 结论

- store 对事务 API 的使用是正确的。
- 模式是:
  - `platformdb.WithTx(ctx, pool, func(tx pgx.Tx) error {...})`
  - `sqlc.NewWithTx(tx)`
  - `store.WithTx(ctx, func(txStore Store) error {...})`

- 事务边界从 platform/db 一直贯通到 module service，没有出现“事务内仍误用 pool-backed queries”的问题。

### 保留风险

- `WithTx()` 只处理 error，不处理 panic；如果 `fn(tx)` panic，当前实现不会显式 rollback。证据: `internal/platform/db/tx.go:11-21`。

## 13. shared 工具

### `Retry`

位置: `internal/platform/shared/retry.go:9-28`

结论:
- 实现可用。
- 当前调用点只有 codexapp:
  - `internal/provider/codexapp/recovery.go:39-43`
  - `internal/provider/codexapp/transport.go:155-167`

限制:
- 无 jitter
- 无 max delay cap
- 使用 `math.Pow` 做指数退避，策略极简

### `RequireNonEmpty`

位置: `internal/platform/shared/validation.go:9-14`

结论:
- 当前零引用。
- 属于尚未接入的公用校验工具。

### `NewID`

位置:
- `internal/platform/shared/idgen.go:10-14`
- `internal/dto/shared/ids.go:10-14`

结论:
- 两份实现完全重复。
- 当前运行时是双轨使用:
  - `internal/platform/shared.NewID(...)` 被 claudecli 使用
  - `internal/dto/shared.NewID(...)` 被 thread/turn 使用

这说明:
- ID 生成并没有收敛到单一平台工具
- `platform/shared/idgen.go` 不是唯一权威

## 14. fx 注册

### `bus/module.go`

位置: `internal/platform/bus/module.go:10-35`

结论:
- 完整。
- 提供 dispatcher、各类 emitters、`LogSink`
- 还补了生命周期关闭逻辑

### `config/module.go`

位置: `internal/platform/config/module.go:5`

结论:
- 完整但很薄，只 `Provide(New)`。

### `db/module.go`

位置: `internal/platform/db/module.go:13-39`

结论:
- 完整。
- `Provide(NewPool)` + `Invoke(registerLifecycle)`

### `runner/module.go`

位置: `internal/platform/runner/module.go:5`

结论:
- 空壳 module。
- 运行时真正可用的 API 是导出的 `RunGroup()` 和 `Runner` interface，而不是 Fx provider。

### `statemachine/module.go`

位置: `internal/platform/statemachine/module.go:5`

结论:
- 同样是空壳 module。
- 真正起作用的是导出的 `New()` 和 `AllowedTriggers()`；Fx 层没有提供任何状态机实例或配置。

### `shared`

结论:
- 没有 `module.go`。
- 从性质看它是纯函数工具包，这并不奇怪；但如果维度要求“每个子包都有 module.go”，那么 `shared` 不满足。

### app 装配面

位置: `internal/app/modules.go:23-44`

结论:
- `config.Module`、`db.Module`、`bus.Module`、`platformrunner.Module`、`statemachine.Module` 都已挂入总装配。
- 但 `runner/statemachine` 这两个 module 只是占位，不承担真实 provider 职责。

## 15. import 方向

### 审查范围内平台包相互依赖

全仓命中只有:
- `internal/platform/db/module.go` -> `internal/platform/config`

未发现:
- `bus -> rpc`
- `config -> db`
- `runner -> db/config/bus`
- `statemachine -> 其他 platform 包`
- `shared -> 其他 platform 包`

### 范围外补充

范围外但能说明方向是单向的:
- `internal/platform/rpc/push.go` -> `internal/platform/bus`
- `internal/platform/rpc/module.go` -> `internal/platform/config`

这仍然是“上层平台能力依赖下层平台能力”的方向，没有回流。

### 额外证据

`internal/archtest/dependency_direction_test.go:90-95` 已有规则:
- `platform` 目录不能 import `module`

结论:
- 审查范围内依赖方向是单向的。
- 当前没有发现 platform 子包之间的循环依赖或明显越层。

## 最终判断

### 明确通过

- 文件规模控制
- `fireOrForceLocked` 已移除 force fallback
- store 对事务 API 的接入方式
- platform 包之间的依赖方向
- EventHeader 链内零重复字段

### 明确缺口

- config 没有文件来源层
- `TurnStalled` / `TurnResumed` / `Task*` / `UI*` 为“听了没人发”
- `awaiting_user_input` 状态链路未接入运行时
- db pool 未消费 timeout/health 配置
- `LogLevel` 与多数 timeout 常量未接入
- `platform/shared` 内存在未使用工具和重复 ID 生成实现

### 建议优先级

P0:
- 先补 `config` 文件来源和来源优先级
- 明确 bus 里哪些事件是“真实业务事件”，哪些只是 DTO 预留；把 orphan 事件清掉或接上生产方
- 把 approval / user-input 状态真正接入 orchestration 状态机

P1:
- 给 db pool 增加生命周期、空闲、健康检查、query timeout 配置
- 收敛 `NewID()` 到单一实现
- 决定 `runner/statemachine` 的空 module 是保留占位还是删掉

P2:
- 若 `LogSink` 不算业务消费，则为高价值事件补真实订阅方，避免 bus 成为“只写日志的广播层”
