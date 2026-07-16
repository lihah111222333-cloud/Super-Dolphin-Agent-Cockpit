# Reasonix Production Hardening - Task 0 Design Freeze

TASK_ID: `TASK_0_DESIGN`

STATUS: `DESIGN_FROZEN_PENDING_REVIEW`

BASE_HEAD: `b40867229af8e17916c00393639ccb0fcb4bf6fc`

`implementation_design_complete=false`

`p0_executable=false`

本文件把 Task 0 的剩余设计 blocker 冻结为 exact owner、landing file、schema/version、producer、consumer、具名测试、命令、fixture 和预计生成物。它不实现 P0 行为，不把预计 RED 写成已运行 RED，也不替代两条 fresh reviewer lane。

## 1. Common Contract

- 所有新增持久化或签名 schema 使用整数 `schema_version=1`；未知版本 fail-fast。
- 所有 identity 先 `TrimSpace`，再按 owner 规则校验；空值、重复值、未知 enum 和多 variant 混填 fail-fast。
- `canonical_json_v1` 使用 UTF-8、无 BOM、无 insignificant whitespace、struct 字段声明顺序、map key bytewise ascending、`json.Number` 精确十进制、拒绝 duplicate key/NaN/Inf、字符串不做隐式语义改写。
- 所有 contract digest 使用 `sha256` lowercase hex，并绑定 `schema_version + canonical_encoding + subject_type + subject_id + canonical_bytes`，禁止裸 payload hash 跨 subject 复用。
- 生产 struct 字段由 reflection、JSON schema、SQLC row metadata 或 canonical registry 动态枚举；测试手写 required-field slice 不能作为字段真相源。
- 真实 fail-first RED 在对应实现任务的第一个 commit 前运行；本文件只冻结测试身份、命令和预期失败断言。

## 2. Release Truth Freeze

唯一模块 owner 为 `internal/module/appupdate/releaseunit`。package/publish 脚本只能调用 `cmd/super-dolphin-release-manifest`，不能各自重新实现 registry、inventory、graph、digest 或 trust verifier。

| Landing file | Producer / contract |
| --- | --- |
| `internal/module/appupdate/releaseunit/model.go` | `ComponentRegistry`, `ProductIPBoundary`, `ThirdPartyComponentDescriptor`, `ReleaseUnitDescriptor`, `ObservedArtifactInventory`, `ReleaseComponentGraph`, `ReleaseDigestGraph`, `ReleaseAttributionBundleDescriptor` |
| `internal/module/appupdate/releaseunit/registry.go` | canonical component registry v1 and generated ownership projections |
| `internal/module/appupdate/releaseunit/canonical.go` | `canonical_json_v1`, subject-bound SHA-256 digest |
| `internal/module/appupdate/releaseunit/inventory.go` | subject-external final artifact extraction contract and declared/observed bidirectional diff |
| `internal/module/appupdate/releaseunit/component_graph.go` | Go build info, `go.mod/go.sum`, npm lock/bundle, native scan and bundle manifest graph derivation |
| `internal/module/appupdate/releaseunit/digest_graph.go` | acyclic phase-ordered digest DAG and release-unit edge verifier |
| `internal/module/appupdate/releaseunit/trust.go` | `ReleaseAttributionTrustPolicy`, `ProviderCLICompatibilityTrustPolicy`, `PackageTrustPolicy`, `UpdateSourceDescriptor`, `UpdateTrustKeyring` |
| `internal/module/appupdate/releaseunit/verify.go` | strict version, ownership, redistribution evidence, signature usage/generation/validity/revocation and replay checks |
| `cmd/super-dolphin-release-manifest/main.go` | sole CLI adapter; final artifact observation happens after platform sign/notarize/staple |
| `scripts/package_macos.sh`, `scripts/package_linux.sh`, `scripts/package_windows_local.ps1`, `scripts/publish_github_release.sh` | invoke the sole CLI and fail when platform capability is disabled or generated closure differs |

`ComponentRegistry` is the only writable ownership SSOT. `ProductIPBoundary`, third-party descriptors, README/NOTICE/SBOM/About/release-note data are generated projections. OpenAI Codex CLI/app-server and Anthropic Claude CLI remain third-party components; package/sign/install operations do not transfer ownership.

