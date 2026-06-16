# Round 059 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:39:03 KST
- 结束：2026-05-17 08:47:56 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 turn watcher、provider terminal event、observation、trajectory collector 与 cron progress subscriber 的终态链路。重点看 provider handle 完成、bus terminal、local/provider turn id 映射是否同源。

- `internal/module/turn/service.go`
- `internal/module/turn/observation_wiring.go`
- `internal/module/turn/observation/subscribers.go`
- `internal/module/turn/observation/memory.go`
- `internal/module/turn/trajectory_collector.go`
- `internal/module/cron/progress_subscriber.go`
- `internal/provider/codexapp/session_dispatch.go`
- `internal/provider/codexapp/factory.go`
- `internal/provider/codexapp/event_map.go`
- `internal/provider/claudecli/session.go`
- `internal/provider/claudecli/session_receive_exit.go`
- `internal/provider/claudecli/factory.go`

## Findings

1. **[major] provider handle 完成不等于 bus terminal，cron run 可能收不到终态**
   - 证据：turn service 的 `watchTurn()` 只更新本地 tracker 与 dedupe registry：handle error 时 `tracker.Complete(...false)`，成功时 `tracker.Complete(...true)`，并调用 `recordDedupeTerminalForLocalID()`；没有发布 `TurnCompleted/TurnInterrupted/TurnStalled` bus 事件（`internal/module/turn/service.go:456-496`）。cron progress 只从 bus 订阅 `TurnCompleted` / `TurnInterrupted` 后调用 `Scheduler.CompleteTurn()`（`internal/module/cron/progress_subscriber.go:202-221`）。
   - 风险：如果 provider session 因 transport 关闭、recovery 失败或本地 stop 直接完成 handle，但没有对应 bus terminal，cron run 只会看到本地 handle 已结束，却不会进入 `CompleteTurn()`。量化任务状态可能停留在 submitted/running，后续依赖 lease/recovery 才能兜底，终态延迟且语义不确定。
   - 建议：turn service watcher 应在 provider 未发布 terminal 时补发规范化 terminal event，或 cron scheduler 应直接订阅 turn service tracker terminal；至少为“handle done without bus terminal”加指标和测试。

2. **[major] Codex `failTurns()` 关闭 handle 但不发 turn terminal，recovery 失败会留下调度终态黑洞**
   - 证据：Codex `failTurns()` 遍历 active turns 后只 `h.complete(err)`，没有 dispatch `turn/completed` 或 `turn/interrupted`（`internal/provider/codexapp/session_dispatch.go:182-195`）。`shutdownSession()` 在 graceful/force stop 前调用 `failTurns()`（`internal/provider/codexapp/factory.go:233-247`），`failRecovery()` 也调用 `failTurns()` 后仅 dispatch `connection.dead`（`internal/provider/codexapp/factory.go:276-305`）。
   - 风险：量化任务运行中如果 Codex transport 被关闭或 recovery 失败，StartTurn 返回的 handle 会失败，turn watcher 能标本地失败，但 cron progress subscriber 收不到 terminal bus 事件。run 可能需要等 recovery/dangling 逻辑二次判定，期间同一任务窗口可能被误判为 still running 或 observe_lost。
   - 建议：`failTurns()` 应按每个 provider turn id 发失败 `TurnCompleted` 或 `TurnInterrupted`，并包含 agent/thread/turn identity；测试覆盖 `failRecovery` 后 cron subscriber 能完成 run。

