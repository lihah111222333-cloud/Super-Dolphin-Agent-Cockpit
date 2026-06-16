# 审查：fx DI + jrpc2 RPC 框架

注：已按要求使用 LSP `workspace_symbol`、`references`、`call_hierarchy`、精确 `read_file` 取证；`ast_search` 在当前环境不可用（LSP 返回 `sg not found in PATH`），因此本报告用符号索引与交叉引用替代 AST 结果。

## 1. fx DI 图

### 1.1 模块注册清单（逐模块列出 fx.Provide/Invoke）

`app.Module` 在 `internal/app/modules.go:23-43` 注册了 15 个子模块，并额外直接注册了 3 个根级 provider：`NewLogger`、`AsRPCRunner`、`newThreadOrchestrationFacade`（`internal/app/modules.go:24`, `internal/app/modules.go:40-43`, `internal/app/modules.go:46-48`）。

| 模块 | app 装配证据 | module.go 证据 | 结论 |
| --- | --- | --- | --- |
| `config.Module` | `internal/app/modules.go:25` | `internal/platform/config/module.go:5` | 仅 `fx.Provide(New)`；无 `fx.Invoke` / `fx.Supply`。 |
| `db.Module` | `internal/app/modules.go:26` | `internal/platform/db/module.go:13-17` | `fx.Provide(NewPool)` + `fx.Invoke(registerLifecycle)`；无 `fx.Supply`。 |
| `bus.Module` | `internal/app/modules.go:27` | `internal/platform/bus/module.go:10-23` | 7 个 bus provider + `fx.Invoke(registerLifecycle)`；无 `fx.Supply`。 |
| `rpc.Module` | `internal/app/modules.go:28` | `internal/platform/rpc/module.go:13-22` | `fx.Provide(NewServer/NewPushBridge/NewApprovalManager/NewCapabilityResolver/ApprovalResponder cast)` + `fx.Invoke(registerAllHandlers)`；无 `fx.Supply`。 |
| `platformrunner.Module` | `internal/app/modules.go:29` | `internal/platform/runner/module.go:5` | 空模块；无 provider / invoke。 |
| `statemachine.Module` | `internal/app/modules.go:30` | `internal/platform/statemachine/module.go:5` | 空模块；无 provider / invoke。 |
| `store.Module` | `internal/app/modules.go:31` | `internal/store/module.go:14-21` | `fx.Provide(sqlc.New)`，并只组合 `binding/commandcard/prompt/thread/workspace` 五个 store 子模块。 |
| `skill.Module` | `internal/app/modules.go:32` | `internal/module/skill/module.go:5-8` | `fx.Provide(NewService)` + `fx.Provide(NewSkillHandlers)`。 |
| `thread.Module` | `internal/app/modules.go:33` | `internal/module/thread/module.go:7-18` | `fx.Provide(NewService)`、`fx.Provide(NewThreadHandlers)`，两者均带 `fx.Annotate`。 |
| `turn.Module` | `internal/app/modules.go:34` | `internal/module/turn/module.go:7-15` | `fx.Provide(NewService)` + `fx.Annotate(NewTurnHandlers, ...)`。 |
| `orchestration.Module` | `internal/app/modules.go:35` | `internal/sidecar/orch/orchestration/module.go:5-12` | `fx.Provide(NewService/service->Service/NewOrchestrationHandlers/NewRunnerActor[group:"runners"])`。 |
| `workspace.Module` | `internal/app/modules.go:36` | `internal/module/workspace/module.go:5-8` | `fx.Provide(NewService)` + `fx.Provide(NewWorkspaceHandlers)`。 |
| `unified.Module` | `internal/app/modules.go:37` | `internal/provider/unified/module.go:17-27` | `fx.Provide(NewEventDispatcher/NewRegistry/NewClient/NewSessionManager/NewSessionProvider/NewSessionCleaner/NewSessionResolver)`，其中 3 个带 `fx.As(...)`。 |
| `claudecli.Module` | `internal/app/modules.go:38` | `internal/provider/claudecli/module.go:21-26` | `fx.Annotate(NewDriverFactory, group:"drivers")` + `fx.Invoke(RegisterTranslators)`。 |
| `codexapp.Module` | `internal/app/modules.go:39` | `internal/provider/codexapp/module.go:21-26` | `fx.Annotate(NewDriverFactory, group:"drivers")` + `fx.Invoke(RegisterTranslators)`。 |

根级 provider 额外补了两条关键链路：

- `AsRPCRunner(server *rpc.Server) RunnerResult` 把 `*rpc.Server` 放入 `group:"runners"`，证据：`internal/app/modules.go:40-43`, `internal/app/modules.go:46-48`, `internal/app/runner.go:13-16`。
- `newThreadOrchestrationFacade(svc orchestration.Service) thread.OrchestrationFacade` 为 thread 模块提供 orchestration 适配器，证据：`internal/app/modules.go:40-43`, `internal/app/thread_orchestration_adapter.go:14-35`。

### 1.2 group 标签完整性（rpc_handlers/runners/drivers）

#### `group:"rpc_handlers"`

