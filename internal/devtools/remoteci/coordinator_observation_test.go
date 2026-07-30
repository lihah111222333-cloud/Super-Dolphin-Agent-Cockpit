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
	return []eci.ContainerGroup{{ID: ids[0], Status: runtime.status}}, nil
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

func TestWaitShardClassifiesCoordinatorObservationStall(t *testing.T) {
	coordinator := observationTestCoordinator(observationRuntime{blockStatus: true}, 20*time.Millisecond)
	_, err := coordinator.waitShard(context.Background(), observationTestShard(), "eci-stalled")
	if err == nil || !strings.Contains(err.Error(), "coordinator observation stalled") ||
		!strings.Contains(err.Error(), "phase=status observation") {
		t.Fatalf("waitShard() error = %v", err)
	}
}

func TestWaitShardClassifiesCloudShardStillRunning(t *testing.T) {
	coordinator := observationTestCoordinator(observationRuntime{status: "Running"}, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := coordinator.waitShard(ctx, observationTestShard(), "eci-running")
	if err == nil || !strings.Contains(err.Error(), "cloud shard has not reached terminal state") ||
		strings.Contains(err.Error(), "coordinator observation stalled") {
		t.Fatalf("waitShard() error = %v", err)
	}
}

func TestWaitShardClassifiesTerminalReportAggregationStall(t *testing.T) {
	coordinator := observationTestCoordinator(
		observationRuntime{status: "Succeeded", blockLog: true},
		20*time.Millisecond,
	)
	_, err := coordinator.waitShard(context.Background(), observationTestShard(), "eci-terminal")
	if err == nil || !strings.Contains(err.Error(), "coordinator observation stalled") ||
		!strings.Contains(err.Error(), "phase=terminal report aggregation") ||
		!strings.Contains(err.Error(), `last_status="Succeeded"`) {
		t.Fatalf("waitShard() error = %v", err)
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
	}
}

func observationTestShard() gate.ContainerShard {
	return gate.ContainerShard{
		Index: 1, IdentityDigest: "sha256:" + strings.Repeat("a", 64),
		EstimatedDurationMS: 1, GateIDs: []gate.GateID{gate.GateIDFrontendTest},
	}
}
