# Production Guard One-Shot Parallel Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在一次并行执行中消灭当前生产 baseline 的所有真实代码守卫违规，并在集成分支重新生成收缩后的生产 baseline。

**Architecture:** 10 个彼此隔离的修复车道从同一个固定提交 `0a5cc09b0` 创建独立 worktree，各车道只修改自己拥有的文件；集成控制器只负责合并、冲突裁决和最终 `go run ./scripts/code_size_guard.go --freeze`。方案不设置 W1/W2，也不允许先留冻结再二次清理。

**Tech Stack:** Go, repo-native archtest/code guard, git worktree, cmd/mcp-lsp LSP 工具链, repo test scripts.

**Verification Surface:** LSP 定位/影响面/诊断证据、车道 owned `.go` 文件单文件守卫、车道包级 `./scripts/test_with_guard.sh`、集成分支 `go run ./scripts/code_size_guard.go --freeze`、最终 `./scripts/test_with_guard.sh ./... -count=1`。

---

## Baseline Snapshot

基线来源: fixed commit `0a5cc09b0 fix: 校验工作流 DAG 启动响应`。

生产 baseline 当前冻结文件数: 100。

本计划只处理真实生产违规，不处理已被守卫排除的 `missing_docs`，也不把 `lines`、`max_params`、`max_returns`、`max_struct_fields` 当作独立冻结原因。

真实生产违规分布:

| Violation | Files | Total |
| --- | ---: | ---: |
| `raw_goroutines` | 56 | 75 |
| `max_struct_methods` | 21 | 351 |
| `global_vars` | 14 | 26 |
| `naked_goroutines` | 12 | 17 |
| `panic_count` | 11 | 11 |
| `has_init` | 2 | 2 |

## Global Rules

- [ ] 每个车道从固定提交 `0a5cc09b0` 创建隔离 worktree；不得从浮动远端分支派生，避免远端推进后清单失真。
- [ ] 每个车道只能修改自己文件清单内的生产文件、同包测试、必要的同包 helper；跨车道文件必须退回集成控制器裁决。
- [ ] 不允许新增 `archguard` ignore、扩大 baseline、放宽守卫规则、删除测试来通过。
- [ ] `missing_docs` 不参与本计划修复。
- [ ] 车道不得 stage 或 commit `internal/archtest/baseline.json`、`internal/archtest/baseline_test.json`、`internal/archtest/freeze_registry.go`；这些生成文件只由集成控制器最终一次性更新。
- [ ] 每个车道必须提交 LSP 证据摘要: 定位、影响面、精读文件、诊断。
- [ ] 所有 goroutine 修复必须保留原生命周期和取消语义；禁止用同步调用替换异步行为来“消除计数”。
- [ ] 所有 panic 修复必须把错误返回到已有边界；禁止用 recover 吞错。
- [ ] 所有全局状态修复必须转为显式依赖、构造参数、局部不可变工厂或测试隔离状态；禁止换一个包继续全局化。

## Unique Best Fixes

这些规则是每类违规的唯一最优修复路线，车道不得自行选择替代方案。

| Violation | Unique Fix |
| --- | --- |
| `raw_goroutines` | 将 `go func` 接入当前对象或模块的生命周期 runner: 优先复用已有 `rungroup`、`safe_go`、`WaitGroup`、`errgroup` 或包内 worker supervisor；缺失时在同包新增最小生命周期 helper，接收 `context.Context`、命名任务和错误处理回调。 |
| `naked_goroutines` | 将裸 goroutine 绑定到可等待、可取消、可记录错误的 runner；调用点必须有 shutdown 或 ctx 归属。 |
| `max_struct_methods` | 按职责切分 receiver，把方法移动到 role-specific collaborator 或 narrow interface；旧 struct 只保留状态聚合和核心编排方法，不允许保留转发壳方法。 |
| `global_vars` | 可为 `const` 的改为 `const`；不可为 `const` 的改为显式构造依赖、局部工厂函数返回值或测试隔离 fixture。可变状态必须归属实例。 |
| `has_init` | 删除 `init`，改为显式 bootstrap/constructor 注册；调用方必须在启动路径显式触发。 |
| `panic_count` | 改为返回 `error`，在 CLI/main/fx 边界统一 fatal；库层和 store 层不得 panic。 |

