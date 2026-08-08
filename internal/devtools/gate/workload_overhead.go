package gate

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// ShardOverheadPolicyVersion 标识 deterministic overhead quantile policy。
// 它是 accepted provenance digest 的一部分，不得漂移。
const ShardOverheadPolicyVersion = "accounted-interval-union-nearest-rank-p95-v2"

// ShardOrchestrationOverheadSchemaVersion 标记持久化 overhead aggregate 的版本。
const ShardOrchestrationOverheadSchemaVersion uint32 = 2

// ShardOrchestrationOverhead 是同一 accepted generation、校准资源和环境
// 下由权威分片计时派生的 per-shard overhead P95。它独立于完整 duration
// calibration，允许 generation one 先接受 overhead 再完成 workload calibration。
type ShardOrchestrationOverhead struct {
	SchemaVersion                uint32  `json:"schema_version"`
	PolicyVersion                string  `json:"policy_version"`
	Platform                     string  `json:"platform"`
	Runner                       string  `json:"runner"`
	Toolchain                    string  `json:"toolchain"`
	CalibrationResourceClassID   string  `json:"calibration_resource_class_id"`
	CalibrationResourceCPU       float64 `json:"calibration_resource_cpu"`
	CalibrationResourceMemoryGiB float64 `json:"calibration_resource_memory_gib"`
	P95MS                        int64   `json:"p95_ms"`
	SampleCount                  int     `json:"sample_count"`
	ProvenanceDigest             string  `json:"provenance_digest"`
	AcceptedGeneration           uint64  `json:"accepted_generation"`
	AcceptedSnapshotID           string  `json:"accepted_snapshot_id"`
}

// ShardOrchestrationOverheadSample 保存 aggregate P95 所选择的每个权威分片样本。
// 它是 provenance digest 之外可重新核对的事实，禁止只保留不透明摘要。
type ShardOrchestrationOverheadSample struct {
	AcceptedGeneration     uint64    `json:"accepted_generation"`
	ProvenanceDigest       string    `json:"provenance_digest"`
	JobID                  string    `json:"job_id"`
	ShardIdentity          string    `json:"shard_identity"`
	TotalStartedAt         time.Time `json:"total_started_at"`
	TotalCompletedAt       time.Time `json:"total_completed_at"`
	WorkloadEnvelopeStart  time.Time `json:"workload_envelope_start"`
	WorkloadEnvelopeEnd    time.Time `json:"workload_envelope_end"`
	AccountedDurationMS    int64     `json:"accounted_duration_ms"`
	AccountedIntervalCount int       `json:"accounted_interval_count"`
	OverheadMS             int64     `json:"overhead_ms"`
}

// ValidateShardOrchestrationOverhead 校验可复核的 accepted overhead 身份。
func ValidateShardOrchestrationOverhead(overhead ShardOrchestrationOverhead) error {
	if err := validateShardOverheadIdentity(overhead); err != nil {
		return err
	}
	if err := validateShardOverheadResourceMetadata(overhead); err != nil {
		return err
	}
	return validateShardOverheadMeasurement(overhead)
}

// validateShardOverheadIdentity 校验 overhead 的 schema、环境和 provenance。
func validateShardOverheadIdentity(overhead ShardOrchestrationOverhead) error {
	if overhead.SchemaVersion != ShardOrchestrationOverheadSchemaVersion || overhead.PolicyVersion != ShardOverheadPolicyVersion {
		return errors.New("shard orchestration overhead schema or policy is invalid")
	}
	if err := validateDurationEnvironment(overhead.Platform, overhead.Runner, overhead.Toolchain); err != nil {
		return err
	}
	if !isPrefixedSHA256Digest(overhead.ProvenanceDigest) || overhead.AcceptedGeneration == 0 || strings.TrimSpace(overhead.AcceptedSnapshotID) == "" {
		return errors.New("shard orchestration overhead provenance identity is invalid")
	}
	return nil
}

func validateShardOverheadResourceMetadata(overhead ShardOrchestrationOverhead) error {
	if err := cicontract.ValidateCalibrationResources(overhead.CalibrationResourceClassID, overhead.CalibrationResourceCPU, overhead.CalibrationResourceMemoryGiB); err != nil {
		return fmt.Errorf("shard orchestration overhead calibration resource: %w", err)
	}
	return nil
}

