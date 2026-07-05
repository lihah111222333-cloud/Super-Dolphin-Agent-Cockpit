# 30-Agent Production Risk Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-07-05 30-agent 全量生产风险复核中确认的生产可达风险，并为每项固定唯一最优修复和必要的上层防护。

**Architecture:** 本计划按写集和故障边界拆成 8 条 lane：UI/状态、mcp-lsp、thread/provider、cron/DAG、日志与隐私、memory/skill、store/容量、release packaging。每项风险先锁定当前坏行为的 RED 测试，再做根因修复；若同类缺陷可能从相邻入口绕过，则在入口 schema、共享 helper、状态机 fence、日志 sanitizer、registry guard 或 store/query cap 增加上层防护。所有修复坚持 fail-fast，不用默认值、吞错或静默降级掩盖配置、协议或持久化错误。

**Tech Stack:** Go 1.25.7、SQLite/sqlc、Fx lifecycle、Wails RPC/HTTP、React/Vite/Vitest、mcp-lsp、mcp-orch、Codex/Claude provider adapters、memory/skill runtime、shell packaging scripts、repo guard scripts。

**Verification Surface:** Go 变更使用各 lane 明确列出的 `./scripts/test_with_guard.sh ... -count=1` 命令；SQL/store 变更额外运行 `make sqlc-verify`；frontend-app 变更使用 `cd frontend-app && npm run lint && npm test && npm run build`；release/script 变更运行对应 `go test ./scripts ...` 和 packaging verifier；每个 lane 完成前运行 `git diff --check`，集成前运行 `git status --short` 并确认只包含本 lane 文件。

---

## Current Boundary

- [ ] 审查基线: `main @ aa98366b6cb6`。
- [ ] 本计划是修复方案，不代表已批准执行源码修改。
- [ ] 本轮要求是“生产可达才上报”。本计划只收录主控裁决为生产可达的风险；workflow-only guard 缺口、纯 style diagnostics、无当前坏行为证据的 future-drift 项不进入修复队列。
- [ ] 30 个子 agent 复核产出 R01-R24；同步 `origin/main` 到 `aa98366b6cb6` 后，已唤起超时 agent 继续复核，5-agent 修复方案裁决完成。本版已吸收 5 个 agent 对路径、唯一最优修复和上层防护的裁决。
- [ ] 当前主工作区仅允许保留本计划文档变更。执行源码修复前仍必须重新检查 dirty 边界。

## Global Execution Rules

- [ ] 控制器执行前运行:

```bash
git status --short
git rev-parse --short=12 HEAD
```

Expected: 记录当前 dirty 边界和 HEAD；无关文件保持原样。

- [ ] 每个 lane 使用独立 worktree。以下示例使用 L01；其他 lane 把 `l01-ui-state` 替换为本计划中的 lane id 和短名，例如 `l02-mcp-lsp`、`l03-thread-provider`。

```bash
base_branch=$(git branch --show-current)
git worktree add ".worktrees/20260705-risk-l01-ui-state" -b "codex/20260705-risk-l01-ui-state" "$base_branch"
cd ".worktrees/20260705-risk-l01-ui-state"
git status --short
```

Expected: 新 worktree 干净；如不干净，停止并返回真实路径和状态。

- [ ] worker 只能改本计划 lane 写集列出的文件和同包测试。需要越界时返回 `NEEDS_APPROVAL`，包含路径、原因、缺少该越界无法关闭的风险。
- [ ] 每个风险先写 RED 测试并确认失败，再实现最小修复，再运行 GREEN 验证。禁止先改生产代码再补测试。若 `-run` 输出 `[no tests to run]`、测试名未命中或只证明旧测试仍绿，视为 blocker，必须先补上能失败的新测试。
- [ ] 不沿用旧诊断行号做修复依据。worker 必须在源码修改前后运行本 lane 的 `gopls check`；输出中的 Error、Warning、Information、Hint 都是 blocker，必须修复或在 lane 报告里列出文件、行号、规则和阻塞原因。
- [ ] 每个 Go 源文件改完后运行各 lane 明确列出的 `gofmt` 和 `gopls check` 命令。若 worker 需要额外触碰同 lane 内 Go 文件，必须把该精确路径追加到本 lane 的 verify 命令并在最终报告列出。

Expected: `gopls check` 无 diagnostics 输出。

- [ ] 每个 frontend 变更至少运行关联 Vitest、lint、build；若 lane 触及 chat store 或 Wails bridge，必须包含 `src/entities/client/model/useClientStore.test.js` 或 `src/shared/api/wailsBridge.test.js` 的定向覆盖。
- [ ] 每个 lane 完成前运行 `git diff --check`。若新建文件仍 untracked，使用 `git diff --no-index --check /dev/null docs/plans/2026-07-05-30agent-production-risk-remediation.md` 的同形命令验证该新文件 whitespace，并把真实文件路径写入命令。
- [ ] 集成顺序固定: L01 UI/state -> L02 mcp-lsp -> L03 thread/provider -> L04 cron/DAG -> L05 logging/privacy -> L06 memory/skill -> L07 store/capacity -> L08 release packaging。L05 是日志 sanitizer 事实源 owner；其他 lane 需要新增日志字段时必须复用 L05 helper，不得引入 raw payload/path/prompt 日志。

## Risk Matrix: Unique Fix And Upper Defense

