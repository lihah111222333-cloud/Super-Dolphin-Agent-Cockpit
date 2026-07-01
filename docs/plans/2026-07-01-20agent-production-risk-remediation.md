# 20-Agent Production Risk Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-07-01 20-agent 环形审查中已证实生产可达的 15 组风险，并为每项固定唯一最优修复和必要的上层防护。

**Architecture:** 采用控制器/worker 分离：控制器保留裁决权，worker 只在独立 worktree 内按 lane 写集执行。每项风险先修真实根因，再在入口、共享 helper、schema、preflight、UI 状态或 guard 层增加一层防护，防止同类缺陷从邻近路径绕过。所有修复以 fail-fast、最小写集、同提交回归测试为准，不用静默兜底替代错误暴露。

**Tech Stack:** Go 1.25.7、SQLite/sqlc、Fx lifecycle、Wails HTTP/RPC、MCP stdio/http、mcp-orch、mcp-lsp、Codex/Claude provider adapters、React/Vite/Vitest、shell release scripts、repo guard scripts。

**Verification Surface:** Go 变更使用 `./scripts/test_with_guard.sh <affected packages> -count=1`；SQL/store 变更额外运行 `make sqlc-verify`；frontend-app 变更使用 `cd frontend-app && npm run lint && npm test && npm run build`；release/script/guard 变更运行对应脚本测试、`git diff --check`、`make guard`；每个 lane 完成前运行 `git status --short` 并确认只包含本 lane 文件。

---

## Current Boundary

- [ ] 当前审查基线: `main @ d9ce73f0f25ee0f2887920cc70b151e24fc490c0`。
- [ ] 本计划是修复方案，不代表已批准执行源码修改。
- [ ] 当前主工作区已有 unrelated dirty 文件，执行时不得回退、格式化、stage 或混入本计划提交：

```text
.githooks/README.md
.githooks/pre-commit
docs/plans/2026-06-29-reasonix-design-absorption-plan.md
scripts/guard_fix_commits_have_tests.sh
scripts/guard_fix_commits_have_tests_guard_test.go
scripts/guard_fix_commits_have_tests_helpers_test.go
```

- [ ] 本计划文件当前是 untracked docs artifact：`docs/plans/2026-07-01-20agent-production-risk-remediation.md`。执行源码修复 lane 时不得把它混入生产修复提交；除非控制器明确批准，只能作为独立 docs-only plan 提交处理。
- [ ] A13 审查 agent 在时间盒内未形成可用 finding。本计划只包含主线程复核过入口、调用链和当前源码行号的风险。

## Global Execution Rules

- [ ] 控制器执行前运行：

```bash
git status --short
rg --files | rg '(^AGENTS.md$|README.md$|docs/doc/codemap/README.md$)'
```

Expected: 记录当前 dirty 边界；无关文件保持原样。

- [ ] 每个 lane 使用独立 worktree，分支命名 `codex/20260701-risk-lXX-<slug>`，worktree 命名 `.worktrees/20260701-risk-lXX-<slug>`。

```bash
base_branch=$(git branch --show-current)
git worktree add ".worktrees/20260701-risk-lXX-<slug>" -b "codex/20260701-risk-lXX-<slug>" "$base_branch"
cd ".worktrees/20260701-risk-lXX-<slug>"
git status --short
```

Expected: 新 worktree 干净；当前执行不安装 hooks。只有控制器明确批准 hook 链路验证时才运行 `make install-hooks`。如不干净，停止并向控制器报告真实路径。

- [ ] worker 只能改本计划 lane 写集列出的文件和同包测试。需要越界时返回 `NEEDS_APPROVAL`，写明路径、原因、缺少该越界后无法关闭的风险。
- [ ] lane 新建文件时，`git diff --check` 前必须先用 `git add -N <new-file>` 纳入 intent-to-add，或对仍需保持 untracked 的文件逐个运行 `git diff --no-index --check /dev/null <new-file>`。禁止把 intent-to-add 当作内容 staging；最终提交仍由控制器显式 stage。
- [ ] 每项风险先写 RED 测试并运行确认失败，再实现最小修复，最后运行 GREEN 验证。
- [ ] 每个 Go 源文件改完后运行单文件守卫，例如：

```bash
./scripts/test_with_guard.sh internal/module/thread/lifecycle_fork.go
```

Expected: exit 0。

- [ ] 集成顺序固定：L01 MCP tool 安全边界 -> L02 RPC 日志与 Wails HTTP -> L03 provider/skill -> L04 orchestration/thread 状态 -> L05 memory -> L06 release -> L07 mcp-lsp -> L08 store/tool semantics。

## Risk Matrix

