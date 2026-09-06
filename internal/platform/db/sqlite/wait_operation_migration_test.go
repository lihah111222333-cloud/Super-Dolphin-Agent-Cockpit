package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

const waitOperationMigration = "136_orchestration_wait_operation.sql"

func TestWaitOperationMigrationContract(t *testing.T) {
	t.Run("schema", testWaitOperationSchema)
	t.Run("canonical_identity_and_selector_relations", testWaitOperationRelations)
	t.Run("append_only_contiguous_journal", testWaitOperationJournal)
	t.Run("derived_grant_and_wait_relations", testWaitOperationDerivedRelations)
	t.Run("one_hot_phase_and_status", testWaitOperationOneHot)
	t.Run("aggregate_ready_and_cleanup_gates", testWaitOperationAggregateGates)
	t.Run("invalid_transitions", testWaitOperationInvalidTransitions)
	t.Run("marker_and_latest_version", testWaitOperationMarker)
}

func openWaitOperationMigrationDB(t *testing.T) *sql.DB {
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
	seedWaitOperationIdentity(t, db)
	return db
}

func seedWaitOperationIdentity(t *testing.T, db *sql.DB) {
	t.Helper()
	seedIdentityOwner(t, db)
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-b", 1, "reservation-b", "incarnation-b", "native_subagent", 1, 1,
		"native-subagent-b", "source-fence-b", "active-process-b", strings.Repeat("7", 64), "active", 1); err != nil {
		t.Fatalf("insert target current binding: %v", err)
	}
	for _, agent := range []string{"agent-a", "agent-b"} {
		for _, state := range []string{"claimed", "identity_pending", "activation_pending", "activated"} {
			if _, err := db.Exec("UPDATE orchestration_identity_reservations SET state = ? WHERE agent_id = ?", state, agent); err != nil {
				t.Fatalf("activate reservation %s/%s: %v", agent, state, err)
			}
		}
	}
}

func testWaitOperationSchema(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	opColumns := []string{
		"operation_key", "operation_digest", "target_set_digest", "caller_principal_ref", "scope_ref",
		"source_agent_id", "source_reservation_id", "source_reservation_generation", "source_incarnation_id",
		"source_incarnation_generation", "source_binding_version", "source_binding_digest", "source_fence_ref",
		"issued_at", "deadline_at", "target_count", "revision", "status", "uncertain_phase",
		"cleanup_request_digest", "cleanup_reason_ref", "resolved_at", "created_at", "updated_at", "schema_version",
	}
	targetColumns := []string{
		"operation_key", "operation_digest", "target_key", "target_index", "version", "target_digest", "record_digest", "previous_record_digest",
		"target_agent_id", "target_reservation_id", "target_reservation_generation", "target_incarnation_id", "target_incarnation_generation", "target_binding_version", "target_binding_digest", "target_fence_ref",
		"source_id", "source_epoch", "lane", "selector_version", "selector_json", "selector_digest", "event_types_json", "after_source_sequence", "after_cursor", "deadline_at",
		"original_grant_id", "grant_namespace", "grant_key_digest", "grant_request_digest", "resolved_grant_id", "grant_revision", "grant_status",
		"wait_namespace", "wait_key_digest", "wait_request_digest", "resolved_wait_handle_id", "wait_revision", "wait_status", "grant_cleanup_state", "wait_cleanup_state", "phase", "uncertain_phase", "error_code", "error_ref", "resolved_at", "recorded_at", "schema_version",
	}
	markerColumns := []string{
		"marker_key", "operation_key", "operation_digest", "target_key", "mutation_kind", "request_digest",
		"pre_state_digest", "post_state_digest", "state", "created_at", "resolved_at", "schema_version",
	}
	if got := tableColumns(t, db, "orchestration_wait_operations"); !slicesEqual(got, opColumns) {
		t.Fatalf("operation columns = %v, want %v", got, opColumns)
	}
	if got := tableColumns(t, db, "orchestration_wait_operation_target_journal"); !slicesEqual(got, targetColumns) {
		t.Fatalf("target columns = %v, want %v", got, targetColumns)
	}
	if got := tableColumns(t, db, "orchestration_wait_operation_commit_markers"); !slicesEqual(got, markerColumns) {
		t.Fatalf("commit marker columns = %v, want %v", got, markerColumns)
	}
	assertNotNullColumns(t, db, "orchestration_wait_operations", opColumns)
	assertNotNullColumns(t, db, "orchestration_wait_operation_target_journal", withoutColumns(targetColumns, "resolved_grant_id", "resolved_wait_handle_id"))
	assertNotNullColumns(t, db, "orchestration_wait_operation_commit_markers", markerColumns)
	assertPrimaryKey(t, db, "orchestration_wait_operations", []string{"operation_key"})
	assertPrimaryKey(t, db, "orchestration_wait_operation_target_journal", []string{"operation_key", "target_key", "version"})
	assertForeignKeySet(t, db, "orchestration_wait_operations", map[string]string{
		"source_reservation_id": "orchestration_identity_reservations", "source_agent_id": "orchestration_identity_reservations", "source_incarnation_id": "orchestration_execution_incarnations",
	})
	assertForeignKeySet(t, db, "orchestration_wait_operation_target_journal", map[string]string{
		"operation_key": "orchestration_wait_operations", "operation_digest": "orchestration_wait_operations",
		"target_reservation_id": "orchestration_identity_reservations", "target_agent_id": "orchestration_identity_reservations",
		"target_incarnation_id": "orchestration_execution_incarnations", "resolved_grant_id": "orchestration_access_grants", "resolved_wait_handle_id": "orchestration_event_wait_handles",
	})
	for _, index := range []struct{ table, name string }{
		{"orchestration_wait_operations", "idx_orchestration_wait_operations_status"},
		{"orchestration_wait_operation_target_journal", "idx_orchestration_wait_operation_target_head"},
		{"orchestration_wait_operation_target_journal", "idx_orchestration_wait_operation_target_index"},
		{"orchestration_wait_operation_commit_markers", "idx_orchestration_wait_operation_commit_markers_unresolved"},
	} {
		assertIndex(t, db, index.table, index.name, false, "")
	}
	wantTriggers := []string{
		"trg_orchestration_wait_operations_source_identity_relation", "trg_orchestration_wait_operations_complete_on_insert", "trg_orchestration_wait_operation_target_relation", "trg_orchestration_wait_operation_target_identity_relation", "trg_orchestration_wait_operation_target_selector_relation", "trg_orchestration_wait_operation_target_contiguous", "trg_orchestration_wait_operation_target_index_map", "trg_orchestration_wait_operation_target_identity", "trg_orchestration_wait_operation_target_previous_digest", "trg_orchestration_wait_operation_resolved_grant_relation", "trg_orchestration_wait_operation_resolved_wait_relation", "trg_orchestration_wait_operation_cleanup_continuity", "trg_orchestration_wait_operation_unknown_resource_continuity", "trg_orchestration_wait_operation_target_head_stability", "trg_orchestration_wait_operations_immutable", "trg_orchestration_wait_operations_cas", "trg_orchestration_wait_operations_unknown_continuity", "trg_orchestration_wait_operations_cleanup_metadata_continuity", "trg_orchestration_wait_operations_cleanup_unknown_no_reopen", "trg_orchestration_wait_operations_complete_head_count", "trg_orchestration_wait_operation_target_phase_transition", "trg_orchestration_wait_operations_no_delete", "trg_orchestration_wait_operation_target_no_update", "trg_orchestration_wait_operation_target_no_delete", "trg_orchestration_wait_operation_commit_marker_pending_insert", "trg_orchestration_wait_operation_commit_marker_immutable", "trg_orchestration_wait_operation_commit_marker_transition", "trg_orchestration_wait_operation_commit_marker_no_delete",
	}
	if got := triggerNames(t, db); !slicesEqual(got, appendExistingTriggers(got, wantTriggers...)) {
		t.Fatalf("wait-operation trigger set missing expected triggers: got=%v want=%v", got, wantTriggers)
	}
}