- group 定义与消费点清晰：生产端类型是 `rpc.HandlerMapResult`，字段 `Handlers handler.Map 'group:"rpc_handlers"'`；消费端是 `serverParams.Handlers []handler.Map 'group:"rpc_handlers"'`，`registerAllHandlers` 调用 `server.Register(p.Handlers...)`，证据：`internal/platform/rpc/module.go:31-47`。
- `HandlerMapResult` 的 LSP `references` 结果只命中 5 个生产者：`NewOrchestrationHandlers`、`NewSkillHandlers`、`NewThreadHandlers`、`NewTurnHandlers`、`NewWorkspaceHandlers`，证据：`internal/sidecar/orch/orchestration/rpc.go:11-35`, `internal/module/skill/rpc.go:12-64`, `internal/module/thread/rpc.go:18-82`, `internal/module/turn/rpc.go:14-93`, `internal/module/workspace/rpc.go:13-23`。
- 这 5 个 handler producer 对应的模块全部在 `app.Module` 中注册，证据：`internal/app/modules.go:32-36`。
- 结论：`rpc_handlers` group 完整，当前没有“定义了 `HandlerMapResult` 但未被 app 装配”的模块。

#### `group:"runners"`

- 生产者有 2 个：`NewRunnerActor` 通过 `fx.ResultTags('group:"runners"')` 输出 orchestration runner，`AsRPCRunner` 通过 `RunnerResult` 输出 rpc server runner，证据：`internal/sidecar/orch/orchestration/module.go:6-11`, `internal/sidecar/orch/orchestration/runner_actor.go:22-24`, `internal/app/modules.go:46-48`, `internal/app/runner.go:13-16`。
- 消费者只有 `BindRuntime`，其 `runtimeParams` 注入 `[]platformrunner.Runner 'group:"runners"'`，再交给 `platformrunner.RunGroup` 统一运行，证据：`internal/app/runner.go:18-26`, `internal/app/runner.go:30-60`, `internal/platform/runner/group.go:14-18`, `internal/platform/runner/group.go:49-58`。
- `BindRuntime` 的调用链明确：`NewApp` 中 `fx.Invoke(BindRuntime)`，证据：`internal/app/app.go:17-21`；`rpc.Server` 自身实现了 `Run(ctx)`，证据：`internal/platform/rpc/server.go:37-58`。
- 结论：`runners` group 消费正确，当前至少有两个 runner 实例参与运行组。

#### `group:"drivers"`

- 生产者只有两个 provider driver factory：`claudecli.NewDriverFactory` 与 `codexapp.NewDriverFactory`，都通过 `fx.ResultTags('group:"drivers"')` 输出，证据：`internal/provider/claudecli/module.go:21-26`, `internal/provider/codexapp/module.go:21-26`。
- 消费者是 `unified.RegistryParams` 的 `Drivers []contract.DriverFactory 'group:"drivers"'`，`NewRegistry` 遍历该切片构建 provider registry，证据：`internal/provider/unified/module.go:11-18`, `internal/provider/unified/registry.go:15-24`。
- 两个 provider 模块都在 `app.Module` 注册，证据：`internal/app/modules.go:37-39`。
- 结论：`drivers` group 完整，当前 registry 只有 `claude` / `codex` 两个驱动来源。

### 1.3 挂空依赖（provider 缺失的类型）

#### 当前图闭合情况

未发现 `app.Module` 当前装配下的实际挂空依赖；关键链路如下。

