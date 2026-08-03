package remoteci

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// recordRemoteCIRun 将本次远程执行的身份、结果和派生耗时观察一次性写入 SQLite 权威账本。
// 任一缺失或失真的生产者区间都会阻止持久化，不能以非适用观测掩盖。
func recordRemoteCIRun(store *gate.DurationLedgerStore, result RunResult, runErr error) error {
	if store == nil {
		return errors.New("remote CI duration ledger SQLite authority is required")
	}
	timingObservations, err := remoteTimingObservations(result)
	if err != nil {
		return err
	}
	return store.RecordProvisionalRemoteCIRun(gate.RemoteCIRunRecord{
		JobID: result.JobID, AcceptedGeneration: result.AcceptedGeneration, ImageCacheSnapshotID: result.ImageCacheSnapshotID, AgentTokenDigest: result.AgentTokenDigest,
		Entrypoint: result.Entrypoint, Profile: result.Profile, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest,
		SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, RunnerImage: result.RunnerImage,
		Status: result.Status, Authoritative: false, StartedAt: result.StartedAt, CompletedAt: result.CompletedAt,
		CleanupComplete: result.CleanupComplete, ErrorText: boundedRemoteRunErrorText(runErr),
		Shards: remoteCIShardRecords(result.Shards), Executions: result.GateExecutions,
		WorkloadExecutions: result.FreshWorkloadExecutions, WorkloadResults: remoteCIWorkloadResults(result),
		Warnings: append([]string(nil), result.OptimizationWarnings...), TimingWarnings: append([]gate.RemoteCITimingWarning(nil), result.TimingWarnings...), TimingObservations: timingObservations,
	})
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
	runObservation, err := remoteRunTimingObservation(result.JobID, result.Shards)
	if err != nil {
		return nil, err
	}
	return append(append(workloadObservations, shardObservations...), runObservation), nil
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

// remoteShardTimingObservations 生成分片基础设施区间和其 workload 汇总区间。
func remoteShardTimingObservations(result RunResult, byShard map[string][]gate.PlanGateExecution) ([]gate.TimingObservation, error) {
	observations := make([]gate.TimingObservation, 0, len(result.Shards)*6)
	for _, shard := range result.Shards {
		executions := byShard[shard.ShardIdentity]
		if len(executions) == 0 {
			return nil, fmt.Errorf("remote CI shard %q has no measured workload intervals", shard.ShardIdentity)
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

// remoteShardSummaryObservations 用已经验证的总时长替换区间端点相减的聚合时长。
func remoteShardSummaryObservations(jobID string, shard ShardResult, startup, body phaseIntervalUnion) ([]gate.TimingObservation, error) {
	observations := make([]gate.TimingObservation, 0, 3)
	for _, item := range []timingSummary{
		{phase: cicontract.TimingStartup, started: startup.startedAt, completed: startup.completedAt, aggregation: cicontract.TimingAggregationIntervalUnion, durationMS: startup.durationMS},
		{phase: cicontract.TimingTestBody, started: body.startedAt, completed: body.completedAt, aggregation: cicontract.TimingAggregationIntervalUnion, durationMS: body.durationMS},
		{phase: cicontract.TimingTotal, started: shard.ECIWaitStartedAt, completed: shard.ECITerminalAt, aggregation: cicontract.TimingAggregationCriticalPath, durationMS: shard.ECITerminalAt.Sub(shard.ECIWaitStartedAt).Milliseconds()},
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
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI workload %q measured startup and test-body intervals are required", execution.GateID)
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
