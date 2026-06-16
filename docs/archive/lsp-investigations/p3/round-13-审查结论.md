# 第 13 轮审查结论

## 审查范围

- `internal/module/turn/service.go`（turn service 构造、PrepareTurn、StartTurn、SteerTurn、ForceCompleteTurn、resolveBinaryDir）
- `internal/module/turn/service_helpers.go`（ensureLocalTurnID、waitForHandle、syntheticMemoryContext）
- `internal/module/turn/tracker.go`（turnTracker、inMemoryTurnTrackerStore、Start/Update/Complete/Stall/Cleanup/ActiveByThread/AbortThread）
- `internal/module/turn/interrupt_service.go`（InterruptTurn、finishInterrupt、timeoutInterruptStatus）

> 与第 12 轮覆盖的 `tool_result_storage/budget/lifecycle` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `service.go:59-61` `NewService` | 弱契约 | logger=nil 兜底全局 | 调用方传 nil 是 bug | panic |
| `service.go:86-120` `newService` | 兜底 | logger nil 兜底；manifestBuild nil 兜底 `contract.BuildManifest`；turnContextProvider nil 不设置 | 多重兜底掩盖装配错误 | logger/manifestBuild nil 应 panic；turnContextProvider nil 是合法 optional |
| `service.go:122-131` `resolveBinaryDir` | 兜底+静默 | `resolvePeerBinDir()` 返回 "" 时 fallback 到 `os.Executable` 的 dir；`os.Executable` 失败返回 "" | 二进制目录解析失败完全无日志；后续 manifest builder 用空字符串构造 MCP binary 路径 | 至少 Warn；空字符串应 error |
| `service.go:133-142` `resolvePeerBinDir` | 兜底 | 多候选目录中找不到 managed binary 时 fallback 到 `dirs[0]` | dirs[0] 可能不含 mcp-lsp/mcp-orch；后续 manifest 引用不存在的 binary | 找不到 managed binary 应 Warn + 返回 "" 让上层 fallback 到 os.Executable |
| `service.go:167-175` `hasManagedPeerBinary` | 静默 | `os.Stat` 失败静默 continue | 权限错误被当成"不存在" | 区分 NotExist 与其它 |
| `service.go:231-237` `cleanupStaleToolResults` | 静默 | `s == nil \|\| s.logger == nil \|\| result.Cleared == 0` 时不 log | nil service 是 bug；logger nil 是 bug | nil service panic |
| `service.go:254-289` `StartTurn` | 兜底 | `handle == nil` 时构造 error + Complete + return | 这是正确的 fail-fast；但 `session.StartTurn` 返回 nil handle 是 session 实现 bug，应该在 session 层 panic | 当前处理合理；但应在 session 接口文档中标注"handle must not be nil" |
| `service_helpers.go:13-18` `ensureLocalTurnID` | 兜底 | 空 localID 时自动生成 | 调用方传空是 bug（PrepareTurn 已经生成了 localID）；StartTurn 再兜底等于掩盖 | 空 localID 应 error |
| `service_helpers.go:28-42` `waitForHandle` | 兜底 | `handle == nil` 返回 nil | nil handle 是调用方 bug | panic 或 error |
| `service_helpers.go:48-71` `syntheticMemoryContext` | 兜底 | `s == nil \|\| s.turnContextProvider == nil` 返回零值 payload | nil service 是 bug；turnContextProvider nil 是合法 optional | nil service panic |
| `tracker.go:150-173` `Start` | 兜底 | `localID == ""` 直接 return | 空 localID 是调用方 bug | panic 或 error |
| `tracker.go:175-187` `AttachHandle` | 兜底 | `localID == "" \|\| handle == nil` 直接 return | 同上 | 同上 |
| `tracker.go:189-198` `BindProviderID` | 兜底 | `localID == "" \|\| providerID == ""` 直接 return | 同上 | 同上 |
| `tracker.go:210-222` `Update` | 兜底+静默 | `localID == "" \|\| trigger == ""` 直接 return；state machine fire 失败仅 Warn | 空 localID 是 bug；fire 失败说明状态转换非法，仅 Warn 不够 | 空 localID panic；fire 失败应 return error（当前 Update 无返回值） |
| `tracker.go:224-254` `Complete` | 兜底+静默 | `localID == ""` 直接 return；fire 失败仅 Warn | 同上 | 同上 |
| `tracker.go:256-272` `MarkInterruptRequested` | 兜底 | `localID == ""` 返回 false；fire 失败静默（不 Warn） | fire 失败时 `interrupted` 保持 false，调用方以为中断未成功 | fire 失败至少 Warn |
| `tracker.go:274-287` `Stall` | 兜底+静默 | 同 Update | 同上 | 同上 |
| `tracker.go:289-294` `Cleanup` | 兜底 | 无 nil receiver 校验（`t.store.DeleteMatching` 会 panic 如果 store 为 nil） | 如果 tracker 构造异常 store=nil，Cleanup 会 panic | 入口校验 |
| `tracker.go:296-320` `ActiveByThread` | 兜底 | `threadID == ""` 返回 `(zero, false)` | 空 threadID 是调用方 bug | error 或 panic |
| `tracker.go:322-342` `AbortThread` | 兜底+静默 | `threadID == ""` 返回 false；fire 失败静默 | 同上 | 同上 |
| `interrupt_service.go:11-27` `InterruptTurn` | 兜底 | `!tracked` 时返回带 interrupt envelope 的零值 status + nil error | 没有 active turn 时"中断成功"是合理的语义（幂等）；但 envelope 中 `interrupted=false` 让调用方需要解析 envelope 才知道实际没中断 | 当前合理；但应在 API 文档中标注 |
| `interrupt_service.go:43-68` `finishInterrupt` | 兜底 | `waitForTurnSettle` 失败时走 `timeoutInterruptStatus`；如果不是 DeadlineExceeded 则返回 `(TurnStatus{}, err)` | 非 timeout 错误（如 ctx canceled）返回零值 status + error；调用方拿到零值 status 无法判断中断是否部分成功 | 非 timeout 错误也应返回 before status 作为 fallback |
| `interrupt_service.go:81-97` `timeoutInterruptStatus` | 兜底 | `ctx.Err() != nil` 时返回 `(zero, false)` 让上层走 error 路径 | 合理 | OK |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `service.go:122-131` | resolveBinaryDir os.Executable 失败返回 "" |
| `service.go:133-142` | resolvePeerBinDir 找不到 managed binary fallback dirs[0] |
| `service.go:167-175` | hasManagedPeerBinary stat 失败静默 |
| `service.go:231-237` | cleanupStaleToolResults nil/logger nil 静默 |
| `tracker.go:150-173` | Start localID="" 静默 return |
| `tracker.go:175-198` | AttachHandle/BindProviderID 空参数静默 |
| `tracker.go:210-287` | Update/Complete/Stall fire 失败仅 Warn |
| `tracker.go:256-272` | MarkInterruptRequested fire 失败不 Warn |
| `tracker.go:322-342` | AbortThread fire 失败静默 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `service.go:59-84` | 三个 NewService* 构造函数，参数组合不同但都不做 nil 校验 |
| `service.go:86-120` | newService 内部 logger/manifestBuild nil 兜底 |
| `service.go:122-175` | resolveBinaryDir 多层 fallback |
| `service_helpers.go:13-18` | ensureLocalTurnID 空值自动生成 |
| `service_helpers.go:28-42` | waitForHandle nil handle 返回 nil |
| `tracker.go:146-148` | newTurnTracker 用全局 logger |
| `tracker.go:150-342` | 所有 tracker 方法对空 localID/threadID 静默 return |
| `tracker.go:53-57` `Put` | localID 不做 trim/校验 |
| `tracker.go:59-67` `Mutate` | fn=nil 时 fn(turn) 会 panic |
| `tracker.go:70-76` `RangeMut` | fn=nil 时 fn(id, turn) 会 panic |
| `interrupt_service.go:43-68` | finishInterrupt 非 timeout 错误返回零值 status |