| 构造函数 | 形参 | provider 证据 | 结论 |
| --- | --- | --- | --- |
| `internal/platform/db.NewPool` | `*config.Config` | `internal/platform/config/config.go:11-17` | `*config.Config` 由 `config.New` 提供，闭合。 |
| `internal/store/sqlc.New` | `*pgxpool.Pool` | `internal/platform/db/module.go:19-26`, `internal/store/sqlc/db.go:22-24` | `*pgxpool.Pool` 由 `NewPool` 提供，闭合。 |
| `internal/module/skill.NewService` | `commandcard.Store` | `internal/store/module.go:14-21`, `internal/store/commandcard/module.go:5-7`, `internal/module/skill/service.go:25-31` | `commandcard.Store` 已提供，闭合。 |
| `internal/module/workspace.NewService` | `storeworkspace.Store` | `internal/store/module.go:14-21`, `internal/store/workspace/module.go:5-7`, `internal/module/workspace/service.go:29-31` | `workspace.Store` 已提供，闭合。 |
| `internal/module/thread.NewService` | `threadstore.Store`, `bindingstore.Store` | `internal/store/thread/module.go:5-7`, `internal/store/binding/module.go:5-7`, `internal/module/thread/service.go:40-47` | 两个 store 均已提供，闭合。 |
| `internal/module/thread.NewService` | `SessionProvider` | `internal/module/thread/lifecycle.go:17-20`, `internal/provider/unified/module.go:23`, `internal/provider/unified/session_adapter.go:17-27` | 由 `NewSessionProvider` 经 `fx.As(new(thread.SessionProvider))` 提供，闭合。 |
| `internal/module/thread.NewService` | `SessionStarter` | `internal/module/thread/lifecycle.go:17-20`, `internal/provider/unified/module.go:21`, `internal/provider/unified/client.go:18-27` | 由 `NewClient` 经 `fx.As(new(thread.SessionStarter))` 提供，闭合。 |
| `internal/module/thread.NewService` | `OrchestrationFacade` | `internal/module/thread/lifecycle.go:22-26`, `internal/app/thread_orchestration_adapter.go:14-35` | 由 app 根级 adapter 提供，闭合。 |
| `internal/module/turn.NewTurnHandlers` | `contract.SessionResolver` | `internal/contract/session_resolver.go:5-7`, `internal/provider/unified/session_resolver.go:19-46` | 由 `NewSessionResolver` 提供，闭合。 |
| `internal/module/turn.NewTurnHandlers` | `contract.ApprovalResponder` | `internal/contract/approval.go:7-9`, `internal/platform/rpc/module.go:17-20`, `internal/platform/rpc/approval.go:61-69`, `internal/platform/rpc/approval.go:105-116` | 由 `ApprovalManager` 转型提供，闭合。 |
| `internal/platform/rpc.NewCapabilityResolver` | `contract.SessionResolver` | `internal/platform/rpc/handler.go:20-31`, `internal/provider/unified/session_resolver.go:19-46` | 依赖已提供，闭合。 |
| `internal/sidecar/orch/orchestration.NewService` | `SessionCleaner` | `internal/sidecar/orch/orchestration/contract.go:25-27`, `internal/provider/unified/module.go:24`, `internal/provider/unified/session_adapter.go:21-33` | 由 `NewSessionCleaner` 经 `fx.As(new(orchestration.SessionCleaner))` 提供，闭合。 |
| `internal/sidecar/orch/orchestration.NewRunnerActor` | `*service` | `internal/sidecar/orch/orchestration/service.go:67-78`, `internal/sidecar/orch/orchestration/module.go:6-10`, `internal/sidecar/orch/orchestration/runner_actor.go:22-24` | `NewService` 直接产出 concrete `*service`，故可注入；同时另有 `func(s *service) Service` 提供接口视图。 |
| `internal/provider/unified.NewRegistry` | `[]contract.DriverFactory` | `internal/provider/unified/module.go:11-18`, `internal/provider/claudecli/module.go:21-26`, `internal/provider/codexapp/module.go:21-26` | 两个 driver factory group producer 已齐。 |
| `internal/provider/claudecli.NewDriverFactory` / `internal/provider/codexapp.NewDriverFactory` | `*unified.EventDispatcher` | `internal/provider/unified/event_map.go:20-28`, `internal/provider/claudecli/module.go:21-26`, `internal/provider/codexapp/module.go:21-26` | dispatcher 已由 unified 模块提供，闭合。 |
| `internal/app.AsRPCRunner` | `*rpc.Server` | `internal/platform/rpc/module.go:14-21`, `internal/app/modules.go:46-48` | `*rpc.Server` 已提供，闭合。 |

#### 需要特别标记的弱化点

- `thread.NewService` 把除 `logger` 之外的 5 个参数都标成了 `optional:"true"`，包括 `threadstore.Store` 和 `bindingstore.Store`；一旦 provider 被移除，fx 启动不会失败，而是运行期在 `Delete/List/getThread/resolveSession` 等路径报 `"thread store is not configured"` / `"session provider is not configured"`，证据：`internal/module/thread/module.go:9-16`, `internal/module/thread/service.go:115-124`, `internal/module/thread/service.go:144-147`, `internal/module/thread/service.go:218-224`。结论：当前无挂空，但 fail-fast 保护被削弱。
- `thread.NewThreadHandlers` 把 `rpc.CapabilityResolver` 标成可选，`turn.NewTurnHandlers` 把 `contract.SessionResolver` 与 `rpc.CapabilityResolver` 标成可选；对应 handler 在运行期显式判断 `resolver == nil` / `approver == nil`，证据：`internal/module/thread/module.go:13-16`, `internal/module/turn/module.go:10-12`, `internal/module/turn/rpc.go:20-29`, `internal/module/turn/rpc.go:79-87`。结论：当前图闭合，但依赖缺失会退化成运行期错误。

## 2. jrpc2 使用

### 2.1 handler 签名合规性

- V3 代码库未直接使用 `handler.New(...)`；所有 RPC 注册点都经 `rpc.StrictHandler`、`rpc.ThreadHandler`、`rpc.CapabilityThreadHandler` 或其 helper 包装，证据：`internal/sidecar/orch/orchestration/rpc.go:12-35`, `internal/module/skill/rpc.go:13-64`, `internal/module/thread/rpc.go:19-114`, `internal/module/turn/rpc.go:32-92`, `internal/module/workspace/rpc.go:14-22`。
- `rpc.StrictHandler` 明确走 `handler.Check(fn).AllowArray(false).SetStrict(true).Wrap()`，证据：`internal/platform/rpc/strict.go:11-16`。因此 V3 统一启用了对象参数、禁数组参数、严格字段检查。
- jrpc2 库本身允许的签名比“`func(ctx, *T)` 或 `func(ctx)`”更宽：`handler.Check` 只要求第一个参数是 `context.Context`，第二个参数可有可无（`handler/handler.go:291-300`），且参数既支持指针也支持按值解码（`handler/handler.go:178-202`）；返回值允许 `(T, error)`、`error`、`T` 三类（`handler/handler.go:307-317`）。证据：`/Users/mima0000/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/handler/handler.go:178-202`, `/Users/mima0000/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/handler/handler.go:291-317`。
- V3 注册点实际使用的是合法的“按值参数”形式，例如 `func(ctx context.Context, p launchParams) (any, error)`、`func(ctx context.Context, p turnStartParams) (any, error)`、`func(ctx context.Context, p listRunsParams) (runsResult, error)`，证据：`internal/sidecar/orch/orchestration/rpc.go:13-29`, `internal/module/turn/rpc.go:33-57`, `internal/module/workspace/rpc.go:15-22`, `internal/module/workspace/rpc.go:52-60`。
- `ThreadHandler` / `CapabilityThreadHandler` 在 strict binding 之前先执行 `ThreadScope`，其实现会从原始 params 中抽取 `threadId` 注入 context，证据：`internal/platform/rpc/handler.go:44-66`, `internal/platform/rpc/handler.go:88-95`；而 thread/turn 相关 param struct 均显式声明了 `json:"threadId"` 字段，因此 strict 模式不会因未知字段拒绝请求，证据：`internal/module/thread/rpc_types.go:3-37`, `internal/module/turn/rpc_types.go:5-26`。结论：当前 handler 签名与 strict binding 组合是合法的。

