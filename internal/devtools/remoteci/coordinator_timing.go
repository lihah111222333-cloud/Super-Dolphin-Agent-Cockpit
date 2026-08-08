package remoteci

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// recordRemoteCIRun 将本次远程执行的身份、结果和派生耗时观察一次性写入 SQLite 权威账本。
// 失败终态只保留生产者真实提供的区间；投影或原子写入失败时直接返回，禁止降级重试。
func recordRemoteCIRun(store *gate.DurationLedgerStore, result RunResult, runErr error) error {
	if store == nil {
		return errors.New("remote CI duration ledger SQLite authority is required")
	}
	provisional := remoteRunStatusRequiresProvisional(result.Status)
	timingObservations, timingErr := remoteRunTimingProjection(result)
	if timingErr != nil && !remoteTimingProjectionErrorAllowed(provisional, timingErr, runErr) {
		return fmt.Errorf("complete remote CI timing projection: %w", timingErr)
	}
	compileTimingObservations, compileErr := remoteCompileTimingObservations(result)
	if compileErr != nil {
		return fmt.Errorf("complete remote CI compile timing projection: %w", compileErr)
	}
	durationSamples, sampleErr := remoteProvisionalDurationSamples(result, runErr)
	if sampleErr != nil && !provisional {
		return sampleErr
	}
	projectionErr := errors.Join(runErr, timingErr, compileErr, sampleErr)
	shardRecords := remoteCIShardRecords(result.Shards)
	record := gate.RemoteCIRunRecord{
		JobID: result.JobID, AcceptedGeneration: result.AcceptedGeneration, ImageCacheSnapshotID: result.ImageCacheSnapshotID, AgentTokenDigest: result.AgentTokenDigest, Force: result.Force,
		Entrypoint: result.Entrypoint, Profile: result.Profile, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest,
		SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, RunnerImage: result.RunnerImage,
		Status: result.Status, Authoritative: false, StartedAt: result.StartedAt, CompletedAt: result.CompletedAt,
		CleanupComplete: result.CleanupComplete, ErrorText: boundedRemoteRunErrorText(projectionErr),
		Shards: shardRecords, Executions: result.GateExecutions,
		WorkloadExecutions: result.FreshWorkloadExecutions, WorkloadResults: remoteCIWorkloadResults(result),
		Warnings: append([]string(nil), result.OptimizationWarnings...), TimingWarnings: append([]gate.RemoteCITimingWarning(nil), result.TimingWarnings...), TimingObservations: timingObservations,
		CompileTimingObservations: compileTimingObservations, DurationSamples: durationSamples,
	}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		return fmt.Errorf("persist remote CI run projection: %w", err)
	}
	return nil
}

// remoteTimingProjectionErrorAllowed 只放行失败终态中可审计的 workload 缺失或 create 占位。
func remoteTimingProjectionErrorAllowed(provisional bool, timingErr, runErr error) bool {
	if !provisional {
		return false
	}
	if remoteFailedTimingProjectionErrorIsWorkloadOmission(timingErr) {
		return true
	}
	return runErr != nil && remoteFailedTimingProjectionErrorIsUncreatedPlaceholderOnly(timingErr)
}

// remoteRunTimingProjection 选择当前终态对应的严格耗时投影实现。
func remoteRunTimingProjection(result RunResult) ([]gate.TimingObservation, error) {
	if remoteRunStatusRequiresProvisional(result.Status) {
		return remoteFailedTimingObservations(result)
	}
	return remoteTimingObservations(result)
}

// remoteProvisionalDurationSamples 失败或非权威终态才把通过账本校验的成功样本交给 provisional 事务。
func remoteProvisionalDurationSamples(result RunResult, runErr error) ([]gate.DurationSample, error) {
	if runErr == nil && !remoteRunStatusRequiresProvisional(result.Status) {
		return nil, nil
	}
	accepted := make([]gate.DurationSample, 0, len(result.DurationSamples))
	var sampleErr error
	for _, sample := range result.DurationSamples {
		if !sample.Succeeded {
			continue
		}
		ledger := gate.NewDurationLedger()
		ledger.Samples = []gate.DurationSample{sample}
		if err := gate.ValidateDurationLedger(ledger); err != nil {
			sampleErr = errors.Join(sampleErr, fmt.Errorf("remote CI provisional duration sample %q is invalid: %w", sample.Bucket.WorkloadID, err))
			continue
		}
		accepted = append(accepted, sample)
	}
	return accepted, sampleErr
}

// remoteRunStatusRequiresProvisional 失败终态即使没有 Go error 也必须保留为非权威 provisional。
func remoteRunStatusRequiresProvisional(status gate.ResultStatus) bool {
	switch status {
	case gate.ResultStatusFailed, gate.ResultStatusCancelled, gate.ResultStatusTimeout,
		gate.ResultStatusInfraFailed, gate.ResultStatusPassedStalePolicy:
		return true
	default:
		return false
	}
}

