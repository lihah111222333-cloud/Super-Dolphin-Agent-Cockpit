package remoteci

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func remoteShardableWorkloads(catalog gate.WorkloadCatalog) []gate.Workload {
	workloads := make([]gate.Workload, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			workloads = append(workloads, workload)
		}
	}
	return workloads
}

// remoteExecutionCatalog 从已验证的完整 catalog 计划派生 worker 输入，而不创建新的权威目录。
func remoteExecutionCatalog(plan gate.WorkloadExecutionPlan) gate.WorkloadCatalog {
	selected := make(map[gate.GateID]struct{}, len(plan.ExecutionWorkloadIDs))
	for _, id := range plan.ExecutionWorkloadIDs {
		selected[id] = struct{}{}
	}
	catalog := gate.WorkloadCatalog{Version: plan.Catalog.Version, Authoritative: false}
	for _, workload := range plan.Catalog.Workloads {
		if _, ok := selected[gate.GateID(workload.ID)]; ok {
			catalog.Workloads = append(catalog.Workloads, workload)
		}
	}
	return catalog
}

func remoteExecutionWorkloads(plan gate.WorkloadExecutionPlan) []gate.Workload {
	return remoteExecutionCatalog(plan).Workloads
}

// remoteFreshWorkloadExecutions 汇总全部新分片结果；单个分片报告失败时保留其他已严格解码的 workload，并拒绝重复执行。
func remoteFreshWorkloadExecutions(workloads []gate.Workload, results []ShardResult) (map[string]gate.PlanGateExecution, error) {
	catalog := make(map[string]gate.Workload, len(workloads))
	for _, workload := range workloads {
		catalog[workload.ID] = workload
	}
	executions := make(map[string]gate.PlanGateExecution)
	var workerExecutionErr error
	invalid := make(map[string]struct{})
	for _, result := range results {
		projected, resultWorkerErr, err := projectRemoteFreshWorkloadResult(catalog, result)
		if err != nil {
			workerExecutionErr = errors.Join(workerExecutionErr, fmt.Errorf("remote shard %q workload projection: %w", result.ShardIdentity, err))
			continue
		}
		workerExecutionErr = errors.Join(workerExecutionErr, resultWorkerErr)
		for _, item := range projected {
			if _, rejected := invalid[item.workloadID]; rejected {
				continue
			}
			if _, duplicate := executions[item.workloadID]; duplicate {
				delete(executions, item.workloadID)
				invalid[item.workloadID] = struct{}{}
				workerExecutionErr = errors.Join(workerExecutionErr, fmt.Errorf("remote workload %q was executed more than once", item.workloadID))
				continue
			}
			executions[item.workloadID] = item.execution
		}
	}
	return executions, workerExecutionErr
}

type remoteProjectedWorkloadExecution struct {
	workloadID string
	execution  gate.PlanGateExecution
}

// projectRemoteFreshWorkloadResult 返回已投影 gate、worker 终态错误和阻断性校验错误；终态错误不丢弃合法 gate。
func projectRemoteFreshWorkloadResult(
	catalog map[string]gate.Workload,
	result ShardResult,
) ([]remoteProjectedWorkloadExecution, error, error) {
	if result.ShardIdentity == "" {
		return nil, nil, errors.New("remote workload result shard identity is required")
	}
	skip, err := skipRemoteWorkloadReport(result)
	if err != nil {
		return nil, nil, err
	}
	if skip {
		return nil, nil, nil
	}
	if result.Report.SchemaVersion != gate.ExecutorPlanReportSchemaVersion {
		return nil, nil, errors.New("remote workload result report schema is unsupported")
	}
	if err := result.Report.ExecutionOutcome.Validate(); err != nil {
		return nil, nil, fmt.Errorf("remote workload result execution outcome is invalid: %w", err)
	}
	var workerExecutionErr error
	if result.Report.ExecutionOutcome.Status == gate.WorkerExecutionStatusFailed {
		workerExecutionErr = fmt.Errorf(
			"remote worker execution failed (exit_code=%d reason_code=%s)",
			result.Report.ExecutionOutcome.ExitCode,
			result.Report.ExecutionOutcome.ReasonCode,
		)
	}
	projected := make([]remoteProjectedWorkloadExecution, 0, len(result.Report.Gates))
	for _, execution := range result.Report.Gates {
		workloadID, execution, err := projectRemoteFreshWorkloadExecution(catalog, result.ShardIdentity, execution)
		if err != nil {
			return nil, nil, err
		}
		projected = append(projected, remoteProjectedWorkloadExecution{workloadID: workloadID, execution: execution})
	}
	return projected, workerExecutionErr, nil
}

