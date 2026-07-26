package localci

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
)

const (
	workloadLogicalCPUs       int64 = 4
	workloadMemoryGiB         int64 = 8
	bytesPerGiB               int64 = 1 << 30
	minActiveWorkloads              = 1
	maxAllowedActiveWorkloads       = 64
)

// DaemonCapacityInspector 从受信 Docker API 适配器读取指定 daemon 的总容量。
type DaemonCapacityInspector interface {
	InspectDaemonCapacity(ctx context.Context, daemonID string) (DaemonCapacity, error)
}

// DaemonCapacity 表示一次绑定 Docker daemon 身份和观测时间的容量快照。
type DaemonCapacity struct {
	DaemonID    string
	ObservedAt  time.Time
	LogicalCPUs int64
	MemoryBytes int64
}

// CapacityRequirement 表示启动权威 workload 调度所需的 daemon 总容量。
type CapacityRequirement struct {
	LogicalCPUs int64
	MemoryBytes int64
}

// CapacityEvidence 固化 daemon capacity preflight 的需求和可用容量证据。
type CapacityEvidence struct {
	DaemonID   string
	ObservedAt time.Time
	Required   CapacityRequirement
	Available  DaemonCapacity
}

// ValidateDaemonCapacity 检查指定 daemon 能否兑现固定的权威 workload 并发合同。
func ValidateDaemonCapacity(
	ctx context.Context,
	daemonID string,
	maxActiveWorkloads int,
	inspector DaemonCapacityInspector,
) (CapacityEvidence, error) {
	if err := validateCapacityPreflightInputs(ctx, daemonID, maxActiveWorkloads, inspector); err != nil {
		return CapacityEvidence{}, err
	}

	available, err := inspector.InspectDaemonCapacity(ctx, daemonID)
	if err != nil {
		return CapacityEvidence{}, fmt.Errorf("inspect docker daemon capacity: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return CapacityEvidence{}, fmt.Errorf("inspect docker daemon capacity: %w", err)
	}
	if err := available.Validate(); err != nil {
		return CapacityEvidence{}, fmt.Errorf("validate available daemon capacity: %w", err)
	}
	if available.DaemonID != daemonID {
		return CapacityEvidence{}, fmt.Errorf(
			"docker daemon identity mismatch: requested %q, inspected %q",
			daemonID,
			available.DaemonID,
		)
	}

	required, err := requiredDaemonCapacity(maxActiveWorkloads)
	if err != nil {
		return CapacityEvidence{}, err
	}
	evidence := CapacityEvidence{
		DaemonID:   daemonID,
		ObservedAt: available.ObservedAt,
		Required:   cloneCapacityRequirement(required),
		Available:  cloneDaemonCapacity(available),
	}
	if err := evidence.Validate(); err != nil {
		return CapacityEvidence{}, err
	}
	return evidence, nil
}

// Validate 严格检查 daemon capacity 快照中的身份、时间和资源值。
func (capacity DaemonCapacity) Validate() error {
	if err := validateDaemonID(capacity.DaemonID); err != nil {
		return err
	}
	if capacity.ObservedAt.IsZero() {
		return errors.New("daemon capacity observedAt is required")
	}
	return CapacityRequirement{
		LogicalCPUs: capacity.LogicalCPUs,
		MemoryBytes: capacity.MemoryBytes,
	}.Validate()
}

// Validate 严格检查容量需求中的 CPU 和内存均为正数。
func (requirement CapacityRequirement) Validate() error {
	if requirement.LogicalCPUs <= 0 {
		return fmt.Errorf("logical CPU capacity must be positive: %d", requirement.LogicalCPUs)
	}
	if requirement.MemoryBytes <= 0 {
		return fmt.Errorf("memory capacity must be positive: %d", requirement.MemoryBytes)
	}
	return nil
}

// Validate 严格检查 evidence 的绑定关系和容量下限。
func (evidence CapacityEvidence) Validate() error {
	if err := validateDaemonID(evidence.DaemonID); err != nil {
		return err
	}
	if evidence.ObservedAt.IsZero() {
		return errors.New("capacity evidence observedAt is required")
	}
	if err := evidence.Required.Validate(); err != nil {
		return fmt.Errorf("validate required daemon capacity: %w", err)
	}
	if err := evidence.Available.Validate(); err != nil {
		return fmt.Errorf("validate available daemon capacity: %w", err)
	}
	if evidence.Available.DaemonID != evidence.DaemonID {
		return fmt.Errorf(
			"capacity evidence daemon identity mismatch: evidence %q, available %q",
			evidence.DaemonID,
			evidence.Available.DaemonID,
		)
	}
	if evidence.Available.ObservedAt != evidence.ObservedAt {
		return errors.New("capacity evidence observedAt does not match available capacity")
	}
	if evidence.Available.LogicalCPUs < evidence.Required.LogicalCPUs {
		return fmt.Errorf(
			"docker daemon logical CPU capacity insufficient: available %d, required %d",
			evidence.Available.LogicalCPUs,
			evidence.Required.LogicalCPUs,
		)
	}
	if evidence.Available.MemoryBytes < evidence.Required.MemoryBytes {
		return fmt.Errorf(
			"docker daemon memory capacity insufficient: available %d bytes, required %d bytes",
			evidence.Available.MemoryBytes,
			evidence.Required.MemoryBytes,
		)
	}
	return nil
}

// validateCapacityPreflightInputs 校验节点容量探测所需的身份、容量与上下文。
func validateCapacityPreflightInputs(
	ctx context.Context,
	daemonID string,
	maxActiveWorkloads int,
	inspector DaemonCapacityInspector,
) error {
	if ctx == nil {
		return errors.New("daemon capacity context is nil")
	}
	if isNilCapacityInspector(inspector) {
		return errors.New("daemon capacity inspector is nil")
	}
	if err := validateDaemonID(daemonID); err != nil {
		return err
	}
	if err := validateMaxActiveWorkloads(maxActiveWorkloads); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("inspect docker daemon capacity: %w", err)
	}
	return nil
}

