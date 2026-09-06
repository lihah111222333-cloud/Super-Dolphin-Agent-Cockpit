# RecoveryCustody 外部单实例 / SQLite 专项 Gate（planning-only）

Status: `pending`

Decision: `NO-GO`

Authority: `planning_only`

日期：2026-09-03；父工作包：[Gate S0](README.md)。

Gate-ID: `RECOVERY_CUSTODY_SINGLETON_S0`

S0-Owner: `ROLLOUT`（单实例/迁移/回滚）、`ID`（owner generation/head）、`OUT`（operation/receipt）、`VERIFY`（证据与 guard）；CONTENT/RESOURCE 只提供其各自 proof，不得代替 custody owner。

S0-Scope: 外部 RecoveryCustody 服务的唯一 owner、fresh/既有 SQLite bootstrap、unknown receipt reconcile、private SQLite/no-PG、network-FS identity 和 trust-root/anti-rollback；不包含 provider dispatch、Room 或 public tool 启用。

S0-Dependencies: `VERIFY_S`, `ROLLOUT_S`, `VERIFY_DONE`, `PRECANARY_DONE`（这些是既有祖先门，不新增 DAG edge）。

S0-Inputs: canonical authority scope/path、owner epoch/fence、migration revision/manifest、operation+digest、typed receipt/observation、filesystem classification、anchor predecessor/result、security-time/retention/trust-root generation。

S0-Outputs: 唯一 owner lease/fence、fresh/open 的 schema identity、原 operation 的 typed observation/indeterminate、验签且 predecessor-CAS 的 terminal candidate；所有输出均为 planning acceptance 条件，不是授权。

S0-Failure: duplicate/stale owner、migration drift/partial marker、commit/receipt unknown、generic/product/PG path、network/unknown filesystem、trust-root/anchor/rollback mismatch 均 fail-fast，zero-row/zero-startup-side-effect，并保持 `pending|indeterminate|unavailable`。

S0-Recovery: 只 query 原 operation；owner crash 需 lease expiry + predecessor exact CAS takeover；migration 只原子 rollback/quarantine 后人工重试；filesystem/trust-root/anchor 不确定时 query-only，禁止换 key、复制 DB、fresh genesis 或 fallback backend。

S0-Named-Acceptance: `RED-RC-SINGLETON-DUPLICATE-OWNER`, `GREEN-RC-SINGLETON-TAKEOVER-CAS`, `Guard-RC-SINGLETON-ONE-WRITER`, `RED-RC-MIGRATION-IMPLICIT-UPGRADE`, `GREEN-RC-MIGRATION-FRESH-AND-REOPEN`, `Guard-RC-MIGRATION-SCHEMA-OWNER`, `RED-RC-UNKNOWN-REFINALIZE`, `GREEN-RC-UNKNOWN-ORIGINAL-QUERY`, `Guard-RC-UNKNOWN-RECEIPT-DIGEST`, `RED-RC-NOPG-GENERIC-PATH`, `GREEN-RC-NOPG-EXTERNAL-MODE`, `Guard-RC-NOPG-PRODUCTION-IMPORT`, `RED-RC-NETWORK-FS-REJECT`, `GREEN-RC-NETWORK-FS-CANONICAL-IDENTITY`, `Guard-RC-NETWORK-FS-OPEN-PATH`, `RED-RC-TRUST-ROOT-TAMPER`, `GREEN-RC-TRUST-ROOT-VERIFIED-WINNER`, `Guard-RC-TRUST-ROOT-DIGEST-COVERAGE`。

## 定位与 DAG 审计

本专项是对 RecoveryCustody 生产方向的可复查 gate checklist，不是新的 runtime owner、receipt 签发器、数据库、授权器或发布开关。只读审计 `release-gate-dag.json` 的 28 个 node、63 条 edge 和 8 个 cross-task requirement 后，确认其中没有 singleton、RecoveryCustody、SQLite authority 或 PostgreSQL exclusion 的等价 gate；已有的
[SQLite authority 候选](recovery-custody-sqlite-authority.md)、[服务候选](recovery-custody-service-candidate.md)和[生产决策门](content-resource-custody-decision-gate.md)是设计/实现记录，不替代本专项的逐项输入输出和故障恢复裁决。

