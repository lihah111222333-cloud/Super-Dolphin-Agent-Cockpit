# 第 08 轮审查结论

## 审查范围

- `internal/platform/mcpcontrol/sweeper.go`（lease sweep loop、stale 标记、过期 evict）
- `internal/platform/mcpcontrol/sweeper_runner.go`（Sweeper 包成 platformrunner.Runner）
- `internal/platform/mcpcontrol/handlers.go`（jrpc2 method handlers：register/heartbeat/context/event/log/approval/hook/report）
- `internal/platform/mcpcontrol/router.go`（NotifyConfigChanged / NotifyBySelector / Hook callback fanout）
- `internal/platform/mcpcontrol/resolution.go`（lease 查找、context payload 构建、FindActiveByKind/FindActiveForScope）
- `internal/platform/bus/subscription.go`（Subscription 批量 cancel 容器）

> 与第 01-07 轮已覆盖的 `mcpcontrol/{registry,peers}.go`、`bus/{resilient,sink}.go` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `sweeper_runner.go:31-38` `Run` | 兜底 | `r == nil \|\| r.sweeper == nil` 时 `<-ctx.Done(); return ctx.Err()` | nil receiver 是装配 bug；当成"等待 ctx done"会让 fx 装配错误被 RunGroup 当成正常 idle runner | nil receiver / nil sweeper 必须 panic |
| `sweeper.go:75-79` `Sweeper.Run` | 兜底 | `s == nil \|\| s.registry == nil` 直接 return | nil sweeper / 缺失 registry 是装配 bug | panic |
| `sweeper.go:92-95` `Sweep` | 兜底 | nil 同上，return SweepResult{} | 同上 | panic |
| `sweeper.go:123-129` `Sweep` evict 后 | 静默 | `_ = s.registry.disconnectLease(...)` 显式忽略 disconnect 错误 | sweeper 路径里 evict 失败完全无日志；与 round-07 OnDisconnect/Heartbeat 同根 | 至少 Warn；最好 metrics counter |
| `sweeper.go:75-90` `Run` 主循环 | 静默 | timer 唯一退出条件是 `ctx.Done()`；Sweep 内部如果 panic 没有 recover | sweeper goroutine 由 SweeperRunner 调用，Run 内 panic 会向上冒到 RunGroup 的 runOne，被 round-03 提到的 recover 兜底——但 sweep 单次 panic 后整个 sweeper 死掉，registry 永远不再扫描 | Sweep 单次调用应在 defer recover 内，让单次 sweep 失败不杀掉 loop |
| `sweeper.go:107-119` 时间比较 | 弱契约 | `instance.LastHeartbeat.Add(s.timeout).Before(now)` | LastHeartbeat 是 `time.Time` 零值时（构造异常）`Add(timeout).Before(now)` 必为真，所有 instance 立刻被标 stale | 显式校验 `!LastHeartbeat.IsZero()`；零值视为构造未完成的 bug |
| `sweeper.go:195-200` `nextInterval` | 弱契约 | `s.jitter <= 0` 时返回 tick；正值用 `rand.Int63n(int64(s.jitter))` | jitter 是 int64 不可能负，但若构造时 `tick == 0` 则 timer 会以 0 间隔无限循环 | 校验 tick > 0；构造期 |
| `handlers.go:57-79` `NewHandlers` | 兜底 | 所有 sink/provider 都用 `defaultXxx` 兜底；`p.Registry` 不做 nil 校验 | Registry 为 nil 时所有 handler 都会在 RPC 调用时 panic（registry.Register 等），生产事故只能从 stack 反查 | 装配期校验 Registry/Logger 必填；让 default* 只用于真正可选依赖 |
| `handlers.go:65-67` defaultEventSink | 兜底 | dispatcher/logger 都可能 nil；`HandleEvent` 内做了 nil-guard | 与 round-07 LogSink 同问题 | dispatcher/logger 应在装配时强制 |
| `handlers.go:230-246` `HandleEvent` | 兜底 | `dispatcher == nil` 时不 publish；`logger == nil` 时不 log；最后 `return nil` | event 静悄悄丢失，control plane 误以为事件已记录 | dispatcher/logger 任一缺失应 return error |
| `handlers.go:253-263` `HandleLog` | 兜底 | `logger == nil` 时 return nil；事件被吞 | 同上 | 同上 |
| `handlers.go:127-172` `requestApproval` | 静默 | `payload := decodePayloadMap(req.Payload)` 错误处理依赖 helper（看不到这里）；TimeoutMs<=0 走默认 ctx 不超时 | TimeoutMs 缺失时审批可能无限等待，阻塞调用栈 | 强制 TimeoutMs 必填；或显式默认 timeout 而非裸 ctx |
| `handlers.go:174-181` `approvalDecisionSource` | 兜底 | switch 中只有 `auto_approved`，default 全部归为 `DecisionSourceUI` | 任何不是 auto_approved 的 reason（包括 timeout、denial）都被打成 UI 来源，metrics 失真 | 显式枚举所有 reason；未知 reason 报 error 或 unknown |
| `handlers.go:183-212` `lookupAgentSnapshot` | 兜底 | agentID 为空返回 (nil, nil)；`p.agents == nil` 返 errInvalidParams；`err != nil \|\| snapshot == nil` 都归并为 errInvalidParams | DB IO 错误与 not found 不可区分；调用方拿到 invalid_params 但实际是后端故障 | 区分 not found 与 lookup error |
| `handlers.go:214-223` `resolveAgentContextSource` | 兜底 | `orchestration.(AgentContextSource)` 断言失败时静默返回 nil | 装配 bug 被吞掉；后续 lookupAgentSnapshot 的 nil-guard 会把所有 context 请求当成 invalid_params | 断言失败应 log + panic 或 error |
| `handlers.go:265-276` `controlLogLevel` | 兜底 | 未知 level 默认 Info | 用户配置 `level=critical` 拿到 Info；告警链路失效 | 未知 level 至少 Warn 提示；或返回 error |
| `handlers.go:295-308` `controlLogArgs` `Fields` 排序 | 静默 | `req.Fields` map 中 key 为空就 skip | 调用方传错时不告知；fields 漏掉但日志正常输出 | 至少 debug log 一条 |
| `router.go:30-32` `NotifyBySelector` | 静默 | `r.IntersectTargets(sel)` 空集时 `r.notifyTargets` 静默成功；topic 已校验，但 selector 空也无任何提示 | 配置错误的 selector 会让通知静悄悄无目标，运维不知 | 空目标至少 debug log |
| `router.go:58-66` `callbackHookTopic` | 静默 | topic 校验完后 fanout；fanout 内部错误依赖 `callbackTargets` 处理 | hook callback 如果所有 peer 都失败，错误会被合并 | 此函数本身合理；但 round-02 里发现 hook handler nil 默认值不一致的问题再现：control-plane 端 callback 全失败应 metrics 标识 |
| `router.go:68-91` `callbackHookDecision` | 兜底 | `instance.Peer == nil` 返 errPeerUnavailable；invokeFanoutTarget 失败时返回 zero decision + err | zero `T` decision 会被调用方用 `decision.Approved` 等访问；这是 Go 默认零值，但调用方很难知道是"明确拒绝"还是"调用失败" | 调用方应只在 err==nil 时使用 decision；建议在文档/lint 层强约束 |
| `resolution.go:21-57` `contextPayload` switch | 弱契约 | 4 个合法 scope；default 返回 errScopeNotAllowed | 校验 OK，但函数签名是 `(map, error)`，错误信息只用 sentinel error，调用方不易区分非法 scope vs map 构造失败 | error message 已带 scope，OK |
| `resolution.go:59-64` `contextAgentFields` | 兜底 | `snapshot == nil` 时全部用 instance 字段；非 nil 时全部用 snapshot | snapshot 部分字段为空（如 ThreadID）时 instance 的 ThreadID 被忽略，可能让 context 出现"snapshot 部分覆盖"陷阱 | 字段级 fallback：snapshot.ThreadID 为空时 fallback 到 instance.ThreadID |
| `resolution.go:79-110` `FindActiveForScope` | 兜底 | scope.AgentID+ThreadID 命中失败时 relax 到 only AgentID；再失败时 relax 到 shared service | 三层 fallback 让"我的 agent 没有自己的 peer"被透明替换为"其他 agent 的 shared peer"；隔离边界模糊 | 必须显式 fallback 决策日志，让调用方可见；建议 fallback 标志 |
| `bus/subscription.go:12-21` `Subscription` | 弱契约 | `Add(cancel)` 不校验 nil；`CancelAll` 直接遍历调用 | nil cancel 会 panic | 入参 nil 跳过或 panic（见后续讨论） |
| `bus/subscription.go:16-21` `CancelAll` | 静默 | 任一 cancel 内部 panic 会让后续 cancel 不被调用 | 单个 subscriber cancel panic 会污染整个清理路径 | 用 defer recover 包每次 cancel；或日志记录 panic |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `sweeper.go:125-128` | sweeper evict 后 disconnectLease 错误吞 |
| `sweeper.go:75-90` | Run 内单次 Sweep panic 杀整个 loop |
| `handlers.go:230-246` | HandleEvent dispatcher/logger nil 时 return nil |
| `handlers.go:253-263` | HandleLog logger nil 时 return nil |
| `handlers.go:174-181` | approvalDecisionSource 未识别 reason 全归 UI |
| `handlers.go:265-276` | controlLogLevel 未知 level 默认 Info |
| `handlers.go:298-303` | controlLogArgs 空 key fields skip |
| `handlers.go:204-211` | lookupAgentSnapshot 错误归并 invalid_params |
| `handlers.go:218-222` | resolveAgentContextSource 类型断言失败静默 nil |
| `router.go:30-32` | NotifyBySelector 空目标静默 |
| `bus/subscription.go:16-21` | CancelAll 单个 cancel panic 中断 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `sweeper_runner.go:24-26` `NewSweeperRunner` | sweeper=nil 不校验 |
| `sweeper.go:57-73` `NewSweeper` / `NewSweeperWithOptions` | registry=nil 不校验；options 全字段零值兜底 |
| `sweeper.go:140-153` `newSweepTarget` | instance=nil 时返回 partial target，pid/binary 等都是零值 |
| `sweeper.go:195-200` `nextInterval` | tick<=0 不校验 |
| `handlers.go:57-79` `NewHandlers` | Registry/Approvals/HookManager/Bridge 都不做 nil 校验 |
| `handlers.go:127-172` `requestApproval` | TimeoutMs <= 0 走 ctx 默认（可能无超时） |
| `handlers.go:265-276` `controlLogLevel` | 未知 level 默认 Info |
| `router.go:12-28` `NotifyConfigChanged` | scope 为 nil 时跳过设置 sel.Scope；topic 已校验 |
| `resolution.go:13-19` `resolveRegisteredInstance` | thin wrapper；registry/key 不做 nil 校验（依赖 lookupLease） |
| `resolution.go:79-110` `FindActiveForScope` | 三层 fallback 静默；调用方不知用了哪层 |
| `bus/subscription.go:12-15` `Add` | nil cancel 不校验 |

