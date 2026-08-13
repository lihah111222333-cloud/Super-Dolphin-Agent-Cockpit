// Package cicontract owns the executable remote-CI ECI ImageCache contract.
//
// 该包必须保持为只依赖标准库的叶子包。远程 CI 的命令、SQLite store、
// planner、ECI requester 和架构守卫只能消费这里的协议，不得各自复制常量、
// 状态机或同义规则。
package cicontract

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ECIVSwitch 将 normal CI 的 vSwitch 身份绑定到显式可用区，防止多个 ID 实际仍落在同一区。
type ECIVSwitch struct {
	ID     string `json:"id"`
	ZoneID string `json:"zone_id"`
}

const (
	// ID 是文档与代码共同使用的远程 CI 契约身份。
	ID = "remote-ci-aliyun-eci-imagecache/v5"
	// DocumentPath 是 Accepted 文档契约的仓库相对路径。
	DocumentPath = "docs/契约/remote-ci-eci-imagecache-contract.md"
	// ExecutionPathID 标识唯一正常 CI 执行路径。
	ExecutionPathID = "sqlite-correctness-authority-live-verified-refresh-imagecache-aliyun-eci-shards/v3"
	// ExecutionProviderID 冻结远程 CI 唯一执行与验收提供方；不得抽象或降级到其他容器平台。
	ExecutionProviderID = "aliyun-eci/v1"
	// CIExecutionBoundary 冻结所有远程 CI 动作及其镜像物料加工只能运行在阿里云 ECI；
	// GitHub Actions 不得提供远程 CI runner、编译、测试、cache-prime 或镜像构建能力。
	// 独立产品发布 workflow 不签发远程 CI 结论，不属于本契约执行面。
	CIExecutionBoundary = "aliyun-eci-only-no-github-runner/v1"
	// GenerationOneBootstrapPathID 标识 normal run/hook 在空 singleton 时消费配置 strict ECI receipt 的唯一首代路径。
	// 非空 singleton 继续提供 correctness identity；执行物料可消费实时验证的有限期刷新 snapshot。
	GenerationOneBootstrapPathID = "normal-run-hook-configured-aliyun-eci-generation-one-strict-receipt-bootstrap/v1"
	// ImageCacheRefreshOperatorPathID 标识唯一候选缓存刷新入口；本地只上传内容寻址源码与依赖，编译和镜像加工只在 ECI 内执行。
	// 该入口只创建有限期非权威加速物料，不读取或改写 SQLite；normal run 只在严格解码并实时复核 Ready 后消费。
	ImageCacheRefreshOperatorPathID = "script-oss-handoff-aliyun-eci-offline-imagecache-runtime/v2"
	// SQLAuthorityID 标识 accepted state、规划、回执和校准共用的唯一数据源。
	SQLAuthorityID = "duration-ledger-sqlite/v1"
	// DurationLedgerSQLiteSchemaVersion 是 duration-ledger SQLite 物理 schema 的唯一版本 owner。
	DurationLedgerSQLiteSchemaVersion = 18
	// CacheMaterialSchemaID 标识 OCI 构建阶段可生成的非权威缓存材料 manifest。
	// 它只描述将被阿里云 ECI ImageCache 消费的镜像内容，绝不是 CI 或首代检查回执。
	CacheMaterialSchemaID = "remote-ci-cache-material/v1"
	// CacheMaterialAuthority 冻结缓存材料不得进入任何 authoritative receipt 或 SQLite PASS 路径。
	CacheMaterialAuthority = "non_authoritative_material"
	// GoBuildCachePathID 冻结普通分片只消费 accepted ImageCache 的唯一只读 seed 与私有小写层。
	GoBuildCachePathID = "accepted-imagecache-single-readonly-go-build-seed-private-delta/v2"
	// CompileGroupExecutionPathID 冻结测试二进制编译只能在既有 ECI shard worker
	// 路径内按 compile group 执行；它不是第二 executor，也不允许跨 shard CAS。
	CompileGroupExecutionPathID = "same-eci-shard-worker-test-binary-compile-no-cross-shard-cas/v1"
	// CompileGroupMaxSelectors 冻结所有 compile group 的 exact selector 上限；超过该上限必须拆成独立 CompileGroup/ECI shard，
	// 不能在同一个 4 GiB worker 内堆积成一个 test-binary。
	CompileGroupMaxSelectors = 64
	// ArchtestMaxSelectorsPerCompileGroup 保留历史符号作为兼容别名；新的 atomic
	// package planner 必须统一使用 CompileGroupMaxSelectors owner。
	ArchtestMaxSelectorsPerCompileGroup = CompileGroupMaxSelectors
	// RemoteShardRequestMaxBytes 冻结 coordinator、OSS materializer 与 strict
	// JSON decoder 共用的完整 shard request 上限。该上限约束请求总字节数，
	// 不再用 gate 数量猜测合法分片大小。
	RemoteShardRequestMaxBytes = 1 << 20
	// WorkloadExecutionPlanSchemaVersion 冻结确定性分片计划的唯一 wire schema。
	WorkloadExecutionPlanSchemaVersion uint32 = 10
	// WorkloadPlanningAlgorithmID 冻结 normal/calibration 共用的 D-CPAP 算法身份。
	WorkloadPlanningAlgorithmID = "deterministic-critical-path-aware-packing/v3"
	// WorkloadPlanningObjective 冻结 planner 比较器的优先级顺序；不得改写为近似或隐式成本。
	WorkloadPlanningObjective = "hard constraints>target excess>minimum shards>makespan>setup proxy>canonical layout"
	// WorkloadPlanningSearchNodeBudget 冻结 exact proof 的确定性节点预算；耗尽时必须 fail-fast。
	WorkloadPlanningSearchNodeBudget = 1_000_000
	// D-CPAP policy 常量由协议 owner 冻结；planner 实现不得重复声明或漂移。
	WorkloadPlanningExactPackableUnitThreshold        = 12
	WorkloadPlanningExactSolverModeID                 = "deterministic-branch-and-bound-exact/v1"
	WorkloadPlanningHeuristicSolverModeID             = "deterministic-setup-aware-bfd-2move-3cycle-beam/v1"
	WorkloadPlanningIsolatedSolverModeID              = "isolated-only/v1"
	WorkloadPlanningHeuristicMaxTwoMoveTransitions    = 64
	WorkloadPlanningHeuristicMaxThreeCycleTransitions = 64
	WorkloadPlanningHeuristicBeamWidth                = 8
	WorkloadPlanningHeuristicBeamDepth                = 3
	WorkloadPlanningHeuristicMaxBeamTransitions       = 128
	// WorkloadEstimationPolicyVersion 冻结估时摘要的 schema；估时摘要缺失必须拒绝计划。
	WorkloadEstimationPolicyVersion = "duration-estimate-overhead-policy/v1"
	// WorkloadPassIdentityDomain 冻结可复用 PASS identity 的密码学域；无域或其他域材料不得命中。
	WorkloadPassIdentityDomain = "pass-identity/v2"
	// WorkloadInputFingerprintDomain 冻结 workload 生产输入摘要的密码学域；旧无域摘要自然 MISS。
	WorkloadInputFingerprintDomain = "input-fingerprint/v2"
	// WorkloadInputFingerprintSchemaVersion 保留 fingerprint 内部材料 schema 4，并与密码学域显式分离。
	WorkloadInputFingerprintSchemaVersion uint32 = 4
	// WorkloadPassSourceReplayDomain 冻结历史来源树重算证明；它只允许把已验证的直接 PASS 投影到当前精确输入身份。
	WorkloadPassSourceReplayDomain = "workload-pass-source-replay/v1"
	// WorkloadPassEnvironmentReplayDomain 冻结 environment 变化时的 origin-tree
	// 重放证明域；它只绑定已验证来源 PASS 与当前 environment identity，不改变
	// pass-identity/v2 的 correctness lookup 域。
	WorkloadPassEnvironmentReplayDomain = "workload-pass-environment-replay/v1"
	// WorkloadPassEnvironmentSchemaVersion 冻结 correctness PASS 环境为 v10；旧版本严格拒绝。
	WorkloadPassEnvironmentSchemaVersion = "remote-workload-pass-environment/v10"
	// AcceptedBootstrapRequestSchemaVersion、AcceptedCompileGroupSchemaVersion 与
	// AcceptedBootstrapManifestSchemaVersion 冻结 accepted ImageCache 只读
	// bootstrap 协议；full request/manifest 由下列独立版本承载。
	AcceptedBootstrapRequestSchemaVersion  uint32 = 14
	AcceptedCompileGroupSchemaVersion      uint32 = 1
	AcceptedBootstrapManifestSchemaVersion uint32 = 1
	ShardRequestSchemaVersion              uint32 = 15
	ShardExecutionManifestSchemaVersion    uint32 = 2
	CompileGroupSchemaVersion              uint32 = 2
	// CompileGroupSerialPackingPolicyID 冻结 ordinary side-effect-safe 同资源串行共箱策略。
	CompileGroupSerialPackingPolicyID = "same-resource-side-effect-safe-serial-packing/v1"
	// CriticalPathCostPolicyID 冻结 compile-once 与 wave critical-path 记账公式。
	CriticalPathCostPolicyID = "compile-once+sum-wave-max-body/v1"
	// ImageSeedValidationPathID 冻结完整依赖/缓存树只在不可变镜像构建与内容验收时扫描；
	// normal shard 仅校验当前清单、固定根和只读镜像挂载，禁止每分片或每 workload 全树复验。
	ImageSeedValidationPathID = "image-build-full-tree-normal-shard-manifest-roots-readonly-mount/v1"
	// BaselineStateSchemaVersion 是 accepted baseline JSON wire schema 的唯一版本 owner。
	BaselineStateSchemaVersion uint32 = 13

	// SourceBaselineRepositoryPath 是 accepted ImageCache 中唯一可复用的 Git
	// baseline object store。它只包含 RunnerBaseTree 的 tree/blob closure 和
	// 一个确定性的无 parent baseline commit。
	SourceBaselineRepositoryPath = "/opt/super-dolphin-gate/source-baseline.git"
	// SourceBundleName 是每次 CI 唯一允许上传的候选源码传输对象名称。
	SourceBundleName = "source.bundle"
	// SourceManifestName 是 source bundle 的严格 manifest 对象名称。
	SourceManifestName = "source-manifest.json"
	// SourceBundleRef 是 bundle 中唯一允许广告的候选 transport ref。
	SourceBundleRef = "refs/source/materialized"
	// SourceBaseRef 是物化工作树中唯一允许标记 candidate-parent synthetic base
	// commit 的 ref；该 commit 的 parent 是 accepted baseline，tree 是 SourceSpec
	// parent/base tree。diff、LSP changed-files 等门禁只从该受信任边界比较候选。
	SourceBaseRef = "refs/source/base"
	// SourceManifestSchemaVersion 是源码 transport strict manifest 的当前版本。
	SourceManifestSchemaVersion uint32 = 3
	// SourceTransportKind 是相对 accepted baseline 的唯一源码传输类型。
	SourceTransportKind = "git-bundle-thin"
	// SourceBundleHeaderVersion 是 Git bundle header 的唯一允许版本。
	SourceBundleHeaderVersion = "v2"
	// SourceBundlePrerequisiteCount 冻结 bundle header 必须恰好包含一个 prerequisite。
	SourceBundlePrerequisiteCount = 1
	// SourceAssetsUploadBarrier 标识 source bundle 和 strict manifest 完整上传后
	// 才能按 SQLite LPT 计划创建并发 shards 的唯一顺序边界。
	SourceAssetsUploadBarrier = "source-bundle-and-strict-manifest-before-lpt-shards/v1"

	// TargetPlatform 是远程 CI 与外部供给物料的唯一生产目标平台。
	TargetPlatform = "linux/amd64"
	// GoToolchainVersion 是基准镜像和候选编译唯一接受的 Go 发行版。
	GoToolchainVersion = "go1.26.5"
	// ECIEphemeralStorageGiB 冻结每个 CI、镜像物料构建和 cache-prime
	// 容器组的原始临时盘容量，避免默认小盘导致构建中途耗尽。
	ECIEphemeralStorageGiB = 100
	// ECIMultiZoneScheduleStrategy 冻结 normal、校准与首代检查的多可用区随机调度策略。
	ECIMultiZoneScheduleStrategy = "VSwitchRandom"
	// ECIMinVSwitchCount 禁止单可用区库存成为全部并发分片的串行瓶颈。
	ECIMinVSwitchCount = 2
	// ECIMaxVSwitchCount 对齐阿里云 CreateContainerGroup 的官方 vSwitch 数量上限。
	ECIMaxVSwitchCount = 10

	// AcceptedBaselineTable 是已接受基准代的唯一 SQLite 权威表。
	AcceptedBaselineTable = "ci_remote_baseline_state"
	// DurationSamplesTable 是 workload 历史耗时与 LPT 规划样本的唯一 SQLite 权威表。
	DurationSamplesTable = "duration_samples"
	// DurationShardOverheadsTable 是 accepted shard orchestration overhead aggregate 的唯一权威表。
	DurationShardOverheadsTable = "duration_shard_overheads"
	// DurationShardOverheadSamplesTable 保存 aggregate P95 所选择的逐分片 timing 样本。
	DurationShardOverheadSamplesTable = "duration_shard_overhead_samples"
	// RemoteRunsTable 是远程 CI run 回执的唯一 SQLite 权威表。
	RemoteRunsTable = "ci_runs"
	// RemoteRunExecutionScopesTable 是 subset remote CI run execution scope 的 additive side table。
	RemoteRunExecutionScopesTable = "ci_remote_run_execution_scopes"
	// RetainedWorkloadPassProofsTable 是 retained reused PASS 的 consumer-owned immutable 辅助投影。
	// 它不构成第八个 retention root，也不提供第二 authority。
	RetainedWorkloadPassProofsTable = "ci_retained_workload_pass_proofs"
	// RetainedWorkloadPassProofLookupIndex 支撑 retained proof 的 identity/consumer 严格读取。
	RetainedWorkloadPassProofLookupIndex = "idx_ci_retained_workload_pass_proofs_lookup"
	// RunWorkloadResultsRetentionIndex 支撑 v16 retained proof 的 direct-origin 验证和 backfill。
	RunWorkloadResultsRetentionIndex = "idx_ci_run_workload_results_retention"
	// WorkloadPassEvidenceSourceReplayIndex 按 workload/execution/environment 分区 direct proof 候选。
	WorkloadPassEvidenceSourceReplayIndex = "idx_ci_workload_pass_evidence_source_replay"
	// RetainedWorkloadPassProofSourceReplayIndex 按 workload 分区 retained consumer proof 候选。
	RetainedWorkloadPassProofSourceReplayIndex = "idx_ci_retained_workload_pass_proofs_source_replay"
	// RemoteShardsTable 是远程 CI shard 回执的唯一 SQLite 权威表。
	RemoteShardsTable = "ci_shards"
	// WorkloadExecutionsTable 是 workload 执行与缓存事实的唯一 SQLite 权威表。
	WorkloadExecutionsTable = "ci_workload_executions"
	// RunWarningsTable 是运行最终投影中供人阅读的告警文本表。
	RunWarningsTable = "ci_run_warnings"
	// LiveTimingWarningsTable 是运行仍在执行时 100 秒目标超限事实的唯一 SQLite 权威表。
	LiveTimingWarningsTable = "ci_live_timing_warnings"
	// RunTimingWarningsTable 是最终运行投影吸收后的结构化 100 秒目标超限事实表。
	RunTimingWarningsTable = "ci_run_timing_warnings"
	// CalibrationCheckpointsTable 是固定规格校准 checkpoint 的唯一 SQLite 权威表。
	CalibrationCheckpointsTable = "remote_ci_calibration_checkpoints"
	// CheckReceiptsTable 是全部必跑检查结果的唯一 SQLite 权威表。
	CheckReceiptsTable = "ci_check_receipts"
	// TimingObservationsTable 是 shard/workload 原始阶段观测的唯一 SQLite 权威表。
	TimingObservationsTable = "ci_timing_observations"
	// CompileTimingObservationsTable 是 compile-group measured/raw 历史的唯一 SQLite 权威表。
	// 它通过 ci_runs 外键参与现有 accepted-generation retention，不形成第二份 ledger。
	CompileTimingObservationsTable = "ci_compile_timing_observations"
	// TimingDurationColumn 是所有实测与聚合耗时共同使用的精确毫秒列。
	TimingDurationColumn = "duration_ms"
	// WorkloadCatalogsTable 是 workload catalog 内容的唯一 SQLite 权威表。
	WorkloadCatalogsTable = "ci_workload_catalogs"
	// CatalogObservationsTable 是 catalog 与候选 tree 观测关系的唯一 SQLite 权威表。
	CatalogObservationsTable = "ci_catalog_observations"
	// CatalogWorkloadsTable 是 catalog workload 明细的唯一 SQLite 权威表。
	CatalogWorkloadsTable = "ci_catalog_workloads"
	// RunAgentIdentitiesTable 是远程 CI agent digest 投影的唯一 SQLite 权威表。
	RunAgentIdentitiesTable = "ci_run_agent_identities"
	// ShardWorkloadsTable 是 shard 到 workload 绑定的唯一 SQLite 权威表。
	ShardWorkloadsTable = "ci_shard_workloads"
	// GateExecutionsTable 是 Gate 执行回执的唯一 SQLite 权威表。
	GateExecutionsTable = "ci_gate_executions"
	// RunWorkloadResultsTable 是本次 run 的 executed/reused workload 结果投影的唯一 SQLite 权威表。
	RunWorkloadResultsTable = "ci_run_workload_results"
	// WorkloadPassEvidenceTable 是 strict workload PASS reuse evidence 的唯一 SQLite 权威表。
	WorkloadPassEvidenceTable = "ci_workload_pass_evidence"
	// CalibrationCheckpointScenariosTable 是 checkpoint scenario 的唯一 SQLite 权威表。
	CalibrationCheckpointScenariosTable = "remote_ci_calibration_checkpoint_scenarios"

	// GitHookInvocationConcurrencyPolicy 明确多个 agent 可以同时触发 Git hook；仓库不得用全局 hook 锁串行化调用。
	GitHookInvocationConcurrencyPolicy = "unbounded_by_repository"
	// RemoteCIJobConcurrencyPolicy 明确远程 CI job 可以并发；不得设置 active-job 锁或 admission cap。
	RemoteCIJobConcurrencyPolicy = "unbounded_by_repository"
	// ShardConcurrencyPolicy 明确动态 shard 只受当前可分片原子 workload 数量限制，不设置数量或 coordinator 并发上限。
	ShardConcurrencyPolicy = "unbounded_by_repository"
	// GitIndexLockBoundary 明确同一 worktree 的 Git index.lock 只保护 Git index 一致性，不是 CI admission 或并发上限。
	GitIndexLockBoundary = "git_worktree_index_consistency_not_ci_admission"

	// RetentionGenerations 只约束 accepted baseline 代数；每代内部行数不受限制。
	RetentionGenerations = 3
	// WorkloadPassEvidenceGenerationWindow 只接受当前 accepted generation 及其前两代的 strict PASS evidence。
	WorkloadPassEvidenceGenerationWindow = RetentionGenerations
	// WorkloadPassEvidenceFreshnessPolicy 冻结 strict PASS 的 freshness 含义：
	// accepted generation 窗口、权威状态、完整 identity 和 canonical receipt proof；禁止 wall-clock TTL。
	WorkloadPassEvidenceFreshnessPolicy = "accepted-generation-authority-identity-receipt-no-wall-clock-ttl/v1"
	// AcceptedGenerationColumn 是所有 SQLite 历史根共享的 generation 列名。
	AcceptedGenerationColumn = "accepted_generation"
)

