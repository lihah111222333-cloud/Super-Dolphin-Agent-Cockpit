package remoteci

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

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
	result.TimingWarnings = append(result.TimingWarnings, warnings...)
	human := make([]string, len(warnings))
	for index, warning := range warnings {
		human[index] = warning.WarningText
	}
	result.OptimizationWarnings = appendUniqueRemoteWarnings(result.OptimizationWarnings, human)
	return result, nil
}
