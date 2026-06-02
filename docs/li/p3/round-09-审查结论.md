# 第 09 轮审查结论

## 审查范围

- `internal/platform/mcpcontrol/release_scope.go`（LSP scope 释放、target 选择、merge 结果、event payload 解析）
- `internal/platform/mcpcontrol/fanout.go`（IntersectTargets、selector 桶、notifyTargets、recoverWorkerPanic、notePeerFailure）
- `internal/platform/mcpcontrol/config_change.go`（agent/thread bus 事件 → config 变更通知 payload 构造）
- `internal/platform/mcpcontrol/report_handlers.go`（runtime/completion report 路由、orchestration 转发）
- `internal/platform/mcpcontrol/handlers_hooks.go`（hook subscribe/resolve/pending 处理、payload 解码）

> 与第 01-08 轮已覆盖的 `mcpcontrol/{registry,peers,sweeper,handlers,router,resolution}` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `release_scope.go:63-66` `DispatchLSPReleaseScope` | 兜底 | `if r == nil { return ... errPeerUnavailable("mcp registry is nil") }` | nil registry 是装配 bug，不是 peer 不可用；用 errPeerUnavailable 让客户端误以为可重试 | nil registry 应 panic 或 errInvalidParams |
| `release_scope.go:71-74` 无 target | 兜底 | `len(targets) == 0` 时返回 `(LSPReleaseScopeResult{}, nil)` | 调用方期望释放某个 agent/thread 的 scope，结果"找不到 peer"被当成"成功释放零个"，调用方无法感知 LSP 不在线 | 改为 `errPeerUnavailable("no lsp peer matches scope %s/%s/%s")` |
| `release_scope.go:78-101` 单 target failure 路径 | 静默 | 失败时 `_ = r.disconnectLease(...)` 显式忽略 disconnect 错误 | 与 round-07/08 OnDisconnect/sweeper 同根 | 改 errors.Join 上抛 |
| `release_scope.go:79-81` peer==nil skip | 静默 | `if target == nil \|\| target.Peer == nil { continue }` | 半状态 peer 被静默忽略，统计的 ManagerKeys 不包含它 | 至少 metrics counter；nil target 应 panic（不可能发生） |
| `release_scope.go:16-49` `releaseScopeRequestFromConfigPayload` | 兜底 | unknown event 返 `(zero, false)`；agent_id 缺失返 `false` | 调用方靠 bool 判断；无法区分"event 不需要 release scope" 与"参数缺失" | 拆成两步：classifyEvent → buildRequest |
| `fanout.go:25-37` `IntersectTargets` | 兜底 | `selectorBucketsLocked` 返回 `(_, false)` 时返回 nil；buckets 为空时**返回所有 active targets** | 空 selector → 全广播。这对 ConfigChange 等"全员通知"是合理的，但若调用方写错 selector 字段名导致 buckets 为空，会意外全广播 | 至少 debug log "selector matched all"；或要求调用方显式传 `BroadcastAll: true` |
| `fanout.go:33-35` 全广播路径 | 兜底 | 同上 | 同上 | 同上 |
| `fanout.go:111-117` `activeTargetLocked` | 兜底 | instance==nil / Peer==nil / Status!=Active 都静默 skip | 半状态 instance 被屏蔽；多 instance 路径中调用方拿不到任何错误信号 | 不报错可接受，但应在 fanout 层统计 "skipped_inactive" 指标 |
| `fanout.go:155-175` `recoverWorkerPanic` | 兜底 | panic 后 evict failure threshold 命中才真 evict；否则 closePeer | panic 在阈值内被当成可恢复，但 panic 通常是不可恢复 bug | panic 应直接 evict（不计入 ConsecutiveFailures），panic 不该用阈值 throttle |
| `fanout.go:177-191` `notePeerFailure` | 兜底 | failure 累加到阈值才 evict | 阈值期间客户端持续失败被 retry；与第 7 轮 ShutdownInstance failure 同根 | 设计本身合理，但 panic 路径不应走它（见上一条） |
| `fanout.go:159-163` panic recovery 内的 disconnect | 静默 | `_ = r.disconnectLease(...)` 显式忽略 | 同 release_scope.go:89 | errors.Join + 返回 |
| `config_change.go:31-33` `registerConfigChangeSubscriptions` | 兜底 | `dispatcher == nil \|\| worker == nil` 返回 nil | 装配 bug 被吞，订阅静悄悄不工作 | nil 应 panic（装配阶段） |
| `config_change.go:73-88` `configChangePayloadString` | 静默 | key 不存在/类型不符/空字符串都 continue 到下一个 key；最后返回 "" | 用户传错字段名/错类型，"找不到"与"找到但空"无法区分 | 类型不符应至少 debug log（"agentId is not string"） |
| `config_change.go:101-163` 各 payload 函数 | 兜底 | 字段值为空时 `setPayloadString` 静默不 set | 最终 payload 缺失字段时调用方无法区分"事件未携带"vs"事件携带空值" | 当前可接受（语义"空字符串 = 未提供"），但需文档 |
| `report_handlers.go:48-79` `dispatchReport` | 兜底 | `runtimeReports == nil`/`completionReports == nil` 返 errCapabilityMismatch | NewHandlers 已注入 default*；理论 nil 不会发生。但若装配错误，errCapabilityMismatch 提示对调用方无意义 | nil handler 是装配 bug，应 panic 或 errInternal |
| `report_handlers.go:60-72` Runtime/Completion 二级 nil 检查 | 兜底 | `if req.Report.Runtime != nil { report = *req.Report.Runtime }` 否则用零值 | 客户端传 `Type=runtime` 但 `Runtime=nil` 会用零值 RuntimeReport 跑下去 | type 与字段不匹配应 errInvalidParams |
| `report_handlers.go:74-78` 不支持的 variant | 静默 | progress/diagnostic 与 default 都返回 errInvalidParams | 行为正确，但日志/metrics 没有区分"已知未实现"与"未知 variant" | 拆 progress/diagnostic 单独的 errCapabilityMismatch |
| `report_handlers.go:96-100` `HandleRuntimeReport` orchestration 错误 | 兜底 | `errReportConflict("failed to persist runtime report: %v", err)` | 任何 orchestration 错误都被打成 conflict，丢失原始错误类型 | 用 errors.Wrap/%w 保留原 err |
| `report_handlers.go:122-131` `HandleCompletionReport` | 兜底 | 同上，`errReportConflict("failed to persist completion report: %v", err)` | 同上 | 同上 |
| `report_handlers.go:154-163` `completionEventData` | 静默 | `report.Completion.Metadata` 为空时尝试 marshal 整个 report；marshal 失败返回 nil | 事件数据完全丢失；调用方拿到 nil event_data 不知道是真无数据还是 marshal 失败 | marshal 失败 panic 或 return error（这是不可达分支） |
| `handlers_hooks.go:99-121` `lookupLeaseByServer` | 兜底 | nil registry / nil server 返回 `(zero, false)` | 调用方靠 bool 判断；无法区分"nil 输入"与"未注册" | nil 输入应 panic；保留 not found 用 false |
| `handlers_hooks.go:123-132` `decodePayloadMap` | 兜底 | json.Unmarshal 失败时**伪造一个 `{"payload": <raw>}` 包装** | 用户传错 JSON 时被自动当作合法对象包装；下游用 `payload["agentId"]` 等访问拿不到字段；语义破损 | unmarshal 失败应 return error，不要伪造 |
| `handlers_hooks.go:134-139` `timeDurationMillis` | 兜底 | timeoutMs<=0 返 defaultNotifyTimeout | 与 round-08 requestApproval 同根问题；timeoutMs 缺失静默走默认 | 调用方应显式声明 default；这里至少 log |
| `handlers_hooks.go:61-77` `resolveHookPendingAgentID` | 兜底 | instanceAgentID 与 requestAgentID 都为空 → errInvalidParams（OK）；instance 有 agent 而 request 没传 → 用 instance | shared-service peer 与 agent-bound peer 的 agent_id 解析逻辑混合，未来难以扩展 | 当前合理；但应在 PendingRequest 上加显式 `IsSharedService` 字段，避免 implicit |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `release_scope.go:79-81` | nil target/peer skip |
| `release_scope.go:89-95` | failure 路径 disconnectLease 错误吞 |
| `fanout.go:159-163` | panic 路径 disconnectLease 错误吞 |
| `fanout.go:111-117` | activeTargetLocked nil/inactive skip 无 metrics |
| `config_change.go:31-33` | nil dispatcher/worker 返回 nil |
| `config_change.go:73-88` | configChangePayloadString 类型不符 continue |
| `config_change.go:165-172` | setPayloadString 空值 noop |
| `report_handlers.go:96-100, 122-131` | orchestration 错误归并为 conflict |
| `report_handlers.go:154-163` | completionEventData marshal 失败返 nil |
| `handlers_hooks.go:123-132` | decodePayloadMap unmarshal 失败伪造 wrapper |
| `handlers_hooks.go:99-121` | lookupLeaseByServer nil 输入返 false |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `release_scope.go:51-58` `firstNonEmptyString` | 又一份"首个非空"helper（与 round-04 firstNonEmpty 重复实现） |
| `release_scope.go:105-112` `normalizeLSPReleaseScopeRequest` | 仅 trim；未校验 ScopeKind 在白名单内 |
| `release_scope.go:134-143` `releaseScopeTargets` | manager_key 路径返回所有 LSP；无 ManagerKey 过滤 |
| `release_scope.go:145-155` `mergeLSPReleaseScopeResult` | dst==nil 时静默 return |
| `release_scope.go:157-166` `appendUniqueStrings` | 用 slices.Contains 线性扫描；大集合 O(n²) |
| `fanout.go:25-37` `IntersectTargets` | 空 buckets → 全广播 |
| `fanout.go:39-63` `selectorBucketsLocked` | 6 个固定维度；新增维度需修改这里和索引初始化 |
| `fanout.go:119-125` `selectorIndexBucket` | key="" 返回 (nil, true)；空字符串 = 不参与过滤 |
| `fanout.go:127-135` `smallestSelectorBucket` | buckets 长度 0 时 buckets[0] 越界 panic |
| `config_change.go:62-71` `configChangeSelectorScope` | scope 解 normalize 后比较 zero value 决定是否返 nil；与上层 NotifyBySelector 路径耦合 |
| `report_handlers.go:48-78` `dispatchReport` | 没有兜底 default handler；progress/diagnostic 与 default 同处理 |
| `report_handlers.go:140-152` `reportVariant` | 无 Type 字段时按 Runtime/Completion 字段推断；二者都为 nil 时返 "" |
| `handlers_hooks.go:14-58` 三个 `handleHook*` | 路径相似但 validate 函数不同；模板化结构 |
| `handlers_hooks.go:99-121` `lookupLeaseByServer` | 接受 jrpcPeer 与 *jrpcPeer 双形式；类型断言宽容 |