## 修复优先级

### P0（必须本周修）
1. `tracker.go:150-342` 所有 tracker 方法对空 localID/threadID 的静默 return 改为 panic（这些是调用方 bug，不是合法输入）
2. `service.go:122-131` resolveBinaryDir 空字符串返回应 error/Warn，不能让 manifest builder 用空路径
3. `tracker.go:210-287` state machine fire 失败应升级为 error 返回（需要改 Update/Complete/Stall 签名为返回 error）
4. `service_helpers.go:13-18` ensureLocalTurnID 空值改 error（StartTurn 调用方已经生成了 localID）

### P1（本月）
5. `service.go:86-120` newService logger nil 改 panic
6. `service.go:167-175` hasManagedPeerBinary stat 错误区分 NotExist 与其它
7. `service_helpers.go:28-42` waitForHandle nil handle 改 panic
8. `tracker.go:59-76` Mutate/RangeMut fn=nil 入口校验
9. `interrupt_service.go:43-68` finishInterrupt 非 timeout 错误也返回 before status
10. `tracker.go:256-272` MarkInterruptRequested fire 失败加 Warn

### P2（下个 sprint）
11. `service.go:59-84` 三个 NewService* 统一为一个 builder/options 模式
12. `tracker.go:146-148` newTurnTracker 接受 logger 参数
13. `service.go:133-142` resolvePeerBinDir 找不到 managed binary 时不 fallback dirs[0]
14. `service.go:231-237` cleanupStaleToolResults nil service panic
15. `tracker.go:53-57` Put localID trim + 校验