| ID | Severity | Evidence | 唯一最优修复 | 是否需要上层防护 | 上层防护最优落点与方案 |
|---|---|---|---|---|---|
| R01 | P0 | `frontend-app/src/pages/chat/adapters/codePreviewAdapter.js:68-82`, `frontend-app/src/pages/chat/ChatPage.jsx:151-154`, `internal/ui/wails/code_preview.go:78-90` | 给 code open/save contract 增加显式 `previewMode=full|snippet`、`contentVersion` 和 `range`；snippet preview 默认只读，只有 full content 且版本匹配才允许整文件 save。 | 必须 | `internal/ui/wails/code_preview.go` 的 save RPC 校验 `previewMode=full` 和 `contentVersion`；前端 adapter 对 snippet 设置 `editable=false`；新增 arch/unit guard 禁止 `snippet` 直接初始化 editable draft。 |
| R02 | P1 | `cmd/mcp-lsp/tools/tool_edit_replace_update.go:21`, `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go:239`, `cmd/mcp-lsp/tools/tool_edit_rename.go` | mcp-lsp 所有源文件写入统一走同目录 temp write + fsync + atomic rename，并保存原 mode；初始写失败不得触碰原文件。 | 必须 | 新增 `cmd/mcp-lsp/tools/atomic_write.go`，replace_range、rename、code_action、format、rename 和 rollback 都只能调用该 helper；测试用 injected writer 证明 `ENOSPC`/`EIO` 保留原文件。 |
| R03 | P1 | `internal/module/thread/lifecycle_fork.go:264-270`, `internal/provider/unified/session.go:75-84` | `ensureRecoveredSession` 返回实际 resumed session；recover 持久化成功后调用 `activateResumedSession(agentID)`，持久化失败时 stop/cleanup pending session。 | 必须 | thread recover path 统一复用 normal resume 的 pending activation guard；新增测试确保 pending session 在 recover success 前不可见，失败后不残留。 |
| R04 | P1 | `internal/module/cron/scheduler.go:361-391`, `internal/module/cron/scheduler_recovery.go:42-70` | `SetRunTurn`、`SetActiveTurn`、`submitting -> submitted` 放入同一 store 事务；active fence 写入前不发布 submitted 状态。 | 必须 | cron terminal handler 遇到 active fence mismatch 且 run 已绑定 turn 时进入 retryable pending，不再当 stale 丢弃；补早到终态和 `SetActiveTurn` 失败回归测试。 |
| R05 | P1 | `cmd/mcp-orch/orchestration/wakeup_dispatcher.go:199-205`, `cmd/mcp-orch/orchestration/node_router.go`, `cmd/mcp-orch/orchestration/nodeexec/executor_agent.go`, `cmd/mcp-orch/store/taskdag/store_node_spawn.go` | DAG wakeup 的 claim/lease/attempt fence 传入 `RouteByWakeup`、`RecordNodeSpawn` 和 running 写回；spawn/running 写回必须 CAS。 | 必须 | `spawning_thread_id` 只允许空值或同 thread id 幂等写入；CAS 失败立即停止已启动 child agent 并返回 retry/fail，防止重复 child agent 留在外部运行。 |
| R06 | P1 | `internal/provider/codexapp/recovery.go:171-180`, `internal/provider/codexapp/recovery.go:223-248` | Codex auth failure 先用原始 reason 做分类，外发 turn error、`RawProviderEvent`、`AgentFailed.Error` 和日志时统一替换为安全错误码。 | 必须 | Codex auth 分类保持 provider 包内 helper；跨 provider/event/log 的安全字段复用 `internal/platform/observability/sanitizer.go` 和 `safe_preview.go`。arch/unit tests 断言 `sk-`、`api key provided`、absolute path 不进入 event/log/handle error。 |
| R07 | P1 | `internal/provider/claudecli/transport_config.go:119-137` | Claude MCP launch 日志只记录 server name/type、command basename、arg count/hash、URL scheme/host/port、固定 `/mcp` path 或 path hash/segment count、env keys；禁止 raw args、URL query/fragment 和非固定 path 原文。 | 必须 | L05 的 observability sanitizer 提供 `SafeMCPLaunchServerSummary`; `transport_config_test.go` 用 fake token in argv/query/path 锁定脱敏行为。 |
| R08 | P1 | `internal/module/memory/retrieval/render.go:212-225`, `internal/module/memory/retrieval/entry_read.go`, `internal/module/memory/rules_provider.go` | memory 读取逻辑保留绝对路径，provider attachment 只渲染相对 memory root 的 display path 或 frontmatter name。 | 必须 | 不向共享 `MemoryEntry` 写入展示语义；在 `internal/module/memory/retrieval` 增加 `MemoryDisplayPath(root, entry)`，render/rules provider/attachment freeze 调用该 helper。测试禁止 `/Users/`、home、absolute path 进入 provider prompt。 |
| R09 | P1 | `internal/module/turn/tool_result_storage.go:51-56`, `internal/app/modules.go:221-226`, `internal/provider/shared/hooks.go:22-29` | `PersistFailed/PersistError` 从 turn module 透传到 provider shared DTO、`ToolCallEnd`、eventsurface payload 和 uistate/timeline；落盘失败时成功工具结果必须带可见 storage diagnostic。 | 必须 | provider hook contract 扩展失败字段；Codex/Claude tool translators 在 persistence failure 时发 warning event；`internal/platform/eventsurface/bind.go`、wire DTO registry 和 timeline/uistate 测试确保字段不会被静默丢弃，UI/tool timeline 展示 “result persistence failed”。 |
| R10 | P1 | `frontend-app/src/entities/client/model/useClientStore.js:2588-2605`, `frontend-app/src/entities/client/model/warningRuntime.js` | `agent/failed` 从普通终态分支拆出，先 flush/finalize，再 `addWarning('error', ...)` 并保留 error/recoverable。 | 必须 | bridge event reducer 新增 typed terminal failure helper；warning runtime 继续对 warning correlation fields 做 secret/path 脱敏；测试确保任何 `*/failed` 事件不会被前置 terminal branch 吞掉，也不会绕过 `safeWarningFields`。 |
| R11 | P1 | `frontend-app/src/pages/chat/components/ChatPageHeader.jsx:124-127`, `internal/provider/codexapp/session.go:497-500` | force complete 前端只在 active turn 可被完成时启用；后端找不到 target turn 返回 typed no-target error，不再 nil success。 | 必须 | RPC response 增加 `ForceCompleted:false` 或 error code；frontend action toast 对 no-target 显示不可完成而非成功。 |
| R12 | P1 | `cmd/mcp-lsp/tools/factory.go:282-310` | `work_dir` 必须非空且有效才消费；`cwd`/`agent_id` 出现在 tool arguments 时返回明确 migration error，不再从业务参数里删除。 | 必须 | tool wrapper schema 增加 reserved-field rejection helper；所有 tool handler dispatch 前共享调用，测试覆盖 `work_dir:""`、`cwd`、`agent_id`。 |
| R13 | P1 | `cmd/mcp-orch/tools/orchestration_tools.go:407-414` | `ArchiveAgent` 返回 `(ArchiveOutcome, error)`；handler 只有 outcome 表明 runtime/thread/binding 至少一个被处理时才 `archived=true`。 | 必须 | orchestration service 对 not found 返回 typed `ErrAgentNotFound`；MCP response schema 明确 `archived=false` 和错误，不允许 no-op success。 |
| R14 | P1 | `internal/module/skill/mirror_publisher.go:345-347`, `internal/module/skill/mirror_publisher.go:392-394` | provider-readable personal mirror drift 和 deleted-with-drift 都进入 blocking conflict；不再降级到 `Skipped`。 | 必须 | `EnsureNoSkillMirrorConflicts` 检查 conflicts 加 provider-readable skipped drift；mirror drift taxonomy 共享一个 predicate，测试覆盖 `~/.claude/skills` 和 `~/.agents/skills` stale copy。 |
| R15 | P1 | `internal/module/uistate/timeline/projector.go:78-90`, `internal/module/uistate/projector.go` | 抽出无环共享 `internal/module/uistate/terminalstatus.Status(success,status,reason,err)`；主投影和 timeline 都调用该 helper，`Success:false` 必须显示 failed，即使 error/reason/status 为空。 | 必须 | 新增 shared terminal status helper 单测、主投影状态测试和 timeline/sidebar parity test，覆盖 empty diagnostic failed event，禁止 timeline 复制一份漂移逻辑。 |
| R16 | P2 | `internal/platform/bus/sink.go:111-119`, `internal/platform/bus/sink.go:183-223` | bus log 不再记录通用 JSON preview；只用 per-event safe summary allowlist。 | 必须 | `busEventLogArgs` 只输出 event type、ids、status、bytes、hash；archtest 禁止 `event_preview` 字段和 raw event struct marshal 进入 Info/Debug 日志。 |
| R17 | P2 | `internal/module/memory/retrieval/manifest.go:24-31`, `internal/module/memory/retrieval/manifest.go:44-55` | memory manifest 在 WalkDir 内执行文件数/错误预算；I/O/header 错误返回 prefetch error，只有明确 unsafe/out-of-scope 路径可跳过。 | 必须 | `ScanHeadersSafe` 接受 `ManifestScanBudget`；超过 cap 返回 typed `memory_manifest_truncated`，`PrefetchHandle.Err()` 暴露给 turn context。 |
| R18 | P2 | `internal/module/memory/index.go`, `internal/module/memory/config.go`, `internal/module/memory/consolidation_prompt.go:47-83` | consolidation prompt 输入读取加文件数、单文件字节、总字节和 ctx cancel budget；超限返回显式 diagnostic。 | 必须 | `scanMemoryEntries` 与 `scanConsolidationLogDocuments` 共享 budget helper；provider 256KB prompt cap 之外新增前置 read cap，测试用大 logs 目录锁定。 |
| R19 | P2 | `internal/module/thread/service.go:317-321` | `thread/listPage`、`thread/loaded/listPage`、Wails active counter 改为 SQL 分页和 SQL count/filter；生产调用方迁移到显式 `limit/cursor`，不再全表读后 Go 过滤。 | 需要 | thread contract/store 增加 `ListPage`, `ListLoadedPage`, `CountActive` 和 `(status, created_at)` / `created_at` 索引；新分页 RPC 缺少 `limit` 直接 fail-fast，`limit` 最大 200。旧无参兼容入口不得作为生产 UI 路径，保留时必须有硬 cap 和弃用测试。 |
| R20 | P2 | `internal/module/cron/scheduler_recovery.go:52-59`, `internal/module/cron/scheduler_recovery.go:177-184` | cron startup recovery 用 batch cursor；terminal fallback 增加 `GetSubmittedOrRunningRunByTurnID` 点查，不再扫所有 unresolved runs。 | 需要 | cron store 增加 `turn_id/status` 索引和 query cap；同步更新 migration、baseline、`internal/store/sqlc/querier.go` 和 DB schema/query-plan tests；startup recovery 每批显式 limit，记录 last cursor，测试覆盖大量历史 runs。 |
| R21 | P2 | `internal/platform/hooks/module.go:60-66`, `internal/store/hookstore/hookstore.go:197-206` | hook startup recovery 只做 `COUNT(*)`；需要恢复明细时走分页；per-agent pending list 加显式 limit/cursor。 | 需要 | `internal/contract/hooks.go`、`internal/platform/hooks/resolver.go`、`internal/platform/hooks/manager.go` 和 hookstore contract 增加 `CountPendingReviews` / `ListPendingReviewsPage`；新分页查询缺少 limit fail-fast，max 500。 |
| R22 | P2 | `internal/store/datasource/store.go:148-166`, `internal/module/datasource/prompt_provider.go:31-42` | `datasource_documents` 迁入 SQLite migrations + `sql/queries/datasource.sql` + sqlc；prompt dynamic section 只读取有界文档摘要/分页内容。 | 必须 | datasource store 只包装 sqlc querier，禁止运行时自建表；生成 `internal/store/sqlc/datasource.sql.go` 并更新 `querier.go`；改写现有 store 懒建表测试为禁止 runtime DDL，并在 SQLite baseline/schema contract tests 登记表结构；prompt provider 增加 count、total bytes、single document bytes cap，超限返回 critical prompt section error。 |
| R23 | P2 | `scripts/package_macos.sh:1801-1805`, `scripts/package_macos.sh:1830-1835`, `scripts/package_macos_guard_test.go` | macOS installer heredoc 注入经校验的 `$app_name`，或脚本在非默认 `APP_NAME` 时 fail-fast；选择支持 override，因为脚本当前已暴露 `APP_NAME`。 | 需要 | package guard test 构造 `APP_NAME=Foo`，断言 staged app、install command 和 `SRC_APP` 同名；shell quote helper 禁止未转义 app name 写入 install script。 |
| R24 | P3 | `internal/module/datasource_v2/service.go:237-253`, `internal/module/datasource_v2/rpc.go`, `internal/module/datasource_v2/store_port.go`, `internal/store/datasourcev2/store.go:113-125`, `frontend-app/src/shared/api/backendApi.js`, `frontend-app/src/pages/skills/SkillsPage.jsx` | `datasourceV2/get` 返回文档元信息和第一页 chunks；后续 chunks 走 `datasourceV2/list_chunks` 显式 `limit/cursor`。 | 需要 | RPC response 加 `hasMore`/`nextCursor`/`chunkLimit`，最大响应字节 cap；store port、frontend backend API、contract matrix 和 SkillsPage 同步分页契约；RED 阶段必须包含 frontend fail-first，证明旧 `get`/`chunks` 契约不能悄悄通过。 |

