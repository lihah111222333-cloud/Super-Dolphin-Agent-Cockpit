# 第 10 轮审查结论

## 审查范围

- `internal/platform/mcpcontrol/config_fanout_worker.go`（config 变更 fanout 队列、worker 生命周期、dispatch、drain）
- `internal/platform/mcpcontrol/factory.go`（hook 校验、resolveServerPeer、withResolvedInstance、handleHookRPC、disconnectLease、fanoutTargets、invokeFanoutTarget、mapHookHandlerError）
- `internal/platform/mcpcontrol/scope.go`（ToolScope、normalizeScopeCWD）
- `internal/platform/mcpcontrol/errors.go`（jrpc2 错误码 helper 集合）
- `internal/platform/mcpcontrol/registry_helpers.go`（configVersion 管理、shutdownHooks、cleanupLease、lookupInstance）

> 与第 07-09 轮已覆盖的 `mcpcontrol/{registry,peers,sweeper,handlers,router,resolution,release_scope,fanout,config_change,report_handlers,handlers_hooks}` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `config_fanout_worker.go:96-114` `Start` | 兜底+静默 | `w == nil` 直接 return；`notifier == nil \|\| versions == nil` 时 close(doneCh) 让 Stop 成 noop | nil worker 是装配 bug；nil notifier/versions 让整个 config 变更通知链路静悄悄失效 | nil worker panic；nil notifier/versions 在 `newConfigFanoutWorker` 构造期 panic |
| `config_fanout_worker.go:105-113` Start goroutine | 静默 | `defer recover()` 后仅 `pkglogger.Error`，不 close(doneCh) | panic 后 doneCh 永远不 close；Stop 会永远阻塞在 `<-w.doneCh` 直到 drain grace timeout | recover 后必须 `close(w.doneCh)` 再 return |
| `config_fanout_worker.go:119-139` `Enqueue` | 兜底 | `w == nil` 直接 return；topic 为空直接 return；post-Stop 直接 return | nil receiver 是 bug；topic 为空是调用方 bug；post-Stop 丢弃是设计但无 metrics | nil receiver panic；topic 为空 panic 或 error；post-Stop 丢弃加 metrics counter |
| `config_fanout_worker.go:144-149` `FanoutCtx` | 兜底 | `w == nil` 返回 `context.Background()` | nil receiver 是 bug | panic |
| `config_fanout_worker.go:153-178` `Stop` | 兜底 | `w == nil` 返回 nil；`ctx == nil` 兜底 Background | nil receiver 是 bug；nil ctx 是调用方 bug | nil receiver panic；nil ctx panic |
| `config_fanout_worker.go:220-242` `dispatch` | 静默 | marshal 失败仅 Warn 后 return（不 processedTotal.Add）；NotifyConfigChanged 失败仅 Warn；DispatchLSPReleaseScope 失败仅 Warn | 三处错误全部被吞；config 变更通知失败 peer 不知道配置变了 | marshal 失败应 metrics + 标记 request 为 failed；Notify 失败应 metrics；LSP release 失败应 metrics |
| `config_fanout_worker.go:76-91` `newConfigFanoutWorker` | 弱契约 | notifier/versions 不做 nil 校验；logger nil 兜底全局 | 构造后 Start 才发现 nil，此时已经 close(doneCh) 让整个 worker 成 noop | 构造期校验 |
| `factory.go:83-97` `resolveServerPeer` | 静默 | `defer recover()` 后把 panic 转成 errPeerUnavailable | jrpc2.ServerFromContext panic 是 jrpc2 库 bug 或 ctx 被篡改；当作 peer 不可用会让调用方重试 | recover 后应 fatal log + metrics；errPeerUnavailable 不够精确 |
| `factory.go:99-111` `withResolvedInstance` | 弱契约 | registry 为 nil 时 resolveRegisteredInstance 内部报 errLeaseNotFound | nil registry 是装配 bug，不是 lease 找不到 | 入口显式 `if registry == nil { panic }` |
| `factory.go:174-185` `disconnectLease` | 兜底 | `key == (LeaseKey{})` 时跳过 cleanup 但仍 closePeer | 零值 key 是调用方 bug；closePeer 可能关闭一个不该关的 peer | 零值 key 应 panic 或 error |
| `factory.go:216-246` `fanoutTargets` | 兜底 | `len(targets) == 0` 时 return nil | 与 round-09 IntersectTargets 空目标同根；这里 return nil 是合理的（无目标无需 fanout），但调用方无法区分"无目标"与"全部成功" | 至少 debug log |
| `factory.go:248-275` `runFanoutWorker` | 静默 | 外层 `defer recover()` 后仅 log，不把 panic 写入 errs channel | 如果 worker goroutine 自身 panic（非 target 级），errs channel 少一个写入，`fanoutTargets` 的 `for range targets` 会永远阻塞 | 外层 recover 后必须把剩余 jobs 全部 drain 并写 nil/err 到 errs |
| `factory.go:277-295` `invokeFanoutTarget` | 静默 | failure 路径 `_ = r.disconnectLease(...)` 显式忽略 | 与 round-07/08/09 同根 | errors.Join |
| `factory.go:318-338` `mapHookHandlerError` | 兜底 | StoreError → errInternal（丢失原始错误）；default → errInternal（丢失原始错误） | 调用方只看到 "hook subscribe failed"，无法定位是 DB 超时还是逻辑错误 | 用 %w 保留原 err |
| `scope.go:30-39` `normalizeScopeCWD` | 静默 | 非绝对路径返回 "" | 与 round-02 `mcpserver/common/scope.go:129-141` 完全相同的实现（重复代码）；非法路径静默丢弃 | 统一到一处；非法路径返回 error |
| `registry_helpers.go:13-20` `setHookLifecycle` | 兜底 | `r == nil` 直接 return | nil receiver 是 bug | panic |
| `registry_helpers.go:22-27` `currentConfigVersionLocked` | 兜底 | `r == nil \|\| configVersion < 1` 返回 1 | nil receiver 是 bug；configVersion < 1 是数据损坏 | nil panic；configVersion < 1 应 panic 或 error |
| `registry_helpers.go:29-46` `advanceConfigVersion` | 兜底 | `r == nil` 返回 1 | 同上 | panic |
| `registry_helpers.go:48-65` `shutdownHooks` | 兜底 | `r == nil` / `key == zero` / `ctx == nil` / `hookLifecycle == nil` 全部静默 return nil | 多重兜底掩盖装配/调用方 bug | nil receiver panic；zero key error；ctx nil panic；hookLifecycle nil 是合法（未配置 hook） |
| `registry_helpers.go:67-71` `cleanupLease` | 静默 | shutdownHooks 错误仅 Warn | 与 round-07/08/09 同根 | 返回 error 让调用方决定 |
| `registry_helpers.go:117-127` `lookupInstance` | 兜底 | lookupLease 错误转 `(nil, false)` | 调用方无法区分"不存在"与"查找失败" | 返回 (instance, error) |
| `errors.go:10-12` `newMCPError` | 弱契约 | `fmt.Sprintf(format, args...)` 后再 `jrpc2.Errorf(..., "%s", ...)` | 双层 Sprintf 是为了避免 jrpc2.Errorf 的 format 解析；但 args 中含 `%` 时第一层 Sprintf 会误解析 | 用 `jrpc2.NewError(code, message)` 替代 Errorf |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `config_fanout_worker.go:105-113` | Start goroutine panic 后 doneCh 不 close |
| `config_fanout_worker.go:119-139` | Enqueue post-Stop 丢弃无 metrics |
| `config_fanout_worker.go:220-242` | dispatch 三处错误全部 Warn 后吞 |
| `factory.go:83-97` | resolveServerPeer panic 转 errPeerUnavailable |
| `factory.go:248-275` | runFanoutWorker 外层 panic 不写 errs channel |
| `factory.go:277-295` | invokeFanoutTarget disconnectLease 错误吞 |
| `factory.go:318-338` | mapHookHandlerError 丢失原始错误 |
| `registry_helpers.go:67-71` | cleanupLease shutdownHooks 错误仅 Warn |
| `registry_helpers.go:117-127` | lookupInstance 错误转 false |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `config_fanout_worker.go:76-91` | notifier/versions/logger 不做 nil 校验 |
| `config_fanout_worker.go:96-149` | 所有公开方法 nil receiver 兜底 |
| `factory.go:99-111` `withResolvedInstance` | registry nil 不校验 |
| `factory.go:113-124` `withCurrentRegisteredInstance` | 同上 |
| `factory.go:126-151` `handleHookRPC` | hookManager nil 返 errCapabilityMismatch（合理但不区分装配 bug vs 功能未启用） |
| `factory.go:153-172` `forEachInstanceBucket` | r/instance/fn nil 全部静默 return |
| `factory.go:174-185` `disconnectLease` | zero key 跳过 cleanup |
| `scope.go:20-28` `normalizeToolScope` | 仅 trim；CWD 非法路径静默丢弃 |
| `scope.go:30-39` `normalizeScopeCWD` | 与 mcpserver/common/scope.go 重复实现 |
| `registry_helpers.go:13-65` | 多处 nil receiver 兜底 |
| `registry_helpers.go:73-94` `activeLeaseKeys` | nil receiver 返回 nil |
| `registry_helpers.go:96-105` `shutdownActiveLeases` | nil receiver 返回 nil |
| `errors.go:10-12` | 双层 Sprintf 有 format 注入风险 |