### 2.2 handler.Map key 规范

- 当前 key 规范不统一。orchestration 使用点号命名：`agent.launch`、`agent.stop`、`agent.list`、`agent.snapshot`、`orchestration.report`，证据：`internal/sidecar/orch/orchestration/rpc.go:13`, `internal/sidecar/orch/orchestration/rpc.go:21`, `internal/sidecar/orch/orchestration/rpc.go:24`, `internal/sidecar/orch/orchestration/rpc.go:27`, `internal/sidecar/orch/orchestration/rpc.go:34`。
- thread/turn/skill/workspace 使用斜杠命名：如 `thread/start`、`turn/start`、`skills/list`、`workspace/run/create`，证据：`internal/module/thread/rpc.go:20`, `internal/module/turn/rpc.go:33`, `internal/module/skill/rpc.go:34`, `internal/module/workspace/rpc.go:15`。
- `Server.Register` 只是把字符串 key 原样写入 `s.methods`，框架层没有统一命名策略或校验逻辑，证据：`internal/platform/rpc/server.go:29-34`。结论：如果目标规范是统一为 `namespace/method`，当前实现不合格；如果以 V2 兼容优先，则这是历史兼容保留而非严格违规。

### 2.3 重复 key 检查

- 现有 5 个 `handler.Map` 的 key 集合按模块划分清晰：orchestration 9 个（`internal/sidecar/orch/orchestration/rpc.go:12-34`），skill 22 个（`internal/module/skill/rpc.go:19-64`），thread 29 个（`internal/module/thread/rpc.go:19-80`），turn 6 个（`internal/module/turn/rpc.go:32-92`），workspace 8 个（`internal/module/workspace/rpc.go:14-22`）；人工交叉比对未发现重复 key。结论：当前版本无重复注册。
- 但框架没有重复 key 保护；`registerAllHandlers` 直接把 group 收集到的 `[]handler.Map` 交给 `Server.Register`，而 `Server.Register` 遇到同名 key 只会 `s.methods[name] = handlerFunc` 静默覆盖，证据：`internal/platform/rpc/module.go:42-47`, `internal/platform/rpc/server.go:29-34`。结论：当前“无重复”不等于机制安全，后续新增 handler 时存在静默覆盖风险。

## 3. 注册完整性

### 3.1 V3 总 handler 数

V3 的完整 RPC 暴露面只来自 5 个 `rpc.HandlerMapResult` producer，证据：`internal/platform/rpc/module.go:31-35`, `internal/sidecar/orch/orchestration/rpc.go:11-35`, `internal/module/skill/rpc.go:12-64`, `internal/module/thread/rpc.go:18-82`, `internal/module/turn/rpc.go:14-93`, `internal/module/workspace/rpc.go:13-23`。

| 模块 | handler 数 | 证据 |
| --- | ---: | --- |
| orchestration | 9 | `internal/sidecar/orch/orchestration/rpc.go:12-34` |
| skill | 22 | `internal/module/skill/rpc.go:19-64` |
| thread | 29 | `internal/module/thread/rpc.go:19-80` |
| turn | 6 | `internal/module/turn/rpc.go:32-92` |
| workspace | 8 | `internal/module/workspace/rpc.go:14-22` |
| 合计 | 74 | 上述 5 个 `handler.Map` 逐项计数 |

需要单独指出：V3 已注册但仍返回 `ErrNotImplemented` 的方法至少有 6 个，即 `task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update`、`orchestration/report`、`review/start`，证据：`internal/sidecar/orch/orchestration/rpc.go:30-40`, `internal/module/turn/rpc.go:74-76`。

### 3.2 V2 vs V3 方法差距表

V2 是“手动填充 `s.methods` map”模式：`New()` 中执行 `s.registerMethods()`，证据：`go-agent-v2/internal/apiserver/server.go:163-225`；`registerMethods()` 再分发到 core/thread-turn/skill/config-account/workspace/ui/debug/dashboard/orchestration 各子注册函数，证据：`go-agent-v2/internal/apiserver/methods.go:128-166`, `go-agent-v2/internal/apiserver/methods.go:229-272`, `go-agent-v2/internal/apiserver/methods.go:320-346`, `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`, `go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113`, `go-agent-v2/internal/apiserver/dashboard_bindings.go:152-160`, `go-agent-v2/internal/dashrpc/register.go:89-103`。

按注册点逐项计数，V2 总方法数为 151：

