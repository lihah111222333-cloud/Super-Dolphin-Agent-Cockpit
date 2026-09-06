package unifiedv2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

type inspectorReaderStub struct {
	submission orchestrationcommand.DurableSubmission
	intent     orchestrationcommand.Record
	dbtx       storedb.DBTX
}

func (s *inspectorReaderStub) GetTx(_ context.Context, dbtx storedb.DBTX, _ orchestrationcommand.DurableSubmissionKey) (orchestrationcommand.DurableSubmission, error) {
	s.dbtx = dbtx
	return s.submission, nil
}

func (s *inspectorReaderStub) GetIntentTx(_ context.Context, dbtx storedb.DBTX, _ string) (orchestrationcommand.Record, error) {
	if dbtx != s.dbtx {
		return orchestrationcommand.Record{}, errors.New("split transaction")
	}
	return s.intent, nil
}

type inspectorUnknownDBTX struct{}

func (inspectorUnknownDBTX) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil
}
func (inspectorUnknownDBTX) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, nil
}
func (inspectorUnknownDBTX) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}
func (inspectorUnknownDBTX) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func inspectorFixture(resolution orchestrationcommand.AcceptResolution) (CommandSubmissionInspectInput, orchestrationcommand.DurableSubmission, orchestrationcommand.Record) {
	digest := strings.Repeat("a", 64)
	req := orchestrationcommand.DurableSubmissionRequest{CommandID: "command-1", CommandVersion: 1, SubmissionID: "submission-1", SubjectSelector: "agent:one", CallerPrincipalRef: "principal/one", ScopeRef: "scope/one", SourceFenceRef: "source/one", TargetFenceRef: "target/one", IdempotencyNamespace: "send/one", IdempotencyKeyDigest: digest, RequestDigest: strings.Repeat("b", 64), PendingCursor: "cursor/one", PendingCursorDigest: strings.Repeat("c", 64)}
	intent := orchestrationcommand.Record{Intent: orchestrationcommand.Intent{CommandID: req.CommandID, CommandKind: "send_message", CallerPrincipalRef: req.CallerPrincipalRef, ScopeRef: req.ScopeRef, IdempotencyNamespace: req.IdempotencyNamespace, IdempotencyKeyDigest: req.IdempotencyKeyDigest, RequestDigest: req.RequestDigest, DigestKeyID: "key/one", RequestContentRef: "content/one", SourceFenceRef: req.SourceFenceRef, TargetFenceRef: req.TargetFenceRef}, CommandVersion: 1, SchemaVersion: 1}
	input := CommandSubmissionInspectInput{CommandID: req.CommandID, CommandVersion: req.CommandVersion, SubmissionID: req.SubmissionID, SubjectSelector: req.SubjectSelector, CallerPrincipalRef: req.CallerPrincipalRef, ScopeRef: req.ScopeRef, SourceFenceRef: req.SourceFenceRef, TargetFenceRef: req.TargetFenceRef, IdempotencyNamespace: req.IdempotencyNamespace, IdempotencyKeyDigest: req.IdempotencyKeyDigest, RequestDigest: req.RequestDigest}
	kind := orchestrationcommand.ResolutionPending
	if resolution == orchestrationcommand.AcceptAccepted {
		kind = orchestrationcommand.ResolutionAccepted
	} else if resolution == orchestrationcommand.AcceptAborted {
		kind = orchestrationcommand.ResolutionRejected
	}
	return input, orchestrationcommand.DurableSubmission{Request: req, Resolution: resolution, ResolutionKind: kind, Revision: 2}, intent
}

