package gate

import "fmt"

// replaceBatchedGateResults 以 request 的 canonical 顺序填充 runner 未启动的 pending 结果。
// 已观察到的失败或成功结果属于更高优先级执行证据，批处理快照只能填充 pending。
func replaceBatchedGateResults(ids []GateID, report []PlanGateExecution, batched map[GateID]PlanGateExecution) ([]PlanGateExecution, error) {
	if len(report) != len(ids) {
		return report, fmt.Errorf("batched gate result coverage mismatch: report=%d request=%d", len(report), len(ids))
	}
	for index, id := range ids {
		current := report[index]
		if current.GateID != id {
			return report, fmt.Errorf("batched gate result identity mismatch: report[%d]=%q request=%q", index, current.GateID, id)
		}
		result, ok := batched[id]
		if !ok {
			continue
		}
		if result.GateID != id {
			return report, fmt.Errorf("batched gate result identity mismatch: batched[%q]=%q", id, result.GateID)
		}
		if (current.Status == ResultStatusCancelled || current.Status == ResultStatusTimeout) &&
			current.ExitCode == -1 && current.StartedAt.Equal(current.CompletedAt) {
			report[index] = result
		}
	}
	return report, nil
}
