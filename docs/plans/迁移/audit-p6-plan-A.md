# P6 计划审查 — Agent A

## 1. Wails 依赖

- Blocker。V3 根模块 `go.mod` 的依赖表只有 `jrpc2`、`websocket`、`pgx`、`event`、`run`、`fx` 等，未包含 `github.com/wailsapp/wails/v3`，见 `go.mod:5-27`。计划本身也把该依赖列为待确认项，见 `docs/plans/迁移/p6-execution-plan.md:166`。
- 同名依赖只出现在 V2 子模块，见 `go-agent-v2/go.mod:15`。因此当前仓库状态下，P6 批次 A/B 不能直接在 V3 根模块里编译 Wails 绑定层。

## 2. dispatch 注入

- Warning。`rpc.Server` 只暴露了 `Register`、`NotifyAll`、`Run`，内部 `methods handler.Map` 是私有字段，见 `internal/platform/rpc/server.go:16-23`、`internal/platform/rpc/server.go:34-40`、`internal/platform/rpc/server.go:42-66`。当前不存在公开的本地 `dispatch`/`invoke` 入口。
- Warning。`fx` 侧当前只提供 `NewServer`/`NewPushBridge` 等对象，并通过 `registerAllHandlers` 把 `group:"rpc_handlers"` 注册进 `server.Register(...)`；没有向外导出 `dispatch func(ctx, method, params)`，见 `internal/platform/rpc/module.go:14-24`、`internal/platform/rpc/module.go:47-56`。
- Warning。这里不是“把 `handler.Map` 暴露出来”这么简单。现有 handler 都被包装成 `handler.Func`，并依赖 `*jrpc2.Request`、严格参数解码、`threadId` 注入、capability gate 等中间件，见 `internal/platform/rpc/strict.go:10-17`、`internal/platform/rpc/handler.go:43-96`。新增本地 dispatch 时，如果不构造等价的请求对象并复用这条调用链，就会绕过 `ThreadScope` / `CapabilityGate` / strict decode。
- 改动面评估：至少需要改 `internal/platform/rpc/server.go` 新增公开 `Invoke`/`Dispatch` 能力，并改 `internal/platform/rpc/module.go` 暴露可注入的函数或直接注入 `*rpc.Server`；随后 `internal/ui/wails/binding.go` 才能安全复用这条链路。结论：P6 这里是“可做但未具备现成接口”，不是现成装配。

## 3. event bridge

- Warning。当前 `PushBridge` 是 jrpc2 专用桥。`NotifyClient` 的入参是 `*jrpc2.Server`，实际调用的是 `server.Notify(...)`，见 `internal/platform/rpc/push.go:22-40`。这和 Wails 的 `Event.Emit(name, data)` 不是同一层 API。
- Warning。订阅侧也只知道 jrpc2。`subscribeCoreEventPushes` 收到 bus 事件后统一走 `server.NotifyAll(context.Background(), bridge, method, ev)`，见 `internal/platform/rpc/push.go:75-92`；而 `NotifyAll` 只遍历 `active map[*jrpc2.Server]struct{}` 并逐个调用 `bridge.NotifyClient(...)`，见 `internal/platform/rpc/server.go:21-23`、`internal/platform/rpc/server.go:42-50`、`internal/platform/rpc/server.go:122-130`。Wails app 不在这条活跃连接集合里。
- Warning。V2 的 Wails 事件语义也不同。后端不是直接把 RPC method 往前端发，而是先包装成 `bridge-event` / `agent-event` 频道，再由 `wailsApp.Event.Emit(...)` 发出，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:13-29`、`go-agent-v2/cmd/agent-terminal/app_bridge.go:94-115`、`go-agent-v2/cmd/agent-terminal/app_bridge.go:150-160`。前端订阅的也是这些频道名，而不是 jrpc2 method 名，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:362-440`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:475-506`。
- Warning。当前 jrpc2 push method 只有 `ui/state/changed`、`turn/started`、`turn/completed`，见 `internal/platform/rpc/push.go:16-20`；V2 Wails 侧则显式维护 `bridge-event` / `agent-event` / `files-dropped` / `app-will-quit` 等频道，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:362-506`、`go-agent-v2/cmd/agent-terminal/main_setup.go:450-477`。因此“复用现有 push bridge 模式把 bus event 推到 Wails events”可以共享订阅点，但不能按现状直接复用 API。更稳妥的是独立的 Wails bridge，或抽象一个多 sink 事件层。

## 4. V2 方法覆盖

