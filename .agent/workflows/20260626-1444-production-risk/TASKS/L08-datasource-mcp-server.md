---
task_id: L08
owner: worker-l08
status: planned
depends_on: []
---

# L08 datasource and MCP server safety boundaries

## 1. Goal
Constrain datasource imports/PDF/text/list limits, MCP stdio/sqlite boundaries, and HTTP egress redirect/IP policy.

## 2. Input
- Plan lane L08.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-08-datasource-mcp-server`.

## 3. Output
- Six RED test groups and minimal fail-fast implementation.

## 4. File Permissions
- RW: `internal/module/datasource_v2/`, `internal/module/datasource/`, `internal/module/mcp_server/`, `internal/platform/httpegress/` listed source and tests.
- NO-TOUCH: dashboard/store SQL limit work owned by L09.

## 5. Steps
1. RED: workspace path escape, PDF decompression cap, datasourceV2 list oversized limit, npx argv bypass, sqlite arbitrary path, redirect-to-localhost.
2. Add import limits, exact stdio argv validator, fixed product DB sqlite server, per-hop redirect and resolved-IP deny.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/module/datasource_v2 ./internal/module/datasource ./internal/module/mcp_server ./internal/platform/httpegress -count=1
```

## 7. DoD
- [ ] Out-of-workspace imports rejected.
- [ ] Oversize parsing/list rejected.
- [ ] MCP/egress bypasses rejected.

## 8. Boundary
Shared dashboard/store changes require `NEEDS_APPROVAL`.

## 9. Rollback
Discard branch/worktree.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