func testWaitOperationRelations(t *testing.T) {
	t.Run("valid_relation", func(t *testing.T) {
		db := openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-valid", 1)
		if err := insertWaitTarget(db, newWaitTarget("op-valid", 1)); err != nil {
			t.Fatalf("valid target rejected: %v", err)
		}
	})
	t.Run("operation_digest_and_target_index", func(t *testing.T) {
		db := openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-relations", 1)
		candidate := newWaitTarget("op-relations", 1)
		candidate.operationDigest = strings.Repeat("b", 64)
		if err := insertWaitTarget(db, candidate); err == nil {
			t.Fatal("target with mismatched operation digest was accepted")
		}
		candidate = newWaitTarget("op-relations", 1)
		candidate.targetKey = "target-2"
		candidate.targetIndex = 2
		if err := insertWaitTarget(db, candidate); err == nil {
			t.Fatal("target index beyond operation target_count was accepted")
		}
	})
	t.Run("source_newer_binding_rejected", func(t *testing.T) {
		db := openWaitOperationMigrationDB(t)
		if err := identityExec(t, db, identityCurrentBindingInsert,
			"agent-a", 2, "reservation-a", "incarnation-a", "provider_session", 1, 1,
			"provider-session-a", "source-fence-a", "active-process-a-2", strings.Repeat("8", 64), "revoked", 1); err != nil {
			t.Fatalf("insert newer source binding: %v", err)
		}
		if _, err := insertWaitOperationResult(db, "op-source-newer", 1); err == nil {
			t.Fatal("operation bound to superseded source binding was accepted")
		}
	})
	t.Run("target_newer_binding_rejected", func(t *testing.T) {
		db := openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-target-newer", 1)
		if err := identityExec(t, db, identityCurrentBindingInsert,
			"agent-b", 2, "reservation-b", "incarnation-b", "native_subagent", 1, 1,
			"native-subagent-b", "source-fence-b", "active-process-b-2", strings.Repeat("8", 64), "revoked", 1); err != nil {
			t.Fatalf("insert newer revoked binding: %v", err)
		}
		candidate := newWaitTarget("op-target-newer", 1)
		if err := insertWaitTarget(db, candidate); err == nil {
			t.Fatal("target bound to superseded current binding was accepted")
		}
	})
	t.Run("selector_source_union", func(t *testing.T) {
		db := openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-selector-target", 1)
		candidate := newWaitTarget("op-selector-target", 1)
		candidate.sourceID, candidate.sourceEpoch = "task/agent-b", "incarnation-b"
		candidate.selectorJSON = selectorJSON(candidate.sourceID, candidate.sourceEpoch, candidate.lane, candidate.targetAgentID)
		if err := insertWaitTarget(db, candidate); err != nil {
			t.Fatalf("target-source selector union rejected: %v", err)
		}
		db = openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-selector-forged", 1)
		candidate = newWaitTarget("op-selector-forged", 1)
		candidate.sourceID = "task/forged"
		candidate.selectorJSON = selectorJSON(candidate.sourceID, candidate.sourceEpoch, candidate.lane, candidate.targetAgentID)
		if err := insertWaitTarget(db, candidate); err == nil {
			t.Fatal("selector source outside operation/target union was accepted")
		}
	})
}