- core 8 个，证据：`go-agent-v2/internal/apiserver/methods.go:157-162`
- noop 5 个，证据：`go-agent-v2/internal/apiserver/methods.go:135-139`, `go-agent-v2/internal/apiserver/methods.go:163`
- dashboard 12 个，证据：`go-agent-v2/internal/dashrpc/register.go:89-103`
- orchestration 12 个，证据：`go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`
- thread/turn 35 个，证据：`go-agent-v2/internal/apiserver/methods_thread_turn.go:8-113`
- skill 14 个，证据：`go-agent-v2/internal/apiserver/methods.go:229-236`
- config/account 21 个，证据：`go-agent-v2/internal/apiserver/methods.go:239-252`
- workspace 5 个，证据：`go-agent-v2/internal/apiserver/methods.go:255-259`
- UI 19 个，证据：`go-agent-v2/internal/apiserver/methods.go:262-271`
- debug/stub 20 个，证据：`go-agent-v2/internal/apiserver/methods.go:320-345`

V3 当前 74 个；与 V2 交集为 58 个，因此 **V2 有但 V3 缺的方法共 93 个**。V3 的新增面主要是 `command/card/*`、`workspace/run/status/update`、`workspace/run/files/list`、`workspace/run/file/get`、`agent.snapshot`、`task/dag/*` / `task/node/update` / `orchestration/report`，证据：`internal/module/skill/rpc.go:20-31`, `internal/module/workspace/rpc.go:18-22`, `internal/sidecar/orch/orchestration/rpc.go:27-34`。

#### 差距表

| 类别 | 缺失数 | V2 有但 V3 缺的方法 | 证据 |
| --- | ---: | --- | --- |
| core + noop | 11 | `initialize`, `fuzzyFileSearch`, `app/list`, `log/list`, `log/filters`, `log/relay`, `initialized`, `fuzzyFileSearch/sessionStart`, `fuzzyFileSearch/sessionUpdate`, `fuzzyFileSearch/sessionStop`, `feedback/upload` | V2: `go-agent-v2/internal/apiserver/methods.go:157-164`；V3 全量仅见 5 个 handler map：`internal/sidecar/orch/orchestration/rpc.go:11-35`, `internal/module/skill/rpc.go:12-64`, `internal/module/thread/rpc.go:18-82`, `internal/module/turn/rpc.go:14-93`, `internal/module/workspace/rpc.go:13-23` |
| dashboard | 12 | `dashboard/agentStatus`, `dashboard/dags`, `dashboard/taskAcks`, `dashboard/taskTraces`, `dashboard/commandCards`, `dashboard/prompts`, `dashboard/sharedFiles`, `dashboard/auditLogs`, `dashboard/aiLogs`, `dashboard/busLogs`, `dashboard/skills`, `dashboard/dagDetail` | V2: `go-agent-v2/internal/dashrpc/register.go:89-103`；V3 全量见上 |
| orchestration | 9 | `agent.submit`, `agent.submitPrompt`, `agent.getReport`, `agent.rememberReportRequest`, `agent.reportEvent`, `agent.getState`, `agent.saveSubAgent`, `agent.deleteSubAgent`, `agent.persistSubAgentBinding` | V2: `go-agent-v2/internal/apiserver/methods_orchestration.go:15-26`；V3 orchestration 仅有 `agent.launch/stop/list/snapshot` 与 DAG 占位：`internal/sidecar/orch/orchestration/rpc.go:12-34` |
| thread/turn 兼容项 | 1 | `mock/experimentalMethod` | V2: `go-agent-v2/internal/apiserver/methods_thread_turn.go:111-112`；V3 thread/turn 全量：`internal/module/thread/rpc.go:19-80`, `internal/module/turn/rpc.go:32-92` |
| config/account | 21 | `model/list`, `collaborationMode/list`, `experimentalFeature/list`, `config/read`, `externalAgentConfig/detect`, `externalAgentConfig/import`, `config/value/write`, `config/batchWrite`, `config/lspPromptHint/read`, `config/lspPromptHint/write`, `configRequirements/read`, `account/login/start`, `account/login/cancel`, `account/logout`, `account/read`, `account/rateLimits/read`, `mcpServer/oauth/login`, `config/mcpServer/reload`, `mcpServerStatus/list`, `windowsSandbox/setupStart`, `lsp_diagnostics_query` | V2: `go-agent-v2/internal/apiserver/methods.go:239-252`；V3 全量见上 |
| UI | 19 | `ui/preferences/get`, `ui/preferences/set`, `ui/preferences/getAll`, `ui/projects/get`, `ui/projects/add`, `ui/projects/remove`, `ui/projects/setActive`, `ui/code/open`, `ui/code/locate`, `ui/code/save`, `ui/dashboard/get`, `ui/state/get`, `ui/sidebar/get`, `lsp/gui_file`, `lsp/gui_grep`, `lsp/gui_structure`, `lsp/gui_inspect`, `lsp/gui_xref`, `ui/log` | V2: `go-agent-v2/internal/apiserver/methods.go:262-271`；V3 全量见上 |
| debug/stub | 20 | `debug/runtime`, `debug/gc`, `ml-interceptor/status`, `workspace-root-options`, `agent-home`, `git-origins`, `mcp-servers`, `platform-info`, `open-in-targets`, `agent-agents-md`, `local-environments/list`, `worktrees/list`, `tasks/list`, `tasks/get`, `inbox-items`, `inbox-items/get`, `pending-automation-runs`, `mcp/status`, `config/read-all`, `diff/get` | V2: `go-agent-v2/internal/apiserver/methods.go:320-345`；V3 全量见上 |

