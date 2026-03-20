# P6 修复审查 — Agent A

## 逐项验证+行号

### 1. Wails 依赖
- `OK`：根模块 `go.mod` 已包含 `github.com/wailsapp/wails/v3 v3.0.0-alpha.74 // indirect`，见 `go.mod:54`。
- `Warning`：当前它仍被标记为 `// indirect`，但仓内已有直接 import，例如 `internal/app/app.go:11-13`、`internal/ui/wails/module.go:16`、`internal/ui/wails/assets.go:8`。

### 2. rpc.Server.Dispatch
- `OK`：`internal/platform/rpc/server.go` 已定义 `Dispatch`，签名为 `func (s *Server) Dispatch(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)`，返回值确实是 `json.RawMessage`，见 `internal/platform/rpc/server.go:44-64`。
- `OK`：`Dispatch` 不是直接调用 service，而是通过 `jrpcserver.NewLocal(s.methods, ...)` 和 `local.Client.CallResult(...)` 走 jrpc2 本地调用，见 `internal/platform/rpc/server.go:49-63`。
- `OK`：`s.methods` 来自 `Server.Register` 合并的 `handler.Map`，不是单独旁路，见 `internal/platform/rpc/server.go:36-40`。
- `OK`：strict / ThreadScope / CapabilityGate 仍在 handler 包装层里。`ThreadHandler` 组合 `ThreadScope()+StrictHandler()`，`CapabilityThreadHandler` 组合 `ThreadScope()+CapabilityGate()+StrictHandler()`，见 `internal/platform/rpc/handler.go:88-96`；`StrictHandler` 本身开启 object-only strict decode，见 `internal/platform/rpc/strict.go:10-17`。实际 handler 注册也确实使用这些包装器，例如 `internal/module/thread/rpc.go:20-83`、`internal/module/turn/rpc.go:32-92`。
- `OK`：Wails binding 侧 `App.dispatch` 的字段签名与 `Server.Dispatch` 一致，`CallAPI` 解析 JSON 后直接调用该签名并再解码结果，见 `internal/ui/wails/binding.go:16-39`。

### 3. RunDesktop()
- `OK`：`internal/app/app.go` 已存在 `RunDesktop()`，见 `internal/app/app.go:30-50`。
- `Blocker`：从函数表面看是 `fx.Start -> Wails Run() -> fx.Stop`，见 `internal/app/app.go:35-50`；但这条桌面主循环并未真正闭环，因为 `newDesktopFXApp()` 仍把 `uiwails.Module` 和 `fx.Invoke(BindRuntime)` 一起装入 FX，见 `internal/app/app.go:68-79`。
- `Blocker`：`uiwails.Module` 自己已经提供 `application.App`、`application.Service` 和 `group:"runners"` 的 Wails runner，见 `internal/ui/wails/module.go:19-25`、`internal/ui/wails/module.go:49-62`；而 `BindRuntime` 会在 `fx.OnStart` 里启动 `platformrunner.RunGroup(...)`，见 `internal/app/runner.go:32-53`。这意味着 desktop 路径在 `fx.Start()` 之后就已开始跑 runner。
- `Blocker`：`RunDesktop()` 又额外手工创建了第二个 `wailsApp := createWailsApp(...)` 并调用 `wailsApp.Run()`，见 `internal/app/app.go:39-50`。当前代码同时存在“FX 内建 Wails app/runner”与“RunDesktop 手工 Wails app”两条路径。
- `Blocker`：手工 `createWailsApp()` 没有注册 `Services: []application.Service{...}`，见 `internal/app/app.go:98-123`；而 FX 内建 `NewWailsApplication()` 是有注册 binding service 的，见 `internal/ui/wails/module.go:49-60`。因此手工 app 缺少绑定服务。
- `Blocker`：`RunDesktop()` 在 `fx.Start()` 前就要求 `lifecycle != nil`，否则直接返回 `wails lifecycle not available`，见 `internal/app/app.go:31-34`；但 `uiwails.Module` 的 provider 列表并没有 `NewWailsLifecycle`，见 `internal/ui/wails/module.go:19-27`。当前装配链里 lifecycle 没有真正接入。
- `OK`：headless `Run()` 路径保持原样，仍是 `Run() -> runApp(NewApp())`，而 `runApp()` 仍是 `fx.Start -> <-app.Done() -> fx.Stop`，见 `internal/app/app.go:26-28`、`internal/app/app.go:60-66`。