为保持 Gate S0 机器输入与总规格 Mermaid 完全一致，本文件不新增 DAG node、edge、cross-task capability 或第三份 machine JSON。它挂在既有 `VERIFY_S`、`ROLLOUT_S`、`VERIFY_DONE`、`PRECANARY_DONE` 祖先门之下；本文件所有状态只能保持 `pending` 或 `needs_revalidation`，决策只能是 `NO-GO`，不能因本地测试或矩阵完整而改变。

## 不可变生产方向

- RecoveryCustody 必须是外部单实例服务；该进程是 authority SQLite、writer fence、submission/resolution/head 和 terminal winner 的唯一写 owner。
- authority SQLite、WAL/SHM、lock sentinel、备份和同盘副本必须位于独立私有 state root；orch 只能使用认证协议 client，不得打开、迁移或共享 authority 文件。
- PostgreSQL、产品主 SQLite、network filesystem、普通文件 lock、同盘备份和测试替身都不是兼容后端，也不能生成 availability/abort terminal receipt。
- SQLite commit 只能产生 `pending`；外部 EvidenceFinalizer、anti-rollback anchor、security-time、retention 和签名 trust root 缺任一项时，结果必须是 stable `indeterminate`/`unavailable`，不得进入 `accepted`、`available`、`aborted` 或 provider dispatch。

## Gate 输入、输出与总失败语义

每个检查项必须同时记录：canonical authority scope/path、owner generation/epoch、operation ID + digest、schema/migration revision、receipt/anchor/trust-root digest、filesystem classification 和 rollback predecessor。缺字段、重复字段、未知字段、路径 alias、digest 漂移、response unknown 或证据只来自本机时，输出为 `pending|indeterminate` 并保持零副作用；不得从 namespace、文件名、客户端 metadata、SQLite commit 或 provider ACK 推断成功。

| Check ID | 必须覆盖的输入 | 允许的输出 | 失败与恢复边界 | 当前状态 / 决策 |
| --- | --- | --- | --- | --- |
| `RC-SINGLETON-OWNER` | canonical `AuthorityScope`、private parent/db/WAL/SHM identity、lock identity、owner boot/process identity、当前 `writer_epoch/deployment_fence` | 首 owner 的 lease + DB fence；重复 owner typed busy；takeover 仅 predecessor exact CAS 后生成 successor generation | duplicate/stale/alias/lease drift → fail-fast、query-only、zero-row write；crash 或 commit unknown → 只 query 原 operation，验证 lease/anchor 后再 takeover，不能 fresh genesis | `needs_revalidation` / `NO-GO` |
| `RC-MIGRATION-BOOTSTRAP` | 空库 fresh-bootstrap 标记、已存在 revision/head/mode/fence、migration manifest/hash、单实例 lease | fresh 空库一次性 bootstrap 到冻结 revision；已存在库只做完整 identity/schema read-back；旧 revision 明确拒绝 | revision/mode/manifest 不匹配、并发 migration、leader crash 或部分 marker → 原子 rollback/保持旧库、quarantine 并人工重试；禁止隐式升级、双 runner 或 product DB fallback | `pending` / `NO-GO` |
| `RC-UNKNOWN-RECEIPT` | 原 submission/resolution/finalization operation + digest、attempt/head predecessor、typed observation generation/read fence、外部 provider receipt/query key | `absent|inflight|finalized` observation；仅原 operation 的 deterministic replay；`outcome_unknown`/`indeterminate` | timeout、commit outcome unknown、late/ambiguous receipt、anchor unavailable → query-only、保留原句柄和 reservation；不得换 key、重复 Finalize、伪造 acknowledged/terminal 或提前 release | `needs_revalidation` / `NO-GO` |
| `RC-PRIVATE-SQLITE-NO-PG` | `external_service=true`、独立 absolute private state root、owner-only modes、SQLite DSN/PRAGMA；显式无 product/workspace/PG path | 服务只打开自己的 SQLite；client 只经 loopback authenticated protocol；PG/product path 被拒绝且无 startup side effect | generic DB path、PostgreSQL env/driver、共享文件、宽权限、credential/symlink drift → fail before lock/open/listen/ready，返回 typed unavailable，保留零状态变化 | `pending` / `NO-GO` |
| `RC-NETWORK-FS` | canonical physical path、mount/statfs classification、intermediate directory identity、lock/db sidecar identity、受信部署 backing-mount 证明 | 仅明确本地文件系统 + identity read-back 可继续；network/UNC/unknown/detection-error 均拒绝 | rename/recreate、network FS、statfs error、path alias/TOCTOU → fail-closed；不要降级为普通 lock、换路径或复制 SQLite，恢复须重新 canonicalize 并取得新 lease | `needs_revalidation` / `NO-GO` |
| `RC-TRUST-ROOT-ROLLBACK` | terminal receipt 三层 digest、security-domain/head root、anchor predecessor/result、security-time/retention、签名 trust root/key generation/rotation、rollback tombstone | 只有验签、anchor/read-back、head CAS 全部匹配才可 terminalize；same operation+digest 精确 replay | proof tamper、旧 v1 digest、trust-root 缺失/rotation gap、head rollback、late opposite winner → 拒绝 terminal，保留 abort/unknown tombstone；恢复只能按原 operation query 和 predecessor successor，禁止复制 DB 重置 generation | `pending` / `NO-GO` |