## 结论

### Blocker（必须修复）

- **迁移完整性差距过大。** V2 注册了 151 个方法，而 V3 当前只有 74 个；V2 有但 V3 缺 93 个，缺口覆盖 core/noop、dashboard、orchestration、config/account、UI、debug/stub 六大类，证据：`go-agent-v2/internal/apiserver/methods.go:128-166`, `go-agent-v2/internal/apiserver/methods.go:229-272`, `go-agent-v2/internal/apiserver/methods.go:320-346`, `go-agent-v2/internal/apiserver/methods_orchestration.go:14-27`, `go-agent-v2/internal/dashrpc/register.go:89-103`, `internal/sidecar/orch/orchestration/rpc.go:11-35`, `internal/module/skill/rpc.go:12-64`, `internal/module/thread/rpc.go:18-82`, `internal/module/turn/rpc.go:14-93`, `internal/module/workspace/rpc.go:13-23`。
- **V3 已暴露的方法里有明确占位实现。** `task/dag/create`、`task/dag/get`、`task/dag/list`、`task/node/update`、`orchestration/report`、`review/start` 已注册，但直接返回 `ErrNotImplemented`，证据：`internal/sidecar/orch/orchestration/rpc.go:30-40`, `internal/module/turn/rpc.go:74-76`。

### Warning（建议修复）

- **thread/turn 模块把关键依赖标成 optional，削弱了 fx 的 fail-fast 保证。** 当前 provider 是齐的，但一旦未来移除 `threadstore.Store`、`bindingstore.Store`、`SessionResolver`、`CapabilityResolver` 等 provider，应用仍可启动，只会在运行期报错，证据：`internal/module/thread/module.go:9-16`, `internal/module/turn/module.go:10-12`, `internal/module/thread/service.go:115-124`, `internal/module/thread/service.go:218-224`, `internal/module/turn/rpc.go:20-29`, `internal/module/turn/rpc.go:79-87`。
- **RPC method key 命名规范不统一。** orchestration 使用点号，其他大多数 namespace 使用斜杠；如果目标是统一 `namespace/method`，当前不满足，证据：`internal/sidecar/orch/orchestration/rpc.go:13-34`, `internal/module/thread/rpc.go:20-80`, `internal/module/skill/rpc.go:20-61`, `internal/module/workspace/rpc.go:15-22`。
- **handler 注册缺少重复 key 防御。** 现状无重复，但 `Server.Register` 对重复 key 只做最后一次赋值，后续新增模块时会静默覆盖，证据：`internal/platform/rpc/module.go:45-47`, `internal/platform/rpc/server.go:29-34`。

### OK（确认合格）

- **fx group 装配链路正确。** `rpc_handlers`、`runners`、`drivers` 三条 group 生产/消费链都能闭合，证据：`internal/platform/rpc/module.go:31-47`, `internal/app/runner.go:13-26`, `internal/provider/unified/module.go:11-27`, `internal/provider/claudecli/module.go:21-26`, `internal/provider/codexapp/module.go:21-26`。
- **当前 app.Module 下未发现实际挂空依赖。** `SessionStarter`、`SessionProvider`、`OrchestrationFacade`、`SessionCleaner`、`SessionResolver`、`ApprovalResponder`、各 store 接口、driver group 都能在 DI 图中找到明确 provider，证据：`internal/provider/unified/module.go:21-26`, `internal/provider/unified/client.go:18-27`, `internal/provider/unified/session_adapter.go:17-33`, `internal/provider/unified/session_resolver.go:19-46`, `internal/app/thread_orchestration_adapter.go:14-35`, `internal/platform/rpc/module.go:17-20`, `internal/store/module.go:14-21`。
- **jrpc2 包装方式当前合法。** `StrictHandler` 基于 `handler.Check` 做严格绑定，库源码确认“按值参数”签名合法，因此现有 `func(context.Context, T) (R, error)` 形式可以成立，证据：`internal/platform/rpc/strict.go:11-16`, `/Users/mima0000/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/handler/handler.go:178-202`, `/Users/mima0000/go/pkg/mod/github.com/creachadair/jrpc2@v1.3.5/handler/handler.go:291-317`。

## 互辩：批判其他 4 份报告

### 对 audit-event-sm 的批判

