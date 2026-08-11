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
	return record.Status == ResultStatusPassed && remoteCIRunAllReuseShape(record)
}

// remoteCIRunHasOnlyReusedCancelledAudit 识别 job 身份后取消且尚未执行 fresh 的 all-hit 审计投影。
// 该形态只允许非权威 cancelled/timeout，且必须保留 reuse workload 结果，不放宽权威计时校验。
func remoteCIRunHasOnlyReusedCancelledAudit(record RemoteCIRunRecord) bool {
	if record.Authoritative || !remoteCIRunCancelledAuditStatus(record.Status) {
		return false
	}
	if strings.TrimSpace(record.CandidateGateSourceSHA256) != "" || strings.TrimSpace(record.CandidateGateToolchainSHA256) != "" {
		return false
	}
	if record.CleanupComplete || len(record.Executions) != 0 {
		return false
	}
	return remoteCIRunAllReuseShape(record)
}

func remoteCIRunCancelledAuditStatus(status ResultStatus) bool {
	return status == ResultStatusCancelled || status == ResultStatusTimeout
}

func remoteCIRunAllReuseShape(record RemoteCIRunRecord) bool {
	if len(record.WorkloadResults) == 0 || len(record.Shards) != 0 || len(record.WorkloadExecutions) != 0 ||
		len(record.TimingObservations) != 0 || len(record.CompileTimingObservations) != 0 ||
		len(record.DurationSamples) != 0 {
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
		"agent token digest":   record.AgentTokenDigest,
		"job ID":               record.JobID,
		"entrypoint":           string(record.Entrypoint),
		"profile":              string(record.Profile),
		"plan digest":          record.PlanDigest,
		"catalog digest":       record.CatalogDigest,
		"image cache snapshot": record.ImageCacheSnapshotID,
		"source tree":          record.SourceTreeSHA,
		"runner image":         record.RunnerImage,
		"status":               string(record.Status),
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("remote CI run %s is required", field)
		}
	}
	if err := validateRemoteCIRunCandidateGateIdentity(record); err != nil {
		return err
	}
	return nil
}

// validateRemoteCIRunCandidateGateIdentity 只允许纯复用 PASS 或取消审计投影省略候选 Gate 编译身份。
func validateRemoteCIRunCandidateGateIdentity(record RemoteCIRunRecord) error {
	source := strings.TrimSpace(record.CandidateGateSourceSHA256)
	toolchain := strings.TrimSpace(record.CandidateGateToolchainSHA256)
	if remoteCIRunHasOnlyReusedWorkloadResults(record) || remoteCIRunHasOnlyReusedCancelledAudit(record) {
		if source != "" || toolchain != "" {
			return errors.New("all-hit remote CI run must omit candidate gate compile identity")
		}
		return nil
	}
	if !isPrefixedSHA256Digest(source) || !isPrefixedSHA256Digest(toolchain) {
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
		expectedFlags, err := WorkloadExecutionGoFlags(string(execution.GateID))
		if err != nil {
			return fmt.Errorf("remote CI workload execution %q expected GoFlags: %w", execution.GateID, err)
		}
		if execution.ExecutionProfile.GoFlags != expectedFlags {
			return fmt.Errorf("remote CI workload execution %q profile GoFlags %q does not match expected %q", execution.GateID, execution.ExecutionProfile.GoFlags, expectedFlags)
		}
		if err := ValidatePlanGateTimingEvidence(execution); err != nil {
			return fmt.Errorf("remote CI workload execution %q timing evidence: %w", execution.GateID, err)
		}
	}
	return nil
}

// validateRemoteCIRunShards 校验分片归属、资源规格和终态与运行计划一致，避免无证据分片进入权威投影。
func validateRemoteCIRunShards(record RemoteCIRunRecord) error {
	seenWorkloads := make(map[GateID]string)
	uncreatedWorkloads := make(map[GateID]string)
	for _, shard := range record.Shards {
		if err := validateRemoteCIShardEvidence(record.Status, record.Authoritative, shard, uncreatedWorkloads); err != nil {
			return err
		}
		if err := validateRemoteCIShardWorkloads(shard, seenWorkloads); err != nil {
			return err
		}
	}
	return rejectRemoteCIUncreatedExecutions(record, uncreatedWorkloads)
}

// validateRemoteCIShardEvidence 校验分片身份、物化证据及失败终态允许的未创建占位。
func validateRemoteCIShardEvidence(status ResultStatus, authoritative bool, shard RemoteCIShardRecord, uncreatedWorkloads map[GateID]string) error {
	if strings.TrimSpace(shard.ShardIdentity) == "" || strings.TrimSpace(shard.ContainerStatus) == "" {
		return errors.New("remote CI shard identity and status are required")
	}
	if err := validateRemoteCIShardMaterializationTiming(status, authoritative, shard); err != nil {
		return errors.New("remote CI shard materialization timing is invalid")
	}
	if !remoteCIShardWasNotCreated(shard) {
		return validateRemoteCIShardResources(status, authoritative, shard)
	}
	if status == ResultStatusPassed || status == ResultStatusPassedStalePolicy {
		return fmt.Errorf("remote CI %s run cannot contain uncreated shard %q", status, shard.ShardIdentity)
	}
	for _, workloadID := range shard.Workloads {
		uncreatedWorkloads[workloadID] = shard.ShardIdentity
	}
	return nil
}

