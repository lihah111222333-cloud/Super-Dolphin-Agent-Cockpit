package remoteci

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCoordinatorBatchesPollingCloudCalls(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	input.MaxShards = 5
	plannedSet := mustBuildRemoteExecutionShardSet(t, input)
	if len(plannedSet.Shards) <= 1 {
		t.Fatalf("planned shards=%d, want a batch", len(plannedSet.Shards))
	}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, runtime)
	if _, err := coordinator.Run(context.Background(), input); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.describes) != 1 || len(runtime.describes[0]) != len(plannedSet.Shards) {
		t.Fatalf("DescribeContainerGroups calls = %#v, want one complete batch", runtime.describes)
	}
}

func TestCoordinatorPollingHonorsCloudBatchLimit(t *testing.T) {
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, runtime)
	pending := make([]pendingRemoteShard, remoteContainerGroupBatchLimit+1)
	results := make([]ShardResult, len(pending))
	for index := range pending {
		pending[index] = pendingRemoteShard{index: index, groupID: fmt.Sprintf("eci-%d", index)}
	}
	groups, err := coordinator.observePendingShardStatuses(context.Background(), pending, results)
	if err != nil {
		t.Fatalf("observePendingShardStatuses() error = %v", err)
	}
	if len(groups) != len(pending) {
		t.Fatalf("observed groups=%d, want=%d", len(groups), len(pending))
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.describes) != 2 ||
		len(runtime.describes[0]) != remoteContainerGroupBatchLimit ||
		len(runtime.describes[1]) != 1 {
		t.Fatalf("DescribeContainerGroups batches = %#v", runtime.describes)
	}
}

func TestDecodeReportLogRejectsRecordsBeyondShardBudgetDuringScan(t *testing.T) {
	expected := []gate.GateID{gate.GateIDWhitespaceCheck}
	limit, err := gate.PlanExecutionReportRecordLimit(len(expected))
	if err != nil {
		t.Fatal(err)
	}
	line := gate.ExecutorPlanReportChunkPrefix + "over-budget\n"
	_, err = decodeReportLog(strings.Repeat(line, limit+1), expected)
	if err == nil || !strings.Contains(err.Error(), "exceeds shard record budget") {
		t.Fatalf("decodeReportLog() error = %v, want shard record budget rejection", err)
	}
}
