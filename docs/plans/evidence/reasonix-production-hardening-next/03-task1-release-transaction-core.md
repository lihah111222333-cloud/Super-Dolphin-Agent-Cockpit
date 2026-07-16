# Task 1 Release Transaction Core Evidence

## Scope

Task 1 implements only the release transaction runtime: durable journal persistence,
backup retention through probation, pending/commit/rollback trust generation, crash
replay, cross-process transaction locking, and exact transaction identity. It does
not add the Task 2 supervisor, Guard, recovery graph, packaging, E2E, or MCP work.

The transaction core lives at `internal/platform/appupdaterecovery`. This is an
intentional runtime seam adjustment from the plan's provisional module path:
`cmd/super-dolphin-updater` is forbidden from importing `internal/module`, while
both the updater and the later Task 2 early selector need the same low-level,
business-module-independent recovery primitive. The command allow policy registers
only this exact platform package and keeps the existing narrow import boundary.

## Implemented Contract

- Atomic, checksum-protected, strict-JSON journals use file fsync, rename, and parent
  directory fsync; missing or malformed state fails fast.
- The stateless state machine persists intent before each filesystem effect and
  persists completion after it. Replay only reconciles a persisted pending intent.
- Identity binds transaction ID, attempt ID, old digest/signer, and candidate
  digest/signer. A mismatched identity cannot mutate journal or filesystem state.
- Backup remains present while the candidate is in probation. Healthy commit first
  commits trust and then removes the exact backup; rollback restores the exact old
  release and marks trust rolled back.
- The updater success path creates the transaction, retains the backup, installs the
  candidate, and returns only in probation with pending trust.
- A missing target keeps first-install compatibility through a same-parent durable
  staging fsync and atomic rename. It intentionally creates no rollback transaction.
- The old timeout test now explicitly proves a pre-transaction preparation failure.
  A separate core test corrupts the candidate after backup retention and proves real
  rollback from `install_pending`.

## RED And GREEN

| Evidence | Result |
|---|---|
| Mandatory fail-first transaction test | RED: package did not compile before transaction types existed |
| First transaction runtime run | RED: journal checksum mismatch exposed unstable indented `json.RawMessage` encoding |
| `TestUpdateTransactionRetainsBackupUntilHealthy` | GREEN |
| `TestTrustGenerationCommitsOnlyAfterHealthy` | GREEN |
| Updater retained-backup integration | RED: no retained backup before updater wiring; GREEN after wiring |
| Missing-target compatibility | RED: old-release digest failed with target absent; GREEN through first-install atomic path |
| Crash replay, illegal transition, wrong identity, field mutation matrix | GREEN |
| Post-backup install failure and exact rollback | GREEN |

## Review Dimensions

| Dimension | Coverage | Evidence |
|---|---|---|
| D01 Architecture | Applied | platform seam, exact command allow policy, codemap 13, full archtest |
| D02 Fail-fast | Applied | strict journal decode/checksum, required fields, illegal state and malformed identity errors |
| D03 MCP | N/A | Task 1 has no MCP protocol surface |
| D04 LSP Product | N/A | LSP was used for implementation evidence but no LSP product behavior changed |
| D05 Provider/runtime | N/A | no provider session or runtime event behavior changed |
| D06 Orchestration | N/A | no DAG, cron, wakeup, supervisor, or recovery graph was implemented |
| D07 Store/sqlc | N/A | no database schema, query, migration, or sqlc surface changed |
| D08 Skill/Memory/Prompt/Thread | N/A | no skill, memory, prompt, or thread path changed |
| D09 Frontend | N/A | no frontend surface changed |
| D10 Security | Applied | same-parent path derivation, exact digest/signer identity, 0600 journal, nonblocking file lock |
| D11 Observability | N/A | no logging or event schema changed; durable journal is the recovery record |
| D12 Testing | Applied | fail-first RED, state/mutation matrix, full guarded tests, race, archtest |
| D13 Release/Install | Applied | updater replacement path, retained backup, first-install compatibility |
| D14 Performance | Applied | bounded release walk, explicit resource close, cross-process lock, race verification |
| D15 UX/Product | N/A | no UI or user-facing state presentation changed |
| D16 Git/Workflow | Applied | isolated worktree, exact write set, generated map refresh and drift gates |
| D17 Field Guard | Applied | journal fields are recursively enumerated from real producer types; mutation tests fail closed |
| D18 DRY | Applied | shared platform transaction runtime replaces command-local backup/rollback control flow |
| D19 SSOT | Applied | journal is the sole transaction/trust state owner; generated maps remain generator-owned |

