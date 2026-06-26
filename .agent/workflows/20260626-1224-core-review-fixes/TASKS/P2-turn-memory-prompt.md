---
task_id: P2-turn-memory-prompt
owner: agent-2
status: done
depends_on: [P0-plan]
---

# P2-turn-memory-prompt

## 1. Goal

Remove silent failures in turn durable dedupe, memory root resolution/prefetch/history, and prompt enable_when parsing.

## 2. Inputs

- Review findings in `internal/module/turn/service.go`, `internal/module/memory/service.go`, `retrieval/prefetch.go`, `rules_provider.go`, and `internal/module/prompt/enable_when.go`.

## 3. Outputs

- Failing tests first, then minimal production changes.
- Explicit errors or fail-closed behavior where current code silently falls back.

## 4. File Permissions

- RW: `internal/module/turn/`, `internal/module/memory/`, `internal/module/prompt/`
- RO: `internal/contract/`, `internal/store/`
- NO-TOUCH: cron, skill, app, provider, frontend.

## 5. Steps

1. Add a failing turn test proving dedupe upsert/provider ID errors abort or surface.
2. Add failing memory/prompt tests for AutoMem path failure, prefetch error exposure, ReadHistory error exposure, and bad enable_when behavior.
3. Implement the smallest behavior changes.
4. Run file guards and focused package guards.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh internal/module/turn/service.go
./scripts/test_with_guard.sh internal/module/memory/service.go
./scripts/test_with_guard.sh internal/module/memory/retrieval/prefetch.go
./scripts/test_with_guard.sh internal/module/memory/rules_provider.go
./scripts/test_with_guard.sh internal/module/prompt/enable_when.go
./scripts/test_with_guard.sh ./internal/module/turn ./internal/module/memory ./internal/module/prompt -count=1
```

## 7. DoD

- [ ] RED failures observed.
- [ ] New behavior matches fail-fast policy.
- [ ] No edits outside owned module directories.
- [ ] Focused guards pass or blocker is reported.

## 8. Edge Cases

- Existing compatibility tests may assert fail-open; update only with clear policy evidence.
- Avoid changing public prompt APIs unless necessary.

## 9. Rollback

Revert owned directories only.

## 10. Evidence

Append command summaries to `CHECKS/EVIDENCE.md` via orchestrator.
