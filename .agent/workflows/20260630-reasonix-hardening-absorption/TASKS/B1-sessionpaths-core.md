---
task_id: B1-sessionpaths-core
owner: agent-b1
status: ready
depends_on: [A2-toolbridge-stage-gate, A3-readonly-delegation-filter]
---

# B1-sessionpaths-core

## 1. Goal

Add a stdlib-only `internal/platform/sessionpaths` package that captures current deterministic path behavior before callers move.

## 2. Inputs

- `SOURCE_PLAN_SNAPSHOT.md` Lane B.
- `internal/provider/codexapp/history_rollout.go`
- `internal/util/historyjsonl/history.go`
- `internal/module/thread/scratchpad.go`
- Existing path tests in provider, util, and thread packages.

## 3. Outputs

- `internal/platform/sessionpaths` package.
- Golden tests for rollout glob, managed scratchpad dir, managed scratchpad check, and project path sanitize.
- Arch guard proving sessionpaths does not import repo-internal packages.

## 4. File Permissions

- RW: `internal/platform/sessionpaths/`
- RW: `internal/archtest/sessionpaths_dependency_guard_test.go`
- RO: provider/module/util callers.

## 5. Steps

1. Add golden tests for `CodexRolloutGlob(codexHome, threadID)`.
2. Add golden tests for `ManagedScratchpadDir(tempRoot, projectRoot, threadID)`.
3. Add golden tests for `IsManagedScratchpadDir(tempRoot, dir)`.
4. Add golden tests for `SanitizeProjectPath(raw)`.
5. Implement the helper using only the standard library.
6. Add dependency arch guard denying `github.com/anthropic-ai/super-agent-v3/` imports from sessionpaths.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh ./internal/platform/sessionpaths -run 'Rollout|Scratchpad|Path' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Path' -count=1
```

## 7. DoD

- [ ] Golden tests capture current path strings.
- [ ] Package is stdlib-only.
- [ ] Dependency guard is limited to sessionpaths import direction.
- [ ] No caller migration yet.

## 8. Rollback

Revert `internal/platform/sessionpaths/` and task-owned archtest changes.
