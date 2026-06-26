---
description: Fix confirmed core-domain review findings with four isolated worker agents.
workflow_key: 20260626-1224-core-review-fixes
---

# Core Review Fixes

## 1. Goal

- Business goal: remove confirmed fail-fast and state-consistency risks found in the core-domain review.
- Technical goal: add regression tests first, then fix only the owned Go packages for each lane.
- Acceptance goal: each lane passes its focused guard command, then integration guard passes over affected packages.

## 2. Scope

- In Scope: cron scheduling validation, turn durable dedupe errors, memory/prompt fail-fast behavior, skill subsystem truncation/home/import checks.
- Out of Scope: frontend changes, provider-wide refactors, store schema migrations, docs archive updates, unrelated review findings not assigned below.

## 3. Execution Topology

- Serial: P0 planning, PN integration.
- Parallel: P1 cron, P2 turn-memory-prompt, P3 skill, P4 contract-app.

## 4. DAG

See `DAG.json` for machine-readable state and `TASKS/` for task cards.

## 5. Current Status

- Overall: done
- Blockers: none
- Next step: review final diff and commit only task-owned files if desired.

## 6. Quick Navigation

- Ownership: `FILE_OWNERSHIP.tsv`
- Risks: `RISK_REGISTER.md`
- Gates: `CHECKS/RESULT_GATES.md`
- Evidence: `CHECKS/EVIDENCE.md`
- Handoff: `HANDOFF.md`
