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
	terminalAt := materializerStartedAt.Add(17 * time.Second)
	result := ShardResult{}
	if err := bindObservedECIShardTiming(&result, eci.ContainerGroup{
		Status: "Succeeded", CreationTime: createdAt, SucceededTime: terminalAt,
		InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: materializerStartedAt}}},
	}); err != nil {
		t.Fatal(err)
	}
	if !result.ECIWaitStartedAt.Equal(createdAt) || !result.ECIWaitCompletedAt.Equal(materializerStartedAt) || !result.ECITerminalAt.Equal(terminalAt) {
		t.Fatalf("provider timing binding = %#v", result)
	}
}

func TestBindObservedECIShardTimingFailsFastOnMissingProviderFields(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)
	for name, group := range map[string]eci.ContainerGroup{
		"creation":     {Status: "Succeeded", SucceededTime: createdAt.Add(time.Second), InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: createdAt}}}},
		"materializer": {Status: "Succeeded", CreationTime: createdAt, SucceededTime: createdAt.Add(time.Second)},
		"terminal":     {Status: "Succeeded", CreationTime: createdAt, InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: createdAt.Add(time.Millisecond)}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := bindObservedECIShardTiming(&ShardResult{}, group); err == nil || !strings.Contains(err.Error(), "missing") {
				t.Fatalf("bindObservedECIShardTiming() error = %v, want missing provider field", err)
			}
		})
	}
}
