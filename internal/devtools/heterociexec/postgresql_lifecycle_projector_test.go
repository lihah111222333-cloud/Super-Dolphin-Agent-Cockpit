package heterociexec

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProjectPostgreSQLLifecycleSingleHostSuccess(t *testing.T) {
	input := projectorSingleHostInput(t)
	projection, err := ProjectPostgreSQLLifecycle(input)
	if err != nil {
		t.Fatalf("ProjectPostgreSQLLifecycle() error = %v", err)
	}
	assertPostgreSQLLifecycleProjection(t, projection, PostgreSQLReceiptStatusSucceeded)
	for index, event := range projection.Events {
		if index < 3 && len(event.Shards) != 0 {
			t.Fatalf("event %d exposes shards before reservation: %#v", index+1, event.Shards)
		}
		if index >= 3 && len(event.Shards) != 1 {
			t.Fatalf("event %d shard count = %d, want 1", index+1, len(event.Shards))
		}
	}
}

func TestProjectPostgreSQLLifecycleSingleHostFailure(t *testing.T) {
	input := projectorSingleHostInput(t)
	input.Status = PostgreSQLReceiptStatusFailed
	input.ExitCode = 17
	input.ErrorCode = "worker_failed"
	input.TerminalShards[0].Status = PostgreSQLReceiptStatusFailed
	input.TerminalShards[0].ExitCode = 17
	input.TerminalShards[0].ErrorCode = input.ErrorCode
	projection, err := ProjectPostgreSQLLifecycle(input)
	if err != nil {
		t.Fatalf("ProjectPostgreSQLLifecycle(failed) error = %v", err)
	}
	assertPostgreSQLLifecycleProjection(t, projection, PostgreSQLReceiptStatusFailed)
	if projection.Events[5].State != PostgreSQLReceiptEventStateFailed || projection.Events[5].Code != input.ErrorCode {
		t.Fatalf("execute failure event = %#v", projection.Events[5])
	}
	if projection.Events[6].State != PostgreSQLReceiptEventStateCompleted || len(projection.Events[6].Shards) != 1 {
		t.Fatalf("cleanup failure event = %#v", projection.Events[6])
	}
	if projection.Events[7].State != PostgreSQLReceiptEventStateFailed || projection.Events[7].Code != input.ErrorCode {
		t.Fatalf("terminal failure event = %#v", projection.Events[7])
	}
}

func TestProjectPostgreSQLLifecycleSingleHostBlocked(t *testing.T) {
	input := projectorSingleHostInput(t)
	input.Status = PostgreSQLReceiptStatusBlocked
	input.ExitCode = -1
	input.ErrorCode = ""
	input.CapacityCode = "capacity_blocked"
	input.TerminalShards = []PostgreSQLTerminalShard{}
	input.NodeSetDigest = mustPostgreSQLTerminalNodeSetDigest(t, input.TerminalShards)
	projection, err := ProjectPostgreSQLLifecycle(input)
	if err != nil {
		t.Fatalf("ProjectPostgreSQLLifecycle(blocked) error = %v", err)
	}
	assertPostgreSQLLifecycleProjection(t, projection, PostgreSQLReceiptStatusBlocked)
	for index, event := range projection.Events {
		if len(event.Shards) != 0 {
			t.Fatalf("blocked event %d exposes shards: %#v", index+1, event.Shards)
		}
	}
	if projection.Events[2].State != PostgreSQLReceiptEventStateBlocked || projection.Events[2].Code != input.CapacityCode {
		t.Fatalf("blocked capacity event = %#v", projection.Events[2])
	}
	for index := 3; index < 7; index++ {
		if projection.Events[index].State != PostgreSQLReceiptEventStateSkipped || projection.Events[index].Code != input.CapacityCode {
			t.Fatalf("blocked skipped event %d = %#v", index+1, projection.Events[index])
		}
	}
}

func TestProjectPostgreSQLLifecycleRejectsSortingAndMissingFacts(t *testing.T) {
	input := projectorSingleHostInput(t)
	// Append a lexicographically earlier synthetic host only as malformed input so the ordering guard remains covered.
	unsorted := input.TerminalShards[0]
	unsorted.NodeID = "malformed-extra-host"
	unsorted.NodeIDDigest = testReceiptDigest(unsorted.NodeID)
	unsorted.ChildOperationID = strings.Repeat("c", 64)
	unsorted.ShardDigest = testReceiptDigest("shard-unsorted")
	input.TerminalShards = append(input.TerminalShards, unsorted)
	if _, err := ProjectPostgreSQLLifecycle(input); !errors.Is(err, ErrPostgreSQLReceiptStore) {
		t.Fatalf("unsorted shards error = %v", err)
	}

	input = projectorSingleHostInput(t)
	input.PlanDigest = ""
	if _, err := ProjectPostgreSQLLifecycle(input); !errors.Is(err, ErrPostgreSQLReceiptStore) {
		t.Fatalf("missing plan digest error = %v", err)
	}

	input = projectorSingleHostInput(t)
	input.StageTimes = input.StageTimes[:7]
	if _, err := ProjectPostgreSQLLifecycle(input); !errors.Is(err, ErrPostgreSQLReceiptStore) {
		t.Fatalf("missing stage time error = %v", err)
	}
}

