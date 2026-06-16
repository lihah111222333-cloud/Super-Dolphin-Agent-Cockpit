# P6 Wails Desktop 集成层能力+容错审查

## 审查方法

- 只使用 LSP：`text_search`、`workspace_symbol`、`references(compact)`、`call_hierarchy`、`read_file`
- 审查范围：
  - V3：`internal/ui/wails/`、`internal/app/`、`internal/platform/rpc/`、`internal/module/*/rpc.go`、`cmd/*`
  - V2 对照：`go-agent-v2/cmd/agent-terminal/` 及其前端 service/runtime 订阅点

## 总结

当前 P6 Wails desktop 集成层的 Go 侧骨架已经基本闭环，PB1/PB2 相关的 3 个关键点已经能在源码层验证为已修：

- `RunDesktop()` 直接消费 FX 产出的单一 `*application.App`，不再手工再造第二个 Wails app。
- `WailsLifecycle` 已由 `uiwails.Module` 提供，并通过 `bindWailsLifecycle()` 接到 `fx.Shutdowner`。
- 反向 shutdown 已闭环：`runner error -> NotifyBackendFailed() -> Wails.Quit()`。

但这还不等于“桌面能力达到 V2 等价”。当前最大缺口不在 backend wiring，而在桌面产品面：

- shipped frontend 只有占位页，未实际调用 `CallAPI`，也未订阅 runtime event。
- 若按 V2 能力面对照，仍缺 `OpenNewWindow`、`agent-event`、`files-dropped`、`SelectProjectDirs`、真实 `group` 语义。
- `SaveClipboardImage` 在 V3 已真实实现，但 contract 已漂移，不再兼容 V2 前端调用方式。

## 12 项结论总表

| # | 维度 | 结论 |
| --- | --- | --- |
| 1 | CallAPI 完整链路 | 部分通过 |
| 2 | Dispatch 中间件复用 | 通过 |
| 3 | 原生绑定 | 部分通过 |
| 4 | event bridge 闭环 | 部分通过 |
| 5 | ShouldQuit | 通过 |
| 6 | 双向 shutdown | 通过 |
| 7 | 单一 Wails app | 通过 |
| 8 | WailsLifecycle fx.Provide | 通过 |
| 9 | 前端资产 | 部分通过 |
| 10 | 窗口配置 | 通过 |
| 11 | MCP server 不受影响 | 通过 |
| 12 | V2 桌面能力等价性 | 不通过 |

## 逐项审查

### 1. CallAPI 完整链路

结论：**部分通过**。

已验证的 Go 侧链路：

- `application.NewService(app)` 已把 `App` 暴露为 Wails service，见 `internal/ui/wails/module.go:37-39`。
- `NewApp()` 把 `server.Dispatch` 注入到 binding，见 `internal/ui/wails/module.go:30-35`。
- `CallAPI()` 做参数校验后直接调用 `a.dispatch(...)`，并把 `json.RawMessage` 解回 `any`，见 `internal/ui/wails/binding.go:22-39`。
- `Dispatch()` 通过 `jrpc2/server.NewLocal(s.methods, ...)` 走同一份注册后的 `handler.Map`，见 `internal/platform/rpc/server.go:44-64`。
- `registerAllHandlers()` 把 `group:"rpc_handlers"` 全量注册进 `Server`，见 `internal/platform/rpc/module.go:35-51`。
- handler 包装层仍在：`StrictHandler()` 做 strict object-only decode；`ThreadHandler()`/`CapabilityThreadHandler()` 组合了 `ThreadScope()` 和 `CapabilityGate()`，见 `internal/platform/rpc/strict.go:10-17`、`internal/platform/rpc/handler.go:44-96`。
- 具体路由已实际消费这些包装：例如 `thread/messages`、`thread/model/set`、`turn/start`，见 `internal/module/thread/rpc.go:51-83`、`internal/module/turn/rpc.go:32-59`。

未闭环的部分：