func validateShardOverheadMeasurement(overhead ShardOrchestrationOverhead) error {
	if overhead.P95MS < 0 || overhead.P95MS >= FullCITargetDurationMS {
		return errors.New("shard orchestration overhead p95 must leave a positive planning budget")
	}
	if overhead.SampleCount <= 0 {
		return errors.New("shard orchestration overhead sample count must be positive")
	}
	return nil
}

// ValidateShardOrchestrationOverheadSample 校验 total interval 减 accounted
// interval union 的非负公式和可持久化身份。
func ValidateShardOrchestrationOverheadSample(sample ShardOrchestrationOverheadSample) error {
	if sample.AcceptedGeneration == 0 || !isPrefixedSHA256Digest(sample.ProvenanceDigest) {
		return errors.New("shard orchestration overhead sample identity is invalid")
	}
	return ValidateShardOrchestrationOverheadSampleIntervals(sample)
}

func validatePlanningContext(context PlanningContext) error {
	if err := validatePlanningContextBase(context); err != nil {
		return err
	}
	if context.Calibration && planningContextOverheadIsEmpty(context) {
		return nil
	}
	if err := validateShardOverheadMetadata(
		context.ShardOverheadP95MS,
		context.ShardOverheadSampleCount,
		context.ShardOverheadProvenanceDigest,
		context.TargetDurationMS,
		false,
	); err != nil {
		return fmt.Errorf("shard orchestration overhead: %w", err)
	}
	return nil
}

// validatePlanningContextBase 校验环境、accepted snapshot 和名义目标。
func validatePlanningContextBase(context PlanningContext) error {
	if err := cicontract.ValidateShardTargetDuration(time.Duration(context.TargetDurationMS) * time.Millisecond); err != nil {
		return fmt.Errorf("target_duration_ms: %w", err)
	}
	if err := validateDurationEnvironment(context.Platform, context.Runner, context.Toolchain); err != nil {
		return err
	}
	if strings.TrimSpace(context.AcceptedSnapshotID) == "" {
		return errors.New("planning accepted snapshot identity is required")
	}
	if context.Calibration {
		if err := cicontract.ValidateCalibrationResources(context.CalibrationResourceClassID, context.CalibrationResourceCPU, context.CalibrationResourceMemoryGiB); err != nil {
			return fmt.Errorf("planning calibration resource: %w", err)
		}
	} else if context.CalibrationResourceClassID != "" || context.CalibrationResourceCPU != 0 || context.CalibrationResourceMemoryGiB != 0 {
		return errors.New("normal planning must not carry calibration resource identity")
	}
	return nil
}

func planningContextOverheadIsEmpty(context PlanningContext) bool {
	return context.ShardOverheadP95MS == 0 &&
		context.ShardOverheadSampleCount == 0 &&
		context.ShardOverheadProvenanceDigest == ""
}

// validateShardOverheadMetadata 拒绝缺样本、负时长或不可复核的 overhead 身份。
// p95=0 是合法的真实测量结果；缺少 sample count 或 provenance digest 则不是。
func validateShardOverheadMetadata(p95MS int64, sampleCount int, provenanceDigest string, targetDurationMS int64, allowEmpty bool) error {
	if allowEmpty && p95MS == 0 && sampleCount == 0 && provenanceDigest == "" {
		return nil
	}
	if p95MS < 0 {
		return errors.New("p95 milliseconds must not be negative")
	}
	if sampleCount <= 0 {
		return errors.New("sample count must be positive")
	}
	if !isPrefixedSHA256Digest(provenanceDigest) {
		return errors.New("provenance digest must be a prefixed SHA-256 digest")
	}
	if targetDurationMS <= 0 || p95MS >= targetDurationMS {
		return errors.New("p95 milliseconds must leave a positive effective planning budget")
	}
	return nil
}

