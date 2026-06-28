# 20-Agent Production Risk Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes only when persistent orchestration records are required. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 2026-06-28 20-agent 全域生产风险审查中裁决出的生产可达风险，并为每个风险同时落地源头修复和上层防御。

**Architecture:** 采用控制器/worker 分离。主控制器只负责创建隔离 worktree、派发 worker、审批写集扩展、审查 diff、复跑验证和最终集成；worker 只在本计划 lane 写集内按 TDD 修复。所有风险先修源头，再在调用入口、provider/toolbridge、UI 或 guard 层补一层防御，避免同类缺陷绕过单点修复。

**Tech Stack:** Go 1.25.7、SQLite/sqlc、Fx lifecycle、Wails、MCP stdio/http、Codex/Claude provider adapters、React/Vite/Vitest、repo guard scripts。

**Verification Surface:** Go 变更使用 `./scripts/test_with_guard.sh <affected packages> -count=1`；SQL/store 变更额外运行 `make sqlc-verify`；frontend-app 变更使用 `cd frontend-app && npm run lint && npm test && npm run build`；guard/script 变更运行对应脚本测试、`make guard`、`./scripts/test_with_guard.sh ./internal/archtest -count=1` 和 `git diff --check`。

---

## Plan State

- [ ] `NEEDS_APPROVAL`: 本文件是修复方案，不代表已经批准执行源码修改。
- [ ] 审查覆盖说明: 20 个 agent 已调度；18 个返回结果；`L04 mcp-orch lifecycle/DAG/Cron/Wakeup` 与 `L16 release/install/package/embed` 多次等待仍超时。本计划包含主控和已返回 agent 已复核的风险；执行前控制器必须重新用 LSP 复核每个 lane 的当前源码行号。
- [ ] 生产风险判断标准: 只修生产可达、可复现、可加回归测试的问题；纯风格、注释、历史迁移或没有生产入口证据的问题不进入 worker 写集。
- [ ] 5-agent 交叉复核裁决: 5/5 reviewer 均返回；R01-R29 均保留为生产可达或生产门禁可达风险，无项进入不可达注释队列；R09/R13 合并执行；R05、R17、R18、R24、R26、R27、R28、R29 必须按本计划的收窄描述执行，不得按旧宽口径扩大写集。
- [ ] 每个修复类提交必须包含同提交 bug-locking 测试、fixture、golden 或 snapshot。

## 5-Agent Adjudication Summary

| Queue | Items | Decision |
|---|---|---|
| FIX | R01-R04, R06-R08, R11-R12, R14-R16, R19-R23, R25 | 生产可达或生产门禁可达，按矩阵中的唯一最优修复进入 worker 队列。 |
| FIX with merged scope | R09 + R13, R10 | R09/R13 合并为 prompt snapshot hardening；R10 保留为 thread start/fork 事件发布与补偿修复，但必须覆盖 start 和 fork，不只修 fork。 |
| FIX with narrowed scope | R05, R17, R18, R24, R26, R27, R28, R29 | 可达但原计划过宽或已有部分防护；worker 必须按裁决后的更小写集执行。 |
| COMMENT | None | 5/5 reviewer 未确认任何 Rxx 完全不可生产触达；不创建注释队列条目。 |
| DROP | None | 无整项删除。 |

## Global Execution Rules

- [ ] 开始执行前，控制器从仓库根目录运行:

```bash
git status --short
rg --files | rg '(^AGENTS.md$|README.md$|docs/doc/codemap/README.md$)'
```

Expected: 记录既有脏状态；不得 revert、format、stage 或移动无关文件。

- [ ] 每条 lane 在独立 worktree 中执行，分支命名为 `codex/20260628-risk-lXX-<slug>`，worktree 命名为 `.worktrees/20260628-risk-lXX-<slug>`。

```bash
base_branch=$(git branch --show-current)
git worktree add ".worktrees/20260628-risk-lXX-<slug>" -b "codex/20260628-risk-lXX-<slug>" "$base_branch"
cd ".worktrees/20260628-risk-lXX-<slug>"
make install-hooks
git status --short
```

Expected: 新 worktree 干净；如已有用户脏改，停止并向控制器报告真实路径。

- [ ] worker 只能修改本计划列出的 `Files`。需要越界时停止并输出 `NEEDS_APPROVAL`，包含 lane、真实文件路径、越界原因、拒绝后的不可修复风险。
- [ ] 任何 Go 源文件改完后先运行单文件守卫:

```bash
./scripts/test_with_guard.sh path/to/file.go
```

Expected: exit 0；该单文件守卫可能无输出。

- [ ] 每个风险先写 RED 测试并运行确认失败，再实现最小修复，最后运行 GREEN 验证。没有新鲜命令输出不得报告 lane 完成。
- [ ] 执行源码分析、影响面判断、共享符号修改时必须按 `docs/internal-notes/LSP系统提示词.md` 使用 LSP；如果 LSP 无法检查目标语言，停止源码修改并报告 blocker。
- [ ] 控制器集成顺序固定为: L01 automation secret/command -> L02 Wails -> L03 provider/skill -> L04 thread/prompt -> L05 approval/Codex -> L06 MCP/LSP -> L07 store/memory -> L08 frontend/observability -> L09 guard/backpressure。

