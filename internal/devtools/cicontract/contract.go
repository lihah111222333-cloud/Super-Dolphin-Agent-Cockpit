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
	ID = "remote-ci-aliyun-eci-imagecache/v4"
	// DocumentPath 是 Accepted 文档契约的仓库相对路径。
	DocumentPath = "docs/契约/remote-ci-eci-imagecache-contract.md"
	// ExecutionPathID 标识唯一正常 CI 执行路径。
	ExecutionPathID = "sqlite-generation-one-bootstrap-or-accepted-imagecache-snapshot-aliyun-eci-shards/v2"
	// ExecutionProviderID 冻结远程 CI 唯一执行与验收提供方；不得抽象或降级到其他容器平台。
	ExecutionProviderID = "aliyun-eci/v1"
	// CIExecutionBoundary 冻结所有远程 CI 动作及其镜像物料加工只能运行在阿里云 ECI；
	// GitHub Actions 不得提供远程 CI runner、编译、测试、cache-prime 或镜像构建能力。
	// 独立产品发布 workflow 不签发远程 CI 结论，不属于本契约执行面。
	CIExecutionBoundary = "aliyun-eci-only-no-github-runner/v1"
	// GenerationOneBootstrapPathID 标识 normal run/hook 在空 singleton 时消费配置 strict ECI receipt 的唯一首代路径。
	// 非空 singleton 与所有后续代仍只消费 accepted snapshot；不得实现 successor refresh executor。
	GenerationOneBootstrapPathID = "normal-run-hook-configured-aliyun-eci-generation-one-strict-receipt-bootstrap/v1"
	// ImageCacheRefreshOperatorPathID 标识唯一候选缓存刷新入口；本地只上传内容寻址源码与依赖，编译和镜像加工只在 ECI 内执行。
	// 该入口只创建有限期非权威候选，不读取或改写 SQLite，也不得接入 normal run/hook。
	ImageCacheRefreshOperatorPathID = "script-oss-handoff-aliyun-eci-offline-imagecache-candidate/v1"
	// SQLAuthorityID 标识 accepted state、规划、回执和校准共用的唯一数据源。
	SQLAuthorityID = "duration-ledger-sqlite/v1"
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
	// ArchtestMaxSelectorsPerCompileGroup 冻结单个 archtest compile group 的
	// selector 上限。超过该上限的 exact selector 必须由 planner 拆成独立
	// CompileGroup/ECI shard，不能在同一个 4 GiB worker 内堆积成一个 test-binary。
	ArchtestMaxSelectorsPerCompileGroup = 64
	// RemoteShardRequestMaxBytes 冻结 coordinator、OSS materializer 与 strict
	// JSON decoder 共用的完整 shard request 上限。该上限约束请求总字节数，
	// 不再用 gate 数量猜测合法分片大小。
	RemoteShardRequestMaxBytes = 1 << 20
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
	{ID: "1.2", Section: 1, Summary: "normal CI 仅在空 singleton 时消费配置 strict ECI receipt 原子首写，非空后只读 accepted explicit snapshot；pre-push 只按 exact pushed tree 非阻塞调度唯一隔离刷新脚本，脚本只创建有限期非权威候选且不接入 SQLite", Enforcement: "runtime call graph + hook dispatch + script boundary + archtest"},
	{ID: "1.3", Section: 1, Summary: "hook、job 与动态 shards 无仓库并发上限；index.lock 仅保护 Git index", Enforcement: "cicontract concurrency policy + archtest"},
	{ID: "1.5", Section: 1, Summary: "所有远程 CI 动作、镜像构建与 cache-prime 只能在阿里云 ECI 执行；禁止 GitHub Actions 或其他环境承载远程 CI", Enforcement: "provider identity + ECI request/receipt field guard + remote-CI workflow deletion + archtest"},
	{ID: "1.6", Section: 1, Summary: "100 秒只触发一次目标超限告警；不得取消、中断、kill 或标失败", Enforcement: "planner + non-terminating timing warning"},
	{ID: "1.7", Section: 1, Summary: "校准运行使用独立于 normal 的固定 4C/8GiB 规格并被回执绑定；所有 ECI 执行使用 100 GiB 原始临时盘", Enforcement: "request + receipt + SQLite + ECI CLI guard"},
	{ID: "1.8", Section: 1, Summary: "运行以真实起止和 duration_ms 分别记录物化、编译、启动、测试、总耗时、缓存与等待；并发聚合使用精确 interval union", Enforcement: "receipt + SQLite timing ledger"},
	{ID: "1.9", Section: 1, Summary: "normal 与 calibration 默认均只复用同一 correctness-bound exact workload identity 的权威 PASS；all-hit 不创建 workload CI ECI、workload shard、workload OSS/temp 或 calibration，且不执行测试；部分命中只执行 miss；只有显式 --force 才绕过 PASS 查询并执行全部 shardable workload；execution mode、资源规格与 force 只绑定本次 run/shard/duration evidence，不阻断或污染等价 PASS", Enforcement: "required-check catalogue + strict reuse receipt validation + force audit + archtest"},
	{ID: "2.1", Section: 2, Summary: "accepted baseline、duration history、run/shard receipt 与 calibration checkpoint 只读写同一 SQLite authority", Enforcement: "SQLite schema + store"},
	{ID: "2.2", Section: 2, Summary: "运行时缓存只由 accepted ImageCache ID 与 snapshot ID 选择", Enforcement: "state validation + ECI request"},
	{ID: "2.3", Section: 2, Summary: "候选源码和 Gate 编译分别绑定 exact Git identity 与根 module 跨代稳定传递编译闭包；Gate 禁止导入 repository-local replace module，嵌套 module metadata 只进 worker execution digest", Enforcement: "materializer + receipt + closure guard"},
	{ID: "2.4", Section: 2, Summary: "严格 JSON 仅作协议编码，OSS 仅作内容寻址传输，二者均非权威", Enforcement: "strict decoders + archtest"},
	{ID: "2.5", Section: 2, Summary: "accepted state、check receipts、timing 与 warnings 只写同一个 SQLite authority", Enforcement: "SQLite schema + receipt store"},
	{ID: "2.6", Section: 2, Summary: "generation-one 只能由 normal CI 消费配置中的外部严格 ECI receipt；唯一刷新脚本用 OSS strict scheduling receipt 判断 24h 成功间隔并创建非权威 ImageCache 候选，但 receipt 不是 SQLite 权威且不得晋级 accepted", Enforcement: "strict receipt boundary + candidate-only scheduling receipt guard + archtest"},
	{ID: "2.7", Section: 2, Summary: "无 token 请求只返回 --agent-token=issue 与 env=issue 申请方式和实际 token 的 flag/env 使用方式；仅单一显式 issue 才签发 raw token，前两阶段均不执行 CI/ECI；git hook 无状态且只继承/验证实际 env token，跨 SQLite/OSS/ECI/log/tag/checkpoint/receipt 只传 sha256 digest", Enforcement: "agent-token contract + field guard + archtest"},
	{ID: "2.8", Section: 2, Summary: "首次读取缺失 SQLite 时原子初始化 schema/index；normal run/hook 仅凭显式 strict ECI receipt 原子首写 baseline 与账本元数据，缺失或漂移仍 fail-fast", Enforcement: "SQLite initializer + generation-one bootstrap test"},
	{ID: "2.9", Section: 2, Summary: "authoritative JobID 的全部 SQLite 投影不可重写；晋级前必须精确回读 aggregate/workload execution 的状态、摘要、时序与执行画像", Enforcement: "provisional write guard + strict aggregate/workload readback tests"},
	{ID: "2.10", Section: 2, Summary: "每次阿里云 CLI 调用同时受 context 超时与进程管道 WaitDelay 约束；子孙进程持有输出管道不得使轮询、汇总或清理无界等待", Enforcement: "ECI CLI process watchdog + inherited-pipe regression test"},
	{ID: "3.1", Section: 3, Summary: "正常 CI 唯一路径为 accepted SQLite 到 LPT 到无上限并发 ECI shards", Enforcement: "runtime call graph + archtest"},
	{ID: "3.2", Section: 3, Summary: "正常 shard 显式绑定 accepted snapshot，禁止 AutoMatch 选择", Enforcement: "ECI request validation"},
	{ID: "3.3", Section: 3, Summary: "候选 Gate 在 exact candidate tree 内增量编译，测试二进制在同一 ECI shard worker 路径按 compile group 各编译一次；cache hit 不跳过身份验证", Enforcement: "materializer + shard worker + receipt"},
	{ID: "3.4", Section: 3, Summary: "工具链、依赖、浏览器与缓存只直读 accepted ImageCache 镜像层；ECI request 数据面只能有 source/work/temp 三个 EmptyDir，normal shard 只额外允许一个 init-only、只读、无凭据 bootstrap ConfigFileVolume 绕过命令字符限制，main 不得挂载，所有卷总数本地锁定 ECI 20 卷硬上限；禁止 FlexVolume/OSS bootstrap、expanded-data、DataCache、缓存卷、subPath 或逐 shard 复制展开；node_modules 与 Vite cache 必须链接镜像层，dist 不得冒充 cache；Dockerfile/seed worker 只作配方审计且不得改变依赖内容 identity，依赖不变必须复用上一代 runtime 镜像", Enforcement: "ECI request shape + stable dependency identity + image closure + executor seed behavior + archtest"},
	{ID: "3.5", Section: 3, Summary: "accepted ImageCache 在 /opt/super-dolphin-gate/source-baseline.git 提供 RunnerBaseTree 的 tree/blob closure 与确定性无 parent baseline commit；每次 CI 只上传标准 v2 git-bundle-thin，bundle 由 tree=SourceSpec parent/base tree 的 candidate-parent synthetic commit（唯一 parent=baseline）再连接 tree=候选的 transport commit，且 header 恰好一个 prerequisite、物化 refs/source/base 指向 synthetic base；严禁自包含/full fallback/raw whole repo，bundle 与 strict manifest 完整上传后才按 SQLite LPT 创建全部并发 shards", Enforcement: "source baseline + deterministic transport commit + strict bundle/manifest verifier + upload barrier + archtest"},
	{ID: "3.6", Section: 3, Summary: "私有 GHCR normal shard 只从当前进程两个固定环境变量取得完整短期凭据并映射 ECI ImageRegistryCredential；server 固定 ghcr.io 且必须匹配主/init 不可变镜像；缺失或错配在创建前 fail-fast，原始凭据禁止进入 remote config、SQLite、receipt、日志、tag、OSS、Git、命令投影或 ConfigFileVolume", Enforcement: "environment loader + ECI request mapping + dynamic field guard + redaction tests"},
	{ID: "3.7", Section: 3, Summary: "PASS lookup 先于一切规划和副作用，只有 MISS 构造并 create-only 上传同 job 内容寻址的冻结 accepted schema14/CompileGroup-v1 bootstrap request 与完整 current CompileGroup-v2 ShardRequest；accepted bootstrap 的 gate_ids 仅在 accepted encoder/identity 边界把 expansion-only backend:nilness::go-package::<pkg> 投影为 canonical backend:nilness，current request/manifest 保留精确 per-package IDs，禁止把投影扩散到 coordinator/current worker；accepted Gate 发布临时 v1 manifest，候选 Gate 编译后严格交叉校验并原子替换固定 v2 manifest，worker 保持唯一 executor，两个请求各限 1 MiB且禁止宽松解码、协商、fallback 或刷新镜像绕过升级", Enforcement: "miss-only call-order guard + dual-request rolling manifest guard + request byte-limit tests"},
	{ID: "4.1", Section: 4, Summary: "唯一写 accepted singleton 的路径是 normal run/hook 空库 bootstrap；首写前必须消费配置 strict receipt 并实时 Describe 阿里云 ECI cache/container groups，绑定 provider、region、唯一 group/container、Ready snapshot、immutable image、tags、零退出、真实时间、逐项 normal CPU/内存、generation=1、state SHA、源码/工具链/策略/seed 与固定规格；外部 operator 仅可用只读 ConfigFileVolume 投影控制文本和小型增量 bundle，禁止投影依赖、缓存、registry 凭据或第二状态源；公网私有 registry 的 ImageCache 临时 ECI 必须绑定 EIP 或已验证 NAT，并只在进程和 API 密文参数传短期凭据，终态后确认 EIP 解绑", Enforcement: "receipt validation + live ECI API verification + SQLite INSERT + archtest"},
	{ID: "4.2", Section: 4, Summary: "首写只允许空 singleton 原子 INSERT baseline 与账本元数据；同态并发幂等收敛，异态、非空、缺字段或非首代 receipt 必须 fail-fast", Enforcement: "strict bootstrap boundary"},
	{ID: "4.3", Section: 4, Summary: "pre-push 从 exact pushed tree 后台静默启动唯一 dispatcher；仅当 OSS strict 成功回执超过 24h 才由刷新脚本上传内容寻址源码与依赖并在 ECI 内创建有限期非权威 ImageCache 候选；维护锁只去重刷新，不限制 hook、job 或 shard", Enforcement: "exact-tree hook dispatch + scheduling receipt + maintenance lock + archtest"},
	{ID: "5.1", Section: 5, Summary: "shard 数只受可分片原子 workload 数量限制", Enforcement: "LPT planner + archtest"}, {ID: "5.2", Section: 5, Summary: "云配额与 API 限流必须显式失败，不得静默降并发或转本地", Enforcement: "runtime + archtest"},
	{ID: "5.3", Section: 5, Summary: "normal 按单 workload 账本估时固定选择 <=5s 2C/4GiB、5-70s 4C/8GiB、>70s 8C/16GiB，资源身份持久化进 plan 并原样投影到 ECI selector，禁止后续按 duration 二次分类；先分档再在档内 LPT；首次升档无新档 exact sample 时携带上一档权威实测估值规划新档，不伪造样本或回退资源；bootstrap 三类均固定 2C/4GiB，资源策略摘要只进入规划、duration sample 与 shard/run evidence，不进入 PASS identity；禁止额外内存档、混档污染、自动内存抬升、CPU 抬档、按 shard 聚合估时或复用旧策略 PASS，观测或 OOM 在同档没有更大内存必须 fail-fast", Enforcement: "tiered LPT + workload plan projection + resource selector + PASS identity tests"},
	{ID: "5.4", Section: 5, Summary: "CompileGroup schema v2 冻结 selector 估时、batch digest、wave/batch 覆盖与 warning；同一 package-affinity compile group 只执行一次 go test -c；archtest 每个 compile group 最多 64 个 selector、每个 ECI shard 仅一个 test-binary batch/process 并固定 GOMEMLIMIT=3GiB，423 个 selector 按有界组拆成约 7 个独立 CompileGroup/ECI shard 并无上限并发，允许跨 shard 增量编译但禁止跨 shard CAS；同 wave 普通 test2json 并发，codexapp exclusive selector 各占串行 wave；成功 selector 日志最多 512 字节、每个 compile group 首个失败日志保留完整 32KiB 窗口，其余 compile-group selector（包括其他 batch 的失败和 PASS）最多 512 字节；worker plan report framed output 与 strict decoder 累计均不得超过 1 MiB，后端 ExecutionProfile 全字段必须进入 report digest；源码 helper 声明在候选清单阶段排除，旧/伪造 manifest 的 helper/manual selector 由执行协议拒绝；worker supervisor 必须显式将 TMPDIR 绑定现有 temp-data 挂载根 /tmp，禁止依赖镜像默认环境；每个 batch 必须在该挂载根下创建唯一短 0700 运行根并令 TMPDIR/GOTMPDIR 指向其子目录，结束时清理，禁止使用长 lane/batchRoot；batch 的 HOME/XDG 独立、同 shard candidate cache 可写共享、accepted seed 只读共享、metrics 独立，planner warning 必须投影到 RunResult 与 SQLite warnings；compile timing history 只能按 PackageTarget、SemanticKey、Platform、RunnerIdentityDigest、ToolchainDigest、ExecutionMode 与 ResourceClassID/CPU/Memory 九维完整 identity 查询；只允许最近三个 accepted generation 中 authoritative、passed、cleanup-complete、measured/raw observation，source tree、shared input 与 artifact digest 不得跨 identity 混用；normal 无历史固定 small 2C/4GiB，owner fixed-point 必须在 PlannedWorkload 创建前完成，新档无样本时携带上一档实测 compile 值，shared compile cost 每组只计一次且不得写入 selector body；calibration 始终固定 4C/8GiB，不得按 compile duration 重分类", Enforcement: "compile-group schema/batch/helper/warning/history arch guard + worker/planner/coordinator tests"},
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
	{ID: "7.5", Section: 7, Summary: "七个 SQLite 历史根（含 shard overhead aggregate 与逐分片样本）写前证明 generation 已被接受，并共享全库唯一保留集合；每代行数不限，只保留最新 3 个有数据代，第四代写入与第一代全族淘汰同事务；SQLite FULL auto-vacuum 在提交时归还淘汰页，禁止无消费者的全量运行快照事件链", Enforcement: "accepted-generation proof + single write-transaction compactor + FULL auto-vacuum + retired-object archtest"},
	{ID: "7.6", Section: 7, Summary: "失败终态仍写 non-authoritative provisional run、迁移 live warning 且只保留真实已测区间；缺失阶段不伪造 0ms/not_applicable", Enforcement: "failed-run SQLite projection + receipt authority guard"},
	{ID: "7.7", Section: 7, Summary: "required-check 精确绑定当前持久化 workload catalog；较小 profile 不伪造 release-only 检查，release 仍覆盖完整六类", Enforcement: "catalog-scoped observation + receipt + SQLite reload validation"},
	{ID: "7.8", Section: 7, Summary: "阿里云 ECI 终态生命周期字段允许沿同一 Describe 路径按 PollInterval 每分片最多重读 3 次；不得伪造时间、提前消费报告、移出 pending、取消兄弟或跳过清理，窗口耗尽仍 fail-fast", Enforcement: "bounded terminal evidence reread + fanout drain tests + timing guard"},
}

