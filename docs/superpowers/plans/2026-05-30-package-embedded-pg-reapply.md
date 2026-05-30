# Package Embedded PG Reapply Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在正确 `origin/main` (`ecc15fcc2`) 之上重新铺回旧 PR #51 的正确打包方向代码，避免带回坏 main 的历史或无关内容。

**Architecture:** 新总分支 `codex/package-embedded-pg-reapply-main` 从正确 `origin/main` 派生。五个 worker 分别在独立 worktree 中应用旧 PR #51 (`ce9c78650^1..ce9c78650`) 的互斥文件子集；reviewer 对每个 worker worktree 做交叉审查，合格后才合回总分支。

**Tech Stack:** Go 1.25.7, Wails/Vue frontend, shell packaging scripts, sqlc migrations, repository guard/test wrappers.

---

## Fixed Inputs

- Correct base: `origin/main` = `ecc15fcc27df303154030dde1874b8aef6348a09`
- Old package source branch: `codex/package-embedded-pg` = `f60c84d7726d6dac2ab4538844f7ad37b1e55161`
- Old PR merge commit: `ce9c7865086c5d3117900aea44e96e9bc098a694`
- Patch source range: `ce9c78650^1..ce9c78650`
- Generated patch root: `/tmp/package-reapply-20260530`

## Non-negotiable Rules

1. Do **not** merge `codex/package-embedded-pg` directly into the new branch.
2. Do **not** rebase the old polluted branch onto the new main.
3. Apply only the assigned patch/file subset in each worker worktree.
4. Preserve fail-fast behavior; no silent fallbacks/defaults.
5. Reviewer approval is required before integration.
6. Minor non-blocking review notes may be logged, but runtime-breaking, compile-breaking, test-breaking, or package-direction issues must be fixed before merge.

## File Group Boundaries

Patch files are already generated from old PR #51:

- A01 runtime/config/db/backend glue: `/tmp/package-reapply-20260530/patches/a01-runtime-config-db.patch`
- A02 Codex provider runtime/autoinstall: `/tmp/package-reapply-20260530/patches/a02-codex-provider-runtime.patch`
- A03 MCP/orchestration peers: `/tmp/package-reapply-20260530/patches/a03-mcp-orch-peers.patch`
- A04 Frontend provider UI/preferences: `/tmp/package-reapply-20260530/patches/a04-frontend-provider-ui.patch`
- A05 Packaging scripts/docs/guards/Wails glue: `/tmp/package-reapply-20260530/patches/a05-packaging-scripts-docs-guards.patch`

Corresponding path lists live in `/tmp/package-reapply-20260530/lists/`.

---

### Task 1: A01 Runtime, Config, DB, Backend Glue

**Branch/worktree:** `codex/package-reapply-a01-runtime` at `.worktrees/package-reapply-a01-runtime`

**Files:** Apply exactly `/tmp/package-reapply-20260530/patches/a01-runtime-config-db.patch`.

- [ ] Step 1: Apply patch with three-way merge.

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-reapply-a01-runtime
git apply --3way /tmp/package-reapply-20260530/patches/a01-runtime-config-db.patch
```

- [ ] Step 2: Resolve conflicts by preserving current correct-main APIs and the old PR's package/runtime behavior.

- [ ] Step 3: Run formatting and targeted tests.

```bash
gofmt -w $(git diff --name-only -- '*.go')
./scripts/test_with_guard.sh ./internal/app ./internal/contract ./internal/module/thread ./internal/module/uistate ./internal/platform/config ./internal/platform/db ./internal/platform/embeddedpg ./internal/platform/runtimeenv ./internal/platform/rpc -count=1
```

- [ ] Step 4: Verify diff scope.

```bash
git diff --check
git diff --name-only
```

- [ ] Step 5: Commit only this task's files.

```bash
git add $(cat /tmp/package-reapply-20260530/lists/a01-runtime-config-db.txt)
git commit -m "package: reapply runtime config and embedded db"
```

### Task 2: A02 Codex Provider Runtime and Autoinstall

**Branch/worktree:** `codex/package-reapply-a02-codex-provider` at `.worktrees/package-reapply-a02-codex-provider`

**Files:** Apply exactly `/tmp/package-reapply-20260530/patches/a02-codex-provider-runtime.patch`.

- [ ] Apply patch:

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-reapply-a02-codex-provider
git apply --3way /tmp/package-reapply-20260530/patches/a02-codex-provider-runtime.patch
```

