# 第 29 轮审查结论

## 审查范围

- `cmd/mcp-orch/hook_subscription.go`（subscribeOrchestrationHooks）
- `cmd/mcp-orch/notify/turn.go`（TurnNotifier.OnTurnCompleted/Interrupted、OnThreadStopped、lookupAlias、enqueue）
- `cmd/mcp-orch/notify/resolve.go`（resolveNodeAlias、extractNotifyChannel、isTerminalNodeStatus）
- `cmd/mcp-orch/notify/module.go`（provideOrchResolver、provideOrchWebhookClient、provideOrchNotifier、provideOrchFlusher、registerDAGSubscriberLifecycle）
- `cmd/mcp-orch/orchestration/sharedfile_adapter.go`（ReadSharedFile、WriteSharedFile）
- `cmd/mcp-orch/orchestration/nodeexec/stubs.go`（HybridExecutor stub）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `hook_subscription.go:27-29` | 静默 | `client == nil` 时 `return nil` | 钩子订阅是 agent 生命周期感知核心；client 未注入时整个编排器对 agent 状态盲视，但不报错 | Fail-Fast: `return errors.New("hook subscriber required")` |
| `hook_subscription.go:36-38` | 静默 | 所有 topic trim 后为空时 `return nil` | 不会发生（topics 是常量），但若未来改为配置注入，空配置将静默禁用所有订阅 | 至少 Warn 日志 |
| `turn.go:127-131` enqueue | 静默 | `t.notifier == nil` 时只增计数器 + return，无日志 | 通知器未注入是配置错误，应在 NewTurnNotifier 构造期就 fail，而不是运行时静默吞 | 在 `NewTurnNotifier` 拒绝 `notifier == nil` |
| `turn.go:132-141` enqueue | 静默 | `TryEnqueue` 失败仅 Warn 日志 + 计数器，错误不上抛 | 注释明确「never bubbled to the hook consumer」——这是设计选择，但意味着系统性通知故障无法触发熔断或告警 | 加连续错误率监控，超阈值时上抛或主动告警 |
| `turn.go:64-67, 79-82, 100-103` | 弱契约 | alias 为空时静默 skipped++ + return | 整个通知 pipeline 可被静默丢弃。注释说是「P2 plan-compliant drop」 | 可观测性补全：定期 Info 日志「skipped count = N」让运维可见 |
| `resolve.go:65-83` extractNotifyChannel | 静默 | JSON unmarshal 失败 / 类型断言失败均 `return ""` | 用户配置 notify_channel 类型错（数字、对象）会被静默丢弃，配置错误无感知 | 解析失败应记录字段名 + Warn 日志 |
| `module.go:70-75` provideOrchResolver | 兜底 | `cfg == nil` 时 parse 空字符串 | 配置未加载是严重问题，但被降级为「无 channel」静默禁用 notify | Fail-Fast: `if cfg == nil { return nil, errors.New("notify config required") }` |
| `module.go:88-97` provideOrchNotifier | 兜底 | logger nil → fallback；capacity ≤ 0 → 默认值 | 弱兜底但合理；construction-time 不应依赖默认值，但 fx 链可能传 nil | 可接受 |
| `sharedfile_adapter.go:59-61` ReadSharedFile | 弱契约 | `file == nil && err == nil` 时 return `("", false, nil)` | 这假设 store.Get 在 not-found 之外不会返回 nil；若 store 有 bug（nil file + nil err），会被静默映射为不存在 | Fail-Fast: `panic("store contract violation: nil file with nil err")` 或 return error |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `turn.go:127-144` enqueue | TurnNotifier.enqueue 在 hook consumer 同步路径，TryEnqueue 阻塞会传染到 agent 主循环 | 1) 在 enqueue 入口/出口加 `start := time.Now()`，duration > 100ms 打 Warn；2) 加 metrics 字段 `enqueueDurationP99` |
| `turn.go:62-115` On* 三个 callback | 每个 hook event 走 alias 解析 + JSON 构建 + enqueue 同步串联 | 在 OnTurnCompleted/Interrupted/ThreadStopped 入口打 trace 日志带 turn_id，全链路 trace 关联 |
| `module.go:139-152` registerDAGSubscriberLifecycle | OnStart/OnStop 钩子内 cancel 闭包是非阻塞的，但 Subscribe 内的 worker 可能阻塞 | 让 DAGNotifier.Run 暴露 `LastEventProcessedAt` 给 healthz endpoint |
| `sharedfile_adapter.go:48-63` ReadSharedFile | DB 抖动时 store.Get 可能阻塞；nodeexec inputs 装载阶段会被卡住 | 调用方 nodeexec.loadFromSharedfiles 应传带 timeout 的 ctx；adapter 不强依赖但应在 P99 慢调用时打 Warn |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `hook_subscription.go:27-29` | client == nil 时 return nil（无日志） |
| `hook_subscription.go:36-38` | topics 全空时 return nil（无日志） |
| `turn.go:121-125` lookupAlias | t == nil 或 resolver == nil 时返回 "" 静默 |
| `turn.go:127-130` enqueue | notifier == nil 时只计数不上抛 |
| `turn.go:132-141` enqueue | TryEnqueue 错误只 Warn 日志，不上抛 hook consumer |
| `resolve.go:70-72` extractNotifyChannel | JSON unmarshal 失败静默 return "" |
| `resolve.go:77-79` extractNotifyChannel | 类型断言失败静默 continue |
| `module.go:70-74` provideOrchResolver | cfg == nil 时 parse 空字符串（disabled notify） |
| `sharedfile_adapter.go:59-61` | store.Get 返回 nil file + nil err 时静默映射为「不存在」 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `hook_subscription.go:27` | client == nil 不报错 |
| `turn.go:51-58` NewTurnNotifier | 接受 nil notifier；构造期不校验 |
| `turn.go:127` enqueue | 错误不上抛，调用方无法熔断 |
| `module.go:70-75` provideOrchResolver | cfg nil → 空 resolver（feature 静默禁用） |
| `module.go:110-120` provideOrchFlusher | logger nil / drain ≤ 0 → 默认值兜底 |
| `sharedfile_adapter.go:48-63` | nil store.Get + nil err 隐式约定 |