var sqlAuthorityBindings = [...]SQLAuthorityBinding{
	{Domain: SQLDomainAcceptedBaseline, Table: AcceptedBaselineTable},
	{Domain: SQLDomainDurationHistory, Table: DurationSamplesTable},
	{Domain: SQLDomainShardOverhead, Table: DurationShardOverheadsTable},
	{Domain: SQLDomainShardOverheadSample, Table: DurationShardOverheadSamplesTable},
	{Domain: SQLDomainRemoteRun, Table: RemoteRunsTable},
	{Domain: SQLDomainRemoteShard, Table: RemoteShardsTable},
	{Domain: SQLDomainWorkloadExecution, Table: WorkloadExecutionsTable},
	{Domain: SQLDomainRunWarning, Table: RunWarningsTable},
	{Domain: SQLDomainLiveTimingWarning, Table: LiveTimingWarningsTable},
	{Domain: SQLDomainRunTimingWarning, Table: RunTimingWarningsTable},
	{Domain: SQLDomainCalibrationCheckpoint, Table: CalibrationCheckpointsTable},
	{Domain: SQLDomainCheckReceipt, Table: CheckReceiptsTable},
	{Domain: SQLDomainTimingObservation, Table: TimingObservationsTable},
	{Domain: SQLDomainCompileTiming, Table: CompileTimingObservationsTable},
	{Domain: SQLDomainWorkloadCatalog, Table: WorkloadCatalogsTable},
	{Domain: SQLDomainCatalogObservation, Table: CatalogObservationsTable},
	{Domain: SQLDomainCatalogWorkload, Table: CatalogWorkloadsTable},
	{Domain: SQLDomainRunAgentIdentity, Table: RunAgentIdentitiesTable},
	{Domain: SQLDomainShardWorkload, Table: ShardWorkloadsTable},
	{Domain: SQLDomainGateExecution, Table: GateExecutionsTable},
	{Domain: SQLDomainRunWorkloadResult, Table: RunWorkloadResultsTable},
	{Domain: SQLDomainWorkloadPassEvidence, Table: WorkloadPassEvidenceTable},
	{Domain: SQLDomainCalibrationScenario, Table: CalibrationCheckpointScenariosTable},
}

