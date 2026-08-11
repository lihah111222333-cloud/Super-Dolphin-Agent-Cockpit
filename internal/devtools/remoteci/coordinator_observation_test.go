package remoteci

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type observationRuntime struct {
	status      string
	blockStatus bool
	blockLog    bool
}

func (runtime observationRuntime) CreateContainerGroup(context.Context, eci.CreateRequest) (eci.ContainerGroup, error) {
	return eci.ContainerGroup{}, errors.New("unexpected create")
}

func (runtime observationRuntime) DescribeContainerGroups(ctx context.Context, ids ...string) ([]eci.ContainerGroup, error) {
	if runtime.blockStatus {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	createdAt := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	groups := make([]eci.ContainerGroup, len(ids))
	for index, id := range ids {
		groups[index] = eci.ContainerGroup{
			ID: id, Status: runtime.status, CreationTime: createdAt, SucceededTime: createdAt.Add(time.Second),
			InitContainers: []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: createdAt.Add(time.Millisecond)}}},
			Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{
				StartTime: createdAt.Add(2 * time.Millisecond), FinishTime: createdAt.Add(time.Second),
			}}},
		}
	}
	return groups, nil
}

func (runtime observationRuntime) DescribeContainerLog(ctx context.Context, _ string, _ string) (string, error) {
	if runtime.blockLog {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "", errors.New("unexpected log")
}

func (observationRuntime) DeleteContainerGroup(context.Context, string) error {
	return errors.New("unexpected delete")
}

// ConfirmContainerGroupAbsent simulates an ECI absence proof.
func (observationRuntime) ConfirmContainerGroupAbsent(context.Context, string) (bool, error) {
	return true, nil
}

func TestWaitShardsClassifiesCoordinatorObservationStall(t *testing.T) {
	coordinator := observationTestCoordinator(observationRuntime{blockStatus: true}, 20*time.Millisecond)
	_, _, err := coordinator.waitShards(context.Background(), []gate.ContainerShard{observationTestShard()}, []string{"eci-stalled"}, remoteTimingWarningRun{})
	if err == nil || !strings.Contains(err.Error(), "coordinator observation stalled") ||
		!strings.Contains(err.Error(), "phase=status observation") {
		t.Fatalf("waitShards() error = %v", err)
	}
}

func TestWaitShardsClassifiesCloudShardStillRunning(t *testing.T) {
	coordinator := observationTestCoordinator(observationRuntime{status: "Running"}, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err := coordinator.waitShards(ctx, []gate.ContainerShard{observationTestShard()}, []string{"eci-running"}, remoteTimingWarningRun{})
	if err == nil || !strings.Contains(err.Error(), "cloud shard has not reached terminal state") ||
		strings.Contains(err.Error(), "coordinator observation stalled") {
		t.Fatalf("waitShards() error = %v", err)
	}
}

func TestWaitShardsClassifiesTerminalReportAggregationStall(t *testing.T) {
	coordinator := observationTestCoordinator(
		observationRuntime{status: "Succeeded", blockLog: true},
		20*time.Millisecond,
	)
	_, _, err := coordinator.waitShards(context.Background(), []gate.ContainerShard{observationTestShard()}, []string{"eci-terminal"}, remoteTimingWarningRun{})
	if err == nil || !strings.Contains(err.Error(), "coordinator observation stalled") ||
		!strings.Contains(err.Error(), "phase=terminal report aggregation") ||
		!strings.Contains(err.Error(), `last_status="Succeeded"`) {
		t.Fatalf("waitShards() error = %v", err)
	}
}

func TestTerminalECIStatusIncludesSchedulingAndExpiryFailures(t *testing.T) {
	for _, status := range []string{"Succeeded", "Failed", "ScheduleFailed", "Expired"} {
		if !terminalECIStatus(status) {
			t.Errorf("terminalECIStatus(%q) = false", status)
		}
	}
	for _, status := range []string{"", "Pending", "Scheduling", "Running", "Restarting", "Updating", "Terminating"} {
		if terminalECIStatus(status) {
			t.Errorf("terminalECIStatus(%q) = true", status)
		}
	}
}

func observationTestCoordinator(runtime Runtime, timeout time.Duration) *Coordinator {
	return &Coordinator{
		config:  CoordinatorConfig{PollInterval: time.Millisecond},
		runtime: runtime, observationTimeout: timeout,
		now: func() time.Time { return time.Date(2026, 8, 3, 0, 0, 2, 0, time.UTC) },
	}
}

func observationTestShard() gate.ContainerShard {
	return gate.ContainerShard{
		Index: 1, IdentityDigest: "sha256:" + strings.Repeat("a", 64),
		EstimatedDurationMS: 1, GateIDs: []gate.GateID{gate.GateIDFrontendTest},
	}
}
