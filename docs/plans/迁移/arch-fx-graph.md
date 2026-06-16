# fx DI 图完整性 + 循环依赖检测

## 范围与方法

- 主图以 `internal/archtest/fx_graph_test.go:11-14` 的 `fx.ValidateApp(app.Module)` 为准；也就是只分析 `internal/app/modules.go:23-44` 拼出的 `app.Module`。
- 桌面增量单独备注：`internal/app/app.go:68-75` 会在 `app.Module` 之外额外叠加 `uiwails.Module`。
- 验证方式：
  - 只用 LSP `text_search` / `read_file` 建图与反查消费者。
  - 额外运行了 `go test ./internal/archtest -run TestFxValidateApp`，结果通过。

## 结论摘要

- `fx.In` 标记的依赖缺失数：`0`
- `optional:"true"` 依赖数：`9`
- value group 闭环：
  - `group:"rpc_handlers"`: `5` 个生产者，`1` 个消费者
  - `group:"runners"`: `2` 个生产者，`1` 个消费者
  - `group:"drivers"`: `2` 个生产者，`1` 个消费者
- 悬空 provider：`19`
  - `5` 个 bus emitters
  - `14` 个 store provider
- 循环依赖：未发现 `A -> B -> C -> A` 路径；LSP 图与 `fx.ValidateApp(app.Module)` 结果一致

## 1. fx.Provide 完整性：悬空 provider

判定口径：provider 输出类型若没有被任何 constructor 参数、`fx.In` 字段、`fx.Invoke` 参数、`fx.As(...)` 目标或 value-group 消费点引用，则记为悬空。可选依赖也算“有消费者”。

其余 provider 都能闭环；当前主图里的悬空 provider 只有下表这 `19` 个。

| Provider | 输出类型 | 证据 | 结论 |
| --- | --- | --- | --- |
| `bus.NewAgentEmitters` | `*bus.AgentEmitters` | 定义：`internal/platform/bus/emitters.go:42-44`；全仓仅命中定义与 `internal/platform/bus/module.go:14` | 悬空 |
| `bus.NewTurnEmitters` | `*bus.TurnEmitters` | 定义：`internal/platform/bus/emitters.go:46-48`；全仓仅命中定义与 `internal/platform/bus/module.go:15` | 悬空 |
| `bus.NewToolEmitters` | `*bus.ToolEmitters` | 定义：`internal/platform/bus/emitters.go:50-52`；全仓仅命中定义与 `internal/platform/bus/module.go:16` | 悬空 |
| `bus.NewTaskEmitters` | `*bus.TaskEmitters` | 定义：`internal/platform/bus/emitters.go:54-56`；全仓仅命中定义与 `internal/platform/bus/module.go:17` | 悬空 |
| `bus.NewUIEmitters` | `*bus.UIEmitters` | 定义：`internal/platform/bus/emitters.go:62-64`；全仓仅命中定义与 `internal/platform/bus/module.go:19` | 悬空 |
| `agentstatus.NewStore` | `agentstatus.Store` | 定义：`internal/store/agentstatus/store.go:14-16`；全仓仅命中该包自身与 `internal/store/module.go:30` | 悬空 |
| `ailog.NewStore` | `ailog.Store` | 定义：`internal/store/ailog/store.go:14-16`；全仓仅命中该包自身与 `internal/store/module.go:31` | 悬空 |
| `auditlog.NewStore` | `auditlog.Store` | 定义：`internal/store/auditlog/store.go:14-16`；全仓仅命中该包自身与 `internal/store/module.go:32` | 悬空 |
| `buslog.NewStore` | `buslog.Store` | 定义：`internal/store/buslog/store.go:14-16`；全仓仅命中该包自身与 `internal/store/module.go:34` | 悬空 |
| `cwdlock.NewStore` | `cwdlock.Store` | 定义：`internal/store/cwdlock/store.go:14-16`；全仓仅命中该包自身与 `internal/store/module.go:36` | 悬空 |
| `dbquery.NewStore` | `dbquery.Store` | 定义：`internal/store/dbquery/store.go:14`；全仓仅命中该包自身与 `internal/store/module.go:37` | 悬空 |
| `interaction.NewStore` | `interaction.Store` | 定义：`internal/store/interaction/store.go:14`；全仓仅命中该包自身与 `internal/store/module.go:38` | 悬空 |
| `prompt.NewStore` | `prompt.Store` | 定义：`internal/store/prompt/store.go:15`；全仓仅命中该包自身与 `internal/store/module.go:39` | 悬空 |
| `sharedfile.NewStore` | `sharedfile.Store` | 定义：`internal/store/sharedfile/store.go:14-16`；全仓仅命中该包自身与 `internal/store/module.go:40` | 悬空 |
| `systemlog.NewStore` | `systemlog.Store` | 定义：`internal/store/systemlog/store.go:14-16`；全仓仅命中该包自身与 `internal/store/module.go:41` | 悬空 |
| `taskack.NewStore` | `taskack.Store` | 定义：`internal/store/taskack/store.go:14`；全仓仅命中该包自身与 `internal/store/module.go:42` | 悬空 |
| `tasktrace.NewStore` | `tasktrace.Store` | 定义：`internal/store/tasktrace/store.go:14`；全仓仅命中该包自身与 `internal/store/module.go:44` | 悬空 |
| `topologyapproval.NewStore` | `topologyapproval.Store` | 定义：`internal/store/topologyapproval/store.go:14`；全仓仅命中该包自身与 `internal/store/module.go:46` | 悬空 |
| `uipreference.NewStore` | `uipreference.Store` | 定义：`internal/store/uipreference/store.go:15-17`；全仓仅命中该包自身与 `internal/store/module.go:47` | 悬空 |

