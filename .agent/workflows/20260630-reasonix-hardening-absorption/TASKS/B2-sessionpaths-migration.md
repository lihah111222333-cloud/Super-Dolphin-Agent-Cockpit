---
task_id: B2-sessionpaths-migration
owner: agent-b2
status: ready
depends_on: [B1-sessionpaths-core]
---

# B2-sessionpaths-migration

## 1. Goal

Move existing rollout and scratchpad callers to `sessionpaths` without changing behavior or ownership.

## 2. Inputs

- B1 helper and golden tests.
- `internal/provider/codexapp/history_rollout.go`
- `internal/util/historyjsonl/history.go`
- `internal/module/thread/scratchpad.go`
- Existing tests:
  - `internal/provider/codexapp/history_rollout_path_test.go`
  - `internal/module/thread/phasef_scratchpad_test.go`
  - `internal/module/thread/stop_test.go`

## 3. Outputs

- Provider/codexapp rollout glob uses `sessionpaths` while preserving explicit opt-in fallback behavior.
- util/historyjsonl Codex discovery uses `sessionpaths` while preserving empty Codex home fallback to `~/.codex`.
- thread scratchpad path/sanitize/managed check uses `sessionpaths`.
- Literal placement guard added for rollout/scratchpad path fragments.

## 4. File Permissions

- RW: `internal/provider/codexapp/history_rollout.go`
- RW: `internal/provider/codexapp/history_rollout_path_test.go`
- RW: `internal/util/historyjsonl/history.go`
- RW: `internal/module/thread/scratchpad.go`
- RW: `internal/module/thread/phasef_scratchpad_test.go`
- RW: `internal/module/thread/stop_test.go`
- RW: `internal/archtest/sessionpaths_literal_guard_test.go`
- RO: `internal/contract/codex_identity.go`
- NO-TOUCH: provider home, skill mirror, runtime install roots.

## 5. Steps

1. Run existing focused tests before edits and record behavior.
2. Replace rollout glob construction in provider/codexapp with `sessionpaths`.
3. Replace Codex discovery glob construction in util/historyjsonl with `sessionpaths`, keeping its fallback semantics distinct.
4. Replace managed scratchpad path, sanitize, and managed check in thread with `sessionpaths`.
5. Add or update literal-placement guard so rollout glob fragments and managed scratchpad suffix live only in allowed production files.
6. Run focused tests and arch guard.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh ./internal/platform/sessionpaths ./internal/provider/codexapp ./internal/module/thread ./internal/util/historyjsonl -run 'Rollout|Scratchpad|Path|Cleanup|CodexHome|History' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Path|Provider|Thread' -count=1
```

## 7. DoD

- [ ] Provider/codexapp explicit Codex home semantics unchanged.
- [ ] util/historyjsonl empty home fallback to `~/.codex` unchanged.
- [ ] Scratchpad cleanup semantics unchanged.
- [ ] Literal-placement guard is separate from B1's dependency guard.
- [ ] No provider home or skill mirror responsibilities moved into sessionpaths.

## 8. Rollback

Revert only task-owned provider/util/thread/sessionpaths/archtest changes.
