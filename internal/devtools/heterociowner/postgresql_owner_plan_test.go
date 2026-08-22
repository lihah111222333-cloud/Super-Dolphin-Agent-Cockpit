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

func TestPostgreSQLOwnerPlanSeparatesSameCallerAcrossRepositories(t *testing.T) {
	harness := newPostgreSQLPlanHarness(t)
	firstRequest, first := harness.plan(t, "repository-a", strings.Repeat("a", 40), "shared-run")
	secondRequest, second := harness.plan(t, "repository-b", strings.Repeat("b", 40), "shared-run")
	assertPostgreSQLPlanIdentitiesSeparate(t, firstRequest, first, secondRequest, second)
}

type postgresqlPlanHarness struct {
	owner   *PostgreSQLOwner
	fence   heteroci.OwnerFence
	session heteroci.TopologySession
	now     time.Time
}

func newPostgreSQLPlanHarness(t *testing.T) postgresqlPlanHarness {
	t.Helper()
	database, _ := openPostgreSQLOwnerTestDB(t)
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
	return postgresqlPlanHarness{owner: owner, fence: fence, session: session, now: now}
}

func (harness postgresqlPlanHarness) plan(t *testing.T, repositoryID, treeSHA, callerRunID string) (heteroci.TopologyPlanRequest, heteroci.TopologyReservedPlan) {
	t.Helper()
	request := postgresqlPlanRequestFixture(t, harness.fence, harness.now)
	request.RunIdentity = topologyRunIdentity(t, repositoryID, treeSHA, callerRunID)
	request.OperationID = topologyPlanOperationID(t, request.RunIdentity.DurableRunID)
	result, err := harness.owner.PlanAndReserve(context.Background(), harness.session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve %s: %v", repositoryID, err)
	}
	return request, result
}

func assertPostgreSQLPlanIdentitiesSeparate(t *testing.T, firstRequest heteroci.TopologyPlanRequest, first heteroci.TopologyReservedPlan, secondRequest heteroci.TopologyPlanRequest, second heteroci.TopologyReservedPlan) {
	t.Helper()
	if firstRequest.RunIdentity.DurableRunID == secondRequest.RunIdentity.DurableRunID || first.OperationID == second.OperationID {
		t.Fatalf("PostgreSQL plans collided: first=%#v second=%#v", first, second)
	}
	if len(first.Leases) != 1 || len(second.Leases) != 1 || first.Leases[0].RunID != string(firstRequest.RunIdentity.DurableRunID) || second.Leases[0].RunID != string(secondRequest.RunIdentity.DurableRunID) {
		t.Fatalf("PostgreSQL plan run bindings drifted: first=%#v second=%#v", first.Leases, second.Leases)
	}
}

func postgresqlPlanRequestFixture(t *testing.T, fence heteroci.OwnerFence, now time.Time) heteroci.TopologyPlanRequest {
	t.Helper()
	manifest := durableRoutingManifest()
	runIdentity, err := heteroci.DeriveTopologyRunIdentity("repository-a", strings.Repeat("b", 40), "run-a")
	if err != nil {
		t.Fatalf("DeriveTopologyRunIdentity: %v", err)
	}
	operationID := topologyPlanOperationID(t, runIdentity.DurableRunID)
	topologyManifestDigest, err := heteroci.DeriveTopologyManifestDigest(manifest)
	if err != nil {
		t.Fatalf("DeriveTopologyManifestDigest: %v", err)
	}
	return heteroci.TopologyPlanRequest{
		OperationID: operationID, ExternalOperationID: heteroci.OperationID(strings.Repeat("c", 64)), RequestDigest: digestBytes([]byte("topology-request-" + string(runIdentity.DurableRunID))), ManifestDigest: "sha256:" + strings.Repeat("9", 64), TopologyManifestDigest: topologyManifestDigest, RunIdentity: runIdentity,
		Manifest: manifest, SourceSnapshot: durableRoutingSnapshot(manifest, fence.OwnerEpoch, now),
		Units: []heteroci.ExecutionUnit{{UnitID: "unit-a", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 512 * 1024 * 1024}}, HeartbeatTTL: 30 * time.Second,
	}
}

func topologyPlanOperationID(t *testing.T, runID heteroci.TopologyRunID) heteroci.OperationID {
	t.Helper()
	operationID, err := heteroci.DeriveTopologyOperationID(runID, heteroci.TopologyOperationPhasePlan, "")
	if err != nil {
		t.Fatalf("DeriveTopologyOperationID: %v", err)
	}
	return operationID
}