const (
	// CalibrationResourceCPU 和 CalibrationResourceMemoryGiB 是唯一的 calibration tuple。
	// class ID 仍由 policy 负责，因此每个 policy 都能保持 calibration 与 normal class ID 独立。
	CalibrationResourceCPU       = 4
	CalibrationResourceMemoryGiB = 8
	// ShardTargetDuration 是拆分、优化与告警目标，不是 worker 硬超时。
	ShardTargetDuration = 100 * time.Second
	// ImageCacheRefreshInterval 冻结 pre-push 后台候选缓存维护的最短成功间隔。
	ImageCacheRefreshInterval = 24 * time.Hour
	// FastWorkloadResourceDuration 是 normal workload 使用 2C 的固定账本估时上界。
	FastWorkloadResourceDuration = 5 * time.Second
	// MediumWorkloadResourceDuration 是 normal workload 使用 4C 的固定账本估时上界；更慢 workload 使用 8C。
	MediumWorkloadResourceDuration = 70 * time.Second
	// TimingResolution 是结构化回执和 SQLite 耗时账本的统一整数分辨率。
	TimingResolution = time.Millisecond
)

// WorkloadResourceTier 是 normal workload 按权威账本估时选择的 CPU 档。
type WorkloadResourceTier uint8

const (
	WorkloadResourceTierFast WorkloadResourceTier = iota + 1
	WorkloadResourceTierMedium
	WorkloadResourceTierSlow
)