| ID | Severity | Risk | 唯一最优修复 | 是否需要上层防护 | 上层防护怎么加 |
|---|---|---|---|---|---|
| F01 | P0 | `cmd/mcp-orch/tools/av_merge_tools.go:28` / `:67` 允许 `av_merge` 读取 absolute input 并用 `ffmpeg -y` 覆盖 arbitrary output；同一 registry 还暴露 `tts_generate`、`video_generate`、`video_with_audio` 等本地写文件/下载/合成工具。 | 将 mcp-orch 本地媒体输出统一改为 sharedfile artifact ref 或 workspace-relative media path；输出只能走受控 resolver，禁止 absolute output、`~/Movies`/home fallback 和 overwrite outside workspace。 | 需要 | 在 mcp-orch registry/handler 边界给本地文件写入、下载和媒体合成工具增加 `ToolPathPolicy`，schema 声明 `path_authority=sharedfile|workspace_relative`；registry 拒绝注册缺策略的本地写文件工具，handler 执行前校验策略。 |
| F02 | P1 | `internal/platform/rpc/server.go:79` / `:228` / `:134` 把 raw RPC params 压缩后写入失败日志。 | 删除 raw `params_preview`；改为 method-specific safe summary，只保留 request id、thread id、agent id、字段名、字节长度和 hash。 | 需要 | 在 RPC tracker 出口加 `SafeRPCLogSummary(method, params)`；新增 guard test 禁止日志字段名 `params_preview` 和直接调用 `req.ParamString()` 进入 logger。 |
| F03 | P1 | `internal/provider/shared/provider_home.go:66` / `:97` provider home chmod 失败只 warning 后继续启动。 | app-managed 和 explicit provider home 权限收紧失败直接返回错误并阻断 provider start/resume。默认 CLI home 若需兼容，必须单独走只读诊断分支。 | 需要 | Claude/Codex driver 的 provider home preflight 统一调用 fail-fast helper；UI/provider start 返回 `provider_home_permission_failed`，不进入 mirror reconcile。 |
| F04 | P1 | `internal/provider/shared/provider_home.go:439` 放过 project/app-managed skill same-name 冲突，provider 继续启动但冲突 skill 不进 mirror。 | `same_name` / `same_name_scope_conflict` 对 active project、`:project:`、`:app-managed:` target 阻断 provider 启动；personal report-only 冲突继续只报告。 | 需要 | provider start preflight 输出 conflict taxonomy；Skill resolution UI 和 provider error 共用同一 conflict kind，避免 provider 层把缺 skill 当正常启动。 |
| F05 | P1 | `cmd/mcp-orch/orchestration/dag_dispatch.go:53` assign node 后 `:57` 才 enqueue wakeup，失败留下半写状态。 | 新增 store 原子方法 `AssignNodeAndEnqueueWakeup`，在同一 SQLite immediate transaction 内完成状态 fence、assigned_to 更新和 wakeup 入队。 | 需要 | 增加 startup/recovery scan：发现 `assigned_to != ''` 且无 active wakeup 的 pending/ready node，标记 `dispatch_incomplete` 并阻断重复调度。 |
| F06 | P1 | `internal/module/thread/lifecycle_fork.go:43` fork kickoff 前持久化 thread/snapshot，后续失败无回滚。 | fork 使用可补偿状态机：预写入必须标记 `creating`，kickoff 成功后转 `created_only`；任一 kickoff 失败删除新 thread/binding/snapshot 或转 `failed` 且不可 resume。 | 需要 | thread list/resume 层拒绝 `creating`/`failed` fork 进入普通 resume；UI 显示 fork kickoff failure 和清理入口。 |
| F07 | P1 | `internal/module/memory/extract_runtime.go:55` 显式 remember/forget 写失败只 warning，用户 turn 仍看似成功。 | 显式记忆指令失败必须产生结构化 `memory.intent_failed` event，并写入 thread/user-visible diagnostic；普通非记忆文本不产生诊断。 | 需要 | UI state projector 订阅 memory diagnostic，thread card 展示 warning；turn completion payload 附带 memory side-effect warning，不把记忆失败吞到后台日志。 |
| F08 | P1 | `scripts/package_linux.sh:782` Linux launcher 未声明 packaged runtime，`:803` 未运行 verifier 即 ready。 | Linux `run.sh` 设置 packaged runtime env，并在打包 ready 前强制运行 `scripts/verify_packaged_app_linux.sh "$stage"`。 | 需要 | package guard 和 Linux verifier 锁定 `run.sh` 必须声明 `SUPER_DOLPHIN_PACKAGE_ROOT`、`SUPER_DOLPHIN_RUNTIME_MODE=packaged`、`SUPER_DOLPHIN_PACKAGED_LAUNCHER=1`，且 verifier 必须在 tar 前执行；保留 macOS/Windows packaged 自动识别契约，不做全局“包形态但无 env 即失败”改动。 |
| F09 | P1 | `cmd/mcp-lsp/tools/tool_grep.go:240` runtime fallback 可搜索未配置兄弟 worktree。 | 移除自动 sibling fallback；runtime root 不匹配时返回 stale-root error，要求调用方传显式 `work_dir` / trusted `_workspaceRoots`。 | 需要 | mcp-lsp tool entrypoint 要求 workspace roots authority；missing roots 不进入 grep/search，并返回可行动诊断。 |
| F10 | P1 | `internal/provider/codexapp/session_dispatch.go:130` synthetic turn completion 无条件 `success=true/status=completed`。 | active turn 记录 tool/item failure state；存在未处理失败时 synthetic completion 标记 `success=false` 或 `status=completed_with_errors`。 | 需要 | event DTO 增加 failure correlation 字段；UI projector 将 completed-with-errors 显示为 warning/error，而不是 idle clean success。 |
| F11 | P2 | `internal/module/memory/ui_rpc.go:363` UI memory snapshot 同步 walk/read 整个 memory root，similarity health 后续还会二次读取 entry body。 | `ui/memory/get` 改为有界 metadata scan：传递 ctx、限制 entry 数、单文件字节和总字节；详情内容走单条 entry read，similarity/health 只能使用同一 scan budget 内的数据。 | 需要 | 前端 AbortSignal/timeout 只作为用户体验防护；后端 resource cap 才是事实防线，超限返回 `memory_scan_truncated`，并停止 similarity 二次全文读取。 |
| F12 | P2 | `internal/module/memory/domain_bridges.go:180` `memory_write` overflow merge 不返回 error，`:534` 删除旧项忽略错误。 | overflow merge/delete/index 更新返回 typed error；memory_write 返回 `success=false` 或 `partial=true` structured result，不能纯成功。 | 需要 | toolbridge memory_write adapter 识别 partial/degraded result 并发 event/metric；prompt memory invalidation 只在真实成功或明确 partial 后执行。 |
| F13 | P2 | `cmd/mcp-orch/tools/registry_tools.go:157` `shared_file_list` 缺省 limit 直传 0；`cmd/mcp-orch/sql/queries/shared_file.sql:19` prefix 实际是 contains。 | `limit <= 0` 使用明确默认 `50`，最大 `200`；prefix 查询改为 prefix-only match 并 escape `%` / `_`。 | 需要 | sharedfile store test 和 SQL guard 锁定 default limit、max cap、prefix negative case；root SQL 与 cmd/mcp-orch SQL 同步。 |
| F14 | P2 | `cmd/mcp-lsp/tools/factory.go:447` 位置型工具把用户列号直接 `column-1` 当 LSP UTF-16 character，completion retry helper 还会把 LSP character 当 rune index 继续计算。 | 解析 `pos` 后读取目标行，将用户 1-based rune column 转成 LSP UTF-16 code unit character；completion retry 使用同一 rune/UTF-16 映射，计算边界用 rune index，发给 LSP 前转换回 UTF-16。 | 需要 | 所有 inspect/xref/completion/rename/code_action/edit 入口只允许使用共享 `ResolveLSPPosition`/position mapping helper，archtest 禁止新增直接 `Character: column - 1` 或绕过共享转换的 retry position 构造。 |
| F15 | P2 | `internal/ui/wails/http_server.go:46` `/metrics` 直接注册，未复用 Wails Host/Origin/token guard。 | `/metrics` 默认仅在显式 metrics env 开关启用；启用后包同一 Host guard，必要时要求 Wails token。 | 需要 | Wails HTTP route registration helper 强制所有非静态 route 声明 auth policy；无 policy 的 route 测试失败。 |

