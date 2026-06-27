# Context

## Background
The plan at `docs/plans/2026-06-26-production-reachable-risk-remediation.md` lists 18 independent repair lanes for production-reachable risk. The main repository worktree is only a controller. Workers must edit inside dedicated worktrees.

## Hard Constraints
- Each lane has a dedicated branch and worktree named `codex/risk-fix-lane-XX-*`.
- Workers must not edit outside the lane write set.
- Any write-set expansion must stop with `NEEDS_APPROVAL` and wait for the main agent.
- TDD is mandatory: write failing tests first, capture RED failure, then implement.
- After every changed Go file, run `./scripts/test_with_guard.sh <file.go>` before continuing.
- No silent fallback: invalid config, out-of-scope paths, missing resources, storage errors, oversize inputs, and policy violations must return explicit errors or degraded state.
- Final lane reports must include RED command/failure, GREEN command/pass, per-file guard commands, lane verification command, modified files, approvals, and residual risk.

## Non-Goals
- No broad formatting.
- No opportunistic refactors.
- No generated baseline freeze.
- No main-worktree implementation.

## Tooling Note
The repository AGENTS policy allows native sub-agent dispatch directly. This workflow uses `DAG.json`, `STATE.json`, and Codex blank-context worker agents as its controller ledger. Use `mcp-go-agent-orchestration` only when persistent DAG state, retry/lease semantics, or structured cross-agent handoff records are specifically needed.