反向看，当前确实闭环的关键节点包括：

- `*sqlc.Queries` 被所有 store provider 消费，见 `internal/store/sqlc/db.go:22-24` 与各 `internal/store/*/store.go`
- `commandcardstore.Store` 被 `skill.newService` 消费，见 `internal/module/skill/module.go:20-26`
- `threadstore.Store` 被 `thread.NewService` 与 `unified.NewSessionResolver` 消费，见 `internal/module/thread/service.go:40-47`、`internal/provider/unified/session_resolver.go:19-21`
- `bindingstore.Store` 被 `thread.NewService` 消费，见 `internal/module/thread/service.go:40-47`
- `taskdag.Store` 被 `orchestration.NewService` 消费，见 `internal/sidecar/orch/orchestration/service.go:80-102`
- `storeworkspace.Store` 与 `*bus.WorkspaceEmitters` 被 `workspace.NewService` 消费，见 `internal/module/workspace/service.go:49-59`

## 2. fx.In 标记依赖：缺失依赖检查

当前 `fx.In` 结构体只有 `5` 个：`app.runtimeParams`、`rpc.Params`、`rpc.serverParams`、`unified.RegistryParams`、`wails.applicationParams`。

缺失依赖列表：空。

逐项核对如下：

| `fx.In` 消费点 | 依赖 | Provider | 结果 |
| --- | --- | --- | --- |
| `internal/app/runner.go:19-26` `runtimeParams` | `Logger *slog.Logger` | `internal/app/app.go:15-19` `NewLogger` | 满足 |
| `internal/app/runner.go:19-26` `runtimeParams` | `Runners []platformrunner.Runner \`group:"runners"\`` | `AsRPCRunner` + `NewRunnerActor`，见下文 group 章节 | 满足 |
| `internal/app/runner.go:19-26` `runtimeParams` | `Shutdowner fx.Shutdowner` | fx 内建 | 满足 |
| `internal/app/runner.go:19-26` `runtimeParams` | `Lifecycle *uiwails.WailsLifecycle \`optional:"true"\`` | 基础图可缺省；桌面增量由 `internal/ui/wails/lifecycle.go:43-51` 提供 | 满足 |
| `internal/platform/rpc/module.go:29-34` `Params` | `Logger *slog.Logger` | `NewLogger` | 满足 |
| `internal/platform/rpc/module.go:29-34` `Params` | `Config *config.Config` | `internal/platform/config/config.go:15-22` `New` | 满足 |
| `internal/platform/rpc/module.go:42-48` `serverParams` | `Logger *slog.Logger` | `NewLogger` | 满足 |
| `internal/platform/rpc/module.go:42-48` `serverParams` | `Config *config.Config` | `config.New` | 满足 |
| `internal/platform/rpc/module.go:42-48` `serverParams` | `Handlers []handler.Map \`group:"rpc_handlers"\`` | 5 个 `rpc.HandlerMapResult` producer，见下文 group 章节 | 满足 |
| `internal/provider/unified/module.go:13-17` `RegistryParams` | `Drivers []contract.DriverFactory \`group:"drivers"\`` | 2 个 driver factory producer，见下文 group 章节 | 满足 |
| `internal/ui/wails/module.go:62-70` `applicationParams` | `Logger` / `Config` / `Binding *App` / `Service application.Service` / `Lifecycle *WailsLifecycle` | 仅在 `app.Module + uiwails.Module` 桌面图里存在，LSP 静态闭合 | 满足 |

