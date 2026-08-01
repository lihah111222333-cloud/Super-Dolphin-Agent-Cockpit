package remoteci

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteDurationSamples 为 catalog 内每个实际 workload 结果生成独立时长样本。
func remoteDurationSamples(
	workloadCatalog gate.WorkloadCatalog,
	shards []ShardResult,
	input RunInput,
) ([]gate.DurationSample, error) {
	catalog := make(map[string]gate.Workload, len(workloadCatalog.Workloads))
	for _, workload := range workloadCatalog.Workloads {
		catalog[workload.ID] = workload
	}
	var samples []gate.DurationSample
	var sampleErr error
	for _, shard := range shards {
		executed, err := remoteExecutedWorkloadSet(shard.ExecutedWorkloads)
		if err != nil {
			sampleErr = errors.Join(sampleErr, err)
			continue
		}
		shardSamples, err := remoteShardDurationSamples(catalog, executed, shard.Report.Gates, input)
		samples = append(samples, shardSamples...)
		if err != nil {
			sampleErr = errors.Join(sampleErr, err)
		}
	}
	return samples, sampleErr
}

func remoteExecutedWorkloadSet(ids []gate.GateID) (map[gate.GateID]struct{}, error) {
	executed := make(map[gate.GateID]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := executed[id]; duplicate {
			return nil, fmt.Errorf("remote CI duration workload %q is duplicated", id)
		}
		executed[id] = struct{}{}
	}
	return executed, nil
}

// remoteShardDurationSamples 将分片执行报告转换为绑定环境与 workload 的耗时样本。
func remoteShardDurationSamples(
	catalog map[string]gate.Workload,
	executed map[gate.GateID]struct{},
	executions []gate.PlanGateExecution,
	input RunInput,
) ([]gate.DurationSample, error) {
	samples := make([]gate.DurationSample, 0, len(executed))
	observed := make(map[gate.GateID]struct{}, len(executed))
	for _, execution := range executions {
		if _, wanted := executed[execution.GateID]; !wanted {
			continue
		}
		if _, duplicate := observed[execution.GateID]; duplicate {
			return nil, fmt.Errorf("remote CI duration workload %q is duplicated", execution.GateID)
		}
		workload, ok := catalog[string(execution.GateID)]
		if !ok {
			return nil, fmt.Errorf("remote CI duration result %q is absent from catalog", execution.GateID)
		}
		observed[execution.GateID] = struct{}{}
		samples = append(samples, remoteDurationSample(workload, execution, input))
		testSamples, err := remoteGoTestDurationSamples(workload, execution, input)
		if err != nil {
			return nil, err
		}
		samples = append(samples, testSamples...)
	}
	if len(observed) != len(executed) {
		return samples, errors.New("remote CI duration execution coverage is incomplete")
	}
	return samples, nil
}

// remoteGoTestDurationSamples 将一个 Go workload 的逐测试计时投影成可跨批次复用的独立样本。
func remoteGoTestDurationSamples(
	workload gate.Workload,
	execution gate.PlanGateExecution,
	input RunInput,
) ([]gate.DurationSample, error) {
	if len(execution.TestTimings) == 0 {
		return nil, nil
	}
	if workload.Kind != gate.WorkloadKindGoTest {
		return nil, fmt.Errorf("remote CI non-Go workload %q reported Go test timings", workload.ID)
	}
	parentWorkload, err := remoteGoTestDurationParent(workload)
	if err != nil {
		return nil, err
	}
	samples := make([]gate.DurationSample, 0, len(execution.TestTimings))
	for _, timing := range execution.TestTimings {
		samples = append(samples, gate.DurationSample{
			Bucket: gate.DurationBucket{
				WorkloadID:    gate.GoTestDurationWorkloadID(parentWorkload.ID, timing.Name),
				CommandDigest: gate.GoTestDurationCommandDigest(parentWorkload.CommandDigest, timing.Name),
				Platform:      input.Platform,
				Runner:        input.RunnerIdentityDigest,
				Toolchain:     input.ToolchainDigest,
			},
			Succeeded:           timing.Status != gate.GoTestStatusFail,
			DurationMS:          timing.DurationMS,
			TargetKind:          gate.WorkloadKindGoTest,
			ParentWorkloadID:    parentWorkload.ID,
			ParentCommandDigest: parentWorkload.CommandDigest,
			TargetName:          timing.Name,
			TargetStatus:        timing.Status,
		})
	}
	return samples, nil
}