## 修复优先级

### P0（必须本周修）
1. `DispatchLSPReleaseScope` nil registry 改 panic（装配错误必须暴露）
2. `DispatchLSPReleaseScope` 无 target 时返回 errPeerUnavailable，不能静默"成功"
3. `recoverWorkerPanic` 中 panic 直接 evict，不走 ConsecutiveFailures 阈值（panic 不该 throttle）
4. `decodePayloadMap` unmarshal 失败应 return error，不能伪造 `{"payload": raw}` 包装
5. `registerConfigChangeSubscriptions` nil 依赖必须 panic
6. `dispatchReport` 中 `Type=runtime` 但 `Runtime=nil` 应 errInvalidParams，不能用零值

### P1（本月）
7. `release_scope.go:89, fanout.go:159` 等 disconnectLease `_ =` 改 errors.Join 上抛（与 round-07/08 协同）
8. `IntersectTargets` 空 buckets 全广播加 debug log 或要求显式 BroadcastAll
9. `HandleRuntimeReport`/`HandleCompletionReport` orchestration 错误用 %w 保留原类型
10. `report_handlers.go:74-78` progress/diagnostic 拆为 capability mismatch（区分已知未实现与未知）
11. `completionEventData` marshal 失败 panic（不可达）
12. `releaseScopeRequestFromConfigPayload` 拆为 classify+build，避免 bool 兜底
13. `lookupLeaseByServer` nil 输入 panic
14. `timeDurationMillis` timeoutMs<=0 至少 log

