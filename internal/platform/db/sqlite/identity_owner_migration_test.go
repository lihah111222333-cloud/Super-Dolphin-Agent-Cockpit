package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

const identityReservationInsert = `
	INSERT INTO orchestration_identity_reservations (
		reservation_id, command_id, agent_id, identity_kind, source_kind,
		caller_principal_ref, scope_ref, parent_agent_id, idempotency_namespace,
		idempotency_key_digest, request_digest, source_fence_ref, target_fence_ref,
		expected_binding_version, reservation_generation, state, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const identityIncarnationInsert = `
	INSERT INTO orchestration_execution_incarnations (
		incarnation_id, reservation_id, agent_id, identity_kind,
		external_identity_ref, external_epoch, source_fence_ref, identity_digest,
		incarnation_generation, schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const identityPreparationInsert = `
	INSERT INTO orchestration_binding_preparations (
		preparation_id, reservation_id, incarnation_id, agent_id, identity_kind,
		preparation_generation, expected_binding_version, expected_incarnation_generation,
		binding_digest, source_fence_ref, active_process_receipt_ref, state,
		schema_version, resolved_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const identityCurrentBindingInsert = `
	INSERT INTO orchestration_current_bindings (
		agent_id, binding_version, reservation_id, incarnation_id, identity_kind,
		reservation_generation, incarnation_generation, external_identity_ref,
		source_fence_ref, active_process_receipt_ref, binding_digest, state,
		schema_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

type identityIndexContract struct {
	name   string
	unique bool
}

type identityTableContract struct {
	name       string
	primaryKey []string
	columns    []string
	notNull    []string
	checks     []string
	fks        []foreignKeyContract
	indexes    []identityIndexContract
}

// TestIdentityOwnerMigrationContract 聚合身份 owner 四表的 schema、约束和生命周期合同。
func TestIdentityOwnerMigrationContract(t *testing.T) {
	db := openIdentityOwnerMigrationDB(t)
	assertIdentityOwnerSchema(t, db)
	seedIdentityOwner(t, db)
	t.Run("unique_and_monotonic_generations", func(t *testing.T) {
		assertIdentityOwnerUniquenessAndGenerations(t, db)
	})
	t.Run("immutable_metadata", func(t *testing.T) {
		assertIdentityOwnerImmutability(t, db)
	})
	t.Run("preparation_finalize", func(t *testing.T) {
		assertIdentityOwnerFinalizePreparation(t, db)
	})
	t.Run("preparation_abort", func(t *testing.T) {
		assertIdentityOwnerAbortPreparation(t, db)
	})
	t.Run("current_binding_cas", func(t *testing.T) {
		assertIdentityOwnerCurrentBindingCAS(t, db)
	})
	t.Run("current_binding_append_history", func(t *testing.T) {
		assertIdentityOwnerCurrentBindingHistory(t, db)
	})
}

func openIdentityOwnerMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, "128_orchestration_identity_owner.sql", 1)
	return db
}

func assertIdentityOwnerSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	contracts := []identityTableContract{
		identityReservationTableContract(), identityIncarnationTableContract(),
		identityPreparationTableContract(), identityCurrentBindingTableContract(),
	}
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			assertPrimaryKey(t, db, contract.name, contract.primaryKey)
			if columns := tableColumns(t, db, contract.name); !slicesEqual(columns, contract.columns) {
				t.Fatalf("%s columns = %v, want %v", contract.name, columns, contract.columns)
			}
			notNull := contract.notNull
			if notNull == nil {
				notNull = contract.columns
			}
			assertNotNullColumns(t, db, contract.name, notNull)
			assertTableSQLContains(t, db, contract.name, contract.checks)
			assertForeignKeys(t, db, contract.name, contract.fks)
			for _, index := range contract.indexes {
				assertIndex(t, db, contract.name, index.name, index.unique, "")
			}
		})
	}
}

