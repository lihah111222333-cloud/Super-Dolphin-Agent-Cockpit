# AI Maintenance Boundaries P0 Baseline

> Source orchestration: [2026-07-07-ai-maintenance-boundaries-parallel-agents.md](./2026-07-07-ai-maintenance-boundaries-parallel-agents.md). This file records `P0-controller-baseline` only. It does not start implementation agents and does not authorize production source edits.

## Baseline

| Field | Value |
|---|---|
| Captured at | 2026-07-07 17:48:59 KST |
| Branch | `main` |
| Upstream | `origin/main` |
| Base commit | `ae5565f3f499040127fca7d3c28591dd43fb63d8` |
| Base commit short | `ae5565f3f` |
| Base commit title | `docs: 编排 AI 维护边界并行代理执行` |
| Controller package | `P0-controller-baseline` |
| Controller status | `DONE_WITH_EVIDENCE` |
| Implementation agents launched | None |
| Production source files changed by P0 | None |

## Preserved Unrelated Dirty Files

These paths existed before P0 and are not owned by this baseline:

| Status | Path |
|---|---|
| `M` | `docs/ai01-docs/project-sop/README.md` |
| `??` | `docs/ai01-docs/project-sop/09-code-anti-rot-guard-guide.md` |
| `??` | `docs/ai01-docs/月度工作汇报/2026-06-hai-architecture-fix-monthly-report.md` |
| `??` | `docs/ai01-docs/月度工作汇报/2026-07-06-hai-work-report.docx` |
| `??` | `docs/plans/2026-07-06-20agent-full-review-remediation.md` |

## Source Plan State

| Source path | P0 observation |
|---|---|
| `docs/plans/2026-07-07-ai-maintenance-boundaries.md` | Tracked and clean at base commit. |
| `docs/plans/2026-07-07-ai-maintenance-boundaries-parallel-agents.md` | Tracked and clean at base commit. |

## Owner Table

| Package | Role | Current status | Next dispatch gate |
|---|---|---|---|
| `P0-controller-baseline` | Controller | `DONE_WITH_EVIDENCE` | This baseline file and self-check are present. |
| `A1-config-profile` | Go dependency-profile worker | `READY` | Can start after controller explicitly dispatches Wave 1. |
| `C0-provider-prep` | Provider read-only prep worker | `READY` | Can start with A1 in Wave 1. |
| `B1-frontend-surface` | Frontend boundary worker | `WAITING` | Start only after A1 opens the first RED guard. |
| `A2-app-runtime` | Go app/provider runtime-report worker | `BLOCKED` | Wait for A1 completion. |
| `A3-thread-bind` | Go thread worker | `BLOCKED` | Wait for A1 completion. |
| `A4-toolbridge-contract` | Go toolbridge worker | `BLOCKED` | Wait for A1 completion. |
| `B2a-files-memory` | Frontend page-service worker | `BLOCKED` | Wait for B1 completion. |
| `B2b-observability-prompts` | Frontend page-service worker | `BLOCKED` | Wait for B1 completion. |
| `A5-lane-a-integrator` | Go Fx graph integrator | `BLOCKED` | Wait for A2, A3, and A4 completion. |
| `B3-frontend-dto-golden` | Frontend contract-test worker | `BLOCKED` | Wait for B2a and B2b completion. |
| `C1-contracttest-harness` | Provider contract harness worker | `BLOCKED` | May prep read-only, but must not write until A5 completes. |
| `C2u-unified-provider` | Unified provider worker | `BLOCKED` | Wait for C1 completion. |
| `C2c-claudecli-provider` | Claude provider contract worker | `BLOCKED` | Wait for C1 completion and A2 provider runtime-report hunks green. |
| `C2x-codexapp-provider` | Codex provider contract worker | `BLOCKED` | Wait for C1 completion and A2 provider runtime-report hunks green. |
| `C3-provider-template` | Provider scaffold worker | `BLOCKED` | Wait for C2u, C2c, and C2x completion. |
| `R-wave-review` | Five independent read-only reviewers | `BLOCKED` | Run after Wave 2, after Wave 4, and before final integration. |
| `I-final-integration` | Controller | `BLOCKED` | Wait for all implementation packages and required review rings. |

## P0 Self-Check

| Check | Result | Evidence |
|---|---|---|
| Starting point frozen | `PASS` | `git rev-parse HEAD`, `git status -sb`, and `git status --short --untracked-files=all` were captured for base commit `ae5565f3f`. |
| Unrelated dirty files identified | `PASS` | The preserved unrelated dirty table lists all current modified and untracked paths observed before P0. |
| Source plan files clean before P0 | `PASS` | `git diff --name-only -- docs/plans/2026-07-07-ai-maintenance-boundaries.md docs/plans/2026-07-07-ai-maintenance-boundaries-parallel-agents.md` returned no dirty plan paths. |
| Owner table written | `PASS` | The owner table above maps every dispatch package from the companion document to a P0 status and next gate. |
| Production source untouched | `PASS` | P0 writes only this docs baseline artifact; no `internal/**`, `cmd/**`, or `frontend-app/**` files are owned or changed by P0. |
| Implementation agent dispatch | `PASS` | No implementation or review agents were launched in P0. |
