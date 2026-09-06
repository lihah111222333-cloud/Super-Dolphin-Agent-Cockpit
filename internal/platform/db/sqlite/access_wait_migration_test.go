package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestAccessWaitMigrationContract covers the v130 authorization and wait
// owners. The fixture intentionally uses only identity-owner rows and proves
// that lifecycle state cannot be used to rewrite immutable metadata.
func TestAccessWaitMigrationContract(t *testing.T) {
	db := prepareAccessWaitDB(t)

	t.Run("grant_metadata_and_delete_are_immutable", func(t *testing.T) {
		assertGrantMetadataImmutable(t, db)
	})
	t.Run("grant_expiry_and_capability", func(t *testing.T) {
		assertGrantExpiryAndCapability(t, db)
	})

	insertAccessGrant(t, db, "grant-wait", "2030-01-01T00:00:00Z", "2030-01-01T00:10:00Z")
	insertWaitHandle(t, db, "wait-1", "grant-wait", "2030-01-01T00:05:00Z")
	assertAccessWaitRelationTriggers(t, db)
	t.Run("wait_one_hot_and_cas", func(t *testing.T) {
		assertWaitInitialTransitions(t, db)
		assertTerminalWaitTransitions(t, db)
	})
}

func prepareAccessWaitDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatal(err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertAccessWaitSchema(t, db)
	seedAccessWaitIdentity(t, db, "src")
	seedAccessWaitIdentity(t, db, "dst")
	insertAccessGrant(t, db, "grant-active", "2030-01-01T00:00:00Z", "2030-01-01T00:10:00Z")
	return db
}

func assertGrantMetadataImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("UPDATE orchestration_access_grants SET scope_ref = 'changed' WHERE grant_id = 'grant-active'"); err == nil {
		t.Fatal("grant metadata update unexpectedly succeeded")
	}
	if _, err := db.Exec("DELETE FROM orchestration_access_grants WHERE grant_id = 'grant-active'"); err == nil {
		t.Fatal("grant delete unexpectedly succeeded")
	}
}

func assertGrantExpiryAndCapability(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("UPDATE orchestration_access_grants SET status = 'expired' WHERE grant_id = 'grant-active'"); err == nil {
		t.Fatal("grant lifecycle update without revision unexpectedly succeeded")
	}
	if _, err := db.Exec("UPDATE orchestration_access_grants SET can_read = 0, can_send = 0 WHERE grant_id = 'grant-active'"); err == nil {
		t.Fatal("grant with no capability unexpectedly succeeded")
	}
	if _, err := db.Exec("UPDATE orchestration_access_grants SET status = 'expired', revision = revision + 1, updated_at = '2030-01-01T00:11:00Z' WHERE grant_id = 'grant-active' AND expires_at <= '2030-01-01T00:11:00Z'"); err != nil {
		t.Fatalf("grant expiry CAS: %v", err)
	}
}

func assertWaitInitialTransitions(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("UPDATE orchestration_event_wait_handles SET status = 'satisfied' WHERE wait_handle_id = 'wait-1'"); err == nil {
		t.Fatal("wait satisfied without CAS unexpectedly succeeded")
	}
	if _, err := db.Exec("UPDATE orchestration_event_wait_handles SET status = 'satisfied', revision = revision + 1, matched_event_id = 'event-1', matched_source_sequence = 2, matched_canonical_cursor = '', resolved_at = '2030-01-01T00:01:00Z' WHERE wait_handle_id = 'wait-1'"); err == nil {
		t.Fatal("satisfied wait with incomplete match unexpectedly succeeded")
	}
	if _, err := db.Exec("UPDATE orchestration_event_wait_handles SET status = 'timed_out', revision = revision + 1, resolved_at = '2030-01-01T00:06:00Z', updated_at = '2030-01-01T00:06:00Z' WHERE wait_handle_id = 'wait-1' AND revision = 1 AND deadline_at <= '2030-01-01T00:06:00Z'"); err != nil {
		t.Fatalf("wait timeout CAS: %v", err)
	}
	if _, err := db.Exec("UPDATE orchestration_event_wait_handles SET status = 'cancelled', revision = revision + 1, resolved_at = '2030-01-01T00:07:00Z' WHERE wait_handle_id = 'wait-1'"); err == nil {
		t.Fatal("terminal wait transition unexpectedly succeeded")
	}
	if _, err := db.Exec("DELETE FROM orchestration_event_wait_handles WHERE wait_handle_id = 'wait-1'"); err == nil {
		t.Fatal("wait delete unexpectedly succeeded")
	}
}

