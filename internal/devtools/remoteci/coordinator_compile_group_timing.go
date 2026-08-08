package remoteci

import (
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteCompileGroupExecutions 仅收集 fresh worker 观测；复用 workload 没有分片报告，不能进入账本。
func remoteCompileGroupExecutions(result RunResult) ([]gate.CompileGroupExecution, error) {
	var executions []gate.CompileGroupExecution
	seen := make(map[string]struct{})
	var firstErr error
	for _, shard := range result.Shards {
		shardExecutions := remoteCompileGroupExecutionsForShard(shard, seen)
		executions = append(executions, shardExecutions.executions...)
		if firstErr == nil {
			firstErr = shardExecutions.firstErr
		}
	}
	return executions, firstErr
}

type compileGroupExecutionCollection struct {
	executions []gate.CompileGroupExecution
	firstErr   error
}

// remoteCompileGroupExecutionsForShard 按分片严格校验并去重 worker 报告的编译执行。
func remoteCompileGroupExecutionsForShard(shard ShardResult, seen map[string]struct{}) compileGroupExecutionCollection {
	collection := compileGroupExecutionCollection{}
	if len(shard.Report.CompileGroupExecutions) == 0 {
		return collection
	}
	if shard.ShardIdentity == "" {
		collection.firstErr = errors.New("compile group report has no shard identity")
		return collection
	}
	for _, execution := range shard.Report.CompileGroupExecutions {
		if err := validateCompileGroupExecutionForShard(shard, execution); err != nil {
			if collection.firstErr == nil {
				collection.firstErr = err
			}
			continue
		}
		key := compileGroupExecutionKey(shard, execution)
		if _, duplicate := seen[key]; duplicate {
			if collection.firstErr == nil {
				collection.firstErr = fmt.Errorf("shard %q compile group %q is duplicated", shard.ShardIdentity, execution.GroupID)
			}
			continue
		}
		seen[key] = struct{}{}
		collection.executions = append(collection.executions, execution)
	}
	return collection
}

func validateCompileGroupExecutionForShard(shard ShardResult, execution gate.CompileGroupExecution) error {
	if err := execution.Validate(); err != nil {
		return fmt.Errorf("shard %q compile group %q: %w", shard.ShardIdentity, execution.GroupID, err)
	}
	if execution.ResourceClassID != shard.ResourceClass {
		return fmt.Errorf("shard %q compile group %q resource class drifted", shard.ShardIdentity, execution.GroupID)
	}
	return nil
}

func compileGroupExecutionKey(shard ShardResult, execution gate.CompileGroupExecution) string {
	return shard.ShardIdentity + "\x00" + execution.GroupID + "\x00" + execution.ArtifactKey
}

// remoteCompileGroupTimingObservations 将每个 fresh 编译执行投影为独立的
// test_binary_compile 区间，不改变 shard/workload envelope，避免重复计时。
func remoteCompileGroupTimingObservations(result RunResult) ([]gate.TimingObservation, error) {
	if !remoteRunHasCompileGroupExecutions(result) {
		return nil, nil
	}
	if result.ExecutionMode != gate.DurationExecutionModeNormal && result.ExecutionMode != gate.DurationExecutionModeCalibration {
		return nil, errors.New("compile group timing execution mode is required")
	}
	observations := make([]gate.TimingObservation, 0)
	seen := make(map[string]struct{})
	for _, shard := range result.Shards {
		for _, execution := range shard.Report.CompileGroupExecutions {
			include, err := remoteCompileGroupTimingExecutionEligibility(result, shard, execution)
			if err != nil {
				return nil, err
			}
			if !include {
				continue
			}
			key := compileGroupExecutionKey(shard, execution)
			if _, duplicate := seen[key]; duplicate {
				return nil, fmt.Errorf("shard %q compile group %q is duplicated", shard.ShardIdentity, execution.GroupID)
			}
			seen[key] = struct{}{}
			observation, err := remoteCompileGroupTimingObservation(result, shard, execution)
			if err != nil {
				return nil, fmt.Errorf("shard %q compile group %q timing: %w", shard.ShardIdentity, execution.GroupID, err)
			}
			observations = append(observations, observation)
		}
	}
	return observations, nil
}

func remoteRunHasCompileGroupExecutions(result RunResult) bool {
	for _, shard := range result.Shards {
		if len(shard.Report.CompileGroupExecutions) != 0 {
			return true
		}
	}
	return false
}

// remoteCompileGroupTimingObservation 将单个 compile-group execution 转为 SQLite timing row。
func remoteCompileGroupTimingObservation(result RunResult, shard ShardResult, execution gate.CompileGroupExecution) (gate.TimingObservation, error) {
	cacheEvidence := gate.NewCompileGroupCacheEvidence(execution)
	observation := gate.TimingObservation{
		JobID: result.JobID, Scope: cicontract.TimingScopeCompileGroup, ShardIdentity: shard.ShardIdentity,
		Phase: cicontract.TimingTestBinaryCompile, StartedAt: time.UnixMilli(execution.StartedAtUnixMS).UTC(),
		CompletedAt: time.UnixMilli(execution.CompletedAtUnixMS).UTC(), DurationMS: execution.DurationMS,
		Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw,
		CacheEvidence:  cacheEvidence,
		CompileGroupID: execution.GroupID, CompileArtifactKey: execution.ArtifactKey,
		CompilePackageTarget: execution.PackageTarget, CompileWorkloadIDs: append([]gate.GateID(nil), execution.WorkloadIDs...),
		CompileArtifactSHA256: execution.ArtifactSHA256, CompileArtifactSize: execution.ArtifactSize,
		CompileCacheHits: execution.CacheHits, CompileCacheMisses: execution.CacheMisses, CompileCachePuts: execution.CachePuts,
		CompileCacheStatus: string(cacheEvidence.Go.Status), CompileStatus: string(execution.Status), CompileExitCode: execution.ExitCode,
		CompileErrorText: execution.ErrorText, CompileCommandDigest: execution.CompileCommandDigest, CompileProfileDigest: execution.ProfileDigest,
		CompileResourceClassID: execution.ResourceClassID, CompileResourceCPU: shard.Resources.CPU, CompileResourceMemoryGiB: shard.Resources.MemoryGiB,
		CompileExecutionMode: result.ExecutionMode,
	}
	return observation, observation.Validate()
}

// remoteCompileGroupTimingExecutionEligibility 保留失败终态的真实测量边界并拒绝失真 execution。
func remoteCompileGroupTimingExecutionEligibility(result RunResult, shard ShardResult, execution gate.CompileGroupExecution) (bool, error) {
	if err := validateCompileGroupExecutionForShard(shard, execution); err != nil {
		return false, err
	}
	if execution.HasMeasuredObservation() {
		return true, nil
	}
	if remoteRunStatusRequiresProvisional(result.Status) {
		// Preparation or Cmd.Start failed before go test -c existed; retain only the bounded failure record.
		return false, nil
	}
	return false, fmt.Errorf("shard %q compile group %q has no measured test_binary_compile observation", shard.ShardIdentity, execution.GroupID)
}

// remoteFailedCompileGroupTimingObservations 在失败运行中保留所有有效编译行，
// 并将畸形行记录到 provisional error_text。
func remoteFailedCompileGroupTimingObservations(result RunResult) ([]gate.TimingObservation, error) {
	if !remoteRunHasCompileGroupExecutions(result) {
		return nil, nil
	}
	if result.ExecutionMode != gate.DurationExecutionModeNormal && result.ExecutionMode != gate.DurationExecutionModeCalibration {
		return nil, errors.New("compile group timing execution mode is required")
	}
	observations := make([]gate.TimingObservation, 0)
	seen := make(map[string]struct{})
	var firstErr error
	for _, shard := range result.Shards {
		for _, execution := range shard.Report.CompileGroupExecutions {
			observation, include, err := remoteFailedCompileGroupTimingObservation(result, shard, execution, seen)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if include {
				observations = append(observations, observation)
			}
		}
	}
	return observations, firstErr
}

// remoteFailedCompileGroupTimingObservation 以共享 seen 集合投影一次失败 compile-group observation。
func remoteFailedCompileGroupTimingObservation(result RunResult, shard ShardResult, execution gate.CompileGroupExecution, seen map[string]struct{}) (gate.TimingObservation, bool, error) {
	include, err := remoteCompileGroupTimingExecutionEligibility(result, shard, execution)
	if err != nil {
		return gate.TimingObservation{}, false, err
	}
	if !include {
		return gate.TimingObservation{}, false, nil
	}
	key := compileGroupExecutionKey(shard, execution)
	if _, duplicate := seen[key]; duplicate {
		return gate.TimingObservation{}, false, fmt.Errorf("shard %q compile group %q is duplicated", shard.ShardIdentity, execution.GroupID)
	}
	seen[key] = struct{}{}
	observation, err := remoteCompileGroupTimingObservation(result, shard, execution)
	if err != nil {
		return gate.TimingObservation{}, false, fmt.Errorf("shard %q compile group %q timing: %w", shard.ShardIdentity, execution.GroupID, err)
	}
	return observation, true, nil
}

// remoteCompileTimingObservations 将每个 fresh shared compile group 投影为一条
// 可复用的 measured/raw 历史样本。selector 成员只用于推导唯一 semantic key，
// 不会把同一组的编译耗时复制到每个 selector。
func remoteCompileTimingObservations(result RunResult) ([]gate.CompileTimingObservation, error) {
	if !remoteRunHasCompileGroupExecutions(result) {
		return nil, nil
	}
	if err := validateRemoteCompileTimingProjectionIdentity(result); err != nil {
		return nil, err
	}

	observations := make([]gate.CompileTimingObservation, 0)
	seen := make(map[string]struct{})
	for _, shard := range result.Shards {
		for _, execution := range shard.Report.CompileGroupExecutions {
			observation, include, err := remoteCompileTimingObservationForExecution(result, shard, execution, seen)
			if err != nil {
				return observations, err
			}
			if include {
				observations = append(observations, observation)
			}
		}
	}
	return observations, nil
}

// validateRemoteCompileTimingProjectionIdentity 校验编译历史行共用的运行身份。
func validateRemoteCompileTimingProjectionIdentity(result RunResult) error {
	if result.ExecutionMode != gate.DurationExecutionModeNormal && result.ExecutionMode != gate.DurationExecutionModeCalibration {
		return errors.New("compile timing execution mode is required")
	}
	if result.Platform == "" || result.RunnerIdentityDigest == "" || result.ToolchainDigest == "" {
		return errors.New("compile timing platform, runner identity, and toolchain identity are required")
	}
	return nil
}

// remoteCompileTimingObservationForExecution 校验单个执行并构造唯一 measured/raw 行。
func remoteCompileTimingObservationForExecution(result RunResult, shard ShardResult, execution gate.CompileGroupExecution, seen map[string]struct{}) (gate.CompileTimingObservation, bool, error) {
	include, err := remoteCompileGroupTimingExecutionEligibility(result, shard, execution)
	if err != nil {
		return gate.CompileTimingObservation{}, false, err
	}
	if !include {
		return gate.CompileTimingObservation{}, false, nil
	}
	key := remoteCompileTimingExecutionKey(execution)
	if _, duplicate := seen[key]; duplicate {
		return gate.CompileTimingObservation{}, false, fmt.Errorf("compile group %q is duplicated across fresh reports", execution.GroupID)
	}
	seen[key] = struct{}{}
	semanticKey, err := remoteCompileTimingSemanticKey(execution)
	if err != nil {
		return gate.CompileTimingObservation{}, false, fmt.Errorf("compile group %q semantic identity: %w", execution.GroupID, err)
	}
	observation := gate.CompileTimingObservation{
		Identity: gate.CompileTimingIdentity{
			PackageTarget:        execution.PackageTarget,
			SemanticKey:          semanticKey,
			Platform:             result.Platform,
			RunnerIdentityDigest: result.RunnerIdentityDigest,
			ToolchainDigest:      result.ToolchainDigest,
			ExecutionMode:        result.ExecutionMode,
			ResourceClassID:      execution.ResourceClassID,
			ResourceCPU:          shard.Resources.CPU,
			ResourceMemoryGiB:    shard.Resources.MemoryGiB,
		},
		DurationMS:  execution.DurationMS,
		StartedAt:   time.UnixMilli(execution.StartedAtUnixMS).UTC(),
		CompletedAt: time.UnixMilli(execution.CompletedAtUnixMS).UTC(),
		Measurement: cicontract.ObservationMeasured,
		Aggregation: cicontract.TimingAggregationRaw,
	}
	if err := observation.Validate(); err != nil {
		return gate.CompileTimingObservation{}, false, fmt.Errorf("compile group %q timing observation: %w", execution.GroupID, err)
	}
	return observation, true, nil
}

// remoteCompileTimingExecutionKey 标识一个共享编译执行，不把报告它的分片外壳混入键。
func remoteCompileTimingExecutionKey(execution gate.CompileGroupExecution) string {
	return execution.GroupID + "\x00" + execution.ArtifactKey
}

// remoteCompileTimingSemanticKey 要求组内每个 selector 都携带相同的 canonical 语义键。
// selector 缺失或混用属于身份错误，不能借机合成默认语义。
func remoteCompileTimingSemanticKey(execution gate.CompileGroupExecution) (string, error) {
	var semanticKey string
	for index, workloadID := range execution.WorkloadIDs {
		semantic, err := gate.CompileGroupSemanticKeyForWorkloadID(workloadID)
		if err != nil {
			return "", fmt.Errorf("workload %q: %w", workloadID, err)
		}
		if index == 0 {
			semanticKey = semantic
			continue
		}
		if semantic != semanticKey {
			return "", fmt.Errorf("workload %q semantic key %q differs from %q", workloadID, semantic, semanticKey)
		}
	}
	if semanticKey == "" {
		return "", errors.New("compile group workload semantic key is empty")
	}
	return semanticKey, nil
}
