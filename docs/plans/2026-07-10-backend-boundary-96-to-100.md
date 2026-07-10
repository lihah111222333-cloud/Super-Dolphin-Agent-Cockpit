# Backend Boundary 96-to-100 Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复后端边界治理评分中剩余的三项缺口：弱语义 surface、registry 声明定位、生成文件 symlink TOCTOU。

**Architecture:** 保持 `DefaultBackendBoundaryRegistry()` 为唯一规则事实源，不新增 JSON/YAML 清单。用两条有实际约束力的 canonical allow-import rules 覆盖原先只有 `fx_assembly_scope` 的 command/support surfaces；用 canonical registry 源码 AST 为治理元数据错误补物理 `file:line:column`；用 Go 1.25 `os.Root` 将生成文件读取、建目录、暂存、rename 与 rollback 锚定到已打开的仓库根，避免检查后重新解析绝对路径。

**Tech Stack:** Go 1.25.7、`go/ast`、`go/parser`、`go/token`、`os.Root`、现有 archtest evaluator/registry、现有 `scripts/archtestmap` 事务测试框架。

**Verification Surface:** `internal/archtest`、`scripts/archtestmap`、`make guard`、`make codemap-check`、`make project-map-check`、LSP diagnostics、`git diff --check`。

---

## File structure and ownership

- Modify `internal/archtest/backend_boundary_registry.go`: 只在 canonical registry 中新增 owner/rule/surface 映射；不得复制规则事实到消费者。
- Modify `internal/archtest/backend_boundary_governance.go`: surface 语义覆盖验证与 canonical registry AST 定位；不拆新生产文件。
- Modify `internal/archtest/backend_boundary_evaluator.go` only if existing rule kind cannot express the required allow-import policies;优先复用现有 evaluator。
- Modify existing `internal/archtest/*_test.go`: 每个规则和声明定位行为必须先 RED 后 GREEN。
- Modify `scripts/archtestmap/main.go`: 用 `os.Root` 重写生成文件 I/O/rename/rollback；不拆新生产文件。
- Modify `scripts/archtestmap/main_test.go`: 添加确定性的竞态窗口负例，并保持现有 rollback tests。
- Refresh generator-owned `docs/doc/codemap/13-archtest-boundaries.md`、`README.md`、`docs/doc/codemap/ai-index.json` 和 project-map outputs only when generators require them。
- Do not touch existing unrelated untracked files in the primary worktree; they are not present or owned in this isolated worktree.

### Task 1: Replace Fx-only surface placeholders with semantic rules

- [ ] **Step 1: Write failing registry/evaluator tests**

Add tests that prove all surfaces previously mapped only to `fx_assembly_scope` also reference a semantic rule whose kind is not merely scoped Fx placement. At minimum cover:

```text
cmd/agent-runtime
cmd/agent-terminal
cmd/super-dolphin-release-manifest
cmd/super-dolphin-updater
internal/devtools
internal/dto
internal/testutil
internal/util
```

Add fixture tests proving these intended policies reject an illegal import:

```text
command_narrow_import_surface:
  cmd/agent-runtime + cmd/agent-terminal -> only internal/app and registered runtime platform seams
  cmd/super-dolphin-release-manifest -> only internal/module/appupdate
  cmd/super-dolphin-updater -> only internal/util/ctxutil

internal_support_narrow_import_surface:
  internal/dto -> only internal/dto descendants
  internal/devtools -> only internal/devtools descendants
  internal/testutil -> internal/testutil descendants plus internal/contract
  internal/util -> internal/util descendants plus the currently required registered platform seams
```

The test must reject placeholder `all_backend`, empty allowlists, rules that match zero files, or rules that only repeat `fx_assembly_scope`.

- [ ] **Step 2: Verify RED**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'Test.*(Semantic|NarrowImport|Governance)' -count=1
```

Expected: FAIL because the eight surfaces currently have no non-Fx semantic rule and the fixture imports are not governed by the new IDs.

- [ ] **Step 3: Add the minimum canonical registry rules**

Add typed owner/rule IDs and `BoundaryRuleAllowInternalImports` policies in `defaultBackendBoundaryRegistry()`. Reuse the existing policy evaluator and per-source-pattern allow semantics. Register the two rule IDs on the exact eight surfaces. Do not add a second registry, procedural allowlist, default fallback, broad `all_backend` rule, or exemption without a non-empty reason.

- [ ] **Step 4: Verify GREEN**

Run the same focused command and then:

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

Expected: PASS, with current production imports accepted and negative fixtures rejected.

### Task 2: Report canonical registry metadata errors at physical file:line:column

- [ ] **Step 1: Write failing provenance tests**

Mutate a cloned default registry to create representative owner, rule, guard, and surface descriptor errors. Call `ValidateBackendBoundaryGovernance(root, registry)` and assert each canonical entry error begins with a physical position matching:

```text
internal/archtest/backend_boundary_registry.go:<positive-line>:<positive-column>
```

Also assert the reported position is the physical composite-literal entry, not a `//line` logical position or a hard-coded stale line number. Custom synthetic registries with no canonical source match must retain an explicit deterministic fallback label rather than fabricating a position.

