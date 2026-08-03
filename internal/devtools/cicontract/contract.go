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

const (
	// ID 是文档与代码共同使用的远程 CI 契约身份。
	ID = "remote-ci-eci-imagecache/v1"
	// DocumentPath 是 Accepted 文档契约的仓库相对路径。
	DocumentPath = "docs/契约/remote-ci-eci-imagecache-contract.md"
	// ExecutionPathID 标识唯一正常 CI 执行路径。
	ExecutionPathID = "accepted-sqlite-imagecache-snapshot-eci-shards/v1"
	// GenerationOneReceiptImportPathID 标识唯一允许写入 accepted singleton 的外部首代严格回执导入。
	// 仓库内 normal CI 只消费 accepted snapshot；不得实现 successor refresh executor。
	GenerationOneReceiptImportPathID = "external-eci-imagecache-generation-one-strict-receipt-import/v1"
	// SQLAuthorityID 标识 accepted state、刷新、规划、回执和校准共用的唯一数据源。
	SQLAuthorityID = "duration-ledger-sqlite/v1"

	// TargetPlatform 是远程 CI 和基准刷新的唯一生产目标平台。
	TargetPlatform = "linux/amd64"
	// GoToolchainVersion 是基准镜像和候选编译唯一接受的 Go 发行版。
	GoToolchainVersion = "go1.26.5"

	// AcceptedBaselineTable 是已接受基准代的唯一 SQLite 权威表。
	AcceptedBaselineTable = "ci_remote_baseline_state"
	// DurationSamplesTable 是 workload 历史耗时与 LPT 规划样本的唯一 SQLite 权威表。
	DurationSamplesTable = "duration_samples"
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
	// ShardTargetDuration 是拆分、优化与告警目标，不是 worker 硬超时。
	ShardTargetDuration = 100 * time.Second
)

// TimingPhase 是权威回执和人类账本必须显式表达的耗时阶段。
type TimingPhase string