## Lane L01: UI Code Preview And User-Visible Terminal State

**Risks:** R01, R10, R11, R15.

**Files:**
- Modify: `internal/ui/wails/code_preview.go`
- Modify: `internal/ui/wails/rpc.go`
- Modify: `frontend-app/src/pages/chat/adapters/codePreviewAdapter.js`
- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Modify: `frontend-app/src/pages/chat/components/RuntimePanel.jsx`
- Modify: `frontend-app/src/pages/chat/components/CodePreviewDialog.jsx`
- Modify: `frontend-app/src/entities/client/model/useClientStore.js`
- Modify: `frontend-app/src/entities/client/model/warningRuntime.js`
- Modify: `frontend-app/src/pages/chat/components/ChatPageHeader.jsx`
- Modify: `frontend-app/src/entities/client/model/threadLifecycleRuntime.js`
- Modify: `internal/provider/codexapp/session.go`
- Modify: `internal/module/turn/rpc_helpers.go`
- Modify: `internal/module/uistate/projector.go`
- Modify: `internal/module/uistate/timeline/projector.go`
- Create: `internal/module/uistate/terminalstatus/status.go`
- Test: `internal/ui/wails/code_preview_test.go`
- Test: `internal/module/uistate/projector_status_test.go`
- Test: `internal/module/uistate/timeline/projector_parity_test.go`
- Create test: `internal/module/uistate/terminalstatus/status_test.go`
- Test: `frontend-app/src/pages/chat/ChatPage.test.jsx`
- Create test: `frontend-app/src/pages/chat/components/CodePreviewDialog.test.jsx`
- Test: `frontend-app/src/entities/client/model/useClientStore.test.js`
- Test: `frontend-app/src/entities/client/model/warningRuntime.test.js`

- [ ] **Step 1: RED tests for snippet save overwrite.**

```bash
./scripts/test_with_guard.sh ./internal/ui/wails -run 'TestBuildCodeOpenResultSnippetIsNotFullSaveToken|TestSaveScopedFileRejectsSnippetPreviewMode' -count=1
cd frontend-app && npm test -- src/pages/chat/ChatPage.test.jsx src/pages/chat/components/CodePreviewDialog.test.jsx
```

Expected: Go test fails because save accepts no preview mode; Vitest fails because snippet preview is editable and save sends snippet draft.

- [ ] **Step 2: Implement R01 root fix and defense.**

Add fields to open result: `previewMode`, `contentVersion`, `rangeStartLine`, `rangeEndLine`. `buildSnippetResult` returns `previewMode:"snippet"` and no full-save token. `saveScopedFile` requires `previewMode:"full"` and a matching `contentVersion` for existing-file overwrite. Frontend adapter sets `editable=false` when `previewMode !== 'full'`; CodePreviewDialog hides save for snippet previews.
Run the lane `gopls check` after the change and treat any diagnostics as blocker.

- [ ] **Step 3: RED tests for swallowed `agent/failed`.**

```bash
cd frontend-app && npm test -- src/entities/client/model/useClientStore.test.js src/entities/client/model/warningRuntime.test.js
```

Expected: FAIL for a new case where `agent/failed` carries `error:"boom"` but no warning/action notice is created, or where failed-event warning fields bypass `safeWarningFields`.

