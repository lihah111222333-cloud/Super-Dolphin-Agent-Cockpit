package remoteci

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// ProgressEventSchemaVersion 版本化远程运行的旁路进度事件，不属于 RunResult 回执。
const ProgressEventSchemaVersion uint32 = 1

// ReuseDiagnosticSchemaVersion 版本化非权威 PASS 复用归因旁路。
const ReuseDiagnosticSchemaVersion uint32 = 12

// ShardPlanDiagnosticSchemaVersion 版本化非权威分片装箱摘要旁路。
const ShardPlanDiagnosticSchemaVersion uint32 = 1

// progressHeartbeatInterval 在分片计数不变时限制旁路心跳频率。
const progressHeartbeatInterval = 10 * time.Second

// progressFailureState 复用 gate owner 的失败状态，避免旁路重声明协议值。
const progressFailureState = string(gate.ResultStatusFailed)

// ProgressPhase 是远程运行可观察但不具权威性的生命周期阶段。
type ProgressPhase string

const (
	ProgressPhasePrepare  ProgressPhase = "prepare"
	ProgressPhaseUpload   ProgressPhase = "upload"
	ProgressPhaseCreate   ProgressPhase = "create"
	ProgressPhaseRun      ProgressPhase = "run"
	ProgressPhaseTerminal ProgressPhase = "terminal"
	ProgressPhaseCleanup  ProgressPhase = "cleanup"
	ProgressPhaseComplete ProgressPhase = "complete"
)

// ProgressEvent 是 stdout 最终回执之外、stderr 进度旁路中的低噪声机器可读观察。
// 它只携带计数和耗时；JobID 仅用于关联本次运行，不携带路径、token、摘要或云凭据。
type ProgressEvent struct {
	SchemaVersion   uint32        `json:"schema_version"`
	Kind            string        `json:"kind"`
	Sequence        uint64        `json:"sequence"`
	JobID           string        `json:"job_id,omitempty"`
	Phase           ProgressPhase `json:"phase"`
	State           string        `json:"state"`
	ElapsedMS       int64         `json:"elapsed_ms"`
	TotalShards     int           `json:"total_shards"`
	PendingShards   int           `json:"pending_shards"`
	RunningShards   int           `json:"running_shards"`
	CompletedShards int           `json:"completed_shards"`
	FailedShards    int           `json:"failed_shards"`
	CacheHits       int           `json:"cache_hits"`
	CacheMisses     int           `json:"cache_misses"`
	CacheReused     int           `json:"cache_reused"`
	CompileTimingMS *int64        `json:"compile_timing_ms,omitempty"`
	TestTimingMS    *int64        `json:"test_timing_ms,omitempty"`
	CleanupTotal    int           `json:"cleanup_total"`
	CleanupComplete int           `json:"cleanup_complete"`
	CleanupFailed   int           `json:"cleanup_failed"`
}

// ReuseDiagnostic 区分严格 identity lookup 与 package 原子降级后的有效复用。
type ReuseDiagnostic struct {
	SchemaVersion             uint32 `json:"schema_version"`
	Kind                      string `json:"kind"`
	Forced                    bool   `json:"forced"`
	MissConfirmationThreshold int    `json:"miss_confirmation_threshold"`
	DirectHits                int    `json:"direct_hits"`
	SourceReplayHits          int    `json:"source_replay_hits"`
	EnvironmentReplayHits     int    `json:"environment_replay_hits"`
	ExactHits                 int    `json:"exact_hits"`
	DirectMisses              int    `json:"direct_misses"`
	RecoveredDirectMisses     int    `json:"recovered_direct_misses"`
	ReplayMisses              int    `json:"replay_misses"`
	AtomicDemoted             int    `json:"atomic_demoted"`
	// CalibrationDurationDemoted 是冻结兼容字段；校准不得改变 correctness PASS，因此必须为零。
	CalibrationDurationDemoted int                    `json:"calibration_duration_demoted"`
	EffectiveHits              int                    `json:"effective_hits"`
	EffectiveMisses            int                    `json:"effective_misses"`
	Replay                     ReuseReplayDiagnostic  `json:"replay"`
	MissGroups                 []ReuseDiagnosticGroup `json:"miss_groups,omitempty"`
}