## Risk Matrix: Unique Fix and Upper Defense

| ID | Severity | Evidence | 唯一最优修复 | 是否需要上层防御 | 上层防御落点与做法 |
|---|---|---|---|---|---|
| R01 | P0 | `cmd/mcp-orch/orchestration/nodeexec/executor_automation_command.go:97` | automation command result 不再持久化 `Args`，并在 result 组装前递归删除 `__inputs` 与 sharedfile 原文；stdout/stderr/command 统一走强脱敏器。 | 必须 | `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go` 的 `finalizeAutomationOutcome` 作为最终出口再做一次 result scrub；`cmd/mcp-orch/orchestration/dag.go` 的 DTO 输出层拒绝暴露未脱敏 `Result`。 |
| R02 | P1 | `cmd/mcp-orch/orchestration/nodeexec/executor_automation_command.go:77` | command card 改为 argv 执行模型；禁止 `sh -c` 执行用户模板拼出的整串命令。 | 必须 | `cmd/mcp-orch/tools/task_tools.go` 与 node config validator 在创建、apply_ops、dispatch 三个入口统一校验 executable config，缺 cwd/roots 或含 shell metachar 直接拒绝。 |
| R03 | P1 | `internal/ui/wails/clipboard_assets.go:53` | `/local-image` 只接受后端登记的短期 token，不接受 raw absolute path。 | 必须 | `frontend-app/src/pages/chat/components/markdownMessageModel.js` 停止把模型文本中的绝对路径转成 `/local-image?path=`；Wails HTTP handler 作为安全边界校验 token。 |
| R04 | P1 | `internal/ui/wails/window.go:130` | `FRONTEND_DEVSERVER_URL` 使用与 `VITE_DEV_URL` 相同的 loopback URL 校验；生产模式拒绝 dev URL。 | 必须 | Wails 启动 preflight 在 `internal/ui/wails/assets.go` / `window.go` 统一校验所有 dev URL env；校验失败阻断窗口创建。 |
| R05 | P1 | `internal/provider/shared/provider_home.go:436` | 只阻断或隔离 active project/app-managed mirror 的 drift、unmanaged 或 canonical-deleted-with-drift；personal/UI-only 普通内容冲突保留为可见告警，不一刀切拖死 provider。 | 必须 | Claude/Codex driver 启动 preflight 复用同一 conflict taxonomy；provider start API 展示 conflict kind、scope、建议动作，只有 active mirror 高危冲突阻断运行。 |
| R06 | P1 | `internal/module/skill/mirrorpath/path.go:17` | 禁止项目 mirror root 本身是 symlink；只允许系统祖先路径符号链接。 | 必须 | mirror publisher/reconciler 保留原始 root 做冲突报告，不先 resolve 后再判断；provider_home 启动前复用同一 root validator。 |
| R07 | P1 | `internal/provider/claudecli/transport_config.go:72` | Claude system prompt dump 默认关闭；debug 模式也只写私有目录 `0600`，默认只记录 hash 和长度。 | 需要 | provider launch preflight 检查 debug dump 开关、目录权限和输出路径；生产模式发现 dump 开关时要求显式 developer approval。 |
| R08 | P1 | `internal/provider/claudecli/config.go:77` | `disallowed_tools` 等安全配置严格解析，类型错误直接返回 provider 启动错误。 | 必须 | provider config normalization 层返回 typed error；UI/provider start API 不允许把 malformed config 降级为默认值或空列表。 |
| R09 | P1 | `internal/module/thread/prompt_snapshot.go:218` | post-snapshot 线程 resume/fork 必须读取并校验 stored snapshot；legacy pre-snapshot 线程只能走显式迁移/兼容窗口；同时合并 R13 的 hash 覆盖修复。 | 必须 | thread service 在 provider resume/fork 前统一调用 snapshot preflight；frontend 将 snapshot_missing/snapshot_invalid/legacy_snapshot_required 显示为阻断或迁移状态。 |
| R10 | P1 | `internal/module/thread/lifecycle_fork.go:43` | start/fork 都必须在 row、binding、snapshot 持久化成功后才能发布 `thread.Started` 或 kickoff turn；失败时补偿清理，或返回明确 `created_only`/`snapshot_failed` 状态。 | 必须 | `persistStartedThread`/thread event publisher 入口禁止 snapshot 未保存时发 Started；provider kickoff 只能消费已通过 snapshot gate 的 thread state。 |
| R11 | P1 | `internal/module/turn/prompt_assembly.go:16` | 正常 turn 缺 `promptAssembly` 直接 fail-fast；只有显式 minimal/no-prompt 模式允许空 assembly。 | 必须 | turn service 构造时要求 promptAssembly dependency；RPC 层和 provider mapper 不接受空 assembly 进入 provider request。 |
| R12 | P1 | `internal/module/prompt/dynamic.go:422` | dynamic provider 同名 slot 注册改为启动期错误，或显式 composite provider。 | 需要 | Fx app assembly 添加 dynamic-section health check；完整 app 同时加载 datasource/datasource_v2 时必须显式选择 composite 方案。 |
| R13 | P1 | `internal/module/thread/prompt_snapshot.go:160` | 并入 R09；snapshot hash 覆盖 sorted `SectionSnapshot`、`Generation` 和 boundary 字段，篡改任何 prompt section 都 invalid。 | 需要 | `storedPromptSnapshotValid` 作为 provider resume/fork 的唯一入口；与 R09 同提交覆盖 section-only tamper、missing snapshot、legacy gate 测试。 |
| R14 | P1 | `internal/provider/codexapp/session_approval.go:46` | malformed approval payload 返回 typed error；有 request id 时回写拒绝或错误决策，无 request id 时终止该 turn。 | 必须 | approval bridge 通知入口不再跳过 raw dispatch 后吞错；approval manager 记录 approval_parse_failed 事件。 |
| R15 | P1 | `internal/provider/codexapp/recovery.go:338` | recovery replay 前区分 lost、completed、failed、canceled；只有明确 lost 才 replay。 | 必须 | provider session status API 返回 typed state；thread recovery service 对 unknown state 阻断并提示人工确认。 |
| R16 | P1 | `internal/platform/rpc/approval_support.go:217` | `approvalPolicy` 只从可信 session/server state 读取；peer payload 内 policy 字段被忽略或拒绝。 | 必须 | `internal/platform/mcpcontrol/handlers.go` 的 approval request 入口剥离 peer-controlled policy；approval manager 记录 source authority。 |
| R17 | P1 | `cmd/mcp-lsp/tools/factory.go:109` | 收窄为 mcp-lsp direct handlers 仍使用 `decodeLenient` 的缺口；复用已有 strict decoder/schema，不重造 toolbridge validator。 | 必须 | direct MCP handler 注册层切到 strict/schema；toolbridge 已有 pre-dispatch schema 防线，只补覆盖测试并防止回退。 |
| R18 | P1 | `cmd/mcp-lsp/tools/tool_edit_support.go:136` | 收窄为 ordinary edit/format 等 direct edit 默认允许 app-managed roots；rename/code_action 的 WorkspaceEdit all-or-none 校验若已存在则不重复实现。 | 必须 | mcp-lsp tool scope 增加 app-managed write capability；所有 direct write 入口共享 capability validator，缺 capability 拒绝。 |
| R19 | P1 | `internal/store/sharedfile/store.go:88` | sharedfile 写入使用 staging temp file；DB 成功提交后再 atomic rename/publish，失败不覆盖旧正文。 | 需要 | read disk-missing 已有 fail-fast 时保持；补 orphan temp cleanup 与 write-conflict telemetry，degraded 状态作为二期可选。 |
| R20 | P1 | `internal/platform/db/module.go:83` | schema gate 校验关键列、索引和约束，不只看 schema_migrations 版本和表存在。 | 必须 | DB module 启动 preflight 对 `agent_threads.prompt_snapshot`、`shared_files.content_location` 等关键字段做 PRAGMA 校验；缺失阻断启动。 |
| R21 | P1 | `internal/module/memory/extract_transcript.go:94` | memory extraction prompt 把 transcript 包进不可信边界并转义，禁止对话内容改写 extractor 指令。 | 需要 | memory entrypoint/retrieval 渲染统一使用 untrusted fence；缺 provider 时不得 silent heuristic 写入，除非配置显式允许。 |
| R22 | P1 | `internal/provider/dreamexec/dreamexec.go:92` | dream CLI stderr 不进入 model/user-visible error 原文；只记录 exit code、长度、hash、脱敏摘要。 | 需要 | unified dream executor 日志层增加 secret/path redactor；metrics 区分 failed-attempt usage 和 successful dream usage。 |
| R23 | P1 | `frontend-app/src/shared/api/wailsBridge.js:239` | Wails event JSON 解析失败生成可见 `bridge.event.parse_failed`，不得返回 `{}` 后静默丢弃。 | 必须 | client store 的 bridge event handler 对 missing method 进入 error channel；UI 显示 bridge degraded 状态。 |
| R24 | P1 | `frontend-app/src/adapters/observabilityAdapter.js:5` | 收窄为 bad event、missing events、non-array events 不能归一为空/ok；response 非对象已 fail-fast 的部分不重复修。 | 必须 | observability service/page 保留 `degraded`、`parseError`、`tailError`、`tailTimedOut`，页面不得把缺失状态默认显示成 `ok`。 |
| R25 | P2 | `frontend-app/src/pages/chat/components/ChatApprovalMessage.jsx:24` | approval 提交按 requestId 幂等锁定；超时后不得恢复同一 request 的二次点击。 | 需要 | client store 保存 approval submit state；后端 approval/respond 对重复 request 返回 idempotent result 或 conflict。 |
| R26 | P1 | `scripts/code_size_guard.go:141` | 按 AGENTS 裁决收窄：保留显式 repair/shrink 工作流；hook/CI 的 guard 路径必须 read-only 或在 guard 后 fail on baseline/freeze drift。 | 必须 | pre-commit/pre-push/CI 在 guard 后检查 `internal/archtest/baseline*.json` 与 freeze registry 无漂移；需要 shrink 时必须走显式命令和人工审查。 |
| R27 | P1 | `internal/archtest/ratchet.go:186` | absent-from-baseline 但位于扫描范围内的 HEAD-existing 文件也执行 zero-tolerance/new-file metrics；不要声称所有硬阈值都绕过。 | 必须 | `CheckAll`/CI 覆盖 absent-baseline metrics，新增测试锁定 HEAD-existing nonbaseline 文件的 panic/init/global/naked goroutine 等指标。 |
| R28 | P1 | `scripts/guard_fix_commits_have_tests.sh:117` | direct test path 也必须 relation-aware：同包、owner 映射或显式 bug id；保留已有 fixture owner 逻辑。 | 必须 | commit-msg/pre-push/CI 共享 relation-aware helper；无关 direct test 不得满足 fix commit。 |
| R29 | P2 | `internal/module/memory/hook_worker.go:118` | memory hook worker 增加有界队列、backpressure 和 lifecycle ctx；Stop 已有超时返回，剩余问题是错误未被 runner 聚合成可见降级状态。 | 需要 | service runner 聚合/上报 Stop error，client/store 暴露 backlog/degraded 指标，dispatch 不再使用 `context.Background()` 脱离生命周期。 |