func identityReservationTableContract() identityTableContract {
	return identityTableContract{
		name:       "orchestration_identity_reservations",
		primaryKey: []string{"reservation_id"},
		columns: []string{
			"reservation_id", "command_id", "agent_id", "identity_kind", "source_kind",
			"caller_principal_ref", "scope_ref", "parent_agent_id", "idempotency_namespace",
			"idempotency_key_digest", "request_digest", "source_fence_ref", "target_fence_ref",
			"expected_binding_version", "reservation_generation", "state", "schema_version",
			"created_at", "updated_at",
		},
		notNull: []string{
			"reservation_id", "agent_id", "identity_kind", "source_kind", "caller_principal_ref",
			"scope_ref", "parent_agent_id", "idempotency_namespace", "idempotency_key_digest",
			"request_digest", "source_fence_ref", "target_fence_ref", "expected_binding_version",
			"reservation_generation", "state", "schema_version", "created_at", "updated_at",
		},
		checks: []string{
			"STRICT", "UNIQUE(idempotency_namespace, idempotency_key_digest)",
			"UNIQUE(agent_id, reservation_generation)", "UNIQUE(command_id)",
			"UNIQUE(reservation_id, agent_id, identity_kind)",
			"REFERENCES orchestration_command_intents(command_id) ON DELETE RESTRICT",
			"source_kind IN ('first_launch', 'recover', 'replace')",
			"source_kind IN ('provider_discovery', 'a2a_observation')",
		},
		fks:     []foreignKeyContract{{Column: "command_id", Table: "orchestration_command_intents"}},
		indexes: []identityIndexContract{{name: "idx_orchestration_identity_reservations_agent"}},
	}
}

func identityIncarnationTableContract() identityTableContract {
	return identityTableContract{
		name:       "orchestration_execution_incarnations",
		primaryKey: []string{"incarnation_id"},
		columns: []string{
			"incarnation_id", "reservation_id", "agent_id", "identity_kind",
			"external_identity_ref", "external_epoch", "source_fence_ref", "identity_digest",
			"incarnation_generation", "schema_version", "created_at",
		},
		checks: []string{
			"STRICT", "UNIQUE(agent_id, incarnation_generation)",
			"UNIQUE(identity_kind, external_identity_ref, external_epoch)",
			"UNIQUE(incarnation_id, reservation_id, agent_id, identity_kind)",
			"FOREIGN KEY(reservation_id, agent_id, identity_kind) REFERENCES orchestration_identity_reservations(reservation_id, agent_id, identity_kind) ON DELETE RESTRICT",
		},
		fks:     []foreignKeyContract{{Column: "reservation_id", Table: "orchestration_identity_reservations"}},
		indexes: []identityIndexContract{{name: "idx_orchestration_execution_incarnations_agent"}},
	}
}

func identityPreparationTableContract() identityTableContract {
	return identityTableContract{
		name:       "orchestration_binding_preparations",
		primaryKey: []string{"preparation_id"},
		columns: []string{
			"preparation_id", "reservation_id", "incarnation_id", "agent_id", "identity_kind",
			"preparation_generation", "expected_binding_version", "expected_incarnation_generation",
			"binding_digest", "source_fence_ref", "active_process_receipt_ref", "state",
			"schema_version", "created_at", "resolved_at",
		},
		checks: []string{
			"STRICT", "UNIQUE(reservation_id, preparation_generation)",
			"UNIQUE(incarnation_id, preparation_generation)",
			"FOREIGN KEY(reservation_id, agent_id, identity_kind) REFERENCES orchestration_identity_reservations(reservation_id, agent_id, identity_kind) ON DELETE RESTRICT",
			"FOREIGN KEY(incarnation_id, reservation_id, agent_id, identity_kind) REFERENCES orchestration_execution_incarnations(incarnation_id, reservation_id, agent_id, identity_kind) ON DELETE RESTRICT",
		},
		fks: []foreignKeyContract{
			{Column: "reservation_id", Table: "orchestration_identity_reservations"},
			{Column: "incarnation_id", Table: "orchestration_execution_incarnations"},
		},
		indexes: []identityIndexContract{{name: "idx_orchestration_binding_preparations_agent"}},
	}
}

