package heterociworkflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// DeriveExecutionMaterialDigest 以 domain-separated length framing 绑定完整执行材料。
// MaterialDigest 字段自身不参与摘要，避免自引用；其余 batch identity、单位、assignment 与 started 全部参与。
func DeriveExecutionMaterialDigest(batch ExecutionBatch) (string, error) {
	if err := validateExecutionMaterialShape(batch); err != nil {
		return "", err
	}
	parts, err := marshalExecutionMaterialParts(batch)
	if err != nil {
		return "", err
	}
	return "sha256:" + workflowDigest("heteroci-workflow-execution-material/v1", parts...), nil
}

// validateExecutionMaterialShape 拒绝缺失或顺序漂移的 batch 材料。
func validateExecutionMaterialShape(batch ExecutionBatch) error {
	if err := validateExecutionMaterialBasics(batch); err != nil {
		return err
	}
	unitIDs := make([]string, len(batch.Units))
	for index, unit := range batch.Units {
		unitIDs[index] = unit.UnitID
	}
	if err := validateExecutionMaterialBatchIdentity(batch, unitIDs); err != nil {
		return err
	}
	if err := validateExecutionMaterialAssignments(batch); err != nil {
		return err
	}
	return validateExecutionMaterialStarted(batch)
}

// validateExecutionMaterialBasics 校验 batch 的必需集合和基础 lane。
func validateExecutionMaterialBasics(batch ExecutionBatch) error {
	if batch.BatchID == "" || batch.RunID == "" || len(batch.Units) == 0 || len(batch.Assignments) != len(batch.Units) || len(batch.Started) != len(batch.Units) {
		return fmt.Errorf("%w: execution material shape is incomplete", ErrBatchResult)
	}
	if err := batch.Key.Validate(); err != nil {
		return fmt.Errorf("%w: execution material executor key is invalid", ErrBatchResult)
	}
	return heteroci.ValidateExecutionUnits(batch.Units)
}

// validateExecutionMaterialBatchIdentity 校验 BatchID 与完整 unit 顺序的一致性。
func validateExecutionMaterialBatchIdentity(batch ExecutionBatch, unitIDs []string) error {
	wantID, err := DeriveBatchID(batch.RunID, batch.Key, batch.Wave, unitIDs)
	if err != nil || wantID != batch.BatchID {
		return fmt.Errorf("%w: execution material batch identity drifted", ErrBatchResult)
	}
	return nil
}

// validateExecutionMaterialAssignments 校验 assignment 顺序和 executor lane 绑定。
func validateExecutionMaterialAssignments(batch ExecutionBatch) error {
	for index, assignment := range batch.Assignments {
		if assignment.UnitID != batch.Units[index].UnitID || assignment.Executor != batch.Key.Kind {
			return fmt.Errorf("%w: execution material assignment identity drifted", ErrBatchResult)
		}
		if err := validateExecutionMaterialAssignmentForKey(batch.Key, assignment); err != nil {
			return err
		}
	}
	return nil
}

// validateExecutionMaterialAssignmentForKey 校验 local/ECI assignment 的完整 lane 形状。
func validateExecutionMaterialAssignmentForKey(key ExecutorKey, assignment heteroci.RouteAssignment) error {
	switch key.Kind {
	case heteroci.ExecutorKindLocalNode:
		return validateExecutionMaterialLocalAssignment(key, assignment)
	case heteroci.ExecutorKindAliyunECI:
		return validateExecutionMaterialECIAssignment(key, assignment)
	default:
		return fmt.Errorf("%w: execution material executor kind is invalid", ErrBatchResult)
	}
}

// validateExecutionMaterialLocalAssignment 校验 local node assignment 的端点与探针身份。
func validateExecutionMaterialLocalAssignment(key ExecutorKey, assignment heteroci.RouteAssignment) error {
	if assignment.NodeID != key.NodeID || assignment.ECITier != "" || assignment.Endpoint == "" || assignment.ProbeIdentity == "" || assignment.ProbeBootID == "" {
		return fmt.Errorf("%w: execution material local assignment is invalid", ErrBatchResult)
	}
	return nil
}