func assertTerminalWaitTransitions(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, terminal := range []string{"satisfied", "cancelled", "revoked", "expired"} {
		waitID := "wait-" + terminal
		insertWaitHandle(t, db, waitID, "grant-wait", "2030-01-01T00:10:00Z")
		result, err := db.Exec(waitTerminalCASStatement(terminal), waitID)
		if err != nil {
			t.Fatalf("%s wait CAS: %v", terminal, err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			t.Fatalf("%s wait CAS affected %d rows, want 1", terminal, affected)
		}
	}
}

func waitTerminalCASStatement(terminal string) string {
	switch terminal {
	case "satisfied":
		return "UPDATE orchestration_event_wait_handles SET status = 'satisfied', revision = revision + 1, matched_event_id = 'event-1', matched_source_sequence = 2, matched_canonical_cursor = 'cursor-1', resolved_at = '2030-01-01T00:01:00Z', updated_at = '2030-01-01T00:01:00Z' WHERE wait_handle_id = ? AND revision = 1 AND status = 'pending'"
	case "cancelled", "revoked":
		return "UPDATE orchestration_event_wait_handles SET status = '" + terminal + "', revision = revision + 1, resolved_at = '2030-01-01T00:01:00Z', updated_at = '2030-01-01T00:01:00Z' WHERE wait_handle_id = ? AND revision = 1 AND status = 'pending'"
	case "expired":
		return "UPDATE orchestration_event_wait_handles SET status = 'expired', revision = revision + 1, resolved_at = '2030-01-01T00:11:00Z', updated_at = '2030-01-01T00:11:00Z' WHERE wait_handle_id = ? AND revision = 1 AND status = 'pending' AND deadline_at <= '2030-01-01T00:11:00Z'"
	default:
		return ""
	}
}

func assertAccessWaitSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	grantColumns := []string{"grant_id", "caller_principal_ref", "scope_ref", "source_agent_id", "target_agent_id", "source_reservation_id", "source_reservation_generation", "source_incarnation_id", "source_incarnation_generation", "source_binding_version", "source_binding_digest", "source_fence_ref", "target_reservation_id", "target_reservation_generation", "target_incarnation_id", "target_incarnation_generation", "target_binding_version", "target_binding_digest", "target_fence_ref", "can_read", "can_send", "idempotency_namespace", "idempotency_key_digest", "request_digest", "revision", "status", "issued_at", "expires_at", "created_at", "updated_at", "schema_version"}
	waitColumns := []string{"wait_handle_id", "grant_id", "grant_revision", "caller_principal_ref", "scope_ref", "source_agent_id", "target_agent_id", "target_reservation_id", "target_reservation_generation", "target_incarnation_id", "target_incarnation_generation", "target_binding_version", "target_binding_digest", "target_fence_ref", "source_id", "source_epoch", "lane", "selector_version", "selector_json", "selector_digest", "event_types_json", "after_source_sequence", "after_cursor", "deadline_at", "idempotency_namespace", "idempotency_key_digest", "request_digest", "revision", "status", "matched_event_id", "matched_source_sequence", "matched_canonical_cursor", "resolved_at", "created_at", "updated_at", "schema_version"}
	for _, table := range []struct {
		name string
		cols []string
		fks  []string
	}{
		{"orchestration_access_grants", grantColumns, []string{"source_reservation_id", "target_reservation_id", "source_incarnation_id", "target_incarnation_id"}},
		{"orchestration_event_wait_handles", waitColumns, []string{"grant_id", "target_reservation_id", "target_incarnation_id"}},
	} {
		info := tableInfo(t, db, table.name)
		for _, col := range table.cols {
			if _, ok := info[col]; !ok {
				t.Errorf("%s missing column %s", table.name, col)
			}
		}
		foreignKeys := foreignKeys(t, db, table.name)
		for _, col := range table.fks {
			if _, ok := foreignKeys[col]; !ok {
				t.Errorf("%s missing foreign key %s", table.name, col)
			}
		}
		var ddl string
		if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table.name).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToUpper(ddl), "STRICT") {
			t.Errorf("%s is not STRICT", table.name)
		}
		for _, forbidden := range []string{"body", "content", "command_id"} {
			if strings.Contains(strings.ToLower(ddl), forbidden+" ") {
				t.Errorf("%s contains forbidden field %q", table.name, forbidden)
			}
		}
	}
}