## 修复优先级

### P0（必须本周修）
1. `config_fanout_worker.go:105-113` Start goroutine panic 后必须 `close(w.doneCh)`，否则 Stop 永远阻塞
2. `factory.go:248-275` runFanoutWorker 外层 panic 后必须 drain 剩余 jobs 并写 errs，否则 fanoutTargets 死锁
3. `config_fanout_worker.go:76-91` newConfigFanoutWorker 构造期校验 notifier/versions 非 nil
4. `config_fanout_worker.go:220-242` dispatch 三处错误加 metrics counter（至少 Notify 失败必须可观测）
5. `factory.go:83-97` resolveServerPeer panic 后加 fatal log + metrics，不能只转 errPeerUnavailable
6. `errors.go:10-12` 改用 `jrpc2.NewError(code, message)` 避免双层 Sprintf 的 format 注入

### P1（本月）
7. `factory.go:174-185` disconnectLease zero key 改 error/panic
8. `factory.go:318-338` mapHookHandlerError 用 %w 保留原 err
9. `factory.go:277-295` invokeFanoutTarget disconnectLease 错误 errors.Join（与 round-07/08/09 协同）
10. `registry_helpers.go:117-127` lookupInstance 改返回 (instance, error)
11. `registry_helpers.go:67-71` cleanupLease 返回 error
12. `scope.go:30-39` normalizeScopeCWD 与 mcpserver/common/scope.go 统一到一处
13. `config_fanout_worker.go:96-149` 所有公开方法 nil receiver 改 panic