- Warning。计划只列 `CallAPI/GetBuildInfo/GetGroup`，覆盖面偏窄。V2 `App` 在 `app.go` 还公开了 `LaunchAgent`、`LaunchBatch`、`SubmitInput`、`SubmitWithFiles`、`SendCommand`、`StopAgent`、`ListAgents`、`GetLSPDiagnostics`、`GetLSPStatus`、`SaveClipboardImage`、`OpenNewWindow`，见 `go-agent-v2/cmd/agent-terminal/app.go:78-300`。
- `LaunchAgent`/`LaunchBatch` 可以退化为 `CallAPI`。`LaunchAgent` 本质是 `thread/start`，有 prompt 时再补一发 `turn/start`，见 `go-agent-v2/cmd/agent-terminal/app.go:78-113`；`LaunchBatch` 只是循环调用它，见 `go-agent-v2/cmd/agent-terminal/app.go:133-146`。V2 当前前端也已经直接走 `callAPI('thread/start', ...)`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:161-179`。
- `SubmitInput`/`SubmitWithFiles` 可以退化为 `CallAPI`。V2 这两个方法直接调 `mgr.Submit(...)`，见 `go-agent-v2/cmd/agent-terminal/app.go:149-162`；但 V3 的 `turn/start` 已经原生接收 `Prompt`、`Images`、`Files`，见 `internal/module/turn/rpc_types.go:5-12`、`internal/module/turn/rpc_helpers.go:5-14`、`internal/module/turn/rpc.go:33-47`。V2 当前前端发送消息也已经直接调 `callAPI('turn/start', requestPayload)`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:242-295`。
- `StopAgent`/`ListAgents` 可以退化为 `CallAPI`，但语义应转成 thread/turn 模型。V2 方法本身直接调 `mgr.Stop` / `mgr.List`，见 `go-agent-v2/cmd/agent-terminal/app.go:173-190`；V2 当前前端已经改成 `callAPI('turn/interrupt', ...)` 和 `callAPI('thread/list', {})`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/thread-actions-helpers.js:182-221`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/thread-sync-helpers.js:219-220`。V3 也已有对应 handler，见 `internal/module/turn/rpc.go:60-65`、`internal/module/thread/rpc.go:41-43`。
- `SendCommand` 不必保留为独立绑定，但不能等价替换成一个“通用 sendCommand RPC”。V2 方法直接调 `mgr.SendCommand(...)`，见 `go-agent-v2/cmd/agent-terminal/app.go:165-170`；V3 暴露的是一组方法化的 thread command RPC，例如 `thread/config/get`、`thread/model/set`、`thread/compact/start`、`thread/realtime/*`，见 `internal/module/thread/rpc.go:58-82`、`internal/module/thread/rpc.go:101-115`。如果前端仍要保留“任意斜杠命令字符串”入口，需要额外 RPC 或前端映射层。
- `GetLSPDiagnostics`/`GetLSPStatus` 不需要保留为独立绑定。V2 这两个方法只是薄封装旧 RPC 名称 `lsp_diagnostics_query` / `mcpServerStatus/list`，见 `go-agent-v2/cmd/agent-terminal/app.go:211-234`；而当前前端 IDE 已经直接走 `callAPI('lsp/gui_*', ...)`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/lsp-api.js:18-170`。V3 `internal/` 下也没有这两个旧方法名的实现。
- `OpenNewWindow` 不一定要保留为独立绑定，但当前计划必须补一条 UI RPC。V2 前端已经通过 `callAPI('ui/openNewWindow', { cwd })` 触发开窗，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/composables/useThreadActions.js:303-312`；服务端 route 再委托给 `OpenNewWindow(...)`，见 `go-agent-v2/cmd/agent-terminal/app_handlers.go:332-352`、`go-agent-v2/cmd/agent-terminal/app.go:272-300`。当前 V3 `internal/` 下没有 `ui/openNewWindow` 对应实现。
- `SaveClipboardImage` 需要保留为独立原生绑定，除非前端 API 契约一起改。当前前端通过 Wails method ID 直接调用，而不是 `CallAPI`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:289-297`、`go-agent-v2/cmd/agent-terminal/shim/wails-runtime.js:17-33`。当前 V3 `internal/` 下也没有 `ui/saveClipboardImage` RPC。
- 额外漏项：真实的 V2 原生绑定面还包括 `SelectProjectDir` / `SelectProjectDirs` / `SelectFiles`。这些方法虽然不在 `app.go`，但当前前端依赖它们做原生文件/目录选择，见 `go-agent-v2/cmd/agent-terminal/app_dialogs.go:59-149`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:232-297`、`go-agent-v2/cmd/agent-terminal/shim/wails-runtime.js:17-33`。P6 的 `binding.go` 只列 `CallAPI/GetBuildInfo/GetGroup`，对现有前端资产来说不够。

