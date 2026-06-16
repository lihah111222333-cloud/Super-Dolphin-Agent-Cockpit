# 第 48 轮审查结论

## 审查范围

- `internal/sidecar/orch/orchestration/runtime.go`（UpdateRuntime、snapshotLocked、applyRuntimeReportLocked、processPID、shouldUpdatePort/Provider、runtimeSnapshotChanged、runtimeProviderSource、normalizeRuntimeProvider、isKnownRuntimeProvider、resetRuntimeStateLocked、clearAgentLifecycleErrorLocked、clearAgentTurnStateLocked、clearAgentStopReasonLocked、snapshotPort、snapshotProvider）
- `internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go`（ensureRuntimeForPersistedAgent、canRehydratePersistedRuntime、buildRuntimeFromPersistedBinding、loadPersistedRuntimeSource、activePersistedThreadForBinding、newPersistedRuntimeAgent、persistedThreadForBinding、persistedRuntimeTime 等）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `persistent_runtime_rehydrate.go:20-45` ensureRuntimeForPersistedAgent | 静默 | 整个函数无返回值（void）；所有错误路径只 Warn 日志 + return | caller 不知道 rehydrate 是否成功；如果 binding 查询失败（DB 不可达），agent 静默不可用 | 改为 `(rehydrated bool, err error)` 让 caller 决定是否 fail-fast |
| `persistent_runtime_rehydrate.go:265-277` persistedRuntimeTime | 静默 fallback | binding/thread 时间都为零时 `return time.Now()` | 与第43轮 `FirstEventTime` 同问题——用墙钟代替上游时间戳让事件顺序错乱 | 全零时 return error 或 Warn |
| `persistent_runtime_rehydrate.go:179-185` persistedThreadInactive | 弱契约 | `thread == nil` 时返 false（表示「active」） | nil thread 不是 active——是「未知状态」。返 false 让 caller 继续 rehydrate 一个可能不存在的 thread | nil thread 返 true（inactive）或改三态 |
| `persistent_runtime_rehydrate.go:215-230` persistedThreadForBinding | 静默 fallback | 先按 remoteThreadID 查，失败后按 agentID 查 | 两次查询的 fallback 链让 caller 不知道最终用了哪个 ID 找到的 thread | 加 Debug 日志带最终命中的 key |
| `runtime.go:33-38` UpdateRuntime provider fail-fast | 正面案例 | 非法 provider 直接返 error（双语错误消息） | 这是 P23 README §default-safety 的落地——**正面案例** | 维持 |
| `runtime.go:143-150` runtimeProviderSource | 弱契约 | `isKnownRuntimeProvider` 返 false 时 source="runtime-unverified" | 但 line 33-38 已经拒绝了未知 provider——所以 line 143-150 的 "runtime-unverified" 分支永远不可达 | 改为 panic（不可达分支）或删除 |
| `runtime.go:165-173` resetRuntimeStateLocked | 静默 | `agent == nil` 时静默 return | nil agent 是 caller bug（withAgentLocked 保证非 nil），但防御性 nil-check 掩盖 | 改 panic（开发期）或删除 nil-check |
| `runtime.go:179-185, 191-196, 202-209` clear*Locked | 静默 | 同上 `agent == nil` 静默 return | 同上 | 同上 |
| `persistent_runtime_rehydrate.go:47-64` canRehydratePersistedRuntime | 静默 | 5 个条件任一不满足静默返 false | caller 不知道为什么不能 rehydrate（是 launcher 不支持？还是 binding 缺失？） | 改为 `(bool, string)` 返回 reason |
| `persistent_runtime_rehydrate.go:136-138` loadPersistedRuntimeSource | 弱契约 | `provider != "codex"` 时返 "unsupported_provider" + nil | 只支持 codex rehydrate；claude agent 重启后不 rehydrate——这是设计选择但无文档 | 加注释说明「claude agent 是 local process，重启后需重新 launch」 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `persistent_runtime_rehydrate.go:20-45` ensureRuntimeForPersistedAgent | 同步路径：binding 查询 + thread 查询 + 构造 agent | 加 duration 日志；DB 查询慢时（>100ms）打 Warn |
| `persistent_runtime_rehydrate.go:215-230` persistedThreadForBinding | 两次 DB 查询串行 | 可并行化（但 fallback 语义要求串行）；加 per-query duration |
| `runtime.go:12-59` UpdateRuntime | `s.withAgentLocked` 持锁期间做 snapshot + publish | 锁持有时间监控；publish 如果阻塞会传染到锁 |
| `runtime.go:61-99` snapshotLocked | 纯内存操作（字符串 trim + 条件判断）| 无延迟风险 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `persistent_runtime_rehydrate.go:20-45` | 整个函数 void，错误只 Warn |
| `persistent_runtime_rehydrate.go:265-277` | 时间全零 fallback time.Now() |
| `persistent_runtime_rehydrate.go:179-185` | nil thread 返 false（active） |
| `persistent_runtime_rehydrate.go:47-64` | 5 个条件静默返 false |
| `runtime.go:165-209` | 4 个 clear*Locked nil-agent 静默 return |
| `runtime.go:143-150` | "runtime-unverified" 分支不可达但存在 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `persistent_runtime_rehydrate.go:20-45` | void 返回值 |
| `persistent_runtime_rehydrate.go:97-113` buildRuntimeFromPersistedBinding | 三元组 `(*agentRuntime, string, error)` 中 reason 和 error 互斥但靠约定 |
| `persistent_runtime_rehydrate.go:136-138` | 只支持 codex 无文档 |
| `runtime.go:143-150` | 不可达分支 |
| `runtime.go:127-141` runtimeSnapshotChanged | 8 个参数函数——可读性差 |

