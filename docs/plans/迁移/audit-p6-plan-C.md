# P6 计划审查 — Agent C

## 1. V2 体量

- 统计口径：函数数使用 `document_symbol` 的 `Function/Method` 符号；行数以文件尾行号为准。`app_dialogs.go` 里的 3 个 function-valued var 不计入函数数，因为 `document_symbol` 将其识别为变量。证据见各文件符号跨度与尾行号。`go-agent-v2/cmd/agent-terminal/app_dialogs.go:10-57,59-149`

| 文件 | 行数 | `document_symbol` 函数/方法数 | 证据 |
| --- | ---: | ---: | --- |
| `main.go` | 327 | 16 | `go-agent-v2/cmd/agent-terminal/main.go:61-326,327` |
| `main_setup.go` | 491 | 13 | `go-agent-v2/cmd/agent-terminal/main_setup.go:45-490,491` |
| `app.go` | 301 | 15 | `go-agent-v2/cmd/agent-terminal/app.go:49-300,301` |
| `app_handlers.go` | 452 | 27 | `go-agent-v2/cmd/agent-terminal/app_handlers.go:15-451,452` |
| `app_helpers.go` | 526 | 39 | `go-agent-v2/cmd/agent-terminal/app_helpers.go:27-525,526` |
| `app_bridge.go` | 195 | 7 | `go-agent-v2/cmd/agent-terminal/app_bridge.go:15-194,195` |
| `app_dialogs.go` | 150 | 3 | `go-agent-v2/cmd/agent-terminal/app_dialogs.go:59-149,150` |
| `build_info.go` | 107 | 4 | `go-agent-v2/cmd/agent-terminal/build_info.go:24-106,107` |
| `debug_server.go` | 808 | 26 | `go-agent-v2/cmd/agent-terminal/debug_server.go:68-804,805-808` |
| `doc.go` | 4 | 0 | `go-agent-v2/cmd/agent-terminal/doc.go:1-4` |
| `main_skills.go` | 60 | 5 | `go-agent-v2/cmd/agent-terminal/main_skills.go:11-59,60` |
| **合计** | **3421** | **155** | 上表 |

- 计划把 V2 入口层写成 5 个文件、约 `~1500` 行，但仅计划点名的 5 个文件 `main.go/main_setup.go/app.go/app_handlers.go/app_helpers.go` 就已经是 `327 + 491 + 301 + 452 + 526 = 2097` 行，不是 `~1500` 行。`docs/plans/迁移/p6-execution-plan.md:18-26` `go-agent-v2/cmd/agent-terminal/main.go:327` `go-agent-v2/cmd/agent-terminal/main_setup.go:491` `go-agent-v2/cmd/agent-terminal/app.go:301` `go-agent-v2/cmd/agent-terminal/app_handlers.go:452` `go-agent-v2/cmd/agent-terminal/app_helpers.go:526`

- 计划问到“还有别的？”答案是有，而且不止一个：除 5 个主文件外，当前 V2 非测试入口层还包括 `app_bridge.go`、`app_dialogs.go`、`build_info.go`、`debug_server.go`、`main_skills.go`、`doc.go`。其中 `debug_server.go` 单文件就有 808 行，已经足以把 `~1500` 基线打穿。`docs/plans/迁移/p6-execution-plan.md:18-26` `go-agent-v2/cmd/agent-terminal/app_bridge.go:195` `go-agent-v2/cmd/agent-terminal/app_dialogs.go:150` `go-agent-v2/cmd/agent-terminal/build_info.go:107` `go-agent-v2/cmd/agent-terminal/debug_server.go:805-808` `go-agent-v2/cmd/agent-terminal/main_skills.go:60` `go-agent-v2/cmd/agent-terminal/doc.go:1-4`

## 2. 遗漏检查

