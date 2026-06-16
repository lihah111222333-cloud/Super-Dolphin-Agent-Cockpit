# 架构合规：错误处理一致性

## 范围与方法

- 本次核对只使用 LSP 读文件、文本检索与符号引用完成，未使用 `grep/find/cat/sed/awk`。
- 核对范围覆盖 `internal/` 主实现，并补充检查 `go-agent-v2/` 里仍存在的 `// ignore` 前端吞错点。
- 关键检索词包括：`jrpc2.Errorf(`、`jrpc2.Code(`、`WrapStoreError`、`CapabilityError`、`errors.Is(`、`errors.As(`、`_ = `、`// ignore`、`recover()`、`go func()`、`context.Background()`、`.Close(`、`exec.Command(`。

## 总结

| 检查项 | 结论 | 说明 |
| --- | --- | --- |
| 1. RPC 层错误码是否全部避开 `-32xxx` | 不通过 | 自定义业务码全部在 `-31001..-31006`，但 `ThreadScope` 仍直接返回 `jrpc2.InvalidParams`（`-32602`）。 |
| 2. store 层错误包装是否一致 | 通过（repo 层） | `WrapStoreError` 已覆盖 `internal/store` 下 19/19 个 repo；`internal/store/sqlc/db.go` 这种基础 helper 仍裸返回原始 DB 错误。 |
| 3. `CapabilityError` 是否被一致消费 | 不通过 | 生产构造点只有 Claude provider 的 3 处；生产调用方未发现 `errors.Is`/`errors.As` typed detection，只有测试在做 `errors.As`。 |
| 4. 静默吞错是否存在 | 不通过 | 发现 38 个生产 Go 吞错点、2 个工具链 Go 吞错点、9 个前端 `// ignore` 注释吞错点。 |
| 5. goroutine / bus 是否有 panic 防护 | 不通过 | 生产代码里唯一显式 `recover` 在 `bus.ResilientSubscribe`；其余 goroutine 无本地 `recover`，direct bus subscriber panic 会直接炸出 goroutine。 |
| 6. context 传递与 Close timeout 是否一致 | 不通过 | 进程启动、RPC push、session close 等多条长路径仍用 `context.Background()` 或无 ctx；`Session.Close(ctx)` 两个 provider 实现都忽略了传入 ctx。 |

## 1. RPC 层错误码

### 1.1 自定义业务码

`internal/platform/rpc/errors.go:5-11` 与 `internal/platform/rpc/errors_helper.go:5-8` 当前定义了 6 个自定义业务码：

| 常量 | 取值 | 结论 |
| --- | --- | --- |
| `CodeNotFound` | `-31001` | 保留区间外 |
| `CodeInvalidState` | `-31002` | 保留区间外 |
| `CodeConflict` | `-31003` | 保留区间外 |
| `CodeCapabilityGate` | `-31004` | 保留区间外 |
| `CodeApprovalTimeout` | `-31005` | 保留区间外 |
| `CodeNotImplemented` | `-31006` | 保留区间外 |

这部分本身是合规的。

### 1.2 仍落入 `-32xxx` 的 RPC 返回

`internal/platform/rpc/handler.go:58-75` 的 `ThreadScope` 在两处直接构造：

- `jrpc2.InvalidParams`，用于参数解码失败：`internal/platform/rpc/handler.go:61`
- `jrpc2.InvalidParams`，用于缺少 `threadId`：`internal/platform/rpc/handler.go:74`

`jrpc2.InvalidParams` 对应 JSON-RPC 标准保留码 `-32602`，属于 `-32xxx` 区间。

### 1.3 结论

- 如果要求是“自定义业务错误码不要占用 jrpc2/JSON-RPC 保留区间”，当前是通过的。
- 如果要求是“RPC 层所有返回码都必须 `!= -32xxx`”，当前不通过，因为 `InvalidParams` 仍在生产路径中直接返回。

## 2. store 层错误包装

### 2.1 `WrapStoreError` 已覆盖的 repo

对 `internal/platform/db/errors.go:47-61` 的 `WrapStoreError` 做 LSP references，命中 19 个 repo 引用，刚好覆盖 `internal/store` 下全部 19 个 store 包：