## 修复优先级

### P0（必须本周修）
1. `sweeper_runner.go:31-38`、`sweeper.go:75-95` nil receiver/registry/sweeper 改 panic（装配错误必须暴露）
2. `sweeper.go:75-90` Run 主循环包 defer recover，避免单次 Sweep panic 杀掉整个清理 loop
3. `handlers.go:230-263` HandleEvent/HandleLog 在 dispatcher/logger 缺失时返回 error，不要静默吞事件
4. `sweeper.go:107-119` LastHeartbeat 零值场景显式校验，不应自动判 stale
5. `handlers.go:218-222` resolveAgentContextSource 类型断言失败应 panic 或显式 error
6. `bus/subscription.go:16-21` CancelAll 用 defer recover 包每次 cancel，避免单个 panic 中断清理

### P1（本月）
7. `sweeper.go:125-129` evict 后 disconnectLease 错误改为 errors.Join + log（与 round-07 P1 协同）
8. `handlers.go:204-211` lookupAgentSnapshot 区分 not found / lookup error
9. `handlers.go:174-181` approvalDecisionSource 显式枚举所有 reason；未知 reason 走 unknown
10. `handlers.go:127-172` requestApproval TimeoutMs<=0 走 platformconfig 默认 timeout
11. `handlers.go:265-276` controlLogLevel 未知 level 至少 Warn
12. `resolution.go:79-110` FindActiveForScope 三层 fallback 加决策日志或返回 fallback 标志
13. `handlers.go:57-79` NewHandlers 校验 Registry/Logger 等核心依赖

