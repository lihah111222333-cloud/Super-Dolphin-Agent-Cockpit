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

const (
	readStateRunID       = "run-read-state"
	readStateSecretURL   = "https://secret-endpoint.invalid/private"
	readStateSecretValue = "secret-payload"
)

func TestOwnerReadStateCoversPlannedStartedAndTerminal(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	planID := operationID("a")
	plan, err := owner.PlanAndReserve(context.Background(), fence, planID, readStateRunID, manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	planned := assertReadRunState(t, owner, readStateRunID, plan.Fence, manifest, units, plan.Decision, plan.Leases, 30*time.Second, false, plan.Fence.Revision)
	assertLocateRunState(t, owner, readStateRunID, planned)

	startAt := now.Add(time.Second)
	owner.now = func() time.Time { return startAt }
	started, err := owner.Start(context.Background(), plan.Fence, operationID("b"), readStateRunID, "unit-a", durableRoutingSnapshot(manifest, fence.OwnerEpoch, startAt))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	assertReadRunState(t, owner, readStateRunID, started.Fence, manifest, units, plan.Decision, []heteroci.RoutingLease{started.Lease}, 30*time.Second, false, started.Fence.Revision)

	finishAt := now.Add(2 * time.Second)
	owner.now = func() time.Time { return finishAt }
	finished, err := owner.Finish(context.Background(), started.Fence, operationID("c"), readStateRunID, "unit-a", heteroci.LeaseStateSucceeded)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	assertReadRunState(t, owner, readStateRunID, finished.Fence, manifest, units, plan.Decision, []heteroci.RoutingLease{finished.Lease}, 30*time.Second, true, finished.Fence.Revision)
}

func TestOwnerReadOperationDistinguishesMissingAndExactReplay(t *testing.T) {
	owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
	opID := operationID("d")
	plan, err := owner.PlanAndReserve(context.Background(), fence, opID, "run-operation-read", manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	assertMissingReadOperation(t, owner, fence)
	if _, err := owner.PlanAndReserve(context.Background(), fence, opID, "run-operation-read", manifest, snapshot, units, 30*time.Second); err != nil {
		t.Fatalf("exact PlanAndReserve replay: %v", err)
	}
	state, found, err := owner.ReadOperation(context.Background(), plan.Fence, opID)
	if err != nil {
		t.Fatalf("ReadOperation exact replay: %v", err)
	}
	assertReadOperationState(t, state, found, plan.Fence, opID)
	assertReadOperationResultCopy(t, owner, plan.Fence, opID, state)
}

func assertMissingReadOperation(t *testing.T, owner *Owner, fence heteroci.OwnerFence) {
	t.Helper()
	missing, found, err := owner.ReadOperation(context.Background(), fence, operationID("e"))
	if err != nil {
		t.Fatalf("ReadOperation missing: %v", err)
	}
	if found || missing.OperationID != "" || len(missing.ResultBytes) != 0 {
		t.Fatalf("missing operation = %#v, found=%t", missing, found)
	}
}

func assertReadOperationState(t *testing.T, state DurableOperationState, found bool, wantFence heteroci.OwnerFence, wantID heteroci.OperationID) {
	t.Helper()
	if !found {
		t.Fatalf("exact operation was not found: %#v", state)
	}
	if state.Fence != wantFence || state.OperationID != wantID || state.Kind != "plan_and_reserve" || state.Revision != wantFence.Revision {
		t.Fatalf("exact operation identity = %#v", state)
	}
	if state.RequestDigest == "" || state.ResultDigest == "" || len(state.ResultBytes) == 0 {
		t.Fatalf("exact operation integrity fields = %#v", state)
	}
}

func assertReadOperationResultCopy(t *testing.T, owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, state DurableOperationState) {
	t.Helper()
	wantResult := append([]byte(nil), state.ResultBytes...)
	state.ResultBytes[0] ^= 0xff
	reloaded, found, err := owner.ReadOperation(context.Background(), fence, operationID)
	if err != nil || !found {
		t.Fatalf("ReadOperation after caller mutation = %#v, found=%t, err=%v", reloaded, found, err)
	}
	if !reflect.DeepEqual(reloaded.ResultBytes, wantResult) {
		t.Fatalf("operation result bytes were not deep-copied: got=%q want=%q", reloaded.ResultBytes, wantResult)
	}
}

func TestOwnerReadStateMissingIsExplicit(t *testing.T) {
	owner := newTestOwner(t)
	history, err := owner.LocateRun(context.Background(), "run-missing")
	if err != nil {
		t.Fatalf("LocateRun missing: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("LocateRun missing returned %#v", history)
	}
	state, err := owner.ReadRun(context.Background(), "run-missing")
	if err == nil {
		t.Fatal("ReadRun missing returned nil error")
	}
	if state.RunID != "" || len(state.Leases) != 0 || len(state.Manifest.Nodes) != 0 {
		t.Fatalf("ReadRun missing leaked state: %#v", state)
	}
	assertReadStateErrorSafe(t, err)
}

func TestOwnerReadRunRequiresRecoveryAfterTakeover(t *testing.T) {
	owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
	plan, err := owner.PlanAndReserve(context.Background(), fence, operationID("f"), "run-recovery", manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("old epoch PlanAndReserve: %v", err)
	}
	if _, err := owner.Acquire(context.Background()); err != nil {
		t.Fatalf("takeover Acquire: %v", err)
	}
	history, err := owner.LocateRun(context.Background(), "run-recovery")
	assertHistoricalRunState(t, history, err, plan.Fence)
	state, err := owner.ReadRun(context.Background(), "run-recovery")
	assertRecoveryRequired(t, state, err)
}

func assertHistoricalRunState(t *testing.T, history []DurableRunState, err error, wantFence heteroci.OwnerFence) {
	t.Helper()
	if err != nil || len(history) != 1 {
		t.Fatalf("LocateRun old history = %#v, err=%v", history, err)
	}
	if history[0].Fence != wantFence || len(history[0].Leases) != 1 || history[0].Leases[0].State != heteroci.LeaseStatePlanned {
		t.Fatalf("LocateRun old history identity = %#v", history[0])
	}
}

func assertRecoveryRequired(t *testing.T, state DurableRunState, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("ReadRun returned retired planned run as executable")
	}
	if _, ok := errors.AsType[*RunRecoveryRequiredError](err); !ok {
		t.Fatalf("ReadRun error = %v, want RunRecoveryRequiredError", err)
	}
	if state.RunID != "" || len(state.Leases) != 0 || len(state.Manifest.Nodes) != 0 {
		t.Fatalf("recovery error leaked planned state: %#v", state)
	}
	assertReadStateErrorSafe(t, err)
}

func TestOwnerLocateRunRejectsCrossEpochIdentityAmbiguity(t *testing.T) {
	owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
	plan, err := owner.PlanAndReserve(context.Background(), fence, operationID("1"), "run-ambiguous", manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("old epoch PlanAndReserve: %v", err)
	}
	newFence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("takeover Acquire: %v", err)
	}
	insertCrossEpochRunProjection(t, owner, fence.OwnerEpoch, newFence.OwnerEpoch, "run-ambiguous")
	before := readReadStateMutationFingerprint(t, owner, fence, "run-ambiguous", plan.Fence.Revision)
	history, err := owner.LocateRun(context.Background(), "run-ambiguous")
	if err == nil {
		t.Fatal("LocateRun accepted same-run projections from two owner epochs")
	}
	var ambiguous *RunIdentityAmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("LocateRun ambiguity error = %v, want RunIdentityAmbiguousError", err)
	}
	if len(history) != 0 {
		t.Fatalf("ambiguous LocateRun returned state: %#v", history)
	}
	assertReadStateErrorSafe(t, err)
	state, err := owner.ReadRun(context.Background(), "run-ambiguous")
	if err == nil || !errors.As(err, &ambiguous) || state.RunID != "" {
		t.Fatalf("ReadRun ambiguity = %#v, err=%v", state, err)
	}
	after := readReadStateMutationFingerprint(t, owner, fence, "run-ambiguous", plan.Fence.Revision)
	if after != before {
		t.Fatalf("ambiguous reads mutated durable state: before=%#v after=%#v", before, after)
	}
}

func TestOwnerReadStateRejectsTamperingWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		read   func(*Owner, heteroci.OwnerFence, heteroci.OperationID, string) error
		tamper func(*testing.T, *Owner, heteroci.OwnerFence, heteroci.OperationID, string)
	}{
		{
			name: "run manifest digest",
			read: func(owner *Owner, _ heteroci.OwnerFence, _ heteroci.OperationID, runID string) error {
				_, err := owner.ReadRun(context.Background(), runID)
				return err
			},
			tamper: func(t *testing.T, owner *Owner, fence heteroci.OwnerFence, _ heteroci.OperationID, runID string) {
				tamperReadStateField(t, owner, "UPDATE heteroci_routing_runs SET manifest_digest = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ?", testDigest("f"), fence.Scope, fence.OwnerEpoch, runID)
			},
		},
		{
			name: "lease json",
			read: func(owner *Owner, _ heteroci.OwnerFence, _ heteroci.OperationID, runID string) error {
				_, err := owner.ReadRun(context.Background(), runID)
				return err
			},
			tamper: func(t *testing.T, owner *Owner, fence heteroci.OwnerFence, _ heteroci.OperationID, runID string) {
				tamperReadStateField(t, owner, "UPDATE heteroci_routing_leases SET lease_json = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ?", []byte(`{"run_id":"secret-payload"}`), fence.Scope, fence.OwnerEpoch, runID, "unit-a")
			},
		},
		{
			name: "lease state",
			read: func(owner *Owner, _ heteroci.OwnerFence, _ heteroci.OperationID, runID string) error {
				_, err := owner.ReadRun(context.Background(), runID)
				return err
			},
			tamper: func(t *testing.T, owner *Owner, fence heteroci.OwnerFence, _ heteroci.OperationID, runID string) {
				tamperReadStateField(t, owner, "UPDATE heteroci_routing_leases SET state = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ?", heteroci.LeaseStateStarted, fence.Scope, fence.OwnerEpoch, runID, "unit-a")
			},
		},
		{
			name: "operation digest",
			read: func(owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, _ string) error {
				_, _, err := owner.ReadOperation(context.Background(), fence, operationID)
				return err
			},
			tamper: func(t *testing.T, owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, _ string) {
				tamperReadStateField(t, owner, "UPDATE heteroci_routing_operations SET result_digest = ? WHERE scope = ? AND owner_epoch = ? AND operation_id = ?", testDigest("e"), fence.Scope, fence.OwnerEpoch, operationID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, fence, manifest, snapshot, units, _ := durableRoutingFixture(t)
			manifest.Nodes[0].Endpoint = readStateSecretURL
			snapshot.Nodes[0].Endpoint = readStateSecretURL
			runID := "run-tamper"
			operationID := operationID("2")
			plan, err := owner.PlanAndReserve(context.Background(), fence, operationID, runID, manifest, snapshot, units, 30*time.Second)
			if err != nil {
				t.Fatalf("PlanAndReserve: %v", err)
			}
			tc.tamper(t, owner, fence, operationID, runID)
			before := readReadStateMutationFingerprint(t, owner, fence, runID, plan.Fence.Revision)
			err = tc.read(owner, plan.Fence, operationID, runID)
			if err == nil {
				t.Fatal("tampered durable state was accepted")
			}
			if errors.Is(err, ErrOperationConflict) {
				t.Fatalf("tampered read returned ErrOperationConflict: %v", err)
			}
			assertReadStateErrorSafe(t, err)
			after := readReadStateMutationFingerprint(t, owner, fence, runID, plan.Fence.Revision)
			if after != before {
				t.Fatalf("tampered read mutated durable state: before=%#v after=%#v", before, after)
			}
		})
	}
}

