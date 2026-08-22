package heterociowner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const (
	durableRoutingNowUnixMilli = int64(1_700_000_000_000)
	durableRoutingMemoryBytes  = int64(8 * 1024 * 1024 * 1024)
)

func TestOwnerPlanAndReserveCommitsAndReplaysAtomically(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	operationID := heteroci.OperationID(strings.Repeat("a", heteroci.OperationIDHexLength))
	first, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, operationID, "run-a", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	assertDurablePlan(t, first, fence, operationID, now)
	assertDurablePlanRows(t, owner, fence, "run-a", 1, 1)
	replay, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, operationID, "run-a", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("PlanAndReserve replay: %v", err)
	}
	if !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay drifted:\n got %#v\nwant %#v", replay, first)
	}
	current, err := owner.CurrentFence(context.Background())
	if err != nil || current != first.Fence {
		t.Fatalf("current fence = %#v, %v", current, err)
	}
}

func TestOwnerPlanAndReserveRejectsOperationDriftAndStaleFence(t *testing.T) {
	owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
	operationID := heteroci.OperationID(strings.Repeat("b", heteroci.OperationIDHexLength))
	if _, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, operationID, "run-a", manifest, snapshot, units, 30*time.Second)); err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	drifted := append([]heteroci.ExecutionUnit(nil), units...)
	drifted[0].EstimatedDurationMS++
	if _, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, operationID, "run-a", manifest, snapshot, drifted, 30*time.Second)); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("operation drift error = %v, want ErrOperationConflict", err)
	}
	staleID := heteroci.OperationID(strings.Repeat("c", heteroci.OperationIDHexLength))
	if _, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, staleID, "run-b", manifest, snapshot, units, 30*time.Second)); err == nil {
		t.Fatal("PlanAndReserve accepted stale owner fence")
	}
	assertDurablePlanRows(t, owner, fence, "run-a", 1, 1)
}

func TestOwnerStartCommitsOccupancyAndReplaysExactly(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	planID := heteroci.OperationID(strings.Repeat("d", heteroci.OperationIDHexLength))
	plan, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, planID, "run-start", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	startAt := now.Add(time.Second)
	owner.now = func() time.Time { return startAt }
	current := durableRoutingSnapshot(manifest, fence.OwnerEpoch, startAt)
	startID := heteroci.OperationID(strings.Repeat("e", heteroci.OperationIDHexLength))
	started, err := owner.Start(context.Background(), plan.Fence, startID, "run-start", "unit-a", current)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	assertDurableStartedLease(t, started, plan, startAt)
	replay, err := owner.Start(context.Background(), plan.Fence, startID, "run-start", "unit-a", current)
	if err != nil {
		t.Fatalf("Start replay: %v", err)
	}
	if !reflect.DeepEqual(replay, started) {
		t.Fatalf("Start replay drifted:\n got %#v\nwant %#v", replay, started)
	}
	assertOwnerLeaseState(t, owner, fence, "run-start", "unit-a", heteroci.LeaseStateStarted, started.Fence.Revision)
}

