# P6 修复审查 — Agent B

## 逐项验证+行号

### 1. App struct + CallAPI

- `internal/ui/wails/binding.go` 存在；`App` 仅持有 `dispatch/emitter/wailsApp` 三个字段，公开绑定方法是 `CallAPI/GetBuildInfo/GetGroup`，原生公开方法另在 `binding_native.go` 中实现为 `SaveClipboardImage/SelectProjectDir/SelectFiles`。`internal/ui/wails/binding.go:16-20`，`internal/ui/wails/binding.go:22-47`，`internal/ui/wails/binding_native.go:15-65`
- `CallAPI` 先把 `paramsJSON` 校验成 `json.RawMessage`，空串映射为 `{}`，再调用 `a.dispatch(a.callContext(), method, params)`，最后把 `json.RawMessage` 反序列化为 `any`；这与 `rpc.Server.Dispatch(ctx, method, params json.RawMessage) (json.RawMessage, error)` 的签名是对齐的。`internal/ui/wails/binding.go:22-39`，`internal/ui/wails/binding.go:75-96`，`internal/ui/wails/module.go:29-34`，`internal/platform/rpc/server.go:42-64`
- `GetBuildInfo` 存在并返回 `version/commit/runtime/buildTime/dirty` map；`GetGroup` 也存在，但当前只返回常量空串 `defaultGroup=""`，不是实际窗口分组。`internal/ui/wails/binding.go:14-15`，`internal/ui/wails/binding.go:41-47`，`internal/ui/wails/binding.go:98-140`

### 2. 原生绑定

- `SaveClipboardImage` 已实现，按 `runtime.GOOS` 分发：macOS 依赖 `pngpaste`，Linux 依赖 `wl-paste` 或 `xclip`，Windows 依赖 `powershell Get-Clipboard -Format Image`。`internal/ui/wails/binding_native.go:15-24`，`internal/ui/wails/binding_native.go:124-179`
- `SelectProjectDir` / `SelectFiles` 已实现，走 Wails `application.App.Dialog.OpenFile()`，分别调用 `PromptForSingleSelection()` / `PromptForMultipleSelection()`。`internal/ui/wails/binding_native.go:26-77`
- `GetBuildInfo` / `GetGroup` 均存在；其中 `GetGroup` 仍是 stub。`internal/ui/wails/binding.go:41-47`

### 3. 窗口创建

- `internal/ui/wails/window.go` 存在。`CreateMainWindow()` 中确实配置了 `1440x900`、最小 `800x600`、`EnableFileDrop=true`、深色背景，以及 `FRONTEND_DEVSERVER_URL` 开发服务器模式。`internal/ui/wails/window.go:11-33`
- 但 `cmd/agent-terminal` 实际入口没有复用这套窗口，而是 `RunDesktop()` 手工新建了第二个 `application.App` 和窗口：最小尺寸变成 `960x640`，没有 `EnableFileDrop`，也没有 dev-server URL 分支。`cmd/agent-terminal/main.go:10-12`，`internal/app/app.go:30-50`，`internal/app/app.go:93-124`

### 4. module.go 注册

- `App` 在 `uiwails.Module` 内确实被注册成 Wails service：`NewService(app)` 返回 `application.NewService(app)`，`NewWailsApplication()` 把它放进 `Services`。`internal/ui/wails/module.go:19-27`，`internal/ui/wails/module.go:36-38`，`internal/ui/wails/module.go:49-61`
- 但 `RunDesktop()` 实际运行的是 `createWailsApp()` 手工创建的 app，这个 app 没有 `Services` 字段，因此当前入口路径上的窗口没有把 `App` 绑定注册进去。`internal/app/app.go:30-50`，`internal/app/app.go:93-124`
- “event bridge 是否 fx.Invoke 启动”：否。`uiwails.Module` 只 `fx.Invoke(registerCoreEvents)`；`EventBridge`/`NewEventBridge`/`Start()` 在当前模块里没有 `fx.Provide` 或 `fx.Invoke` 接线。当前事件转发是 `registerCoreEvents()` 直接订阅 dispatcher 后 `app.emit(...)`。`internal/ui/wails/module.go:19-27`，`internal/ui/wails/module.go:76-109`，`internal/ui/wails/bridge.go:22-64`
- “fx 依赖链是否闭合”：桌面链路未闭合。`newDesktopFXApp()` 想 `fx.Populate(&lifecycle)`，`RunDesktop()` 还在入口处显式判空 `if lifecycle == nil { return error }`，但 `uiwails.Module` 并没有 provider 返回 `*WailsLifecycle`。同时 `BindRuntime` 只把 `Lifecycle` 标成 optional，archtest 也只验证 `app.Module`，没有验证 `app.Module + uiwails.Module` 的 desktop 图。`internal/app/app.go:30-34`，`internal/app/app.go:68-79`，`internal/app/runner.go:19-28`，`internal/ui/wails/module.go:19-27`，`internal/ui/wails/lifecycle.go:43-51`，`internal/archtest/fx_graph_test.go:11-13`

### 5. 前端资产

- 有 embed 资产，目录就是 `internal/ui/wails/frontend`：`//go:embed frontend` 后再 `fs.Sub(frontendAssets, "frontend")`。`internal/ui/wails/assets.go:11-24`
- 代码里有 dev server 支持，但只出现在 `CreateMainWindow()` 的 `FRONTEND_DEVSERVER_URL` 分支；实际 `RunDesktop()` 手工窗口没有这条逻辑，所以当前真实入口上的 dev-server 支持不成立。`internal/ui/wails/window.go:27-30`，`internal/app/app.go:93-124`
- 另有一个未接线的 fallback HTML `bootstrapWindowHTML()`，LSP diagnostics 也报它未使用。`internal/ui/wails/window.go:35-58`