- `debug_server.go` 被计划遗漏。V2 运行时显式暴露 `--debug` / `--debug-port` 标志，`main()` 在正常启动链里调用 `startMainDebugServer()`，再由它进入 `startDebugServer()`；计划文件的 V2 参考与批次拆分都没有覆盖这条链路。若 P6 不迁移，必须明写降级。`go-agent-v2/cmd/agent-terminal/main_setup.go:66-73` `go-agent-v2/cmd/agent-terminal/main.go:73-76` `go-agent-v2/cmd/agent-terminal/main_setup.go:362-370` `go-agent-v2/cmd/agent-terminal/debug_server.go:272-371` `docs/plans/迁移/p6-execution-plan.md:52-63` `docs/plans/迁移/p6-execution-plan.md:134-142`

- 计划的 V2 对照映射有一处硬错误：它把 `app_handlers.go` 映射到 `bridge.go`，但真实的事件转发函数 `handleBridgeNotification` 在 `app_bridge.go`，`app_handlers.go` 主要是 `CallAPI` 和 UI-only routes。当前映射会直接低估 bridge 迁移量。`docs/plans/迁移/p6-execution-plan.md:139-141` `go-agent-v2/cmd/agent-terminal/app_bridge.go:32-129` `go-agent-v2/cmd/agent-terminal/app_handlers.go:15-157`

- `app_edge_contract_test.go` 不应被视作“可自然丢失”的测试。该文件覆盖了 `CallAPI` 默认路由、参数 marshal 失败、`approval/respond` 归一化、bridge shutdown/no-emitter、CWD 归一化、timeout 与日志判定、nil-server safety 等边界；计划 Done 只保留 1 个 smoke，测试面明显缩水。至少要写明“迁移哪些契约，放弃哪些契约”。`go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:17-620` `go-agent-v2/cmd/agent-terminal/app_contract_test.go:87-269` `go-agent-v2/cmd/agent-terminal/app_open_window_test.go:135-462` `docs/plans/迁移/p6-execution-plan.md:116-116` `docs/plans/迁移/p6-execution-plan.md:154-160`

- 窗口管理不是“只有主窗口”。V2 暴露 `ui/openNewWindow`，`handleUIOpenNewWindow()` 会编码 snapshot/cwd，再调用 `OpenNewWindow()` 以 `--group --n --ui-bootstrap --cwd` 启动新进程；对应的稳定响应面和 snapshot 行为都有测试。计划只写了 `window.go` 的主窗口创建，没有给多窗口任何位置。`go-agent-v2/cmd/agent-terminal/app_handlers.go:148-153` `go-agent-v2/cmd/agent-terminal/app_handlers.go:332-352` `go-agent-v2/cmd/agent-terminal/app.go:270-300` `go-agent-v2/cmd/agent-terminal/app_open_window_test.go:135-187` `go-agent-v2/cmd/agent-terminal/response_surface_guard_test.go:46-85` `docs/plans/迁移/p6-execution-plan.md:61-63` `docs/plans/迁移/p6-execution-plan.md:108-116`

- `SaveClipboardImage` 在 V2 是公开 Wails 绑定，但计划没有给出迁移或删除说明。就 Go 侧 LSP 引用看，它没有被其他 Go 代码直接调用，因此它不是和 `OpenNewWindow` 同级的“必迁移 blocker”；但如果前端仍依赖剪贴板图片落盘，这里会直接缺口，计划必须显式 de-scope 或保留。`go-agent-v2/cmd/agent-terminal/app.go:236-264` `docs/plans/迁移/p6-execution-plan.md:56-63` `docs/plans/迁移/p6-execution-plan.md:108-116`

- 除上面 4 项外，计划还漏掉了 `app_dialogs.go` 与 `build_info.go` 这两个支持文件：前者承载原生目录/文件选择，后者承载 `GetBuildInfo()` 的真实实现。计划只在 `binding.go` 表中写了 `GetBuildInfo`，却没把依赖源文件计入。`go-agent-v2/cmd/agent-terminal/app_dialogs.go:12-149` `go-agent-v2/cmd/agent-terminal/build_info.go:17-106` `go-agent-v2/cmd/agent-terminal/app_contract_test.go:316-399` `docs/plans/迁移/p6-execution-plan.md:59-60`

## 3. 代码量预估