## Worktree Matrix

集成控制器先执行以下命令创建 10 个隔离工作树，然后并行派发子代理:

```bash
BASE_COMMIT=0a5cc09b0
git worktree add .worktrees/20260705-prodguard-pg01-mcp-lsp -b codex/20260705-prodguard-pg01-mcp-lsp "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg02-mcp-orch -b codex/20260705-prodguard-pg02-mcp-orch "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg03-cmd-other -b codex/20260705-prodguard-pg03-cmd-other "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg04-module -b codex/20260705-prodguard-pg04-module "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg05-platform-mcpserver -b codex/20260705-prodguard-pg05-platform-mcpserver "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg06-provider -b codex/20260705-prodguard-pg06-provider "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg07-store -b codex/20260705-prodguard-pg07-store "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg08-pkg -b codex/20260705-prodguard-pg08-pkg "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg09-internal-other -b codex/20260705-prodguard-pg09-internal-other "$BASE_COMMIT"
git worktree add .worktrees/20260705-prodguard-pg10-misc -b codex/20260705-prodguard-pg10-misc "$BASE_COMMIT"
```

## Common Lane Workflow

每个车道都执行同一个闭环:

- [ ] 读取 `AGENTS.md`、本计划、相关 repo-local 技能。
- [ ] 用 LSP 完成定位、影响面、精读和诊断；命令失败时收窄路径重试，并把 blocker 写入车道回传。
- [ ] 仅按本计划的 Unique Fix 修改所属文件。
- [ ] 运行车道 owned `.go` 文件单文件守卫命令；该命令不使用 baseline，必须证明所属文件当前违规清零。
- [ ] 运行车道包级验证命令；该命令用于证明行为和同包测试没有回归。
- [ ] 如果包级验证自动改写 guard 生成文件，提交前执行 `git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go`，保持车道提交不含 baseline。
- [ ] 提交前执行 `git status --short`，只 stage 当前车道拥有的生产文件、同包测试和同包 helper；禁止 `git add .`。
- [ ] 使用本车道 Validation 代码块里的 exact `git commit -m ...` 命令提交；只有车道新增或修改行为测试并确认为 bug 修复时才把 `chore:` 改为 `fix:`。
- [ ] 回传: commit hash、修改文件、已清零违规、验证命令输出摘要、剩余 blocker。

通用 LSP 证据模板:

```text
定位: grep(text_search|ast_search) 或 structure(document_symbol/workspace_symbol)
影响面: xref(references/call_hierarchy)
精读: file(read_file)
诊断: file(diagnostics)
```

## PG-01 mcp-lsp

Worktree: `.worktrees/20260705-prodguard-pg01-mcp-lsp`

Branch: `codex/20260705-prodguard-pg01-mcp-lsp`

Owned files and violations:

| File | Violations |
| --- | --- |
| `cmd/mcp-lsp/manager/registry.go` | `max_struct_methods=13` |
| `cmd/mcp-lsp/manager/scope.go` | `max_struct_methods=27` |
| `cmd/mcp-lsp/middleware/timeout.go` | `naked_goroutines=1`, `raw_goroutines=1` |
| `cmd/mcp-lsp/multilsp/bootstrap_doc.go` | `naked_goroutines=1`, `raw_goroutines=1` |
| `cmd/mcp-lsp/multilsp/cache.go` | `max_struct_methods=12` |
| `cmd/mcp-lsp/multilsp/manager_symbols.go` | `max_struct_methods=18` |
| `cmd/mcp-lsp/multilsp/recycler.go` | `global_vars=1` |
| `cmd/mcp-lsp/multilsp/transport.go` | `raw_goroutines=2` |
| `cmd/mcp-lsp/multilsp/transport_conn.go` | `naked_goroutines=2`, `raw_goroutines=2` |
| `cmd/mcp-lsp/search/searchutil.go` | `naked_goroutines=1`, `raw_goroutines=1` |
| `cmd/mcp-lsp/tools/tool_edit_replace_update.go` | `naked_goroutines=2`, `raw_goroutines=2` |
| `cmd/mcp-lsp/tools/tool_file.go` | `naked_goroutines=2`, `raw_goroutines=2` |

