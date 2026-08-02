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
	"net/url"
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
	// RefreshPathID 标识唯一后台增量刷新路径。
	RefreshPathID = "sqlite-lease-successor-imagecache-cas/v1"
	// SQLAuthorityID 标识 accepted state、刷新、规划、回执和校准共用的唯一数据源。
	SQLAuthorityID = "duration-ledger-sqlite/v1"

	// TargetPlatform 是远程 CI 和基准刷新的唯一生产目标平台。
	TargetPlatform = "linux/amd64"
	// GoToolchainVersion 是基准镜像和候选编译唯一接受的 Go 发行版。
	GoToolchainVersion = "go1.26.5"

	// AcceptedBaselineTable 是已接受基准代的唯一 SQLite 权威表。
	AcceptedBaselineTable = "ci_remote_baseline_state"
	// RefreshLeaseTable 是刷新 owner、attempt 和候选生命周期的唯一 SQLite 权威表。
	RefreshLeaseTable = "ci_remote_baseline_refresh_lease"
	// DurationSamplesTable 是 workload 历史耗时与 LPT 规划样本的唯一 SQLite 权威表。
	DurationSamplesTable = "duration_samples"
	// RemoteRunsTable 是远程 CI run 回执的唯一 SQLite 权威表。
	RemoteRunsTable = "ci_runs"
	// RemoteShardsTable 是远程 CI shard 回执的唯一 SQLite 权威表。
	RemoteShardsTable = "ci_shards"
	// WorkloadExecutionsTable 是 workload 执行与缓存事实的唯一 SQLite 权威表。
	WorkloadExecutionsTable = "ci_workload_executions"
	// RunWarningsTable 是 100 秒目标超限告警的唯一 SQLite 权威表。
	RunWarningsTable = "ci_run_warnings"
	// CalibrationCheckpointsTable 是固定规格校准 checkpoint 的唯一 SQLite 权威表。
	CalibrationCheckpointsTable = "remote_ci_calibration_checkpoints"
	// RefreshDeltasTable 是 accepted snapshot 相对 delta identity 的唯一 SQLite 权威表。
	RefreshDeltasTable = "ci_remote_refresh_deltas"
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
	// RunRequestersTable 是远程 CI 请求者投影的唯一 SQLite 权威表。
	RunRequestersTable = "ci_run_requesters"
	// ShardWorkloadsTable 是 shard 到 workload 绑定的唯一 SQLite 权威表。
	ShardWorkloadsTable = "ci_shard_workloads"
	// GateExecutionsTable 是 Gate 执行回执的唯一 SQLite 权威表。
	GateExecutionsTable = "ci_gate_executions"
	// CalibrationCheckpointScenariosTable 是 checkpoint scenario 的唯一 SQLite 权威表。
	CalibrationCheckpointScenariosTable = "remote_ci_calibration_checkpoint_scenarios"

	// ShardConcurrencyPolicy 明确仓库不设置 shard 数量或 coordinator 并发上限。
	ShardConcurrencyPolicy = "unbounded_by_repository"
	// RefreshTransferModeID 规定刷新源码只能相对 accepted snapshot 传输增量。
	RefreshTransferModeID = "accepted_snapshot_delta_only/v1"
	// SourceSnapshotRootPath 是 accepted source snapshot 在 worker 内的唯一根目录。
	SourceSnapshotRootPath = "/opt/super-dolphin-gate/source-snapshot/root"
	// SourceSnapshotManifestPath 是 accepted source snapshot 的唯一 manifest 路径。
	SourceSnapshotManifestPath = "/opt/super-dolphin-gate/source-snapshot/manifest.json"
	// RefreshCheckLogPrefix 是 refresh worker 输出非权威构建检查日志的唯一前缀。
	RefreshCheckLogPrefix = "REMOTE_CI_REFRESH_CHECK_PASS="

	// RetentionGenerations 只约束 accepted baseline 代数；每代内部行数不受限制。
	RetentionGenerations = 3
	// AcceptedGenerationColumn 是所有 SQLite 历史根共享的 generation 列名。
	AcceptedGenerationColumn = "accepted_generation"
)

const (
	// ShardTargetDuration 是拆分、优化与告警目标，不是 worker 硬超时。
	ShardTargetDuration = 100 * time.Second
	// RefreshMinimumInterval 限制每两小时最多开始一个新 attempt；过期接管不新建 attempt。
	RefreshMinimumInterval = 2 * time.Hour
)

// RefreshPhase 是后台增量刷新从抢占到清理的完整生命周期。
type RefreshPhase string