- 计划的“`ui/wails/` 总 ~280 行”只在显式缩 scope 时才成立；按当前文本，它宣称的是“薄装配层，不引入新的业务逻辑”，不是“删除 V2 UI 合同”。但现有 V2 的 Wails 层并不只是 `CallAPI`，还含 UI-only routes、dialogs、clipboard、multi-window、build info、service startup、bridge 过滤与 payload shape。`docs/plans/迁移/p6-execution-plan.md:10-12` `go-agent-v2/cmd/agent-terminal/app_handlers.go:128-157` `go-agent-v2/cmd/agent-terminal/app_handlers.go:173-209` `go-agent-v2/cmd/agent-terminal/app_handlers.go:326-379` `go-agent-v2/cmd/agent-terminal/app_dialogs.go:12-149` `go-agent-v2/cmd/agent-terminal/app.go:236-300` `go-agent-v2/cmd/agent-terminal/app_helpers.go:274-336`

- 已经被 V3 吸收的确有一批“后端骨架”工作：`internal/app.Module` 已经接好 `db.Module/bus.Module/rpc.Module/...`，`BindRuntime` 已经把 `group:"runners"` 接入运行时，`rpc.Module` 已经注册 handler map 并把 bus 事件桥到 jrpc2 push。DB、handler 注册、run-group 这部分不需要在 Wails 层重写。`internal/app/modules.go:23-44` `internal/app/app.go:17-31` `internal/app/runner.go:13-61` `internal/platform/rpc/module.go:14-24` `internal/platform/rpc/module.go:47-68` `internal/platform/rpc/push.go:75-91`

- 但“被骨架吸收”不等于“Wails 层只剩 280 行”。当前 `PushBridge` 只知道 `*jrpc2.Server` 的 `Notify/Callback`，计划里“把 push bridge 改为也推 Wails events”并不是无成本复用；如果真这样做，等于把 UI emitter 设计塞回 `internal/platform/rpc`。更合理的工作量仍应计入 `internal/ui/wails/bridge.go` 或等价适配器。`docs/plans/迁移/p6-execution-plan.md:44-45` `internal/platform/rpc/push.go:22-58` `internal/platform/rpc/push.go:75-91`

- 结论：对照当前代码，P6 如果要保留 V2 合同的主体，`280 行` 不现实；如果要做 MVP 薄桥，只保留 `CallAPI + basic events + single window`，则可以明显缩量，但计划必须先把 `debug_server/multi-window/dialogs/clipboard/contract tests` 明确列为降级项。`docs/plans/迁移/p6-execution-plan.md:56-63` `docs/plans/迁移/p6-execution-plan.md:108-116` `docs/plans/迁移/p6-execution-plan.md:154-160`

## 4. 守卫约束

- 计划写的守卫值 `每文件 ≤ 400 / 每函数 ≤ 80 / CC ≤ 10` 不是建议，而是仓库真实 guard 常量；因此体量预估必须按 guard 反推可拆分性，而不能按理想化“压缩”估。`docs/plans/迁移/p6-execution-plan.md:145-149` `internal/archtest/guardlib.go:17-25`

- `main_setup.go 490 -> lifecycle.go 80` 不可直接接受。V2 与 lifecycle 直接相关的代码至少分布在 4 段：信号与 shutdown reason 建模、Wails `ShouldQuit/OnShutdown` 挂接、quit overlay/延迟退出、OnShutdown deadline/coverage/pool close；这些逻辑分别落在 `main_setup.go:137-183`、`main_setup.go:388-430`、`main.go:79-123`、`main.go:134-219`。不写新的拆分方案，`80 行 lifecycle.go` 只是数字，没有落点。`docs/plans/迁移/p6-execution-plan.md:61-61` `docs/plans/迁移/p6-execution-plan.md:145-149` `go-agent-v2/cmd/agent-terminal/main_setup.go:137-183` `go-agent-v2/cmd/agent-terminal/main_setup.go:388-430` `go-agent-v2/cmd/agent-terminal/main.go:79-219`

