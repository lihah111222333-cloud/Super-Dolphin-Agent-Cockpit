# Super-Dolphin Full-Test Rerun Report

Date: 2026-05-22
Branch: `codex/full-test-remediation-20260522`
HEAD: `d7b070e2ee36fbb1e399ac52838e9aea4ab44a03`
Workspace: `D:\project\Super-Dolphin`
DB: `super_agent_v3_ai01_fulltest_20260522_rerun`
Log root: `D:\project\Super-Dolphin\docs\ai01-docs\test\logs\2026-05-22-rerun`

## Summary

本轮修复后，全量本地命令矩阵通过。`run-debug.ps1` 按 `1 -> 2` Server 模式启动成功，`4511/metrics` 和 `5173` 均返回 200，`super-agent-debug.exe` 监听 `4511/8090`，`mcp-orch.exe` 与 `mcp-lsp.exe` 存活并出现 listening 日志。

真实 provider smoke 为部分通过：Claude 完成 `thread/start -> turn/start -> turn/completed`，返回 `OK`；Codex 被本机环境阻塞，错误为 `exec: "codex": executable file not found in %PATH%`，未进入真实 turn。

## Environment Repairs

- MSYS2 installed at `D:\Configuration\msys64`; `make` and UCRT `gcc` are available through user PATH.
- Playwright real E2E runner now supports Windows `.cmd` execution and `PLAYWRIGHT_CHROMIUM_EXECUTABLE`.
- PostgreSQL isolated DB was recreated before service startup.
- Owner salt ACL was repaired for Windows and provider salt validation now uses platform-specific permission checks.

## Command Matrix

| Command | Status | Duration | Log |
| --- | --- | ---: | --- |
| `git status --short --branch` | PASS | 0.43s | `logs/2026-05-22-rerun/git_status.log` |
| `git rev-parse HEAD` | PASS | 0.39s | `logs/2026-05-22-rerun/git_rev_parse_HEAD.log` |
| `make guard` | PASS | 9.60s | `logs/2026-05-22-rerun/make_guard.log` |
| `go vet ./...` | PASS | 4.28s | `logs/2026-05-22-rerun/go_vet_all.log` |
| `make test` | PASS | 154.03s | `logs/2026-05-22-rerun/make_test.log` |
| `make build-plain` | PASS | 14.58s | `logs/2026-05-22-rerun/make_build_plain.log` |
| `make sqlc-verify` | PASS | 9.39s | `logs/2026-05-22-rerun/make_sqlc_verify.log` |
| `make codemap-check` | PASS | 2.13s | `logs/2026-05-22-rerun/make_codemap_check.log` |
| `node scripts/size-guard.cjs` | PASS | 0.31s | `logs/2026-05-22-rerun/frontend_size_guard.log` |
| `npx vitest run` | PASS | 10.87s | `logs/2026-05-22-rerun/frontend_vitest.log` |
| `npm run build` | PASS | 17.51s | `logs/2026-05-22-rerun/frontend_build.log` |
| `npm run test:e2e` | PASS | 54.22s | `logs/2026-05-22-rerun/frontend_e2e_mock.log` |
| `npm run test:e2e:real -- --skip-build --base-url=http://127.0.0.1:4511` | PASS, 2 skipped | 6.06s | `logs/2026-05-22-rerun/frontend_e2e_real.log` |
| Service health checks | PASS | 3.30s | `logs/2026-05-22-rerun/service-health.log` |
| Provider smoke | PARTIAL | 186.20s | `logs/2026-05-22-rerun/provider-smoke.log` |

Raw matrix: `logs/2026-05-22-rerun/matrix-results.ndjson`.

## Service Verification

- `http://127.0.0.1:4511/metrics`: 200, response length 9746.
- `http://localhost:5173`: 200, response length 732.
- Listening ports:
  - `5173`: `node.exe`
  - `4511`: `super-agent-debug.exe`
  - `8090`: `super-agent-debug.exe`
- Required log markers found:
  - `db pool ready`
  - `http asset server listening`
  - `mcp-orch http: listening`
  - `mcp-lsp http: listening`

## Provider Smoke

- Codex: `thread/start` failed before turn execution because `codex` is not on PATH. This is an environment/tool installation blocker, not a provider salt failure.
- Claude: `thread/start` succeeded for `agent_1779453046647339000`; `turn/start` returned `turn_1779453051371_e3942a22d763f9a9`; `turn/completed` succeeded with message `OK`.

## Notable Fixes Covered

- Frontend size guard violations were refactored without changing thresholds or baseline.
- Windows path/root tests now use shared containment normalization, JSON-marshaled roots, trusted tool scope, and precise symlink privilege skips.
- Provider salt permission validation is platform-specific: POSIX keeps mode checks; Windows validates ACLs and rejects broad principals.
- SQLC EOL consistency is pinned by `.gitattributes`; SQLC and codemap checks pass.
- `run-real-e2e.cjs` now fails on spawn errors and can invoke Playwright `.cmd` correctly on Windows.
- Flaky async tests were made deterministic by adding explicit test synchronization rather than weakening guards.

## Residual Risks

- Real E2E command is runnable, but both `chat-refresh.real.spec.js` scenarios skipped in Server-mode browser because the current spec requires Wails-style real backend bridge/history preconditions.
- Codex provider smoke is blocked until `codex` CLI is installed or added to PATH for the service process.
- Multiple `mcp-orch.exe` and `mcp-lsp.exe` processes were observed after startup; active listening markers are present, but stale peer cleanup should be reviewed separately.
- Worktree remains intentionally dirty with remediation changes and generated/report artifacts; this report does not imply a clean commit-ready state.
