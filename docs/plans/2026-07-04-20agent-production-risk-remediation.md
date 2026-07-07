# 20-Agent Production Risk Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: 修复源码 lane 必须使用 superpowers:子代理驱动开发；只读裁决、文档核验、逐项执行计划检查必须使用 superpowers:执行计划。In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-07-04 20-agent 按文件环形审查中确认的生产风险，并为每个风险固定唯一最优修复和必要的上层防御。

**Architecture:** 本计划按文件责任边界拆 lane：mcp-lsp 先修工具权限和诊断一致性，mcp-orch 修 fork/approval 生命周期，provider 修 Codex turn fail-fast 与恢复幂等，unified resolver 修 session 身份和 auto-resume，thread 修持久化元数据与失败清理，RPC 修认证前 active peer，skill 修 mirror drift 事实源。每项先补 RED 测试锁住当前坏行为，再做最小实现，最后补入口级、共享 helper、状态机、registry 或 guard 层的上层防御，防止相邻入口绕过单点修复。

**Tech Stack:** Go 1.25.7、MCP stdio/http、mcp-lsp、mcp-orch、Fx lifecycle、SQLite/sqlc、Wails RPC、Codex provider adapter、repo guard scripts。

**Verification Surface:** Go 变更使用 `./scripts/test_with_guard.sh <affected packages> -count=1`；mcp-lsp 变更额外运行 `gopls check <touched files>`；SQL/store 变更运行 `make sqlc-verify`；计划/文档变更运行 `git diff --check`；整合前运行 `git status --short` 并确认只包含本 lane 文件。

---

## Current Boundary

- [ ] 审查基线: `main @ e703982743ce7b59f9ef813ed701527ac6554344`。
- [ ] 本文件是修复计划，不代表已批准执行源码修改。
- [ ] 本轮审查是只读环形审查，20 个 agent 按 10 个文件双审；控制器复核了 P1/P2 候选的当前源码窗口。
- [ ] 当前主工作树在计划创建前 `git status --short` 无输出。执行修复前仍必须重新检查 dirty 边界。
- [ ] 本轮控制器可用工具面未暴露仓库 MCP LSP `grep/file/structure/inspect/xref/diagnostics` 工具；已用 `gopls check` 对 10 个目标文件补做诊断检查。执行源码修复时，如果 LSP MCP 工具仍不可用，worker 必须至少运行 `gopls check`、`gopls references` 或等价 gopls CLI 取证，并记录 blocker。

## Global Execution Rules

- [ ] 控制器执行前运行:

```bash
git status --short
rg --files | rg '(^AGENTS.md$|README.md$|docs/doc/codemap/README.md$)'
```

Expected: 记录当前 dirty 边界；无关文件保持原样。

- [ ] 每个 lane 使用独立 worktree，分支命名 `codex/20260704-risk-lXX-<slug>`，worktree 命名 `.worktrees/20260704-risk-lXX-<slug>`。

```bash
base_branch=$(git branch --show-current)
git worktree add ".worktrees/20260704-risk-lXX-<slug>" -b "codex/20260704-risk-lXX-<slug>" "$base_branch"
cd ".worktrees/20260704-risk-lXX-<slug>"
git status --short
```

Expected: 新 worktree 干净；如不干净，停止并返回真实路径和状态。

- [ ] worker 只能改本计划 lane 写集列出的文件和同包测试。需要越界时返回 `NEEDS_APPROVAL`，包含路径、原因、缺少该越界无法关闭的风险。
- [ ] 每个风险先写 RED 测试并确认失败，再实现最小修复，再运行 GREEN 验证。禁止先改生产代码再补测试。
- [ ] 每个 Go 源文件改完后至少运行:

```bash
gofmt -w <touched-go-files>
gopls check <touched-go-files>
```

Expected: `gopls check` 无 diagnostics 输出。

- [ ] 每个 lane 完成前运行 `git diff --check`。若新建文件仍 untracked，先 `git add -N <new-file>` 或逐个运行 `git diff --no-index --check /dev/null <new-file>`。
- [ ] 6-agent 执行映射固定: A1=L01, A2=L02, A3=L03, A4=L04+L06, A5=L05, A6=L07+L08。A4 同时 owns unified resolver 与 RPC auth/approval 边界，避免第二波串行；A6 同时 owns skill mirror 与 R15 日志防线，且是 R15 log call-site 的唯一 owner，其他 agent 不修改日志 call sites。
- [ ] 集成顺序固定: A1 L01 -> A2 L02 -> A3 L03 -> A4 L04+L06 -> A5 L05 -> A6 L07+L08。若 A6 的 R15 log call-site patch 与 A1/A3/A5 发生同文件冲突，保留 A6 的 safe logging 语义，再按对应 lane 的非日志修复重放。

## Risk Matrix: Unique Fix and Upper Defense

**5-agent adjudication result:** R01-R17 and R19 stay in the production-risk repair queue. R18 is removed from the production-risk queue because current HEAD maps every existing `dto.StartAssembly` field; it remains only as optional hardening if a later lane chooses to add future-drift coverage. Controller retained R11 despite one OUT vote because `resolveForkContext` uses empty `threadMeta` after `lookupThreadMeta` failure and can still continue from binding data.