- `app.go 301 -> binding.go 80` 同样缺少边界定义。若只保留 `CallAPI/GetBuildInfo/GetGroup`，80 行有机会成立；但当前 V2 binding 还承载 `ListAgents`、dialogs、clipboard、multi-window、auto-launch startup，以及一组通过 `CallAPI` 暴露的 UI-only routes。计划没有写这些合同删除，因此 `80 行` 不是可信预算。`docs/plans/迁移/p6-execution-plan.md:59-60` `go-agent-v2/cmd/agent-terminal/app.go:187-300` `go-agent-v2/cmd/agent-terminal/app_handlers.go:128-157` `go-agent-v2/cmd/agent-terminal/app_dialogs.go:59-149` `go-agent-v2/cmd/agent-terminal/app_helpers.go:274-287` `go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:495-520`

- `window.go ~40` 也偏乐观。仅 V2 的窗口选项与 file-drop handler 就落在 `main_setup.go:434-482`，再叠加 asset handler / embed 路径，通常需要独立 `window.go + assets.go` 或等价拆分，才能同时满足 `≤400` 与低复杂度。`docs/plans/迁移/p6-execution-plan.md:62-63` `go-agent-v2/cmd/agent-terminal/main_setup.go:434-482` `go-agent-v2/cmd/agent-terminal/main.go:32-33` `go-agent-v2/cmd/agent-terminal/main.go:221-231` `go-agent-v2/cmd/agent-terminal/main_setup.go:214-232`

## 5. 并行策略

- `A→B` 不是“完全串行”，只是“最终接线串行”。Batch B 的目标主要是 `RunDesktop()` 和 `internal/app` 组装；当前 `internal/app/app.go` 与 `internal/app/modules.go` 都是很薄的 assembly 文件，B 完全可以先把 desktop entry 和 option 结构准备好，最后再落 `wails.Module` import。真正硬依赖 A 的只有模块符号存在与最终编译。`docs/plans/迁移/p6-execution-plan.md:100-106` `internal/app/app.go:17-31` `internal/app/modules.go:23-48`

- 但 `C 可与 A 并行` 这一句按当前切片不成立。A 已经声明要新建 `bridge.go/lifecycle.go/window.go`，C 又声明要做 `embed 前端/窗口配置/Shutdown/Signal`；这在 V2 中分别对应 `main.go`、`main_setup.go`、`debug_server.go` 的同一批职责，不是独立写集。按现计划，A/C 会同时争抢 `window.go` 与 `lifecycle.go` 的边界。`docs/plans/迁移/p6-execution-plan.md:56-63` `docs/plans/迁移/p6-execution-plan.md:108-116` `docs/plans/迁移/p6-execution-plan.md:123-130` `go-agent-v2/cmd/agent-terminal/main.go:32-33` `go-agent-v2/cmd/agent-terminal/main.go:79-219` `go-agent-v2/cmd/agent-terminal/main_setup.go:214-232` `go-agent-v2/cmd/agent-terminal/main_setup.go:434-482`

- Shutdown 还直接依赖 emitter/binding 约定，不能把它当作与 A 完全无关的 sidecar。V2 的 quit overlay 是在 `runMainDesktopApplication()` 里通过 `appSvc.emitUIEvent("app-will-quit", payload)` 发出的，而 emitter 实现在 `App.emitUIEvent()`；这说明 shutdown 语义和 binding/bridge contract 是同一设计面。`go-agent-v2/cmd/agent-terminal/main_setup.go:388-399` `go-agent-v2/cmd/agent-terminal/app_bridge.go:146-161`

## 6. Done 标准

- “Desktop app 能完整启动”当前不可测，因为计划只写了 smoke `desktop boot`，没有说明 headless/CI 策略；而 V2 已经证明，Wails 相关逻辑完全可以通过 `application.App{}` stub、fake dialog hook、fake emitter 做单元契约测试，不依赖真实 GUI。P6 应把“真实 GUI 启动”降为手工 smoke，把 CI Done 改为可 stub 的 builder/bridge/lifecycle 测试。`docs/plans/迁移/p6-execution-plan.md:116-116` `docs/plans/迁移/p6-execution-plan.md:154-160` `go-agent-v2/cmd/agent-terminal/app_contract_test.go:316-399` `go-agent-v2/cmd/agent-terminal/app_contract_test.go:406-455`

