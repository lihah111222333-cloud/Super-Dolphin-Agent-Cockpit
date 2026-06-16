# A01: Upstream Sync and Conflict Map

**Goal:** 在不丢失当前 packaged clean VM 打包改动的前提下，把 worktree 同步到 `origin/main` 并输出可审计冲突处理清单。

**Files:**
- Read: `git status --short --branch`
- Read: `git diff --name-status`
- Read: `git diff --stat`
- Read: upstream changed files from `git diff --name-status HEAD..origin/main`
- Modify only after conflict resolution: files reported by git merge/rebase conflicts
- Write: `docs/cc/p0/01-upstream-sync-conflict-map.md` execution notes

**Steps:**
- [ ] Record pre-fetch refs: `git rev-parse HEAD origin/main` and `git rev-list --left-right --count HEAD...origin/main`.
- [ ] Hard gate: run `git fetch origin main`. If fetch fails or `origin/main` cannot be resolved after fetch, stop; do not merge against a stale remote-tracking ref.
- [ ] Record post-fetch refs again: `git rev-parse HEAD origin/main` and `git rev-list --left-right --count HEAD...origin/main`.
- [ ] Classify every dirty file as packaging/runtime/frontend/provider/docs/scripts/test.
- [ ] Create a safe WIP commit or equivalent reversible snapshot before merge; do not use `git stash` if untracked files are required for packaging.
- [ ] Merge `origin/main` into `codex/package-embedded-pg`; prefer merge over rebase unless user explicitly requests history rewrite.
- [ ] Resolve conflicts by preserving upstream architecture changes and reapplying only packaged/runtime intent from this branch.
- [ ] Record every conflict resolution in the fixed conflict map table below before downstream handoff.
- [ ] Run focused compile/test smoke for conflicted packages before handing off downstream agents.

**Conflict map output:**

| File | Conflict type | Resolution (`upstream`/`branch`/`manual fusion`) | Downstream agent | Unresolved semantic conflict |
| --- | --- | --- | --- | --- |

During A01 execution, add one row per conflict using these exact columns. If the merge has no conflicts, write one sentence below the table: `No merge conflicts after fresh fetch and merge of origin/main.` Do not leave placeholder rows in the completed plan.

Rules:
- `upstream` means the resolved file intentionally takes `origin/main` behavior.
- `branch` means the resolved file intentionally preserves package-embedded-pg behavior.
- `manual fusion` means both sides were edited together and must name the downstream owner that validates the fused semantics.
- Any `yes` in **Unresolved semantic conflict** is a hard handoff item; the named downstream agent must either resolve it or stop with an explicit blocker.

**Validation:**
```bash
git status --short --branch
git diff --check
```

**Done:** worktree has freshly fetched `origin/main` merged, conflicts resolved, post-fetch refs recorded, and downstream agents know which files changed during conflict resolution.
