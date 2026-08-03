package gate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// validateRemoteCIRunRecord 在写入 SQLite 前闭合完整结果覆盖、新执行分片、复用证据和账本约束。
func validateRemoteCIRunRecord(record RemoteCIRunRecord) error {
	if err := validateRemoteCIRunIdentity(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunShards(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunExecutions(record.Executions); err != nil {
		return err
	}
	if err := validateRemoteCIRunWorkloadExecutions(record.WorkloadExecutions); err != nil {
		return err
	}
	if err := validateRemoteCIWorkloadResults(record.WorkloadResults); err != nil {
		return err
	}
	if err := validateRemoteCIRunTimingObservations(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunWarnings(record); err != nil {
		return err
	}
	if err := validateRemoteCIRunTimingWarnings(record); err != nil {
		return err
	}
	return nil
}

// validateRemoteCIRunExecutions 校验所有 parent gate 聚合 profile 均绑定唯一身份和真实关键路径。
func validateRemoteCIRunExecutions(executions []PlanGateExecution) error {
	seen := make(map[GateID]struct{}, len(executions))
	for _, execution := range executions {
		if execution.GateID == "" {
			return errors.New("remote CI aggregate execution gate ID is required")
		}
		if _, duplicate := seen[execution.GateID]; duplicate {
			return fmt.Errorf("remote CI aggregate execution %q is duplicated", execution.GateID)
		}
		seen[execution.GateID] = struct{}{}
		if err := validateRemoteCIAggregateExecution(execution); err != nil {
			return fmt.Errorf("remote CI aggregate execution %q: %w", execution.GateID, err)
		}
	}
	return nil
}

// validateRemoteCIRunTimingObservations 只把本次 fresh execution 的完整账本写入 SQLite authority。
func validateRemoteCIRunTimingObservations(record RemoteCIRunRecord) error {
	if record.Authoritative {
		return validateAuthoritativeRemoteCIRunTimingObservations(record)
	}
	return validateNonAuthoritativeRemoteCIRunTimingObservations(record)
}

// validateAuthoritativeRemoteCIRunTimingObservations 允许全复用 PASS 省略账本，其余 fresh run 必须完整计时。
func validateAuthoritativeRemoteCIRunTimingObservations(record RemoteCIRunRecord) error {
	if remoteCIRunHasOnlyReusedWorkloadResults(record) {
		if len(record.TimingObservations) != 0 {
			return errors.New("all-reused authoritative remote CI run must not contain fresh timing observations")
		}
		return nil
	}
	if len(record.TimingObservations) == 0 {
		return errors.New("authoritative remote CI run requires complete timing observations")
	}
	if err := ValidateAuthoritativeTimingObservations(record.JobID, record.TimingObservations, record.WorkloadExecutions, record.Shards); err != nil {
		return fmt.Errorf("remote CI authoritative timing observations: %w", err)
	}
	return nil
}

// remoteCIRunHasOnlyReusedWorkloadResults 识别已声明完整复用且不存在本次 fresh 记录的 PASS 运行。
func remoteCIRunHasOnlyReusedWorkloadResults(record RemoteCIRunRecord) bool {
	if record.Status != ResultStatusPassed || len(record.WorkloadResults) == 0 || len(record.Shards) != 0 || len(record.WorkloadExecutions) != 0 {
		return false
	}
	for _, result := range record.WorkloadResults {
		if result.Disposition != WorkloadDispositionReused {
			return false
		}
	}
	return true
}

func validateNonAuthoritativeRemoteCIRunTimingObservations(record RemoteCIRunRecord) error {
	for _, observation := range record.TimingObservations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("remote CI timing observation: %w", err)
		}
		if observation.JobID != record.JobID {
			return errors.New("remote CI timing observation job binding is invalid")
		}
	}
	return nil
}

func validateRemoteCIRunIdentity(record RemoteCIRunRecord) error {
	for _, validate := range []func(RemoteCIRunRecord) error{
		validateRemoteCIRunRequiredFields,
		validateRemoteCIRunEntrypoint,
		validateRemoteCIRunTiming,
		validateRemoteCIRunAgentTokenDigest,
		validateRemoteCIRunCatalogDigest,
		validateRemoteCIRunStatus,
	} {
		if err := validate(record); err != nil {
			return err
		}
	}
	return nil
}

// validateRemoteCIRunRequiredFields 确保运行投影具备绑定候选、接受代和 agent digest 的全部持久化身份字段。
func validateRemoteCIRunRequiredFields(record RemoteCIRunRecord) error {
	if record.AcceptedGeneration == 0 {
		return errors.New("remote CI run accepted baseline generation is required")
	}
	for field, value := range map[string]string{
		"agent token digest":              record.AgentTokenDigest,
		"job ID":                          record.JobID,
		"entrypoint":                      string(record.Entrypoint),
		"profile":                         string(record.Profile),
		"plan digest":                     record.PlanDigest,
		"catalog digest":                  record.CatalogDigest,
		"image cache snapshot":            record.ImageCacheSnapshotID,
		"source tree":                     record.SourceTreeSHA,
		"candidate gate source digest":    record.CandidateGateSourceSHA256,
		"candidate gate toolchain digest": record.CandidateGateToolchainSHA256,
		"runner image":                    record.RunnerImage,
		"status":                          string(record.Status),
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("remote CI run %s is required", field)
		}
	}
	if !isPrefixedSHA256Digest(record.CandidateGateSourceSHA256) || !isPrefixedSHA256Digest(record.CandidateGateToolchainSHA256) {
		return errors.New("remote CI run candidate gate compile identity is invalid")
	}
	return nil
}