const (
	RefreshIdle           RefreshPhase = "idle"
	RefreshClaimed        RefreshPhase = "claimed"
	RefreshBuilding       RefreshPhase = "building"
	RefreshCachePreparing RefreshPhase = "cache_preparing"
	RefreshReadyValidated RefreshPhase = "ready_validated"
	RefreshPromoted       RefreshPhase = "promoted"
	RefreshRetiring       RefreshPhase = "retiring"
	RefreshCleanupPending RefreshPhase = "cleanup_pending"
	RefreshUnchanged      RefreshPhase = "unchanged"
	RefreshFailed         RefreshPhase = "failed"
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

// RefreshTransferMode 规定后台刷新源码的唯一传输模式。
type RefreshTransferMode string

const (
	RefreshTransferAcceptedSnapshotDelta RefreshTransferMode = "accepted_snapshot_delta"
)

// TimingWarningAction 规定 100 秒目标超限后的唯一处理动作。
type TimingWarningAction string

const (
	TimingWarningWarnAndContinue TimingWarningAction = "warn_and_continue"
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
type CheckObservation struct {
	Check              RequiredCheck `json:"check"`
	Executed           bool          `json:"executed"`
	Passed             bool          `json:"passed"`
	SourceTree         string        `json:"source_tree"`
	AcceptedSnapshotID string        `json:"accepted_snapshot_id"`
	PlanDigest         string        `json:"plan_digest"`
	StartedAtUnixMS    int64         `json:"started_at_unix_ms"`
	CompletedAtUnixMS  int64         `json:"completed_at_unix_ms"`
	DurationMS         int64         `json:"duration_ms"`
	ReceiptSHA256      string        `json:"receipt_sha256"`
}

// RefreshCheck 是后台 refresh 为重新编译、cache seed 和依赖完整性生成的检查目录。
// 它们明确不是 normal CI 的测试通过回执。
type RefreshCheck string

const (
	RefreshCheckGateBuild     RefreshCheck = "gate_build"
	RefreshCheckNormalCompile RefreshCheck = "normal_compile"
	RefreshCheckE2ECompile    RefreshCheck = "e2e_compile"
	RefreshCheckRaceCompile   RefreshCheck = "race_compile"
	RefreshCheckFrontendBuild RefreshCheck = "frontend_build"
	RefreshCheckDependency    RefreshCheck = "dependency"
)

// RefreshCheckObservation 记录 refresh 实际执行的非测试操作，并明确声明 test_body 不适用。
type RefreshCheckObservation struct {
	Check                         RefreshCheck `json:"check"`
	Executed                      bool         `json:"executed"`
	Passed                        bool         `json:"passed"`
	SourceTree                    string       `json:"source_tree"`
	AcceptedSnapshotID            string       `json:"accepted_snapshot_id"`
	PlanDigest                    string       `json:"plan_digest"`
	StartedAtUnixMS               int64        `json:"started_at_unix_ms"`
	CompletedAtUnixMS             int64        `json:"completed_at_unix_ms"`
	DurationMS                    int64        `json:"duration_ms"`
	CandidateCompileMS            int64        `json:"candidate_compile_ms"`
	CandidateCompileNotApplicable bool         `json:"candidate_compile_not_applicable"`
	TestBodyNotApplicable         bool         `json:"test_body_not_applicable"`
	ReceiptSHA256                 string       `json:"receipt_sha256"`
}

// SQLDomain 是必须由同一个 duration-ledger SQLite authority 持久化的事实域。
type SQLDomain string

const (
	SQLDomainAcceptedBaseline      SQLDomain = "accepted_baseline"
	SQLDomainRefreshLease          SQLDomain = "refresh_lease"
	SQLDomainDurationHistory       SQLDomain = "duration_history"
	SQLDomainRemoteRun             SQLDomain = "remote_run"
	SQLDomainRemoteShard           SQLDomain = "remote_shard"
	SQLDomainWorkloadExecution     SQLDomain = "workload_execution"
	SQLDomainRunWarning            SQLDomain = "run_warning"
	SQLDomainCalibrationCheckpoint SQLDomain = "calibration_checkpoint"
	SQLDomainRefreshDelta          SQLDomain = "refresh_delta"
	SQLDomainCheckReceipt          SQLDomain = "check_receipt"
	SQLDomainTimingObservation     SQLDomain = "timing_observation"
	SQLDomainWorkloadCatalog       SQLDomain = "workload_catalog"
	SQLDomainCatalogObservation    SQLDomain = "catalog_observation"
	SQLDomainCatalogWorkload       SQLDomain = "catalog_workload"
	SQLDomainRunRequester          SQLDomain = "run_requester"
	SQLDomainShardWorkload         SQLDomain = "shard_workload"
	SQLDomainGateExecution         SQLDomain = "gate_execution"
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
	{ID: "1.2", Section: 1, Summary: "正常 CI 只读 accepted generation 且不依赖 refresh 发布配置；后台刷新缺配置或执行期间仍使用旧代", Enforcement: "runtime + SQLite"},
	{ID: "1.3", Section: 1, Summary: "跨用户刷新只允许一个 SQLite lease owner", Enforcement: "SQLite transaction"},
	{ID: "1.4", Section: 1, Summary: "每两小时最多开始一个新 refresh attempt，过期接管沿用同一 attempt", Enforcement: "SQLite lease"},
	{ID: "1.5", Section: 1, Summary: "前后端 normal/e2e/race 按历史耗时动态分片且无仓库并发上限", Enforcement: "planner + archtest"},
	{ID: "1.6", Section: 1, Summary: "100 秒只触发一次目标超限告警；不得取消、中断、kill 或标失败", Enforcement: "planner + non-terminating timing warning"},
	{ID: "1.7", Section: 1, Summary: "校准运行使用固定且被回执绑定的 CPU 与内存规格", Enforcement: "request + receipt + SQLite"},
	{ID: "1.8", Section: 1, Summary: "运行以真实起止和 duration_ms 分别记录物化、编译、启动、测试、总耗时、缓存与等待；并发聚合使用精确 interval union", Enforcement: "receipt + SQLite timing ledger"},
	{ID: "1.9", Section: 1, Summary: "正常 CI 每次全量执行 Gate、normal、e2e、race、frontend 与 dependency checks；refresh 只回执真实 build/cache-seed/dependency 操作且不得声称 tests PASS", Enforcement: "required-check catalogue + refresh receipt validation"},
	{ID: "2.1", Section: 2, Summary: "accepted baseline、refresh lease/attempt、duration history、run/shard receipt 与 calibration checkpoint 只读写同一 SQLite authority", Enforcement: "SQLite schema + store"},
	{ID: "2.2", Section: 2, Summary: "运行时缓存只由 accepted ImageCache ID 与 snapshot ID 选择", Enforcement: "state validation + ECI request"},
	{ID: "2.3", Section: 2, Summary: "候选源码和 Gate 编译分别绑定 exact Git identity 与真实传递编译闭包", Enforcement: "materializer + receipt"},
	{ID: "2.4", Section: 2, Summary: "严格 JSON 仅作协议编码，OSS 仅作内容寻址传输，二者均非权威", Enforcement: "strict decoders + archtest"},
	{ID: "2.5", Section: 2, Summary: "accepted、lease、delta identity、check receipts、timing 与 warnings 只写同一个 SQLite authority", Enforcement: "SQLite schema + receipt store"},
	{ID: "2.6", Section: 2, Summary: "source snapshot root、manifest 与 refresh-only 人类日志前缀只由 cicontract 定义，bind/core/embed/worker 只消费该 owner", Enforcement: "canonical constants + archtest"},
	{ID: "3.1", Section: 3, Summary: "正常 CI 唯一路径为 accepted SQLite 到 LPT 到无上限并发 ECI shards", Enforcement: "runtime call graph + archtest"},
	{ID: "3.2", Section: 3, Summary: "正常 shard 显式绑定 accepted snapshot，禁止 AutoMatch 选择", Enforcement: "ECI request validation"},
	{ID: "3.3", Section: 3, Summary: "候选 Gate 在 exact candidate tree 内增量编译，cache hit 不跳过身份验证", Enforcement: "materializer + receipt"},
	{ID: "4.1", Section: 4, Summary: "唯一后台链为 TryClaim、successor、CreateImageCache、Ready、双 CAS 与延迟清理", Enforcement: "runtime + SQLite state machine"},
	{ID: "4.2", Section: 4, Summary: "刷新必须 heartbeat；失去 token、lease 或 accepted identity 时禁止晋级", Enforcement: "SQLite CAS"},
	{ID: "4.3", Section: 4, Summary: "已有 accepted generation 时只允许增量刷新，缺失 accepted 时禁止自动全量 bootstrap", Enforcement: "refresh validation"},
	{ID: "4.4", Section: 4, Summary: "ImageCache 是运行缓存权威，非 ACR OCI digest 仅是 CreateImageCache 内容输入", Enforcement: "builder protocol + config validation"},
	{ID: "4.5", Section: 4, Summary: "晋级前失败保留旧代；晋级后旧代清理失败进入 cleanup pending", Enforcement: "SQLite state machine"},
	{ID: "4.6", Section: 4, Summary: "身份未变化时不创建 ImageCache、不增加 generation，只把检查结果记为 unchanged", Enforcement: "SQLite state machine"},
	{ID: "4.7", Section: 4, Summary: "refresh source 只传相对 accepted generation/source snapshot 的 delta；缺 snapshot 必须 fail-fast", Enforcement: "transfer-mode validation + archtest"},
	{ID: "4.8", Section: 4, Summary: "云端必须从 accepted snapshot 加 delta 重建并验证完整目标 Git tree 与编译 closure", Enforcement: "rebuild receipt validation"},
	{ID: "4.9", Section: 4, Summary: "检查 marker 只用于人类日志；晋级必须验证绑定 tree、snapshot、plan、执行、耗时与摘要的结构化 observation", Enforcement: "builder receipt validation + archtest"},
	{ID: "5.1", Section: 5, Summary: "shard 数只受可分片原子 workload 数量限制", Enforcement: "LPT planner + archtest"},
	{ID: "5.2", Section: 5, Summary: "云配额与 API 限流必须显式失败，不得静默降并发或转本地", Enforcement: "runtime + archtest"},
	{ID: "6.1", Section: 6, Summary: "固定校准规格贯穿 RunInput、ShardRequest、receipt、SQLite 与 checkpoint identity", Enforcement: "field guard + store"},
	{ID: "6.2", Section: 6, Summary: "校准规格漂移或缺失必须 fail-fast，固定规格不限制 shard 并发", Enforcement: "validation + archtest"},
	{ID: "7.1", Section: 7, Summary: "shard 与 workload 账本显式表达六个耗时阶段及其作用域", Enforcement: "receipt + ledger renderer"},
	{ID: "7.2", Section: 7, Summary: "账本证明 workload 实际 executed，并记录仅用于加速的 Go cache 与前端 seed/Vite cache 证据", Enforcement: "receipt + ledger renderer"},
	{ID: "7.3", Section: 7, Summary: "不适用阶段写 not_applicable，缺失应有观测拒绝 authoritative receipt", Enforcement: "receipt validation"},
	{ID: "7.4", Section: 7, Summary: "100 秒告警固定 warn_and_continue；不得 cancel、kill 或 fail shard", Enforcement: "warning-action validation + archtest"},
	{ID: "8.1", Section: 8, Summary: "DataCache、旧 bundle、本地 Docker、ACR、JSON truth、第二 executor 与隐式 fallback 禁止存在", Enforcement: "deletion + archtest"},
	{ID: "8.2", Section: 8, Summary: "固定 shard 数、并发上限、自动全量重建与第二 refresh writer 禁止存在", Enforcement: "deletion + archtest"},
	{ID: "9.1", Section: 9, Summary: "变更必须闭合 LSP、字段链、状态矩阵、守卫和变更面测试", Enforcement: "repository gates"},
	{ID: "9.2", Section: 9, Summary: "远程验收绑定同一 candidate tree、generation、snapshot、资源与完整账本", Enforcement: "authoritative receipt"},
	{ID: "9.3", Section: 9, Summary: "非 authoritative 结果保持 NOT_VERIFIED，warm CI 超过 100 秒继续优化", Enforcement: "remote acceptance"},
	{ID: "7.5", Section: 7, Summary: "四个 SQLite 历史根写前证明 generation 已被接受，并共享全库唯一保留集合；每代行数不限，只保留最新 3 个有数据代，第四代写入与第一代全族淘汰同事务", Enforcement: "accepted-generation proof + single write-transaction compactor + archtest"},
}

var forbiddenLegacyCapabilities = [...]string{
	"DataCache/Anchor/Delta/direct-cache/zstd bundle",
	"local Docker/Docker Desktop/buildx/localci",
	"ACR-specific auth/role/registry access/repository",
	"JSON baseline or ledger truth source and compatibility dual-read",
	"candidate CLI artifact builder/candidate test-binary builder/second executor",
	"workload PASS result cache/reused return/test skip",
	"spot or remote-to-local implicit fallback",
	"fixed shard count or coordinator concurrency cap",
	"automatic full rebuild without an accepted Ready ImageCache",
	"second refresh command or promotion writer outside the SQLite lease",
}

var sqlAuthorityBindings = [...]SQLAuthorityBinding{
	{Domain: SQLDomainAcceptedBaseline, Table: AcceptedBaselineTable},
	{Domain: SQLDomainRefreshLease, Table: RefreshLeaseTable},
	{Domain: SQLDomainDurationHistory, Table: DurationSamplesTable},
	{Domain: SQLDomainRemoteRun, Table: RemoteRunsTable},
	{Domain: SQLDomainRemoteShard, Table: RemoteShardsTable},
	{Domain: SQLDomainWorkloadExecution, Table: WorkloadExecutionsTable},
	{Domain: SQLDomainRunWarning, Table: RunWarningsTable},
	{Domain: SQLDomainCalibrationCheckpoint, Table: CalibrationCheckpointsTable},
	{Domain: SQLDomainRefreshDelta, Table: RefreshDeltasTable},
	{Domain: SQLDomainCheckReceipt, Table: CheckReceiptsTable},
	{Domain: SQLDomainTimingObservation, Table: TimingObservationsTable},
	{Domain: SQLDomainWorkloadCatalog, Table: WorkloadCatalogsTable},
	{Domain: SQLDomainCatalogObservation, Table: CatalogObservationsTable},
	{Domain: SQLDomainCatalogWorkload, Table: CatalogWorkloadsTable},
	{Domain: SQLDomainRunRequester, Table: RunRequestersTable},
	{Domain: SQLDomainShardWorkload, Table: ShardWorkloadsTable},
	{Domain: SQLDomainGateExecution, Table: GateExecutionsTable},
	{Domain: SQLDomainCalibrationScenario, Table: CalibrationCheckpointScenariosTable},
}

var retentionRootBindings = [...]RetentionRootBinding{
	{Table: DurationSamplesTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: CatalogObservationsTable, GenerationColumn: AcceptedGenerationColumn},
	{Table: RemoteRunsTable, GenerationColumn: AcceptedGenerationColumn},
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

// RequiredChecks 返回每次远程 CI 都必须实际执行并通过的稳定检查目录。
func RequiredChecks() []RequiredCheck {
	return []RequiredCheck{RequiredCheckGate, RequiredCheckNormal, RequiredCheckE2E, RequiredCheckRace, RequiredCheckFrontend, RequiredCheckDependency}
}

// RefreshChecks 返回后台 refresh 唯一允许的非测试检查目录。
func RefreshChecks() []RefreshCheck {
	return []RefreshCheck{RefreshCheckGateBuild, RefreshCheckNormalCompile, RefreshCheckE2ECompile, RefreshCheckRaceCompile, RefreshCheckDependency, RefreshCheckFrontendBuild}
}

// ValidateSourceSnapshotLayout 拒绝 bind/core/embed/worker 私自漂移 accepted snapshot 目录或 manifest。
func ValidateSourceSnapshotLayout(rootPath, manifestPath string) error {
	if rootPath != SourceSnapshotRootPath || manifestPath != SourceSnapshotManifestPath {
		return errors.New("remote CI source snapshot layout must use cicontract paths")
	}
	return nil
}

// ValidateRefreshCheckLogPrefix 拒绝私有、空或暗示 normal test PASS 的 refresh 日志前缀。
func ValidateRefreshCheckLogPrefix(prefix string) error {
	if prefix != RefreshCheckLogPrefix {
		return fmt.Errorf("remote CI refresh check log prefix must equal %q", RefreshCheckLogPrefix)
	}
	return nil
}

// ValidateTimingWarningAction 拒绝把 100 秒目标告警转化为取消、终止或失败。
func ValidateTimingWarningAction(action TimingWarningAction) error {
	if action != TimingWarningWarnAndContinue {
		return fmt.Errorf("remote CI timing warning action must equal %q", TimingWarningWarnAndContinue)
	}
	return nil
}

// ValidateIncrementalRefreshTransfer 拒绝完整 closure/workspace 传输及缺 snapshot 的全量 fallback。
func ValidateIncrementalRefreshTransfer(mode RefreshTransferMode, acceptedGeneration uint64, acceptedSnapshotID, deltaIdentity string) error {
	if mode != RefreshTransferAcceptedSnapshotDelta {
		return fmt.Errorf("remote CI refresh transfer mode must equal %q", RefreshTransferAcceptedSnapshotDelta)
	}
	if acceptedGeneration <= 0 || strings.TrimSpace(acceptedSnapshotID) == "" || strings.TrimSpace(deltaIdentity) == "" {
		return errors.New("remote CI incremental refresh requires accepted generation, snapshot, and delta identity")
	}
	return nil
}

// ValidateDeltaRebuild 要求云端回执证明 accepted snapshot 加 delta 重建了完整目标 tree 与 closure。
func ValidateDeltaRebuild(mode RefreshTransferMode, acceptedGeneration uint64, acceptedSnapshotID, deltaIdentity, targetTreeID, closureDigest string) error {
	if err := ValidateIncrementalRefreshTransfer(mode, acceptedGeneration, acceptedSnapshotID, deltaIdentity); err != nil {
		return err
	}
	if strings.TrimSpace(targetTreeID) == "" || strings.TrimSpace(closureDigest) == "" {
		return errors.New("remote CI delta rebuild requires complete target Git tree and closure evidence")
	}
	return nil
}

// ValidateRequiredChecksObservedPass 拒绝 missing、重复或未通过的必跑检查。
func ValidateRequiredChecksObservedPass(observations []CheckObservation) error {
	required := RequiredChecks()
	if len(observations) != len(required) {
		return fmt.Errorf("remote CI required check observations = %d, want %d", len(observations), len(required))
	}
	seen := make(map[RequiredCheck]struct{}, len(observations))
	for _, observation := range observations {
		if !observation.Executed || !observation.Passed {
			return fmt.Errorf("remote CI required check %q did not pass", observation.Check)
		}
		if strings.TrimSpace(observation.SourceTree) == "" || strings.TrimSpace(observation.AcceptedSnapshotID) == "" || strings.TrimSpace(observation.PlanDigest) == "" || observation.StartedAtUnixMS <= 0 || observation.CompletedAtUnixMS <= observation.StartedAtUnixMS || observation.DurationMS != observation.CompletedAtUnixMS-observation.StartedAtUnixMS || observation.DurationMS <= 0 {
			return fmt.Errorf("remote CI required check %q receipt is incomplete", observation.Check)
		}
		wantDigest, err := CheckObservationReceiptDigest(observation)
		if err != nil || observation.ReceiptSHA256 != wantDigest {
			return fmt.Errorf("remote CI required check %q receipt digest is invalid", observation.Check)
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

// CheckObservationReceiptDigest returns the canonical digest for one
// structured required-check receipt. The digest never trusts its own field.
func CheckObservationReceiptDigest(observation CheckObservation) (string, error) {
	data := fmt.Sprintf("%s\x00%t\x00%t\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d", observation.Check, observation.Executed, observation.Passed, observation.SourceTree, observation.AcceptedSnapshotID, observation.PlanDigest, observation.StartedAtUnixMS, observation.CompletedAtUnixMS, observation.DurationMS)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(data))), nil
}

// ValidateRefreshChecksObservedPass 拒绝 refresh 将编译或依赖操作伪装为 normal CI 测试通过。
func ValidateRefreshChecksObservedPass(observations []RefreshCheckObservation) error {
	required := RefreshChecks()
	if len(observations) != len(required) {
		return fmt.Errorf("remote CI refresh check observations = %d, want %d", len(observations), len(required))
	}
	seen := make(map[RefreshCheck]struct{}, len(observations))
	for _, observation := range observations {
		if !observation.Executed || !observation.Passed || !observation.TestBodyNotApplicable {
			return fmt.Errorf("remote CI refresh check %q is not an executed non-test pass", observation.Check)
		}
		if strings.TrimSpace(observation.SourceTree) == "" || strings.TrimSpace(observation.AcceptedSnapshotID) == "" || strings.TrimSpace(observation.PlanDigest) == "" || observation.StartedAtUnixMS <= 0 || observation.CompletedAtUnixMS <= observation.StartedAtUnixMS || observation.DurationMS != observation.CompletedAtUnixMS-observation.StartedAtUnixMS || observation.DurationMS <= 0 {
			return fmt.Errorf("remote CI refresh check %q receipt is incomplete", observation.Check)
		}
		if observation.Check == RefreshCheckDependency {
			if !observation.CandidateCompileNotApplicable || observation.CandidateCompileMS != 0 {
				return errors.New("remote CI refresh dependency check must mark candidate compile not_applicable")
			}
		} else if observation.CandidateCompileNotApplicable || observation.CandidateCompileMS <= 0 || observation.CandidateCompileMS > observation.DurationMS {
			return fmt.Errorf("remote CI refresh check %q compile timing is invalid", observation.Check)
		}
		wantDigest, err := RefreshCheckObservationReceiptDigest(observation)
		if err != nil || observation.ReceiptSHA256 != wantDigest {
			return fmt.Errorf("remote CI refresh check %q receipt digest is invalid", observation.Check)
		}
		if _, duplicate := seen[observation.Check]; duplicate {
			return fmt.Errorf("remote CI refresh check %q is duplicated", observation.Check)
		}
		seen[observation.Check] = struct{}{}
	}
	for _, check := range required {
		if _, exists := seen[check]; !exists {
			return fmt.Errorf("remote CI refresh check %q is missing", check)
		}
	}
	return nil
}

// RefreshCheckObservationReceiptDigest returns the canonical digest for a refresh-only observation.
func RefreshCheckObservationReceiptDigest(observation RefreshCheckObservation) (string, error) {
	data := fmt.Sprintf("%s\x00%t\x00%t\x00%s\x00%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%t\x00%t", observation.Check, observation.Executed, observation.Passed, observation.SourceTree, observation.AcceptedSnapshotID, observation.PlanDigest, observation.StartedAtUnixMS, observation.CompletedAtUnixMS, observation.DurationMS, observation.CandidateCompileMS, observation.CandidateCompileNotApplicable, observation.TestBodyNotApplicable)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(data))), nil
}

// ValidateTargetPlatform 拒绝非 linux/amd64 的远程 CI 或刷新目标。
func ValidateTargetPlatform(platform string) error {
	if platform != TargetPlatform {
		return fmt.Errorf("remote CI platform must equal %q", TargetPlatform)
	}
	return nil
}

// ValidateNonACRRegistryHost rejects the Alibaba Cloud ACR host family while
// preserving support for any other OCI registry. Callers retain ownership of
// their syntax and immutable-digest validation.
func ValidateNonACRRegistryHost(reference string) error {
	repository, _, _ := strings.Cut(reference, "@")
	host, _, hasRegistryHost := strings.Cut(repository, "/")
	if !hasRegistryHost {
		return nil
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Hostname() == "" {
		return errors.New("OCI registry host is invalid")
	}
	normalized := strings.ToLower(strings.TrimRight(parsed.Hostname(), "."))
	if normalized == "aliyuncs.com" || strings.HasSuffix(normalized, ".aliyuncs.com") {
		return errors.New("Alibaba Cloud ACR registry hosts are forbidden")
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

// ValidateRefreshMinimumInterval 保证新 attempt 使用统一的两小时间隔。
func ValidateRefreshMinimumInterval(interval time.Duration) error {
	if interval != RefreshMinimumInterval {
		return fmt.Errorf("remote CI refresh minimum interval must equal %s", RefreshMinimumInterval)
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
	if len(retentionRootBindings) != 4 {
		return fmt.Errorf("remote CI SQLite retention must own exactly four historical roots, got %d", len(retentionRootBindings))
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

// ValidateCalibrationResources 拒绝缺失或不可被回执精确绑定的固定规格。
func ValidateCalibrationResources(classID string, cpu, memoryGiB float64) error {
	if strings.TrimSpace(classID) == "" || classID != strings.TrimSpace(classID) || cpu <= 0 || memoryGiB <= 0 {
		return errors.New("remote CI calibration class, CPU, and memory are required")
	}
	return nil
}

// IsRefreshCandidatePhase 表示一个 phase 是否仍属于晋级前的候选生命周期。
func IsRefreshCandidatePhase(phase RefreshPhase) bool {
	return phase == RefreshClaimed || phase == RefreshBuilding || phase == RefreshCachePreparing || phase == RefreshReadyValidated
}

// ValidateRefreshTransition 拒绝跳过 Ready 验证、回退或丢失清理责任的状态迁移。
func ValidateRefreshTransition(current, next RefreshPhase) error {
	if validRefreshTransition(current, next) {
		return nil
	}
	return fmt.Errorf("remote CI refresh transition %q -> %q is forbidden", current, next)
}

func validRefreshTransition(current, next RefreshPhase) bool {
	if IsRefreshCandidatePhase(current) && next == RefreshFailed {
		return true
	}
	return (current == RefreshIdle && next == RefreshClaimed) ||
		(current == RefreshClaimed && next == RefreshUnchanged) ||
		(current == RefreshClaimed && next == RefreshBuilding) ||
		(current == RefreshBuilding && next == RefreshCachePreparing) ||
		(current == RefreshCachePreparing && next == RefreshReadyValidated) ||
		(current == RefreshReadyValidated && next == RefreshPromoted) ||
		(current == RefreshPromoted && next == RefreshRetiring) ||
		(current == RefreshRetiring && (next == RefreshIdle || next == RefreshCleanupPending)) ||
		(current == RefreshCleanupPending && (next == RefreshRetiring || next == RefreshIdle))
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
%[1]s 是唯一 retention 常量 owner。duration samples、catalog observations、runs 与 calibration checkpoints 四个历史根都必须绑定已验证的 accepted baseline generation；每个根写事务必须先读取同一 SQLite authority 的 accepted singleton，拒绝零值、无 authority、无效 authority 与晚于当前 accepted generation 的伪造未来代。refresh 只能逐代晋级，因此已启动旧代运行仍可在完成时写入。四个根的 distinct generation 并集按数值确定全库唯一保留集合，任何根都不得保留该集合之外的数据。每一代可包含任意数量的 workload、sample、shard、timing、receipt 或 scenario，禁止用固定行数限制代码和测试增长。SQLite 只保留最新 %[2]d 个有数据的 generation；第四个有数据代首次成功写入时，必须在同一事务内淘汰最老一代全部历史根及其 cascade 子数据。

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
	if ID == "" || DocumentPath == "" || ExecutionPathID == "" || RefreshPathID == "" || SQLAuthorityID == "" || RefreshTransferModeID == "" || SourceSnapshotRootPath == "" || SourceSnapshotManifestPath == "" || RefreshCheckLogPrefix == "" {
		return errors.New("remote CI contract identity is incomplete")
	}
	if err := ValidateTargetPlatform(TargetPlatform); err != nil {
		return err
	}
	if err := ValidateGoToolchainVersion(GoToolchainVersion); err != nil {
		return err
	}
	if err := ValidateShardTargetDuration(ShardTargetDuration); err != nil {
		return err
	}
	if err := ValidateRefreshMinimumInterval(RefreshMinimumInterval); err != nil {
		return err
	}
	if err := ValidateRetentionGenerations(); err != nil {
		return err
	}
	if err := ValidateTimingContract(); err != nil {
		return err
	}
	if err := ValidateTimingWarningAction(TimingWarningWarnAndContinue); err != nil {
		return err
	}
	if err := ValidateSourceSnapshotLayout(SourceSnapshotRootPath, SourceSnapshotManifestPath); err != nil {
		return err
	}
	if err := ValidateRefreshCheckLogPrefix(RefreshCheckLogPrefix); err != nil {
		return err
	}
	if err := ValidateIncrementalRefreshTransfer(RefreshTransferAcceptedSnapshotDelta, 1, "accepted-snapshot", "delta"); err != nil {
		return err
	}
	if err := ValidateDeltaRebuild(RefreshTransferAcceptedSnapshotDelta, 1, "accepted-snapshot", "delta", "target-tree", "closure-digest"); err != nil {
		return err
	}
	observations := make([]CheckObservation, 0, len(RequiredChecks()))
	for _, check := range RequiredChecks() {
		observation := CheckObservation{Check: check, Executed: true, Passed: true, SourceTree: "target-tree", AcceptedSnapshotID: "accepted-snapshot", PlanDigest: "plan-digest", StartedAtUnixMS: 1, CompletedAtUnixMS: 2, DurationMS: 1}
		digest, err := CheckObservationReceiptDigest(observation)
		if err != nil {
			return err
		}
		observation.ReceiptSHA256 = digest
		observations = append(observations, observation)
	}
	if err := ValidateRequiredChecksObservedPass(observations); err != nil {
		return err
	}
	refreshObservations := make([]RefreshCheckObservation, 0, len(RefreshChecks()))
	for _, check := range RefreshChecks() {
		observation := RefreshCheckObservation{Check: check, Executed: true, Passed: true, SourceTree: "target-tree", AcceptedSnapshotID: "accepted-snapshot", PlanDigest: "plan-digest", StartedAtUnixMS: 1, CompletedAtUnixMS: 2, DurationMS: 1, TestBodyNotApplicable: true}
		if check == RefreshCheckDependency {
			observation.CandidateCompileNotApplicable = true
		} else {
			observation.CandidateCompileMS = 1
		}
		digest, err := RefreshCheckObservationReceiptDigest(observation)
		if err != nil {
			return err
		}
		observation.ReceiptSHA256 = digest
		refreshObservations = append(refreshObservations, observation)
	}
	if err := ValidateRefreshChecksObservedPass(refreshObservations); err != nil {
		return err
	}
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
