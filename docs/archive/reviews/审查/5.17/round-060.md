# Round 060 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:48:03 KST
- 结束：2026-05-17 08:56:21 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 insight/trajectory flush 链路。重点看终态 turn event 如何触发 insight 持久化、observation facts 如何被读取、队列/重试/UPSERT 是否能保证量化反馈闭环不丢。

- `internal/module/insight/collector.go`
- `internal/module/insight/flusher.go`
- `internal/module/insight/subscribers.go`
- `internal/module/insight/module.go`
- `internal/module/insight/contract.go`
- `internal/module/turn/trajectory_collector.go`
- `internal/module/turn/skill_extractor.go`
- `internal/module/turn/module.go`
- `internal/store/insight/contract.go`
- `internal/store/insight/store.go`
- `sql/queries/session_insight.sql`

## Findings

1. **[major] insight collector 把 provider `TurnID` 直接命名为 `LocalTurnID`，会写错 insight 主键**
   - 证据：collector 订阅 `TurnCompleted/TurnInterrupted/TurnStalled` 后直接把 `ev.TurnID` 传入 `enqueueTerminal()`（`internal/module/insight/collector.go:55-64`）。`enqueueTerminal()` 将该值保存为 `flushSignal.LocalTurnID`（`internal/module/insight/collector.go:77-90`）。flusher 随后以 `sig.LocalTurnID` 读取 observation terminal/tokens/skills，并以 `(thread_id, local_turn_id)` UPSERT（`internal/module/insight/flusher.go:150-190`；`sql/queries/session_insight.sql:41-43`）。
   - 风险：provider bus event 的 TurnID 很可能是 provider turn id，而 PrepareTurn/skills/dedupe 使用 local turn id。insight row 会以 provider id 作为 local_turn_id，或者因 observation 的 local bucket 没有 terminal 被重试后丢弃。后续成功率、token 成本、技能选择反馈会与真实 turn 脱钩。
   - 建议：collector 或 flusher 应调用 observation `ResolveLocalTurn(providerID)` 做 canonicalize；无法解析时保留 provider_turn_id 字段但不要伪装成本地 id。

2. **[major] insight flush queue 满时直接丢终态信号，且终态通常没有第二次机会**
   - 证据：collector 队列默认容量 512（`internal/module/insight/collector.go:18-21`），`enqueueTerminal()` 非阻塞写入，满队列时只增加 dropped counter 并 warn（`internal/module/insight/collector.go:73-101`）。终态信号来源是 bus subscriber，一次 terminal event 只 enqueue 一次（`internal/module/insight/collector.go:47-65`）。
   - 风险：高并发量化任务或 DB 慢写时，超过队列容量的 terminal insight 永久丢失。cron/DAG 可能已完成，但 session_insights 没有对应行，后续“按历史表现调优”的量化反馈闭环会系统性漏样本。
   - 建议：终态 insight 信号应有 durable inbox/outbox，或至少对 dropped counter 接入告警并支持按 terminal event replay 重建；不能只靠内存队列承载唯一事实。

3. **[major] observation race 只重试一次，订阅顺序稍慢就会永久丢 insight**
   - 证据：flusher `buildParams()` 发现 `obs.Terminal(sig.LocalTurnID)` 不存在就返回 false（`internal/module/insight/flusher.go:150-154`）。`handle()` 只把信号重入队一次，第二次仍 miss 就静默返回（`internal/module/insight/flusher.go:118-134`）。注释也承认这是 terminal event 早于 observation subscriber 的 race（`internal/module/insight/flusher.go:121-124`）。
   - 风险：bus subscriber 调度顺序、goroutine 抢占或 observation 写入变慢时，insight 会在两个快速 dequeue 周期内都读不到 terminal，然后永久丢弃。量化任务完成越密集，越容易在 flusher 快于 observation 时产生缺口。
   - 建议：用带延迟和最大时长的 retry/backoff，或把 terminal event payload 自身作为最小事实直接入 insight，再异步补 token/skills。

4. **[moderate] store upsert 失败只 log，不重试也不回队列**
   - 证据：`Flusher.handle()` 调 `store.Upsert()` 失败只 warn 后返回（`internal/module/insight/flusher.go:135-141`）。测试也固定了“DB hiccup 不撕 down runner”的语义，但没有补偿（`internal/module/insight/flusher_test.go:275-291`）。
   - 风险：DB 短暂不可用时，终态 insight 信号被消费但未持久化，后续不会自动补写。对于量化引擎，这会把失败/中断样本从统计中删除，造成成功率和成本评估偏乐观。
   - 建议：写失败进入 bounded retry 队列或 durable outbox；至少按 turn id 做有限重试并暴露失败计数。

5. **[moderate] skill extractor runner 不在 shutdown 时 drain trajectory，终态接近退出会丢技能候选**
   - 证据：`ExtractorRunner.Run()` 在 `ctx.Done()` 时直接返回，不 drain collector 中已经 completed 的 trajectories（`internal/module/turn/skill_extractor.go:384-399`）。实际处理只发生在 tick 分支调用 `flushOnce()`（`internal/module/turn/skill_extractor.go:402-418`）。runner 注册到 root `group:"runners"`，生命周期随应用停止取消（`internal/module/turn/module.go:63-76`）。
   - 风险：量化任务刚完成后应用退出，trajectory collector 已收集 completed 但 extractor tick 未到，技能候选/经验提取会直接丢失；这会削弱后续轮次的自动学习与工具选择。
   - 建议：在 `ctx.Done()` 路径做一次 bounded drain/extract，或把 trajectory 完成信号持久化后由后台 worker 消费。

## 误报与已覆盖项

- session insight SQL 的 terminal precedence 和 token no-regression 已在 UPSERT 层保护：interrupted/aborted 不会被 later completed 覆盖，token 只取 GREATEST（`sql/queries/session_insight.sql:51-80`）。
- flusher shutdown 有 5 秒 bounded drain，可以处理已经排队且 DB 正常的信号（`internal/module/insight/flusher.go:79-112`）。
- store 默认把空 `SkillsSelected` 转成 `[]`，不会把 nil JSON 写坏（`internal/store/insight/store.go:75-103`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/insight ./internal/store/insight ./internal/module/turn -count=1
```

结果：通过。

## 下一轮建议

- Round 061 审查 skill extractor/evaluator 反馈质量：`internal/module/turn/skill_evaluator.go`、`skill_extractor.go`、redactor 与 skillcandidate store，确认哪些成功/失败样本会进入自动技能生成。
