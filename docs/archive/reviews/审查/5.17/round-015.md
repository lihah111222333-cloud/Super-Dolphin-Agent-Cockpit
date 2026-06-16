# Round 015 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 05:58:19 KST
- 结束：2026-05-17 06:05:31 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 auto-continue、thread watchdog、持久化和长任务进度协议接线，重点看 token/status/stall 这些量化状态是否存在漏触发、误触发、计数污染或用户停止意图失效。

- `cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js`
- `cmd/agent-terminal/frontend/vue-app/composables/auto-continue-gating.js`
- `cmd/agent-terminal/frontend/vue-app/composables/useThreadWatchdog.js`
- `cmd/agent-terminal/frontend/vue-app/composables/thread-watchdog-gating.js`
- `cmd/agent-terminal/frontend/vue-app/composables/useAutoContinueStatePersistence.js`
- `cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js`
- `cmd/agent-terminal/frontend/vue-app/use-auto-continue.test.js`
- `cmd/agent-terminal/frontend/vue-app/use-thread-watchdog.test.js`
- `cmd/agent-terminal/frontend/vue-app/thread-watchdog-gating.test.js`
- `cmd/agent-terminal/frontend/vue-app/auto-continue-e2e.test.js`

## Findings

1. **[major] auto-continue 只监听状态跃迁，启动时已经 critical/error 的任务不会被自动处理**
   - 证据：初始化时 `primeState()` 把当前 token level 和 status 写进 `prevLevelByThread` / `prevStatusByThread`（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:368-376`）。后续 watcher 只有在 `prev !== newLevel` 且新 level 为 `critical` 时触发 token 续接（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:314-338`），status 也只有从非 error 跃迁到 error 才触发 recover/fork（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:341-365`）。现有测试还把“already critical 不再触发”锁定为行为（`cmd/agent-terminal/frontend/vue-app/use-auto-continue.test.js:96-105`）。
   - 风险：页面刷新、组件重挂载、偏好从未 ready 到 ready、或持久化状态恢复时，如果任务已经处于 critical/error，自动续接不会介入。量化状态明明已经越界，但调度器把它当作基线，用户只能等下一次先跌出再跨入的变化。
   - 建议：在 `prefReady` 变为 true、组件 mount 后、或 `agentRuntimeById` 补齐 taskId 后执行一次显式扫描；扫描只处理 task thread，并复用现有 gate/inflight，避免重复 fork。

2. **[major] compact 成功后不校验 token level 是否退出 critical，可能把仍然超限的任务标为已处理**
   - 证据：`handleTokenCritical()` 在 compact 能力存在时调用 `tryCompact()`，只要返回 `ok` 就直接 `return`（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:196-208`）。只有 compact 失败后才重新检查当前 level 是否仍为 critical（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:209-215`）。测试也只断言“compact success -> no fork”，没有验证 token 降级（`cmd/agent-terminal/frontend/vue-app/use-auto-continue.test.js:111-123`）。
   - 风险：后端 compact RPC 成功不等价于上下文真实降到安全区。如果压缩效果不足、tokenUsage patch 延迟或统计口径不变，watcher 不会再触发，因为 level 仍是 critical 且 prev 已经是 critical。最终表现是系统记录 compact done，但任务继续处于不可恢复的高占用状态。
   - 建议：compact 成功后等待一次 tokenUsage 刷新或主动读取最新 usage；若仍为 critical，应进入 fork 兜底或写入 failed 状态让用户手动决策。

3. **[moderate] manual abort 只抑制 status_error，不抑制 token_critical**
   - 证据：`handleStatusError()` 在 error 处理前检查 `manualAbortByThread` 并跳过（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:268-274`）。`handleTokenCritical()` 没有同等检查，只要 token level 为 critical 且 gate 允许，就会 compact/fork（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinue.js:196-230`）。测试覆盖了 manual abort 抑制 status error（`cmd/agent-terminal/frontend/vue-app/use-auto-continue.test.js:551-565`），没有覆盖 token critical 抑制。
   - 风险：用户主动 stop 后，如果 tokenUsage 后续被 patch 到 critical 或重新跨入 critical，系统仍可能自动 compact/fork，违背“用户主动停止后不自动续接”的意图。对长任务风控而言，停止动作只压住一条状态通道，另一条量化通道仍可触发。
   - 建议：`handleTokenCritical()` 在 gate 前复用 manual abort 检查；用户主动 retry、start/fork 成功或明确清除时再解除抑制。

4. **[moderate] thread watchdog 在进度协议判断前先记录 gate，done/progress-advanced 也会消耗节流和全局保险丝**
   - 证据：`processThread()` 先 `gate.check()`，随后立即 `gate.recordPoke()`，之后才读取 `taskId` 并调用 `pokeTaskThread()`（`cmd/agent-terminal/frontend/vue-app/composables/useThreadWatchdog.js:151-175`）。而有 progress protocol 时，`pokeTaskThreadWithProtocol()` 才调用 `applyProgressProtocol()`，done 命中会 `skip`，progress 增长会重置累计（`cmd/agent-terminal/frontend/vue-app/composables/useThreadWatchdog.js:83-143`）。gate 本身记录 per-thread 60s 和全局 5min/10（`cmd/agent-terminal/frontend/vue-app/composables/thread-watchdog-gating.js:40-63`）。
   - 风险：一个实际已经 done 或持续写 progress 的任务，本来不应发送“继续”，却仍然被计入 watchdog poke。结果是 60 秒内真实卡住时会被 `thread_throttled` 挡住，大量正常推进任务也可能把 5 分钟全局保险丝耗尽，造成后续真正卡住的任务无法自动恢复。
   - 建议：把 `recordPoke()` 移到确认将要发送“继续”之后；或为 progress/done skip 提供 gate release/rollback，确保节流计数只代表真实 poke。

5. **[moderate] 用户清除 stuck 状态不触发持久化删除，刷新后可能恢复旧的累计上限计数**
   - 证据：watchdog 的持久化快照只包含 `watchdogPokeCount`（`cmd/agent-terminal/frontend/vue-app/composables/useAutoContinueStatePersistence.js:121-140`），UnifiedChatPage 从 `cumulativePokeCountByThread` 读取该值（`cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js:422-430`）。`resetCumulativePokeCount()` 会删除累计计数并通知持久化（`cmd/agent-terminal/frontend/vue-app/composables/useThreadWatchdog.js:210-215`），但 `clearStuck()` 只删除 `stuckByThread`，不通知也不清 cumulative（`cmd/agent-terminal/frontend/vue-app/composables/useThreadWatchdog.js:216-219`）。手动“重试卡住线程”路径只删除 stuck，不调用 reset 或持久化（`cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js:380-388`）。
   - 风险：用户点击普通重试后界面上 stuck 消失，但持久化文件中的 `watchdogPokeCount` 仍保留。刷新页面后累计计数会恢复，任务更容易直接撞上 cumulative limit，用户以为已经清除的卡住状态又以另一种形式回来。
   - 建议：所有用户确认处理 stuck 的入口都应调用统一的 `clearWatchdogState(threadId)`，同时删除 stuck、清累计、清 progress baseline 并触发持久化写空状态。

## 误报与已覆盖项

- watchdog 累计计数并非完全未持久化；`UnifiedChatPage` 已通过 `useAutoContinueStatePersistence` 接入 `watchdogPokeCount` 和 `manualAbortAt`（`cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js:418-456`）。
- watchdog 对 progress 增长会重置 cumulative count，这是已覆盖行为（`cmd/agent-terminal/frontend/vue-app/use-thread-watchdog.test.js:484-510`）。本轮风险只针对 gate 记录早于 progress 判断。
- auto-continue 对 `status_error` 已有 manual abort 抑制测试（`cmd/agent-terminal/frontend/vue-app/use-auto-continue.test.js:551-565`），本轮不重复报告 status_error 抑制缺失。

## 验证

```bash
cd cmd/agent-terminal/frontend
npx vitest run use-auto-continue.test.js auto-continue-e2e.test.js use-thread-watchdog.test.js thread-watchdog-gating.test.js use-auto-continue-state-persistence.test.js
```

结果：前端 size guard 通过；5 个 vitest 文件共 96 个测试通过。

## 下一轮建议

- Round 016 审查长任务进度协议和 task handoff 文件读写链路，重点看 done/progress 的路径、权限、not-found 处理和 taskId 绑定是否会误判任务进展。
