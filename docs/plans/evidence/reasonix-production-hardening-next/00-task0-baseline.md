# Reasonix Production Hardening - Task 0 Baseline

TASK_ID: `TASK_0`

PACKAGE: reasonix-production-hardening-task0

STATUS: DESIGN_FROZEN_PENDING_REVIEW

AGENTID: 019f6938-f750-7ab1-ae8c-7d3f1f0f6ade

BASE_HEAD: b40867229af8e17916c00393639ccb0fcb4bf6fc

CURRENTNESS_NOTICE: superseded by `02-current-main-recheck.md` for latest `origin/main` status. This file remains historical evidence for the `b40867229...` Task 0 execution object and must not be used as current-main LSP PASS.

VERDICT: `REVIEW_BLOCKED`

`implementation_design_complete=false`

`p0_executable=false`

BLOCKERS:

- The committed design freeze has not received two fresh reviewer lanes with `0 P0 / 0 P1` on the same externally supplied immutable SHA.

OWNED_FILES_CHANGED:

- cmd/super-dolphin-updater/install.go
- internal/module/appupdate/service.go
- docs/plans/2026-07-15-reasonix-production-hardening-next-absorption.md
- docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md
- docs/plans/evidence/reasonix-production-hardening-next/01-task0-design-freeze.md

UNRELATED_DIRTY_FILES_PRESERVED:

- docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md

本文件冻结当前执行对象、已证明的源码事实、Task 0-D0 诊断清零结果和未闭合 blocker。Task 0-D0 之外的生产修改仍未授权；局部 LSP 或命令绿色不等于 P0 可执行。

## Review Object

| Field | Frozen value |
| --- | --- |
| Worktree | `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-p0-task0-20260716` |
| Branch | `codex/reasonix-p0-task0-20260716` |
| Base SHA | `b40867229af8e17916c00393639ccb0fcb4bf6fc` |
| Head SHA | `b40867229af8e17916c00393639ccb0fcb4bf6fc` |
| `origin/main` | `b40867229af8e17916c00393639ccb0fcb4bf6fc` |
| Divergence | `origin/main...HEAD = 0/0` |
| Staged tree | empty |
| Worktree status at entry | clean |
| Plan's historical v3 baseline | `5482a52cfc256e1ee386dd3ce4e125b01e7dbc85`; superseded for this execution object |

The main checkout is outside this review object. It contains the user's pre-existing modification:

`docs/plans/2026-07-15-frontend-maintainability-error-discoverability-90-plan.md`

Task 0 does not edit, stage, reset, or review that file.

## Baseline Gates

| Command | Exit | Result |
| --- | ---: | --- |
| `git rev-parse HEAD` | 0 | exact head above |
| `git rev-parse origin/main` | 0 | exact remote-tracking SHA above |
| `git rev-list --left-right --count origin/main...HEAD` | 0 | `0 0` |
| `git diff --check` | 0 | no whitespace error |
| `git diff --cached --check` | 0 | no staged whitespace error |
| `make project-map-check` | 0 | 10 generated files up to date; drift OK |
| `make codemap-check` | 0 | 385 files, 1540 refs, 352 sections, 19 codemaps; up to date |
| `go run ./scripts/ai_maintenance plan --changed-file docs/plans/2026-07-15-reasonix-production-hardening-next-absorption.md --changed-file docs/doc/codemap/project-map/index/docs-agent.tsv` | 0 | requires `codemap:check`, `project-map:check`, `diff:whitespace`, and `generated:source` evidence |
| `go run ./scripts/ai_maintenance run --evidence docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md --changed-file docs/plans/2026-07-15-reasonix-production-hardening-next-absorption.md --changed-file docs/doc/codemap/project-map/index/docs-agent.tsv` | 0 | evidence accepted as `PLAN_BLOCKER`; codemap, project-map and whitespace gates passed |

The zero exit above means the blocked evidence is structurally valid and the command gates passed. It does not satisfy the plan's unfrozen production contracts.

## Existing Capability Freeze

The current head still contains the exact §1.4 frontend owners:

| Capability | LSP symbol evidence |
| --- | --- |
| Command registry | `frontend-app/src/app/commands/appCommandRegistry.js:2` `APP_COMMAND_IDS`; line 102 `APP_COMMAND_REGISTRY` |
| Shortcut model | `frontend-app/src/shared/keyboard/shortcutModel.js:67` `resolveShortcut`; line 87 `matchesShortcut` |
| Prompt history | `frontend-app/src/features/prompt-history/model/promptHistoryController.js` document symbols present |
| Performance pressure | `frontend-app/src/shared/diagnostics/frontendPerformancePressure.js` document symbols present |
| Timeline materialization | `frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js:14` `useTimelineMaterialization` |

LSP diagnostics returned no findings for those five files. The existing MCP dynamic-tool path still projects `DeferLoading` from `internal/platform/toolbridge/handler_peer_decode.go:697`, and `launch_agent` remains canonical in `internal/contract/orchestration.go:30`. These anchors prevent reopening the already-absorbed frontend tranche or creating a second orchestration control plane.

Persistent-agent, selected-skill, recovery, and report-control-plane behavior has not yet been re-proved end to end in this Task 0 file. That part of §1.4 remains pending rather than inferred from symbol presence.

## LSP Evidence

All calls used the review-object worktree above.

### LSP_LOCATE

- `grep(text_search)` located `validateMCPTools` at `internal/platform/toolbridge/types.go:175`.
- `grep(ast_search)` located production `ListTools` implementations at `http_mcp_client.go:139` and `stdio_mcp_client.go:138`.
- `grep(text_search)` located updater `installFromMount` at `cmd/super-dolphin-updater/install.go:107`.
- `grep(text_search)` located appupdate `ProvideConfig` at `internal/module/appupdate/service.go:128`.
- `grep(ast_search)` located `renderMCPServerConfigMap` at `internal/module/thread/start_session_helpers.go:624` and `mcpBinaryFromServerObject` at `internal/provider/shared/config_helpers.go:263`.
- `structure(document_symbol)` located desktop `main` at `cmd/agent-terminal/main.go:14` and the MCP client interface at `handler_peer_decode.go:25`.
- Shell LSP read/structure/diagnostics opened `scripts/package_macos.sh`; the current package script is 1911 lines and exposes update source/key/unsigned variables before the package graph has a canonical release descriptor.

### LSP_INSPECT

- `inspect(definition)` froze `validateMCPTools` at lines 175-188.
- `inspect(definition)` froze `updaterApp.installFromMount` at lines 107-139.
- `inspect(definition)` froze appupdate `ProvideConfig` at lines 128-173.
- `inspect(definition)` froze `renderMCPServerConfigMap` at lines 624-663.
- `inspect(definition)` froze `mcpBinaryFromServerObject` at lines 263-289.
- `inspect(implementation)` on `mcpClient.ListTools` returned HTTP, stdio, and two test implementations.

### LSP_XREF

- `validateMCPTools` is called by `validatePeerToolsListResult`; the whole-list decoder reaches it through `decodePeerToolsListResult`.
- `installFromMount` references are the wrapper and install flow; focused tests exercise waiting and copy timeout.
- `ProvideConfig` references include Fx module registration and tests for source exclusivity, HTTPS, Windows signer, version, and packaged paths.
- `mcpClient.ListTools` is consumed by `prepareMCPSurfaceBinaries` and tested by both HTTP and stdio client suites.
- `renderMCPServerConfigMap` is consumed by the thread start-config renderer and a focused NPX config test.
- `mcpBinaryFromServerObject` is consumed by the provider shared config conversion path.

### LSP_READ