- `agentstatus`
- `ailog`
- `auditlog`
- `binding`
- `buslog`
- `commandcard`
- `cwdlock`
- `dbquery`
- `interaction`
- `prompt`
- `sharedfile`
- `systemlog`
- `taskack`
- `taskdag`
- `tasktrace`
- `thread`
- `topologyapproval`
- `uipreference`
- `workspace`

代表性证据：

- `internal/store/commandcard/store.go:17-87`
- `internal/store/taskdag/store.go:16-292`
- `internal/store/workspace/store.go:16-154`

### 2.2 哪些 repo 仍裸返回

针对 `internal/store/**/store.go` 做 LSP 文本检索：

- `return err`：无命中
- `return nil, err`：无命中

也就是说，repo 层方法没有找到“直接把底层 DB 错误原样抛出”的裸返回点。

### 2.3 仍裸返回的位置

仍存在原始 DB 错误裸返回，但位置不在 repo 层，而在 `sqlc` 基础 helper：

- `internal/store/sqlc/db.go:60-63` `exec(...)` 直接 `return err`
- `internal/store/sqlc/db.go:65-70` `execRows(...)` 直接返回 `err`
- `internal/store/sqlc/db.go:77-91` `queryMany(...)` 直接返回 `err` / `rows.Err()`

这层目前依赖上层 repo 再统一 `WrapStoreError`。因此：

- repo 层一致性：通过
- sqlc helper 层一致性：未统一包装

## 3. `CapabilityError`

### 3.1 定义与生产使用点

类型定义在 `internal/dto/provider/capability.go:17-28`。

对 `NewCapabilityError` 做 LSP references，生产代码只命中 3 处：

- `internal/provider/claudecli/session.go:232-234` `ListThreads`
- `internal/provider/claudecli/session.go:236-238` `ForkThread`
- `internal/provider/claudecli/session_config.go:13-27` `Configure`

也就是说，`CapabilityError` 目前只在 Claude provider 里小范围使用。

### 3.2 调用方如何消费

生产调用方当前都是“原样上传错误”，没有 typed handling：

- `internal/module/thread/lifecycle.go:113-115` `session.ForkThread(...)` 出错后直接 `return ForkResult{}, err`
- `internal/module/thread/command.go:123-124` `session.Configure(...)` 出错后直接 `return threadConfigResult{}, err`
- `internal/module/thread/command.go:141-142` `session.Configure(...)` 出错后直接 `return threadCommandResult{}, err`

RPC 路由侧也没有把这些错误统一映射成专门的 RPC 码：

- `internal/module/thread/rpc.go:34-36` 的 `thread/fork` 走 `newThreadCall`，不是 `CapabilityThreadHandler`
- `internal/module/thread/rpc.go:61-66` 的 `thread/config/set` / `thread/personality/set` / `thread/approvals/set` 也不是 capability-gated

只有部分路由会先被 RPC gate 挡住：

- `internal/module/thread/rpc.go:62` `thread/model/set`
- `internal/module/thread/rpc.go:67` `thread/compact/start`
- `internal/module/thread/rpc.go:89-95` realtime 相关路由

### 3.3 是否有 `errors.Is` 检测

未发现任何生产代码对 `CapabilityError` 做 `errors.Is` 检测。

未发现任何生产代码对 `CapabilityError` 做 `errors.As` 检测。

当前唯一 typed detection 在测试里：

- `internal/provider/unified/contract_test.go:146-160` 使用 `errors.As(err, &capErr)`

另外，`CapabilityError` 本身没有 sentinel，也没有自定义 `Is` 方法，因此从接口设计上也不适合 `errors.Is`。

### 3.4 结论

- `CapabilityError` 的定义本身没问题。
- 但它没有形成统一消费约定；生产调用方既不 `errors.Is`，也不 `errors.As`，更多是直接透传。
- 从架构一致性角度看，这一项不通过。

## 4. 静默吞错

### 4.1 生产 Go 吞错点：`_ =` / `_, _ =`

