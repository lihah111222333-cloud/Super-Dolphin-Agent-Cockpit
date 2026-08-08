package remoteci

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteDurationResourceIdentity 按 execution mode 选择唯一的资源身份来源。
// normal 只读取 ECI 容器回执，calibration 只接受请求冻结的 4C/8GiB 回执；
// 两条路径不能互相读取对方的资源选择或 compile-group 标签。
func remoteDurationResourceIdentity(input RunInput, shard ShardResult) (gate.DurationBucket, error) {
	if input.Calibration {
		return remoteCalibrationDurationResourceIdentity(input, shard)
	}
	return remoteNormalDurationResourceIdentity(input, shard)
}

func remoteNormalDurationResourceIdentity(input RunInput, shard ShardResult) (gate.DurationBucket, error) {
	resource := gate.DurationBucket{
		WorkloadID:        "duration-resource",
		CommandDigest:     strings.Repeat("0", 64),
		InputDigest:       "sha256:" + strings.Repeat("0", 64),
		Platform:          input.Platform,
		Runner:            input.RunnerIdentityDigest,
		Toolchain:         input.ToolchainDigest,
		ExecutionMode:     gate.DurationExecutionModeNormal,
		ResourceClassID:   shard.ResourceClass,
		ResourceCPU:       shard.Resources.CPU,
		ResourceMemoryGiB: shard.Resources.MemoryGiB,
	}
	if err := gate.ValidateDurationBucket(resource); err != nil {
		return gate.DurationBucket{}, fmt.Errorf("validate remote CI duration resource receipt: %w", err)
	}
	return resource, nil
}

func remoteCalibrationDurationResourceIdentity(input RunInput, shard ShardResult) (gate.DurationBucket, error) {
	expected := input.CalibrationResource
	resource := gate.DurationBucket{
		WorkloadID:        "duration-resource",
		CommandDigest:     strings.Repeat("0", 64),
		InputDigest:       "sha256:" + strings.Repeat("0", 64),
		Platform:          input.Platform,
		Runner:            input.RunnerIdentityDigest,
		Toolchain:         input.ToolchainDigest,
		ExecutionMode:     gate.DurationExecutionModeCalibration,
		ResourceClassID:   expected.ID,
		ResourceCPU:       expected.VCPU,
		ResourceMemoryGiB: expected.MemoryGiB,
	}
	if shard.ResourceClass != expected.ID || shard.Resources.CPU != expected.VCPU || shard.Resources.MemoryGiB != expected.MemoryGiB {
		return gate.DurationBucket{}, fmt.Errorf(
			"remote CI calibration duration resource receipt drifted: observed class=%q %.gC/%.gGiB, expected class=%q %.gC/%.gGiB",
			shard.ResourceClass, shard.Resources.CPU, shard.Resources.MemoryGiB,
			expected.ID, expected.VCPU, expected.MemoryGiB,
		)
	}
	if err := gate.ValidateDurationBucket(resource); err != nil {
		return gate.DurationBucket{}, fmt.Errorf("validate remote CI duration resource receipt: %w", err)
	}
	return resource, nil
}

func durationInputDigest(workload gate.Workload, inputDigests map[string]string) (string, bool) {
	digest := workload.InputDigest
	if digest == "" {
		digest = inputDigests[workload.ID]
	}
	return digest, strings.TrimSpace(digest) != ""
}