func assertPostgreSQLLifecycleProjection(t *testing.T, projection PostgreSQLLifecycleProjection, status string) {
	t.Helper()
	assertPostgreSQLLifecycleShape(t, projection, status)
	assertPostgreSQLLifecycleReceipt(t, projection)
	assertPostgreSQLLifecycleEvents(t, projection.Events)
}

func assertPostgreSQLLifecycleShape(t *testing.T, projection PostgreSQLLifecycleProjection, status string) {
	t.Helper()
	if len(projection.Events) != PostgreSQLReceiptLifecycleStageCount {
		t.Fatalf("event count = %d, want %d", len(projection.Events), PostgreSQLReceiptLifecycleStageCount)
	}
	if projection.Receipt.EventSeq != int64(PostgreSQLReceiptLifecycleStageCount) || projection.Receipt.EventDigest != projection.Events[7].EventDigest {
		t.Fatalf("terminal event binding = seq %d digest %q", projection.Receipt.EventSeq, projection.Receipt.EventDigest)
	}
	if projection.Receipt.Status != status || projection.Receipt.CleanupState != PostgreSQLReceiptCleanupVerified {
		t.Fatalf("terminal outcome = %#v", projection.Receipt)
	}
}

func assertPostgreSQLLifecycleReceipt(t *testing.T, projection PostgreSQLLifecycleProjection) {
	t.Helper()
	if err := projection.Receipt.Validate(); err != nil {
		t.Fatalf("receipt.Validate() error = %v", err)
	}
	if err := validatePostgreSQLReceiptEventChain(projection.Events, projection.Receipt); err != nil {
		t.Fatalf("validatePostgreSQLReceiptEventChain() error = %v", err)
	}
	_, digest, err := EncodePostgreSQLTerminalReceipt(projection.Receipt)
	if err != nil || digest != projection.Receipt.ReceiptDigest {
		t.Fatalf("terminal canonical digest = %q/%q, err=%v", projection.Receipt.ReceiptDigest, digest, err)
	}
}

func assertPostgreSQLLifecycleEvents(t *testing.T, events []PostgreSQLReceiptEvent) {
	t.Helper()
	for index, event := range events {
		if event.Seq != int64(index+1) {
			t.Fatalf("event %d seq = %d", index+1, event.Seq)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("event %d Validate() error = %v", index+1, err)
		}
		_, digest, err := EncodePostgreSQLReceiptEvent(event)
		if err != nil || digest != event.EventDigest {
			t.Fatalf("event %d canonical digest = %q/%q, err=%v", index+1, event.EventDigest, digest, err)
		}
	}
}

func projectorSingleHostInput(t *testing.T) PostgreSQLLifecycleProjectionInput {
	t.Helper()
	base := time.Date(2026, 8, 22, 1, 2, 0, 0, time.UTC)
	shards := make([]PostgreSQLTerminalShard, 0, 1)
	for index, nodeID := range []string{"ubuntu-physical-host"} {
		shards = append(shards, PostgreSQLTerminalShard{
			ChildOperationID: testReceiptOperationID(index + 1),
			NodeID:           nodeID,
			NodeIDDigest:     testReceiptDigest(nodeID),
			ExecutorKind:     "remote_node",
			ShardDigest:      testReceiptDigest("shard-" + nodeID),
			BatchSetDigest:   testReceiptDigest("batch-" + nodeID),
			UnitSetDigest:    testReceiptDigest("unit-" + nodeID),
			StartedAt:        base,
			FinishedAt:       base.Add(7 * time.Second),
			ExitCode:         0,
			CleanupState:     PostgreSQLReceiptCleanupVerified,
			Status:           PostgreSQLReceiptStatusSucceeded,
		})
	}
	return PostgreSQLLifecycleProjectionInput{
		OperationID:           testReceiptOperationID(0),
		RequestDigest:         testReceiptDigest("request"),
		ManifestDigest:        testReceiptDigest("manifest"),
		PlanDigest:            testReceiptDigest("plan"),
		ExecutorBindingDigest: testReceiptDigest("executor-binding"),
		NodeSetDigest:         mustPostgreSQLTerminalNodeSetDigest(t, shards),
		AuthorityGeneration:   9,
		StageTimes: []time.Time{
			base, base.Add(time.Second), base.Add(2 * time.Second), base.Add(3 * time.Second),
			base.Add(4 * time.Second), base.Add(5 * time.Second), base.Add(6 * time.Second), base.Add(7 * time.Second),
		},
		TerminalShards: shards,
		Status:         PostgreSQLReceiptStatusSucceeded,
		CleanupState:   PostgreSQLReceiptCleanupVerified,
	}
}

func mustPostgreSQLTerminalNodeSetDigest(t *testing.T, shards []PostgreSQLTerminalShard) string {
	t.Helper()
	digest, err := PostgreSQLTerminalNodeSetDigest(shards)
	if err != nil {
		t.Fatalf("PostgreSQLTerminalNodeSetDigest() error = %v", err)
	}
	return digest
}

func testReceiptOperationID(seed int) string {
	if seed == 0 {
		return strings.Repeat("a", 64)
	}
	return strings.Repeat("b", 64)
}