**Additional cross-adjudication closure:** The latest 5-agent cross round produced 1 ACCEPT_PLAN and 4 NEEDS_DOC_PATCH votes. No agent added or removed a production-reachable risk. Applied closure patches are: deterministic worker skill selection, R10 no-UI fail-closed behavior, L04 `session_resolver_auto_resume.go` write-set label, R07 internal-only resolver boundary, and R15 explicit safe runtime log helper plus `internal/archtest` guard scope. Execution is now compressed to 6 agents by combining L04+L06 and L07+L08 while preserving R15 single-owner semantics.

| ID | Severity | Evidence | 唯一最优修复 | 是否需要上层防御 | 上层防御最优落点与方案 |
|---|---|---|---|---|---|
| R01 | P1 | `cmd/mcp-lsp/tools/factory.go:74-97` + `cmd/mcp-lsp/search/fileutil.go:125-143` | `ResolvePathInRoots` 不再自动 fallback 到 app-managed roots；LSP `file/diagnostics` 默认只读 trusted workspace roots。 | 必须 | `cmd/mcp-lsp/tools/factory.go` 增加 `WithAppManagedReadCapability` 与 `toolReadableRoots`；只有应用侧显式授权的 tool context 才追加 app-managed read roots，direct MCP 调用缺能力时返回 `path_outside_workspace`。 |
| R02 | P1 | `cmd/mcp-lsp/multilsp/manager_lifecycle.go:480-493` + `manager_diagnostics.go:231-266` | full-document `DidChange` 后必须递增 per-URI/scope diagnostic epoch，并拒绝写入 pre-change epoch 的 nil-version diagnostics。 | 必须 | `manager_diagnostics.go` 增加 `diagnosticEpoch`/`documentChangeGeneration`，`PublishDiagnostics` 写入前必须匹配当前文档 epoch；`recordFullDocumentDidChange` 是唯一更新 epoch 的上层入口。 |
| R03 | P1 | `cmd/mcp-lsp/multilsp/manager_lifecycle.go:449-457` | `DidChange` 后重建 LSP client 时必须恢复完整 workspace bootstrap，不只 reopen 当前文档。 | 必须 | `rebuildClientAfterFailure` 调用点统一传入 restore policy；对 `DidChange` failure 使用 `restoreBootstrappedWorkspace`，并重置旧 client scope state，防止 fake-ready。 |
| R04 | P1 | `cmd/mcp-orch/orchestration/service_launcher_bridge.go:210-217` | `forked` launch 不再信任 tool payload 中的 `parent_thread_id`；parent thread 必须从可信 runtime/binding/store 解析。 | 必须 | `cmd/mcp-orch/tools/orchestration_tools.go` 只接收 `parent_id`；`orchestration.Service` 在 launch bridge 内按 caller/workspace/agent binding 解析 parent thread，无法证明归属时 fail-fast。 |
| R05 | P1 | `internal/provider/codexapp/session.go:419-438` + `internal/provider/codexapp/support.go:561-575` | 抽出同一 typed model resolver，`thread/start` 和 `turn/start` 在空/default model 必须依赖 `model/list` 时，列表失败或结果为空都直接阻断，不再 warn 后继续。 | 必须 | `internal/provider/codexapp/supportutil/model_config.go` 暴露 typed `ErrModelResolutionRequired`; `startRemoteThreadWithParams` 与 `applyRuntimeTurnStartOverrides` 统一调用该 resolver 并向用户返回 provider error。 |
| R06 | P1 | `internal/provider/codexapp/session.go:342-357` + `recovery.go:104-114` | `turn/start` 作为非幂等调用不得在 transport recovery 中自动重发；发送后未拿到 provider turn id 时返回 recoverable error。 | 必须 | `session.callTransport` 增加 per-method retry policy；`turn/start` 标记 `non_retryable_after_write`，只有 provider 支持 local idempotency key 后才能重试。 |
| R07 | P1 | `internal/provider/unified/session_resolver.go:56-70` | 公共 `ResolveSession(threadID)` 移除 agent-id 快路径；agent-id 解析拆成 internal-only `ResolveSessionByAgentID`。 | 必须 | `internal/module/turn/rpc_helpers.go` 和 RPC capability resolver 只调用 public thread/provider binding resolver；编译期接口拆分防止外部 RPC 参数绕过 binding。 |
| R08 | P1 | `internal/provider/unified/session_resolver.go:154-170` + `internal/store/binding/session_adapter.go:77-97` | `contract.SessionBinding` 增加 `Archived` 并从 store adapter 映射；auto-resume 遇到 archived binding 必须拒绝。 | 必须 | `rejectBindingAutoResumeLifecycle` 作为 resolver 唯一 binding lifecycle gate；public-thread 与 provider-thread 两条路径都必须先过该 gate。 |
| R09 | P1 | `internal/provider/unified/session_resolver.go:180-203` | cold auto-resume 使用 `golang.org/x/sync/singleflight.Group`，key 使用 canonical provider + agent id + provider/codex thread id；多个 `ResolveSession` 同时 miss 时只调用一次 provider `ResumeSession`。 | 必须 | `sessionResolver` 持有 resolver-local `singleflight.Group`；waiter 共享 owner 结果，resume identity/backfill 成功前不得把 session 暴露给 `SessionManager.Get`，失败 session 只由 owner close/cleanup 一次。 |
| R10 | P1 | `internal/platform/rpc/server.go:478-482` + `internal/platform/rpc/module.go:77-90` | TCP control RPC 连接只有 `ctl/register` token 校验成功后才进入 active peer；审批 callback 只允许 UI peer。 | 必须 | `approvalRequester.activeServer` 删除 fallback-to-any-peer；无 UI peer 时返回 typed `ErrNoUIPeer`/existing no-frontend decline 并 fail-closed，禁止 indefinite pending；只有已选中 UI peer 且 dispatch 出现可恢复错误时才进入 pending replay。`NotifyAll` 只广播 authenticated active peers。 |
| R11 | P1 | `internal/module/thread/lifecycle.go:586-590` | `lookupThreadMeta` 返回 `(threadMeta, error)`；`getThread` error 或 missing row 阻断 fork/recover。 | 必须 | fork/recover/snapshot rebuild 统一通过 `requireThreadMeta`；禁止空 metadata 被当成功恢复上下文。 |
| R12 | P1 | `internal/module/thread/lifecycle_helpers.go:407-420` + `internal/module/thread/lifecycle_fork.go:110-127` | 所有 public thread upsert builder 都持久化 `ParentAgentID`、`AgentType`、`AgentMemoryScope`，包括 start 的 `upsertPublicThread` 和 fork creating 的 `upsertForkThreadStatus`。 | 必须 | thread store DTO/roundtrip 测试把 subagent identity 作为必填映射守卫；start/fork/recover 读取同一持久化字段。 |
| R13 | P1 | `internal/module/thread/lifecycle.go:235-305` | start 失败清理必须 close/remove local provider session，再 stop orchestration agent；使用 generation 保护避免误删新 session。 | 必须 | 提取 `cleanupFailedStartedSession(ctx, agentID)`，内部调用 session provider cleaner 和 orchestration stop；所有 provider session 创建后的错误路径统一调用它。 |
| R14 | P1 | `internal/module/skill/mirror_reconciler.go:453-455` + `mirror_publisher.go:470-471` | `managedMirrorConflict` 把 `Owned=false` 视为 drift，即使 hash 匹配也必须进入 resolution inventory。 | 必须 | reconciler 与 publisher 共用 `driftedManagedMirror` predicate；provider startup blocking taxonomy 只消费这一份事实源。 |
| R15 | P2 | `cmd/mcp-lsp/fx.go:76-77`, `:263-292`; `cmd/mcp-lsp/tools/tool_file.go:122-128`; `internal/provider/codexapp/session.go:229-239`; `internal/provider/codexapp/support.go:568-595`; `internal/module/thread/start_session.go:205-211`, `:330-350`; `internal/module/thread/events.go:178` | 移除生产结构化日志中的 raw payload/cwd/prompt/config body；例行 trace 降为 debug/info，但 Info/Warn/Error 都只允许脱敏字段。 | 需要 | 在 `internal/platform/shared` 新增中央 safe runtime log sanitizer，并由 `internal/archtest` 静态 guard 约束 Info/Warn/Error。安全字段必须覆盖 path/root/cwd-derived values、payload、prompt、config body、instructions、sandbox_policy，只能输出 `has_*`、byte length、stable hash、display class 等摘要；禁止 raw `cwd`、absolute path、payload、prompt/config/instructions/sandbox_policy 直接进入生产日志。 |
| R16 | P2 | `cmd/mcp-lsp/fx.go:201-218` | non-packaged semantic LSP availability 只能从 runtime semantic LSP language adapter 的 server command 派生 binary list；禁止手工只补 `bash-language-server`。 | 需要 | `runtimeSemanticLSPServerBinaries` 改为从 `runtimePrimaryLanguageIDs()` + `multilsp.LanguageAdapterRegistry.AdapterForLanguage(...).ServerCommand(...).Executable` 生成；installer registry 只作为覆盖测试守卫，新增 semantic language 缺 server command 时 fail-fast。 |
| R17 | P2 | `cmd/mcp-orch/orchestration/service.go:253-271` | approval lifecycle 使用与 turn lifecycle 相同的 lifecycle context fence。 | 需要 | `RegisterApprovalLifecycle` 在 OnStart 建 context，OnStop 先 cancel 再 unsubscribe；handler 开头检查 ctx，防止已排队事件 shutdown 后写状态。 |
| R18 | OUT | `internal/module/thread/start_session.go:303-316` + `internal/dto/provider/session.go:58-75` | 不进入生产风险修复队列；当前 `toProviderStartAssembly` 覆盖现有 `dto.StartAssembly` 字段。 | 不需要 | 可选 hardening: 若后续要防未来 DTO 漂移，只在 `internal/module/thread/start_session_helpers_test.go` 增加 sentinel-value mapper 覆盖/深拷贝 guard；不得用 `contract.StartAssembly` vs `dto.StartAssembly` 字段比较，因为 contract 当前是 alias。 |
| R19 | P2 | `internal/module/skill/mirror_reconciler.go:269-290`, `:499-518` | mirror conflict 检测先判 managed/canonical/orphan，再 hash 目录；对 root entry 数量和单目录 hash 成本加 cap。 | 需要 | `DetectSkillMirrorConflicts` 入口加入 budget 参数和 default cap；personal orphan 目录不递归 hash，超限返回 typed `mirror_scan_truncated`。 |