## 具名 RED / GREEN / Guard 参考

以下 token 是后续 runner 的索引，不是已通过的 receipt。每个 token 都必须绑定当前 fixture revision、输入 digest、owner generation、实际进程/挂载和 guard 输出；本机结果统一解释为 `LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED`。

### `RC-SINGLETON-OWNER`

- 输入：两个进程使用同一 canonical scope，第二个进程尝试 lock/bootstrap/mutation；另有旧连接在 successor epoch 后写入。
- 输出：一个 owner 获得 lease/fence；第二 owner 得 typed busy；旧 writer 返回 `stale_writer` 且业务 callback/rows 为零。owner crash 后 takeover 必须 exact predecessor + generation+1。
- 失败/恢复：lock、DB identity 或 lease drift 立即 query-only；commit unknown 只查原 operation，不自动重试。
- RED：`RED-RC-SINGLETON-DUPLICATE-OWNER` → `cmd/mcp-orch/orchestration/recoverycustody/sqliteauthority/kernel_lease_test.go:TestKernelAuthorityLeaseRejectsSecondOwner`、`internal/platform/securefs/pinned_directory_lock_unix_test.go:TestPinnedDirectoryLockSecondOwnerBusy`；`RED-RC-SINGLETON-STALE-WRITER` → `sqliteauthority/writer_epoch_authority_test.go:TestAuthorityStaleWriterSubmitFailsBeforeBusinessCallback`。
- GREEN（待复核）：`GREEN-RC-SINGLETON-TAKEOVER-CAS` → `sqliteauthority/writer_epoch_authority_test.go:TestAuthorityTakeoverRequiresLeaseAndFencesCurrentInstance`、`TestTakeoverAuthoritySupportsSuccessorWithoutOldInstance`。
- Guard：`Guard-RC-SINGLETON-ONE-WRITER` → `internal/archtest/recovery_custody_authority_sqlite_open_guard_test.go`、`sqliteauthority/writer_epoch_test.go:TestWriterFenceTakeoverFencesExistingConnection`。
- 状态：`needs_revalidation`；决策：`NO-GO`。

### `RC-MIGRATION-BOOTSTRAP`

- 输入：空库 fresh bootstrap、已存在 revision 3、revision 1/2、manifest/hash/mode/fence 以及并发启动/leader crash 注入。
- 输出：fresh 只 bootstrap 一次并写完整 mode/fence；既有库只在 schema/identity/read-back 成功后打开；旧 revision 不隐式迁移。
- 失败/恢复：marker/DDL/hash 不完整或 migration commit unknown 时保留原库、回滚事务并 quarantine；下一次只按原 revision/operation 重试，禁止第二 runner 或回退 genesis。
- RED：`RED-RC-MIGRATION-IMPLICIT-UPGRADE` → `sqliteauthority/writer_epoch_authority_test.go:TestOpenRejectsRevisionOneWithoutImplicitMigration`、`TestOpenAndBootstrapRejectRevisionTwoWithoutImplicitMigration`；`RED-RC-MIGRATION-PARTIAL-MARKER` → `internal/platform/db/sqlite/migrate_test.go:TestRunMigrationsRollsBackBodyAndMarkerOnFailure`。
- GREEN（待复核）：`GREEN-RC-MIGRATION-FRESH-AND-REOPEN` → `sqliteauthority/writer_epoch_authority_test.go:TestBootstrapSeedsDatabaseRevisionAndWriterFence`、`sqliteauthority/authority_lifecycle_test.go:TestSQLiteAuthorityLifecycle`；`GREEN-RC-MIGRATION-EMBEDDED-IDEMPOTENT` → `internal/platform/db/sqlite/embedded_migrations_test.go:TestRunEmbeddedMigrationsFreshDatabaseIsIdempotent`。
- Guard：`Guard-RC-MIGRATION-SCHEMA-OWNER` → `internal/archtest/recovery_custody_sqlite_only_guard_test.go`、`sqliteauthority/schema_hardening_test.go:TestSQLiteAuthoritySchemaHardening`。
- 状态：`pending`；决策：`NO-GO`。

