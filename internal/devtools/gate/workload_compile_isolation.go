package gate

import "fmt"

// compileUnitHardIsolationReason 是 planner 内部的非持久化隔离证明。
type compileUnitHardIsolationReason string

const (
	compileUnitHardIsolationReasonNone           compileUnitHardIsolationReason = "none"
	compileUnitHardIsolationReasonLSPDiagnostics compileUnitHardIsolationReason = "testmain-global-process+aggregate-only-receipt"
)

const lspDiagnosticsCompilePackageTarget = "./scripts/lsp_diagnostics_gate"

func (reason compileUnitHardIsolationReason) validate() error {
	switch reason {
	case compileUnitHardIsolationReasonNone, compileUnitHardIsolationReasonLSPDiagnostics:
		return nil
	default:
		return fmt.Errorf("unknown compile hard-isolation reason %q", reason)
	}
}

func (reason compileUnitHardIsolationReason) hardIsolated() bool {
	return reason == compileUnitHardIsolationReasonLSPDiagnostics
}

// canonicalCompileUnitHardIsolationReason 从 workload ID 与可选 package target
// 重新派生进程/receipt 隔离理由；结果不写入 ShardPlan。
func canonicalCompileUnitHardIsolationReason(workloadID string, packageTargets ...string) (compileUnitHardIsolationReason, error) {
	if len(packageTargets) > 1 {
		return "", fmt.Errorf("compile hard-isolation package target is ambiguous")
	}
	if len(packageTargets) == 1 && packageTargets[0] == "" {
		return "", fmt.Errorf("compile hard-isolation package target is empty")
	}
	packageTarget, parsed, err := compilePackageTargetFromWorkloadID(workloadID)
	if err != nil {
		return "", err
	}
	if len(packageTargets) == 1 {
		if !parsed {
			return "", fmt.Errorf("workload %q has no canonical package target", workloadID)
		}
		if packageTarget != packageTargets[0] {
			return "", fmt.Errorf("workload %q package target %q does not match %q", workloadID, packageTarget, packageTargets[0])
		}
		packageTarget = packageTargets[0]
	}
	if packageTarget == lspDiagnosticsCompilePackageTarget {
		return compileUnitHardIsolationReasonLSPDiagnostics, nil
	}
	return compileUnitHardIsolationReasonNone, nil
}

func compilePackageTargetFromWorkloadID(workloadID string) (string, bool, error) {
	_, kind, payload, targeted, err := ParseWorkloadID(workloadID)
	if err != nil {
		return "", false, fmt.Errorf("parse workload %q for compile isolation: %w", workloadID, err)
	}
	if !targeted {
		return "", false, nil
	}
	switch kind {
	case WorkloadTargetGoPackage:
		return payload, true, nil
	case WorkloadTargetGoTest, WorkloadTargetGoBenchmark:
		target, err := parseCompileGroupTarget(kind, payload)
		if err != nil {
			return "", false, fmt.Errorf("parse workload %q compile target: %w", workloadID, err)
		}
		return target.Package, true, nil
	default:
		return "", false, nil
	}
}

func compilePlanningUnitHardIsolationReason(unit compilePlanningUnit) (compileUnitHardIsolationReason, error) {
	if len(unit.workloads) == 0 {
		return "", fmt.Errorf("compile planning unit has no workloads for hard-isolation derivation")
	}
	reason, err := deriveCompileUnitHardIsolationReason(unit.workloads)
	if err != nil {
		return "", err
	}
	if unit.hardIsolationReason == "" && reason.hardIsolated() {
		return "", fmt.Errorf("compile planning unit hard-isolation reason is empty")
	}
	if unit.hardIsolationReason != "" {
		if err := unit.hardIsolationReason.validate(); err != nil {
			return "", err
		}
		if unit.hardIsolationReason != reason {
			return "", fmt.Errorf("compile planning unit hard-isolation reason %q disagrees with canonical reason %q", unit.hardIsolationReason, reason)
		}
	}
	return reason, nil
}