## Lane L01: MCP Tool File and Dispatch Safety

**Risks:** F01, F05, F13

**Files:**
- Modify: `cmd/mcp-orch/tools/types.go`
- Modify: `cmd/mcp-orch/tools/factory.go`
- Modify: `cmd/mcp-orch/tools/av_merge_tools.go`
- Modify: `cmd/mcp-orch/tools/tts_tools.go`
- Modify: `cmd/mcp-orch/tools/video_tools.go`
- Modify: `cmd/mcp-orch/tools/video_with_audio_tools.go`
- Modify: `cmd/mcp-orch/tools/shared_file_tools.go`
- Modify: `cmd/mcp-orch/tools/registry.go`
- Modify: `cmd/mcp-orch/tools/registry_tools.go`
- Modify: `cmd/mcp-orch/tools/task_tools.go`
- Modify: `cmd/mcp-orch/orchestration/dag_dispatch.go`
- Modify: `cmd/mcp-orch/store/taskdag/contract.go`
- Modify: `cmd/mcp-orch/store/taskdag/store.go`
- Modify: `cmd/mcp-orch/sql/queries/shared_file.sql`
- Modify: `sql/queries/shared_file.sql`
- Modify generated: `cmd/mcp-orch/store/sqlc/shared_file.sql.go`
- Modify generated: `internal/store/sqlc/shared_file.sql.go`
- Test: `cmd/mcp-orch/tools/av_merge_tools_test.go`
- Test: `cmd/mcp-orch/tools/tool_metadata_test.go`
- Test: `cmd/mcp-orch/tools/video_tools_test.go`
- Test/Create: `cmd/mcp-orch/tools/tts_tools_test.go`
- Test/Create: `cmd/mcp-orch/tools/video_with_audio_tools_test.go`
- Test: `cmd/mcp-orch/tools/registry_tools_test.go`
- Test: `cmd/mcp-orch/orchestration/dag_dispatch_test.go`
- Test: `cmd/mcp-orch/store/taskdag/*_test.go`
- Test: `cmd/mcp-orch/store/sharedfile/*_test.go`