func identityCurrentBindingTableContract() identityTableContract {
	return identityTableContract{
		name:       "orchestration_current_bindings",
		primaryKey: []string{"agent_id", "binding_version"},
		columns: []string{
			"agent_id", "binding_version", "reservation_id", "incarnation_id", "identity_kind",
			"reservation_generation", "incarnation_generation", "external_identity_ref",
			"source_fence_ref", "active_process_receipt_ref", "binding_digest", "state",
			"schema_version", "updated_at",
		},
		checks: []string{
			"STRICT", "PRIMARY KEY(agent_id, binding_version)",
			"FOREIGN KEY(reservation_id, agent_id, identity_kind) REFERENCES orchestration_identity_reservations(reservation_id, agent_id, identity_kind) ON DELETE RESTRICT",
			"FOREIGN KEY(incarnation_id, reservation_id, agent_id, identity_kind) REFERENCES orchestration_execution_incarnations(incarnation_id, reservation_id, agent_id, identity_kind) ON DELETE RESTRICT",
			"CHECK(state = 'revoked' OR binding_version > 0)",
		},
		fks: []foreignKeyContract{
			{Column: "reservation_id", Table: "orchestration_identity_reservations"},
			{Column: "incarnation_id", Table: "orchestration_execution_incarnations"},
		},
		indexes: []identityIndexContract{{name: "idx_orchestration_current_bindings_latest"}},
	}
}

func seedIdentityOwner(t *testing.T, db *sql.DB) {
	t.Helper()
	digest := strings.Repeat
	intentA := validCommandIntentFixture()
	intentA.CommandID, intentA.IdempotencyNamespace = "identity-command-a", "identity/command-a"
	intentA.IdempotencyKeyDigest, intentA.RequestDigest = digest("1", 64), digest("2", 64)
	insertCommandIntent(t, db, intentA)
	intentB := intentA
	intentB.CommandID, intentB.IdempotencyNamespace = "identity-command-b", "identity/command-b"
	intentB.IdempotencyKeyDigest, intentB.RequestDigest = digest("3", 64), digest("4", 64)
	insertCommandIntent(t, db, intentB)

	if err := identityExec(t, db, identityReservationInsert,
		"reservation-a", intentA.CommandID, "agent-a", "provider_session", "first_launch",
		"principal-a", "scope-a", "", "identity/agent-a", digest("a", 64), digest("b", 64),
		"source-fence-a", "target-fence-a", 0, 1, "reserved", 1); err != nil {
		t.Fatalf("insert identity reservation A: %v", err)
	}
	if err := identityExec(t, db, identityReservationInsert,
		"reservation-b", intentB.CommandID, "agent-b", "native_subagent", "first_launch",
		"principal-b", "scope-b", "", "identity/agent-b", digest("c", 64), digest("d", 64),
		"source-fence-b", "target-fence-b", 0, 1, "reserved", 1); err != nil {
		t.Fatalf("insert identity reservation B: %v", err)
	}
	if err := identityExec(t, db, identityIncarnationInsert,
		"incarnation-a", "reservation-a", "agent-a", "provider_session", "provider-session-a", "epoch-a",
		"source-fence-a", digest("e", 64), 1, 1); err != nil {
		t.Fatalf("insert execution incarnation A: %v", err)
	}
	if err := identityExec(t, db, identityIncarnationInsert,
		"incarnation-b", "reservation-b", "agent-b", "native_subagent", "native-subagent-b", "epoch-b",
		"source-fence-b", digest("f", 64), 1, 1); err != nil {
		t.Fatalf("insert execution incarnation B: %v", err)
	}
	if err := identityExec(t, db, identityPreparationInsert,
		"preparation-a", "reservation-a", "incarnation-a", "agent-a", "provider_session", 1, 0, 1,
		digest("6", 64), "source-fence-a", "", "prepared_non_authorizing", 1, ""); err != nil {
		t.Fatalf("insert binding preparation A: %v", err)
	}
	if err := identityExec(t, db, identityPreparationInsert,
		"preparation-b", "reservation-b", "incarnation-b", "agent-b", "native_subagent", 1, 0, 1,
		digest("7", 64), "source-fence-b", "", "prepared_non_authorizing", 1, ""); err != nil {
		t.Fatalf("insert binding preparation B: %v", err)
	}
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-a", 1, "reservation-a", "incarnation-a", "provider_session", 1, 1,
		"provider-session-a", "source-fence-a", "active-process-a", digest("6", 64), "active", 1); err != nil {
		t.Fatalf("insert current binding A: %v", err)
	}
}