## Lane L01: Automation Command, DAG Result, and Shell Execution

**Branch / Worktree:** `codex/20260628-risk-l01-automation-command` / `.worktrees/20260628-risk-l01-automation-command`

**Files:**
- Modify: `cmd/mcp-orch/orchestration/nodeexec/executor_automation_command.go`
- Modify: `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- Modify: `cmd/mcp-orch/orchestration/node_router.go`
- Modify: `cmd/mcp-orch/orchestration/dag.go`
- Modify: `cmd/mcp-orch/tools/task_tools.go`
- Modify: `cmd/mcp-orch/tools/task_apply_ops.go`
- Test: `cmd/mcp-orch/orchestration/nodeexec/*_test.go`
- Test: `cmd/mcp-orch/orchestration/*_test.go`
- Test: `cmd/mcp-orch/tools/*_test.go`

**Risks:** R01, R02.

- [ ] **Step 1: Write RED tests for sharedfile input leakage.**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/nodeexec -run TestAutomationCommandResultRedactsSharedFileInputs -count=1
```

Expected RED: the result still contains a token from `inputs.from_sharedfiles` under `Args.__inputs`.

- [ ] **Step 2: Write RED tests for final DAG DTO leakage.**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration -run TestAutomationOutcomeNeverPersistsRawInputs -count=1
```

Expected RED: `NodeOutcome.Result` or `dagNodeDTO.Result` exposes raw sharedfile content.

- [ ] **Step 3: Write RED tests for shell expansion injection.**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/nodeexec -run TestAutomationCommandRejectsShellExpansionTemplates -count=1
```

Expected RED: command template containing `$VAR`, `${VAR}`, glob, or whitespace-splitting input reaches `sh -c`.

- [ ] **Step 4: Implement unique optimal fix.**

Change command execution to argv-based execution. Remove `Args` from persisted command result or replace it with metadata-only fields: arg count, redaction marker, template id, and safe command family. Recursively scrub `__inputs`, `token`, `authorization`, `cookie`, `secret`, `password`, and `api_key` before any result persistence.

- [ ] **Step 5: Add upper defense.**

Make node config validation shared by create DAG, apply ops, and dispatch. Reject command cards without trusted cwd/roots, reject shell mode, and reject terminal node dispatch before executor runs.

- [ ] **Step 6: Verify lane.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools -count=1
```

## Lane L02: Wails Local Resource and Dev URL Boundary

**Branch / Worktree:** `codex/20260628-risk-l02-wails-boundary` / `.worktrees/20260628-risk-l02-wails-boundary`

**Files:**
- Modify: `internal/ui/wails/clipboard_assets.go`
- Modify: `internal/ui/wails/http_server.go`
- Modify: `internal/ui/wails/window.go`
- Modify: `internal/ui/wails/assets.go`
- Modify: `frontend-app/src/pages/chat/components/markdownMessageModel.js`
- Modify: `frontend-app/src/pages/chat/components/MarkdownInline.jsx`
- Test: `internal/ui/wails/*_test.go`
- Test: `frontend-app/src/pages/chat/components/*test*`

**Risks:** R03, R04.

- [ ] **Step 1: Write RED test for arbitrary local image path.**

```bash
./scripts/test_with_guard.sh ./internal/ui/wails -run TestLocalImageRejectsUnregisteredAbsolutePath -count=1
```

Expected RED: raw absolute image path returns 200.

- [ ] **Step 2: Write RED test for frontend absolute path conversion.**

```bash
cd frontend-app
npm test -- --run markdownMessageModel
```

Expected RED: markdown model converts a raw local absolute path into `/local-image?path=...`.

- [ ] **Step 3: Write RED test for unsafe dev URL.**

```bash
./scripts/test_with_guard.sh ./internal/ui/wails -run TestWindowURLRejectsNonLoopbackFrontendDevServerURL -count=1
```

Expected RED: `FRONTEND_DEVSERVER_URL=https://example.com` is accepted.

- [ ] **Step 4: Implement unique optimal fix.**

Introduce a backend-issued local asset token registry. `/local-image` accepts only token ids created from clipboard, generated image, dropped file, or project-scoped preview flows. Replace raw path rendering in Markdown with backend-issued preview URL rendering.

- [ ] **Step 5: Add upper defense.**

Centralize dev URL validation in Wails startup and apply it to `VITE_DEV_URL` and `FRONTEND_DEVSERVER_URL`. Production mode rejects all dev URL env values.

- [ ] **Step 6: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/ui/wails -count=1
cd frontend-app && npm run lint && npm test && npm run build
```

## Lane L03: Provider Home, Skill Mirrors, and Claude Config

**Branch / Worktree:** `codex/20260628-risk-l03-provider-skill` / `.worktrees/20260628-risk-l03-provider-skill`

**Files:**
- Modify: `internal/provider/shared/provider_home.go`
- Modify: `internal/module/skill/mirrorpath/path.go`
- Modify: `internal/module/skill/mirror_publisher.go`
- Modify: `internal/module/skill/mirror_reconciler.go`
- Modify: `internal/provider/claudecli/transport_config.go`
- Modify: `internal/provider/claudecli/config.go`
- Test: `internal/provider/shared/*_test.go`
- Test: `internal/module/skill/*_test.go`
- Test: `internal/provider/claudecli/*_test.go`

**Risks:** R05, R06, R07, R08.

- [ ] **Step 1: Write RED tests for mirror drift blocking.**

```bash
./scripts/test_with_guard.sh ./internal/provider/shared ./internal/module/skill -run 'TestProviderHomeBlocks(Drift|Unmanaged|CanonicalDeletedWithDrift)' -count=1
```

Expected RED: active project/app-managed mirror drift or unmanaged provider skill returns non-blocking issue and provider launch continues; personal/UI-only drift remains report-only.

- [ ] **Step 2: Write RED tests for root symlink escape.**

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run TestMirrorRootSymlinkIsRejected -count=1
```

Expected RED: a symlink whose target basename is `skills` is accepted.

- [ ] **Step 3: Write RED tests for prompt dump and malformed disallowed tools.**

```bash
./scripts/test_with_guard.sh ./internal/provider/claudecli -run 'TestSystemPromptDumpDisabledByDefault|TestDisallowedToolsRejectsMalformedConfig' -count=1
```

Expected RED: prompt dump writes a file, or malformed `disallowed_tools` becomes empty explicit override.

- [ ] **Step 4: Implement unique optimal fix.**

Make active project/app-managed mirror drift or unmanaged provider skill blocking or isolated before launch; keep personal/UI-only conflicts visible but non-blocking. Reject mirror root symlinks before resolution; disable prompt dump unless explicit debug config is set; parse Claude security config strictly.

- [ ] **Step 5: Add upper defense.**

Provider launch paths for Claude and Codex must call the same mirror/security preflight and convert high-risk active mirror failures into startup errors. UI/provider start API must show conflict kind and scope rather than falling back to stale provider state.

- [ ] **Step 6: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/provider/shared ./internal/module/skill ./internal/provider/claudecli -count=1
```

## Lane L04: Thread, Prompt, Snapshot, and Dynamic Sections

**Branch / Worktree:** `codex/20260628-risk-l04-thread-prompt` / `.worktrees/20260628-risk-l04-thread-prompt`

**Files:**
- Modify: `internal/module/thread/prompt_snapshot.go`
- Modify: `internal/module/thread/lifecycle_fork.go`
- Modify: `internal/module/thread/lifecycle.go`
- Modify: `internal/module/thread/start_session_helpers.go`
- Modify: `internal/module/turn/prompt_assembly.go`
- Modify: `internal/module/prompt/dynamic.go`
- Modify: `internal/module/prompt/assembler.go`
- Test: `internal/module/thread/*_test.go`
- Test: `internal/module/turn/*_test.go`
- Test: `internal/module/prompt/*_test.go`

**Risks:** R09, R10, R11, R12, R13. R13 is merged into the R09 snapshot-hardening work; do not create a separate worker for it.

- [ ] **Step 1: Write RED tests for corrupt/missing snapshot resume and fork.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread -run 'TestPostSnapshotResumeRejectsMissingPromptSnapshot|TestPostSnapshotForkRejectsInvalidPromptSnapshot|TestLegacyThreadUsesExplicitSnapshotMigrationGate' -count=1
```

Expected RED: post-snapshot resume/fork proceeds with empty snapshot, or legacy no-snapshot behavior is not gated by an explicit migration/compatibility marker.

- [ ] **Step 2: Write RED tests for start/fork publish ordering.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread -run 'TestStartDoesNotPublishStartedBeforeSnapshotSaved|TestForkDoesNotPublishStartedBeforeSnapshotSaved' -count=1
```

Expected RED: `thread.Started` is published before snapshot save succeeds on start or fork.

- [ ] **Step 3: Write RED tests for empty promptAssembly and duplicate dynamic section.**

```bash
./scripts/test_with_guard.sh ./internal/module/turn ./internal/module/prompt -run 'TestPrepareTurnRejectsMissingPromptAssembly|TestDynamicSectionDuplicateProviderFails' -count=1
```

Expected RED: missing assembly returns nil error, or duplicate slot overwrites.

- [ ] **Step 4: Write RED test for snapshot hash section tampering.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread -run TestPromptSnapshotHashCoversSectionSnapshot -count=1
```

Expected RED: modifying only `SectionSnapshot` keeps hash valid.

- [ ] **Step 5: Implement unique optimal fix.**

Require valid stored snapshot for post-snapshot resume/fork while preserving an explicit legacy migration gate; reorder start/fork persistence so row, binding, and snapshot succeed before `thread.Started` or kickoff; make promptAssembly a required production dependency; reject duplicate dynamic section providers; include sorted section snapshot and generation in hash.

- [ ] **Step 6: Add upper defense.**

Thread service preflight blocks provider resume/fork when snapshot is invalid and no legacy migration marker applies. Thread event publisher rejects Started without a saved snapshot. Fx app assembly runs dynamic-section duplicate health check. UI receives typed snapshot/prompt errors and does not display a running thread for these failures.

- [ ] **Step 7: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/turn ./internal/module/prompt -count=1
```

## Lane L05: Approval, Codex Recovery, and Approval Policy Authority

**Branch / Worktree:** `codex/20260628-risk-l05-approval-codex-policy` / `.worktrees/20260628-risk-l05-approval-codex-policy`

**Files:**
- Modify: `internal/provider/codexapp/session_approval.go`
- Modify: `internal/provider/codexapp/recovery.go`
- Modify: `internal/provider/codexapp/support.go`
- Modify: `internal/platform/rpc/approval_support.go`
- Modify: `internal/platform/mcpcontrol/handlers.go`
- Modify: `internal/dto/mcp/protocol.go`
- Test: `internal/provider/codexapp/*_test.go`
- Test: `internal/platform/rpc/*_test.go`
- Test: `internal/platform/mcpcontrol/*_test.go`

**Risks:** R14, R15, R16.

- [ ] **Step 1: Write RED test for malformed approval payload.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run TestApprovalPayloadMalformedReturnsErrorDecision -count=1
```

Expected RED: malformed approval notification is swallowed and provider waits.

- [ ] **Step 2: Write RED test for duplicate replay.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -run TestRecoveryDoesNotReplayCompletedTurn -count=1
```

Expected RED: a completed/failed/canceled provider turn represented as `active=false` is replayed because recovery lacks typed terminal/lost state.

- [ ] **Step 3: Write RED test for peer-controlled approvalPolicy.**

```bash
./scripts/test_with_guard.sh ./internal/platform/rpc ./internal/platform/mcpcontrol -run TestApprovalRequestIgnoresPeerControlledApprovalPolicy -count=1
```

Expected RED: payload policy `never` can auto-approve.

- [ ] **Step 4: Implement unique optimal fix.**

Convert approval parse failure into explicit error decision; replay only when provider reports lost/not_found; remove policy authority from peer payload.

- [ ] **Step 5: Add upper defense.**

Approval manager records source authority and rejects policy changes from untrusted paths. Recovery service treats unknown provider state as blocked, not replayable.

- [ ] **Step 6: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/platform/rpc ./internal/platform/mcpcontrol -count=1
```

## Lane L06: MCP/LSP Direct Strict Decode and App-Managed Write Capability

**Branch / Worktree:** `codex/20260628-risk-l06-mcp-lsp-direct-boundaries` / `.worktrees/20260628-risk-l06-mcp-lsp-direct-boundaries`

**Files:**
- Modify: `cmd/mcp-lsp/tools/factory.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_support.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_rename.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go`
- Test: `cmd/mcp-lsp/tools/*_test.go`

**Risks:** R17, R18.

- [ ] **Step 1: Write RED tests for direct handler unknown field rejection.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run TestDirectToolInputRejectsUnknownFields -count=1
```

Expected RED: a direct MCP LSP handler using `decodeLenient` accepts a schema-forbidden field. The existing toolbridge pre-dispatch schema path is not the target of this RED test.

- [ ] **Step 2: Write RED tests for app-managed write outside workspace.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run TestEditRejectsAppManagedPathWithoutWriteCapability -count=1
```

Expected RED: edit writes app-managed path outside workspace roots.

- [ ] **Step 3: Write guard tests for existing WorkspaceEdit all-or-none behavior.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run 'TestRenameRejectsWorkspaceEditOutsideRoots|TestCodeActionRejectsWorkspaceEditOutsideRoots' -count=1
```

Expected: if these tests already pass before production edits, keep them as guard-only regression coverage and do not reimplement the existing all-or-none validator. If either test fails, fix only the shared validator path needed by that failure.

- [ ] **Step 4: Implement unique optimal fix.**

Switch concrete mcp-lsp direct handlers from lenient decode to existing strict decode/schema validation. Add explicit legacy alias normalization only where the source contract still requires it. Add an app-managed write capability check in the shared direct-write path so ordinary edit/format cannot write provider/app-managed roots by default.

- [ ] **Step 5: Add upper defense.**

Record that toolbridge pre-dispatch schema validation already exists and must stay covered by tests; do not add a second validator there. LSP direct edit tools require explicit write capability for app-managed paths.

- [ ] **Step 6: Verify lane.**

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -count=1
```

## Lane L07: Store, DB Schema Gate, Memory, and Dream Logging

**Branch / Worktree:** `codex/20260628-risk-l07-store-memory` / `.worktrees/20260628-risk-l07-store-memory`

**Files:**
- Modify: `internal/store/sharedfile/store.go`
- Modify: `internal/platform/db/module.go`
- Modify: `sql/queries/shared_file.sql`
- Modify: `internal/module/memory/extract_transcript.go`
- Modify: `internal/module/memory/entrypoint_provider.go`
- Modify: `internal/module/memory/retrieval/render.go`
- Modify: `internal/provider/dreamexec/dreamexec.go`
- Modify: `internal/provider/unified/dream_executor.go`
- Test: `internal/store/sharedfile/*_test.go`
- Test: `internal/platform/db/*_test.go`
- Test: `internal/module/memory/*_test.go`
- Test: `internal/provider/dreamexec/*_test.go`
- Test: `internal/provider/unified/*_test.go`

**Risks:** R19, R20, R21, R22.

- [ ] **Step 1: Write RED tests for sharedfile DB failure after disk write.**

```bash
./scripts/test_with_guard.sh ./internal/store/sharedfile -run TestSharedFileUpsertDoesNotOverwriteOnDBFailure -count=1
```

Expected RED: disk content changes even though DB upsert fails.

- [ ] **Step 2: Write RED tests for schema column gate.**

```bash
./scripts/test_with_guard.sh ./internal/platform/db -run TestSchemaGateRejectsMissingRequiredColumns -count=1
```

Expected RED: database with required table but missing key column passes startup.

- [ ] **Step 3: Write RED tests for memory transcript injection and dream stderr.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory ./internal/provider/dreamexec ./internal/provider/unified -run 'TestExtractPromptWrapsTranscriptAsUntrusted|TestDreamExecDoesNotReturnRawStderr' -count=1
```

Expected RED: transcript is raw in prompt, or stderr appears verbatim in returned/logged error.

- [ ] **Step 4: Implement unique optimal fix.**

Make sharedfile writes publish through a staging temp file: write temp content, commit the DB pointer/update successfully, then atomic rename/publish; on DB failure, the old disk content remains intact and temp content is cleaned up. Expand DB schema gate to PRAGMA column/index checks; wrap extraction transcript in untrusted fence; redact dream stderr and separate failed usage metrics.

- [ ] **Step 5: Add upper defense.**

Keep existing disk-missing read fail-fast behavior and add orphan temp cleanup/write-conflict telemetry. DB app startup blocks incomplete schemas. Memory retrieval/entrypoint rendering uses the same untrusted fence helper.

- [ ] **Step 6: Verify lane.**

```bash
make sqlc-verify
./scripts/test_with_guard.sh ./internal/store/sharedfile ./internal/platform/db ./internal/module/memory ./internal/provider/dreamexec ./internal/provider/unified -count=1
```

## Lane L08: Frontend Bridge, Observability, and Approval UI

**Branch / Worktree:** `codex/20260628-risk-l08-frontend-observability` / `.worktrees/20260628-risk-l08-frontend-observability`

**Files:**
- Modify: `frontend-app/src/shared/api/wailsBridge.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.js`
- Modify: `frontend-app/src/adapters/observabilityAdapter.js`
- Modify: `frontend-app/src/services/modules/observabilityService.js`
- Modify: `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- Modify: `frontend-app/src/pages/chat/components/ChatApprovalMessage.jsx`
- Test: `frontend-app/src/shared/api/*test*`
- Test: `frontend-app/src/entities/client/model/*test*`
- Test: `frontend-app/src/adapters/*test*`
- Test: `frontend-app/src/services/modules/*test*`
- Test: `frontend-app/src/pages/observability/*test*`
- Test: `frontend-app/src/pages/chat/components/*test*`

**Risks:** R23, R24, R25.

- [ ] **Step 1: Write RED tests for malformed bridge event.**

```bash
cd frontend-app
npm test -- --run wailsBridge
```

Expected RED: malformed event JSON becomes `{}` and is dropped without visible error.

- [ ] **Step 2: Write RED tests for observability normalization.**

```bash
cd frontend-app
npm test -- --run observabilityAdapter
```

Expected RED: bad events, missing events, or non-array events become empty/ok state. Response non-object failure is already a valid fail-fast path and should not be rewritten.

- [ ] **Step 3: Write RED tests for approval duplicate submit.**

```bash
cd frontend-app
npm test -- --run ChatApprovalMessage
```

Expected RED: timeout re-enables buttons for the same request while original RPC is still in flight.

- [ ] **Step 4: Implement unique optimal fix.**

Turn bridge parse failure into explicit event; make observability adapter fail-fast or preserve degraded fields for bad/missing/non-array events; lock approval submit state by requestId until the original promise settles.

- [ ] **Step 5: Add upper defense.**

Client store routes bridge parse errors to the same visible error channel as backend failures. Observability page never defaults missing status to `ok`. Approval store tracks idempotent request status and blocks duplicate approve/reject.

- [ ] **Step 6: Verify lane.**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

## Lane L09: Guards, CI, and Background Task Backpressure

**Branch / Worktree:** `codex/20260628-risk-l09-guards-backpressure` / `.worktrees/20260628-risk-l09-guards-backpressure`

**Files:**
- Modify: `scripts/code_size_guard.go`
- Modify: `internal/archtest/ratchet.go`
- Modify: `internal/archtest/guardlib.go`
- Modify: `scripts/guard_fix_commits_have_tests.sh`
- Modify: `.githooks/pre-commit`
- Modify: `.githooks/pre-push`
- Modify: `.github/workflows/ci.yml`
- Modify: `internal/module/memory/hook_worker.go`
- Test: `internal/archtest/*_test.go`
- Test: `scripts/*guard*_test.go`
- Test: `internal/module/memory/*_test.go`

**Risks:** R26, R27, R28, R29.

- [ ] **Step 1: Write RED tests for guard drift in hook/CI mode.**

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestGuardHookModeFailsOnBaselineOrFreezeDrift -count=1
```

Expected RED: a hook/CI-style guard path can repair or shrink baseline/freeze registry without failing on the resulting git diff. This does not ban explicit developer repair/shrink commands.

- [ ] **Step 2: Write RED tests for zero-tolerance baseline gap.**

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestExistingNonBaselineFileGetsZeroToleranceMetrics -count=1
```

Expected RED: a HEAD-existing scanned file absent from baseline bypasses absent-baseline zero-tolerance metrics.

- [ ] **Step 3: Write RED tests for unrelated fix evidence.**

```bash
./scripts/test_with_guard.sh ./scripts -run TestFixCommitRejectsUnrelatedTestFile -count=1
```

Expected RED: an unrelated direct test file satisfies a fix commit even when it is outside the changed production package/owner and has no explicit bug id.

- [ ] **Step 4: Write RED tests for memory hook queue.**

```bash
./scripts/test_with_guard.sh ./internal/module/memory -run TestMemoryHookWorkerBackpressureAndStopReportsTimeout -count=1
```

Expected RED: the queue grows without bound, dispatch uses a background context detached from service lifecycle, or Stop timeout/backlog is not propagated to the service runner as a degraded condition.

- [ ] **Step 5: Implement unique optimal fix.**

Keep explicit baseline repair/shrink commands available, but make hook/CI guard paths fail on any baseline or freeze-registry drift. Apply absent-baseline zero-tolerance metrics to all scanned non-baseline files. Make direct-test fix evidence relation-aware while preserving existing fixture owner logic. Add bounded queue, backpressure, and lifecycle ctx to memory hook worker.

- [ ] **Step 6: Add upper defense.**

Hooks and CI fail on generated baseline/freeze drift after guard execution. Commit-msg/pre-push share the relation-aware helper. Memory service exposes backlog/degraded state and propagates Stop timeout or backlog errors to the runner.

- [ ] **Step 7: Verify lane.**

```bash
./scripts/test_with_guard.sh ./internal/archtest ./internal/module/memory -count=1
make guard
git diff --check
```

## Integration Checklist

- [ ] 控制器收回每条 lane 后先运行:

```bash
git status --short
git diff --stat
git diff --check
```

Expected: 只有 lane 允许写集中的文件变化；无 whitespace error。

- [ ] 控制器对每条 lane 复跑其 GREEN 命令。worker 报告不能替代控制器验证。
- [ ] 每条 lane 合入前确认 `唯一最优修复` 和 `上层防御` 都存在。若 worker 主张某个上层防御不需要，必须输出 `NEEDS_APPROVAL` 并给出源代码证据；控制器批准前不得合入。
- [ ] 合入顺序按全局执行规则执行；每合入一组安全/状态边界 lane 后复跑相邻 affected package。
- [ ] 最终广义验证:

```bash
make guard
make sqlc-verify
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/nodeexec ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/tools ./internal/ui/wails ./internal/provider/shared ./internal/module/skill ./internal/provider/claudecli ./internal/module/thread ./internal/module/turn ./internal/module/prompt ./internal/provider/codexapp ./internal/platform/rpc ./internal/platform/mcpcontrol ./cmd/mcp-lsp/tools ./internal/store/sharedfile ./internal/platform/db ./internal/module/memory ./internal/provider/dreamexec ./internal/provider/unified ./internal/archtest -count=1
cd frontend-app && npm run lint && npm test && npm run build
git diff --check
```

Expected: all commands exit 0. If any command is split for runtime reasons, final report must list the exact split commands and outputs.

## Worker Dispatch Prompt Template

```text
You are worker for Lane LXX in /Users/mima0000/Desktop/wj/super-agent-v3.
Use docs/plans/2026-06-28-20agent-production-risk-remediation.md as the only implementation plan.
Work only inside branch codex/20260628-risk-lXX-<slug> and worktree .worktrees/20260628-risk-lXX-<slug>.
Do not modify files outside this lane's Files list.
Start with git status --short, README.md, docs/doc/codemap/README.md, the relevant codemap volume, docs/internal-notes/LSP系统提示词.md, and LSP navigation for the files you will touch.
For each risk, first write the RED test, run it, and record the failing output. Then implement the unique optimal fix and the listed upper defense. Run the GREEN verification command.
If any required source file or test is outside the lane write set, stop with NEEDS_APPROVAL and list exact paths and reasons.
Final report must include: changed files, RED command and failure summary, GREEN command and pass summary, upper defense location, remaining risk.
```