### P2（下个 sprint）
15. `release_scope.go:51-58` 删除重复 firstNonEmptyString，统一到一处（与 round-04 firstNonEmpty 协同）
16. `release_scope.go:114-132` `validateLSPReleaseScopeRequest` switch default 已 return error，但 ScopeKind 在 normalize 阶段就该白名单校验
17. `fanout.go:127-135` `smallestSelectorBucket` 空 slice panic 防御
18. `config_change.go:73-88` configChangePayloadString 类型不符 debug log
19. `handlers_hooks.go:99-121` lookupLeaseByServer 双类型断言简化（统一为 *jrpcPeer 或 jrpcPeer）

## 边界条件

1. **`IntersectTargets` 空 selector 全广播是 ConfigChanged 的关键路径**：当 NotifyConfigChanged 传一个只有 topic 没有 scope 的 selector 时，需要广播给所有订阅者。改为 `BroadcastAll` 显式参数会改 NotifyConfigChanged 的签名，要先 grep 调用面。
2. **`recoverWorkerPanic` 直接 evict 的影响**：当前阈值机制是为了防止偶发 panic 导致频繁 evict-reconnect 抖动。改为 panic 直接 evict 后，如果 peer 真的有 panic-recovery 路径，会被立刻断开，可能影响稳定性。建议：panic 计数翻倍而非直接 evict，或单独设置 PanicEvictThreshold=1。
3. **`decodePayloadMap` 的 wrapper fallback 是为了兼容老 client**：旧版本 hook 可能发送非对象 payload（数组或字符串）。改为 return error 前要先确认 payload 协议是否强制对象类型；如果是，可以直接报错。
4. **`completionEventData` 的 marshal 失败实际不会发生**：所有字段都是 JSON-tagged 类型；fail-fast 改 panic 是 defensive。低优先级。
5. **`appendUniqueStrings` 的 O(n²) 性能**：ScopeKeys/ManagerKeys 通常不超过 50 个，O(n²) 可接受。但若未来 LSP peer 数量大幅增加要换 set。
6. **`handlers_hooks.go:lookupLeaseByServer` 的双类型断言**：`jrpcPeer` 与 `*jrpcPeer` 都被支持。这是因为 `peers.go:15` 定义的是值类型，但部分调用点可能存了指针。修改前先统一定义。
7. **`report_handlers.go` orchestration 错误归并为 conflict**：当前所有持久化失败都打 errReportConflict 是因为客户端可以根据这个错误决定是否重试。改为 %w 保留原 err 后，调用方仍能用 errors.Is(err, ErrReportConflict) 判断；只是包装方式变更，向前兼容。
8. **`firstNonEmptyString` 重复实现**：本轮在 release_scope.go:51 又发现一份。`handler_managed_launch.go:189` 已有同名函数。统一收口建议放在 `internal/platform/shared` 或 contract 层。

---

下一轮范围建议：
- `internal/platform/mcpcontrol/factory.go` + `module.go` + `subscribers.go`
- `internal/platform/mcpcontrol/scope.go` + `registry_helpers.go` + `registry_support.go`
- `internal/platform/mcpcontrol/runner_provider.go` + `errors.go` + `config_fanout_worker.go`