1. `audit-event-sm.md:125-129` 把“不存在硬孤儿事件”列为 OK，但这个结论的方法论过宽。当前 typed bus 的实际订阅者只有 `LogSink` 一处，且它只做日志镜像，不承载业务收敛，证据：`internal/platform/bus/sink.go:21-97`；面向客户端的 `BindEventToNotify` 只有定义、没有任何引用，LSP `references` 只命中声明本身，证据：`internal/platform/rpc/push.go:50-63`。因此 turn/tool/task/workspace 事件在运行时仍然是“只写日志的功能性孤儿”，报告把它记成 OK 过于宽松。
2. `audit-event-sm.md:114` 只把 `awaiting_user_input` 判成“触发器无触发点”，但遗漏了更严重的上游不可达事实。`RequestApproval` 的唯一 incoming 调用者是同文件里的 `RequestUserInput`，证据：`internal/platform/rpc/approval.go:98-103`；而对 `RequestUserInput` 做 LSP incoming `call_hierarchy` 返回空集，说明整个 approval request 链当前根本没有外部入口。结合状态定义里的 `TriggerUserInputRequested/Resolved` 仅存在声明与转换表，证据：`internal/dto/agent/state.go:28-29`, `internal/dto/agent/state.go:90-95`，问题不只是“状态死掉”，而是整个 approval/user-input 请求链条不可达。
3. `audit-event-sm.md:116` 虽然指出 `fireOrForceLocked()` 会绕过状态机，但它把问题写成若干已知特例，低估了影响面。实际 launch success/failure、turn accepted/completed/aborted、process exited 这些核心 happy-path/主失败路径全都走 `fireOrForceLocked()`，证据：`internal/sidecar/orch/orchestration/service.go:209-225`, `internal/sidecar/orch/orchestration/service.go:265-299`, `internal/sidecar/orch/orchestration/service.go:320-332`；而一旦 `agent.sm.FireCtx` 出错就直接 `forceStateLocked`，证据：`internal/sidecar/orch/orchestration/service.go:231-252`。这意味着状态机在 V3 不是“局部会被绕过”，而是整个 agent lifecycle 都存在表外强制落态。
4. `audit-event-sm.md:123` 把 `agentdto.StateChanged` 双源发布列成 Warning，但在当前消费者结构下，这个问题的现实危害低于“完全没出站接线”。双源确实存在，证据：`internal/sidecar/orch/orchestration/events.go:13-23`, `internal/provider/codexapp/event_map.go:47-53`；但因为 agent 事件订阅者目前仍只有 `LogSink`，证据：`internal/platform/bus/sink.go:43-49`，所以更该优先上升的是“事件无业务消费者/无 RPC push 接线”，而不是先把双源冲突放在前排。

### 对 audit-store-sqlc 的批判

1. `audit-store-sqlc.md:140` 把 `agent_codex_binding` / `AgentThreadBindingStore` 未迁移定成 Blocker，但代码证据并不支持这是当前运行面的直接阻塞点。V3 实际 thread 绑定链只走 `bindingStore.GetByAgentID` / `GetByProviderThread`，证据：`internal/module/thread/service.go:191-205`；绑定实体本身已经内含 `CodexThreadID`，证据：`internal/store/binding/contract.go:39-49`；底层 SQL 也只访问 `agent_provider_binding`，证据：`migrations/001_baseline.sql:48-52`, `internal/store/sqlc/query_agent_binding.go:6-11,20-40`。换言之，单独保留 `agent_codex_binding` 更像旧 schema/文档残留，不足以直接升为当前 V3 的 runtime blocker。
2. `audit-store-sqlc.md:141` 正确抓到了 `UpdateAgentProviderBindingArchived` 的生成/源漂移，但证据链停在“生成物和 SQL 对不上”，没有追到 live 调用面。实际 `thread.Archive` / `thread.Unarchive` 会走 `setBindingArchived`，证据：`internal/module/thread/archive.go:5-20`, `internal/module/thread/service.go:243-256`；再进入 `binding.Store.SetArchived`，最终依赖的正是 `UpdateAgentProviderBindingArchived`，证据：`internal/store/binding/store.go:66-72`, `internal/store/sqlc/query_agent_binding.go:36-38`。真正更严重的问题是：这不是静态漂移，而是直接坐在归档/反归档热路径上。
3. `audit-store-sqlc.md:148` 把 store 边界泄漏聚焦在 `binding/store.go` import `platform/db`，但它漏掉了更根部的泄漏点：`sqlc` 核心包装层自己就 import 了 `internal/platform/db`，并在 `Queries.WithTx` 内直接调用 `platformdb.WithTx`，证据：`internal/store/sqlc/db.go:3-8`, `internal/store/sqlc/db.go:51-57`。而 `taskdag/store.go` 又通过 `s.q.WithTx` 继续依赖这条链，证据：`internal/store/taskdag/store.go:15-18`。如果要批判 store 边界，这个核心层泄漏比 `binding/store.go:6,41-42` 更值得优先点名。
4. `audit-store-sqlc.md:153-156` 把“19 个 repo 子包三件套齐全”列为 OK，但这个 OK 与运行系统几乎没有关系。应用实际只装配了 5 个 store 模块：`binding/commandcard/prompt/thread/workspace`，证据：`internal/store/module.go:14-21`；app 层也只引入这一层 `store.Module`，证据：`internal/app/modules.go:23-39`。从 fx/RPC 实际装配看，磁盘上有 19 个子包并不能说明迁移完成，反而容易掩盖“多数 store 根本没进 DI 图”的关键事实。

### 对 audit-provider 的批判

