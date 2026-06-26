---
task_id: L09
owner: worker-l09
status: planned
depends_on: []
---

# L09 dashboard/store/sql query boundaries

## 1. Goal
Return audit `extra`, execute normalized SQL, and reject oversized dashboard insight limits.

## 2. Input
- Plan lane L09.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-09-dashboard-store-sql`.

## 3. Output
- SQL/store/dashboard tests and implementation.

## 4. File Permissions
- RW: `sql/queries/audit_log.sql`, `sql/queries/session_insight.sql`, `internal/store/auditlog/`, `internal/store/dbquery/`, `internal/store/insight/`, `internal/module/dashboard/`.
- Approved expansion after `NEEDS_APPROVAL`: `internal/store/sqlc/audit_log.sql.go` only as generated output from `sql/queries/audit_log.sql`.
- NO-TOUCH: datasourceV2 list limit owned by L08.

## 5. Steps
1. RED: audit extra roundtrip, dbquery actual SQL includes normalized LIMIT, insight oversized limit rejected.
2. Update sqlc queries and run sqlc verification.
3. Guard every changed Go file.

## 6. Verification
```bash
make sqlc-verify
./scripts/test_with_guard.sh ./internal/store/auditlog ./internal/store/dbquery ./internal/store/insight ./internal/module/dashboard -count=1
```

## 7. DoD
- [ ] SQL generated code consistent.
- [ ] Oversized insight list limit returns invalid argument.

## 8. Boundary
Generated sqlc files not listed in the plan require `NEEDS_APPROVAL` before editing or committing.

Approved generated-file expansion is limited to `internal/store/sqlc/audit_log.sql.go`; any other sqlc output requires a new approval request.

## 9. Rollback
Discard lane branch/worktree.

## 10. Evidence
Report RED/GREEN, sqlc, guards, verification, files, approvals, residual risk.
