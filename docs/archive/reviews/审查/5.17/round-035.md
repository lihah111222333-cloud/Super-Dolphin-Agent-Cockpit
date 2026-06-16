# Round 035 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 08:03:45 KST
- 结束：2026-05-17 08:18:02 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 DAG spawned agent 停止、ArchiveAgent 回收、持久化 thread/binding 归档和资源清理风险，重点看任务完成后子 agent 是否可靠释放、失败是否会重试或告警、归档状态是否一致。

- `cmd/mcp-orch/orchestration/stop_helper.go`
- `cmd/mcp-orch/orchestration/stop_metric.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- `cmd/mcp-orch/orchestration/archive.go`
- `cmd/mcp-orch/orchestration/archive_test.go`
- `cmd/mcp-orch/orchestration/stop_helper_test.go`
- `cmd/mcp-orch/tools/orchestration_tools.go`
- `internal/platform/metrics/dag.go`

## Findings

1. **[major] DAG 完成后的 spawned agent stop 是 best-effort，失败不重试也不阻塞 DAG 终态**
   - 证据：subscriber 在 DB advance 后调用 `stopSpawnedAgentForSubscriber()`；该函数调用 `StopSpawnedAgent()` 后仅 warn，不向上返回错误（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:661-682`）。`StopSpawnedAgent()` 注释也明确调用方不得传播错误（`cmd/mcp-orch/orchestration/stop_helper.go:96-114`）。
   - 风险：量化 DAG 节点已标 done/failed，但子 agent 进程可能继续运行，占用 provider session、文件句柄或继续写输出。批量策略运行时会累积孤儿 agent。
   - 建议：stop failure 写入 run/node metadata 或专门 cleanup queue；后台重试停止，超过阈值触发告警。

2. **[major] lookup 失败会跳过 stop，且没有补偿任务**
   - 证据：`resolveAgentIDForStop()` 在 `AgentThreadLookup.GetByThreadID` 报非 not-found 错误时返回 `StopResultSkippedLookupFailed`，不调用 StopAgent（`cmd/mcp-orch/orchestration/stop_helper.go:161-172`）。测试锁定 lookup failed 不调用 StopAgent（`cmd/mcp-orch/orchestration/stop_helper_test.go:237-253`）。
   - 风险：DB/RPC 瞬断时，最需要清理的 spawned agent 直接被跳过，后续也没有重试来源。量化任务完成高峰时，短暂持久化 store 故障会造成大量残留运行时。
   - 建议：lookup failed 进入 cleanup retry queue，保留 threadID；或在 service runtime registry 中按 threadID 反查兜底。

3. **[major] ArchiveAgent 远端 archive 成功后跳过本地 thread/binding archived 标记**
   - 证据：`ArchiveAgent()` 若 `stopArchiveTarget()` 返回 `remoteArchived=true`，则不会调用 `archivePersistedArchiveTarget()`（`cmd/mcp-orch/orchestration/archive.go:30-47`）。测试 `TestArchiveAgentArchivesOwningRuntimeWhenCalledWithProviderThreadID` 和 `TestArchiveAgentInvokesLauncherArchiveNotStop` 断言 remote archive 后 `UpdateStatus`/`SetArchived` 调用为 0（`cmd/mcp-orch/orchestration/archive_test.go:140-207`）。
   - 风险：远端 runtime 已归档，但本地持久化 binding/thread 仍显示 created/running。后续 UI、stop helper 或调度恢复可能把已归档 agent 当成可用对象。
   - 建议：远端 archive 成功后仍写本地 archived 状态；若本地写失败，返回部分失败并可重试。

4. **[major] ArchiveAgent 先停 runtime 再写 DB，DB 归档失败会留下“运行时已停、本地未归档”的半状态**
   - 证据：ArchiveAgent 先 `stopArchiveTarget()`，随后才 `archivePersistedArchiveTarget()`（`cmd/mcp-orch/orchestration/archive.go:30-47`）。`archivePersistedArchiveTarget()` 中 thread UpdateStatus 或 binding SetArchived 任一失败会返回错误（`cmd/mcp-orch/orchestration/archive.go:80-117`）。
   - 风险：用户看到 stop/archive 工具失败后可能重试；此时 runtime 已被停止，但 DB 仍未归档，重试路径和 dashboard 状态可能不一致。
   - 建议：归档写入采用事务性 outbox 或补偿状态 `archive_pending`；先持久化意图，再停止 runtime，最后确认完成。

5. **[moderate] StopSpawnedAgent metrics 仍是本地 snapshot，未进入 Prometheus**
   - 证据：`StopSpawnedAgentCounters()` 只是包内 atomic snapshot（`cmd/mcp-orch/orchestration/stop_metric.go:95-105`）；Prometheus DAG collector 只导出 dispatch retry 指标（`internal/platform/metrics/dag.go:9-62`）。
   - 风险：`skipped_lookup_failed`、`failed`、`binding_missing` 这些资源泄漏强信号不会被统一监控抓到。
   - 建议：导出 `dag_node_stop_spawned_agent_total{result=...}`，并对 failed/lookup_failed 设置告警。

## 误报与已覆盖项

- stop helper 对 already stopped / already archived 做了幂等分类，避免把正常重复清理记为失败（`cmd/mcp-orch/orchestration/stop_helper.go:240-263`）。
- ArchiveAgent 已经优先走 `ArchiveAgent` recycle path，工具层在服务支持时不会退回裸 StopAgent（`cmd/mcp-orch/tools/orchestration_tools.go:221-244`）。
- StopSpawnedAgent 的七类结果有单元测试覆盖，分类本身不是本轮问题；问题在补偿和监控出口不足。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools ./internal/platform/metrics -count=1
```

结果：通过。

## 下一轮建议

- Round 036 审查 persistent runtime rehydrate 与 runner/exit monitor，重点看进程重启后 agent/thread/DAG 节点状态恢复。