// ReuseReplayDiagnostic 汇总两类 exact-tree replay 的候选与拒绝阶段，不携带 workload 或摘要。
type ReuseReplayDiagnostic struct {
	SourceCandidateWorkloads            int `json:"source_candidate_workloads"`
	SourceCandidates                    int `json:"source_candidates"`
	SourceCandidateTrees                int `json:"source_candidate_trees"`
	SourceCandidateEvaluations          int `json:"source_candidate_evaluations"`
	SourceInputUnavailable              int `json:"source_input_unavailable"`
	SourceInputMismatch                 int `json:"source_input_mismatch"`
	SourceSingleVoteRecovered           int `json:"source_single_vote_recovered"`
	SourceDeclarationMissVotes          int `json:"source_declaration_miss_votes"`
	SourceRuntimeMissVotes              int `json:"source_runtime_miss_votes"`
	SourceCompileMissVotes              int `json:"source_compile_miss_votes"`
	SourceCompileObligations            int `json:"source_compile_obligations"`
	SourceCompileCoveredRecoveries      int `json:"source_compile_covered_recoveries"`
	SourceAlgorithmCompatibleRecoveries int `json:"source_algorithm_compatible_recoveries"`
	SourceConfirmedMisses               int `json:"source_confirmed_misses"`
	EnvironmentHintWorkloads            int `json:"environment_hint_workloads"`
	EnvironmentHints                    int `json:"environment_hints"`
	EnvironmentGenerationMismatch       int `json:"environment_generation_mismatch"`
	EnvironmentTargetUnavailable        int `json:"environment_target_unavailable"`
	EnvironmentSourceUnavailable        int `json:"environment_source_unavailable"`
	EnvironmentHistoricalMismatch       int `json:"environment_historical_mismatch"`
	EnvironmentCurrentWorkerMismatch    int `json:"environment_current_worker_mismatch"`
	EnvironmentInputMismatch            int `json:"environment_input_mismatch"`
	EnvironmentSingleVoteRecovered      int `json:"environment_single_vote_recovered"`
	EnvironmentDeclarationMissVotes     int `json:"environment_declaration_miss_votes"`
	EnvironmentRuntimeMissVotes         int `json:"environment_runtime_miss_votes"`
	EnvironmentCompileMissVotes         int `json:"environment_compile_miss_votes"`
	EnvironmentCompileOwners            int `json:"environment_compile_owners"`
	EnvironmentCompileCoveredRecoveries int `json:"environment_compile_covered_recoveries"`
	EnvironmentConfirmedMisses          int `json:"environment_confirmed_misses"`
	EnvironmentAlgorithmCompatibleTrees int `json:"environment_algorithm_compatible_trees"`
	EnvironmentInputPrewarmSkipped      int `json:"environment_input_prewarm_skipped"`
	CacheSnapshotComputations           int `json:"cache_snapshot_computations"`
	CacheSnapshotLoads                  int `json:"cache_snapshot_loads"`
	CacheInputComputations              int `json:"cache_input_computations"`
	CacheCompileComputations            int `json:"cache_compile_computations"`
	CacheSemanticComputations           int `json:"cache_semantic_computations"`
	CacheEnvironmentComputations        int `json:"cache_environment_computations"`
	CacheWorkerComputations             int `json:"cache_worker_computations"`
	CacheAlgorithmComputations          int `json:"cache_algorithm_computations"`
	CachePersistentInputHits            int `json:"cache_persistent_input_hits"`
	CachePersistentInputWrites          int `json:"cache_persistent_input_writes"`
}

