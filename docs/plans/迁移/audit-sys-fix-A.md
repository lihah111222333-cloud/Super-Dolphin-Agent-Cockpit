# 系统级修复审查 — Agent A
## 1. S1 bus→push（逐项验证+行号）

1. 订阅事件：OK。
`bindEventBridge` 在 `fx.Invoke` 中注册 lifecycle hook，并在 `OnStart` 调用 `subscribeCoreEventPushes`，见 `internal/platform/rpc/module.go:22-23`、`internal/platform/rpc/module.go:54-58`。`subscribeCoreEventPushes` 实际订阅了 `agentdto.StateChanged`、`turndto.TurnStarted`、`turndto.TurnCompleted` 三类事件，见 `internal/platform/rpc/push.go:82-90`。

2. push method name 格式：Blocker。
当前推送 method 固定为 `"event/agent/stateChanged"`、`"event/turn/started"`、`"event/turn/completed"`，见 `internal/platform/rpc/push.go:17-19`。仓内可见消费端仍按裸 method 消费：`go-agent-v2/internal/uistate/event_normalizer.go:79-81` 只识别 `turn/started` 与 `turn/completed`，`go-agent-v2/cmd/agent-terminal/app_helpers.go:104-110` 只把 `ui/state/changed`、`thread/started`、`turn/completed`、`turn/aborted` 作为 bridge focus 事件，`go-agent-v2/internal/apiserver/server_notify_ui_matrix_guard_test.go:20-23` 也以 `ui/state/changed` 与 `thread/started` 为矩阵输入。可见 `event/...` 前缀与当前可见前端消费协议不对齐。

3. 活跃 jrpc2 会话跟踪线程安全：OK。
`Server` 用 `sync.RWMutex` 保护 `active map[*jrpc2.Server]struct{}`，见 `internal/platform/rpc/server.go:16-23`。`addActive`、`removeActive`、`snapshotActive` 分别在写锁/读锁下访问该 map，见 `internal/platform/rpc/server.go:104-130`。`NotifyAll` 先取快照再逐个通知，网络调用不在锁内，见 `internal/platform/rpc/server.go:46-49`、`internal/platform/rpc/server.go:122-130`。

4. `NotifyAll` 失败处理与阻塞风险：Warning。
单个会话推送失败只记 warn 并继续其他会话，见 `internal/platform/rpc/server.go:46-49`；这保证了单点失败不会中断后续 fan-out。风险在于该路径没有 timeout 或异步隔离：订阅回调直接调用 `server.NotifyAll(context.Background(), ...)`，见 `internal/platform/rpc/push.go:82-90`，`ResilientSubscribe` 也是直接执行 `fn(ev)`，仅做 panic recover，不起 goroutine，见 `internal/platform/bus/resilient.go:10-22`。因此慢会话会把当前 subscriber 回调拖长，代码内没有本地限时保护。

5. fx lifecycle / DI 图匹配：OK。
`bindEventBridge` 需要 `fx.Lifecycle`、`*PushBridge`、`*Server`、`*slog.Logger`，见 `internal/platform/rpc/module.go:51-68`。`*Server` 与 `*PushBridge` 由同一模块提供，见 `internal/platform/rpc/module.go:14-21`；`NewPushBridge` 依赖的 `*event.Dispatcher` 由 `bus.Module` 中的 `NewDispatcher` 提供，见 `internal/platform/bus/module.go:10-23`、`internal/platform/bus/bus.go:14-16`；`*slog.Logger` 由 `NewLogger` 提供，见 `internal/app/app.go:11-15`，并在 app 组合中引入 `bus.Module`、`rpc.Module`、`config.Module`，见 `internal/app/modules.go:23-31`、`internal/platform/config/module.go:5`。就可见依赖而言，注入链闭合。

6. archtest 行数/复杂度：OK。
archtest 守卫阈值是 `MaxFileLines=400`、`MaxFuncLines=80`、`MaxCCComplexity=10`，见 `internal/archtest/guardlib.go:17-24`、`internal/archtest/guardlib.go:260-279`。本次涉及的 RPC 文件都很小：`push.go` 到第 92 行结束，`server.go` 到第 146 行结束，`module.go` 到第 69 行结束，见 `internal/platform/rpc/push.go:75-92`、`internal/platform/rpc/server.go:133-146`、`internal/platform/rpc/module.go:51-69`。编译守卫中的 `go test ./internal/archtest/... -count=1` 也已通过，未报这几处文件的 size/complexity violation。