- “`CallAPI("thread/start")` 返回正确响应”不需要 Wails test harness。V2 的 `CallAPI` 合同测试直接对 `App.CallAPI()` 注入 fake `invokeMethod`，同时覆盖 special-route bypass、default-route invoke、approval normalization、多窗口 snapshot 等行为；P6 应沿用这类 unit contract，而不是把它模糊成 GUI smoke。`docs/plans/迁移/p6-execution-plan.md:157-157` `go-agent-v2/cmd/agent-terminal/app_contract_test.go:87-269` `go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:17-43` `go-agent-v2/cmd/agent-terminal/app_open_window_test.go:135-187`

- “bus events 推送到 frontend”同样可以在 CI 无 GUI 验证。V2 已有 `handleBridgeNotification()` 的 payload shape、顺序、threadless/no-emitter、shutdown suppression、CWD filter 等契约测试，全部用 fake `emitEvent` 完成。P6 若新增 `internal/ui/wails/bridge.go`，应把 Done 写成“bridge unit tests 通过”，而不是“人工看前端是否收到事件”。`docs/plans/迁移/p6-execution-plan.md:158-158` `go-agent-v2/cmd/agent-terminal/app_contract_test.go:487-661` `go-agent-v2/cmd/agent-terminal/app_edge_contract_test.go:228-428`

- `go build + go vet + archtest 全通过` 还缺一条前置：当前 `go.mod` 没有 `github.com/wailsapp/wails/v3`，计划只把它写成“需要确认是否已在 go.mod”，但 Done 却默认它会自然成立。应把“依赖已引入且新 UI 包不打破 code-size guard”写成显式前置检查。`docs/plans/迁移/p6-execution-plan.md:160-167` `go.mod:5-27` `internal/archtest/code_size_guard_test.go:10-24`

## 结论（Blocker / Warning / OK）

- `Blocker` 基线体量失真。计划声称的 `~1500` 行与当前 V2 实测 `2097`（仅 5 个点名文件）/ `3421`（整个非测试入口包）不符，且 `bridge.go` 对照文件映射错误，会直接导致排期、拆分与守卫预算全偏小。`docs/plans/迁移/p6-execution-plan.md:18-26` `docs/plans/迁移/p6-execution-plan.md:139-141` `go-agent-v2/cmd/agent-terminal/app_bridge.go:32-129`

- `Blocker` 守卫预算未闭合。`lifecycle.go ~80`、`binding.go ~80`、`window.go ~40` 都没有与现有职责面形成可执行拆分；按仓库 guard 常量，这不是“实现时自然压缩”的问题，而是计划层缺拆分设计。`docs/plans/迁移/p6-execution-plan.md:59-63` `internal/archtest/guardlib.go:17-25` `go-agent-v2/cmd/agent-terminal/main.go:79-219` `go-agent-v2/cmd/agent-terminal/main_setup.go:137-183` `go-agent-v2/cmd/agent-terminal/main_setup.go:388-482`

- `Blocker` 并行切片自相矛盾。A 已声明负责 `lifecycle.go/window.go`，C 又声明负责 `窗口配置/Shutdown/Signal/embed 前端`；这不是并行，而是职责重叠。B 则只在最终接线上依赖 A，不应被描述为全程阻塞。`docs/plans/迁移/p6-execution-plan.md:56-63` `docs/plans/迁移/p6-execution-plan.md:100-116` `docs/plans/迁移/p6-execution-plan.md:123-130`

