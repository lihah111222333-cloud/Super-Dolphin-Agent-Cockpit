# 第 28 轮审查结论

## 审查范围

- `internal/sidecar/orch/notify/dispatch_retry_alert.go`（AlertDispatchRetry、findNode、getDAG、buildDispatchRetryAlertBody）
- `internal/sidecar/orch/orchestration/wakeup_reclaim.go`（WakeupReclaimer.Run、ReclaimOnce、ProvideWakeupReclaimerRunner）
- `internal/sidecar/lsp/multilsp/transport.go`（newTransport、request、dispatchMessage、spawnResponder、respondToServerRequest、handleNotification）
- `internal/sidecar/lsp/multilsp/transport_conn.go`（Close、drainResponders、readLoop、writeMessage、stopWithError）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `dispatch_retry_alert.go:33-34` | 静默 | `n == nil \|\| n.notifier == nil` 时 `return nil` | 通知器未注入时告警被静默吞掉，调用方无感知。配置错误永远不会暴露 | Fail-Fast: `return fmt.Errorf("dispatch retry alert: notifier not configured")` |
| `dispatch_retry_alert.go:37-39` | 弱契约 | `dagKey == "" \|\| nodeKey == ""` 时 `return nil` | 空参数静默丢弃告警。上游传空 key 是 bug，应该被暴露 | Fail-Fast: 返回 error 或至少 Warn 日志 |
| `dispatch_retry_alert.go:64-76` findNode | 静默 | store.ListNodes 失败时 Debug 日志 + return nil | Debug 级别在生产环境不可见。store 故障导致告警降级为无 node 信息，但调用方不知道 | 改为 Warn 级别；或返回 error 让调用方决定是否继续 |
| `dispatch_retry_alert.go:84-96` getDAG | 静默 | store.GetDAG 失败时 Debug 日志 + return nil | 同上：store 故障被 Debug 日志吞掉 | 同上 |
| `transport.go:191` respondToServerRequest | 协程延迟 | `context.Background()` 无超时无取消 | 如果 requestHandler 阻塞，responder 协程永久挂起。drainResponders 2s 超时后放弃等待，协程泄漏 | 使用 `context.WithTimeout(ctx, defaultRequestTimeout)` |
| `wakeup_reclaim.go:95` Run 主循环 | 静默 | `_, _ = r.ReclaimOnce(ctx)` 错误和行数都丢弃 | 如果 ReclaimOnce 连续失败（DB 持续不可用），主循环无感知，无法触发告警或退避 | 加连续失败计数器；超阈值时 Warn + 指数退避 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `transport.go:191` respondToServerRequest | requestHandler 无超时，可能阻塞在外部 LSP 服务器 | 1) 加 `context.WithTimeout` 2) 在 handler 入口/出口打 `slog.Debug` 带 `duration` 字段 3) 超 5s 打 Warn |
| `wakeup_reclaim.go:95` ReclaimOnce | DB 查询可能因锁竞争延迟 | 在 ReclaimOnce 内加 `start := time.Now()` + 出口 `slog.Debug("reclaim_once_duration", "ms", time.Since(start).Milliseconds())` |
| `transport.go:145-159` spawnResponder | 协程数量无上限，高并发时可能 OOM | 加 semaphore（`make(chan struct{}, maxConcurrentResponders)`）限制并发 |
| `transport_conn.go:84-96` readLoop | readMessage 阻塞在 stdout.ReadString，进程卡死时无超时 | 已有 `done` channel 兜底（进程退出时 stdout EOF），但建议加 read deadline 或 health check ticker |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `dispatch_retry_alert.go:33-34` | notifier == nil 时静默 return nil |
| `dispatch_retry_alert.go:37-39` | dagKey/nodeKey 为空时静默 return nil |
| `dispatch_retry_alert.go:47-51` | alias 为空时 Debug 日志 + return nil（通知渠道未配置，告警丢失） |
| `dispatch_retry_alert.go:64-76` | store.ListNodes 失败 → Debug 日志 + return nil |
| `dispatch_retry_alert.go:84-96` | store.GetDAG 失败 → Debug 日志 + return nil |
| `transport.go:166` handleResponse | 孤儿响应（无匹配 pending）静默丢弃 |
| `transport.go:181-183` handleNotification | notificationHandler == nil 时通知静默丢弃 |
| `transport.go:185-188` | ErrUnsupportedNotification 静默吞掉，无日志 |
| `wakeup_reclaim.go:95` | ReclaimOnce 错误在循环层被 `_, _` 丢弃 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `dispatch_retry_alert.go:33-39` | AlertDispatchRetry 接受 nil notifier + 空参数不报错 |
| `dispatch_retry_alert.go:41-43` | ctx == nil fallback 到 Background（掩盖上游 nil-ctx bug） |
| `wakeup_reclaim.go:77-79, 103-105` | 同上 ctx == nil fallback |
| `transport.go:191` | respondToServerRequest 用 Background ctx，无超时契约 |
| `transport.go:145-159` spawnResponder | 无并发上限，无背压机制 |

