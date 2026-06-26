---
task_id: L01
owner: worker-l01
status: planned
depends_on: []
---

# L01 MCP LSP transport/search/installer/diagnostics

## 1. Goal
Fix LSP request write cancellation, installer timeout, unsupported diagnostics, and search result caps.

## 2. Input
- Plan lane L01.
- Start in `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-01-mcp-lsp`.

## 3. Output
- RED/GREEN tests and minimal implementation.

## 4. File Permissions
- RW: `cmd/mcp-lsp/multilsp/transport.go`, `cmd/mcp-lsp/multilsp/transport_conn.go`, `cmd/mcp-lsp/installer/installer.go`, `cmd/mcp-lsp/manager/registry.go`, `cmd/mcp-lsp/search/searchutil.go`, matching package `*_test.go`.
- NO-TOUCH: all other files unless `NEEDS_APPROVAL`.

## 5. Steps
1. Add RED tests: `TestTransportRequestWriteHonorsContext`, search max/cancel tests, installer timeout, diagnostics unsupported.
2. Run RED commands and record expected failures.
3. Implement cancellable writes, installer deadline, unsupported error envelope, and search limiter cancellation.
4. After each changed Go file, run `./scripts/test_with_guard.sh <file.go>`.
5. Run lane verification.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./cmd/mcp-lsp/... -count=1
```

## 7. DoD
- [ ] RED and GREEN evidence reported.
- [ ] Guard run for every changed Go file.
- [ ] Changed files stay inside RW set.

## 8. Boundary
If transport process lifecycle changes require unlisted files, stop with `NEEDS_APPROVAL`.

## 9. Rollback
Leave branch/worktree intact; controller can drop the branch if not integrated.

## 10. Evidence
Report commands, exit codes, modified files, approvals, and residual risk.