func testWaitOperationJournal(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-journal", 1)
	first := newWaitTarget("op-journal", 1)
	if err := insertWaitTarget(db, first); err != nil {
		t.Fatalf("insert journal head: %v", err)
	}
	second := first
	second.version = 2
	second.previousRecordDigest = first.recordDigest
	second.recordDigest = digestFor("journal-version-2")
	if err := insertWaitTarget(db, second); err != nil {
		t.Fatalf("contiguous journal append rejected: %v", err)
	}
	third := second
	third.version = 4
	third.previousRecordDigest = second.recordDigest
	third.recordDigest = digestFor("journal-version-4")
	if err := insertWaitTarget(db, third); err == nil {
		t.Fatal("journal version skipped a number")
	}
	badPrevious := second
	badPrevious.version = 3
	badPrevious.previousRecordDigest = digestFor("wrong-predecessor")
	badPrevious.recordDigest = digestFor("journal-version-3")
	if err := insertWaitTarget(db, badPrevious); err == nil {
		t.Fatal("journal with wrong predecessor digest was accepted")
	}
	if _, err := db.Exec("UPDATE orchestration_wait_operation_target_journal SET phase = phase WHERE operation_key = ?", "op-journal"); err == nil {
		t.Fatal("journal update was accepted")
	}
	if _, err := db.Exec("DELETE FROM orchestration_wait_operation_target_journal WHERE operation_key = ?", "op-journal"); err == nil {
		t.Fatal("journal delete was accepted")
	}
}

func testWaitOperationDerivedRelations(t *testing.T) {
	t.Run("grant_splice_rejected", testWaitOperationGrantSpliceRejected)
	t.Run("grant_capability_and_deadline_relation", func(t *testing.T) {
		t.Run("send_only_grant_rejected", func(t *testing.T) {
			db := openWaitOperationMigrationDB(t)
			insertWaitOperation(t, db, "op-grant-capability", 1)
			insertWaitOperationGrantShape(t, db, "grant-send-only", 0, 1, "2030-01-01T00:00:00Z", "2030-01-01T00:10:00Z")
			candidate := newWaitTarget("op-grant-capability", 1)
			candidate.originalGrantID, candidate.resolvedGrantID = "grant-send-only", stringPtr("grant-send-only")
			candidate.grantRevision, candidate.grantStatus = 1, "active"
			candidate.waitStatus, candidate.phase = "planned", "wait_pending"
			if err := insertWaitTarget(db, candidate); err == nil {
				t.Fatal("send-only grant was accepted for a wait target")
			}
		})
		t.Run("grant_deadline_mismatch_rejected", func(t *testing.T) {
			db := openWaitOperationMigrationDB(t)
			insertWaitOperation(t, db, "op-grant-deadline", 1)
			insertWaitOperationGrantShape(t, db, "grant-late", 1, 0, "2030-01-01T00:00:00Z", "2030-01-01T00:20:00Z")
			candidate := newWaitTarget("op-grant-deadline", 1)
			candidate.originalGrantID, candidate.resolvedGrantID = "grant-late", stringPtr("grant-late")
			candidate.grantRevision, candidate.grantStatus = 1, "active"
			candidate.waitStatus, candidate.phase = "planned", "wait_pending"
			if err := insertWaitTarget(db, candidate); err == nil {
				t.Fatal("grant with operation-deadline mismatch was accepted")
			}
		})
	})
	t.Run("target_deadline_relation", func(t *testing.T) {
		db := openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-target-deadline", 1)
		candidate := newWaitTarget("op-target-deadline", 1)
		candidate.deadlineAt = "2030-01-01T00:11:00Z"
		candidate.selectorJSON = selectorJSON(candidate.sourceID, candidate.sourceEpoch, candidate.lane, candidate.targetAgentID)
		if err := insertWaitTarget(db, candidate); err == nil {
			t.Fatal("target with operation-deadline mismatch was accepted")
		}
	})
	t.Run("wait_splice_rejected", func(t *testing.T) {
		db := openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-wait", 1)
		insertWaitOperationGrant(t, db, "grant-wait", true)
		insertWaitOperationHandle(t, db, "wait-good", "grant-wait", true)
		satisfyWaitOperationHandle(t, db, "wait-good")
		candidate := newWaitTarget("op-wait", 1)
		candidate.phase, candidate.originalGrantID, candidate.resolvedGrantID, candidate.grantRevision, candidate.grantStatus = "satisfied", "grant-wait", stringPtr("grant-wait"), 1, "active"
		candidate.resolvedWaitHandleID, candidate.waitRevision, candidate.waitStatus = stringPtr("wait-good"), 2, "satisfied"
		candidate.selectorDigest = digestFor("wait-good:selector")
		candidate.resolvedAt = "2030-01-01T00:05:00Z"
		if err := insertWaitTarget(db, candidate); err != nil {
			t.Fatalf("matching wait handle rejected: %v", err)
		}
		db = openWaitOperationMigrationDB(t)
		insertWaitOperation(t, db, "op-wait-bad", 1)
		insertWaitOperationGrant(t, db, "grant-wait-bad", true)
		insertWaitOperationHandle(t, db, "wait-bad", "grant-wait-bad", false)
		candidate = newWaitTarget("op-wait-bad", 1)
		candidate.phase, candidate.originalGrantID, candidate.resolvedGrantID, candidate.grantRevision, candidate.grantStatus = "waiting", "grant-wait-bad", stringPtr("grant-wait-bad"), 1, "active"
		candidate.resolvedWaitHandleID, candidate.waitRevision, candidate.waitStatus = stringPtr("wait-bad"), 1, "pending"
		if err := insertWaitTarget(db, candidate); err == nil {
			t.Fatal("wait handle spliced across selector relation was accepted")
		}
	})
}

