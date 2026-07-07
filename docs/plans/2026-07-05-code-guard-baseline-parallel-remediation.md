# Code Guard Baseline Parallel Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 分阶段修复当前代码守卫 baseline 冻结债务，优先清理低风险、可并行的 docs/shape 类违规，并避免用重新 freeze 或放宽规则掩盖真实问题。

**Architecture:** 本计划把 `internal/archtest/baseline.json` 与 `internal/archtest/baseline_test.json` 的冻结项按规则字段、风险等级和写集边界拆分为 20 个并行 lane。第一阶段只处理容易验证且行为风险低的项；第二阶段处理测试 helper 结构与低风险测试复杂度；第三阶段只审查并发、panic、global var 等高风险债务，生成更小修复计划后再实施。

**Tech Stack:** Go 1.25.7, `internal/archtest` code-size guard, `scripts/code_size_guard.go`, repo-local LSP toolchain, `./scripts/test_with_guard.sh`, Git worktrees.

**Verification Surface:** Implementation lanes run the matching file-level `./scripts/test_with_guard.sh <file.go>` or package-level `./scripts/test_with_guard.sh <packages> -count=1`; review-only lanes A14-A17 must produce selector output, LSP evidence, diagnostics status, and `[no tests to run: review-only]`. All baseline changes must run `go run scripts/code_size_guard.go --freeze` and then `./scripts/test_with_guard.sh ./internal/archtest -count=1`; integration must run `git diff --check`.

---

## Current Baseline Evidence

本计划基于 2026-07-05 重新 freeze 后的当前文件:

- `internal/archtest/baseline.json`: 生产冻结 189 个文件。
- `internal/archtest/baseline_test.json`: 测试冻结 142 个文件。
- 测试文件 `missing_docs` 已不参与冻结；`baseline_test.json` 中 `missing_docs` 条目为 0。
- 生产 `missing_docs` 仍参与冻结，避免导出生产 API 缺注释。

重新统计命令:

```bash
jq -n --slurpfile prod internal/archtest/baseline.json --slurpfile test internal/archtest/baseline_test.json '
  def reason_counts($obj):
    reduce ($obj[0] | to_entries[]) as $e ({};
      .missing_docs += (if ($e.value.missing_docs // 0) > 0 then 1 else 0 end) |
      .raw_goroutines += (if ($e.value.raw_goroutines // 0) > 0 then 1 else 0 end) |
      .naked_goroutines += (if ($e.value.naked_goroutines // 0) > 0 then 1 else 0 end) |
      .max_struct_methods += (if ($e.value.max_struct_methods // 0) > 10 then 1 else 0 end) |
      .global_vars += (if ($e.value.global_vars // 0) > 0 then 1 else 0 end) |
      .panic_count += (if ($e.value.panic_count // 0) > 0 then 1 else 0 end) |
      .max_complexity += (if ($e.value.max_complexity // 0) > 10 then 1 else 0 end) |
      .empty_funcs += (if ($e.value.empty_funcs // 0) > 0 then 1 else 0 end) |
      .todo_count += (if ($e.value.todo_count // 0) > 0 then 1 else 0 end) |
      .has_init += (if ($e.value.has_init // false) then 1 else 0 end) |
      .naked_returns += (if ($e.value.naked_returns // 0) > 0 then 1 else 0 end) |
      .max_func_len += (if ($e.value.max_func_len // 0) > 80 then 1 else 0 end)
    );
  {entries:{prod:($prod[0]|length), test:($test[0]|length)}, prod:reason_counts($prod), test:reason_counts($test)}'
```

Current result:

| Reason | Production | Test |
|---|---:|---:|
| `missing_docs` | 83 | 0 |
| `raw_goroutines` | 56 | 106 |
| `naked_goroutines` | 13 | 103 |
| `max_struct_methods` | 21 | 29 |
| `global_vars` | 14 | 0 |
| `panic_count` | 11 | 1 |
| `max_complexity` | 0 | 7 |
| `empty_funcs` | 4 | 1 |
| `todo_count` | 4 | 0 |
| `has_init` | 2 | 1 |
| `naked_returns` | 1 | 0 |
| `max_func_len` | 0 | 1 |

