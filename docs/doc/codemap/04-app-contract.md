# 04 App核心与契约层代码地图

> 扫描范围：`internal/app/*.go` 与 `internal/contract/*.go`。交叉核对实现包：`internal/platform/rpc`、`internal/platform/hooks`、`internal/platform/mcpcontrol`、`internal/provider/unified`、`internal/provider/claudecli`、`internal/provider/codexapp`、`internal/module/thread`、`internal/module/turn`、`internal/module/dashboard`、`internal/module/uistate`、`internal/ui/wails`、`cmd/mcp-orch`。

## 1. 模块概述

### 1.1 `internal/app`：根级组装层 / 运行入口层

`app` 包本身不承载业务规则，职责非常集中：

- 作为 **Uber Fx 根组装层**，把 platform / store / module / provider 组装成一个可运行应用；
- 暴露应用入口：`NewApp()`、`Run()`、`RunDesktop()`；
- 聚合 `group:"runners"` 的多个 `platformrunner.Runner`，统一管理 start / stop 生命周期；
- 提供两类跨边界适配器：
  - `contract.RuntimeReporter`
  - `thread.OrchestrationFacade`
- 桌面模式额外叠加 `uiwails.Module`，并通过 `fx.Populate` 取出 `*application.App` 和 `*uiwails.WailsLifecycle`。

**关键边界：** `app.Module` 明确 **不内嵌 `cmd/mcp-orch/orchestration.Module`**。源码注释也写明：agent orchestration 由独立 `mcp-orch` MCP 服务承载；桌面进程只暴露控制面与适配层，避免桌面二进制被再次拉起成子进程。

### 1.2 `internal/contract`：稳定契约层

`contract` 包是项目的稳定边界，设计意图很清晰：

- 只定义 **窄接口、数据结构、哨兵错误**，不放实现；
- 把 provider / thread / turn / hooks / mcpcontrol / orchestration 之间的依赖压缩为接口依赖；
- 把 RPC、sqlc、provider transport、standalone orchestration 这类易变实现隔离到外层；
- 允许桌面态在 **没有 orchestration service** 时，依靠 `optional:"true"` + noop 适配器完成大部分组装；
- 按能力域拆分：`approval`、`hooks`、`mcp_control`、`orchestration`、`provider`、`runtime_reporter`、`session_resolver`。

一句话：**`app` 负责“怎么装”，`contract` 负责“装出来的东西如何说话”。**

---

## 2. 目录结构（关键文件）

### 2.1 `internal/app/`

| 文件 | 作用 |
| --- | --- |
| `app.go` | 应用入口；初始化 logger/build info；创建普通 FX App 与桌面 FX App；统一 start/stop 超时；桌面模式接入 Wails 生命周期。 |
| `modules.go` | 根级 `Module` 组装清单；把 platform/store/module/provider 全部纳入 Fx；补充 `AsRPCRunner`、`newThreadOrchestrationFacade`、`newRuntimeReporter`。 |
| `runner.go` | 定义 `group:"runners"` 聚合输出；`BindRuntime` 负责把所有 Runner 作为运行时整体启动/关闭。 |
| `runtime_reporter_adapter.go` | 将可选的 `contract.OrchestrationService` 适配为 `contract.RuntimeReporter`；缺省时退化为 noop。 |
| `thread_orchestration_adapter.go` | 将可选的 `contract.OrchestrationService` 适配为 `thread.OrchestrationFacade`；缺省时退化为 noop。 |

### 2.2 `internal/contract/`

| 文件 | 作用 |
| --- | --- |
| `approval.go` | 工具调用审批契约：`ApprovalResponder`、`ApprovalDecision`。 |
| `errors.go` | 通用哨兵错误：`ErrSessionNotFound`。 |
| `hooks.go` | Hook 管理、生命周期、审批持久化契约；定义 hook 相关错误。 |
| `mcp_control.go` | MCP 控制面注册/通知/回调/组合控制面的契约与 `ToolInstance` 快照。 |
| `orchestration.go` | agent orchestration 的总边界：生命周期、turn、runtime、report、DAG。 |
| `provider.go` | provider 三层抽象：`Driver` / `Session` / `TurnHandle`。 |
| `runtime_reporter.go` | provider 向 orchestration 回报 runtime 元信息的最小契约。 |
| `session_resolver.go` | 由 threadID 解析运行中或可恢复 session 的契约。 |