### P2（下个 sprint）
14. `resolution.go:59-64` contextAgentFields 字段级 fallback
15. `bus/subscription.go:12-15` Add 入参 nil 校验或显式忽略 + log
16. `router.go:30-32` NotifyBySelector 空目标 debug log
17. `sweeper.go:140-153` newSweepTarget instance=nil 时 panic（构造期 bug）
18. `sweeper_runner.go:24-26` / `sweeper.go:57-73` 构造期 nil 校验

## 边界条件

1. **`sweeper_runner.go` 的 nil 兜底是为了让 fx 装配未注入 sweeper 时仍能"等 ctx 取消"**：本意是让单元测试更容易构造空 fx app。但生产路径里 sweeper 缺失就是装配 bug，应区分。修复时让生产路径强制非 nil；测试用 fixture 注入 noop sweeper。
2. **sweeper Sweep panic recovery**：本轮列了 P0，但要注意 sweeper.Run 的实际错误已经从 `Sweep` 转为 `result.Staled/Evicted` 计数；panic 通常发生在 `evictLocked`、`disconnectLease` 等 registry 操作里。修 recover 时要确保 panic 后下一轮 tick 仍能跑（不要把 timer 一起灭了）。
3. **`handlers.go` 的 default* 是为了减少 fx Provide 列表**：让框架装配可以不显式注入这些 sink。改为强制依赖会扩大 wiring 改动面，建议保留 default* 但在 default* 内部对 dispatcher/logger 做 fail-fast 校验（构造期 panic）。
4. **`approvalDecisionSource` 把 `auto_approved` 之外都归 UI**：当前可能有多个真实 reason 也走 UI 路径（如默认 fallback 决策）。改 unknown 前要先 grep 一下生成 reason 的位置，确认所有合法值。
5. **`FindActiveForScope` 的三层 fallback**：这是为多 agent 共用 shared peer 设计的；ThreadID-bound peer 不可用时降级到 AgentID-bound、再降级到 shared service。完全删掉会破坏共享 peer 模式。修复方向是"加日志"而非"删 fallback"。
6. **`LastHeartbeat.IsZero()` 校验**：可能影响 Register 后的窗口期——instance 刚 register 时如果 LastHeartbeat 还没初始化就被 sweep 扫到，会被 IsZero 误判。检查 Register 路径（round-07 已确认 Register 时设置 `LastHeartbeat: now`，不会有零值）。
7. **`bus/subscription.CancelAll` 加 recover 的副作用**：`event.Subscribe` 返回的 cancel 是 idempotent，理论不该 panic。加 recover 是防御 future 注册的非标准 cancel；权衡日志噪声 vs 防护代价。
8. **`sweeper.go` rand.Int63n**：用了全局 rand 而非 per-sweeper rand。多 sweeper 并发时 jitter 是同一个 PRNG，会有同步触发问题。本轮没列为高危但可作为长尾改进。

---

下一轮范围建议：
- `internal/platform/mcpcontrol/handlers_hooks.go` + `release_scope.go` + `report_handlers.go`
- `internal/platform/mcpcontrol/factory.go` + `module.go` + `subscribers.go`
- `internal/platform/mcpcontrol/scope.go` + `registry_helpers.go` + `registry_support.go`
- `internal/platform/mcpcontrol/fanout.go` + `config_change.go` + `config_fanout_worker.go`