## 修复优先级

### P0（必须本周修）
1. **`persistent_runtime_rehydrate.go:20-45` ensureRuntimeForPersistedAgent void 返回值**——rehydrate 失败时 caller 不知道。如果 DB 不可达导致所有 agent 都 rehydrate 失败，系统静默运行但所有 agent 不可用。改为 `(bool, error)` 让 caller 在系统性失败时 fail-fast。
2. **`persistent_runtime_rehydrate.go:179-185` persistedThreadInactive nil thread 返 false**——nil thread 被视为 active → rehydrate 继续 → `newPersistedRuntimeAgent` 拿到 nil thread → line 189-206 中 thread 相关字段全为零值 → agent 被创建但 port=0, cwd="" → 后续 turn 路由到 port=0 失败。改为 nil → true（inactive）。

### P1（本月）
3. `persistent_runtime_rehydrate.go:265-277` persistedRuntimeTime 全零改 Warn
4. `runtime.go:143-150` 不可达分支改 panic 或删除
5. `runtime.go:165-209` 4 个 nil-agent check 改 panic
6. `persistent_runtime_rehydrate.go:47-64` canRehydratePersistedRuntime 改 (bool, string)
7. `runtime.go:127-141` runtimeSnapshotChanged 改为 struct 比较

### P2（下个 sprint）
8. `persistent_runtime_rehydrate.go:136-138` 加注释说明 codex-only 设计
9. `persistent_runtime_rehydrate.go:215-230` 加 per-query duration 日志
10. `persistent_runtime_rehydrate.go:97-113` 三元组改 typed result

## 边界条件

1. **`runtime.go:33-38` UpdateRuntime provider fail-fast 是项目正面案例**：注释明确引用 P23 README §default-safety，双语错误消息（中英），拒绝非法值而非静默放行。这是「100% Fail-Fast」的具体落地——**建议作为模板推广到其他 runtime 上报路径**。
2. **`persistent_runtime_rehydrate.go` 的 rehydrate 设计意图**：mcp-orch 重启后内存 agent map 为空，但 DB 中仍有 persisted binding。UI 列表 agent 时触发 rehydrate——从 DB 重建 agentRuntime。这是 stateful service 的标准 warm-up 模式。但当前实现只支持 codex（remote agent），不支持 claude（local process）——因为 local process 重启后需要重新 launch（进程已死）。这是合理的设计选择但应文档化。
3. **`persistent_runtime_rehydrate.go:179-185` persistedThreadInactive 的 nil 语义**：函数名是「thread 是否 inactive」。nil thread 的语义应该是「thread 不存在 → 不可能 active → inactive」。但当前返 false（not inactive = active）。这让 caller 继续 rehydrate 一个不存在的 thread。**P0 因为它导致 port=0 的 zombie agent 被创建**。
4. **`runtime.go:127-141` runtimeSnapshotChanged 的 8 参数函数**：这是 Go 中常见的「避免 struct 分配」优化——直接传 8 个值比创建 2 个 snapshot struct 更快。但可读性极差。建议改为 `type portProviderSnapshot struct { port int; portSource string; provider string; providerSource string }` + `func (a portProviderSnapshot) Equal(b portProviderSnapshot) bool`。
5. **`runtime.go:143-150` runtimeProviderSource 的不可达分支**：line 33-38 已经拒绝了 `!isKnownRuntimeProvider(provider)` 的情况。所以 `applyRuntimeReportLocked` 只会被调用在 known provider 上。`runtimeProviderSource` 的 `!isKnownRuntimeProvider` 分支永远不会被 UpdateRuntime 触发。但可能被其他 caller 触发（如 rehydrate 路径）——需确认。如果确实不可达，改 panic 让 invariant 违反早期暴露。
6. **`persistent_runtime_rehydrate.go:187-213` newPersistedRuntimeAgent 的状态机初始化**：line 207-211 用 `platformstatemachine.New` 创建状态机，初始状态是 `agentdto.StateIdle`。这是合理的——rehydrated agent 应该是 idle（等待下一个 turn）。但如果 thread 实际上正在执行 turn（mcp-orch 重启时 turn 还在跑），agent 会被标记 idle 而实际 running → 状态不一致。建议 rehydrate 时检查 thread 的 active turn 状态。

---

**本轮总结**：发现 2 个 P0 问题：①ensureRuntimeForPersistedAgent void 返回值让系统性 DB 故障静默；②persistedThreadInactive nil thread 返 false 导致 port=0 zombie agent 被创建。`runtime.go:33-38` UpdateRuntime provider fail-fast 是 P23 §default-safety 的正面落地案例。persistent_runtime_rehydrate 整体是 stateful service warm-up 的合理设计但 codex-only 限制需文档化。

**累计进度**：48 轮完成。cron `fd4b4728` 继续推进。