Unique remediation:

- [ ] Split manager/scope/cache/symbol receiver methods into role-specific collaborators that match existing packages: registry lookup, lifecycle, scope matching, symbol cache, and symbol query.
- [ ] Replace all mcp-lsp goroutine launches with context-bound lifecycle runner used by the manager/transport/tool request path.
- [ ] Replace `recycler.go` package global with explicit recycler config/state owned by manager construction.

Validation:

```bash
./scripts/test_with_guard.sh \
  cmd/mcp-lsp/manager/registry.go \
  cmd/mcp-lsp/manager/scope.go \
  cmd/mcp-lsp/middleware/timeout.go \
  cmd/mcp-lsp/multilsp/bootstrap_doc.go \
  cmd/mcp-lsp/multilsp/cache.go \
  cmd/mcp-lsp/multilsp/manager_symbols.go \
  cmd/mcp-lsp/multilsp/recycler.go \
  cmd/mcp-lsp/multilsp/transport.go \
  cmd/mcp-lsp/multilsp/transport_conn.go \
  cmd/mcp-lsp/search/searchutil.go \
  cmd/mcp-lsp/tools/tool_edit_replace_update.go \
  cmd/mcp-lsp/tools/tool_file.go
./scripts/test_with_guard.sh ./cmd/mcp-lsp/manager ./cmd/mcp-lsp/middleware ./cmd/mcp-lsp/multilsp ./cmd/mcp-lsp/search ./cmd/mcp-lsp/tools -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-01 mcp-lsp 违规"
```

## PG-02 mcp-orch

Worktree: `.worktrees/20260705-prodguard-pg02-mcp-orch`

Branch: `codex/20260705-prodguard-pg02-mcp-orch`

Owned files and violations:

| File | Violations |
| --- | --- |
| `cmd/mcp-orch/fxadapter/dag_cron_store.go` | `global_vars=1` |
| `cmd/mcp-orch/notify/subscribers.go` | `raw_goroutines=1` |
| `cmd/mcp-orch/orchestration/cron/scheduler_cron.go` | `naked_goroutines=1`, `raw_goroutines=1` |
| `cmd/mcp-orch/orchestration/documentartifact/document_artifact.go` | `global_vars=1` |
| `cmd/mcp-orch/orchestration/exitmonitor/monitor.go` | `naked_goroutines=2`, `raw_goroutines=2` |
| `cmd/mcp-orch/orchestration/launcher.go` | `raw_goroutines=1` |
| `cmd/mcp-orch/orchestration/process_lifecycle.go` | `naked_goroutines=2`, `raw_goroutines=2` |
| `cmd/mcp-orch/store/sqlctx/db.go` | `panic_count=1` |
| `cmd/mcp-orch/store/taskdag/store.go` | `max_struct_methods=23` |
| `cmd/mcp-orch/store/taskdag/store_wakeup.go` | `max_struct_methods=14` |
| `cmd/mcp-orch/tools/orchestration_tools.go` | `naked_goroutines=1`, `raw_goroutines=1` |

Unique remediation:

- [ ] Move store/taskdag methods into command/query/wakeup collaborators with narrow receivers; update constructors and tests.
- [ ] Convert orchestration goroutines to rungroup/supervisor lifecycle tasks owned by cron, launcher, exitmonitor, or process lifecycle objects.
- [ ] Replace package globals in fxadapter/documentartifact with explicit constructor dependencies.
- [ ] Replace sqlctx panic with returned error and fail-fast propagation at orchestration startup boundary.