- [ ] **Step 1: RED test for arbitrary `av_merge` output.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run 'TestAVMergeRejectsAbsoluteOutputOutsideWorkspace|TestMediaFileWritingToolsRequirePathPolicy|TestVideoGenerateRejectsHomeFallbackOutput|TestVideoWithAudioUsesControlledOutputPath' -count=1
```

Expected: FAIL because current media handlers accept absolute/home output paths or have no `ToolPathPolicy`.

- [ ] **Step 2: RED test for unregistered media input.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools -run TestAVMergeRequiresSharedFileOrWorkspaceRelativeInputs -count=1
```

Expected: FAIL because current schema accepts arbitrary absolute `video_path` and `audio_path`.

- [ ] **Step 3: RED test for dispatch half-write.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run TestDispatchNodeDoesNotPersistAssignmentWhenWakeupEnqueueFails -count=1
```

Expected: FAIL because `AssignNode` succeeds before `EnqueueWakeup` returns error.

- [ ] **Step 4: RED tests for sharedfile list semantics.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./cmd/mcp-orch/store/sharedfile -run 'TestSharedFileList(DefaultLimit|PrefixIsPrefixOnly|LimitMaxCap)' -count=1
```

Expected: FAIL for missing default limit and contains-style prefix matching.

- [ ] **Step 5: Implement F01 unique fix.**

Replace `avMergeInput` absolute paths with `video_ref`, `audio_ref`, and optional `output_path` constrained to workspace-relative or sharedfile artifact output. Apply the same controlled output resolver to `tts_generate`, `video_generate`, and `video_with_audio`; remove default writes to `~/Movies` and any fallback to home. Resolve refs through existing sharedfile/workspace path validators before invoking ffmpeg or downloading generated media.

- [ ] **Step 6: Add F01 upper defense.**

Add `ToolPathPolicy` to mcp-orch tool definition metadata. The registry refuses to register local filesystem write/download/media tools without a policy, and the registry/handler call path validates the policy before execution. `shared_file_write` must declare its existing `sharedfilepath.ValidateAgentWritePath` authority as policy metadata. Do not move this defense into `internal/platform/toolbridge` unless implementation proves mcp-orch execution actually crosses that layer; that would require `NEEDS_APPROVAL`.

- [ ] **Step 7: Implement F05 unique fix.**

Add `AssignNodeAndEnqueueWakeup(ctx, input)` to the taskdag store and use one transaction for assignment and wakeup enqueue. `DispatchNodeStore` should depend on that atomic method instead of separate `AssignNode` + `EnqueueWakeup`.

- [ ] **Step 8: Add F05 upper defense.**

Add a recovery/preflight helper that flags assigned pending/ready nodes without active wakeup as `dispatch_incomplete`; expose the state in dispatch errors so retry does not silently proceed.

- [ ] **Step 9: Implement F13 unique fix.**

Set default limit `50`, max `200`, reject negative values, and change SQL prefix predicate to prefix-only match with escaping for `%` and `_`.

- [ ] **Step 10: Verify lane.**

```bash
make sqlc-verify
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/store/sharedfile -count=1
git diff --check
```

## Lane L02: RPC Logs, Wails Metrics, and Route Policy

**Risks:** F02, F15

**Files:**
- Modify: `internal/platform/rpc/server.go`
- Create: `internal/platform/rpc/safe_log_summary.go`
- Modify: `internal/platform/rpc/server_test.go`
- Modify: `internal/ui/wails/http_server.go`
- Create: `internal/ui/wails/http_route_policy.go`
- Modify: `internal/ui/wails/http_server_test.go`
- Modify: `internal/platform/metrics/http.go`
- Test: `internal/platform/rpc/*_test.go`
- Test: `internal/ui/wails/*_test.go`
- Test: `internal/platform/metrics/*_test.go`

- [ ] **Step 1: RED test for prompt leakage in RPC failure log.**

```bash
./scripts/test_with_guard.sh ./internal/platform/rpc -run TestRPCFailureLogDoesNotIncludeRawParamsPreview -count=1
```

Expected: FAIL because raw prompt text appears in `params_preview`.

- [ ] **Step 2: RED test for method-specific safe summary.**