func requiredDaemonCapacity(maxActiveWorkloads int) (CapacityRequirement, error) {
	if err := validateMaxActiveWorkloads(maxActiveWorkloads); err != nil {
		return CapacityRequirement{}, err
	}
	logicalCPUs, err := checkedCapacityProduct(workloadLogicalCPUs, int64(maxActiveWorkloads))
	if err != nil {
		return CapacityRequirement{}, fmt.Errorf("calculate required logical CPU capacity: %w", err)
	}
	workloadMemoryBytes, err := checkedCapacityProduct(workloadMemoryGiB, bytesPerGiB)
	if err != nil {
		return CapacityRequirement{}, fmt.Errorf("calculate workload memory capacity: %w", err)
	}
	memoryBytes, err := checkedCapacityProduct(workloadMemoryBytes, int64(maxActiveWorkloads))
	if err != nil {
		return CapacityRequirement{}, fmt.Errorf("calculate required memory capacity: %w", err)
	}
	return CapacityRequirement{LogicalCPUs: logicalCPUs, MemoryBytes: memoryBytes}, nil
}

func validateMaxActiveWorkloads(value int) error {
	if value < minActiveWorkloads || value > maxAllowedActiveWorkloads {
		return fmt.Errorf("max active workloads %d is outside %d..%d", value, minActiveWorkloads, maxAllowedActiveWorkloads)
	}
	return nil
}

func checkedCapacityProduct(value, multiplier int64) (int64, error) {
	if value <= 0 || multiplier <= 0 {
		return 0, fmt.Errorf("capacity factors must be positive: value=%d multiplier=%d", value, multiplier)
	}
	if value > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("capacity multiplication overflows int64: value=%d multiplier=%d", value, multiplier)
	}
	return value * multiplier, nil
}

func validateDaemonID(daemonID string) error {
	trimmed := strings.TrimSpace(daemonID)
	if trimmed == "" {
		return errors.New("docker daemon ID is required")
	}
	if trimmed != daemonID {
		return errors.New("docker daemon ID must not contain surrounding whitespace")
	}
	return nil
}

func isNilCapacityInspector(inspector DaemonCapacityInspector) bool {
	if inspector == nil {
		return true
	}
	value := reflect.ValueOf(inspector)
	kind := value.Kind()
	return kind >= reflect.Chan && kind <= reflect.Slice && value.IsNil()
}

func cloneCapacityRequirement(requirement CapacityRequirement) CapacityRequirement {
	return CapacityRequirement{
		LogicalCPUs: requirement.LogicalCPUs,
		MemoryBytes: requirement.MemoryBytes,
	}
}

func cloneDaemonCapacity(capacity DaemonCapacity) DaemonCapacity {
	return DaemonCapacity{
		DaemonID:    capacity.DaemonID,
		ObservedAt:  capacity.ObservedAt,
		LogicalCPUs: capacity.LogicalCPUs,
		MemoryBytes: capacity.MemoryBytes,
	}
}