func testWaitOperationGrantSpliceRejected(t *testing.T) {
	t.Helper()
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-grant", 1)
	insertWaitOperationGrant(t, db, "grant-good", true)
	candidate := newWaitTarget("op-grant", 1)
	candidate.phase, candidate.originalGrantID, candidate.resolvedGrantID, candidate.grantRevision, candidate.grantStatus = "wait_pending", "grant-good", stringPtr("grant-good"), 1, "active"
	candidate.waitStatus = "planned"
	if err := insertWaitTarget(db, candidate); err != nil {
		t.Fatalf("matching grant rejected: %v", err)
	}
	db = openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-grant-bad", 1)
	insertWaitOperationGrant(t, db, "grant-bad", false)
	candidate = newWaitTarget("op-grant-bad", 1)
	candidate.phase, candidate.originalGrantID, candidate.resolvedGrantID, candidate.grantRevision, candidate.grantStatus = "wait_pending", "grant-bad", stringPtr("grant-bad"), 1, "active"
	candidate.waitStatus = "planned"
	if err := insertWaitTarget(db, candidate); err == nil {
		t.Fatal("grant row spliced across operation identity was accepted")
	}
}

func testWaitOperationOneHot(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	if _, err := db.Exec(`INSERT INTO orchestration_wait_operations (
operation_key, operation_digest, target_set_digest, caller_principal_ref, scope_ref,
source_agent_id, source_reservation_id, source_reservation_generation, source_incarnation_id,
source_incarnation_generation, source_binding_version, source_binding_digest, source_fence_ref,
issued_at, deadline_at, target_count, revision, status, uncertain_phase, created_at, updated_at, schema_version
) VALUES ('op-one-hot', ?, ?, 'principal-a', 'scope-a', 'agent-a', 'reservation-a', 1, 'incarnation-a', 1, 1, ?, 'source-fence-a', '2030-01-01T00:00:00Z', '2030-01-01T00:10:00Z', 1, 1, 'provisioning', 'grant_create', '2030-01-01T00:00:00Z', '2030-01-01T00:00:00Z', 1)`, strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("6", 64)); err == nil {
		t.Fatal("operation one-hot status/uncertain phase mismatch was accepted")
	}
	insertWaitOperation(t, db, "op-one-hot-valid", 1)
	invalid := newWaitTarget("op-one-hot-valid", 1)
	invalid.phase, invalid.grantStatus, invalid.waitStatus = "waiting", "planned", "absent"
	if err := insertWaitTarget(db, invalid); err == nil {
		t.Fatal("journal phase/status mismatch was accepted")
	}
}

func testWaitOperationAggregateGates(t *testing.T) {
	t.Run("incomplete_heads_block_ready_and_cleanup", testWaitOperationAggregateIncompleteHeads)
	t.Run("cleanup_cleared_requires_head_cleanup", testWaitOperationAggregateCleanupHeads)
	t.Run("unknown_cleanup_metadata_is_immutable", testWaitOperationAggregateUnknownMetadata)
}

