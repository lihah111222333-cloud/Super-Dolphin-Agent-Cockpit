package room

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func ownerFixture(t *testing.T) (*Owner, Operation) {
	t.Helper()
	room := roomFixture()
	members := []Member{
		{PrincipalRef: "principal-1", Subject: "authenticated_subject", Role: MemberOwner, Active: true},
		{PrincipalRef: "target-a", Subject: "target-a", Role: MemberParticipant, Active: true},
		{PrincipalRef: "target-b", Subject: "target-b", Role: MemberParticipant, Active: true},
	}
	owner, err := NewFixtureOwner(room, members)
	if err != nil {
		t.Fatal(err)
	}
	op := operationFixture(OperationBroadcast)
	op.OperationID, op.IdempotencyKeyDigest = "broadcast-1", digest("key-1")
	var digestErr error
	op, digestErr = FinalizeOperationDigest(op)
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	return owner, op
}

func TestOwnerWithoutPrincipalVerifierFailsClosed(t *testing.T) {
	room := roomFixture()
	op := operationFixture(OperationBroadcast)
	op, _ = FinalizeOperationDigest(op)
	owner, err := NewOwner(room, []Member{{PrincipalRef: "principal-1", Subject: "ignored", Role: MemberOwner, Active: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Authorize(context.Background(), op); !errors.Is(err, ErrAuthorization) {
		t.Fatalf("missing verifier authorization=%v", err)
	}
	if _, err := owner.PlanFanout(context.Background(), op, []string{"target-a"}); !errors.Is(err, ErrAuthorization) {
		t.Fatalf("missing verifier plan=%v", err)
	}
}

func TestOwnerWithTypedNilPrincipalVerifierFailsClosed(t *testing.T) {
	room := roomFixture()
	var verifier *fixtureVerifier
	if _, err := NewOwnerWithVerifier(room, verifier); !errors.Is(err, ErrAuthorization) {
		t.Fatalf("typed nil verifier constructor error=%v", err)
	}
}

func ownerCandidate(owner *Owner, operation Operation, nextState string, commandRoot string, count int64) AdmissionCandidate {
	head := owner.arbiter.Head()
	return AdmissionCandidate{SchemaVersion: SchemaVersion, RoomID: operation.RoomID, OperationID: operation.OperationID,
		OperationVersion: operation.OperationVersion, OperationDigest: operation.RequestDigest, ExpectedGeneration: head.Generation,
		NextState: nextState, NextCustodyResolution: CustodyPending, DispatchGeneration: head.DispatchGeneration,
		PreviousInventoryRoot: head.CommandInventoryRoot, PreviousInventoryCount: head.CommandInventoryCount,
		CommandInventoryRoot: commandRoot, CommandInventoryCount: count}
}

func receipt(operation Operation, target, status string) TargetReceipt {
	value := TargetReceipt{SchemaVersion: SchemaVersion, OperationID: operation.OperationID, OperationDigest: operation.RequestDigest,
		TargetID: target, TargetMappingDigest: digest(target + ":mapping"), DeliveryBranch: "per_target", ResourceDecision: "reserved", Status: status}
	if status == ReceiptRejected {
		value.ResourceDecision, value.ErrorCode = "denied", "target_denied"
	} else if status == ReceiptOutcomeUnknown {
		value.ResourceDecision, value.ErrorCode, value.ReceiptID, value.ReceiptDigest = "quarantined", "effect_unknown", "attempt-1", digest(target+":attempt")
	} else {
		value.ReceiptID, value.ReceiptDigest = "receipt-"+target, digest(target+":receipt")
	}
	return value
}

func TestOwnerMembershipAndCrossRoomAuthorization(t *testing.T) {
	owner, operation := ownerFixture(t)
	if err := owner.Authorize(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	unauthorized := operation
	unauthorized.Principal.PrincipalRef = "unknown"
	unauthorized, _ = FinalizeOperationDigest(unauthorized)
	if err := owner.Authorize(context.Background(), unauthorized); !errors.Is(err, ErrAuthorization) {
		t.Fatalf("unknown member error=%v", err)
	}
	crossRoom := operation
	crossRoom.RoomID = "room-other"
	crossRoom, _ = FinalizeOperationDigest(crossRoom)
	if err := owner.Authorize(context.Background(), crossRoom); !errors.Is(err, ErrAuthorization) {
		t.Fatalf("cross-room error=%v", err)
	}
	plan, err := owner.PlanFanout(context.Background(), operation, []string{"target-a"})
	if err != nil || plan.RoomID != operation.RoomID {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if _, err := owner.PlanFanout(context.Background(), operation, []string{"target-a", "target-b"}); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("changed target denominator error=%v", err)
	}
}

func TestOwnerFanoutPerTargetAndUnknownCannotComplete(t *testing.T) {
	owner, operation := ownerFixture(t)
	plan, err := owner.PlanFanout(context.Background(), operation, []string{"target-a", "target-b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Admit(context.Background(), operation, ownerCandidate(owner, operation, StatusOpen, digest(strings.Join(plan.TargetIDs, ",")), 2)); err != nil {
		t.Fatal(err)
	}
	unknown := FanoutResult{SchemaVersion: SchemaVersion, RoomID: operation.RoomID, OperationID: operation.OperationID,
		OperationDigest: operation.RequestDigest, Status: FanoutPartial, PlannedTargetIDs: plan.TargetIDs,
		Receipts: []TargetReceipt{receipt(operation, "target-a", ReceiptAccepted), receipt(operation, "target-b", ReceiptOutcomeUnknown)}}
	if err := owner.FinalizeFanout(context.Background(), operation, unknown); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("unknown finalize error=%v", err)
	}
	if got := owner.arbiter.Head().CustodyResolution; got != CustodyPending {
		t.Fatalf("unknown changed custody=%q", got)
	}
	complete := unknown
	complete.Status = FanoutApplied
	complete.Receipts[1] = receipt(operation, "target-b", ReceiptAccepted)
	if err := owner.FinalizeFanout(context.Background(), operation, complete); err != nil {
		t.Fatal(err)
	}
	if got := owner.arbiter.Head().CustodyResolution; got != CustodyAvailable {
		t.Fatalf("complete custody=%q", got)
	}
	mutated := complete
	mutated.Receipts[0] = receipt(operation, "target-a", ReceiptRejected)
	mutated.Status = FanoutPartial
	if err := owner.FinalizeFanout(context.Background(), operation, mutated); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same operation changed receipts error=%v", err)
	}
}

func TestOwnerCloseSingleWinnerAbortAndLateReceipt(t *testing.T) {
	owner, operation := ownerFixture(t)
	plan, err := owner.PlanFanout(context.Background(), operation, []string{"target-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Admit(context.Background(), operation, ownerCandidate(owner, operation, StatusOpen, digest("fanout"), 1)); err != nil {
		t.Fatal(err)
	}
	if err := owner.FinalizeFanout(context.Background(), operation, FanoutResult{SchemaVersion: SchemaVersion, RoomID: operation.RoomID,
		OperationID: operation.OperationID, Status: FanoutApplied, PlannedTargetIDs: plan.TargetIDs,
		Receipts: []TargetReceipt{receipt(operation, "target-a", ReceiptAccepted)}}); err != nil {
		t.Fatal(err)
	}
	closeOp := operation
	closeOp.OperationID, closeOp.Kind, closeOp.IdempotencyKeyDigest = "close-1", OperationClose, digest("close-key")
	closeOp, err = FinalizeOperationDigest(closeOp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.BeginClose(context.Background(), closeOp, []string{"target-a"}); err != nil {
		t.Fatal(err)
	}
	assertCloseReplay(t, owner, closeOp)
	unknown := FanoutResult{SchemaVersion: SchemaVersion, RoomID: closeOp.RoomID, OperationID: closeOp.OperationID,
		PlannedTargetIDs: []string{"target-a"}, Status: FanoutPartial, Receipts: []TargetReceipt{receipt(closeOp, "target-a", ReceiptOutcomeUnknown)}}
	if err := owner.FinalizeClose(context.Background(), closeOp, unknown, "drain"); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("close unknown error=%v", err)
	}
	if err := owner.Abort(context.Background(), closeOp, digest("abort")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := owner.Snapshot()
	if err != nil || snapshot.Status != StatusOpen {
		t.Fatalf("abort snapshot=%+v err=%v", snapshot, err)
	}
}

func assertCloseReplay(t *testing.T, owner *Owner, closeOp Operation) {
	t.Helper()
	if replay, err := owner.BeginClose(context.Background(), closeOp, []string{"target-a"}); err != nil || replay.Winner {
		t.Fatalf("close replay=%+v err=%v", replay, err)
	}
}

func TestOwnerCloseFinalizeReplayAndAbortTerminalFailFast(t *testing.T) {
	owner, operation := ownerFixture(t)
	plan, err := owner.PlanFanout(context.Background(), operation, []string{"target-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Admit(context.Background(), operation, ownerCandidate(owner, operation, StatusOpen, digest("fanout-close"), 1)); err != nil {
		t.Fatal(err)
	}
	if err := owner.FinalizeFanout(context.Background(), operation, FanoutResult{SchemaVersion: SchemaVersion, RoomID: operation.RoomID, OperationID: operation.OperationID,
		Status: FanoutApplied, PlannedTargetIDs: plan.TargetIDs, Receipts: []TargetReceipt{receipt(operation, "target-a", ReceiptAccepted)}}); err != nil {
		t.Fatal(err)
	}
	closeOp := operation
	closeOp.OperationID, closeOp.Kind, closeOp.IdempotencyKeyDigest = "close-replay", OperationClose, digest("close-replay-key")
	closeOp, err = FinalizeOperationDigest(closeOp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.BeginClose(context.Background(), closeOp, []string{"target-a"}); err != nil {
		t.Fatal(err)
	}
	result := FanoutResult{SchemaVersion: SchemaVersion, RoomID: closeOp.RoomID, OperationID: closeOp.OperationID,
		Status: FanoutApplied, PlannedTargetIDs: []string{"target-a"}, Receipts: []TargetReceipt{receipt(closeOp, "target-a", ReceiptAccepted)}}
	if err := owner.FinalizeClose(context.Background(), closeOp, result, "done"); err != nil {
		t.Fatal(err)
	}
	if err := owner.FinalizeClose(context.Background(), closeOp, result, "done"); err != nil {
		t.Fatalf("exact close replay=%v", err)
	}
	assertForgedCloseReplaysRejected(t, owner, closeOp, result)
	assertLateCloseAbortRejected(t, owner, closeOp, operation)
}

func assertForgedCloseReplaysRejected(t *testing.T, owner *Owner, closeOp Operation, result FanoutResult) {
	t.Helper()
	for name, mutate := range map[string]func(*Operation){
		"unknown_operation": func(value *Operation) { value.OperationID = "not-recorded" },
		"forged_principal":  func(value *Operation) { value.Principal.PrincipalRef = "target-a" },
	} {
		t.Run(name, func(t *testing.T) {
			forged := closeOp
			mutate(&forged)
			forged, _ = FinalizeOperationDigest(forged)
			forgedResult := result
			if name == "unknown_operation" {
				forgedResult.OperationID = forged.OperationID
				forgedResult.Receipts = []TargetReceipt{receipt(forged, "target-a", ReceiptAccepted)}
			}
			if err := owner.FinalizeClose(context.Background(), forged, forgedResult, "done"); !errors.Is(err, ErrAuthorization) {
				t.Fatalf("forged closed replay=%v", err)
			}
		})
	}
}

func assertLateCloseAbortRejected(t *testing.T, owner *Owner, closeOp, operation Operation) {
	t.Helper()
	if err := owner.Abort(context.Background(), closeOp, digest("late-abort")); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("late abort=%v", err)
	}
	if err := owner.Abort(context.Background(), operation, digest("wrong-kind")); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("non-close abort=%v", err)
	}
}