Expected generated artifacts under `dist/release/metadata/v1/`:

- `component-registry.json`
- `product-ip-boundary.json`
- `third-party-components.json`
- `release-unit.json`
- `observed-artifact-inventory.json`
- `release-component-graph.json`
- `release-digest-graph.json`
- `release-attribution.json` and `release-attribution.sig`
- `provider-cli-matrix.json` and `provider-cli-matrix.sig`
- `update-source.json`, `update-keyring.json`, `package-trust-policy.json`
- `sbom.cdx.json`, `NOTICE.generated`

Frozen tests:

| Test | Command | Expected RED |
| --- | --- | --- |
| `TestComponentRegistryProjectionFieldChains` | `./scripts/test_with_guard.sh ./internal/module/appupdate/releaseunit -run '^TestComponentRegistryProjectionFieldChains$' -count=1` | missing/stale/duplicate component, ownership mismatch, reverse-written projection |
| `TestObservedArtifactInventoryDeclaredClosure` | same package, `-run '^TestObservedArtifactInventoryDeclaredClosure$'` | extra/missing member, path escape, symlink/type/mode/hash mismatch, stale exemption |
| `TestReleaseComponentGraphSBOMClosure` | same package, `-run '^TestReleaseComponentGraphSBOMClosure$'` | added/removed Go/npm/native/bundle dependency absent from registry/SBOM |
| `TestReleaseDigestGraphTrustAndReplay` | same package, `-run '^TestReleaseDigestGraphTrustAndReplay$'` | cycle, self/downstream reference, missing release-unit edge, wrong phase/key/usage, N-1 replay |
| `TestFinalArtifactObservationAfterPlatformSigning` | `./scripts/test_with_guard.sh ./cmd/super-dolphin-release-manifest -run '^TestFinalArtifactObservationAfterPlatformSigning$' -count=1` | declared list used as observed truth or observation before final platform signing |
| `TestGeneratedOwnershipClaimsDoNotDrift` | `./scripts/test_with_guard.sh ./scripts -run '^TestGeneratedOwnershipClaimsDoNotDrift$' -count=1` | README/NOTICE/SBOM/About/release-note projection differs from registry |

## 3. Provider Execution And Ingress Freeze

The shared owner is `internal/provider/shared`; provider-specific packages may adapt commands and events but cannot mint compatibility trust, process identity, ingress authority or public attestation independently.

| Landing file | Producer / contract |
| --- | --- |
| `internal/provider/shared/executable_contract.go` | `ProviderExecutionComponentPolicy`, `ResolvedProviderExecutable`, `ProviderExecutableLaunchPlan`, `PreparedProviderExec`, `ProviderProcessIdentity` |
| `internal/provider/shared/executable_resolver.go` | bundled/PATH/managed source selection and full launcher/interpreter/target chain |
| `internal/provider/shared/executable_prepare_darwin.go`, `executable_prepare_unix.go`, `executable_prepare_windows.go` | platform pre-exec file/volume/signature handle or immutable staging identity |
| `internal/provider/shared/compatibility_contract.go` | strict `HandshakeEvidence | MatrixEvidence | BlockedEvidence`, `ProviderCLICompatibilityMatrix`, `ProviderCLIRuntimeDescriptor` |
| `internal/provider/shared/compatibility_verify.go` | exact subject and package-anchored matrix trust verification |
| `internal/provider/shared/attestation.go` | allowlisted `ProviderExecutableAttestation`, `ExecutionLayerFailure`, redaction-before-digest |
| `internal/provider/shared/ingress_contract.go` | opaque `ProviderIngressAuthority`, `ProviderIngressEnvelope`, `ValidatedProviderEvent`, `ProviderProtocolDrift` |
| `internal/provider/shared/ingress_gate.go` | sole private issuer and earliest protocol gate |
| `internal/provider/shared/mcp_capability.go` | `ProviderMCPCapabilityStatus`, generation-bound `MCPReadinessAttestation`; no v3 peer authority conversion |
| `internal/provider/codexapp/pool_spawn_cmd.go`, `transport_helpers.go`, `server_pool.go`, `driver_pool_routing.go` | consume one prepared identity for auth/spawn/reuse; exact tuple in pool key |
| `internal/provider/claudecli/config.go`, `auth_preflight.go`, `transport.go`, `transport_unix.go`, `transport_windows.go`, `driver.go` | consume one prepared identity for auth/start/restart; Claude remains direct MCP observation only |
| `internal/contract/runtime_reporter.go`, `internal/dto/agent/runtime.go`, `internal/app/runtime_reporter_adapter.go` and existing status consumers | typed public projection only; no path, HOME, provider-home, credential, raw payload/stderr or low-entropy stable hash |