```bash
./scripts/test_with_guard.sh ./internal/platform/rpc -run TestRPCSafeLogSummaryKeepsIDsAndLengthsOnly -count=1
```

Expected: FAIL because current implementation has no safe summary.

- [ ] **Step 3: RED test for metrics route without policy.**

```bash
./scripts/test_with_guard.sh ./internal/ui/wails ./internal/platform/metrics -run 'TestMetricsRouteRequiresExplicitPolicy|TestMetricsRouteDisabledByDefault' -count=1
```

Expected: FAIL because `/metrics` is directly registered.

- [ ] **Step 4: Implement F02 unique fix.**

Replace `rpcParamPreview(req.ParamString())` with `SafeRPCLogSummary(method, params)`. The summary must remove content fields including `prompt`, `baseInstructions`, `developerInstructions`, `input`, `content`, `attachments`, `config`, `headers`, `token`, `cookie`, `authorization`, and `apiKey`; keep only ids, field names, byte counts, and stable hash.

- [ ] **Step 5: Add F02 upper defense.**

Add an archtest or package test that scans `internal/platform/rpc` for logger arguments named `params_preview` and direct `ParamString()` logging. The only allowed direct `ParamString()` use is inside the safe summary test fixture.

- [ ] **Step 6: Implement F15 unique fix.**

Register `/metrics` only when an explicit env such as `SUPER_DOLPHIN_ENABLE_METRICS=1` is set. When enabled, wrap it in the same loopback Host/Origin guard used for Wails RPC; if a Wails token is configured, require it.

- [ ] **Step 7: Add F15 upper defense.**

Introduce `RoutePolicy` for Wails HTTP routes. Non-static routes must declare `public_static`, `wails_rpc`, `metrics_guarded`, or `local_asset_token`; tests fail if a route is registered without a policy.

- [ ] **Step 8: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/platform/rpc ./internal/ui/wails ./internal/platform/metrics -count=1
git diff --check
```

## Lane L03: Provider Home and Skill Mirror Startup Gates

**Risks:** F03, F04

**Files:**
- Modify: `internal/provider/shared/provider_home.go`
- Modify: `internal/provider/shared/provider_home_test.go`
- Modify: `internal/provider/claudecli/driver.go`
- Modify: `internal/provider/claudecli/driver_mirror_test.go`
- Modify: `internal/provider/codexapp/driver_pool_routing.go`
- Modify: `internal/provider/codexapp/driver_pool_routing_test.go`
- Test: `internal/module/skill/mirror_publisher_reconcile_test.go`

- [ ] **Step 1: RED test for chmod failure.**

```bash
./scripts/test_with_guard.sh ./internal/provider/shared -run TestEnsureProviderHomeFailsWhenChmodFails -count=1
```

Expected: FAIL because chmod errors are currently warning-only.

- [ ] **Step 2: RED tests for Claude/Codex start surfacing chmod error.**

```bash
./scripts/test_with_guard.sh ./internal/provider/claudecli ./internal/provider/codexapp -run 'Test.*ProviderHomeChmodFailureBlocksStart' -count=1
```

Expected: FAIL because provider start does not receive a fail-fast error.

- [ ] **Step 3: RED test for active same-name conflict.**

```bash
./scripts/test_with_guard.sh ./internal/provider/shared -run TestEnsureNoSkillMirrorConflictsBlocksActiveProjectSameName -count=1
```

Expected: FAIL because `same_name` is currently report-only.

- [ ] **Step 4: Implement F03 unique fix.**

Return errors from `EnsureAppManagedProviderHome` and explicit-home `EnsureProviderHome` when chmod fails. Preserve path and provider in the error message. If default CLI home needs permissive compatibility, split that branch explicitly and return a warning diagnostic object instead of silently continuing.

- [ ] **Step 5: Implement F04 unique fix.**

Update `isBlockingSkillMirrorConflict` so `same_name` and `same_name_scope_conflict` block only active project/app-managed mirror targets. Personal UI-only conflicts remain report-only and must be visible to the resolution UI.

- [ ] **Step 6: Add upper defense.**

Claude and Codex driver preflight must call the same provider home and mirror conflict helpers before acquiring or resuming a provider session. On failure, return typed errors `provider_home_permission_failed` or `skill_mirror_conflict`.

- [ ] **Step 7: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/provider/shared ./internal/provider/claudecli ./internal/provider/codexapp ./internal/module/skill -count=1
git diff --check
```

## Lane L04: Thread Fork State and Codex Turn Outcome

**Risks:** F06, F10