## Residual Risks

- Task 1 leaves health observation and automatic commit/rollback triggering to Task 2.
- First install has no old release to restore, so it deliberately has no rollback
  transaction; a parent-directory fsync error is reported even if rename completed.
- Per-transaction locks prevent duplicate mutation of one exact transaction. Global
  selection among multiple transactions for one target remains Task 2 policy.

## AI Maintenance Evidence

```yaml
PACKAGE: TASK_1_RELEASE_TRANSACTION_CORE
STATUS: DONE_WITH_EVIDENCE
AGENTID: 019f6a69-7f3b-7810-afeb-9744403cd6a5
BASE_HEAD: 3d6fccfc58b904e2c9a6f358285cdee6d6ea7753
OWNED_FILES_CHANGED:
  - cmd/super-dolphin-updater/install.go
  - cmd/super-dolphin-updater/install_test.go
  - docs/doc/codemap/13-archtest-boundaries.md
  - docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md
  - docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json
  - docs/doc/codemap/project-map/AI_PROJECT_MAP.md
  - docs/doc/codemap/project-map/index/docs-agent.tsv
  - docs/doc/codemap/project-map/index/other.tsv
  - docs/doc/codemap/project-map/index/platform-provider.tsv
  - docs/plans/evidence/reasonix-production-hardening-next/03-task1-release-transaction-core.md
  - internal/archtest/backend_boundary_governance_test.go
  - internal/archtest/backend_boundary_loader_parity_test.go
  - internal/archtest/backend_boundary_registry.go
  - internal/archtest/priority_ssa_loader_parity_test.go
  - internal/platform/appupdaterecovery/digest.go
  - internal/platform/appupdaterecovery/effects.go
  - internal/platform/appupdaterecovery/field_guard.go
  - internal/platform/appupdaterecovery/field_guard_test.go
  - internal/platform/appupdaterecovery/journal.go
  - internal/platform/appupdaterecovery/lock.go
  - internal/platform/appupdaterecovery/lock_unix.go
  - internal/platform/appupdaterecovery/lock_windows.go
  - internal/platform/appupdaterecovery/machine.go
  - internal/platform/appupdaterecovery/store.go
  - internal/platform/appupdaterecovery/transaction_test.go
  - internal/platform/appupdaterecovery/types.go
UNRELATED_DIRTY_FILES_PRESERVED: []
LSP_EVIDENCE:
  locate: PASS
  inspect: PASS
  xref: PASS
  read_file: PASS
  diagnostics: PASS
COMMANDS_RUN:
  - cmd: ./scripts/test_with_guard.sh ./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater -count=1
    exit: 0
  - cmd: ./scripts/test_with_guard.sh ./internal/archtest -count=1
    exit: 0
  - cmd: ./scripts/test_with_guard.sh --with-race ./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater -- ./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater -count=1 # executes go test -race
    exit: 0
  - cmd: go run ./scripts/lsp_diagnostics_gate --file cmd/super-dolphin-updater/install.go --file cmd/super-dolphin-updater/install_test.go --file internal/platform/appupdaterecovery --file internal/archtest/backend_boundary_registry.go --file internal/archtest/backend_boundary_governance_test.go
    exit: 0
  - cmd: make codemap-check
    exit: 0
  - cmd: make project-map-refresh
    exit: 0
  - cmd: make project-map-check
    exit: 0
  - cmd: make capcontract-check
    exit: 0
  - cmd: git diff --check
    exit: 0
GENERATED_FILES:
  - path: docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md
    precheck_failed: make project-map-check
    source_command: make project-map-refresh
  - path: docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json
    precheck_failed: make project-map-check
    source_command: make project-map-refresh
  - path: docs/doc/codemap/project-map/AI_PROJECT_MAP.md
    precheck_failed: make project-map-check
    source_command: make project-map-refresh
  - path: docs/doc/codemap/project-map/index/docs-agent.tsv
    precheck_failed: make project-map-check
    source_command: make project-map-refresh
  - path: docs/doc/codemap/project-map/index/other.tsv
    precheck_failed: make project-map-check
    source_command: make project-map-refresh
  - path: docs/doc/codemap/project-map/index/platform-provider.tsv
    precheck_failed: make project-map-check
    source_command: make project-map-refresh
BLOCKERS: []
```