Frozen tests:

| Test | Owner package | Expected RED |
| --- | --- | --- |
| `TestProviderExecutableLaunchChainIsPreparedOnce` | `internal/provider/shared` | PATH/shim/interpreter/target/file identity changes between prepare and spawn; replacement executable call count must remain zero |
| `TestProviderCapabilityEvidenceVariantAndMatrixSubject` | `internal/provider/shared` | mixed/empty variant, source/OS/arch/launcher/version/protocol/schema/capability/trust mismatch, wrong signer/usage/replay |
| `TestProviderExecutableAttestationRedactsBeforeDigest` | `internal/provider/shared` | absolute path, HOME, provider-home, secret, raw payload/stderr or stable secret hash reaches public projection |
| `TestProviderIngressGatePrecedesEveryConsumer` | `internal/provider/shared` | unknown type/bad JSON dropped or state/action/raw-bus/translator runs before gate |
| `TestValidatedProviderEventFieldChains` | `internal/provider/shared` | authority/host/process/transport/ingress generation/protocol/kind/sequence/variant missing or consumer reconstructs from raw |
| `TestCodexPoolRejectsPreparedIdentityDrift` | `internal/provider/codexapp` | pool reuse across process/matrix/source generation |
| `TestClaudeMCPReadinessBlocksPayloadUntilExactTuple` | `internal/provider/claudecli` | user payload sent with missing/stale/partial/extra readiness tuple or post-first-message init |
| `TestPeerRuntimeReportCannotOverwriteProviderTruth` | `internal/platform/mcpcontrol` | peer report injects CLI identity/version/matrix/readiness/status/failure/drift |

Focused command:

`./scripts/test_with_guard.sh ./internal/provider/shared ./internal/provider/codexapp ./internal/provider/claudecli ./internal/platform/mcpcontrol ./internal/contract ./internal/dto/agent ./internal/app -count=1`

## 4. Update Transaction And Recovery Freeze

| Landing file | Owner / contract |
| --- | --- |
| `internal/module/appupdate/recovery/model.go` | `UpdateTransaction`, `TrustGenerationState`, `StartupAttempt`, health ACK and stable status codes |
| `internal/module/appupdate/recovery/state.go` | append-only state transitions and transaction lineage |
| `internal/module/appupdate/recovery/fileset.go` | release members, backup/staging parity and `PreHealthyWriteSet` journal |
| `internal/module/appupdate/recovery/lock_darwin.go`, `lock_unix.go`, `lock_windows.go` | cross-process owner lock and fail-closed timeout |
| `internal/module/appupdate/recovery/fsync_darwin.go`, `fsync_unix.go`, `fsync_windows.go` | file + parent directory durability contract |
| `internal/module/appupdate/recovery/supervisor.go` | lease, process identity, deadlines, bounded stop/kill, rollback and healthy commit |
| `internal/module/appupdate/recovery/environment.go` | immutable `NormalProcessEnvPlan` and recovery/Guard allowlist |
| `internal/module/appupdate/recovery/writers.go` | dynamic pre-healthy writer registry; unregistered writer blocks launch |
| `cmd/super-dolphin-updater/install.go`, `transaction.go`, `supervisor.go` | consume prepared transaction, retain backup through probation, report exact status |
| `cmd/super-dolphin-guard/main.go`, `check.go`, `launch.go`, `recover.go`, `status.go`, `restore.go` | detached minimal worker; no desktop/provider/toolbridge/store graph |
| `cmd/agent-terminal/main.go`, `internal/app/recovery_bootstrap.go`, `internal/app/recovery_module.go` | pre-Fx selector, transaction child re-exec, standalone Recovery graph |
| `internal/platform/runtimeenv/normal_process_env.go` | packaged source precedence and explicit normal child `Cmd.Env` |
| `internal/archtest/backend_boundary_registry.go` | exact registration for `cmd/super-dolphin-guard`; no broad command/module allowlist |