// skipRemoteWorkloadReport 仅跳过未创建或失败分片的零值报告，成功分片缺报立即阻断。
func skipRemoteWorkloadReport(result ShardResult) (bool, error) {
	if result.Report.SchemaVersion != 0 {
		return false, nil
	}
	if result.ContainerGroup == "" || result.ContainerStatus != "Succeeded" {
		return true, nil
	}
	return false, fmt.Errorf("remote workload result report is missing for shard %s", result.ShardIdentity)
}

// projectRemoteFreshWorkloadExecution 绑定分片身份并校验目录成员及当前结构化 profile。
func projectRemoteFreshWorkloadExecution(
	catalog map[string]gate.Workload,
	shardIdentity string,
	execution gate.PlanGateExecution,
) (string, gate.PlanGateExecution, error) {
	if execution.ShardIdentity != "" && execution.ShardIdentity != shardIdentity {
		return "", gate.PlanGateExecution{}, fmt.Errorf("remote workload %q reported shard identity %q, want %q", execution.GateID, execution.ShardIdentity, shardIdentity)
	}
	execution.ShardIdentity = shardIdentity
	workloadID := string(execution.GateID)
	_, ok := catalog[workloadID]
	if !ok {
		return "", gate.PlanGateExecution{}, fmt.Errorf("remote workload result %q is outside the current catalog", workloadID)
	}
	if err := validateRemoteWorkloadExecutionProfile(workloadID, execution.ExecutionProfile); err != nil {
		return "", gate.PlanGateExecution{}, err
	}
	return workloadID, execution, nil
}

// collectFreshRemoteWorkloadExecutions 验证当前运行的全部 workload 均有新执行结果。
func collectFreshRemoteWorkloadExecutions(
	workloads []gate.Workload,
	fresh map[string]gate.PlanGateExecution,
) (map[string]gate.PlanGateExecution, error) {
	observed := make(map[string]gate.PlanGateExecution, len(workloads))
	for _, workload := range workloads {
		execution, ok := fresh[workload.ID]
		if !ok || execution.GateID != gate.GateID(workload.ID) {
			return nil, fmt.Errorf("remote workload %q has no matching result", workload.ID)
		}
		if err := validateRemoteWorkloadExecutionProfile(workload.ID, execution.ExecutionProfile); err != nil {
			return nil, err
		}
		observed[workload.ID] = execution
	}
	if len(observed) != len(fresh) {
		return nil, fmt.Errorf("remote workload results contain entries outside the current catalog")
	}
	return observed, nil
}

// validateRemoteWorkloadExecutionProfile binds fresh report evidence to the
// same canonical workload producer used for execution and PASS identity.
func validateRemoteWorkloadExecutionProfile(workloadID string, profile gate.ExecutionProfile) error {
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("remote workload %q execution profile: %w", workloadID, err)
	}
	expected, err := gate.WorkloadExecutionGoFlags(workloadID)
	if err != nil {
		return fmt.Errorf("remote workload %q expected GoFlags: %w", workloadID, err)
	}
	if profile.GoFlags != expected {
		return fmt.Errorf("remote workload %q execution profile GoFlags %q does not match expected %q", workloadID, profile.GoFlags, expected)
	}
	return nil
}