// ClassifyWorkloadResourceDuration 将正数毫秒估时映射到唯一 2C、4C、8C 档。
func ClassifyWorkloadResourceDuration(durationMS int64) (WorkloadResourceTier, error) {
	if durationMS <= 0 {
		return 0, errors.New("workload resource duration must be positive")
	}
	if durationMS <= FastWorkloadResourceDuration.Milliseconds() {
		return WorkloadResourceTierFast, nil
	}
	if durationMS <= MediumWorkloadResourceDuration.Milliseconds() {
		return WorkloadResourceTierMedium, nil
	}
	return WorkloadResourceTierSlow, nil
}

// TimingPhase 是权威回执和人类账本必须显式表达的耗时阶段。
type TimingPhase string

const (
	TimingECIWait           TimingPhase = "eci_wait"
	TimingSourceMaterialize TimingPhase = "source_materialize"
	TimingCandidateCompile  TimingPhase = "candidate_compile"
	TimingTestBinaryCompile TimingPhase = "test_binary_compile"
	TimingStartup           TimingPhase = "startup"
	TimingTestBody          TimingPhase = "test_body"
	TimingTotal             TimingPhase = "total"
)

// ObservationState 区分真实观测、不适用和缺失观测，禁止以 0 伪装。
type ObservationState string

const (
	ObservationMeasured      ObservationState = "measured"
	ObservationNotApplicable ObservationState = "not_applicable"
)

// TimingScope identifies the subject represented by a timing observation.
type TimingScope string

const (
	TimingScopeRun          TimingScope = "run"
	TimingScopeShard        TimingScope = "shard"
	TimingScopeWorkload     TimingScope = "workload"
	TimingScopeCompileGroup TimingScope = "compile_group"
)

// TimingAggregation identifies whether an observation is raw or a wall-clock derivation.
type TimingAggregation string

const (
	TimingAggregationRaw           TimingAggregation = "raw"
	TimingAggregationIntervalUnion TimingAggregation = "interval_union"
	TimingAggregationCriticalPath  TimingAggregation = "critical_path"
)

// TimingWarningAction 规定 100 秒目标超限后的唯一处理动作。
type TimingWarningAction string

const (
	TimingWarningWarnAndContinue TimingWarningAction = "warn_and_continue"
)

// TimingWarningEvidenceKind 区分运行中 provider 事实与完成后的 workload 原始区间。
type TimingWarningEvidenceKind string

const (
	TimingWarningEvidenceRunning  TimingWarningEvidenceKind = "running"
	TimingWarningEvidenceTestBody TimingWarningEvidenceKind = "test_body"
	TimingWarningEvidenceTotal    TimingWarningEvidenceKind = "total"
)