// testWaitOperationAggregateIncompleteHeads 验证聚合状态不能绕过目标 head 完整性。
func testWaitOperationAggregateIncompleteHeads(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-aggregate", 2)
	if err := insertWaitTarget(db, newWaitTarget("op-aggregate", 1)); err != nil {
		t.Fatalf("insert aggregate target: %v", err)
	}
	if _, err := db.Exec("UPDATE orchestration_wait_operations SET status = 'ready', revision = 2 WHERE operation_key = 'op-aggregate'"); err == nil {
		t.Fatal("ready aggregate with incomplete target heads was accepted")
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operations SET status = 'cleanup_pending', uncertain_phase = 'cleanup', cleanup_request_digest = ?, cleanup_reason_ref = 'test', revision = 2 WHERE operation_key = 'op-aggregate'`, strings.Repeat("c", 64)); err == nil {
		t.Fatal("cleanup_pending aggregate with incomplete target heads was accepted")
	}
	failed := newWaitTarget("op-aggregate", 2)
	failed.targetKey, failed.targetIndex = "target-2", 2
	failed.targetDigest, failed.recordDigest = digestFor("op-aggregate:target-2"), digestFor("op-aggregate:target-2:record")
	failed.version, failed.previousRecordDigest = 1, ""
	failed.phase, failed.errorCode, failed.resolvedAt = "failed", "target-failed", "2030-01-01T00:01:00Z"
	if err := insertWaitTarget(db, failed); err != nil {
		t.Fatalf("insert second failed target: %v", err)
	}
	firstFailed := newWaitTarget("op-aggregate", 1)
	firstFailed.version, firstFailed.previousRecordDigest = 2, firstFailed.recordDigest
	firstFailed.recordDigest = digestFor("op-aggregate:target-1:failed")
	firstFailed.phase, firstFailed.errorCode, firstFailed.resolvedAt = "failed", "target-failed", "2030-01-01T00:01:00Z"
	if err := insertWaitTarget(db, firstFailed); err != nil {
		t.Fatalf("insert first failed target: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operations SET status = 'cleanup_pending', uncertain_phase = 'cleanup', cleanup_request_digest = ?, cleanup_reason_ref = 'test', revision = 2 WHERE operation_key = 'op-aggregate'`, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("enter cleanup_pending: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operations SET status = 'cleanup_cleared', uncertain_phase = 'cleanup', cleanup_request_digest = ?, cleanup_reason_ref = 'other', resolved_at = '2030-01-01T00:11:00Z', revision = 3 WHERE operation_key = 'op-aggregate'`, strings.Repeat("d", 64)); err == nil {
		t.Fatal("cleanup metadata drift was accepted")
	}
	if _, err := db.Exec("UPDATE orchestration_wait_operations SET status = 'cleanup_cleared', resolved_at = '2030-01-01T00:11:00Z', revision = 3 WHERE operation_key = 'op-aggregate'"); err == nil {
		t.Fatal("cleanup_cleared aggregate with incomplete target heads was accepted")
	}
}

// testWaitOperationAggregateCleanupHeads 验证失败目标 head 进入清理后仍需逐目标收口。
func testWaitOperationAggregateCleanupHeads(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-aggregate-cleanup", 2)
	seedFailedWaitTargetHeads(t, db, "op-aggregate-cleanup")
	if _, err := db.Exec(`UPDATE orchestration_wait_operations SET status = 'cleanup_pending', uncertain_phase = 'cleanup', cleanup_request_digest = ?, cleanup_reason_ref = 'test', revision = 2 WHERE operation_key = 'op-aggregate-cleanup'`, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("enter cleanup_pending: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operations SET status = 'cleanup_cleared', uncertain_phase = 'cleanup', cleanup_request_digest = ?, cleanup_reason_ref = 'other', resolved_at = '2030-01-01T00:11:00Z', revision = 3 WHERE operation_key = 'op-aggregate-cleanup'`, strings.Repeat("d", 64)); err == nil {
		t.Fatal("cleanup metadata drift was accepted")
	}
	if _, err := db.Exec("UPDATE orchestration_wait_operations SET status = 'cleanup_cleared', resolved_at = '2030-01-01T00:11:00Z', revision = 3 WHERE operation_key = 'op-aggregate-cleanup'"); err == nil {
		t.Fatal("cleanup_cleared aggregate with incomplete target heads was accepted")
	}
}

// seedFailedWaitTargetHeads 为聚合清理断言写入两个失败目标 head。
func seedFailedWaitTargetHeads(t *testing.T, db *sql.DB, operationKey string) {
	t.Helper()
	first := newWaitTarget(operationKey, 1)
	if err := insertWaitTarget(db, first); err != nil {
		t.Fatalf("insert first aggregate target: %v", err)
	}
	second := newWaitTarget(operationKey, 2)
	second.targetKey, second.targetIndex = "target-2", 2
	second.targetDigest, second.recordDigest = digestFor(operationKey+":target-2"), digestFor(operationKey+":target-2:record")
	second.version, second.previousRecordDigest = 1, ""
	second.phase, second.errorCode, second.resolvedAt = "failed", "target-failed", "2030-01-01T00:01:00Z"
	if err := insertWaitTarget(db, second); err != nil {
		t.Fatalf("insert second failed target: %v", err)
	}
	first.version, first.previousRecordDigest = 2, first.recordDigest
	first.recordDigest = digestFor(operationKey + ":target-1:failed")
	first.phase, first.errorCode, first.resolvedAt = "failed", "target-failed", "2030-01-01T00:01:00Z"
	if err := insertWaitTarget(db, first); err != nil {
		t.Fatalf("insert first failed target: %v", err)
	}
}

// testWaitOperationAggregateUnknownMetadata 验证 unknown 清理元数据不可漂移。
func testWaitOperationAggregateUnknownMetadata(t *testing.T) {
	dbUnknown := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, dbUnknown, "op-cleanup-unknown-metadata", 1)
	initialUnknown := newWaitTarget("op-cleanup-unknown-metadata", 1)
	if err := insertWaitTarget(dbUnknown, initialUnknown); err != nil {
		t.Fatalf("insert cleanup unknown initial target: %v", err)
	}
	unknownTarget := initialUnknown
	unknownTarget.version, unknownTarget.previousRecordDigest = 2, unknownTarget.recordDigest
	unknownTarget.recordDigest = digestFor("cleanup-unknown-target")
	unknownTarget.phase, unknownTarget.uncertainPhase = "outcome_unknown", "cleanup"
	unknownTarget.grantCleanupState, unknownTarget.waitCleanupState = "pending", "pending"
	if err := insertWaitTarget(dbUnknown, unknownTarget); err != nil {
		t.Fatalf("insert cleanup unknown target: %v", err)
	}
	if _, err := dbUnknown.Exec(`UPDATE orchestration_wait_operations SET status = 'outcome_unknown', uncertain_phase = 'cleanup', cleanup_request_digest = ?, cleanup_reason_ref = 'first', revision = 2 WHERE operation_key = 'op-cleanup-unknown-metadata'`, strings.Repeat("e", 64)); err != nil {
		t.Fatalf("enter cleanup outcome_unknown: %v", err)
	}
	if _, err := dbUnknown.Exec(`UPDATE orchestration_wait_operations SET status = 'cleanup_pending', uncertain_phase = 'cleanup', cleanup_request_digest = ?, cleanup_reason_ref = 'second', revision = 3 WHERE operation_key = 'op-cleanup-unknown-metadata'`, strings.Repeat("f", 64)); err == nil {
		t.Fatal("cleanup metadata drift from unknown was accepted")
	}
}

func testWaitOperationInvalidTransitions(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-invalid", 1)
	if _, err := db.Exec("UPDATE orchestration_wait_operations SET status = 'cleanup_cleared', revision = 2 WHERE operation_key = 'op-invalid'"); err == nil {
		t.Fatal("invalid operation status transition was accepted")
	}
	if err := insertWaitTarget(db, newWaitTarget("op-invalid", 1)); err != nil {
		t.Fatalf("insert invalid-transition fixture: %v", err)
	}
	invalid := newWaitTarget("op-invalid", 1)
	invalid.version = 2
	invalid.previousRecordDigest = newWaitTarget("op-invalid", 1).recordDigest
	invalid.recordDigest = digestFor("invalid-transition")
	invalid.phase = "waiting"
	invalid.grantStatus, invalid.waitStatus, invalid.grantRevision, invalid.waitRevision = "active", "pending", 1, 1
	insertWaitOperationGrant(t, db, "grant-1", true)
	insertWaitOperationHandle(t, db, "wait-1", "grant-1", true)
	invalid.resolvedGrantID, invalid.resolvedWaitHandleID = stringPtr("grant-1"), stringPtr("wait-1")
	invalid.selectorDigest = digestFor("wait-1:selector")
	if err := insertWaitTarget(db, invalid); err == nil {
		t.Fatal("invalid planned-to-waiting transition was accepted")
	}
}

func testWaitOperationMarker(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	assertMigrationMarkerCount(t, db, waitOperationMigration, 1)
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, waitOperationMigration, 1)
	digest := strings.Repeat("a", 64)
	if _, err := db.Exec(`INSERT INTO orchestration_wait_operation_commit_markers
        (marker_key, operation_key, operation_digest, mutation_kind, request_digest,
         pre_state_digest, state, created_at, schema_version)
        VALUES ('1111111111111111111111111111111111111111111111111111111111111111', 'op-marker', ?, 'plan', ?, '', 'pending', '2030-01-01T00:00:00Z', 1)`, digest, strings.Repeat("b", 64)); err != nil {
		t.Fatalf("insert pending commit marker: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operation_commit_markers
        SET post_state_digest = ? WHERE marker_key = '1111111111111111111111111111111111111111111111111111111111111111'`, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("record commit marker post digest: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operation_commit_markers
        SET state = 'resolved', resolved_at = '2030-01-01T00:00:01Z' WHERE marker_key = '1111111111111111111111111111111111111111111111111111111111111111'`); err != nil {
		t.Fatalf("resolve commit marker: %v", err)
	}
	assertResolvedWaitOperationMarkerImmutable(t, db)
	assertMalformedWaitOperationMarkersRejected(t, db, digest)
}

// assertResolvedWaitOperationMarkerImmutable 验证已解析 marker 不可重开或篡改。
func assertResolvedWaitOperationMarkerImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	mutations := []struct {
		name, statement string
		args            []any
	}{
		{"resolved marker reopen", `UPDATE orchestration_wait_operation_commit_markers SET state = 'pending' WHERE marker_key = '1111111111111111111111111111111111111111111111111111111111111111'`, nil},
		{"resolved marker post digest drift", `UPDATE orchestration_wait_operation_commit_markers SET post_state_digest = ? WHERE marker_key = '1111111111111111111111111111111111111111111111111111111111111111'`, []any{strings.Repeat("d", 64)}},
		{"marker identity update", `UPDATE orchestration_wait_operation_commit_markers SET request_digest = ? WHERE marker_key = '1111111111111111111111111111111111111111111111111111111111111111'`, []any{strings.Repeat("e", 64)}},
		{"marker delete", `DELETE FROM orchestration_wait_operation_commit_markers WHERE marker_key = '1111111111111111111111111111111111111111111111111111111111111111'`, nil},
	}
	for _, mutation := range mutations {
		if _, err := db.Exec(mutation.statement, mutation.args...); err == nil {
			t.Fatalf("%s was accepted", mutation.name)
		}
	}
}