State root is `<app-state>/update-transactions/v1/`. `current.json` points to one transaction directory containing `transaction.json`, `attempts.jsonl`, `trust-generation.json`, `pre-healthy-writes.jsonl`, `release.new/`, and same-volume `release.old/`. Every file and containing directory is fsynced before the next state transition.

Frozen numeric policy:

| Parameter | Value |
| --- | ---: |
| lock acquisition deadline | 5 seconds |
| supervisor registration deadline | 10 seconds |
| bootstrap ACK deadline | 30 seconds |
| healthy probation window | 90 seconds |
| graceful stop deadline | 10 seconds |
| bounded termination deadline | 5 seconds |
| stale lease takeover threshold | 45 seconds |
| maximum automatic rollback attempts | 1 per transaction |

Platform capability v1: macOS packaged DMG is enabled. Windows and Linux transactional update config, Check, Install, and update-manifest publication are disabled until the same transaction/rollback contract has native GREEN evidence; they return `app_update_transaction_unsupported_platform`. This disables the current Windows direct-installer update route instead of describing it as safe.

The legacy bootstrap feed remains `latest-legacy.json`; the transactional feed is physically separate as `latest-transactional-v2.json`. A legacy client can only receive the old-compatible bootstrap. Transactional clients below protocol v2 return `manual_upgrade_required`.

Frozen tests:

- `TestUpdateTransactionCrashMatrix` in `internal/module/appupdate/recovery`
- `TestTrustGenerationPromotesOnlyAfterHealthyACK` in `internal/module/appupdate/recovery`
- `TestPreHealthyWriterRegistryAndRollbackParity` in `internal/module/appupdate/recovery`
- `TestUpdaterRetainsBackupThroughProbation` in `cmd/super-dolphin-updater`
- `TestGuardRestoresExactTransactionAfterSupervisorCrash` in `cmd/super-dolphin-guard`
- `TestAgentTerminalSelectorRunsBeforeNormalEnvironment` in `cmd/agent-terminal`
- `TestRecoveryGraphContainsNoNormalConstructors` in `internal/app`
- `TestUnsupportedPlatformsCannotCheckInstallOrPublish` in `internal/module/appupdate` and `scripts`
- `TestLegacyAndTransactionalFeedsRemainPhysicallySeparate` in `scripts`

Focused command:

`./scripts/test_with_guard.sh ./internal/module/appupdate/... ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./cmd/agent-terminal ./internal/app ./internal/platform/runtimeenv ./internal/archtest ./scripts -count=1`

## 5. MCP Authority, Refresh And RPC Freeze

Shared types move to a budget-safe subpackage rather than growing the already-large root contract file.

| Landing file | Owner / contract |
| --- | --- |
| `internal/contract/mcpcontrol/authority.go` | `MCPManifestAuthoritySnapshot`, `ResolvedMCPManifest`, current read/CAS ports |
| `internal/contract/mcpcontrol/refresh.go` | refresh states, per-server committed state, `CommittedMCPWorkspaceSnapshot` |
| `internal/contract/mcpcontrol/admission.go` | opaque `MCPPeerRuntimeAuthority`, `AdmissionGrant`, reservation and `NoEntry | MayHaveEntered | EffectResolved` |
| `internal/contract/mcpcontrol/rpc.go` | server-only `AuthenticatedRPCConnection`, `RPCSessionPrincipal`, workspace token claims |
| `internal/app/runtimeadapter/toolbridge/adapter.go` | sole module-to-platform adapter for authority, refresh, aggregate read and admission ports |
| `internal/platform/toolbridge/types.go`, `http_mcp_client.go`, `stdio_mcp_client.go` | wire envelope decode with `[]json.RawMessage`; no whole-list tool decode before class resolution |
| `internal/platform/toolbridge/schema/compile.go`, `policy.go` | canonicalize, preflight budgets, compile and stable diagnostic classes without growing the already-large root package |
| `internal/platform/toolbridge/schema/quarantine.go` | trusted-external per-tool quarantine plan; root toolbridge owns lifecycle application |
| `internal/platform/toolbridge/admission/grant.go`, `arguments.go` | immutable args copy/digest and grant validation; root toolbridge owns the exact client call barrier |
| `internal/module/mcp_server/refresh_coordinator.go` | only durable refresh/admission owner and startup takeover |
| `internal/module/mcp_server/workspace_snapshot.go` | sorted server vector and aggregate digest |
| `internal/store/mcpserver/refresh.go` | SQLC adapter; no durable `applied` state |
| `internal/platform/db/sqlite/migrations/118_mcp_refresh_authority.sql` | journal, per-server state, aggregate snapshot, quarantine and reservation tables |
| `sql/queries/mcp_refresh_authority.sql` and generated `internal/store/sqlc/mcp_refresh_authority.sql.go` | prepared/terminal CAS, takeover, transaction commit and current read queries |
| `internal/platform/rpc/authenticated_connection.go`, `transport_ws.go`, `control_rpc_auth.go`, `server.go` | proof issuance only after Wails HTTP guard or successful `ctl/register` |
| `internal/ui/wails/http_server.go`, `window_state.go`, `lifecycle.go` | per-window owner selected; close destroys owner and revokes principals/tokens |
| `internal/app/tool_catalog_rpc.go` | principal-bound `toolbridge/workspace/resolve(CWD)`; clients never provide workspace ID/roots |