## 修复优先级

### P0（必须本周修）
1. **`hook_subscription.go:27-29` client nil 静默**——hook 订阅是 agent 生命周期感知核心。client 未注入时整个 mcp-orch 失去对 agent 状态的感知，但不报错。这是严重的「假装在工作」反模式。改为 fail-fast。
2. **`turn.go:127-131` notifier nil 静默**——构造期就该拒绝 nil notifier，运行时静默吞是 anti-pattern。

### P1（本月）
3. `module.go:70-75` provideOrchResolver cfg nil 改为 fail-fast
4. `resolve.go:70-72, 77-79` JSON 解析失败加 Warn 日志（带字段路径）
5. `turn.go:132-141` enqueue 错误率监控 + 超阈值上抛

### P2（下个 sprint）
6. `turn.go` 全文加 hook duration 监控（P99 > 100ms 打 Warn）
7. `sharedfile_adapter.go:59-61` store contract 加显式断言或文档化

## 边界条件

1. **TurnNotifier 同步 hook consumer 设计取舍**：`enqueue` 在 hook consumer 同步路径上跑（line 73、90、110）。如果 `notifier.TryEnqueue` 用 try-send 语义（队列满直接 drop），则不会阻塞。但若 TryEnqueue 内部有 mutex 或 chan send，hook 主循环会被传染。需要确认 `notifyplatform.Notifier.TryEnqueue` 实现——本轮未读到该文件，建议下轮覆盖。
2. **Hook 订阅 client nil 的实际触发条件**：从 fx 注入图看，`hookSubscriber` 应当总是非 nil（mcp 客户端必然存在）。但 nil-check 仍存在说明代码作者考虑了「测试模式不注入 client」的场景。这是合理的弱契约——但应在生产 fx 模块中加 invariant 检查。
3. **resolveNodeAlias 的精确语义**：`extractNotifyChannel` 大小写不敏感（`strings.EqualFold`）+ 容忍前后空格——注释说「user-authored in practice」。这是对错误配置的容忍，可能掩盖排错难度。建议解析后打 Debug 日志「resolved alias=X from node.config」，便于运维排错。
4. **HybridExecutor stub 风险**：line 17-19 直接返回 `Status: NodeStatusDone`，没有真正执行任何逻辑。如果生产环境创建 `node_type=hybrid` 节点，会被错误标记为 done。应在 stub 内 panic 或 return error 阻止误用，等 F3.1 真实实现落地。
5. **sharedfile_adapter ReadSharedFile 的三态契约**：`(content, exists, err)` 设计是对 nodeexec.inputs.go validation 友好的——not-found 不视为 transient 错误。但 line 59-61 的 `file == nil && err == nil` 兜底依赖 store.Get 的隐式约定。建议在 store.Get 文档（contract.go）显式声明「成功时 file 必非 nil」，让 adapter 可以删除该兜底。
6. **Notify 系统整体「设计性静默」哲学**：从 `turn.go` 注释「never bubbled to the hook consumer」、`resolve.go` 「empty string means drop」、`hook_subscription.go` nil-client return nil 模式可见，notify 子系统刻意采取「fail-soft」姿态——这是合理的设计（通知不应阻塞业务），但与项目全局的 fail-fast 要求形成张力。建议补充：每 5 分钟 Info 日志「notify metrics: skipped=X, enqueued=Y, errors=Z」让 fail-soft 有 fail-loud 的可观测性。

---

**本轮总结**：发现 2 个 P0 静默问题（hook 订阅 nil-client、TurnNotifier nil-notifier）。整个 notify 子系统采取「设计性 fail-soft」——这是合理选择但与全局 fail-fast 形成张力，建议加可观测性指标（每 5min 输出 metrics）让 fail-soft 不变成 silent-fail。Hook 订阅 client nil 是真正的反模式：「假装在工作」。

**累计进度**：29 轮完成。cron `fd4b4728` 继续推进。
