package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

func TestWaitOperationCleanupKeepsResourcesAcrossBindingRollover(t *testing.T) {
	db, waiting := setupCleanupRolloverJournal(t)
	cleanup := assertCleanupRolloverAppend(t, db, waiting)
	cleared := assertCleanupRequiresDurableResources(t, db, cleanup)
	assertCleanupCannotDropResources(t, db, cleared)
}

// setupCleanupRolloverJournal 建立跨 binding rollover 的清理前置 journal。
func setupCleanupRolloverJournal(t *testing.T) (*sql.DB, waitTargetFixture) {
	t.Helper()
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-cleanup-rollover", 1)
	insertWaitOperationGrant(t, db, "grant-good", true)
	insertWaitOperationHandle(t, db, "wait-good", "grant-good", true)
	initial := newWaitTarget("op-cleanup-rollover", 1)
	initial.selectorDigest = digestFor("wait-good:selector")
	initial.originalGrantID, initial.grantNamespace = "grant-good", "grant-namespace"
	initial.grantKeyDigest, initial.grantRequestDigest = strings.Repeat("d", 64), strings.Repeat("e", 64)
	if err := insertWaitTarget(db, initial); err != nil {
		t.Fatalf("insert initial target: %v", err)
	}
	grantPendingPhase := initial
	grantPendingPhase.version, grantPendingPhase.previousRecordDigest = 2, initial.recordDigest
	grantPendingPhase.recordDigest, grantPendingPhase.phase = digestFor("cleanup-grant-pending-phase"), "grant_pending"
	if err := insertWaitTarget(db, grantPendingPhase); err != nil {
		t.Fatalf("insert grant-pending phase: %v", err)
	}
	grantPending := grantPendingPhase
	grantPending.version, grantPending.previousRecordDigest = 3, grantPendingPhase.recordDigest
	grantPending.recordDigest = digestFor("cleanup-grant-pending")
	grantPending.resolvedGrantID, grantPending.grantRevision, grantPending.grantStatus = stringPtr("grant-good"), 1, "active"
	grantPending.waitStatus, grantPending.phase = "planned", "wait_pending"
	if err := insertWaitTarget(db, grantPending); err != nil {
		t.Fatalf("insert grant-backed target: %v", err)
	}
	waiting := grantPending
	waiting.version, waiting.previousRecordDigest = 4, grantPending.recordDigest
	waiting.recordDigest, waiting.resolvedWaitHandleID = digestFor("cleanup-waiting"), stringPtr("wait-good")
	waiting.waitRevision, waiting.waitStatus, waiting.phase = 1, "pending", "waiting"
	if err := insertWaitTarget(db, waiting); err != nil {
		t.Fatalf("insert waiting target: %v", err)
	}
	return db, waiting
}

// assertCleanupRolloverAppend 验证 rollover 后只允许 cleanup_pending 继续追加。
func assertCleanupRolloverAppend(t *testing.T, db *sql.DB, waiting waitTargetFixture) waitTargetFixture {
	t.Helper()
	if err := identityExec(t, db, identityCurrentBindingInsert,
		"agent-b", 2, "reservation-b", "incarnation-b", "native_subagent", 1, 1,
		"native-subagent-b", "source-fence-b", "active-process-b-2", strings.Repeat("8", 64), "revoked", 1); err != nil {
		t.Fatalf("insert target binding rollover: %v", err)
	}
	nonCleanup := waiting
	nonCleanup.version, nonCleanup.previousRecordDigest = 5, waiting.recordDigest
	nonCleanup.recordDigest = digestFor("post-rollover-non-cleanup")
	if err := insertWaitTarget(db, nonCleanup); err == nil {
		t.Fatal("non-cleanup append on stale binding was accepted")
	}
	cleanup := waiting
	cleanup.version, cleanup.previousRecordDigest = 5, waiting.recordDigest
	cleanup.recordDigest = digestFor("cleanup-pending")
	cleanup.grantCleanupState, cleanup.waitCleanupState = "pending", "pending"
	cleanup.phase, cleanup.uncertainPhase = "cleanup_pending", "cleanup"
	if err := insertWaitTarget(db, cleanup); err != nil {
		t.Fatalf("cleanup after binding rollover: %v", err)
	}
	return cleanup
}