1. `types.go:138-188` first unmarshals `tools` into `[]dto.MCPTool`, then validates every tool. A malformed tool therefore fails the whole tools/list result before per-tool quarantine can exist.
2. HTTP and stdio `ListTools` both call the same `decodePeerToolsListResult`; there is no transport-specific isolation seam today.
3. `prepareMCPSurfaceBinaries:180-237` records the first `ListTools` error, cancels the batch, closes clients, and returns no results. A bad server/tool can therefore abort the current surface preparation batch.
4. `installFromMount:107-139` validates, waits, verifies, replaces, and optionally restarts. It has no probation state, health ACK, retained-backup commit, or supervisor transaction.
5. `ProvideConfig:128-173` reads manifest URL/repository/channel/helper/target/public key/unsigned and Windows signer inputs from process environment. The planned package-owned source/keyring/signer authority does not exist here.
6. `cmd/agent-terminal/main.go:14-37` runs packaged runtime configuration, video environment loading, and frontend distribution resolution before `app.RunDesktop`. A Recovery selector placed only inside the Fx graph would be too late.
7. `renderMCPServerConfigMap:624-663` writes `RuntimeMCPTrustedServerIDKey = name`; `mcpBinaryFromServerObject:263-289` separately validates a server reference and writes the returned `serverID` into `MCPBinary.TrustedServerID`. Equality and generation authority are not yet proven across the producer chain.

### LSP_DIAGNOSTICS

No diagnostics were reported for the toolbridge HTTP/stdio clients, `types.go`, `handler_peer_decode.go`, desktop `main.go`, trusted-server renderer/consumer files, package shell file, or the five §1.4 frontend files.

Task 0 entry baseline contained the following 12 diagnostics, all treated as blockers under repository policy:

| File | Diagnostic |
| --- | --- |
| `cmd/super-dolphin-updater/install.go` | Information `unusedfunc` at lines 78, 385, 402, 433, 493, 510, 536, 606, 647 |
| `cmd/super-dolphin-updater/install.go` | Hint `stringsseq` at line 525 |
| `cmd/super-dolphin-updater/install.go` | Hint `stringscutprefix` at line 527 |
| `internal/module/appupdate/service.go` | Hint `stringscut` at line 730 |

The plan now defines `Task 0-D0` as the separate, scoped implementation decision for those two owner files. D0 resolved all 12 diagnostics without widening into P0 behavior.

### Task 0-D0 Resolution

LSP `xref(references)` returned zero references for each unused top-level wrapper before deletion: `install`, `mountDMG`, `detachDMG`, `expectedTeamID`, `appTeamID`, `signingDetails`, `replaceTargetApp`, `copyApp`, and `quarantineAttributeRemains`. LSP text search and `rg` confirmed that production uses the `updaterApp` methods directly and same-package tests do not use those nine wrappers. Existing test-facing wrappers such as `installFromMount`, `restartTargetApp`, `verifyAppSignature`, and `clearQuarantine` were preserved.

The three standard-library diagnostics were fixed with behavior-equivalent APIs:

- `parseSigningValue` uses `strings.SplitSeq` and `strings.CutPrefix` while preserving whitespace trimming and first-match behavior.
- appupdate `plistStringValue` uses `strings.Cut` while preserving first-key and missing-key behavior.

Post-edit LSP diagnostics for both files returned `No diagnostics found`.

| D0 command | Exit | Result |
| --- | ---: | --- |
| `./scripts/test_with_guard.sh ./cmd/super-dolphin-updater ./internal/module/appupdate -count=1` | 0 | guard passed; updater, appupdate, and automatically selected `internal/archtest` packages passed |
| `make guard` | 0 | file/function/nesting/complexity/package and priority SSA guards passed; `internal/archtest` passed |
| `make codemap-check` | 0 | 385 files, 1540 refs, 352 sections, 19 codemaps; generated files current |
| `make project-map-check` | 0 | 10 files current; strict drift OK |
| `make capcontract-check` | 0 | 41 packages, 2249 functions, 1145 methods, 183 interfaces; manifest current |
| `go run ./scripts/ai_maintenance run --evidence docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md --changed-file cmd/super-dolphin-updater/install.go --changed-file internal/module/appupdate/service.go --changed-file docs/plans/2026-07-15-reasonix-production-hardening-next-absorption.md --changed-file docs/plans/evidence/reasonix-production-hardening-next/00-task0-baseline.md --changed-file docs/plans/evidence/reasonix-production-hardening-next/01-task0-design-freeze.md` | 0 | final diff: all 15 affected Go packages passed; changed-file diagnostics `0`; codemap, project-map, capcontract, and whitespace gates passed |