// ResolvePlanningContext 将规划绑定到同环境的 accepted overhead。
// calibration mode 允许 generation one 在尚无 accepted overhead 时执行采样。
func ResolvePlanningContext(context PlanningContext, ledger DurationLedger) (PlanningContext, error) {
	if err := validatePlanningContextBase(context); err != nil {
		return PlanningContext{}, err
	}
	if err := validateAcceptedCalibration(context, ledger.Calibration); err != nil {
		return PlanningContext{}, err
	}
	overhead := ledger.ShardOverhead
	if overhead == nil {
		return resolveWithoutAcceptedCalibration(context, ledger.Calibration)
	}
	if err := ValidateShardOrchestrationOverhead(*overhead); err != nil {
		return PlanningContext{}, fmt.Errorf("accepted shard overhead: %w", err)
	}
	if err := validateAcceptedOverheadBinding(context, *overhead); err != nil {
		return PlanningContext{}, err
	}
	context = bindPlanningOverhead(context, *overhead)
	if err := validatePlanningContext(context); err != nil {
		return PlanningContext{}, err
	}
	return context, nil
}

// validateAcceptedCalibration 校验已接受校准的完整身份，并保持 normal 与 calibration 资源隔离。
func validateAcceptedCalibration(context PlanningContext, calibration *DurationCalibration) error {
	if calibration == nil {
		return nil
	}
	if err := ValidateDurationCalibration(*calibration); err != nil {
		return fmt.Errorf("accepted duration calibration: %w", err)
	}
	if !context.Calibration {
		return nil
	}
	if context.CalibrationResourceClassID != calibration.CalibrationResourceClassID ||
		context.CalibrationResourceCPU != calibration.CalibrationResourceCPU ||
		context.CalibrationResourceMemoryGiB != calibration.CalibrationResourceMemoryGiB {
		return errors.New("calibration planning resource does not match accepted duration calibration")
	}
	return nil
}

// resolveWithoutAcceptedCalibration 在缺少 accepted overhead 时仅允许 calibration 采样并校验旧证据身份。
func resolveWithoutAcceptedCalibration(context PlanningContext, calibration *DurationCalibration) (PlanningContext, error) {
	if !context.Calibration {
		return PlanningContext{}, errors.New("normal planning requires an accepted duration calibration with shard overhead")
	}
	if calibration != nil && context.AcceptedSnapshotID != calibration.AcceptedSnapshotID {
		return PlanningContext{}, errors.New("calibration planning snapshot does not match accepted duration calibration")
	}
	if calibration != nil && (context.CalibrationResourceClassID != calibration.CalibrationResourceClassID || context.CalibrationResourceCPU != calibration.CalibrationResourceCPU || context.CalibrationResourceMemoryGiB != calibration.CalibrationResourceMemoryGiB) {
		return PlanningContext{}, errors.New("calibration planning resource does not match accepted duration calibration")
	}
	if err := validatePlanningContext(context); err != nil {
		return PlanningContext{}, err
	}
	return context, nil
}

// validateAcceptedOverheadBinding 校验规划环境与 accepted overhead 完全一致。
func validateAcceptedOverheadBinding(context PlanningContext, overhead ShardOrchestrationOverhead) error {
	if context.Platform != overhead.Platform || context.Runner != overhead.Runner || context.Toolchain != overhead.Toolchain {
		return errors.New("planning context environment does not match accepted shard overhead")
	}
	if context.AcceptedSnapshotID != overhead.AcceptedSnapshotID {
		return errors.New("planning context accepted snapshot does not match accepted shard overhead")
	}
	if planningContextOverheadIsEmpty(context) {
		return nil
	}
	if context.ShardOverheadP95MS != overhead.P95MS ||
		context.ShardOverheadSampleCount != overhead.SampleCount ||
		context.ShardOverheadProvenanceDigest != overhead.ProvenanceDigest {
		return errors.New("planning context shard overhead identity does not match accepted shard overhead")
	}
	return nil
}

func bindPlanningOverhead(context PlanningContext, overhead ShardOrchestrationOverhead) PlanningContext {
	if planningContextOverheadIsEmpty(context) {
		context.ShardOverheadP95MS = overhead.P95MS
		context.ShardOverheadSampleCount = overhead.SampleCount
		context.ShardOverheadProvenanceDigest = overhead.ProvenanceDigest
	}
	return context
}

// EffectiveTargetDurationMS 返回扣除 accepted overhead 后的分片预算。
func (context PlanningContext) EffectiveTargetDurationMS() (int64, error) {
	if err := validatePlanningContext(context); err != nil {
		return 0, err
	}
	if context.Calibration {
		return context.TargetDurationMS, nil
	}
	effective := context.TargetDurationMS - context.ShardOverheadP95MS
	if effective <= 0 {
		return 0, errors.New("effective planning budget must be positive")
	}
	return effective, nil
}
