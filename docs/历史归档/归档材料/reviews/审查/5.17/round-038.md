# Round 038 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:18:43 KST
- 结束：2026-05-17 07:26:18 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 turn completion/interruption lifecycle、hook consumer 与 user input approval 事件，重点看量化任务终态事件是否能可靠匹配当前 turn，以及重复/乱序事件是否会掩盖真实异常。

- `internal/sidecar/orch/orchestration/turn_lifecycle.go`
- `internal/sidecar/orch/orchestration/hook_consumer.go`
- `internal/sidecar/orch/orchestration/factory.go`
- `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go`
- `internal/sidecar/orch/orchestration/turn_lifecycle_test.go`
- `internal/sidecar/orch/orchestration/user_input_test.go`
- `internal/dto/shared/event.go`
- `internal/dto/turn/event.go`

## Findings

1. **[critical] 空 turn_id 的 completed/interrupted 事件会匹配当前 active turn 并终结它**
   - 证据：`TurnIDHeader.TurnID` 是 `omitempty` 字段（`internal/dto/shared/event.go:86-95`）。completion handler 不校验 turn id 非空，直接调用 `CompleteTurn()`（`internal/sidecar/orch/orchestration/turn_lifecycle.go:22-41`）。`finalizeActiveTurnLocked()` 只在 `turnID != "" && activeTurnID != turnID` 时拒绝，因此空 turn_id 会绕过匹配检查并清掉当前 active turn（`internal/sidecar/orch/orchestration/factory.go:116-133`）。interruption 也走同一终态 finalize 逻辑（`internal/sidecar/orch/orchestration/turn_lifecycle.go:185-194`）。
   - 风险：量化 worker 若发出缺少 turn_id 的终态 hook，或旧 provider 事件丢字段，本地会把当前正在运行的 turn 标成完成/中断，导致 DAG 节点错误收敛。
   - 建议：对 completed/interrupted 强制要求非空 turn_id；仅在单 turn legacy 模式下允许空值，并需 session/thread fence。

2. **[major] force-idle recovery 先清 activeTurnID，再执行状态修复；修复失败会留下半更新 runtime**
   - 证据：`forceIdleAfterTurnTerminalLocked()` 在调用 `kind.recover` 前已经 `agent.activeTurnID = ""` 并写 `lastError/updatedAt`（`internal/sidecar/orch/orchestration/factory.go:136-158`）。completion/interruption 错误恢复都依赖该 helper（`internal/sidecar/orch/orchestration/turn_lifecycle.go:197-239`）。
   - 风险：如果状态机修复失败，active turn 已被清空但 state 可能仍在 `turn_running/awaiting_user_input`，后续事件既无法按 turn id 匹配，也可能被 reclaimer 误判为无 active turn。
   - 建议：先验证并完成状态转换，再原子清理 activeTurnID；或在失败时回滚 activeTurnID。

3. **[major] 终态“已收敛”判断不校验 turn_id 是否等于刚完成的 turn，可能压低错配事件等级**
   - 证据：`shouldIgnoreTurnLifecycleErr()` 对 `errTurnNotActive` 且 `turnTerminalConverged()` 为真时直接忽略（`internal/sidecar/orch/orchestration/turn_lifecycle.go:309-325`）。`turnTerminalConvergedLocked()` 只要求 activeTurnID 空、state idle，并且事件 turn_id 或 agent threadID 非空，不比较历史终结的 turn id（`internal/sidecar/orch/orchestration/turn_lifecycle.go:327-338`）。
   - 风险：量化 agent 先完成 turn-A 后，又收到 turn-B 的 late completion，系统会把它当作幂等收敛而不是 turn 错配；排查重复执行、provider 回放或 DAG active_turn 漂移时缺少告警。
   - 建议：记录 lastTerminalTurnID，并仅对相同 turn id 的重复终态降级；不同 turn id 应至少 warn 和计数。

4. **[major] hook 路径和独立 DAG subscriber 都可消费 TurnCompleted，重复来源依赖下游幂等而非入口去重**
   - 证据：hook consumer 解出 `turn.completed` 后既推进 runtime，又直接调用 `handleDAGTurnCompletedFromHook()`（`internal/sidecar/orch/orchestration/hook_consumer.go:219-222`、`internal/sidecar/orch/orchestration/hook_consumer.go:472-499`）。独立 subscriber 也订阅同一 `TurnCompleted` 类型并调用 `handleDAGTurnCompleted()`（`internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go:63-110`）。
   - 风险：同一量化 turn 如果同时通过 hook relay 和 event bus 投递，DAG 完成逻辑会运行两次；虽然部分 store 更新有幂等保护，但 sharedfile materialization、metrics 和日志可能出现重复或顺序反转。
   - 建议：为 TurnCompleted 引入 event id / source fence，在入口层去重；或者明确 hook 事件不再重复发布到 dispatcher。

5. **[moderate] 普通 kind=`tool` approval 被等同于 request_user_input，可能把非阻塞工具审批误投影为 awaiting_user_input**
   - 证据：`isRequestUserInputEvent()` 同时接受 `request_user_input` 和 `tool`，注释说明普通 tool approval 会被规范化为 `tool`（`internal/sidecar/orch/orchestration/turn_lifecycle.go:168-172`）。测试固定了 `Kind="tool"` 会进入 awaiting_user_input（`internal/sidecar/orch/orchestration/user_input_test.go:31-47`）。
   - 风险：量化 agent 的普通工具审批、命令卡审批或非交互 gate 也可能让 runtime state 显示为 awaiting_user_input，调度器误判 worker 卡在人审阶段。
   - 建议：approval kind 与 request_user_input 使用独立字段；至少要求 ToolName 或 ApprovalID 类型明确为 request_user_input。

## 误报与已覆盖项

- 对相同 turn 的重复 interrupted/completed 已有幂等测试，重复事件不会刷新 `updatedAt` 或覆盖 lastError（`internal/sidecar/orch/orchestration/turn_lifecycle_test.go:116-180`）。
- completion 从 `awaiting_user_input` 成功收敛时会先 resolve user input 再完成，避免合法审批中的 turn 被卡住（`internal/sidecar/orch/orchestration/turn_lifecycle.go:261-275`、`internal/sidecar/orch/orchestration/user_input_test.go:95-114`）。
- hook state-change 有 session fence，旧 session 的普通 state mirror 不会直接改当前 session（`internal/sidecar/orch/orchestration/hook_consumer.go:286-300`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration -count=1
```

结果：通过。

## 下一轮建议

- Round 039 审查 DAG TurnCompleted subscriber 与 node completion materialization，重点看输出物化、失败分类、重复 completion 与 downstream 推进。