### 4. 双向 shutdown
- `Blocker`：Wails quit -> `fx.Shutdowner` 的接口壳子已写好。`WailsLifecycle.ShouldQuit()` 和 `OnShutdown()` 都会走 `requestBackendShutdown()`，而 `requestBackendShutdown()` 会调用已设置的 shutdowner，见 `internal/ui/wails/lifecycle.go:82-103`、`internal/ui/wails/lifecycle.go:145-155`。
- `Blocker`：但当前没有任何地方调用 `SetShutdownerFunc(...)`。仓内命中只有方法定义本身，见 `internal/ui/wails/lifecycle.go:61-69`；`RunDesktop()` 只调用了 `SetQuitFunc(...)` 和 `SetEventEmitter(...)`，见 `internal/app/app.go:40-43`。所以 Wails quit 目前没有真正连到 `fx.Shutdowner`。
- `Warning`：runner error -> `wailsApp.Quit()` 也不是直接路径。`BindRuntime` 在 runner 报错时做的是 `_ = p.Shutdowner.Shutdown()`，然后仅在 `p.Lifecycle != nil` 时调用 `p.Lifecycle.NotifyBackendFailed()`，见 `internal/app/runner.go:44-49`。
- `Warning`：反向 quit 目前只能依赖 `watchFXShutdown()` 把 `app.Done()` 翻译成 `lifecycle.NotifyBackendFailed()`，再由 `NotifyBackendFailed()` 走到已存的 `quitFunc`，见 `internal/app/app.go:81-91`、`internal/ui/wails/lifecycle.go:105-112`、`internal/ui/wails/lifecycle.go:166-173`。这条路径依赖 lifecycle 已经存在并已注入 `quitFunc`，但当前 lifecycle 本身未装配完成，见 `internal/app/app.go:31-34`、`internal/ui/wails/module.go:19-27`。
- `Blocker`：desktop 模式并未稳定禁用 `run.Group` signal actor。`RunGroup()` 只认识 `EnableSignals bool`，没有显式 desktop 模式选项，见 `internal/platform/runner/group.go:18-20`、`internal/platform/runner/group.go:30-34`。`BindRuntime` 试图用 `EnableSignals: p.Lifecycle == nil` 来关 signal actor，见 `internal/app/runner.go:38-40`；但 `Lifecycle` 在参数上是 optional，且 `uiwails.Module` 没有提供它，见 `internal/app/runner.go:25`、`internal/ui/wails/module.go:19-27`。按当前装配结果，desktop 路径不会可靠关闭 signal actor。

### 5. Wails event bridge
- `OK`：`internal/ui/wails/bridge.go` 文件存在，见 `internal/ui/wails/bridge.go:1-109`。
- `OK`：`Start()` 订阅了 3 类 bus 事件：`agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted`，见 `internal/ui/wails/bridge.go:53-63`。
- `OK`：`publish()` 推送到 `bridgeEventName`，而 `bridgeEventName = "bridge-event"`，与 V2 频道名一致，见 `internal/ui/wails/bridge.go:81-88`、`internal/ui/wails/lifecycle.go:12`。
- `Blocker`：但 `EventBridge` 现在没有装进 FX。`NewEventBridge(...)` 在仓内没有被 provider/invoke 引用，`uiwails.Module` 也没有 `Provide(NewEventBridge)` 或 `Invoke` 它的 `Start()`，见 `internal/ui/wails/bridge.go:31-64`、`internal/ui/wails/module.go:19-27`。
- `Blocker`：当前真正装配进 FX 的是 `registerCoreEvents()`，它直接往 Wails 发原始频道 `ui/state/changed`、`turn/started`、`turn/completed`，不是统一的 `bridge-event`，见 `internal/ui/wails/module.go:76-108`。

### 6. lifecycle
- `OK`：`internal/ui/wails/lifecycle.go` 文件存在，见 `internal/ui/wails/lifecycle.go:1-191`。
- `OK`：`ShouldQuit()` 会先检查活跃 agent 数量：调用 `activeAgentCount()`，并在 `activeCount > 0` 时阻止立即退出，见 `internal/ui/wails/lifecycle.go:82-95`、`internal/ui/wails/lifecycle.go:124-135`。
- `OK`：活跃 agent 存在时会发 quit overlay，事件名是 `app-will-quit`，见 `internal/ui/wails/lifecycle.go:13`、`internal/ui/wails/lifecycle.go:92-94`、`internal/ui/wails/lifecycle.go:137-143`。
- `OK`：`lifecycle.go` 没有 import `fx`；其 import 列表只有 `context`、`log/slog`、`sync`、`sync/atomic`、`time`，见 `internal/ui/wails/lifecycle.go:3-9`。
- `Blocker`：lifecycle 逻辑虽然写了，但装配没完成。`NewWailsLifecycle(...)` 和 `ActiveAgentCounter` 的引用仍停留在该文件内，`uiwails.Module` 没有提供 lifecycle，也没有把某个 active-agent counter 接进来，见 `internal/ui/wails/lifecycle.go:17-25`、`internal/ui/wails/lifecycle.go:43-51`、`internal/ui/wails/module.go:19-27`。

