# Test Code Guard Baseline Zero Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clear every current test-file code-guard freeze so `internal/archtest/baseline_test.json` becomes an empty JSON object.

**Architecture:** Treat this as test-only debt removal. Worker lanes modify only owned `_test.go` files and package-local test helpers, never production code and never `internal/archtest/baseline_test.json`; the integration controller runs the final freeze once all lanes pass. The dominant debt is raw test goroutine launching, so lanes must replace naked `go` statements with `sync.WaitGroup.Go` or a package-local `testGoroutineGroup` helper registered with `t.Cleanup`.

**Tech Stack:** Go 1.25.7, `sync.WaitGroup.Go`, repo `./scripts/test_with_guard.sh`, `scripts/code_size_guard.go`, `internal/archtest` baseline ratchet.

**Verification Surface:** Per-file `./scripts/test_with_guard.sh <file.go>`, affected package `./scripts/test_with_guard.sh <package> -count=1`, final `go run scripts/code_size_guard.go --freeze`, `jq 'length' internal/archtest/baseline_test.json`, `make guard`, and `git diff --check`.

---

## Current Evidence Snapshot

- Current production baseline is already clear: `jq 'length' internal/archtest/baseline.json` returns `0`.
- Current test baseline count is `81`: `jq 'length' internal/archtest/baseline_test.json`.
- Actual current test violations by guard rule:
  - `raw_goroutines`: 77 files.
  - `naked_goroutines`: 75 files.
  - `max_struct_methods`: 2 files.
  - `has_init`: 1 file.
  - `panic_count`: 1 file.
  - `empty_funcs`: 1 file.
- Non-goroutine files:
  - `cmd/mcp-lsp/tools_call_plain_text_test.go`: `has_init`.
  - `cmd/mcp-orch/store/taskdag/test_helpers_dispatch_guard_test.go`: `panic_count`.
  - `internal/platform/runner/contract_test.go`: `empty_funcs`, plus goroutine debt.
  - `internal/store/binding/store_shard12_helpers_test.go`: `max_struct_methods`.
  - `internal/store/prompt/store_test.go`: `max_struct_methods`.

## Required Fix Patterns

Use package-local helpers for repeated goroutine launches:

```go
type testGoroutineGroup struct {
	wg sync.WaitGroup
}

func newTestGoroutineGroup(t *testing.T) *testGoroutineGroup {
	t.Helper()
	group := &testGoroutineGroup{}
	t.Cleanup(group.Wait)
	return group
}

func (g *testGoroutineGroup) Go(fn func()) {
	g.wg.Go(fn)
}

func (g *testGoroutineGroup) Wait() {
	g.wg.Wait()
}
```

Use one-off `WaitGroup.Go` only when a helper would be used once in that package:

```go
var wg sync.WaitGroup
t.Cleanup(wg.Wait)
wg.Go(func() {
	done <- run(ctx)
})
```

For goroutines that are intentionally blocked until cancellation, the cleanup order must cancel or close first and wait second:

```go
ctx, cancel := context.WithCancel(context.Background())
done := make(chan struct{})
var wg sync.WaitGroup
t.Cleanup(func() {
	cancel()
	wg.Wait()
})
wg.Go(func() {
	defer close(done)
	run(ctx)
})
```

For process waiters, preserve timeout-and-kill behavior while moving the goroutine under the group:

```go
done := make(chan error, 1)
group := newTestGoroutineGroup(t)
group.Go(func() { done <- cmd.Wait() })
select {
case err := <-done:
	if err != nil {
		t.Logf("process exited with %v", err)
	}
case <-time.After(5 * time.Second):
	_ = cmd.Process.Kill()
}
```

For `func init` in tests, move registration into a helper with cleanup:

```go
func registerPlainTextRendererForTest(t *testing.T) {
	t.Helper()
	common.RegisterToolResultPlainTextRenderer(lsptools.FormatToPlainText)
	t.Cleanup(func() { common.RegisterToolResultPlainTextRenderer(nil) })
}
```

For panic in test helpers, thread `*testing.T` into the helper:

```go
func validAgentConfigForTest(t *testing.T, agent string) json.RawMessage {
	t.Helper()
	agentKey := strings.TrimSpace(agent)
	if agentKey == "" {
		agentKey = "agent-test"
	}
	raw, err := json.Marshal(map[string]any{"exec": map[string]string{"agent_key": agentKey, "cwd": "/tmp/node-cwd"}})
	if err != nil {
		t.Fatalf("marshal agent config: %v", err)
	}
	return raw
}
```

For oversized stub method sets, split direct receiver methods across embedded focused stubs:

```go
type promptQuerierStub struct {
	promptTemplateReadStub
	promptTemplateWriteStub
	promptSectionStub
	promptDraftStub
}
```

Methods move onto the embedded focused stubs. The outer stub still satisfies the production interface through embedding, while the guard no longer sees more than 10 direct methods on one test struct.

## Task 1: Baseline Audit Lock

**Files:**
- Read: `internal/archtest/baseline_test.json`
- Read: `internal/archtest/metric_registry.go`
- Read: `internal/archtest/guardlib.go`
- Read: `internal/archtest/code_size_guard_test.go`

- [ ] **Step 1: Confirm current counts**

Run:

```bash
jq 'length' internal/archtest/baseline.json internal/archtest/baseline_test.json
```

Expected:

```text
0
81
```

- [ ] **Step 2: Confirm current test violation categories**

Run:

```bash
jq -r '
  def viols:
    [
      (if (.lines // 0) > 800 then "lines" else empty end),
      (if (.max_func_len // 0) > 80 then "max_func_len" else empty end),
      (if (.max_nesting // 0) > 4 then "max_nesting" else empty end),
      (if (.max_complexity // 0) > 10 then "max_complexity" else empty end),
      (if (.max_underscore // 0) > 3 then "max_underscore" else empty end),
      (if (.global_vars // 0) > 0 then "global_vars" else empty end),
      (if (.panic_count // 0) > 0 then "panic_count" else empty end),
      (if (.naked_returns // 0) > 0 then "naked_returns" else empty end),
      (if (.empty_funcs // 0) > 0 then "empty_funcs" else empty end),
      (if (.todo_count // 0) > 0 then "todo_count" else empty end),
      (if (.naked_goroutines // 0) > 0 then "naked_goroutines" else empty end),
      (if (.raw_goroutines // 0) > 0 then "raw_goroutines" else empty end),
      (if (.missing_docs // 0) > 0 then "missing_docs" else empty end),
      (if (.max_struct_methods // 0) > 10 then "max_struct_methods" else empty end),
      (if (.has_init // false) == true then "has_init" else empty end)
    ];
  [to_entries[] | {path:.key, violations:(.value|viols)}] as $items |
  ($items | map(.violations[]) | group_by(.)[] | {kind:.[0], files:length} | "\(.kind)\t\(.files)")
' internal/archtest/baseline_test.json
```

Expected lines:

```text
empty_funcs	1
has_init	1
max_struct_methods	2
naked_goroutines	75
panic_count	1
raw_goroutines	77
```

- [ ] **Step 3: Do not edit generated baselines in worker lanes**

Run before committing any worker lane:

```bash
git diff -- internal/archtest/baseline.json internal/archtest/baseline_test.json internal/archtest/freeze_registry.go
```

Expected: no output.

## Task 2: Special Non-Goroutine Debt

**Files:**
- Modify: `cmd/mcp-lsp/tools_call_plain_text_test.go`
- Modify: `cmd/mcp-orch/store/taskdag/test_helpers_dispatch_guard_test.go`
- Modify: `cmd/mcp-orch/store/taskdag/store_fail_downstream_test.go`
- Modify: `cmd/mcp-orch/store/taskdag/store_run_isolation_test.go`
- Modify: `internal/platform/runner/contract_test.go`
- Modify: `internal/store/binding/store_shard12_helpers_test.go`
- Modify: `internal/store/prompt/store_test.go`

- [ ] **Step 1: Remove `func init` from `cmd/mcp-lsp/tools_call_plain_text_test.go`**

Delete:

```go
func init() {
	common.RegisterToolResultPlainTextRenderer(lsptools.FormatToPlainText)
}
```