// remoteGoTestDurationParent 将精确测试 workload 还原到规范包级父 workload。
func remoteGoTestDurationParent(workload gate.Workload) (gate.Workload, error) {
	parent, kind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return gate.Workload{}, err
	}
	if !targeted || kind != gate.WorkloadTargetGoTest {
		return workload, nil
	}
	testTarget, err := gate.ParseGoTestTarget(target)
	if err != nil {
		return gate.Workload{}, err
	}
	parentWorkload, err := gate.NewGoPackageWorkload(parent, testTarget.Package, 1)
	if err != nil {
		return gate.Workload{}, err
	}
	return parentWorkload, nil
}

// remoteDurationSample 将实际执行耗时归入稳定 runner 身份对应的统计桶。
func remoteDurationSample(
	workload gate.Workload,
	execution gate.PlanGateExecution,
	input RunInput,
) gate.DurationSample {
	duration := execution.CompletedAt.Sub(execution.StartedAt).Milliseconds()
	if duration <= 0 {
		duration = 1
	}
	return gate.DurationSample{
		Bucket: gate.DurationBucket{
			WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
			Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
		},
		Succeeded: execution.Status == gate.ResultStatusPassed, DurationMS: duration,
	}
}

type remoteCalibrationParentSample struct {
	workload   gate.Workload
	succeeded  bool
	durationMS int64
}

// remoteCalibrationParentDurationSamples 将校准分片的完整逐测试结果聚合为可比较的包级时长事实。
func remoteCalibrationParentDurationSamples(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
	input RunInput,
) ([]gate.DurationSample, error) {
	if !input.Calibration {
		return nil, nil
	}
	parents := make(map[string]*remoteCalibrationParentSample)
	for _, workload := range catalog.Workloads {
		parent, targeted, err := remoteCalibrationGoTestParent(workload)
		if err != nil {
			return nil, err
		}
		if !targeted {
			continue
		}
		execution, ok := observed[workload.ID]
		if !ok {
			return nil, fmt.Errorf("remote CI calibration child %q has no terminal execution", workload.ID)
		}
		if err := addRemoteCalibrationParentExecution(parents, parent, execution); err != nil {
			return nil, err
		}
	}
	return remoteCalibrationParentSamples(parents, input), nil
}

func remoteCalibrationGoTestParent(workload gate.Workload) (gate.Workload, bool, error) {
	_, kind, _, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return gate.Workload{}, false, err
	}
	if !targeted || kind != gate.WorkloadTargetGoTest {
		return gate.Workload{}, false, nil
	}
	parent, err := remoteGoTestDurationParent(workload)
	return parent, true, err
}

func addRemoteCalibrationParentExecution(
	parents map[string]*remoteCalibrationParentSample,
	parent gate.Workload,
	execution gate.PlanGateExecution,
) error {
	key := parent.ID + "\x00" + parent.CommandDigest
	aggregate := parents[key]
	if aggregate == nil {
		aggregate = &remoteCalibrationParentSample{workload: parent, succeeded: true}
		parents[key] = aggregate
	}
	duration := execution.CompletedAt.Sub(execution.StartedAt).Milliseconds()
	if duration <= 0 {
		duration = 1
	}
	if aggregate.durationMS > math.MaxInt64-duration {
		return fmt.Errorf("remote CI calibration parent %q duration overflows", parent.ID)
	}
	aggregate.durationMS += duration
	aggregate.succeeded = aggregate.succeeded && execution.Status == gate.ResultStatusPassed
	return nil
}

