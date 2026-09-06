package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"
)

const commandAttemptMigration = "135_orchestration_command_attempts.sql"

func TestCommandAttemptMigrationContract(t *testing.T) {
	t.Run("schema", TestCommandAttemptMigrationConstrainsSchema)
	t.Run("intent_binding", TestCommandAttemptMigrationBindsToIntent)
	t.Run("immutable_identity_and_delete", TestCommandAttemptMigrationProtectsIdentityAndDelete)
	t.Run("outcome_one_hot", TestCommandAttemptMigrationEnforcesOneHotOutcomes)
	t.Run("outcome_transitions", TestCommandAttemptMigrationEnforcesOutcomeTransitions)
	t.Run("marker_and_version", TestCommandAttemptMigrationRecordsMarkerAndLatestVersion)
}

func openCommandAttemptMigrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigrationTestDB(t)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func TestCommandAttemptMigrationConstrainsSchema(t *testing.T) {
	db := openCommandAttemptMigrationDB(t)
	assertTableSQLContains(t, db, "orchestration_command_attempts", []string{"STRICT", "PRIMARY KEY", "UNIQUE", "effect_pending", "provider_definitive_failure"})
	want := []string{
		"command_id", "attempt_id", "wakeup_id", "wakeup_fence",
		"idempotency_namespace", "idempotency_key_digest", "request_digest",
		"attempt_number", "dispatch_receipt", "permit", "nonce", "phase", "outcome",
		"provider_submission_id", "provider_receipt_id", "failure_code", "recorded_at",
	}
	if got := tableColumns(t, db, "orchestration_command_attempts"); !slicesEqual(got, want) {
		t.Fatalf("command attempt columns = %v, want %v", got, want)
	}
	assertNotNullColumns(t, db, "orchestration_command_attempts", want)
	assertPrimaryKey(t, db, "orchestration_command_attempts", []string{"attempt_id"})
	assertForeignKeys(t, db, "orchestration_command_attempts", []foreignKeyContract{{Column: "command_id", Table: "orchestration_command_intents"}})
	assertIndex(t, db, "orchestration_command_attempts", "idx_orchestration_command_attempts_command", false, "")
	for _, trigger := range []string{
		"trg_orchestration_command_attempts_intent_binding",
		"trg_orchestration_command_attempts_immutable_update",
		"trg_orchestration_command_attempts_immutable_delete",
		"trg_orchestration_command_attempts_outcome_transition",
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?", trigger).Scan(&count); err != nil {
			t.Fatalf("read trigger %s: %v", trigger, err)
		}
		if count != 1 {
			t.Fatalf("trigger %s count = %d, want 1", trigger, count)
		}
	}
}

func TestCommandAttemptMigrationBindsToIntent(t *testing.T) {
	db := openCommandAttemptMigrationDB(t)
	valid := newCommandAttemptFixture("binding-valid")
	insertCommandIntent(t, db, valid.intent)
	if _, err := insertCommandAttemptResult(db, valid); err != nil {
		t.Fatalf("matching command intent rejected: %v", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*commandAttemptFixture)
	}{
		{name: "namespace", mutate: func(v *commandAttemptFixture) { v.idempotencyNamespace = "other-namespace" }},
		{name: "key digest", mutate: func(v *commandAttemptFixture) { v.idempotencyKeyDigest = digestFor(v.attemptID + ":other-key") }},
		{name: "request digest", mutate: func(v *commandAttemptFixture) { v.requestDigest = digestFor(v.attemptID + ":other-request") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := newCommandAttemptFixture("binding-" + strings.ReplaceAll(tc.name, " ", "-"))
			insertCommandIntent(t, db, candidate.intent)
			tc.mutate(&candidate)
			if _, err := insertCommandAttemptResult(db, candidate); err == nil {
				t.Fatalf("intent mismatch %s was accepted", tc.name)
			}
		})
	}

	missing := newCommandAttemptFixture("binding-missing-intent")
	if _, err := insertCommandAttemptResult(db, missing); err == nil {
		t.Fatal("attempt without a command intent was accepted")
	}
}