// ReuseDiagnosticGroup 按 workload 类型与编译单元聚合 MISS 来源；它不携带
// 单个 selector、身份摘要或凭据，只用于定位过宽失效面。
type ReuseDiagnosticGroup struct {
	TargetKind      string `json:"target_kind"`
	TargetGroup     string `json:"target_group"`
	ExactHits       int    `json:"exact_hits"`
	DirectMisses    int    `json:"direct_misses"`
	AtomicDemoted   int    `json:"atomic_demoted"`
	EffectiveHits   int    `json:"effective_hits"`
	EffectiveMisses int    `json:"effective_misses"`
}

// ShardPlanDiagnostic 汇总一次 MISS-only 计划的装箱形状，不暴露 workload 名称、摘要或凭据。
type ShardPlanDiagnostic struct {
	SchemaVersion                 uint32 `json:"schema_version"`
	Kind                          string `json:"kind"`
	Calibration                   bool   `json:"calibration"`
	TargetDurationMS              int64  `json:"target_duration_ms"`
	TotalShards                   int    `json:"total_shards"`
	TotalWorkloads                int    `json:"total_workloads"`
	MinWorkloadsPerShard          int    `json:"min_workloads_per_shard"`
	MaxWorkloadsPerShard          int    `json:"max_workloads_per_shard"`
	MinEstimatedShardDurationMS   int64  `json:"min_estimated_shard_duration_ms"`
	MaxEstimatedShardDurationMS   int64  `json:"max_estimated_shard_duration_ms"`
	OverTargetEstimatedShardCount int    `json:"over_target_estimated_shard_count"`
}

// ProgressObserver 接收不具权威性的旁路进度，不改变执行结果。
type ProgressObserver interface {
	ObserveRemoteCIProgress(ProgressEvent)
}

type reuseDiagnosticObserver interface {
	ObserveRemoteCIReuseDiagnostic(ReuseDiagnostic)
}

type shardPlanDiagnosticObserver interface {
	ObserveRemoteCIShardPlanDiagnostic(ShardPlanDiagnostic)
}

// JSONProgressObserver 将每条进度事件按行写入调用方的旁路。
type JSONProgressObserver struct {
	writer io.Writer
	mu     sync.Mutex
	err    error
}

// NewJSONProgressObserver 创建可选的 NDJSON 进度旁路。
func NewJSONProgressObserver(writer io.Writer) *JSONProgressObserver {
	return &JSONProgressObserver{writer: writer}
}

// ObserveRemoteCIProgress 原子写入单条事件，并把 writer 错误保留给 CLI 边界。
func (observer *JSONProgressObserver) ObserveRemoteCIProgress(event ProgressEvent) {
	if observer == nil || observer.writer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.err != nil {
		return
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = ProgressEventSchemaVersion
	}
	if event.Kind == "" {
		event.Kind = "remote_ci_progress"
	}
	observer.err = json.NewEncoder(observer.writer).Encode(event)
}

// ObserveRemoteCIReuseDiagnostic 原子写入不含 workload 名称或身份摘要的聚合归因。
func (observer *JSONProgressObserver) ObserveRemoteCIReuseDiagnostic(diagnostic ReuseDiagnostic) {
	if observer == nil || observer.writer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.err != nil {
		return
	}
	if diagnostic.SchemaVersion == 0 {
		diagnostic.SchemaVersion = ReuseDiagnosticSchemaVersion
	}
	if diagnostic.Kind == "" {
		diagnostic.Kind = "remote_ci_reuse_diagnostic"
	}
	observer.err = json.NewEncoder(observer.writer).Encode(diagnostic)
}

// ObserveRemoteCIShardPlanDiagnostic 原子写入不含 workload 身份的装箱摘要。
func (observer *JSONProgressObserver) ObserveRemoteCIShardPlanDiagnostic(diagnostic ShardPlanDiagnostic) {
	if observer == nil || observer.writer == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.err != nil {
		return
	}
	if diagnostic.SchemaVersion == 0 {
		diagnostic.SchemaVersion = ShardPlanDiagnosticSchemaVersion
	}
	if diagnostic.Kind == "" {
		diagnostic.Kind = "remote_ci_shard_plan_diagnostic"
	}
	observer.err = json.NewEncoder(observer.writer).Encode(diagnostic)
}