Add:

```go
func registerPlainTextRendererForTest(t *testing.T) {
	t.Helper()
	common.RegisterToolResultPlainTextRenderer(lsptools.FormatToPlainText)
	t.Cleanup(func() { common.RegisterToolResultPlainTextRenderer(nil) })
}
```

Call `registerPlainTextRendererForTest(t)` at the start of `TestDirectToolsCallReadFileReturnsPlainTextContent` and inside `runDirectToolCallForPlainText`.

- [ ] **Step 2: Replace panic helper in taskdag tests**

Change the helper signature:

```go
func validAgentConfigForTest(t *testing.T, agent string) json.RawMessage {
	t.Helper()
	agentKey := strings.TrimSpace(agent)
	if agentKey == "" {
		agentKey = "agent-test"
	}
	raw, err := json.Marshal(map[string]any{
		"exec": map[string]string{
			"agent_key": agentKey,
			"cwd":       "/tmp/node-cwd",
		},
	})
	if err != nil {
		t.Fatalf("marshal agent config: %v", err)
	}
	return raw
}
```

Update each call site:

```go
Config: validAgentConfigForTest(t, n.agent),
```

and:

```go
Config: validAgentConfigForTest(t, agent),
```

- [ ] **Step 3: Remove the empty test method in runner contract tests**

Replace:

```go
type shutdownProbeWorker struct {
	stopCtxErr error
}

func (w *shutdownProbeWorker) Start() {}
```

with:

```go
type shutdownProbeWorker struct {
	stopCtxErr error
	started    bool
}

func (w *shutdownProbeWorker) Start() {
	w.started = true
}
```

After `case <-started:` in `TestWorkerRunnerStopUsesFreshShutdownContext`, add:

```go
if !worker.started {
	t.Fatal("worker was not started")
}
```

- [ ] **Step 4: Split `bindingQuerierStub` direct methods**

In `internal/store/binding/store_shard12_helpers_test.go`, replace the single direct-method stub shape with embedded focused stubs:

```go
type bindingQuerierStub struct {
	bindingThreadStub
	bindingProviderStub
	bindingMutationStub
}
```

Use this exact split:

```text
bindingThreadStub:
  bindAgentThreadFn
  getThreadByAgentFn
  listAgentThreadBindingsFn
  rebindAgentThreadTxFn
  unbindAgentThreadFn
  BindAgentThread
  GetThreadByAgent
  ListAgentThreadBindings
  RebindAgentThreadTx
  UnbindAgentThread

bindingProviderStub:
  deleteAgentProviderBindingByIDFn
  getAgentProviderBindingByAgentIDFn
  getByProviderThreadFn
  updateArchivedFn
  DeleteAgentProviderBindingByAgentID
  GetAgentProviderBindingByAgentID
  GetAgentProviderBindingByProviderThread
  UpdateAgentProviderBindingArchived

bindingMutationStub:
  updateAgentCwdFn
  updateProviderThreadIDFn
  updateSessionUUIDFn
  upsertAgentProviderBindingFn
  UpdateAgentCwd
  UpdateAgentProviderBindingProviderThreadID
  UpdateAgentProviderBindingSessionUUID
  UpsertAgentProviderBinding
```

Preserve all existing nil-return behavior. Do not change production store interfaces.

- [ ] **Step 5: Split `promptQuerierStub` direct methods**

In `internal/store/prompt/store_test.go`, replace the single direct-method stub shape with embedded focused stubs:

```go
type promptQuerierStub struct {
	promptTemplateReadStub
	promptTemplateWriteStub
	promptSectionStub
	promptDraftStub
}
```

Use this exact split:

```text
promptTemplateReadStub:
  listFn
  getFn
  ListPromptTemplates
  GetPromptTemplate

promptTemplateWriteStub:
  deleteFn
  insertVersionFn
  createFn
  upsertFn
  DeletePromptTemplate
  InsertPromptVersion
  CreatePromptTemplate
  UpsertPromptTemplate

promptSectionStub:
  listSectionsFn
  listSectionsBatchFn
  listRecallFn
  listDefaultRulesFn
  upsertSectionFn
  lockRecallFn
  upsertRecallTargetFn
  ListPromptTemplateSectionsByTemplate
  ListPromptTemplateSectionsByTemplates
  ListRecallSections
  ListDefaultRuleSections
  UpsertPromptTemplateSection
  LockRecallTopicInCWD
  UpsertPromptRecallTopicTargetInCWD

promptDraftStub:
  upsertDraftFn
  getDraftFn
  listDraftsFn
  updateDraftStatusFn
  UpsertPromptIntentDraft
  GetPromptIntentDraft
  ListPromptIntentDrafts
  UpdatePromptIntentDraftStatus
```

Preserve all existing nil-return behavior and existing SQLite-backed tests. Do not change production store interfaces.

- [ ] **Step 6: Verify special files**

Run:

```bash
./scripts/test_with_guard.sh cmd/mcp-lsp/tools_call_plain_text_test.go
./scripts/test_with_guard.sh cmd/mcp-orch/store/taskdag/test_helpers_dispatch_guard_test.go
./scripts/test_with_guard.sh internal/platform/runner/contract_test.go
./scripts/test_with_guard.sh internal/store/binding/store_shard12_helpers_test.go
./scripts/test_with_guard.sh internal/store/prompt/store_test.go
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/taskdag -count=1
./scripts/test_with_guard.sh ./internal/platform/runner -count=1
./scripts/test_with_guard.sh ./internal/store/binding ./internal/store/prompt -count=1
```

Expected: each command exits `0`; no `has_init`, `panic_count`, `empty_funcs`, or `max_struct_methods` appears in output.

## Task 3: MCP-LSP And MCP-IDA Goroutine Lane

**Files:**
- Modify: `cmd/mcp-ida/fx_test.go`
- Modify: `cmd/mcp-lsp/lsp_binary_completion_e2e_test.go`
- Modify: `cmd/mcp-lsp/lsp_binary_go_worktree_e2e_test.go`
- Modify: `cmd/mcp-lsp/lsp_binary_gopls_log_e2e_test.go`
- Modify: `cmd/mcp-lsp/lsp_binary_java_e2e_test.go`
- Modify: `cmd/mcp-lsp/lsp_binary_python_red_e2e_test.go`
- Modify: `cmd/mcp-lsp/lsp_binary_residual_test.go`
- Modify: `cmd/mcp-lsp/middleware/timeout_test.go`
- Modify: `cmd/mcp-lsp/multilsp/manager_cold_start_test.go`
- Modify: `cmd/mcp-lsp/multilsp/multi_cwd_e2e_test.go`
- Modify: `cmd/mcp-lsp/multilsp/recycler_runner_test.go`
- Modify: `cmd/mcp-lsp/multilsp/transport_request_context_test.go`
- Modify: `cmd/mcp-lsp/multilsp/transport_responder_drain_test.go`
- Modify: `cmd/mcp-lsp/tool_timeout_test.go`

- [ ] **Step 1: Add package-local helpers where repeated launches exist**

Create helper files only in packages with two or more launches:

```text
cmd/mcp-lsp/test_goroutine_group_test.go
cmd/mcp-lsp/multilsp/test_goroutine_group_test.go
```

Use the `testGoroutineGroup` code from Required Fix Patterns.

- [ ] **Step 2: Convert one-off launches directly**

For single launches in `cmd/mcp-ida`, `cmd/mcp-lsp/middleware`, and single-file package cases, replace:

```go
go func() {
	work()
}()
```

with:

```go
var wg sync.WaitGroup
t.Cleanup(wg.Wait)
wg.Go(func() {
	work()
})
```

- [ ] **Step 3: Preserve process wait timeouts**

In `cmd/mcp-lsp/lsp_binary_completion_e2e_test.go` and related binary e2e files, keep the existing timeout and `Process.Kill` behavior. Only replace the launch primitive:

```go
group := newTestGoroutineGroup(t)
group.Go(func() { done <- c.cmd.Wait() })
```

- [ ] **Step 4: Verify lane files and packages**

Run:

```bash
./scripts/test_with_guard.sh cmd/mcp-ida/fx_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/lsp_binary_completion_e2e_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/lsp_binary_go_worktree_e2e_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/lsp_binary_gopls_log_e2e_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/lsp_binary_java_e2e_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/lsp_binary_python_red_e2e_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/lsp_binary_residual_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/middleware/timeout_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/multilsp/manager_cold_start_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/multilsp/multi_cwd_e2e_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/multilsp/recycler_runner_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/multilsp/transport_request_context_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/multilsp/transport_responder_drain_test.go
./scripts/test_with_guard.sh cmd/mcp-lsp/tool_timeout_test.go
./scripts/test_with_guard.sh ./cmd/mcp-ida ./cmd/mcp-lsp ./cmd/mcp-lsp/middleware ./cmd/mcp-lsp/multilsp -count=1
```

Expected: all commands exit `0`.

## Task 4: Internal Module Goroutine Lane

**Files:**
- Modify: `internal/module/cron/actors_test.go`
- Modify: `internal/module/memory/bus_runner_test.go`
- Modify: `internal/module/memory/consolidation_lock_test.go`
- Modify: `internal/module/memory/module_test.go`
- Modify: `internal/module/memory/nested_ingest_worker_test.go`
- Modify: `internal/module/memory/phase4_1a_combined_rules_test.go`
- Modify: `internal/module/memory/phase_self_1a_upsert_atomic_test.go`
- Modify: `internal/module/memory/similarity/similarity_test.go`
- Modify: `internal/module/memory/team_sync_coordinator_test.go`
- Modify: `internal/module/prompt/assembler_test.go`
- Modify: `internal/module/prompt/cache_invalidation_test.go`
- Modify: `internal/module/prompt/concurrent_invalidation_test.go`
- Modify: `internal/module/prompt/phase2_3a_invalidate_assemble_race_test.go`
- Modify: `internal/module/skill/approval_test.go`
- Modify: `internal/module/thread/agent_id_dedupe_test.go`
- Modify: `internal/module/thread/agent_launched_worker_test.go`
- Modify: `internal/module/thread/bus_runner_test.go`
- Modify: `internal/module/thread/launch_intent_idempotency_test.go`
- Modify: `internal/module/thread/session_recovery_worker_test.go`
- Modify: `internal/module/turn/expanded_state_test.go`
- Modify: `internal/module/turn/observation/memory_test.go`
- Modify: `internal/module/uistate/projects_concurrency_test.go`

- [ ] **Step 1: Add package-local helpers for repeated launch packages**

Create helper files where a package has repeated goroutine launch sites:

```text
internal/module/memory/test_goroutine_group_test.go
internal/module/prompt/test_goroutine_group_test.go
internal/module/thread/test_goroutine_group_test.go
```

Use the `testGoroutineGroup` code from Required Fix Patterns.

- [ ] **Step 2: Convert one-off launch packages directly**

Use direct `sync.WaitGroup` in:

```text
internal/module/cron/actors_test.go
internal/module/skill/approval_test.go
internal/module/turn/expanded_state_test.go
internal/module/turn/observation/memory_test.go
internal/module/uistate/projects_concurrency_test.go
```

Each replacement must keep existing timeout, cancellation, and channel-close ordering.

- [ ] **Step 3: Verify lane files and packages**

Run:

```bash
./scripts/test_with_guard.sh internal/module/cron/actors_test.go
./scripts/test_with_guard.sh internal/module/memory/bus_runner_test.go
./scripts/test_with_guard.sh internal/module/memory/consolidation_lock_test.go
./scripts/test_with_guard.sh internal/module/memory/module_test.go
./scripts/test_with_guard.sh internal/module/memory/nested_ingest_worker_test.go
./scripts/test_with_guard.sh internal/module/memory/phase4_1a_combined_rules_test.go
./scripts/test_with_guard.sh internal/module/memory/phase_self_1a_upsert_atomic_test.go
./scripts/test_with_guard.sh internal/module/memory/similarity/similarity_test.go
./scripts/test_with_guard.sh internal/module/memory/team_sync_coordinator_test.go
./scripts/test_with_guard.sh internal/module/prompt/assembler_test.go
./scripts/test_with_guard.sh internal/module/prompt/cache_invalidation_test.go
./scripts/test_with_guard.sh internal/module/prompt/concurrent_invalidation_test.go
./scripts/test_with_guard.sh internal/module/prompt/phase2_3a_invalidate_assemble_race_test.go
./scripts/test_with_guard.sh internal/module/skill/approval_test.go
./scripts/test_with_guard.sh internal/module/thread/agent_id_dedupe_test.go
./scripts/test_with_guard.sh internal/module/thread/agent_launched_worker_test.go
./scripts/test_with_guard.sh internal/module/thread/bus_runner_test.go
./scripts/test_with_guard.sh internal/module/thread/launch_intent_idempotency_test.go
./scripts/test_with_guard.sh internal/module/thread/session_recovery_worker_test.go
./scripts/test_with_guard.sh internal/module/turn/expanded_state_test.go
./scripts/test_with_guard.sh internal/module/turn/observation/memory_test.go
./scripts/test_with_guard.sh internal/module/uistate/projects_concurrency_test.go
./scripts/test_with_guard.sh ./internal/module/cron ./internal/module/memory ./internal/module/memory/similarity ./internal/module/prompt ./internal/module/skill ./internal/module/thread ./internal/module/turn ./internal/module/turn/observation ./internal/module/uistate -count=1
```

Expected: all commands exit `0`.

## Task 5: Internal Platform And App Goroutine Lane

**Files:**
- Modify: `internal/app/app_test.go`
- Modify: `internal/mcpserver/common/bootstrap/log_relay_e2e_test.go`
- Modify: `internal/platform/bus/bus_test.go`
- Modify: `internal/platform/cachekeepalive/drain_test.go`
- Modify: `internal/platform/hooks/dispatch_worker_test.go`
- Modify: `internal/platform/hooks/race_test.go`
- Modify: `internal/platform/mcpcontrol/config_fanout_worker_test.go`
- Modify: `internal/platform/mcpcontrol/sweeper_runner_test.go`
- Modify: `internal/platform/notify/flusher_test.go`
- Modify: `internal/platform/observability/diagnose_tail_test.go`
- Modify: `internal/platform/observability/service_test.go`
- Modify: `internal/platform/rpc/approval_cleanup_runner_test.go`
- Modify: `internal/platform/rpc/approval_test.go`
- Modify: `internal/platform/rpc/push_worker_test.go`
- Modify: `internal/platform/runner/contract_test.go`
- Modify: `internal/platform/shared/idgen_agent_test.go`
- Modify: `internal/platform/toolbridge/codex_surface_test.go`
- Modify: `internal/platform/toolbridge/host_tools_test.go`
- Modify: `internal/platform/toolbridge/proxy_runner_test.go`

- [ ] **Step 1: Add helpers for repeated platform packages**

Create package-local helper files:

```text
internal/platform/rpc/test_goroutine_group_test.go
internal/platform/toolbridge/test_goroutine_group_test.go
```

Use the `testGoroutineGroup` code from Required Fix Patterns.

- [ ] **Step 2: Convert one-off launch packages directly**

Use direct `sync.WaitGroup` in the remaining files listed for this task. In `internal/platform/runner/contract_test.go`, combine this goroutine cleanup with the `empty_funcs` fix from Task 2.

- [ ] **Step 3: Verify lane files and packages**

Run:

```bash
./scripts/test_with_guard.sh internal/app/app_test.go
./scripts/test_with_guard.sh internal/mcpserver/common/bootstrap/log_relay_e2e_test.go
./scripts/test_with_guard.sh internal/platform/bus/bus_test.go
./scripts/test_with_guard.sh internal/platform/cachekeepalive/drain_test.go
./scripts/test_with_guard.sh internal/platform/hooks/dispatch_worker_test.go
./scripts/test_with_guard.sh internal/platform/hooks/race_test.go
./scripts/test_with_guard.sh internal/platform/mcpcontrol/config_fanout_worker_test.go
./scripts/test_with_guard.sh internal/platform/mcpcontrol/sweeper_runner_test.go
./scripts/test_with_guard.sh internal/platform/notify/flusher_test.go
./scripts/test_with_guard.sh internal/platform/observability/diagnose_tail_test.go
./scripts/test_with_guard.sh internal/platform/observability/service_test.go
./scripts/test_with_guard.sh internal/platform/rpc/approval_cleanup_runner_test.go
./scripts/test_with_guard.sh internal/platform/rpc/approval_test.go
./scripts/test_with_guard.sh internal/platform/rpc/push_worker_test.go
./scripts/test_with_guard.sh internal/platform/runner/contract_test.go
./scripts/test_with_guard.sh internal/platform/shared/idgen_agent_test.go
./scripts/test_with_guard.sh internal/platform/toolbridge/codex_surface_test.go
./scripts/test_with_guard.sh internal/platform/toolbridge/host_tools_test.go
./scripts/test_with_guard.sh internal/platform/toolbridge/proxy_runner_test.go
./scripts/test_with_guard.sh ./internal/app ./internal/mcpserver/common/bootstrap ./internal/platform/bus ./internal/platform/cachekeepalive ./internal/platform/hooks ./internal/platform/mcpcontrol ./internal/platform/notify ./internal/platform/observability ./internal/platform/rpc ./internal/platform/runner ./internal/platform/shared ./internal/platform/toolbridge -count=1
```

Expected: all commands exit `0`.

## Task 6: Provider Goroutine Lane

**Files:**
- Modify: `internal/provider/claudecli/image_tracker_test.go`
- Modify: `internal/provider/claudecli/session_interrupt_pending_test.go`
- Modify: `internal/provider/claudecli/session_interrupt_test.go`
- Modify: `internal/provider/claudecli/session_recovery_test.go`
- Modify: `internal/provider/claudecli/session_restart_config_test.go`
- Modify: `internal/provider/claudecli/session_restart_test.go`
- Modify: `internal/provider/claudecli/session_silent_turn_test.go`
- Modify: `internal/provider/claudecli/thread_identity_test.go`
- Modify: `internal/provider/codexapp/forcecomplete_approval_race_test.go`
- Modify: `internal/provider/codexapp/peer_supervisor_shutdown_timeout_test.go`
- Modify: `internal/provider/codexapp/peer_supervisor_test.go`
- Modify: `internal/provider/codexapp/recovery_transport_test.go`
- Modify: `internal/provider/codexapp/server_pool_concurrency_test.go`
- Modify: `internal/provider/codexapp/server_pool_test.go`
- Modify: `internal/provider/codexapp/session_approval_shard02_test.go`
- Modify: `internal/provider/codexapp/session_test.go`
- Modify: `internal/provider/codexapp/transport_local_test.go`
- Modify: `internal/provider/codexapp/turn_output_accumulator_test.go`
- Modify: `internal/provider/codexapp/turn_output_sniff_test.go`

- [ ] **Step 1: Add helpers in provider packages**

Create:

```text
internal/provider/claudecli/test_goroutine_group_test.go
internal/provider/codexapp/test_goroutine_group_test.go
```

Use the `testGoroutineGroup` code from Required Fix Patterns.

- [ ] **Step 2: Convert provider launch sites**

Replace every raw test launch in the listed files with the helper:

```go
group := newTestGoroutineGroup(t)
group.Go(func() {
	runProviderTestWorker()
})
```

For tests that already use cancellation or process cleanup, register the goroutine wait first, then register cancellation so `t.Cleanup` runs `cancel()` before `Wait()`:

```go
group := newTestGoroutineGroup(t)
t.Cleanup(cancel)
group.Go(func() {
	_ = srv.Run(ctx)
})
```

- [ ] **Step 3: Verify provider lane**

Run:

```bash
./scripts/test_with_guard.sh internal/provider/claudecli/image_tracker_test.go
./scripts/test_with_guard.sh internal/provider/claudecli/session_interrupt_pending_test.go
./scripts/test_with_guard.sh internal/provider/claudecli/session_interrupt_test.go
./scripts/test_with_guard.sh internal/provider/claudecli/session_recovery_test.go
./scripts/test_with_guard.sh internal/provider/claudecli/session_restart_config_test.go
./scripts/test_with_guard.sh internal/provider/claudecli/session_restart_test.go
./scripts/test_with_guard.sh internal/provider/claudecli/session_silent_turn_test.go
./scripts/test_with_guard.sh internal/provider/claudecli/thread_identity_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/forcecomplete_approval_race_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/peer_supervisor_shutdown_timeout_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/peer_supervisor_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/recovery_transport_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/server_pool_concurrency_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/server_pool_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/session_approval_shard02_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/session_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/transport_local_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/turn_output_accumulator_test.go
./scripts/test_with_guard.sh internal/provider/codexapp/turn_output_sniff_test.go
./scripts/test_with_guard.sh ./internal/provider/claudecli ./internal/provider/codexapp -count=1
```

Expected: all commands exit `0`.

## Task 7: Store And Remaining Goroutine Lane

**Files:**
- Modify: `internal/store/cron/claim_sqlite_concurrency_test.go`
- Modify: `internal/store/prompt/store_recall_concurrency_test.go`
- Modify: `internal/store/thread/snapshot_test.go`

- [ ] **Step 1: Convert store goroutines**

Use direct `sync.WaitGroup` or existing test helper style. For loops, prefer `wg.Go` with copied loop variables:

```go
var wg sync.WaitGroup
for i := range workers {
	i := i
	wg.Go(func() {
		runWorker(i)
	})
}
wg.Wait()
```

If the goroutine must outlive the assertion phase, register cleanup:

```go
t.Cleanup(wg.Wait)
```

- [ ] **Step 2: Verify store lane**

Run:

```bash
./scripts/test_with_guard.sh internal/store/cron/claim_sqlite_concurrency_test.go
./scripts/test_with_guard.sh internal/store/prompt/store_recall_concurrency_test.go
./scripts/test_with_guard.sh internal/store/thread/snapshot_test.go
./scripts/test_with_guard.sh ./internal/store/cron ./internal/store/prompt ./internal/store/thread -count=1
```

Expected: all commands exit `0`.

## Task 8: Integration Freeze To Zero

**Files:**
- Modify: `internal/archtest/baseline_test.json`
- Verify unchanged or empty: `internal/archtest/baseline.json`
- Verify unchanged: `internal/archtest/freeze_registry.go`

- [ ] **Step 1: Re-run guard freeze once from the integration branch**

Run:

```bash
go run scripts/code_size_guard.go --freeze
```

Expected output includes:

```text
✅  生产 baseline — 0 个文件已冻结
✅  测试 baseline — 0 个文件已冻结
```

- [ ] **Step 2: Verify JSON baseline counts**

Run:

```bash
jq 'length' internal/archtest/baseline.json internal/archtest/baseline_test.json
```

Expected:

```text
0
0
```

- [ ] **Step 3: Run final guard**

Run:

```bash
make guard
```

Expected output includes:

```text
📊  生产 baseline 棘轮通过 — 0 个文件冻结中
📊  测试 baseline 棘轮通过 — 0 个文件冻结中
✅  代码守卫: 全部通过
```

- [ ] **Step 4: Run broad test verification**

Run:

```bash
./scripts/test_with_guard.sh ./... -count=1
```

Expected: all packages exit `0`.

- [ ] **Step 5: Confirm only intended generated baseline diff remains**

Run:

```bash
git diff --stat
git diff -- internal/archtest/baseline_test.json
git diff -- internal/archtest/baseline.json internal/archtest/freeze_registry.go
git diff --check
```

Expected:

```text
internal/archtest/baseline_test.json
```

is the only generated baseline file with a content diff, and `git diff --check` exits `0`.

## Dispatch Boundary

- Lane workers may modify only their listed `_test.go` files plus package-local `test_goroutine_group_test.go` helper files.
- Lane workers must not modify `internal/archtest/baseline.json`, `internal/archtest/baseline_test.json`, or `internal/archtest/freeze_registry.go`.
- If a worker believes a production API change is necessary, it must stop and report the exact file, symbol, failing command, and why test-only refactoring cannot solve the guard debt.
- The controller performs the final `--freeze` and stages `internal/archtest/baseline_test.json` only after every lane passes.