结论：当前没有“`fx.In` 字段是强依赖但根图没有 provider”的情况。

## 3. `optional:"true"` 依赖与 nil 影响面

### 3.1 `app.runtimeParams`

| 位置 | 可选依赖 | 当前状态 | 运行时为 nil 时的影响面 |
| --- | --- | --- | --- |
| `internal/app/runner.go:25` | `*uiwails.WailsLifecycle` | 在 `newFXApp` 基础图里为 `nil`；仅 `newDesktopFXApp` 会通过 `uiwails.Module` 提供，见 `internal/app/app.go:68-75` | `BindRuntime` 会打开 `EnableSignals: true`，见 `internal/app/runner.go:38-40`；运行时异常时不会向前端发 `NotifyBackendFailed()`，见 `internal/app/runner.go:44-48` |

### 3.2 `thread.Module`

`internal/module/thread/module.go:8-16` 把 `NewService` 的后 5 个参数和 `NewThreadHandlers` 的第 2 个参数都标成了 optional。这意味着即使这些 provider 被移走，`fx.ValidateApp(app.Module)` 仍然可能继续通过，错误会延迟到运行时。

| 可选依赖 | 当前 provider | 运行时为 nil 时的影响面 |
| --- | --- | --- |
| `threadstore.Store` | `internal/store/thread/store.go:14-16` | 线程元数据读写会退化：`List/Get/SetName/Delete/Archive/Unarchive` 这类路径会报 `thread store is not configured`，见 `internal/module/thread/service.go:121-166`；`persistThreadState` 会静默跳过线程表持久化，见 `internal/module/thread/lifecycle.go:238-258`；同时 `unified.NewSessionResolver` 仍可被构造，但会在运行时因 `threadStore == nil` 失败，见 `internal/provider/unified/session_resolver.go:23-45` |
| `bindingstore.Store` | `internal/store/binding/store.go:14-16` | thread 与 agent 的绑定无法查找/持久化：`resolveBinding/resolveSession` 会报 `binding store is not configured`，见 `internal/module/thread/service.go:183-226`；`persistThreadState` 会跳过 binding upsert，见 `internal/module/thread/lifecycle.go:259-271` |
| `SessionProvider` | `internal/provider/unified/module.go:25` `NewSessionProvider` | `lookupSession/resolveSession` 失败，`Start/Resume/Recover/ReadHistory/Fork` 等依赖活动 session 的路径会在运行时报 `session provider is not configured`，见 `internal/module/thread/lifecycle.go:57-60,92-95,147-158,231-235` 与 `internal/module/thread/service.go:213-225` |
| `SessionStarter` | `internal/provider/unified/module.go:23` `NewClient` 通过 `fx.As(new(thread.SessionStarter))` | `Start/Resume/Recover` 无法创建或恢复 session，直接报 `session starter is not configured`，见 `internal/module/thread/lifecycle.go:204-229` |
| `OrchestrationFacade` | `internal/app/thread_orchestration_adapter.go:14-16` | agent 进程管理会退化成 no-op：`launchAgent` / `recoverAgent` 提前返回 `nil`，`stopAgent` 不再停进程，见 `internal/module/thread/lifecycle.go:298-323`；结果是 thread 路径不会在装配期失败，但运行时会依赖“外部已存在的 agent 进程” |
| `rpc.CapabilityResolver` | `internal/platform/rpc/handler.go:22-40` `NewCapabilityResolver` | capability-gated 的 thread RPC 会失败：`thread/model/set`、`thread/compact/start`、`thread/realtime/*` 都经 `CapabilityGate` 走 resolver，见 `internal/module/thread/rpc.go:62,67,89-95`；resolver 为 nil 时会返回 invalid-state 类错误，见 `internal/platform/rpc/handler.go:80-120` |

