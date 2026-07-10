# Backend Guard Applies-To Proof Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让专项 guard 与后端 surface 的关联由 typed `AppliesTo` 双向校验，错误绑定必须在 archtest 中失败。

**Architecture:** `BackendBoundaryGuard` 增加 `AppliesTo []BoundarySurfaceID`，保留 `BackendBoundarySurface.GuardIDs` 作为面向 surface 的消费视图。registry 校验器对两侧关系做双向等价检查：surface 引用的 guard 必须声明适用该 surface，guard 声明的每个 surface 也必须反向引用该 guard。

**Tech Stack:** Go AST/typed registry、table-driven Go tests、`scripts/archtestmap` 生成器。

**Verification Surface:** `internal/archtest`、`scripts/archtestmap`、generated codemap、project-map strict drift、LSP diagnostics。

---

## File structure

- Modify `internal/archtest/backend_boundary_registry.go`: 新增 `BoundarySurfaceID`、`BackendBoundaryGuard.AppliesTo` 和深拷贝。
- Modify `internal/archtest/backend_boundary_governance.go`: 登记每个 guard 的适用 surface，并执行双向绑定校验。
- Modify `internal/archtest/backend_boundary_governance_test.go`: 用真实合法 guard 构造错误 surface 绑定，并覆盖 descriptor/deep-copy 约束。
- Modify `scripts/archtestmap/main.go`: 在生成的 guard 表中输出 `Applies to`。
- Modify `scripts/archtestmap/main_test.go`: 固定生成内容和 fixture 的 typed applies-to 关系。
- Generate `docs/doc/codemap/13-archtest-boundaries.md`: 从同一 Go registry 刷新，不手工编辑。

### Task 1: Prove the current false positive

**Files:**
- Test: `internal/archtest/backend_boundary_governance_test.go`

- [x] **Step 1: Write the failing behavior test**

```go
func TestValidateBackendBoundaryGovernanceRejectsGuardSurfaceMismatch(t *testing.T) {
    t.Parallel()

    root, registry := validBackendBoundaryGovernanceFixture(t)
    registry.Surfaces[0].GuardIDs = []archtest.BoundaryGuardID{"backend_surface_governance"}
    assertGovernanceViolation(t, root, registry, `surface "cmd/tool" guard "backend_surface_governance" is not declared in guard applies_to`)
}
```

- [x] **Step 2: Run RED**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run TestValidateBackendBoundaryGovernanceRejectsGuardSurfaceMismatch -count=1
```

Expected: FAIL because the current validator accepts the legal guard ID on the unrelated `cmd/tool` surface.

### Task 2: Add typed applicability and fail-closed validation

**Files:**
- Modify: `internal/archtest/backend_boundary_registry.go`
- Modify: `internal/archtest/backend_boundary_governance.go`
- Test: `internal/archtest/backend_boundary_governance_test.go`

- [x] **Step 1: Add the typed field and deep copy**

```go
type BoundarySurfaceID string

type BackendBoundaryGuard struct {
    ID        BoundaryGuardID
    File      string
    TestNames []string
    BuildTags []string
    AppliesTo []BoundarySurfaceID
    Reason    string
}
```

`cloneBackendBoundaryRegistry` must copy it with:

```go
AppliesTo: append([]BoundarySurfaceID(nil), guard.AppliesTo...),
```

- [x] **Step 2: Register exact applicability in the canonical registry**

Each item from `defaultBackendBoundaryGuards()` receives its current reverse mapping from `defaultBackendBoundarySurfaces()`. `pkg_public_boundary` lists all four `pkg/*metrics`/`pkg/logger` surfaces; no second JSON or independent registry is introduced.

- [x] **Step 3: Validate fields and both directions**

Add a helper with this contract:

```go
func validateBackendBoundaryGuardApplicability(guards []BackendBoundaryGuard, surfaces []BackendBoundarySurface) []string
```

It must reject empty, duplicate, malformed, or unknown `AppliesTo` entries, reject surface references outside `guard.AppliesTo`, and reject `AppliesTo` targets whose surface does not reference the guard. Errors include both guard ID and surface path.

- [x] **Step 4: Run GREEN**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'Test(ValidateBackendBoundaryGovernanceRejectsGuardSurfaceMismatch|ValidateBackendBoundaryGovernanceRejectsInvalidDescriptors|BackendBoundaryGovernanceRegistryReturnsDeepCopy)$' -count=1
```

Expected: PASS.

### Task 3: Expose proof in the generated AI map

**Files:**
- Modify: `scripts/archtestmap/main.go`
- Modify: `scripts/archtestmap/main_test.go`
- Generate: `docs/doc/codemap/13-archtest-boundaries.md`

- [x] **Step 1: Add generator assertions before implementation**

The deterministic map test must require an `Applies to` column and a rendered surface such as `` `internal/archtest` `` for `fixture_guard`.

- [x] **Step 2: Verify generator RED**

Run:

```bash
./scripts/test_with_guard.sh ./scripts/archtestmap -run TestRenderRuleMapIsDeterministic -count=1
```

Expected: FAIL because the current guard table omits applies-to data.

- [x] **Step 3: Render typed surface IDs**

Update `renderGuards` to emit:

```text
| Guard | Test file | Build tags | Runnable tests | Applies to | Reason |
```

Use a small typed renderer that sorts a copied slice and delegates to the existing code-list renderer.

- [x] **Step 4: Refresh and check generated outputs**

Run:

```bash
make codemap-refresh
make codemap-check
```

Expected: generated rule map contains all guard applicability rows and the check is read-only/pass.

### Task 4: Final verification and owned-diff audit

**Files:**
- Verify only the files listed above plus generator-owned codemap/project-map artifacts if repository checks require them.

- [x] **Step 1: Run focused and repository gates**

```bash
./scripts/test_with_guard.sh ./scripts/archtestmap -count=1
make guard
make codemap-check
node scripts/generate_ai_project_map.mjs --check --strict-drift
git diff --check
```

- [x] **Step 2: Run LSP diagnostics**

Diagnostics must be empty for the four changed Go source/test files.

- [x] **Step 3: Audit final diff**

Confirm no local absolute paths, timestamps, unrelated files, baseline reductions, hidden fallbacks, staging, commits, or pushes. Commit is intentionally omitted because the user authorized implementation in a worktree, not Git publication.