var retentionRootBindings = [...]RetentionRootBinding{
	{Table: DurationSamplesTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: DurationShardOverheadsTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: DurationShardOverheadSamplesTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: CatalogObservationsTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: RemoteRunsTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: WorkloadPassEvidenceTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: CalibrationCheckpointsTable, GenerationColumn: AcceptedGenerationColumn},
}

// SQLAuthorityBindings 返回所有远程 CI 持久化事实的唯一 SQL 表绑定。
func SQLAuthorityBindings() []SQLAuthorityBinding {
	result := make([]SQLAuthorityBinding, len(sqlAuthorityBindings))
	copy(result, sqlAuthorityBindings[:])
	return result
}

// RetentionRootBindings 返回必须由统一三代 compactor 管理的历史根副本。
func RetentionRootBindings() []RetentionRootBinding {
	result := make([]RetentionRootBinding, len(retentionRootBindings))
	copy(result, retentionRootBindings[:])
	return result
}

// ValidateSQLAuthorityBinding 拒绝把一个事实域写入第二张表或非 SQL 真相源。
func ValidateSQLAuthorityBinding(domain SQLDomain, table string) error {
	for _, binding := range sqlAuthorityBindings {
		if binding.Domain == domain {
			if table != binding.Table {
				return fmt.Errorf("remote CI SQL domain %q must use table %q", domain, binding.Table)
			}
			return nil
		}
	}
	return fmt.Errorf("remote CI SQL domain %q is unsupported", domain)
}