// RequiredCheck 是每次远程运行都必须观察到 PASS 的检查目录。
type RequiredCheck string

const (
	RequiredCheckGate       RequiredCheck = "gate"
	RequiredCheckNormal     RequiredCheck = "normal"
	RequiredCheckE2E        RequiredCheck = "e2e"
	RequiredCheckRace       RequiredCheck = "race"
	RequiredCheckFrontend   RequiredCheck = "frontend"
	RequiredCheckDependency RequiredCheck = "dependency"
)

// CheckObservation 是一个必跑检查绑定到同一 authority 回执的结果。
// Reused 表示此检查严格复用了同一 SQLite authority 中仍新鲜的权威 PASS
// evidence；它绝不表示缓存命中或未验证的跳过。
type CheckObservation struct {
	Check              RequiredCheck `json:"check"`
	Executed           bool          `json:"executed"`
	Reused             bool          `json:"reused"`
	ReuseProofSHA256   string        `json:"reuse_proof_sha256"`
	Passed             bool          `json:"passed"`
	SourceTree         string        `json:"source_tree"`
	AcceptedSnapshotID string        `json:"accepted_snapshot_id"`
	PlanDigest         string        `json:"plan_digest"`
	StartedAtUnixMS    int64         `json:"started_at_unix_ms"`
	CompletedAtUnixMS  int64         `json:"completed_at_unix_ms"`
	DurationMS         int64         `json:"duration_ms"`
	ReceiptSHA256      string        `json:"receipt_sha256"`
}

// ProvisionCheck 是外部 generation-one strict receipt 的内容验证目录；它不是仓库内 refresh executor 或 ImageCache writer。
type ProvisionCheck string

const (
	ProvisionCheckGateBuild     ProvisionCheck = "gate_build"
	ProvisionCheckNormalCompile ProvisionCheck = "normal_compile"
	ProvisionCheckE2ECompile    ProvisionCheck = "e2e_compile"
	ProvisionCheckRaceCompile   ProvisionCheck = "race_compile"
	ProvisionCheckFrontendBuild ProvisionCheck = "frontend_build"
	ProvisionCheckDependency    ProvisionCheck = "dependency"
)

// ProvisionCheckObservation 是外部 generation-one receipt 中已完成内容验证的严格编码。
// 它不得被解释为 normal CI PASS，也不得授权仓库创建 successor ImageCache。
type ProvisionCheckObservation struct {
	Check                         ProvisionCheck `json:"check"`
	ExecutionProvider             string         `json:"execution_provider"`
	RegionID                      string         `json:"region_id"`
	ContainerGroupID              string         `json:"container_group_id"`
	ContainerName                 string         `json:"container_name"`
	ResourceClassID               string         `json:"resource_class_id"`
	ResourceCPU                   float64        `json:"resource_cpu"`
	ResourceMemoryGiB             float64        `json:"resource_memory_gib"`
	Executed                      bool           `json:"executed"`
	Passed                        bool           `json:"passed"`
	SourceTree                    string         `json:"source_tree"`
	ProvisionSnapshotID           string         `json:"provision_snapshot_id"`
	PlanDigest                    string         `json:"plan_digest"`
	StartedAtUnixMS               int64          `json:"started_at_unix_ms"`
	CompletedAtUnixMS             int64          `json:"completed_at_unix_ms"`
	DurationMS                    int64          `json:"duration_ms"`
	CandidateCompileMS            int64          `json:"candidate_compile_ms"`
	CandidateCompileNotApplicable bool           `json:"candidate_compile_not_applicable"`
	TestBodyNotApplicable         bool           `json:"test_body_not_applicable"`
	ReceiptSHA256                 string         `json:"receipt_sha256"`
}

const (
	// GenerationOneECITagProvider 将 provision group 绑定到唯一接受的 cloud executor。
	GenerationOneECITagProvider = "super-dolphin-ci-provider"
	// GenerationOneECITagImageCache 将 provision group 绑定到精确的 Ready ImageCache identity。
	GenerationOneECITagImageCache = "super-dolphin-ci-image-cache"
	// GenerationOneECITagSnapshot 将 provision group 绑定到正在测试的 Ready ImageCache snapshot。
	GenerationOneECITagSnapshot = "super-dolphin-ci-snapshot"
	// GenerationOneECITagSourceTree 将 provision group 绑定到正在测试的精确 source tree。
	GenerationOneECITagSourceTree = "super-dolphin-ci-source-tree"
	// GenerationOneECITagCheck 将 provision group 绑定到唯一的 generation-one check。
	GenerationOneECITagCheck = "super-dolphin-ci-provision-check"
	// GenerationOneECITagPlanDigest 将 provision group 绑定到 immutable execution plan。
	GenerationOneECITagPlanDigest = "super-dolphin-ci-plan-digest"
)

// SQLDomain 是必须由同一个 duration-ledger SQLite authority 持久化的事实域。
type SQLDomain string

const (
	SQLDomainAcceptedBaseline      SQLDomain = "accepted_baseline"
	SQLDomainDurationHistory       SQLDomain = "duration_history"
	SQLDomainShardOverhead         SQLDomain = "shard_orchestration_overhead"
	SQLDomainShardOverheadSample   SQLDomain = "shard_orchestration_overhead_sample"
	SQLDomainRemoteRun             SQLDomain = "remote_run"
	SQLDomainRemoteShard           SQLDomain = "remote_shard"
	SQLDomainWorkloadExecution     SQLDomain = "workload_execution"
	SQLDomainRunWarning            SQLDomain = "run_warning"
	SQLDomainLiveTimingWarning     SQLDomain = "live_timing_warning"
	SQLDomainRunTimingWarning      SQLDomain = "run_timing_warning"
	SQLDomainCalibrationCheckpoint SQLDomain = "calibration_checkpoint"
	SQLDomainCheckReceipt          SQLDomain = "check_receipt"
	SQLDomainTimingObservation     SQLDomain = "timing_observation"
	SQLDomainCompileTiming         SQLDomain = "compile_timing"
	SQLDomainWorkloadCatalog       SQLDomain = "workload_catalog"
	SQLDomainCatalogObservation    SQLDomain = "catalog_observation"
	SQLDomainCatalogWorkload       SQLDomain = "catalog_workload"
	SQLDomainRunAgentIdentity      SQLDomain = "run_agent_identity"
	SQLDomainShardWorkload         SQLDomain = "shard_workload"
	SQLDomainGateExecution         SQLDomain = "gate_execution"
	SQLDomainRunWorkloadResult     SQLDomain = "run_workload_result"
	SQLDomainWorkloadPassEvidence  SQLDomain = "workload_pass_evidence"
	SQLDomainCalibrationScenario   SQLDomain = "calibration_checkpoint_scenario"
)

// SQLAuthorityBinding 将一个事实域绑定到唯一规范表。
type SQLAuthorityBinding struct {
	Domain SQLDomain
	Table  string
}

// RetentionRootBinding 将一个可增长历史根绑定到统一的 accepted generation 列。
type RetentionRootBinding struct {
	Table            string
	GenerationColumn string
}

// Requirement 是 Accepted 文档每项设计目标在代码中的稳定映射。
type Requirement struct {
	ID          string
	Section     uint8
	Summary     string
	Enforcement string
}