## 2. S3 CloseAll lifecycle（逐项验证+行号）

1. `OnStop` hook 注册与 ctx 传递：OK。
`registerSessionShutdown` 由 `fx.Invoke` 注册，见 `internal/provider/unified/module.go:29`、`internal/provider/unified/module.go:32-42`。`OnStop` 直接把 Fx 传入的 `ctx` 传给 `sessions.CloseAll(ctx)`，见 `internal/provider/unified/module.go:37-39`。调用链也直接落到 `CloseAll`，见 `internal/provider/unified/module.go:38`、`internal/provider/unified/session.go:69-86`。

2. `CloseAll` 幂等性与 nil 保护：OK。
`CloseAll` 先判空 receiver，再为 nil ctx 回退 `context.Background()`，见 `internal/provider/unified/session.go:69-75`。`drain` 会复制现有 map 后立即把 `m.sessions` 置成新空 map，见 `internal/provider/unified/session.go:88-97`，因此同一 `SessionManager` 上重复调用 `CloseAll` 时，第二次只会遍历空 map，不会重复关闭已 drain 的 session。

3. `drain()` 的 mutex 使用与死锁风险：OK。
`drain` 只在复制和清空 map 时持有 `m.mu`，见 `internal/provider/unified/session.go:89-97`。`CloseAll` 在锁外执行 `session.Close(ctx)` 和 `session.ForceStop()`，见 `internal/provider/unified/session.go:76-84`，因此不会把外部阻塞调用包在 manager 锁里，也没有自死锁路径。

4. `RemoveSession` 与 `CloseAll` 的并发语义：Blocker。
从 map 访问一致性看，`Remove` 与 `drain` 都使用同一把 `m.mu`，见 `internal/provider/unified/session.go:59-67`、`internal/provider/unified/session.go:88-97`，所以 map 本身是并发安全的。问题出在生命周期语义：`sessionCleanerAdapter.RemoveSession` 只是转调 `SessionManager.Remove`，不做 `Close`/`ForceStop`，见 `internal/provider/unified/session_adapter.go:29-34`、`internal/provider/unified/session.go:59-67`；而 orchestration 会在 `StopAgent`、`StopAllAgents`、`handleProcessExit` 三条路径上调用 `removeSession`，见 `internal/sidecar/orch/orchestration/service.go:125`、`internal/sidecar/orch/orchestration/service.go:136`、`internal/sidecar/orch/orchestration/service.go:363`。runner 在 runtime ctx 被取消时执行 `StopAllAgents()`，见 `internal/sidecar/orch/orchestration/runner_actor.go:36-39`、`internal/sidecar/orch/orchestration/runner_actor.go:79-80`；`BindRuntime.OnStop` 又会先 cancel 运行时再等待退出，见 `internal/app/runner.go:48-58`。Fx 明确规定 `OnStop` 逆序执行，见 `/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/app.go:709-723`、`/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/internal/lifecycle/lifecycle.go:260-289`。在当前装配下，runtime stop 很容易先把 session 从 manager 删除，随后 `registerSessionShutdown` 的 `CloseAll` 只能看到残留在 map 中的 session。由于 `contract.Session` 明确暴露了 `Close`/`ForceStop` 资源收口接口，见 `internal/contract/provider.go:22-37`，这个 delete-only 路径会让 shutdown 清理不完整。

## 3. S4 store 聚合（逐项验证+行号）