## 5. 前端资产

- Warning。V2 明确使用 `//go:embed all:frontend` 嵌入前端资产，见 `go-agent-v2/cmd/agent-terminal/main.go:32-33`，并在桌面启动时把 assets 接到 `application.New(...)`，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:416-424`。
- Warning。V3 当前入口仍是 headless 模式：`cmd/agent-terminal/main.go` 只调用 `app.Run()`，见 `cmd/agent-terminal/main.go:10-14`；`internal/app/app.go` 也只有 `Run()` 启停 `fx.App`，见 `internal/app/app.go:17-31`。当前仓库里没有对应 V3 `//go:embed all:frontend` 接入点。
- 基于 LSP `text_search("frontend/")` 的结果，`cmd/` 和 `internal/` 范围没有 V3 侧 `frontend/` 引用；命中只来自 V2 和计划文档。因此 P6 的批次 C 不是“接已有目录”，而是前端目录、嵌入和窗口资产链路的净新增工作。

## 结论（Blocker / Warning / OK）

- Blocker：V3 根 `go.mod` 缺少 `github.com/wailsapp/wails/v3`，见 `go.mod:5-27`。P6 在补依赖前不可落地。
- Warning：`rpc.Server` 目前没有公开本地 dispatch/invoke 能力，且现有 handler 链依赖 `*jrpc2.Request` 中间件；P6 需要补正式调用适配层，不能把这一步当成“薄装配”，见 `internal/platform/rpc/server.go:16-66`、`internal/platform/rpc/strict.go:10-17`、`internal/platform/rpc/handler.go:43-96`。
- Warning：现有 push bridge 是 jrpc2 push，不是 Wails event bridge；建议独立 Wails bridge 或抽象多 sink 事件层，而不是把 `PushBridge` 直接扩成 `Event.Emit` 适配器，见 `internal/platform/rpc/push.go:22-92`、`internal/platform/rpc/server.go:42-50`。
- Warning：计划低估了绑定面。除 `CallAPI/GetBuildInfo/GetGroup` 外，至少还要覆盖 `SaveClipboardImage`、`SelectProjectDir` / `SelectFiles` 等原生能力；`OpenNewWindow` 也需要新的 UI RPC 或独立绑定，见 `go-agent-v2/cmd/agent-terminal/app_dialogs.go:59-149`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:232-360`、`go-agent-v2/cmd/agent-terminal/shim/wails-runtime.js:17-33`。
- Warning：V3 当前没有已接好的 frontend 资产链路；批次 C 是净新增，不是简单迁移，见 `go-agent-v2/cmd/agent-terminal/main.go:32-33`、`cmd/agent-terminal/main.go:10-14`、`internal/app/app.go:17-31`。

## 互辩

### 对 audit-p6-plan-B 的批判

1. `audit-p6-plan-B.md:35` 把“最大问题”定成 shutdown authority，不够挑剔，遗漏了两个更先决的 blocker：根模块还没有 `github.com/wailsapp/wails/v3`，见 `go.mod:5-14`；同时 P6 设想的 `dispatch` 由 fx 注入、直接分发 `rpc.Server` handler.Map，但当前 `rpc.Server` 只公开 `Register/NotifyAll/Run`，并没有本地 `dispatch/invoke`，见 `docs/plans/迁移/p6-execution-plan.md:68-80`、`internal/platform/rpc/server.go:16-40`、`internal/platform/rpc/module.go:14-24`。B 把生命周期问题排在这些编译/装配 blocker 前面，排序失真。
2. `audit-p6-plan-B.md:16` 说 desktop 模式会形成“至少三方竞争退出：`fx.Done`、`run.Group`、Wails lifecycle”，代码证据不够严。当前 `Run()` 只是 `app.Start()` 之后阻塞等待 `<-app.Done()` 再调用 `app.Stop()`，并没有注册 signal 或主动驱动 shutdown，见 `internal/app/app.go:24-30`；真正注册 `SIGINT/SIGTERM` 的是 `RunGroup` 自己的 signal actor，见 `internal/platform/runner/group.go:34-47`。因此现有代码能证明的是 `run.Group` 信号面与未来 Wails lifecycle 的冲突风险，而不是已经存在第三个“`fx.Done` authority”。
3. `audit-p6-plan-B.md:10-11` 对 “Wails runner 若返回 `nil` / `context.Canceled` / error 会怎样” 的分析是合理推演，但不是现成代码证据。当前 runner 契约只有 `Run(ctx) error`，见 `internal/platform/runner/group.go:14-18`；仓库里还没有 `RunDesktop()` 或任何 Wails runner 实现，见 `internal/app/app.go:17-30`；计划文本也只说 “`runner.go` 不变（Wails runner 通过 `group:"runners"` 自动加入 run.Group）”，见 `docs/plans/迁移/p6-execution-plan.md:104-106`。更精确的批判应当是“计划没有定义 Wails runner 的返回语义，因此无法验证它和 `BindRuntime` 的兼容性”，而不是把某种返回值编码当成既成事实。
4. B 报告通篇聚焦 lifecycle / signal，但遗漏了现有前端对直接 Wails 绑定面的硬依赖。当前前端不仅走 `CallAPI`，还通过 runtime method ID 直接调用 `SelectProjectDir`、`SelectFiles`、`SaveClipboardImage`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:292-357`、`go-agent-v2/cmd/agent-terminal/shim/wails-runtime.js:21-32`。而 P6 绑定表只列 `CallAPI / GetBuildInfo / GetGroup`，见 `docs/plans/迁移/p6-execution-plan.md:58-60`。这比 B 花很多篇幅展开的 `Headless vs Desktop` 讨论更接近现成的产品级缺口。

