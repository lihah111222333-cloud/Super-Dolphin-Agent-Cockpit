package remoteci

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCoordinatorBatchesPollingCloudCalls(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
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

func TestShardTargetWarningIsNonTerminatingAndEmittedOnce(t *testing.T) {
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	now := time.Date(2026, time.August, 3, 1, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { return now }
	pending := []pendingRemoteShard{{index: 0, groupID: "eci-slow"}}
	groups := map[string]eci.ContainerGroup{"eci-slow": {ID: "eci-slow", Status: "Running"}}
	executingSince := make(map[int]time.Time)
	warned := make(map[int]struct{})

	if warnings := coordinator.observeShardTargetWarnings(pending, groups, executingSince, warned); len(warnings) != 0 {
		t.Fatalf("initial Running observation warnings = %#v, want none", warnings)
	}
	now = now.Add(cicontract.ShardTargetDuration)
	warnings := coordinator.observeShardTargetWarnings(pending, groups, executingSince, warned)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "execution continues without cancellation") {
		t.Fatalf("target warnings = %#v, want one non-terminating warning", warnings)
	}
	// The helper only observes status and never owns a context, cancellation, or cleanup path.
	if groups["eci-slow"].Status != "Running" {
		t.Fatalf("shard status = %q, want Running after target warning", groups["eci-slow"].Status)
	}
	now = now.Add(time.Second)
	if warnings := coordinator.observeShardTargetWarnings(pending, groups, executingSince, warned); len(warnings) != 0 {
		t.Fatalf("repeated target warnings = %#v, want exactly one warning", warnings)
	}
}

func TestCoordinatorPollingObservesAllPendingShardsWithoutRepositoryBatchCap(t *testing.T) {
	if cicontract.ShardConcurrencyPolicy != "unbounded_by_repository" {
		t.Fatalf("ShardConcurrencyPolicy = %q, want unbounded_by_repository", cicontract.ShardConcurrencyPolicy)
	}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, runtime)
	pending := make([]pendingRemoteShard, 21)
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
	if len(runtime.describes) != 1 || len(runtime.describes[0]) != len(pending) {
		t.Fatalf("DescribeContainerGroups requests = %#v, want one request for all %d pending shards", runtime.describes, len(pending))
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