- [ ] **Step 4: Implement R10 root fix and defense.**

Move `agent/failed` out of the generic terminal branch. Create `handleFailedBridgeEvent(eventName, method, payload)` that flushes deltas, finalizes assistant messages, calls `addWarning('error', method, payload)`, and preserves `payload.error` and `payload.recoverable` in action notice state. Keep warning correlation fields flowing through `safeWarningFields` in `warningRuntime.js`; do not add a direct warning-entry write path in `useClientStore.js`.

- [ ] **Step 5: RED tests for force-complete false success.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/module/turn -run 'TestForceCompleteNoTargetReturnsError|TestForceCompleteRPCDoesNotReportSuccessWhenProviderNoTarget' -count=1
cd frontend-app && npm test -- src/pages/chat/ChatPage.test.jsx
```

Expected: FAIL because no-target returns nil and frontend enables force complete without active turn.

- [ ] **Step 6: Implement R11 root fix and defense.**

Change Codex `ForceComplete` no-target to return typed `ErrForceCompleteTargetNotFound`. RPC maps it to `ForceCompleted:false` with error code. Frontend derives `canForceCompleteThread` from active turn/interruptible state and disables both header and menu buttons when no target exists.

- [ ] **Step 7: RED tests for timeline failed parity.**

```bash
./scripts/test_with_guard.sh ./internal/module/uistate/terminalstatus ./internal/module/uistate ./internal/module/uistate/timeline -run 'TestTurnTerminalStatus|TestTimelineTurnCompletedSuccessFalseWithoutErrorIsFailed|TestTimelineAndProjectionTerminalStatusParity' -count=1
```

Expected: FAIL because timeline currently emits `completed` for `Success:false` with empty diagnostics.

- [ ] **Step 8: Implement R15 root fix and defense.**

Create `internal/module/uistate/terminalstatus.Status(success bool, status, reason, err string) string` so the main uistate package and timeline subpackage can both call the same helper without import cycles. For `Success:false` with empty diagnostic fields, append a timeline error item with text `turn failed without provider diagnostic`.

- [ ] **Step 9: Verify lane.**

```bash
gofmt -w internal/ui/wails/code_preview.go internal/ui/wails/rpc.go internal/provider/codexapp/session.go internal/module/turn/rpc_helpers.go internal/module/uistate/projector.go internal/module/uistate/timeline/projector.go internal/module/uistate/terminalstatus/status.go
gopls check internal/ui/wails/code_preview.go internal/ui/wails/rpc.go internal/provider/codexapp/session.go internal/module/turn/rpc_helpers.go internal/module/uistate/projector.go internal/module/uistate/timeline/projector.go internal/module/uistate/terminalstatus/status.go
./scripts/test_with_guard.sh ./internal/ui/wails ./internal/provider/codexapp ./internal/module/turn ./internal/module/uistate/terminalstatus ./internal/module/uistate ./internal/module/uistate/timeline -count=1
cd frontend-app && npm run lint && npm test && npm run build
git diff --check
```

## Lane L02: MCP-LSP Edit Atomicity And Tool Argument Fail-Fast

**Risks:** R02, R12.

**Files:**
- Modify: `cmd/mcp-lsp/tools/tool_edit_replace_update.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_rename.go`
- Modify: `cmd/mcp-lsp/tools/factory.go`
- Create: `cmd/mcp-lsp/tools/atomic_write.go`
- Create test: `cmd/mcp-lsp/tools/tool_edit_atomic_write_test.go`
- Test: `cmd/mcp-lsp/tools/tool_edit_rename_test.go`
- Test: `cmd/mcp-lsp/tools/factory_test.go`

- [ ] **Step 1: RED atomic write tests.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run 'TestReplaceRangePreservesOriginalWhenInitialWriteFails|TestTextEditActionPreservesOriginalWhenWriteFails|TestRollbackUsesAtomicWrite' -count=1
```

Expected: FAIL because current implementation calls `os.WriteFile` directly.

- [ ] **Step 2: Implement R02 root fix and defense.**

Create `atomicReplaceFile(path string, content []byte, mode os.FileMode, writer fileWriter) error`. It writes to a hidden `.tmp-*` file in the same directory, fsyncs file and directory, then `os.Rename`s. Wire replace_range, LSP text edit actions, rename, and rollback through this helper. Keep direct `os.WriteFile` out of source edit paths.

- [ ] **Step 3: RED wrapper field tests.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run 'TestWrapperRejectsEmptyWorkDir|TestWrapperRejectsLegacyCWDInArguments|TestWrapperRejectsLegacyAgentIDInArguments' -count=1
```

Expected: FAIL because `work_dir`, `cwd`, and `agent_id` are currently stripped from arguments.

- [ ] **Step 4: Implement R12 root fix and defense.**

Replace `stripKnownToolWrapperFields` with `validateReservedToolWrapperFields`. `work_dir` may only be consumed by `contextWithExplicitToolWorkDir` when non-empty and valid. `cwd` and `agent_id` inside `arguments` return an explicit migration error that names top-level `_cwd` and `_agentId`.

- [ ] **Step 5: Verify lane.**

```bash
gofmt -w cmd/mcp-lsp/tools/tool_edit_replace_update.go cmd/mcp-lsp/tools/tool_edit_lsp_actions.go cmd/mcp-lsp/tools/tool_edit_rename.go cmd/mcp-lsp/tools/factory.go cmd/mcp-lsp/tools/atomic_write.go
gopls check cmd/mcp-lsp/tools/tool_edit_replace_update.go cmd/mcp-lsp/tools/tool_edit_lsp_actions.go cmd/mcp-lsp/tools/tool_edit_rename.go cmd/mcp-lsp/tools/factory.go cmd/mcp-lsp/tools/atomic_write.go
./scripts/test_with_guard.sh ./cmd/mcp-lsp ./cmd/mcp-lsp/tools -count=1
git diff --check
```

## Lane L03: Thread Recover, Tool Result Persistence, And Agent Archive Semantics

**Risks:** R03, R09, R13.

**Files:**
- Modify: `internal/module/thread/lifecycle_fork.go`
- Modify: `internal/module/thread/lifecycle.go`
- Modify: `internal/provider/unified/session.go`
- Modify: `internal/app/modules.go`
- Modify: `internal/provider/shared/hooks.go`
- Modify: `internal/dto/tool/event.go`
- Modify: `internal/provider/codexapp/event_map.go`
- Modify: `internal/provider/codexapp/session_rollout_events.go`
- Modify: `internal/provider/codexapp/session_enrich.go`
- Modify: `internal/provider/claudecli/event_map.go`
- Modify: `internal/platform/eventsurface/bind.go`
- Modify: `internal/module/uistate/timeline/projector_parity.go`
- Modify: `cmd/mcp-orch/tools/orchestration_tools.go`
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Test: `internal/module/thread/fork_isolation_test.go`
- Test: `internal/provider/unified/client_test.go`
- Test: `internal/module/turn/tool_result_storage_test.go`
- Test: `internal/provider/codexapp/event_map_test.go`
- Test: `internal/provider/claudecli/event_map_test.go`
- Test: `internal/platform/eventsurface/bind_test.go`
- Test: `internal/archtest/wire_dto_field_registry_test.go`
- Test: `internal/module/uistate/timeline/projector_parity_test.go`
- Test: `cmd/mcp-orch/tools/orchestration_tools_test.go`
- Test: `cmd/mcp-orch/orchestration/archive_test.go`

- [ ] **Step 1: RED recover pending-session test.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/unified -run 'TestServiceRecoverActivatesResumedSessionAfterPersist|TestServiceRecoverCleansPendingSessionWhenPersistFails' -count=1
```