func remoteCalibrationParentSamples(
	parents map[string]*remoteCalibrationParentSample,
	input RunInput,
) []gate.DurationSample {
	keys := make([]string, 0, len(parents))
	for key := range parents {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	samples := make([]gate.DurationSample, 0, len(keys))
	for _, key := range keys {
		aggregate := parents[key]
		samples = append(samples, gate.DurationSample{
			Bucket: gate.DurationBucket{
				WorkloadID: aggregate.workload.ID, CommandDigest: aggregate.workload.CommandDigest,
				Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
			},
			Succeeded: aggregate.succeeded, DurationMS: aggregate.durationMS,
		})
	}
	return samples
}

// remoteOptimizationWarnings 把超过 100 秒的实际 workload 转成非阻断优化告警。
func remoteOptimizationWarnings(samples []gate.DurationSample) []string {
	warnings := make([]string, 0)
	for _, sample := range samples {
		if sample.ParentWorkloadID != "" || sample.DurationMS <= gate.FullCITargetDurationMS {
			continue
		}
		outcome := "failed"
		if sample.Succeeded {
			outcome = "passed"
		}
		warnings = append(warnings, fmt.Sprintf(
			"CI optimization warning: workload %q %s in %dms (target %dms); optimize or split this shard",
			sample.Bucket.WorkloadID,
			outcome,
			sample.DurationMS,
			gate.FullCITargetDurationMS,
		))
	}
	sort.Strings(warnings)
	return warnings
}

// aggregateRemoteReports 按权威 catalog 汇总所有 worker workload 报告。
func aggregateRemoteReports(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
	shards []ShardResult,
) ([]gate.PlanGateExecution, []gate.PlanGateExecution, gate.ResultStatus, error) {
	status := remoteContainerStatus(shards)
	workloadExecutions, err := remoteWorkloadExecutions(catalog, observed)
	if err != nil {
		return nil, nil, gate.ResultStatusFailed, err
	}
	executions, aggregateStatus, err := aggregateCatalogWorkloads(catalog, observed)
	if err != nil {
		return nil, nil, gate.ResultStatusFailed, err
	}
	if aggregateStatus != gate.ResultStatusPassed {
		status = gate.ResultStatusFailed
	}
	return executions, workloadExecutions, status, nil
}

// remoteWorkloadExecutions keeps each worker-owned workload profile separate from parent gate aggregation.
func remoteWorkloadExecutions(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
) ([]gate.PlanGateExecution, error) {
	workloads := make([]gate.PlanGateExecution, 0, len(catalog.Workloads))
	for _, spec := range catalog.Workloads {
		if !spec.Shardable {
			continue
		}
		execution, ok := observed[spec.ID]
		if !ok || execution.GateID != gate.GateID(spec.ID) {
			return nil, fmt.Errorf("remote CI workload %q has no matching observation", spec.ID)
		}
		execution, err := normalizeRemoteWorkloadExecutionProfile(spec, 1, execution)
		if err != nil {
			return nil, fmt.Errorf("remote CI workload %q execution profile: %w", spec.ID, err)
		}
		if err := execution.ExecutionProfile.Validate(); err != nil {
			return nil, fmt.Errorf("remote CI workload %q execution profile: %w", spec.ID, err)
		}
		workloads = append(workloads, execution)
	}
	if len(workloads) != len(observed) {
		return nil, errors.New("remote CI workload observation coverage is not exact")
	}
	return workloads, nil
}

func remoteContainerStatus(shards []ShardResult) gate.ResultStatus {
	for _, shard := range shards {
		if shard.ContainerStatus != "Succeeded" {
			return gate.ResultStatusFailed
		}
	}
	return gate.ResultStatusPassed
}

// aggregateCatalogWorkloads 按 catalog 顺序合并 shardable workload 为父 gate 结果。
func aggregateCatalogWorkloads(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
) ([]gate.PlanGateExecution, gate.ResultStatus, error) {
	grouped, parents, status, err := groupCatalogWorkloadExecutions(catalog, observed)
	if err != nil {
		return nil, gate.ResultStatusFailed, err
	}
	executions := make([]gate.PlanGateExecution, 0, len(parents))
	for _, parent := range parents {
		aggregate, aggregateStatus, err := aggregateWorkloadGate(parent, grouped[parent])
		if err != nil {
			return nil, gate.ResultStatusFailed, err
		}
		if aggregateStatus != gate.ResultStatusPassed {
			status = gate.ResultStatusFailed
		}
		executions = append(executions, aggregate)
	}
	return executions, status, nil
}

// groupCatalogWorkloadExecutions 按目录顺序将 workload 结果聚合回父门禁执行。
func groupCatalogWorkloadExecutions(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
) (map[gate.GateID][]gate.PlanGateExecution, []gate.GateID, gate.ResultStatus, error) {
	grouped := make(map[gate.GateID][]gate.PlanGateExecution)
	var parents []gate.GateID
	expected := 0
	status := gate.ResultStatusPassed
	for _, spec := range catalog.Workloads {
		if !spec.Shardable {
			continue
		}
		execution, parent, err := catalogWorkloadExecution(spec, observed)
		if err != nil {
			return nil, nil, gate.ResultStatusFailed, err
		}
		expected++
		if len(grouped[parent]) == 0 {
			parents = append(parents, parent)
		}
		grouped[parent] = append(grouped[parent], execution)
		if execution.Status != gate.ResultStatusPassed {
			status = gate.ResultStatusFailed
		}
	}
	if len(observed) != expected {
		return nil, nil, gate.ResultStatusFailed, errors.New("remote CI workload observation coverage is not exact")
	}
	return grouped, parents, status, nil
}

func catalogWorkloadExecution(
	spec gate.Workload,
	observed map[string]gate.PlanGateExecution,
) (gate.PlanGateExecution, gate.GateID, error) {
	execution, ok := observed[spec.ID]
	if !ok || execution.GateID != gate.GateID(spec.ID) {
		return gate.PlanGateExecution{}, "", fmt.Errorf("remote CI workload %q has no matching observation", spec.ID)
	}
	parent, err := gate.WorkloadParentGateID(spec.ID)
	if err != nil {
		return gate.PlanGateExecution{}, "", err
	}
	return execution, parent, nil
}

// aggregateWorkloadGate 将同一父 gate 的所有 workload 证据合并为一个 gate 执行。
func aggregateWorkloadGate(
	gateID gate.GateID,
	workloads []gate.PlanGateExecution,
) (gate.PlanGateExecution, gate.ResultStatus, error) {
	if len(workloads) == 0 {
		return gate.PlanGateExecution{}, gate.ResultStatusFailed, fmt.Errorf("remote CI gate %q has no workload results", gateID)
	}
	result := gate.PlanGateExecution{
		GateID: gateID, Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: workloads[0].StartedAt, CompletedAt: workloads[0].CompletedAt,
	}
	var proof strings.Builder
	for _, workload := range workloads {
		if workload.StartedAt.Before(result.StartedAt) {
			result.StartedAt = workload.StartedAt
		}
		if workload.CompletedAt.After(result.CompletedAt) {
			result.CompletedAt = workload.CompletedAt
		}
		if workload.Status != gate.ResultStatusPassed {
			result.Status = gate.ResultStatusFailed
			result.ExitCode = 1
		}
		fmt.Fprintf(
			&proof,
			"workload=%s status=%s log_digest=%s\n",
			workload.GateID,
			workload.Status,
			workload.LogDigest,
		)
	}
	total := result.CompletedAt.Sub(result.StartedAt).Milliseconds()
	if total < 0 {
		return gate.PlanGateExecution{}, gate.ResultStatusFailed, errors.New("aggregated remote CI gate time is invalid")
	}
	result.ExecutionProfile = gate.ExecutionProfile{CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "not_measured", StartupMS: total, TotalMS: total}
	result.Log = []byte(proof.String())
	sum := sha256.Sum256(result.Log)
	result.LogDigest = fmt.Sprintf("sha256:%x", sum)
	return result, result.Status, nil
}