---

## 3. 核心类型 / 接口

### 3.1 `app` 包关键类型

| 类型 | 文件 | 说明 |
| --- | --- | --- |
| `buildInfo` | `app.go` | 启动时记录版本、commit、build time、runtime。 |
| `RunnerResult` | `runner.go` | `fx.Out` 包装，把 `platformrunner.Runner` 输出到 `group:"runners"`。 |
| `runtimeParams` | `runner.go` | `BindRuntime` 的 `fx.In`：logger、runner 组、shutdowner、可选 `WailsLifecycle`。 |
| `runtimeReporterParams` | `runtime_reporter_adapter.go` | 构造 `contract.RuntimeReporter` 所需依赖。 |
| `threadOrchestrationParams` | `thread_orchestration_adapter.go` | 构造 `thread.OrchestrationFacade` 所需依赖。 |
| `orchestrationRuntimeReporter` / `noopRuntimeReporter` | `runtime_reporter_adapter.go` | 有/无 orchestration service 两种 runtime reporter 实现。 |
| `threadOrchestrationAdapter` / `noopThreadOrchestrationFacade` | `thread_orchestration_adapter.go` | 有/无 orchestration service 两种 thread 编排门面实现。 |

### 3.2 `contract` 包全部接口及实现者

> 说明：下表以生产实现为主；测试桩仅在必要处点到。`TurnHandle` 通过方法集可确认实现者，源码未写显式 `var _` 断言。

| 接口 | 定义文件 | 核心职责 | 生产实现者 | Fx / 备注 |
| --- | --- | --- | --- | --- |
| `ApprovalResponder` | `approval.go` | 响应工具审批结果 | `*internal/platform/rpc.ApprovalManager` | 由 `internal/platform/rpc.Module` 导出。 |
| `HookManager` | `hooks.go` | Hook 订阅、三阶段分发、resolve、查询待审批 | `*internal/platform/hooks.Manager` | 由 `internal/platform/hooks.Module` 导出。 |
| `HookLifecycle` | `hooks.go` | Hook 关闭与清理 | `*internal/platform/hooks.Manager` | 由 `internal/platform/hooks.Module` 导出。 |
| `HookReviewStore` | `hooks.go` | 持久化 pending hook review | `*internal/store/hookstore.Store` | `hookstore.NewStore` 返回 `contract.HookReviewStore`。 |
| `ToolRegistry` | `mcp_control.go` | MCP peer 注册、心跳、实例查询、关闭 | `*internal/platform/mcpcontrol.ToolRegistry` | 由 `internal/platform/mcpcontrol.Module` 导出。 |
| `ToolNotifier` | `mcp_control.go` | 按订阅/能力/selector 广播通知 | `*internal/platform/mcpcontrol.ToolRegistry` | 同上。 |
| `ToolHookCallback` | `mcp_control.go` | 向订阅 peer 分发 hook callback | `*internal/platform/mcpcontrol.ToolRegistry` | 同上。 |
| `PeerCallback` | `mcp_control.go` | 对单个 lease 做 before/check/after callback | `*internal/platform/mcpcontrol.ToolRegistry` | 同上。 |
| `ToolControlPlane` | `mcp_control.go` | 上述 4 个 MCP 控制面接口的组合 | `*internal/platform/mcpcontrol.ToolRegistry` | 组合接口，无单独实现。 |
| `OrchestrationService` | `orchestration.go` | agent 生命周期、turn、runtime、report、DAG | `*cmd/mcp-orch/orchestration.service` | 由 `cmd/mcp-orch/orchestration.Module` 导出；桌面态默认不内嵌该模块。 |
| `OrchestrationSessionCleaner` | `orchestration.go` | orchestration 关闭 agent 时清理本地 session | `*internal/provider/unified.sessionCleanerAdapter`；standalone noop：`cmd/mcp-orch.noopSessionCleaner` | `sessionCleanerAdapter` 额外实现了非契约方法 `RemoveSessionGeneration(...)`，供 orchestration 通过类型断言使用。 |
| `OrchestrationTurnStarter` | `orchestration.go` | orchestration 触发 turn 启动 | `internal/module/turn.orchestrationTurnStarter`；standalone noop：`cmd/mcp-orch.noopTurnStarter` | `turn.Module` 直接提供返回值类型 `contract.OrchestrationTurnStarter`。 |
| `Driver` | `provider.go` | provider 工厂抽象：启动/恢复 session | `*internal/provider/claudecli.driver`、`*internal/provider/codexapp.driver` | 通过 `contract.DriverFactory` 注入 `group:"drivers"`。 |
| `Session` | `provider.go` | 统一 provider session 抽象 | `*internal/provider/claudecli.session`、`*internal/provider/codexapp.session` | 由各 `Driver` 返回；由 `unified.SessionManager` 管理。 |
| `TurnHandle` | `provider.go` | 运行中 turn 的句柄 | `*internal/provider/claudecli.turnHandle`、`*internal/provider/codexapp.turnHandle` | 分别由 provider 的 `Session.StartTurn` 返回。 |
| `RuntimeReporter` | `runtime_reporter.go` | provider 上报 runtime 信息 | `internal/app.orchestrationRuntimeReporter`、`internal/app.noopRuntimeReporter` | `internal/app.Module` 提供桌面态实现。另有 `cmd/mcp-orch/orchestration.runtimeReporter` 辅助类型定义在 `service.go`，但当前 Fx 图未导出它。 |
| `SessionResolver` | `session_resolver.go` | 由 threadID 解析/自动恢复 session | `*internal/provider/unified.sessionResolver` | 由 `internal/provider/unified.Module` 导出。 |