func identityExec(t *testing.T, db *sql.DB, statement string, args ...any) error {
	t.Helper()
	_, err := db.Exec(statement, args...)
	return err
}

func assertIdentityOwnerUniquenessAndGenerations(t *testing.T, db *sql.DB) {
	t.Helper()
	digest := strings.Repeat
	duplicateIntent := validCommandIntentFixture()
	duplicateIntent.CommandID, duplicateIntent.IdempotencyNamespace = "identity-command-duplicate", "identity/command-duplicate"
	duplicateIntent.IdempotencyKeyDigest, duplicateIntent.RequestDigest = digest("8", 64), digest("9", 64)
	insertCommandIntent(t, db, duplicateIntent)
	if err := identityExec(t, db, identityReservationInsert,
		"reservation-duplicate-idempotency", duplicateIntent.CommandID, "agent-a", "provider_session", "first_launch",
		"principal-a", "scope-a", "", "identity/agent-a", digest("a", 64), digest("b", 64),
		"source-fence-a-duplicate", "target-fence-a-duplicate", 0, 2, "reserved", 1); err == nil {
		t.Fatal("duplicate identity idempotency namespace/key was accepted")
	}
	if err := identityExec(t, db, identityReservationInsert,
		"reservation-b-rollback", nil, "agent-b", "native_subagent", "provider_discovery",
		"principal-b", "scope-b", "", "identity/agent-b/rollback", digest("1", 64), digest("2", 64),
		"source-fence-b-rollback", "target-fence-b-rollback", 0, 1, "reserved", 1); err == nil || !strings.Contains(err.Error(), "must increase") {
		t.Fatalf("reservation generation rollback error = %v, want monotonic-generation rejection", err)
	}
	if err := identityExec(t, db, identityReservationInsert,
		"reservation-b-generation-2", nil, "agent-b", "native_subagent", "provider_discovery",
		"principal-b", "scope-b", "", "identity/agent-b/generation-2", digest("3", 64), digest("4", 64),
		"source-fence-b-generation-2", "target-fence-b-generation-2", 0, 2, "reserved", 1); err != nil {
		t.Fatalf("insert reservation B generation 2: %v", err)
	}
	if err := identityExec(t, db, identityIncarnationInsert,
		"incarnation-b-rollback", "reservation-b-generation-2", "agent-b", "native_subagent",
		"native-subagent-b-rollback", "epoch-b-rollback", "source-fence-b-generation-2", digest("5", 64), 1, 1); err == nil || !strings.Contains(err.Error(), "must increase") {
		t.Fatalf("incarnation generation rollback error = %v, want monotonic-generation rejection", err)
	}
	if err := identityExec(t, db, identityReservationInsert,
		"reservation-a-external-duplicate", nil, "agent-a", "provider_session", "provider_discovery",
		"principal-a", "scope-a", "", "identity/agent-a/external-duplicate", digest("6", 64), digest("7", 64),
		"source-fence-a-external-duplicate", "target-fence-a-external-duplicate", 0, 2, "reserved", 1); err != nil {
		t.Fatalf("insert reservation A for external identity duplicate: %v", err)
	}
	if err := identityExec(t, db, identityIncarnationInsert,
		"incarnation-a-external-duplicate", "reservation-a-external-duplicate", "agent-a", "provider_session",
		"provider-session-a", "epoch-a", "source-fence-a-external-duplicate", digest("8", 64), 2, 1); err == nil {
		t.Fatal("duplicate external identity/epoch was accepted")
	}
}

