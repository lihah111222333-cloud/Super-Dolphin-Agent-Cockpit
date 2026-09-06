package orchestrationcommand

import (
	"context"
	"errors"
	"strings"
	"testing"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestDurableSubmissionSchemaRejectsPendingRecoveryAndMutableTerminalTime(t *testing.T) {
	db := openCommandIntentTestDB(t)
	if err := EnsureCandidateSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	intent := commandIntentFixture("submission-schema-contract")
	if _, err := storeIntentForCandidate(db, intent); err != nil {
		t.Fatal(err)
	}
	values := durableSubmissionFixture(intent.CommandID)
	values.IdempotencyNamespace = intent.IdempotencyNamespace
	values.IdempotencyKeyDigest = intent.IdempotencyKeyDigest
	values.RequestDigest = intent.RequestDigest
	if _, err := db.Exec(`INSERT INTO orchestration_command_submissions (command_id, command_version, submission_id, subject_selector, caller_principal_ref, scope_ref, source_fence_ref, target_fence_ref, idempotency_namespace, idempotency_key_digest, request_digest, pending_cursor, pending_cursor_digest, recovery_receipt_ref, recovery_receipt_digest) VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, values.CommandID, "schema-pending", values.SubjectSelector, values.CallerPrincipalRef, values.ScopeRef, values.SourceFenceRef, values.TargetFenceRef, values.IdempotencyNamespace, values.IdempotencyKeyDigest, values.RequestDigest, values.PendingCursor, values.PendingCursorDigest, "recovery:forbidden", strings.Repeat("d", 64)); err == nil {
		t.Fatal("pending row accepted recovery receipt")
	}
	store, err := NewDurableSubmissionStore(db, allowSubmissionAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.Prepare(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE orchestration_command_submissions SET caller_principal_ref = 'forged' WHERE command_id = ?`, values.CommandID); err == nil {
		t.Fatal("submission caller binding mutation was accepted")
	}
	resolution := SubmissionResolution{Request: prepared.Request, Resolution: AcceptAccepted, AcceptedCommandFence: durableCommandFence(prepared.Request, "schema-accept"), AcceptReceiptRef: "accept:schema", AcceptReceiptDigest: strings.Repeat("e", 64), RecoveryReceiptRef: "recovery:schema", RecoveryReceiptDigest: strings.Repeat("f", 64), ExpectedRevision: 1}
	if _, err := store.Resolve(context.Background(), resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE orchestration_command_submissions SET updated_at = 'BAD' WHERE command_id = ?`, values.CommandID); err == nil {
		t.Fatal("terminal updated_at mutation was accepted")
	}
}

func TestEnsureCandidateSchemaSupportsGeneratedSQLCShape(t *testing.T) {
	db := openCommandIntentTestDB(t)
	if err := EnsureCandidateSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	intent := commandIntentFixture("submission-sqlc-shape")
	if _, err := storeIntentForCandidate(db, intent); err != nil {
		t.Fatal(err)
	}
	request := durableSubmissionFixture(intent.CommandID)
	request.SubmissionID = "submission-sqlc-shape"
	request.IdempotencyNamespace = intent.IdempotencyNamespace
	request.IdempotencyKeyDigest = intent.IdempotencyKeyDigest
	request.RequestDigest = intent.RequestDigest
	queries := storedb.New(db)
	created, err := queries.CreateOrchestrationCommandSubmission(context.Background(), storedb.CreateOrchestrationCommandSubmissionParams{
		CommandID:            request.CommandID,
		SubmissionID:         request.SubmissionID,
		SubjectSelector:      request.SubjectSelector,
		CallerPrincipalRef:   request.CallerPrincipalRef,
		ScopeRef:             request.ScopeRef,
		SourceFenceRef:       request.SourceFenceRef,
		TargetFenceRef:       request.TargetFenceRef,
		IdempotencyNamespace: request.IdempotencyNamespace,
		IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		RequestDigest:        request.RequestDigest,
		PendingCursor:        request.PendingCursor,
		PendingCursorDigest:  request.PendingCursorDigest,
	})
	if err != nil {
		t.Fatalf("generated SQLC create candidate submission: %v", err)
	}
	if created.CommandID != request.CommandID || created.SubmissionID != request.SubmissionID || created.AcceptResolution != string(AcceptPending) {
		t.Fatalf("generated SQLC row = %#v, want pending submission for %q", created, request.CommandID)
	}
	loaded, err := queries.GetOrchestrationCommandSubmissionByID(context.Background(), storedb.GetOrchestrationCommandSubmissionByIDParams{CommandID: request.CommandID, SubmissionID: request.SubmissionID})
	if err != nil {
		t.Fatalf("generated SQLC get candidate submission: %v", err)
	}
	if loaded.RequestDigest != request.RequestDigest || loaded.PendingCursorDigest != request.PendingCursorDigest {
		t.Fatalf("generated SQLC loaded row = %#v, want request digests", loaded)
	}
}

func TestDurableSubmissionMapperRejectsMalformedTimestampAndReceiptShape(t *testing.T) {
	base := storedb.OrchestrationCommandSubmission{
		CommandID: "mapper-command", CommandVersion: 1, SubmissionID: "mapper-submission", SubjectSelector: "agent:one",
		CallerPrincipalRef: "principal", ScopeRef: "scope", SourceFenceRef: "source", TargetFenceRef: "target",
		IdempotencyNamespace: "namespace", IdempotencyKeyDigest: strings.Repeat("a", 64), RequestDigest: strings.Repeat("b", 64),
		PendingCursor: "cursor:1", PendingCursorDigest: strings.Repeat("c", 64), AcceptResolution: "pending", ResolutionKind: "pending", Revision: 1,
		RecordedAt: "BAD", UpdatedAt: "BAD",
	}
	if _, err := mapDurableSubmission(base); !errors.Is(err, ErrDurableSubmissionConflict) {
		t.Fatalf("malformed timestamp error = %v", err)
	}
	base.RecordedAt, base.UpdatedAt = "2026-09-04T00:00:00Z", "2026-09-04T00:00:00Z"
	base.RecoveryReceiptRef, base.RecoveryReceiptDigest = "recovery:orphan", strings.Repeat("d", 64)
	if _, err := mapDurableSubmission(base); !errors.Is(err, ErrDurableSubmissionConflict) {
		t.Fatalf("pending receipt shape error = %v", err)
	}
}

func TestEnsureCandidateSchemaRequiresForeignKeys(t *testing.T) {
	db := openCommandIntentTestDB(t)
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCandidateSchema(context.Background(), db); err == nil {
		t.Fatal("candidate schema installer accepted foreign_keys=OFF")
	}
}

func TestDurableSubmissionAuthorityFailureRollsBackResolution(t *testing.T) {
	db := openCommandIntentTestDB(t)
	if err := EnsureCandidateSchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	intent := commandIntentFixture("submission-schema-rollback")
	if _, err := storeIntentForCandidate(db, intent); err != nil {
		t.Fatal(err)
	}
	store, err := NewDurableSubmissionStore(db, allowSubmissionAuthority{err: errors.New("authority unavailable")})
	if err != nil {
		t.Fatal(err)
	}
	req := durableSubmissionFixture(intent.CommandID)
	req.IdempotencyNamespace, req.IdempotencyKeyDigest, req.RequestDigest = intent.IdempotencyNamespace, intent.IdempotencyKeyDigest, intent.RequestDigest
	prepared, err := store.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resolution := SubmissionResolution{Request: prepared.Request, Resolution: AcceptAborted, RejectedCommandFence: durableCommandFence(prepared.Request, "rollback-reject"), RejectReceiptRef: "reject:rollback", RejectReceiptDigest: strings.Repeat("d", 64), RecoveryReceiptRef: "recovery:rollback", RecoveryReceiptDigest: strings.Repeat("e", 64), ExpectedRevision: 1}
	if _, err := store.Resolve(context.Background(), resolution); !errors.Is(err, ErrDurableSubmissionAuthority) {
		t.Fatalf("authority failure = %v", err)
	}
	got, err := store.Get(context.Background(), DurableSubmissionKey{CommandID: intent.CommandID, SubmissionID: prepared.Request.SubmissionID})
	if err != nil || got.Resolution != AcceptPending {
		t.Fatalf("after rollback got=%#v err=%v, want pending", got, err)
	}
}
