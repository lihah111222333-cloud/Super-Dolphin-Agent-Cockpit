# 第 51 轮审查结论

## 审查范围

- `cmd/mcp-orch/orchestration/node_router.go`（NodeExecutorRouter、NewNodeExecutorRouter、WithEventBus、spawnReplanPlanner、sanitizeReplanLaunchName、buildReplanPlannerPrompt）
- `cmd/mcp-orch/orchestration/helpers.go`（buildStatesFromDefinitions、BindActiveTurnID、reconcileReadyStateLocked、startTurnExecution、finishTurnStartSuccess/Failure、stopAgentLocked、stopAgentWithReason、requestAgentStop、waitForSubmitSessionReady、startProcessLocked、fireOrForceLocked、fireAndPublishLocked、listAgents）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `helpers.go:312-327` fireOrForceLocked | 静默 fallback | `ctx == nil` 时 fallback Background（line 313-315） | 与全项目 ctx-nil 同问题；但此处更严重——状态机转换在无 trace/cancel 的 ctx 下执行 | 改 panic |
| `helpers.go:353-370` fireAndPublishLocked | 协程延迟/死锁 | TODO 注释明确说「Publish 在持锁期间调用存在潜在风险——如果 kelindar/event 的投递策略变更为同步将导致死锁」 | 代码作者已识别风险但未修复。当前 kelindar/event 是异步投递所以暂时安全，但这是定时炸弹 | 将 Publish 移到锁外（收集 events → 释放锁 → 批量 publish） |
| `helpers.go:267-310` startProcessLocked | 弱契约 | line 300-306 `s.exitMonitor != nil` 时 Arm；nil 时静默跳过 | exitMonitor nil 意味着进程退出不被监控 → 进程 crash 后 agent 永远标记 running | exitMonitor nil 时 fail-fast（不应启动进程） |
| `helpers.go:234-265` waitForSubmitSessionReady | 静默 | `s == nil \|\| s.turnStarter == nil` 时 `return nil`（line 235-237） | turnStarter 未注入时静默跳过 session ready 等待 → turn 可能在 session 未就绪时提交 → 失败 | turnStarter nil 时返 error |
| `helpers.go:95-133` startTurnExecution | 弱契约 | 整个函数 void；错误通过 `finishTurnStartFailure` 内部处理 | caller 不知道 turn 是否成功启动；需要通过状态机变化间接感知 | 合理设计（异步 turn 提交），但应加 metrics |
| `helpers.go:74-93` reconcileReadyStateLocked | 静默 | `fireOrForceLocked` 失败只 Warn + return | 状态机转换失败 → agent 状态不一致（如 turn_starting 但无 active turn） | 加 metrics counter；连续失败时 alert |
| `node_router.go:41-60` NewNodeExecutorRouter | 弱契约 | store 参数无 nil 校验；agentExec/autoExec 允许 nil（注释说明） | store nil 时后续调用 panic | store nil 时 fail-fast |
| `node_router.go:62-67` WithEventBus | 静默 | `r != nil` 时设置 bus；r nil 时静默 return r（nil） | nil router 调 WithEventBus 返 nil——caller 链式调用 `router.WithEventBus(bus).DoSomething()` 会 panic | nil r 时 panic 或返 error |
| `node_router.go:76-103` spawnReplanPlanner | 静默 | `d == nil` 时返 false（line 77-79） | nil dispatcher 是配置 bug，被静默吞掉 | 改 panic |
| `helpers.go:372-385` listAgents | 性能 | 持 RLock 期间深拷贝所有 agent（`*agent` → snapshot） | agent 数量大时锁持有时间长；其他 goroutine 等待写锁被阻塞 | 改为 copy-on-write 或 snapshot cache |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `helpers.go:353-370` fireAndPublishLocked | Publish 在持锁期间——如果 subscriber 回调 service 方法会死锁 | 加 Publish duration 监控；> 1ms 打 Warn（正常应 < 100µs） |
| `helpers.go:234-265` waitForSubmitSessionReady | 最多等 5s（submitSessionReadyTimeout）；已有 duration 日志 | **正面案例**：line 259 `elapsed >= longWaitLogThreshold` 打 Warn |
| `helpers.go:267-310` startProcessLocked | cmd.Start() 同步；进程启动可能慢（如 JVM） | 加 Start duration 日志 |
| `helpers.go:372-385` listAgents | RLock 期间深拷贝 | 加锁持有 duration；> 10ms 打 Warn |
| `helpers.go:95-133` startTurnExecution | waitForSubmitSessionReady（5s）+ StartTurn（网络 roundtrip） | 已有 duration 日志（line 121, 131）——**正面案例** |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `helpers.go:313-315` | ctx nil fallback Background |
| `helpers.go:235-237` | turnStarter nil 静默 return nil |
| `helpers.go:300-306` | exitMonitor nil 静默跳过 Arm |
| `helpers.go:74-93` reconcileReadyStateLocked | 状态机转换失败只 Warn |
| `node_router.go:62-67` WithEventBus | nil router 静默 return nil |
| `node_router.go:77-79` spawnReplanPlanner | nil dispatcher 静默返 false |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `helpers.go:353-370` | Publish 在持锁期间（TODO 标注的死锁风险） |
| `helpers.go:267-310` startProcessLocked | exitMonitor nil 时不阻止启动 |
| `node_router.go:41-60` | store 无 nil 校验 |
| `helpers.go:95-133` startTurnExecution | void 返回值 |
| `helpers.go:372-385` listAgents | 持锁深拷贝 |