Validation:

```bash
./scripts/test_with_guard.sh \
  cmd/mcp-orch/fxadapter/dag_cron_store.go \
  cmd/mcp-orch/notify/subscribers.go \
  cmd/mcp-orch/orchestration/cron/scheduler_cron.go \
  cmd/mcp-orch/orchestration/documentartifact/document_artifact.go \
  cmd/mcp-orch/orchestration/exitmonitor/monitor.go \
  cmd/mcp-orch/orchestration/launcher.go \
  cmd/mcp-orch/orchestration/process_lifecycle.go \
  cmd/mcp-orch/store/sqlctx/db.go \
  cmd/mcp-orch/store/taskdag/store.go \
  cmd/mcp-orch/store/taskdag/store_wakeup.go \
  cmd/mcp-orch/tools/orchestration_tools.go
./scripts/test_with_guard.sh ./cmd/mcp-orch/fxadapter ./cmd/mcp-orch/notify ./cmd/mcp-orch/orchestration/... ./cmd/mcp-orch/store/... ./cmd/mcp-orch/tools -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-02 mcp-orch 违规"
```

## PG-03 cmd-other

Worktree: `.worktrees/20260705-prodguard-pg03-cmd-other`

Branch: `codex/20260705-prodguard-pg03-cmd-other`

Owned files and violations:

| File | Violations |
| --- | --- |
| `cmd/agent-terminal/frontend.go` | `panic_count=1` |
| `cmd/super-dolphin-updater/install.go` | `global_vars=1` |
| `cmd/super-dolphin-updater/main.go` | `naked_goroutines=1`, `raw_goroutines=1` |

Unique remediation:

- [ ] Return frontend setup errors instead of panic; terminate only in CLI/main error handling.
- [ ] Move updater global state into explicit install options or app struct.
- [ ] Attach updater goroutine to context-aware runner with shutdown wait.

Validation:

```bash
./scripts/test_with_guard.sh \
  cmd/agent-terminal/frontend.go \
  cmd/super-dolphin-updater/install.go \
  cmd/super-dolphin-updater/main.go
./scripts/test_with_guard.sh ./cmd/agent-terminal ./cmd/super-dolphin-updater -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-03 cmd-other 违规"
```

## PG-04 module

Worktree: `.worktrees/20260705-prodguard-pg04-module`

Branch: `codex/20260705-prodguard-pg04-module`

Owned files and violations:

| File | Violations |
| --- | --- |
| `internal/module/cron/module.go` | `max_struct_methods=20` |
| `internal/module/cron/progress_subscriber.go` | `raw_goroutines=1` |
| `internal/module/datasource_v2/module.go` | `max_struct_methods=11` |
| `internal/module/memory/auto_dream.go` | `raw_goroutines=1` |
| `internal/module/memory/auto_dream_task.go` | `global_vars=1`, `panic_count=1`, `raw_goroutines=1` |
| `internal/module/memory/extract_runtime.go` | `raw_goroutines=2` |
| `internal/module/memory/hook_worker.go` | `raw_goroutines=1` |
| `internal/module/memory/kairos.go` | `raw_goroutines=1` |
| `internal/module/memory/nested_ingest_worker.go` | `raw_goroutines=1` |
| `internal/module/memory/retrieval/prefetch.go` | `raw_goroutines=1` |
| `internal/module/memory/store.go` | `max_struct_methods=11` |
| `internal/module/memory/team_sync_coordinator.go` | `raw_goroutines=1` |
| `internal/module/prompt/module.go` | `max_struct_methods=19` |
| `internal/module/skill/skills_fs.go` | `max_struct_methods=13` |
| `internal/module/thread/agent_launched_worker.go` | `raw_goroutines=1` |
| `internal/module/thread/session_recovery_worker.go` | `raw_goroutines=2` |
| `internal/module/turn/observation/memory.go` | `max_struct_methods=19` |
| `internal/module/turn/tracker.go` | `max_struct_methods=15` |

