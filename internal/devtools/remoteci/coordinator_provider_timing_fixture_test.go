package remoteci

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"

// fillSyntheticWorkerFinishTime 为通用 fake runtime 补齐真实 ECI 终态必有的 worker provider 时间。
func fillSyntheticWorkerFinishTime(group eci.ContainerGroup, status string) eci.ContainerGroup {
	terminalAt := group.SucceededTime
	if status != "Succeeded" {
		terminalAt = group.FailedTime
	}
	for index := range group.Containers {
		if group.Containers[index].Name != "worker" {
			continue
		}
		if group.Containers[index].CurrentState.FinishTime.IsZero() {
			group.Containers[index].CurrentState.FinishTime = terminalAt
		}
		return group
	}
	group.Containers = append(group.Containers, eci.ContainerStatus{
		Name: "worker", CurrentState: eci.ContainerState{FinishTime: terminalAt},
	})
	return group
}