// validateExecutionMaterialECIAssignment 校验 ECI assignment 不携带 local identity。
func validateExecutionMaterialECIAssignment(key ExecutorKey, assignment heteroci.RouteAssignment) error {
	if assignment.NodeID != "" || assignment.Endpoint != "" || assignment.ProbeIdentity != "" || assignment.ProbeBootID != "" || assignment.ECITier != key.ECITier {
		return fmt.Errorf("%w: execution material ECI assignment is invalid", ErrBatchResult)
	}
	return nil
}

// validateExecutionMaterialStarted 校验每个 started lease 的 fence、lease 和 occupancy identity。
func validateExecutionMaterialStarted(batch ExecutionBatch) error {
	for index, started := range batch.Started {
		if err := validateExecutionMaterialStartedLease(batch, index, started); err != nil {
			return err
		}
		if err := validateExecutionMaterialAssignmentLease(batch.Assignments[index], started.Lease); err != nil {
			return err
		}
		if err := validateExecutionMaterialStartedOccupancy(started); err != nil {
			return err
		}
	}
	return nil
}

// validateExecutionMaterialAssignmentLease 将 started lease 的不可变路由字段绑定回 assignment。
func validateExecutionMaterialAssignmentLease(assignment heteroci.RouteAssignment, lease heteroci.RoutingLease) error {
	if lease.Executor != assignment.Executor || lease.NodeID != assignment.NodeID || lease.Endpoint != assignment.Endpoint || lease.ProbeIdentity != assignment.ProbeIdentity || lease.ProbeBootID != assignment.ProbeBootID || lease.ECITier != assignment.ECITier {
		return fmt.Errorf("%w: execution material assignment and started lease drifted", ErrBatchResult)
	}
	return nil
}

// validateExecutionMaterialStartedLease 校验 started fence、run、unit、状态和 executor。
func validateExecutionMaterialStartedLease(batch ExecutionBatch, index int, started heteroci.StartedRoutingLease) error {
	if err := started.Fence.Validate(); err != nil {
		return fmt.Errorf("%w: execution material started fence is invalid", ErrBatchResult)
	}
	lease := started.Lease
	if lease.RunID != batch.RunID || lease.UnitID != batch.Units[index].UnitID || lease.State != heteroci.LeaseStateStarted || lease.Executor != batch.Key.Kind {
		return fmt.Errorf("%w: execution material started lease identity drifted", ErrBatchResult)
	}
	return nil
}

// validateExecutionMaterialStartedOccupancy 校验 started lease 的 epoch 与 decision identity。
func validateExecutionMaterialStartedOccupancy(started heteroci.StartedRoutingLease) error {
	lease := started.Lease
	occupancy := started.Occupancy
	if err := occupancy.Validate(); err != nil || occupancy.OwnerEpoch != started.Fence.OwnerEpoch || occupancy.RunID != lease.RunID || occupancy.UnitID != lease.UnitID || occupancy.DecisionDigest != lease.DecisionDigest {
		return fmt.Errorf("%w: execution material started occupancy drifted", ErrBatchResult)
	}
	return nil
}

// marshalExecutionMaterialParts 编码每个字段后交给 workflowDigest 做长度分帧。
func marshalExecutionMaterialParts(batch ExecutionBatch) ([]string, error) {
	values := []any{batch.BatchID, batch.RunID, batch.Key, batch.Wave, batch.Units, batch.Assignments, batch.Started}
	parts := make([]string, len(values))
	for index, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("%w: marshal execution material field %d", ErrBatchResult, index)
		}
		parts[index] = string(encoded)
	}
	return parts, nil
}

// validWorkflowDigest 校验 workflow 边界使用的小写 sha256 摘要形状。
func validWorkflowDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
