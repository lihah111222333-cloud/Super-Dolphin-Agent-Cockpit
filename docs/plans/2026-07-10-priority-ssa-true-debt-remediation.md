# Priority SSA True Debt Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the ten true `priority_ssa_error_string` violations with typed SQLite, retry, and memory-error classification while leaving all eight wide-port violations untouched.

**Architecture:** The DB platform classifies only `*modernc.org/sqlite.Error` numeric result codes. The skill summary executor owns an explicit retry marker consumed by its retry loop. Contract owns memory failure sentinels visible to both memory and toolbridge; memory aliases preserve existing identities and the outer AgentMemoryError code remains `partial`.

**Tech Stack:** Go errors.Is/errors.As, modernc SQLite result codes, Go SSA guard baseline ratchet.

**Verification Surface:** targeted RED/GREEN Go tests, affected package guard tests, `internal/archtest`, strict code-size guard, LSP diagnostics, generated `freeze_baseline.json`.

---

### Task 1: Replace SQLite text classification with numeric result codes

**Files:**
- Modify: `internal/platform/db/errors.go:76-108`
- Modify: `internal/platform/db/sqlite_helpers.go:39-49`
- Modify: `internal/platform/db/errors_test.go:35-59,136-162`

- [x] **Step 1: Write failing typed-error tests**

Add a test helper that creates a real SQLite UNIQUE failure through `database/sql` and asserts `IsConflict` is true for that typed failure but false for `errors.New("UNIQUE constraint failed")`. Add a unit test for the primary-code helper with `SQLITE_BUSY | (1 << 8)` and `SQLITE_LOCKED | (1 << 8)` to prove extended busy/locked codes remain retryable.

- [x] **Step 2: Run the DB tests and verify RED**

Run: `go test ./internal/platform/db -run '^(TestIsConflict|TestSQLitePrimaryResultCode)$' -count=1`

Expected: FAIL because current production code still accepts synthetic error-message strings and has no primary-code helper.

- [x] **Step 3: Implement typed SQLite classification**

Use `errors.As` to unwrap `*sqlite.Error`, match UNIQUE with `sqlite3.SQLITE_CONSTRAINT_UNIQUE`, and match busy/locked after masking the extended result code with `0xff`. Remove every `strings.Contains` check from the two classifiers.

- [x] **Step 4: Run the DB tests and verify GREEN**

Run: `./scripts/test_with_guard.sh ./internal/platform/db -count=1`

Expected: PASS; typed UNIQUE remains a conflict, extended busy/locked primary codes remain retryable, and synthetic message-only errors no longer classify.

### Task 2: Mark only known skill-summary parse failures as retryable

**Files:**
- Modify: `internal/module/skill/summarysuggest/execute.go:12-53`
- Create: `internal/module/skill/summarysuggest/execute_test.go`
- Modify: `internal/module/skill/rpc_types.go:87-102`
- Modify: `internal/module/skill/rpc_types_test.go`

- [x] **Step 1: Write failing retry-marker tests**

Test that `summarysuggest.IsRetryable(summarysuggest.MarkRetryable(err))` is true, plain errors are false, and both malformed JSON and an empty skill summary returned by `parseSkillSummarySuggestionResult` carry that marker.

- [x] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/module/skill/... -run '^(TestRetryable|TestParseSkillSummarySuggestionResult.*Retryable)$' -count=1`

Expected: FAIL because the retry marker API does not exist and parser errors are currently only message text.

- [x] **Step 3: Implement the marker without changing retry count**

Add a private retry sentinel plus exported `MarkRetryable(error) error` and `IsRetryable(error) bool` in `summarysuggest`. Make `ExecuteWithOptions` use `IsRetryable`; wrap only the JSON parse and empty-description errors in `parseSkillSummarySuggestionResult` with `MarkRetryable`.

- [x] **Step 4: Run the skill package tests and verify GREEN**

Run: `./scripts/test_with_guard.sh ./internal/module/skill/... -count=1`

Expected: PASS; `ExecuteWithOptions` still retries once only for the two known parse outcomes.

### Task 3: Move cross-layer memory cause identities to contract sentinels

**Files:**
- Modify: `internal/contract/memory.go:10-17`
- Modify: `internal/module/memory/domain_bridges.go:24-28`
- Modify: `internal/module/memory/factory.go:19-25`
- Modify: `internal/module/memory/ui_rpc.go:475-489`
- Modify: `internal/platform/toolbridge/memory_write_tool.go:3-10,114-125`
- Modify: `internal/platform/toolbridge/host_tools_memory_registry_shard19_test.go:40-67`
- Add or modify: focused memory UI error-code test

- [x] **Step 1: Write failing sentinel-identity tests**

Add tests that hide each contract sentinel behind an opaque `Unwrap` chain. Assert memory UI and toolbridge return the matching stable marker; toolbridge must retain outer `AgentMemoryError{Code:"partial"}` metadata.

- [x] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/module/memory ./internal/platform/toolbridge -run '^(TestRedactedMemoryIntentError|TestMemoryWrite.*Partial)' -count=1`

Expected: FAIL because contract does not yet export these sentinels and both consumers still parse `err.Error()` text.

- [x] **Step 3: Implement contract-owned identities**

Declare the three sentinels in `contract`; make memory's existing exported variables aliases to them. Replace the two marker loops with ordered `errors.Is` checks. Keep `partialMemoryWriteToolResult` and its `code:"partial"` behavior unchanged.

- [x] **Step 4: Run memory and toolbridge tests and verify GREEN**

Run: `./scripts/test_with_guard.sh ./internal/module/memory ./internal/platform/toolbridge -count=1`

Expected: PASS; transport-visible marker text is preserved through typed causes and the outer partial status is unchanged.

### Task 4: Prove the true debt is zero and preserve wide-port debt

**Files:**
- Modify: `internal/archtest/freeze_baseline.json`
- Verify: all files from Tasks 1-3

- [x] **Step 1: Run the architecture guard to shrink only stale true debt**

Run: `./scripts/test_with_guard.sh ./internal/archtest -count=1`

Expected: PASS; the freeze ratchet removes exactly ten `priority_ssa_error_string` entries and retains eight `priority_ssa_wide_port` entries.

- [x] **Step 2: Run strict scan and verify wide-port-only output**

Run: `go run ./scripts/code_size_guard.go --strict`

Expected: exit 1 with exactly eight `priority_ssa_wide_port` findings and no `priority_ssa_error_string` findings.

- [x] **Step 3: Run final quality checks**

Run: affected guarded package tests, LSP `file(diagnostics)` for every changed Go file, `git diff --check`, and `jq '.priority_ssa | length' internal/archtest/freeze_baseline.json`.

Expected: tests pass, diagnostics are empty, whitespace check passes, and the unified priority SSA baseline contains exactly eight wide-port entries.
