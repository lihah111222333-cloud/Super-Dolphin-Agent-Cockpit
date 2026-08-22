package heterociworkflow

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// OperationPhase 是 durable operation identity 的稳定阶段枚举。
type OperationPhase = heteroci.TopologyOperationPhase

const (
	OperationPhasePlan          = heteroci.TopologyOperationPhasePlan
	OperationPhaseStart         = heteroci.TopologyOperationPhaseStart
	OperationPhaseFinish        = heteroci.TopologyOperationPhaseFinish
	OperationPhaseCancelPlanned = heteroci.TopologyOperationPhaseCancelPlanned
)

// BatchID 是跨重启保持稳定的 execution batch identity。
type BatchID string

// DeriveOperationID 从内部 durable run key、phase、unit 派生确定性 operation identity。
func DeriveOperationID(durableRunID heteroci.TopologyRunID, phase OperationPhase, unitID string) (heteroci.OperationID, error) {
	return heteroci.DeriveTopologyOperationID(durableRunID, phase, unitID)
}

// DeriveBatchID 从 run、executor lane、wave 和 unit 集合派生稳定 batch identity。
func DeriveBatchID(runID string, key ExecutorKey, wave int, unitIDs []string) (BatchID, error) {
	if err := validateWorkflowRunID(runID); err != nil {
		return "", err
	}
	if err := key.Validate(); err != nil {
		return "", err
	}
	if len(unitIDs) == 0 || wave < -1 {
		return "", errors.New("heteroci workflow batch shape is invalid")
	}
	parts := []string{runID, string(key.Kind), key.NodeID, strconv.Itoa(wave)}
	for _, unitID := range unitIDs {
		if !validWorkflowUnitID(unitID) {
			return "", errors.New("heteroci workflow batch unit is invalid")
		}
		parts = append(parts, unitID)
	}
	return BatchID(workflowDigest("heteroci-workflow-batch/v2", parts...)), nil
}

// Validate 校验 executor lane 的平台绑定形状。
func (key ExecutorKey) Validate() error {
	switch key.Kind {
	case heteroci.ExecutorKindLocalNode:
		if !validWorkflowUnitID(key.NodeID) {
			return errors.New("heteroci workflow local executor key is invalid")
		}
	default:
		return errors.New("heteroci workflow executor kind is invalid")
	}
	return nil
}

// Validate 校验一次 run 的 executor key/batch 绑定计划是完整、排序且去重的。
func (plan ExecutorBindingPlan) Validate() error {
	if len(plan.Bindings) == 0 {
		return errors.New("heteroci workflow executor binding plan is empty")
	}
	seenKeys := make(map[ExecutorKey]struct{}, len(plan.Bindings))
	seenBatches := make(map[BatchID]struct{})
	var previous ExecutorKey
	for index, binding := range plan.Bindings {
		if err := validateExecutorBinding(index, previous, binding, seenKeys, seenBatches); err != nil {
			return err
		}
		previous = binding.Key
	}
	return nil
}

// validateExecutorBinding 校验单个 key 的顺序、唯一性与 batch 覆盖。
func validateExecutorBinding(index int, previous ExecutorKey, binding ExecutorBinding, seenKeys map[ExecutorKey]struct{}, seenBatches map[BatchID]struct{}) error {
	if err := binding.Key.Validate(); err != nil {
		return errors.New("heteroci workflow executor binding plan key is invalid")
	}
	if index > 0 && !executorKeyLess(previous, binding.Key) {
		return errors.New("heteroci workflow executor binding plan keys are not strictly sorted")
	}
	if _, duplicate := seenKeys[binding.Key]; duplicate {
		return errors.New("heteroci workflow executor binding plan key is duplicated")
	}
	seenKeys[binding.Key] = struct{}{}
	return validateExecutorBindingBatches(binding.BatchIDs, seenBatches)
}

// validateExecutorBindingBatches 校验一个 key 的 batch identity 严格排序且全局唯一。
func validateExecutorBindingBatches(batchIDs []BatchID, seenBatches map[BatchID]struct{}) error {
	if len(batchIDs) == 0 {
		return errors.New("heteroci workflow executor binding plan has no batches")
	}
	var previous BatchID
	for index, batchID := range batchIDs {
		if !validWorkflowUnitID(string(batchID)) {
			return errors.New("heteroci workflow executor binding plan batch id is invalid")
		}
		if index > 0 && string(previous) >= string(batchID) {
			return errors.New("heteroci workflow executor binding plan batches are not strictly sorted")
		}
		if _, duplicate := seenBatches[batchID]; duplicate {
			return errors.New("heteroci workflow executor binding plan batch is duplicated")
		}
		seenBatches[batchID] = struct{}{}
		previous = batchID
	}
	return nil
}

// executorKeyLess 定义 binding plan 的稳定 key 排序。
func executorKeyLess(left, right ExecutorKey) bool {
	if left.Kind != right.Kind {
		return string(left.Kind) < string(right.Kind)
	}
	return left.NodeID < right.NodeID
}

// sortExecutorBindingPlan 返回 caller slice 与 batch slice 均不共享的稳定计划副本。
func sortExecutorBindingPlan(bindings map[ExecutorKey][]BatchID) ExecutorBindingPlan {
	keys := make([]ExecutorKey, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool { return executorKeyLess(keys[left], keys[right]) })
	plan := ExecutorBindingPlan{Bindings: make([]ExecutorBinding, 0, len(keys))}
	for _, key := range keys {
		batchIDs := append([]BatchID(nil), bindings[key]...)
		sort.Slice(batchIDs, func(left, right int) bool { return string(batchIDs[left]) < string(batchIDs[right]) })
		plan.Bindings = append(plan.Bindings, ExecutorBinding{Key: key, BatchIDs: batchIDs})
	}
	return plan
}

// validateWorkflowRunID 校验 run identity 的非空、长度、空白和控制字符边界。
func validateWorkflowRunID(runID string) error {
	if runID == "" || strings.TrimSpace(runID) != runID || len(runID) > 256 {
		return errors.New("heteroci workflow run id is invalid")
	}
	for _, character := range runID {
		if character < 0x20 || character == 0x7f {
			return errors.New("heteroci workflow run id is invalid")
		}
	}
	return nil
}

// validWorkflowUnitID 判断 unit/node identity 是否满足稳定非控制字符边界。
func validWorkflowUnitID(unitID string) bool {
	if unitID == "" || strings.TrimSpace(unitID) != unitID || len(unitID) > 256 {
		return false
	}
	for _, character := range unitID {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func workflowDigest(domain string, parts ...string) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(domain)
	for _, part := range parts {
		write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