### `RC-UNKNOWN-RECEIPT`

- 输入：原 operation/digest、head predecessor、typed `FinalizationObservation`（`absent|inflight|finalized`）、query/read fence、provider receipt/query key。
- 输出：unknown 保持同一 handle 和 `indeterminate`；只有原 operation definitive `absent` 且 head claim 复验成功才允许一次同 key Finalize。
- 失败/恢复：任何 timeout、late receipt、anchor/read error 或 commit unknown 都进入 query-only；不得 refinalize、换 key、提前释放 RESOURCE 或写 definitive terminal。
- RED：`RED-RC-UNKNOWN-REFINALIZE` → `sqliteauthority/outcome_unknown_test.go:TestTerminalCommitAppliedUnknownDoesNotRefinalizeOrReanchor`；`RED-RC-UNKNOWN-UNVERIFIED-TERMINAL` → `recoverycustody/verified_client_test.go:TestVerifiedClientNeverPassesTerminalOnBackendError`。
- GREEN（待复核）：`GREEN-RC-UNKNOWN-ORIGINAL-QUERY` → `sqliteauthority/reconcile_test.go:TestReconcileResolutionRestartAbsentFinalizesSameKey`、`TestConcurrentReconcileResolutionCreatesOnePhysicalEffect`。
- Guard：`Guard-RC-UNKNOWN-RECEIPT-DIGEST` → `recoverycustody/field_guard_test.go:TestExternalReceiptEveryClaimAffectsDigest`、`sqliteauthority/field_guard_test.go:TestFinalizationObservationFieldGuardDetectsMissingAndStale`。
- 状态：`needs_revalidation`；决策：`NO-GO`。

### `RC-PRIVATE-SQLITE-NO-PG`

- 输入：显式 external-service 配置、独立 authority path、owner-only credential/state files，以及 product/workspace/PG path 的负例。
- 输出：只允许 private SQLite + authenticated loopback protocol；服务启动前拒绝 generic DB、PG、产品 DB 或共享目录，startup side effect 为零。
- 失败/恢复：配置或权限不满足时在 lock/open/listen/ready 前 fail-fast；修复后必须从 canonical path 重新 bootstrap/open，不能复用部分状态或 fallback backend。
- RED：`RED-RC-NOPG-GENERIC-PATH` → `cmd/mcp-recovery-custody/config_test.go:TestDecodeConfigRejectsGenericDatabasePaths`、`service_test.go:TestServiceConfigOmitsGenericDatabasePaths`；`RED-RC-NOPG-SHARED-PATH` → `config_test.go:TestLoadConfigRejectsParentReplacement`。
- GREEN（待复核）：`GREEN-RC-NOPG-EXTERNAL-MODE` → `service_test.go:TestValidateServiceConfigRequiresExternalServiceMode`；`GREEN-RC-NOPG-POSTGRES-IGNORED` → `internal/platform/db/sqlite/release_smoke_fixture_test.go:TestSQLiteRuntimeIgnoresPostgresEnvironment`。
- Guard：`Guard-RC-NOPG-PRODUCTION-IMPORT` → `recoverycustody/field_guard_test.go:TestRecoveryCustodyProductionHasNoEffectDependencies`、`internal/archtest/recovery_custody_sqlite_only_guard_test.go`。
- 状态：`pending`；决策：`NO-GO`。

### `RC-NETWORK-FS`