- 当前 embed frontend 只是静态占位页，没有任何 `CallAPI` 或 runtime JS 代码，见 `internal/ui/wails/frontend/index.html:1-53`。
- 因此“前端 -> Wails binding”这一跳在当前仓内 shipped frontend 上**没有实际消费证据**；现状更准确地说是“binding 以下闭环，binding 以上缺 UI”。

容错观察：

- `CallAPI()` 对空 method、非法 JSON 直接 fail-fast；这部分防御是正确的，见 `internal/ui/wails/binding.go:23-33`。

### 2. Dispatch 中间件复用

结论：**通过**。

- `Dispatch()` 没有绕过 middleware，也没有走单独的“轻量路径”；它把 `s.methods` 原样交给 `jrpc2` local server，见 `internal/platform/rpc/server.go:49-60`。
- `s.methods` 来自 `server.Register(p.Handlers...)`，见 `internal/platform/rpc/module.go:49-50`。
- `ThreadScope()`、`CapabilityGate()`、`StrictHandler()` 的组合仍在 handler 构造层，见 `internal/platform/rpc/handler.go:44-96`、`internal/platform/rpc/strict.go:10-17`。
- 因此 Wails binding 走 `Dispatch()` 时，和 TCP RPC server 走的是**同一份 handler map、同一层 strict/thread/capability 包装**。

### 3. 原生绑定

结论：**部分通过**。

已实现部分：

- `SaveClipboardImage()`、`SelectProjectDir()`、`SelectFiles()` 都是真实现，不是 stub，见 `internal/ui/wails/binding_native.go:15-178`。
- `SaveClipboardImage()` 按 `runtime.GOOS` 分发：
  - macOS：`pngpaste`
  - Linux：`wl-paste` 或 `xclip`
  - Windows：`powershell Get-Clipboard -Format Image`
  见 `internal/ui/wails/binding_native.go:124-178`。
- `SelectProjectDir()` / `SelectFiles()` 走 Wails `OpenFile` dialog，目录/多文件选择都是真调用，见 `internal/ui/wails/binding_native.go:26-65`。

兼容/容错问题：

- `SaveClipboardImage` 的 **contract 已漂移**。
  - V2：`SaveClipboardImage(base64Data string)`，前端直接传 base64，见 `go-agent-v2/cmd/agent-terminal/app.go:241-264`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:349-357`。
  - V3：`SaveClipboardImage(filename string)`，含义变成“把当前系统剪贴板图片存到路径”，见 `internal/ui/wails/binding_native.go:15-24`。
- 这意味着如果沿用 V2 前端，`SaveClipboardImage` 不兼容。
- Linux/macOS 方案依赖外部命令；缺命令时直接报错，没有浏览器侧 fallback，见 `internal/ui/wails/binding_native.go:137-167`。

### 4. event bridge 闭环

结论：**部分通过**。

后端侧闭环已成立：

- `EventBridge.Start()` 订阅内部 bus 上的 `agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted`，见 `internal/ui/wails/bridge.go:42-64`。
- `publish()` 把这些 typed event 统一封装成 `bridge-event` 信道 payload，再交给 `WailsLifecycle.EmitEvent()`，见 `internal/ui/wails/bridge.go:81-89`。
- `NewWailsApplication()` 通过 `SetEventEmitter()` 把 `lifecycle.EmitEvent()` 接到 `wailsApp.Event.Emit()`，见 `internal/ui/wails/module.go:88-97`。
- `uiwails.Module` 已 `fx.Invoke(bindEventBridge)`，bridge 会在 FX lifecycle 的 `OnStart` 启动，见 `internal/ui/wails/module.go:17-28`、`internal/ui/wails/module.go:120-134`。

V2 兼容性：

- V2 bridge 主通道也是 `bridge-event`，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:94-115`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:440-456`。
- quit overlay 通道也保持为 `app-will-quit`，见 `internal/ui/wails/lifecycle.go:11-15`；V2 同名，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:388-390`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:467-474`。
- envelope 内的 `type` 仍是 `ui/state/changed`、`turn/started`、`turn/completed`，与 V2 事件 method 保持一致，见 `internal/ui/wails/bridge.go:16-20`、`go-agent-v2/internal/uistate/event_normalizer.go:43-81`。

未等价部分：

- 当前 V3 没有 `agent-event` 兼容信道；V2 有，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:114-115`、`go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js:423-438`。
- 当前 shipped frontend 也没有任何 runtime event 订阅代码，`bridge-event` 在占位页中无消费方，见 `internal/ui/wails/frontend/index.html:1-53`。

