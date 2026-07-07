# Codex Readonly Roots Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve restricted read-only sandbox readable roots when Codex provider launches or reuses runtime config.

**Architecture:** Keep the existing sandbox mode normalization, but extend the read-only branch to carry a validated `access` object into `sandboxPolicy`. When native tool policy forces read-only, preserve an already-restricted read-only policy instead of overwriting it with bare `{type:"readOnly"}`.

**Tech Stack:** Go, `encoding/json`, existing codexapp provider tests.

**Verification Surface:** `internal/provider/codexapp/driver.go`, `internal/provider/codexapp/driver_pool_routing.go`, `internal/provider/codexapp/input_map_test.go`, `internal/provider/codexapp/driver_pool_routing_test.go`, codexapp Go package tests.

---

### Task 1: Lock Restricted Read-Only Policy With Tests

**Files:**
- Modify: `internal/provider/codexapp/input_map_test.go`
- Modify: `internal/provider/codexapp/driver_pool_routing_test.go`

- [ ] **Step 1: Add failing start-param tests**

Add tests that build thread start params from:

```go
Config: map[string]any{
    "sandbox": map[string]any{
        "type": "readOnly",
        "access": map[string]any{
            "type": "restricted",
            "readableRoots": []string{"/repo/app", "/Users/ai/shared"},
            "includePlatformDefaults": true,
        },
    },
}
```

and a snake_case equivalent. Assert `params.SandboxPolicy` includes `type`, `access.type`, `access.readableRoots`, and `access.includePlatformDefaults`.

- [ ] **Step 2: Add failing runtime-config test**

Add a `canonicalStartRuntimeConfig` test asserting restricted read-only `sandboxPolicy` survives both initial canonicalization and native-tool read-only enforcement when the original policy is already read-only restricted.

- [ ] **Step 3: Run focused tests to confirm failure**

```bash
go test ./internal/provider/codexapp -run 'RestrictedReadOnly|CanonicalStartRuntimeConfig' -count=1
```

Expected before implementation: readable roots are absent or the policy is overwritten to bare `{type:"readOnly"}`.

### Task 2: Preserve Readable Roots In Policy Conversion

**Files:**
- Modify: `internal/provider/codexapp/driver.go`
- Modify: `internal/provider/codexapp/driver_pool_routing.go`

- [ ] **Step 1: Copy restricted read-only access**

In `codexSandboxPolicyFromMode`, when mode is `read-only`, copy an `access` object only if `access.type == "restricted"` and at least one non-empty readable root exists. Accept both `readableRoots` and `readable_roots`, and normalize `includePlatformDefaults` / `include_platform_defaults` to `includePlatformDefaults`.

- [ ] **Step 2: Preserve restricted read-only during native-tool enforcement**

Change `ApplyThreadStartParams`, `canonicalStartRuntimeConfig`, and resume runtime policy helpers so they keep an existing read-only policy with `access.type == "restricted"` instead of overwriting it with bare read-only. If no restricted policy exists, keep the current bare read-only behavior.

- [ ] **Step 3: Run focused tests to confirm pass**

```bash
go test ./internal/provider/codexapp -run 'RestrictedReadOnly|CanonicalStartRuntimeConfig' -count=1
```

Expected after implementation: all new tests pass.

### Task 3: Full Verification And Push

**Files:**
- Validate: `internal/provider/codexapp`
- Commit: plan, implementation, tests

- [ ] **Step 1: Run package verification**

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
```

- [ ] **Step 2: Commit and push directly to remote main**

```bash
git add docs/plans/2026-07-05-codex-readonly-roots.md internal/provider/codexapp/driver.go internal/provider/codexapp/driver_pool_routing.go internal/provider/codexapp/input_map_test.go internal/provider/codexapp/driver_pool_routing_test.go
git commit -m "fix: 保留 Codex 只读根权限"
git push origin HEAD:main
```
