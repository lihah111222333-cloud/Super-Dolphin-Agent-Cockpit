---
task_id: L12
owner: worker-l12
status: planned
depends_on: []
---

# L12 Wails host bridge/proxy/clipboard

## 1. Goal
Restrict `VITE_DEV_URL` to loopback http/https and preserve clipboard whitespace.

## 2. Input
- Plan lane L12.
- Worktree: `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/risk-fix-lane-12-wails-host`.

## 3. Output
- Wails/script tests and implementation.

## 4. File Permissions
- RW: `internal/ui/wails/assets.go`, `internal/ui/wails/rpc.go`, `run-new-ui-desktop.sh`, `run-new-ui-desktop.ps1`, `internal/ui/wails/*_test.go`, `scripts/*new_ui_desktop*_test.go`.
- NO-TOUCH: frontend app files.

## 5. Steps
1. RED: `TestViteDevProxyRejectsNonLoopbackURL`, `TestCopyTextPreservesLeadingAndTrailingWhitespace`.
2. Add shared loopback validation and remove clipboard `TrimSpace` behavior.
3. Guard every changed Go file.

## 6. Verification
```bash
./scripts/test_with_guard.sh ./internal/ui/wails -count=1
./scripts/test_with_guard.sh ./scripts -run 'TestNewUIDesktopDev' -count=1
```

## 7. DoD
- [ ] Non-loopback dev URLs rejected in app and scripts.
- [ ] Clipboard text preserved except empty check.

## 8. Boundary
Frontend runtime files require `NEEDS_APPROVAL`.

## 9. Rollback
Discard lane.

## 10. Evidence
Report RED/GREEN, guards, verification, files, approvals, residual risk.
