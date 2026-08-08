package remoteci

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
)

func TestBindObservedECIShardTimingUsesProviderLifecycleTimes(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)
	materializerStartedAt := createdAt.Add(3 * time.Second)
	groupTerminalAt := materializerStartedAt.Add(17 * time.Second)
	workerFinishedAt := groupTerminalAt.Add(267*time.Millisecond + 999*time.Nanosecond)
	wantWorkerFinishedAt := workerFinishedAt.Truncate(time.Millisecond)
	result := ShardResult{}
	if err := bindObservedECIShardTiming(&result, eci.ContainerGroup{
		Status: "Succeeded", CreationTime: createdAt, SucceededTime: groupTerminalAt,
		InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: materializerStartedAt}}},
		Containers:     []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{FinishTime: workerFinishedAt}}},
	}); err != nil {
		t.Fatal(err)
	}
	if !result.ECIWaitStartedAt.Equal(createdAt) || !result.ECIWaitCompletedAt.Equal(materializerStartedAt) || !result.ECITerminalAt.Equal(wantWorkerFinishedAt) {
		t.Fatalf("provider timing binding = %#v", result)
	}
}

func TestBindObservedECIShardTimingFailsFastOnMissingProviderFields(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)
	for name, group := range map[string]eci.ContainerGroup{
		"creation":     {Status: "Succeeded", SucceededTime: createdAt.Add(time.Second), InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: createdAt}}}, Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{FinishTime: createdAt.Add(time.Second)}}}},
		"materializer": {Status: "Succeeded", CreationTime: createdAt, SucceededTime: createdAt.Add(time.Second), Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{FinishTime: createdAt.Add(time.Second)}}}},
		"terminal":     {Status: "Succeeded", CreationTime: createdAt, InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: createdAt.Add(time.Millisecond)}}}, Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{FinishTime: createdAt.Add(time.Second)}}}},
		"worker":       {Status: "Succeeded", CreationTime: createdAt, SucceededTime: createdAt.Add(time.Second), InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: createdAt.Add(time.Millisecond)}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := bindObservedECIShardTiming(&ShardResult{}, group); err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("bindObservedECIShardTiming() error = %v, want missing provider field", err)
			}
		})
	}
}

func TestBindObservedECIShardTimingUsesFailedProviderTerminalTime(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 3, 0, 0, 0, time.UTC)
	materializerStartedAt := createdAt.Add(time.Second)
	workerFinishedAt := materializerStartedAt.Add(time.Second)

	for name, testCase := range map[string]struct {
		failedAt time.Time
		wantErr  bool
	}{
		"provider failed time":         {failedAt: materializerStartedAt.Add(2 * time.Second)},
		"missing provider failed time": {wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			result := ShardResult{}
			group := eci.ContainerGroup{
				Status: "Failed", CreationTime: createdAt, FailedTime: testCase.failedAt,
				InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: materializerStartedAt}}},
				Containers:     []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{FinishTime: workerFinishedAt}}},
			}
			err := bindObservedECIShardTiming(&result, group)
			if testCase.wantErr {
				if err == nil || !strings.Contains(err.Error(), "missing terminal time") {
					t.Fatalf("bindObservedECIShardTiming() error = %v, want missing FailedTime", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("bindObservedECIShardTiming() error = %v", err)
			}
			if !result.ECITerminalAt.Equal(testCase.failedAt) {
				t.Fatalf("ECITerminalAt = %s, want later provider FailedTime %s", result.ECITerminalAt, testCase.failedAt)
			}
		})
	}
}