// assertCleanupRequiresDurableResources 验证清理完成前必须先持久化资源终态。
func assertCleanupRequiresDurableResources(t *testing.T, db *sql.DB, cleanup waitTargetFixture) waitTargetFixture {
	t.Helper()
	cleared := cleanup
	cleared.version, cleared.previousRecordDigest = 6, cleanup.recordDigest
	cleared.recordDigest = digestFor("cleanup-cleared")
	cleared.grantCleanupState, cleared.waitCleanupState = "cleared", "cleared"
	cleared.resolvedAt, cleared.phase = "2030-01-01T00:11:00Z", "cleanup_cleared"
	if err := insertWaitTarget(db, cleared); err == nil {
		t.Fatal("cleanup cleared retained active grant/pending wait")
	}
	if _, err := db.Exec(`UPDATE orchestration_event_wait_handles
SET revision = 2, status = 'cancelled', resolved_at = '2030-01-01T00:10:00Z', updated_at = '2030-01-01T00:10:00Z'
WHERE wait_handle_id = 'wait-good' AND revision = 1 AND status = 'pending'`); err != nil {
		t.Fatalf("cancel wait for cleanup: %v", err)
	}
	if _, err := db.Exec(`UPDATE orchestration_access_grants
SET revision = 2, status = 'revoked', updated_at = '2030-01-01T00:10:00Z'
WHERE grant_id = 'grant-good' AND revision = 1 AND status = 'active'`); err != nil {
		t.Fatalf("revoke grant for cleanup: %v", err)
	}
	cleared.grantRevision, cleared.grantStatus = 2, "revoked"
	cleared.waitRevision, cleared.waitStatus = 2, "cancelled"
	if err := insertWaitTarget(db, cleared); err != nil {
		t.Fatalf("cleanup cleared after durable resource cleanup: %v", err)
	}
	return cleared
}

// assertCleanupCannotDropResources 验证清理 journal 不可删除已解析资源引用。
func assertCleanupCannotDropResources(t *testing.T, db *sql.DB, cleared waitTargetFixture) {
	t.Helper()
	dropped := cleared
	dropped.version = 7
	dropped.previousRecordDigest = cleared.recordDigest
	dropped.recordDigest = digestFor("cleanup-dropped-resource")
	dropped.resolvedGrantID, dropped.resolvedWaitHandleID = nil, nil
	if err := insertWaitTarget(db, dropped); err == nil {
		t.Fatal("cleanup append dropping resolved resources was accepted")
	}

}