### 5. ShouldQuit

结论：**通过**。

- `ShouldQuit()` 会先看 `quitAllowed`；首次拦截后读取活跃 agent 数量，见 `internal/ui/wails/lifecycle.go:82-99`。
- 若有活跃 agent：
  - 发送 `app-will-quit`
  - 延迟 `320ms`
  - 再请求 backend shutdown
  见 `internal/ui/wails/lifecycle.go:90-94`、`internal/ui/wails/lifecycle.go:137-143`。
- 若无活跃 agent，则直接进入 backend shutdown，见 `internal/ui/wails/lifecycle.go:97-98`。

容错观察：

- `activeAgentCount()` 失败时会记 warning 并返回 `0`，即退出策略是 **fail-open**，见 `internal/ui/wails/lifecycle.go:124-135`。
- 这意味着“无法统计活跃 agent”会退化成“允许无 overlay 直接关”；实现上可用，但不是最保守的容错策略。

### 6. 双向 shutdown

结论：**通过**。

`Wails quit -> fx.Shutdowner` 路径：

- `ShouldQuit()` / `OnShutdown()` 最终都走 `requestBackendShutdown()`，见 `internal/ui/wails/lifecycle.go:82-103`。
- `bindWailsLifecycle()` 已把 `shutdowner.Shutdown()` 注入到 `WailsLifecycle`，见 `internal/ui/wails/module.go:111-118`。

`runner error -> Wails.Quit` 路径：

- `BindRuntime()` 中 `RunGroup()` 出错时会 `NotifyBackendFailed()`，随后无论成功失败都会 `p.Shutdowner.Shutdown()`，见 `internal/app/runner.go:37-53`。
- `watchFXShutdown()` 监听 `app.Done()`，FX 停止后也会再调用一次 `NotifyBackendFailed()`，见 `internal/app/app.go:78-88`。
- `NotifyBackendFailed()` 会设置 `quitAllowed=true`，前端 ready 时直接 `invokeQuit()`，未 ready 则 `pendingQuit`，见 `internal/ui/wails/lifecycle.go:105-112`、`internal/ui/wails/lifecycle.go:157-173`。
- `NewWailsApplication()` 已通过 `SetQuitFunc(wailsApp.Quit)` 把最终 quit 函数接上，见 `internal/ui/wails/module.go:88-90`。

结论：

- 双向 shutdown 在装配层已经闭环，PB1/PB2 相关 shutdown wiring 可视为**已修**。

### 7. 单一 Wails app

结论：**通过**。

- 当前 source 中唯一的 `application.New(...)` 位于 `internal/ui/wails/module.go:72-98`。
- `RunDesktop()` 通过 `fx.Populate(&wailsApp, &lifecycle)` 取出 FX 容器中的那一个 `*application.App`，随后直接 `wailsApp.Run()`，见 `internal/app/app.go:29-51`。
- 全仓未发现第二个 desktop 路径上的 Wails app 构造点。
- `internal/ui/wails/runner.go` 虽存在 `NewRunner(app *application.App)`，但没有 provider/reference，属于未接线死代码，不会再造第二个实例。

结论：

- “FX 产出的 `*application.App` 是否唯一实例”这一点已可判定为 **是**。PB1 可视为已修。

### 8. WailsLifecycle fx.Provide

结论：**通过**。

- `uiwails.Module` 已 `fx.Provide(NewWailsLifecycle, ...)`，见 `internal/ui/wails/module.go:17-28`。
- 同一个 module 内还 `fx.Invoke(bindWailsLifecycle)` / `fx.Invoke(bindEventBridge)`，说明 `WailsLifecycle` 不是孤立对象，而是装配链一部分，见 `internal/ui/wails/module.go:17-28`。
- `RunDesktop()` 通过 `fx.Populate(&lifecycle)` 显式消费它，见 `internal/app/app.go:29-45`。
- `BindRuntime()` 也以 optional 方式消费 `Lifecycle`，用于 desktop/headless 分岔，见 `internal/app/runner.go:19-26`。