## Lane L01: mcp-lsp Path, Diagnostics, Lifecycle, and Availability

**Risks:** R01, R02, R03, R16. R15 由 L08 单独串行处理，L01 不修改日志 call sites。

**Files:**
- Modify: `cmd/mcp-lsp/tools/factory.go`
- Modify: `cmd/mcp-lsp/search/fileutil.go`
- Modify: `cmd/mcp-lsp/tools/tool_file.go`
- Modify: `cmd/mcp-lsp/tools/tool_diagnostics.go`
- Modify: `cmd/mcp-lsp/multilsp/manager_lifecycle.go`
- Modify: `cmd/mcp-lsp/multilsp/manager_diagnostics.go`
- Modify: `cmd/mcp-lsp/multilsp/state.go`
- Modify: `cmd/mcp-lsp/fx.go`
- Modify: `cmd/mcp-lsp/runtime.go`
- Test: `cmd/mcp-lsp/tools/tool_file_app_managed_test.go`
- Test: `cmd/mcp-lsp/tools/tool_diagnostics_test.go`
- Test: `cmd/mcp-lsp/multilsp/p2_lifecycle_cache_hardening_diagnostics_shard11_test.go`
- Test: `cmd/mcp-lsp/multilsp/manager_lifecycle_test.go`
- Test: `cmd/mcp-lsp/tools_test.go`

