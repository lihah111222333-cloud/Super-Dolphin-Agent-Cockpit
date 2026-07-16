
# Task 2 Supervisor, Guard, and Recovery Evidence

## Scope

- Reused `internal/platform/appupdaterecovery` as the sole transaction/trust state owner.
- Added exact probation lease/ACK identity, kernel-backed process identity, blocking supervisor lifecycle, detached Guard takeover, and an early selector that does not ACK before desktop readiness.
- Candidate startup now retains a controlled handle with one `cmd.Wait` reaper; crash and TERM/KILL paths coordinate through its bounded result state instead of releasing the child.
- Added the four-constructor Recovery-only graph plus an isolated Wails multi-page surface for state/check/retry/restore; normal provider/store/toolbridge/skill graphs remain excluded.
- Added deterministic staged-snapshot embed placeholder handling without reading ignored `web-dist`; package trust/E2E and MCP expansion remain out of scope.

## RED / GREEN

Initial fail-first tests failed to compile because the Task 2 APIs did not exist:

- `TestProbationFailureRollsBackExactTransaction`
- `TestAgentTerminalSelectsRecoveryBeforeNormalPreflight`
- `TestGuardTakesOverStaleProbationOnce`
- `TestRecoveryGraphContainsOnlyAllowedConstructors`
- `TestSelectStartupDoesNotRecordHealthyACKBeforeReady`
- `TestPrepareDesktopRuntimeRecordsReadyAfterStartAndValidation`
- `TestPrepareDesktopRuntimeReadyFailureStopsFXOnce`
- `TestActiveProbationNormalFailureDoesNotOpenRecovery`
- `TestCandidateHandleReapsCrashedProcess`
- `TestCandidateHandleTerminatesAndReapsExactProcess`

Final focused command:

```text
./scripts/go_with_guard.sh test ./internal/platform/appupdaterecovery ./internal/platform/pidregistry ./internal/platform/runtimeenv ./internal/app ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./cmd/agent-terminal -count=1
```

Result: PASS, including code-size guard, priority SSA guard, and archtest guard.

Additional focused coverage proves zero early ACK, ready-after-Fx/Wails ordering, ready-failure Fx cleanup, active-probation startup failure exit without Recovery, exact ACK rejection/commit, crash rollback, timeout rollback, supervisor interruption, wrong lease zero mutation, stale lease CAS takeover, second takeover zero mutation, Guard restart-once behavior, and native candidate reaping.

## Verification

- Race PASS for `internal/platform/appupdaterecovery`, `pidregistry`, updater, Guard, Recovery app services, runtimeenv, and agent-terminal.
- Native process PASS: `TestRunCommandTimesOutAndKillsProcessGroup`, candidate crash reaping, and exact TERM reaping; PID is no longer observable after the sole `cmd.Wait`.
- Frontend PASS: lint, 161 files/2436 tests, multi-page production build, and Playwright desktop/mobile Recovery smoke with zero horizontal overflow.
- Production build contains `dist/recovery.html`, a dedicated Recovery JS/CSS asset, and the synchronized `web-dist` surface.
- LSP locate/inspect/xref/read/diagnostics workflow completed; final changed-file diagnostics are empty.
- Dynamic producer guards enumerate lease, process identity, ACK, probation record, Recovery projection/action fields, and backend FQN method IDs.

## D01-D19