结论：

- “`WailsLifecycle` 是否被 module 提供并被 `RunDesktop` 消费”这一点可以确认 **是**。PB2 可视为已修。

### 9. 前端资产

结论：**部分通过**。

- embed 前端存在：`//go:embed frontend`，见 `internal/ui/wails/assets.go:11-24`。
- Wails asset handler 已接上：`AssetHandler()` 被放进 `application.Options.Assets.Handler`，见 `internal/ui/wails/assets.go:14-16`、`internal/ui/wails/module.go:79-81`。
- dev server 支持存在：`FRONTEND_DEVSERVER_URL` 非空时窗口直接指向该 URL，否则回落 `/`，见 `internal/ui/wails/window.go:27-31`。

但当前 shipped asset 只是占位页：

- `internal/ui/wails/frontend/index.html` 只有静态文案和样式，没有 runtime JS、没有 binding 调用、没有 event 订阅，见 `internal/ui/wails/frontend/index.html:1-53`。

结论：

- “有 embed / 有 dev server” 是真的。
- “当前桌面 UI 已可工作” 则**不是**。

### 10. 窗口配置

结论：**通过**。

- 主窗口配置已满足：
  - `1440x900`
  - `EnableFileDrop: true`
  - 深色背景 `application.NewRGB(15, 23, 42)`
  见 `internal/ui/wails/window.go:15-32`。

与 V2 的差距：