- [ ] **Step 1: RED app-managed read boundary.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run 'TestFileReadRejectsAppManagedPathWithoutReadCapability|TestDiagnosticsRejectsAppManagedPathWithoutReadCapability' -count=1
```

Expected: FAIL because app-managed paths are currently accepted without explicit read capability.

- [ ] **Step 2: Implement R01 unique fix and defense.**

Create `WithAppManagedReadCapability(ctx)` beside `WithAppManagedWriteCapability`. Change `toolWorkspaceRoots` into two helpers: workspace-only roots for default reads, and app-managed roots only when read/write capability is present. Remove app-managed fallback from `search.ResolvePathInRoots`; callers that truly need app-managed access must pass those roots explicitly.

- [ ] **Step 3: RED nil-version diagnostic stale test.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/multilsp -run TestFullDocumentDidChangePurgesNilVersionDiagnostics -count=1
```

Expected: FAIL because nil-version diagnostics survive after a newer full-document change.

- [ ] **Step 4: Implement R02 unique fix and defense.**

Add per-document diagnostic epoch keyed by scope+URI. `recordFullDocumentDidChange` increments epoch, purges same-scope nil-version snapshots, and stores the epoch in bootstrap state. `publishDiagnosticsForGeneration` writes diagnostics only when its captured epoch is current.

- [ ] **Step 5: RED restart fake-ready regression.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/multilsp -run TestDidChangeRestartRestoresAllReadyDocuments -count=1
```

Expected: FAIL because only the changed document is reopened after rebuild.

- [ ] **Step 6: Implement R03 unique fix and defense.**

Change the DidChange recovery path to call `rebuildClientAfterFailure(ctx, client, true)` with the existing full workspace restore behavior. Reset stale ready state for the replaced client before returning.

- [ ] **Step 7: RED shell availability test.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp -run TestRuntimeSemanticLSPServerBinariesIncludeShell -count=1
```

Expected: FAIL because shell availability is not derived from the runtime semantic language adapter server command.

- [ ] **Step 8: Implement R16.**

Derive semantic binary list only from `runtimePrimaryLanguageIDs()` plus `multilsp.LanguageAdapterRegistry.AdapterForLanguage(...).ServerCommand(...).Executable`. The test must prove shell support appears through adapter data, not by appending a one-off hardcoded binary. Installer registry coverage is a guard, not the fact source.

- [ ] **Step 9: Verify lane.**

```bash
gofmt -w cmd/mcp-lsp/tools/factory.go cmd/mcp-lsp/search/fileutil.go cmd/mcp-lsp/tools/tool_file.go cmd/mcp-lsp/tools/tool_diagnostics.go cmd/mcp-lsp/multilsp/manager_lifecycle.go cmd/mcp-lsp/multilsp/manager_diagnostics.go cmd/mcp-lsp/multilsp/state.go cmd/mcp-lsp/fx.go cmd/mcp-lsp/runtime.go
gopls check cmd/mcp-lsp/tools/factory.go cmd/mcp-lsp/search/fileutil.go cmd/mcp-lsp/tools/tool_file.go cmd/mcp-lsp/tools/tool_diagnostics.go cmd/mcp-lsp/multilsp/manager_lifecycle.go cmd/mcp-lsp/multilsp/manager_diagnostics.go cmd/mcp-lsp/multilsp/state.go cmd/mcp-lsp/fx.go cmd/mcp-lsp/runtime.go
./scripts/test_with_guard.sh ./cmd/mcp-lsp ./cmd/mcp-lsp/tools ./cmd/mcp-lsp/multilsp -count=1
git diff --check
```

## Lane L02: mcp-orch Fork Authority and Approval Lifecycle