## Owner And Landing Paths

### P0-A Release, update, recovery

Current owners:

- `cmd/super-dolphin-updater/install.go` and its same-package tests: detached install/replace/restart behavior.
- `internal/module/appupdate/service.go`, `manifest.go`, module/RPC/tests: update configuration, manifest, stage, helper launch.
- `cmd/agent-terminal/main.go`, `internal/app/app.go`, `internal/platform/runtimeenv`: pre-Fx and desktop preflight order.
- `scripts/package_macos.sh`, Linux/Windows package scripts, release guards, `cmd/super-dolphin-release-manifest`: package and release generation.

Exact landing paths for `ComponentRegistry`, release inventory/graph/digest/attribution, package trust/keyring, transaction state, Recovery graph, Guard worker, provider executable identity, and compatibility evidence are frozen in `01-task0-design-freeze.md`; they remain pending fresh dual review on one externally supplied immutable SHA.

### P0-B MCP schema, authority, quarantine

Current owners:

- `internal/platform/toolbridge/types.go`: tools/list envelope and whole-list validation.
- `internal/platform/toolbridge/http_mcp_client.go` and `stdio_mcp_client.go`: transport clients.
- `internal/platform/toolbridge/handler_peer_decode.go` and helpers: client interface, concurrent surface preparation, projection, lifecycle filtering, call route.
- `internal/provider/shared/config_helpers.go`: provider manifest conversion and trusted server ID.
- `internal/module/thread/start_session_helpers.go`: runtime MCP config rendering.
- `internal/app/runtimeadapter/toolbridge/adapter.go`: app-side adapter boundary.

Exact owners and landing files for raw per-tool envelopes, compiled schema, quarantine diagnostics, manifest authority snapshot, durable refresh journal, aggregate workspace snapshot, single-use admission grant, RPC principal, and provider readiness/status are frozen in `01-task0-design-freeze.md`; they remain pending fresh dual review on one externally supplied immutable SHA.

## Field Chain Starter Ledger

| FIELD_CHAIN_ID | Producer | Current consumers | Dynamic diff / roundtrip / mutation RED | Verdict |
| --- | --- | --- | --- | --- |
| `APPUPDATE_CONFIG_CURRENT` | Process environment in `ProvideConfig` | appupdate service, manifest verification, helper launch | no dynamic producer enumeration; no package-authority roundtrip; no mutation RED | `TARGET_FROZEN_PENDING_REVIEW` |
| `MCP_TOOLS_LIST_CURRENT` | peer JSON `tools` array | shared decoder -> HTTP/stdio clients -> surface preparation -> Codex dynamic projection | whole-list typed decode; no per-tool raw identity, compiler result, quarantine or mutation RED | `TARGET_FROZEN_PENDING_REVIEW` |
| `MCP_TRUSTED_SERVER_ID_CURRENT` | runtime policy validation returns `serverID` | provider `MCPBinary.TrustedServerID`; thread renderer also writes map-key `name` | equality/generation/membership not dynamically proven; no missing/stale/roundtrip/mutation RED | `TARGET_FROZEN_PENDING_REVIEW` |

No exemptions are approved. These rows preserve current-production discovery facts; the target independent `FIELD_CHAIN_ID` registry, package-local dynamic guards and mutation test identities are frozen in the design evidence.

## Review Coverage Ledger

