---
task_id: L17
owner: worker-l17
status: planned
depends_on: []
---

# L17 logger relay and mcp-ida payload redaction

## 1. Goal
Redact relay attributes and avoid logging raw mcp-ida payloads.

## 2. Input
- Plan lane L17.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-17-logger-ida-redaction`.

## 3. Output
- Redaction tests and implementation.

## 4. File Permissions
- RW: `pkg/logger/relay.go`, `pkg/logger/redact.go`, logger tests, `cmd/mcp-ida/fx.go`, mcp-ida tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED: relay token/password/api_key redaction and mcp-ida config log excludes payload.
2. Reuse `sanitizeLogAttr`; log only scope/version/selector/payload_size/payload_hash.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./pkg/logger ./cmd/mcp-ida -count=1
```

## 7. DoD
- [ ] Sensitive relay attrs redacted.
- [ ] Raw payload not logged.

## 8. Boundary
Logger API changes beyond listed files require approval.

## 9. Rollback
Discard lane.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