Unique remediation:

- [ ] Split large module receivers by runtime role: registration/config, command handlers, subscriptions, store adapters, worker coordination.
- [ ] Convert memory/thread/cron workers to lifecycle-owned background workers using the module context and explicit stop/wait semantics.
- [ ] Replace memory global/panic with injected config or returned error; startup boundary handles fatal.

Validation:

```bash
./scripts/test_with_guard.sh \
  internal/module/cron/module.go \
  internal/module/cron/progress_subscriber.go \
  internal/module/datasource_v2/module.go \
  internal/module/memory/auto_dream.go \
  internal/module/memory/auto_dream_task.go \
  internal/module/memory/extract_runtime.go \
  internal/module/memory/hook_worker.go \
  internal/module/memory/kairos.go \
  internal/module/memory/nested_ingest_worker.go \
  internal/module/memory/retrieval/prefetch.go \
  internal/module/memory/store.go \
  internal/module/memory/team_sync_coordinator.go \
  internal/module/prompt/module.go \
  internal/module/skill/skills_fs.go \
  internal/module/thread/agent_launched_worker.go \
  internal/module/thread/session_recovery_worker.go \
  internal/module/turn/observation/memory.go \
  internal/module/turn/tracker.go
./scripts/test_with_guard.sh ./internal/module/cron ./internal/module/datasource_v2 ./internal/module/memory/... ./internal/module/prompt ./internal/module/skill ./internal/module/thread ./internal/module/turn/... -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-04 module 违规"
```

## PG-05 platform-mcpserver

Worktree: `.worktrees/20260705-prodguard-pg05-platform-mcpserver`

Branch: `codex/20260705-prodguard-pg05-platform-mcpserver`

Owned files and violations:

| File | Violations |
| --- | --- |
| `internal/mcpserver/common/bootstrap/client.go` | `raw_goroutines=2` |
| `internal/mcpserver/common/bootstrap/heartbeat.go` | `raw_goroutines=1` |
| `internal/mcpserver/common/bootstrap/lifecycle.go` | `raw_goroutines=1` |
| `internal/mcpserver/common/bootstrap/reconnect.go` | `raw_goroutines=1` |
| `internal/mcpserver/common/http_transport.go` | `raw_goroutines=1` |
| `internal/mcpserver/common/server.go` | `raw_goroutines=1` |
| `internal/platform/cachekeepalive/manager.go` | `raw_goroutines=1` |
| `internal/platform/db/tx.go` | `panic_count=1` |
| `internal/platform/hooks/dispatch_worker.go` | `raw_goroutines=1` |
| `internal/platform/hooks/dispatcher.go` | `raw_goroutines=1` |
| `internal/platform/mcpcontrol/config_fanout_worker.go` | `raw_goroutines=1` |
| `internal/platform/mcpcontrol/factory.go` | `raw_goroutines=1` |
| `internal/platform/observability/record_error.go` | `global_vars=1` |
| `internal/platform/rpc/approval.go` | `raw_goroutines=1` |
| `internal/platform/rpc/push.go` | `global_vars=1` |
| `internal/platform/rpc/push_worker.go` | `raw_goroutines=1` |
| `internal/platform/rpc/server.go` | `panic_count=1` |
| `internal/platform/runtimesafe/safego.go` | `raw_goroutines=1` |
| `internal/platform/shared/safe_go.go` | `raw_goroutines=1` |
| `internal/platform/toolbridge/handler_host_tools.go` | `raw_goroutines=1` |
| `internal/platform/toolbridge/proxy_runner.go` | `raw_goroutines=1` |

Unique remediation:

- [ ] Standardize platform goroutines on a single lifecycle helper per package boundary, preserving current logging and cancellation semantics.
- [ ] Convert DB/RPC panic sites to returned errors with explicit startup/request boundary handling.
- [ ] Replace observability and RPC push globals with injected recorder/push registry instances.

Validation:

```bash
./scripts/test_with_guard.sh \
  internal/mcpserver/common/bootstrap/client.go \
  internal/mcpserver/common/bootstrap/heartbeat.go \
  internal/mcpserver/common/bootstrap/lifecycle.go \
  internal/mcpserver/common/bootstrap/reconnect.go \
  internal/mcpserver/common/http_transport.go \
  internal/mcpserver/common/server.go \
  internal/platform/cachekeepalive/manager.go \
  internal/platform/db/tx.go \
  internal/platform/hooks/dispatch_worker.go \
  internal/platform/hooks/dispatcher.go \
  internal/platform/mcpcontrol/config_fanout_worker.go \
  internal/platform/mcpcontrol/factory.go \
  internal/platform/observability/record_error.go \
  internal/platform/rpc/approval.go \
  internal/platform/rpc/push.go \
  internal/platform/rpc/push_worker.go \
  internal/platform/rpc/server.go \
  internal/platform/runtimesafe/safego.go \
  internal/platform/shared/safe_go.go \
  internal/platform/toolbridge/handler_host_tools.go \
  internal/platform/toolbridge/proxy_runner.go
./scripts/test_with_guard.sh ./internal/mcpserver/common/... ./internal/platform/cachekeepalive ./internal/platform/db ./internal/platform/hooks ./internal/platform/mcpcontrol ./internal/platform/observability ./internal/platform/rpc ./internal/platform/runtimesafe ./internal/platform/shared ./internal/platform/toolbridge -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-05 platform-mcpserver 违规"
```

## PG-06 provider

Worktree: `.worktrees/20260705-prodguard-pg06-provider`

Branch: `codex/20260705-prodguard-pg06-provider`

Owned files and violations:

| File | Violations |
| --- | --- |
| `internal/provider/claudecli/session.go` | `max_struct_methods=12` |
| `internal/provider/claudecli/session_log_watcher.go` | `raw_goroutines=1` |
| `internal/provider/claudecli/session_log_watcher_integration.go` | `raw_goroutines=3` |
| `internal/provider/claudecli/transport.go` | `raw_goroutines=1` |
| `internal/provider/codexapp/peer_supervisor.go` | `raw_goroutines=3` |
| `internal/provider/codexapp/pool_spawner.go` | `raw_goroutines=2` |
| `internal/provider/codexapp/server_pool.go` | `raw_goroutines=1` |
| `internal/provider/codexapp/session.go` | `max_struct_methods=13` |
| `internal/provider/codexapp/session_runtime.go` | `raw_goroutines=3` |
| `internal/provider/codexapp/transport_process.go` | `raw_goroutines=3` |

Unique remediation:

- [ ] Split provider session receivers into runtime, transport, log watching, and lifecycle collaborators.
- [ ] Route all provider background loops through provider-owned supervisor with context cancellation and wait on shutdown.
- [ ] Preserve provider process contract: no silent fallback when child process, socket, or log stream fails.

Validation:

```bash
./scripts/test_with_guard.sh \
  internal/provider/claudecli/session.go \
  internal/provider/claudecli/session_log_watcher.go \
  internal/provider/claudecli/session_log_watcher_integration.go \
  internal/provider/claudecli/transport.go \
  internal/provider/codexapp/peer_supervisor.go \
  internal/provider/codexapp/pool_spawner.go \
  internal/provider/codexapp/server_pool.go \
  internal/provider/codexapp/session.go \
  internal/provider/codexapp/session_runtime.go \
  internal/provider/codexapp/transport_process.go
./scripts/test_with_guard.sh ./internal/provider/claudecli ./internal/provider/codexapp -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-06 provider 违规"
```

## PG-07 store