1. `store.Module` 已聚合 `sqlc.New` + 19 个 repo module：OK。
`sqlc.New` 在 `internal/store/module.go:29` 注册。19 个 repo module 均已出现且各 1 次：
- `agentstatus.Module`，`internal/store/module.go:30`，定义见 `internal/store/agentstatus/module.go:5`
- `ailog.Module`，`internal/store/module.go:31`，定义见 `internal/store/ailog/module.go:5`
- `auditlog.Module`，`internal/store/module.go:32`，定义见 `internal/store/auditlog/module.go:5`
- `binding.Module`，`internal/store/module.go:33`，定义见 `internal/store/binding/module.go:5`
- `buslog.Module`，`internal/store/module.go:34`，定义见 `internal/store/buslog/module.go:5`
- `commandcard.Module`，`internal/store/module.go:35`，定义见 `internal/store/commandcard/module.go:5`
- `cwdlock.Module`，`internal/store/module.go:36`，定义见 `internal/store/cwdlock/module.go:5`
- `dbquery.Module`，`internal/store/module.go:37`，定义见 `internal/store/dbquery/module.go:5`
- `interaction.Module`，`internal/store/module.go:38`，定义见 `internal/store/interaction/module.go:5`
- `prompt.Module`，`internal/store/module.go:39`，定义见 `internal/store/prompt/module.go:5`
- `sharedfile.Module`，`internal/store/module.go:40`，定义见 `internal/store/sharedfile/module.go:5`
- `systemlog.Module`，`internal/store/module.go:41`，定义见 `internal/store/systemlog/module.go:5`
- `taskack.Module`，`internal/store/module.go:42`，定义见 `internal/store/taskack/module.go:5`
- `taskdag.Module`，`internal/store/module.go:43`，定义见 `internal/store/taskdag/module.go:5`
- `tasktrace.Module`，`internal/store/module.go:44`，定义见 `internal/store/tasktrace/module.go:5`
- `thread.Module`，`internal/store/module.go:45`，定义见 `internal/store/thread/module.go:5`
- `topologyapproval.Module`，`internal/store/module.go:46`，定义见 `internal/store/topologyapproval/module.go:5`
- `uipreference.Module`，`internal/store/module.go:47`，定义见 `internal/store/uipreference/module.go:5`
- `workspace.Module`，`internal/store/module.go:48`，定义见 `internal/store/workspace/module.go:5`

2. import 路径正确：OK。
19 个子模块 import 全部位于 `internal/store/module.go:6-25`。本次 `go build ./...` 已通过，说明这些 import 路径在当前仓库下都能解析并参与编译。

3. 重复注册：OK。
`internal/store/module.go:30-48` 中每个 module 名称只出现一次，import block `internal/store/module.go:6-25` 里也没有重复包路径。未见重复注册。

4. app 侧是否只通过 `store.Module` 引入：OK。
`internal/app/modules.go` 只 import 顶层 `internal/store`，见 `internal/app/modules.go:20`，并只在 options 中放入 `store.Module`，见 `internal/app/modules.go:31`。未见 `internal/store/*` 子模块被 app 直接引入的绕过路径。

## 4. 编译守卫

1. `go build ./...`：通过。
2. `go vet ./...`：通过。
3. `go test ./internal/archtest/... -count=1`：通过，结果为 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.276s`。
4. LSP diagnostics：未见类型/编译错误，仅有两条 `mapsloop` 优化提示，分别位于 `internal/platform/rpc/server.go:37` 与 `internal/provider/unified/session.go:94`。

## 结论（Blocker / Warning / OK）

Blocker：
- S1 的 push method 命名为 `event/...`，与当前仓内可见 bridge/UI 消费协议 `ui/state/changed`、`turn/started`、`turn/completed` 不对齐，见 `internal/platform/rpc/push.go:17-19`、`go-agent-v2/internal/uistate/event_normalizer.go:79-81`、`go-agent-v2/cmd/agent-terminal/app_helpers.go:104-110`。
- S3 的 shutdown 生命周期存在 delete-before-close 窗口：runtime stop 会先触发 `StopAllAgents -> RemoveSession`，随后 `CloseAll` 只能关闭仍留在 map 中的 session，见 `internal/app/runner.go:48-58`、`internal/sidecar/orch/orchestration/runner_actor.go:36-39`、`internal/sidecar/orch/orchestration/service.go:130-137`、`internal/provider/unified/session_adapter.go:29-34`、`internal/provider/unified/session.go:59-67`、`internal/provider/unified/module.go:37-39`。

Warning：
- S1 的 push fan-out 对失败会记录 warn 并继续，但没有 timeout/异步隔离；慢 notify 会拖长 subscriber 回调，见 `internal/platform/rpc/push.go:82-90`、`internal/platform/bus/resilient.go:10-22`、`internal/platform/rpc/server.go:42-49`。

OK：
- S1 的订阅集合、active session 锁保护、DI 注入链、archtest 守卫均成立，见 `internal/platform/rpc/push.go:82-90`、`internal/platform/rpc/server.go:104-130`、`internal/platform/rpc/module.go:14-24`、`internal/archtest/guardlib.go:17-24`。
- S4 的 `store.Module` 已聚合 `sqlc.New` 与 19 个 store repo module，且 app 侧只通过 `store.Module` 引入，见 `internal/store/module.go:28-49`、`internal/app/modules.go:20-31`。
