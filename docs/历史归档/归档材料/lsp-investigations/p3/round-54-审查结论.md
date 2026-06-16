# 第 54 轮审查结论

## 审查范围

- `internal/sidecar/orch/orchestration/factory.go`（resetLaunchState、cleanupAgentState、prepareLaunchLocked、markStoppingLocked、commitLaunchFailureLocked、commitLaunchSuccessLocked、finalizeActiveTurnLocked、forceIdleAfterTurnTerminalLocked、shouldAutoRecoverProcessExitLocked、recoverAfterProcessExit、recoverLauncherWithReason、prepareLauncherRecovery、commitLauncherRecoverySuccess/Failure、withAgentLocked、withAgentReadLocked、lockRead、lookupAgentByIDLocked、lookupAgentByIdentityLocked、lookupAgentBySeqLocked、agentSessionFenceOK 等 ~60 个函数）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `factory.go:691-705` lockRead | 协程延迟 | `for !s.mu.TryRLock() { time.Sleep(time.Millisecond) }` 自旋等待 | 写锁持有时间长时（如 fireAndPublishLocked 第51轮 P0），lockRead 自旋消耗 CPU；且 1ms sleep 粒度让 P99 延迟不可预测 | 改为 `s.mu.RLock()`（阻塞但不消耗 CPU）+ ctx 超时用 select |
| `factory.go:692-694` lockRead | 静默 fallback | `ctx == nil` 时 fallback Background | 与全项目 ctx-nil 同问题 | 改 panic |
| `factory.go:346-359` commitLauncherRecoveryFailure | 协程延迟 | `s.mu.Lock()` 后做 `commitLaunchFailureLocked` + `setNoReportFallbackLocked`（可能涉及 DB 写） | 持锁期间做 DB 写——如果 DB 慢，锁持有时间长，阻塞所有其他 agent 操作 | DB 写移到锁外（先收集数据 → 释放锁 → 写 DB） |
| `factory.go:361-397` commitLauncherRecoverySuccess | 协程延迟 | 同上：`s.mu.Lock()` 后做 `rekeyLaunchedAgentLocked` + `commitLaunchSuccessLocked` + `finishLauncherRecoveryTurnLocked` | 持锁期间多步操作（rekey + commit + replay turn）——任一步慢都阻塞全局 | 同上 |
| `factory.go:196-218` shouldAutoRecoverProcessExitLocked | 弱契约 | `maxProcessExitAutoRecoveries = 3` + `processExitAutoRecoverWindow = 2min` 硬编码 | 3 次 / 2min 的限制无法配置；某些场景（如网络抖动频繁）需要更多重试 | 改为 cfg-driven |
| `factory.go:209-212` processExitAutoRecoverable | 弱契约 | 复杂条件：`s != nil && agent != nil && err != nil && !agent.stopRequested && (len(command)>0 \|\| launcher!=nil && cmd==nil && remoteThreadID!="")` | 6 个条件 AND 在一行——可读性极差；任一条件变化需重新理解整行 | 拆为多行 + 每个条件加注释 |
| `factory.go:741-747` lookupAgentByIdentityLocked | 性能 | `agentIdentityAny` 模式下线性扫描所有 agents 找 remoteAgentID/remoteThreadID | agent 数量大时（>100）每次 hook event 都 O(N) 扫描 | 加 reverse index map（remoteAgentID → agent） |
| `factory.go:18-31` resetLaunchState | 静默 | `agent == nil` 静默 return | 与第48轮 runtime.go 同模式——nil agent 是 caller bug | 改 panic |
| `factory.go:73-96` commitLaunchFailureLocked | 弱契约 | `launchErr == nil` 时 `return nil`（line 79-81）——但函数名是 commitLaunchFailure | 调用方传 nil error 到 commitLaunchFailure 是逻辑错误 | nil launchErr 改 panic |
| `factory.go:749-758` agentSessionFenceOK | 弱契约 | `ev == ""` 时返 true（legacy input 无 session ID 时放行） | 旧版 hook event 无 session ID 时 fence 不生效——stale event 可能污染状态 | 加 metrics 计数「legacy events without session ID」；逐步强制 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `factory.go:691-705` lockRead | 自旋等待 + 1ms sleep | 加 spin count 监控；> 100 次 spin 打 Warn |
| `factory.go:346-397` commitLauncherRecovery* | 持锁期间 DB 写 | 加锁持有 duration 监控；> 100ms 打 Warn |
| `factory.go:647-659` withAgentLocked | 每次 agent 操作都获取全局写锁 | 加 per-call duration；考虑改为 per-agent 锁（sharded lock） |
| `factory.go:741-747` lookupAgentByIdentityLocked | 线性扫描 | 加 reverse index；或 scan count 监控 |
| `factory.go:304-322` recoverLauncherWithReason | 同步路径：prepare → loadTurn → Stop → Launch → commit | 加 per-step duration 日志 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `factory.go:18-31, 33-44` | nil agent 静默 return |
| `factory.go:73-81` | nil launchErr 静默 return nil |
| `factory.go:692-694` | ctx nil fallback Background |
| `factory.go:749-758` | 空 session ID 静默放行 |
| `factory.go:238-246` setProcessExitFallbackReportLocked | persist 失败只 Warn |
| `factory.go:248-260` recoverAfterProcessExit | recovery 失败只 Warn |
| `factory.go:415-423` notifyRecoveryFailure | 通知失败只 Warn |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `factory.go:691-705` lockRead | 自旋 + sleep 模式 |
| `factory.go:196-218` | 硬编码 3 次 / 2min 限制 |
| `factory.go:209-212` | 6 条件 AND 一行 |
| `factory.go:741-747` | 线性扫描 reverse lookup |
| `factory.go:346-397` | 持锁期间 DB 写 |
| `factory.go:73-96` | nil launchErr 不 panic |