## Priority Model

| Priority | Scope | Why |
|---|---|---|
| P0 | `missing_docs` only in production | 80 files, behavior-neutral, mostly adding maintainer-facing comments. |
| P1 | `empty_funcs`, `todo_count`, `naked_returns` only | 9 production files, usually local and easy to verify, but still inspect intent before editing. |
| P1 | test `max_complexity` only | 6 files, usually refactor test helpers or table cases. |
| P2 | test `max_struct_methods` only | 29 files, likely helper receiver consolidation; mechanical but can touch many tests. |
| P2 | production `max_struct_methods` only | 21 files, may imply real API/receiver shape decisions. Review before editing. |
| P3 | `raw_goroutines` / `naked_goroutines` | High count and high semantic risk; must not be mechanically wrapped. |
| P3 | `global_vars`, `panic_count`, `has_init` | Often fail-fast or lifecycle related; review-first, then narrower plans. |

## Global Worktree And Dispatch Rules

- [ ] Controller must not execute these lanes in the dirty main worktree.
- [ ] Before creating lane worktrees, the controller must create a clean handoff point that already contains the current guard-rule change, refreshed baselines, and this plan. Do not fork lanes from a branch whose `HEAD` still has the old test baseline. Current required handoff counts are production `189` and test `142`; if a lane sees production `189` and test `1163`, it is on the stale pre-handoff baseline and must stop.
- [ ] Build the handoff branch with a narrow staged set:

```bash
handoff_branch="codex/20260705-guard-baseline-handoff"
if git show-ref --verify --quiet "refs/heads/$handoff_branch"; then
  git switch "$handoff_branch"
else
  git switch -c "$handoff_branch"
fi

git add internal/archtest/baseline.json \
  internal/archtest/baseline_test.json \
  internal/archtest/guardlib.go \
  internal/archtest/ratchet.go \
  internal/archtest/ratchet_test.go \
  docs/plans/2026-07-05-code-guard-baseline-parallel-remediation.md

./scripts/test_with_guard.sh --guard-only
jq -r 'length' internal/archtest/baseline.json internal/archtest/baseline_test.json
git diff --cached --check
git status --short
git commit -m "chore: prepare code guard baseline handoff"
```

Expected before worker dispatch: the handoff branch commit contains only the guard-rule change, refreshed baselines, the Hint cleanup, and this plan; `jq` prints `189` then `142`. If the controller cannot create a commit, export an equivalent patch bundle and require every lane worktree to apply it before preflight.
- [ ] Create one worktree per lane from the clean handoff branch:

```bash
handoff_branch="codex/20260705-guard-baseline-handoff"
git worktree add ".worktrees/20260705-guard-a01-docs-cmd-lsp" -b "codex/20260705-guard-a01-docs-cmd-lsp" "$handoff_branch"
```

- [ ] Each worker must run this preflight:

```bash
git status --short
jq 'length' internal/archtest/baseline.json internal/archtest/baseline_test.json
```

Expected: clean lane worktree; baseline counts match controller handoff unless the controller explicitly hands over a newer integration baseline.

- [ ] Workers must not edit `internal/archtest/baseline*.json` directly. After source edits, run:

```bash
go run scripts/code_size_guard.go --freeze
./scripts/test_with_guard.sh ./internal/archtest -count=1
git diff --check
```

- [ ] If the worker touches only production `missing_docs`, the source change is comments only. Do not rename symbols, move files, change exports, or alter behavior.
- [ ] If `./scripts/test_with_guard.sh ./internal/archtest -count=1` rewrites unrelated baseline entries because another lane changed the base, stop and report `NEEDS_REBASE`.
- [ ] Any lane encountering LSP Error, Warning, Information, or Hint in its touched files must either fix it or report a blocker with file, line, rule, and reason.

## Stable Lane Selectors

Workers use these selectors inside their lane worktree to derive their exact file set from the current baseline.

Production `missing_docs` only:

