# 20-Agent Production Risk Remediation Plan

> **For agentic workers:** 本计划使用普通控制器调度，不使用编排调度。控制器在主会话创建隔离 worktree、派发普通 subagent、收集结果、审查 diff、按优先级合并。所有步骤使用 checkbox (`- [ ]`) 语法追踪。

**Goal:** 修复 20-agent 全域代码风险审查裁决出的 28 组已证实生产可达风险，并为每个修复明确是否需要上层防护以及防护加法。

**Architecture:** 唯一最优执行方案是按写集和运行时边界拆成 11 条并行 lane。每条 lane 在独立 worktree 和独立分支上完成 RED 测试、最小修复、GREEN 验证和自审；主控制器只做调度、越界审批、最终裁决和集成验证。这样能最大化并行度，同时避免 mcp-lsp、mcp-orch、Wails、thread/provider、frontend、release/guard 等高冲突区域互相踩写。

**Tech Stack:** Go 1.25.7、SQLite/sqlc、Fx、Wails、MCP sidecar、React/Vite/Vitest、shell/PowerShell release scripts、Git hooks。

**Verification Surface:** Go 变更使用 `./scripts/test_with_guard.sh <packages> -count=1`；frontend-app 变更使用 `cd frontend-app && npm run lint && npm test && npm run build`；release/script/guard 变更使用对应脚本测试、`git diff --check`、`make guard`、`./scripts/test_with_guard.sh ./internal/archtest -count=1`；SQL/store 变更额外运行 `make sqlc-verify`。

---

## 普通调度规则

- [ ] 控制器先运行 `git status --short`，记录 main 工作区既有脏改；不得 revert、format、stage 或移动无关文件。
- [ ] 每条 lane 创建独立 worktree，命名为 `.worktrees/20260627-risk-lXX-<slug>`，分支命名为 `codex/20260627-risk-lXX-<slug>`。
- [ ] 每条 lane 执行：

```bash
base_branch=$(git branch --show-current)
git worktree add ".worktrees/20260627-risk-lXX-<slug>" -b "codex/20260627-risk-lXX-<slug>" "$base_branch"
cd ".worktrees/20260627-risk-lXX-<slug>"
make install-hooks
git status --short
```

- [ ] 控制器用普通 subagent 派发 lane，不使用编排调度工具。
- [ ] 任何涉及 `frontend-app` 的 lane，在进入 worktree 后先运行 `make frontend-app-deps`。如果该 target 在执行环境不可用，则运行 `cd frontend-app && npm ci`，再执行 frontend lint/test/build，避免新 worktree 因缺少被 gitignore 的 `node_modules` 产生假失败。
- [ ] worker 只允许修改 lane 写集中列出的文件和同包测试。需要越界时必须停止并返回 `NEEDS_APPROVAL`，写明真实路径、越界原因、拒绝后的不可修复风险。
- [ ] 每个生产风险必须先写 RED 测试，运行并记录失败摘要，再写生产代码。修复类提交必须同提交包含 bug-locking 测试、fixture、golden 或 snapshot。
- [ ] 每改完一个 Go 文件，立即运行单文件守卫，例如 `./scripts/test_with_guard.sh internal/module/skill/exec.go`。单文件守卫静默 exit 0 才能继续。
- [ ] lane 完成前必须运行本计划列出的 GREEN 验证；没有新鲜验证输出不得报告完成。
- [ ] worker 最终报告必须包含：改动文件、RED 命令和失败摘要、GREEN 命令和通过摘要、上层防护实际落点、未覆盖风险。
- [ ] 控制器集成顺序固定为：P0 安全边界 lane -> mcp-orch 生命周期 lane -> thread/provider lane -> store/skill/toolbridge lane -> frontend lane -> release/guard lane -> P2 cleanup lane。

## 风险到修复与上层防护裁决

