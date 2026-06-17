# Repo Audit: codex/feature-integration-20260617

Date: 2026-06-17

Worktree: `D:\project\Super-Dolphin-worktrees\feature-integration-20260617`

Branch: `codex/feature-integration-20260617`

Base: `main` at `809776e12182f1666b6f23c78f79c1870cd6fec2`

## Scope

This audit looked for bugs and operational blockers visible on the current branch. The branch currently has no tracked source-code diff against `main`, so the scan focused on:

- branch/startup behavior in a fresh worktree
- repository guard and frontend checks
- desktop runtime readiness
- dependency audit output
- concrete issues that can be reproduced from commands or logs

Generated and ignored runtime/build files were not treated as source changes.

## Executive Summary

No failing lint, test, build, or Go guard result was found in the initial branch audit. The branch was then updated to fix the clean-worktree startup blocker and dependency audit findings.

The fixed branch has been verified with targeted Go guard/tests, frontend lint/tests/build, PowerShell syntax parsing, and `npm audit`.

Latest dependency audit result:

- `vite`: `8.0.16`
- `dompurify`: `3.4.10`
- `npm audit --json`: `total = 0`

The branch was previously proven running with the new UI desktop stack on:

- frontend: `http://127.0.0.1:5175`
- backend metrics: `http://127.0.0.1:4512/metrics`
- control RPC: `127.0.0.1:8092`

The startup entrypoint has also passed a post-fix runtime check. If the later security scan changes code again, rerun the startup check before final handoff.

## Findings

### P1 - Clean worktree desktop startup can fail before backend readiness

Status: fixed in this branch

Area: Windows desktop startup

Files:

- `run-new-ui-desktop.ps1`
- `cmd/agent-terminal/frontend.go`
- `Makefile`

Observed behavior:

Starting `run-new-ui-desktop.ps1` in the new worktree failed on the first attempt after Vite became ready. The backend exited before `http://127.0.0.1:4512/metrics` became ready.

Evidence from `backend.err.log`:

```text
cmd\agent-terminal\frontend.go:13:12: pattern all:frontend/dist: no matching files found
```

Relevant source evidence:

- `cmd/agent-terminal/frontend.go:13` embeds `all:frontend/dist`, so Go compilation requires `cmd/agent-terminal/frontend/dist` to exist.
- `run-new-ui-desktop.ps1:626-657` ensures frontend dependencies and peer binaries, then starts Vite and runs `go run ./cmd/agent-terminal`.
- `Makefile:60-62` shows the expected build path: `frontend-app` build plus sync into the embed directory before Go compilation.

Impact:

A fresh or cleaned worktree can fail to start even though `frontend-app/node_modules` exists and Vite itself is healthy. This blocks the advertised Windows startup entrypoint unless the developer manually runs `npm run build` and `node scripts/sync-frontend-dist.mjs` first.

Suggested fix:

Add a small preflight in `run-new-ui-desktop.ps1` before `go run ./cmd/agent-terminal`:

- if `cmd/agent-terminal/frontend/dist/index.html` is missing or stale, run the existing `frontend-app` build and `frontend-app/scripts/sync-frontend-dist.mjs`
- fail fast if the sync does not produce `cmd/agent-terminal/frontend/dist/index.html`
- keep the existing Vite dev server flow intact

Implemented fix:

- `run-new-ui-desktop.ps1` now checks whether `cmd\agent-terminal\frontend\dist\index.html` is missing or stale before the backend starts.
- When missing or stale, it runs the existing `frontend-app` build and `frontend-app/scripts/sync-frontend-dist.mjs`.
- The script fails fast if the embedded index file still does not exist after sync.
- A focused regression test was added in `internal/app/new_ui_ps1_embed_dist_test.go`.

Verification after manual workaround:

```text
npm run build
node .\scripts\sync-frontend-dist.mjs
http://127.0.0.1:5175 -> 200
http://127.0.0.1:4512/metrics -> 200
```

Verification after fix:

```text
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\app\new_ui_ps1_embed_dist_test.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/app -count=1
go test ./cmd/agent-terminal
PowerShell PSParser syntax check for run-new-ui-desktop.ps1
run-new-ui-desktop.ps1 post-fix startup: 5175 -> 200, 4512/metrics -> 200, 8092 listening
```