// ProgressError 返回旁路写入失败，但不改变远程运行回执。
func ProgressError(observer ProgressObserver) error {
	if source, ok := observer.(interface{ ProgressError() error }); ok {
		return source.ProgressError()
	}
	return nil
}

// ProgressError 暴露保留的旁路 writer 错误给 CLI 边界。
func (observer *JSONProgressObserver) ProgressError() error {
	if observer == nil {
		return nil
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return observer.err
}

type progressShardState uint8

const (
	progressShardPending progressShardState = iota
	progressShardRunning
	progressShardCompleted
	progressShardFailed
)

type progressTracker struct {
	observer    ProgressObserver
	clock       func() time.Time
	startedAt   time.Time
	mu          sync.Mutex
	sequence    uint64
	jobID       string
	totalShards int
	cacheHits   int
	cacheMisses int
	cacheReused int
	shards      map[string]progressShardState
	createFails int
	cleanupTotal,
	cleanupComplete,
	cleanupFailed int
	lastCounts    progressCounts
	haveCounts    bool
	lastEmittedAt time.Time
}

type progressCounts struct {
	pending, running, completed, failed int
}

// newProgressTracker 创建使用调用方时钟的旁路进度聚合器。
func newProgressTracker(observer ProgressObserver, clock func() time.Time) *progressTracker {
	return &progressTracker{observer: observer, clock: clock, startedAt: clock(), shards: make(map[string]progressShardState)}
}

// enabled 判断当前是否启用了进度旁路。
func (tracker *progressTracker) enabled() bool { return tracker != nil && tracker.observer != nil }

// setCacheCounts 更新规划阶段的命中、未命中和复用计数。
func (tracker *progressTracker) setCacheCounts(hits, misses, reused int) {
	if !tracker.enabled() {
		return
	}
	tracker.mu.Lock()
	tracker.cacheHits, tracker.cacheMisses, tracker.cacheReused = hits, misses, reused
	tracker.mu.Unlock()
}

// observeReuseDecision 发出严格 lookup 与 package 原子降级之间的聚合差异。
func (tracker *progressTracker) observeReuseDecision(diagnostic ReuseDiagnostic) {
	if !tracker.enabled() {
		return
	}
	observer, ok := tracker.observer.(reuseDiagnosticObserver)
	if ok {
		observer.ObserveRemoteCIReuseDiagnostic(diagnostic)
	}
}

// observeShardPlan 发出 MISS-only 分片的数量与估时范围，便于识别过度装箱。
func (tracker *progressTracker) observeShardPlan(plan gate.WorkloadExecutionPlan) {
	if !tracker.enabled() {
		return
	}
	observer, ok := tracker.observer.(shardPlanDiagnosticObserver)
	if ok {
		observer.ObserveRemoteCIShardPlanDiagnostic(newShardPlanDiagnostic(plan))
	}
}

// newShardPlanDiagnostic 从冻结计划计算无身份信息的确定性装箱摘要。
func newShardPlanDiagnostic(plan gate.WorkloadExecutionPlan) ShardPlanDiagnostic {
	diagnostic := ShardPlanDiagnostic{Calibration: plan.Context.Calibration, TargetDurationMS: plan.Context.TargetDurationMS, TotalShards: len(plan.Shards)}
	for index, shard := range plan.Shards {
		workloads := len(shard.Workloads)
		diagnostic.TotalWorkloads += workloads
		if index == 0 || workloads < diagnostic.MinWorkloadsPerShard {
			diagnostic.MinWorkloadsPerShard = workloads
		}
		diagnostic.MaxWorkloadsPerShard = max(diagnostic.MaxWorkloadsPerShard, workloads)
		if index == 0 || shard.EstimatedDurationMS < diagnostic.MinEstimatedShardDurationMS {
			diagnostic.MinEstimatedShardDurationMS = shard.EstimatedDurationMS
		}
		diagnostic.MaxEstimatedShardDurationMS = max(diagnostic.MaxEstimatedShardDurationMS, shard.EstimatedDurationMS)
		if shard.EstimatedDurationMS > plan.Context.TargetDurationMS {
			diagnostic.OverTargetEstimatedShardCount++
		}
	}
	return diagnostic
}

// setJobID 绑定可安全用于关联的远程 job 标识。
func (tracker *progressTracker) setJobID(jobID string) {
	if !tracker.enabled() {
		return
	}
	tracker.mu.Lock()
	tracker.jobID = jobID
	tracker.mu.Unlock()
}

// setTotal 设置当前候选计划的总分片数。
func (tracker *progressTracker) setTotal(total int) {
	if !tracker.enabled() || total < 0 {
		return
	}
	tracker.mu.Lock()
	tracker.totalShards = total
	tracker.mu.Unlock()
}

// planUpdated 记录当前计划分片总数，并标记准备阶段已更新。
func (tracker *progressTracker) planUpdated(total int) {
	tracker.setTotal(total)
	tracker.phase(ProgressPhasePrepare, "updated")
}

// uploadStarted 标记源资产上传阶段开始。
func (tracker *progressTracker) uploadStarted() {
	tracker.phase(ProgressPhaseUpload, "started")
}

// uploadFinished 标记源资产上传阶段成功或失败。
func (tracker *progressTracker) uploadFinished(err error) {
	if err != nil {
		tracker.phase(ProgressPhaseUpload, progressFailureState)
		return
	}
	tracker.phase(ProgressPhaseUpload, "completed")
}

// createStarted 标记 ECI 分片创建阶段开始。
func (tracker *progressTracker) createStarted() {
	tracker.phase(ProgressPhaseCreate, "started")
}

// createFinished 标记 ECI 分片创建阶段成功或失败。
func (tracker *progressTracker) createFinished(err error) {
	if err != nil {
		tracker.phase(ProgressPhaseCreate, progressFailureState)
		return
	}
	tracker.phase(ProgressPhaseCreate, "completed")
}

// runFinished 发出终态报告，并标记分片运行阶段成功或失败。
func (tracker *progressTracker) runFinished(shards []ShardResult, err error) {
	state := "completed"
	if err != nil {
		state = progressFailureState
	}
	tracker.phase(ProgressPhaseRun, state)
	tracker.emitTerminal(shards, state)
}

// cleanupFinished 标记清理阶段成功或失败。
func (tracker *progressTracker) cleanupFinished(err error) {
	state := "completed"
	if err != nil {
		state = progressFailureState
	}
	tracker.phase(ProgressPhaseCleanup, state)
}

// phase 发出一个生命周期阶段事件。
func (tracker *progressTracker) phase(phase ProgressPhase, state string) {
	tracker.emit(phase, state, nil, nil)
}

// markCreated 记录一个已创建 ECI 分片及其当前状态。
func (tracker *progressTracker) markCreated(group eci.ContainerGroup) {
	if !tracker.enabled() || group.ID == "" {
		return
	}
	tracker.mu.Lock()
	tracker.shards[group.ID] = progressShardStateForECI(group.Status)
	tracker.mu.Unlock()
	tracker.emitChanged(ProgressPhaseCreate, "updated", nil, nil)
}

// markCreateFailed 记录一次 ECI 创建失败。
func (tracker *progressTracker) markCreateFailed() {
	if !tracker.enabled() {
		return
	}
	tracker.mu.Lock()
	tracker.createFails++
	tracker.mu.Unlock()
	tracker.emit(ProgressPhaseCreate, progressFailureState, nil, nil)
}

// observeGroups 吸收 ECI 查询返回的状态并按变化发出事件。
func (tracker *progressTracker) observeGroups(groups []eci.ContainerGroup) {
	if !tracker.enabled() || len(groups) == 0 {
		return
	}
	tracker.mu.Lock()
	for _, group := range groups {
		if group.ID != "" {
			tracker.shards[group.ID] = progressShardStateForECI(group.Status)
		}
	}
	tracker.mu.Unlock()
	tracker.emitChanged(ProgressPhaseRun, "updated", nil, nil)
}

// beginCleanup 开始记录对象和 ECI 分片清理进度。
func (tracker *progressTracker) beginCleanup(total int) {
	if !tracker.enabled() {
		return
	}
	tracker.mu.Lock()
	tracker.cleanupTotal, tracker.cleanupComplete, tracker.cleanupFailed = total, 0, 0
	tracker.mu.Unlock()
	tracker.phase(ProgressPhaseCleanup, "started")
}

// markCleanup 记录一项清理成功或失败。
func (tracker *progressTracker) markCleanup(success bool) {
	if !tracker.enabled() {
		return
	}
	tracker.mu.Lock()
	tracker.cleanupComplete++
	if !success {
		tracker.cleanupFailed++
	}
	tracker.mu.Unlock()
	tracker.emit(ProgressPhaseCleanup, "updated", nil, nil)
}

// emitTerminal 发出终态阶段及已测量的编译、测试耗时。
func (tracker *progressTracker) emitTerminal(shards []ShardResult, state string) {
	compileMS, testMS := remoteProgressTimings(shards)
	tracker.emit(ProgressPhaseTerminal, state, compileMS, testMS)
}

// emitFinal 发出不改变权威回执的最终旁路事件。
func (tracker *progressTracker) emitFinal(result RunResult) {
	tracker.phase(ProgressPhaseComplete, string(result.Status))
}

// emitChanged 仅在分片计数变化时发出低噪声状态事件。
func (tracker *progressTracker) emitChanged(phase ProgressPhase, state string, compileMS, testMS *int64) {
	if !tracker.enabled() {
		return
	}
	tracker.mu.Lock()
	counts := tracker.countsLocked()
	changed := !tracker.haveCounts || counts != tracker.lastCounts
	heartbeat := false
	if !changed && !tracker.lastEmittedAt.IsZero() {
		heartbeat = tracker.clock().Sub(tracker.lastEmittedAt) >= progressHeartbeatInterval
	}
	if changed {
		tracker.lastCounts, tracker.haveCounts = counts, true
	}
	tracker.mu.Unlock()
	if changed || heartbeat {
		if heartbeat {
			state = "heartbeat"
		}
		tracker.emit(phase, state, compileMS, testMS)
	}
}

// emit 构造并串行写出一条进度事件，确保 sequence 与输出顺序一致。
func (tracker *progressTracker) emit(phase ProgressPhase, state string, compileMS, testMS *int64) {
	if !tracker.enabled() {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.sequence++
	now := tracker.clock()
	event := ProgressEvent{
		SchemaVersion: ProgressEventSchemaVersion, Kind: "remote_ci_progress", Sequence: tracker.sequence,
		JobID: tracker.jobID,
		Phase: phase, State: state, ElapsedMS: max(now.Sub(tracker.startedAt).Milliseconds(), 0),
		CacheHits: tracker.cacheHits, CacheMisses: tracker.cacheMisses, CacheReused: tracker.cacheReused,
		CompileTimingMS: compileMS, TestTimingMS: testMS,
		CleanupTotal: tracker.cleanupTotal, CleanupComplete: tracker.cleanupComplete, CleanupFailed: tracker.cleanupFailed,
	}
	counts := tracker.countsLocked()
	event.TotalShards, event.PendingShards, event.RunningShards = tracker.totalShards, counts.pending, counts.running
	event.CompletedShards, event.FailedShards = counts.completed, counts.failed
	tracker.lastEmittedAt = now
	tracker.observer.ObserveRemoteCIProgress(event)
}

// countsLocked 在持锁状态下计算当前分片和清理前的计划计数。
func (tracker *progressTracker) countsLocked() progressCounts {
	counts := progressCounts{failed: tracker.createFails}
	for _, state := range tracker.shards {
		switch state {
		case progressShardRunning:
			counts.running++
		case progressShardCompleted:
			counts.completed++
		case progressShardFailed:
			counts.failed++
		default:
			counts.pending++
		}
	}
	counts.pending = max(tracker.totalShards-counts.running-counts.completed-counts.failed, counts.pending)
	return counts
}

// progressShardStateForECI 将 Aliyun ECI 终态映射为旁路计数状态。
func progressShardStateForECI(status string) progressShardState {
	switch status {
	case "Running":
		return progressShardRunning
	case "Succeeded":
		return progressShardCompleted
	case "Failed", "Stopped", "Cancelled", "Canceled", "Exception":
		return progressShardFailed
	default:
		return progressShardPending
	}
}

// remoteProgressTimings 汇总 worker 报告中的已测量耗时，仅服务旁路观察，不改 SQLite 权威账本。
func remoteProgressTimings(shards []ShardResult) (*int64, *int64) {
	var compileMS, testMS int64
	compileMeasured, testMeasured := false, false
	for _, shard := range shards {
		for _, execution := range shard.Report.CompileGroupExecutions {
			if execution.DurationMS > 0 {
				compileMS += execution.DurationMS
				compileMeasured = true
			}
		}
		for _, execution := range shard.Report.Gates {
			if execution.ExecutionProfile.TestBodyMS > 0 {
				testMS += execution.ExecutionProfile.TestBodyMS
				testMeasured = true
			}
		}
	}
	var compile, test *int64
	if compileMeasured {
		compile = &compileMS
	}
	if testMeasured {
		test = &testMS
	}
	return compile, test
}

// progressRuntime 观察 ECI 生命周期响应，但不替换 ECI 执行器。
type progressRuntime struct {
	inner   Runtime
	tracker *progressTracker
}

// CreateContainerGroup 观察 Aliyun ECI 创建结果并保留原有执行语义。
func (runtime *progressRuntime) CreateContainerGroup(ctx context.Context, request eci.CreateRequest) (eci.ContainerGroup, error) {
	group, err := runtime.inner.CreateContainerGroup(ctx, request)
	if err != nil {
		runtime.tracker.markCreateFailed()
		return eci.ContainerGroup{}, err
	}
	runtime.tracker.markCreated(group)
	return group, nil
}

// DescribeContainerGroups 观察 Aliyun ECI 分片状态查询结果。
func (runtime *progressRuntime) DescribeContainerGroups(ctx context.Context, ids ...string) ([]eci.ContainerGroup, error) {
	groups, err := runtime.inner.DescribeContainerGroups(ctx, ids...)
	if err == nil {
		runtime.tracker.observeGroups(groups)
	}
	return groups, err
}

// DescribeContainerLog 保持原有 ECI 日志读取边界，不输出日志正文。
func (runtime *progressRuntime) DescribeContainerLog(ctx context.Context, groupID, containerName string) (string, error) {
	return runtime.inner.DescribeContainerLog(ctx, groupID, containerName)
}

// DeleteContainerGroup 观察 Aliyun ECI 清理结果并保留原有错误。
func (runtime *progressRuntime) DeleteContainerGroup(ctx context.Context, groupID string) error {
	return runtime.inner.DeleteContainerGroup(ctx, groupID)
}

// ConfirmContainerGroupAbsent 转发 ECI 删除后的 provider absence proof。
func (runtime *progressRuntime) ConfirmContainerGroupAbsent(ctx context.Context, groupID string) (bool, error) {
	return runtime.inner.ConfirmContainerGroupAbsent(ctx, groupID)
}

// newProgressRuntime 仅在启用旁路时包装现有 Aliyun ECI runtime。
func newProgressRuntime(runtime Runtime, tracker *progressTracker) Runtime {
	if tracker == nil || !tracker.enabled() {
		return runtime
	}
	return &progressRuntime{inner: runtime, tracker: tracker}
}