func validateRemoteCIRunEntrypoint(record RemoteCIRunRecord) error {
	entrypoint, ok := canonicalCIEntrypoint(record.Entrypoint)
	if !ok {
		return fmt.Errorf("remote CI run entrypoint %q is not canonical", record.Entrypoint)
	}
	// An authoritative entrypoint is eligible for promotion, not authoritative
	// before its receipts, cleanup, and final authority CAS have all succeeded.
	if !record.Authoritative {
		return nil
	}
	if !entrypoint.Authoritative {
		return fmt.Errorf("remote CI run authoritative record requires canonical authoritative entrypoint %q", record.Entrypoint)
	}
	return nil
}

func validateRemoteCIRunTiming(record RemoteCIRunRecord) error {
	if record.StartedAt.IsZero() || record.CompletedAt.IsZero() {
		return errors.New("remote CI run timestamps are required")
	}
	if record.CompletedAt.Before(record.StartedAt) {
		return errors.New("remote CI run completion precedes start")
	}
	return nil
}

func validateRemoteCIRunAgentTokenDigest(record RemoteCIRunRecord) error {
	if err := cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest); err != nil {
		return fmt.Errorf("remote CI run agent token digest: %w", err)
	}
	return nil
}

func validateRemoteCIRunCatalogDigest(record RemoteCIRunRecord) error {
	if !isPrefixedSHA256Digest(record.CatalogDigest) {
		return errors.New("remote CI run catalog digest is invalid")
	}
	return nil
}

func validateRemoteCIRunStatus(record RemoteCIRunRecord) error {
	switch record.Status {
	case ResultStatusPassed, ResultStatusFailed, ResultStatusCancelled, ResultStatusTimeout,
		ResultStatusInfraFailed, ResultStatusPassedStalePolicy:
	default:
		return fmt.Errorf("remote CI run status %q is not supported", record.Status)
	}
	return nil
}

func validateRemoteCIRunWarnings(record RemoteCIRunRecord) error {
	for _, warning := range record.Warnings {
		if strings.TrimSpace(warning) == "" {
			return errors.New("remote CI run warning is empty")
		}
	}
	return nil
}