### 3.3 `app` 包实现的外部桥接

| 外部接口 / 约束 | `app` 中实现 | 说明 |
| --- | --- | --- |
| `thread.OrchestrationFacade` | `threadOrchestrationAdapter` / `noopThreadOrchestrationFacade` | 把“大而全”的 `contract.OrchestrationService` 缩减成 `thread` 模块真正需要的 4 个动作：launch / stop / recover / bindSessionGeneration。 |
| `contract.RuntimeReporter` | `orchestrationRuntimeReporter` / `noopRuntimeReporter` | 把 `OrchestrationService.UpdateRuntime` 缩减成 provider 可消费的最小接口。 |
| `group:"runners"` 输出 | `AsRPCRunner(*rpc.Server)` | 这是 Fx 结果适配，不是行为适配；作用是把 `*rpc.Server` 包装进 `RunnerResult`。 |

### 3.4 契约相关核心数据与错误

- `ApprovalDecision`：审批结果载体；包含 `Approved *bool`、`Reason`、`Detail json.RawMessage`。
- `ToolInstance`：MCP peer 快照；字段包含 `Lease`、废弃中的 `LeaseID`、`BinaryName`、`AgentID`、`ThreadID`、`PID`、`Capabilities`、`Subscriptions`、`PeerKind`、`ClientKind`、`Status`、`ConfigVersion`。
- `RuntimeReport`：provider 上报 runtime 的最小载荷，仅含 `AgentID`、`Port`、`Provider`。
- `LaunchRequest`、`AgentSnapshot`、`AgentStateResult`、`AgentReportResult`、`ReportEvent`：orchestration 核心输入/输出模型。
- `CreateDAGRequest`、`CreateDAGNodeRequest`、`ListDAGsFilter`、`UpdateNodeStatusRequest`、`DAGSummary`、`DAGNode`、`DAGDetail`：任务编排 / DAG 相关模型。
- 哨兵错误：
  - `ErrSessionNotFound`
  - `ErrAgentNotFound`
  - `ErrHookReviewPermissionDenied`
  - `ErrHookReviewNotFound`

---

## 4. DI（Fx）模块组装

### 4.1 根组装：`internal/app/modules.go`

`app.Module` 不是命名 `fx.Module`，而是根级 `fx.Options(...)` 聚合：

```go
var Module = fx.Options(
    fx.Provide(NewLogger),
    fx.Provide(pidregistry.New),
    config.Module,
    db.Module,
    bus.Module,
    rpc.Module,
    hooks.Module,
    mcpcontrol.Module,
    platformrunner.Module,
    statemachine.Module,
    store.Module,
    dashboard.Module,
    lspgui.Module,
    skill.Module,
    thread.Module,
    turn.Module,
    uistate.Module,
    unified.Module,
    claudecli.Module,
    codexapp.Module,
    toolbridge.Module,
    fx.Provide(
        AsRPCRunner,
        newThreadOrchestrationFacade,
        newRuntimeReporter,
    ),
)
```