func TestOwnerStartRejectsDriftAndStaleFenceWithoutMutation(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	plan, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, heteroci.OperationID(strings.Repeat("3", 64)), "run-start", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	startAt := now.Add(time.Second)
	owner.now = func() time.Time { return startAt }
	current := durableRoutingSnapshot(manifest, fence.OwnerEpoch, startAt)
	startID := heteroci.OperationID(strings.Repeat("4", 64))
	if _, err := owner.Start(context.Background(), plan.Fence, startID, "run-start", "unit-a", current); err != nil {
		t.Fatalf("Start: %v", err)
	}
	drifted := cloneSnapshot(current)
	drifted.Nodes[0].ProbeBootID = "different-boot"
	if _, err := owner.Start(context.Background(), plan.Fence, startID, "run-start", "unit-a", drifted); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("Start drift error = %v, want ErrOperationConflict", err)
	}
	staleID := heteroci.OperationID(strings.Repeat("5", 64))
	if _, err := owner.Start(context.Background(), plan.Fence, staleID, "run-start", "unit-a", current); err == nil {
		t.Fatal("Start accepted stale fence after committed transition")
	}
	assertOwnerLeaseState(t, owner, fence, "run-start", "unit-a", heteroci.LeaseStateStarted, plan.Fence.Revision+1)
}
func TestOwnerStartRejectsLeaseJSONIdentitySwapWithoutMutation(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	second := units[0]
	second.UnitID = "unit-b"
	units = append(units, second)
	plan := mustDurablePlan(t, owner, fence, operationID("a"), "run-target", manifest, snapshot, units)
	// Use a valid planned lease from another run and unit while preserving the decision digest.
	sourceOwner, sourceFence, sourceManifest, sourceSnapshot, sourceUnits, _ := durableRoutingFixture(t)
	sourceSecond := sourceUnits[0]
	sourceSecond.UnitID = "unit-b"
	sourceUnits = append(sourceUnits, sourceSecond)
	sourcePlan := mustDurablePlan(t, sourceOwner, sourceFence, operationID("b"), "run-source", sourceManifest, sourceSnapshot, sourceUnits)
	if sourcePlan.Decision.DecisionDigest != plan.Decision.DecisionDigest {
		t.Fatalf("source decision digest = %s, want target %s", sourcePlan.Decision.DecisionDigest, plan.Decision.DecisionDigest)
	}
	swappedLease := readDurableLeaseJSON(t, sourceOwner, sourceFence, "run-source", "unit-b")
	writeDurableLeaseJSON(t, owner, fence, "run-target", "unit-a", swappedLease)
	startAt := now.Add(time.Second)
	owner.now = func() time.Time { return startAt }
	started, err := owner.Start(context.Background(), plan.Fence, operationID("c"), "run-target", "unit-a", durableRoutingSnapshot(manifest, fence.OwnerEpoch, startAt))
	if err == nil {
		t.Fatal("Start accepted lease JSON whose run/unit identity differs from the SQL key")
	}
	if started != (heteroci.StartedRoutingLease{}) {
		t.Fatalf("rejected Start returned started occupancy: %#v", started)
	}
	assertOwnerLeaseState(t, owner, fence, "run-target", "unit-a", heteroci.LeaseStatePlanned, plan.Fence.Revision)
	assertOwnerLeaseState(t, owner, fence, "run-target", "unit-b", heteroci.LeaseStatePlanned, plan.Fence.Revision)
	assertDurablePlanRows(t, owner, fence, "run-target", 2, 1)
	assertOwnerRowCount(t, owner, "heteroci_routing_leases", "scope = ? AND owner_epoch = ? AND state = ?", []any{fence.Scope, fence.OwnerEpoch, heteroci.LeaseStateStarted}, 0)
	assertOwnerRunRevision(t, owner, fence, "run-target", plan.Fence.Revision)
	assertOwnerCurrentFence(t, owner, plan.Fence)
}

func mustDurablePlan(t *testing.T, owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID string, manifest heteroci.Manifest, snapshot heteroci.SnapshotSet, units []heteroci.ExecutionUnit) DurableReservedRoutingPlan {
	t.Helper()
	plan, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, operationID, runID, manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("PlanAndReserve %s: %v", runID, err)
	}
	return plan
}

func readDurableLeaseJSON(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID, unitID string) []byte {
	t.Helper()
	var leaseBytes []byte
	if err := owner.db.QueryRowContext(context.Background(), "SELECT lease_json FROM heteroci_routing_leases WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ?", fence.Scope, fence.OwnerEpoch, runID, unitID).Scan(&leaseBytes); err != nil {
		t.Fatalf("read %s/%s lease JSON: %v", runID, unitID, err)
	}
	return leaseBytes
}