## 修复优先级

### P0（必须本周修）
1. **`factory.go:691-705` lockRead 自旋等待**——`for !TryRLock() { sleep(1ms) }` 在写锁持有时间长时（第51轮 fireAndPublishLocked P0）会自旋数百次，消耗 CPU 且延迟不可预测。改为标准 `s.mu.RLock()` + 用 goroutine + select 实现 ctx 超时。
2. **`factory.go:346-397` commitLauncherRecovery* 持锁期间 DB 写**——全局写锁持有期间做 DB 操作（setNoReportFallbackLocked → persistAgentReportFileAndGC）。DB 慢时所有 agent 操作被阻塞。这是全局锁 + IO 的经典反模式。改为：收集需要持久化的数据 → 释放锁 → 异步写 DB。

### P1（本月）
3. `factory.go:692-694` ctx nil 改 panic
4. `factory.go:741-747` 加 reverse index map
5. `factory.go:209-212` 拆多行 + 注释
6. `factory.go:196-218` 硬编码改 cfg-driven
7. `factory.go:749-758` 空 session ID 加 metrics

### P2（下个 sprint）
8. `factory.go:18-31` nil agent 改 panic
9. `factory.go:73-81` nil launchErr 改 panic
10. `factory.go:647-659` withAgentLocked 评估 per-agent sharded lock

## 边界条件

1. **`factory.go:691-705` lockRead 的设计意图**：标准 `s.mu.RLock()` 不支持 ctx 取消——一旦调用就阻塞直到获取锁。`lockRead` 用 TryRLock + sleep 实现了「可取消的读锁获取」。这是 Go sync.RWMutex 的已知限制（不支持 context-aware locking）。但 1ms sleep 自旋是低效实现——更好的方案是用 channel-based lock 或 `sync.Cond`。
2. **`factory.go:346-397` 持锁 DB 写的根因**：`setNoReportFallbackLocked` 需要在锁内调用（因为它修改 agent.lastReport），但内部调 `persistAgentReportFileAndGC` 做 DB 写。这是「状态修改 + 持久化」耦合的问题。解法：①分离状态修改（锁内）和持久化（锁外）；②用 write-ahead log 模式（先写 WAL 再修改内存）。
3. **`factory.go:196-218` auto-recovery 的 3 次 / 2min 限制是合理的安全阀**：防止 crash-loop 无限重启。`resetProcessExitAutoRecoverWindowLocked` 在窗口过期后重置计数器——让长期稳定运行的 agent 在偶发 crash 后仍能 auto-recover。这是良好的 circuit-breaker 设计。
4. **`factory.go:647-659` withAgentLocked 是全局写锁**：所有 agent 操作（launch、stop、state change、turn submit）都通过 `s.mu.Lock()` 串行化。这是简单但低效的并发模型——100 个 agent 的操作互相阻塞。长期应改为 per-agent 锁（`agent.mu`），但需要仔细处理跨 agent 操作（如 listAgents）。
5. **`factory.go:304-322` recoverLauncherWithReason 的 Stop → Launch 顺序**：先 Stop 旧实例再 Launch 新实例。如果 Stop 失败（远端不可达），仍然尝试 Launch——可能导致双实例。当前 line 314-316 Stop 失败走 `commitLauncherRecoveryFailure`（不继续 Launch）。这是正确的——Stop 失败意味着旧实例可能仍在跑，不应启动新实例。
6. **`factory.go:728-747` lookupAgentByIdentityLocked 的 reverse lookup**：`agentIdentityAny` 模式下遍历所有 agent 找 `remoteAgentID == agentID || remoteThreadID == agentID`。这是为了支持 hook event 用 remote ID 查找 agent（hook 可能只知道 remote thread ID）。O(N) 扫描在 agent 少时（<20）可接受，但应加 reverse index 以备 scale。

---

**本轮总结**：发现 2 个 P0 问题：①lockRead 自旋等待消耗 CPU 且延迟不可预测；②commitLauncherRecovery* 持锁期间 DB 写阻塞全局。`factory.go` 是 orchestration service 的核心状态管理文件（~815 行），包含 agent 生命周期的所有关键操作。auto-recovery 的 3次/2min circuit-breaker 是良好设计。全局写锁是简单但低效的并发模型，长期应改 per-agent 锁。

**累计进度**：54 轮完成。cron `fd4b4728` 继续推进。

---

## 第54轮后累计状态

- 已完成：第27-54轮，共28轮
- 累计 P0 问题：**61 个**
- orchestration 核心已全面覆盖（factory + helpers + runtime + hook_consumer + exit_monitor + dag_dispatch + node_router + node_executor_dispatch + dag_turn_completed_subscriber + wakeup_reclaim + launcher_protocol + persistent_runtime_rehydrate + sharedfile_adapter）
- 下一轮计划：`orchestration/launcher.go` 或 `orchestration/persistent_agents.go`
