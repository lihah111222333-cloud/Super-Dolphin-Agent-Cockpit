# Evidence

## Controller
- 2026-06-26 14:44 KST: `git status --short` showed only `?? docs/plans/2026-06-26-production-reachable-risk-remediation.md` before dispatch.
- 2026-06-26 14:44 KST: `.worktrees/` exists and `git check-ignore -v .worktrees` confirmed `.gitignore:91:/.worktrees/`.
- 2026-06-26 14:44 KST: base ref is `main@023f759b`.
- 2026-06-26 14:44 KST: created 18 worktrees and 18 branches under `.worktrees/risk-fix-lane-*`.
- 2026-06-26 14:44 KST: spawned 18 blank-context workers with `fork_context:false`; agent ids are recorded in `STATE.json`.
- 2026-06-26 14:xx KST: L02 requested write-set expansion for retry hard-cap parameterization. Controller inspected `task_dag_wakeup_dispatch.sql`, generated sqlc, `RetryWakeupInput`, `store_wakeup.go`, and `wakeup_dispatcher.go`; approved only `cmd/mcp-orch/store/taskdag/contract.go`, `cmd/mcp-orch/store/taskdag/store_wakeup.go`, `cmd/mcp-orch/store/sqlc/task_dag_wakeup_dispatch.sql.go`, and `cmd/mcp-orch/orchestration/wakeup_dispatcher.go`.
- 2026-06-26 14:xx KST: L09 requested generated sqlc expansion. Controller inspected `sql/queries/audit_log.sql` and `internal/store/sqlc/audit_log.sql.go`; approved only `internal/store/sqlc/audit_log.sql.go` as generated output for audit extra roundtrip.

## Lane Evidence
Workers must append or report:
- RED command, exit code, expected failure summary.
- GREEN command, exit code, pass summary.
- Per-file guard commands and exit codes.
- Lane verification command and exit code.

## Controller Verification Summary
- 2026-06-26 15:55 KST: All 18 blank-context workers reported DONE or DONE_WITH_CONCERNS; no lane remains waiting for approval.
- 2026-06-26 15:55 KST: No lane was staged, committed, pushed, or merged into `main`.
- 2026-06-26 15:55 KST: Workers reported RED/GREEN evidence and per-changed-Go-file guard evidence for their changed Go files. Controller additionally re-ran per-file guard loops for L01, L02, L03, L04, and L11 after their final diffs returned.
- L01 controller gate: `./scripts/test_with_guard.sh ./cmd/mcp-lsp/... -count=1` exit 0; `git diff --check` exit 0. Controller confirmed grep limit-hit responses keep `truncated=true` and hint via the search-layer `limitReached` marker.
- L02 controller gate: `./scripts/test_with_guard.sh ./cmd/mcp-orch/... -count=1` exit 0; `./scripts/test_with_guard.sh ./cmd/mcp-orch/orchestration ./cmd/mcp-orch/store/taskdag ./cmd/mcp-orch/store/sqlc -count=1` exit 0; `git diff --check` exit 0. `make sqlc-generate` exit 0 and approved sqlc/query diff hash stayed `9032348a78e0f3070a20534320e5a3ae01599fe68a33e07142e63a5965755161`.
- L03 controller gate: `./scripts/test_with_guard.sh ./cmd/mcp-orch/workspace ./cmd/mcp-orch/store/sharedfile ./cmd/mcp-orch/tools ./internal/platform/db/sqlite -count=1` exit 0; `./scripts/test_with_guard.sh ./cmd/mcp-orch/store/sqlc ./internal/store/sqlc -count=1` exit 0; `git diff --check` exit 0. `make sqlc-generate` exit 0 and approved sqlc/query diff hash stayed `d9495dd85e66ed0288fbadef2014d3fbab5cf6b50d175fdd3bea4ce3021dcc3a`.
- L04 controller gate: `./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1` exit 0; `git diff --check` exit 0.
- L05 controller gate: `./scripts/test_with_guard.sh ./internal/provider/claudecli -count=1` exit 0; `git diff --check` exit 0.
- L06 controller gate: `./scripts/test_with_guard.sh ./internal/provider/unified -count=1` exit 0; `git diff --check` exit 0.
- L07 controller gate: `./scripts/test_with_guard.sh ./internal/module/cron ./internal/module/appupdate -count=1` exit 0.
- L08 controller gate: `./scripts/test_with_guard.sh ./internal/module/datasource_v2 ./internal/module/datasource ./internal/module/mcp_server ./internal/platform/httpegress -count=1` exit 0; `git diff --check` exit 0.
- L09 controller gate: `./scripts/test_with_guard.sh ./internal/store/auditlog ./internal/store/dbquery ./internal/store/insight ./internal/module/dashboard -count=1` exit 0; `git diff --check` exit 0. `make sqlc-generate` exit 0 and approved generated diff hash stayed `f699f26764df2f4944fd4df7084b709977538f0213e35d2483130401ec777b31`.
- L10 controller gate: `./scripts/test_with_guard.sh ./internal/platform/rpc ./internal/platform/hooks ./internal/platform/mcpcontrol ./internal/mcpserver/common ./internal/platform/runner -count=1` exit 0; `./scripts/test_with_guard.sh ./internal/platform/toolbridge -count=1` exit 0; `git diff --check` exit 0.
- L11 controller gate: `./scripts/test_with_guard.sh ./internal/platform/config ./internal/platform/cachekeepalive ./internal/platform/pidregistry ./internal/provider/codexapp -count=1` exit 0; `git diff --check` exit 0. Controller confirmed codexapp production child-process registration paths use `RegisterChecked`.
- L12 controller gate: `./scripts/test_with_guard.sh ./internal/ui/wails -count=1` exit 0; `./scripts/test_with_guard.sh ./scripts -run 'TestNewUIDesktopDev' -count=1` exit 0; `bash -n run-new-ui-desktop.sh` exit 0.
- L13 controller gate: `npm test -- src/entities/client/model/runtimeSlice.test.js src/shared/api/wailsBridge.test.js src/entities/client/model/threadLifecycleRuntime.test.js` exit 0; `npm run lint` exit 0; `npm run build` exit 0.
- L14 controller gate: `./scripts/test_with_guard.sh ./internal/module/thread -count=1` exit 0; `git diff --check` exit 0.
- L15 controller gate: `./scripts/test_with_guard.sh ./internal/module/memory/... -count=1` exit 0; `git diff --check` exit 0.
- L16 controller gate: `./scripts/test_with_guard.sh ./internal/module/skill/... -count=1` exit 0; `git diff --check` exit 0.
- L17 controller gate: `./scripts/test_with_guard.sh ./pkg/logger ./cmd/mcp-ida -count=1` exit 0.
- L18 controller gate: `./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -count=1` exit 0.

## Known Verification Notes
- `make sqlc-verify` was run in L02, L03, and L09 but exits non-zero while approved generated files remain dirty against `HEAD`; controller instead verified `make sqlc-generate` stability by before/after diff hashes. Re-run `make sqlc-verify` after staging/committing or replaying the generated changes into an integration branch.
- Full repository integration gates (`make guard`, `make test`, `make build-plain`, frontend full gates after merge) have not been run because no lane has been merged into a common integration branch.
