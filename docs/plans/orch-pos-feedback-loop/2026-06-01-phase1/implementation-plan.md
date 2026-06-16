# Phase 1 Implementation Plan

## Goal

Make read-only orch tools accept a single flattened `pos` selector while preserving all legacy selector fields.

## Steps

1. Add `internal/sidecar/orch/tools/pos.go`.
2. Define supported selectors:
   - `agent:<agent_id>`
   - `dag:<dag_key>`
   - `run:<run_key>`
   - `dag:<dag_key>/run:<run_key>`
   - `dag:<dag_key>/run:<run_key>/node:<node_key>`
   - `workspace:<run_key>`
   - `shared:<path>`
   - `prompt:<prompt_key>`
   - `command:<card_key>`
3. Add resolver helpers for each legacy field.
4. Update read-only handler inputs to include `pos`.
5. Update read-only tool schemas to expose `pos` and remove legacy selector requirements.
6. Add tests for parser, invalid selectors, schema exposure, handler usage, and conflicts.
7. Run focused tests.

## Out Of Scope

- Mutation tool `pos` support.
- DAG creation / apply ops flattening.
- Result envelope normalization.
- Full scoring automation.