// assertMalformedWaitOperationMarkersRejected 验证 marker key 与前置摘要必须符合规范。
func assertMalformedWaitOperationMarkersRejected(t *testing.T, db *sql.DB, digest string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO orchestration_wait_operation_commit_markers
        (marker_key, operation_key, operation_digest, mutation_kind, request_digest,
         pre_state_digest, state, created_at, resolved_at, schema_version)
        VALUES ('marker-short', 'op-marker-short', ?, 'plan', ?, '', 'pending', '2030-01-01T00:00:00Z', '', 1)`, digest, strings.Repeat("b", 64)); err == nil {
		t.Fatal("noncanonical marker key was accepted")
	}
	if _, err := db.Exec(`INSERT INTO orchestration_wait_operation_commit_markers
        (marker_key, operation_key, operation_digest, mutation_kind, request_digest,
         pre_state_digest, state, created_at, schema_version)
        VALUES ('2222222222222222222222222222222222222222222222222222222222222222', 'op-marker', ?, 'target_append', ?, '', 'pending', '2030-01-01T00:00:00Z', 1)`, digest, strings.Repeat("b", 64)); err == nil {
		t.Fatal("target mutation marker without pre digest was accepted")
	}
}

type waitTargetFixture struct {
	operationKey, operationDigest, targetKey                               string
	targetIndex, version                                                   int
	targetDigest, recordDigest, previousRecordDigest                       string
	targetAgentID, targetReservationID                                     string
	targetReservationGeneration                                            int
	targetIncarnationID                                                    string
	targetIncarnationGeneration, targetBindingVersion                      int
	targetBindingDigest, targetFenceRef                                    string
	sourceID, sourceEpoch, lane                                            string
	selectorVersion                                                        int
	selectorJSON, selectorDigest, eventTypesJSON                           string
	afterSourceSequence                                                    int
	afterCursor, deadlineAt                                                string
	originalGrantID, grantNamespace, grantKeyDigest, grantRequestDigest    string
	resolvedGrantID                                                        *string
	grantRevision                                                          int
	grantStatus, waitNamespace, waitKeyDigest, waitRequestDigest           string
	resolvedWaitHandleID                                                   *string
	waitRevision                                                           int
	waitStatus, grantCleanupState, waitCleanupState, phase, uncertainPhase string
	errorCode, errorRef, resolvedAt, recordedAt                            string
}

func newWaitTarget(operationKey string, version int) waitTargetFixture {
	sourceID, sourceEpoch, lane, targetAgent := "task/agent-a", "incarnation-a", "control", "agent-b"
	return waitTargetFixture{
		operationKey: operationKey, operationDigest: strings.Repeat("a", 64), targetKey: "target-1", targetIndex: 1, version: version,
		targetDigest: digestFor(operationKey + ":target"), recordDigest: digestFor(fmt.Sprintf("%s:record:%d", operationKey, version)),
		targetAgentID: targetAgent, targetReservationID: "reservation-b", targetReservationGeneration: 1, targetIncarnationID: "incarnation-b", targetIncarnationGeneration: 1,
		targetBindingVersion: 1, targetBindingDigest: strings.Repeat("7", 64), targetFenceRef: "source-fence-b",
		sourceID: sourceID, sourceEpoch: sourceEpoch, lane: lane, selectorVersion: 1, selectorJSON: selectorJSON(sourceID, sourceEpoch, lane, targetAgent), selectorDigest: digestFor(operationKey + ":selector"), eventTypesJSON: `["event"]`,
		afterSourceSequence: 0, afterCursor: "", deadlineAt: "2030-01-01T00:10:00Z", originalGrantID: "grant-1", grantNamespace: "grant-namespace", grantKeyDigest: strings.Repeat("d", 64), grantRequestDigest: strings.Repeat("e", 64), grantRevision: 0, grantStatus: "planned",
		waitNamespace: "wait-namespace", waitKeyDigest: strings.Repeat("f", 64), waitRequestDigest: strings.Repeat("a", 64), waitRevision: 0, waitStatus: "absent", grantCleanupState: "none", waitCleanupState: "none", phase: "planned", recordedAt: "2030-01-01T00:00:01Z",
	}
}

func selectorJSON(sourceID, sourceEpoch, lane, targetAgent string) string {
	return fmt.Sprintf(`{"version":1,"source_id":%q,"source_epoch":%q,"lane":%q,"target_agent_id":%q,"event_types":["event"]}`, sourceID, sourceEpoch, lane, targetAgent)
}

func insertWaitOperation(t *testing.T, db *sql.DB, key string, targetCount int) {
	t.Helper()
	_, err := insertWaitOperationResult(db, key, targetCount)
	if err != nil {
		t.Fatalf("insert operation %s: %v", key, err)
	}
}

func insertWaitOperationResult(db *sql.DB, key string, targetCount int) (sql.Result, error) {
	return db.Exec(`INSERT INTO orchestration_wait_operations (
operation_key, operation_digest, target_set_digest, caller_principal_ref, scope_ref,
source_agent_id, source_reservation_id, source_reservation_generation, source_incarnation_id,
source_incarnation_generation, source_binding_version, source_binding_digest, source_fence_ref,
issued_at, deadline_at, target_count, revision, status, created_at, updated_at, schema_version
) VALUES (?, ?, ?, 'principal-a', 'scope-a', 'agent-a', 'reservation-a', 1, 'incarnation-a', 1, 1, ?, 'source-fence-a', '2030-01-01T00:00:00Z', '2030-01-01T00:10:00Z', ?, 1, 'provisioning', '2030-01-01T00:00:00Z', '2030-01-01T00:00:00Z', 1)`, key, strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("6", 64), targetCount)
}

func insertWaitTarget(db *sql.DB, f waitTargetFixture) error {
	_, err := db.Exec(`INSERT INTO orchestration_wait_operation_target_journal (
operation_key, operation_digest, target_key, target_index, version, target_digest, record_digest, previous_record_digest,
target_agent_id, target_reservation_id, target_reservation_generation, target_incarnation_id, target_incarnation_generation, target_binding_version, target_binding_digest, target_fence_ref,
source_id, source_epoch, lane, selector_version, selector_json, selector_digest, event_types_json, after_source_sequence, after_cursor, deadline_at,
original_grant_id, grant_namespace, grant_key_digest, grant_request_digest, resolved_grant_id, grant_revision, grant_status,
wait_namespace, wait_key_digest, wait_request_digest, resolved_wait_handle_id, wait_revision, wait_status, grant_cleanup_state, wait_cleanup_state, phase, uncertain_phase, error_code, error_ref, resolved_at, recorded_at, schema_version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		f.operationKey, f.operationDigest, f.targetKey, f.targetIndex, f.version, f.targetDigest, f.recordDigest, f.previousRecordDigest,
		f.targetAgentID, f.targetReservationID, f.targetReservationGeneration, f.targetIncarnationID, f.targetIncarnationGeneration, f.targetBindingVersion, f.targetBindingDigest, f.targetFenceRef,
		f.sourceID, f.sourceEpoch, f.lane, f.selectorVersion, f.selectorJSON, f.selectorDigest, f.eventTypesJSON, f.afterSourceSequence, f.afterCursor, f.deadlineAt,
		f.originalGrantID, f.grantNamespace, f.grantKeyDigest, f.grantRequestDigest, f.resolvedGrantID, f.grantRevision, f.grantStatus,
		f.waitNamespace, f.waitKeyDigest, f.waitRequestDigest, f.resolvedWaitHandleID, f.waitRevision, f.waitStatus, f.grantCleanupState, f.waitCleanupState, f.phase, f.uncertainPhase, f.errorCode, f.errorRef, f.resolvedAt, f.recordedAt)
	return err
}

