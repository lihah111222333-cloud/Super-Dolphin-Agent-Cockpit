# Round 057 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:26:05 KST
- 结束：2026-05-17 08:30:54 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 cron 首次触发 bootstrap、`TurnServiceAdapter`、`ThreadServiceBootstrapper`、thread/turn contract adapter。重点看空 `thread_id` 的量化任务首次执行时，thread/agent 绑定、运行配置、dedupe 与 turn 上下文是否一致。

- `internal/module/cron/turn_adapter.go`
- `internal/module/cron/thread_bootstrapper.go`
- `internal/module/cron/module.go`
- `internal/module/cron/scheduler.go`
- `internal/module/cron/turn_adapter_test.go`
- `internal/module/cron/turn_adapter_shard11_helpers_test.go`
- `internal/module/thread/contract_adapter.go`
- `internal/module/thread/contract.go`
- `internal/module/turn/contract.go`
- `internal/contract/cron.go`
- `internal/provider/unified/session_resolver.go`

## Findings

1. **[major] 首次 bootstrap 得到的 agent_id 没有进入本次 `CronPrepareTurn`**
   - 证据：`StartTurn()` 调 `resolveThreadAgent()` 得到 `agentID`，但随后 `executeTurn(ctx, session, req)` 仍把原始 `req` 传入（`internal/module/cron/turn_adapter.go:87-103`）。`buildPrepareInput()` 从 `req.AgentID` 填 `CronPrepareInput.AgentID`（`internal/module/cron/turn_adapter.go:238-255`），而 bootstrap 产生的新 `agentID` 只在最终 `StartTurnResult` 写回（`internal/module/cron/turn_adapter.go:106-127`；`internal/module/cron/scheduler.go:371-399`）。
   - 风险：空 `thread_id` 的首轮量化任务会创建新 agent/thread，但 PrepareTurn 仍看到空或旧 agent_id。依赖 agent_id 的 prompt/memory/权限/观测上下文可能按错误身份装配；写回 DB 时又变成 bootstrap agent，形成“执行上下文”和“持久化归属”不一致。
   - 建议：bootstrap 后构造更新过 `ThreadID/AgentID` 的 request 传给 `executeTurn`，并加测试断言首次 bootstrap 的 `CronPrepareInput.AgentID` 是新 agent_id。

2. **[major] `ThreadServiceBootstrapper` 不能传递 cron prompt/skills/notify 等任务语义，首次线程像空壳线程启动**
   - 证据：`BootstrapRequest` 只有 `JobID/Provider/Model/CWD/Name/Config`（`internal/module/cron/scheduler.go:127-134`），`bootstrapFirstRun()` 也只投影这些字段且 `Name` 固定为 `JobID`（`internal/module/cron/turn_adapter.go:157-168`）。`ThreadServiceBootstrapper` 再转成 `CronStartThreadRequest`，仍不含 prompt、skills、notify 或 schedule metadata（`internal/module/cron/thread_bootstrapper.go:48-74`）。
   - 风险：thread/start 的路由、命名、prompt snapshot 和运行态配置只能看到 job id/name，而不是量化任务的真实 prompt/skills。第一轮 turn 虽然随后会 StartTurn，但 thread 级别的 prompt snapshot、sidebar name、路由元数据可能和实际任务脱节。
   - 建议：bootstrap request 应携带 job name、prompt、skills、schedule metadata，或明确以 deferred spawn/first-turn 输入作为 thread 路由依据。

3. **[major] bootstrap eager start 固定 `DeferSpawn=false`，无法复用 pending_launch 的按首个真实输入路由机制**
   - 证据：`ThreadServiceBootstrapper` 注释明确 `CronStartThread` 是 eager，`DeferSpawn=false`（`internal/module/cron/thread_bootstrapper.go:14-20`）。而 thread `StartRequest.DeferSpawn` 是专门用于先写 pending thread、再在 first turn 用真实输入分类路由（`internal/module/thread/contract.go:158-164`）。cron 的 `CronStartThreadRequest` contract 没有 `DeferSpawn` 或 first-turn prompt 字段（`internal/contract/cron.go:24-39`）。
   - 风险：cron 首次任务无法用真实量化 prompt 做路由分类，只能在空/弱语义 thread.start 阶段启动 provider CLI。复杂量化任务可能落到默认 persona/工具配置，和用户期望的任务角色不一致。
   - 建议：要么 cron bootstrap 显式传 prompt 给 thread/start 做路由，要么走 pending_launch 并在 `CronStartTurn` 前 `SpawnIfNeeded`，确保首轮输入参与路由。

4. **[moderate] bootstrap 成功但 `ResolveSession` 失败会创建孤儿 thread/agent，job 不记录 thread_id**
   - 证据：`resolveThreadAgent()` 在 threadID 为空时先调用 `bootstrapFirstRun()`，拿到 threadID 后再 `resolver.ResolveSession()`；resolver 失败直接返回错误（`internal/module/cron/turn_adapter.go:106-127`）。只有 `StartTurn` 完整成功后，scheduler 才通过 `SetRunTurn/SetActiveTurn` 写回 thread/agent（`internal/module/cron/scheduler.go:371-399`）。
   - 风险：如果 thread start 已成功持久化，但 resolver 因 binding/session 竞态或 auto-resume 失败返回错误，cron job 仍没有 thread_id；下一次 retry 会再次 bootstrap 新 thread，留下孤儿量化线程/agent。
   - 建议：bootstrap 成功后应先以独立事务记录 job.thread_id/agent_id，或提供可按 job_id 查回已 bootstrap 线程的幂等键。

5. **[moderate] `decodeRuntimeConfig` 对损坏 config 静默返回 nil，运行期可能退回默认配置**
   - 证据：turn adapter 的 `decodeRuntimeConfig()` 遇到 JSON unmarshal 错误直接返回 nil（`internal/module/cron/turn_adapter.go:258-273`）。相对地，bootstrap config 解码会返回错误（`internal/module/cron/thread_bootstrapper.go:77-91`）。
   - 风险：如果 cron row 的 config 被迁移、手工修复或历史数据污染为非法 JSON，首次/后续 turn prepare 会失去 codexHome/modelProvider 等 runtime override，可能跑到默认实例；同时没有错误暴露给 job/run 状态。
   - 建议：运行期 config 损坏应 fail closed，把 run 标为 failed/invalid_config，而不是在 turn prepare 侧静默降级。

## 误报与已覆盖项

- 无 bootstrapper 时会明确返回 `ErrJobNotBootstrapped`，scheduler 会走失败/重试路径；已有测试覆盖（`internal/module/cron/turn_adapter_test.go:513-520`）。
- `ThreadServiceBootstrapper` 会拒绝 malformed bootstrap config，不会默默丢弃 codexHome（`internal/module/cron/thread_bootstrapper.go:77-91`；`internal/module/cron/turn_adapter_test.go:583-592`）。
- dedupe key 能从 `StartTurnRequest` 传到 `CronPrepareInput`，已有测试覆盖（`internal/module/cron/turn_adapter_test.go:362-380`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/cron ./internal/module/thread ./internal/module/turn -count=1
```

结果：通过。

## 下一轮建议

- Round 058 审查 turn dedupe durable registry：`internal/store/turndedupe`、turn service registry 写入/终态标记、sweep 是否真实接入 cron，确认跨进程恢复是否会误判或重复提交。