// remoteFailedTimingObservations 只投影失败运行中仍有完整生产者区间的证据。
// 未创建分片保持缺失，失真区间直接阻断持久化，绝不以零值或 not_applicable 伪造实测结果。
func remoteFailedTimingObservations(result RunResult) ([]gate.TimingObservation, error) {
	assignments, assignmentErr := remoteFailedWorkloadAssignments(result.Shards)
	projection := failedTimingProjection{
		observations: make([]gate.TimingObservation, 0, len(result.Shards)*6+len(result.FreshWorkloadExecutions)*6+1),
		byShard:      make(map[string][]gate.PlanGateExecution, len(result.Shards)),
		firstErr:     assignmentErr,
		structural:   assignmentErr != nil,
	}
	projection.appendWorkloads(result.JobID, remoteTimingWorkloadExecutions(result), assignments)
	compileObservations, compileErr := remoteFailedCompileGroupTimingObservations(result)
	projection.observations = append(projection.observations, compileObservations...)
	if compileErr != nil {
		projection.omitStructural(1, compileErr)
	}
	for _, shard := range result.Shards {
		if remoteFailedShardWasNotCreated(shard) && len(shard.ExecutedWorkloads) != 0 {
			projection.omitUncreatedPlaceholder(fmt.Errorf("remote CI shard %q is an uncreated workload placeholder", shard.ShardIdentity))
		}
	}
	observedShards := remoteFailedObservedShards(result.Shards)
	projection.appendShards(result.JobID, observedShards)
	if len(observedShards) == len(result.Shards) && len(observedShards) != 0 {
		projection.appendRun(result.JobID, observedShards)
	}
	return projection.result()
}

// remoteFailedObservedShards 排除没有创建容器的计划占位；这些分片没有可测时间区间。
func remoteFailedObservedShards(shards []ShardResult) []ShardResult {
	observed := make([]ShardResult, 0, len(shards))
	for _, shard := range shards {
		if !remoteFailedShardWasNotCreated(shard) {
			observed = append(observed, shard)
		}
	}
	return observed
}

// remoteFailedShardWasNotCreated 只接受生产初始化阶段生成的空容器占位。
func remoteFailedShardWasNotCreated(shard ShardResult) bool {
	return remoteFailedShardContainerIsEmpty(shard) &&
		remoteFailedShardTimingIsEmpty(shard) &&
		remoteFailedShardEvidenceIsEmpty(shard)
}

// remoteFailedShardContainerIsEmpty 判断分片没有 provider group 或终态。
func remoteFailedShardContainerIsEmpty(shard ShardResult) bool {
	status := strings.TrimSpace(shard.ContainerStatus)
	return strings.TrimSpace(shard.ContainerGroup) == "" && (status == "" || status == "Unknown")
}

// remoteFailedShardTimingIsEmpty 判断分片没有物化或 ECI 时间证据。
func remoteFailedShardTimingIsEmpty(shard ShardResult) bool {
	return shard.MaterializationTiming.Measurement == gate.MaterializationMeasurementNotMeasured &&
		shard.MaterializationTiming.Validate() == nil && shard.ECIWaitStartedAt.IsZero() &&
		shard.ECIWaitCompletedAt.IsZero() && shard.ECITerminalAt.IsZero()
}

// remoteFailedShardEvidenceIsEmpty 判断分片没有资源、分类或 worker 报告证据。
func remoteFailedShardEvidenceIsEmpty(shard ShardResult) bool {
	return shard.Resources.CPU == 0 && shard.Resources.MemoryGiB == 0 && shard.ResourceClass == "" &&
		len(shard.Report.Gates) == 0 && len(shard.Report.CompileGroupExecutions) == 0
}

type failedTimingProjection struct {
	observations       []gate.TimingObservation
	byShard            map[string][]gate.PlanGateExecution
	skipped            int
	firstErr           error
	structural         bool
	structuralReasons  int
	placeholderReasons int
}

type remoteFailedTimingProjectionError struct {
	omitted            int
	firstErr           error
	structural         bool
	structuralReasons  int
	placeholderReasons int
}

// Error 返回失败投影中被省略的计时区间和首个根因。
func (err *remoteFailedTimingProjectionError) Error() string {
	if err == nil {
		return ""
	}
	if err.firstErr == nil {
		return fmt.Sprintf("partial remote CI timing projection omitted %d invalid intervals", err.omitted)
	}
	return fmt.Sprintf("partial remote CI timing projection omitted %d invalid intervals: %v", err.omitted, err.firstErr)
}

// Unwrap 暴露失败投影的首个底层计时错误，供 errors.Is/As 保留判定能力。
func (err *remoteFailedTimingProjectionError) Unwrap() error { return err.firstErr }

// remoteFailedTimingProjectionErrorIsWorkloadOmission 区分可审计的 workload 缺失计时与结构损坏。
func remoteFailedTimingProjectionErrorIsWorkloadOmission(err error) bool {
	var projectionErr *remoteFailedTimingProjectionError
	return errors.As(err, &projectionErr) && !projectionErr.structural
}

// remoteFailedTimingProjectionErrorIsUncreatedPlaceholderOnly 判断是否只有 create 未返回的分片占位被省略。
func remoteFailedTimingProjectionErrorIsUncreatedPlaceholderOnly(err error) bool {
	var projectionErr *remoteFailedTimingProjectionError
	if !errors.As(err, &projectionErr) || projectionErr.placeholderReasons == 0 {
		return false
	}
	return projectionErr.structuralReasons == projectionErr.placeholderReasons
}