- [ ] Format and run targeted tests:

```bash
gofmt -w $(git diff --name-only -- '*.go')
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/provider/shared -count=1
```

- [ ] Scope check and commit:

```bash
git diff --check
git add $(cat /tmp/package-reapply-20260530/lists/a02-codex-provider-runtime.txt)
git commit -m "package: reapply codex provider runtime packaging"
```

### Task 3: A03 MCP and Orchestration Peers

**Branch/worktree:** `codex/package-reapply-a03-mcp-orch` at `.worktrees/package-reapply-a03-mcp-orch`

**Files:** Apply exactly `/tmp/package-reapply-20260530/patches/a03-mcp-orch-peers.patch`.

- [ ] Apply patch:

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-reapply-a03-mcp-orch
git apply --3way /tmp/package-reapply-20260530/patches/a03-mcp-orch-peers.patch
```

- [ ] Format and run targeted tests:

```bash
gofmt -w $(git diff --name-only -- '*.go')
./scripts/test_with_guard.sh ./cmd/mcp-lsp ./cmd/mcp-ida ./cmd/mcp-orch -count=1
```

- [ ] Scope check and commit:

```bash
git diff --check
git add $(cat /tmp/package-reapply-20260530/lists/a03-mcp-orch-peers.txt)
git commit -m "package: reapply mcp sidecar runtime support"
```

### Task 4: A04 Frontend Provider UI and Preferences

**Branch/worktree:** `codex/package-reapply-a04-frontend` at `.worktrees/package-reapply-a04-frontend`

**Files:** Apply exactly `/tmp/package-reapply-20260530/patches/a04-frontend-provider-ui.patch`.

- [ ] Apply patch:

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-reapply-a04-frontend
git apply --3way /tmp/package-reapply-20260530/patches/a04-frontend-provider-ui.patch
```

- [ ] Run frontend verification:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

- [ ] Scope check and commit:

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-reapply-a04-frontend
git diff --check
git add $(cat /tmp/package-reapply-20260530/lists/a04-frontend-provider-ui.txt)
git commit -m "package: reapply frontend provider packaging controls"
```

### Task 5: A05 Packaging Scripts, Docs, Guards, Wails Glue

**Branch/worktree:** `codex/package-reapply-a05-packaging` at `.worktrees/package-reapply-a05-packaging`

**Files:** Apply exactly `/tmp/package-reapply-20260530/patches/a05-packaging-scripts-docs-guards.patch`.

- [ ] Apply patch:

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-reapply-a05-packaging
git apply --3way /tmp/package-reapply-20260530/patches/a05-packaging-scripts-docs-guards.patch
```

- [ ] Format and run targeted tests:

```bash
gofmt -w $(git diff --name-only -- '*.go')
./scripts/test_with_guard.sh ./internal/archtest ./internal/platform/shared ./internal/platform/toolbridge ./internal/ui/wails -count=1
make guard
```

- [ ] Shell script sanity check:

```bash
bash -n scripts/package_macos.sh scripts/package_macos_local.sh scripts/package_linux.sh scripts/package_linux_local.sh scripts/prepare_lsp_bundle_macos.sh scripts/prepare_lsp_bundle_linux.sh scripts/verify_packaged_app_macos.sh scripts/verify_packaged_app_linux.sh scripts/build_relocatable_postgres_macos.sh docs/scripts/macos_release_smoke.sh
```

- [ ] Scope check and commit:

```bash
git diff --check
git add $(cat /tmp/package-reapply-20260530/lists/a05-packaging-scripts-docs-guards.txt)
git commit -m "package: reapply release scripts docs and guards"
```

---

## Review Gates

Reviewer 1 checks A01, A03, A05 primarily; Reviewer 2 checks A02, A04, and integration risks primarily. Both reviewers must inspect all task summaries and may flag blockers in any worktree.

Required reviewer answer per worktree:

- `APPROVED`: safe to merge.
- `APPROVED_WITH_MINOR_NOTES`: safe to merge; notes are non-blocking.
- `CHANGES_REQUIRED`: do not merge; list exact blocker(s), file(s), and verification failure(s).

## Integration Gate

After a worktree is approved:

```bash
cd /Users/ai/Desktop/Super-Dolphin/.worktrees/package-embedded-pg-reapply-main
git merge --no-ff <approved-worker-branch>
```

After all approved worker branches are merged, run:

```bash
make guard
make test
make build-plain
```

If frontend changed, run:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```