**Risks:** R04, R17.

**Files:**
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify: `cmd/mcp-orch/orchestration/service_launcher_bridge.go`
- Modify: `cmd/mcp-orch/tools/orchestration_tools.go`
- Modify: `cmd/mcp-orch/tools/orchestration_tool_definitions.go`
- Test: `cmd/mcp-orch/orchestration/service_launcher_bridge_test.go`
- Test: `cmd/mcp-orch/orchestration/turn_lifecycle_test.go`
- Test: `cmd/mcp-orch/tools/orchestration_tools_test.go`

- [ ] **Step 1: RED fork authority test.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run TestForkedLaunchRejectsUntrustedExternalParentThreadID -count=1
```

Expected: FAIL because arbitrary `parent_thread_id` for external parent is accepted.

- [ ] **Step 2: Implement R04 unique fix and defense.**

Remove public `ParentThreadID` from model-controlled launch input and schema. Resolve fork parent thread from trusted runtime map or binding/store by `parent_id`, caller identity, and workspace. If no trusted match exists, return `parent agent is required for forked launch`.

- [ ] **Step 3: RED approval shutdown fence test.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run TestApprovalLifecycleIgnoresQueuedEventsAfterStop -count=1
```

Expected: FAIL because queued approval events can still call handlers after OnStop begins.

- [ ] **Step 4: Implement R17 unique fix and defense.**

Mirror `RegisterTurnLifecycle`: create `lifecycleCtx` in OnStart, check `lifecycleCtx.Err()` at the top of requested/resolved callbacks, and cancel before unsubscribe in OnStop.

- [ ] **Step 5: Verify lane.**

```bash
gofmt -w cmd/mcp-orch/orchestration/service.go cmd/mcp-orch/orchestration/service_launcher_bridge.go cmd/mcp-orch/tools/orchestration_tools.go cmd/mcp-orch/tools/orchestration_tool_definitions.go
gopls check cmd/mcp-orch/orchestration/service.go cmd/mcp-orch/orchestration/service_launcher_bridge.go cmd/mcp-orch/tools/orchestration_tools.go cmd/mcp-orch/tools/orchestration_tool_definitions.go
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools -count=1
git diff --check
```

## Lane L03: Codex Provider Turn Start Fail-Fast and Recovery

**Risks:** R05, R06. R15 由 L08 单独串行处理，L03 不修改日志 call sites。

**Files:**
- Modify: `internal/provider/codexapp/session.go`
- Modify: `internal/provider/codexapp/recovery.go`
- Modify: `internal/provider/codexapp/support.go`
- Modify: `internal/provider/codexapp/supportutil/model_config.go`
- Test: `internal/provider/codexapp/session_turn_test.go`
- Test: `internal/provider/codexapp/driver_model_selection_test.go`
- Test: `internal/provider/codexapp/recovery_replay_policy_test.go`
- Test: `internal/provider/codexapp/transport_port_race_test.go`

- [ ] **Step 1: RED model/list fail-fast test.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestStartTurnFailsWhenRequiredModelListResolutionFails|TestThreadStartFailsWhenRequiredModelListResolutionFails' -count=1
```

Expected: FAIL because `StartTurn` and `thread/start` continue with empty/default model after warning.

- [ ] **Step 2: Implement R05 unique fix and defense.**

Move model/list default resolution into one typed helper in `supportutil/model_config.go`. Both `resolveTurnStartModel(ctx, requested)` and `startRemoteThreadWithParams` must call it; if `CodexModelNeedsListResolution(requested)` is true and `resolveSupportedCodexModel` errors or returns empty, return a typed error and do not call `turn/start` or `thread/start`.

- [ ] **Step 3: RED duplicate turn/start recovery test.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run TestTurnStartIsNotRetriedAfterWriteBeforeProviderID -count=1
```

Expected: FAIL because `callTransport` retries `turn/start` after reconnect.

- [ ] **Step 4: Implement R06 unique fix and defense.**

Introduce method retry policy:

```go
type transportRetryPolicy int

const (
    retryAfterReconnect transportRetryPolicy = iota
    noRetryAfterWrite
)
```

`callTransport` uses `noRetryAfterWrite` for `turn/start`; on reconnect-worthy error after write, return a recoverable error that tells caller no provider turn id is known.

- [ ] **Step 5: Verify lane.**

```bash
gofmt -w internal/provider/codexapp/session.go internal/provider/codexapp/recovery.go internal/provider/codexapp/support.go internal/provider/codexapp/supportutil/model_config.go
gopls check internal/provider/codexapp/session.go internal/provider/codexapp/recovery.go internal/provider/codexapp/support.go internal/provider/codexapp/supportutil/model_config.go
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
git diff --check
```

## Lane L04: Unified Session Resolver Identity and Auto-Resume

**Risks:** R07, R08, R09.

**Files:**
- Modify: `internal/provider/unified/session_resolver.go`
- Modify: `internal/provider/unified/session.go`
- Modify: `internal/contract/session.go`
- Modify: `internal/store/binding/session_adapter.go`
- Modify: `internal/module/turn/rpc_helpers.go`
- Modify: `internal/platform/rpc/handler.go`
- Test: `internal/provider/unified/session_resolver_test.go`
- Modify: `internal/provider/unified/session_resolver_auto_resume.go`
- Test: `internal/store/binding/store_test.go`
- Test: `internal/module/turn/rpc_helpers_test.go`