**源码对照结论：** 文档所述根模块清单与 `internal/app/modules.go` 当前实现一致；没有内嵌 orchestration module。

### 4.2 `NewApp` / `RunDesktop` 的组装差异

- `newFXApp(...)`
  - `Module`
  - `fx.Invoke(BindRuntime)`
- `newDesktopFXApp(...)`
  - `Module`
  - `uiwails.Module`
  - `fx.Invoke(BindRuntime)`

即：**桌面态 = 核心 `app.Module` + `uiwails.Module`。**

### 4.3 `group:"runners"` 的运行时汇聚

`BindRuntime` 消费所有 `platformrunner.Runner`：

```text
AsRPCRunner(*rpc.Server) ------------------------┐
uiwails.NewHTTPAssetServer() [desktop only] -----┼--> []platformrunner.Runner
(未来其它 Runner 也可继续追加) ------------------┘
                                       ↓
                                BindRuntime(...)
                                       ↓
                         platformrunner.RunGroup(ctx, runners, ...)
```

`BindRuntime` 的行为：

1. `OnStart`：创建后台 `runCtx`；
2. goroutine 中运行 `platformrunner.RunGroup`；
3. 任一 Runner 退出后：
   - 若是异常退出且不是 `context.Canceled`，记日志；
   - 若存在 `WailsLifecycle`，通知前端 backend failed；
   - 无论成功/失败，都调用 `fx.Shutdowner.Shutdown()`；
4. `OnStop`：取消 `runCtx`，等待 Runner 组退出或 stop 超时。

### 4.4 契约与关键桥接的 Fx 输出图

| 输出接口 / 值 | 提供者 | 主要消费者 |
| --- | --- | --- |
| `contract.ApprovalResponder` | `rpc.Module` 中 `func(m *ApprovalManager) contract.ApprovalResponder { return m }` | `module/turn`、provider 审批回调 |
| `contract.HookManager` / `HookLifecycle` | `platform/hooks.Module` | `platform/mcpcontrol` |
| `contract.HookReviewStore` | `store/hookstore.NewStore` | `platform/hooks.HookResolver` |
| `contract.ToolRegistry` / `ToolNotifier` / `ToolHookCallback` / `PeerCallback` / `ToolControlPlane` | `platform/mcpcontrol.Module` | hooks / MCP 控制面 / 相关集成 |
| `thread.SessionStarter` | `unified.NewClient`（`fx.As(new(thread.SessionStarter))`） | `module/thread.NewService` |
| `thread.SessionProvider` | `unified.NewSessionProvider`（`fx.As(new(thread.SessionProvider))`） | `module/thread.NewService` |
| `turn.SessionProvider` | `unified.NewTurnSessionProvider` | `turn.NewOrchestrationTurnStarter` |
| `contract.OrchestrationSessionCleaner` | `unified.NewSessionCleaner`（`fx.As(new(contract.OrchestrationSessionCleaner))`） | `cmd/mcp-orch/orchestration.NewService` |
| `contract.SessionResolver` | `unified.NewSessionResolver` | `rpc.NewCapabilityResolver`、`turn.NewTurnHandlers` |
| `rpc.CapabilityResolver` | `rpc.NewCapabilityResolver(contract.SessionResolver)` | `thread.NewThreadHandlers`、`turn.NewTurnHandlers` |
| `group:"drivers"` (`contract.DriverFactory`) | `provider/claudecli.Module`、`provider/codexapp.Module` | `unified.NewRegistry` |
| `contract.OrchestrationTurnStarter` | `turn.NewOrchestrationTurnStarter` | `cmd/mcp-orch/orchestration.NewService` |
| `contract.RuntimeReporter` | `app.newRuntimeReporter` | `provider/claudecli.NewDriverFactory`、`provider/codexapp.NewDriverFactory` |
| `thread.OrchestrationFacade` | `app.newThreadOrchestrationFacade` | `module/thread.NewService` |

### 4.5 关键注入链（按职责）

#### A. Provider 装配链

```text
claudecli.Module / codexapp.Module
        ↓ (group:"drivers")
provider/unified.Registry
        ↓
provider/unified.Client  --as--> thread.SessionStarter
provider/unified.SessionManager
        ├─> provider/unified.NewSessionProvider --as--> thread.SessionProvider
        ├─> provider/unified.NewTurnSessionProvider ----> turn.SessionProvider
        ├─> provider/unified.NewSessionCleaner ---------> contract.OrchestrationSessionCleaner
        └─> provider/unified.NewSessionResolver --------> contract.SessionResolver
```

