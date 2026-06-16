# Round 016 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:00:11 KST
- 结束：2026-05-17 06:02:06 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查长任务进度协议、task handoff 预检和 shared_file 写入策略，重点看 progress/done 这些量化信号是否能被 agent 正确写入、被前端正确读取，并且与续接任务的 handoff 文件一致。

- `cmd/agent-terminal/frontend/vue-app/composables/useThreadProgressProtocol.js`
- `cmd/agent-terminal/frontend/vue-app/use-thread-progress-protocol.test.js`
- `cmd/agent-terminal/frontend/vue-app/composables/useTaskHandoff.js`
- `cmd/agent-terminal/frontend/vue-app/use-task-handoff.test.js`
- `internal/module/thread/task_handoff.go`
- `internal/module/thread/task_handoff_render.go`
- `internal/module/thread/task_handoff_worker.go`
- `internal/module/thread/task_handoff_test.go`
- `internal/module/thread/task_handoff_worker_test.go`
- `internal/module/thread/promote_task_test.go`
- `internal/module/thread/rpc.go`
- `internal/module/thread/service_handlers_test.go`
- `internal/platform/sharedfilepath/policy.go`
- `internal/sidecar/orch/tools/shared_file_tools.go`
- `internal/sidecar/orch/tools/parity_v2_test.go`

## Findings

1. **[major] fork 前预检只用 taskId 推导默认 handoff 路径，忽略 runtime 中真实 handoffFile**
   - 证据：前端 `continueTaskById()` 从 runtime 解析 `task.handoffFile` 并把它传给新 thread 配置（`cmd/agent-terminal/frontend/vue-app/composables/useTaskHandoff.js:199-249`），但预检 RPC 只发送 `threadId` 和 `taskId`（`cmd/agent-terminal/frontend/vue-app/composables/useTaskHandoff.js:224-231`）。后端 `EnsureHandoffExists()` 直接用 `defaultTaskHandoffPath(taskID)` 查 `handoff/tasks/<taskId>.md`（`internal/module/thread/task_handoff.go:305-328`），`FlushAndVerifyTaskHandoff()` 也只收 `threadID, taskID`（`internal/module/thread/task_handoff.go:331-356`）。测试只覆盖默认路径存在/缺失（`internal/module/thread/task_handoff_test.go:287-332`）。
   - 风险：如果历史数据、导入数据或手工 runtime 使用了自定义 `handoffFile`，前端续接会把真实路径传给新线程，但预检却检查默认路径。默认路径不存在时会误判 `handoff_missing` 并阻止续接；默认路径存在但不是当前任务摘要时，又可能让过期或错误摘要通过。
   - 建议：`ui/task/flush_and_verify` 接收并校验 `handoffFile`，后端优先使用 runtime/请求中的 handoff path；同时确认该路径与 source thread 当前 runtime 一致。

2. **[major] handoff 文档要求“追加” progress 行，但 MCP `shared_file_write` 实际是覆盖写，行数增长信号很容易不可达**
   - 证据：handoff 模板要求 agent “向 `_internal/progress/<taskId>.md` 追加一行”（`internal/module/thread/task_handoff_render.go:152-160`）。前端进度协议用非空行数增长判断任务仍在推进（`cmd/agent-terminal/frontend/vue-app/composables/useThreadProgressProtocol.js:31-58`），watchdog 在行数增长时重置累计上限（`cmd/agent-terminal/frontend/vue-app/composables/useThreadWatchdog.js:83-143`）。但 MCP 工具定义和实现是 `shared_file_write` 覆盖写 `content` 到路径，没有 append 参数（`internal/sidecar/orch/tools/shared_file_tools.go:47-56`、`internal/sidecar/orch/tools/shared_file_tools.go:79-105`）。
   - 风险：agent 按工具能力直觉每次写一行时会覆盖旧内容，前端看到的非空行数长期为 1，不会增长。长任务明明持续上报进度，watchdog 仍可能累计到 5 次后停止自动戳并提示人工介入。
   - 建议：新增 `shared_file_append` 或给 `shared_file_write` 增加 append mode；如果暂不做，协议应改成读取版本号/updated_at/内容 hash 变化，而不是行数增长。