## 修复优先级

### P0（必须本周修）
1. **`transport.go:191` respondToServerRequest 无超时** —— requestHandler 阻塞会导致协程泄漏，且 drainResponders 2s 超时后直接放弃，泄漏的协程持有 transport 引用阻止 GC。改为 `context.WithTimeout(context.Background(), defaultRequestTimeout)`。
2. **`dispatch_retry_alert.go:33-34` notifier nil 静默** —— 告警系统的核心路径不应静默失败。如果 notifier 未注入是合法状态（feature flag 关闭），至少打 Info 日志；如果是配置错误，应 return error。

### P1（本月）
3. `dispatch_retry_alert.go:64-76, 84-96` store 错误日志从 Debug 升级到 Warn
4. `wakeup_reclaim.go:95` 加连续失败计数 + 超阈值告警
5. `transport.go:145-159` spawnResponder 加并发上限 semaphore

### P2（下个 sprint）
6. `dispatch_retry_alert.go:37-39` 空参数改为 return error
7. 全局清理 `ctx == nil → context.Background()` fallback 模式（应在调用方保证 ctx 非 nil）

## 边界条件

1. **respondToServerRequest 的 Background ctx**：当前 spawnResponder 在 `t.closed.Load()` 时拒绝新 spawn，但已 spawn 的协程如果 handler 阻塞，drainResponders 2s 后放弃。这意味着 Close() 可能在协程仍在运行时返回。协程持有 `t` 引用，如果 `t` 被 GC 回收前协程仍在跑，会访问已关闭的 stdin/stdout。实际上 `writeMessage` 检查 `t.closed.Load()` 会 return ErrTransportClosed，所以不会写入已关闭的 pipe——但协程本身不会被回收直到 handler 返回。
2. **dispatch_retry_alert 的 nil-guard 设计意图**：从 `ProvideWakeupReclaimerRunner` 的模式看（store == nil 时返回 NoopRunner），项目有「optional dependency → graceful degrade」的设计哲学。`notifier == nil` 可能是有意的 feature-off 路径。但即使如此，应该有 Info 级别日志标记「告警被跳过」，否则运维无法区分「告警正常发送」和「告警被静默跳过」。
3. **wakeup_reclaim 的 tick 堆积**：Go 的 `time.Ticker` 在 receiver 未消费时不会堆积（channel buffer=1，多余 tick 被丢弃）。所以即使 ReclaimOnce 耗时超过 TickInterval，也不会堆积——但会导致实际 reclaim 频率低于配置频率，且无日志可观测。
4. **transport handleResponse 孤儿响应**：`removePending` 返回 nil 时静默丢弃是合理的——可能是超时后 ctx.Done 已经消费了 pending，响应晚到。但在调试协议问题时这个静默会增加排查难度。建议加 Debug 日志。
5. **spawnResponder 的 panic recovery**：line 153-155 recover 后只打 slog.Error，不 stopWithError。这是正确的——单个 handler panic 不应杀死整个 transport。但 panic 信息缺少 method/params 上下文，排查困难。

---

**本轮总结**：发现 2 个 P0 问题。`transport.go:191` 的无超时 Background ctx 是协程泄漏的直接风险点；`dispatch_retry_alert.go` 整个文件是静默失败的典型案例——告警系统本身不应该静默失败。wakeup_reclaim 的 tick 循环虽然不会堆积（Go Ticker 语义保证），但缺乏执行耗时可观测性，建议加 duration 日志辅助定位内部延迟。

**累计进度**：28 轮完成。cron `fd4b4728` 继续推进。
