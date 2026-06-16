# mcp-orch pos Phase 1 Feedback Loop

Date: 2026-06-01

## Scope

This round covers Phase 1 only: baseline inventory, shared `pos` parser, and read-only tool compatibility.

It does not cover mutation tools, complex DAG creation flattening, raw ops flattening, or full output envelope normalization. Those are later phases.

## Total Milestones

- M0 Baseline inventory and initial scoring.
- M1 Shared orch `pos` parser, structured `invalid_pos` / `pos_conflict` errors, parser tests.
- M2 Read-only tools accept `pos` while legacy fields stay compatible.
- M3 Mutation tools accept `pos`: `send_message`, `stop_agent`, DAG lifecycle and node update/dispatch.
- M4 Complex input simplification: `task_create_dag` and `task_dag_apply_ops` get flattened primary paths.
- M5 Output and hint normalization: compact list/result envelopes where safe.
- M6 Feedback-loop artifact automation: scorecards, issue ledger, adjudication, rescore templates.
- M7 Final verification, codemap/docs sync, migration notes, and release decision.

## Leadership Workflow Mapping

| Required step | Phase 1 artifact | Status |
| --- | --- | --- |
| Machine scoring | `scorecards/*.json` | Done for this round as three scorer views |
| Review | `adjudication.md` | Done |
| Write plan | `implementation-plan.md` | Done |
| Implementation | Code under `internal/sidecar/orch/tools` | Done |
| Machine review implementation | `review-after-implementation.md` | Done |
| Fix | Parser/schema/handler/test corrections listed in review | Done |
| Re-score calibration | `rescore.md` | Done |
| Feed remaining gaps to next round | `issues-ledger.json` | Done |

## Important Caveat

The initial code changes were made before these governance artifacts were written down. This directory backfills the visible evidence chain for Phase 1 and defines the standard that the next phases must follow before implementation starts.