func insertWaitOperationGrant(t *testing.T, db *sql.DB, grantID string, matching bool) {
	t.Helper()
	scope, targetDigest := "scope-a", strings.Repeat("7", 64)
	if !matching {
		scope = "scope-spliced"
	}
	_, err := db.Exec(`INSERT INTO orchestration_access_grants (
grant_id, caller_principal_ref, scope_ref, source_agent_id, target_agent_id, source_reservation_id, source_reservation_generation, source_incarnation_id, source_incarnation_generation, source_binding_version, source_binding_digest, source_fence_ref, target_reservation_id, target_reservation_generation, target_incarnation_id, target_incarnation_generation, target_binding_version, target_binding_digest, target_fence_ref, can_read, can_send, idempotency_namespace, idempotency_key_digest, request_digest, revision, status, issued_at, expires_at, schema_version
) VALUES (?, 'principal-a', ?, 'agent-a', 'agent-b', 'reservation-a', 1, 'incarnation-a', 1, 1, ?, 'source-fence-a', 'reservation-b', 1, 'incarnation-b', 1, 1, ?, 'source-fence-b', 1, 0, 'grant-namespace', ?, ?, 1, 'active', '2030-01-01T00:00:00Z', '2030-01-01T00:10:00Z', 1)`, grantID, scope, strings.Repeat("6", 64), targetDigest, strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatalf("insert grant %s: %v", grantID, err)
	}
}

