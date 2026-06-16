# V2↔V3 1:1 对齐：应用启停 + 优雅关闭

## 范围

本次只比对以下源码：

- V2 desktop: `go-agent-v2/cmd/agent-terminal/main.go`, `go-agent-v2/cmd/agent-terminal/main_setup.go`, `go-agent-v2/cmd/agent-terminal/app_helpers.go`, `go-agent-v2/internal/runner/manager_lifecycle.go`
- V2 headless: `go-agent-v2/cmd/app-server/main.go`
- V2 provider stop: `go-agent-v2/legacy-agentsdk/claude/client.go`, `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go`
- V3 app/fx: `internal/app/app.go`, `internal/app/modules.go`, `internal/app/runner.go`
- V3 wails: `internal/ui/wails/lifecycle.go`, `internal/ui/wails/module.go`
- V3 runtime/shutdown: `internal/platform/runner/group.go`, `internal/sidecar/orch/orchestration/runner_actor.go`, `internal/sidecar/orch/orchestration/service.go`, `internal/sidecar/orch/orchestration/helpers.go`, `internal/provider/unified/module.go`, `internal/provider/unified/session.go`, `internal/provider/unified/session_adapter.go`, `internal/platform/db/module.go`

补充语义基线：

- 本地 `go doc go.uber.org/fx.App.Start`: `OnStart` 按注册顺序执行
- 本地 `go doc go.uber.org/fx.App.Stop`: `OnStop` 按逆序执行

## 总览

| 项目 | 结论 | 结论摘要 |
| --- | --- | --- |
| 启动顺序 | ⚠️ | 两边都是“先后端、后桌面 UI”，但 V2 是手写串行装配，V3 是 FX 装配 + `app.Start()` 后再 `wailsApp.Run()`，不是 1:1 形状。 |
| headless vs desktop 双模式 | ⚠️ | 两边都覆盖 headless/desktop，但 V2 是两个入口程序，V3 是同一 `internal/app` 下 `Run()`/`RunDesktop()` 双路径。 |
| signal 处理 | ❌ | V2 desktop 有显式 OS signal 根上下文；V3 只在 headless `RunGroup` 打开 `SIGINT/SIGTERM`，desktop 模式代码层面关闭 signal runner。 |
| shutdown 链 | ⚠️ | V3 可以推导出 `quit overlay → agent stop → session close → db close`，但实际清理时点、agent stop 手法、session close 组织方式都与 V2 不同。 |
| runner nil 返回处理 | ❌ | V3 任一 runner 返回后都会触发 `fx.Shutdown()`；V2 desktop 的 app-server goroutine 只在 `err != nil` 时记日志，`nil` 返回不驱动整应用退出。 |

## 1. 启动顺序

### V2

`go-agent-v2/cmd/agent-terminal/main.go:61-77` 的主顺序是显式串行：

1. `initializeMainRuntime()` 先做 env/logger/flag/API listener 初始化
2. `setupShutdownSignals()` 建立 root ctx 和 shutdown reason
3. `setupDatabase()` 初始化 DB/migration/DB handler
4. `setupCwdInstanceLock()`
5. `setupAppServer()` 创建 `AgentManager`、LSP manager、app-server，并立刻后台 `ServeListener`
6. `startMainDebugServer()` 按 flag 进入 debug web UI
7. `runMainDesktopApplication()` 构造并运行 Wails app

关键代码：

- `go-agent-v2/cmd/agent-terminal/main_setup.go:137-183`
- `go-agent-v2/cmd/agent-terminal/main_setup.go:240-258`
- `go-agent-v2/cmd/agent-terminal/main_setup.go:312-360`
- `go-agent-v2/cmd/agent-terminal/main_setup.go:372-490`

### V3

V3 desktop 路径在 `internal/app/app.go:29-50`：

1. `newDesktopFXApp(...)` 组装 `Module + uiwails.Module + fx.Invoke(BindRuntime)`
2. `app.Start(ctx)` 先跑 FX `OnStart`
3. `BindRuntime.OnStart` 启动 `RunGroup(...)`
4. 只有在 FX 已经 start 完成后，才执行 `wailsApp.Run()`
5. `wailsApp.Run()` 返回后，再 `app.Stop(ctx)`

关键代码：

- `internal/app/modules.go:23-48`
- `internal/app/app.go:29-50`
- `internal/app/app.go:68-75`
- `internal/app/runner.go:32-56`

### 判断

`⚠️`

高层顺序是对齐的：都是“后端先起来，桌面事件循环后进入”。但 V2 是手写初始化链，V3 是 FX 生命周期装配；V2 还把 `cwd lock`、`debug server`、`app-server listener` 明确插在 UI 之前，V3 没有这个 1:1 对应形状。

## 2. headless vs desktop 双模式

### V2

V2 不是同一入口双模式，而是两个程序：

- desktop: `go-agent-v2/cmd/agent-terminal/main.go:61-77`
- headless: `go-agent-v2/cmd/app-server/main.go:20-83`

`cmd/app-server` 自己处理 `signal.NotifyContext`、DB、migration、LSP、`srv.ListenAndServe(ctx, *listen)`，与 desktop 不是同一启动/关闭骨架。