func writeDurableLeaseJSON(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID, unitID string, leaseBytes []byte) {
	t.Helper()
	result, err := owner.db.ExecContext(context.Background(), "UPDATE heteroci_routing_leases SET lease_json = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ?", leaseBytes, fence.Scope, fence.OwnerEpoch, runID, unitID)
	if err != nil {
		t.Fatalf("swap %s/%s lease JSON: %v", runID, unitID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("count swapped %s/%s lease rows: %v", runID, unitID, err)
	}
	if rows != 1 {
		t.Fatalf("swapped %s/%s lease rows = %d, want 1", runID, unitID, rows)
	}
}

func assertOwnerRunRevision(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID string, want heteroci.RoutingOwnerRevision) {
	t.Helper()
	var got int64
	if err := owner.db.QueryRowContext(context.Background(), "SELECT last_revision FROM heteroci_routing_runs WHERE scope = ? AND owner_epoch = ? AND run_id = ?", fence.Scope, fence.OwnerEpoch, runID).Scan(&got); err != nil {
		t.Fatalf("read %s run revision: %v", runID, err)
	}
	if got != int64(want) {
		t.Fatalf("%s run revision = %d, want %d", runID, got, want)
	}
}

func assertOwnerCurrentFence(t *testing.T, owner *Owner, want heteroci.OwnerFence) {
	t.Helper()
	got, err := owner.CurrentFence(context.Background())
	if err != nil {
		t.Fatalf("CurrentFence: %v", err)
	}
	if got != want {
		t.Fatalf("owner fence = %#v, want %#v", got, want)
	}
}

func TestOwnerStartAdvancesMultipleLeasesWithIndependentCAS(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	second := units[0]
	second.UnitID = "unit-b"
	units = append(units, second)
	plan, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, heteroci.OperationID(strings.Repeat("6", 64)), "run-multi", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	startAt := now.Add(time.Second)
	owner.now = func() time.Time { return startAt }
	current := durableRoutingSnapshot(manifest, fence.OwnerEpoch, startAt)
	first, err := owner.Start(context.Background(), plan.Fence, heteroci.OperationID(strings.Repeat("7", 64)), "run-multi", "unit-a", current)
	if err != nil {
		t.Fatalf("Start unit-a: %v", err)
	}
	secondStarted, err := owner.Start(context.Background(), first.Fence, heteroci.OperationID(strings.Repeat("8", 64)), "run-multi", "unit-b", current)
	if err != nil {
		t.Fatalf("Start unit-b: %v", err)
	}
	if secondStarted.Fence.Revision != first.Fence.Revision+1 || secondStarted.Occupancy.UnitID != "unit-b" {
		t.Fatalf("second started lease = %#v", secondStarted)
	}
}

func TestOwnerPlanAndReserveSubtractsCurrentEpochReservationsExactlyOnce(t *testing.T) {
	owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
	first, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, heteroci.OperationID(strings.Repeat("9", 64)), "run-first", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("first PlanAndReserve: %v", err)
	}
	second, err := owner.PlanAndReserve(context.Background(), first.Fence, topologyPlanRequest(first.Fence, heteroci.OperationID(strings.Repeat("a", 64)), "run-second", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("second PlanAndReserve: %v", err)
	}
	gross := snapshot.Nodes[0].Available
	if second.SourceSnapshot.Nodes[0].Available != gross {
		t.Fatalf("second source capacity = %#v, want gross %#v", second.SourceSnapshot.Nodes[0].Available, gross)
	}
	want := gross
	want.CPUMilli -= units[0].CPUMilli
	want.MemoryBytes -= units[0].MemoryBytes
	want.MaxConcurrency--
	if second.EffectiveSnapshot.Nodes[0].Available != want {
		t.Fatalf("second effective capacity = %#v, want %#v", second.EffectiveSnapshot.Nodes[0].Available, want)
	}
}

func TestOwnerTakeoverDoesNotReloadOldEpochReservations(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	first, err := owner.PlanAndReserve(context.Background(), fence, topologyPlanRequest(fence, heteroci.OperationID(strings.Repeat("b", 64)), "run-old", manifest, snapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("old epoch PlanAndReserve: %v", err)
	}
	if first.Fence.Revision != 2 {
		t.Fatalf("old epoch committed fence = %#v", first.Fence)
	}
	newFence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("takeover Acquire: %v", err)
	}
	newSnapshot := durableRoutingSnapshot(manifest, newFence.OwnerEpoch, now)
	current, err := owner.PlanAndReserve(context.Background(), newFence, topologyPlanRequest(newFence, heteroci.OperationID(strings.Repeat("c", 64)), "run-new", manifest, newSnapshot, units, 30*time.Second))
	if err != nil {
		t.Fatalf("new epoch PlanAndReserve: %v", err)
	}
	if current.EffectiveSnapshot.Nodes[0].Available != newSnapshot.Nodes[0].Available {
		t.Fatal("new owner epoch reloaded old epoch reservation")
	}
}

func TestOwnerPlanJournalBindsCanonicalPerRunRepositoryAudit(t *testing.T) {
	owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
	firstRequest := topologyPlanRequest(fence, operationID("7"), "run-repository-a", manifest, snapshot, units, 30*time.Second)
	firstRequest.RepositoryID = "repository-a"
	firstRequest.SourceTreeSHA = strings.Repeat("a", 40)
	first, err := owner.PlanAndReserve(context.Background(), fence, firstRequest)
	if err != nil {
		t.Fatalf("first PlanAndReserve: %v", err)
	}
	firstBytes := readPlanAuditRequestBytes(t, owner, fence, firstRequest.OperationID)
	assertCanonicalPlanAuditRequest(t, firstBytes, fence, firstRequest)
	replay, err := owner.PlanAndReserve(context.Background(), fence, firstRequest)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatalf("canonical plan replay = %#v, %v; want %#v", replay, err, first)
	}

	secondRequest := topologyPlanRequest(first.Fence, operationID("8"), "run-repository-b", manifest, snapshot, units, 30*time.Second)
	secondRequest.RepositoryID = "repository-b"
	secondRequest.SourceTreeSHA = strings.Repeat("b", 40)
	if _, err := owner.PlanAndReserve(context.Background(), first.Fence, secondRequest); err != nil {
		t.Fatalf("second repository PlanAndReserve: %v", err)
	}
	secondBytes := readPlanAuditRequestBytes(t, owner, fence, secondRequest.OperationID)
	assertCanonicalPlanAuditRequest(t, secondBytes, first.Fence, secondRequest)
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("different repository/tree audit produced identical journal request bytes")
	}
}