Refresh transaction states are exactly `prepared -> committed|aborted|superseded`. `prepared` persists previous committed vector/digest, owner lease, deadline, plan digest and fencing token. Commit atomically writes lifecycle, quarantine, per-server state, sorted workspace aggregate and terminal journal state. Startup takeover is CAS by stale lease and never backfills twice.

Every side-effecting call reserves `(principal_id, host_call_id)`, receives one non-serializable grant bound to workspace/server aggregate, canonical tool, compiled schema digest, lifecycle, exact peer/client/process, immutable canonical arguments, expiry and fence, then consumes it at the exact client. N+1 revokes unconsumed grants or bounded-drains grants already inside the old exact client.

Frozen tests:

- `TestToolsListWireDecodeDefersPerToolSchema` in `internal/platform/toolbridge`
- `TestTrustedExternalSchemaFailureQuarantinesOneTool` in `internal/platform/toolbridge`
- `TestManagedSchemaFailureWritesNothing` in `internal/platform/toolbridge`
- `TestRefreshCoordinatorCrashTakeoverAndAtomicCommit` in `internal/module/mcp_server`
- `TestWorkspaceAggregateVectorIsSortedAndCurrent` in `internal/module/mcp_server`
- `TestAdmissionGrantExactBindingSingleUseAndNPlusOne` in `internal/platform/toolbridge`
- `TestAdmissionReservationBlocksMayHaveEnteredRetry` in `internal/module/mcp_server`
- `TestAuthenticatedConnectionCannotBeForged` in `internal/platform/rpc`
- `TestWailsWindowCloseRevokesPrincipalAndToken` in `internal/ui/wails`
- `TestWorkspaceResolveRejectsClientOwnedScope` in `internal/app`
- `TestMCPRefreshSQLCFieldChains` in `internal/store/mcpserver`

Focused command:

`./scripts/test_with_guard.sh ./internal/contract/mcpcontrol ./internal/app/runtimeadapter/toolbridge ./internal/platform/toolbridge ./internal/module/mcp_server ./internal/store/mcpserver ./internal/platform/rpc ./internal/ui/wails ./internal/app -count=1`

## 6. Field Guard Freeze

The canonical consumer registry is `internal/archtest/p0_field_chain_registry.go`; it records `FIELD_CHAIN_ID`, producer type/schema owner, consumer owner and mutation test symbol, but never lists producer fields. `TestP0FieldChainRegistryCoverage` fails on duplicate IDs, missing owner, missing test symbol, stale exemption or a producer without a package-local dynamic guard.