- 输入：canonical physical path、mount type/statfs result、intermediate symlink/rename、SQLite sidecar and lock identity。
- 输出：只接受已证明 local filesystem；UNC、已知 network、unknown 或 statfs error 明确拒绝；canonicalize/open/bootstrap/Takeover 使用同一 physical path。
- 失败/恢复：TOCTOU、mount drift 或 detection failure 时不换路径、不降级、不复制 DB；停止并重新取得 canonical scope + lease。
- RED：`RED-RC-NETWORK-FS-REJECT` → `internal/platform/securefs/local_filesystem_test.go:TestCheckLocalFilesystemRejectsNetworkAndMalformedPaths`、`local_filesystem_linux_test.go:TestCheckLocalFilesystemLinuxFailsClosedWhenStatfsUnavailable`；`RED-RC-NETWORK-FS-IDENTITY-DRIFT` → `sqliteauthority/authority_path_alias_test.go:TestAuthorityPathIntermediateSymlinkUsesCanonicalTarget`、`securefs/pinned_directory_lock_unix_test.go:TestPinnedDirectoryLockRenameRecreate`（路径替换/TOCTOU 负例）。
- GREEN（待复核）：`GREEN-RC-NETWORK-FS-CANONICAL-IDENTITY` → `internal/platform/securefs/pinned_directory_lock_unix_test.go:TestPinnedDirectoryLockAliasSameInode`、`sqliteauthority/authority_path_alias_test.go:TestAuthorityPathIntermediateSymlinkUsesCanonicalTarget`。
- Guard：`Guard-RC-NETWORK-FS-OPEN-PATH` → `internal/platform/securefs/local_filesystem_linux_test.go`、`internal/platform/db/sqlite/open_path_test.go:TestOpenRejectsPathWithSurroundingWhitespace`。
- 状态：`needs_revalidation`；决策：`NO-GO`。

### `RC-TRUST-ROOT-ROLLBACK`

- 输入：receipt v2 三层 digest、security-domain/head root、anchor predecessor/result、security-time/retention、签名 trust-root generation/rotation。
- 输出：验签、anchor read-back、head CAS 和 retention 全匹配后才可 terminalize；同 operation+digest 只返回原记录。
- 失败/恢复：proof tamper、旧 digest、trust-root gap、head rollback 或 late opposite receipt 均拒绝 terminal；保留 tombstone/unknown，恢复只 query 原 operation 并按 predecessor successor，不能复制 SQLite 重置代际。
- RED：`RED-RC-TRUST-ROOT-TAMPER` → `recoverycustody/receipt_digest_test.go:TestReceiptDigestRejectsProofMetadataTampering`；`RED-RC-TRUST-ROOT-LEGACY` → `receipt_digest_test.go:TestReceiptDigestV1IsRejectedWithoutCompatibilityFallback`；`RED-RC-TRUST-ROOT-ROLLBACK` → `sqliteauthority/reconcile_test.go:TestFinalizerSameOperationDifferentDigestConflicts`。
- GREEN（待复核）：`GREEN-RC-TRUST-ROOT-VERIFIED-WINNER` → `sqliteauthority/evidence_binding_test.go:TestSQLiteAuthorityAvailabilityAbortSingleWinnerAndExactReplay`；`GREEN-RC-TRUST-ROOT-OBSERVATION` → `sqliteauthority/finalization_observation_test.go:TestFinalizationObservationContract`。
- Guard：`Guard-RC-TRUST-ROOT-DIGEST-COVERAGE` → `recoverycustody/field_guard_test.go:TestExternalReceiptDigestPayloadHasReasonedCoverage`、`sqliteauthority/field_guard_test.go:TestFinalizationRequestFieldGuardDetectsMissingAndStale`。
- 状态：`pending`；决策：`NO-GO`。

## 退出条件与证据边界

本专项只有在每个 check 的 RED、GREEN、Guard 三类引用都绑定同一 revision/owner generation，并补齐真实外部 EvidenceFinalizer、ReceiptVerifier、trust-root/key rotation、network-FS backing-mount、双进程 crash/reclaim、migration leader/fresh bootstrap、rollback/late-receipt 和 production supervisor/Fx/OUT 接线后，才可提交人工复评。届时还必须满足 `VERIFY_S`、`ROLLOUT_S`、`VERIFY_DONE`、`PRECANARY_DONE` 的适用祖先闭合。

当前候选测试、静态守卫、配置检查和本矩阵本身均不能关闭 blocker，不能把 `pending`/`needs_revalidation` 改成 `approved`/`closed`，不能签发 capability lease、command accepted、availability/abort receipt 或 release/enable 决策。缺任一生产证据时，唯一合法结论仍为 `NO-GO`。