## 修复优先级

### P0（必须本周修）
1. **`helpers.go:353-370` fireAndPublishLocked Publish 在持锁期间**——代码作者已在 TODO 中明确标注死锁风险。当前 kelindar/event 异步投递暂时安全，但这是**已知的定时炸弹**。一旦 event 库升级或 subscriber 行为变化，整个 orchestration service 死锁。改为：收集 pending events → 释放锁 → 批量 publish。
2. **`helpers.go:300-306` exitMonitor nil 时静默跳过 Arm**——进程启动后不被监控 → crash 后 agent 永远 running。这与第47轮 exit_monitor P0（event 丢弃）是同一问题的另一面。改为 exitMonitor nil 时拒绝启动进程。

### P1（本月）
3. `helpers.go:313-315` ctx nil 改 panic
4. `helpers.go:235-237` turnStarter nil 改返 error
5. `node_router.go:41-60` store nil 校验
6. `node_router.go:62-67` WithEventBus nil router 改 panic
7. `helpers.go:372-385` listAgents 锁持有 duration 监控

### P2（下个 sprint）
8. `helpers.go:74-93` reconcileReadyStateLocked 加 metrics counter
9. `node_router.go:77-79` spawnReplanPlanner nil dispatcher 改 panic
10. `helpers.go:353-370` 长期方案：trigger channel 解耦

## 边界条件

1. **`helpers.go:353-370` fireAndPublishLocked 的 TODO 是项目内最危险的已知风险**：代码作者明确写了「如果 kelindar/event 的投递策略变更为同步，或 subscriber 在同一 goroutine 回调，将导致死锁」。这不是假设——Go 的 sync.Mutex 不可重入，任何 subscriber 回调 service 方法（如 `BindActiveTurnID`）都会尝试获取 s.mu → 死锁。当前安全仅因为 kelindar/event 用 goroutine 投递。**P0 因为这是已知的、有明确触发条件的定时炸弹**。
2. **`helpers.go:234-265` waitForSubmitSessionReady 是协程延迟监控的正面案例**：line 259 `elapsed >= longWaitLogThreshold` 打 Warn；line 246 在等待前打 Info 带 timeout 值；line 252 完成后打 duration。这是「每个阻塞点都有可观测性」的良好实践。建议推广到其他阻塞点（如 startProcessLocked、listAgents）。
3. **`helpers.go:267-310` startProcessLocked 的 rollback 设计**：line 283-294 如果 `commitLaunchSuccessLocked` 失败，会 ForceStop 进程 + Close guard + 清空 cmd。这是「启动失败回滚」的良好实践——不留半启动状态。但 line 284-286 ForceStop 失败只 Warn 不 return error——如果进程 Kill 失败，agent.cmd 被清空但进程仍在跑 → zombie。
4. **`node_router.go:76-103` spawnReplanPlanner 的设计意图**：DAG 节点失败后，如果 on_failure 策略是 "replan"，启动一个 planner agent 来修改 DAG 图。这是 self-healing DAG 的高级特性。但 planner agent 本身也可能失败——当前 line 99-101 `LaunchAgent` 失败走 `failSmartRetryPrepare`。如果 retry 也失败，节点永远 stuck。建议加 max-replan-attempts 限制。
5. **`helpers.go:372-385` listAgents 的深拷贝设计**：line 378-379 `snapshot.queue = nil; snapshot.sm = nil` 清空内部状态——防止 caller 通过 snapshot 修改 queue/sm。这是正确的防御性拷贝。但 `*agent` 的其他字段（如 cmd、processGuard）仍是 pointer——caller 理论上可以通过 snapshot.cmd 操作进程。建议 snapshot 类型改为 value-only struct（无 pointer 字段）。
6. **`helpers.go:95-133` startTurnExecution 的 duration 日志是正面案例**：line 96 `startedAt := time.Now()` + line 121/131 `time.Since(startedAt).Milliseconds()` 在成功和失败路径都记录耗时。这是「每个外部调用都有 duration」的良好实践。

---

**本轮总结**：发现 2 个 P0 问题：①fireAndPublishLocked 在持锁期间 Publish 是代码作者已标注的定时炸弹（死锁风险）；②exitMonitor nil 时静默跳过 Arm 让进程 crash 后 agent 永远 running。`waitForSubmitSessionReady` 和 `startTurnExecution` 的 duration 日志是协程延迟监控正面案例。`startProcessLocked` 的 rollback 设计是「启动失败回滚」的良好实践。

**累计进度**：51 轮完成。cron `fd4b4728` 继续推进。