### 3.3 `turn.Module`

`internal/module/turn/module.go:11-14` 把 `NewTurnHandlers` 的第 2、第 4 个参数标成了 optional。

| 可选依赖 | 当前 provider | 运行时为 nil 时的影响面 |
| --- | --- | --- |
| `contract.SessionResolver` | `internal/provider/unified/session_resolver.go:19-21` | `turn/start`、`turn/steer`、`turn/interrupt`、`turn/forceComplete` 共用 `withSession`，resolver 为 nil 直接报 `turn rpc: session resolver is not configured`，见 `internal/module/turn/rpc.go:20-30,60-75` |
| `rpc.CapabilityResolver` | `internal/platform/rpc/handler.go:22-40` `NewCapabilityResolver` | capability-gated 的 `turn/start` / `turn/steer` 会在进入 service 前失败，见 `internal/module/turn/rpc.go:33-58` 与 `internal/platform/rpc/handler.go:80-130` |

备注：`turn.NewTurnHandlers` 的 `contract.ApprovalResponder` 不是 optional；当前由 `internal/platform/rpc/module.go:22` 的 adapter 提供。

## 4. `group:"rpc_handlers"`：生产者 + 唯一消费者

group 定义：`internal/platform/rpc/module.go:36-40` 的 `HandlerMapResult` 把 `handler.Map` 输出到 `group:"rpc_handlers"`。

唯一消费者：`internal/platform/rpc/module.go:42-52`

- `serverParams.Handlers []handler.Map \`group:"rpc_handlers"\``
- `registerAllHandlers(server *Server, p serverParams)` 统一调用 `server.Register(p.Handlers...)`

生产者共有 `5` 个：

| 生产者 | 证据 |
| --- | --- |
| `skill.NewSkillHandlers` | `internal/module/skill/rpc.go:42-88` |
| `thread.NewThreadHandlers` | `internal/module/thread/rpc.go:19-97` |
| `turn.NewTurnHandlers` | `internal/module/turn/rpc.go:14-96` |
| `orchestration.NewOrchestrationHandlers` | `internal/sidecar/orch/orchestration/rpc.go:15-77` |
| `workspace.NewWorkspaceHandlers` | `internal/module/workspace/rpc.go:13-23` |

结论：`group:"rpc_handlers"` 是 `5 -> 1`，消费者唯一且闭合。

## 5. `group:"runners"`：生产者 + 唯一消费者

group 定义：`internal/app/runner.go:14-17` 的 `RunnerResult` 把 `platformrunner.Runner` 输出到 `group:"runners"`。

唯一消费者：`internal/app/runner.go:19-28`

- `runtimeParams.Runners []platformrunner.Runner \`group:"runners"\``
- `BindRuntime(...)` 把整组 runner 交给 `platformrunner.RunGroup(...)`

生产者共有 `2` 个：