// TimingPhases 返回权威耗时契约的稳定顺序。
func TimingPhases() []TimingPhase {
	return []TimingPhase{TimingECIWait, TimingSourceMaterialize, TimingCandidateCompile, TimingStartup, TimingTestBody, TimingTotal}
}

// CompileGroupTimingPhases 返回 compile group 专属的编译阶段；它不属于 workload
// startup/body/total，也不把 candidate Gate CLI 编译时间混入测试二进制编译。
func CompileGroupTimingPhases() []TimingPhase {
	return []TimingPhase{TimingTestBinaryCompile}
}

// ValidateTimingContract 锁定精确耗时字段、阶段与聚合语义的代码 owner。
func ValidateTimingContract() error {
	if TimingDurationColumn != "duration_ms" {
		return errors.New("remote CI timing duration column must be duration_ms")
	}
	wantPhases := [...]TimingPhase{TimingECIWait, TimingSourceMaterialize, TimingCandidateCompile, TimingStartup, TimingTestBody, TimingTotal}
	phases := TimingPhases()
	if len(phases) != len(wantPhases) {
		return errors.New("remote CI timing phases are incomplete")
	}
	for index := range wantPhases {
		if phases[index] != wantPhases[index] {
			return errors.New("remote CI timing phase order drifted")
		}
	}
	if TimingAggregationRaw != "raw" || TimingAggregationIntervalUnion != "interval_union" || TimingAggregationCriticalPath != "critical_path" {
		return errors.New("remote CI timing aggregation vocabulary drifted")
	}
	if len(CompileGroupTimingPhases()) != 1 || CompileGroupTimingPhases()[0] != TimingTestBinaryCompile {
		return errors.New("remote CI compile group timing phase drifted")
	}
	return nil
}