Worktree: `.worktrees/20260705-prodguard-pg07-store`

Branch: `codex/20260705-prodguard-pg07-store`

Owned files and violations:

| File | Violations |
| --- | --- |
| `internal/store/binding/store.go` | `max_struct_methods=15` |
| `internal/store/cron/store.go` | `max_struct_methods=23` |
| `internal/store/datasourcev2/store.go` | `max_struct_methods=11` |
| `internal/store/feedback/store.go` | `panic_count=1` |
| `internal/store/insight/store.go` | `panic_count=1` |
| `internal/store/mcpserver/store.go` | `global_vars=1` |
| `internal/store/prompt/intent_drafts.go` | `panic_count=1` |
| `internal/store/prompt/store.go` | `max_struct_methods=15` |
| `internal/store/thread/store.go` | `max_struct_methods=21` |

Unique remediation:

- [ ] Split store receivers into query, command, migration/maintenance, and transaction collaborators without changing SQL contract.
- [ ] Convert store panics to returned errors; callers must already handle store construction/query errors or be updated in the same package boundary.
- [ ] Replace mcpserver store global with explicit store registry/config owned by constructor.

Validation:

```bash
./scripts/test_with_guard.sh \
  internal/store/binding/store.go \
  internal/store/cron/store.go \
  internal/store/datasourcev2/store.go \
  internal/store/feedback/store.go \
  internal/store/insight/store.go \
  internal/store/mcpserver/store.go \
  internal/store/prompt/intent_drafts.go \
  internal/store/prompt/store.go \
  internal/store/thread/store.go
./scripts/test_with_guard.sh ./internal/store/binding ./internal/store/cron ./internal/store/datasourcev2 ./internal/store/feedback ./internal/store/insight ./internal/store/mcpserver ./internal/store/prompt ./internal/store/thread -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-07 store 违规"
```

## PG-08 pkg

Worktree: `.worktrees/20260705-prodguard-pg08-pkg`

Branch: `codex/20260705-prodguard-pg08-pkg`

Owned files and violations:

| File | Violations |
| --- | --- |
| `pkg/dagmetrics/dagmetrics.go` | `global_vars=1` |
| `pkg/logger/agent_logger.go` | `global_vars=1` |
| `pkg/logger/logger.go` | `global_vars=13`, `has_init=1` |
| `pkg/logger/relay.go` | `has_init=1` |
| `pkg/logger/safego.go` | `raw_goroutines=1` |
| `pkg/logger/watchdog.go` | `global_vars=1` |

Unique remediation:

- [ ] Replace logger package globals and init registration with explicit logger runtime/config created at program bootstrap.
- [ ] Keep package-level pure constants only; mutable writers, relays, watchdog state, and metrics registries must live on instances.
- [ ] Bind logger safe goroutine to logger runtime lifecycle.

Validation:

```bash
./scripts/test_with_guard.sh \
  pkg/dagmetrics/dagmetrics.go \
  pkg/logger/agent_logger.go \
  pkg/logger/logger.go \
  pkg/logger/relay.go \
  pkg/logger/safego.go \
  pkg/logger/watchdog.go
./scripts/test_with_guard.sh ./pkg/dagmetrics ./pkg/logger -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-08 pkg 违规"
```

## PG-09 internal-other

Worktree: `.worktrees/20260705-prodguard-pg09-internal-other`

Branch: `codex/20260705-prodguard-pg09-internal-other`

Owned files and violations:

| File | Violations |
| --- | --- |
| `internal/app/runner.go` | `panic_count=1` |
| `internal/contract/contracttest/section_invalidator.go` | `naked_goroutines=1`, `raw_goroutines=1` |
| `internal/devtools/sqlitepackagesmoke/main.go` | `global_vars=1` |
| `internal/devtools/sqlitereleasegate/runner.go` | `global_vars=1`, `panic_count=1` |

Unique remediation:

- [ ] Convert app/devtools panics to returned errors or explicit CLI fatal at `main` boundary.
- [ ] Move devtools globals into command/run config structs.
- [ ] Bind contracttest invalidator goroutine to test context and waitgroup.

Validation:

```bash
./scripts/test_with_guard.sh \
  internal/app/runner.go \
  internal/contract/contracttest/section_invalidator.go \
  internal/devtools/sqlitepackagesmoke/main.go \
  internal/devtools/sqlitereleasegate/runner.go
./scripts/test_with_guard.sh ./internal/app ./internal/contract/contracttest ./internal/devtools/sqlitepackagesmoke ./internal/devtools/sqlitereleasegate -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-09 internal-other 违规"
```

## PG-10 misc

Worktree: `.worktrees/20260705-prodguard-pg10-misc`

Branch: `codex/20260705-prodguard-pg10-misc`

Owned files and violations:

| File | Violations |
| --- | --- |
| `internal/testutil/golden/orchestration_stub.go` | `max_struct_methods=26` |
| `internal/ui/wails/assets.go` | `panic_count=1` |
| `internal/ui/wails/http_server.go` | `raw_goroutines=1` |
| `internal/ui/wails/lifecycle.go` | `raw_goroutines=2` |
| `internal/ui/wails/module.go` | `raw_goroutines=1` |
| `internal/util/safego/safego.go` | `raw_goroutines=1` |

Unique remediation:

- [ ] Split golden orchestration stub into focused fake collaborators while keeping tests API stable through explicit composition.
- [ ] Convert Wails asset panic to returned startup error.
- [ ] Route Wails and util/safego goroutines through context-aware lifecycle helpers that preserve current panic/error logging behavior.

Validation:

```bash
./scripts/test_with_guard.sh \
  internal/testutil/golden/orchestration_stub.go \
  internal/ui/wails/assets.go \
  internal/ui/wails/http_server.go \
  internal/ui/wails/lifecycle.go \
  internal/ui/wails/module.go \
  internal/util/safego/safego.go
./scripts/test_with_guard.sh ./internal/testutil/golden ./internal/ui/wails ./internal/util/safego -count=1
git restore --staged --worktree -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
git commit -m "chore: 消除生产守卫 PG-10 misc 违规"
```

## Integration Controller

集成控制器使用独立集成 worktree，按车道回传 commit 逐个合并，不在车道内做跨域修复。

```bash
BASE_COMMIT=0a5cc09b0
git worktree add .worktrees/20260705-prodguard-integration -b codex/20260705-prodguard-integration "$BASE_COMMIT"
```

Integration steps:

- [ ] 确认 10 个车道都有 commit hash、验证摘要和 LSP 证据摘要。
- [ ] 在集成 worktree 逐个 `git merge --no-ff` 车道分支。
- [ ] 对 baseline 冲突采用重新生成策略，不手工拼接旧 baseline。
- [ ] 运行 `go run ./scripts/code_size_guard.go --freeze`，确认生产 baseline 自动收缩到真实剩余值。
- [ ] 运行完整验证。
- [ ] 只在全部验证通过后合并到主分支。

Final verification commands:

```bash
go run ./scripts/code_size_guard.go --freeze
./scripts/test_with_guard.sh --guard-only
./scripts/test_with_guard.sh ./... -count=1
git diff --check
```

Acceptance criteria:

- [ ] `internal/archtest/baseline.json` 不再包含本计划列出的 100 个生产冻结文件，或只保留由当前守卫确认为非本计划范围的记录型指标。
- [ ] `missing_docs` 仍不参与生产或测试守卫失败。
- [ ] `raw_goroutines`、`naked_goroutines`、`panic_count`、`global_vars`、`has_init`、`max_struct_methods` 不再导致生产文件冻结。
- [ ] 没有新增守卫 ignore、没有扩大阈值、没有删除生产行为测试。
- [ ] 所有车道提交可独立审查，集成提交只包含合并结果和最终 baseline 收缩。