// validateRemoteCIShardResources 校验实际资源规格；失败 provisional 可保留未观察到的空资源证据。
func validateRemoteCIShardResources(status ResultStatus, authoritative bool, shard RemoteCIShardRecord) error {
	if remoteCIShardResourcesMissingAllowed(status, authoritative, shard) {
		return nil
	}
	if err := shard.Resources.Validate(); err != nil {
		return fmt.Errorf("remote CI shard resources are invalid: %w", err)
	}
	return nil
}

// remoteCIShardResourcesMissingAllowed 只允许失败 provisional 显式缺失资源，不把零值解释为规格。
func remoteCIShardResourcesMissingAllowed(status ResultStatus, authoritative bool, shard RemoteCIShardRecord) bool {
	if authoritative || !remoteCIProvisionalFailureStatus(status) {
		return false
	}
	if remoteCIShardWasNotCreated(shard) {
		return true
	}
	return shard.ContainerGroup != "" && shard.Resources == (RemoteCIShardResources{})
}

func validateRemoteCIShardWorkloads(shard RemoteCIShardRecord, seenWorkloads map[GateID]string) error {
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
	return nil
}

// rejectRemoteCIUncreatedExecutions 禁止将未创建分片的 workload 伪装为已执行证据。
func rejectRemoteCIUncreatedExecutions(record RemoteCIRunRecord, uncreatedWorkloads map[GateID]string) error {
	for _, executions := range [][]PlanGateExecution{record.Executions, record.WorkloadExecutions} {
		for _, execution := range executions {
			if shardIdentity, ok := uncreatedWorkloads[execution.GateID]; ok {
				return fmt.Errorf("remote CI execution %q is bound to uncreated shard %q", execution.GateID, shardIdentity)
			}
		}
	}
	return nil
}

// remoteCIShardWasNotCreated 标识唯一允许省略 resource evidence 的 placeholder
// 形态：没有 provider group、Unknown status 且没有 timing。
func remoteCIShardWasNotCreated(shard RemoteCIShardRecord) bool {
	return shard.ContainerGroup == "" && shard.ContainerStatus == "Unknown" &&
		shard.MaterializationTiming.Measurement == MaterializationMeasurementNotMeasured
}

// validateRemoteCIShardMaterializationTiming 校验物化阶段的真实区间和缓存证据。
// 失败 provisional 可以诚实保留已创建但未观察到的分片；PASS 或 authoritative 仍必须有 measured 证据。
func validateRemoteCIShardMaterializationTiming(status ResultStatus, authoritative bool, shard RemoteCIShardRecord) error {
	timing := shard.MaterializationTiming
	if err := timing.Validate(); err != nil {
		return err
	}
	if shard.ContainerGroup == "" {
		return validateUncreatedRemoteCIShardTiming(shard)
	}
	return validateCreatedRemoteCIShardTiming(status, authoritative, shard)
}

// validateUncreatedRemoteCIShardTiming 只允许没有云资源的 Unknown/not_measured 占位形态。
func validateUncreatedRemoteCIShardTiming(shard RemoteCIShardRecord) error {
	if remoteCIShardWasNotCreated(shard) {
		return nil
	}
	return errors.New("uncreated remote CI shard timing is invalid")
}

// validateCreatedRemoteCIShardTiming 分派 measured 与失败 provisional 的 unavailable 证据校验。
func validateCreatedRemoteCIShardTiming(status ResultStatus, authoritative bool, shard RemoteCIShardRecord) error {
	switch shard.MaterializationTiming.Measurement {
	case MaterializationMeasurementMeasured:
		return validateMeasuredRemoteCIShardTiming(shard)
	case MaterializationMeasurementUnavailable:
		return validateUnavailableRemoteCIShardTiming(status, authoritative)
	default:
		return errors.New("created remote CI shard materialization timing evidence is required")
	}
}

// validateMeasuredRemoteCIShardTiming 校验 measured 证据绑定分片，并要求终态有候选编译区间。
func validateMeasuredRemoteCIShardTiming(shard RemoteCIShardRecord) error {
	timing := shard.MaterializationTiming
	if timing.ShardIdentity != shard.ShardIdentity {
		return errors.New("measured remote CI shard timing identity does not match shard")
	}
	if remoteCIShardTerminalStatus(shard.ContainerStatus) && timing.CandidateCompile.MaterializeMS <= 0 {
		return errors.New("terminal remote CI shard candidate compile timing evidence is required")
	}
	return nil
}

// validateUnavailableRemoteCIShardTiming 仅在失败 provisional 中保留诚实的未观察证据。
func validateUnavailableRemoteCIShardTiming(status ResultStatus, authoritative bool) error {
	if remoteCIStatusRequiresMeasuredTiming(status, authoritative) {
		return errors.New("passed remote CI run requires measured shard materialization timing evidence")
	}
	if !remoteCIProvisionalFailureStatus(status) {
		return errors.New("remote CI shard unavailable timing requires a failure provisional status")
	}
	return nil
}

// remoteCIStatusRequiresMeasuredTiming 判断 PASS 或 authoritative 投影是否必须具备 measured 证据。
func remoteCIStatusRequiresMeasuredTiming(status ResultStatus, authoritative bool) bool {
	return authoritative || status == ResultStatusPassed || status == ResultStatusPassedStalePolicy
}

func remoteCIProvisionalFailureStatus(status ResultStatus) bool {
	switch status {
	case ResultStatusFailed, ResultStatusCancelled, ResultStatusTimeout, ResultStatusInfraFailed:
		return true
	default:
		return false
	}
}

func remoteCIShardTerminalStatus(status string) bool {
	switch status {
	case "Succeeded", "Failed", "ScheduleFailed", "Expired":
		return true
	default:
		return false
	}
}