| 文件 | 吞错点 |
| --- | --- |
| `internal/app/runner.go` | `:52` `_ = p.Shutdowner.Shutdown()` |
| `internal/dto/shared/ids.go` | `:12` `_, _ = rand.Read(buf)` |
| `internal/sidecar/orch/orchestration/service.go` | `:257` `_ = stopProcess(cmd)` |
| `internal/module/skill/exec.go` | `:173` `_, _ = b.buf.Write(p)` |
| `internal/module/skill/skills_fs.go` | `:255` `_ = os.RemoveAll(target)` |
| `internal/module/thread/command.go` | `:207` `_ = s.upsertThread(ctx, *thread)` |
| `internal/module/thread/lifecycle.go` | `:311` `_ = s.orchestration.StopAgent(ctx, ...)` |
| `internal/module/thread/service.go` | `:108` `_ = s.closeSessionIfActive(ctx, id)` |
| `internal/module/workspace/service_helpers.go` | `:37` `sourceFile.Close()`；`:47` `targetFile.Close()`；`:315` `file.Close()` |
| `internal/platform/db/tx.go` | `:17` `_ = tx.Rollback(ctx)` |
| `internal/platform/rpc/transport_ws.go` | `:40` `_ = conn.WriteControl(...)` |
| `internal/platform/shared/idgen.go` | `:12` `_, _ = rand.Read(buf)` |
| `internal/provider/claudecli/driver.go` | `:127` `_ = s.stop(true)` |
| `internal/provider/claudecli/history.go` | `:33` `file.Close()` |
| `internal/provider/claudecli/session.go` | `:308` `_ = oldTransport.Close()` |
| `internal/provider/claudecli/transport.go` | `:106` `_ = t.signalProcess(syscall.SIGKILL)`；`:160` `_ = t.stdin.Close()` |
| `internal/provider/claudecli/transport_config.go` | `:178` `_ = os.Remove(path)`；`:180` `_ = file.Close()` |
| `internal/provider/codexapp/driver.go` | `:84`、`:89`、`:102`、`:107` 全部是 `_ = s.ForceStop()` |
| `internal/provider/codexapp/history_rollout.go` | `:40` `file.Close()` |
| `internal/provider/codexapp/recovery.go` | `:70` `_ = s.attemptRecovery(reason)`；`:137`、`:140` `_ = s.attemptRecovery(...)` |
| `internal/provider/codexapp/transport.go` | `:73` `_ = t.Kill()`；`:125` `_ = t.Notify("shutdown", nil)`；`:199` `_ = ws.SetWriteDeadline(...)`；`:281`、`:290` `_ = t.ws.Close()`；`:304` `_ = cmd.Process.Signal(os.Interrupt)`；`:310` `_ = cmd.Process.Kill()` |
| `internal/provider/codexapp/transport_helpers.go` | `:37` `_ = listener.Close()` |
| `internal/ui/wails/module.go` | `:116` `_ = shutdowner.Shutdown()` |

这些点并不都同等危险，但它们都属于“显式丢弃 error”。

风险较高的点主要有：

- 生命周期关闭失败被静默吞掉：`internal/app/runner.go:52`、`internal/module/thread/service.go:108`、`internal/ui/wails/module.go:116`
- provider 恢复/停机失败被静默吞掉：`internal/provider/claudecli/driver.go:127`、`internal/provider/codexapp/driver.go:84/89/102/107`、`internal/provider/codexapp/recovery.go:70/137/140`
- 事务 rollback / websocket close / process signal 失败被静默吞掉：`internal/platform/db/tx.go:17`、`internal/provider/codexapp/transport.go:281/290/304/310`

### 4.2 工具链 Go 吞错点

| 文件 | 吞错点 |
| --- | --- |
| `internal/archtest/guardlib.go` | `:203` 整个 `filepath.WalkDir(...)` 返回值被忽略；虽然 callback 内会记录 `walkErr`，但 walk 结束态仍未检查。 |
| `scripts/extract_jsonrpc_methods.go` | `:28` 整个 `filepath.WalkDir(...)` 返回值被忽略；callback 会向 `stderr` 打印 walk 错误，但主流程不感知失败。 |

补充说明：

- LSP 搜索 `_ = ` 还命中了 `scripts/refactor/rename_codexsdk_to_agentsdk_main_guardrail_test.go:123` 的字符串字面量 `"var _ = runtimepkg.TurnRuntime{}"`；这不是可执行吞错点，已排除。

### 4.3 `// ignore` 命中点

后端 Go 代码里没有找到 `// ignore` 注释吞错点。