// ValidateSourceTransportContract 锁定 accepted baseline、thin bundle、strict
// manifest 和 shard 创建前的唯一源码传输边界。
func ValidateSourceTransportContract() error {
	if !validSourceTransportAssets() || !validSourceTransportProtocol() {
		return errors.New("remote CI incremental source transport contract drifted")
	}
	return nil
}

// validSourceTransportAssets 锁定 accepted baseline 与候选传输资产的规范路径和名称。
func validSourceTransportAssets() bool {
	return SourceBaselineRepositoryPath == "/opt/super-dolphin-gate/source-baseline.git" &&
		SourceBundleName == "source.bundle" &&
		SourceManifestName == "source-manifest.json" &&
		SourceBundleRef == "refs/source/materialized" &&
		SourceBaseRef == "refs/source/base"
}

// validSourceTransportProtocol 锁定 thin bundle 协议、单 prerequisite 与上传屏障。
func validSourceTransportProtocol() bool {
	return SourceManifestSchemaVersion == 3 &&
		SourceTransportKind == "git-bundle-thin" &&
		SourceBundleHeaderVersion == "v2" &&
		SourceBundlePrerequisiteCount == 1 &&
		SourceAssetsUploadBarrier == "source-bundle-and-strict-manifest-before-lpt-shards/v1"
}

// ValidateConcurrencyPolicy 锁定三层正常执行并发且不允许仓库内 admission 边界。
func ValidateConcurrencyPolicy() error {
	if GitHookInvocationConcurrencyPolicy != "unbounded_by_repository" || RemoteCIJobConcurrencyPolicy != "unbounded_by_repository" || ShardConcurrencyPolicy != "unbounded_by_repository" {
		return errors.New("remote CI hook, job, and shard concurrency must be unbounded by repository policy")
	}
	if GitIndexLockBoundary != "git_worktree_index_consistency_not_ci_admission" {
		return errors.New("remote CI Git index lock boundary drifted")
	}
	return nil
}