| ID | Lane | 风险类型 | 唯一最优修复 | 上层防护裁决与加法 |
|---|---|---|---|---|
| R01 | L01 | P0 LSP WorkspaceEdit 可写出 workspace | WorkspaceEdit 先收集所有 URI，统一 canonicalize、root containment、symlink 和 regular-file 校验；任一失败则不写任何文件 | 需要。`rename`/`code_action` 工具入口增加高风险 operation guard：缺 `_workspaceRoots` 直接拒绝，UI/调用层显示“workspace edit denied” |
| R02 | L02 | P0 `command/exec` 可读 workspace 外文件 | 将 `command/exec` 改成 allowlist-only 的只读命令面；cwd 和所有 path-like 参数都由可信 workspace roots 校验；未知命令、可嵌入越权读取的命令和 symlink escape 均失败 | 需要。RPC 层拒绝请求体自带 cwd 作为 authority，provider/toolbridge 只传受控 scope |
| R03 | L03 | P0 `/local-image` 任意本机图片读取 | 后端只服务已登记资源 token；前端停止把任意本地绝对路径转成 `/local-image?path=` | 需要。后端 token registry 是安全边界；前端过滤只作为触发面缩小 |
| R04 | L04 | P0 MCP tool payload 全量落盘泄密 | payload log 默认 metadata-only；显式 debug 才记录脱敏 payload；secret-like 字段强制 redaction | 需要。MCP common 层加最终脱敏，provider/toolbridge 同步避免传 secret |
| R05 | L09 | P0 Windows package 泄露 API key | Windows release 默认禁止写入 `SILICONFLOW_API_KEY` 等敏感 key；release 包只加载用户本地配置 | 需要。packaged `.env` verifier 加 denylist，打包时发现敏感 key 直接失败 |
| R06 | L03 | P0/P1 Wails WS cookie/CSRF 边界弱 | `/wails/ws` 改为 per-window bootstrap token 或显式 Authorization；静态资源响应不再发 WS token cookie | 需要。敏感 RPC 方法增加 capability/method allowlist，防止传输层漏网 |
| R07 | L05 | P1 automation command 缺 workspace/env 边界 | command card 执行必须收到可信 cwd、allowed roots、env allowlist；缺失 fail-fast | 需要。`task_start/dispatch` 和 UI 展示执行边界，拒绝未绑定 scope 的 automation |
| R08 | L03 | P1 `FRONTEND_DEVSERVER_URL` 可加载外部页 | Go 层统一校验 dev URL，仅允许本机开发源；生产包拒绝 dev URL env | 需要。Wails host 启动 preflight 发现生产模式 dev URL 直接失败 |
| R09 | L05 | P1 mcp-orch DAG 下游 pending 导致 run 永久 running | failed ancestor 的直接/传递 downstream 必须终态收敛；`fail_fast` 只控制无关分支取消 | 需要。调度和 UI 检测 failed ancestor + pending dependent + running run 并告警 |
| R10 | L05 | P1 `task_dag_apply_ops` 绕过节点 config 校验 | create/apply_ops 共享节点 config validator；写入前验证，失败不 bump version | 需要。start/run 前做历史 bad template preflight 并显示阻断原因 |
| R11 | L04 | P1 tool schema 禁 unknown 但 handler 宽松吞字段 | handler decode 改 strict JSON/schema validation，兼容字段必须显式映射或显式失败 | 需要。provider/toolbridge 调用前同步验证 schema，错误类型区分 unknown field |
| R12 | L04 | P1 mcp-lsp/orch runner fatal error 被吞 | runner done error 在 Fx OnStop 返回非 canceled 错误；启动失败必须非零退出 | 需要。supervisor/桌面 host 检测 sidecar early exit 并阻断继续使用 |
| R13 | L07 | P1 Codex fork/handoff 丢 provider home/mirror 身份 | fork/handoff 从 source binding/runtime 读取完整 CodexHome、InstanceKey、ModelProvider 并持久化到新线程 | 需要。service 边界遇 Codex source identity 缺失或 partial 直接 fail-fast |
| R14 | L07 | P1 thread fork 半成功和首 turn 乐观成功 | thread row、binding、snapshot、首 turn kickoff 原子化；或返回明确 `created_only` / `turn_failed` 状态 | 需要。前端不得把 thread 创建成功等同首 turn 成功；后端返回可区分状态 |
| R15 | L02 | P1 canonical skill 扫描跟随 `SKILL.md` symlink | canonical scan 用 `Lstat`，拒绝 symlink；ListSkills/skill/list 遇 symlink 返回错误 | 需要。turn hydration 和 provider mirror 不使用 stale skill list，错误向上暴露 |
| R16 | L06 | P1 MCP server config runtime migration 非事务 | legacy table rebuild 放入事务，处理 `_next` 残留恢复；失败保持旧表可用或启动失败 | 需要。mcp server service 检测 migration anomaly，拒绝返回空配置假成功 |
| R17 | L08 | P1 workflow template save/rollback 暴露给动态工具 | host tools 拆 read/write registry；普通动态工具只暴露 read，write 需显式 admin/developer approval | 需要。toolbridge dispatch 强制 capability，前端 allowlist 只作产品控制 |
| R18 | L08 | P1 toolbridge 策略读取吞错 fail-open | binding/thread/runtime 读取改三态：found、not found、read failed；read failed fail-closed | 需要。JSON-RPC/UI 区分用户参数错和策略状态不可用，展示可重试诊断 |
| R19 | L06 | P1 cron 旧 turn 可覆盖新 claim | MarkFinished/MarkFailed 同时 fence active_turn_id/run_id/claim_token | 需要。stale terminal event 或 claim mismatch 进入 metric/alert/UI 状态 |
| R20 | L05 | P1 wakeup lease 过期可重复执行 automation | executor 前检查节点终态和 lease；长执行续约；terminal node wakeup 幂等标 sent | 需要。dispatch/UI 禁止 terminal node 重放，显示 stale wakeup 收敛 |
| R21 | L05 | P1 mcp-orch local launcher 无 `cmd.Wait()` owner | launcher 持有唯一 Wait owner 或复用 exitMonitor；stop 等待退出，超时 kill/reap | 需要。状态查询不只看 `cmd != nil`，必须核验真实 process/exit event |
| R22 | L11 | P1 approval timeline 被前端过滤不可见 | `approval` kind 即使无文本也可见；补 backend-shaped pending approval patch 测试 | 需要。pending approval 有 requestId 但无可渲染项时前端告警或阻断会话假死 |
| R23 | L09 | P1 release/update 指错 repo、旧 UI、断更新 | 强制显式 update repo；frontend cache key 覆盖全部输入或 release 禁 cache；Windows 校验旧公钥连续性 | 需要。package verifier 阻断占位 repo、dist hash 不匹配、公钥不连续 |
| R24 | L10 | P1 E2E fixture provider 进入生产 Fx 图 | fixture provider 移出生产 app Module，或生产模式遇 fixture env 直接失败 | 需要。launcher/preflight 清理 fixture env 并阻断 packaged production 启动 |
| R25 | L10 | P1 门禁盲区导致测试/工具链结论不可信 | guard 扫新增文件全指标、import-direction、pkg；fix evidence 关联生产改动；CI 不排除生产包 | 需要。hook/CI 加 relation-aware 强制门禁，拒绝无审计的生产包排除 |
| R26 | L08 | P2 observability tail 失败静默降级 | IncludeTail 失败返回错误，或响应暴露 degraded/tailError/tailFilesScanned | 需要。UI 把 memory-only 结果标为降级，不能当完整诊断 |
| R27 | L11 | P2 shutdown/polling cleanup | Codex app-server close 贯穿 ctx；Memory poller 使用 AbortSignal/cancel token | 需要。session/page 层保留超时和单 poller 防护，避免重复后台任务 |
| R28 | L10 | P2 架构边界漏防 | provider 不 import platform/db；platform 不直 import store，改 contract port；guard 加禁止规则 | 需要。import-direction guard 覆盖 provider->platform/db、platform->store |

