# Phase 1 Adjudication

## Decision Summary

The three scorer views agree that Phase 1 should stay narrow: add a unified selector contract for read-only tools without changing service-layer behavior.

## Must Fix In This Round

- Add a shared `pos` parser.
- Support `pos` on read-only tools.
- Keep old fields working.
- Reject conflicting `pos` and legacy selectors.
- Add contract tests for grammar, schema, and handlers.

## Accepted For Later Rounds

- Mutation tools still use legacy selectors. This is M3.
- `task_create_dag` and `task_dag_apply_ops` still have complex nested primary inputs. This is M4.
- Output envelope normalization is not complete. This is M5.
- Full multi-agent scoring automation is not yet implemented. This is M6.

## Rationale

This round deliberately avoids changing persistence, orchestration service contracts, or node execution behavior. That keeps the first slice low risk while still reducing AI input reasoning cost for common read paths.
