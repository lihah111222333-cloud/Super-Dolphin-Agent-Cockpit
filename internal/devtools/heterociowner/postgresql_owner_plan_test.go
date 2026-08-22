package heterociowner

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestPostgreSQLOwnerPlanAndReserveJournalReplay(t *testing.T) {
	var _ heteroci.TopologySessionMutator = (*PostgreSQLOwner)(nil)
	database, state := openPostgreSQLOwnerTestDB(t)
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPostgreSQLOwnerWithClock: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	request := postgresqlPlanRequestFixture(t, fence, now)
	state.mu.Lock()
	state.commitAppliedError = errors.New("network dropped after COMMIT")
	state.mu.Unlock()
	first, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve with commit reconciliation: %v", err)
	}
	replay, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve replay: %v", err)
	}
	if !reflect.DeepEqual(first, replay) {
		t.Fatalf("replay drifted:\n got %#v\nwant %#v", replay, first)
	}
	drifted := request
	drifted.Units = append([]heteroci.ExecutionUnit(nil), request.Units...)
	drifted.Units[0].EstimatedDurationMS++
	if _, err := owner.PlanAndReserve(context.Background(), session, drifted); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("operation drift error = %v, want ErrOperationConflict", err)
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != first.Fence {
		t.Fatalf("CurrentFence = %#v, %v; want %#v", current, err, first.Fence)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	assertPostgreSQLOwnerTransactionEvidence(t, state, 4)
}

func postgresqlPlanRequestFixture(t *testing.T, fence heteroci.OwnerFence, now time.Time) heteroci.TopologyPlanRequest {
	t.Helper()
	manifest := durableRoutingManifest()
	return heteroci.TopologyPlanRequest{
		OperationID: heteroci.OperationID(strings.Repeat("a", heteroci.OperationIDHexLength)), RepositoryID: "repository-a", SourceTreeSHA: strings.Repeat("b", 40), RunID: "run-a",
		Manifest: manifest, SourceSnapshot: durableRoutingSnapshot(manifest, fence.OwnerEpoch, now),
		Units: []heteroci.ExecutionUnit{{UnitID: "unit-a", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 512 * 1024 * 1024}}, HeartbeatTTL: 30 * time.Second,
	}
}