#### B. Thread / Turn / RPC 链

```text
thread.SessionStarter + thread.SessionProvider + thread.OrchestrationFacade
        ↓
module/thread.Service
        ├─> Start / Resume / Recover / Stop 线程生命周期
        └─> bindSessionGeneration(...)
                ↓
      thread.OrchestrationFacade.BindSessionGeneration(...)
                ↓
      contract.OrchestrationService.BindSessionGeneration(...)

contract.SessionResolver
        ↓
rpc.NewCapabilityResolver(...)
        ↓
thread RPC handlers / turn RPC handlers

turn.SessionProvider + turn.Service
        ↓
turn.NewOrchestrationTurnStarter
        ↓
contract.OrchestrationTurnStarter
        ↓
cmd/mcp-orch/orchestration.service
```

#### C. Hook 与 MCP 控制面链

```text
store/hookstore.Store
        ↓ as contract.HookReviewStore
platform/hooks.HookResolver
        ↓
platform/hooks.Manager
        ↓ as contract.HookManager + contract.HookLifecycle
platform/mcpcontrol.ToolRegistry
        ↓ as ToolRegistry / ToolNotifier / ToolHookCallback / PeerCallback / ToolControlPlane
```

#### D. 可选 orchestration 链

```text
[optional] contract.OrchestrationService
        ├─> app.newRuntimeReporter ---------> contract.RuntimeReporter
        ├─> app.newThreadOrchestrationFacade -> thread.OrchestrationFacade
        ├─> dashboard.Module / uistate.Module / uiwails.Module（均 optional 注入）
        └─> platform/mcpcontrol 默认 runtime/completion report handler（optional 注入）
```

**重点：** 桌面 App 默认不提供 `contract.OrchestrationService`，因此依赖它的桌面侧链路必须要么 `optional:"true"`，要么通过 noop 适配器降级。

---

## 5. 关键函数 / 方法签名

### 5.1 `internal/app/app.go`

```go
func NewLogger() *slog.Logger
func NewApp() *fx.App
func Run() error
func RunDesktop(frontendFS fs.FS) error
func newFXApp(options ...fx.Option) *fx.App
func runApp(app *fx.App) error
func newDesktopFXApp(options ...fx.Option) *fx.App
func stopFXApp(parent context.Context, app *fx.App) error
func watchFXShutdown(app *fx.App, lifecycle *uiwails.WailsLifecycle) chan struct{}
```

要点：

- `NewLogger` 初始化 console/file logger，并输出构建信息；
- `RunDesktop` 用 `fx.Populate(&wailsApp, &lifecycle)` 从容器中取 UI 运行对象；
- `watchFXShutdown` 监听 `app.Done()`，在后端异常退出时通知前端。

### 5.2 `internal/app/modules.go`

```go
var Module = fx.Options(...)
func AsRPCRunner(server *rpc.Server) RunnerResult
```

### 5.3 `internal/app/runner.go`

```go
type RunnerResult struct {
    fx.Out
    Runner platformrunner.Runner `group:"runners"`
}

func BindRuntime(lc fx.Lifecycle, p runtimeParams)
```

### 5.4 `internal/app/runtime_reporter_adapter.go`

```go
func newRuntimeReporter(p runtimeReporterParams) contract.RuntimeReporter
func (r orchestrationRuntimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error
func (r noopRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error
```

### 5.5 `internal/app/thread_orchestration_adapter.go`

```go
func newThreadOrchestrationFacade(p threadOrchestrationParams) thread.OrchestrationFacade
func (a threadOrchestrationAdapter) LaunchAgent(ctx context.Context, req thread.LaunchAgentRequest) error
func (a threadOrchestrationAdapter) StopAgent(ctx context.Context, agentID string) error
func (a threadOrchestrationAdapter) Recover(ctx context.Context, agentID string) error
func (a threadOrchestrationAdapter) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error
```

### 5.6 `contract` 包接口签名（按领域完整列出）

#### 审批

```go
type ApprovalResponder interface {
    Respond(callID string, requestID *int64, decision ApprovalDecision) error
}
```

#### Hook