### 对 audit-p6-plan-C 的批判

1. `audit-p6-plan-C.md:36` 用“Go 侧没有直接引用”来弱化 `SaveClipboardImage` 的重要性，这个判定方法不成立。真实依赖在前端 runtime：`saveClipboardImage()` 直接调用 `callByID(METHOD_IDS.SAVE_CLIPBOARD_IMAGE, ...)`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:349-357`；debug shim 再把该 method ID 直连到 `app.SaveClipboardImage(...)`，见 `go-agent-v2/cmd/agent-terminal/shim/wails-runtime.js:27-28`；而 composer 在图片拖拽和粘贴两条链路都会调用它，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/composer.js:90-97`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/stores/composer.js:162-170`。因此这里不是“次级缺口”，而是现有前端合同的直接断点。
2. C 报告把主要 Blocker 都放在体量、守卫预算和并行切片上，见 `audit-p6-plan-C.md:78-88`，但遗漏了两个更早发生的 feasibility blocker：根 `go.mod` 还没有 `github.com/wailsapp/wails/v3`，见 `go.mod:5-14`；同时计划假设的 fx 注入 `dispatch` 当前不存在，`rpc.Server` 没有公开本地 `invoke/dispatch`，见 `docs/plans/迁移/p6-execution-plan.md:68-80`、`internal/platform/rpc/server.go:16-40`、`internal/platform/rpc/module.go:14-24`。如果这两件事不先解决，讨论 `3421` 行还是 `280` 行还没到执行门槛。
3. `audit-p6-plan-C.md:20-24`、`audit-p6-plan-C.md:80-84` 用整个 V2 入口包 `3421` 行去推导 P6 Blocker，口径偏粗。至少其中 `debug_server.go` 是显式 feature-flag 路径，`startMainDebugServer()` 在 `enabled=false` 时立即返回，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:362-366`；而 V3 已经把大量 backend 组装吸收到 `internal/app.Module`，包括 `config/db/bus/rpc/skill/thread/turn/orchestration/workspace/provider`，见 `internal/app/modules.go:23-44`。因此真正的 blocker 不是“包总行数过大”，而是计划没有明确哪些合同保留、哪些降级；把总行数本身上升为首要 blocker，会掩盖范围定义问题。
4. `audit-p6-plan-C.md:38` 把 `app_dialogs.go` 和 `build_info.go` 写成“支持文件”，措辞偏轻。当前前端对 `GET_BUILD_INFO`、`SELECT_PROJECT_DIR`、`SELECT_FILES` 也是直接 method-ID 级依赖：`getBuildInfo()` 走 `METHOD_IDS.GET_BUILD_INFO`，`selectProjectDir()` 走 `METHOD_IDS.SELECT_PROJECT_DIR`，`selectFiles()` 优先走 `METHOD_IDS.SELECT_FILES`，见 `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:292-346`、`go-agent-v2/cmd/agent-terminal/shim/wails-runtime.js:21-32`。这些不是简单的“依赖源文件”，而是必须被新 binding 明确保留或显式 de-scope 的公开 runtime surface。