**Files:**
- Modify: `internal/module/thread/lifecycle_fork.go`
- Modify: `internal/module/thread/rpc.go`
- Modify: `internal/module/thread/types.go`
- Modify: `internal/module/thread/*fork*_test.go`
- Modify: `internal/provider/codexapp/session_dispatch.go`
- Modify: `internal/provider/codexapp/session_approval.go`
- Modify: `internal/provider/codexapp/event_map.go`
- Modify: `internal/provider/codexapp/*test*.go`
- Modify: `internal/module/uistate/projector_handlers.go`
- Modify: `internal/module/uistate/*test*.go`
- Modify: `internal/dto/shared/event.go`

- [ ] **Step 1: RED tests for fork kickoff failures.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread -run 'TestFork(KickoffFailureLeavesNoRecoverableThread|BindGenerationFailureMarksForkFailed|PersistStartedFailureCleansSnapshot)' -count=1
```

Expected: FAIL because failure paths leave persisted state without final success.

- [ ] **Step 2: RED test for Codex synthetic success after tool failure.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/module/uistate -run 'TestSyntheticAssistantCompletionPreservesToolFailure|TestUIStateCompletedWithErrorsDoesNotBecomeCleanIdle' -count=1
```

Expected: FAIL because synthetic completion currently emits `success=true/status=completed`.

- [ ] **Step 3: Implement F06 unique fix.**

Persist fork state as `creating` before kickoff. On successful kickoff, transition to `created_only` or final started state. On failure, either delete the new thread row, binding, and prompt snapshot in one cleanup path, or transition to `failed` with no resume eligibility.

- [ ] **Step 4: Add F06 upper defense.**

Thread list/resume rejects `creating` and `failed` fork states. RPC returns a structured fork kickoff failure response that UI can render; no ordinary resume path may consume the half-created thread.

- [ ] **Step 5: Implement F10 unique fix.**

Track per active turn tool failures in Codex session state. `completeSyntheticTurn` must inspect that state and emit `success=false/status=completed_with_errors` when failures exist.

- [ ] **Step 6: Add F10 upper defense.**

Add failure correlation fields to event DTO or turn completion metadata. UI projector maps `completed_with_errors` to warning/error visible state, not clean idle.