// RequiredChecks 返回每次远程 CI 都必须有执行 miss 或严格复用 hit 通过证据的稳定检查目录。
func RequiredChecks() []RequiredCheck {
	return []RequiredCheck{RequiredCheckGate, RequiredCheckNormal, RequiredCheckE2E, RequiredCheckRace, RequiredCheckFrontend, RequiredCheckDependency}
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

// ValidateRetentionGenerations 拒绝偏离统一的三代 SQLite 历史窗口。
func ValidateRetentionGenerations() error {
	if RetentionGenerations != 3 {
		return errors.New("remote CI SQLite retention generation count drifted from the accepted contract")
	}
	if AcceptedGenerationColumn != "accepted_generation" {
		return errors.New("remote CI SQLite retention generation column drifted from the accepted contract")
	}
	if len(retentionRootBindings) != 7 {
		return fmt.Errorf("remote CI SQLite retention must own exactly seven historical roots, got %d", len(retentionRootBindings))
	}
	authorityTables := make(map[string]struct{}, len(sqlAuthorityBindings))
	for _, binding := range sqlAuthorityBindings {
		authorityTables[binding.Table] = struct{}{}
	}
	seenRoots := make(map[string]struct{}, len(retentionRootBindings))
	for _, binding := range retentionRootBindings {
		if strings.TrimSpace(binding.Table) == "" || binding.GenerationColumn != AcceptedGenerationColumn {
			return fmt.Errorf("remote CI SQLite retention root %+v is invalid", binding)
		}
		if _, exists := authorityTables[binding.Table]; !exists {
			return fmt.Errorf("remote CI SQLite retention root %q is not a SQL authority table", binding.Table)
		}
		if _, exists := seenRoots[binding.Table]; exists {
			return fmt.Errorf("remote CI SQLite retention root %q is duplicated", binding.Table)
		}
		seenRoots[binding.Table] = struct{}{}
	}
	return nil
}

// ValidateWorkloadPassEvidenceGeneration 校验 WorkloadPassEvidenceFreshnessPolicy 的代际部分。
// freshness 不使用 wall-clock TTL；调用方还必须核验权威状态、完整 identity 和 canonical receipt proof。
// future、零值和超过当前 accepted generation 前两代窗口的 evidence 必须视为 miss，而不能降级复用。
func ValidateWorkloadPassEvidenceGeneration(acceptedGeneration, evidenceGeneration uint64) error {
	if acceptedGeneration == 0 || evidenceGeneration == 0 {
		return errors.New("remote CI workload PASS evidence requires accepted and evidence generations")
	}
	if evidenceGeneration > acceptedGeneration {
		return errors.New("remote CI workload PASS evidence generation is in the future")
	}
	if acceptedGeneration-evidenceGeneration >= WorkloadPassEvidenceGenerationWindow {
		return fmt.Errorf("remote CI workload PASS evidence generation %d is outside accepted generation %d reuse window", evidenceGeneration, acceptedGeneration)
	}
	return nil
}

// ValidateNormalResources 拒绝 generation-one 内容检查使用 calibration 或未登记的资源规格。
func ValidateNormalResources(cpu, memoryGiB float64) error {
	if cpu <= 0 || memoryGiB <= 0 {
		return errors.New("remote CI normal resource CPU and memory are required")
	}
	if !((cpu == 2 && memoryGiB == 4) || (cpu == 4 && memoryGiB == 8) || (cpu == 8 && memoryGiB == 16)) {
		return errors.New("remote CI normal resources must use exactly 2 vCPU/4 GiB, 4 vCPU/8 GiB, or 8 vCPU/16 GiB")
	}
	return nil
}

// ValidateCalibrationResources 拒绝缺失或不可被回执精确绑定的固定规格。
func ValidateCalibrationResources(classID string, cpu, memoryGiB float64) error {
	if strings.TrimSpace(classID) == "" || classID != strings.TrimSpace(classID) || cpu <= 0 || memoryGiB <= 0 {
		return errors.New("remote CI calibration class, CPU, and memory are required")
	}
	if classID == "medium" {
		return errors.New("remote CI calibration resource class ID must remain independent from the normal medium class")
	}
	if cpu != CalibrationResourceCPU || memoryGiB != CalibrationResourceMemoryGiB {
		return errors.New("remote CI calibration resources must be exactly 4 vCPU and 8 GiB")
	}
	return nil
}

// ValidateECIMultiZoneScheduling 校验 ECI 请求只能使用两到十个不同 vSwitch 的随机多区调度。
func ValidateECIMultiZoneScheduling(strategy string, vSwitches []ECIVSwitch) error {
	if strategy != ECIMultiZoneScheduleStrategy {
		return errors.New("remote CI ECI schedule strategy must be VSwitchRandom")
	}
	if len(vSwitches) < ECIMinVSwitchCount || len(vSwitches) > ECIMaxVSwitchCount {
		return errors.New("remote CI requires two to ten vSwitch IDs for multi-zone scheduling")
	}
	zoneCount, err := validateECIVSwitchBindings(vSwitches)
	if err != nil {
		return err
	}
	if zoneCount < 2 {
		return errors.New("remote CI vSwitches must cover at least two zones")
	}
	return nil
}

// validateECIVSwitchBindings 校验 ECI vSwitch 集合的标识、可用区和唯一性。
func validateECIVSwitchBindings(vSwitches []ECIVSwitch) (int, error) {
	seenIDs := make(map[string]struct{}, len(vSwitches))
	seenZones := make(map[string]struct{}, len(vSwitches))
	for _, vSwitch := range vSwitches {
		if strings.TrimSpace(vSwitch.ID) != vSwitch.ID || !strings.HasPrefix(vSwitch.ID, "vsw-") || len(vSwitch.ID) <= len("vsw-") {
			return 0, errors.New("remote CI vSwitch ID is invalid")
		}
		if strings.TrimSpace(vSwitch.ZoneID) != vSwitch.ZoneID || !strings.HasPrefix(vSwitch.ZoneID, "cn-") {
			return 0, errors.New("remote CI vSwitch zone ID is invalid")
		}
		if _, duplicate := seenIDs[vSwitch.ID]; duplicate {
			return 0, errors.New("remote CI vSwitch IDs must be unique")
		}
		seenIDs[vSwitch.ID] = struct{}{}
		seenZones[vSwitch.ZoneID] = struct{}{}
	}
	return len(seenZones), nil
}

// CanonicalMarkdown 返回必须逐字嵌入 Accepted 文档的代码契约映射块。
func CanonicalMarkdown() string {
	var builder strings.Builder
	builder.WriteString("<!-- cicontract:begin -->\n| ID | 章节 | 代码约束 | 执行层 |\n")
	builder.WriteString("| --- | --- | --- | --- |\n")
	for _, requirement := range requirements {
		fmt.Fprintf(&builder, "| `%s` | §%d | %s | `%s` |\n", requirement.ID, requirement.Section, requirement.Summary, requirement.Enforcement)
	}
	builder.WriteString("<!-- cicontract:end -->")
	return builder.String()
}

// CanonicalRetentionMarkdown 返回 accepted 文档必须逐字嵌入的有界增长策略。
func CanonicalRetentionMarkdown() string {
	return fmt.Sprintf(`<!-- cicontract:retention:begin -->
%[1]s 是唯一 retention 常量 owner。duration samples、shard overhead aggregates、逐分片 overhead samples、catalog observations、runs、strict workload PASS evidence 与 calibration checkpoints 七个历史根都必须绑定已验证的 accepted baseline generation；每个根写事务必须先读取同一 SQLite authority 的 accepted singleton，拒绝零值、无 authority、无效 authority 与晚于当前 accepted generation 的伪造未来代。已启动的旧 accepted generation 运行仍可在完成时写入。七个根的 distinct generation 并集按数值确定全库唯一保留集合，任何根都不得保留该集合之外的数据。每一代可包含任意数量的 workload、sample、shard、timing、receipt 或 scenario，禁止用固定行数限制代码和测试增长。SQLite 只保留最新 %[2]d 个有数据的 generation；第四个有数据代首次成功写入时，必须在同一事务内淘汰最老一代全部历史根及其 cascade 子数据。

100 秒结构化 timing warning 只能沿同一 SQLite authority 的互斥生命周期流转：ci_live_timing_warnings 只暂存仍在运行的 provider StartTime 事实，run finalizer 必须在同一事务精确吸收到 ci_run_timing_warnings 并删除对应 live 行；不得预写或伪造 ci_runs 失败终态，也不得让 live 与 final 行同时存在。live 表不是第八个历史根或第二真相源，不参与七根 generation 并集；唯一 compactor 必须按已校验 accepted singleton 的 current/current-2 数值窗口保留 active 行并清理崩溃残留。

唯一 compactDurationLedgerAuthority 只能在既有成功写事务的 commit 前同步调用，禁止 timer、goroutine、后台 GC 或第二入口。generation 按数值排序，不能用行数、时间戳或插入顺序冒充；无法证明 generation 的旧行必须 fail-fast，不能默认绑定当前代。删除旧 run 依靠 FK cascade 同步删除 requester、shard/workload、execution、timing、warning 与 receipt；删除旧 checkpoint 同步删除任意数量 scenario；catalog 内容只有在不再被保留代 observation/run 引用时才能删除。SQLite authority 必须使用 FULL auto-vacuum，让每次成功淘汰在同一提交边界自动归还空页；无生产读取者且重复保存完整 run payload 的 raw observation event 表、索引、触发器和旧 schema migration 入口均已退役，禁止恢复。accepted baseline 是当前状态 singleton，duration meta/calibration 与 query meta 不是历史代，不参与淘汰。
<!-- cicontract:retention:end -->`, "`cicontract`", RetentionGenerations)
}

// CanonicalSchedulingMarkdown 返回 accepted 文档必须逐字嵌入的多可用区调度语义。
func CanonicalSchedulingMarkdown() string {
	return `<!-- cicontract:scheduling:begin -->
所有 normal、校准与首代内容检查 ECI container group 必须使用配置中 2 到 10 个不同 vSwitch，并显式绑定每个 vSwitch 的 zone_id；集合必须覆盖至少两个可用区。CreateContainerGroup 必须把全部 ID 以阿里云原生逗号列表传入 VSwitchId，并固定 ScheduleStrategy=VSwitchRandom。禁止单 vSwitch、多个同区 vSwitch、单区失败重试、串行 fallback 或用并发上限掩盖 NoStock；调度库存等待必须作为 provider eci_wait 记录。
<!-- cicontract:scheduling:end -->`
}

// CanonicalTimingMarkdown 返回 accepted 文档必须逐字嵌入的精确耗时语义。
func CanonicalTimingMarkdown() string {
	return `<!-- cicontract:timing:begin -->
每条 measured observation 必须在同一 SQLite authority 保存真实 started_at、completed_at 与 duration_ms；统一账本分辨率是 1ms，实际为正但不足 1ms 的 worker startup/test_body 阶段必须在唯一计时生产者处量化为 1ms，禁止向下截断成表示缺失的 0ms；仅当量化后的两个串行阶段超出真实 workload total 时，生产者才可将 workload completed_at 向上规范化到恰好覆盖二者，禁止改写 started_at 或扩大到额外整数毫秒。并发 selector 的 test_body 必须以 top-level run→pause→cont→terminal 事件中的 cont 时间为起点，run→pause 排队等待不得计入 workload test_body；测试进程仍保持并发，shard/run wall time 继续保留真实起止与关键路径。raw 和 critical_path 的 duration_ms 必须严格等于按该分辨率规范化后的区间长度。interval_union 的 duration_ms 必须是全部原始 workload 子区间的精确并集：重叠只计一次、空隙不计入，禁止用最早开始到最晚结束的 envelope 冒充活跃耗时。

workload 的 startup、test_body 与 total 是 raw；shard 的 startup 与 test_body 是 workload raw 区间的 interval_union，shard/run total 是 critical_path。每个 compile group 另以 test_binary_compile raw observation 记录一次，scope=compile_group，包含 group/artifact identity、真实起止、Go cache hit/miss/put、artifact digest/size/status；该时间不得写入 workload startup/test_body/total，也不得与 candidate Gate CLI 的 candidate_compile 合并或重复计数。每个 calibration-resource shard 的 orchestration overhead 必须按 v2 accounted-interval-union 计算：从 shard total interval 中扣除 workload total、shard eci_wait/source_materialize/candidate_compile 以及 compile-group test_binary_compile 的全部 measured 区间精确并集，重叠只扣一次、间隙保留为真实编排开销；禁止用最早 workload 到最晚 workload 的 envelope 把上述已单独计量阶段重新算作 overhead。aggregate 使用 nearest-rank P95，并把 accounted duration/count、workload envelope、完整样本事实、accepted generation、snapshot 与 4C/8GiB 资源身份写入同一 SQLite authority；缺少任一必需 shard 阶段、区间越过 shard total、重复 workload/compile-group 身份或旧 v1 policy 必须 fail-fast。eci_wait 只能使用 ECI provider 返回的 CreationTime 到 materializer CurrentState.StartTime；shard total 终点必须取同一终态响应中 container-group SucceededTime/FailedTime 与唯一 worker CurrentState.FinishTime 的较晚者，两者都属于 provider lifecycle evidence。阿里云 ECI 已返回终态但 CreationTime、materializer CurrentState.StartTime、SucceededTime/FailedTime 或唯一 worker CurrentState.FinishTime 尚未同步时，只允许沿同一 Describe 路径按 PollInterval 对该分片有界重读最多 3 次；重读期间不得伪造时间、消费报告、移出 pending、取消兄弟分片或跳过清理，窗口耗尽后缺失任一项必须 fail-fast。禁止用 worker 日志、report 端点、本地请求或轮询时间替换 provider 终态；任一真实子阶段仍越过该 provider envelope 时保持 provisional NOT_VERIFIED。本地请求、轮询或日志时间不得写成权威耗时。所有 cache evidence 与阶段观测绑定，人类账本只能读取同一事务已提交的 SQLite observations。compile timing history 只能按 PackageTarget、SemanticKey、Platform、RunnerIdentityDigest、ToolchainDigest、ExecutionMode 与 ResourceClassID/CPU/Memory 完整 identity 查询；只允许最近三个 accepted generation 中 authoritative、passed、cleanup-complete、measured/raw 的真实 compile-group observation，source tree、shared input 与 artifact digest 不得跨 identity 混用。normal 无历史固定 2C/4GiB，owner fixed-point 发生在 PlannedWorkload 创建前，shared compile cost 每组只计一次且不写入 selector body；calibration 固定 4C/8GiB。
<!-- cicontract:timing:end -->`
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
	if CacheMaterialSchemaID != "remote-ci-cache-material/v1" || CacheMaterialAuthority != "non_authoritative_material" {
		return errors.New("remote CI cache material authority contract drifted")
	}
	if RemoteShardRequestMaxBytes != 1<<20 {
		return errors.New("remote CI shard request byte limit must equal 1 MiB")
	}
	if ImageCacheRefreshInterval != 24*time.Hour {
		return errors.New("remote CI ImageCache refresh interval must equal 24 hours")
	}
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
	seenSQLDomains := make(map[SQLDomain]struct{}, len(sqlAuthorityBindings))
	seenSQLTables := make(map[string]struct{}, len(sqlAuthorityBindings))
	for _, binding := range sqlAuthorityBindings {
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