func TestOwnerPlanRejectsMissingOrNonCanonicalRepositoryAudit(t *testing.T) {
	owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
	base := topologyPlanRequest(fence, operationID("9"), "run-invalid-audit", manifest, snapshot, units, 30*time.Second)
	for _, test := range []struct {
		name   string
		mutate func(*heteroci.TopologyPlanRequest)
	}{
		{name: "empty repository", mutate: func(request *heteroci.TopologyPlanRequest) { request.RepositoryID = "" }},
		{name: "empty tree", mutate: func(request *heteroci.TopologyPlanRequest) { request.SourceTreeSHA = "" }},
		{name: "uppercase tree", mutate: func(request *heteroci.TopologyPlanRequest) { request.SourceTreeSHA = strings.Repeat("A", 40) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.mutate(&request)
			if result, err := owner.PlanAndReserve(context.Background(), fence, request); err == nil || !reflect.ValueOf(result).IsZero() {
				t.Fatalf("invalid audit result=%#v err=%v", result, err)
			}
		})
	}
}

func readPlanAuditRequestBytes(t *testing.T, owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID) []byte {
	t.Helper()
	var raw []byte
	if err := owner.db.QueryRowContext(context.Background(), "SELECT request_bytes FROM heteroci_routing_operations WHERE scope = ? AND owner_epoch = ? AND operation_id = ?", fence.Scope, fence.OwnerEpoch, operationID).Scan(&raw); err != nil {
		t.Fatalf("read plan audit request bytes: %v", err)
	}
	return raw
}

