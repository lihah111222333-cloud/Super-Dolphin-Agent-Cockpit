package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func openIdentityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return openIdentityTestDBAtPath(t, filepath.Join(t.TempDir(), "identity.sqlite"))
}

func openIdentityTestDBAtPath(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatalf("sqliteruntime.Open(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close identity database: %v", err)
		}
	})
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatalf("RunEmbeddedMigrations() error = %v", err)
	}
	return db
}

func mustIdentityStore(t *testing.T, db *sql.DB) *Store {
	t.Helper()
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func seedCommandIntent(t *testing.T, db *sql.DB, commandID string) orchestrationcommand.Record {
	t.Helper()
	store, err := orchestrationcommand.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(command) error = %v", err)
	}
	record, err := store.Reserve(t.Context(), orchestrationcommand.Intent{CommandID: commandID, CommandKind: "launch_agent",
		CallerPrincipalRef: "principal/" + commandID, ScopeRef: "scope/" + commandID,
		IdempotencyNamespace: "identity-command/" + commandID, IdempotencyKeyDigest: testDigest('1'), RequestDigest: testDigest('2'),
		DigestKeyID: "test-key", RequestContentRef: "content/" + commandID, SourceFenceRef: "source/" + commandID, TargetFenceRef: "target/" + commandID})
	if err != nil {
		t.Fatalf("seed command %q error = %v", commandID, err)
	}
	return record
}

func commandReservationRequest(reservationID, commandID, agentID string, generation int64) ReservationRequest {
	return ReservationRequest{ReservationID: reservationID, CommandID: commandID, AgentID: agentID, IdentityKind: identityKindNativeSubagent,
		SourceKind: sourceKindFirstLaunch, CallerPrincipalRef: "principal/" + commandID, ScopeRef: "scope/" + commandID,
		IdempotencyNamespace: "identity-reservation/" + commandID, IdempotencyKeyDigest: testDigest('3'), RequestDigest: testDigest('4'),
		SourceFenceRef: "source/" + commandID, TargetFenceRef: "target/" + commandID,
		ReservationGeneration: generation}
}

func observationReservationRequest(reservationID, agentID string, generation int64) ReservationRequest {
	return ReservationRequest{ReservationID: reservationID, AgentID: agentID, IdentityKind: identityKindA2ARemoteAgent,
		SourceKind: sourceKindA2AObservation, CallerPrincipalRef: "principal/" + agentID, ScopeRef: "scope/" + agentID,
		IdempotencyNamespace: "identity-observation/" + agentID, IdempotencyKeyDigest: testDigest('5'), RequestDigest: testDigest('6'),
		SourceFenceRef: "source-fence/" + agentID, TargetFenceRef: "target-fence/" + agentID,
		ReservationGeneration: generation}
}

func createIdentityChain(t *testing.T, db *sql.DB, store *Store, suffix string, generation int64) identityChain {
	t.Helper()
	return createIdentityChainForAgent(t, db, store, suffix, "agent-"+suffix, generation)
}

func createIdentityChainForAgent(t *testing.T, db *sql.DB, store *Store, suffix, agentID string, generation int64) identityChain {
	t.Helper()
	commandID := "cmd-" + suffix
	seedCommandIntent(t, db, commandID)
	reservationRequest := commandReservationRequest("reservation-"+suffix, commandID, agentID, generation)
	reservation, err := store.Reserve(t.Context(), reservationRequest)
	if err != nil {
		t.Fatalf("Reserve(%s) error = %v", suffix, err)
	}
	incarnationRequest := incarnationRequestFor(reservation, "incarnation-"+suffix, "external-"+suffix, "epoch-"+suffix)
	incarnation, err := store.CreateIncarnation(t.Context(), incarnationRequest)
	if err != nil {
		t.Fatalf("CreateIncarnation(%s) error = %v", suffix, err)
	}
	prep := BindingPreparationRequest{PreparationID: "preparation-" + suffix, ReservationID: reservation.ReservationID,
		IncarnationID: incarnation.IncarnationID, AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		SourceFenceRef: reservation.SourceFenceRef, BindingDigest: testDigest('7'), PreparationGeneration: generation,
		ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: generation}
	return identityChain{reservation: reservation, incarnation: incarnation, prep: prep}
}