func seedAccessWaitIdentity(t *testing.T, db *sql.DB, agent string) {
	t.Helper()
	digest := strings.Repeat("a", 64)
	_, err := db.Exec(`INSERT INTO orchestration_identity_reservations (
reservation_id, agent_id, identity_kind, source_kind, caller_principal_ref, scope_ref,
parent_agent_id, idempotency_namespace, idempotency_key_digest, request_digest,
source_fence_ref, target_fence_ref, expected_binding_version, reservation_generation, state, schema_version
) VALUES (?, ?, 'native_subagent', 'provider_discovery', 'principal', 'scope', '', ?, ?, ?, 'source-fence', 'target-fence', 1, 1, 'activated', 1)`,
		"reservation-"+agent, agent, "ns-"+agent, digest, digest, digest)
	if err != nil {
		t.Fatalf("seed reservation %s: %v", agent, err)
	}
	_, err = db.Exec(`INSERT INTO orchestration_execution_incarnations (
incarnation_id, reservation_id, agent_id, identity_kind, external_identity_ref,
external_epoch, source_fence_ref, identity_digest, incarnation_generation, schema_version
) VALUES (?, ?, ?, 'native_subagent', ?, 'epoch-1', 'source-fence', ?, 1, 1)`,
		"incarnation-"+agent, "reservation-"+agent, agent, "external-"+agent, digest)
	if err != nil {
		t.Fatalf("seed incarnation %s: %v", agent, err)
	}
}

func insertAccessGrant(t *testing.T, db *sql.DB, grantID, issuedAt, expiresAt string) {
	t.Helper()
	digest := strings.Repeat("a", 64)
	_, err := db.Exec(`INSERT INTO orchestration_access_grants (
grant_id, caller_principal_ref, scope_ref, source_agent_id, target_agent_id,
source_reservation_id, source_reservation_generation, source_incarnation_id, source_incarnation_generation,
source_binding_version, source_binding_digest, source_fence_ref, target_reservation_id, target_reservation_generation,
target_incarnation_id, target_incarnation_generation, target_binding_version, target_binding_digest, target_fence_ref,
can_read, can_send, idempotency_namespace, idempotency_key_digest, request_digest, revision, status, issued_at, expires_at, schema_version
) VALUES (?, 'principal', 'scope', 'src', 'dst', 'reservation-src', 1, 'incarnation-src', 1, 1, ?, 'source-fence', 'reservation-dst', 1, 'incarnation-dst', 1, 1, ?, 'target-fence', 1, 0, ?, ?, ?, 1, 'active', ?, ?, 1)`,
		grantID, digest, digest, "ns-"+grantID, digest, digest, issuedAt, expiresAt)
	if err != nil {
		t.Fatalf("insert grant %s: %v", grantID, err)
	}
}

func insertWaitHandle(t *testing.T, db *sql.DB, waitID, grantID, deadline string) {
	t.Helper()
	digest := strings.Repeat("a", 64)
	_, err := db.Exec(`INSERT INTO orchestration_event_wait_handles (
wait_handle_id, grant_id, grant_revision, caller_principal_ref, scope_ref, source_agent_id, target_agent_id,
target_reservation_id, target_reservation_generation, target_incarnation_id, target_incarnation_generation,
target_binding_version, target_binding_digest, target_fence_ref, source_id, source_epoch, lane, selector_version,
selector_json, selector_digest, event_types_json, after_source_sequence, after_cursor, deadline_at,
idempotency_namespace, idempotency_key_digest, request_digest, revision, status, schema_version
) VALUES (?, ?, 1, 'principal', 'scope', 'src', 'dst', 'reservation-dst', 1, 'incarnation-dst', 1, 1, ?, 'target-fence', 'source', 'epoch-1', 'control', 1, '{"selector":"agent"}', ?, '["event"]', 0, '', ?, ?, ?, ?, 1, 'pending', 1)`,
		waitID, grantID, digest, digest, deadline, "ns-"+waitID, digest, digest)
	if err != nil {
		t.Fatalf("insert wait %s: %v", waitID, err)
	}
}