func deriveCompileUnitHardIsolationReason(workloads []PlannedWorkload) (compileUnitHardIsolationReason, error) {
	reason := compileUnitHardIsolationReasonNone
	for _, planned := range workloads {
		current, err := canonicalCompileUnitHardIsolationReason(planned.Workload.ID)
		if err != nil {
			return "", err
		}
		if !current.hardIsolated() {
			continue
		}
		if reason != compileUnitHardIsolationReasonNone && reason != current {
			return "", fmt.Errorf("compile planning unit mixes hard-isolation reasons")
		}
		reason = current
	}
	return reason, nil
}

func validateCompilePlanningUnitsHardIsolation(units []compilePlanningUnit) error {
	for index, unit := range units {
		if _, err := compilePlanningUnitHardIsolationReason(unit); err != nil {
			return fmt.Errorf("compile planning unit %d hard-isolation: %w", index, err)
		}
	}
	return nil
}

// compileUnitCanShareShard 施加 hard isolation、affinity、artifact、资源 class 与 compile-group 域约束。
func compileUnitCanShareShard(shard ShardPlan, affinities, artifactKeys map[string]struct{}, unit compilePlanningUnit) bool {
	if !compileUnitHardIsolationAllowsShare(shard, unit) {
		return false
	}
	if _, duplicate := affinities[unit.affinityKey]; duplicate {
		return false
	}
	if unit.group == nil {
		return len(shard.CompileGroupIDs) == 0
	}
	return compileGroupCanShareShard(shard, affinities, artifactKeys, unit)
}

func compileUnitHardIsolationAllowsShare(shard ShardPlan, unit compilePlanningUnit) bool {
	reason, err := compilePlanningUnitHardIsolationReason(unit)
	if err != nil || reason.hardIsolated() && len(shard.Workloads) != 0 {
		return false
	}
	for _, planned := range shard.Workloads {
		existing, err := canonicalCompileUnitHardIsolationReason(planned.Workload.ID)
		if err != nil || existing.hardIsolated() {
			return false
		}
	}
	return true
}

// distributeDCPAPWithHardIsolation 在 ordinary planner 中先派生不可共箱 workload，
// 再将其余 workload 交给唯一 D-CPAP solver；无隔离项时保持原布局不变。
func distributeDCPAPWithHardIsolation(planned []PlannedWorkload, targetDurationMS int64) ([]ShardPlan, error) {
	if len(planned) == 0 {
		return distributeDCPAP(planned, targetDurationMS)
	}
	ordinary := make([]PlannedWorkload, 0, len(planned))
	isolated := make([]ShardPlan, 0)
	for _, workload := range planned {
		reason, err := canonicalCompileUnitHardIsolationReason(workload.Workload.ID)
		if err != nil {
			return nil, fmt.Errorf("derive workload %q hard isolation: %w", workload.Workload.ID, err)
		}
		if !reason.hardIsolated() {
			ordinary = append(ordinary, workload)
			continue
		}
		if workload.EstimatedDurationMS <= 0 {
			return nil, fmt.Errorf("workload %q estimate must be positive", workload.Workload.ID)
		}
		isolated = append(isolated, ShardPlan{EstimatedDurationMS: workload.EstimatedDurationMS, Workloads: []PlannedWorkload{workload}})
	}
	if len(isolated) == 0 {
		return distributeDCPAP(planned, targetDurationMS)
	}
	if targetDurationMS <= 0 {
		return distributeDCPAP(planned, targetDurationMS)
	}
	shards := isolated
	if len(ordinary) != 0 {
		ordinaryShards, err := distributeDCPAP(ordinary, targetDurationMS)
		if err != nil {
			return nil, err
		}
		shards = append(shards, ordinaryShards...)
	}
	return canonicalCompilePacking(shards), nil
}