func TestWaitOperationReadyHeadAllowsOnlyCompatibleTerminalAppend(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-ready-stability", 1)
	insertWaitOperationGrant(t, db, "grant-ready", true)
	insertWaitOperationHandle(t, db, "wait-ready", "grant-ready", true)
	waiting := newWaitTarget("op-ready-stability", 1)
	waiting.selectorDigest = digestFor("wait-ready:selector")
	waiting.originalGrantID, waiting.grantNamespace = "grant-ready", "grant-namespace"
	waiting.grantKeyDigest, waiting.grantRequestDigest = strings.Repeat("d", 64), strings.Repeat("e", 64)
	if err := insertWaitTarget(db, waiting); err != nil {
		t.Fatalf("insert planned seed: %v", err)
	}
	grantPendingPhase := waiting
	grantPendingPhase.version = 2
	grantPendingPhase.previousRecordDigest = waiting.recordDigest
	grantPendingPhase.recordDigest = digestFor("ready-grant-pending-phase")
	grantPendingPhase.phase = "grant_pending"
	if err := insertWaitTarget(db, grantPendingPhase); err != nil {
		t.Fatalf("insert grant-pending phase: %v", err)
	}
	grantPending := grantPendingPhase
	grantPending.version = 3
	grantPending.previousRecordDigest = grantPendingPhase.recordDigest
	grantPending.recordDigest = digestFor("ready-grant-pending")
	grantPending.originalGrantID, grantPending.resolvedGrantID = "grant-ready", stringPtr("grant-ready")
	grantPending.grantNamespace, grantPending.grantKeyDigest, grantPending.grantRequestDigest = "grant-namespace", strings.Repeat("d", 64), strings.Repeat("e", 64)
	grantPending.grantRevision, grantPending.grantStatus = 1, "active"
	grantPending.waitStatus, grantPending.phase = "planned", "wait_pending"
	if err := insertWaitTarget(db, grantPending); err != nil {
		t.Fatalf("insert grant-backed seed: %v", err)
	}
	waiting = grantPending
	waiting.version = 4
	waiting.previousRecordDigest = grantPending.recordDigest
	waiting.recordDigest = digestFor("ready-waiting")
	waiting.resolvedWaitHandleID, waiting.waitRevision, waiting.waitStatus, waiting.phase = stringPtr("wait-ready"), 1, "pending", "waiting"
	if err := insertWaitTarget(db, waiting); err != nil {
		t.Fatalf("insert waiting seed: %v", err)
	}
	erased := waiting
	erased.version = 5
	erased.previousRecordDigest = waiting.recordDigest
	erased.recordDigest = digestFor("waiting-to-unknown-erases-resources")
	erased.phase, erased.uncertainPhase = "outcome_unknown", "target_transition"
	erased.resolvedGrantID, erased.resolvedWaitHandleID = nil, nil
	erased.grantRevision, erased.grantStatus = 0, "planned"
	erased.waitRevision, erased.waitStatus = 0, "absent"
	if err := insertWaitTarget(db, erased); err == nil {
		t.Fatal("non-cleanup outcome_unknown dropped resolved resources")
	}
	if _, err := db.Exec(`UPDATE orchestration_wait_operations SET status = 'ready', revision = 2 WHERE operation_key = 'op-ready-stability'`); err != nil {
		t.Fatalf("ready status with waiting head: %v", err)
	}
	satisfyWaitOperationHandle(t, db, "wait-ready")
	terminal := waiting
	terminal.version = 5
	terminal.previousRecordDigest = waiting.recordDigest
	terminal.recordDigest = digestFor("ready-terminal")
	terminal.phase, terminal.grantStatus, terminal.waitStatus = "satisfied", "active", "satisfied"
	terminal.grantRevision, terminal.waitRevision = 1, 2
	terminal.resolvedGrantID, terminal.resolvedWaitHandleID = stringPtr("grant-ready"), stringPtr("wait-ready")
	terminal.resolvedAt = "2030-01-01T00:11:00Z"
	if err := insertWaitTarget(db, terminal); err != nil {
		t.Fatalf("ready head rejected compatible terminal append: %v", err)
	}
	unknown := terminal
	unknown.version = 6
	unknown.previousRecordDigest = terminal.recordDigest
	unknown.recordDigest = digestFor("ready-unknown-forged")
	unknown.phase, unknown.uncertainPhase = "outcome_unknown", "target_transition"
	if err := insertWaitTarget(db, unknown); err == nil {
		t.Fatal("ready head accepted incompatible outcome_unknown append")
	}
}

func TestWaitOperationTargetTransitionUnknownRequiresCoherentRecovery(t *testing.T) {
	db := openWaitOperationMigrationDB(t)
	insertWaitOperation(t, db, "op-unknown-recovery", 1)
	initial := newWaitTarget("op-unknown-recovery", 1)
	if err := insertWaitTarget(db, initial); err != nil {
		t.Fatalf("insert unknown seed: %v", err)
	}
	unknown := initial
	unknown.version = 2
	unknown.previousRecordDigest = initial.recordDigest
	unknown.recordDigest = digestFor("unknown-target-transition")
	unknown.phase, unknown.uncertainPhase = "outcome_unknown", "target_transition"
	if err := insertWaitTarget(db, unknown); err != nil {
		t.Fatalf("coherent target-transition unknown: %v", err)
	}
	directCleared := unknown
	directCleared.version = 3
	directCleared.previousRecordDigest = unknown.recordDigest
	directCleared.recordDigest = digestFor("unknown-direct-cleanup-cleared")
	directCleared.phase, directCleared.uncertainPhase = "cleanup_cleared", "cleanup"
	directCleared.grantCleanupState, directCleared.waitCleanupState = "cleared", "cleared"
	directCleared.resolvedAt = "2030-01-01T00:02:00Z"
	if err := insertWaitTarget(db, directCleared); err == nil {
		t.Fatal("outcome_unknown cleanup was allowed to skip cleanup_pending")
	}
	forged := unknown
	forged.version = 3
	forged.previousRecordDigest = unknown.recordDigest
	forged.recordDigest = digestFor("unknown-forged-reopen")
	forged.phase, forged.uncertainPhase = "waiting", ""
	forged.grantStatus, forged.waitStatus = "active", "pending"
	forged.grantRevision, forged.waitRevision = 1, 1
	forged.resolvedGrantID, forged.resolvedWaitHandleID = stringPtr("grant-missing"), stringPtr("wait-missing")
	if err := insertWaitTarget(db, forged); err == nil {
		t.Fatal("target-transition unknown accepted unproven recovery branch")
	}
}