### 7. 代码守卫
- `OK`：文件行数守卫阈值是 `MaxFileLines = 400`，见 `internal/archtest/guardlib.go:17-24`；守卫测试入口是 `TestCodeSizeGuard`，见 `internal/archtest/code_size_guard_test.go:10-24`。
- `OK`：当前 worktree 的未跟踪新增文件都小于 400 行；实测最大的是 `internal/ui/wails/lifecycle.go` 191 行、`internal/ui/wails/binding_native.go` 179 行、`internal/ui/wails/binding.go` 140 行、`internal/ui/wails/bridge.go` 109 行，其余更小。对应文件本体见 `internal/ui/wails/lifecycle.go:1-191`、`internal/ui/wails/binding_native.go:1-179`、`internal/ui/wails/binding.go:1-140`、`internal/ui/wails/bridge.go:1-109`。
- `OK`：本次对审查范围文件跑 LSP diagnostics 未报错，审查文件集包括 `internal/platform/rpc/server.go`、`internal/app/app.go`、`internal/app/runner.go`、`internal/platform/runner/group.go`、`internal/ui/wails/bridge.go`、`internal/ui/wails/lifecycle.go`、`internal/ui/wails/module.go`、`internal/ui/wails/binding.go`。
- `OK`：`go build ./...` 通过。
- `OK`：`go vet ./...` 通过。
- `OK`：`go test ./internal/archtest/... -count=1` 通过，结果为 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 0.934s`；对应守卫入口见 `internal/archtest/code_size_guard_test.go:10-24`。

## 结论（Blocker / Warning / OK）

### Blocker
- desktop 装配链未闭环：`RunDesktop()` 不是单一的 `fx.Start -> Wails Run -> fx.Stop`，而是同时保留了 FX 内建 Wails app/runner 与手工 `createWailsApp()` 两条路径；并且手工 app 丢了 binding service，见 `internal/app/app.go:30-50`、`internal/app/app.go:98-123`、`internal/ui/wails/module.go:19-25`、`internal/ui/wails/module.go:49-62`、`internal/app/runner.go:32-53`。
- lifecycle 虽已实现，但没有 provider 装配；`RunDesktop()` 还在入口显式要求 `lifecycle != nil`，因此当前 desktop 路径处于未闭环状态，见 `internal/app/app.go:31-34`、`internal/ui/wails/module.go:19-27`、`internal/ui/wails/lifecycle.go:43-51`。
- 双向 shutdown 未真正接通：`SetShutdownerFunc()` 有定义但无调用；runner error 也没有直接 `wailsApp.Quit()` 路径，只能依赖未装好的 lifecycle 间接翻译，见 `internal/ui/wails/lifecycle.go:61-69`、`internal/app/app.go:40-43`、`internal/app/runner.go:44-49`、`internal/app/app.go:81-91`。
- `bridge.go` 文件虽存在且频道名正确，但未接入 FX；当前 live path 仍直接发 `ui/state/changed` / `turn/started` / `turn/completed`，没有统一到 `bridge-event`，见 `internal/ui/wails/bridge.go:42-64`、`internal/ui/wails/bridge.go:81-88`、`internal/ui/wails/module.go:76-108`。
- desktop 模式没有显式的 runner 组选项，当前靠 `EnableSignals` 间接推断，且由于 lifecycle 未注入，signal actor 不会被可靠禁用，见 `internal/platform/runner/group.go:18-20`、`internal/platform/runner/group.go:30-34`、`internal/app/runner.go:25`、`internal/app/runner.go:38-40`。

### Warning
- `go.mod` 已有 `github.com/wailsapp/wails/v3 v3.0.0-alpha.74`，但仍标记为 `// indirect`，与现有直接 import 不一致，见 `go.mod:54`、`internal/app/app.go:11-13`、`internal/ui/wails/module.go:16`。
- `rpc.Server.Dispatch` 的实现本身合理，但当前 desktop/Wails binding 总装配不通，导致这个本地 dispatch 路径虽然存在，桌面模式下仍未形成可验证的完整调用链，见 `internal/platform/rpc/server.go:44-64`、`internal/ui/wails/binding.go:22-39`、`internal/app/app.go:30-50`。

### OK
- `rpc.Server.Dispatch` 已按 jrpc2 本地调用实现，并复用了注册后的 handler map，因此不会绕过 strict / ThreadScope / CapabilityGate 链，见 `internal/platform/rpc/server.go:44-64`、`internal/platform/rpc/handler.go:88-96`、`internal/platform/rpc/strict.go:10-17`。
- headless `Run()` 路径未被改坏，仍保持原来的 `Start -> wait Done -> Stop` 语义，见 `internal/app/app.go:26-28`、`internal/app/app.go:60-66`。
- `lifecycle.go` 本身满足“不 import fx”的约束，也已经实现活跃 agent 检查和 quit overlay 逻辑，见 `internal/ui/wails/lifecycle.go:3-9`、`internal/ui/wails/lifecycle.go:82-95`、`internal/ui/wails/lifecycle.go:137-143`。
- 静态守卫层面目前是绿色：`go build ./...`、`go vet ./...`、`go test ./internal/archtest/... -count=1` 全通过，且新增文件行数都未超过 `internal/archtest/guardlib.go:17-24` 的 400 行阈值。