Expected: FAIL because recover looks up a still-pending session and no cleanup assertion exists.

- [ ] **Step 2: Implement R03.**

Change `ensureRecoveredSession` to return `(mode string, session contract.Session, err error)`. Use the returned session for recover state persistence. Call `activateResumedSession(agentID)` only after `persistThreadState` succeeds; on any post-resume error call the same pending-session cleanup used by normal resume failures.

- [ ] **Step 3: RED tool-result persistence tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/turn ./internal/provider/codexapp ./internal/provider/claudecli -run 'TestToolResultPersistFailurePropagatesToProviderRecord|TestToolCallEndReportsPersistFailure' -count=1
./scripts/test_with_guard.sh ./internal/platform/eventsurface -run 'TestToolCallEndPayloadIncludesPersistFailure' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'TestWireDTORegistryCoversSelectedSurfaceFields|TestWireDTOFieldRegistryRequiresToolCallEndPersistFailure' -count=1
./scripts/test_with_guard.sh ./internal/module/uistate/timeline -run 'TestRegisterSubscriptions_ToolCallEndWithoutToolNameUpdatesBeginRow|TestToolCallEndPersistFailureShowsTimelineWarning' -count=1
```

Expected: FAIL because provider shared record drops `PersistFailed/PersistError`, eventsurface omits the fields, or timeline does not surface persistence failure.

- [ ] **Step 4: Implement R09.**

Add `PersistFailed` and `PersistError` to `providershared.ToolResultRecord` and `tooldto.ToolCallEnd`. Update provider hooks, Codex/Claude event translators, `internal/platform/eventsurface/bind.go`, the wire DTO field registry, and timeline/uistate projection so a successful tool whose result failed to persist emits a warning/error diagnostic and visible timeline item.

- [ ] **Step 5: RED archive outcome tests.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools -run 'TestArchiveAgentMissingReturnsNotFound|TestHandleStopAgentDoesNotReportArchivedForNoop' -count=1
```

Expected: FAIL because missing agent currently reaches nil return and `archived=true`.

- [ ] **Step 6: Implement R13.**

Change archive service API to return `ArchiveOutcome{RuntimeStopped, ThreadArchived, BindingArchived bool}`. `HandleStopAgent` sets `archived=true` only when any outcome field is true; all false returns typed `ErrAgentNotFound`.

- [ ] **Step 7: Verify lane.**

```bash
gofmt -w internal/module/thread/lifecycle_fork.go internal/module/thread/lifecycle.go internal/provider/unified/session.go internal/app/modules.go internal/provider/shared/hooks.go internal/dto/tool/event.go internal/provider/codexapp/event_map.go internal/provider/codexapp/session_rollout_events.go internal/provider/codexapp/session_enrich.go internal/provider/claudecli/event_map.go internal/platform/eventsurface/bind.go internal/platform/eventsurface/bind_test.go internal/archtest/wire_dto_field_registry_test.go internal/module/uistate/timeline/projector_parity.go internal/module/uistate/timeline/projector_parity_test.go cmd/mcp-orch/tools/orchestration_tools.go cmd/mcp-orch/orchestration/service.go
gopls check internal/module/thread/lifecycle_fork.go internal/module/thread/lifecycle.go internal/provider/unified/session.go internal/app/modules.go internal/provider/shared/hooks.go internal/dto/tool/event.go internal/provider/codexapp/event_map.go internal/provider/codexapp/session_rollout_events.go internal/provider/codexapp/session_enrich.go internal/provider/claudecli/event_map.go internal/platform/eventsurface/bind.go internal/platform/eventsurface/bind_test.go internal/archtest/wire_dto_field_registry_test.go internal/module/uistate/timeline/projector_parity.go internal/module/uistate/timeline/projector_parity_test.go cmd/mcp-orch/tools/orchestration_tools.go cmd/mcp-orch/orchestration/service.go
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/unified ./internal/module/turn ./internal/provider/codexapp ./internal/provider/claudecli ./internal/platform/eventsurface ./internal/archtest ./internal/module/uistate ./internal/module/uistate/timeline ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools -count=1
git diff --check
```

## Lane L04: Cron And DAG Scheduling Fences

**Risks:** R04, R05, R20.

**Files:**
- Modify: `internal/module/cron/scheduler.go`
- Modify: `internal/module/cron/scheduler_recovery.go`
- Modify: `internal/module/cron/progress_subscriber.go`
- Modify: `internal/module/cron/module.go`
- Modify: `internal/store/cron/contract.go`
- Modify: `internal/store/cron/store.go`
- Modify: `sql/queries/cron_job.sql`
- Modify migration: `internal/platform/db/sqlite/migrations/001_baseline.sql`
- Create migration: `internal/platform/db/sqlite/migrations/114_cron_job_runs_turn_status_index.sql`
- Modify generated: `internal/store/sqlc/cron_job.sql.go`
- Modify generated: `internal/store/sqlc/querier.go`
- Modify: `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`
- Modify: `cmd/mcp-orch/orchestration/node_executor_dispatch.go`
- Modify: `cmd/mcp-orch/orchestration/node_router.go`
- Modify: `cmd/mcp-orch/orchestration/nodeexec/executor_agent.go`
- Modify: `cmd/mcp-orch/store/taskdag/contract.go`
- Modify: `cmd/mcp-orch/store/taskdag/store_node_spawn.go`
- Modify: `cmd/mcp-orch/sql/queries/task_dag_node_spawning_thread.sql`
- Modify generated: `cmd/mcp-orch/store/sqlc/task_dag_node_spawning_thread.sql.go`
- Test: `internal/platform/db/sqlite/schema_baseline_test.go`
- Test: `internal/platform/db/sqlite/schema_contract_test.go`
- Test: `internal/platform/db/sqlite/query_plan_test.go`
- Test: `internal/platform/db/sqlite/migrate_test.go`
- Test: `internal/module/cron/scheduler_test.go`
- Test: `cmd/mcp-orch/orchestration/wakeup_dispatcher_test.go`
- Test: `cmd/mcp-orch/orchestration/nodeexec/executor_agent_test.go`
- Test: `cmd/mcp-orch/store/taskdag/store_sqlite_task11_test.go`
- Test: `cmd/mcp-orch/store/taskdag/store_update_running_status_test.go`

- [ ] **Step 1: RED cron early terminal race tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/cron -run 'TestPersistSubmittedTurnAtomicWithActiveTurn|TestTerminalEarlyArrivalDoesNotBecomePermanentStale|TestSetActiveTurnFailureDoesNotPublishSubmitted' -count=1
```

Expected: FAIL because current order publishes submitted before active turn is written.

- [ ] **Step 2: Implement R04.**

Add store method `SubmitRunWithActiveTurn(ctx, runID, jobID, claimToken, result, now)` in the cron store contract/adapter using an immediate transaction. It writes run turn, active turn, and submitted status atomically. Move `publishRunState(...submitted...)` after the transaction succeeds.

- [ ] **Step 3: RED cron full-scan tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/cron -run 'TestTerminalRunByTurnIDUsesPointLookup|TestRecoverDanglingRunsProcessesBatches' -count=1
```