func assertReadRunState(t *testing.T, owner *Owner, runID string, wantFence heteroci.OwnerFence, wantManifest heteroci.Manifest, wantUnits []heteroci.ExecutionUnit, wantDecision heteroci.RoutingDecision, wantLeases []heteroci.RoutingLease, wantTTL time.Duration, wantReleased bool, wantRevision heteroci.RoutingOwnerRevision) DurableRunState {
	t.Helper()
	got, err := owner.ReadRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ReadRun %s: %v", runID, err)
	}
	assertReadRunIdentity(t, got, runID, wantFence)
	assertReadRunPayload(t, got, wantManifest, wantUnits, wantDecision, wantLeases)
	assertReadRunLifecycle(t, got, wantTTL, wantReleased, wantRevision)
	return got
}

func assertReadRunIdentity(t *testing.T, got DurableRunState, runID string, wantFence heteroci.OwnerFence) {
	t.Helper()
	if got.Fence != wantFence || got.RunID != runID {
		t.Fatalf("ReadRun identity = %#v", got)
	}
}

func assertReadRunPayload(t *testing.T, got DurableRunState, wantManifest heteroci.Manifest, wantUnits []heteroci.ExecutionUnit, wantDecision heteroci.RoutingDecision, wantLeases []heteroci.RoutingLease) {
	t.Helper()
	if !reflect.DeepEqual(got.Manifest, wantManifest) || !reflect.DeepEqual(got.Units, wantUnits) || !reflect.DeepEqual(got.Decision, wantDecision) || !reflect.DeepEqual(got.Leases, wantLeases) {
		t.Fatalf("ReadRun payload = %#v", got)
	}
}