1. `audit-provider.md:102` 把 `SessionResolver` 只支持 canonical thread id 判成 Warning，但这已经落在 turn/RPC 主路径上，严重性被判轻了。`turn/start` / `turn/steer` 会直接用 `resolver.ResolveSession(ctx, rpc.ThreadIDFrom(ctx))` 取 session，证据：`internal/module/turn/rpc.go:20-29`, `internal/module/turn/rpc.go:33-57`；`CapabilityResolver` 也走同一条 resolver 链，证据：`internal/platform/rpc/handler.go:20-31`, `internal/platform/rpc/handler.go:71-82`。而 thread 模块自己的绑定解析却支持 `GetByAgentID` + `GetByProviderThread` 回退，证据：`internal/module/thread/service.go:191-205`。这会直接让已注册的 turn/capability-gated RPC 在 alias/provider-thread id 输入下失败，更接近 Blocker 而不是 Warning。
2. `audit-provider.md:97` 把 session lifecycle 的注意力集中在 `Delete` 不调用 `RemoveSession`，但它遗漏了更基础的 shutdown 漏洞。`SessionManager.CloseAll` 只有声明没有任何调用者，LSP `references` 只命中定义，证据：`internal/provider/unified/session.go:69-83`；同时 `provider.unified` 模块完全没有 `fx.Invoke` 或 lifecycle hook，证据：`internal/provider/unified/module.go:17-27`。因此当前进程退出时不会统一关闭 session，这个问题比单一路径的 `Delete` 清理遗漏更底层。
3. `audit-provider.md:96` 把 `ToolCallResponder` 未完成列为 Blocker，但代码显示它目前连活跃接线都没有。接口只存在于合同层，LSP `references` 仅命中声明本身，证据：`internal/contract/provider.go:47-50`；`RespondResult` / `RespondError` 也没有任何实现或调用，证据：`internal/contract/provider.go:49-50`。这更像 parity 债或预留合同，不是当前 app 跑起来后立即命中的主路径 blocker。
4. `audit-provider.md:98` 把 agent 级事件双源发布列成 Blocker，但没有结合当前消费者结构校准严重性。双源本身属实，证据：`internal/sidecar/orch/orchestration/events.go:13-64`, `internal/provider/claudecli/event_map.go:35-53`, `internal/provider/codexapp/event_map.go:39-74`；但 agent 事件当前订阅者仍只有 `LogSink`，证据：`internal/platform/bus/sink.go:43-49`，而对外通知桥 `BindEventToNotify` 没有任何引用，证据：`internal/platform/rpc/push.go:50-63`。在现状下，事件冲突的现实风险低于前两条 resolver/lifecycle 缺陷，Blocker 排序不准确。

### 对 audit-module-v2-parity 的批判

1. `audit-module-v2-parity.md:245-269` 把 `68` 写成 “V2 总数” 并给出 `85.3%` 覆盖率，但这只是 thread/turn/skill/workspace/orchestration 的 scoped subtotal，不是 V2 全量。真正的 `registerMethods()` 还会注册 core、dashboard、config/account、UI、debug/stub 等面，证据：`go-agent-v2/internal/apiserver/methods.go:142-148`, `go-agent-v2/internal/apiserver/methods.go:157-166`, `go-agent-v2/internal/apiserver/methods.go:239-272`, `go-agent-v2/internal/apiserver/methods.go:320-345`, `go-agent-v2/internal/apiserver/dashboard_bindings.go:152-160`, `go-agent-v2/internal/dashrpc/register.go:89-103`。这份报告的小节标题会让读者误以为 `68` / `85.3%` 是全局迁移率，口径表达不够严格。
2. `audit-module-v2-parity.md:295-305` 的结论没有把“已注册但直接 `ErrNotImplemented`”单列为 Blocker，优先级失真。当前 `task/dag/create|get|list`、`task/node/update`、`orchestration/report` 在 orchestration 模块里统一走 `newNotImplementedHandler`，证据：`internal/sidecar/orch/orchestration/rpc.go:30-41`；`review/start` 也直接返回 `ErrNotImplemented`，证据：`internal/module/turn/rpc.go:74-77`。这些是假阳性公开 API，比“同名方法语义弱化”更应该在结论层被单独提升。
3. `audit-module-v2-parity.md:262-270` 用“有同名 handler/同域入口”计算覆盖率，但没有把 resolver 约束纳入 parity 质量，导致已覆盖方法被高估。V3 的 `turn/start` / `turn/steer` 依赖 `SessionResolver.ResolveSession`，证据：`internal/module/turn/rpc.go:20-29`, `internal/module/turn/rpc.go:33-57`；`thread/model/set` / `thread/compact/start` / realtime 等 capability-gated RPC 也依赖 `CapabilityResolver -> SessionResolver`，证据：`internal/platform/rpc/handler.go:20-31`, `internal/platform/rpc/handler.go:71-95`；而当前 `SessionResolver` 只认 canonical thread id，证据：`internal/provider/unified/session_resolver.go:23-45`，弱于 thread 模块自己的 alias/provider-thread fallback，证据：`internal/module/thread/service.go:191-205`。因此不少“名字还在”的入口在输入兼容面并不等价。
4. `audit-module-v2-parity.md:236-243` 正确数出 V3 只有 5 个 `HandlerMapResult` producer、共 74 个 handler，但报告没有把这个事实继续上推到 DI/RPC 装配层。当前 `rpc_handlers` group 的生产者确实只有 `orchestration/skill/thread/turn/workspace` 5 个模块，证据：`internal/platform/rpc/module.go:31-47`, `internal/app/modules.go:32-36`。这意味着 core/config/ui/debug/dashboard 的巨大缺口不是“统计时暂不纳入”，而是压根没有 V3 的 mounting point；如果 parity 审查不把这一层说透，读者会误把它理解成口径差异而不是架构事实。