// validateRemoteCIRunWorkloadExecutions 逐项核验计划 workload 真实执行且身份唯一，阻止缓存结果伪装成本次通过。
func validateRemoteCIRunWorkloadExecutions(executions []PlanGateExecution) error {
	seen := make(map[GateID]struct{}, len(executions))
	for _, execution := range executions {
		if strings.TrimSpace(string(execution.GateID)) == "" {
			return errors.New("remote CI workload execution ID is required")
		}
		if _, duplicate := seen[execution.GateID]; duplicate {
			return fmt.Errorf("remote CI workload execution %q is duplicated", execution.GateID)
		}
		seen[execution.GateID] = struct{}{}
		if execution.StartedAt.IsZero() || execution.CompletedAt.Before(execution.StartedAt) {
			return fmt.Errorf("remote CI workload execution %q timing is invalid", execution.GateID)
		}
		if err := execution.ExecutionProfile.Validate(); err != nil {
			return fmt.Errorf("remote CI workload execution %q profile: %w", execution.GateID, err)
		}
	}
	return nil
}

// validateRemoteCIRunShards 校验分片归属、资源规格和终态与运行计划一致，避免无证据分片进入权威投影。
func validateRemoteCIRunShards(record RemoteCIRunRecord) error {
	seenWorkloads := make(map[GateID]string)
	for _, shard := range record.Shards {
		if strings.TrimSpace(shard.ShardIdentity) == "" || strings.TrimSpace(shard.ContainerStatus) == "" {
			return errors.New("remote CI shard identity and status are required")
		}
		if err := validateRemoteCIShardMaterializationTiming(shard); err != nil {
			return errors.New("remote CI shard materialization timing is invalid")
		}
		shardWorkloads := make(map[GateID]struct{}, len(shard.Workloads))
		for _, workloadID := range shard.Workloads {
			if strings.TrimSpace(string(workloadID)) == "" {
				return errors.New("remote CI shard workload ID is required")
			}
			if _, duplicate := shardWorkloads[workloadID]; duplicate {
				return fmt.Errorf("remote CI shard workload %q is duplicated", workloadID)
			}
			if previousShard, duplicate := seenWorkloads[workloadID]; duplicate {
				return fmt.Errorf(
					"remote CI shard workload %q is duplicated across shards %q and %q",
					workloadID, previousShard, shard.ShardIdentity,
				)
			}
			shardWorkloads[workloadID] = struct{}{}
			seenWorkloads[workloadID] = shard.ShardIdentity
		}
	}
	return nil
}

// validateRemoteCIShardMaterializationTiming 校验物化阶段的真实区间和缓存证据；不适用阶段必须显式声明原因。
func validateRemoteCIShardMaterializationTiming(shard RemoteCIShardRecord) error {
	timing := shard.MaterializationTiming
	if err := timing.Validate(); err != nil {
		return err
	}
	if shard.ContainerGroup == "" {
		if shard.ContainerStatus != "Unknown" || timing.Measurement != MaterializationMeasurementNotMeasured {
			return errors.New("uncreated remote CI shard timing is invalid")
		}
		return nil
	}
	if timing.Measurement == MaterializationMeasurementMeasured && timing.ShardIdentity != shard.ShardIdentity {
		return errors.New("measured remote CI shard timing identity does not match shard")
	}
	if timing.Measurement != MaterializationMeasurementMeasured {
		return errors.New("created remote CI shard materialization timing evidence is required")
	}
	if remoteCIShardTerminalStatus(shard.ContainerStatus) && timing.CandidateCompile.MaterializeMS <= 0 {
		return errors.New("terminal remote CI shard candidate compile timing evidence is required")
	}
	return nil
}

func remoteCIShardTerminalStatus(status string) bool {
	switch status {
	case "Succeeded", "Failed", "ScheduleFailed", "Expired":
		return true
	default:
		return false
	}
}