// appendWorkloads 仅收集同时拥有 shard 归属和完整实测阶段的 workload。
func (projection *failedTimingProjection) appendWorkloads(jobID string, executions []gate.PlanGateExecution, assignments map[gate.GateID]string) {
	for _, execution := range executions {
		assigned, err := remoteExecutionShardAssignment(execution, assignments)
		if err != nil {
			projection.omitStructural(1, err)
			continue
		}
		workload, omitted, err := remoteFailedWorkloadTimingObservations(jobID, assigned, execution)
		projection.observations = append(projection.observations, workload...)
		if omitted != 0 && !remoteFailedWorkloadTimingOmissionAllowed(execution, err) {
			projection.structural = true
		}
		projection.omit(omitted, err)
		projection.byShard[assigned] = append(projection.byShard[assigned], execution)
	}
}

// remoteFailedWorkloadTimingOmissionAllowed 仅允许失败或取消 workload 的真实缺失边界。
func remoteFailedWorkloadTimingOmissionAllowed(execution gate.PlanGateExecution, err error) bool {
	if !remoteFailedWorkloadProfileIsNonnegative(execution) || !remoteFailedWorkloadStatusAllowsOmission(execution) {
		return false
	}
	// A failed/cancelled worker may terminate before a boundary or before startup /
	// test-body profiling begins. Missing phases are omitted honestly; malformed
	// positive intervals that exceed the total remain structural errors.
	if remoteFailedWorkloadTimingBoundsMissing(execution) {
		return true
	}
	return err == nil
}

// remoteFailedWorkloadProfileIsNonnegative 拒绝负数 profile，避免把格式损坏当成合法缺失。
func remoteFailedWorkloadProfileIsNonnegative(execution gate.PlanGateExecution) bool {
	profile := execution.ExecutionProfile
	return profile.StartupMS >= 0 && profile.TestBodyMS >= 0 && profile.TotalMS >= 0
}

// remoteFailedWorkloadStatusAllowsOmission 将缺失计时范围限制在失败或取消终态。
func remoteFailedWorkloadStatusAllowsOmission(execution gate.PlanGateExecution) bool {
	return execution.Status == gate.ResultStatusFailed || execution.Status == gate.ResultStatusCancelled
}

// remoteFailedWorkloadTimingBoundsMissing 检测 worker 尚未提供可测边界或阶段 profile 的情况。
func remoteFailedWorkloadTimingBoundsMissing(execution gate.PlanGateExecution) bool {
	return execution.StartedAt.IsZero() || execution.CompletedAt.IsZero() ||
		!execution.CompletedAt.After(execution.StartedAt) ||
		execution.ExecutionProfile.StartupMS == 0 || execution.ExecutionProfile.TestBodyMS == 0
}

// remoteFailedWorkloadTimingObservations 保留失败执行中逐阶段仍有真实边界的 workload 观测。
// startup、test_body、total 各自独立投影；缺失一段不会伪造其它阶段，也不会写 not_applicable。
func remoteFailedWorkloadTimingObservations(jobID, shardIdentity string, execution gate.PlanGateExecution) ([]gate.TimingObservation, int, error) {
	projection := failedWorkloadTimingProjection{
		jobID: jobID, shardIdentity: shardIdentity, gateID: execution.GateID,
		cacheEvidence: gate.NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile),
	}
	projection.appendStartup(execution)
	projection.appendTestBody(execution)
	projection.appendObservation(cicontract.TimingTotal, execution.StartedAt, execution.CompletedAt)
	return projection.observations, projection.omitted, projection.firstErr
}

type failedWorkloadTimingProjection struct {
	jobID         string
	shardIdentity string
	gateID        gate.GateID
	cacheEvidence gate.CacheEvidence
	observations  []gate.TimingObservation
	omitted       int
	firstErr      error
}

func (projection *failedWorkloadTimingProjection) appendStartup(execution gate.PlanGateExecution) {
	durationMS := execution.ExecutionProfile.StartupMS
	if durationMS < 0 {
		projection.omit(fmt.Errorf("remote CI workload %q startup interval is negative", projection.gateID))
		return
	}
	if durationMS == 0 {
		projection.omit(fmt.Errorf("remote CI workload %q startup interval is missing", projection.gateID))
		return
	}
	completedAt := execution.StartedAt.Add(time.Duration(durationMS) * time.Millisecond)
	if !execution.CompletedAt.IsZero() && completedAt.After(execution.CompletedAt) {
		projection.omit(fmt.Errorf("remote CI workload %q startup interval exceeds its total", projection.gateID))
		return
	}
	projection.appendObservation(cicontract.TimingStartup, execution.StartedAt, completedAt)
}

func (projection *failedWorkloadTimingProjection) appendTestBody(execution gate.PlanGateExecution) {
	durationMS := execution.ExecutionProfile.TestBodyMS
	if durationMS < 0 {
		projection.omit(fmt.Errorf("remote CI workload %q test-body interval is negative", projection.gateID))
		return
	}
	if durationMS == 0 {
		projection.omit(fmt.Errorf("remote CI workload %q test-body interval is missing", projection.gateID))
		return
	}
	startedAt := execution.CompletedAt.Add(-time.Duration(durationMS) * time.Millisecond)
	if !execution.StartedAt.IsZero() && startedAt.Before(execution.StartedAt) {
		projection.omit(fmt.Errorf("remote CI workload %q test-body interval exceeds its total", projection.gateID))
		return
	}
	projection.appendObservation(cicontract.TimingTestBody, startedAt, execution.CompletedAt)
}

