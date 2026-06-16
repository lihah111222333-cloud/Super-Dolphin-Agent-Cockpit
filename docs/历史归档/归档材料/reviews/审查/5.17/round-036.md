# Round 036 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:18:03 KST
- 结束：2026-05-17 08:33:16 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 persistent runtime rehydrate、persisted agent projection 与 process exit monitor，重点看 mcp-orch 重启后 agent/thread/DAG 节点状态是否能恢复，以及 exit event 丢失对状态机的影响。

- `internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go`
- `internal/sidecar/orch/orchestration/persistent_agents.go`
- `internal/sidecar/orch/orchestration/persistent_agents_test.go`
- `internal/sidecar/orch/orchestration/exit_monitor.go`
- `internal/sidecar/orch/orchestration/service.go`
- `internal/sidecar/orch/orchestration/service_launcher_bridge.go`

## Findings

1. **[major] persisted runtime rehydrate 只在 Submit/Archive 等入口惰性触发，ListAgents 仅显示投影不恢复运行态**
   - 证据：`ListAgents` 通过 `listPersistedAgentSnapshots()` 读取 persisted threads 并生成 snapshot（`internal/sidecar/orch/orchestration/persistent_agents.go:14-37`），测试覆盖 runtime empty 时仍能列出 persisted agent（`internal/sidecar/orch/orchestration/persistent_agents_test.go:14-45`）。真正 `ensureRuntimeForPersistedAgent()` 是在 service launcher bridge / Archive 路径触发，而不是列表投影触发（`internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go:14-43`）。
   - 风险：mcp-orch 重启后，UI 看到量化 agent 仍存在，但内部 runtime map 为空；如果没有下一次 Submit/Archive，DAG active 节点不会恢复执行控制，监控状态与可操作状态不一致。
   - 建议：启动时主动扫描可恢复 persisted bindings，或在 ListAgents 输出中标记 `runtime_rehydrated=false`。

2. **[major] rehydrate 一律把 agent runtime 设为 idle，丢失重启前 turn/DAG active 状态**
   - 证据：`newPersistedRuntimeAgent()` 固定 `state: agentdto.StateIdle`、`queue: &SubmissionQueue{}`，active turn 为空（`internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go:185-210`）。测试 `TestSubmitTurnRehydratesPersistedAgentRuntimeAfterPeerRestart` 只验证重建后能提交新 turn（`internal/sidecar/orch/orchestration/persistent_agents_test.go:151-187`）。
   - 风险：重启前若量化 DAG agent 正在执行节点，重启后 runtime 看起来 idle，可能接受新 turn 或被错误停止；DAG node 的 active_turn/active_wakeup 也不会自动恢复。
   - 建议：rehydrate 时读取 persisted active turn、DAG running node、wakeup 状态，恢复为 turn_running/awaiting_user_input 或标记需要人工恢复。

3. **[major] rehydrate 只支持 codex provider，其他 provider 的 persisted thread 会显示但不可恢复**
   - 证据：`loadPersistedRuntimeSource()` 只接受 `provider == "codex"`，否则返回 `unsupported_provider`（`internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go:119-143`）。List projection 对 provider 没有同等限制（`internal/sidecar/orch/orchestration/persistent_agents.go:85-127`）。
   - 风险：Claude 或其他 provider 的量化 agent 在重启后仍可能显示在列表中，但无法恢复 runtime；用户提交或停止时行为不一致。
   - 建议：ListAgents 标记 provider recovery capability；不支持恢复的 provider 在重启后应转 stopped/needs_relaunch，而不是 idle。

4. **[major] process exit monitor 事件通道满后会丢 exit event，agent 状态可能永久不收敛**
   - 证据：`processExitMonitor` events channel 大小为 32；`publishExit()` 在满时最多阻塞 `publishBlockTimeout=5s`，超时后只 log error 并丢事件（`internal/sidecar/orch/orchestration/exit_monitor.go:46-55`、`internal/sidecar/orch/orchestration/exit_monitor.go:109-135`）。
   - 风险：大量量化 agent 同时退出或 runner 卡住时，exit event 丢失会让 runtime state 停在旧状态，stop/archive/recover 决策都可能基于过期状态。
   - 建议：exit events 使用无界/持久化队列或至少设置 dropped counter；丢事件后启动 reconciliation 扫描 cmd/process 状态。

5. **[moderate] persisted thread 状态 `created/running` 都投影为 idle，pending_launch 才投影 provisioning**
   - 证据：`persistedThreadAgentState()` 将空、created、running 都映射为 idle，只有 `PendingLaunch` 才 provisioning（`internal/sidecar/orch/orchestration/persistent_agents.go:113-127`）。
   - 风险：持久化层如果记录 running，UI 仍显示 idle，用户可能误以为量化 worker 可接新任务。
   - 建议：保留 running/created 的区别；若 runtime 不在本进程，显示 `detached_running` 或 `unknown`。

## 误报与已覆盖项

- rehydrate 对 archived binding 和 stopped/failed persisted thread 会跳过，避免恢复明确不可用对象（`internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go:127-129`、`internal/sidecar/orch/orchestration/persistent_runtime_rehydrate.go:171-183`）。
- remote Codex thread rehydrate 后可以把下一次 SubmitTurn 发回同一 provider thread，测试覆盖该 happy path（`internal/sidecar/orch/orchestration/persistent_agents_test.go:151-187`）。
- exit monitor 对同一 `(agentID, launchSeq)` 有 exactly-once fence，重复 wait/emit 不会重复推进（`internal/sidecar/orch/orchestration/exit_monitor.go:109-152`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration -count=1
```

结果：通过。

## 下一轮建议

- Round 037 审查 SubmitTurn、TurnStarted/Completed 状态机与 pending queue，重点看重启/恢复后新 turn 与 DAG turn 的串行化。