const (
	TimingECIWait           TimingPhase = "eci_wait"
	TimingSourceMaterialize TimingPhase = "source_materialize"
	TimingCandidateCompile  TimingPhase = "candidate_compile"
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
	TimingScopeRun      TimingScope = "run"
	TimingScopeShard    TimingScope = "shard"
	TimingScopeWorkload TimingScope = "workload"
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

// SQLDomain 是必须由同一个 duration-ledger SQLite authority 持久化的事实域。
type SQLDomain string

const (
	SQLDomainAcceptedBaseline      SQLDomain = "accepted_baseline"
	SQLDomainDurationHistory       SQLDomain = "duration_history"
	SQLDomainRemoteRun             SQLDomain = "remote_run"
	SQLDomainRemoteShard           SQLDomain = "remote_shard"
	SQLDomainWorkloadExecution     SQLDomain = "workload_execution"
	SQLDomainRunWarning            SQLDomain = "run_warning"
	SQLDomainLiveTimingWarning     SQLDomain = "live_timing_warning"
	SQLDomainRunTimingWarning      SQLDomain = "run_timing_warning"
	SQLDomainCalibrationCheckpoint SQLDomain = "calibration_checkpoint"
	SQLDomainCheckReceipt          SQLDomain = "check_receipt"
	SQLDomainTimingObservation     SQLDomain = "timing_observation"
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
	{ID: "1.2", Section: 1, Summary: "normal CI 只读 accepted ImageCache explicit snapshot；仓库内不存在 successor refresh executor", Enforcement: "runtime call graph + archtest"},
	{ID: "1.3", Section: 1, Summary: "hook、job 与动态 shards 无仓库并发上限；index.lock 仅保护 Git index", Enforcement: "cicontract concurrency policy + archtest"},
	{ID: "1.6", Section: 1, Summary: "100 秒只触发一次目标超限告警；不得取消、中断、kill 或标失败", Enforcement: "planner + non-terminating timing warning"},
	{ID: "1.7", Section: 1, Summary: "校准运行使用固定且被回执绑定的 CPU 与内存规格", Enforcement: "request + receipt + SQLite"},
	{ID: "1.8", Section: 1, Summary: "运行以真实起止和 duration_ms 分别记录物化、编译、启动、测试、总耗时、缓存与等待；并发聚合使用精确 interval union", Enforcement: "receipt + SQLite timing ledger"},
	{ID: "1.9", Section: 1, Summary: "normal all-hit 不创建 workload CI ECI、workload shard、workload OSS/temp 或 calibration，且不执行测试；部分命中只执行 miss，calibration 永不复用", Enforcement: "required-check catalogue + strict reuse receipt validation + archtest"},
	{ID: "2.1", Section: 2, Summary: "accepted baseline、duration history、run/shard receipt 与 calibration checkpoint 只读写同一 SQLite authority", Enforcement: "SQLite schema + store"},
	{ID: "2.2", Section: 2, Summary: "运行时缓存只由 accepted ImageCache ID 与 snapshot ID 选择", Enforcement: "state validation + ECI request"},
	{ID: "2.3", Section: 2, Summary: "候选源码和 Gate 编译分别绑定 exact Git identity 与真实传递编译闭包", Enforcement: "materializer + receipt"},
	{ID: "2.4", Section: 2, Summary: "严格 JSON 仅作协议编码，OSS 仅作内容寻址传输，二者均非权威", Enforcement: "strict decoders + archtest"},
	{ID: "2.5", Section: 2, Summary: "accepted state、check receipts、timing 与 warnings 只写同一个 SQLite authority", Enforcement: "SQLite schema + receipt store"},
	{ID: "2.6", Section: 2, Summary: "generation-one 只能导入外部严格 receipt；normal CI 和仓库内代码不得创建或晋级 ImageCache", Enforcement: "strict receipt boundary + archtest"},
	{ID: "2.7", Section: 2, Summary: "无 token 请求只返回 --agent-token=issue 与 env=issue 申请方式和实际 token 的 flag/env 使用方式；仅单一显式 issue 才签发 raw token，前两阶段均不执行 CI/ECI；git hook 无状态且只继承/验证实际 env token，跨 SQLite/OSS/ECI/log/tag/checkpoint/receipt 只传 sha256 digest", Enforcement: "agent-token contract + field guard + archtest"},
	{ID: "2.8", Section: 2, Summary: "首次读取缺失的 SQLite authority 只原子初始化 current schema 与查询索引；不得生成 accepted baseline，缺少 generation-one 仍 fail-fast", Enforcement: "SQLite initializer + baseline store test"},
	{ID: "3.1", Section: 3, Summary: "正常 CI 唯一路径为 accepted SQLite 到 LPT 到无上限并发 ECI shards", Enforcement: "runtime call graph + archtest"},
	{ID: "3.2", Section: 3, Summary: "正常 shard 显式绑定 accepted snapshot，禁止 AutoMatch 选择", Enforcement: "ECI request validation"},
	{ID: "3.3", Section: 3, Summary: "候选 Gate 在 exact candidate tree 内增量编译，cache hit 不跳过身份验证", Enforcement: "materializer + receipt"},
	{ID: "3.4", Section: 3, Summary: "工具链、依赖、浏览器与缓存只直读 accepted ImageCache 镜像层；禁止 expanded-data、DataCache、缓存卷、subPath 或逐 shard 复制展开，只有 source/work/temp 可使用私有 EmptyDir", Enforcement: "ECI request shape + image closure + archtest"},
	{ID: "4.1", Section: 4, Summary: "唯一写 accepted singleton 的路径是 external generation-one strict receipt import；它绑定 generation=1、state SHA、Ready snapshot、immutable image、源码/工具链/策略/seed 与固定规格", Enforcement: "receipt validation + SQLite INSERT + archtest"},
	{ID: "4.2", Section: 4, Summary: "导入只允许空 singleton 原子 INSERT；重复、非空、缺字段或非首代 receipt 必须 fail-fast", Enforcement: "strict import boundary"},
	{ID: "4.3", Section: 4, Summary: "仓库内禁止 refresh command、BuildKit publish、output_repository、CreateImageCache writer、candidate reservation 与 CAS promotion", Enforcement: "deletion + archtest"},
	{ID: "5.1", Section: 5, Summary: "shard 数只受可分片原子 workload 数量限制", Enforcement: "LPT planner + archtest"}, {ID: "5.2", Section: 5, Summary: "云配额与 API 限流必须显式失败，不得静默降并发或转本地", Enforcement: "runtime + archtest"},
	{ID: "6.1", Section: 6, Summary: "固定校准规格贯穿 RunInput、ShardRequest、receipt、SQLite 与 checkpoint identity", Enforcement: "field guard + store"},
	{ID: "6.2", Section: 6, Summary: "校准规格漂移或缺失必须 fail-fast，固定规格不限制 shard 并发", Enforcement: "validation + archtest"},
	{ID: "7.1", Section: 7, Summary: "shard 与 workload 账本显式表达六个耗时阶段及其作用域", Enforcement: "receipt + ledger renderer"},
	{ID: "7.2", Section: 7, Summary: "账本证明 workload 实际 executed miss 或严格 reused hit，并记录 canonical reuse proof 与仅用于加速的 Go cache、前端 seed/Vite cache 证据", Enforcement: "receipt + ledger renderer"},
	{ID: "7.3", Section: 7, Summary: "不适用阶段写 not_applicable，缺失应有观测拒绝 authoritative receipt", Enforcement: "receipt validation"},
	{ID: "7.4", Section: 7, Summary: "100 秒告警固定 warn_and_continue；不得 cancel、kill 或 fail shard", Enforcement: "warning-action validation + archtest"},
	{ID: "8.1", Section: 8, Summary: "DataCache、旧 bundle、本地 Docker、ACR、JSON truth、第二 executor 与隐式 fallback 禁止存在", Enforcement: "deletion + archtest"},
	{ID: "8.2", Section: 8, Summary: "固定 shard 数、并发上限、自动全量重建及任何仓库内 successor refresh executor 禁止存在", Enforcement: "deletion + archtest"},
	{ID: "9.1", Section: 9, Summary: "变更必须闭合 LSP、字段链、状态矩阵、守卫和变更面测试", Enforcement: "repository gates"},
	{ID: "9.2", Section: 9, Summary: "远程验收绑定同一 candidate tree、generation、snapshot、资源与完整账本", Enforcement: "authoritative receipt"}, {ID: "9.3", Section: 9, Summary: "非 authoritative 结果保持 NOT_VERIFIED，warm CI 超过 100 秒继续优化", Enforcement: "remote acceptance"},
	{ID: "7.5", Section: 7, Summary: "五个 SQLite 历史根写前证明 generation 已被接受，并共享全库唯一保留集合；每代行数不限，只保留最新 3 个有数据代，第四代写入与第一代全族淘汰同事务", Enforcement: "accepted-generation proof + single write-transaction compactor + archtest"},
}

var forbiddenLegacyCapabilities = [...]string{
	"DataCache/Anchor/Delta/direct-cache/zstd bundle",
	"local Docker/Docker Desktop/buildx/localci",
	"ACR-specific auth/role/registry access/repository",
	"JSON baseline or ledger truth source and compatibility dual-read",
	"candidate CLI artifact builder/candidate test-binary builder/second executor",
	"workload PASS result cache、JSON/OSS/.pass/ci_workload_fingerprints 旧 reuse schema 或未绑定 canonical proof 的 test skip",
	"spot or remote-to-local implicit fallback",
	"global hook lock/active-job lock/admission cap/shared raw token/fixed shard count or coordinator concurrency cap",
	"automatic full rebuild without an accepted Ready ImageCache",
	"repository successor refresh executor, BuildKit publish, output_repository, CreateImageCache writer, candidate reservation, or CAS promotion",
	"expanded-data/DataCache/cache volume/subPath mount or per-shard dependency/cache extraction",
}

var sqlAuthorityBindings = [...]SQLAuthorityBinding{
	{Domain: SQLDomainAcceptedBaseline, Table: AcceptedBaselineTable},
	{Domain: SQLDomainDurationHistory, Table: DurationSamplesTable},
	{Domain: SQLDomainRemoteRun, Table: RemoteRunsTable},
	{Domain: SQLDomainRemoteShard, Table: RemoteShardsTable},
	{Domain: SQLDomainWorkloadExecution, Table: WorkloadExecutionsTable},
	{Domain: SQLDomainRunWarning, Table: RunWarningsTable},
	{Domain: SQLDomainLiveTimingWarning, Table: LiveTimingWarningsTable},
	{Domain: SQLDomainRunTimingWarning, Table: RunTimingWarningsTable},
	{Domain: SQLDomainCalibrationCheckpoint, Table: CalibrationCheckpointsTable},
	{Domain: SQLDomainCheckReceipt, Table: CheckReceiptsTable},
	{Domain: SQLDomainTimingObservation, Table: TimingObservationsTable},
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
	{Table: CatalogObservationsTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: RemoteRunsTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: WorkloadPassEvidenceTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: CalibrationCheckpointsTable, GenerationColumn: AcceptedGenerationColumn},
}

// Requirements 返回文档设计目标的只读副本。
func Requirements() []Requirement {
	result := make([]Requirement, len(requirements))
	copy(result, requirements[:])
	return result
}

// ForbiddenLegacyCapabilities 返回必须由删除和架构守卫共同拒绝的旧能力。
func ForbiddenLegacyCapabilities() []string {
	result := make([]string, len(forbiddenLegacyCapabilities))
	copy(result, forbiddenLegacyCapabilities[:])
	return result
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
	return nil
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
	required := RequiredChecks()
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

// ValidateTargetPlatform 拒绝非 linux/amd64 的远程 CI 或刷新目标。
func ValidateTargetPlatform(platform string) error {
	if platform != TargetPlatform {
		return fmt.Errorf("remote CI platform must equal %q", TargetPlatform)
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
	if len(retentionRootBindings) != 5 {
		return fmt.Errorf("remote CI SQLite retention must own exactly five historical roots, got %d", len(retentionRootBindings))
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

// ValidateCalibrationResources 拒绝缺失或不可被回执精确绑定的固定规格。
func ValidateCalibrationResources(classID string, cpu, memoryGiB float64) error {
	if strings.TrimSpace(classID) == "" || classID != strings.TrimSpace(classID) || cpu <= 0 || memoryGiB <= 0 {
		return errors.New("remote CI calibration class, CPU, and memory are required")
	}
	return nil
}

// CanonicalMarkdown 返回必须逐字嵌入 Accepted 文档的代码契约映射块。
func CanonicalMarkdown() string {
	var builder strings.Builder
	builder.WriteString("<!-- cicontract:begin -->\n")
	builder.WriteString("| ID | 章节 | 代码约束 | 执行层 |\n")
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
%[1]s 是唯一 retention 常量 owner。duration samples、catalog observations、runs、strict workload PASS evidence 与 calibration checkpoints 五个历史根都必须绑定已验证的 accepted baseline generation；每个根写事务必须先读取同一 SQLite authority 的 accepted singleton，拒绝零值、无 authority、无效 authority 与晚于当前 accepted generation 的伪造未来代。refresh 只能逐代晋级，因此已启动旧代运行仍可在完成时写入。五个根的 distinct generation 并集按数值确定全库唯一保留集合，任何根都不得保留该集合之外的数据。每一代可包含任意数量的 workload、sample、shard、timing、receipt 或 scenario，禁止用固定行数限制代码和测试增长。SQLite 只保留最新 %[2]d 个有数据的 generation；第四个有数据代首次成功写入时，必须在同一事务内淘汰最老一代全部历史根及其 cascade 子数据。

100 秒结构化 timing warning 只能沿同一 SQLite authority 的互斥生命周期流转：ci_live_timing_warnings 只暂存仍在运行的 provider StartTime 事实，run finalizer 必须在同一事务精确吸收到 ci_run_timing_warnings 并删除对应 live 行；不得预写或伪造 ci_runs 失败终态，也不得让 live 与 final 行同时存在。live 表不是第六个历史根或第二真相源，不参与五根 generation 并集；唯一 compactor 必须按已校验 accepted singleton 的 current/current-2 数值窗口保留 active 行并清理崩溃残留。

唯一 compactDurationLedgerAuthority 只能在既有成功写事务的 commit 前同步调用，禁止 timer、goroutine、后台 GC 或第二入口。generation 按数值排序，不能用行数、时间戳或插入顺序冒充；无法证明 generation 的旧行必须 fail-fast 或经显式迁移，不能默认绑定当前代。删除旧 run 依靠 FK cascade 同步删除 requester、shard/workload、execution、timing、warning、delta 与 receipt；删除旧 checkpoint 同步删除任意数量 scenario；catalog 内容只有在不再被保留代 observation/run 引用时才能删除。accepted baseline 与 refresh lease 是当前状态 singleton，duration meta/calibration、query meta 和源码枚举的 schema migration registry 不是历史代，不参与淘汰。
<!-- cicontract:retention:end -->`, "`cicontract`", RetentionGenerations)
}

// CanonicalTimingMarkdown 返回 accepted 文档必须逐字嵌入的精确耗时语义。
func CanonicalTimingMarkdown() string {
	return `<!-- cicontract:timing:begin -->
每条 measured observation 必须在同一 SQLite authority 保存真实 started_at、completed_at 与 duration_ms；raw 和 critical_path 的 duration_ms 必须严格等于真实区间长度。interval_union 的 duration_ms 必须是全部原始 workload 子区间的精确并集：重叠只计一次、空隙不计入，禁止用最早开始到最晚结束的 envelope 冒充活跃耗时。

workload 的 startup、test_body 与 total 是 raw；shard 的 startup 与 test_body 是 workload raw 区间的 interval_union，shard/run total 是 critical_path。eci_wait 只能使用 ECI provider 返回的 CreationTime 到 materializer CurrentState.StartTime，shard total 终点只能使用 provider terminal time；本地请求、轮询或日志时间不得写成权威耗时。所有 cache evidence 与阶段观测绑定，人类账本只能读取同一事务已提交的 SQLite observations。
<!-- cicontract:timing:end -->`
}

// Validate 校验代码契约自身不存在重复 ID、章节缺口或无效不变量。
func Validate() error {
	for _, validate := range []func() error{validateContractIdentity, validateContractConstants, validateContractObservations, validateRequirements, validateSQLAuthorityBindings} {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateContractIdentity 校验代码契约的稳定身份与唯一 owner 路径均已定义。
func validateContractIdentity() error {
	values := []string{ID, DocumentPath, ExecutionPathID, GenerationOneReceiptImportPathID, SQLAuthorityID}
	if slices.Contains(values, "") {
		return errors.New("remote CI contract identity is incomplete")
	}
	return nil
}

// validateContractConstants 校验固定平台、时长、保留和并发常量。
func validateContractConstants() error {
	for _, validate := range []func() error{
		func() error { return ValidateTargetPlatform(TargetPlatform) },
		func() error { return ValidateGoToolchainVersion(GoToolchainVersion) },
		func() error { return ValidateShardTargetDuration(ShardTargetDuration) },
		ValidateRetentionGenerations,
		ValidateTimingContract,
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