- [ ] **Step 7: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp ./internal/module/uistate -count=1
git diff --check
```

## Lane L05: Memory Runtime, Tool Results, and UI Scan Bounds

**Risks:** F07, F11, F12

**Files:**
- Modify: `internal/module/memory/extract_runtime.go`
- Modify: `internal/module/memory/service.go`
- Modify: `internal/module/memory/domain_bridges.go`
- Modify: `internal/module/memory/ui_rpc.go`
- Modify: `internal/module/memory/index.go`
- Modify: `internal/module/memory/extract.go`
- Modify: `internal/platform/toolbridge/memory_write_tool.go`
- Modify: `internal/platform/toolbridge/*memory*_test.go`
- Modify: `frontend-app/src/services/modules/memoryService.js`
- Modify: `frontend-app/src/pages/memory/MemoryPage.jsx`
- Build-only ignored output: `frontend-app/dist/**`, `cmd/agent-terminal/web-dist/**` must be verified or cleaned after `npm run build`; do not commit embed output unless the controller explicitly approves an embed refresh.
- Test: `internal/module/memory/*_test.go`
- Test: `frontend-app/src/services/modules/memoryService.test.js`

- [ ] **Step 1: RED test for explicit memory failure visibility.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory -run TestExplicitMemoryIntentFailurePublishesDiagnostic -count=1
```

Expected: FAIL because failure is warning-only.

- [ ] **Step 2: RED test for memory_write overflow failure.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory ./internal/platform/toolbridge -run 'TestMemoryWriteReportsOverflowMergeFailure|TestMemoryWriteReportsDeleteFailureAsPartial' -count=1
```

Expected: FAIL because merge/delete failure is swallowed.

- [ ] **Step 3: RED test for UI memory scan bounds.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory -run 'TestUIMemoryGetRespectsEntryLimit|TestUIMemoryGetRejectsOversizedEntry|TestUIMemoryGetStopsOnContextCancel|TestUIMemorySimilarityHealthRespectsScanBudget' -count=1
```

Expected: FAIL because scan walks/reads all files and similarity health can read entry bodies again after the initial scan.

- [ ] **Step 4: Implement F07 unique fix.**

When explicit remember/forget handling returns an error, publish `memory.intent_failed` with thread id, turn id, action, and redacted error. Add a thread diagnostic that UI can show. Do not emit this for ordinary text that is not a memory intent.

- [ ] **Step 5: Implement F12 unique fix.**

Make overflow merge/delete/index update return typed errors. `WriteAgentMemory` returns a structured `partial` result when the primary write succeeded but maintenance failed; it returns `success=false` when primary write failed.

- [ ] **Step 6: Add F12 upper defense.**

Toolbridge memory_write converts partial/degraded result into visible tool content and event metadata. Memory invalidation only runs after success or explicit partial result.

- [ ] **Step 7: Implement F11 unique fix.**

Thread `context.Context` through UI memory scan. Add caps for entry count, single file bytes, and total scan bytes. `ui/memory/get` returns metadata and preview only; detailed content remains in a single-entry read handler. `populateUIMemoryHealthSimilarGroups` and any health snapshot loading must share the same scan budget and must not loop back through every entry body after the cap trips.

- [ ] **Step 8: Add F11 upper defense.**

Frontend passes AbortSignal and handles `memory_scan_truncated` / `memory_scan_canceled` as visible degraded state. Backend resource caps remain authoritative even if frontend timeout is absent. When scan data is truncated, similarity groups are omitted or marked degraded instead of forcing a second full read.

- [ ] **Step 9: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory ./internal/platform/toolbridge -count=1
(
  cd frontend-app
  npm run lint
  npm test
  npm run build
)
git status --ignored --short frontend-app/dist cmd/agent-terminal/web-dist
git diff --check
```

## Lane L06: Linux Release Runtime and Packaged Verifier

**Risks:** F08

**Files:**
- Modify: `scripts/package_linux.sh`
- Modify: `scripts/package_linux_guard_test.go`
- Modify: `scripts/sqlite_release_gate_package_smoke_runtime_test.go`
- Modify: `scripts/verify_packaged_app_linux.sh`
- Verify only: `internal/platform/runtimeenv/runtime_resolution_test.go` existing macOS/Windows auto-detect and explicit Linux package-root contract tests.

- [ ] **Step 1: RED test for Linux launcher packaged env.**

```bash
go test ./scripts -run TestPackageLinuxRunScriptDeclaresPackagedRuntime -count=1
```

Expected: FAIL because generated `run.sh` lacks `SUPER_DOLPHIN_RUNTIME_MODE=packaged`, `SUPER_DOLPHIN_PACKAGE_ROOT`, and `SUPER_DOLPHIN_PACKAGED_LAUNCHER`.

- [ ] **Step 2: RED test for verifier order.**

```bash
go test ./scripts -run TestPackageLinuxRunsVerifierBeforeTarReady -count=1
```

Expected: FAIL because package script tars and prints ready without invoking Linux verifier.

- [ ] **Step 3: RED verifier test for launcher env completeness.**

```bash
go test ./scripts -run TestVerifyPackagedAppLinuxRejectsLauncherMissingRuntimeEnv -count=1
```

Expected: FAIL because the Linux verifier does not currently reject `run.sh` missing one of `SUPER_DOLPHIN_PACKAGE_ROOT`, `SUPER_DOLPHIN_RUNTIME_MODE=packaged`, or `SUPER_DOLPHIN_PACKAGED_LAUNCHER=1`.

- [ ] **Step 4: Implement F08 unique fix.**

Update generated `run.sh` to export:

```bash
export SUPER_DOLPHIN_PACKAGE_ROOT="$here"
export SUPER_DOLPHIN_RUNTIME_MODE=packaged
export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1
```

Call `"$root/scripts/verify_packaged_app_linux.sh" "$stage"` after manifest/script generation and before `tar`.

- [ ] **Step 5: Add F08 upper defense.**

Linux package guard and verifier reject a launcher that is missing any packaged runtime env declaration, and the verifier checks SQLite home resolution does not land under package root. Do not change `ResolveRuntime` into a global "package shape without env fails" rule: existing macOS manifest auto-detection and Windows executable-in-package-bin auto-detection must remain green.

- [ ] **Step 6: Verify lane.**

```bash
go test ./scripts -run 'TestPackageLinux|TestSQLiteReleaseGate' -count=1
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -run 'TestResolveRuntimeValidMacOSManifestResolvesPackaged|TestResolveRuntimeWindowsExecutableInPackageBinAutoDetectsPackagedRoot|TestResolveRuntimeExplicitLinuxPackageRootValidManifestResolvesPackaged' -count=1
git diff --check
```

## Lane L07: mcp-lsp Scope and Position Semantics

**Risks:** F09, F14

**Files:**
- Modify: `cmd/mcp-lsp/tools/tool_grep.go`
- Modify: `cmd/mcp-lsp/tools/tool_grep_log.go`
- Modify: `cmd/mcp-lsp/tools/factory.go`
- Modify: `cmd/mcp-lsp/tools/tool_inspect.go`
- Modify: `cmd/mcp-lsp/tools/tool_xref.go`
- Modify: `cmd/mcp-lsp/tools/tool_completion.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_rename.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go`
- Test: `cmd/mcp-lsp/tools/tool_grep_test.go`
- Test: `cmd/mcp-lsp/tools/tool_position_test.go`
- Test: `cmd/mcp-lsp/tools/tool_edit_rename_test.go`
- Test: `internal/archtest/lsp_tool_guard_test.go`

- [ ] **Step 1: RED test for sibling fallback.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run TestGrepRuntimeFallbackDoesNotSearchSiblingWorktree -count=1
```

Expected: FAIL because sibling worktree match is returned.

- [ ] **Step 2: RED test for missing workspace roots.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run TestGrepRequiresTrustedWorkspaceRootsWhenRuntimeFallbackWouldApply -count=1
```

Expected: FAIL because fallback proceeds instead of stale-root error.

- [ ] **Step 3: RED tests for UTF-16 position conversion.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run 'TestResolveLSPPositionConvertsEmojiColumn|TestRenameUsesUTF16Position|TestCompletionRetryUsesUTF16PositionAfterEmoji' -count=1
```

Expected: FAIL because `column-1` is used directly and completion retry currently treats LSP UTF-16 character offsets as rune indexes.

- [ ] **Step 4: Implement F09 unique fix.**

Delete automatic sibling search. When runtime fallback would search outside configured roots, return `mcp-lsp: stale workspace root; pass work_dir or _workspaceRoots` with no file reads.

- [ ] **Step 5: Add F09 upper defense.**

Tool entrypoint requires trusted workspace roots for grep/search. Missing roots fail before invoking search backend.

- [ ] **Step 6: Implement F14 unique fix.**

Add `ResolveLSPPosition(ctx, filePath, line, column)` plus a shared line-position mapping helper that reads the target line, converts 1-based rune columns to UTF-16 code unit `protocol.Position`, and can convert retry rune indexes back to UTF-16. Replace all direct `requirePosition` call sites for LSP protocol calls; completion retry may calculate identifier boundaries in rune indexes but must convert each retry position through the shared helper before calling LSP.

- [ ] **Step 7: Add F14 upper defense.**

Archtest scans mcp-lsp tool files and rejects new `Character: column - 1` direct conversions or retry-position constructors that bypass `ResolveLSPPosition` / the shared mapping helper.

- [ ] **Step 8: Verify lane.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools ./internal/archtest -count=1
git diff --check
```

## Lane L08: Store/Tool Semantics Integration Check

**Risks:** F13 post-L01 verification ownership after SQL changes

**Files:**
- Controller verification only: L01 owns `sql/queries/shared_file.sql`, `cmd/mcp-orch/sql/queries/shared_file.sql`, `internal/store/sqlc/shared_file.sql.go`, and `cmd/mcp-orch/store/sqlc/shared_file.sql.go`.
- Do not modify production SQL or generated files in L08 unless L01 returns `NEEDS_APPROVAL`; otherwise L08 is a read-only integration check.

- [ ] **Step 1: Confirm generated SQL ownership after L01.**

```bash
git diff --name-only | rg '(shared_file|sqlc|queries)'
```

Expected: only L01-owned sharedfile query and generated sqlc files changed; if L08 sees missing generated output, it reports back to L01 instead of silently taking ownership.

- [ ] **Step 2: Run SQL verification.**

```bash
make sqlc-verify
```

Expected: generated files are up to date and no query drift remains.

- [ ] **Step 3: Run sharedfile focused tests.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/sharedfile ./cmd/mcp-orch/tools -run 'SharedFile' -count=1
```

Expected: default limit and prefix-only tests pass.

## Final Integration Gates

- [ ] Run focused lane checks for all merged commits:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/store/sharedfile -count=1
./scripts/test_with_guard.sh ./internal/platform/rpc ./internal/ui/wails ./internal/platform/metrics -count=1
./scripts/test_with_guard.sh ./internal/provider/shared ./internal/provider/claudecli ./internal/provider/codexapp ./internal/module/skill -count=1
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp ./internal/module/uistate -count=1
./scripts/test_with_guard.sh ./internal/module/memory ./internal/platform/toolbridge -count=1
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools ./internal/archtest -count=1
```

- [ ] Run frontend verification if any frontend files changed:

```bash
cd frontend-app
npm run lint
npm test
npm run build
cd ..
git status --ignored --short frontend-app/dist cmd/agent-terminal/web-dist
```

- [ ] Run release/script verification if Linux package files changed:

```bash
go test ./scripts -run 'TestPackageLinux|TestSQLiteReleaseGate' -count=1
./scripts/test_with_guard.sh ./internal/platform/runtimeenv -count=1
```

- [ ] Run broad guard after all lanes merge:

```bash
make guard
git ls-files --others --exclude-standard
git diff --check
git diff --cached --check
git diff --check d9ce73f0f25ee0f2887920cc70b151e24fc490c0...HEAD
git diff --name-status d9ce73f0f25ee0f2887920cc70b151e24fc490c0...HEAD
git status --short
```

Expected: guard passes; unstaged, staged, and merged base-range diffs have no whitespace errors; base-range changed paths match L01-L08 owned-file allowlists or explicit `NEEDS_APPROVAL`; untracked owned files have been checked with `git add -N` or `git diff --no-index --check`; ignored build outputs are either cleaned or explicitly recorded as build-only artifacts; only intended files are dirty or staged by explicit controller action.