### P2（下个 sprint）
14. `registry_helpers.go:13-65` 多处 nil receiver 兜底改 panic
15. `factory.go:153-172` forEachInstanceBucket nil 参数改 panic
16. `factory.go:99-124` withResolvedInstance/withCurrentRegisteredInstance registry nil 改 panic
17. `config_fanout_worker.go:119-139` Enqueue post-Stop 丢弃加 metrics
18. `config_fanout_worker.go:144-149` FanoutCtx nil receiver 改 panic

## 边界条件

1. **`config_fanout_worker.Start` panic 后 doneCh 不 close 是真实死锁风险**：如果 `runWorker` 内部 panic（如 notifier.NotifyConfigChanged panic），当前 recover 只 log 不 close doneCh。Stop 的 `select { case <-w.doneCh: ... }` 会走 timeout 分支，但 timeout 是 10s——这 10s 内整个 fx shutdown 被阻塞。修复时 recover 内 `defer close(w.doneCh)` 放在 goroutine 最外层。
2. **`runFanoutWorker` 外层 panic 死锁**：当前 `fanoutTargets` 用 `for range targets { joined = errors.Join(joined, <-errs) }` 等待所有 target 的结果。如果 worker goroutine 自身 panic（不是 target 级 panic），errs 少写入，主循环永远阻塞。修复方案：外层 recover 后把 `jobs` channel 中剩余 job 全部 drain 并写 nil 到 errs。
3. **`errors.go` 双层 Sprintf 的 format 注入**：如果 `args` 中包含用户可控字符串（如 tool name 含 `%s`），第一层 Sprintf 会误解析。当前所有调用点的 args 都是 server-side 字符串（instance_id、generation 等），实际风险低。但改为 `jrpc2.NewError` 是更安全的做法。
4. **`normalizeScopeCWD` 重复实现**：`mcpcontrol/scope.go:30-39` 与 `mcpserver/common/scope.go:129-141` 逻辑相同但包不同。统一时要注意 `mcpserver/common` 的版本还处理了 Windows POSIX 路径（`isSlashRootedPOSIXPath`），而 `mcpcontrol` 版本没有。统一应以 common 版本为准。
5. **`config_fanout_worker.dispatch` 三处 Warn 是有意设计**：config 变更通知是 best-effort（peer 可能暂时不可达），所以 Warn + 继续是合理的。但"完全无 metrics"让运维无法感知通知失败率。修复方向是加 metrics，不是改 error 传播。
6. **`mapHookHandlerError` 丢失原始错误是为了安全**：不把 DB 错误细节暴露给 MCP 客户端。改 %w 后要确认 jrpc2 的 error 序列化不会把 wrapped error 的 message 全部暴露给客户端。建议：内部 log 保留原 err，对外仍返回 generic message。
7. **`registry_helpers.go` 的 nil receiver 兜底是为了让 fx 装配可选**：某些测试/standalone 模式下 ToolRegistry 可能为 nil。改 panic 前要确认所有调用路径都保证 registry 非 nil。建议：生产路径 panic；测试用 fixture 注入 noop registry。
8. **`factory.go:216-246` fanoutTargets 空目标 return nil**：这是合理的——没有目标就没有错误。但调用方（如 NotifyBySubscription）可能想知道"是否有人收到了通知"。建议返回 `(int, error)` 表示成功通知的 target 数。

---

下一轮范围建议：
- `internal/platform/mcpcontrol/module.go` + `subscribers.go` + `runner_provider.go` + `registry_support.go`
- 或切换到新目录：`internal/module/turn/`（turn 级 tool result budget、storage）
- 或 `cmd/mcp-lsp/middleware/`（budget_hints、timeout、recovery）
