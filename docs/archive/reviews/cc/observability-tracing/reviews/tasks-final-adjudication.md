# Final Adjudication - Observability Tracing Task Docs

Date: 2026-06-01

## Scope

This adjudication covers the 10 implementation task documents under:

```text
docs/cc/observability-tracing/tasks/
```

Inputs reviewed:

- `docs/cc/observability-tracing/00-implementation-plan.md`
- `docs/cc/observability-tracing/reviews/tasks-review-a-architecture-dependencies.md`
- `docs/cc/observability-tracing/reviews/tasks-review-b-privacy-volume-frontend.md`
- the corrected task docs `01` through `10`

## Final verdict

**Approved for execution as a DAG task package, with the already-applied corrections kept.**

The task docs are not code-complete implementation. They are approved as implementation instructions for future coding agents. Any code implementation must still follow the validation commands in the individual task docs and the final closeout validation in Task 10.

## Accepted findings

### ADJ-1: Accepted - frontend ingest ownership had a real dependency inversion

- **Source**: Review A finding 1.
- **Severity**: High.
- **Decision**: Accepted.
- **Reasoning**: Task 06 requires React remote trace flushing through `observability/frontend/ingest`, but the original Task 10 placement made ingest available only after Task 06. That is a real dependency inversion.
- **Applied correction**:
  - Task 04 now owns first creation and registration of `observability/frontend/ingest { events }`.
  - Task 06 now depends on Task 04's ingest handler and explicitly does not depend on Task 10 for first ingest availability.
  - Task 10 now verifies and extends the ingest contract instead of first introducing it.
- **Affected docs**:
  - `tasks/04-fx-wiring-disabled-service.md`
  - `tasks/06-react-frontend-trace-emitter.md`
  - `tasks/10-dashboard-query-ui-verification-docs.md`

### ADJ-2: Accepted and deduplicated - final closeout validation was incomplete

- **Source**: Review A finding 2 and Review B finding 1.
- **Severity**: Medium.
- **Decision**: Accepted as one deduplicated finding.
- **Reasoning**: Task 10 depends on all prior implementation tasks, but the original final validation omitted surfaces modified by Task 04, Task 08, and Task 09. Final closeout must cover or cite validation for app wiring, bus, provider, toolbridge, and difftracker surfaces.
- **Applied correction**:
  - Task 10 validation now includes:
    - `./internal/app`
    - `./internal/platform/toolbridge`
    - `./internal/platform/difftracker`
    - `./internal/provider/...`
    - `./internal/platform/bus`
  - Task 10 allows narrowing provider package validation only if the exact touched provider package list is recorded in the closeout report.
- **Affected docs**:
  - `tasks/10-dashboard-query-ui-verification-docs.md`

## Rejected or no-action items

No additional reviewer comments were adopted.

The following categories were reviewed and produced no high-confidence actionable issue:

- platform/application module ownership beyond ADJ-1;
- explicit Fx wiring approach using `internal/app/modules.go`;
- `rpc.HandlerMapResult` registration approach;
- React `frontend-app` target truthfulness;
- JSONL path, permissions, retention, and packaging constraints;
- privacy and forbidden payload field coverage;
- bounded memory index and log-volume/OOM controls;
- Phase 1 constraint to avoid PostgreSQL/sqlc/SQLite expansion.

These are treated as **no-change decisions**, not as proof that future implementation code is correct.

## Remaining execution conditions

Before implementation starts:

1. Keep Task 04 before Task 06 in the DAG because Task 06 depends on the ingest RPC contract.
2. Keep Task 10 as the final closeout task after all prior tasks.
3. Do not add PostgreSQL tables, sqlc queries, or SQLite dependencies in Phase 1.
4. Do not use the legacy Vue log bridge as evidence that React remote trace flushing exists.
5. Any implementation agent must run the validation command in its own task doc before handoff.
6. Task 10 must run the broadened integration validation or explicitly cite prior successful validations plus rerun tests for packages touched during closeout.

## Execution readiness

The 10 task docs are now ready to be used as the implementation DAG source, subject to normal code-review and test gates during implementation.