func insertWaitOperationGrantShape(t *testing.T, db *sql.DB, grantID string, canRead, canSend int, issuedAt, expiresAt string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO orchestration_access_grants (
grant_id, caller_principal_ref, scope_ref, source_agent_id, target_agent_id, source_reservation_id, source_reservation_generation, source_incarnation_id, source_incarnation_generation, source_binding_version, source_binding_digest, source_fence_ref, target_reservation_id, target_reservation_generation, target_incarnation_id, target_incarnation_generation, target_binding_version, target_binding_digest, target_fence_ref, can_read, can_send, idempotency_namespace, idempotency_key_digest, request_digest, revision, status, issued_at, expires_at, schema_version
) VALUES (?, 'principal-a', 'scope-a', 'agent-a', 'agent-b', 'reservation-a', 1, 'incarnation-a', 1, 1, ?, 'source-fence-a', 'reservation-b', 1, 'incarnation-b', 1, 1, ?, 'source-fence-b', ?, ?, 'grant-namespace', ?, ?, 1, 'active', ?, ?, 1)`, grantID, strings.Repeat("6", 64), strings.Repeat("7", 64), canRead, canSend, strings.Repeat("d", 64), strings.Repeat("e", 64), issuedAt, expiresAt)
	if err != nil {
		t.Fatalf("insert grant %s: %v", grantID, err)
	}
}

func insertWaitOperationHandle(t *testing.T, db *sql.DB, waitID, grantID string, matching bool) {
	t.Helper()
	selector, eventTypes, sourceID, sourceEpoch, lane, targetAgent := selectorJSON("task/agent-a", "incarnation-a", "control", "agent-b"), `["event"]`, "task/agent-a", "incarnation-a", "control", "agent-b"
	if !matching {
		selector, eventTypes = selectorJSON("task/forged", "incarnation-a", "control", targetAgent), `["forged"]`
	}
	_, err := db.Exec(`INSERT INTO orchestration_event_wait_handles (
wait_handle_id, grant_id, grant_revision, caller_principal_ref, scope_ref, source_agent_id, target_agent_id, target_reservation_id, target_reservation_generation, target_incarnation_id, target_incarnation_generation, target_binding_version, target_binding_digest, target_fence_ref, source_id, source_epoch, lane, selector_version, selector_json, selector_digest, event_types_json, after_source_sequence, after_cursor, deadline_at, idempotency_namespace, idempotency_key_digest, request_digest, revision, status, schema_version
) VALUES (?, ?, 1, 'principal-a', 'scope-a', 'agent-a', 'agent-b', 'reservation-b', 1, 'incarnation-b', 1, 1, ?, 'source-fence-b', ?, ?, ?, 1, ?, ?, ?, 0, '', '2030-01-01T00:10:00Z', 'wait-namespace', ?, ?, 1, 'pending', 1)`, waitID, grantID, strings.Repeat("7", 64), sourceID, sourceEpoch, lane, selector, digestFor(waitID+":selector"), eventTypes, strings.Repeat("f", 64), strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("insert wait handle %s: %v", waitID, err)
	}
}

func satisfyWaitOperationHandle(t *testing.T, db *sql.DB, waitID string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE orchestration_event_wait_handles
SET revision = 2, status = 'satisfied', matched_event_id = 'event-1', matched_source_sequence = 1,
    matched_canonical_cursor = 'cursor-1', resolved_at = '2030-01-01T00:05:00Z', updated_at = '2030-01-01T00:05:00Z'
WHERE wait_handle_id = ? AND revision = 1 AND status = 'pending'`, waitID); err != nil {
		t.Fatalf("satisfy wait handle %s: %v", waitID, err)
	}
}

func assertForeignKeySet(t *testing.T, db *sql.DB, table string, want map[string]string) {
	t.Helper()
	got := foreignKeys(t, db, table)
	if len(got) != len(want) {
		t.Fatalf("%s foreign keys = %v, want %v", table, got, want)
	}
	for column, target := range want {
		if got[column] != target {
			t.Fatalf("%s.%s foreign key = %q, want %q", table, column, got[column], target)
		}
	}
}

func triggerNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master
WHERE type = 'trigger' AND tbl_name IN ('orchestration_wait_operations', 'orchestration_wait_operation_target_journal', 'orchestration_wait_operation_commit_markers')`)
	if err != nil {
		t.Fatalf("list wait-operation triggers: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan trigger: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate triggers: %v", err)
	}
	sort.Strings(names)
	return names
}

func appendExistingTriggers(_ []string, expected ...string) []string {
	sort.Strings(expected)
	return expected
}

func stringPtr(value string) *string {
	return &value
}

func withoutColumns(columns []string, excluded ...string) []string {
	set := make(map[string]struct{}, len(excluded))
	for _, column := range excluded {
		set[column] = struct{}{}
	}
	result := make([]string, 0, len(columns)-len(excluded))
	for _, column := range columns {
		if _, ok := set[column]; !ok {
			result = append(result, column)
		}
	}
	return result
}
