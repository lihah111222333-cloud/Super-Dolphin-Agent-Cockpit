# 架构合规复核：并发竞态 + JSON + 测试

日期：2026-03-21
方法：仅使用 LSP 完成读取、搜索与行号核对；由于 LSP 工具无法在不存在的路径上创建文件，最终落盘仅使用一次补丁创建本文件。

## 已读报告

- `docs/plans/迁移/arch-code-guard.md:1`
- `docs/plans/迁移/arch-concurrency.md:1`
- `docs/plans/迁移/arch-doc-accuracy.md:1`
- `docs/plans/迁移/arch-error-handling.md:1`
- `docs/plans/迁移/arch-event-contract.md:1`
- `docs/plans/迁移/arch-fx-graph.md:1`
- `docs/plans/迁移/arch-import-direction.md:1`
- `docs/plans/迁移/arch-json-wire.md:1`
- `docs/plans/迁移/arch-test-coverage.md:1`
- `docs/plans/迁移/arch-two-zone-dry.md:1`

## 结论总表

| 项目 | 状态 | 结论 |
| --- | --- | --- |
| 并发竞态 #1 SessionManager 代际保护 | ✅ | `generation` 已写入 `SessionManager`，并已接入 `thread -> orchestration -> unified` 清理链路。 |
| 并发竞态 #2 codexapp threadID | ✅ | 已改为 `atomic.Value` 读写；driver 通过 `setThreadID` 写入。 |
| 并发竞态 #3 approval dispatcher | ✅ | `dispatcher` 已在 `registerPending` 锁内写入，`finishPending` 之后只读。 |
| JSON wire / `rpc_types` snake_case | ⚠️ | 大部分请求已转 snake_case，但 `thread/start` 仍保留 camelCase 主 tag，`turnForceCompleteResult` 仍是 `forceCompleted`。 |
| 测试补全 | ✅ | `claudecli` / `codexapp` / `thread` / `rpc` 当前都已有 `_test.go`。 |
| 文档 / `session-summary` | ✅ | `session-summary.md` 已刷新到 `2026-03-21`，内容同步到 `P7w1 完成`。 |

## 1. 并发竞态 #1：SessionManager 代际保护

旧报告：
- `docs/plans/迁移/arch-concurrency.md:19`
- `docs/plans/迁移/arch-concurrency.md:106`

当前代码：
- `internal/provider/unified/session.go:15-25` 新增 `sessionEntry.generation` 和 `nextGeneration`。
- `internal/provider/unified/session.go:37-55` 的 `Register` 返回 generation，并把 `{generation, session}` 写入 map。
- `internal/provider/unified/session.go:78-91`、`internal/provider/unified/session.go:139-152` 的 `Remove(agentID, generation)` 只有代际匹配时才删除。
- `internal/module/thread/session_generation.go:13-26` 在 session 建立后把 generation 绑定到 orchestration。
- `internal/sidecar/orch/orchestration/session_generation.go:29-40` 清理链路优先走 `RemoveSessionGeneration`。
- `internal/provider/unified/session_generation_test.go:39-58` 已补回归测试，验证旧 generation 不会删掉新 session。

判断：✅ 已修。此前“旧 session 的 remove 误删新 session”的主问题已经收口。

补充：
- `internal/provider/unified/session.go:93-106`、`internal/provider/unified/session_adapter.go:34-39`、`internal/provider/unified/session_adapter.go:48-53` 仍保留 `RemoveCurrent` 给显式强删路径使用，但旧报告里的 create/remove 竞态点已不再是主清理路径。

## 2. 并发竞态 #2：codexapp threadID

旧报告：
- `docs/plans/迁移/arch-concurrency.md:20`
- `docs/plans/迁移/arch-concurrency.md:214-226`

当前代码：
- `internal/provider/codexapp/session.go:19-39` 的 `session.threadID` 已改为 `atomic.Value`。
- `internal/provider/codexapp/thread_id.go:5-21` 读经 `Load`，写经 `Store`，并封装为 `ThreadID()/setThreadID()/resolveThreadID()`。
- `internal/provider/codexapp/driver.go:88-94`、`internal/provider/codexapp/driver.go:106-112` 通过 `s.setThreadID(...)` 写入。
- 当前读路径都经 accessor：`internal/provider/codexapp/session.go:207-223`、`internal/provider/codexapp/recovery.go:79-88`、`internal/provider/codexapp/session_history.go:13-20`、`internal/provider/codexapp/session_approval.go:60-66`。

判断：✅ 已修。现在是 atomic 同步，不再是无锁读写 data race。

补充：
- `internal/provider/codexapp/session.go:103-105` 仍然在 thread ID 就绪前启动 read/health loop；这是初始化顺序问题，不是旧报告里的 data race 本体。

## 3. 并发竞态 #3：approval dispatcher