func assertCanonicalPlanAuditRequest(t *testing.T, raw []byte, fence heteroci.OwnerFence, request heteroci.TopologyPlanRequest) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode plan audit envelope fields: %v", err)
	}
	assertExactPlanAuditJSONFields(t, envelope, "fence", "request")
	var requestFields map[string]json.RawMessage
	if err := json.Unmarshal(envelope["request"], &requestFields); err != nil {
		t.Fatalf("decode topology plan request fields: %v", err)
	}
	assertExactPlanAuditJSONFields(t, requestFields,
		"operation_id", "repository_id", "source_tree_sha", "run_id",
		"manifest", "source_snapshot", "units", "heartbeat_ttl",
	)
	var decoded planAndReserveRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode plan audit request: %v", err)
	}
	if !reflect.DeepEqual(decoded, planAndReserveRequest{Fence: fence, Request: request}) {
		t.Fatalf("plan audit request drifted: got %#v", decoded)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode plan audit request: %v", err)
	}
	if !bytes.Equal(raw, canonical) {
		t.Fatalf("plan audit request bytes are noncanonical: %q", raw)
	}
}

func assertExactPlanAuditJSONFields(t *testing.T, fields map[string]json.RawMessage, expected ...string) {
	t.Helper()
	if len(fields) != len(expected) {
		t.Fatalf("plan audit JSON fields = %v, want exactly %v", fields, expected)
	}
	for _, field := range expected {
		if _, ok := fields[field]; !ok {
			t.Fatalf("plan audit JSON field %q is missing: %v", field, fields)
		}
	}
}

func durableRoutingFixture(t *testing.T) (*Owner, heteroci.OwnerFence, heteroci.Manifest, heteroci.SnapshotSet, []heteroci.ExecutionUnit, time.Time) {
	t.Helper()
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner := newTestOwner(t)
	owner.now = func() time.Time { return now }
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	manifest := durableRoutingManifest()
	snapshot := durableRoutingSnapshot(manifest, fence.OwnerEpoch, now)
	units := []heteroci.ExecutionUnit{{UnitID: "unit-a", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 512 * 1024 * 1024}}
	return owner, fence, manifest, snapshot, units, now
}

func durableRoutingManifest() heteroci.Manifest {
	return heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Nodes: []heteroci.NodeConfig{{
			NodeID:        "node-a",
			Endpoint:      "https://node-a.invalid/runner",
			ProbeIdentity: "sha256:" + strings.Repeat("1", 64),
			Platform:      heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{
				Runtime:     heteroci.RuntimeUbuntuContainer,
				Image:       "ubuntu:24.04",
				ImageDigest: "sha256:" + strings.Repeat("2", 64),
			}},
		}},
	}
}

func durableRoutingSnapshot(manifest heteroci.Manifest, ownerEpoch heteroci.RoutingOwnerEpoch, now time.Time) heteroci.SnapshotSet {
	config := manifest.Nodes[0]
	capacity := heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: durableRoutingMemoryBytes, MaxConcurrency: 4}
	return heteroci.SnapshotSet{
		SchemaVersion:       heteroci.SnapshotSchemaVersion,
		Authority:           heteroci.AuthorityLocalNonAuthoritative,
		OwnerEpoch:          ownerEpoch,
		CapacityBasis:       heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		CapturedAtUnixMilli: now.UnixMilli(),
		Nodes: []heteroci.NodeSnapshot{{
			NodeID:             config.NodeID,
			Endpoint:           config.Endpoint,
			Platform:           config.Platform,
			ProbeIdentity:      config.ProbeIdentity,
			ProbeBootID:        "boot-a",
			ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: config.Capabilities[0].ImageDigest}},
			Status:             heteroci.NodeStatusLive,
			HeartbeatUnixMilli: now.UnixMilli(),
			Total:              capacity,
			Available:          capacity,
		}},
	}
}

