package remoteci

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteWorkloadWarningSubject 标识 warning builder 使用的 shard/workload 闭合主体。
type remoteWorkloadWarningSubject struct {
	shardIdentity string
	workloadID    gate.GateID
}

// appendRemotePartialWorkloadTargetWarnings 从失败运行仍具备完整 test_body/total
// 边界的同一份 raw observations 生成 workload warning。缺失阶段的 workload
// 被排除在 warning execution 集合之外，保持失败 provisional 的缺失事实而不伪造告警证据。
func appendRemotePartialWorkloadTargetWarnings(result RunResult, observations []gate.TimingObservation) (RunResult, error) {
	executions := remoteMeasuredWorkloadWarningExecutions(remoteTimingWorkloadExecutions(result), observations)
	if len(executions) == 0 {
		return result, nil
	}
	filtered := remoteWorkloadWarningObservations(executions, observations)
	warnings, err := gate.BuildRemoteCIWorkloadTimingWarnings(
		result.JobID, result.AgentTokenDigest, result.AcceptedGeneration,
		executions, filtered,
	)
	if err != nil {
		return result, err
	}
	result.TimingWarnings = appendRemoteCITimingWarnings(result.TimingWarnings, warnings)
	human := make([]string, len(warnings))
	for index, warning := range warnings {
		human[index] = warning.WarningText
	}
	result.OptimizationWarnings = appendUniqueRemoteWarnings(result.OptimizationWarnings, human)
	return result, nil
}

// remoteMeasuredWorkloadWarningExecutions 只选择同时存在 measured test_body 与 total
// 的 execution；该集合与失败 provisional 校验使用的闭合规则保持一致。
func remoteMeasuredWorkloadWarningExecutions(executions []gate.PlanGateExecution, observations []gate.TimingObservation) []gate.PlanGateExecution {
	type phases struct{ body, total bool }
	measured := make(map[remoteWorkloadWarningSubject]phases)
	for _, observation := range observations {
		if observation.Scope != cicontract.TimingScopeWorkload || observation.Measurement != cicontract.ObservationMeasured {
			continue
		}
		key := remoteWorkloadWarningSubject{shardIdentity: observation.ShardIdentity, workloadID: observation.WorkloadID}
		phase := measured[key]
		switch observation.Phase {
		case cicontract.TimingTestBody:
			phase.body = true
		case cicontract.TimingTotal:
			phase.total = true
		}
		measured[key] = phase
	}
	result := make([]gate.PlanGateExecution, 0, len(executions))
	for _, execution := range executions {
		phase := measured[remoteWorkloadWarningSubject{shardIdentity: execution.ShardIdentity, workloadID: execution.GateID}]
		if phase.body && phase.total {
			result = append(result, execution)
		}
	}
	return result
}

// remoteWorkloadWarningObservations 保留 warning builder 需要的同一执行身份的
// measured body/total observations，避免 partial 运行中其它缺失 workload 成为孤儿观测。
func remoteWorkloadWarningObservations(executions []gate.PlanGateExecution, observations []gate.TimingObservation) []gate.TimingObservation {
	allowed := make(map[remoteWorkloadWarningSubject]struct{}, len(executions))
	for _, execution := range executions {
		allowed[remoteWorkloadWarningSubject{shardIdentity: execution.ShardIdentity, workloadID: execution.GateID}] = struct{}{}
	}
	filtered := make([]gate.TimingObservation, 0, len(executions)*2)
	for _, observation := range observations {
		if observation.Scope != cicontract.TimingScopeWorkload || observation.Measurement != cicontract.ObservationMeasured {
			continue
		}
		if observation.Phase != cicontract.TimingTestBody && observation.Phase != cicontract.TimingTotal {
			continue
		}
		if _, ok := allowed[remoteWorkloadWarningSubject{shardIdentity: observation.ShardIdentity, workloadID: observation.WorkloadID}]; ok {
			filtered = append(filtered, observation)
		}
	}
	return filtered
}

// appendRemoteCITimingWarnings 按 warning 文本合并结构化告警，避免同一 raw 事实重复投影。
func appendRemoteCITimingWarnings(existing, additions []gate.RemoteCITimingWarning) []gate.RemoteCITimingWarning {
	for _, addition := range additions {
		duplicate := false
		for _, current := range existing {
			if current.WarningText == addition.WarningText {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, addition)
		}
	}
	return existing
}

// appendRemoteWorkloadTargetWarnings 从即将写入同一 run 的 raw workload
// timing observations 构造结构化完成后告警；不读取人类文本或 duration sample。
func appendRemoteWorkloadTargetWarnings(result RunResult) (RunResult, error) {
	observations, err := remoteTimingObservations(result)
	if err != nil {
		return result, err
	}
	warnings, err := gate.BuildRemoteCIWorkloadTimingWarnings(
		result.JobID, result.AgentTokenDigest, result.AcceptedGeneration,
		result.FreshWorkloadExecutions, observations,
	)
	if err != nil {
		return result, err
	}
	result.TimingWarnings = appendRemoteCITimingWarnings(result.TimingWarnings, warnings)
	human := make([]string, len(warnings))
	for index, warning := range warnings {
		human[index] = warning.WarningText
	}
	result.OptimizationWarnings = appendUniqueRemoteWarnings(result.OptimizationWarnings, human)
	return result, nil
}
