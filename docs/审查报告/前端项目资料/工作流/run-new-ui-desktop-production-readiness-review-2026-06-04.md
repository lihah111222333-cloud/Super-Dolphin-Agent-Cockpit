# run-new-ui-desktop Production Readiness Review

Date: 2026-06-04

Scope: current working-tree diff for `run-new-ui-desktop.sh` and `internal/app/new_ui_scripts_test.go`, plus directly touched frontend/backend workflow startup code: `internal/app/app.go`, `internal/ui/wails/{http_server.go,window.go,assets.go}`, `frontend-app/vite.config.js`, `frontend-app/package.json`, and startup documentation.

Mode: read-only production readiness review. No functional code was changed by reviewers. Five sub-agents were dispatched with separate focus areas. The repository policy mentions `mcp-go-agent-orchestration`, but those lifecycle tools were not exposed in the current callable tool set; the available `multi_agent_v1` sub-agent tools were used instead.

## Review Agents

| Agent | Focus |
| --- | --- |
| Einstein | Wails/backend/Vite startup ordering, readiness, timeout chain |
| Zeno | Vite watcher, polling, ENOSPC, capacity/config semantics |
| Pasteur | Shell process lifecycle, `wait`, cleanup, stale process handling |
| Epicurus | Tests, docs, maintainability, behavior coverage |
| Descartes | Vite URL/proxy/HTTP bridge configuration chain |

## Consolidated Findings

### Finding 1

Status: VALID

Evidence:

- `run-new-ui-desktop.sh:725-731` starts the desktop backend, waits for backend `/metrics`, seeds preferences, and only then starts Vite.
- `internal/app/app.go:136-149` starts the Fx app and then calls `wailsApp.Run()`.
- Wails dev `preRun()` waits for `FRONTEND_DEVSERVER_URL` at most 10 times with 500 ms sleeps, then fatals: `github.com/wailsapp/wails/v3@v3.0.0-alpha.74/pkg/application/application_dev.go:14-37`.
- Wails fatal exits the process directly: `github.com/wailsapp/wails/v3@v3.0.0-alpha.74/pkg/application/application.go:469-471`.
- `/metrics` readiness only proves the HTTP asset server route exists, not that Wails devserver preRun has succeeded: `internal/ui/wails/http_server.go:29-32`.

Risk impact:

The script can report backend readiness while Wails is still waiting for a frontend server that the script has not started yet. If `seed_dev_preferences`, cold `npm run dev`, first Vite optimize, or slow machine startup takes longer than Wails' roughly 5 second devserver wait, the backend exits before the script reaches frontend readiness. The new `wait_for_http()` checks can print logs after the failure, but they do not remove the race.

Blocks release: Yes, if `run-new-ui-desktop.sh` is the release/acceptance path for the new UI desktop workflow.

### Finding 2

Status: VALID

Evidence:

- `internal/app/new_ui_scripts_test.go:69-70` explicitly asserts that backend wait and preference seeding happen before Vite is started.
- `internal/app/new_ui_scripts_test.go:74-115` adds string-presence tests for readiness failure logging and polling, but does not execute shell behavior or simulate Wails' `FRONTEND_DEVSERVER_URL` wait.

Risk impact:

The tests lock in the startup order that creates Finding 1. They can pass while the real desktop startup is still racy. This weakens the regression safety of a bug-fix style diff.

Blocks release: Yes, because the regression coverage currently protects the problematic ordering rather than the user-observable startup behavior.

### Finding 3

Status: VALID

Evidence:

- `run-new-ui-desktop.sh:262-266` treats any `SUPER_DOLPHIN_VITE_USE_POLLING` value outside the accepted truthy list as false.
- `run-new-ui-desktop.sh:269-278` preserves an existing `CHOKIDAR_USEPOLLING` value even when script-level polling is enabled or disabled.
- `run-new-ui-desktop.sh:729` starts Vite with the inherited environment.
- Vite's bundled Chokidar reads `CHOKIDAR_USEPOLLING` and treats `false` or `0` as disabled, `true` or `1` as enabled, and other non-empty strings as enabled: `frontend-app/node_modules/vite/dist/node/chunks/node.js:9439-9445`.

Risk impact:

The added watcher-capacity protection is not fail-fast. A typo such as `SUPER_DOLPHIN_VITE_USE_POLLING=ture` silently disables the script-level protection. Conversely, a parent-shell `CHOKIDAR_USEPOLLING=0` can make the script print `polling` while Vite actually disables polling. A parent-shell `CHOKIDAR_USEPOLLING=1` can keep polling enabled even when the user requested `SUPER_DOLPHIN_VITE_USE_POLLING=0`. This makes the ENOSPC mitigation and CPU/IO tradeoff unreliable.