func incarnationRequestFor(reservation Reservation, incarnationID, externalRef, externalEpoch string) IncarnationRequest {
	return IncarnationRequest{IncarnationID: incarnationID, ReservationID: reservation.ReservationID, AgentID: reservation.AgentID,
		IdentityKind: reservation.IdentityKind, ExternalIdentityRef: externalRef, ExternalEpoch: externalEpoch,
		SourceFenceRef: reservation.SourceFenceRef, IdentityDigest: testDigest('8'), IncarnationGeneration: reservation.ReservationGeneration}
}

func finalizeRequestFor(chain identityChain, receipt string) FinalizeBindingRequest {
	return FinalizeBindingRequest{PreparationID: chain.prep.PreparationID, AgentID: chain.reservation.AgentID,
		ExpectedPreparationGeneration: chain.prep.PreparationGeneration, ExpectedBindingVersion: chain.prep.ExpectedBindingVersion,
		IncarnationID: chain.incarnation.IncarnationID, ExternalIdentityRef: chain.incarnation.ExternalIdentityRef,
		IdentityKind: chain.incarnation.IdentityKind, SourceFenceRef: chain.incarnation.SourceFenceRef,
		ActiveProcessReceiptRef: receipt, BindingDigest: chain.prep.BindingDigest}
}

func assertActiveBinding(t *testing.T, binding CurrentBinding, chain identityChain, receipt string, version int64) {
	t.Helper()
	expected := CurrentBinding{AgentID: chain.reservation.AgentID, BindingVersion: version,
		ReservationID: chain.reservation.ReservationID, IncarnationID: chain.incarnation.IncarnationID,
		IdentityKind: chain.incarnation.IdentityKind, ReservationGeneration: chain.reservation.ReservationGeneration,
		IncarnationGeneration: chain.incarnation.IncarnationGeneration, ExternalIdentityRef: chain.incarnation.ExternalIdentityRef,
		SourceFenceRef: chain.incarnation.SourceFenceRef, ActiveProcessReceiptRef: receipt,
		BindingDigest: chain.prep.BindingDigest, State: "active", SchemaVersion: 1, UpdatedAt: binding.UpdatedAt}
	if binding.UpdatedAt == "" || !reflect.DeepEqual(binding, expected) {
		t.Fatalf("active binding = %#v, want complete active receipt/fence", binding)
	}
}

func reserveConcurrently(start <-chan struct{}, results chan<- reservationResult, store *Store, request ReservationRequest) {
	<-start
	reservation, err := store.Reserve(context.Background(), request)
	results <- reservationResult{reservation: reservation, err: err}
}

func finalizeConcurrently(start <-chan struct{}, results chan<- bindingResult, store *Store, request FinalizeBindingRequest) {
	<-start
	binding, err := store.FinalizeBinding(context.Background(), request)
	results <- bindingResult{binding: binding, err: err}
}

func expectIdentityError(t *testing.T, actual, want error) {
	t.Helper()
	if !errors.Is(actual, want) {
		t.Fatalf("error = %v, want %v", actual, want)
	}
}

func assertIdentityRowState(t *testing.T, db *sql.DB, table, column, want string) {
	t.Helper()
	var got string
	query := fmt.Sprintf("SELECT %s FROM %s ORDER BY rowid DESC LIMIT 1", column, table)
	if err := db.QueryRowContext(t.Context(), query).Scan(&got); err != nil {
		t.Fatalf("read %s.%s: %v", table, column, err)
	}
	if got != want {
		t.Fatalf("%s.%s = %q, want %q", table, column, got, want)
	}
}

func countIdentityRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return count
}

func legacyIdentityRowCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, table := range []string{"agent_codex_binding", "agent_provider_binding", "agent_threads"} {
		counts[table] = countIdentityRows(t, db, table)
	}
	return counts
}