func assertIdentityOwnerImmutability(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, tc := range []struct {
		name  string
		query string
		args  []any
	}{
		{"reservation metadata update", `UPDATE orchestration_identity_reservations SET caller_principal_ref = 'forged' WHERE reservation_id = 'reservation-a'`, nil},
		{"reservation delete", `DELETE FROM orchestration_identity_reservations WHERE reservation_id = 'reservation-a'`, nil},
		{"incarnation metadata update", `UPDATE orchestration_execution_incarnations SET external_epoch = 'forged' WHERE incarnation_id = 'incarnation-a'`, nil},
		{"incarnation delete", `DELETE FROM orchestration_execution_incarnations WHERE incarnation_id = 'incarnation-a'`, nil},
		{"preparation metadata update", `UPDATE orchestration_binding_preparations SET binding_digest = ? WHERE preparation_id = 'preparation-a'`, []any{strings.Repeat("f", 64)}},
		{"preparation delete", `DELETE FROM orchestration_binding_preparations WHERE preparation_id = 'preparation-a'`, nil},
		{"current binding metadata update", `UPDATE orchestration_current_bindings SET external_identity_ref = 'forged' WHERE agent_id = 'agent-a'`, nil},
		{"current binding delete", `DELETE FROM orchestration_current_bindings WHERE agent_id = 'agent-a'`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := identityExec(t, db, tc.query, tc.args...); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}
}

func assertIdentityOwnerFinalizePreparation(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := identityExec(t, db, `UPDATE orchestration_binding_preparations
		SET state = 'finalized', active_process_receipt_ref = 'active-process-a', resolved_at = '2026-09-01T00:00:00Z'
		WHERE preparation_id = 'preparation-a'`); err != nil {
		t.Fatalf("finalize preparation A: %v", err)
	}
	var state, receipt, resolved, identityKind, bindingDigest, sourceFence string
	if err := db.QueryRow(`SELECT state, active_process_receipt_ref, resolved_at, identity_kind, binding_digest, source_fence_ref
		FROM orchestration_binding_preparations WHERE preparation_id = 'preparation-a'`).Scan(
		&state, &receipt, &resolved, &identityKind, &bindingDigest, &sourceFence); err != nil {
		t.Fatalf("read finalized preparation A: %v", err)
	}
	if state != "finalized" || receipt != "active-process-a" || resolved != "2026-09-01T00:00:00Z" ||
		identityKind != "provider_session" || bindingDigest != strings.Repeat("6", 64) || sourceFence != "source-fence-a" {
		t.Fatalf("finalized preparation A shape = state:%q receipt:%q resolved:%q kind:%q digest:%q fence:%q", state, receipt, resolved, identityKind, bindingDigest, sourceFence)
	}
}

func assertIdentityOwnerAbortPreparation(t *testing.T, db *sql.DB) {
	t.Helper()
	var state, receipt, resolved string
	if err := identityExec(t, db, `UPDATE orchestration_binding_preparations
		SET state = 'finalized', resolved_at = '2026-09-01T00:00:01Z' WHERE preparation_id = 'preparation-b'`); err == nil {
		t.Fatal("finalize without active process receipt was accepted")
	}
	if err := identityExec(t, db, `UPDATE orchestration_binding_preparations
		SET state = 'aborted', resolved_at = '2026-09-01T00:00:02Z' WHERE preparation_id = 'preparation-b'`); err != nil {
		t.Fatalf("abort preparation B: %v", err)
	}
	if err := db.QueryRow(`SELECT state, active_process_receipt_ref, resolved_at
		FROM orchestration_binding_preparations WHERE preparation_id = 'preparation-b'`).Scan(&state, &receipt, &resolved); err != nil {
		t.Fatalf("read aborted preparation B: %v", err)
	}
	if state != "aborted" || receipt != "" || resolved != "2026-09-01T00:00:02Z" {
		t.Fatalf("aborted preparation B shape = state:%q receipt:%q resolved:%q", state, receipt, resolved)
	}
}

func assertIdentityOwnerCurrentBindingCAS(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-c", 1, "reservation-b", "incarnation-b", "native_subagent", 1, 1,
		"native-subagent-b", "source-fence-b", "active-process-b", strings.Repeat("a", 64), "active", 1); err == nil {
		t.Fatal("cross-agent current binding with mismatched composite identity was accepted")
	}

	// DDL owns composite FK/immutable checks; expected-version plus incarnation equality is the API CAS boundary.
	result, err := db.Exec(`INSERT INTO orchestration_current_bindings (
		agent_id, binding_version, reservation_id, incarnation_id, identity_kind,
		reservation_generation, incarnation_generation, external_identity_ref,
		source_fence_ref, active_process_receipt_ref, binding_digest, state, schema_version
	) SELECT ?, binding_version + 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
	FROM orchestration_current_bindings
	WHERE agent_id = ? AND binding_version = ? AND incarnation_id = ?`,
		"agent-a", "reservation-b", "incarnation-b", "native_subagent", 1, 1,
		"native-subagent-b", "source-fence-b", "active-process-b", strings.Repeat("a", 64), "active", 1,
		"agent-a", 1, "incarnation-b")
	if err != nil {
		t.Fatalf("stale cross-agent CAS: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		t.Fatalf("stale cross-agent CAS rows affected: %v", err)
	} else if affected != 0 {
		t.Fatalf("stale cross-agent CAS changed %d rows, want 0", affected)
	}
	var version int
	var incarnation string
	if err := db.QueryRow(`SELECT binding_version, incarnation_id FROM orchestration_current_bindings WHERE agent_id = 'agent-a'`).Scan(&version, &incarnation); err != nil {
		t.Fatalf("read current binding after stale CAS: %v", err)
	}
	if version != 1 || incarnation != "incarnation-a" {
		t.Fatalf("current binding after stale CAS = version:%d incarnation:%q", version, incarnation)
	}
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-a", 3, "reservation-a", "incarnation-a", "provider_session", 1, 1,
		"provider-session-a", "source-fence-a", "active-process-a", strings.Repeat("6", 64), "revoked", 1); err == nil {
		t.Fatal("current binding version gap was accepted")
	}
}

func assertIdentityOwnerCurrentBindingHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-a", 2, "reservation-a", "incarnation-a", "provider_session", 1, 1,
		"provider-session-a", "source-fence-a", "active-process-a", strings.Repeat("6", 64), "revoked", 1); err != nil {
		t.Fatalf("append revoked current binding A: %v", err)
	}
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-a", 3, "reservation-a", "incarnation-a", "provider_session", 1, 1,
		"provider-session-a", "source-fence-a", "active-process-a", strings.Repeat("6", 64), "revoking", 1); err == nil {
		t.Fatal("current binding accepted an invalid revoked-to-revoking state transition")
	}
	if err := identityExec(t, db, identityReservationInsert,
		"reservation-a-replacement", nil, "agent-a", "provider_session", "provider_discovery",
		"principal-a", "scope-a", "", "identity/agent-a/replacement", strings.Repeat("9", 64), strings.Repeat("0", 64),
		"source-fence-a-replacement", "target-fence-a-replacement", 2, 3, "reserved", 1); err != nil {
		t.Fatalf("insert replacement reservation A: %v", err)
	}
	if err := identityExec(t, db, identityIncarnationInsert,
		"incarnation-a-replacement", "reservation-a-replacement", "agent-a", "provider_session",
		"provider-session-a-replacement", "epoch-a-replacement", "source-fence-a-replacement", strings.Repeat("1", 64), 3, 1); err != nil {
		t.Fatalf("insert replacement incarnation A: %v", err)
	}
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-a", 3, "reservation-a-replacement", "incarnation-a-replacement", "provider_session", 3, 3,
		"provider-session-a-replacement", "source-fence-a-replacement", "active-process-a-replacement", strings.Repeat("2", 64), "active", 1); err != nil {
		t.Fatalf("append active replacement current binding A: %v", err)
	}
	if err := identityExec(t, db, `DELETE FROM orchestration_current_bindings WHERE agent_id = 'agent-a' AND binding_version = 3`); err == nil {
		t.Fatal("current binding history accepted DELETE instead of retaining append-only rows")
	}
}