var requirements = [...]Requirement{
	{ID: "1.1", Section: 1, Summary: "基准镜像消费单一 Go 1.26.5 锁，并包含锁定工具、Go module/build cache 与前端依赖/构建 cache", Enforcement: "build closure + runtime manifest + archtest"},
	{ID: "1.2", Section: 1, Summary: "normal CI 仅在空 singleton 时原子首写 accepted correctness identity；pre-push 按 exact pushed tree 非阻塞刷新有限期 ImageCache，normal run 严格读取 OSS 回执并实时复核 Ready identity 后只替换执行镜像与 snapshot，绝不改写 SQLite", Enforcement: "runtime call graph + hook dispatch + strict receipt + live ECI verification + archtest"},
	{ID: "1.3", Section: 1, Summary: "hook、job 与动态 shards 无仓库并发上限；index.lock 仅保护 Git index", Enforcement: "cicontract concurrency policy + archtest"},
	{ID: "1.5", Section: 1, Summary: "所有远程 CI 动作、镜像构建与 cache-prime 只能在阿里云 ECI 执行；禁止 GitHub Actions 或其他环境承载远程 CI", Enforcement: "provider identity + ECI request/receipt field guard + remote-CI workflow deletion + archtest"},
	{ID: "1.6", Section: 1, Summary: "100 秒只触发一次目标超限告警；不得取消、中断、kill 或标失败", Enforcement: "planner + non-terminating timing warning"},
	{ID: "1.7", Section: 1, Summary: "校准运行使用独立于 normal 的固定 4C/8GiB 规格并被回执绑定；所有 ECI 执行使用 100 GiB 原始临时盘", Enforcement: "request + receipt + SQLite + ECI CLI guard"},
	{ID: "1.8", Section: 1, Summary: "运行以真实起止和 duration_ms 分别记录物化、编译、启动、测试、总耗时、缓存与等待；并发聚合使用精确 interval union", Enforcement: "receipt + SQLite timing ledger"},
	{ID: "1.9", Section: 1, Summary: "PASS lookup 必须先读取 canonical WorkloadCatalog、exact correctness fingerprints 与 worker semantic digest；仅 MISS 才允许计算 compile closure、LPT、resource、shard、OSS/temp 和 ECI side effect。normal 与 calibration 默认均只复用同一 SQLite authority 的权威 PASS；all-hit 不创建 workload CI ECI、planner、compile group、资源、shard、OSS/temp 或 calibration，且不执行测试；只有显式 --force 才绕过 PASS 查询并执行全部 shardable workload", Enforcement: "catalog/fingerprint/worker digest + miss-only call-order guard + strict reuse receipt validation + force audit + archtest"},
	{ID: "2.1", Section: 2, Summary: "accepted baseline、duration history、run/shard receipt 与 calibration checkpoint 只读写同一 SQLite authority", Enforcement: "SQLite schema + store"},
	{ID: "2.2", Section: 2, Summary: "correctness identity 只来自 accepted SQLite；运行时加速缓存只由 strict OSS current receipt 与实时 Describe 完全一致的 Ready ImageCache ID、name、image 和 snapshot 选择", Enforcement: "SQLite state validation + strict receipt + live ECI validation + request field guard"},
	{ID: "2.3", Section: 2, Summary: "候选源码和 Gate 编译分别绑定 exact Git identity 与根 module 跨代稳定传递编译闭包；Gate 禁止导入 repository-local replace module，嵌套 module metadata 只进 worker execution digest", Enforcement: "materializer + receipt + closure guard"},
	{ID: "2.4", Section: 2, Summary: "严格 JSON 仅作协议编码，OSS refresh receipt 仅选择可实时复核的加速物料；二者均不得提供 PASS、accepted generation 或 correctness 权威", Enforcement: "strict decoders + live ECI readback + archtest"},
	{ID: "2.5", Section: 2, Summary: "accepted state、check receipts、timing 与 warnings 只写同一个 SQLite authority", Enforcement: "SQLite schema + receipt store"},
	{ID: "2.6", Section: 2, Summary: "generation-one 只能由 normal CI 消费配置外部回执首写；唯一刷新脚本按 OSS strict receipt 的 24h 成功间隔创建有限期 ImageCache，normal run 可在 strict decode、accepted OCI base 绑定和实时 Ready 复核后消费该加速层，但不得晋级或改写 SQLite", Enforcement: "strict receipt boundary + accepted-base binding + live ECI guard + archtest"},
	{ID: "2.7", Section: 2, Summary: "无 token 请求只返回 --agent-token=issue 与 env=issue 申请方式和实际 token 的 flag/env 使用方式；仅单一显式 issue 才签发 raw token，前两阶段均不执行 CI/ECI；git hook 无状态且只继承/验证实际 env token，跨 SQLite/OSS/ECI/log/tag/checkpoint/receipt 只传 sha256 digest", Enforcement: "agent-token contract + field guard + archtest"},
	{ID: "2.8", Section: 2, Summary: "首次读取缺失 SQLite 时原子初始化 schema/index；normal run/hook 仅凭显式 strict ECI receipt 原子首写 baseline 与账本元数据，缺失或漂移仍 fail-fast", Enforcement: "SQLite initializer + generation-one bootstrap test"},
	{ID: "2.9", Section: 2, Summary: "authoritative JobID 的全部 SQLite 投影不可重写；晋级前必须精确回读 aggregate/workload execution 的状态、摘要、时序与执行画像", Enforcement: "provisional write guard + strict aggregate/workload readback tests"},
	{ID: "2.10", Section: 2, Summary: "每次阿里云 CLI 调用同时受 context 超时与进程管道 WaitDelay 约束；子孙进程持有输出管道不得使轮询、汇总或清理无界等待", Enforcement: "ECI CLI process watchdog + inherited-pipe regression test"},
	{ID: "3.1", Section: 3, Summary: "正常 CI 唯一路径为 accepted SQLite correctness identity 加实时验证 refresh runtime，再到 LPT 和无上限并发 ECI shards", Enforcement: "runtime call graph + archtest"},
	{ID: "3.2", Section: 3, Summary: "正常 shard 显式绑定本次实时验证的 refresh snapshot 与同一 cached immutable image，禁止 AutoMatch 或 registry fallback", Enforcement: "ECI request validation"},
	{ID: "3.3", Section: 3, Summary: "候选 Gate 在 exact candidate tree 内增量编译，测试二进制在同一 ECI shard worker 路径按 compile group 各编译一次；cache hit 不跳过身份验证", Enforcement: "materializer + shard worker + receipt"},
	{ID: "3.4", Section: 3, Summary: "工具链与依赖 correctness seed 绑定 accepted OCI base，Go/module 编译缓存可来自其有限期 refresh 镜像层；ECI request 数据面仍只有 source/work/temp 三个 EmptyDir 和单个 init-only 无凭据 bootstrap ConfigFileVolume，禁止 DataCache、缓存卷、逐 shard 展开或第二状态源", Enforcement: "accepted-base binding + ECI request shape + image closure + archtest"},
	{ID: "3.5", Section: 3, Summary: "accepted ImageCache 在 /opt/super-dolphin-gate/source-baseline.git 提供 RunnerBaseTree 的 tree/blob closure 与确定性无 parent baseline commit；每次 CI 只上传标准 v2 git-bundle-thin，bundle 由 tree=SourceSpec parent/base tree 的 candidate-parent synthetic commit（唯一 parent=baseline）再连接 tree=候选的 transport commit，且 header 恰好一个 prerequisite、物化 refs/source/base 指向 synthetic base；严禁自包含/full fallback/raw whole repo，bundle 与 strict manifest 完整上传后才按 SQLite LPT 创建全部并发 shards", Enforcement: "source baseline + deterministic transport commit + strict bundle/manifest verifier + upload barrier + archtest"},
	{ID: "3.6", Section: 3, Summary: "accepted GHCR 镜像路径仍延迟加载固定 ghcr.io 短期凭据；实时验证的 refresh cache-only 路径要求主/init 为同一 cached immutable image 且不编码 registry 凭据，禁止访问已删除临时 registry 或回退拉取", Enforcement: "explicit cache-only request + credential omission + dynamic field guard + redaction tests"},
	{ID: "3.7", Section: 3, Summary: "MISS-only 路径按唯一矩阵构造并 create-only 上传同 job 内容寻址的 accepted schema14/nested CompileGroup1/临时 manifest1 bootstrap request、current ShardRequest15 与 worker manifest2；accepted bootstrap 的 gate_ids 仅在 accepted encoder/identity 边界投影，current request/manifest 保留精确 per-package IDs。两个请求各限 1 MiB，旧版本、unknown fields、schema 协商、兼容 fallback 或刷新镜像绕过升级均严格拒绝", Enforcement: "catalog/fingerprint/worker-digest lookup barrier + protocol matrix guard + dual-request manifest guard + request byte-limit tests"},
	{ID: "4.1", Section: 4, Summary: "唯一写 accepted singleton 的路径是 normal run/hook 空库 bootstrap；首写前必须消费配置 strict receipt 并实时 Describe 阿里云 ECI cache/container groups，绑定 provider、region、唯一 group/container、Ready snapshot、immutable image、tags、零退出、真实时间、逐项 normal CPU/内存、generation=1、state SHA、源码/工具链/策略/seed 与固定规格；外部 operator 仅可用只读 ConfigFileVolume 投影控制文本和小型增量 bundle，禁止投影依赖、缓存、registry 凭据或第二状态源；公网私有 registry 的 ImageCache 临时 ECI 必须绑定 EIP 或已验证 NAT，并只在进程和 API 密文参数传短期凭据，终态后确认 EIP 解绑", Enforcement: "receipt validation + live ECI API verification + SQLite INSERT + archtest"},
	{ID: "4.2", Section: 4, Summary: "首写只允许空 singleton 原子 INSERT baseline 与账本元数据；同态并发幂等收敛，异态、非空、缺字段或非首代 receipt 必须 fail-fast", Enforcement: "strict bootstrap boundary"},
	{ID: "4.3", Section: 4, Summary: "pre-push 从 exact pushed tree 后台静默启动唯一 dispatcher；仅当 OSS strict 成功回执超过 24h 才在 ECI 内创建有限期非权威 ImageCache；normal run 每次 strict 读取并实时复核该物料后显式使用，维护锁只去重刷新", Enforcement: "exact-tree hook dispatch + scheduling receipt + live runtime selection + maintenance lock + archtest"},
	{ID: "5.1", Section: 5, Summary: "shard 数只受可分片原子 workload 数量限制", Enforcement: "LPT planner + archtest"}, {ID: "5.2", Section: 5, Summary: "云配额与 API 限流必须显式失败，不得静默降并发或转本地", Enforcement: "runtime + archtest"},
	{ID: "5.3", Section: 5, Summary: "normal 按单 workload 账本估时固定选择 <=5s 2C/4GiB、5-70s 4C/8GiB、>70s 8C/16GiB，资源身份持久化进 plan 并原样投影到 ECI selector，禁止后续按 duration 二次分类；先分档再在档内 LPT；首次升档无新档 exact sample 时携带上一档权威实测估值规划新档，不伪造样本或回退资源；bootstrap 三类均固定 2C/4GiB，资源策略摘要只进入规划、duration sample 与 shard/run evidence，不进入 PASS identity；禁止额外内存档、混档污染、自动内存抬升、CPU 抬档、按 shard 聚合估时或复用旧策略 PASS，观测或 OOM 在同档没有更大内存必须 fail-fast", Enforcement: "tiered LPT + workload plan projection + resource selector + PASS identity tests"},
	{ID: "5.4", Section: 5, Summary: "CompileGroup schema v2 冻结 selector 估时、batch digest、wave/batch 覆盖与 warning；普通 compile group 仅允许显式 side-effect-safe allowlist、同 resource、严格串行共箱，archtest/super-dolphin-gate/codexapp/mcp-lsp/agent-terminal/race/benchmark 必须独占；同一 group 只编译一次，critical cost=compile once + Σwave max(body)，BodySum 仅 coverage。archtest 每组最多 64 个 selector、423 个 selector 约 7 组并无上限并发，GOMEMLIMIT=3GiB；旧版本/伪造 manifest/helper/manual selector 由执行协议拒绝；worker supervisor 必须显式将 TMPDIR 绑定现有 temp-data 挂载根 /tmp，禁止依赖镜像默认环境；每个 batch 必须在该挂载根下创建唯一短 0700 运行根并令 TMPDIR/GOTMPDIR 指向其子目录，结束时清理，禁止使用长 lane/batchRoot；normal 无历史固定 2C/4GiB、owner fixed-point 在 PlannedWorkload 前完成、calibration 固定 4C/8GiB", Enforcement: "compile-group schema/batch/packing/critical-path/history arch guard + worker/planner/coordinator tests"},
	{ID: "5.5", Section: 5, Summary: "除 release owner 外所有重门禁必须作为 canonical shardable workload，在 PASS lookup 后仅 MISS 进入历史耗时 LPT 与无上限 ECI 分片；最终串行 owner 只能对逐 workload 权威证据生成固定大小、版本化且防篡改的 proof root，不得重跑门禁或拼接无界子日志", Enforcement: "workload catalog + miss-only planner + bounded owner proof + archtest"},
	{ID: "6.1", Section: 6, Summary: "独立于 normal 的固定 4C/8GiB 校准规格贯穿 RunInput、ShardRequest、receipt、SQLite 与 checkpoint identity", Enforcement: "field guard + store"},
	{ID: "6.2", Section: 6, Summary: "校准规格漂移或缺失必须 fail-fast，固定规格不限制 shard 并发", Enforcement: "validation + archtest"},
	{ID: "7.1", Section: 7, Summary: "shard 与 workload 账本以统一 1ms 分辨率显式表达六个耗时阶段；compile group 另以 test_binary_compile 记录每组一次；实际为正的亚毫秒 worker 阶段记为 1ms，禁止降为缺失的 0ms", Enforcement: "worker timing producer + receipt + ledger renderer"},
	{ID: "7.2", Section: 7, Summary: "账本证明 workload 实际 executed miss 或严格 reused hit，并记录 canonical reuse proof 与仅用于加速的 Go cache、前端 seed/Vite cache 证据", Enforcement: "receipt + ledger renderer"},
	{ID: "7.3", Section: 7, Summary: "不适用阶段写 not_applicable，缺失应有观测拒绝 authoritative receipt；orchestration overhead 使用 v2 accounted interval union，扣除 workload、ECI wait、源码物化、候选 Gate 编译和 test-binary 编译的 measured 区间且不重复计数", Enforcement: "receipt validation + overhead schema/policy guard"},
	{ID: "7.4", Section: 7, Summary: "100 秒告警固定 warn_and_continue；不得 cancel、kill 或 fail shard", Enforcement: "warning-action validation + archtest"},
	{ID: "8.1", Section: 8, Summary: "DataCache、旧 bundle、本地 Docker、ACR、JSON truth、GitHub remote-CI runner、通用/第二 provider executor、跨 shard CAS 与隐式 fallback 禁止存在；测试二进制编译只能留在既有 ECI shard worker 路径，镜像物料也必须在 ECI 构建", Enforcement: "deletion + archtest"},
	{ID: "8.2", Section: 8, Summary: "固定 shard 数、CI 并发上限、accepted 缺失自动重建及接入 SQLite 的 successor refresh executor 禁止存在；唯一 pre-push 后台候选刷新维护不得阻塞 push、等待刷新结果或成为第二 CI executor", Enforcement: "hook non-blocking boundary + script deletion + archtest"},
	{ID: "9.1", Section: 9, Summary: "变更必须闭合 LSP、字段链、状态矩阵、守卫和变更面测试", Enforcement: "repository gates"},
	{ID: "9.2", Section: 9, Summary: "远程验收绑定同一 candidate tree、generation、snapshot、资源与完整账本", Enforcement: "authoritative receipt"}, {ID: "9.3", Section: 9, Summary: "非 authoritative 结果保持 NOT_VERIFIED，warm CI 超过 100 秒继续优化", Enforcement: "remote acceptance"},
	{ID: "7.5", Section: 7, Summary: "七个 SQLite 历史根写前必须证明 generation 已被同一 authority 接受，并共享全库唯一保留集合；identity collision、retention proof 缺失或悬空 origin 必须 fail-fast，不得以幂等吞错或默认当前代掩盖。ci_retained_workload_pass_proofs 仅是 live reused consumer 的不可变辅助投影，不是第八历史根或第二 authority；只允许 current+prev2 consumer 消费，第四代可删除 gen1 direct root 而不允许 v15 fallback。每代行数不限，只保留最新 3 个有数据代，第四代写入与第一代全族淘汰同事务；SQLite FULL auto-vacuum 在提交时归还淘汰页", Enforcement: "accepted-generation proof + identity-collision guard + consumer proof projection + single write-transaction compactor + FULL auto-vacuum + retired-object archtest"},
	{ID: "7.6", Section: 7, Summary: "失败终态仍写 non-authoritative provisional run、迁移 live warning 且只保留真实已测区间；缺失阶段不伪造 0ms/not_applicable", Enforcement: "failed-run SQLite projection + receipt authority guard"},
	{ID: "7.7", Section: 7, Summary: "required-check 精确绑定当前持久化 workload catalog；较小 profile 不伪造 release-only 检查，release 仍覆盖完整六类", Enforcement: "catalog-scoped observation + receipt + SQLite reload validation"},
	{ID: "7.8", Section: 7, Summary: "阿里云 ECI 终态生命周期字段允许沿同一 Describe 路径按 PollInterval 每分片最多重读 3 次；不得伪造时间、提前消费报告、移出 pending、取消兄弟或跳过清理，窗口耗尽仍 fail-fast", Enforcement: "bounded terminal evidence reread + fanout drain tests + timing guard"},
}