```bash
jq -r 'to_entries[]
  | select((.value.missing_docs // 0)>0)
  | select((.value.raw_goroutines // 0)==0 and (.value.naked_goroutines // 0)==0)
  | select((.value.max_struct_methods // 0)<=10 and (.value.global_vars // 0)==0)
  | select((.value.panic_count // 0)==0 and (.value.empty_funcs // 0)==0)
  | select((.value.todo_count // 0)==0 and ((.value.has_init // false)|not))
  | select((.value.naked_returns // 0)==0)
  | .key' internal/archtest/baseline.json
```

Production low-risk shape debt:

```bash
jq -r 'to_entries[]
  | select((.value.empty_funcs // 0)>0 or (.value.todo_count // 0)>0 or (.value.naked_returns // 0)>0)
  | .key' internal/archtest/baseline.json
```

Test `max_complexity` only:

```bash
jq -r 'to_entries[]
  | select((.value.max_complexity // 0)>10 and (.value.max_func_len // 0)<=80)
  | select((.value.raw_goroutines // 0)==0 and (.value.naked_goroutines // 0)==0)
  | select((.value.panic_count // 0)==0 and (.value.empty_funcs // 0)==0)
  | select(((.value.has_init // false)|not))
  | .key' internal/archtest/baseline_test.json
```

Test `max_struct_methods` only:

```bash
jq -r 'to_entries[]
  | select((.value.max_struct_methods // 0)>10)
  | select((.value.raw_goroutines // 0)==0 and (.value.naked_goroutines // 0)==0)
  | select((.value.panic_count // 0)==0 and (.value.max_complexity // 0)<=10)
  | .key' internal/archtest/baseline_test.json
```

High-risk concurrency debt:

```bash
jq -r 'to_entries[]
  | select((.value.raw_goroutines // 0)>0 or (.value.naked_goroutines // 0)>0)
  | .key' internal/archtest/baseline.json internal/archtest/baseline_test.json
```

## Parallel Dispatch Matrix

### Phase 1: Behavior-Neutral Or Low-Risk Fixes

