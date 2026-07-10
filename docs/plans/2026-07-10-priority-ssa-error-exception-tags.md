# Priority SSA Error-String Exception Tags Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the priority SSA error-string rule consume the existing `archguard:ignore` protocol so that seven reviewed boundary exceptions graduate from the freeze baseline while all untagged error-string matching remains guarded.

**Architecture:** `prioritySSAPackage` already retains the parsed Go syntax and its `FileSet`. Add one local lookup in the production priority SSA rule that reuses `collectArchGuardIgnores`, matching a call position to its source AST file and requiring the existing `priority_ssa_error_string` metric token. Do not add a second annotation syntax or a path/line allowlist.

**Tech Stack:** Go AST, `go/token`, Go SSA, existing archtest freeze ratchet.

**Verification Surface:** `internal/archtest` targeted RED/GREEN test, guarded archtest suite, strict guard scan, generated `internal/archtest/freeze_baseline.json`, LSP diagnostics.

---

### Task 1: Prove annotation handling before changing the scanner

**Files:**
- Create: `internal/archtest/priority_ssa_rules_test.go`
- Read: `internal/archtest/archguard_ignore.go:9-77`
- Read: `internal/archtest/priority_ssa_rules.go:71-84`

- [x] **Step 1: Write the failing test**

Add a same-package test that parses and type-checks one Go source file containing two `strings.Contains(err.Error(), ...)` calls. Put `// archguard:ignore priority_ssa_error_string -- external CLI text has no typed error` immediately before the first call and leave the second untagged. Build SSA, call `collectPrioritySSAErrorStringViolations`, and assert the result contains only the untagged call.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/archtest -run '^TestPrioritySSAErrorStringRuleHonorsArchguardIgnore$' -count=1`

Expected: FAIL because the scanner currently records both calls and does not consume `archguard:ignore`.

- [x] **Step 3: Implement minimal production lookup**

In `internal/archtest/priority_ssa_rules.go`, add a helper that maps `call.Pos()` through `pkg.fset`, finds the matching `pkg.syntax` file, calls `collectArchGuardIgnores`, and checks `PrioritySSAErrorStringRule`. Skip only that annotated call before appending its violation.

- [x] **Step 4: Run test to verify it passes**

Run: `go test ./internal/archtest -run '^TestPrioritySSAErrorStringRuleHonorsArchguardIgnore$' -count=1`

Expected: PASS; the annotated call is omitted and the untagged call remains a violation.

### Task 2: Tag exactly the reviewed exceptions and refresh the unified freeze

**Files:**
- Modify: `cmd/mcp-orch/orchestration/nodeexec/ops.go:115`
- Modify: `internal/platform/difftracker/git_ops.go:88,183,184`
- Modify: `internal/provider/codexapp/transport_process.go:230`
- Modify: `internal/provider/dreamexec/dreamexec.go:157,158`
- Modify: `internal/archtest/freeze_baseline.json`

- [x] **Step 1: Add only explicit existing-protocol annotations**

Place `archguard:ignore priority_ssa_error_string -- <specific reason>` on the exact source line or immediately preceding source line accepted by `collectArchGuardIgnores`. Reasons must identify the external JSON/Git/OS boundary or the stderr deduplication behavior. Do not annotate the ten true-positive sites.

- [x] **Step 2: Run the guarded suite to shrink stale exceptions**

Run: `./scripts/test_with_guard.sh ./internal/archtest -count=1`

Expected: PASS; the unified freeze ratchet reports 18 priority SSA violations after removing exactly seven stale `priority_ssa_error_string` entries.

- [x] **Step 3: Confirm the generated delta**

Run: `jq '.priority_ssa | length' internal/archtest/freeze_baseline.json && git diff -- internal/archtest/freeze_baseline.json`

Expected: `18`; the diff deletes exactly the seven reviewed error-string baseline entries and does not change metric sections or other priority SSA rules.

### Task 3: Verify guard behavior and source hygiene

**Files:**
- Verify: `internal/archtest/priority_ssa_rules.go`
- Verify: `internal/archtest/priority_ssa_rules_test.go`
- Verify: seven annotated boundary sites

- [x] **Step 1: Run the relevant guarded test suite**

Run: `./scripts/test_with_guard.sh ./internal/archtest -count=1`

Expected: PASS with the 18-entry priority SSA freeze baseline.

- [x] **Step 2: Run strict scan and verify the retained debt**

Run: `go run ./scripts/code_size_guard.go --strict`

Expected: exit 1 with exactly 18 strict priority SSA findings: 8 wide-port findings and 10 true error-string findings.

- [x] **Step 3: Check Go diagnostics and diff scope**

Run: LSP `file(diagnostics)` for changed Go files, then `git diff --check` and `git diff --stat`.

Expected: no new diagnostics in changed files; no whitespace errors; only the plan, scanner/test, seven annotated source sites, and the generated freeze baseline are changed.