`go-agent-v2/` 旧前端里有 9 个明确注释掉的吞错点：

| 文件 | 注释位置 | 实际被吞掉的失败 |
| --- | --- | --- |
| `go-agent-v2/cmd/agent-terminal/frontend/vue-app/composables/useCopyThreadInfo.js` | `:73` | model preference lookup failure |
| `go-agent-v2/cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts` | `:94`、`:146` | provider preference 读取失败；sandbox payload 读取/解析失败 |
| `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/api.js` | `:121`、`:189` | unsubscribe failure；frontend bridge logging failure |
| `go-agent-v2/cmd/agent-terminal/frontend/vue-app/services/log.js` | `:32`、`:101`、`:179` | log level 读取失败；bridge sink flush 失败；log level 持久化失败 |
| `go-agent-v2/cmd/agent-terminal/shim/wails-runtime.js` | `:47` | `EventsOff` 失败 |

## 5. panic 防护

### 5.1 goroutine 中是否有 `recover`

对生产 Go 代码搜索 `recover()`，只有 1 处命中：

- `internal/platform/bus/resilient.go:25-29` `recoverCall(...)`

测试里的 `recover` 只有：

- `internal/platform/bus/bus_test.go:150-160`

也就是说，生产代码里唯一显式 panic 防护只存在于 `bus.ResilientSubscribe(...)`。

### 5.2 生产 goroutine 启动点

对生产代码搜索 `go func()`，命中以下启动点：

- `internal/app/app.go:80-86`
- `internal/app/runner.go:37-53`
- `internal/module/turn/service.go:184-202`
- `internal/platform/rpc/server.go:119-122`
- `internal/provider/claudecli/session_events.go:14-30`
- `internal/provider/codexapp/recovery.go:70`
- `internal/provider/codexapp/recovery.go:114-125`
- `internal/provider/codexapp/session_approval.go:19-23`
- `internal/provider/codexapp/transport.go:307`
- `internal/provider/unified/session.go:123-125`
- `internal/ui/wails/runner.go:25-34`

这些 goroutine 启动点都没有本地 `recover`。

### 5.3 bus subscriber 的 panic 行为

repo 内当前有两类订阅方式：

- 带保护：`internal/platform/bus/resilient.go:10-23` 的 `ResilientSubscribe`
- 不带保护：`internal/platform/bus/router.go:18-22`、`internal/platform/bus/typed.go:25-30`、`internal/platform/bus/sink.go:89-99`、`internal/platform/rpc/push.go:68-72`

`ResilientSubscribe` 的行为是：

- 包一层 `recoverCall`
- panic 后只记日志，不再向外传播

而 direct `event.Subscribe(...)` 的真实行为取决于 `github.com/kelindar/event@v1.5.2` 的实现。LSP 检查本地 module cache 可见：

- `event.go:283-285` `group.Add(...)` 启动 `go sub.Listen(...)`
- `event.go:188-212` `consumer.Listen(...)` 里直接 `fn(event)`，没有 `recover`

因此，当前结论是：

- 通过 `ResilientSubscribe` 注册的 subscriber，panic 会被吞掉并记录日志
- 通过 direct `event.Subscribe` 注册的 subscriber，panic 不会被 repo 本地代码兜住；panic 会从 consumer goroutine 直接冒出，等价于进程级风险

## 6. context 传递与 Close timeout

### 6.1 缺 context 的长操作

以下长操作仍然没有把调用方 ctx 贯穿到底：