### 6. MVP scope

- `OpenNewWindow` 没有被“明确 de-scope”。计划文档仍把 P6 定义为薄装配 desktop 集成，没有列出任何 multi-window 删除项；当前实现里也没有导出 `OpenNewWindow` 相关 binding，只剩单窗口 `CreateMainWindow()`。`docs/plans/迁移/p6-execution-plan.md:108-116`，`docs/plans/迁移/p6-execution-plan.md:154-160`，`internal/ui/wails/binding.go:22-47`，`internal/ui/wails/binding_native.go:15-65`，`internal/ui/wails/window.go:11-33`
- `debug_server` 没有被“明确 de-scope”。当前入口只 `app.RunDesktop()`，未接入任何 debug server 启动路径，但计划文件同样没有写删除说明。`cmd/agent-terminal/main.go:10-12`，`internal/app/app.go:30-50`，`docs/plans/迁移/p6-execution-plan.md:108-116`
- `coverage flush` 也没有被“明确 de-scope”。当前入口没有 flush 逻辑，计划文件也没有 scope 说明。`cmd/agent-terminal/main.go:10-12`，`internal/app/app.go:30-50`，`docs/plans/迁移/p6-execution-plan.md:108-116`

### 7. cmd/agent-terminal

- `main.go` 已切到 `RunDesktop()`，不再是 `Run()`。`cmd/agent-terminal/main.go:10-12`

### 8. 代码守卫

- 守卫阈值仍是每文件 `<=400` 有效行。`internal/archtest/guardlib.go:17-25`
- 这批新增的 Wails 文件都在 400 物理行以内：`assets.go` 24 行、`binding.go` 140 行、`binding_native.go` 179 行、`bridge.go` 109 行、`lifecycle.go` 191 行、`module.go` 109 行、`runner.go` 48 行、`window.go` 58 行。`internal/ui/wails/assets.go:1-24`，`internal/ui/wails/binding.go:1-140`，`internal/ui/wails/binding_native.go:1-179`，`internal/ui/wails/bridge.go:1-109`，`internal/ui/wails/lifecycle.go:1-191`，`internal/ui/wails/module.go:1-109`，`internal/ui/wails/runner.go:1-48`，`internal/ui/wails/window.go:1-58`
- 本地校验结果：`go build ./...` 通过，`go vet ./...` 通过，`go test ./internal/archtest/...` 通过；但 archtest 当前只证明现有守卫未报错，不证明 desktop FX 图正确。`internal/archtest/fx_graph_test.go:11-13`，`internal/archtest/code_size_guard_test.go:10-24`

## 结论（Blocker / Warning / OK）

### Blocker

1. `RunDesktop()` 没有真正复用 `uiwails.Module` 产出的 `application.App`，而是手工再造一个 Wails app；结果是 service 注册、window 配置、dev server 支持、file drop 配置被拆成两套实现，存在双 app / 双主循环风险。`internal/ui/wails/module.go:49-61`，`internal/ui/wails/runner.go:20-35`，`internal/app/app.go:30-50`，`internal/app/app.go:93-124`
2. desktop FX 依赖链未闭合：`RunDesktop()` 需要 `*WailsLifecycle`，但 `uiwails.Module` 没有 provider；`RunDesktop()` 自己也在入口显式把 `nil lifecycle` 当错误处理。`internal/app/app.go:30-34`，`internal/app/app.go:68-79`，`internal/ui/wails/module.go:19-27`，`internal/ui/wails/lifecycle.go:43-51`

### Warning

1. `EventBridge` 类型写了但没进 FX 图；当前是 `registerCoreEvents()` 直接发事件，不是文档里说的 “bridge.go via fx.Invoke” 路径。`internal/ui/wails/module.go:19-27`，`internal/ui/wails/module.go:76-109`，`internal/ui/wails/bridge.go:22-64`
2. `GetGroup()` 现在固定返回空串，若前端仍依赖窗口分组语义，这个实现不完整。`internal/ui/wails/binding.go:14-15`，`internal/ui/wails/binding.go:45-47`
3. `OpenNewWindow` / `debug_server` / `coverage flush` 都只是“当前没做”，不是“已明确 de-scope”；计划文档没有给 MVP 删除项清单。`docs/plans/迁移/p6-execution-plan.md:108-116`，`docs/plans/迁移/p6-execution-plan.md:154-160`
4. archtest 现在只校验 `app.Module`，没有覆盖 `app.Module + uiwails.Module` 的 desktop 组装，所以绿灯不足以证明桌面链路正确。`internal/archtest/fx_graph_test.go:11-13`

### OK

1. `binding.go` / `window.go` / `binding_native.go` / `assets.go` / `module.go` 都已落地，且 `CallAPI` 到 `rpc.Server.Dispatch` 的 JSON 参数/返回值桥接在 Go 侧是正确的。`internal/ui/wails/binding.go:16-39`，`internal/ui/wails/module.go:29-34`，`internal/platform/rpc/server.go:42-64`
2. `SaveClipboardImage`、`SelectProjectDir`、`SelectFiles`、`GetBuildInfo` 都已实现。`internal/ui/wails/binding_native.go:15-179`，`internal/ui/wails/binding.go:41-43`
3. `cmd/agent-terminal/main.go` 已切到 `RunDesktop()`，并且 `go build` / `go vet` / `archtest` 当前都通过。`cmd/agent-terminal/main.go:10-12`，`internal/archtest/code_size_guard_test.go:10-24`