Expected: FAIL because submitted/running fallback lists all unresolved runs.

- [ ] **Step 4: Implement R20.**

Add SQL query `GetSubmittedOrRunningRunByTurnID` and index on `(turn_id, status)`. Change startup recovery to `ListUnresolvedRunsPage(limit, cursor)` and iterate fixed batches. Keep per-batch cap visible in logs. Update baseline, forward migration, `internal/store/sqlc/querier.go`, and SQLite schema/query-plan tests so the index is enforced outside the cron package.

- [ ] **Step 5: RED DAG duplicate launch tests.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/store/taskdag -run 'TestWakeupLeaseExpiryDoesNotDuplicateChildLaunch|TestRecordNodeSpawnRequiresWakeupFence|TestRunningWriteConflictStopsSpawnedChild' -count=1
```

Expected: FAIL because spawn/running writeback has no wakeup lease fence.

- [ ] **Step 6: Implement R05.**

Extend `RouteByWakeup` input in `cmd/mcp-orch/orchestration/node_router.go` with claim token, wakeup id, attempt, and lease deadline. Store methods `RecordNodeSpawn` and `UpdateRunningTaskDagNodeStatus` require that fence. `executor_agent.go` must stop/archive the launched child when spawn or running writeback CAS fails before returning retry/fail.

- [ ] **Step 7: Verify lane.**

```bash
make sqlc-generate && make sqlc-verify
gofmt -w internal/module/cron/scheduler.go internal/module/cron/scheduler_recovery.go internal/module/cron/progress_subscriber.go internal/module/cron/module.go internal/store/cron/contract.go internal/store/cron/store.go internal/store/sqlc/cron_job.sql.go internal/store/sqlc/querier.go cmd/mcp-orch/orchestration/wakeup_dispatcher.go cmd/mcp-orch/orchestration/node_executor_dispatch.go cmd/mcp-orch/orchestration/node_router.go cmd/mcp-orch/orchestration/nodeexec/executor_agent.go cmd/mcp-orch/store/taskdag/contract.go cmd/mcp-orch/store/taskdag/store_node_spawn.go cmd/mcp-orch/store/sqlc/task_dag_node_spawning_thread.sql.go
gopls check internal/module/cron/scheduler.go internal/module/cron/scheduler_recovery.go internal/module/cron/progress_subscriber.go internal/module/cron/module.go internal/store/cron/contract.go internal/store/cron/store.go internal/store/sqlc/querier.go cmd/mcp-orch/orchestration/wakeup_dispatcher.go cmd/mcp-orch/orchestration/node_executor_dispatch.go cmd/mcp-orch/orchestration/node_router.go cmd/mcp-orch/orchestration/nodeexec/executor_agent.go cmd/mcp-orch/store/taskdag/contract.go cmd/mcp-orch/store/taskdag/store_node_spawn.go
./scripts/test_with_guard.sh ./internal/module/cron ./internal/platform/db/sqlite ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/store/taskdag -count=1
git diff --check
```

## Lane L05: Logging, Secrets, And Provider Prompt Privacy

**Risks:** R06, R07, R08, R16.

**Files:**
- Modify: `internal/provider/codexapp/recovery.go`
- Modify: `internal/provider/codexapp/event_map.go`
- Modify: `internal/provider/claudecli/transport_config.go`
- Modify: `internal/module/memory/retrieval/render.go`
- Modify: `internal/module/memory/retrieval/entry_read.go`
- Modify: `internal/module/memory/rules_provider.go`
- Modify: `internal/platform/bus/sink.go`
- Modify: `internal/platform/observability/sanitizer.go`
- Modify: `internal/platform/observability/safe_preview.go`
- Test: `internal/provider/codexapp/recovery_escalation_test.go`
- Test: `internal/provider/claudecli/transport_config_test.go`
- Test: `internal/module/memory/retrieval/retrieval_render_attachment_test.go`
- Test: `internal/module/memory/rules_test.go`
- Test: `internal/platform/bus/sink_test.go`
- Test: `internal/archtest/observability_log_event_guard_test.go`

- [ ] **Step 1: RED Codex auth leak tests.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestConnectionDeadInvalidAPIKeyDoesNotLeakSecretToTurnOrAgentFailed|TestConnectionDeadSafeErrorPreservesAuthClassification' -count=1
```

Expected: FAIL because raw reason is used in turn error and event data.

- [ ] **Step 2: RED Claude MCP launch leak tests.**

```bash
./scripts/test_with_guard.sh ./internal/provider/claudecli -run 'TestLogManifestLaunchRedactsArgsAndURLSecrets|TestLogManifestLaunchKeepsEnvKeysOnly' -count=1
```

Expected: FAIL because args and URL are logged directly.

- [ ] **Step 3: RED memory absolute path prompt tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory ./internal/module/memory/retrieval -run 'TestMemoryRulesProviderDoesNotLeakAbsoluteDisplayPath|TestRenderAttachmentTextUsesRelativeDisplayPath|TestRenderAttachmentTextRejectsAbsoluteMemoryPathLeak' -count=1
```

Expected: FAIL because `memoryDisplayPath` returns `entry.FilePath`.

- [ ] **Step 4: RED bus preview tests.**

```bash
./scripts/test_with_guard.sh ./internal/platform/bus ./internal/archtest -run 'TestBusLogArgsDoNotIncludeEventPreview|TestBusSafeSummaryOmitsCWDPromptAndDelta|TestProductionLogsUseSafeSummaries' -count=1
```

Expected: FAIL because `event_preview` exists and generic JSON preview can include cwd/text/delta.

- [ ] **Step 5: Implement R06, R07, R08, and R16 with scoped sanitizer owners.**

Extend `internal/platform/observability` only for generic log-safe summaries: provider error fields, MCP launch server summaries, and bus event summaries. Keep memory attachment display logic in `internal/module/memory/retrieval.MemoryDisplayPath(root, entry)` and update render/rules provider call sites. Codex recovery emits safe auth error codes after classifying the raw reason. Claude launch logs only safe MCP server summaries. Bus sink removes `event_preview` and logs only allowlisted fields.

- [ ] **Step 6: Verify lane.**

```bash
gofmt -w internal/provider/codexapp/recovery.go internal/provider/codexapp/event_map.go internal/provider/claudecli/transport_config.go internal/module/memory/retrieval/render.go internal/module/memory/retrieval/entry_read.go internal/module/memory/rules_provider.go internal/platform/bus/sink.go internal/platform/observability/sanitizer.go internal/platform/observability/safe_preview.go
gopls check internal/provider/codexapp/recovery.go internal/provider/codexapp/event_map.go internal/provider/claudecli/transport_config.go internal/module/memory/retrieval/render.go internal/module/memory/retrieval/entry_read.go internal/module/memory/rules_provider.go internal/platform/bus/sink.go internal/platform/observability/sanitizer.go internal/platform/observability/safe_preview.go
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/provider/claudecli ./internal/module/memory ./internal/module/memory/retrieval ./internal/platform/bus ./internal/archtest -count=1
git diff --check
```

## Lane L06: Memory Budgets And Skill Mirror Drift

**Risks:** R14, R17, R18.

**Files:**
- Modify: `internal/module/skill/mirror_publisher.go`
- Modify: `internal/module/skill/mirror_reconciler.go`
- Modify: `internal/provider/shared/provider_home.go`
- Modify: `internal/module/memory/retrieval/manifest.go`
- Modify: `internal/module/memory/retrieval/prefetch.go`
- Modify: `internal/module/memory/consolidation_prompt.go`
- Modify: `internal/module/memory/index.go`
- Modify: `internal/module/memory/config.go`
- Modify: `internal/module/memory/retrieval/entry_read.go`
- Test: `internal/module/skill/mirror_publisher_test.go`
- Test: `internal/provider/shared/provider_home_test.go`
- Test: `internal/module/memory/retrieval/manifest_test.go`
- Test: `internal/module/memory/retrieval/prefetch_test.go`
- Test: `internal/module/memory/consolidation_prompt_test.go`

- [ ] **Step 1: RED personal mirror drift tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/provider/shared -run 'TestPersonalMirrorDriftBlocksProviderStartup|TestDeletedPersonalMirrorDriftBlocksProviderStartup|TestSkippedDriftIsNotIgnoredForProviderReadableTargets' -count=1
```