## 边界条件

1. **tracker 方法签名改 error 返回是 breaking change**：`Update`、`Complete`、`Stall` 当前无返回值，所有调用点都是 fire-and-forget。改为返回 error 后需要逐个调用点决定如何处理。建议分两步：先加 error 返回但调用点暂时 `_ = tracker.Update(...)`，再逐步收紧。
2. **`ensureLocalTurnID` 的兜底是为了兼容旧 API**：`StartTurn` 的 `req.LocalID` 可能来自外部 RPC 调用方（如 Codex provider），他们可能不传 localID。改 error 前要确认所有 StartTurn 调用方是否都保证 localID 非空。`PrepareTurn` 已经生成了 localID，但 `StartTurn` 也可以被独立调用。
3. **`resolveBinaryDir` 返回空字符串的影响**：`newManifestBuilder("")` 会用空 binDir 构造 MCP binary 路径。后续 `filepath.Join("", "mcp-lsp")` 会得到 `"mcp-lsp"`（相对路径），exec.Command 会在 PATH 中查找。这实际上是一个**合理的 fallback**（PATH 查找）。改 error 前要确认是否真的需要绝对路径。
4. **state machine fire 失败的语义**：fire 失败意味着当前状态不允许该转换（如 "completed" 状态再 fire "complete"）。当前 Warn + 继续是为了处理竞态（如 provider 发了 completed 但本地已经 interrupted）。改 error 后调用方需要区分"非法转换"（bug）与"竞态重复"（正常）。建议：fire 失败返回 error，但调用方对已知竞态场景（如 Complete 时已 terminal）做 `errors.Is` 判断。
5. **`waitForHandle` nil handle 的调用场景**：`ForceCompleteTurn` 中 `active.handle` 可能为 nil（turn 已经 complete 但 tracker 还没清理）。改 panic 会让这个合法场景崩溃。建议保留 nil → return nil 但加 debug log。
6. **`ActiveByThread` 返回最新 updatedAt 的 turn**：如果同一 thread 有多个非 terminal turn（理论不应该），返回最新的。这是合理的 last-writer-wins 语义。
7. **`InterruptTurn` 的幂等语义**：没有 active turn 时返回"成功"是 API 设计决策（幂等中断）。改为 error 会破坏前端的"连续点击中断按钮"体验。保持当前行为。
8. **`tracker.go:289-294` Cleanup 的 nil store panic**：`newTurnTracker` 总是初始化 store，所以生产路径不会 nil。但如果有人直接构造 `&turnTracker{}` 就会 panic。加 nil 校验是 defensive。

---

下一轮范围建议：
- `internal/module/turn/prompt_context.go` + `prompt_assembly.go`（prompt 构建）
- `internal/module/turn/manifest.go`（MCP manifest 构建）
- `internal/module/turn/skills.go` + `skill_evaluator.go` + `skill_extractor.go`（skill 解析）
- 或切换到 `internal/contract/`（核心接口定义）