func TestCommandSubmissionInspectorProjectsPendingAsIndeterminate(t *testing.T) {
	input, submission, intent := inspectorFixture(orchestrationcommand.AcceptPending)
	reader := &inspectorReaderStub{submission: submission, intent: intent}
	inspector, err := NewCommandSubmissionInspector(reader)
	if err != nil {
		t.Fatal(err)
	}
	tx := &sql.Tx{}
	got, err := inspector.Inspect(context.Background(), tx, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "indeterminate" || got.IndeterminateReason != "accept_resolution_pending" || got.Accepted != nil || got.Rejected != nil || got.ExecutionTerminal {
		t.Fatalf("pending projection = %#v", got)
	}
}

func TestCommandSubmissionInspectorProjectsUnknownAsIndeterminate(t *testing.T) {
	input, submission, intent := inspectorFixture(orchestrationcommand.AcceptPending)
	submission.Resolution = orchestrationcommand.AcceptResolution("unknown")
	reader := &inspectorReaderStub{submission: submission, intent: intent}
	inspector, _ := NewCommandSubmissionInspector(reader)
	got, err := inspector.Inspect(context.Background(), &sql.Tx{}, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "indeterminate" || got.IndeterminateReason != "accept_resolution_unknown" || got.Accepted != nil || got.Rejected != nil {
		t.Fatalf("unknown projection = %#v", got)
	}
}

func TestCommandSubmissionInspectorAcceptedIsAdmissionOnly(t *testing.T) {
	input, submission, intent := inspectorFixture(orchestrationcommand.AcceptAccepted)
	submission.AcceptedCommandFence = orchestrationcommand.CommandFence{Ref: "accept/fence", Digest: strings.Repeat("d", 64), Cursor: "accept/cursor", CommandID: input.CommandID, CommandVersion: 1, SubjectSelector: input.SubjectSelector}
	submission.AcceptReceiptRef, submission.AcceptReceiptDigest = "accept/receipt", strings.Repeat("e", 64)
	submission.RecoveryReceiptRef, submission.RecoveryReceiptDigest = "recovery/receipt", strings.Repeat("f", 64)
	reader := &inspectorReaderStub{submission: submission, intent: intent}
	inspector, _ := NewCommandSubmissionInspector(reader)
	got, err := inspector.Inspect(context.Background(), &sql.Tx{}, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "accepted" || !got.AdmissionOnly || got.ExecutionTerminal || got.Accepted == nil || got.Rejected != nil {
		t.Fatalf("accepted projection = %#v", got)
	}
}

func TestCommandSubmissionInspectorRejectedProjectsAuthoritativeFence(t *testing.T) {
	input, submission, intent := inspectorFixture(orchestrationcommand.AcceptAborted)
	submission.RejectedCommandFence = orchestrationcommand.CommandFence{Ref: "reject/fence", Digest: strings.Repeat("d", 64), Cursor: "reject/cursor", CommandID: input.CommandID, CommandVersion: 1, SubjectSelector: input.SubjectSelector}
	submission.RejectReceiptRef, submission.RejectReceiptDigest = "reject/receipt", strings.Repeat("e", 64)
	submission.RecoveryReceiptRef, submission.RecoveryReceiptDigest = "recovery/receipt", strings.Repeat("f", 64)
	reader := &inspectorReaderStub{submission: submission, intent: intent}
	inspector, _ := NewCommandSubmissionInspector(reader)
	got, err := inspector.Inspect(context.Background(), &sql.Tx{}, input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "rejected" || got.Rejected == nil || got.Rejected.CommandFence.Ref != "reject/fence" || got.Accepted != nil {
		t.Fatalf("rejected projection = %#v", got)
	}
}

func TestCommandSubmissionInspectorRejectsForeignReturnedFence(t *testing.T) {
	input, submission, intent := inspectorFixture(orchestrationcommand.AcceptAccepted)
	submission.AcceptedCommandFence = orchestrationcommand.CommandFence{Ref: "accept/fence", Digest: strings.Repeat("d", 64), Cursor: "accept/cursor", CommandID: "other-command", CommandVersion: 1, SubjectSelector: input.SubjectSelector}
	reader := &inspectorReaderStub{submission: submission, intent: intent}
	inspector, _ := NewCommandSubmissionInspector(reader)
	if _, err := inspector.Inspect(context.Background(), &sql.Tx{}, input); !errors.Is(err, ErrCommandSubmissionInspectorForeignFence) {
		t.Fatalf("foreign returned fence error = %v", err)
	}
}

func TestCommandSubmissionInspectorRejectsMismatchAndForeignFence(t *testing.T) {
	input, submission, intent := inspectorFixture(orchestrationcommand.AcceptPending)
	reader := &inspectorReaderStub{submission: submission, intent: intent}
	inspector, _ := NewCommandSubmissionInspector(reader)
	foreign := input
	foreign.SourceFenceRef = "source/foreign"
	if _, err := inspector.Inspect(context.Background(), &sql.Tx{}, foreign); !errors.Is(err, ErrCommandSubmissionInspectorForeignFence) {
		t.Fatalf("foreign fence error = %v", err)
	}
	mismatch := input
	mismatch.RequestDigest = strings.Repeat("f", 64)
	if _, err := inspector.Inspect(context.Background(), &sql.Tx{}, mismatch); !errors.Is(err, ErrCommandSubmissionInspectorMismatch) {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestCommandSubmissionInspectorRejectsNonTransactionCarrier(t *testing.T) {
	input, submission, intent := inspectorFixture(orchestrationcommand.AcceptPending)
	inspector, _ := NewCommandSubmissionInspector(&inspectorReaderStub{submission: submission, intent: intent})
	if _, err := inspector.Inspect(context.Background(), inspectorUnknownDBTX{}, input); !errors.Is(err, ErrCommandSubmissionInspectorUnavailable) {
		t.Fatalf("unknown DBTX error = %v", err)
	}
}

func TestCommandSubmissionInspectorJSONIsStrict(t *testing.T) {
	input, _, _ := inspectorFixture(orchestrationcommand.AcceptPending)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCommandSubmissionInspectInput(append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
	if _, err := DecodeCommandSubmissionInspectInput([]byte("null")); err == nil {
		t.Fatal("null JSON was accepted")
	}
}