| Guard owner test file | Independent FIELD_CHAIN_ID values |
| --- | --- |
| `internal/module/appupdate/releaseunit/field_chain_guard_test.go` | `RELEASE_COMPONENT_REGISTRY`, `RELEASE_PRODUCT_IP_BOUNDARY`, `RELEASE_THIRD_PARTY_COMPONENT`, `RELEASE_UNIT_DESCRIPTOR`, `RELEASE_OBSERVED_INVENTORY`, `RELEASE_COMPONENT_GRAPH`, `RELEASE_DIGEST_GRAPH`, `RELEASE_ATTRIBUTION_BUNDLE`, `RELEASE_ATTRIBUTION_TRUST`, `RELEASE_PACKAGE_TRUST`, `RELEASE_UPDATE_SOURCE`, `RELEASE_UPDATE_KEYRING` |
| `internal/provider/shared/field_chain_guard_test.go` | `PROVIDER_EXECUTION_POLICY`, `PROVIDER_RESOLVED_EXECUTABLE`, `PROVIDER_LAUNCH_PLAN`, `PROVIDER_PREPARED_EXEC`, `PROVIDER_PROCESS_IDENTITY`, `PROVIDER_PUBLIC_ATTESTATION`, `PROVIDER_CAPABILITY_EVIDENCE`, `PROVIDER_COMPATIBILITY_MATRIX`, `PROVIDER_COMPATIBILITY_TRUST`, `PROVIDER_RUNTIME_DESCRIPTOR`, `PROVIDER_MCP_STATUS`, `PROVIDER_MCP_READINESS`, `PROVIDER_EXECUTION_FAILURE`, `PROVIDER_INGRESS_AUTHORITY`, `PROVIDER_INGRESS_ENVELOPE`, `PROVIDER_VALIDATED_EVENT`, `PROVIDER_NONCRITICAL_EVENT_REGISTRY`, `PROVIDER_PROTOCOL_DRIFT` |
| `internal/platform/mcpcontrol/field_chain_guard_test.go` | `MCP_PEER_RUNTIME_REPORT` |
| `internal/module/appupdate/recovery/field_chain_guard_test.go` | `UPDATE_TRUST_GENERATION`, `UPDATE_STARTUP_ATTEMPT`, `UPDATE_NORMAL_ENV_PLAN`, `UPDATE_PREHEALTHY_WRITESET`, `UPDATE_RECOVERY_PROJECTION`, `UPDATE_HEALTH_ACK` |
| `internal/contract/mcpcontrol/field_chain_guard_test.go` | `MCP_MANIFEST_AUTHORITY`, `MCP_RESOLVED_MANIFEST`, `MCP_REFRESH_JOURNAL`, `MCP_SERVER_COMMITTED_STATE`, `MCP_WORKSPACE_SNAPSHOT`, `MCP_PEER_AUTHORITY`, `MCP_ADMISSION_GRANT`, `RPC_AUTHENTICATED_CONNECTION`, `RPC_SESSION_PRINCIPAL`, `RPC_WORKSPACE_TOKEN` |
| `internal/platform/toolbridge/field_chain_guard_test.go` | `MCP_MANAGED_SERVER_REGISTRY`, `MCP_QUARANTINE_ROW`, `MCP_CATALOG_DTO`, `MCP_DIAGNOSTIC_DTO` |
| `internal/platform/toolbridge/schema/field_chain_guard_test.go` | `MCP_COMPILED_TOOL_SCHEMA` |

Each package-local guard must dynamically enumerate the production producer, execute missing/stale/roundtrip/deep-copy checks where applicable, and run at least one real mapper or terminal-consumer mutation. Every failure includes exact `FIELD_CHAIN_ID`, producer and field. Exemptions live in `internal/archtest/testdata/p0_field_chain_exemptions_v1.json` with `field`, `direction`, `reason`, `evidence`, `owner`, and expiry; invalid or expired entries fail.

Canonical commands:

- `./scripts/test_with_guard.sh ./internal/archtest -run '^TestP0FieldChainRegistryCoverage$' -count=1`
- `./scripts/test_with_guard.sh ./internal/module/appupdate/releaseunit ./internal/module/appupdate/recovery ./internal/provider/shared ./internal/platform/mcpcontrol ./internal/contract/mcpcontrol ./internal/platform/toolbridge -run 'FieldChain|FieldChains' -count=1`

## 7. JSON Schema Compiler And Dependency Gate Freeze

Selected module: `github.com/santhosh-tekuri/jsonschema/v6@v6.0.2`.

Frozen supply-chain facts:

- tag commit: `29cbed948d24a04700eb94436416b18a07953b71`
- module sum: `h1:KRzFb2m7YtdldCEkzs6KqmJw4nqEVZGK7IN2kJkjTuQ=`
- go.mod sum: `h1:JXeL+ps8p7/KNMjDQk3TCwPpBy0wYklyWTfbkIzdIFU=`
- license: Apache-2.0; `LICENSE` SHA-256 `c8858a5a76440bbca484e134cf7df46385d090dd18b2c58e650f939258802e5b`
- module Go version: 1.21; repository Go version is 1.25.7
- unpacked source measured in this review object: 476 KiB / 85 files
- production imports add only `golang.org/x/text`, already present in the repository; the selected module also declares `github.com/dlclark/regexp2 v1.11.0` for its own tests, which is a new module-graph entry and must be recorded by the dependency gate even though production does not import it
- official release and package documentation identify v6.0.2 as latest, support Draft 4/6/7/2019-09/2020-12, expose custom loaders, and document loop detection

Sources:

- `https://github.com/santhosh-tekuri/jsonschema/releases/tag/v6.0.2`
- `https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6@v6.0.2`
- `https://github.com/santhosh-tekuri/jsonschema/blob/v6.0.2/LICENSE`

Compiler policy v1:

- default and only accepted dialect is Draft 2020-12; missing `$schema` is normalized to that exact URI
- external `$ref`, `$dynamicRef`, custom `$schema`, file/http/https/data URIs and non-fragment refs are rejected in the prewalker
- the compiler is configured with a deny-all `URLLoader`; only one in-memory `urn:super-dolphin:mcp-schema:<digest>` resource is registered
- canonical output is immutable `CompiledToolSchema{schema_version,draft,canonical_json,digest,diagnostic}`; consumers never recompile or recompute digest
- compile scheduling uses a fixed pool of 4 workers; context cancellation stops queue admission; at most 4 bounded preflight-approved compiles remain in flight
- elapsed limit is measured around compile and rejects the result after 250 ms; if adversarial fixtures show any in-flight compile can exceed 500 ms after cancellation, Task 4 remains blocked until process isolation replaces the worker pool

Resource budgets:

| Budget | Limit |
| --- | ---: |
| raw schema bytes per tool | 65,536 |
| canonical schema bytes per tool | 65,536 |
| schema nodes per tool | 4,096 |
| maximum nesting depth | 64 |
| local reference count | 128 |
| regex/pattern count | 128 |
| tools per server | 512 |
| aggregate raw schema bytes per server batch | 8,388,608 |
| diagnostics retained per server | 100 |
| public diagnostic summary bytes | 32,768 |
| compile elapsed per tool | 250 ms |
| compile workers | 4 |

Before `go.mod` changes, Task 4 must add:

- `scripts/dependency_policy/main.go` and `main_test.go`
- `docs/契约/go-dependency-policy-v1.json` with module/version/sums/tag/license/license-hash/owner/usage
- Make target `dependency-policy-check`
- pre-commit and pre-push invocation of `make dependency-policy-check`
- pinned vulnerability command `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 -test ./internal/platform/toolbridge`
- fail-first tests `TestDependencyPolicyRejectsUnregisteredModule`, `TestDependencyPolicyRejectsVersionSumOrLicenseDrift`, and `TestSchemaCompilerRejectsExternalLoaderAndBudgets`

The dependency is approved for Task 4 only after those tests are observed RED and the versioned gate is GREEN. Task 0 does not add it.

## 8. Remaining Review Blocker

The seven design domains above are frozen for review. The current exact review object remains uncommitted, so no external reviewer result can yet bind immutable path/line/bytes/SHA.

Required final Task 0 sequence:

1. Commit only the Task 0 plan/evidence and D0 changes with Chinese commit title.
2. Record immutable commit SHA and `origin/main` base in both evidence files.
3. Run two independent fresh reviewer lanes over that exact SHA using D01-D19; each reports coverage, findings and residual risk.
4. Apply findings, create a new immutable SHA, and repeat both lanes until `0 P0 / 0 P1` on the same final object.
5. Only then set `implementation_design_complete=true` and `p0_executable=true`.

Current verdict: `REVIEW_BLOCKED`; Task 1 and Task 4 remain closed.