// ValidateTimingWarningAction 拒绝把 100 秒目标告警转化为取消、终止或失败。
func ValidateTimingWarningAction(action TimingWarningAction) error {
	if action != TimingWarningWarnAndContinue {
		return fmt.Errorf("remote CI timing warning action must equal %q", TimingWarningWarnAndContinue)
	}
	return nil
}

// ValidateTimingWarningEvidenceKind 拒绝未实现生产者的 target-warning 证据类型。
func ValidateTimingWarningEvidenceKind(kind TimingWarningEvidenceKind) error {
	switch kind {
	case TimingWarningEvidenceRunning, TimingWarningEvidenceTestBody, TimingWarningEvidenceTotal:
		return nil
	default:
		return fmt.Errorf("remote CI timing warning evidence kind %q is unsupported", kind)
	}
}

// ValidateRequiredChecksObservedPass 拒绝 missing、重复、未通过、未执行且未复用，
// 以及缺少规范历史 proof 的复用检查。执行与复用可在同一 run 中混合。
func ValidateRequiredChecksObservedPass(observations []CheckObservation) error {
	return ValidateRequiredChecksObservedPassFor(RequiredChecks(), observations)
}

// ValidateRequiredChecksObservedPassFor 要求观测精确覆盖当前 workload catalog 映射出的
// canonical 检查集合；较小 profile 不得伪造未计划检查，release 仍由完整六项集合约束。
func ValidateRequiredChecksObservedPassFor(required []RequiredCheck, observations []CheckObservation) error {
	if err := validateRequiredCheckScope(required); err != nil {
		return err
	}
	if len(observations) != len(required) {
		return fmt.Errorf("remote CI required check observations = %d, want %d", len(observations), len(required))
	}
	seen := make(map[RequiredCheck]struct{}, len(observations))
	for _, observation := range observations {
		if err := validateRequiredCheckObservation(observation); err != nil {
			return err
		}
		if _, duplicate := seen[observation.Check]; duplicate {
			return fmt.Errorf("remote CI required check %q is duplicated", observation.Check)
		}
		seen[observation.Check] = struct{}{}
	}
	for _, check := range required {
		if _, exists := seen[check]; !exists {
			return fmt.Errorf("remote CI required check %q is missing", check)
		}
	}
	return nil
}