// remoteDurationSamples 为 catalog 内每个实际 workload 结果生成独立时长样本。
func remoteDurationSamples(
	workloadCatalog gate.WorkloadCatalog,
	shards []ShardResult,
	input RunInput,
	inputDigests map[string]string,
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
		shardSamples, err := remoteShardDurationSamples(catalog, executed, shard.Report.Gates, input, inputDigests, shard)
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
	inputDigests map[string]string,
	shard ShardResult,
) ([]gate.DurationSample, error) {
	if len(executed) == 0 {
		return nil, nil
	}
	resource, err := remoteDurationResourceIdentity(input, shard)
	if err != nil {
		return nil, err
	}
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
		inputDigest, ok := durationInputDigest(workload, inputDigests)
		if !ok {
			return nil, fmt.Errorf("remote CI duration workload %q input digest is missing", workload.ID)
		}
		samples = append(samples, remoteDurationSample(workload, execution, input, inputDigest, resource))
		testSamples, err := remoteGoTestDurationSamples(workload, execution, input, inputDigests, resource)
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
	inputDigests map[string]string,
	resource gate.DurationBucket,
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
		inputDigest, ok := durationInputDigest(workload, inputDigests)
		if !ok {
			return nil, fmt.Errorf("remote CI duration workload %q input digest is missing", workload.ID)
		}
		samples = append(samples, gate.DurationSample{
			Bucket: gate.DurationBucket{
				WorkloadID:        gate.GoTestDurationWorkloadID(parentWorkload.ID, timing.Name),
				CommandDigest:     gate.GoTestDurationCommandDigest(parentWorkload.CommandDigest, timing.Name),
				InputDigest:       inputDigest,
				Platform:          input.Platform,
				Runner:            input.RunnerIdentityDigest,
				Toolchain:         input.ToolchainDigest,
				ExecutionMode:     resource.ExecutionMode,
				ResourceClassID:   resource.ResourceClassID,
				ResourceCPU:       resource.ResourceCPU,
				ResourceMemoryGiB: resource.ResourceMemoryGiB,
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
	inputDigest string,
	resource gate.DurationBucket,
) gate.DurationSample {
	duration := execution.CompletedAt.Sub(execution.StartedAt).Milliseconds()
	if duration <= 0 {
		duration = 1
	}
	return gate.DurationSample{
		Bucket: gate.DurationBucket{
			WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
			InputDigest: inputDigest,
			Platform:    input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
			ExecutionMode: resource.ExecutionMode, ResourceClassID: resource.ResourceClassID,
			ResourceCPU: resource.ResourceCPU, ResourceMemoryGiB: resource.ResourceMemoryGiB,
		},
		Succeeded: execution.Status == gate.ResultStatusPassed, DurationMS: duration,
	}
}

type remoteCalibrationParentSample struct {
	workload     gate.Workload
	succeeded    bool
	durationMS   int64
	inputDigests map[string]string
}

// remoteCalibrationParentDurationSamples 将校准分片的完整逐测试结果聚合为可比较的包级时长事实。
func remoteCalibrationParentDurationSamples(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
	input RunInput,
	inputDigests map[string]string,
) ([]gate.DurationSample, error) {
	if !input.Calibration {
		return nil, nil
	}
	parents := make(map[string]*remoteCalibrationParentSample)
	incomplete := make(map[string]struct{})
	for _, workload := range catalog.Workloads {
		parent, targeted, err := remoteCalibrationGoTestParent(workload)
		if err != nil {
			return nil, err
		}
		if !targeted {
			continue
		}
		key := parent.ID + "\x00" + parent.CommandDigest
		execution, ok := observed[workload.ID]
		if !ok {
			delete(parents, key)
			incomplete[key] = struct{}{}
			continue
		}
		if _, excluded := incomplete[key]; excluded {
			continue
		}
		inputDigest, ok := durationInputDigest(workload, inputDigests)
		if !ok {
			return nil, fmt.Errorf("remote CI calibration child %q input digest is missing", workload.ID)
		}
		if err := addRemoteCalibrationParentExecution(parents, parent, workload.ID, inputDigest, execution); err != nil {
			return nil, err
		}
	}
	return remoteCalibrationParentSamples(parents, input)
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
	childID string,
	inputDigest string,
	execution gate.PlanGateExecution,
) error {
	key := parent.ID + "\x00" + parent.CommandDigest
	aggregate := parents[key]
	if aggregate == nil {
		aggregate = &remoteCalibrationParentSample{workload: parent, succeeded: true, inputDigests: make(map[string]string)}
		parents[key] = aggregate
	}
	aggregate.inputDigests[childID] = inputDigest
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

// remoteCalibrationParentSamples 将校准子 workload 的真实区间聚合为父门禁耗时样本。
func remoteCalibrationParentSamples(
	parents map[string]*remoteCalibrationParentSample,
	input RunInput,
) ([]gate.DurationSample, error) {
	keys := make([]string, 0, len(parents))
	for key := range parents {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	samples := make([]gate.DurationSample, 0, len(keys))
	for _, key := range keys {
		aggregate := parents[key]
		inputDigest, err := remoteCalibrationParentInputDigest(aggregate.workload, aggregate.inputDigests)
		if err != nil {
			return nil, err
		}
		bucket := gate.DurationBucket{
			WorkloadID: aggregate.workload.ID, CommandDigest: aggregate.workload.CommandDigest,
			InputDigest: inputDigest, Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
			ExecutionMode: gate.DurationExecutionModeCalibration, ResourceClassID: input.CalibrationResource.ID,
			ResourceCPU: input.CalibrationResource.VCPU, ResourceMemoryGiB: input.CalibrationResource.MemoryGiB,
		}
		if err := gate.ValidateDurationBucket(bucket); err != nil {
			return nil, fmt.Errorf("validate remote CI calibration parent duration bucket: %w", err)
		}
		samples = append(samples, gate.DurationSample{
			Bucket:    bucket,
			Succeeded: aggregate.succeeded, DurationMS: aggregate.durationMS,
		})
	}
	return samples, nil
}

func remoteCalibrationParentInputDigest(parent gate.Workload, childDigests map[string]string) (string, error) {
	if len(childDigests) == 0 {
		return "", fmt.Errorf("remote CI calibration parent %q has no child input digests", parent.ID)
	}
	keys := make([]string, 0, len(childDigests))
	for childID := range childDigests {
		keys = append(keys, childID)
	}
	sort.Strings(keys)
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("remote-calibration-parent-input/v1\x00" + parent.ID + "\x00" + parent.CommandDigest + "\x00"))
	for _, childID := range keys {
		_, _ = hasher.Write([]byte(childID + "\x00" + childDigests[childID] + "\x00"))
	}
	return fmt.Sprintf("sha256:%x", hasher.Sum(nil)), nil
}

// appendUniqueRemoteWarnings 合并、去重并按规范顺序返回结构化告警。
func appendUniqueRemoteWarnings(existing []string, additions ...[]string) []string {
	seen := make(map[string]struct{}, len(existing))
	result := make([]string, 0, len(existing))
	for _, warning := range existing {
		if _, exists := seen[warning]; exists {
			continue
		}
		seen[warning] = struct{}{}
		result = append(result, warning)
	}
	for _, warnings := range additions {
		for _, warning := range warnings {
			if _, exists := seen[warning]; exists {
				continue
			}
			seen[warning] = struct{}{}
			result = append(result, warning)
		}
	}
	sort.Strings(result)
	return result
}

// aggregateRemoteReports 按权威 catalog 汇总所有 worker workload 报告。
func aggregateRemoteReports(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
	shards []ShardResult,
	planDigest string,
) ([]gate.PlanGateExecution, []gate.PlanGateExecution, gate.ResultStatus, error) {
	return aggregateRemoteReportsWithClock(catalog, observed, shards, planDigest, time.Now)
}

// aggregateRemoteReportsWithClock 在 owner 时钟下汇总 worker workload，并生成 release owner 证明。
func aggregateRemoteReportsWithClock(
	catalog gate.WorkloadCatalog,
	observed map[string]gate.PlanGateExecution,
	shards []ShardResult,
	planDigest string,
	now func() time.Time,
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
	executions, status, err = appendRemoteOwnerAttestation(catalog, executions, status, planDigest, now)
	if err != nil {
		return executions, workloadExecutions, gate.ResultStatusFailed, err
	}
	return executions, workloadExecutions, status, nil
}

// appendRemoteOwnerAttestation 由 coordinator 在全部 shardable 结果汇合后生成唯一 release 证明。
func appendRemoteOwnerAttestation(
	catalog gate.WorkloadCatalog,
	executions []gate.PlanGateExecution,
	status gate.ResultStatus,
	planDigest string,
	now func() time.Time,
) ([]gate.PlanGateExecution, gate.ResultStatus, error) {
	if !remoteCatalogHasReleaseOwner(catalog) {
		return executions, status, nil
	}
	observed := make(map[gate.GateID]gate.PlanGateExecution, len(executions))
	for _, execution := range executions {
		if _, exists := observed[execution.GateID]; exists {
			return executions, gate.ResultStatusFailed, fmt.Errorf("remote CI owner aggregation duplicated gate %q", execution.GateID)
		}
		observed[execution.GateID] = execution
	}
	attestation, err := gate.ExecuteReleaseLayerAttestation(gate.ProfileRelease, planDigest, observed, now)
	if err != nil {
		return append(executions, attestation), gate.ResultStatusFailed, fmt.Errorf("aggregate remote release owner workload: %w", err)
	}
	return append(executions, attestation), status, nil
}

func remoteCatalogHasReleaseOwner(catalog gate.WorkloadCatalog) bool {
	for _, workload := range catalog.Workloads {
		if !workload.Shardable && workload.ID == string(gate.GateIDReleaseLayeredCheck) {
			return true
		}
	}
	return false
}

// remoteWorkloadExecutions keeps each worker-owned workload profile separate from parent gate aggregation.
// remoteWorkloadExecutions 按权威目录回读并规范化每个 shardable workload 的执行证据。
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
		if err := execution.ExecutionProfile.Validate(); err != nil {
			return nil, fmt.Errorf("remote CI workload %q execution profile: %w", spec.ID, err)
		}
		if err := gate.ValidatePlanGateTimingEvidence(execution); err != nil {
			return nil, fmt.Errorf("remote CI workload %q timing evidence: %w", spec.ID, err)
		}
		canonical, err := gate.CanonicalizePlanGateExecutionTiming(execution)
		if err != nil {
			return nil, fmt.Errorf("remote CI workload %q timing: %w", spec.ID, err)
		}
		observed[spec.ID] = canonical
		workloads = append(workloads, canonical)
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
	profile, startedAt, completedAt, err := aggregateWorkloadExecutionProfile(gateID, workloads)
	if err != nil {
		return gate.PlanGateExecution{}, gate.ResultStatusFailed, err
	}
	result := gate.PlanGateExecution{
		GateID: gateID, Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: startedAt, CompletedAt: completedAt, ExecutionProfile: profile,
	}
	proofHasher := sha256.New()
	_, _ = proofHasher.Write([]byte("remote-parent-workload-proof/v1\x00"))
	passed := 0
	for _, workload := range workloads {
		if workload.Status != gate.ResultStatusPassed {
			result.Status = gate.ResultStatusFailed
			result.ExitCode = 1
		} else {
			passed++
		}
		encoded, err := json.Marshal(struct {
			WorkloadID       gate.GateID           `json:"workload_id"`
			Status           gate.ResultStatus     `json:"status"`
			ExitCode         int                   `json:"exit_code"`
			StartedAt        time.Time             `json:"started_at"`
			CompletedAt      time.Time             `json:"completed_at"`
			ArgvDigest       string                `json:"argv_digest"`
			LogDigest        string                `json:"log_digest"`
			ExecutionProfile gate.ExecutionProfile `json:"execution_profile"`
		}{
			WorkloadID: workload.GateID, Status: workload.Status, ExitCode: workload.ExitCode,
			StartedAt: workload.StartedAt, CompletedAt: workload.CompletedAt,
			ArgvDigest: workload.ArgvDigest, LogDigest: workload.LogDigest, ExecutionProfile: workload.ExecutionProfile,
		})
		if err != nil {
			return gate.PlanGateExecution{}, gate.ResultStatusFailed, fmt.Errorf("encode remote CI gate %q workload proof: %w", gateID, err)
		}
		_, _ = proofHasher.Write(encoded)
		_, _ = proofHasher.Write([]byte{'\n'})
	}
	proofDigest := fmt.Sprintf("sha256:%x", proofHasher.Sum(nil))
	result.Log = fmt.Appendf(nil,
		"[remote-parent-workload-proof] schema=1 gate=%s workloads=%d passed=%d failed=%d proof_digest=%s\n",
		gateID, len(workloads), passed, len(workloads)-passed, proofDigest,
	)
	sum := sha256.Sum256(result.Log)
	result.LogDigest = fmt.Sprintf("sha256:%x", sum)
	return result, result.Status, nil
}