Expected: FAIL because personal drift is appended to `Skipped`.

- [ ] **Step 2: Implement R14.**

Create shared predicate `isProviderReadableMirrorDrift(target, item)`. `publishCanonicalRecords`, `deleteMissingCanonicalRecords`, and `EnsureNoSkillMirrorConflicts` all treat provider-readable personal drift as blocking conflict. Keep non-provider-readable report-only paths as skipped.

- [ ] **Step 3: RED memory manifest budget tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory/retrieval -run 'TestBuildManifestStopsWalkingAtMaxFiles|TestScanHeadersSafeReturnsReadError|TestPrefetchReportsManifestTruncated' -count=1
```

Expected: FAIL because max files is applied after full scan and read errors are skipped.

- [ ] **Step 4: Implement R17.**

Introduce `ManifestScanBudget{MaxFiles, MaxBytes, MaxReadErrors}`. Enforce limits inside `WalkDir` with `fs.SkipAll` when exhausted. Return typed errors for I/O/header failures and truncation; `PrefetchHandle.Err()` surfaces those to turn context. Keep scanning helpers in the existing memory index/retrieval files rather than adding a new store-scan layer.

- [ ] **Step 5: RED consolidation budget tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory -run 'TestConsolidationPromptRejectsTooManyTopicFiles|TestConsolidationPromptRejectsOversizedLogDocuments|TestConsolidationPromptHonorsContextCancel' -count=1
```

Expected: FAIL because consolidation reads all topic files/logs before prompt cap.

- [ ] **Step 6: Implement R18.**

Reuse the memory scan budget for topic files and logs. Add `MaxConsolidationFiles`, `MaxConsolidationFileBytes`, and `MaxConsolidationTotalBytes` defaults in memory config. Exceeding a limit returns a typed consolidation diagnostic and aborts provider prompt construction.

- [ ] **Step 7: Verify lane.**

```bash
gofmt -w internal/module/skill/mirror_publisher.go internal/module/skill/mirror_reconciler.go internal/provider/shared/provider_home.go internal/module/memory/retrieval/manifest.go internal/module/memory/retrieval/prefetch.go internal/module/memory/retrieval/entry_read.go internal/module/memory/consolidation_prompt.go internal/module/memory/index.go internal/module/memory/config.go
gopls check internal/module/skill/mirror_publisher.go internal/module/skill/mirror_reconciler.go internal/provider/shared/provider_home.go internal/module/memory/retrieval/manifest.go internal/module/memory/retrieval/prefetch.go internal/module/memory/retrieval/entry_read.go internal/module/memory/consolidation_prompt.go internal/module/memory/index.go internal/module/memory/config.go
./scripts/test_with_guard.sh ./internal/module/skill ./internal/provider/shared ./internal/module/memory ./internal/module/memory/retrieval -count=1
git diff --check
```

## Lane L07: Store, Query, And Datasource Capacity Bounds

**Risks:** R19, R21, R22, R24.

**Files:**
- Modify: `internal/module/thread/service.go`
- Modify: `internal/module/thread/rpc.go`
- Modify: `internal/module/thread/contract_adapter.go`
- Modify: `internal/contract/thread.go`
- Modify: `internal/ui/wails/module.go`
- Modify: `internal/store/thread/store.go`
- Modify: `sql/queries/agent_thread.sql`
- Modify generated: `internal/store/sqlc/agent_thread.sql.go`
- Modify: `internal/platform/hooks/module.go`
- Modify: `internal/platform/hooks/resolver.go`
- Modify: `internal/platform/hooks/manager.go`
- Modify: `internal/contract/hooks.go`
- Modify: `internal/store/hookstore/hookstore.go`
- Modify: `sql/queries/hook_pending_review.sql`
- Modify generated: `internal/store/sqlc/hook_pending_review.sql.go`
- Modify: `internal/store/datasource/store.go`
- Modify: `internal/module/datasource/service.go`
- Modify: `internal/module/datasource/prompt_provider.go`
- Create: `sql/queries/datasource.sql`
- Create generated: `internal/store/sqlc/datasource.sql.go`
- Modify generated: `internal/store/sqlc/querier.go`
- Modify: `internal/module/datasource_v2/service.go`
- Modify: `internal/module/datasource_v2/rpc.go`
- Modify: `internal/module/datasource_v2/module.go`
- Modify: `internal/module/datasource_v2/store_port.go`
- Modify: `internal/store/datasourcev2/contract.go`
- Modify: `internal/store/datasourcev2/store.go`
- Modify: `sql/queries/datasource_v2.sql`
- Modify generated: `internal/store/sqlc/datasource_v2.sql.go`
- Modify: `internal/platform/db/sqlite/migrations/001_baseline.sql`
- Create migration: `internal/platform/db/sqlite/migrations/115_agent_thread_paging_indexes.sql`
- Create migration: `internal/platform/db/sqlite/migrations/116_datasource_documents.sql`
- Test: `internal/platform/db/sqlite/schema_baseline_test.go`
- Test: `internal/platform/db/sqlite/schema_contract_test.go`
- Test: `internal/platform/db/sqlite/query_plan_test.go`
- Test: `internal/platform/db/sqlite/migrate_test.go`
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify: `frontend-app/src/pages/skills/SkillsPage.jsx`
- Test: `internal/module/thread/service_test.go`
- Test: `internal/ui/wails/lifecycle_test.go`
- Test: `internal/platform/hooks/module_test.go`
- Test: `internal/store/hookstore/hookstore_test.go`
- Test: `internal/module/datasource/service_test.go`
- Test: `internal/module/datasource/prompt_provider_test.go`
- Test: `internal/store/datasource/store_test.go`
- Test: `internal/module/datasource_v2/rpc_test.go`
- Test: `internal/module/datasource_v2/prompt_provider_test.go`
- Create test: `internal/module/datasource_v2/service_test.go`
- Test: `frontend-app/src/shared/api/backendApi.test.js`
- Test: `frontend-app/src/pages/skills/SkillsPage.test.jsx`