func (projection *failedWorkloadTimingProjection) appendObservation(phase cicontract.TimingPhase, startedAt, completedAt time.Time) {
	observation, err := timingObservation(projection.jobID, cicontract.TimingScopeWorkload, projection.shardIdentity, projection.gateID, phase, startedAt, completedAt, cicontract.TimingAggregationRaw, projection.cacheEvidence)
	if err != nil {
		projection.omit(fmt.Errorf("remote CI workload %q %s timing: %w", projection.gateID, phase, err))
		return
	}
	projection.observations = append(projection.observations, observation)
}

func (projection *failedWorkloadTimingProjection) omit(err error) {
	projection.omitted++
	if projection.firstErr == nil {
		projection.firstErr = err
	}
}

// appendShards 只投影失败 shard 中仍可证明的基础设施与 workload 并集区间。
func (projection *failedTimingProjection) appendShards(jobID string, shards []ShardResult) {
	for _, shard := range shards {
		if strings.TrimSpace(shard.ShardIdentity) == "" {
			continue
		}
		infrastructure, omitted, err := remoteFailedShardInfrastructureObservations(jobID, shard)
		projection.observations = append(projection.observations, infrastructure...)
		projection.omitStructural(omitted, err)
		executions := projection.byShard[shard.ShardIdentity]
		if err := validateShardWorkloadCoverage(shard, executions); err != nil {
			projection.omitStructural(1, err)
			continue
		}
		measuredExecutions := measuredRemoteWorkloadExecutions(executions)
		if len(measuredExecutions) == 0 {
			continue
		}
		shardWorkload, err := remoteShardWorkloadObservations(jobID, shard, measuredExecutions)
		if err != nil {
			projection.omitStructural(1, err)
			continue
		}
		projection.observations = append(projection.observations, shardWorkload...)
	}
}

// measuredRemoteWorkloadExecutions 过滤失败/取消执行中没有完整三段边界的 workload，
// 让分片聚合只消费可证明的实测区间；覆盖校验仍由 validateShardWorkloadCoverage 完成。
func measuredRemoteWorkloadExecutions(executions []gate.PlanGateExecution) []gate.PlanGateExecution {
	measured := make([]gate.PlanGateExecution, 0, len(executions))
	for _, execution := range executions {
		if _, _, _, _, err := workloadPhaseIntervals(execution); err == nil {
			measured = append(measured, execution)
		}
	}
	return measured
}

// appendRun 在所有 shard 仍形成合法关键路径时保留 run 汇总区间。
func (projection *failedTimingProjection) appendRun(jobID string, shards []ShardResult) {
	observation, err := remoteRunTimingObservation(jobID, shards)
	if err != nil {
		projection.omitStructural(1, err)
		return
	}
	projection.observations = append(projection.observations, observation)
}

// omit 累计被拒绝的失真区间，并保留首个根因供 SQLite error_text 诊断。
func (projection *failedTimingProjection) omit(count int, err error) {
	projection.skipped += count
	if projection.firstErr == nil && err != nil {
		projection.firstErr = err
	}
}

// omitStructural 标记结构性投影错误；该错误禁止把失败 run 当成可复用证据。
func (projection *failedTimingProjection) omitStructural(count int, err error) {
	if count == 0 && err == nil {
		return
	}
	projection.structural = true
	projection.structuralReasons++
	projection.omit(count, err)
}

// omitUncreatedPlaceholder 记录未创建分片占位，允许 create 失败终态保留其审计行。
func (projection *failedTimingProjection) omitUncreatedPlaceholder(err error) {
	projection.placeholderReasons++
	projection.omitStructural(1, err)
}

// result 返回真实观测，并把所有缺失区间压缩为单个可持久化错误。
func (projection *failedTimingProjection) result() ([]gate.TimingObservation, error) {
	if projection.skipped == 0 && projection.firstErr == nil {
		return projection.observations, nil
	}
	return projection.observations, &remoteFailedTimingProjectionError{omitted: projection.skipped, firstErr: projection.firstErr, structural: projection.structural, structuralReasons: projection.structuralReasons, placeholderReasons: projection.placeholderReasons}
}

// remoteFailedWorkloadAssignments 保留唯一且有稳定分片身份的 workload 归属。
func remoteFailedWorkloadAssignments(shards []ShardResult) (map[gate.GateID]string, error) {
	assignments := make(map[gate.GateID]string)
	invalid := make(map[gate.GateID]bool)
	var firstErr error
	for _, shard := range shards {
		if strings.TrimSpace(shard.ShardIdentity) == "" {
			continue
		}
		for _, workload := range shard.ExecutedWorkloads {
			if prior, duplicate := assignments[workload]; duplicate && prior != shard.ShardIdentity {
				invalid[workload] = true
				if firstErr == nil {
					firstErr = fmt.Errorf("remote CI workload %q is assigned to shards %q and %q", workload, prior, shard.ShardIdentity)
				}
				continue
			}
			assignments[workload] = shard.ShardIdentity
		}
	}
	for workload := range invalid {
		delete(assignments, workload)
	}
	return assignments, firstErr
}