#### A01: Production Missing Docs - `cmd/mcp-lsp/**`

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^cmd/mcp-lsp/'
```

**Expected seed files:** `cmd/mcp-lsp/edit/seeksequence.go`, `cmd/mcp-lsp/format/funcrange.go`, `cmd/mcp-lsp/installer/installer.go`, `cmd/mcp-lsp/multilsp/adapter.go`, `cmd/mcp-lsp/multilsp/client.go`, `cmd/mcp-lsp/multilsp/go_root_resolver.go`.

**Worker goal:** Add concise Chinese doc comments to exported functions/types only. Keep public names and behavior unchanged.

**Verify:**

```bash
gofmt -w $(jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^cmd/mcp-lsp/')
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... -count=1
go run scripts/code_size_guard.go --freeze
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

#### A02: Production Missing Docs - `cmd/mcp-orch/orchestration/**`

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^cmd/mcp-orch/orchestration/'
```

**Worker goal:** Add comments describing orchestration state, DAG, nodeexec, processctl, and metrics boundaries. Do not alter scheduler or state transitions.

**Verify:** `./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration/... -count=1`

#### A03: Production Missing Docs - `cmd/mcp-orch/tools/**` and `cmd/mcp-orch/store/**`

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^cmd/mcp-orch/(tools|store)/'
```

**Worker goal:** Add comments for tool handler and workspace store contracts. Do not change JSON-RPC schemas.

**Verify:** `./scripts/test_with_guard.sh ./cmd/mcp-orch/tools/... ./cmd/mcp-orch/store/... -count=1`

#### A04: Production Missing Docs - `internal/module/memory/**`

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^internal/module/memory/'
```

**Worker goal:** Add comments around memory gate, retrieval, rules, UI RPC, and provider handoff boundaries. Use Chinese comments that explain persistence, root, or provider-visible behavior.

**Verify:** `./scripts/test_with_guard.sh ./internal/module/memory/... -count=1`

#### A05: Production Missing Docs - `internal/module/{skill,thread,threadprompt,turn,uistate,dashboard,feedback}/**`

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^internal/module/(skill|thread|threadprompt|turn|uistate|dashboard|feedback)/'
```

**Worker goal:** Add comments to exported module contracts and RPC/service entrypoints. Do not widen module contracts or change DTOs.

**Verify:** `./scripts/test_with_guard.sh ./internal/module/skill/... ./internal/module/thread/... ./internal/module/threadprompt/... ./internal/module/turn/... ./internal/module/uistate/... ./internal/module/dashboard/... ./internal/module/feedback/... -count=1`

#### A06: Production Missing Docs - `internal/provider/**`

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^internal/provider/'
```

**Worker goal:** Add comments explaining provider-facing contracts, session history, transport helpers, manifest, and shared client boundaries. Do not touch provider process behavior.

**Verify:** `./scripts/test_with_guard.sh ./internal/provider/... -count=1`

#### A07: Production Missing Docs - `internal/{archtest,contract,platform,store}/**`

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.missing_docs // 0)>0) | .key' internal/archtest/baseline.json | rg '^internal/(archtest|contract|platform|store)/'
```

**Worker goal:** Add comments for guard helpers, contract types, platform helpers, and store contracts. Do not change guard thresholds or store interfaces.

**Verify:** `./scripts/test_with_guard.sh ./internal/archtest ./internal/contract/... ./internal/platform/... ./internal/store/... -count=1`

**Coverage note:** A01-A07 cover 82 of the 83 production `missing_docs` files. `pkg/logger/logger.go` is intentionally excluded because it also has `global_vars` and `has_init`; A15 owns that review-first path.

#### A08: Production Low-Risk Shape Debt

**Exact current files:**

```text
cmd/mcp-lsp/internal/hiddenexec/process_default.go
cmd/super-dolphin-updater/detach_default.go
internal/archtest/silent_fallback_guard.go
internal/dto/mcp/errors.go
internal/module/datasource/service.go
internal/module/turn/rpc_helpers.go
internal/module/turn/service.go
internal/platform/rlimit/rlimit_windows.go
internal/platform/rpc/transport_ws.go
```

**Worker goal:** Fix only `empty_funcs`, `todo_count`, and `naked_returns`. `empty_funcs` is based on `len(fd.Body.List) == 0`, so comments do not satisfy the guard. For platform stubs, add a behavior-neutral statement only when the package contract already makes that behavior correct; otherwise return `NEEDS_APPROVAL` instead of adding dummy behavior.

**Verify:**

```bash
./scripts/test_with_guard.sh cmd/mcp-lsp/internal/hiddenexec/process_default.go cmd/super-dolphin-updater/detach_default.go internal/archtest/silent_fallback_guard.go internal/dto/mcp/errors.go internal/module/datasource/service.go internal/module/turn/rpc_helpers.go internal/module/turn/service.go internal/platform/rlimit/rlimit_windows.go internal/platform/rpc/transport_ws.go
go run scripts/code_size_guard.go --freeze
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

### Phase 2: Test-Only Refactors

#### A09: Test `max_complexity` Only

**Exact current files:**

```text
internal/store/agentstatus/sqlite_integration_test.go
internal/store/ailog/store_test.go
internal/store/interaction/store_test.go
internal/store/uipreference/sqlite_integration_test.go
pkg/dreammetrics/dreammetrics_test.go
pkg/skillmetrics/skillmetrics_test.go
```

**Worker goal:** Reduce cyclomatic complexity by splitting table validation helpers or subtests. Keep assertions and failure messages equally strict.

**Verify:** `./scripts/test_with_guard.sh ./internal/store/agentstatus ./internal/store/ailog ./internal/store/interaction ./internal/store/uipreference ./pkg/dreammetrics ./pkg/skillmetrics -count=1`

#### A10: Test `max_struct_methods` - MCP-LSP Helpers

**Exact current files:**

```text
cmd/mcp-lsp/manager/registry_diagnostics_test.go
cmd/mcp-lsp/tools/tool_multi_agent_scope_test.go
cmd/mcp-lsp/tools/tool_structure_test.go
```

**Worker goal:** Split large fake/helper receiver types into smaller helper structs or plain functions. Do not weaken assertions.

**Verify:** `./scripts/test_with_guard.sh ./cmd/mcp-lsp/manager ./cmd/mcp-lsp/tools -count=1`

#### A11: Test `max_struct_methods` - MCP-Orch Helpers

**Exact current files:**

```text
cmd/mcp-orch/orchestration/dag_ops_add_node_test.go
cmd/mcp-orch/orchestration/dag_start_test.go
cmd/mcp-orch/orchestration/wakeup_dispatcher_test_helpers_test.go
cmd/mcp-orch/store/taskdag/test_helpers_test.go
```

**Worker goal:** Split DAG/test helper receiver methods by fixture role. Preserve SQLite/taskdag integration semantics.

**Verify:** `./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag -count=1`

#### A12: Test `max_struct_methods` - Module Helpers

**Exact current files:**

```text
internal/module/cron/scheduler_test.go
internal/module/cron/turn_adapter_test.go
internal/module/dashboard/detail_test.go
internal/module/datasource_v2/prompt_provider_test.go
internal/module/datasource_v2/rpc_test.go
internal/module/memory/context_provider_turn_test.go
internal/module/prompt/service_provider_test.go
internal/module/thread/events_test.go
internal/module/thread/history_test.go
internal/module/thread/resume_shard18_support_test.go
internal/module/thread/resume_shard18_test.go
internal/module/thread/service_handlers_test.go
internal/module/thread/stop_shard16_support_test.go
internal/module/turn/test_msg/send_text_test.go
```

**Worker goal:** Split helper receivers by module boundary. Keep fixture setup local to the same package.

**Verify:** `./scripts/test_with_guard.sh ./internal/module/cron ./internal/module/dashboard ./internal/module/datasource_v2 ./internal/module/memory ./internal/module/prompt ./internal/module/thread ./internal/module/turn/test_msg -count=1`

#### A13: Test `max_struct_methods` - Platform/Store/Provider Helpers

**Exact current files:**

```text
internal/platform/rpc/handler_test.go
internal/platform/toolbridge/handler_runtime_test.go
internal/provider/unified/contract_test.go
internal/store/binding/store_shard12_helpers_test.go
internal/store/cron/store_test.go
internal/store/hookstore/hookstore_helpers_test.go
internal/store/prompt/store_test.go
internal/store/thread/store_test.go
```

**Worker goal:** Split large fake receiver types into focused helper structs or helper functions. Avoid changing production store contracts.

**Verify:** `./scripts/test_with_guard.sh ./internal/platform/rpc ./internal/platform/toolbridge ./internal/provider/unified ./internal/store/binding ./internal/store/cron ./internal/store/hookstore ./internal/store/prompt ./internal/store/thread -count=1`

### Phase 3: Review-First High-Risk Work

A14-A17 are review-only lanes. They must not edit source or baseline files. Each lane returns a review table plus this evidence block:

```text
selector: <exact command and output count>
lsp: locate=<grep or structure evidence>, understand=<inspect evidence>, impact=<references or call_hierarchy evidence>, read=<file read evidence>, diagnostics=<clean or blocker list>
git: git status --short output from the lane worktree
tests: [no tests to run: review-only]
next: concrete implementation lane proposal or [no implementation recommended]
```

#### A14: Production `max_struct_methods` Review

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.max_struct_methods // 0)>10) | .key' internal/archtest/baseline.json
```

**Worker goal:** Produce a review table only: path, receiver name, method count, whether split is behavior-neutral, candidate package/test command. Do not edit source in this lane.

#### A15: Production `global_vars` Review

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.global_vars // 0)>0) | .key' internal/archtest/baseline.json
```

**Worker goal:** Classify each global as mutable state, immutable config, sentinel-like value, or test/dev-only main. Do not replace globals mechanically.

**Required focus:** Include `pkg/logger/logger.go` in the review table. It is the one production `missing_docs` file outside A01-A07 and also carries `global_vars=13` plus `has_init=true`, so it needs a review-first owner decision before comment cleanup.

#### A16: Production `panic_count` Review

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.panic_count // 0)>0) | .key' internal/archtest/baseline.json
```

**Worker goal:** Classify each panic as startup fail-fast, impossible invariant, test helper, or real runtime crash. Only real runtime crashes become follow-up repair tasks.

#### A17: Production Goroutine Review

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.raw_goroutines // 0)>0 or (.value.naked_goroutines // 0)>0) | .key' internal/archtest/baseline.json
```

**Worker goal:** Identify owner lifecycle for each goroutine. Preferred outcomes are `rungroup`, `runtimesafe.SafeGo`, existing actor/worker, or documented false positive. Do not wrap blindly.

#### A18: Test Goroutine Repair - MCP-LSP / IDA

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.raw_goroutines // 0)>0 or (.value.naked_goroutines // 0)>0) | .key' internal/archtest/baseline_test.json | rg '^cmd/mcp-(lsp|ida)/'
```

**Worker goal:** Replace unowned test goroutines with helper-managed contexts, `t.Cleanup`, channels with timeout, or existing test runner helpers. Preserve concurrency assertions.

**Verify:** `./scripts/test_with_guard.sh ./cmd/mcp-lsp/... ./cmd/mcp-ida/... -count=1`

#### A19: Test Goroutine Repair - MCP-Orch

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.raw_goroutines // 0)>0 or (.value.naked_goroutines // 0)>0) | .key' internal/archtest/baseline_test.json | rg '^cmd/mcp-orch/'
```

**Worker goal:** Scope test goroutines to DAG/orchestration lifecycle helpers. No sleep-only stabilization; use observable completion or cancellation.

**Verify:** `./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1`

#### A20: Test Goroutine Repair - Internal Modules, Platform, Store, Provider

**Selector:**

```bash
jq -r 'to_entries[] | select((.value.raw_goroutines // 0)>0 or (.value.naked_goroutines // 0)>0) | .key' internal/archtest/baseline_test.json | rg '^(internal|pkg)/'
```

**Worker goal:** Use package-local cancellation helpers, `t.Cleanup`, or existing runner abstractions. If the goroutine models production actor behavior, return `NEEDS_APPROVAL` with owner package and suggested focused plan.

**Verify:** Run package-specific `./scripts/test_with_guard.sh <affected packages> -count=1`; do not run only `go test`.

## Integration Plan

- [ ] Integrate A01-A08 first. These should produce the largest safe drop in production baseline.
- [ ] After each successful lane:

```bash
go run scripts/code_size_guard.go --freeze
./scripts/test_with_guard.sh ./internal/archtest -count=1
jq 'length' internal/archtest/baseline.json internal/archtest/baseline_test.json
git diff --check
```

- [ ] Only after A01-A08 are merged, dispatch A09-A13.
- [ ] A14-A17 are review lanes. They produce follow-up implementation plans, not source changes.
- [ ] A18-A20 may be implemented after A09-A13, but only if each worker can name the test goroutine owner and cancellation signal.

## Expected First-Phase Outcome

If A01-A08 are cleanly implemented:

- Production baseline should drop by roughly 89 entries: 80 pure `missing_docs` entries plus 9 low-risk shape entries. Two files touched by A01-A07 still have goroutine debt and should remain frozen until A17; `pkg/logger/logger.go` remains under A15 until its global/init ownership is decided.
- Test baseline should remain 142 until Phase 2 begins.
- No production behavior should change except removing empty/stub/deferred-comment/naked-return debt where each file is explicitly validated.

## Final Gate

Before declaring the campaign complete:

```bash
go run scripts/code_size_guard.go --freeze
./scripts/test_with_guard.sh ./internal/archtest -count=1
make guard
git diff --check
git status --short
```

Completion evidence must include:

- Final production and test baseline counts.
- Per-lane file changes and verification commands.
- Remaining high-risk debt, with owner and next plan.
- Any LSP diagnostics that could not be fixed, with blocker details.
