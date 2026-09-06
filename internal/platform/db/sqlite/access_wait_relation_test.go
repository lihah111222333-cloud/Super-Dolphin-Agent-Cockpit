package sqlite

import (
	"database/sql"
	"testing"
)

// assertAccessWaitRelationTriggers 确认 incarnation 不能与其他 reservation/agent 拼接。
func assertAccessWaitRelationTriggers(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO orchestration_access_grants
SELECT 'grant-bad-relation', caller_principal_ref, scope_ref, source_agent_id, target_agent_id,
       source_reservation_id, source_reservation_generation, source_incarnation_id, source_incarnation_generation,
       source_binding_version, source_binding_digest, source_fence_ref, target_reservation_id, target_reservation_generation,
       'incarnation-src', target_incarnation_generation, target_binding_version, target_binding_digest, target_fence_ref,
       can_read, can_send, 'ns-bad-relation', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', request_digest,
       1, 'active', issued_at, expires_at, created_at, updated_at, schema_version
FROM orchestration_access_grants WHERE grant_id = 'grant-active'`); err == nil {
		t.Fatal("grant with mismatched target incarnation relation unexpectedly succeeded")
	}
	if _, err := db.Exec(`INSERT INTO orchestration_event_wait_handles
SELECT 'wait-bad-relation', grant_id, grant_revision, caller_principal_ref, scope_ref, source_agent_id, target_agent_id,
       target_reservation_id, target_reservation_generation, 'incarnation-src', target_incarnation_generation,
       target_binding_version, target_binding_digest, target_fence_ref, source_id, source_epoch, lane, selector_version,
       selector_json, selector_digest, event_types_json, after_source_sequence, after_cursor, deadline_at,
       'ns-wait-bad-relation', 'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', request_digest,
       1, 'pending', '', 0, '', '', created_at, updated_at, schema_version
FROM orchestration_event_wait_handles WHERE wait_handle_id = 'wait-1'`); err == nil {
		t.Fatal("wait with mismatched target incarnation relation unexpectedly succeeded")
	}
}