### V3

V3 在同一 app 层里分两条路径：

- headless: `internal/app/app.go:25-27` -> `runApp(NewApp())`
- desktop: `internal/app/app.go:29-50` -> `RunDesktop()`

两条路径共享 `Module` 和 `BindRuntime`，desktop 只是额外叠加 `uiwails.Module`，并在 `RunDesktop()` 里手动 `wailsApp.Run()`。

### 判断

`⚠️`

能力层面是对齐的，结构层面不是 1:1。V2 是“双二进制入口”，V3 是“同一 FX 组合下的双运行模式”。

## 3. signal 处理

### V2

desktop 的 signal 处理很明确：

- `go-agent-v2/cmd/agent-terminal/main_setup.go:137-183`
- `signal.NotifyContext(..., SIGINT, SIGTERM)` 建 root ctx
- 额外 `signal.Notify(sigCh, SIGINT, SIGTERM, SIGQUIT, SIGHUP)` 记录 shutdown reason，并只发一次 cancel

headless `cmd/app-server` 也有单独的 `signal.NotifyContext(..., SIGINT, SIGTERM)`：

- `go-agent-v2/cmd/app-server/main.go:45-46`

### V3

V3 signal 只挂在 runner group 上：

- `internal/app/runner.go:38-40`: `EnableSignals: p.Lifecycle == nil`
- `internal/platform/runner/group.go:49-64`: 只监听 `SIGINT`, `SIGTERM`

这意味着：

- headless `Run()` 会开 signal actor
- desktop `RunDesktop()` 因为注入了 `WailsLifecycle`，`EnableSignals` 为 `false`
- 代码层面没有 V2 desktop 那种显式 `signal.NotifyContext` 根上下文，也没有 `SIGQUIT/SIGHUP` reason 记录

### 判断

`❌`

这不是 1:1 对齐。V3 desktop 关闭了 repo 级 signal actor，实际更依赖 Wails 自身退出路径；V2 desktop 则明确把 OS signal 纳入主 shutdown 框架。

## 4. shutdown 链

## 4.1 quit overlay

### V2

`go-agent-v2/cmd/agent-terminal/main.go:92-113`：

- 首次退出请求时显示 overlay
- 320ms 后 `runMainQuitRequest()` 调 `wailsApp.Quit()`

### V3

`internal/ui/wails/lifecycle.go:82-99`：

- 若有 active agents，先 `emitQuitOverlay(activeCount)`
- 320ms 后 `requestBackendShutdown()`
- 若没有 active agents，直接请求 backend shutdown

### 判断

`✅`

overlay 的拦截思路基本对齐，V3 还把 `active_agents` 明确放进 payload。

## 4.2 agent stop

### V2

V2 的真正 stop 在 `appSvc.shutdown()` 内：

- `go-agent-v2/cmd/agent-terminal/main.go:134-156`
- `go-agent-v2/cmd/agent-terminal/main.go:189-219`
- `go-agent-v2/cmd/agent-terminal/app_helpers.go:289-336`
- `go-agent-v2/internal/runner/manager_lifecycle.go:19-117`

链路是：

1. `OnShutdown` 进入 `runMainOnShutdown()`
2. 先 `cancelWithReason("wails_on_shutdown")`
3. 再 `appSvc.shutdown()`
4. `appSvc.shutdown()` 调 `mgr.StopAll()`
5. 每个 agent `Stop()` 先 `Client.Shutdown()`，失败才 fallback `Kill()`

这是真正的“graceful stop with force fallback”。

### V3

V3 desktop 下，`ShouldQuit()`/`OnShutdown()` 只是请求 FX shutdown：

- `internal/ui/wails/lifecycle.go:101-155`
- `internal/ui/wails/module.go:111-118`

真正 stop 发生在后续 `app.Stop(ctx)` 触发的 `OnStop`：

- `internal/app/app.go:45-50`
- `internal/app/runner.go:57-67`
- `internal/sidecar/orch/orchestration/runner_actor.go:36-39`
- `internal/sidecar/orch/orchestration/service.go:143-164`
- `internal/sidecar/orch/orchestration/helpers.go:303-308`

链路是：

1. `requestBackendShutdown()` 调 `fx.Shutdowner.Shutdown()`
2. `wailsApp.Run()` 返回后，`RunDesktop()` 才执行 `app.Stop(ctx)`
3. `BindRuntime.OnStop` cancel runtime ctx
4. `runnerActor.Run()` 在 `ctx.Done()` 分支里调用 `StopAllAgents()`
5. `StopAllAgents()` 的 `stopAgentLocked()` 直接 `cmd.Process.Kill()`

这里和 V2 最大的差异不是“有没有 stop”，而是“是否 graceful”：

- V2: `Shutdown()` -> fallback `Kill()`
- V3: 当前实现就是直接 `Kill()`

### 判断

`❌`

如果按“优雅关闭”标准看，V3 这里没有对齐 V2。

## 4.3 session close

### V2

V2 没有独立的 session manager shutdown 步骤。session close 被折叠进 provider client 的 `Shutdown()`：

