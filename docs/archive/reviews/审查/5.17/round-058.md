# Round 058 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:31:07 KST
- 结束：2026-05-17 08:38:42 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 turn dedupe durable registry 与 cron 崩溃恢复的交界。重点看 dedupe key 从 StartTurn 写入、provider id 绑定、终态标记、重启后 lookup、以及 registry sweep 是否形成完整闭环。

- `internal/module/turn/service.go`
- `internal/module/turn/tracker.go`
- `internal/store/turndedupe/contract.go`
- `internal/store/turndedupe/store.go`
- `sql/queries/turn_dedupe_registry.sql`
- `internal/module/turn/service_dedupe_store_test.go`
- `internal/store/turndedupe/store_test.go`

## Findings

1. **[major] durable dedupe 写入是 best-effort，崩溃恢复保护可能静默失效**
   - 证据：`StartTurn()` 在 provider 提交前调用 `recordDedupeUpsert()`，但该函数注释明确错误会被 log 后丢弃，代码只 `Warn` 不返回错误（`internal/module/turn/service.go:204-219`，`internal/module/turn/service.go:373-396`）。provider id 绑定与终态标记也采用同样 best-effort 语义（`internal/module/turn/service.go:398-419`，`internal/module/turn/service.go:421-439`）。
   - 风险：cron recovery 依赖 dedupe registry 判断某个量化任务窗口是否已经提交；如果 upsert 或 bind 在 DB 短暂不可用时失败，provider turn 已启动但 durable registry 没有记录。进程重启后 `LookupByDedupeKey()` 会返回未提交，scheduler 可能再次提交同一窗口。
   - 建议：对 cron/automation 来源的 dedupe key 使用 fail-closed 语义：registry upsert 失败时不要继续 provider StartTurn，或者把失败暴露给 scheduler 进入 retry；同时为 bind/terminal 加 durable retry/outbox。

2. **[major] live registry 命中超过 30 分钟会被当作 zombie，长耗时 turn 可被重复提交**
   - 证据：`trackerTTL` 固定为 30 分钟（`internal/module/turn/tracker.go:15`）。`LookupByDedupeKey()` 在 tracker miss 后查 registry，若 `time.Since(entry.UpdatedAt) > trackerTTL` 就返回 `ok=false`，允许调用方重试（`internal/module/turn/service.go:321-370`）。而 registry 只有 upsert、bind、terminal 会更新时间，运行中的 provider turn 没有 heartbeat 刷新该行（`internal/module/turn/service.go:386-414`）。
   - 风险：一轮量化任务如果 provider 侧执行超过 30 分钟，并且本地进程重启导致 tracker 丢失，durable row 会被判成 zombie。scheduler 会认为窗口未提交，再启动第二个 provider turn；原 turn 仍可能继续运行并最终写回，造成重复交易、重复下单或重复报告。
   - 建议：registry live 行应有运行 heartbeat 或 provider 状态观察；zombie 判断应结合 cron run lease、provider session 查询和 terminal 事件，而不是单独用本地 wall-clock TTL。

3. **[moderate] `turn_dedupe_registry` 的 sweep 有接口和 SQL 注释，但未接入生产 runner**
   - 证据：store 暴露 `Sweep(ctx, cutoff)`（`internal/store/turndedupe/contract.go:57-60`，`internal/store/turndedupe/store.go:100-109`），SQL 注释写明应由 scheduler 粗粒度执行（`sql/queries/turn_dedupe_registry.sql:50-55`）。全仓 `rg` 只发现 tests 与 sqlc 生成代码调用 `SweepTurnDedupeRegistry`，没有 cron/scheduler/runner 生产调用点。
   - 风险：如果终态标记失败或进程在 shutdown 路径退出，registry live 行会长期保留；`GetLive` 会持续把旧 key 当运行中，阻塞后续合法重试。相反，lookup 侧又用 30 分钟 TTL 软过期，DB 表和业务判断形成两个不同生命周期口径。
   - 建议：把 sweep 接入 module lifecycle 或 cron scheduler tick，并把 cutoff、删除数量、失败次数导出为指标；同时让 lookup 和 sweep 使用同一生命周期配置。

4. **[moderate] upsert 冲突时保留旧 `provider_turn_id`，新 local turn 可能关联旧 provider id**
   - 证据：冲突 upsert 会覆盖 `local_turn_id` 并清空 `terminal_at`，但 `provider_turn_id = turn_dedupe_registry.provider_turn_id` 明确保留旧值（`sql/queries/turn_dedupe_registry.sql:15-23`）。
   - 风险：当 zombie key 被重试或 terminal row 被同 key “复活”时，registry 可能出现新 `local_turn_id` + 旧 `provider_turn_id` 的组合。重启后的 `LookupByDedupeKey()` 会把旧 provider id 返回给 scheduler/observer（`internal/module/turn/service.go:363-370`），后续终态事件可能被错误归因。
   - 建议：冲突 upsert 若 `local_turn_id` 变化，应清空 provider_turn_id，直到新的 bind 成功；测试覆盖“同 key 新 localID 不继承旧 providerID”。

5. **[moderate] bind/terminal 更新没有 rows affected 语义，写入顺序破坏会被静默掩盖**
   - 证据：store 的 `BindProviderTurnID()` 和 `MarkTerminal()` 调用 sqlc `:exec` 更新并直接返回错误（`internal/store/turndedupe/store.go:54-75`）；SQL 只按 `dedupe_key` 更新，不返回是否命中（`sql/queries/turn_dedupe_registry.sql:25-36`）。
   - 风险：如果 upsert best-effort 失败，后续 bind/terminal 在缺行情况下也是成功 no-op。日志里看不到“provider 已启动但 registry 不存在”，恢复路径只能在下一次重启时表现为重复提交。
   - 建议：把 bind/terminal SQL 改成返回 rows affected 或 `:one RETURNING`，缺行时至少以 warn/error 指标暴露；对 cron dedupe key 可直接视为一致性错误。

## 误报与已覆盖项

- 同进程内 dedupe 已覆盖：`StartTurn()` 会先注册 tracker dedupe key，`LookupByDedupeKey()` 优先查内存 tracker（`internal/module/turn/service.go:211-218`，`internal/module/turn/service.go:331-333`）。
- provider StartTurn 返回错误或 nil handle 时会标记 tracker terminal，并尝试标记 registry terminal（`internal/module/turn/service.go:219-229`）。
- `GetLive` 只返回 `terminal_at IS NULL` 的行，不会把已成功 terminal 的正常历史行误报为 live（`sql/queries/turn_dedupe_registry.sql:38-48`）。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/module/turn ./internal/store/turndedupe -count=1
```

结果：待执行。
结果：通过。

## 下一轮建议

- Round 059 审查 turn watcher、completion、observation 事件映射：确认 provider Done/Err、local/provider id 映射、terminal facts 写入失败时是否会影响 cron/DAG 的终态判断。