- [ ] **Step 2: Verify RED**

Run:

```bash
./scripts/test_with_guard.sh ./internal/archtest -run 'TestValidateBackendBoundaryGovernance.*Position' -count=1
```

Expected: FAIL because current metadata violations start with `owner[n]`, `rule[n]`, `guard[n]`, or `surface[n]` only.

- [ ] **Step 3: Add AST-backed canonical source positions**

In `backend_boundary_governance.go`, parse `internal/archtest/backend_boundary_registry.go` with a fresh `token.FileSet`, find the `BackendBoundaryRegistry` literal returned by `defaultBackendBoundaryRegistry`, and map `Owners`, `Rules`, `Guards`, and `Surfaces` entries by section/index to physical `PositionFor(pos, false)`. Prefix applicable governance violations at the final aggregation boundary. Do not add manually maintained line constants or provenance facts to the registry.

If the canonical source cannot be read or parsed, return a fail-fast governance violation containing the source path and cause. Keep arbitrary synthetic registries testable through deterministic fallback labels.

- [ ] **Step 4: Verify GREEN**

Run the focused test and full `internal/archtest` package. Expected: PASS; import-policy violations must continue using their existing physical source positions.

### Task 3: Anchor generated artifact transactions with os.Root

- [ ] **Step 1: Write a deterministic race-window test**

Extend the existing injected generated-file operations with a test-only hook that executes after initial target validation but before staging/rename. In that hook, replace an in-root directory with a symlink to an external directory. Assert refresh returns an error and creates/modifies nothing outside the repository. Preserve existing lexical escape, parent symlink, second-commit rollback, double-failure, and check-mode tests.

- [ ] **Step 2: Verify RED**

Run:

```bash
./scripts/test_with_guard.sh ./scripts/archtestmap -run 'Test.*Generated.*(Race|Symlink|Escape)' -count=1
```

Expected: FAIL because current code validates absolute paths and later reopens them through `os.*` path functions.

- [ ] **Step 3: Replace path-reopening I/O with os.Root**

Open the trusted repository root once with `os.OpenRoot`. Convert every artifact target to a validated root-relative path. Perform parent creation, Lstat/read, exclusive temp creation, rename commit, cleanup, and rollback through `*os.Root` methods. Generate collision-resistant temp basenames and open with `O_CREATE|O_EXCL`; keep temp files in the destination directory. Close the root on every path and join close errors when they affect transaction integrity.

Do not use a precheck as the security boundary, do not call absolute-path `os.CreateTemp`, `os.Rename`, `os.ReadFile`, or `os.MkdirAll` for generated targets after the root is open, and do not add platform-specific split files. Preserve existing best-effort multi-file rollback semantics and error context.

- [ ] **Step 4: Verify GREEN**

Run focused `scripts/archtestmap` tests, then its full package. Expected: PASS with no external write.

### Task 4: Refresh generated surfaces and run acceptance

- [ ] Run `gofmt` on modified Go files.
- [ ] Run `./scripts/test_with_guard.sh ./internal/archtest ./scripts/archtestmap -count=1`.
- [ ] Run `make guard`.
- [ ] Run `make codemap-refresh && make codemap-check`.
- [ ] Refresh project-map from the owned staged snapshot or equivalent clean worktree state, then run `make project-map-check`.
- [ ] Run `git diff --check`.
- [ ] Use LSP `grep`, `inspect`, `xref`, `file(read_file)`, and `file(diagnostics)` on all modified Go files; every diagnostic severity must be zero or explicitly blocked.
- [ ] Self-review the exact diff for duplicate truth sources, weakened policies, hidden fallbacks, absolute-path writes, and unrelated generated drift.
- [ ] Commit only owned files with a Chinese commit title after all checks pass; do not push or merge unless the user explicitly asks.