| 位置 | 问题 |
| --- | --- |
| `internal/provider/claudecli/transport_config.go:23-47` + `internal/provider/claudecli/transport.go:33-63` | `launchCLI -> newTransport -> exec.Command` 整条 Claude CLI 启动链没有 ctx 参数；`internal/provider/claudecli/driver.go:87-99` 只在进入前检查一次 `ctx.Err()`，无法取消实际启动过程。 |
| `internal/provider/codexapp/session.go:75-104` + `internal/provider/codexapp/transport.go:63-76` + `internal/provider/codexapp/transport.go:170-190` | `newSession` 不接收 ctx；内部 `newTransport` 用 `context.Background()` 做 15 秒 connect，`spawnLocal` 也用 `exec.Command` 启本地 `codex app-server`，调用方无法取消构造阶段。 |
| `internal/sidecar/orch/orchestration/service.go:234-264` + `internal/sidecar/orch/orchestration/helpers.go:303-308` | agent 进程启动 `exec.Command` 无 ctx；停止路径直接 `Process.Kill()`，没有 ctx 控制的优雅退出窗口。 |
| `internal/platform/db/module.go:19-26` | `pgxpool.NewWithConfig(context.Background(), poolCfg)` 在建连阶段不吃 lifecycle ctx。 |
| `internal/platform/rpc/push.go:68-90` | event push 到 RPC client 时统一用 `context.Background()` 调 `NotifyClient` / `NotifyAll`，不继承请求 ctx，也没有独立 timeout。 |
| `internal/platform/rpc/module.go:79-85` | late connect 时 `approvals.RestorePending(context.Background(), bridge, current)` 不继承 lifecycle / connect ctx。 |
| `internal/ui/wails/binding_native.go:137-178` | 剪贴板读取与文件写入前的 shell 调用使用 `exec.Command(...).Run/Output`，没有 ctx。 |

### 6.2 哪些 Close 缺 timeout

| 位置 | 问题 |
| --- | --- |
| `internal/provider/claudecli/session.go:240-242` | `Close(context.Context)` 签名收 ctx，但实现直接 `return s.stop(false)`，完全忽略传入 ctx。 |
| `internal/provider/codexapp/session.go:211-215` | `Close(context.Context)` 同样忽略 ctx，直接 `s.transport.Close()`。 |
| `internal/provider/codexapp/transport.go:121-129` + `:295-316` | transport close 只有内部固定 1 秒等待窗口，不能继承调用方 timeout。 |
| `internal/provider/claudecli/transport.go:98-109` + `:165-179` | transport close 只有内部固定 `shutdownGracePeriod=3s`，同样不继承调用方 timeout。 |
| `internal/module/thread/service.go:228-240` | `closeSessionIfActive(ctx, threadID)` 直接把原 ctx 传给 `session.Close(ctx)`，没有套 `WithSessionCloseTimeout`。 |
| `internal/module/thread/service.go:102-119` | `Delete(...)` 不仅没有 close timeout，还在 `:108` 直接吞掉了 `closeSessionIfActive` 返回错误。 |
| `internal/provider/unified/session.go:87-104` | `CloseAll(ctx)` 直接复用外层 ctx；只有 `Remove(...)` 在 `:77-78` 使用了 `WithSessionCloseTimeout(context.Background())`。 |
| `internal/provider/unified/session.go:118-131` | `closeSession(ctx, session)` 虽然 `select` 了 `ctx.Done()`，但底层 provider `Close(ctx)` 本身忽略 ctx，因此这里的 timeout 只是 manager 侧提前返回，不是真取消。 |
| `internal/app/app.go:49-50` + `:61-65` | 顶层 `app.Stop(context.Background())` 没有 deadline，导致 Fx OnStop 链整体缺统一 shutdown timeout。仓内已有 `ShutdownTimeout` 常量（`internal/platform/config/timeouts.go:11`），但当前未使用。 |

### 6.3 结论

- 代码里已经有 `SessionCloseTimeout`、`RPCRequestTimeout` 等 timeout 约定，但只在局部路径使用。
- 当前最大的问题不是“完全没有 timeout 常量”，而是“close / startup / push 三类长路径没有统一继承 ctx 或 deadline”。

## 建议整改顺序

1. 先统一 RPC 错误码策略：明确“保留标准 JSON-RPC 码”还是“全部业务化重映射”，不要两套并存。
2. 给 `CapabilityError` 补统一消费约定：
   - 要么 provider 层全部转成统一 RPC/store/domain 错误
   - 要么 caller 侧至少统一 `errors.As(..., *CapabilityError)` 分支
3. 清理高风险吞错点，优先处理 lifecycle/shutdown/recovery/rollback/process signal 相关行。
4. 把 `Session.Close(ctx)` 真正做成可取消，并统一在 session manager / thread service 上套 `WithSessionCloseTimeout(...)`。
5. 对 direct bus subscriber 做收口：要么全面切到 `ResilientSubscribe`，要么在订阅层统一 recover policy。