| Dimension | Status | Evidence |
|---|---|---|
| D01 Architecture | Applied | Guard registered as a narrow command seam; Recovery constructors frozen to state/check/retry/restore |
| D02 Fail-fast | Applied | corrupt, partial, ambiguous, missing expected, wrong identity, and multiple active state fail closed |
| D03 MCP | N/A | no MCP surface changed |
| D04 LSP product | N/A | LSP used for implementation evidence; no LSP product behavior changed |
| D05 Provider/runtime | N/A | selector runs before provider/runtime normal preflight; provider behavior unchanged |
| D06 Orchestration | Applied | blocking supervisor, detached Guard takeover, and controlled candidate reaper lifecycle |
| D07 Store/sqlc | N/A | no database or sqlc change |
| D08 Skill/Memory/Prompt/Thread | N/A | no related behavior changed |
| D09 Frontend | Applied | isolated Recovery entry, typed state/actions, backend FQN parity guard, desktop/mobile visual smoke |
| D10 Security | Applied | ready-only exact transaction/release/process ACK, kernel start token/executable revalidation, lease CAS, frozen detached environment |
| D11 Observability | Applied | durable journal projection carries exact recovery reason and lease identity |
| D12 Testing | Applied | fail-first, ACK timing, ready cleanup, focused, race, native reaper, mutation and takeover tests |
| D13 Release/Install | Applied | updater launches a reaped candidate, Guard, and supervisor on restart updates |
| D14 Performance | Applied | bounded polling, observation, lease, startup, reaper result, and blocking context lifecycle |
| D15 UX/Product | Applied | visible Safe Mode status/check/retry/restore surface; unavailable actions disabled |
| D16 Git/Workflow | Applied | isolated worktree, scoped write set, generated maps and Chinese commit gate |
| D17 Field Guard | Applied | dynamic lease/ACK/projection producer enumeration |
| D18 DRY | Applied | Task 1 journal/store reused; no duplicate transaction truth |
| D19 SSOT | Applied | appupdaterecovery journal remains sole writable transaction/trust owner |

## Residual Risk

- Manual updater use without `-restart` intentionally retains the Task 1 probation journal without starting Task 2 supervision; the product install path already supplies `-restart`.
- Recovery restart currently uses the macOS `open -n` effect boundary; other desktop platform launch effects remain outside this Task.

## AI Maintenance Evidence