- [ ] **Step 1: RED thread pagination/count tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/ui/wails ./internal/store/thread -run 'TestListThreadsUsesLimitAndCursor|TestLoadedThreadsUsesSQLFilter|TestActiveAgentCounterUsesCountQuery' -count=1
```

Expected: FAIL because current service reads all agent threads.

- [ ] **Step 2: Implement R19.**

Add SQL queries for paged thread list, loaded list, and active count. Introduce explicit paged contracts (`thread/listPage`, `thread/loaded/listPage`) whose RPC request must include `limit` and cursor fields; missing limit returns a typed validation error. Keep max limit 200. Production UI/Wails callers migrate to paged/count APIs. Any legacy no-arg compatibility route must not be used by production UI and must have a hard cap plus a deprecation regression test.

- [ ] **Step 3: RED hook pending cap tests.**

```bash
./scripts/test_with_guard.sh ./internal/platform/hooks ./internal/store/hookstore -run 'TestRecoverOnStartupUsesPendingReviewCount|TestListPendingReviewsRequiresLimit|TestListPendingReviewsCapsLimit' -count=1
```

Expected: FAIL because startup loads full pending review rows.

- [ ] **Step 4: Implement R21.**

Add `CountPendingReviews` and paged `ListPendingReviewsPage` across `internal/contract/hooks.go`, hook manager/resolver, and hookstore. Startup recovery logs count and only processes pages when recovery needs row detail. Per-agent pending query requires explicit limit/cursor and caps at 500.

- [ ] **Step 5: RED datasource schema and prompt cap tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/datasource ./internal/store/datasource ./internal/platform/db/sqlite -run 'TestDatasourceDocumentsTableComesFromMigration|TestDocumentStoreDoesNotCreateDatasourceTable|TestDatasourcePromptRejectsOversizedWorkspaceDocuments|TestDatasourcePromptUsesBoundedSummaries|TestSQLiteBaselineCreatesRuntimeTables|TestSQLiteBaselineContracts' -count=1
```

Expected: FAIL because table is created by store SQL and prompt renders full content.

- [ ] **Step 6: Implement R22.**

Move `datasource_documents` table into SQLite baseline migration, a forward migration, `sql/queries/datasource.sql`, and generated sqlc files. Replace store self-created table with a migration-backed store that only wraps sqlc querier methods. Rewrite the existing datasource store lazy-create test so runtime DDL is forbidden, and add SQLite baseline/schema contract coverage for the table. Prompt provider reads bounded summaries with count/byte caps and returns critical prompt error on overflow.

- [ ] **Step 7: RED datasource v2 pagination tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/datasource_v2 ./internal/store/datasourcev2 -run 'TestDatasourceV2GetReturnsFirstPageOnly|TestDatasourceV2ListChunksUsesCursorAndLimit|TestDatasourceV2GetCapsResponseBytes' -count=1
cd frontend-app && npm test -- src/shared/api/backendApi.test.js src/pages/skills/SkillsPage.test.jsx
```

Expected: FAIL because `get` materializes all chunks/content, frontend API matrix lacks `datasourceV2/list_chunks`, or SkillsPage assumes the first `chunks` payload is complete.

- [ ] **Step 8: Implement R24.**

Change `datasourceV2/get` to return metadata plus first chunk page. Add `datasourceV2/list_chunks` with explicit `limit`, `cursor`, `hasMore`, and `nextCursor`. Enforce response byte cap in service before RPC response. Update `backendApi.js`, its contract matrix, and `SkillsPage.jsx` so frontend callers request additional chunks through the paged API instead of assuming `chunks` is complete.

- [ ] **Step 9: Verify lane.**

```bash
make sqlc-generate && make sqlc-verify
gofmt -w internal/module/thread/service.go internal/module/thread/rpc.go internal/module/thread/contract_adapter.go internal/contract/thread.go internal/ui/wails/module.go internal/store/thread/store.go internal/platform/hooks/module.go internal/platform/hooks/resolver.go internal/platform/hooks/manager.go internal/contract/hooks.go internal/store/hookstore/hookstore.go internal/store/datasource/store.go internal/module/datasource/service.go internal/module/datasource/prompt_provider.go internal/module/datasource_v2/service.go internal/module/datasource_v2/rpc.go internal/module/datasource_v2/module.go internal/module/datasource_v2/store_port.go internal/store/datasourcev2/contract.go internal/store/datasourcev2/store.go internal/store/sqlc/agent_thread.sql.go internal/store/sqlc/hook_pending_review.sql.go internal/store/sqlc/datasource.sql.go internal/store/sqlc/datasource_v2.sql.go internal/store/sqlc/querier.go
gopls check internal/module/thread/service.go internal/module/thread/rpc.go internal/module/thread/contract_adapter.go internal/contract/thread.go internal/ui/wails/module.go internal/store/thread/store.go internal/platform/hooks/module.go internal/platform/hooks/resolver.go internal/platform/hooks/manager.go internal/contract/hooks.go internal/store/hookstore/hookstore.go internal/store/datasource/store.go internal/module/datasource/service.go internal/module/datasource/prompt_provider.go internal/module/datasource_v2/service.go internal/module/datasource_v2/rpc.go internal/module/datasource_v2/module.go internal/module/datasource_v2/store_port.go internal/store/datasourcev2/contract.go internal/store/datasourcev2/store.go
./scripts/test_with_guard.sh ./internal/module/thread ./internal/ui/wails ./internal/platform/hooks ./internal/store/hookstore ./internal/module/datasource ./internal/module/datasource_v2 ./internal/store/... ./internal/platform/db/... -count=1
cd frontend-app && npm run lint && npm test -- src/shared/api/backendApi.test.js src/pages/skills/SkillsPage.test.jsx && npm run build
git diff --check
```

## Lane L08: macOS Packaging APP_NAME Contract

**Risks:** R23.

**Files:**
- Modify: `scripts/package_macos.sh`
- Test: `scripts/package_macos_guard_test.go`

- [ ] **Step 1: RED custom app name packaging test.**

```bash
go test ./scripts -run 'TestPackageMacOSInstallerUsesCustomAppName|TestPackageMacOSInstallerRejectsUnsafeAppName' -count=1
```

Expected: FAIL because install heredoc hard-codes `APP_NAME="Super Dolphin"`.

- [ ] **Step 2: Implement R23 root fix and defense.**

Keep `APP_NAME` override support. Validate app name against a conservative pattern that excludes slash, control characters, and shell metacharacters. Generate install script with a quoted literal from the validated value so `SRC_APP="$APP_DIR/$APP_NAME.app"` matches the staged app.

- [ ] **Step 3: Verify lane.**

```bash
bash -n scripts/package_macos.sh
go test ./scripts -run 'TestPackageMacOS|TestMacOS|TestVerifyPackagedAppMacOS' -count=1
git diff --check
```

## Final Integration Checklist

- [ ] Rebase or merge each lane in fixed order L01 -> L08.
- [ ] After each lane, run the lane verification command again in integration worktree.
- [ ] After all lanes:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... ./cmd/mcp-orch/... ./internal/module/thread ./internal/module/turn ./internal/module/cron ./internal/module/uistate ./internal/module/memory ./internal/module/memory/retrieval ./internal/module/skill ./internal/provider/codexapp ./internal/provider/claudecli ./internal/provider/shared ./internal/platform/bus ./internal/platform/hooks ./internal/ui/wails ./internal/store/... ./internal/platform/db/... -count=1
cd frontend-app && npm run lint && npm test && npm run build
go test ./scripts -count=1
git diff --check
git status --short
```

Expected: all commands pass; `git status --short` only shows intended integration files.

- [ ] If a worker claims an upper-layer defense is unnecessary for any risk marked `必须` or `需要`, stop that lane and return `NEEDS_APPROVAL` with source evidence. Controller approval is required before changing this plan's defense decision.
