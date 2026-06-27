---
description: 18 lane production reachable risk remediation controller ledger.
workflow_key: 20260626-1444-production-risk
---

# Production Reachable Risk Remediation

## 1. Goal
- Business: repair adjudicated production-reachable risks from the 20-agent review.
- Technical: run 18 isolated repair lanes with non-overlapping write sets.
- Acceptance: each lane supplies RED, GREEN, per-file guard evidence, final lane verification, changed-file list, and residual risk notes.

## 2. Scope
- In Scope: lanes L01-L18 from `docs/plans/2026-06-26-production-reachable-risk-remediation.md`.
- Out of Scope: Agent19, unrelated refactors, write-set expansion without controller approval, broad cleanup.

## 3. Topology
- Controller: current main worktree. Creates worktrees, assigns lanes, reviews scope expansion requests, integrates later.
- Parallel: L01-L18 each runs in its own branch and worktree.
- Serial later: controller review, lane-by-lane merge, final repository verification.

## 4. DAG
See `DAG.json` for machine-readable state and `TASKS/` for lane cards.

## 5. Current State
- Overall: dispatching
- Blockers: no mcp-go-agent-orchestration tools are exposed; using document ledger plus Codex blank-context workers.
- Next step: create worktrees and spawn one worker per lane with `fork_context:false`.

## 6. Quick Links
- File ownership: `FILE_OWNERSHIP.tsv`
- Risk register: `RISK_REGISTER.md`
- Evidence log: `CHECKS/EVIDENCE.md`
- Result gates: `CHECKS/RESULT_GATES.md`
- Handoff: `HANDOFF.md`