- [ ] **Step 1: RED public resolver agent-id bypass test.**

```bash
./scripts/test_with_guard.sh ./internal/provider/unified ./internal/module/turn -run 'TestResolveSessionRejectsAgentIDOnPublicThreadPath|TestTurnInterruptDoesNotResolveByAgentID' -count=1
```

Expected: FAIL because current resolver first calls `sessions.Get(threadID)`.

- [ ] **Step 2: Implement R07 unique fix and defense.**

Remove `tryExistingSession` from public `ResolveSession`. Add internal-only `ResolveSessionByAgentID` on the provider/unified narrow interface for trusted callers that already hold agent id; do not expose it through public `contract.SessionResolver`, and do not route external RPC/request parameters to it. Update turn/RPC code to pass only canonical public thread ids or provider-thread ids through public resolver.

- [ ] **Step 3: RED archived binding test.**

```bash
./scripts/test_with_guard.sh ./internal/provider/unified ./internal/store/binding -run TestAutoResumeRejectsArchivedBinding -count=1
```

Expected: FAIL because `Archived` is not present in `contract.SessionBinding`.

- [ ] **Step 4: Implement R08 unique fix and defense.**

Add `Archived bool` to `contract.SessionBinding`, map it in `internal/store/binding/session_adapter.go`, and reject archived bindings in `rejectBindingAutoResumeLifecycle`.

- [ ] **Step 5: RED concurrent auto-resume test.**

```bash
./scripts/test_with_guard.sh ./internal/provider/unified -run TestConcurrentColdAutoResumeInvokesProviderResumeOnce -count=1
```

Expected: FAIL because concurrent callers can both call provider `ResumeSession`.

- [ ] **Step 6: Implement R09 unique fix and defense.**

Add a resolver-local `singleflight.Group` keyed by canonical provider + agent id + provider/codex thread id. Concurrent callers wait on the owner result. Do not expose a session through `SessionManager.Get` until resume identity/backfill succeeds; failed sessions are closed/cleaned up exactly once by the owner.

- [ ] **Step 7: Verify lane.**

```bash
gofmt -w internal/provider/unified/session_resolver.go internal/provider/unified/session.go internal/contract/session.go internal/store/binding/session_adapter.go internal/module/turn/rpc_helpers.go internal/platform/rpc/handler.go
gopls check internal/provider/unified/session_resolver.go internal/provider/unified/session.go internal/contract/session.go internal/store/binding/session_adapter.go internal/module/turn/rpc_helpers.go internal/platform/rpc/handler.go
./scripts/test_with_guard.sh ./internal/provider/unified ./internal/store/binding ./internal/module/turn ./internal/platform/rpc -count=1
git diff --check
```

## Lane L05: Thread Metadata Persistence and Failed Start Cleanup

**Risks:** R11, R12, R13. R15 由 L08 单独串行处理，L05 不修改日志 call sites。

**Files:**
- Modify: `internal/module/thread/lifecycle.go`
- Modify: `internal/module/thread/lifecycle_helpers.go`
- Modify: `internal/module/thread/lifecycle_fork.go`
- Modify: `internal/module/thread/start_session.go`
- Test: `internal/module/thread/failfast_risk_test.go`
- Test: `internal/module/thread/fork_isolation_test.go`
- Test: `internal/store/thread/store_test.go`

- [ ] **Step 1: RED lookupThreadMeta fail-fast tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread -run 'TestForkFailsWhenThreadMetaLookupFails|TestRecoverFailsWhenThreadMetaLookupFails' -count=1
```

Expected: FAIL because `lookupThreadMeta` swallows store errors and returns empty metadata.

- [ ] **Step 2: Implement R11 unique fix and defense.**

Change `lookupThreadMeta` to `requireThreadMeta(ctx, threadID) (threadMeta, error)` and propagate errors through fork, recover, and snapshot rebuild call sites. Missing thread row must return a typed not-found error.

- [ ] **Step 3: RED subagent metadata persistence test.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/store/thread -run 'TestStartPersistsSubagentIdentityFields|TestRecoverUsesPersistedSubagentIdentity' -count=1
```

Expected: FAIL because public thread upsert builders omit identity fields.

- [ ] **Step 4: Implement R12 unique fix and defense.**

Add `ParentAgentID`, `AgentType`, and `AgentMemoryScope` to every public thread upsert builder, including `upsertPublicThread` and `upsertForkThreadStatus`. Add store roundtrip assertions for these fields.

