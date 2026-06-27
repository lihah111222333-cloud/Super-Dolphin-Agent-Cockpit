---
task_id: L10
owner: worker-l10
status: planned
depends_on: []
---

# L10 platform queues, stdio transport, runner shutdown

## 1. Goal
Bound platform fanout queues, cap MCP stdio message size, and use fresh runner shutdown context.

## 2. Input
- Plan lane L10.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-10-platform-queues`.

## 3. Output
- Queue overflow, stdio cap, and runner stop tests/implementation.

## 4. File Permissions
- RW: `internal/platform/rpc/push_worker.go`, `internal/platform/hooks/dispatch_worker.go`, `internal/platform/mcpcontrol/config_fanout_worker.go`, `internal/mcpserver/common/stdio.go`, `internal/platform/toolbridge/stdio_mcp_client.go`, `internal/platform/runner/contract.go`, matching tests.
- NO-TOUCH: other files.

## 5. Steps
1. RED: queue overflow tests, stdio `Content-Length` oversize test, `TestWorkerRunnerStopUsesFreshShutdownContext`.
2. Add bounded latest-only queues/degraded event, framed/raw caps, fresh shutdown ctx.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/platform/rpc ./internal/platform/hooks ./internal/platform/mcpcontrol ./internal/mcpserver/common ./internal/platform/runner -count=1
```

## 7. DoD
- [ ] Unbounded queues removed.
- [ ] Oversize MCP input rejected before allocation.
- [ ] Stop preserves drain timeout error.

## 8. Boundary
Metrics or shared constants outside RW set require approval.

## 9. Rollback
Discard lane.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