- `Warning` `debug_server.go`、`multi-window`、`dialogs`、`SaveClipboardImage`、大部分 Wails contract tests 可以在“明确降级为 MVP”时不迁移；但当前计划没有任何 de-scope 文字，反而使用了“完整启动”“bus events 推 frontend”这类接近保留合同的表述，因此不能按默认删除处理。`docs/plans/迁移/p6-execution-plan.md:8-12` `docs/plans/迁移/p6-execution-plan.md:108-116` `docs/plans/迁移/p6-execution-plan.md:154-160` `go-agent-v2/cmd/agent-terminal/debug_server.go:272-371` `go-agent-v2/cmd/agent-terminal/app.go:236-300` `go-agent-v2/cmd/agent-terminal/app_dialogs.go:12-149`

- `结论` 当前版本不应直接执行。需要先修订 4 件事：1) 重写 V2 基线与对照文件表；2) 明确 MVP 删除项与保留项；3) 重切 A/B/C 写集；4) 把 Done 改成可在 CI 用 stub/fake emitter 验证的条目。`docs/plans/迁移/p6-execution-plan.md:18-26` `docs/plans/迁移/p6-execution-plan.md:50-67` `docs/plans/迁移/p6-execution-plan.md:120-130` `docs/plans/迁移/p6-execution-plan.md:154-167`

## 互辩

### 对 audit-p6-plan-A 的批判

1. `Tighten wording`。A 把“根 `go.mod` 缺 Wails 依赖”放成首个 `Blocker`，证据本身成立，但 blocker 级别排位过重。代码只证明根模块当前没有 `github.com/wailsapp/wails/v3`，而计划也已经把这件事写成待确认依赖；相比之下，更立即打断执行的是计划自己的基线和对照表已经失真。`docs/plans/迁移/audit-p6-plan-A.md:5-6` `go.mod:5-27` `docs/plans/迁移/p6-execution-plan.md:166-167` `docs/plans/迁移/p6-execution-plan.md:18-26` `docs/plans/迁移/p6-execution-plan.md:141-141`

2. `Refute`。A 在 dispatch 段把“至少需要改 `internal/platform/rpc/server.go` / `module.go`”说成近似必经路径，但代码只证明“当前没有公开 dispatch 入口”，没有证明“只能改 server 才能做”。现有 `fx` 已经把 `handler.Map` 作为 `group:"rpc_handlers"` 对外提供，且 `rpc.Registry(...)` 可在装配层合并 maps；因此另一条同样被代码允许的实现是：在 `internal/app` 或 `internal/ui/wails/module.go` 侧组装本地 adapter，而不是先动 `server.go`。A 的证据不足以锁死唯一实现路径。`docs/plans/迁移/audit-p6-plan-A.md:10-13` `internal/platform/rpc/server.go:16-40` `internal/platform/rpc/module.go:33-49` `internal/platform/rpc/registry.go:5-13`

3. `Agree but incomplete`。A 花了大量篇幅批 binding 覆盖面，却漏掉了更硬的计划错误：计划把 `cmd/agent-terminal/app_handlers.go` 错映射成 `bridge.go` 对照源文件，但真实 bridge 在 `app_bridge.go`；同时计划的 `~1500 行` 基线连点名 5 文件都覆盖不了。这个遗漏比“是否保留 `SaveClipboardImage` 独立绑定”更基础，因为它会直接污染工期和拆分。`docs/plans/迁移/audit-p6-plan-A.md:15-20` `docs/plans/迁移/p6-execution-plan.md:18-26` `docs/plans/迁移/p6-execution-plan.md:139-141` `go-agent-v2/cmd/agent-terminal/app_bridge.go:32-129` `go-agent-v2/cmd/agent-terminal/app_handlers.go:15-157` `go-agent-v2/cmd/agent-terminal/main.go:327` `go-agent-v2/cmd/agent-terminal/main_setup.go:491` `go-agent-v2/cmd/agent-terminal/app.go:301` `go-agent-v2/cmd/agent-terminal/app_handlers.go:452` `go-agent-v2/cmd/agent-terminal/app_helpers.go:526`