- V2 在 `WindowFilesDropped` 上还有显式 `app.Event.Emit("files-dropped", payload)`，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:450-477`。
- 当前 V3 只有 `EnableFileDrop: true`，没有 `OnWindowEvent(...)` 或 `files-dropped` 事件外发。

结论：

- 配置项已到位。
- 如果按“drop 事件也要可用”的 V2 行为看，则还没完全等价。

### 11. MCP server 不受影响

结论：**通过**。

- `cmd/mcp-lsp`、`cmd/mcp-ida`、`cmd/mcp-orch` 的 `main.go` / `fx.go` 都没有 `wails` import，见：
  - `cmd/mcp-lsp/main.go:1-13`
  - `cmd/mcp-lsp/fx.go:1-13`
  - `cmd/mcp-ida/main.go:1-13`
  - `cmd/mcp-ida/fx.go:1-13`
  - `cmd/mcp-orch/main.go:1-13`
  - `cmd/mcp-orch/fx.go:1-13`
- 核心 app 也仍保持 desktop/headless 分离：
  - `newFXApp()` 只装 `Module`
  - `newDesktopFXApp()` 才额外加 `uiwails.Module`
  见 `internal/app/app.go:53-76`。

补充判断：

- 这些 headless binary 当前本身仍很薄，几乎是 stub；但就“是否依赖 Wails”而言，答案明确是 **不依赖**。

### 12. V2 桌面能力等价性

结论：**不通过**。

已经对齐/部分对齐的能力：

- `CallAPI` 的 Go 侧桥接骨架已建立，见 `internal/ui/wails/binding.go:22-39`、`internal/platform/rpc/server.go:44-64`。
- `bridge-event` / `app-will-quit` 信道名延续 V2，见 `internal/ui/wails/bridge.go:81-89`、`internal/ui/wails/lifecycle.go:11-15`。
- `SelectProjectDir` / `SelectFiles` 有真实原生实现，见 `internal/ui/wails/binding_native.go:26-65`。
- Quit overlay / lifecycle wiring 已回到接近 V2 的控制流，见 `internal/ui/wails/lifecycle.go:82-155`。

仍明显不等价的能力：

- 当前 embed frontend 不是 V2 那个桌面 app，只是占位页，见 `internal/ui/wails/frontend/index.html:1-53`。
- `GetGroup()` 在 V3 固定返回空串；V2 返回真实 `a.group`，见 `internal/ui/wails/binding.go:14-47`、`go-agent-v2/cmd/agent-terminal/app.go:192-199`。
- `SaveClipboardImage` contract 不兼容 V2，见本报告第 3 条。
- V2 的 `OpenNewWindow(...)` 尚未迁入，见 `go-agent-v2/cmd/agent-terminal/app.go:270-300`；V3 source 下无对应绑定。
- V2 有 `agent-event` 兼容通道，V3 没有，见 `go-agent-v2/cmd/agent-terminal/app_bridge.go:114-115`。
- V2 有 `files-dropped` 事件外发，V3 没有，见 `go-agent-v2/cmd/agent-terminal/main_setup.go:450-477`。
- V2 还有 `SelectProjectDirs()` 多目录选择，V3 未实现，见 `go-agent-v2/cmd/agent-terminal/app_dialogs.go:89-118`。

结论：

- 当前 V3 desktop integration 更准确的定位是：
  - **backend/Wails lifecycle 基础设施已成型**
  - **桌面产品能力尚未达到 V2 等价**

## 关键风险

1. **UI 断层风险**
   当前 shipped frontend 不调用 binding、不订阅事件。即使 Go 侧 wiring 完整，最终用户能力仍不可用。

2. **剪贴板图片 contract 漂移**
   旧前端传 base64，新实现读系统剪贴板并把参数当输出路径；若复用 V2 UI，会直接断。

3. **退出保护 fail-open**
   活跃 agent 计数失败会按 `0` 处理，可能在统计异常时绕过 overlay 直接关停。

4. **V2 兼容面仍缺多个桌面专属能力**
   主要是 `OpenNewWindow`、`agent-event`、`files-dropped`、`SelectProjectDirs`、真实 `group` 语义。

## 最终判断

如果问题是“PB1/PB2 修没修、desktop wiring 是否已经闭环”，答案是：**大体已修，源码层闭环成立**。

如果问题是“P6 Wails desktop 是否已经具备 V2 桌面 app 的能力等价性”，答案是：**没有**。当前状态更像“桌面宿主层可用、前端产品层未迁完”。  

## 互审

### 1. 对 `docs/plans/迁移/cap-fx-lifecycle.md` 的批判

1. `cap-fx-lifecycle.md:15-16,122-127` 把“`internal/app.Run()` 没有 caller”扩写成“V3 真实运行入口只有 desktop”，表述过满。`internal/app.Run()` 确实没有 live caller，但仓内仍有 3 个真实 headless binary 入口：`cmd/mcp-lsp/fx.go:5-13`、`cmd/mcp-ida/fx.go:5-13`、`cmd/mcp-orch/fx.go:5-13`。它们不走 `internal/app.Run()`，但不能据此把“headless 入口”整体归零。
2. `cap-fx-lifecycle.md:136,200-201` 把 `RunDesktop()` 的 post-start nil guard 当成显著 lifecycle 缺口，证据力度不足。当前 `newDesktopFXApp()` 固定包含 `uiwails.Module`，见 `internal/app/app.go:68-75`；而 `NewWailsLifecycle()` 与 `NewWailsApplication()` 都无条件返回非 nil，见 `internal/ui/wails/lifecycle.go:43-51`、`internal/ui/wails/module.go:72-98`。在现有 live graph 下，这更像防御式死分支，而不是已证实会发生的漏停路径。
3. `cap-fx-lifecycle.md:89-103,139,226-236` 对 signal 双入口的严重度没有按 live path 收缩。当前桌面真实入口是 `cmd/agent-terminal/main.go:10-14 -> app.RunDesktop()`；而 desktop 模式下 `BindRuntime` 会把 `EnableSignals` 置为 `false`，见 `internal/app/runner.go:37-40`。因此重复 signal 处理只存在于无人调用的 `internal/app.Run()` headless 路径，不应直接投射为当前 shipped desktop 主链的高风险问题。

### 2. 对 `docs/plans/迁移/cap-thread-lifecycle.md` 的批判

1. `cap-thread-lifecycle.md:54-72` 把 `thread/stop` route 缺失直接写成“公共能力缺失”，结论过满。`thread/stop` 的确不存在于 `thread` handler map，见 `internal/module/thread/rpc.go:18-84`；但公开 stop surface 并非 0，`agent.stop` 已经是 public RPC，见 `internal/sidecar/orch/orchestration/rpc.go:40-42`。更准确的批评应是“停止能力落在 agent 维度、与 V2 thread 语义不等价”，而不是“没有公开 stop 能力”。
2. `cap-thread-lifecycle.md:13-14,46,233-258` 把事件面写成“只做到部分内部发布，没有稳定地发布成对外可消费事件”，措辞太绝对。确实没有 thread-specific typed event，但 thread 相关生命周期变化会经过 orchestration 发布 `agentdto.StateChanged`，见 `internal/sidecar/orch/orchestration/service.go:281-288`、`internal/sidecar/orch/orchestration/events.go:13-23`；这些事件又已经桥到 RPC push 与 Wails `bridge-event`，见 `internal/platform/rpc/push.go:82-90`、`internal/ui/wails/bridge.go:53-62`。真实问题是“缺 thread 专属事件语义”，不是“没有对外事件面”。
3. `cap-thread-lifecycle.md:11-12,156-178` 把 `SessionManager` 的按 `agentID` 单槽替换写成普遍 thread 并发风险，也写宽了。正常 `thread/start` 在 `AgentID` 为空时会自动生成新 agent id，见 `internal/module/thread/lifecycle.go:184-186`；`Resume` 也要求显式 `AgentID`，见 `internal/module/thread/lifecycle.go:189-203`。真正共享 `AgentID` 的高风险路径主要是 `Fork`，它复用了旧 binding 的 `AgentID`，并且 `persistThreadState(..., false)` 不写新 binding，见 `internal/module/thread/lifecycle.go:122-132`、`internal/module/thread/lifecycle.go:240-270`。因此这更像 fork/shared-agent 特例被放大，而不是所有 thread 操作都会互踢。

### 3. 对 `docs/plans/迁移/cap-turn-execution.md` 的批判

1. `cap-turn-execution.md:14-15,102-118` 把“direct `turn/start` RPC 不会设置 orchestration `activeTurnID`”直接写成 `TurnCompleted` 闭环缺口，范围下得不够准。对 orchestration 状态机来说这句成立；但 direct RPC 仍会在 `StartTurn()` 中登记 tracker、绑定 handle，并通过 `watchTurn(handle, localID)` 在 `handle.Done()` 后完成本地收敛，见 `internal/module/turn/service.go:76-106`、`internal/module/turn/service.go:173-203`。更准确的结论应是“agent orchestration state 不闭环”，而不是“turn execution 本身不闭环”。
2. `cap-turn-execution.md:27,51,121-133,205-223` 过度把 public submit surface 归结为 `turn/start`。除了 `turn/start`，公开 RPC 还有 `agent.submit` / `agent.submitPrompt`，见 `internal/sidecar/orch/orchestration/rpc.go:20-38`；它们的参数面保留 `SelectedSkills`、`ManualSkillSelection`、`OutputSchema`，见 `internal/sidecar/orch/orchestration/rpc_types.go:70-116`，并在 `internal/module/turn/orchestration_starter.go:54-62` 喂进同一条 `PrepareTurn -> StartTurn` 执行链。所以“对外 RPC 面拿不到 skill/outputSchema”只对 `turn/*` 命名空间成立，不是整个公开 turn 提交面都成立。
3. `cap-turn-execution.md:13,86-100` 对 `forceComplete` 的终态描述写得过死。源码里 `ForceCompleteTurn()` 只发 `session.Interrupt(Source="force_complete")`，既不查 `ActiveByThread`，也不 `MarkInterruptRequested`，更不等待 settle，见 `internal/module/turn/service.go:143-159`；`watchTurn()` 也只有在 tracked handle 之后以 `context.Canceled` 结束时，才会把状态改成 `interrupted`，见 `internal/module/turn/service.go:194-201`。因此“最终 tracker 状态仍收敛到 interrupted”只对 `internal/module/turn/service_test.go:204-245` 覆盖的 tracked-handle 场景成立，不能上升为通用结论。
