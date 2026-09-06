package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func TestStorePlanReplayAndTargetCAS(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	clock := fixture.now
	store, request, first := mustPlanWaitOperation(t, fixture, &clock)
	assertPlanWaitOperationReplay(t, store, request, first, &clock)
	advanced := appendPlanWaitOperationTarget(t, store, request, first, &clock)
	assertFencedPlanWaitOperation(t, store, request, first, advanced, &clock)
	assertExpiredPlanWaitOperation(t, store, request, first, &clock)
}

func TestAppendTargetReplayRequiresExactPredecessorMetadata(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	store, err := NewStore(fixture.db, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{
		OperationKey: "wait-operation-replay-predecessor", OperationDigest: strings.Repeat("1", 64), CallerPrincipalRef: "principal", ScopeRef: "scope",
		Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second), Targets: []TargetPlan{{
			Key: "target-1", Target: fixture.target,
			Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}},
		}},
	}
	planned, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	transition := TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: "target-1",
		ExpectedVersion: 1, ExpectedPhase: "planned", ExpectedRecordDigest: planned.Targets[0].RecordDigest,
		NextPhase: "grant_pending", GrantRevision: 0, GrantStatus: "planned", WaitRevision: 0, WaitStatus: "absent",
		GrantCleanupState: "none", WaitCleanupState: "none",
	}
	advanced, err := store.AppendTarget(t.Context(), transition)
	if err != nil {
		t.Fatal(err)
	}
	replay := transition
	replay.RecordDigest = advanced.RecordDigest
	replay.ExpectedRecordDigest = strings.Repeat("f", 64)
	if _, err := store.AppendTarget(t.Context(), replay); !errors.Is(err, ErrTargetConflict) {
		t.Fatalf("replay with forged predecessor metadata = %v, want ErrTargetConflict", err)
	}
}

func TestStoreRejectsSchemaBelow136(t *testing.T) {
	db := openWaitOperationDB(t)
	store, err := NewStore(db, func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 136`); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckSchema(t.Context()); !errors.Is(err, ErrSchemaUnavailable) {
		t.Fatalf("CheckSchema() = %v, want ErrSchemaUnavailable", err)
	}
}

func TestStoreMappingRejectsTargetAndTargetSetDigestDrift(t *testing.T) {
	fixture := newWaitOperationFixture(t)
	store, err := NewStore(fixture.db, func() time.Time { return fixture.now })
	if err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{
		OperationKey: "wait-operation-digest", OperationDigest: strings.Repeat("c", 64), CallerPrincipalRef: "principal", ScopeRef: "scope",
		Source: fixture.source, DeadlineAt: fixture.now.Add(30 * time.Second), Targets: []TargetPlan{{
			Key: "target-1", Target: fixture.target,
			Selector: accesswait.EventSelector{Version: 1, SourceID: "task/src", SourceEpoch: "inc-src", Lane: eventstore.LaneControl, TargetAgentID: "dst", EventTypes: []string{"output"}},
		}},
	}
	operation, err := store.Plan(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	row, err := store.queries.GetOrchestrationWaitTargetHead(t.Context(), storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	row.TargetDigest = strings.Repeat("f", 64)
	if _, err := targetFromRow(row); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("targetFromRow(forged target digest) = %v, want ErrInvalidOperation", err)
	}
	operation.TargetSetDigest = strings.Repeat("f", 64)
	if err := store.populateHeads(t.Context(), store.queries, &operation); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("populateHeads(forged target set digest) = %v, want ErrInvalidOperation", err)
	}
}

type waitOperationFixture struct {
	db     *sql.DB
	now    time.Time
	source accesswait.FenceSnapshot
	target accesswait.FenceSnapshot
}

func newWaitOperationFixture(t *testing.T) waitOperationFixture {
	t.Helper()
	db := openWaitOperationDB(t)
	seedWaitOperationIdentity(t, db, "src", "res-src", "inc-src")
	seedWaitOperationIdentity(t, db, "dst", "res-dst", "inc-dst")
	digest := strings.Repeat("a", 64)
	return waitOperationFixture{db: db, now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		source: accesswait.FenceSnapshot{AgentID: "src", ReservationID: "res-src", ReservationGeneration: 1, IncarnationID: "inc-src", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest, SourceFenceRef: "source-fence"},
		target: accesswait.FenceSnapshot{AgentID: "dst", ReservationID: "res-dst", ReservationGeneration: 1, IncarnationID: "inc-dst", IncarnationGeneration: 1, BindingVersion: 1, BindingDigest: digest, SourceFenceRef: "target-fence"}}
}

func openWaitOperationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "wait-operation.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqliteruntime.RunEmbeddedMigrations(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedWaitOperationIdentity(t *testing.T, db *sql.DB, agentID, reservationID, incarnationID string) {
	t.Helper()
	digest := strings.Repeat("a", 64)
	fenceRef := "source-fence"
	if agentID == "dst" {
		fenceRef = "target-fence"
	}
	if _, err := db.Exec(`INSERT INTO orchestration_identity_reservations (
reservation_id, agent_id, identity_kind, source_kind, caller_principal_ref, scope_ref,
parent_agent_id, idempotency_namespace, idempotency_key_digest, request_digest,
source_fence_ref, target_fence_ref, expected_binding_version, reservation_generation, state, schema_version
) VALUES (?, ?, 'native_subagent', 'provider_discovery', 'principal', 'scope', '', ?, ?, ?, 'source-fence', 'target-fence', 1, 1, 'activated', 1)`,
		reservationID, agentID, "wait-operation/identity/"+agentID, digest, digest, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO orchestration_execution_incarnations (
incarnation_id, reservation_id, agent_id, identity_kind, external_identity_ref,
external_epoch, source_fence_ref, identity_digest, incarnation_generation, schema_version
) VALUES (?, ?, ?, 'native_subagent', ?, 'epoch-1', ?, ?, 1, 1)`,
		incarnationID, reservationID, agentID, "external-"+agentID, fenceRef, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO orchestration_current_bindings (
agent_id, binding_version, reservation_id, incarnation_id, identity_kind,
reservation_generation, incarnation_generation, external_identity_ref,
source_fence_ref, active_process_receipt_ref, binding_digest, state, schema_version
) VALUES (?, 1, ?, ?, 'native_subagent', 1, 1, ?, ?, ?, ?, 'active', 1)`,
		agentID, reservationID, incarnationID, "external-"+agentID, fenceRef, "receipt-"+agentID, digest); err != nil {
		t.Fatal(err)
	}
}