func assertIdentityJSONFields(t *testing.T, value any) {
	t.Helper()
	typeOfValue := reflect.TypeOf(value)
	want := make(map[string]struct{}, typeOfValue.NumField())
	for index := 0; index < typeOfValue.NumField(); index++ {
		field := typeOfValue.Field(index)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("field %s.%s lacks a JSON name", typeOfValue.Name(), field.Name)
		}
		if _, exists := want[name]; exists {
			t.Fatalf("field %s has duplicate JSON name %q", typeOfValue.Name(), name)
		}
		want[name] = struct{}{}
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%s) error = %v", typeOfValue.Name(), err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s) error = %v", typeOfValue.Name(), err)
	}
	if len(got) != len(want) {
		t.Fatalf("JSON fields for %s = %d, want %d: %s", typeOfValue.Name(), len(got), len(want), payload)
	}
	for name := range want {
		if _, exists := got[name]; !exists {
			t.Fatalf("JSON field %q missing from %s: %s", name, typeOfValue.Name(), payload)
		}
	}
}

func validReservationRow(commandID *string) storedb.OrchestrationIdentityReservation {
	return storedb.OrchestrationIdentityReservation{ReservationID: "reservation-mapper", CommandID: commandID, AgentID: "agent-mapper",
		IdentityKind: identityKindNativeSubagent, SourceKind: sourceKindFirstLaunch, CallerPrincipalRef: "principal-mapper", ScopeRef: "scope-mapper",
		ParentAgentID: "", IdempotencyNamespace: "namespace-mapper", IdempotencyKeyDigest: testDigest('1'), RequestDigest: testDigest('2'),
		SourceFenceRef: "source-fence-mapper", TargetFenceRef: "target-fence-mapper", ExpectedBindingVersion: 0, ReservationGeneration: 1,
		State: "reserved", SchemaVersion: 1, CreatedAt: identityTestTimestamp, UpdatedAt: identityTestTimestamp}
}

func validIncarnationRow() storedb.OrchestrationExecutionIncarnation {
	return storedb.OrchestrationExecutionIncarnation{IncarnationID: "incarnation-mapper", ReservationID: "reservation-mapper", AgentID: "agent-mapper",
		IdentityKind: identityKindNativeSubagent, ExternalIdentityRef: "external-mapper", ExternalEpoch: "epoch-mapper", SourceFenceRef: "source-fence-mapper",
		IdentityDigest: testDigest('3'), IncarnationGeneration: 1, SchemaVersion: 1, CreatedAt: identityTestTimestamp}
}

func validPreparationRow() storedb.OrchestrationBindingPreparation {
	return storedb.OrchestrationBindingPreparation{PreparationID: "preparation-mapper", ReservationID: "reservation-mapper", IncarnationID: "incarnation-mapper", AgentID: "agent-mapper",
		IdentityKind: identityKindNativeSubagent, PreparationGeneration: 1, ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: 1,
		BindingDigest: testDigest('4'), SourceFenceRef: "source-fence-mapper", ActiveProcessReceiptRef: "", State: "prepared_non_authorizing",
		SchemaVersion: 1, CreatedAt: identityTestTimestamp, ResolvedAt: ""}
}

func validBindingRow() storedb.OrchestrationCurrentBinding {
	return storedb.OrchestrationCurrentBinding{AgentID: "agent-mapper", BindingVersion: 1, ReservationID: "reservation-mapper", IncarnationID: "incarnation-mapper",
		IdentityKind: identityKindNativeSubagent, ReservationGeneration: 1, IncarnationGeneration: 1, ExternalIdentityRef: "external-mapper",
		SourceFenceRef: "source-fence-mapper", ActiveProcessReceiptRef: "receipt-mapper", BindingDigest: testDigest('5'), State: "active", SchemaVersion: 1,
		UpdatedAt: identityTestTimestamp}
}

func expectInvalidIdentity(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("error = %v, want ErrInvalidIdentity", err)
	}
}

func testDigest(char byte) string {
	return strings.Repeat(string(char), 64)
}