4. `Tighten wording`。A 的“前端资产是净新增”方向基本对，但第 38 行是基于“无匹配”的消极证据。更强的正向证据应该是：V2 明确在桌面入口 `embed frontend` 并把 assets 接到 `application.New(...)`，而当前 V3 `cmd/agent-terminal/main.go` 仍只调用 headless `app.Run()`，`internal/app/app.go` 也只有 `fx` 启停，没有任何 asset 接入点。这样论证更稳，比“text_search 没搜到”更可复核。`docs/plans/迁移/audit-p6-plan-A.md:36-39` `go-agent-v2/cmd/agent-terminal/main.go:32-33` `go-agent-v2/cmd/agent-terminal/main_setup.go:416-424` `cmd/agent-terminal/main.go:10-14` `internal/app/app.go:17-30`

### 对 audit-p6-plan-B 的批判

1. `Tighten wording`。B 第 1 条 blocker 说“计划没有给出谁负责执行阻塞式 Wails 主循环”，措辞过满。计划文本已经明确把桌面入口改成 `app.RunDesktop()`，所以“潜在 owner”是有名字的；真正缺的不是 owner 名称，而是 `RunDesktop()` 与 `fx.Start()` / `app.Done()` / `fx.Stop()` 的顺序设计。应把问题表述成“主循环与 fx 生命周期编排未定义”，而不是“没有 owner”。`docs/plans/迁移/audit-p6-plan-B.md:5-6` `docs/plans/迁移/p6-execution-plan.md:95-97` `docs/plans/迁移/p6-execution-plan.md:104-106` `internal/app/app.go:24-30`

2. `Tighten wording`。B 的信号冲突判断方向对，但当前态与未来态混写了。当前代码能直接证明的是两层 authority：`Run()` 阻塞在 `app.Done()`，`RunGroup()` 自己注册 `SIGINT/SIGTERM` actor；Wails lifecycle 只是 P6 计划里的第三层候选，还没落地。因此更精确的说法应是“当前已有两层，P6 若照计划再加一层，则变成三层”，而不是把三方竞争写成已经在当前代码里同时存在。`docs/plans/迁移/audit-p6-plan-B.md:15-17` `internal/app/app.go:24-30` `internal/app/runner.go:26-43` `internal/platform/runner/group.go:34-47` `docs/plans/迁移/p6-execution-plan.md:115-115`

3. `Agree but incomplete`。B 抓住了 shutdown authority，但漏掉了更快会炸的执行层问题：A/C 写集重叠，且 V2 参考表对 bridge 源文件映射错误。即使 lifecycle 方案全对，按当前批次拆法，A 的 `lifecycle.go/window.go` 与 C 的 `窗口配置/Shutdown/Signal/embed 前端` 依旧会撞同一块实现面；这比抽象讨论 `Wails runner` 是否进入 `run.Group` 更先阻塞执行。`docs/plans/迁移/audit-p6-plan-B.md:3-37` `docs/plans/迁移/p6-execution-plan.md:56-63` `docs/plans/迁移/p6-execution-plan.md:108-116` `docs/plans/迁移/p6-execution-plan.md:123-130` `docs/plans/迁移/p6-execution-plan.md:141-141` `go-agent-v2/cmd/agent-terminal/main_setup.go:372-482` `go-agent-v2/cmd/agent-terminal/app_bridge.go:32-129`

4. `Tighten wording`。B 在 shutdown 映射第 31 行说 V3 已有“DB close / agent stop / subscription OnStop”的映射，这个方向没错，但证据只能证明“相关 `fx.OnStop` hook 存在”，不能证明“desktop 退出时一定走到这些 hook”。当前真正未定义的正是 desktop authority 如何把 `Wails Quit/ShouldQuit` 送到 `fx.Stop`；在这条路径落地之前，把这些现有 hook 直接表述成“已有映射”会削弱自己前面关于 authority 未定型的 blocker。更准确的表述应是“已有 latent OnStop coverage，但 desktop quit 还没接通”。`docs/plans/迁移/audit-p6-plan-B.md:30-31` `internal/platform/db/module.go:28-39` `internal/provider/unified/module.go:32-40` `internal/provider/unified/session.go:84-101` `internal/platform/bus/module.go:25-35` `internal/platform/rpc/module.go:51-69` `internal/app/app.go:24-30`