func assertReadRunLifecycle(t *testing.T, got DurableRunState, wantTTL time.Duration, wantReleased bool, wantRevision heteroci.RoutingOwnerRevision) {
	t.Helper()
	if got.HeartbeatTTL != wantTTL || got.Released != wantReleased || got.LastRevision != wantRevision {
		t.Fatalf("ReadRun lifecycle = %#v", got)
	}
}

func assertLocateRunState(t *testing.T, owner *Owner, runID string, want DurableRunState) {
	t.Helper()
	got, err := owner.LocateRun(context.Background(), runID)
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("LocateRun %s = %#v, err=%v; want %#v", runID, got, err, want)
	}
	got[0].Manifest.Nodes[0].Endpoint = "mutated-endpoint"
	got[0].Units[0].UnitID = "mutated-unit"
	got[0].Decision.Assignments[0].NodeID = "mutated-node"
	got[0].Leases[0].UnitID = "mutated-lease-unit"
	reloaded, err := owner.ReadRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ReadRun after caller mutation: %v", err)
	}
	if reloaded.Manifest.Nodes[0].Endpoint == "mutated-endpoint" || reloaded.Units[0].UnitID == "mutated-unit" || reloaded.Decision.Assignments[0].NodeID == "mutated-node" || reloaded.Leases[0].UnitID == "mutated-lease-unit" {
		t.Fatalf("run read result shares mutable storage: %#v", reloaded)
	}
}

func insertCrossEpochRunProjection(t *testing.T, owner *Owner, oldEpoch, newEpoch heteroci.RoutingOwnerEpoch, runID string) {
	t.Helper()
	var (
		manifestBytes                                                 []byte
		unitsBytes                                                    []byte
		decisionBytes                                                 []byte
		storedManifestDigest, storedUnitsDigest, storedDecisionDigest string
		ttlMillis, released, reservedRevision, lastRevision           int64
	)
	if err := owner.db.QueryRowContext(context.Background(), "SELECT manifest_json, manifest_digest, units_json, units_digest, heartbeat_ttl_millis, decision_json, decision_digest, released, reserved_revision, last_revision FROM heteroci_routing_runs WHERE scope = ? AND owner_epoch = ? AND run_id = ?", owner.scope, oldEpoch, runID).Scan(&manifestBytes, &storedManifestDigest, &unitsBytes, &storedUnitsDigest, &ttlMillis, &decisionBytes, &storedDecisionDigest, &released, &reservedRevision, &lastRevision); err != nil {
		t.Fatalf("read run projection for duplicate: %v", err)
	}
	if _, err := owner.db.ExecContext(context.Background(), "INSERT INTO heteroci_routing_runs(scope, owner_epoch, run_id, manifest_json, manifest_digest, units_json, units_digest, heartbeat_ttl_millis, decision_json, decision_digest, released, reserved_revision, last_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", owner.scope, newEpoch, runID, manifestBytes, storedManifestDigest, unitsBytes, storedUnitsDigest, ttlMillis, decisionBytes, storedDecisionDigest, released, reservedRevision, lastRevision); err != nil {
		t.Fatalf("insert cross-epoch run projection: %v", err)
	}
	cloneCrossEpochLeases(t, owner, oldEpoch, newEpoch, runID)
	cloneCrossEpochReservations(t, owner, oldEpoch, newEpoch, runID)
}

func cloneCrossEpochLeases(t *testing.T, owner *Owner, oldEpoch, newEpoch heteroci.RoutingOwnerEpoch, runID string) {
	t.Helper()
	type leaseRow struct {
		unitID                      string
		leaseBytes                  []byte
		state                       heteroci.RoutingLeaseState
		reserved, started, finished int64
		revision                    int64
	}
	rows, err := owner.db.QueryContext(context.Background(), "SELECT unit_id, lease_json, state, reserved_at_unix_milli, started_at_unix_milli, finished_at_unix_milli, last_revision FROM heteroci_routing_leases WHERE scope = ? AND owner_epoch = ? AND run_id = ?", owner.scope, oldEpoch, runID)
	if err != nil {
		t.Fatalf("read leases for duplicate: %v", err)
	}
	var leases []leaseRow
	for rows.Next() {
		var row leaseRow
		if err := rows.Scan(&row.unitID, &row.leaseBytes, &row.state, &row.reserved, &row.started, &row.finished, &row.revision); err != nil {
			t.Fatalf("scan lease for duplicate: %v", err)
		}
		row.leaseBytes = append([]byte(nil), row.leaseBytes...)
		leases = append(leases, row)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close leases for duplicate: %v", err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate leases for duplicate: %v", err)
	}
	for _, row := range leases {
		if _, err := owner.db.ExecContext(context.Background(), "INSERT INTO heteroci_routing_leases(scope, owner_epoch, run_id, unit_id, lease_json, state, reserved_at_unix_milli, started_at_unix_milli, finished_at_unix_milli, last_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", owner.scope, newEpoch, runID, row.unitID, row.leaseBytes, row.state, row.reserved, row.started, row.finished, row.revision); err != nil {
			t.Fatalf("insert lease duplicate: %v", err)
		}
	}
}