```go
type HookManager interface {
    Subscribe(ctx context.Context, lease mcp.LeaseKey, req mcp.HookSubscribeRequest) (mcp.HookSubscribeResponse, error)
    DispatchBefore(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.BeforeDecision, error)
    DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.CheckDecision, error)
    DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error)
    Resolve(ctx context.Context, callerLease mcp.LeaseKey, req mcp.HookResolveRequest) (mcp.HookResolveResponse, error)
    GetPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error)
}

type HookLifecycle interface {
    ShutdownHooks(ctx context.Context, lease mcp.LeaseKey) error
}

type HookReviewStore interface {
    SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error
    GetPendingReview(ctx context.Context, hookCallID string) (mcp.PendingHookReview, error)
    GetResolvedReview(ctx context.Context, hookCallID string) (string, time.Time, string, error)
    ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error)
    ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error
    CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error)
    CancelPendingReviewsByAgent(ctx context.Context, agentID string) (int, error)
    CancelExpiredReviews(ctx context.Context) (int, error)
    RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error)
}
```

#### MCP 控制面

```go
type ToolRegistry interface {
    Register(ctx context.Context, req mcp.RegisterRequest) (mcp.RegisterResponse, error)
    Heartbeat(ctx context.Context, req mcp.HeartbeatRequest) (mcp.HeartbeatResponse, error)
    GetInstance(key mcp.LeaseKey) (ToolInstance, bool)
    ShutdownInstance(ctx context.Context, key mcp.LeaseKey, req mcp.ShutdownRequest) error
}

type ToolNotifier interface {
    NotifyBySubscription(ctx context.Context, topic, method string, params any) error
    NotifyByCapability(ctx context.Context, capability, method string, params any) error
    NotifyBySelector(ctx context.Context, sel mcp.Selector, method string, params any) error
    NotifyConfigChanged(ctx context.Context, topic string, scope *mcp.SelectorScope, configVersion int64, payload json.RawMessage) error
}

type ToolHookCallback interface {
    CallbackHookBefore(ctx context.Context, topic string, payload mcp.HookPayload) error
    CallbackHookCheck(ctx context.Context, topic string, payload mcp.HookPayload) error
    CallbackHookAfter(ctx context.Context, topic string, payload mcp.HookPayload) error
}

type PeerCallback interface {
    CallbackBefore(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error)
    CallbackCheck(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error)
    CallbackAfter(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error)
}

type ToolControlPlane interface {
    ToolRegistry
    ToolNotifier
    ToolHookCallback
    PeerCallback
}
```

#### Orchestration

```go
type OrchestrationService interface {
    LaunchAgent(ctx context.Context, req LaunchRequest) error
    ListAgents(ctx context.Context) ([]AgentSnapshot, error)
    StopAgent(ctx context.Context, agentID string) error
    SubmitTurn(ctx context.Context, req TurnSubmission) error
    CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error
    Recover(ctx context.Context, agentID string) error
    BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error
    Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)
    UpdateRuntime(ctx context.Context, report RuntimeReport) error
    GetState(ctx context.Context, agentID string) (AgentStateResult, error)
    GetReport(ctx context.Context, agentID string) (AgentReportResult, error)
    RememberReportRequest(ctx context.Context, req RememberReportRequest) (RememberReportRequestResult, error)
    HandleReportEvent(ctx context.Context, event ReportEvent) (ReportEventResult, error)
    CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error)
    GetDAG(ctx context.Context, dagKey string) (DAGDetail, error)
    ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAGSummary, error)
    UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error)
}

type OrchestrationSessionCleaner interface {
    RemoveSession(agentID string)
}

type OrchestrationTurnStarter interface {
    StartTurn(ctx context.Context, submission TurnSubmission) (string, error)
}
```

#### Provider

```go
type Driver interface {
    Name() string
    StartSession(ctx context.Context, req dto.StartSessionRequest) (Session, error)
    ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (Session, error)
}

type Session interface {
    ThreadID() string
    RolloutPath() string
    Capabilities() dto.CapabilitySet

    StartTurn(ctx context.Context, req dto.TurnRequest) (TurnHandle, error)
    Interrupt(ctx context.Context, req dto.InterruptRequest) error
    ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error

    ListThreads(ctx context.Context) ([]dto.ThreadRef, error)
    ForkThread(ctx context.Context, req dto.ForkRequest) (dto.ForkResult, error)
    ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error)

    Configure(ctx context.Context, patch dto.ThreadConfigPatch) error
    Close(ctx context.Context) error
    ForceStop() error
}

type TurnHandle interface {
    LocalID() string
    ProviderID() string
    Done() <-chan struct{}
    Err() error
}
```