// validateRequiredCheckScope 拒绝空、重复、未知或非 canonical 顺序的检查范围。
func validateRequiredCheckScope(required []RequiredCheck) error {
	if len(required) == 0 {
		return errors.New("remote CI required check scope is empty")
	}
	canonical := RequiredChecks()
	next := 0
	for _, check := range required {
		for next < len(canonical) && canonical[next] != check {
			next++
		}
		if next == len(canonical) {
			return fmt.Errorf("remote CI required check scope contains unknown, duplicate, or out-of-order check %q", check)
		}
		next++
	}
	return nil
}

// validateRequiredCheckObservation 校验单个 normal 检查的通过、复用与完整回执语义。
func validateRequiredCheckObservation(observation CheckObservation) error {
	if !observation.Passed || (!observation.Executed && !observation.Reused) {
		return fmt.Errorf("remote CI required check %q did not pass", observation.Check)
	}
	if err := validateRequiredCheckReuse(observation); err != nil {
		return err
	}
	if err := validateCheckObservationReceipt(observation); err != nil {
		return err
	}
	return nil
}

// validateRequiredCheckReuse 校验严格复用只携带规范的历史证明摘要。
func validateRequiredCheckReuse(observation CheckObservation) error {
	if observation.Reused && !isCanonicalSHA256(observation.ReuseProofSHA256) {
		return fmt.Errorf("remote CI required check %q reuse proof digest is invalid", observation.Check)
	}
	if !observation.Reused && observation.ReuseProofSHA256 != "" {
		return fmt.Errorf("remote CI required check %q must not carry a reuse proof without reuse", observation.Check)
	}
	return nil
}

// validateCheckObservationReceipt 校验 normal 检查回执字段完整且摘要可复算。
func validateCheckObservationReceipt(observation CheckObservation) error {
	if strings.TrimSpace(observation.SourceTree) == "" || strings.TrimSpace(observation.AcceptedSnapshotID) == "" || strings.TrimSpace(observation.PlanDigest) == "" || observation.StartedAtUnixMS <= 0 || observation.CompletedAtUnixMS <= observation.StartedAtUnixMS || observation.DurationMS != observation.CompletedAtUnixMS-observation.StartedAtUnixMS || observation.DurationMS <= 0 {
		return fmt.Errorf("remote CI required check %q receipt is incomplete", observation.Check)
	}
	wantDigest, err := CheckObservationReceiptDigest(observation)
	if err != nil || observation.ReceiptSHA256 != wantDigest {
		return fmt.Errorf("remote CI required check %q receipt digest is invalid", observation.Check)
	}
	return nil
}

// CheckObservationReceiptDigest 基于所有受约束字段计算必需检查回执的规范摘要，
// 且不把回执自身的摘要字段纳入输入。
func CheckObservationReceiptDigest(observation CheckObservation) (string, error) {
	data := fmt.Sprintf("%s\x00%t\x00%t\x00%s\x00%t\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d", observation.Check, observation.Executed, observation.Reused, observation.ReuseProofSHA256, observation.Passed, observation.SourceTree, observation.AcceptedSnapshotID, observation.PlanDigest, observation.StartedAtUnixMS, observation.CompletedAtUnixMS, observation.DurationMS)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(data))), nil
}