| 生产者 | 证据 |
| --- | --- |
| `app.AsRPCRunner` | 输出在 `internal/app/modules.go:46-48`，group tag 在 `internal/app/runner.go:14-17` |
| `orchestration.NewRunnerActor` | `internal/sidecar/orch/orchestration/runner_actor.go:22-24`，注解在 `internal/sidecar/orch/orchestration/module.go:20` |

补充观察：

- `internal/ui/wails/runner.go:16-18` 虽然有 `NewRunner(app *application.App) platformrunner.Runner`，但 `uiwails.Module` 没有 `fx.Provide(NewRunner)`；所以它不在当前 `group:"runners"` 图里。

结论：`group:"runners"` 是 `2 -> 1`，消费者唯一且闭合。

## 6. `group:"drivers"`：生产者 + 唯一消费者

唯一消费者：`internal/provider/unified/module.go:13-17` + `internal/provider/unified/registry.go:15-25`

- `RegistryParams.Drivers []contract.DriverFactory \`group:"drivers"\``
- `NewRegistry(params RegistryParams)` 遍历工厂并构建 registry

生产者共有 `2` 个：

| 生产者 | 证据 |
| --- | --- |
| `claudecli.NewDriverFactory` | `internal/provider/claudecli/module.go:21-25`，group 注解在 `:23` |
| `codexapp.NewDriverFactory` | `internal/provider/codexapp/module.go:26-30`，group 注解在 `:28` |

结论：`group:"drivers"` 是 `2 -> 1`，消费者唯一且闭合。

## 7. 循环依赖检测：`A -> B -> C -> A`

结论：未发现强依赖闭环。

LSP 手工展开后的几条最长链如下，都会在叶子节点终止，没有回边：

1. `thread.Service`
   -> `thread.OrchestrationFacade`
   -> `orchestration.Service`
   -> `orchestration.TurnStarter`
   -> `turn.Service`

   证据：`internal/module/thread/service.go:40-47`、`internal/app/thread_orchestration_adapter.go:14-16`、`internal/sidecar/orch/orchestration/service.go:80-102`、`internal/module/turn/orchestration_starter.go:18-20`、`internal/module/turn/service.go:26-37`

   终止原因：`turn.Service` 不依赖 `thread.Service`。

2. `rpc.CapabilityResolver`
   -> `contract.SessionResolver`
   -> `threadstore.Store`
   -> `*sqlc.Queries`
   -> `*pgxpool.Pool`
   -> `*config.Config`

   证据：`internal/platform/rpc/handler.go:22-40`、`internal/provider/unified/session_resolver.go:19-21`、`internal/store/thread/store.go:14-16`、`internal/store/sqlc/db.go:22-24`、`internal/platform/db/module.go:15-16`、`internal/platform/config/config.go:15-22`

   终止原因：`config.Config` 无反向依赖。

3. `thread.SessionStarter`
   -> `unified.Registry`
   -> `group:"drivers"`
   -> `claudecli.NewDriverFactory | codexapp.NewDriverFactory`
   -> `unified.EventDispatcher`
   -> `*event.Dispatcher`

   证据：`internal/provider/unified/client.go:18-27`、`internal/provider/unified/registry.go:15-25`、`internal/provider/claudecli/module.go:12-25`、`internal/provider/codexapp/module.go:13-30`、`internal/provider/unified/event_map.go:20-28`

   终止原因：driver factory 只往下依赖 logger / event dispatcher / approval manager，不回到 thread/orchestration。

交叉验证：

- `internal/archtest/fx_graph_test.go:11-14` 的 `fx.ValidateApp(app.Module)` 当前通过。
- 如果存在强依赖环，`fx.ValidateApp(app.Module)` 会在这里失败；当前没有出现这种情况。

## 最终判断

- 主图 `app.Module` 当前是可验证闭合的：没有 `fx.In` 缺失依赖、没有 group 断裂、没有强依赖环。
- 主要风险不在“图断了”，而在 `thread.Module` / `turn.Module` 的 optional 设计把若干装配错误推迟到了运行期。
- 当前最明显的图卫生问题是 `19` 个悬空 provider；其中 `14` 个来自 store，`5` 个来自 bus emitters。
