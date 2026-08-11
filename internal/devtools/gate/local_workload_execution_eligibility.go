package gate

import "fmt"

// LocalWorkloadExecutionEligibility 是 gate-owned 本地 workload 执行能力的只读投影。
//
// WorkloadID 保留调用方提交的 canonical 或 expanded identity，CanonicalID 是
// executorProgramForWorkload 解析出的 canonical owner。Reason 对不适用结果给出
// 稳定、可审计的原因；调用方不得把缺省 bool 当作本地能力。
type LocalWorkloadExecutionEligibility struct {
	WorkloadID  GateID
	CanonicalID GateID
	Strategy    ExecutorStrategy
	Eligible    bool
	Reason      string
}

// EvaluateLocalWorkloadExecutionEligibility 根据 gate 的真实 executor mapping
// 和唯一 local capability owner 判断 workload 是否可以安全地在本地主机执行。
//
// 该函数只读取 immutable mapping/validation，不探测主机、不执行 workload、不接触
// 云端或任何 ledger。未知、格式错误或 mapping 无法解析的 workload 直接返回 error；
// 已解析但当前本地 runner 不支持的 strategy 返回 Eligible=false 及明确原因。
func EvaluateLocalWorkloadExecutionEligibility(id GateID) (LocalWorkloadExecutionEligibility, error) {
	canonicalID, program, err := executorProgramForWorkload(id)
	if err != nil {
		return evaluateKnownUnmappedWorkload(id, err)
	}
	result := LocalWorkloadExecutionEligibility{
		WorkloadID:  id,
		CanonicalID: canonicalID,
		Strategy:    program.Strategy,
	}
	if err := validateExecutorProgram(program); err != nil {
		return result, fmt.Errorf("workload %q executor program is invalid: %w", id, err)
	}
	if err := validateLocalExecutorProgramSupport(program); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Eligible = true
	result.Reason = "local executor program is supported"
	return result, nil
}

// evaluateKnownUnmappedWorkload 将已登记但暂时没有 executor mapping 的 workload
// 投影为明确的 local MISS；未登记或无法解析的 workload 仍保持 fail-fast。
func evaluateKnownUnmappedWorkload(id GateID, mappingErr error) (LocalWorkloadExecutionEligibility, error) {
	canonicalID, _, _, _, parseErr := ParseWorkloadID(string(id))
	if parseErr != nil {
		return LocalWorkloadExecutionEligibility{}, mappingErr
	}
	if _, knownErr := RequiredCheckForWorkloadID(string(id)); knownErr == nil {
		return LocalWorkloadExecutionEligibility{
			WorkloadID:  id,
			CanonicalID: canonicalID,
			Reason:      fmt.Sprintf("workload %q has no local executor mapping: %v", id, mappingErr),
		}, nil
	}
	return LocalWorkloadExecutionEligibility{}, mappingErr
}