// isCanonicalSHA256 校验 sha256 摘要仅使用小写十六进制且长度固定。
func isCanonicalSHA256(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, character := range value[len(prefix):] {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// ValidateTargetPlatform 拒绝非 linux/amd64 的远程 CI 或外部供给物料目标。
func ValidateTargetPlatform(platform string) error {
	if platform != TargetPlatform {
		return fmt.Errorf("remote CI platform must equal %q", TargetPlatform)
	}
	return nil
}

// ValidateExecutionProvider 拒绝任何非阿里云 ECI 的远程 CI 执行或验收身份。
func ValidateExecutionProvider(provider string) error {
	if provider != ExecutionProviderID {
		return fmt.Errorf("remote CI execution provider %q is not the accepted Alibaba Cloud ECI provider", provider)
	}
	return nil
}

// ValidateAcceptedBaselineProjection 锁定读取侧最小投影也必须属于当前阿里云 ECI schema 与明确 region。
func ValidateAcceptedBaselineProjection(schemaVersion uint32, provider, regionID string) error {
	if schemaVersion != BaselineStateSchemaVersion {
		return fmt.Errorf("remote baseline state schema %d is not accepted schema %d", schemaVersion, BaselineStateSchemaVersion)
	}
	if provider != ExecutionProviderID || strings.TrimSpace(regionID) == "" || regionID != strings.TrimSpace(regionID) {
		return errors.New("remote baseline projection must bind the Alibaba Cloud ECI provider and one explicit region")
	}
	return nil
}

// ValidateGoToolchainVersion 拒绝偏离基准镜像的 Go 发行版。
func ValidateGoToolchainVersion(version string) error {
	if version != GoToolchainVersion {
		return fmt.Errorf("remote CI Go toolchain must equal %q", GoToolchainVersion)
	}
	return nil
}

// ValidateShardTargetDuration 保证 100 秒只作为统一的规划和非终止告警目标输入。
func ValidateShardTargetDuration(duration time.Duration) error {
	if duration != ShardTargetDuration {
		return fmt.Errorf("remote CI shard target duration must equal %s", ShardTargetDuration)
	}
	return nil
}

// Validate 校验代码契约自身不存在重复 ID、章节缺口或无效不变量。
func Validate() error {
	for _, validate := range []func() error{validateContractIdentity, validateContractConstants, ValidateRemoteRuntimeIdentity, validateContractObservations, validateRequirements, validateSQLAuthorityBindings, validateSQLAuthoritySchemaTables} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateContractIdentity 校验代码契约的稳定身份与唯一 owner 路径均已定义。
func validateContractIdentity() error {
	values := []string{ID, DocumentPath, ExecutionPathID, ExecutionProviderID, CIExecutionBoundary, GenerationOneBootstrapPathID, ImageCacheRefreshOperatorPathID, SQLAuthorityID, CacheMaterialSchemaID, CacheMaterialAuthority, CompileGroupExecutionPathID}
	if slices.Contains(values, "") {
		return errors.New("remote CI contract identity is incomplete")
	}
	return nil
}

// validateContractConstants 校验固定平台、时长、保留和并发常量。
func validateContractConstants() error {
	for _, validate := range []func() error{validateContractConstantValues, validateContractPolicyRules} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateContractConstantValues 校验请求限制、规划身份与 PASS 环境版本。
func validateContractConstantValues() error {
	for _, validate := range []func() error{validateCacheMaterialConstants, validateRequestLimits, validatePlanningConstants} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateCacheMaterialConstants 校验缓存材料的权威边界。
func validateCacheMaterialConstants() error {
	if CacheMaterialSchemaID != "remote-ci-cache-material/v1" || CacheMaterialAuthority != "non_authoritative_material" {
		return errors.New("remote CI cache material authority contract drifted")
	}
	return nil
}

// validateRequestLimits 校验远程请求大小和刷新时间窗。
func validateRequestLimits() error {
	if RemoteShardRequestMaxBytes != 1<<20 {
		return errors.New("remote CI shard request byte limit must equal 1 MiB")
	}
	if ImageCacheRefreshInterval != 24*time.Hour {
		return errors.New("remote CI ImageCache refresh interval must equal 24 hours")
	}
	return nil
}

// validatePlanningConstants 校验协议矩阵、规划策略和 PASS 环境版本。
func validatePlanningConstants() error {
	if err := ValidateRemoteCIProtocolVersions(AcceptedBootstrapRequestSchemaVersion, AcceptedCompileGroupSchemaVersion, AcceptedBootstrapManifestSchemaVersion, ShardRequestSchemaVersion, ShardExecutionManifestSchemaVersion); err != nil {
		return err
	}
	if WorkloadPlanningAlgorithmID == "" || WorkloadPlanningObjective == "" || WorkloadPlanningSearchNodeBudget != 1_000_000 || WorkloadEstimationPolicyVersion == "" || CompileGroupSerialPackingPolicyID == "" || CriticalPathCostPolicyID == "" {
		return errors.New("remote CI planning and compile-group policy identities are incomplete")
	}
	if err := ValidateWorkloadPassEnvironmentSchema(WorkloadPassEnvironmentSchemaVersion); err != nil {
		return err
	}
	return nil
}

func validateContractPolicyRules() error {
	for _, validate := range []func() error{
		func() error { return ValidateExecutionProvider(ExecutionProviderID) },
		func() error { return ValidateTargetPlatform(TargetPlatform) },
		func() error { return ValidateGoToolchainVersion(GoToolchainVersion) },
		func() error {
			return ValidateECIMultiZoneScheduling(ECIMultiZoneScheduleStrategy, []ECIVSwitch{{ID: "vsw-zone-a", ZoneID: "cn-test-a"}, {ID: "vsw-zone-b", ZoneID: "cn-test-b"}})
		},
		func() error { return ValidateShardTargetDuration(ShardTargetDuration) },
		ValidateRetentionGenerations,
		ValidateTimingContract,
		ValidateSourceTransportContract,
		func() error { return ValidateTimingWarningAction(TimingWarningWarnAndContinue) },
		func() error { return ValidateTimingWarningEvidenceKind(TimingWarningEvidenceRunning) },
		func() error { return ValidateTimingWarningEvidenceKind(TimingWarningEvidenceTestBody) },
		func() error { return ValidateTimingWarningEvidenceKind(TimingWarningEvidenceTotal) },
		ValidateConcurrencyPolicy,
	} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateContractObservations 构造最小完整 normal 回执以锁定 fail-fast 语义。
func validateContractObservations() error {
	required, err := requiredPassObservations()
	if err != nil {
		return err
	}
	if err := ValidateRequiredChecksObservedPass(required); err != nil {
		return err
	}
	return nil
}

// requiredPassObservations 返回每项 normal 检查的最小实际执行 PASS 回执。
func requiredPassObservations() ([]CheckObservation, error) {
	observations := make([]CheckObservation, 0, len(RequiredChecks()))
	for _, check := range RequiredChecks() {
		observation := CheckObservation{Check: check, Executed: true, Passed: true, SourceTree: "target-tree", AcceptedSnapshotID: "accepted-snapshot", PlanDigest: "plan-digest", StartedAtUnixMS: 1, CompletedAtUnixMS: 2, DurationMS: 1}
		digest, err := CheckObservationReceiptDigest(observation)
		if err != nil {
			return nil, err
		}
		observation.ReceiptSHA256 = digest
		observations = append(observations, observation)
	}
	return observations, nil
}

// validateRequirements 校验所有需求的 ID 唯一、字段完整且九个章节均有映射。
func validateRequirements() error {
	seenIDs := make(map[string]struct{}, len(requirements))
	seenSections := make(map[uint8]struct{}, 9)
	for _, requirement := range requirements {
		if strings.TrimSpace(requirement.ID) == "" || requirement.Section < 1 || requirement.Section > 9 || strings.TrimSpace(requirement.Summary) == "" || strings.TrimSpace(requirement.Enforcement) == "" {
			return fmt.Errorf("remote CI requirement %+v is invalid", requirement)
		}
		if _, exists := seenIDs[requirement.ID]; exists {
			return fmt.Errorf("remote CI requirement ID %q is duplicated", requirement.ID)
		}
		seenIDs[requirement.ID] = struct{}{}
		seenSections[requirement.Section] = struct{}{}
	}
	for section := uint8(1); section <= 9; section++ {
		if _, exists := seenSections[section]; !exists {
			return fmt.Errorf("remote CI contract section %d has no code requirement", section)
		}
	}
	return nil
}

// validateSQLAuthorityBindings 校验每个 SQLite authority domain 和表均为一对一映射。
func validateSQLAuthorityBindings() error {
	authorityBindings := sqlAuthorityBindingList()
	seenSQLDomains := make(map[SQLDomain]struct{}, len(authorityBindings))
	seenSQLTables := make(map[string]struct{}, len(authorityBindings))
	for _, binding := range authorityBindings {
		if binding.Domain == "" || strings.TrimSpace(binding.Table) == "" {
			return fmt.Errorf("remote CI SQL authority binding %+v is invalid", binding)
		}
		if _, exists := seenSQLDomains[binding.Domain]; exists {
			return fmt.Errorf("remote CI SQL domain %q is duplicated", binding.Domain)
		}
		if _, exists := seenSQLTables[binding.Table]; exists {
			return fmt.Errorf("remote CI SQL table %q owns multiple domains", binding.Table)
		}
		seenSQLDomains[binding.Domain] = struct{}{}
		seenSQLTables[binding.Table] = struct{}{}
	}
	return nil
}