func TestCommandAttemptMigrationProtectsIdentityAndDelete(t *testing.T) {
	db := openCommandAttemptMigrationDB(t)
	fixture := newCommandAttemptFixture("immutable")
	insertCommandIntent(t, db, fixture.intent)
	if _, err := insertCommandAttemptResult(db, fixture); err != nil {
		t.Fatalf("insert immutable fixture: %v", err)
	}
	for _, column := range []string{
		"command_id", "attempt_id", "wakeup_id", "wakeup_fence", "idempotency_namespace",
		"idempotency_key_digest", "request_digest", "attempt_number", "dispatch_receipt",
		"permit", "nonce", "recorded_at",
	} {
		t.Run("update_"+column, func(t *testing.T) {
			if _, err := db.Exec("UPDATE orchestration_command_attempts SET "+column+" = "+column+" WHERE attempt_id = ?", fixture.attemptID); err == nil {
				t.Fatalf("immutable identity column %s accepted UPDATE", column)
			}
		})
	}
	if _, err := db.Exec("DELETE FROM orchestration_command_attempts WHERE attempt_id = ?", fixture.attemptID); err == nil {
		t.Fatal("immutable command attempt accepted DELETE")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM orchestration_command_attempts WHERE attempt_id = ?", fixture.attemptID).Scan(&count); err != nil {
		t.Fatalf("count immutable attempt: %v", err)
	}
	if count != 1 {
		t.Fatalf("immutable attempt row count = %d, want 1", count)
	}
}

func TestCommandAttemptMigrationEnforcesOneHotOutcomes(t *testing.T) {
	db := openCommandAttemptMigrationDB(t)
	cases := []struct {
		name, phase, outcome, submission, receipt, failure string
	}{
		{name: "pending with submission", phase: "effect_pending", outcome: "pending", submission: "submission"},
		{name: "unknown with failure", phase: "outcome_unknown", outcome: "unknown", failure: "failed"},
		{name: "accepted without provider material", phase: "provider_accepted", outcome: "accepted"},
		{name: "accepted with failure", phase: "provider_accepted", outcome: "accepted", submission: "submission", failure: "failed"},
		{name: "failure with receipt", phase: "provider_definitive_failure", outcome: "definitive_failure", receipt: "receipt"},
		{name: "failure without code", phase: "provider_definitive_failure", outcome: "definitive_failure"},
		{name: "phase outcome mismatch", phase: "effect_pending", outcome: "accepted", submission: "submission"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCommandAttemptFixture("one-hot-" + strings.ReplaceAll(tc.name, " ", "-"))
			fixture.phase, fixture.outcome = tc.phase, tc.outcome
			fixture.providerSubmissionID, fixture.providerReceiptID, fixture.failureCode = tc.submission, tc.receipt, tc.failure
			insertCommandIntent(t, db, fixture.intent)
			if _, err := insertCommandAttemptResult(db, fixture); err == nil {
				t.Fatalf("invalid one-hot outcome %s was accepted", tc.name)
			}
		})
	}
}

func TestCommandAttemptMigrationEnforcesOutcomeTransitions(t *testing.T) {
	db := openCommandAttemptMigrationDB(t)
	for _, tc := range []struct {
		name, fromPhase, fromOutcome, toPhase, toOutcome, submission, receipt, failure string
		wantErr                                                                        bool
	}{
		{name: "pending to unknown", fromPhase: "effect_pending", fromOutcome: "pending", toPhase: "outcome_unknown", toOutcome: "unknown"},
		{name: "pending to accepted", fromPhase: "effect_pending", fromOutcome: "pending", toPhase: "provider_accepted", toOutcome: "accepted", submission: "sub"},
		{name: "pending to failure", fromPhase: "effect_pending", fromOutcome: "pending", toPhase: "provider_definitive_failure", toOutcome: "definitive_failure", failure: "failed"},
		{name: "unknown to accepted", fromPhase: "outcome_unknown", fromOutcome: "unknown", toPhase: "provider_accepted", toOutcome: "accepted", receipt: "receipt"},
		{name: "unknown to failure", fromPhase: "outcome_unknown", fromOutcome: "unknown", toPhase: "provider_definitive_failure", toOutcome: "definitive_failure", failure: "failed"},
		{name: "accepted remains accepted", fromPhase: "provider_accepted", fromOutcome: "accepted", toPhase: "provider_accepted", toOutcome: "accepted", submission: "sub"},
		{name: "failure remains failure", fromPhase: "provider_definitive_failure", fromOutcome: "definitive_failure", toPhase: "provider_definitive_failure", toOutcome: "definitive_failure", failure: "failed"},
		{name: "unknown to pending", fromPhase: "outcome_unknown", fromOutcome: "unknown", toPhase: "effect_pending", toOutcome: "pending", wantErr: true},
		{name: "accepted to unknown", fromPhase: "provider_accepted", fromOutcome: "accepted", toPhase: "outcome_unknown", toOutcome: "unknown", wantErr: true},
		{name: "accepted to failure", fromPhase: "provider_accepted", fromOutcome: "accepted", toPhase: "provider_definitive_failure", toOutcome: "definitive_failure", failure: "failed", wantErr: true},
		{name: "failure to accepted", fromPhase: "provider_definitive_failure", fromOutcome: "definitive_failure", toPhase: "provider_accepted", toOutcome: "accepted", submission: "sub", wantErr: true},
		{name: "terminal material changes", fromPhase: "provider_accepted", fromOutcome: "accepted", toPhase: "provider_accepted", toOutcome: "accepted", receipt: "new-receipt", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newCommandAttemptFixture("transition-" + strings.ReplaceAll(tc.name, " ", "-"))
			fixture.phase, fixture.outcome = tc.fromPhase, tc.fromOutcome
			fixture.providerSubmissionID, fixture.providerReceiptID, fixture.failureCode = transitionMaterial(tc.fromPhase, tc.fromOutcome)
			insertCommandIntent(t, db, fixture.intent)
			if _, err := insertCommandAttemptResult(db, fixture); err != nil {
				t.Fatalf("insert transition fixture: %v", err)
			}
			_, err := db.Exec(`UPDATE orchestration_command_attempts
SET phase = ?, outcome = ?, provider_submission_id = ?, provider_receipt_id = ?, failure_code = ?
WHERE attempt_id = ?`, tc.toPhase, tc.toOutcome, tc.submission, tc.receipt, tc.failure, fixture.attemptID)
			if tc.wantErr && err == nil {
				t.Fatal("invalid outcome transition was accepted")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("valid outcome transition rejected: %v", err)
			}
		})
	}
}