- [ ] **Step 5: RED failed-start cleanup tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread -run 'TestStartFailureAfterProviderSessionRemovesLocalSession|TestStartSnapshotFailureRemovesLocalSessionBeforeStopAgent' -count=1
```

Expected: FAIL because current failure paths call `stopAgent` but do not prove local session removal.

- [ ] **Step 6: Implement R13 unique fix and defense.**

Create `cleanupFailedStartedSession(ctx, agentID)` and call it from every error path after provider session creation: bind generation failure, lookup session failure, provider UUID failure, identity failure, config encoding failure, persist thread state failure. Use generation-safe session remove/close before orchestration stop.

- [ ] **Step 7: Verify lane.**

```bash
gofmt -w internal/module/thread/lifecycle.go internal/module/thread/lifecycle_helpers.go internal/module/thread/lifecycle_fork.go internal/module/thread/start_session.go
gopls check internal/module/thread/lifecycle.go internal/module/thread/lifecycle_helpers.go internal/module/thread/lifecycle_fork.go internal/module/thread/start_session.go
./scripts/test_with_guard.sh ./internal/module/thread ./internal/store/thread -count=1
git diff --check
```

## Lane L06: RPC Control Peer Authentication and Approval Callback Boundary

**Risks:** R10.

**Files:**
- Modify: `internal/platform/rpc/server.go`
- Modify: `internal/platform/rpc/control_rpc_auth.go`
- Modify: `internal/platform/rpc/module.go`
- Modify: `internal/platform/rpc/approval.go`
- Test: `internal/platform/rpc/control_rpc_auth_test.go`
- Test: `internal/platform/rpc/approval_lifecycle_test.go`
- Test: `internal/platform/rpc/server_test.go`

- [ ] **Step 1: RED auth-before-active test.**

```bash
./scripts/test_with_guard.sh ./internal/platform/rpc -run TestControlRPCPeerIsNotActiveBeforeRegisterAuth -count=1
```

Expected: FAIL because `serveConn` adds PeerKindTool before `ctl/register`.

- [ ] **Step 2: RED approval callback UI-only test.**

```bash
./scripts/test_with_guard.sh ./internal/platform/rpc -run TestApprovalRequesterDoesNotFallbackToToolPeer -count=1
```

Expected: FAIL because `activeServer` returns any active peer when UI peer is absent.

- [ ] **Step 3: Implement R10 unique fix and defense.**

Delay `addActive` and `notifyConnected` until `ctl/register` succeeds. Store authenticated peer kind from registration. Change `approvalRequester.activeServer` to return only `PeerKindUI`; when no UI peer exists, return typed `ErrNoUIPeer` through the existing no-frontend decline path and fail closed. Do not leave approvals indefinitely pending for missing UI peers, and never fall back to tool peers. Pending replay is allowed only after a UI peer was selected and dispatch failed with a recoverable error.

- [ ] **Step 4: Verify lane.**

```bash
gofmt -w internal/platform/rpc/server.go internal/platform/rpc/control_rpc_auth.go internal/platform/rpc/module.go internal/platform/rpc/approval.go
gopls check internal/platform/rpc/server.go internal/platform/rpc/control_rpc_auth.go internal/platform/rpc/module.go internal/platform/rpc/approval.go
./scripts/test_with_guard.sh ./internal/platform/rpc -count=1
git diff --check
```

## Lane L07: Skill Mirror Drift and Scan Budget

**Risks:** R14, R19.

**Files:**
- Modify: `internal/module/skill/mirror_reconciler.go`
- Modify: `internal/module/skill/mirror_publisher.go`
- Modify: `internal/module/skill/rpc_skill_types.go`
- Modify: `internal/provider/shared/provider_home.go`
- Test: `internal/module/skill/mirror_reconciler_test.go`
- Test: `internal/module/skill/mirror_publisher_test.go`
- Test: `internal/module/skill/rpc_resolution_drift_test.go`
- Test: `internal/provider/shared/provider_home_test.go`

- [ ] **Step 1: RED Owned=false drift visibility test.**

```bash
./scripts/test_with_guard.sh ./internal/module/skill ./internal/provider/shared -run 'TestManagedMirrorOwnedFalseIsReportedAsDrift|TestProviderStartupBlocksOwnedFalseManagedMirrorDrift' -count=1
```

Expected: FAIL because matching hashes with `Owned=false` currently disappear from resolution inventory.

- [ ] **Step 2: Implement R14 unique fix and defense.**

Move `driftedManagedMirror` predicate into shared skill mirror code and use it from reconciler, publisher, and provider startup taxonomy. Managed mirror with `Owned=false` always reports `mirror_drift`.

- [ ] **Step 3: RED mirror scan budget tests.**

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run 'TestDetectSkillMirrorConflictsSkipsPersonalOrphanBeforeHash|TestDetectSkillMirrorConflictsReturnsTruncatedWhenRootEntryCapExceeded' -count=1
```

Expected: FAIL because detection hashes before orphan filtering and has no root-entry cap.

- [ ] **Step 4: Implement R19 unique fix and defense.**

Reorder detection: read manifest entry and canonical record before hashing. Skip personal orphan directories before recursive hash. Add default scan budget with root entry cap, per-directory byte cap, and typed `mirror_scan_truncated` result.

- [ ] **Step 5: Verify lane.**

```bash
gofmt -w internal/module/skill/mirror_reconciler.go internal/module/skill/mirror_publisher.go internal/module/skill/rpc_skill_types.go internal/provider/shared/provider_home.go
gopls check internal/module/skill/mirror_reconciler.go internal/module/skill/mirror_publisher.go internal/module/skill/rpc_skill_types.go internal/provider/shared/provider_home.go
./scripts/test_with_guard.sh ./internal/module/skill ./internal/provider/shared -count=1
git diff --check
```