func cloneCrossEpochReservations(t *testing.T, owner *Owner, oldEpoch, newEpoch heteroci.RoutingOwnerEpoch, runID string) {
	t.Helper()
	type reservationRow struct {
		nodeID, probeIdentity, probeBootID    string
		cpuMilli, memoryBytes, maxConcurrency int64
		released, revision                    int64
	}
	rows, err := owner.db.QueryContext(context.Background(), "SELECT node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency, released, last_revision FROM heteroci_routing_reservations WHERE scope = ? AND owner_epoch = ? AND run_id = ?", owner.scope, oldEpoch, runID)
	if err != nil {
		t.Fatalf("read reservations for duplicate: %v", err)
	}
	var reservations []reservationRow
	for rows.Next() {
		var row reservationRow
		if err := rows.Scan(&row.nodeID, &row.probeIdentity, &row.probeBootID, &row.cpuMilli, &row.memoryBytes, &row.maxConcurrency, &row.released, &row.revision); err != nil {
			t.Fatalf("scan reservation for duplicate: %v", err)
		}
		reservations = append(reservations, row)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close reservations for duplicate: %v", err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate reservations for duplicate: %v", err)
	}
	for _, row := range reservations {
		if _, err := owner.db.ExecContext(context.Background(), "INSERT INTO heteroci_routing_reservations(scope, owner_epoch, run_id, node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency, released, last_revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", owner.scope, newEpoch, runID, row.nodeID, row.probeIdentity, row.probeBootID, row.cpuMilli, row.memoryBytes, row.maxConcurrency, row.released, row.revision); err != nil {
			t.Fatalf("insert reservation duplicate: %v", err)
		}
	}
}

type readStateMutationFingerprint struct {
	headRevision   int64
	headJournal    string
	runRevision    int64
	runReleased    int64
	leaseState     string
	leaseRevision  int64
	operationCount int
}

func readReadStateMutationFingerprint(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID string, operationRevision heteroci.RoutingOwnerRevision) readStateMutationFingerprint {
	t.Helper()
	var state readStateMutationFingerprint
	if err := owner.db.QueryRowContext(context.Background(), "SELECT revision, journal_digest FROM heteroci_owner_heads WHERE scope = ?", fence.Scope).Scan(&state.headRevision, &state.headJournal); err != nil {
		t.Fatalf("read head fingerprint: %v", err)
	}
	if err := owner.db.QueryRowContext(context.Background(), "SELECT last_revision, released FROM heteroci_routing_runs WHERE scope = ? AND owner_epoch = ? AND run_id = ?", fence.Scope, fence.OwnerEpoch, runID).Scan(&state.runRevision, &state.runReleased); err != nil {
		t.Fatalf("read run fingerprint: %v", err)
	}
	if err := owner.db.QueryRowContext(context.Background(), "SELECT state, last_revision FROM heteroci_routing_leases WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ?", fence.Scope, fence.OwnerEpoch, runID, "unit-a").Scan(&state.leaseState, &state.leaseRevision); err != nil {
		t.Fatalf("read lease fingerprint: %v", err)
	}
	if err := owner.db.QueryRowContext(context.Background(), "SELECT count(*) FROM heteroci_routing_operations WHERE scope = ? AND owner_epoch = ? AND revision <= ?", fence.Scope, fence.OwnerEpoch, operationRevision).Scan(&state.operationCount); err != nil {
		t.Fatalf("read operation fingerprint: %v", err)
	}
	return state
}

func tamperReadStateField(t *testing.T, owner *Owner, query string, args ...any) {
	t.Helper()
	result, err := owner.db.ExecContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("tamper read state: %v", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		t.Fatalf("tamper read state rows = %d, err=%v", rows, err)
	}
}

func assertReadStateErrorSafe(t *testing.T, err error) {
	t.Helper()
	if strings.Contains(err.Error(), readStateSecretURL) || strings.Contains(err.Error(), readStateSecretValue) || strings.Contains(err.Error(), "node-a.invalid") {
		t.Fatalf("read error leaked endpoint or payload: %v", err)
	}
}
