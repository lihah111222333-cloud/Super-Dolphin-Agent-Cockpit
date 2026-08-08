package remoteci

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type flakyTimingWarningRecorder struct {
	calls int
}

func (recorder *flakyTimingWarningRecorder) RecordLiveRemoteCITimingWarning(
	warning gate.RemoteCITimingWarning,
) (gate.RemoteCITimingWarning, bool, error) {
	recorder.calls++
	if recorder.calls == 1 {
		return gate.RemoteCITimingWarning{}, false, fmt.Errorf("injected SQLite contention: %w", gate.ErrDurationLedgerBusy)
	}
	return warning, true, nil
}

func TestCoordinatorBatchesPollingCloudCalls(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	plannedSet := mustBuildAllMissRemoteExecutionShardSet(t, input)
	if len(plannedSet.Shards) <= 1 {
		t.Fatalf("planned shards=%d, want a batch", len(plannedSet.Shards))
	}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, runtime)
	if _, err := runCoordinatorTest(t, coordinator, context.Background(), input); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.describes) != 1 || len(runtime.describes[0]) != len(plannedSet.Shards) {
		t.Fatalf("DescribeContainerGroups calls = %#v, want one complete batch", runtime.describes)
	}
}

func TestInitializeRemoteShardResultsDoesNotMarkUncreatedShardWorkloadsExecuted(t *testing.T) {
	shards := []gate.ContainerShard{
		{IdentityDigest: "shard-uncreated", GateIDs: []gate.GateID{"guard:cache-miss"}},
		{IdentityDigest: "shard-created", GateIDs: []gate.GateID{"guard:cache-hit"}},
	}
	results, pending := initializeRemoteShardResults(shards, []string{"", "eci-created"})

	if len(pending) != 1 || pending[0] != (pendingRemoteShard{index: 1, groupID: "eci-created"}) {
		t.Fatalf("pending shards = %#v, want only the created shard", pending)
	}
	if len(results[0].ExecutedWorkloads) != 0 {
		t.Fatalf("uncreated placeholder executed workloads = %#v, want none", results[0].ExecutedWorkloads)
	}
	if got, want := results[1].ExecutedWorkloads, shards[1].GateIDs; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("created shard executed workloads = %#v, want %#v", got, want)
	}
	if len(shards[0].GateIDs) != 1 || shards[0].GateIDs[0] != "guard:cache-miss" {
		t.Fatalf("planned cache-miss workload was mutated: %#v", shards[0].GateIDs)
	}
}

func TestShardTargetWarningIsNonTerminatingAndEmittedOnce(t *testing.T) {
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	now := time.Date(2026, time.August, 3, 1, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { return now }
	ledgerStore, _ := newRemoteRunLedgerAuthority(t, gate.NewDurationLedger())
	shards := []gate.ContainerShard{{IdentityDigest: "shard-slow"}}
	pending := []pendingRemoteShard{{index: 0, groupID: "eci-slow"}}
	groups := map[string]eci.ContainerGroup{"eci-slow": {
		ID: "eci-slow", Status: "Running",
		Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{
			StartTime: now.Add(-cicontract.ShardTargetDuration),
		}}},
	}}
	warningRun := remoteTimingWarningRun{
		jobID: "job-slow", agentTokenDigest: testRemoteAgentTokenDigest,
		acceptedGeneration: 1, store: ledgerStore,
	}
	warned := make(map[int]struct{})
	failures := make([]error, 1)

	warnings := coordinator.observeShardTargetWarnings(shards, pending, groups, warningRun, warned, failures)
	if len(warnings) != 1 || warnings[0].Action != cicontract.TimingWarningWarnAndContinue ||
		warnings[0].EvidenceKind != cicontract.TimingWarningEvidenceRunning ||
		warnings[0].EvidenceStartedAt != now.Add(-cicontract.ShardTargetDuration) ||
		warnings[0].EvidenceDurationMS != cicontract.ShardTargetDuration.Milliseconds() || failures[0] != nil {
		t.Fatalf("target warnings = %#v, want one non-terminating warning", warnings)
	}
	if groups["eci-slow"].Status != "Running" {
		t.Fatalf("shard status = %q, want Running after target warning", groups["eci-slow"].Status)
	}
	now = now.Add(time.Second)
	if warnings := coordinator.observeShardTargetWarnings(shards, pending, groups, warningRun, warned, failures); len(warnings) != 0 {
		t.Fatalf("repeated target warnings = %#v, want exactly one warning", warnings)
	}
}