## Lane L08: Cross-Cutting Log Guards

**Risks:** R15. This is the single serial owner lane for all R15 logging edits; other lanes must defer log call-site changes to L08.

**Files:**
- Create: `internal/platform/shared/safe_log_fields.go`
- Test: `internal/platform/shared/safe_log_fields_test.go`
- Modify: `cmd/mcp-lsp/fx.go`
- Modify: `cmd/mcp-lsp/tools/tool_file.go`
- Modify: `internal/provider/codexapp/session.go`
- Modify: `internal/provider/codexapp/support.go`
- Modify: `internal/module/thread/start_session.go`
- Modify: `internal/module/thread/events.go`
- Modify/Test: `internal/archtest/structured_log_guard_test.go`
- Test: focused tests in `cmd/mcp-lsp`, `internal/provider/codexapp`, and `internal/module/thread`

- [ ] **Step 1: RED global log guard.**

```bash
./scripts/test_with_guard.sh ./internal/platform/shared ./internal/archtest ./cmd/mcp-lsp ./internal/provider/codexapp ./internal/module/thread -run 'TestSafeRuntimeLogFieldsRedactsCWD|TestStructuredLogsRejectRawRuntimeFields|TestRuntimeLogFieldsDoNotExposeRawValues' -count=1
```

Expected: FAIL until all touched call sites use safe fields.

- [ ] **Step 2: Implement shared safe logging helper.**

Create `internal/platform/shared/safe_log_fields.go` with helpers:

```go
func SafePathLogFields(prefix, path string) []any
func SafePayloadLogFields(prefix string, payload []byte) []any
func SafeRuntimeLogFields(prefix string, values RuntimeLogFields) []any
```

Fields must include presence, byte length, stable hash, and display class where useful. Path/root/cwd-derived values must be represented only as summaries. Payload, prompt, config body, instructions, and sandbox policy values must be redacted or summarized; helpers must not emit raw absolute paths, raw payload bytes, raw prompt/config body, raw instructions, or raw sandbox policy.

- [ ] **Step 3: Add static log-field guard.**

Extend the existing structured log guard in `internal/archtest` so production Info/Warn/Error call sites reject banned raw runtime/log fields such as `cwd`, `root`, `path`, `payload`, `prompt`, `config`, `instructions`, or `sandbox_policy` unless the value comes from the safe log field helper. The guard must cover all current R15 call sites, including mcp-lsp config/scope logs, file-tool path logs, Codex provider model/session logs, and thread start/event logs.

- [ ] **Step 4: Verify lane.**

```bash
gofmt -w internal/platform/shared/safe_log_fields.go internal/archtest/structured_log_guard_test.go cmd/mcp-lsp/fx.go cmd/mcp-lsp/tools/tool_file.go internal/provider/codexapp/session.go internal/provider/codexapp/support.go internal/module/thread/start_session.go internal/module/thread/events.go
gopls check internal/platform/shared/safe_log_fields.go internal/archtest/structured_log_guard_test.go cmd/mcp-lsp/fx.go cmd/mcp-lsp/tools/tool_file.go internal/provider/codexapp/session.go internal/provider/codexapp/support.go internal/module/thread/start_session.go internal/module/thread/events.go
./scripts/test_with_guard.sh ./internal/platform/shared ./internal/archtest ./cmd/mcp-lsp ./internal/provider/codexapp ./internal/module/thread -count=1
git diff --check
```

## Final Integration Verification

- [ ] Run focused lane checks after each lane.
- [ ] After all lanes merge into integration worktree, run:

```bash
git status --short
make sqlc-verify
./scripts/test_with_guard.sh ./cmd/mcp-lsp ./cmd/mcp-lsp/tools ./cmd/mcp-lsp/multilsp ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools ./internal/provider/codexapp ./internal/provider/unified ./internal/module/thread ./internal/platform/rpc ./internal/module/skill ./internal/provider/shared ./internal/platform/shared ./internal/archtest -count=1
gopls check cmd/mcp-lsp/fx.go cmd/mcp-lsp/tools/factory.go cmd/mcp-lsp/tools/tool_file.go cmd/mcp-lsp/multilsp/manager_lifecycle.go cmd/mcp-orch/orchestration/service.go internal/provider/codexapp/session.go internal/provider/codexapp/support.go internal/provider/unified/session_resolver.go internal/module/thread/lifecycle.go internal/module/thread/start_session.go internal/module/thread/events.go internal/platform/rpc/server.go internal/module/skill/mirror_reconciler.go internal/platform/shared/safe_log_fields.go internal/archtest/structured_log_guard_test.go
git diff --check
```

Expected: all commands exit 0; `git status --short` contains only approved lane files.

## Self-Review Checklist

- [ ] Every production-reachable P1/P2 finding from the 2026-07-04 ring has exactly one local fix; R18 is explicitly OUT of the production repair queue and only optional hardening.
- [ ] Every risk row says whether upper defense is needed and names the optimal code landing point.
- [ ] No task asks worker to choose between alternatives.
- [ ] Every lane has RED command, implementation target, GREEN command, and exact write set.
- [ ] No production code, generated file, hook, or unrelated dirty file is modified by this plan itself.