3. **[moderate] `_internal/progress/<taskId>.md` 和 `_internal/done/<taskId>.md` 直接拼接 taskId，缺少针对路径分隔符的约束**
   - 证据：前端 `buildProgressPath()` / `buildDonePath()` 直接拼接原始 taskId（`cmd/agent-terminal/frontend/vue-app/composables/useThreadProgressProtocol.js:20-29`）。后端 handoff 模板同样直接把 `taskID` 拼入 `_internal/progress/` 和 `_internal/done/`（`internal/module/thread/task_handoff_render.go:152-160`）。默认新 taskId 来自 `idgen.NewID("task")`，但继承或 runtime 配置可通过 `taskHandoffMetaFromRuntimeConfig()` 接收字符串（`internal/module/thread/task_handoff.go:274-281`），未在 progress/done 协议层限制 `/`、`..` 或过长名称。
   - 风险：正常默认 id 安全，但旧数据或外部配置中的 taskId 如果包含斜杠，会把一个逻辑任务映射到嵌套路径；如果包含路径清理敏感字符，读写端和文档端可能对同一个 task 形成不同路径理解。进度量化信号依赖路径唯一性，这里缺少显式不变量。
   - 建议：生成 progress/done path 时使用与 `defaultTaskHandoffPath()` 类似的 `path.Base` 或新增 taskId sanitizer，并在 runtime 入口测试带 `/`、`\`、`..` 的 taskId。

4. **[moderate] task handoff worker 刷新失败后丢弃 pending seed，不重排也不保留失败状态**
   - 证据：worker `drainPending()` 先把 pending map 清空，再逐个调用 `refreshTaskHandoffFromThread()`；失败只记录 warn，仍然增加 `processedTotal`，没有把 entry 放回 pending（`internal/module/thread/task_handoff_worker.go:182-209`）。`FlushForThread()` 对 pending entry 同样先 delete，刷新失败后直接返回错误，不恢复 pending（`internal/module/thread/task_handoff_worker.go:219-240`）。测试锁定了 refresher error 被传播，但没有检查 pending 是否保留或重试（`internal/module/thread/task_handoff_worker_test.go:311-320`）。
   - 风险：临时磁盘/DB 故障会永久丢掉最新 turn 的 handoff seed。之后 fork 前预检只确认文件存在，不确认是否包含最新 outcome；如果旧 handoff 文件存在，续接线程会读取陈旧摘要，用户以为 flush 已保证“最新”但实际只保证“存在”。
   - 建议：刷新失败时保留 pending 并带退避重试，或在 handoff 文件中记录 last turn id / updated_at，预检校验该版本不早于触发 fork 的 source turn。

5. **[moderate] progress/done 读失败被降级为 0/false，watchdog 会把基础设施故障误当作“无进度”**
   - 证据：`readProgressLineCount()` 对非 NotFound 错误只 `logWarn` 并返回 0（`cmd/agent-terminal/frontend/vue-app/composables/useThreadProgressProtocol.js:44-58`）；`readDoneMarker()` 对非 NotFound 错误只 `logWarn` 并返回 false（`cmd/agent-terminal/frontend/vue-app/composables/useThreadProgressProtocol.js:61-75`）。对应测试明确锁定“其它错误返回默认值并降级”（`cmd/agent-terminal/frontend/vue-app/use-thread-progress-protocol.test.js:47-55`、`cmd/agent-terminal/frontend/vue-app/use-thread-progress-protocol.test.js:93-100`）。
   - 风险：shared-file RPC 断开或权限失败时，watchdog 不知道读侧基础设施坏了，会继续走旧累计上限并发送“继续”。这会把系统故障量化成任务停滞，可能导致不必要的自动戳、累计封顶或人工介入提示。
   - 建议：progress protocol 返回 `{count, errorKind}` / `{done, errorKind}`，watchdog 对非 NotFound 错误暂停自动戳或展示“进度读取失败”，不要与“没有进度文件”混为一谈。

## 误报与已覆盖项

- `_internal/` 不是 agent 写入禁区；`ValidateAgentWritePath()` 允许 `_internal/`，只禁止 `handoff/tasks/`（`internal/platform/sharedfilepath/policy.go:44-90`）。因此本轮不报告 progress/done 完全不可写。
- `shared_file_write` 已有 10MiB 内容上限和系统 handoff 路径保护（`internal/sidecar/orch/tools/shared_file_tools.go:79-105`、`internal/sidecar/orch/tools/parity_v2_test.go:260-300`），本轮不报告无限大写入或覆盖系统 handoff。
- `FlushAndVerifyTaskHandoff` 的 RPC 错误关键字已通过 handler 测试覆盖（`internal/module/thread/service_handlers_test.go:493-537`），本轮风险集中在“校验对象是否正确”和“校验是否足够新”。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/thread -count=1
cd cmd/agent-terminal/frontend
npx vitest run use-thread-progress-protocol.test.js use-task-handoff.test.js use-thread-watchdog.test.js
```

结果：Go guard、`internal/archtest` 与 `internal/module/thread` 通过；前端 size guard 通过，3 个 vitest 文件共 74 个测试通过。

## 下一轮建议

- Round 017 审查 orchestration task DAG 的节点状态、lease、ready/completed 量化逻辑，重点看并发 worker、过期 lease 和下游解锁是否会造成重复执行或永久卡住。