func TestShardTargetWarningUsesProviderTerminalTimeBetweenPolls(t *testing.T) {
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	localNow := time.Date(2026, time.August, 3, 4, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { return localNow }
	ledgerStore, _ := newRemoteRunLedgerAuthority(t, gate.NewDurationLedger())
	startedAt := localNow.Add(-time.Hour)
	shards := []gate.ContainerShard{{IdentityDigest: "shard-terminal"}}
	pending := []pendingRemoteShard{{index: 0, groupID: "eci-terminal"}}

	for _, testCase := range []struct {
		name     string
		duration time.Duration
		want     int
	}{
		{name: "99.9 seconds", duration: 99*time.Second + 900*time.Millisecond},
		{name: "100.1 seconds", duration: 100*time.Second + 100*time.Millisecond, want: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			groups := map[string]eci.ContainerGroup{"eci-terminal": {
				ID: "eci-terminal", Status: "Succeeded", SucceededTime: startedAt.Add(testCase.duration),
				Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{StartTime: startedAt}}},
			}}
			failures := make([]error, 1)
			warnings := coordinator.observeShardTargetWarnings(
				shards, pending, groups,
				remoteTimingWarningRun{jobID: "job-terminal-" + testCase.name, agentTokenDigest: testRemoteAgentTokenDigest, acceptedGeneration: 1, store: ledgerStore},
				make(map[int]struct{}), failures,
			)
			if len(warnings) != testCase.want || failures[0] != nil {
				t.Fatalf("terminal warning count=%d failure=%v, want %d", len(warnings), failures[0], testCase.want)
			}
			if len(warnings) == 1 && !warnings[0].ObservedAt.Equal(startedAt.Add(testCase.duration)) {
				t.Fatalf("ObservedAt=%s, want provider terminal %s", warnings[0].ObservedAt, startedAt.Add(testCase.duration))
			}
		})
	}
}

func TestObservedECIWorkerStartTimeRejectsMissingAndDuplicateEvidence(t *testing.T) {
	for name, group := range map[string]eci.ContainerGroup{
		"missing":   {},
		"zero":      {Containers: []eci.ContainerStatus{{Name: "worker"}}},
		"duplicate": {Containers: []eci.ContainerStatus{{Name: "worker"}, {Name: "worker"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := observedECIWorkerStartTime(group); err == nil {
				t.Fatal("expected invalid provider worker StartTime evidence to be rejected")
			}
		})
	}
}

func TestObservedECIWorkerStartTimeRejectsSQLiteTimestampRange(t *testing.T) {
	maxEvidenceStartedAtMS := int64(math.MaxInt64) - cicontract.ShardTargetDuration.Milliseconds()
	for name, testCase := range map[string]struct {
		startedAtMS int64
		wantError   string
	}{
		"epoch": {
			startedAtMS: 0,
			wantError:   "evidence_started_at Unix milliseconds must be > 0",
		},
		"pre-epoch": {
			startedAtMS: -1,
			wantError:   "evidence_started_at Unix milliseconds must be > 0",
		},
		"sqlite addition overflow": {
			startedAtMS: maxEvidenceStartedAtMS + 1,
			wantError:   fmt.Sprintf("evidence_started_at Unix milliseconds must be <= %d", maxEvidenceStartedAtMS),
		},
	} {
		t.Run(name, func(t *testing.T) {
			group := eci.ContainerGroup{
				Containers: []eci.ContainerStatus{{
					Name:         "worker",
					CurrentState: eci.ContainerState{StartTime: time.UnixMilli(testCase.startedAtMS).UTC()},
				}},
			}
			if _, err := observedECIWorkerStartTime(group); err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("observedECIWorkerStartTime() error = %v, want %q", err, testCase.wantError)
			}
		})
	}
}

func TestTerminalTimingWarningSQLiteBusyIsRetriedBeforeConvergence(t *testing.T) {
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	startedAt := time.Date(2026, time.August, 3, 5, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { return startedAt.Add(time.Hour) }
	shards := []gate.ContainerShard{{IdentityDigest: "shard-retry"}}
	pending := []pendingRemoteShard{{index: 0, groupID: "eci-retry"}}
	groups := map[string]eci.ContainerGroup{"eci-retry": {
		ID: "eci-retry", Status: "Succeeded", SucceededTime: startedAt.Add(100*time.Second + time.Millisecond),
		Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{StartTime: startedAt}}},
	}}
	recorder := &flakyTimingWarningRecorder{}
	run := remoteTimingWarningRun{
		jobID: "job-retry", agentTokenDigest: testRemoteAgentTokenDigest,
		acceptedGeneration: 1, store: recorder,
	}
	warned := make(map[int]struct{})
	failures := make([]error, 1)
	retries := make(map[int]pendingRemoteShard)

	if warnings := coordinator.observeShardTargetWarnings(shards, pending, groups, run, warned, failures); len(warnings) != 0 || !errors.Is(failures[0], gate.ErrDurationLedgerBusy) {
		t.Fatalf("first terminal persistence warnings=%#v failure=%v", warnings, failures[0])
	}
	updateTerminalTimingWarningRetries(pending, groups, failures, retries)
	if len(retries) != 1 {
		t.Fatalf("terminal retry queue=%#v, want one busy warning", retries)
	}
	warnings := coordinator.observeShardTargetWarnings(shards, mergePendingRemoteShards(nil, retries), groups, run, warned, failures)
	updateTerminalTimingWarningRetries(pending, groups, failures, retries)
	if len(warnings) != 1 || failures[0] != nil || len(retries) != 0 || recorder.calls != 2 {
		t.Fatalf("retried terminal warnings=%#v failure=%v retries=%#v calls=%d", warnings, failures[0], retries, recorder.calls)
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