## Lane L01: LSP WorkspaceEdit 根边界

**Branch / Worktree:** `codex/20260627-risk-l01-lsp-workspace-edit` / `.worktrees/20260627-risk-l01-lsp-workspace-edit`

**Files:**
- Modify: `cmd/mcp-lsp/tools/tool_edit_rename.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go`
- Modify: `cmd/mcp-lsp/tools/tool_edit_support.go`
- Test: `cmd/mcp-lsp/tools/tool_edit_rename_test.go`
- Test: `cmd/mcp-lsp/tools/tool_edit_lsp_actions_test.go`

- [ ] RED: Add a rename WorkspaceEdit test where one edit URI points outside every workspace root. Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run TestRenameRejectsWorkspaceEditOutsideRoots -count=1
```

Expected: FAIL because current `applyFileEdits` writes by absolute path without root validation.

- [ ] RED: Add a code_action WorkspaceEdit test with an outside-root file URI. Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -run TestCodeActionRejectsWorkspaceEditOutsideRoots -count=1
```

Expected: FAIL for the same missing containment check.

- [ ] GREEN: Add `validateWorkspaceEditFiles(ctx, roots, changes)` in `tool_edit_support.go`. It must use existing workspace root resolution helpers, reject symlinks, reject non-regular files, and return all offending URIs.
- [ ] GREEN: Call validation before any write in rename and code_action paths. The operation must be all-or-none: if validation fails, no file is changed.
- [ ] Upper guard: in both tool entry handlers, require non-empty trusted workspace roots. Missing roots must return a structured MCP error before requesting LSP edits.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools -count=1
```

## Lane L02: Skill command execution and canonical skill roots

**Branch / Worktree:** `codex/20260627-risk-l02-skill-local-boundary` / `.worktrees/20260627-risk-l02-skill-local-boundary`

**Files:**
- Modify: `internal/module/skill/exec.go`
- Modify: `internal/module/skill/skills_meta.go`
- Modify: `internal/module/skill/canonical_store.go`
- Test: `internal/module/skill/exec_test.go`
- Test: `internal/module/skill/skills_meta_test.go`
- Test: `internal/module/skill/canonical_store_test.go`

- [ ] RED: Add `TestExecCommandRejectsCWDOutsideWorkspaceRoots` with `cwd="/"` and `command="cat"` reading an absolute file. Run:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -run TestExecCommandRejectsCWDOutsideWorkspaceRoots -count=1
```

Expected: FAIL because non-empty cwd is currently accepted as-is.