#### 其它桥接契约

```go
type RuntimeReporter interface {
    ReportRuntime(ctx context.Context, report RuntimeReport) error
}

type SessionResolver interface {
    ResolveSession(ctx context.Context, threadID string) (Session, error)
}
```

---

## 6. 依赖关系

### 6.1 `contract` 被哪些生产包消费

| 包 | 主要引用的 contract 能力 |
| --- | --- |
| `internal/app` | `OrchestrationService`、`RuntimeReporter`、`LaunchRequest`、`RuntimeReport` |
| `internal/platform/rpc` | `ApprovalResponder`、`SessionResolver`、`ApprovalDecision` |
| `internal/platform/hooks` | `HookManager`、`HookLifecycle`、`HookReviewStore`、`PeerCallback`、hook 错误 |
| `internal/platform/mcpcontrol` | `ToolRegistry`、`ToolNotifier`、`ToolHookCallback`、`PeerCallback`、`ToolControlPlane`、`HookManager`、`HookLifecycle`、`OrchestrationService`、`RuntimeReport`、`ReportEvent` |
| `internal/store/hookstore` | `HookReviewStore`、`ErrHookReviewNotFound` |
| `internal/provider/unified` | `DriverFactory`、`Driver`、`Session`、`SessionResolver`、`OrchestrationSessionCleaner`、`ErrSessionNotFound` |
| `internal/provider/claudecli` | `Driver`、`Session`、`TurnHandle`、`RuntimeReporter`、`RuntimeReport` |
| `internal/provider/codexapp` | `Driver`、`Session`、`TurnHandle`、`RuntimeReporter`、`RuntimeReport` |
| `internal/module/thread` | `Session`、`TurnHandle`、`ErrAgentNotFound` |
| `internal/module/turn` | `Session`、`TurnHandle`、`SessionResolver`、`ApprovalResponder`、`OrchestrationTurnStarter`、`ErrSessionNotFound` |
| `internal/module/dashboard` | `OrchestrationService`、`AgentSnapshot`、`AgentReportResult`、`ListDAGsFilter`、`DAGSummary`、`DAGDetail` |
| `internal/module/uistate` | `OrchestrationService`、`AgentSnapshot` |
| `internal/ui/wails` | `OrchestrationService` |
| `cmd/mcp-orch` / `cmd/mcp-orch/orchestration` | `OrchestrationService`、`OrchestrationSessionCleaner`、`OrchestrationTurnStarter` 及相关请求/响应模型 |

### 6.2 `app.Module` 直接纳入的模块

**Platform 层**

- `config.Module`
- `db.Module`
- `bus.Module`
- `rpc.Module`
- `hooks.Module`
- `mcpcontrol.Module`
- `platformrunner.Module`
- `statemachine.Module`

**Store 层**

- `store.Module`

**业务模块层**

- `dashboard.Module`
- `lspgui.Module`
- `skill.Module`
- `thread.Module`
- `turn.Module`
- `uistate.Module`

**Provider / 集成层**

- `unified.Module`
- `claudecli.Module`
- `codexapp.Module`
- `toolbridge.Module`

**本地提供者 / 适配器**

- `NewLogger`
- `pidregistry.New`
- `AsRPCRunner`
- `newThreadOrchestrationFacade`
- `newRuntimeReporter`

**桌面模式额外叠加**

- `uiwails.Module`

### 6.3 适配器模式落点（源码校对后修正）

1. **`newThreadOrchestrationFacade` 是“窄门面适配器”**  
   它把大的 `contract.OrchestrationService` 缩成 `thread` 模块只需要的 4 个动作：
   - `LaunchAgent`
   - `StopAgent`
   - `Recover`
   - `BindSessionGeneration`

   该适配器还做了字段裁剪：`thread.LaunchAgentRequest` 只映射到 `contract.LaunchRequest` 的 `AgentID / Name / ParentID / Cwd / Command / Env`，并不会传 `Prompt / Instructions`。

2. **`newRuntimeReporter` 是“单方法端口适配器”**  
   它只暴露 `ReportRuntime(...)`，内部实际转调 `OrchestrationService.UpdateRuntime(...)`；如果 orchestration service 不存在，则退化为 debug 日志 noop。