- `go-agent-v2/legacy-agentsdk/claude/client.go:414-451`
- `go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go:202-243`

也就是“agent stop”与“session close”混在 provider client 层。

### V3

V3 有显式 session close：

- `internal/sidecar/orch/orchestration/service.go:104-108`
- `internal/sidecar/orch/orchestration/service.go:143-153`
- `internal/provider/unified/session_adapter.go:34-39`
- `internal/provider/unified/session.go:60-104`
- `internal/provider/unified/module.go:33-43`

链路是：

1. `StopAllAgents()` 成功 stop 进程后立即 `removeSession(agent.id)`
2. `SessionManager.Remove()` 用 5s timeout 调 `session.Close(ctx)`，失败再 `ForceStop()`
3. `registerSessionShutdown()` 的 `CloseAll(ctx)` 作为剩余 session 的兜底

provider 侧 `session.Close` 也明确存在：

- `internal/provider/claudecli/session.go:240-245`
- `internal/provider/codexapp/session.go:211-220`

### 判断

`⚠️`

V3 的 session close 比 V2 更清晰、更中心化，但这也意味着它不是 V2 的 1:1 迁移形态。

## 4.4 db close

### V2

DB close 在 desktop `OnShutdown` 的第 `[5/5]` 步：

- `go-agent-v2/cmd/agent-terminal/main.go:212-217`

并且 V2 还有：

- 10s hard deadline 强退：`go-agent-v2/cmd/agent-terminal/main.go:136-143`
- `app.Run()` 不返回时的 3s safety exit：`go-agent-v2/cmd/agent-terminal/main.go:151-155`

### V3

DB close 由 FX hook 管：

- `internal/platform/db/module.go:28-39`

结合本地 `go doc go.uber.org/fx.App.Stop` 的“`OnStop` 逆序执行”，以及 `BindRuntime` 是在 `newDesktopFXApp(..., fx.Invoke(BindRuntime))` 中最后追加的，可以推导出：

1. `BindRuntime.OnStop` 会先跑
2. 其内部触发 runtime cancel -> `runnerActor.stopAll()` -> `StopAllAgents()` -> `removeSession(...)`
3. `unified.registerSessionShutdown()` 再兜底 `CloseAll()`
4. `db.registerLifecycle().OnStop` 更后面才 `pool.Close()`

### 判断

`⚠️`

V3 大体能形成“agent/session 在前，db close 在后”的链条，但它缺少 V2 desktop 那种显式 shutdown deadline 和 safety exit，`RunDesktop()` 目前也是 `app.Stop(context.Background())`，不是有界停止。

## 5. runner nil 返回处理

### V2

V2 desktop 没有统一 runner group。后台 app-server 在 `setupAppServer()` 里直接 goroutine 启动：

- `go-agent-v2/cmd/agent-terminal/main_setup.go:349-353`

它的处理只有：

- `if err := appSrv.ServeListener(ctx, ln); err != nil { logger.Error(...) }`

也就是说：

- `err != nil` 只记日志
- `err == nil` 直接静默结束
- 两种情况都不会驱动桌面应用退出

### V3

V3 的 `BindRuntime()` 对任何 `RunGroup()` 返回都执行统一收口：

- `internal/app/runner.go:37-53`

关键点：

1. `err := platformrunner.RunGroup(...)`
2. 无论 `err` 是否为 `nil`，都会 `_ = p.Shutdowner.Shutdown()`
3. desktop 下 `watchFXShutdown()` 监听 `app.Done()`，然后 `lifecycle.NotifyBackendFailed()` 触发 UI quit
4. headless 下 `runApp()` 直接 `<-app.Done()` 再 `app.Stop(...)`

对应代码：

- `internal/app/app.go:60-66`
- `internal/app/app.go:78-88`
- `internal/ui/wails/lifecycle.go:105-112`

### 判断

`❌`

V3 明确把“runner 返回”视为整个 runtime 结束条件；V2 desktop 没有这一层统一治理。`runner nil return` 行为不是 1:1 对齐，而是 V3 明显更激进。

## 最终结论

如果标准是“行为意图大体一致”，那么 V3 已经覆盖了：

- desktop quit overlay
- headless/desktop 两种运行能力
- agent/session/db 的分层 shutdown

但如果标准是“和 V2 main/shutdown 体系 1:1 对齐”，当前结论是：

- `✅` quit overlay 这一段基本对齐
- `⚠️` 启动顺序、双模式、session close、db close 只有能力级对齐，不是结构级 1:1
- `❌` signal 处理、agent graceful stop、runner nil return 处理没有对齐

最关键的三处缺口：

1. V3 desktop 缺少 V2 那种显式 OS signal 根上下文和 shutdown reason 记录。
2. V3 `StopAllAgents()` 当前是直接 `Kill()`，不等价于 V2 的 `Shutdown() -> Kill fallback`。
3. V3 `RunDesktop()` 的实际 cleanup 发生在 `wailsApp.Run()` 返回之后，且 `app.Stop(context.Background())` 没有 V2 的 hard deadline / safety exit 保护。