旧报告：
- `docs/plans/迁移/arch-concurrency.md:21`
- `docs/plans/迁移/arch-concurrency.md:176-185`

当前代码：
- `internal/platform/rpc/approval.go:81-89` 的 `RequestApproval` 先调用 `registerPending(req, bridgeDispatcher(bridge))`，再发布 requested 事件。
- `internal/platform/rpc/approval.go:131-157` 的 `registerPending` 在持锁区内把 `dispatcher` 写入 `pendingApproval`。
- `internal/platform/rpc/approval.go:242-269` 的 `finishPending` 只在移除 pending 后发布 resolved 事件。
- `internal/platform/rpc/approval_events.go:34-42` 的 `publishResolved` 读取的是已经写好的 `pending.dispatcher`。
- `internal/platform/rpc/approval_test.go:32-43` 已有测试覆盖“先存 dispatcher 再 publish”的约束。

判断：✅ 已修。旧报告里的 register/resolve 并发窗口已被锁内写入消掉。

## 4. JSON wire：`rpc_types` 是否已统一 snake_case

旧报告：
- `docs/plans/迁移/arch-json-wire.md:5-8`

当前代码：
- 已转为 snake_case 的主路径示例：
  - `internal/sidecar/orch/orchestration/rpc_types.go:15`
  - `internal/sidecar/orch/orchestration/rpc_types.go:58`
  - `internal/sidecar/orch/orchestration/rpc_types.go:81`
  - `internal/sidecar/orch/orchestration/rpc_types.go:104-105`
  - `internal/module/turn/rpc_types.go:9-21`
  - `internal/module/workspace/contract.go:25-44`
  - `internal/module/workspace/rpc_types.go:10-15`
- 仍未完全统一的当前 tag：
  - `internal/module/thread/rpc_types.go:22-33` 的 `startParams` 仍以 `modelProvider` / `approvalPolicy` / `baseInstructions` / `developerInstructions` 作为主 tag。
  - `internal/module/turn/rpc_types.go:156-159` 的 `turnForceCompleteResult` 仍输出 `forceCompleted`。
  - `internal/module/thread/rpc_types.go:200-211`、`internal/module/turn/rpc_types.go:31-56`、`internal/module/workspace/rpc_types.go:24-44` 仍保留 camelCase 兼容反序列化。

判断：⚠️ 部分修复。主流新参数大多已收敛到 snake_case，但 `rpc_types` 还不是“全量统一 snake_case”。

## 5. 测试补全：`claudecli` / `codexapp` / `thread` / `rpc` 是否有 `_test.go`

旧报告：
- `docs/plans/迁移/arch-test-coverage.md:17-24`
- `docs/plans/迁移/arch-test-coverage.md:90`

当前代码：
- `claudecli`：`internal/provider/claudecli/driver_capability_test.go:1`、`internal/provider/claudecli/thread_identity_test.go:1`
- `codexapp`：`internal/provider/codexapp/driver_session_test.go:1`
- `thread`：`internal/module/thread/resume_test.go:1`、`internal/module/thread/rpc_types_test.go:1`、`internal/module/thread/service_handlers_test.go:1`、`internal/module/thread/stop_test.go:1`
- `rpc`：`internal/platform/rpc/approval_test.go:1`、`internal/platform/rpc/handler_test.go:1`、`internal/platform/rpc/server_minimal_test.go:1`、`internal/platform/rpc/server_test.go:1`

判断：✅ 已补齐到“有 `_test.go`”这一层。

补充：
- 数量上仍偏薄，尤其 `codexapp` 目前只看到 1 个测试文件，`claudecli` 只有 2 个；但本次核验问题只问“是否有 `_test.go`”，答案是有。

## 6. 文档：`session-summary.md` 是否已更新

旧报告：
- `docs/plans/迁移/arch-doc-accuracy.md:11`
- `docs/plans/迁移/arch-doc-accuracy.md:83-97`

当前代码：
- `docs/plans/迁移/session-summary.md:3-4` 已更新为 `2026-03-21` / `P0-P6 收官 + P7w1 完成`。
- `docs/plans/迁移/session-summary.md:19` 覆盖度已从旧报告指出的 `~68%` 更新为 `~82%`。
- `docs/plans/迁移/session-summary.md:45-48` 已写入 P5/P6 收官与 `P7w1 V2 兼容收尾` 完成状态。

判断：✅ 已更新。

## 最终判断

- 并发 3 项：当前 HEAD 上 3/3 已修。
- JSON wire：仍未完全合规，建议把 `internal/module/thread/rpc_types.go` 的 `startParams` 和 `internal/module/turn/rpc_types.go` 的 `forceCompleted` 收成 snake_case，再保留兼容 `UnmarshalJSON` 层。
- 测试与文档：本次核验要求的“有 `_test.go`”与“`session-summary.md` 已更新”都已满足。