3. **`AsRPCRunner` 只是 Fx 结果适配，不是业务适配器**  
   它的职责只是把 `*rpc.Server` 包装为 `RunnerResult{Runner: server}`，让 RPC Server 进入 `group:"runners"`。

4. **桌面态的 noop 不是“假实现业务”，而是“断开可选 orchestration 链路”**  
   即使 `contract.OrchestrationService` 缺失，`thread.Start/Resume` 依旧会通过 `unified.Client` 启动 provider session；只是外部 orchestration 进程的 launch / stop / recover / generation bind 不再发生。

### 6.4 关键依赖结论

1. **`contract` 是真正的横向边界层。** provider、thread、turn、rpc、hooks、mcpcontrol、dashboard、uistate、wails、`cmd/mcp-orch` 都通过它解耦。  
2. **`app` 是根组装层，不是业务层。** 它只决定“装哪些模块、以什么生命周期运行”。  
3. **桌面端对 orchestration 是可选依赖。** `contract.OrchestrationService` 缺失时，通过 `noopRuntimeReporter` 与 `noopThreadOrchestrationFacade` 保证 provider / thread 仍可构造。  
4. **provider 装配采用 `group:"drivers"`。** 新增 provider 只需追加一个 `contract.DriverFactory`，无需改 thread / turn / rpc 调用链。  
5. **session generation 是 thread ↔ orchestration 的关键衔接点。** `SessionManager.Register()` 生成 generation，`thread.bindSessionGeneration()` 上报，`mcp-orch/orchestration` 在进程退出时再用 generation-aware cleaner 做精确清理。  

---

## 7. 速记版架构图

```text
internal/app
  ├─ NewApp / Run / RunDesktop
  ├─ Module = 平台模块 + store + 业务模块 + provider 模块 + 适配器
  ├─ AsRPCRunner ------------------------------------┐
  ├─ [desktop] uiwails.NewHTTPAssetServer -----------┼--> group:"runners" --> BindRuntime --> RunGroup
  ├─ newRuntimeReporter -----------------------------┘
  └─ newThreadOrchestrationFacade

internal/contract
  ├─ approval         -> rpc / turn 审批流
  ├─ hooks            -> hooks / mcpcontrol / hookstore
  ├─ mcp_control      -> mcpcontrol / hooks
  ├─ orchestration    -> cmd/mcp-orch / dashboard / uistate / wails / app
  ├─ provider         -> unified / claudecli / codexapp / thread / turn
  ├─ runtime_reporter -> app / providers
  └─ session_resolver -> unified / rpc / turn

provider/unified
  ├─ group:"drivers" -> Registry
  ├─ Client -----------------------> thread.SessionStarter
  ├─ SessionManager
  ├─ SessionProviderAdapter -------> thread.SessionProvider / turn.SessionProvider
  ├─ SessionCleanerAdapter --------> contract.OrchestrationSessionCleaner
  └─ SessionResolver -------------> contract.SessionResolver -> rpc.CapabilityResolver
```

## 审查补遗

- 已补齐 `5.6` 节中先前遗漏的接口签名：`HookReviewStore`、`ToolHookCallback`、`PeerCallback`、`ToolControlPlane`、`OrchestrationSessionCleaner`、`OrchestrationTurnStarter`。  
- 已修正 `RuntimeReporter` 的实现说明：`cmd/mcp-orch/orchestration` 中确有 `runtimeReporter` 辅助类型，但它定义在 `service.go` 内，**当前并未进入 Fx 导出图**；桌面态真正接线的是 `internal/app` 下两种 reporter。  
- 已补全此前文档未展开的依赖注入链：`group:"drivers" -> unified.Registry -> Client / SessionManager / SessionResolver -> thread / turn / rpc`，以及 `SessionGeneration -> BindSessionGeneration -> generation-aware session cleaner` 这条链路。  
- 已澄清适配器模式的真实落点：`newThreadOrchestrationFacade` 与 `newRuntimeReporter` 是薄适配层；`AsRPCRunner` 只是 Fx 结果包装，不属于业务语义适配。  
- 已核对 `internal/app/modules.go`：根模块清单与文档一致，仍然 **不内嵌 orchestration module**；桌面态仅通过 optional 依赖和 noop 适配器感知 orchestration。  