// remoteFailedShardInfrastructureObservations 独立保留每个有效基础设施区间。
func remoteFailedShardInfrastructureObservations(jobID string, shard ShardResult) ([]gate.TimingObservation, int, error) {
	items := []timingInterval{
		{phase: cicontract.TimingECIWait, started: shard.ECIWaitStartedAt, completed: shard.ECIWaitCompletedAt},
		{phase: cicontract.TimingSourceMaterialize, started: time.UnixMilli(shard.MaterializationTiming.Source.StartedAtUnixMS), completed: time.UnixMilli(shard.MaterializationTiming.Source.CompletedAtUnixMS)},
		{phase: cicontract.TimingCandidateCompile, started: time.UnixMilli(shard.MaterializationTiming.CandidateCompile.StartedAtUnixMS), completed: time.UnixMilli(shard.MaterializationTiming.CandidateCompile.CompletedAtUnixMS)},
	}
	observations := make([]gate.TimingObservation, 0, len(items))
	omitted := 0
	var firstErr error
	for _, item := range items {
		observation, err := timingObservation(jobID, cicontract.TimingScopeShard, shard.ShardIdentity, "", item.phase, item.started, item.completed, cicontract.TimingAggregationRaw, gate.NewNotApplicableCacheEvidence("not_workload_cache_evidence"))
		if err != nil {
			omitted++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		observations = append(observations, observation)
	}
	return observations, omitted, firstErr
}

// remoteTimingObservations 从已绑定的分片与 workload 执行记录生成账本唯一接受的耗时观察。
// 缺失、重复或跨分片的生产者区间必须报错，不能重写为 not_applicable。
func remoteTimingObservations(result RunResult) ([]gate.TimingObservation, error) {
	if len(result.FreshWorkloadExecutions) == 0 && len(result.Shards) == 0 {
		return nil, nil
	}
	executions := remoteTimingWorkloadExecutions(result)
	assignments, err := remoteWorkloadAssignments(result.Shards)
	if err != nil {
		return nil, err
	}
	workloadObservations, byShard, err := remoteWorkloadTimingObservations(result.JobID, executions, result.Shards, assignments)
	if err != nil {
		return nil, err
	}
	shardObservations, err := remoteShardTimingObservations(result, byShard)
	if err != nil {
		return nil, err
	}
	compileObservations, err := remoteCompileGroupTimingObservations(result)
	if err != nil {
		return nil, err
	}
	runObservation, err := remoteRunTimingObservation(result.JobID, result.Shards)
	if err != nil {
		return nil, err
	}
	observations := append(workloadObservations, shardObservations...)
	observations = append(observations, runObservation)
	return append(observations, compileObservations...), nil
}

// remoteTimingWorkloadExecutions 优先只记录本次 fresh 执行；直接计时单测保留完整 workload 输入。
func remoteTimingWorkloadExecutions(result RunResult) []gate.PlanGateExecution {
	if len(result.FreshWorkloadExecutions) != 0 {
		return result.FreshWorkloadExecutions
	}
	return result.WorkloadExecutions
}

// remoteWorkloadAssignments 确保每个 workload 只归属一个已执行分片。
func remoteWorkloadAssignments(shards []ShardResult) (map[gate.GateID]string, error) {
	assignments := make(map[gate.GateID]string)
	for _, shard := range shards {
		if shard.ShardIdentity == "" {
			return nil, errors.New("remote CI shard timing identity is required")
		}
		for _, workload := range shard.ExecutedWorkloads {
			if prior, duplicate := assignments[workload]; duplicate {
				return nil, fmt.Errorf("remote CI workload %q is assigned to shards %q and %q", workload, prior, shard.ShardIdentity)
			}
			assignments[workload] = shard.ShardIdentity
		}
	}
	return assignments, nil
}

// remoteWorkloadTimingObservations 生成 workload 原始区间并按分片聚合其执行记录。
func remoteWorkloadTimingObservations(jobID string, executions []gate.PlanGateExecution, shards []ShardResult, assignments map[gate.GateID]string) ([]gate.TimingObservation, map[string][]gate.PlanGateExecution, error) {
	observations := make([]gate.TimingObservation, 0, len(shards)*6+len(executions)*6+1)
	byShard := make(map[string][]gate.PlanGateExecution, len(shards))
	for _, execution := range executions {
		assigned, err := remoteExecutionShardAssignment(execution, assignments)
		if err != nil {
			return nil, nil, err
		}
		workloadObservations, err := remoteSingleWorkloadTimingObservations(jobID, assigned, execution)
		if err != nil {
			return nil, nil, err
		}
		observations = append(observations, workloadObservations...)
		byShard[assigned] = append(byShard[assigned], execution)
	}
	return observations, byShard, nil
}

// remoteExecutionShardAssignment 校验 workload 执行报告与分片声明拥有相同身份。
func remoteExecutionShardAssignment(execution gate.PlanGateExecution, assignments map[gate.GateID]string) (string, error) {
	assigned, ok := assignments[execution.GateID]
	if !ok || execution.ShardIdentity == "" || execution.ShardIdentity != assigned {
		return "", fmt.Errorf("remote CI workload %q shard identity is not bound to its executed shard", execution.GateID)
	}
	return assigned, nil
}

// remoteSingleWorkloadTimingObservations 生成一个 workload 的非适用基础设施区间和可测执行区间。
func remoteSingleWorkloadTimingObservations(jobID, shardIdentity string, execution gate.PlanGateExecution) ([]gate.TimingObservation, error) {
	startupStart, startupEnd, bodyStart, bodyEnd, err := workloadPhaseIntervals(execution)
	if err != nil {
		return nil, err
	}
	cacheEvidence := gate.NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)
	observations := remoteNotApplicableWorkloadObservations(jobID, shardIdentity, execution.GateID, cacheEvidence)
	for _, item := range []timingInterval{
		{phase: cicontract.TimingStartup, started: startupStart, completed: startupEnd},
		{phase: cicontract.TimingTestBody, started: bodyStart, completed: bodyEnd},
		{phase: cicontract.TimingTotal, started: execution.StartedAt, completed: execution.CompletedAt},
	} {
		observation, err := timingObservation(jobID, cicontract.TimingScopeWorkload, shardIdentity, execution.GateID, item.phase, item.started, item.completed, cicontract.TimingAggregationRaw, cacheEvidence)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

// remoteNotApplicableWorkloadObservations 标记 workload 不拥有基础设施阶段的缓存证据。
func remoteNotApplicableWorkloadObservations(jobID, shardIdentity string, workloadID gate.GateID, cacheEvidence gate.CacheEvidence) []gate.TimingObservation {
	return []gate.TimingObservation{
		notApplicableWorkloadObservation(jobID, shardIdentity, workloadID, cicontract.TimingECIWait, cacheEvidence),
		notApplicableWorkloadObservation(jobID, shardIdentity, workloadID, cicontract.TimingSourceMaterialize, cacheEvidence),
		notApplicableWorkloadObservation(jobID, shardIdentity, workloadID, cicontract.TimingCandidateCompile, cacheEvidence),
	}
}

// validateShardWorkloadCoverage 要求聚合前完整覆盖分片声明的 workload，并验证每个执行的三段区间。
func validateShardWorkloadCoverage(shard ShardResult, executions []gate.PlanGateExecution) error {
	if strings.TrimSpace(shard.ShardIdentity) == "" {
		return errors.New("remote CI shard timing identity is required")
	}
	expected, err := expectedShardWorkloads(shard)
	if err != nil {
		return err
	}
	if len(expected) == 0 || len(executions) != len(expected) {
		return fmt.Errorf("remote CI shard %q workload timing coverage is incomplete: expected=%d observed=%d", shard.ShardIdentity, len(expected), len(executions))
	}
	return validateShardWorkloadExecutions(shard, executions, expected)
}

func expectedShardWorkloads(shard ShardResult) (map[gate.GateID]struct{}, error) {
	expected := make(map[gate.GateID]struct{}, len(shard.ExecutedWorkloads))
	for _, workloadID := range shard.ExecutedWorkloads {
		if strings.TrimSpace(string(workloadID)) == "" {
			return nil, fmt.Errorf("remote CI shard %q declares an empty workload", shard.ShardIdentity)
		}
		if _, duplicate := expected[workloadID]; duplicate {
			return nil, fmt.Errorf("remote CI shard %q declares workload %q more than once", shard.ShardIdentity, workloadID)
		}
		expected[workloadID] = struct{}{}
	}
	return expected, nil
}

// validateShardWorkloadExecutions 校验每个 workload 的分片归属、声明覆盖和完整三段区间。
func validateShardWorkloadExecutions(shard ShardResult, executions []gate.PlanGateExecution, expected map[gate.GateID]struct{}) error {
	for _, execution := range executions {
		if execution.ShardIdentity != shard.ShardIdentity {
			return fmt.Errorf("remote CI shard %q workload %q has mismatched shard identity", shard.ShardIdentity, execution.GateID)
		}
		if _, exists := expected[execution.GateID]; !exists {
			return fmt.Errorf("remote CI shard %q workload %q is not declared", shard.ShardIdentity, execution.GateID)
		}
		delete(expected, execution.GateID)
		if _, _, _, _, err := workloadPhaseIntervals(execution); err != nil && !remoteFailedWorkloadTimingOmissionAllowed(execution, err) {
			return fmt.Errorf("remote CI shard %q workload %q timing is invalid: %w", shard.ShardIdentity, execution.GateID, err)
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("remote CI shard %q workload timing coverage is missing %d executions", shard.ShardIdentity, len(expected))
	}
	return nil
}

// remoteShardTimingObservations 生成分片基础设施区间和其 workload 汇总区间。
func remoteShardTimingObservations(result RunResult, byShard map[string][]gate.PlanGateExecution) ([]gate.TimingObservation, error) {
	observations := make([]gate.TimingObservation, 0, len(result.Shards)*6)
	for _, shard := range result.Shards {
		executions := byShard[shard.ShardIdentity]
		if err := validateShardWorkloadCoverage(shard, executions); err != nil {
			return nil, err
		}
		shardObservations, err := remoteSingleShardTimingObservations(result.JobID, shard, executions)
		if err != nil {
			return nil, err
		}
		observations = append(observations, shardObservations...)
	}
	return observations, nil
}

// remoteSingleShardTimingObservations 以分片报告和归并后的 workload 区间构造六项原始观察。
func remoteSingleShardTimingObservations(jobID string, shard ShardResult, executions []gate.PlanGateExecution) ([]gate.TimingObservation, error) {
	infrastructure, err := remoteShardInfrastructureObservations(jobID, shard)
	if err != nil {
		return nil, err
	}
	workload, err := remoteShardWorkloadObservations(jobID, shard, executions)
	if err != nil {
		return nil, err
	}
	return append(infrastructure, workload...), nil
}

// remoteShardInfrastructureObservations 记录 ECI 等待、源码物化和候选 CLI 编译的分片原始区间。
func remoteShardInfrastructureObservations(jobID string, shard ShardResult) ([]gate.TimingObservation, error) {
	observations := make([]gate.TimingObservation, 0, 3)
	for _, item := range []timingInterval{
		{phase: cicontract.TimingECIWait, started: shard.ECIWaitStartedAt, completed: shard.ECIWaitCompletedAt},
		{phase: cicontract.TimingSourceMaterialize, started: time.UnixMilli(shard.MaterializationTiming.Source.StartedAtUnixMS), completed: time.UnixMilli(shard.MaterializationTiming.Source.CompletedAtUnixMS)},
		{phase: cicontract.TimingCandidateCompile, started: time.UnixMilli(shard.MaterializationTiming.CandidateCompile.StartedAtUnixMS), completed: time.UnixMilli(shard.MaterializationTiming.CandidateCompile.CompletedAtUnixMS)},
	} {
		observation, err := timingObservation(jobID, cicontract.TimingScopeShard, shard.ShardIdentity, "", item.phase, item.started, item.completed, cicontract.TimingAggregationRaw, gate.NewNotApplicableCacheEvidence("not_workload_cache_evidence"))
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

// remoteShardWorkloadObservations 将 workload 启动、测试主体和分片总区间投影为汇总观察。
func remoteShardWorkloadObservations(jobID string, shard ShardResult, executions []gate.PlanGateExecution) ([]gate.TimingObservation, error) {
	startup, body, err := shardWorkloadIntervals(executions)
	if err != nil {
		return nil, fmt.Errorf("remote CI shard %q workload intervals: %w", shard.ShardIdentity, err)
	}
	return remoteShardSummaryObservations(jobID, shard, startup, body)
}

// remoteShardSummaryObservations 用已经验证的总时长替换区间端点相减的聚合时长；provider 终态低精度时保留 worker 实测边界。
func remoteShardSummaryObservations(jobID string, shard ShardResult, startup, body phaseIntervalUnion) ([]gate.TimingObservation, error) {
	if shard.ECIWaitStartedAt.IsZero() || shard.ECITerminalAt.IsZero() || !shard.ECITerminalAt.After(shard.ECIWaitStartedAt) {
		return nil, fmt.Errorf("remote CI shard %q total interval is missing or invalid", shard.ShardIdentity)
	}
	observations := make([]gate.TimingObservation, 0, 3)
	totalCompletedAt := shard.ECITerminalAt
	if startup.completedAt.After(totalCompletedAt) {
		totalCompletedAt = startup.completedAt
	}
	if body.completedAt.After(totalCompletedAt) {
		totalCompletedAt = body.completedAt
	}
	for _, item := range []timingSummary{
		{phase: cicontract.TimingStartup, started: startup.startedAt, completed: startup.completedAt, aggregation: cicontract.TimingAggregationIntervalUnion, durationMS: startup.durationMS},
		{phase: cicontract.TimingTestBody, started: body.startedAt, completed: body.completedAt, aggregation: cicontract.TimingAggregationIntervalUnion, durationMS: body.durationMS},
		{phase: cicontract.TimingTotal, started: shard.ECIWaitStartedAt, completed: totalCompletedAt, aggregation: cicontract.TimingAggregationCriticalPath, durationMS: totalCompletedAt.Sub(shard.ECIWaitStartedAt).Milliseconds()},
	} {
		observation, err := timingObservation(jobID, cicontract.TimingScopeShard, shard.ShardIdentity, "", item.phase, item.started, item.completed, item.aggregation, gate.NewNotApplicableCacheEvidence("not_workload_cache_evidence"))
		if err != nil {
			return nil, err
		}
		observation.DurationMS = item.durationMS
		if err := observation.Validate(); err != nil {
			return nil, fmt.Errorf("remote CI shard %q %s duration: %w", shard.ShardIdentity, item.phase, err)
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

// remoteRunTimingObservation 以全体分片的最早等待和最晚终态形成运行关键路径。
func remoteRunTimingObservation(jobID string, shards []ShardResult) (gate.TimingObservation, error) {
	startedAt, completedAt, err := remoteRunCriticalPathEnvelope(shards)
	if err != nil {
		return gate.TimingObservation{}, err
	}
	return timingObservation(jobID, cicontract.TimingScopeRun, "", "", cicontract.TimingTotal, startedAt, completedAt, cicontract.TimingAggregationCriticalPath, gate.NewNotApplicableCacheEvidence("not_workload_cache_evidence"))
}

type timingInterval struct {
	phase              cicontract.TimingPhase
	started, completed time.Time
}

type timingSummary struct {
	phase              cicontract.TimingPhase
	started, completed time.Time
	aggregation        cicontract.TimingAggregation
	durationMS         int64
}

// remoteRunCriticalPathEnvelope 仅以全部已观测分片的总区间推导运行关键路径。
// 最早 ECI 等待起点和最晚终态共同证明运行总时长未遗漏任何分片。
func remoteRunCriticalPathEnvelope(shards []ShardResult) (time.Time, time.Time, error) {
	if len(shards) == 0 {
		return time.Time{}, time.Time{}, errors.New("remote CI run has no shard totals")
	}
	var startedAt, completedAt time.Time
	for _, shard := range shards {
		if shard.ECIWaitStartedAt.IsZero() || shard.ECITerminalAt.IsZero() || !shard.ECITerminalAt.After(shard.ECIWaitStartedAt) {
			return time.Time{}, time.Time{}, fmt.Errorf("remote CI shard %q total interval is missing or invalid", shard.ShardIdentity)
		}
		if startedAt.IsZero() || shard.ECIWaitStartedAt.Before(startedAt) {
			startedAt = shard.ECIWaitStartedAt
		}
		if completedAt.IsZero() || shard.ECITerminalAt.After(completedAt) {
			completedAt = shard.ECITerminalAt
		}
	}
	return startedAt, completedAt, nil
}

// notApplicableWorkloadObservation 为 workload 没有所有权的基础设施阶段生成非适用观察。
func notApplicableWorkloadObservation(jobID, shardIdentity string, workloadID gate.GateID, phase cicontract.TimingPhase, cacheEvidence gate.CacheEvidence) gate.TimingObservation {
	return gate.TimingObservation{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: shardIdentity, WorkloadID: workloadID, Phase: phase, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + shardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: cacheEvidence}
}

// timingObservation 构造并验证一个具有真实边界的耗时观察。
func timingObservation(jobID string, scope cicontract.TimingScope, shardIdentity string, workloadID gate.GateID, phase cicontract.TimingPhase, startedAt, completedAt time.Time, aggregation cicontract.TimingAggregation, cacheEvidence gate.CacheEvidence) (gate.TimingObservation, error) {
	observation := gate.TimingObservation{JobID: jobID, Scope: scope, ShardIdentity: shardIdentity, WorkloadID: workloadID, Phase: phase, StartedAt: startedAt.UTC(), CompletedAt: completedAt.UTC(), DurationMS: completedAt.Sub(startedAt).Milliseconds(), Measurement: cicontract.ObservationMeasured, Aggregation: aggregation, CacheEvidence: cacheEvidence}
	if err := observation.Validate(); err != nil {
		return gate.TimingObservation{}, fmt.Errorf("remote CI %s %s timing interval is missing or invalid: %w", scope, phase, err)
	}
	return observation, nil
}

// workloadPhaseIntervals 从 workload 总区间及其受测 profile 还原启动和测试主体区间。
// 两段区间超出总区间即表示远程计时失真，必须拒绝写入账本。
func workloadPhaseIntervals(execution gate.PlanGateExecution) (time.Time, time.Time, time.Time, time.Time, error) {
	if execution.StartedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) || execution.ExecutionProfile.StartupMS <= 0 || execution.ExecutionProfile.TestBodyMS <= 0 {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf(
			"remote CI workload %q measured startup and test-body intervals are required: status=%s startup_ms=%d test_body_ms=%d total_ms=%d",
			execution.GateID, execution.Status, execution.ExecutionProfile.StartupMS, execution.ExecutionProfile.TestBodyMS, execution.ExecutionProfile.TotalMS,
		)
	}
	startupEnd := execution.StartedAt.Add(time.Duration(execution.ExecutionProfile.StartupMS) * time.Millisecond)
	bodyStart := execution.CompletedAt.Add(-time.Duration(execution.ExecutionProfile.TestBodyMS) * time.Millisecond)
	if startupEnd.After(execution.CompletedAt) || bodyStart.Before(execution.StartedAt) {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI workload %q measured phase interval exceeds total", execution.GateID)
	}
	return execution.StartedAt, startupEnd, bodyStart, execution.CompletedAt, nil
}

// phaseIntervalUnion 保存一组可能重叠区间的边界与并集时长。
type phaseIntervalUnion struct {
	startedAt, completedAt time.Time
	durationMS             int64
}

// shardWorkloadIntervals 合并同一分片全部 workload 的启动和测试主体区间。
func shardWorkloadIntervals(executions []gate.PlanGateExecution) (phaseIntervalUnion, phaseIntervalUnion, error) {
	var startupIntervals, bodyIntervals []phaseIntervalUnion
	for _, execution := range executions {
		sStart, sEnd, bStart, bEnd, err := workloadPhaseIntervals(execution)
		if err != nil {
			return phaseIntervalUnion{}, phaseIntervalUnion{}, err
		}
		startupIntervals = append(startupIntervals, phaseIntervalUnion{startedAt: sStart, completedAt: sEnd})
		bodyIntervals = append(bodyIntervals, phaseIntervalUnion{startedAt: bStart, completedAt: bEnd})
	}
	startup, err := mergeWorkloadIntervals(startupIntervals)
	if err != nil {
		return phaseIntervalUnion{}, phaseIntervalUnion{}, err
	}
	body, err := mergeWorkloadIntervals(bodyIntervals)
	if err != nil {
		return phaseIntervalUnion{}, phaseIntervalUnion{}, err
	}
	return startup, body, nil
}

// mergeWorkloadIntervals 计算重叠 workload 区间的并集边界和总毫秒数。
func mergeWorkloadIntervals(intervals []phaseIntervalUnion) (phaseIntervalUnion, error) {
	if len(intervals) == 0 {
		return phaseIntervalUnion{}, errors.New("no workload phase intervals")
	}
	sort.Slice(intervals, func(left, right int) bool { return intervals[left].startedAt.Before(intervals[right].startedAt) })
	merged := phaseIntervalUnion{startedAt: intervals[0].startedAt, completedAt: intervals[0].completedAt}
	segmentStart, segmentEnd := merged.startedAt, merged.completedAt
	for _, interval := range intervals[1:] {
		if interval.startedAt.After(segmentEnd) {
			merged.durationMS += segmentEnd.Sub(segmentStart).Milliseconds()
			segmentStart, segmentEnd = interval.startedAt, interval.completedAt
			continue
		}
		if interval.completedAt.After(segmentEnd) {
			segmentEnd = interval.completedAt
		}
	}
	merged.durationMS += segmentEnd.Sub(segmentStart).Milliseconds()
	merged.completedAt = segmentEnd
	return merged, nil
}