### P2 - `frontend-app` has a high-severity Vite advisory on Windows

Status: fixed in this branch

Area: frontend dependency security/ops risk

Command:

```text
npm audit --json
```

Evidence:

`npm audit` reported `vite` as a direct dependency with high severity:

- advisory: `GHSA-fx2h-pf6j-xcff`
- title: `vite: server.fs.deny bypass on Windows alternate paths`
- affected range: `>=8.0.0 <=8.0.15`
- installed package is within the vulnerable range
- fix available: yes

It also reported a moderate advisory through the same package:

- advisory: `GHSA-v6wh-96g9-6wx3`
- title: `launch-editor: NTLMv2 hash disclosure via UNC path handling on Windows`

Impact:

The development server runs locally on Windows and is part of the normal desktop startup flow. This is primarily a dev/runtime hardening issue, but it matters here because the product uses Vite during the desktop dev path.

Suggested fix:

Upgrade Vite to a fixed version outside `8.0.0 - 8.0.15`, then rerun:

```text
cd frontend-app
npm run lint
npm test
npm run build
npm audit --json
```

Implemented fix:

- `frontend-app/package-lock.json` now resolves Vite to `8.0.16`, outside the vulnerable `>=8.0.0 <=8.0.15` range.

Verification after fix:

```text
cd frontend-app
npm audit --json
npm run lint
npm test
npm run build
```

### P3 - Transitive DOMPurify advisories remain in `frontend-app`

Status: fixed in this branch

Area: frontend dependency security/ops risk

Command:

```text
npm audit --json
```

Evidence:

`npm audit` reported `dompurify` as a transitive dependency with low severity:

- `GHSA-vxr8-fq34-vvx9`
- `GHSA-gvmj-g25r-r7wr`
- affected range: `<=3.4.8`
- fix available: yes

Impact:

This is lower risk than the Vite issue, but DOM sanitization dependencies deserve attention because this app renders user-facing markdown/diagram content.

Suggested fix:

Update the dependency chain that brings `dompurify`, then rerun frontend tests and audit.

Implemented fix:

- `frontend-app/package-lock.json` now resolves DOMPurify to `3.4.10`, outside the vulnerable `<=3.4.8` range.

Verification after fix:

```text
cd frontend-app
npm audit --json
npm run lint
npm test
npm run build
```

## Non-Findings

- `powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 --guard-only` passed.
- `go test ./cmd/agent-terminal` passed.
- `frontend-app` lint passed.
- `frontend-app` tests passed: 69 files, 861 passed, 10 skipped.
- `frontend-app` build passed.
- `npm audit --json` passed with `total = 0`.
- Runtime readiness passed after the embed dist was generated: frontend `200`, metrics `200`.
- Runtime readiness passed again after the startup-script fix: frontend `200`, metrics `200`, control RPC `8092` listening.

## Verification Commands

```text
git status --short --branch --ignored=matching
git diff --stat main...HEAD
git diff --name-status main...HEAD
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 --guard-only
cd frontend-app
npm run lint
npm test
npm run build
npm audit --json
cd ..
go test ./cmd/agent-terminal
Invoke-WebRequest http://127.0.0.1:5175 -UseBasicParsing
Invoke-WebRequest http://127.0.0.1:4512/metrics -UseBasicParsing
```

Additional fix verification:

```text
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\app\new_ui_ps1_embed_dist_test.go
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/app -count=1
PowerShell PSParser syntax check for run-new-ui-desktop.ps1
Invoke-WebRequest http://127.0.0.1:5175 -UseBasicParsing
Invoke-WebRequest http://127.0.0.1:4512/metrics -UseBasicParsing
Get-NetTCPConnection -ErrorAction SilentlyContinue | Where-Object { (5175,4512,8092) -contains $_.LocalPort -and $_.State -eq 'Listen' }
```

## Notes

- The original audit did not claim a branch-specific code regression because the branch initially had no tracked source diff from `main`.
- Ignored generated files are expected after startup/build, including `.tmp/`, `frontend-app/dist/`, `cmd/agent-terminal/frontend/dist/`, `mcp-lsp.exe`, and `mcp-orch.exe`.
- The repeated Vitest `localStorage is not available because --localstorage-file was not provided` warnings did not fail tests, but they add noise to audit output.