- [ ] RED: Add `TestExecCommandRejectsAbsoluteArgOutsideWorkspaceRoots` and `TestExecCommandRejectsSymlinkEscape`.
- [ ] RED: Add `TestExecCommandRejectsUnknownCommand` with a command that is not explicitly allowlisted. Expected: FAIL because the current denylist-style filter can allow unknown binaries.
- [ ] RED: Add tests for commands that can embed file reads or secondary execution, including `awk`, `find`, `sed`, `less`, and `more`. The expected behavior is either rejection or routing through a constrained built-in implementation that still enforces workspace roots.
- [ ] RED: Add `TestExecCommandRequiresTrustedWorkspaceRoots`; a caller-provided cwd without trusted roots must fail before execution.
- [ ] RED: Add `TestListSkillsRejectsSymlinkSkillFile` with `.agent/skills/demo/SKILL.md` symlinked outside the root.
- [ ] GREEN: Replace denylist-style command gating with a small allowlist-only dispatcher. Prefer internal read/search helpers over shelling out where possible. Any retained external command must have exact argv rules, canonical path validation, no shell, and no inherited cwd authority.
- [ ] GREEN: Change `ExecCommand` input resolution so cwd comes from trusted workspace roots or is canonicalized under them. Reject absolute path args and `..` path args after symlink resolution when they escape allowed roots.
- [ ] GREEN: Use `os.Lstat` / `DirEntry.Type` when scanning canonical skill files. Reject symlinked `SKILL.md` with a hard error.
- [ ] Upper guard: skill RPC must require workspace scope for command execution; turn skill hydration must fail visibly if canonical skill scan fails.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./internal/module/skill -count=1
```

## Lane L03: Wails host local-resource and bridge boundary

**Branch / Worktree:** `codex/20260627-risk-l03-wails-local-boundary` / `.worktrees/20260627-risk-l03-wails-local-boundary`

**Files:**
- Modify: `internal/ui/wails/clipboard_assets.go`
- Modify: `internal/ui/wails/http_server.go`
- Modify: `internal/ui/wails/window.go`
- Modify: `internal/ui/wails/assets.go`
- Modify: `internal/ui/wails/binding.go`
- Modify: `internal/platform/rpc/transport_ws.go`
- Create: `internal/ui/wails/rpc_method_policy.go`
- Modify: `frontend-app/src/pages/chat/components/MarkdownInline.jsx`
- Modify: `frontend-app/src/pages/chat/components/markdownMessageModel.js`
- Test: `internal/ui/wails/*_test.go`
- Test: `internal/platform/rpc/*_test.go`
- Test: `frontend-app/src/pages/chat/components/*test*`

- [ ] RED: Add a Wails HTTP test proving `/local-image?path=/tmp/secret.png` is rejected unless the path was registered by the backend. Run:

```bash
./scripts/test_with_guard.sh ./internal/ui/wails -run TestLocalImageRejectsUnregisteredAbsolutePath -count=1
```

Expected: FAIL because current handler serves any valid image path.

- [ ] RED: Add a WS auth test proving a static asset response must not mint the WS cookie used for `/wails/ws`.
- [ ] RED: Add a WS/native bridge policy test proving sensitive methods such as `thread/start`, `thread/stop`, `mcpServer/add`, `mcpServer/start`, `mcpServer/stop`, provider config save/apply, and destructive local actions are rejected unless the caller has the Wails window capability for that method.
- [ ] RED: Add a window URL test proving production mode rejects `FRONTEND_DEVSERVER_URL=https://example.com`.
- [ ] GREEN: Introduce a short-lived local image token registry in the Wails HTTP server. `/local-image` accepts only opaque token IDs, not raw filesystem paths.
- [ ] GREEN: Stop Markdown rendering from converting arbitrary local absolute paths to `/local-image`. Only render backend-issued preview URLs.
- [ ] GREEN: Remove unauthenticated WS token cookie issuance from static asset responses. Require per-window bootstrap token or explicit Authorization/Sec-WebSocket-Protocol for `/wails/ws`.
- [ ] GREEN: Add one Wails RPC method policy shared by WS and native `CallAPI` paths before they enter `Dispatch`. The policy must default-deny sensitive methods, allow read-only methods explicitly, and return a structured authorization error instead of silently dropping requests.
- [ ] GREEN: Validate `FRONTEND_DEVSERVER_URL` with the same allowlist as `VITE_DEV_URL`; packaged/production mode rejects any dev URL.
- [ ] Upper guard: sensitive RPC dispatch over Wails gets method-level capability checks; frontend shows a clear blocked-local-resource state instead of silently trying raw paths.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./internal/ui/wails -count=1
make frontend-app-deps
cd frontend-app && npm run lint && npm test && npm run build
```

## Lane L04: MCP common payload, strict schemas, sidecar fatal errors

**Branch / Worktree:** `codex/20260627-risk-l04-mcp-contracts` / `.worktrees/20260627-risk-l04-mcp-contracts`

**Files:**
- Modify: `internal/mcpserver/common/tool_payload_log.go`
- Modify: `internal/platform/shared/jsonutil.go`
- Modify: `cmd/mcp-lsp/fx.go`
- Modify: `cmd/mcp-orch/runtime.go`
- Modify: `cmd/mcp-lsp/http_runner.go`
- Modify: `cmd/mcp-orch/http_runner.go`
- Modify: `internal/platform/toolbridge/handler_peer_decode.go`
- Modify: `internal/platform/toolbridge/handler_host_tools.go`
- Modify: `internal/platform/toolbridge/host_tools.go`
- Modify: `internal/platform/toolbridge/protocol_contract.go`
- Test: `internal/mcpserver/common/*_test.go`
- Test: `internal/platform/shared/*_test.go`
- Test: `internal/platform/toolbridge/*_test.go`
- Test: `cmd/mcp-lsp/*_test.go`
- Test: `cmd/mcp-orch/*_test.go`

- [ ] RED: Add `TestToolPayloadLogRedactsArgumentsAndResultByDefault`.
- [ ] RED: Add `TestToolPayloadLogDebugModeStillRedactsSecrets`. It must cover stdio and HTTP snapshots with argument/result keys such as `token`, `password`, `api_key`, and token-like strings; raw secret text must not appear even when debug payload logging is explicitly enabled.
- [ ] RED: Add `TestDecodeInputRejectsUnknownFields` using a schema-forbidden camelCase field such as `dryRun`.
- [ ] RED: Add toolbridge pre-dispatch schema tests for host tools, Codex dynamic surface, and peer proxy forwarding. Unknown fields must fail before handler execution unless an alias is explicitly registered.
- [ ] RED: Add sidecar lifecycle tests proving non-canceled runner errors are returned from Fx shutdown/startup.
- [ ] GREEN: Replace full payload snapshots with metadata-only records by default: tool name, call id, duration, status, size, and redaction marker.
- [ ] GREEN: Add an explicit debug opt-in for payload bodies; even debug mode must redact common secret fields and token-looking strings.
- [ ] GREEN: Implement strict JSON decode or schema validation in shared DecodeInput. Known legacy aliases must be declared in code and tested.
- [ ] GREEN: Add a toolbridge pre-dispatch validator that uses each dynamic tool's `InputSchema` before forwarding to host, Codex, or peer handlers. This validator is an upper guard, not a replacement for strict handler decoding.
- [ ] GREEN: Align mcp-lsp and mcp-orch runner shutdown with mcp-ida behavior: non-canceled fatal errors propagate.
- [ ] Upper guard: provider/toolbridge validates schema before dispatch; supervisor treats early sidecar exit as unavailable and blocks calls.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./internal/mcpserver/common ./internal/platform/shared ./internal/platform/toolbridge ./cmd/mcp-lsp ./cmd/mcp-orch -count=1
```

## Lane L05: mcp-orch lifecycle, automation, wakeup, launcher

**Branch / Worktree:** `codex/20260627-risk-l05-orch-lifecycle` / `.worktrees/20260627-risk-l05-orch-lifecycle`

**Files:**
- Modify: `cmd/mcp-orch/orchestration/retrypolicy/policy.go`
- Modify: `cmd/mcp-orch/fx.go`
- Modify: `cmd/mcp-orch/orchestration/service.go`
- Modify: `cmd/mcp-orch/orchestration/dag.go`
- Modify: `cmd/mcp-orch/orchestration/dag_query.go`
- Modify: `cmd/mcp-orch/orchestration/exitmonitor/monitor.go`
- Modify: `cmd/mcp-orch/orchestration/nodeexec/plan.go`
- Modify: `cmd/mcp-orch/orchestration/nodeexec/config.go`
- Modify: `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`
- Modify: `cmd/mcp-orch/orchestration/node_router.go`
- Modify: `cmd/mcp-orch/orchestration/nodeexec/executor_automation.go`
- Modify: `cmd/mcp-orch/orchestration/nodeexec/executor_automation_command.go`
- Modify: `cmd/mcp-orch/orchestration/launcher.go`
- Modify: `cmd/mcp-orch/store/taskdag/store_fail_downstream.go`
- Modify: `cmd/mcp-orch/store/taskdag/store_wakeup.go`
- Modify: `cmd/mcp-orch/store/taskdag/contract.go`
- Modify: `cmd/mcp-orch/sql/queries/task_dag_wakeup_dispatch.sql`
- Modify: `cmd/mcp-orch/sql/queries/task_dag_wakeup_query.sql`
- Modify: `cmd/mcp-orch/tools/task_apply_ops.go`
- Modify: `cmd/mcp-orch/tools/task_tools.go`
- Generated: sqlc output affected by `cmd/mcp-orch/sql/queries/task_dag_wakeup_*.sql`
- Test: `cmd/mcp-orch/orchestration/**/*_test.go`
- Test: `cmd/mcp-orch/tools/*_test.go`
- Test: `cmd/mcp-orch/store/taskdag/*_test.go`

- [ ] RED: Add a production SQLite/sqlc DAG test with A->B->C and a diamond dependency. With `fail_fast` omitted/false, all transitive pending downstream nodes blocked by a failed ancestor must become terminal and the run must finalize.
- [ ] RED: Add apply_ops tests where add_node/update_node writes invalid executable config and must be rejected before persistence.
- [ ] RED: Add automation command tests where missing cwd/roots and inherited sensitive env are rejected.
- [ ] RED: Add wakeup test using the store path where automation execution exceeds lease, node is already done, and second claim must not execute side effects.
- [ ] RED: Add local launcher test through Fx/service + runner actor production wiring proving every local start enters the same exit event stream and stop reaps the process.
- [ ] GREEN: Split downstream convergence from `fail_fast`. Failed dependencies always mark impossible downstream nodes terminal; `fail_fast` only cancels unrelated runnable branches.
- [ ] GREEN: Extract shared typed node config validation and call it inside CreateDAG and ApplyOps transactions, before Upsert/version bump in `dag_query.go`. start/run preflight only handles historical bad templates.
- [ ] GREEN: Pass trusted `AutomationCommandRunOptions` with required cwd, allowed roots, and env allowlist. Never inherit full `os.Environ()` by default.
- [ ] GREEN: Before executing a wakeup side effect, fence node status and lease. Add store-level renew/CAS support for long execution, and ensure CAS miss cannot occur after a side effect has already run. Terminal nodes mark wakeup sent without running executor.
- [ ] GREEN: Connect local launcher to the service `exitMonitor` or move local start back through the service start path so exactly one owner calls `cmd.Wait()` and publishes exit events. stop waits, then force-stops, then waits again.
- [ ] Upper guard: task start/dispatch/update APIs reject missing execution scope, terminal-node dispatch, bad templates, and stale wakeups with structured errors.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/... ./cmd/mcp-orch/tools ./cmd/mcp-orch/store/taskdag -count=1
make sqlc-verify
```

## Lane L06: Store migration and cron fencing

**Branch / Worktree:** `codex/20260627-risk-l06-store-cron` / `.worktrees/20260627-risk-l06-store-cron`

**Files:**
- Modify: `internal/store/mcpserver/store.go`
- Modify: `internal/store/mcpserver/store_test.go`
- Modify: `internal/store/cron/contract.go`
- Modify: `internal/store/cron/store.go`
- Modify: `internal/module/cron/scheduler.go`
- Modify: `internal/module/cron/scheduler_recovery.go`
- Modify: `internal/module/cron/progress_subscriber.go`
- Modify: `sql/queries/cron_job.sql`
- Test: `internal/store/mcpserver/*_test.go`
- Test: `internal/module/cron/*_test.go`
- Generated: sqlc output affected by `sql/queries/cron_job.sql`

- [ ] RED: Add a migration test simulating legacy `mcp_server_configs` and injected failure between drop/rename. Expected current behavior loses or strands config.
- [ ] RED: Add cron stale terminal test: old run lease expires, new claim is active, old turn terminal event arrives, and old event must not finish/release the new claim.
- [ ] GREEN: Wrap legacy MCP server table rebuild in an explicit SQLite transaction. On startup, detect and repair or fail on leftover `_next` state.
- [ ] GREEN: Add all-of run/turn fencing to cron terminal updates. SQL and store contracts must match job id, claim token, expected active turn id, and run id together; no `and/or` fallback is allowed.
- [ ] GREEN: Progress subscriber escalates claim-token mismatch/stale event to observable warning/metric instead of debug-only.
- [ ] Upper guard: mcp server service refuses to return empty config after migration anomaly; cron UI/API exposes stale terminal/mismatch state.
- [ ] Verify:

```bash
make sqlc-verify
./scripts/test_with_guard.sh ./internal/store/mcpserver ./internal/module/cron -count=1
```

## Lane L07: Thread fork/handoff and Codex identity lifecycle

**Branch / Worktree:** `codex/20260627-risk-l07-thread-codex` / `.worktrees/20260627-risk-l07-thread-codex`

**Files:**
- Modify: `internal/module/thread/lifecycle_fork.go`
- Modify: `internal/module/thread/handoff.go`
- Modify: `internal/module/thread/start_session.go`
- Modify: `internal/module/thread/lifecycle.go`
- Modify: `internal/provider/codexapp/driver.go`
- Modify: `internal/provider/codexapp/driver_pool_routing.go`
- Modify: `internal/provider/codexapp/transport.go`
- Modify: `internal/provider/codexapp/server_pool.go`
- Test: `internal/module/thread/*_test.go`
- Test: `internal/provider/codexapp/*_test.go`

- [ ] RED: Add fork test with source thread using non-default CodexHome/InstanceKey/ModelProvider; fork must preserve all three.
- [ ] RED: Add handoff test with non-default Codex identity; target thread must inherit source identity.
- [ ] RED: Add fork snapshot ordering test showing snapshot save fails when new thread row does not exist.
- [ ] RED: Add app-server close test where `Close(ctx)` must return when ctx expires.
- [ ] GREEN: Load source binding/runtime identity in fork and handoff. Persist `ConfigOverride` and Codex identity on new thread state.
- [ ] GREEN: Reorder fork persistence so new thread row/binding/snapshot are durable before kickoff, or wrap them in one transaction/service operation.
- [ ] GREEN: Return explicit fork kickoff state: `turn_started`, `created_only`, or `turn_failed`. StartTurn failure must not be presented as full success.
- [ ] GREEN: Thread/codexapp close paths must pass ctx to graceful/kill waits and stop serial closing when ctx is done.
- [ ] Upper guard: `thread/fork` and `thread/handoff` fail-fast on incomplete Codex identity; frontend must render partial fork state separately.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp -count=1
```

## Lane L08: Toolbridge workflow tools and observability degradation

**Branch / Worktree:** `codex/20260627-risk-l08-toolbridge-observability` / `.worktrees/20260627-risk-l08-toolbridge-observability`

**Files:**
- Modify: `internal/platform/toolbridge/host_tools.go`
- Modify: `internal/platform/toolbridge/handler.go`
- Modify: `internal/platform/toolbridge/handler_peer_decode.go`
- Modify: `internal/platform/toolbridge/proxy.go`
- Modify: `internal/platform/observability/service.go`
- Modify: `internal/module/observability/rpc.go`
- Modify: `frontend-app/src/adapters/observabilityAdapter.js`
- Modify: `frontend-app/src/services/modules/observabilityService.js`
- Modify: `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- Test: `internal/platform/toolbridge/*_test.go`
- Test: `internal/platform/observability/*_test.go`
- Test: `internal/module/observability/*_test.go`
- Test: `frontend-app/src/adapters/*test*`
- Test: `frontend-app/src/services/modules/*test*`

- [ ] RED: Add test proving normal Codex dynamic tools do not list `workflow_template_save` or `workflow_template_rollback`.
- [ ] RED: Add test where binding/thread/runtime read returns a real error and toolbridge must fail-closed, not treat it as no binding.
- [ ] RED: Add observability test where IncludeTail=true and tail read fails; response must be error or degraded with tailError.
- [ ] RED: Add frontend adapter/service tests proving `degraded`, `tailError`, `tailTimedOut`, and `tailFilesScanned` survive normalization and are visible to `ObservabilityPage`.
- [ ] GREEN: Split workflow template read and write registries. Only read tools are exposed by default.
- [ ] GREEN: Require explicit admin/developer capability or approval manager for save/rollback dispatch, even if a caller names the tool directly.
- [ ] GREEN: Replace bool-only binding lookups with typed result: found, not found, failed. Failed returns internal policy-unavailable error.
- [ ] GREEN: Add `degraded`, `tailError`, `tailTimedOut`, and `tailFilesScanned` to observability response or return a typed error when full tail was required.
- [ ] GREEN: Update frontend observability adapter and service contracts so the new degradation fields are preserved instead of being dropped during normalization.
- [ ] Upper guard: UI shows policy-unavailable and observability-degraded states; provider/toolbridge schema blocks workflow writes without approval.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/platform/observability ./internal/module/observability -count=1
make frontend-app-deps
cd frontend-app && npm run lint && npm test && npm run build
```

## Lane L09: Release packaging and updater integrity

**Branch / Worktree:** `codex/20260627-risk-l09-release-packaging` / `.worktrees/20260627-risk-l09-release-packaging`

**Files:**
- Modify: `scripts/package_windows.ps1`
- Modify: `scripts/package_windows_github_release.ps1`
- Modify: `scripts/package_macos_github_release.sh`
- Modify: `scripts/publish_github_release.sh`
- Modify: `scripts/package_macos.sh`
- Modify: `scripts/package_linux.sh`
- Test: `scripts/*test*`
- Test: `.github/workflows/ci.yml` only if release checks must run in CI

- [ ] RED: Add script tests proving Windows packaging refuses to write `SILICONFLOW_API_KEY` into staged `.env`.
- [ ] RED: Add wrapper tests proving missing `SUPER_DOLPHIN_UPDATE_GITHUB_REPO` fails instead of defaulting to placeholder repo.
- [ ] RED: Add frontend cache-key tests proving changes to `frontend-app/package.json`, `vite.config.js`, `index.html`, and `public/**` invalidate package cache.
- [ ] RED: Add Windows release key continuity test proving wrapper rejects current public key that differs from previous package public key.
- [ ] GREEN: Remove default sensitive key export from Windows package. If a key is present in environment, print an explicit refusal with key name but not value.
- [ ] GREEN: Require explicit update repo in all release wrappers. Known placeholder repos fail verification.
- [ ] GREEN: Either disable frontend build cache in release mode or include every Vite input plus Node/npm versions in the cache key.
- [ ] GREEN: Require previous public key input for Windows release wrapper and verify continuity before manifest generation.
- [ ] Upper guard: package verifier fails on sensitive `.env` keys, placeholder repo, stale dist hash, and key mismatch.
- [ ] Verify:

```bash
git diff --check
./scripts/test_with_guard.sh ./scripts -run 'Test.*(Package|Release|Updater|FrontendBuild).*' -count=1
pwsh -NoProfile -ExecutionPolicy Bypass -File ./scripts/package_windows.ps1 -WhatIf
./scripts/publish_github_release.sh --help
```

The exact script-test command must match the repo's existing shell/PowerShell test harness after worker inspection. If no harness exists, add focused script tests under the existing scripts test pattern before changing production scripts.

## Lane L10: Guard, fixture, architecture boundary

**Branch / Worktree:** `codex/20260627-risk-l10-guard-architecture` / `.worktrees/20260627-risk-l10-guard-architecture`

**Files:**
- Modify: `internal/app/modules.go`
- Modify: `internal/app/modules_e2efixture_test.go`
- Modify: `internal/provider/unified/session_resolver.go`
- Modify: `internal/platform/cachekeepalive/manager.go`
- Modify: `internal/contract/errors.go`
- Create: `internal/contract/cache_keepalive.go`
- Modify: `internal/archtest/guardlib.go`
- Modify: `internal/archtest/ratchet.go`
- Modify: `internal/archtest/dependency_direction_test.go`
- Modify: `internal/archtest/naked_goroutine_guard_test.go`
- Modify: `scripts/code_size_guard.go`
- Modify: `scripts/guard_fix_commits_have_tests.sh`
- Modify: `scripts/ci_commit_guard.sh`
- Modify: `.github/workflows/ci.yml`
- Test: `internal/archtest/*_test.go`
- Test: `scripts/*guard*_test.go`

- [ ] RED: Add app module test proving fixture provider cannot be registered in production mode even if `PROMPT_INTENT_E2E_DREAM_FIXTURE` is set.
- [ ] RED: Add archtest fixture for a new production file containing `panic`, `init`, global var, and naked goroutine; guard must fail before baseline update.
- [ ] RED: Add guard test proving `pkg` roots are scanned.
- [ ] RED: Add dependency-direction tests forbidding provider -> `internal/platform/db` and platform -> `internal/store` except explicit audited allowlist.
- [ ] RED: Add fix-commit guard test where production file plus unrelated fixture must fail.
- [ ] RED: Add CI test or workflow assertion forbidding `grep -v /internal/provider/dreamexec` style production package exclusion.
- [ ] GREEN: Move e2e fixture module behind explicit test/e2e harness or production-mode fail-fast.
- [ ] GREEN: Make baseline guard evaluate all metrics for scanned files absent from baseline and fill `NewFileViolations`.
- [ ] GREEN: Add `pkg` to default scan roots and update tests/baseline only after inspecting resulting debt. Do not weaken thresholds.
- [ ] GREEN: Add import-direction checks for provider/platform boundaries and fix current violations with contract ports.
- [ ] GREEN: Replace current production violations before enabling the guard: provider code must use `contract.IsNotFound` instead of importing `internal/platform/db`; cache keepalive must depend on a narrow contract port instead of concrete store packages.
- [ ] GREEN: Make fix evidence relation-aware: fixture/golden/snapshot counts only when referenced by changed tests or mapped to affected package.
- [ ] Upper guard: hooks and CI run the same relation-aware guard; production fixture env and architecture violations fail before merge.
- [ ] Verify:

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
make guard
git diff --check
```

## Lane L11: Frontend approval state and background cleanup

**Branch / Worktree:** `codex/20260627-risk-l11-frontend-state-cleanup` / `.worktrees/20260627-risk-l11-frontend-state-cleanup`

**Files:**
- Modify: `frontend-app/src/entities/client/model/timelineRuntime.js`
- Modify: `frontend-app/src/entities/client/model/bridgePatchState.js`
- Modify: `frontend-app/src/pages/chat/components/TimelineMessage.jsx`
- Modify: `frontend-app/src/entities/client/model/forkSlice.js`
- Modify: `frontend-app/src/entities/client/model/threadForkState.js`
- Modify: `frontend-app/src/pages/memory/MemoryPage.jsx`
- Test: `frontend-app/src/entities/client/model/*test*`
- Test: `frontend-app/src/pages/chat/components/*test*`
- Test: `frontend-app/src/pages/memory/*test*`

- [ ] RED: Add test where a backend-shaped pending approval item has `kind="approval"`, `status="pending"`, `requestId`, and empty text; it must survive `isVisibleTimelineItem` filtering.
- [ ] RED: Add fork UI test where startThread succeeds but startTurn rejects; UI must not show full success or working turn.
- [ ] RED: Add MemoryPage test where component unmount aborts `waitForMemoryConsolidationJob` before the 180 polling attempts complete.
- [ ] GREEN: Treat `approval` kind as visible based on requestId/status, not text.
- [ ] GREEN: Fork UI tracks partial state separately: thread created, turn kickoff failed, rollback pending, or user-action required.
- [ ] GREEN: Add AbortSignal/cancel token to memory consolidation polling and delay; cleanup aborts in-flight and future polls.
- [ ] Upper guard: frontend store warns when pending approval cannot render, and ensures at most one poller per consolidation job.
- [ ] Verify:

```bash
make frontend-app-deps
cd frontend-app
npm run lint
npm test
npm run build
```

## Integration and final verification

- [ ] After each lane returns, controller reviews the diff against the lane write set. Any unapproved file is rejected or sent back for `NEEDS_APPROVAL`.
- [ ] Controller runs same-lane GREEN command in the lane worktree before accepting the lane.
- [ ] Merge order:
  1. L01, L02, L03, L04 security boundary fixes.
  2. L05 orchestration lifecycle fix.
  3. L06 store/cron fix.
  4. L07 thread/provider fix.
  5. L08 toolbridge/observability fix.
  6. L11 frontend state fix.
  7. L09 release fix.
  8. L10 guard/architecture fix.
- [ ] After each merge, run the affected package or frontend verification again from the integration worktree.
- [ ] Final broad verification:

```bash
make guard
make sqlc-verify
./scripts/test_with_guard.sh ./cmd/mcp-lsp/tools ./cmd/mcp-orch/... ./internal/module/skill ./internal/ui/wails ./internal/mcpserver/common ./internal/platform/shared ./internal/store/mcpserver ./internal/module/cron ./internal/module/thread ./internal/provider/codexapp ./internal/platform/toolbridge ./internal/platform/observability ./internal/module/observability ./internal/app ./internal/archtest -count=1
./scripts/test_with_guard.sh ./scripts -run 'Test.*(Package|Release|Updater|FrontendBuild).*' -count=1
make frontend-app-deps
cd frontend-app && npm run lint && npm test && npm run build
git diff --check
```

- [ ] If broad verification is too slow, controller may split it into the same command groups, but final report must include exact command outputs and any skipped surface.
- [ ] Final report must include a table with all R01-R28 and status: fixed, verified, deferred with reason, or blocked with exact blocker. A risk cannot be marked fixed without its listed upper guard being present or explicitly re-adjudicated in review.

## Dispatch prompt template

Use this template for each ordinary subagent:

```text
You are worker for Lane LXX in /Users/mima0000/Desktop/wj/super-agent-v3.
Use the plan file docs/plans/2026-06-27-20agent-production-risk-remediation.md.
Work only inside branch codex/20260627-risk-lXX-<slug> and worktree .worktrees/20260627-risk-lXX-<slug>.
Use ordinary subagent dispatch. Do not use orchestration tooling. Do not modify files outside the lane write set.
Start with git status --short and make install-hooks.
For each listed risk, write RED test first, run it and record the failing summary, then implement the minimal fix and run the GREEN command.
Every fix must include the listed upper guard unless you can prove it is unnecessary; if so, stop and ask controller approval before dropping it.
If any required file change is outside the write set, stop with NEEDS_APPROVAL and list exact paths and reasons.
Final answer must include files changed, RED commands, GREEN commands, upper guard locations, and residual risk.
```