```yaml
PACKAGE: TASK_2_SUPERVISOR_GUARD_RECOVERY
STATUS: DONE_WITH_EVIDENCE
AGENTID: 83bd22a3-365d-41a0-85ec-ae46ffd0f0a4
BASE_HEAD: 77d823c378b0fdc16d89bcbd192becee830703ed
OWNED_FILES_CHANGED:
  - .githooks/pre-commit
  - cmd/agent-terminal/main.go
  - cmd/agent-terminal/main_recovery_test.go
  - cmd/agent-terminal/recovery_ui.go
  - cmd/agent-terminal/recovery_ui_test.go
  - cmd/super-dolphin-guard/main.go
  - cmd/super-dolphin-guard/main_test.go
  - cmd/super-dolphin-updater/install.go
  - cmd/super-dolphin-updater/install_test.go
  - cmd/super-dolphin-updater/probation.go
  - docs/doc/codemap/13-archtest-boundaries.md
  - docs/doc/codemap/ai-index.json
  - docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md
  - docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json
  - docs/doc/codemap/project-map/AI_PROJECT_MAP.md
  - docs/doc/codemap/project-map/index/app-ui.tsv
  - docs/doc/codemap/project-map/index/docs-agent.tsv
  - docs/doc/codemap/project-map/index/other.tsv
  - docs/doc/codemap/project-map/index/platform-provider.tsv
  - docs/plans/evidence/reasonix-production-hardening-next/04-task2-supervisor-guard-recovery.md
  - frontend-app/recovery.html
  - frontend-app/src/features/update-recovery/RecoveryApp.css
  - frontend-app/src/features/update-recovery/RecoveryApp.jsx
  - frontend-app/src/features/update-recovery/RecoveryApp.test.jsx
  - frontend-app/src/features/update-recovery/recoveryClient.js
  - frontend-app/src/features/update-recovery/recoveryClient.test.js
  - frontend-app/src/recovery-main.jsx
  - frontend-app/vite.config.js
  - frontend-app/vite.config.test.js
  - internal/app/app.go
  - internal/app/app_test.go
  - internal/app/recovery_graph.go
  - internal/app/recovery_graph_test.go
  - internal/app/recovery_selection.go
  - internal/archtest/backend_boundary_governance_test.go
  - internal/archtest/backend_boundary_loader_parity_test.go
  - internal/archtest/backend_boundary_registry.go
  - internal/archtest/priority_ssa_loader_parity_test.go
  - internal/platform/appupdaterecovery/discovery.go
  - internal/platform/appupdaterecovery/field_guard_test.go
  - internal/platform/appupdaterecovery/journal.go
  - internal/platform/appupdaterecovery/probation.go
  - internal/platform/appupdaterecovery/store.go
  - internal/platform/appupdaterecovery/supervisor.go
  - internal/platform/appupdaterecovery/supervisor_test.go
  - internal/platform/appupdaterecovery/transaction_test.go
  - internal/platform/appupdaterecovery/types.go
  - internal/platform/pidregistry/exact_process.go
  - internal/platform/pidregistry/process_identity_test.go
  - internal/platform/pidregistry/process_unix.go
  - internal/platform/pidregistry/process_windows.go
  - internal/platform/runtimeenv/recovery_launch.go
  - scripts/guard_fix_commits_have_tests_helpers_test.go
UNRELATED_DIRTY_FILES_PRESERVED: []
LSP_EVIDENCE:
  locate: PASS
  inspect: PASS
  xref: PASS
  read_file: PASS
  diagnostics: PASS
COMMANDS_RUN:
  - cmd: ./scripts/test_with_guard.sh ./internal/platform/appupdaterecovery ./internal/platform/pidregistry ./internal/platform/runtimeenv ./internal/app ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./cmd/agent-terminal -count=1
    exit: 0
  - cmd: ./scripts/go_with_guard.sh test ./internal/platform/appupdaterecovery ./internal/platform/pidregistry ./internal/platform/runtimeenv ./internal/app ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./cmd/agent-terminal -count=1
    exit: 0
  - cmd: ./scripts/go_with_guard.sh test -race ./internal/platform/appupdaterecovery ./internal/platform/pidregistry ./internal/app ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./cmd/agent-terminal -count=1
    exit: 0
  - cmd: ./scripts/go_with_guard.sh test -race ./internal/app ./cmd/agent-terminal ./cmd/super-dolphin-updater -run 'Test(SelectStartupDoesNotRecordHealthyACKBeforeReady|PrepareDesktopRuntime|ActiveProbationNormalFailureDoesNotOpenRecovery|CandidateHandle)' -count=1
    exit: 0
  - cmd: ./scripts/go_with_guard.sh test ./cmd/super-dolphin-updater -run 'TestCandidateHandle' -count=3
    exit: 0
  - cmd: ./scripts/go_with_guard.sh test ./cmd/super-dolphin-updater -run '^TestRunCommandTimesOutAndKillsProcessGroup$' -count=1 -v
    exit: 0
  - cmd: cd frontend-app && npm run lint
    exit: 0
  - cmd: cd frontend-app && npm test
    exit: 0
  - cmd: cd frontend-app && npm run build
    exit: 0
  - cmd: Playwright Recovery desktop/mobile smoke
    exit: 0
  - cmd: make codemap-refresh project-map-refresh
    exit: 0
  - cmd: make codemap-check
    exit: 0
  - cmd: make project-map-check
    exit: 0
  - cmd: make capcontract-check
    exit: 0
  - cmd: go run ./scripts/lsp_diagnostics_gate --file cmd/agent-terminal/main.go --file cmd/super-dolphin-updater --file cmd/super-dolphin-guard --file internal/app/recovery_graph.go --file internal/app/recovery_selection.go --file internal/platform/appupdaterecovery --file internal/platform/runtimeenv/recovery_launch.go --file internal/archtest/backend_boundary_registry.go --file internal/archtest/backend_boundary_governance_test.go
    exit: 0
  - cmd: git diff --check
    exit: 0
GENERATED_FILES:
  - path: docs/doc/codemap/13-archtest-boundaries.md
    precheck_failed: make codemap-check
    source_command: make codemap-refresh
  - path: docs/doc/codemap/ai-index.json
    precheck_failed: make codemap-check
    source_command: make codemap-refresh
BLOCKERS: []
```