func TestCommandAttemptMigrationRecordsMarkerAndLatestVersion(t *testing.T) {
	db := openCommandAttemptMigrationDB(t)
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, commandAttemptMigration, 1)
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, commandAttemptMigration, 1)
}

type commandAttemptFixture struct {
	intent                                               commandIntentFixture
	commandID, attemptID                                 string
	wakeupID, wakeupFence                                string
	idempotencyNamespace                                 string
	idempotencyKeyDigest                                 string
	requestDigest                                        string
	phase, outcome                                       string
	dispatchReceipt, permit, nonce                       string
	providerSubmissionID, providerReceiptID, failureCode string
}

func newCommandAttemptFixture(id string) commandAttemptFixture {
	keyDigest := digestFor(id + ":key")
	requestDigest := digestFor(id + ":request")
	return commandAttemptFixture{
		intent:    commandIntentFixture{CommandID: "attempt-command-" + id, CommandKind: "launch_agent", CallerPrincipalRef: "principal/attempt", ScopeRef: "scope/attempt", IdempotencyNamespace: "attempt-namespace-" + id, IdempotencyKeyDigest: keyDigest, RequestDigest: requestDigest, DigestKeyID: "key-" + id, RequestContentRef: "content-" + id, SourceFenceRef: "source-" + id, TargetFenceRef: "target-" + id, SchemaVersion: 1, CommandVersion: 1},
		commandID: "attempt-command-" + id, attemptID: "attempt-" + id, wakeupID: "wakeup-" + id, wakeupFence: "fence-" + id,
		idempotencyNamespace: "attempt-namespace-" + id, idempotencyKeyDigest: keyDigest, requestDigest: requestDigest,
		phase: "effect_pending", outcome: "pending", dispatchReceipt: "dispatch-" + id, permit: "permit-" + id, nonce: "nonce-" + id,
	}
}

func insertCommandAttemptResult(db *sql.DB, fixture commandAttemptFixture) (sql.Result, error) {
	return db.Exec(`INSERT INTO orchestration_command_attempts (
	command_id, attempt_id, wakeup_id, wakeup_fence, idempotency_namespace,
	idempotency_key_digest, request_digest, attempt_number, dispatch_receipt, permit, nonce,
	phase, outcome, provider_submission_id, provider_receipt_id, failure_code
) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?)`,
		fixture.commandID, fixture.attemptID, fixture.wakeupID, fixture.wakeupFence, fixture.idempotencyNamespace,
		fixture.idempotencyKeyDigest, fixture.requestDigest, fixture.dispatchReceipt, fixture.permit, fixture.nonce,
		fixture.phase, fixture.outcome, fixture.providerSubmissionID, fixture.providerReceiptID, fixture.failureCode)
}

func transitionMaterial(phase, outcome string) (string, string, string) {
	if phase == "provider_accepted" {
		return "sub", "", ""
	}
	if phase == "provider_definitive_failure" {
		return "", "", "failed"
	}
	return "", "", ""
}

func digestFor(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