3. **[major] observation 保存了 local/provider 映射，但 terminal/tokens/trajectory 读写仍按原始 provider turn id，技能和终态会分桶**
   - 证据：StartTurn 成功后会 `mapObservationTurn(localID, providerID)`（`internal/module/turn/service.go:232-235`；`internal/module/turn/observation_wiring.go:34-48`），Memory 也提供 `ResolveLocalTurn/ResolveProviderTurn`（`internal/module/turn/observation/memory.go:38-67`）。但 subscriber 处理 terminal/tokens 时直接使用 `ev.TurnID` 写入，不先把 provider id 解析成本地 id（`internal/module/turn/observation/subscribers.go:97-105`，`internal/module/turn/observation/subscribers.go:192-212`）。PrepareTurn 记录 selected skills 用的是 localID（`internal/module/turn/observation_wiring.go:9-13`）。
   - 风险：provider event 的 TurnID 通常是 provider id，而 skills/dedupe/tracker 的主键是 local id。trajectory collector 用 event TurnID materialize，并从 observation 读 `SkillsSelected(turnID)`（`internal/module/turn/trajectory_collector.go:331-370`），因此一轮量化任务可能出现 terminal/tokens 在 provider bucket、skills 在 local bucket，后续评估/insight 抽取看到不完整事实。
   - 建议：observation subscriber 入口统一 canonicalize turn id：优先 `ResolveLocalTurn(providerID)`，写入本地 turn id；需要保留 provider id 时作为字段而不是 map key。

4. **[moderate] Codex `turn:interrupted` 被 terminal fast-path 吃掉，可能翻译成成功完成**
   - 证据：`isTurnTerminalEvent()` 把 `turn:interrupted` 归为 terminal event（`internal/provider/codexapp/factory.go:156-163`）。`translateTurnEvent()` 在 switch 前先处理所有 terminal event，返回 `TurnCompleted{Success: turnTerminalSuccess(...)}`（`internal/provider/codexapp/event_map.go:167-187`）。`turnTerminalSuccess()` 只把 method 包含 `aborted` 或 payload `success=false/status=failed` 判失败；`turn:interrupted` 且 status 为空会返回 true（`internal/provider/codexapp/factory.go:179-187`）。后面的 `TurnInterrupted` case 因 fast-path 已返回而不可达（`internal/provider/codexapp/event_map.go:188-195`）。
   - 风险：某些 Codex 中断事件会以成功 `TurnCompleted` 进入 cron subscriber，scheduler 将量化 run 标为成功，掩盖用户中断或运行中断。
   - 建议：从 terminal fast-path 移除 `turn:interrupted`，或在 `turnTerminalSuccess()` 对 interrupted 明确返回 false 并映射为 `TurnInterrupted`。

5. **[moderate] Claude session stop 只完成 handle 并发布 agent stopped，不发布 turn terminal**
   - 证据：Claude `stop()` 取出 active turn 后仅 `handle.finish(errors.New("claudecli: session stopped"))`，随后 dispatch `agent:stopped`（`internal/provider/claudecli/session.go:312-342`）。被动 receive exit 会通过 `finishTurnWithError()` 补发 `turn:complete`（`internal/provider/claudecli/session_receive_exit.go:8-22`；`internal/provider/claudecli/factory.go:69-82`），但显式 stop 路径没有同等补偿。
   - 风险：手动停止或 runtime stop 发生在 cron 量化任务执行中时，cron progress 不一定收到 terminal。与 Codex 类似，run 会依赖后续 dangling/recovery 判断，而不是即时失败。
   - 建议：Claude stop 路径复用 `finishTurnWithError()` 或单独 dispatch `turn:interrupted/turn:complete success=false`。

## 误报与已覆盖项

- Claude 被动 read loop 退出已通过 `handleReceiveExit()` 调 `finishTurnWithError()`，会补发失败 `turn:complete`（`internal/provider/claudecli/session_receive_exit.go:8-22`）。
- turn service 已经把 provider id 绑定到 tracker 和 observation map；问题不在映射缺失，而在 observation subscriber/collector 没有把该映射用于 canonical key。
- observation 对 interrupted/aborted 有 sticky terminal 保护，不会被 late completed 覆盖；本轮问题是事件可能根本没进入正确 bucket 或被 Codex 翻译成成功。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/turn ./internal/module/cron ./internal/provider/codexapp ./internal/provider/claudecli -count=1
```

结果：通过。

## 下一轮建议

- Round 060 审查 insight/trajectory flush：`internal/module/insight`、trajectory collector drain、flush signal 生成，确认 local/provider turn id 分裂是否会影响成功率、技能评分与量化反馈闭环。