func assertDurablePlan(t *testing.T, plan DurableReservedRoutingPlan, fence heteroci.OwnerFence, operationID heteroci.OperationID, now time.Time) {
	t.Helper()
	if plan.Fence.Scope != fence.Scope || plan.Fence.OwnerID != fence.OwnerID || plan.Fence.OwnerEpoch != fence.OwnerEpoch || plan.Fence.Revision != fence.Revision+1 || plan.OperationID != operationID || len(plan.Leases) != 1 {
		t.Fatalf("durable plan identity = %#v", plan)
	}
	if plan.Leases[0].State != heteroci.LeaseStatePlanned || plan.Leases[0].ReservedAtUnixMilli != now.UnixMilli() {
		t.Fatalf("durable planned lease = %#v", plan.Leases[0])
	}
	if plan.SourceSnapshot.Nodes[0].Available != plan.SourceSnapshot.Nodes[0].Total {
		t.Fatal("source snapshot was pre-subtracted")
	}
}

func assertDurablePlanRows(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID string, wantLeases, wantReservations int) {
	t.Helper()
	assertOwnerRowCount(t, owner, "heteroci_routing_runs", "scope = ? AND owner_epoch = ? AND run_id = ?", []any{fence.Scope, fence.OwnerEpoch, runID}, 1)
	assertOwnerRowCount(t, owner, "heteroci_routing_leases", "scope = ? AND owner_epoch = ? AND run_id = ?", []any{fence.Scope, fence.OwnerEpoch, runID}, wantLeases)
	assertOwnerRowCount(t, owner, "heteroci_routing_reservations", "scope = ? AND owner_epoch = ? AND run_id = ?", []any{fence.Scope, fence.OwnerEpoch, runID}, wantReservations)
	assertOwnerRowCount(t, owner, "heteroci_routing_operations", "scope = ? AND owner_epoch = ?", []any{fence.Scope, fence.OwnerEpoch}, 1)
}

func assertOwnerRowCount(t *testing.T, owner *Owner, table, where string, args []any, want int) {
	t.Helper()
	var got int
	query := "SELECT COUNT(*) FROM " + table + " WHERE " + where
	if err := owner.db.QueryRowContext(context.Background(), query, args...).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

func assertDurableStartedLease(t *testing.T, started heteroci.StartedRoutingLease, plan DurableReservedRoutingPlan, startAt time.Time) {
	t.Helper()
	if started.Fence.OwnerEpoch != plan.Fence.OwnerEpoch || started.Fence.Revision != plan.Fence.Revision+1 {
		t.Fatalf("started fence = %#v, plan fence %#v", started.Fence, plan.Fence)
	}
	if started.Lease.State != heteroci.LeaseStateStarted || started.Lease.StartedAtUnixMilli != startAt.UnixMilli() {
		t.Fatalf("started lease = %#v", started.Lease)
	}
	want := heteroci.StartedRoutingOccupancy{OwnerEpoch: plan.Fence.OwnerEpoch, RunID: "run-start", UnitID: "unit-a", DecisionDigest: plan.Decision.DecisionDigest}
	if started.Occupancy != want {
		t.Fatalf("started occupancy = %#v, want %#v", started.Occupancy, want)
	}
}

func assertOwnerLeaseState(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID, unitID string, want heteroci.RoutingLeaseState, wantRevision heteroci.RoutingOwnerRevision) {
	t.Helper()
	var state string
	var revision int64
	err := owner.db.QueryRowContext(context.Background(), "SELECT state, last_revision FROM heteroci_routing_leases WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ?", fence.Scope, fence.OwnerEpoch, runID, unitID).Scan(&state, &revision)
	if err != nil {
		t.Fatalf("read durable lease state: %v", err)
	}
	if heteroci.RoutingLeaseState(state) != want || revision != int64(wantRevision) {
		t.Fatalf("durable lease state/revision = %s/%d, want %s/%d", state, revision, want, wantRevision)
	}
}