Blocks release: Yes. It is a configuration validation and capacity-control issue in the new mitigation.

### Finding 4

Status: VALID

Evidence:

- `run-new-ui-desktop.sh:649-657` parses `VITE_DEV_URL` and only checks that host and port are present.
- `run-new-ui-desktop.sh:729` passes the parsed host to Vite via `--host`.
- `frontend-app/vite.config.js:33-40` proxies `/wails/ws` and `/generated-image` to the backend address.
- The backend HTTP server itself is loopback-restricted by `validateHTTPAssetAddr`: `internal/ui/wails/http_server.go:104-115`, but that does not restrict who can connect to Vite if Vite binds externally.

Risk impact:

If `.env` or the shell sets `VITE_DEV_URL=http://0.0.0.0:5175` or a LAN host, Vite can bind externally and proxy external requests into the loopback backend bridge. That can bypass the Go-side loopback binding assumption for dev workflow endpoints.

Blocks release: Yes, unless externally reachable devserver behavior is explicitly intended and protected elsewhere.

### Finding 5

Status: VALID

Evidence:

- `run-new-ui-desktop.sh:650` allows `FRONTEND_DEVSERVER_URL` to differ from `VITE_DEV_URL`.
- `run-new-ui-desktop.sh:731` waits only for `VITE_DEV_URL`.
- `internal/ui/wails/window.go:119-145` builds the actual window URL from `FRONTEND_DEVSERVER_URL`; invalid parse returns the raw base at `window.go:133-135`.

Risk impact:

A stale `.env` can set `FRONTEND_DEVSERVER_URL` to a different or unreachable address. The script can still report `frontend-app vite ready` for `VITE_DEV_URL`, while the desktop window loads a different URL. This is a real false-ready path.

Blocks release: Yes, because readiness can disagree with the URL the window actually loads.

### Finding 6

Status: VALID

Evidence:

- `run-new-ui-desktop.sh:73-98` waits about 10 seconds for frontend readiness.
- `run-new-ui-desktop.sh:215-230` waits about 20 seconds for backend `/metrics`.
- The backend is started with `go run`: `run-new-ui-desktop.sh:289`.

Risk impact:

Cold Go compilation, slow disk, first Vite dependency optimization, or migrations can keep a healthy process from reaching readiness inside the fixed windows. The script then treats a slow but healthy startup as failure and cleans up.

Blocks release: Yes for production-readiness of this workflow entrypoint. It is a real startup reliability risk.

### Finding 7

Status: VALID

Evidence:

- `run-new-ui-desktop.sh:3` enables `set -euo pipefail`.
- `run-new-ui-desktop.sh:233-249` and `run-new-ui-desktop.sh:329-351` call `wait "$PID"` and only then assign `status="$?"`.
- Under `set -e`, a non-zero `wait` exits before the status assignment and before the intended log tail printing.

Risk impact:

After readiness, if Vite or the backend exits non-zero, the script still fails and cleanup still runs, but the intended `print_backend_log_tail` / `print_frontend_log_tail` diagnostics in the long-running monitor paths are skipped.

Blocks release: No. This is a diagnosis-quality issue, not a startup correctness or cleanup blocker.

### Finding 8

Status: VALID

Evidence:

- `frontend-app/README.md:9-12` documents direct `npm run dev`.
- `frontend-app/package.json:7` runs Vite without setting `CHOKIDAR_USEPOLLING`.
- `frontend-app/vite.config.js:30-42` does not set `server.watch.usePolling`.

Risk impact:

The new ENOSPC mitigation only covers `run-new-ui-desktop.sh`. The documented standalone frontend entrypoint can still reproduce watcher exhaustion on the same environment.

Blocks release: No for the desktop script diff alone. It blocks only if the acceptance scope includes all documented frontend dev entrypoints.

### Finding 9

Status: VALID

Evidence:

- `frontend-app/README.md:21-22` says the script starts Vite and then launches `cmd/agent-terminal`.
- The actual script order is backend first, preference seed second, Vite last: `run-new-ui-desktop.sh:725-731`.

Risk impact:

The documentation describes the opposite order from the implemented workflow, which misleads startup-failure diagnosis and maintenance.

Blocks release: No. This is a maintenance/documentation risk.

## INVALID Findings With Existing Protection

### INVALID 1

Claim: Vite/backend exits before frontend readiness can only surface as a silent timeout.

Defense:

- `wait_for_http()` watches both `VITE_PID` and `DESKTOP_PID` during frontend readiness: `run-new-ui-desktop.sh:78-89`.
- `process_exited()` handles normal exits and zombies: `run-new-ui-desktop.sh:126-135`.
- `print_frontend_log_tail()` and `print_backend_log_tail()` print logs on those paths: `run-new-ui-desktop.sh:196-210`.

Protection functions: `wait_for_http`, `process_exited`, `print_frontend_log_tail`, `print_backend_log_tail`.

Blocks release: No.

### INVALID 2

Claim: Backend startup failures wait silently until timeout.

Defense:

- `wait_for_backend()` polls `/metrics`, checks `DESKTOP_PID`, and prints backend logs on early exit or timeout: `run-new-ui-desktop.sh:213-230`.

Protection functions: `wait_for_backend`, `process_exited`, `print_backend_log_tail`.

Blocks release: No.

### INVALID 3

Claim: Default port conflicts can accidentally connect to an old Vite/backend process.

Defense:

- `stop_stale_vite_for_port()` only stops same-port Vite processes whose command line and cwd match `frontend-app`: `run-new-ui-desktop.sh:180-193`.
- `fail_if_port_busy()` then fail-fast checks Vite, backend HTTP, and control RPC ports: `run-new-ui-desktop.sh:101-108`, `run-new-ui-desktop.sh:700-703`.

Protection functions: `stop_stale_vite_for_port`, `process_workdir`, `fail_if_port_busy`.

Blocks release: No.

### INVALID 4

Claim: Child processes are likely to leak on normal exits or interrupts.

Defense:

- `cleanup()` has the `CLEANUP_DONE` idempotence guard: `run-new-ui-desktop.sh:628-643`.
- `stop_process_tree()` recursively terminates children, waits, then sends KILL: `run-new-ui-desktop.sh:156-178`.
- `EXIT`, `INT`, `TERM`, and `HUP` traps call cleanup: `run-new-ui-desktop.sh:644-645`.

Protection functions: `cleanup`, `stop_process_tree`, `process_exited`.

Blocks release: No.

### INVALID 5

Claim: Backend HTTP bridge can bind publicly through `SUPER_DOLPHIN_HTTP_ADDR`.

Defense:

- `validateHTTPAssetAddr()` allows only `localhost`, `127.0.0.1`, and `::1`: `internal/ui/wails/http_server.go:104-115`.

Protection function: `validateHTTPAssetAddr`.

Blocks release: No.

### INVALID 6

Claim: `/metrics` readiness can be swallowed by the Vite asset proxy.

Defense:

- `registerHTTPAssetRoutes()` registers metrics before `/wails/ws` and `/`: `internal/ui/wails/http_server.go:29-32`.
- `wait_for_backend()` explicitly waits on `/metrics`: `run-new-ui-desktop.sh:213-230`.

Protection functions: `registerHTTPAssetRoutes`, `wait_for_backend`.

Blocks release: No.

### INVALID 7

Claim: Polling mode necessarily watches the whole repository including heavy directories.

Defense:

- The script starts Vite from `FRONTEND_APP_DIR`: `run-new-ui-desktop.sh:729`.
- Vite resolves ignored watch paths including `.git`, `node_modules`, `test-results`, cache, and outDir: `frontend-app/node_modules/vite/dist/node/chunks/node.js:12439-12448`.
- Vite watches root/config/env/public for the frontend project, not the repository root: `frontend-app/node_modules/vite/dist/node/chunks/node.js:26103-26108`.

Protection functions/paths: `configure_frontend_watch_mode`, Vite `resolveChokidarOptions`.

Blocks release: No.

### INVALID 8

Claim: Missing host/port in `VITE_DEV_URL` proceeds to a half-started state.

Defense:

- The script checks parsed host and port and exits before startup when either is missing: `run-new-ui-desktop.sh:651-659`.

Protection path: `VITE_DEV_URL` host/port validation in `run-new-ui-desktop.sh`.

Blocks release: No.

## Verification Notes

Agent-reported checks:

- `./scripts/test_with_guard.sh ./internal/app -run TestNewUIDesktopScript -count=1`
- `./scripts/test_with_guard.sh ./internal/app ./internal/ui/wails -count=1`
- `bash -n run-new-ui-desktop.sh`
- `git diff --check`

Parent-session fact checks:

- Confirmed Wails dev preRun timeout and fatal path in the local Wails module.
- Confirmed Chokidar's `CHOKIDAR_USEPOLLING` parsing in bundled Vite code.
- Confirmed `set -e` exits before post-`wait` status capture on non-zero child exit.

Overall release assessment: not production-ready for the desktop new-UI workflow while Findings 1-6 remain open. Findings 7-9 are real but non-blocking.