| Dimension | Coverage | Evidence / residual |
| --- | --- | --- |
| D01 Architecture | Applied | current owners located; future owner files and subpackage boundaries frozen pending review |
| D02 Fail-fast | Applied | strict tools/list and appupdate validation read; planned no-fallback contracts not implemented |
| D03 MCP protocol | Applied | HTTP/stdio envelope, schema validation, projection, and batch abort chain proven |
| D04 LSP tooling | Applied | locate, structure, inspect, xref, read, diagnostics recorded |
| D05 Provider/runtime | Applied | trusted carrier and Codex dynamic projection touched; ingress/readiness chains pending |
| D06 Orchestration | N/A | Task 0 only verifies `launch_agent` already exists; no orchestration behavior change |
| D07 Store/sqlc | Applied | migration 118, SQL query/generated path, adapter and coordinator ownership frozen pending review |
| D08 Skill/Memory/Prompt/Thread | N/A | no behavior change in these product areas; thread file is only a current MCP config producer |
| D09 Frontend | Applied | five existing owners and diagnostics checked only to prevent duplicate implementation |
| D10 Security | Applied | signer/keyring, executable identity, ingress authority and RPC principal contracts frozen pending review |
| D11 Observability | Applied | stable quarantine/drift/failure diagnostic producers, public redaction and consumers frozen pending review |
| D12 Testing | Applied | D0 focused/impact packages pass; P0 test symbols, commands and expected RED assertions frozen, actual RED deferred to implementation task start |
| D13 Release/install | Applied | updater/appupdate/package/bootstrap current behavior located; transaction/platform/feed design frozen pending review |
| D14 Performance | Applied | compiler bytes/nodes/depth/ref/count/diagnostic/elapsed/worker/cancellation limits frozen pending review |
| D15 UX/product | N/A | no UI behavior is being implemented in Task 0 |
| D16 Git/workflow | Applied | exact worktree/base/head/status/index and excluded dirty main file recorded |
| D17 Field guard | Applied | current starter chains recorded; target independent IDs, dynamic producer guards, mutation tests and exemptions owner frozen pending review |
| D18 DRY | Applied | §1.4 existing capabilities and canonical `launch_agent` prevent duplicate control surfaces |
| D19 SSOT | Applied | current env/map-key authorities identified; target canonical registries and sole writable owners frozen pending review |

## Blocker Ledger

1. `RELEASE_TRUTH_FROZEN_PENDING_REVIEW`: owner files, schemas, generated artifacts, closure/replay tests and commands are frozen in `01-task0-design-freeze.md`.
2. `PROVIDER_EXEC_FROZEN_PENDING_REVIEW`: launch chain, prepared/process identity, capability union/matrix trust, attestation and provider tests are frozen.
3. `UPDATE_TRANSACTION_FROZEN_PENDING_REVIEW`: feeds, trust generation, state root, supervisor deadlines, environment/write set, Recovery graph, platform matrix and tests are frozen.
4. `PROVIDER_INGRESS_FROZEN_PENDING_REVIEW`: earliest gate, private authority, validated event, host drift, readiness/status and peer separation tests are frozen.
5. `MCP_AUTHORITY_FROZEN_PENDING_REVIEW`: authority snapshot, refresh journal/SQLC, aggregate snapshot, exact admission, authenticated RPC proof/principal/token and tests are frozen.
6. `FIELD_GUARD_FROZEN_PENDING_REVIEW`: every §4.3 producer has an independent `FIELD_CHAIN_ID`, package-local dynamic guard owner and canonical command.
7. `SCHEMA_COMPILER_FROZEN_PENDING_REVIEW`: exact module/tag/sums/license, loader policy, Draft, budgets, cancellation rule and pre-dependency fail-first gate are frozen.
8. `REVIEW_BLOCKED`: the committed object has not received two fresh reviewer lanes with `0 P0 / 0 P1` on the same SHA resolved by the review controller at invocation time.

## Current Decision

Task 0-D0 is complete and Task 0-Design is `DESIGN_FROZEN_PENDING_REVIEW`. Task 0 as a whole is not complete because the fresh dual review is still absent.

`implementation_design_complete=false`

`p0_executable=false`

Production Task 1 and Task 4 must not start while any blocker above remains.

INITIAL_DESIGN_FREEZE_COMMIT: `785dd9e6b4e480b4b89d7f750c4ebd02a6612a93`

REVIEW_OBJECT_SHA: `RESOLVE_EXTERNALLY_WITH_GIT_REV_PARSE_HEAD`

REMOTE_SHA: `NONE`
