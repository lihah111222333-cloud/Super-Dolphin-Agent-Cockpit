package room

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestRoomAuthorizationRejectsUnsignedAndCrossScope(t *testing.T) {
	room := roomFixture()
	op := operationFixture(OperationBroadcast)
	if err := op.Authorize(room); err != nil {
		t.Fatalf("Authorize(valid) = %v", err)
	}
	for name, mutate := range map[string]func(*Operation){
		"unknown_actor": func(v *Operation) { v.Principal.ActorPrincipal = "agent" },
		"cross_scope":   func(v *Operation) { v.Principal.ScopeRef = "workspace/other" },
		"missing_grant": func(v *Operation) { v.Principal.GrantDigest = "" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := op
			mutate(&invalid)
			if invalid.Principal.GrantDigest == "" {
				if err := invalid.Authorize(room); !errors.Is(err, ErrAuthorization) {
					t.Fatalf("error=%v, want authorization", err)
				}
				return
			}
			finalized, err := FinalizeOperationDigest(invalid)
			if err != nil {
				t.Fatal(err)
			}
			if err := finalized.Authorize(room); !errors.Is(err, ErrAuthorization) {
				t.Fatalf("error=%v, want authorization", err)
			}
		})
	}
}

func TestRoomAuthorizationRejectsStaleDispatchGenerationForEveryOperation(t *testing.T) {
	room := roomFixture()
	for _, kind := range []string{OperationCreate, OperationJoin, OperationLeave, OperationGet, OperationClose, OperationExpire, OperationCleanup, OperationBroadcast, OperationRound} {
		t.Run(kind, func(t *testing.T) {
			op := operationFixture(kind)
			op.ExpectedDispatchGeneration++
			finalized, err := FinalizeOperationDigest(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := finalized.Authorize(room); !errors.Is(err, ErrAuthorization) {
				t.Fatalf("stale dispatch generation error=%v, want ErrAuthorization", err)
			}
		})
	}
}

func TestRoomAdmissionSingleWinnerAndReplay(t *testing.T) {
	arbiter, err := NewArbiter(headFixture())
	if err != nil {
		t.Fatal(err)
	}
	first := candidateFixture("op-a", "a")
	second := candidateFixture("op-b", "b")
	winners, conflicts := concurrentAdmissionCounts(t, arbiter, []AdmissionCandidate{first, second})
	if winners != 1 || conflicts != 1 {
		t.Fatalf("single-winner counts winners=%d conflicts=%d", winners, conflicts)
	}
	winning := arbiter.Head()
	replayCandidate := AdmissionCandidate{
		SchemaVersion: SchemaVersion, RoomID: winning.RoomID, OperationID: winning.OperationID,
		OperationVersion: 1, OperationDigest: winning.OperationDigest, ExpectedGeneration: winning.Generation - 1,
		NextState: winning.State, NextCustodyResolution: CustodyPending, DispatchGeneration: winning.DispatchGeneration,
		PreviousInventoryRoot: winning.PreviousInventoryRoot, PreviousInventoryCount: winning.PreviousInventoryCount,
		CommandInventoryRoot: winning.CommandInventoryRoot, CommandInventoryCount: winning.CommandInventoryCount,
	}
	replay, err := arbiter.TryAdmit(replayCandidate)
	if err != nil || replay.Winner {
		t.Fatalf("exact winner replay = %#v, %v; want non-winning replay", replay, err)
	}
	for name, mutate := range map[string]func(*AdmissionCandidate){
		"expected_generation": func(value *AdmissionCandidate) { value.ExpectedGeneration++ },
		"operation_version":   func(value *AdmissionCandidate) { value.OperationVersion++ },
		"next_state":          func(value *AdmissionCandidate) { value.NextState = StatusClosingPending },
		"dispatch_generation": func(value *AdmissionCandidate) { value.DispatchGeneration++ },
		"previous_root":       func(value *AdmissionCandidate) { value.PreviousInventoryRoot = digest("e") },
		"previous_count":      func(value *AdmissionCandidate) { value.PreviousInventoryCount++ },
		"command_root":        func(value *AdmissionCandidate) { value.CommandInventoryRoot = digest("f") },
		"command_count":       func(value *AdmissionCandidate) { value.CommandInventoryCount++ },
	} {
		t.Run("mutated_"+name, func(t *testing.T) {
			mutated := replayCandidate
			mutate(&mutated)
			if _, err := arbiter.TryAdmit(mutated); !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("mutated replay error=%v, want ErrOperationConflict", err)
			}
		})
	}
	conflict := candidateFixture(winning.OperationID, "f")
	if _, err := arbiter.TryAdmit(conflict); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same operation with different digest = %v, want ErrOperationConflict", err)
	}
}

func TestRoomArbiterHeadNilReceiverIsSafe(t *testing.T) {
	var arbiter *Arbiter
	if got := arbiter.Head(); got != (AdmissionHead{}) {
		t.Fatalf("nil Arbiter.Head()=%+v, want zero head", got)
	}
}

func TestRoomAbortResolutionRestoresPreviousInventory(t *testing.T) {
	arbiter, err := NewArbiter(headFixture())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFixture("close-op", "d")
	candidate.NextState = StatusClosingPending
	candidate.PreviousInventoryCount = 0
	if _, err := arbiter.TryAdmit(candidate); err != nil {
		t.Fatal(err)
	}
	if err := arbiter.Resolve(candidate.OperationID, candidate.OperationDigest, CustodyAborted, digest("r")); err != nil {
		t.Fatal(err)
	}
	assertAbortedRoomHead(t, arbiter, candidate)
	assertAbortedRoomReplay(t, arbiter, candidate)
}

func assertAbortedRoomHead(t *testing.T, arbiter *Arbiter, candidate AdmissionCandidate) {
	t.Helper()
	head := arbiter.Head()
	if head.State != StatusOpen || head.CustodyResolution != CustodyAborted || head.CommandInventoryRoot != head.PreviousInventoryRoot || head.CommandInventoryCount != head.PreviousInventoryCount {
		t.Fatalf("aborted head=%+v, want restored open inventory", head)
	}
	if err := arbiter.Resolve(candidate.OperationID, candidate.OperationDigest, CustodyAvailable, digest("s")); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("second resolution=%v, want conflict", err)
	}
}

func assertAbortedRoomReplay(t *testing.T, arbiter *Arbiter, candidate AdmissionCandidate) {
	t.Helper()
	replay, err := arbiter.TryAdmit(candidate)
	if err != nil || replay.Winner || replay.ReceiptDigest != digest("r") {
		t.Fatalf("aborted operation replay = %#v, %v; want original non-winning result", replay, err)
	}
	mutated := candidate
	mutated.CommandInventoryRoot = digest("s")
	if _, err := arbiter.TryAdmit(mutated); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("mutated aborted replay = %v, want ErrOperationConflict", err)
	}
}

func TestRoomFanoutRequiresIndependentTargetReceipts(t *testing.T) {
	accepted := TargetReceipt{SchemaVersion: SchemaVersion, TargetID: "target-a", TargetMappingDigest: digest("a"), DeliveryBranch: "per_target", ResourceDecision: "reserved", Status: ReceiptAccepted, ReceiptID: "receipt-a", ReceiptDigest: digest("b")}
	rejected := TargetReceipt{SchemaVersion: SchemaVersion, TargetID: "target-b", TargetMappingDigest: digest("c"), DeliveryBranch: "per_target", ResourceDecision: "denied", Status: ReceiptRejected, ErrorCode: "resource_exhausted"}
	partial := FanoutResult{SchemaVersion: SchemaVersion, RoomID: "room-1", OperationID: "op-1", Status: FanoutPartial, PlannedTargetIDs: []string{"target-a", "target-b"}, Receipts: []TargetReceipt{accepted, rejected}}
	if err := partial.Validate(); err != nil {
		t.Fatalf("partial.Validate() = %v", err)
	}
	partial.Receipts = []TargetReceipt{accepted, accepted}
	if !errors.Is(partial.Validate(), ErrTargetReceipt) {
		t.Fatalf("duplicate receipt accepted")
	}
	partial.Receipts = []TargetReceipt{rejected}
	partial.PlannedTargetIDs = []string{"target-b"}
	partial.Status = FanoutRejected
	if err := partial.Validate(); err != nil {
		t.Fatalf("all rejected result = %v", err)
	}
	partial.PlannedTargetIDs = []string{"target-a", "target-b"}
	if !errors.Is(partial.Validate(), ErrInvalidDTO) {
		t.Fatal("missing planned target receipt was accepted")
	}
}

func TestRoomFanoutRequiresCanonicalTargetOrderAndBoundedCount(t *testing.T) {
	accepted := TargetReceipt{SchemaVersion: SchemaVersion, TargetID: "target-a", TargetMappingDigest: digest("a"), DeliveryBranch: "per_target", ResourceDecision: "reserved", Status: ReceiptAccepted, ReceiptID: "receipt-a", ReceiptDigest: digest("b")}
	other := accepted
	other.TargetID = "target-b"
	other.ReceiptID = "receipt-b"
	result := FanoutResult{SchemaVersion: SchemaVersion, RoomID: "room-1", OperationID: "op-1", Status: FanoutApplied, PlannedTargetIDs: []string{"target-a", "target-b"}, Receipts: []TargetReceipt{accepted, other}}
	if err := result.Validate(); err != nil {
		t.Fatalf("canonical target order = %v", err)
	}
	result.PlannedTargetIDs = []string{"target-b", "target-a"}
	result.Receipts[0].TargetID, result.Receipts[1].TargetID = "target-a", "target-b"
	if !errors.Is(result.Validate(), ErrTargetReceipt) {
		t.Fatal("non-canonical planned target order was accepted")
	}
	tooMany := make([]string, MaxFanoutTargets+1)
	tooManyReceipts := make([]TargetReceipt, MaxFanoutTargets+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("target-%02d", i)
		tooManyReceipts[i] = accepted
		tooManyReceipts[i].TargetID = tooMany[i]
		tooManyReceipts[i].ReceiptID = fmt.Sprintf("receipt-%02d", i)
	}
	result.PlannedTargetIDs, result.Receipts = tooMany, tooManyReceipts
	if !errors.Is(result.Validate(), ErrTargetReceipt) {
		t.Fatal("fanout target bound was not enforced")
	}
}

func TestRoomFanoutBindsResultOperationIdentity(t *testing.T) {
	service := NewDisabledService()
	operation := operationFixture(OperationBroadcast)
	receipt := TargetReceipt{SchemaVersion: SchemaVersion, TargetID: "target-a", TargetMappingDigest: digest("a"), DeliveryBranch: "per_target", ResourceDecision: "reserved", Status: ReceiptAccepted, ReceiptID: "receipt-a", ReceiptDigest: digest("b")}
	result := FanoutResult{SchemaVersion: SchemaVersion, RoomID: operation.RoomID, OperationID: operation.OperationID, Status: FanoutApplied, PlannedTargetIDs: []string{"target-a"}, Receipts: []TargetReceipt{receipt}}
	if err := service.ValidateFanout(context.Background(), operation, result); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("matching result error=%v, want disabled after validation", err)
	}
	result.OperationID = "other-operation"
	if !errors.Is(service.ValidateFanout(context.Background(), operation, result), ErrOperationConflict) {
		t.Fatal("cross-operation fanout result was accepted")
	}
	result.OperationID = operation.OperationID
	result.RoomID = "other-room"
	if !errors.Is(service.ValidateFanout(context.Background(), operation, result), ErrOperationConflict) {
		t.Fatal("cross-room fanout result was accepted")
	}
	result.OperationID = operation.OperationID
	result.RoomID = operation.RoomID
	result.OperationDigest = digest("z")
	if !errors.Is(service.ValidateFanout(context.Background(), operation, result), ErrOperationConflict) {
		t.Fatal("cross-operation-digest fanout result was accepted")
	}
	result.OperationDigest = ""
	result.Receipts[0].OperationID = "other-operation"
	if !errors.Is(service.ValidateFanout(context.Background(), operation, result), ErrOperationConflict) {
		t.Fatal("cross-operation target receipt was accepted")
	}
	if !errors.Is(result.Validate(), ErrOperationConflict) {
		t.Fatal("FanoutResult.Validate accepted cross-operation target receipt")
	}
}

func TestRoomAdmissionReplayBindsOperationVersionAfterHeadReconstruction(t *testing.T) {
	candidate := candidateFixture("versioned-op", "v")
	head := headFixture()
	head.Generation = 2
	head.OperationID = candidate.OperationID
	head.OperationVersion = candidate.OperationVersion
	head.OperationDigest = candidate.OperationDigest
	head.State = candidate.NextState
	head.CustodyResolution = CustodyAvailable
	head.PreviousInventoryRoot = candidate.PreviousInventoryRoot
	head.PreviousInventoryCount = candidate.PreviousInventoryCount
	head.CommandInventoryRoot = candidate.CommandInventoryRoot
	head.CommandInventoryCount = candidate.CommandInventoryCount
	arbiter, err := NewArbiter(head)
	if err != nil {
		t.Fatal(err)
	}
	candidate.OperationVersion++
	if _, err := arbiter.TryAdmit(candidate); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("same operation with changed version replay error=%v, want ErrOperationConflict", err)
	}
}

func TestRoomResolveBindsReceiptDigestToAdmissionResult(t *testing.T) {
	arbiter, err := NewArbiter(headFixture())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFixture("receipt-op", "q")
	if _, err := arbiter.TryAdmit(candidate); err != nil {
		t.Fatal(err)
	}
	receiptDigest := digest("r")
	if err := arbiter.Resolve(candidate.OperationID, candidate.OperationDigest, CustodyAvailable, receiptDigest); err != nil {
		t.Fatal(err)
	}
	head := arbiter.Head()
	if head.ReceiptDigest != receiptDigest {
		t.Fatalf("head receipt digest=%q, want %q", head.ReceiptDigest, receiptDigest)
	}
	replay, err := arbiter.TryAdmit(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if replay.ReceiptDigest != receiptDigest || replay.OperationID != candidate.OperationID || replay.OperationDigest != candidate.OperationDigest {
		t.Fatalf("replay result=%+v, want operation/receipt binding", replay)
	}
	if err := replay.Validate(); err != nil {
		t.Fatalf("bound replay result validation = %v", err)
	}
}

func TestRoomRejectsInvalidLifecycleCombinations(t *testing.T) {
	base := roomFixture()
	for name, mutate := range map[string]func(*Room){
		"open_pending":    func(value *Room) { value.CustodyResolution = CustodyPending },
		"closing_aborted": func(value *Room) { value.Status, value.CustodyResolution = StatusClosingPending, CustodyAborted },
		"closed_aborted": func(value *Room) {
			value.Status, value.CustodyResolution, value.CloseReason = StatusClosed, CustodyAborted, "done"
		},
		"open_reason": func(value *Room) { value.CloseReason = "done" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if !errors.Is(candidate.Validate(), ErrInvalidState) {
				t.Fatalf("invalid lifecycle was accepted: %+v", candidate)
			}
		})
	}
}

func TestRoomDisabledServiceFailsClosed(t *testing.T) {
	service := NewDisabledService()
	if NewCandidate().Enabled() {
		t.Fatal("Room candidate must remain disabled")
	}
	operation := operationFixture(OperationBroadcast)
	if _, err := service.Submit(context.Background(), operation); !errors.Is(err, contract.ErrRoomCapabilityNotSupported) {
		t.Fatalf("Submit() = %v, want capability_not_supported", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Submit(ctx, operation); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Submit() = %v", err)
	}
	if err := RequireService((*DisabledService)(nil)); !errors.Is(err, ErrCapabilityDisabled) {
		t.Fatalf("typed nil RequireService() = %v", err)
	}
}

func roomFixture() Room {
	return Room{SchemaVersion: SchemaVersion, RoomID: "room-1", OwnerID: "owner-1", WorkspaceID: "workspace-1", RunScope: "workspace-1/run-1", TTLUnix: 1, DispatchGeneration: 1, MembershipRevision: 1, MembershipSnapshotHash: digest("m"), Status: StatusOpen, CustodyResolution: CustodyAvailable}
}

func headFixture() AdmissionHead {
	return AdmissionHead{SchemaVersion: SchemaVersion, RoomID: "room-1", Generation: 1, State: StatusOpen, CustodyResolution: CustodyAvailable, DispatchGeneration: 1, PreviousInventoryRoot: digest("p"), CommandInventoryRoot: digest("c")}
}

func candidateFixture(id, marker string) AdmissionCandidate {
	return AdmissionCandidate{SchemaVersion: SchemaVersion, RoomID: "room-1", OperationID: id, OperationVersion: 1, OperationDigest: digest(marker), ExpectedGeneration: 1, NextState: StatusOpen, NextCustodyResolution: CustodyPending, DispatchGeneration: 1, PreviousInventoryRoot: digest("c"), CommandInventoryRoot: digest(marker)}
}

func concurrentAdmissionCounts(t *testing.T, arbiter *Arbiter, candidates []AdmissionCandidate) (winners, conflicts int) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, len(candidates))
	for _, candidate := range candidates {
		candidate := candidate
		wg.Go(func() {
			_, callErr := arbiter.TryAdmit(candidate)
			errs <- callErr
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrAdmissionConflict):
			conflicts++
		}
	}
	return winners, conflicts
}

func operationFixture(kind string) Operation {
	op := Operation{SchemaVersion: SchemaVersion, RoomID: "room-1", OperationID: "operation-1", OperationVersion: 1, Kind: kind, ExpectedMembershipRevision: 1, ExpectedDispatchGeneration: 1, IdempotencyKeyDigest: digest("i"), Principal: Principal{PrincipalRef: "principal-1", ScopeRef: "workspace-1/run-1", ActorPrincipal: "authenticated_subject", IssuerBinding: "provider_bridge", GrantDigest: digest("g")}}
	finalized, err := FinalizeOperationDigest(op)
	if err != nil {
		return Operation{}
	}
	return finalized
}

func digest(marker string) string {
	first := marker[:1]
	if !strings.Contains("0123456789abcdef", first) {
		first = "a"
	}
	return strings.Repeat(first, 64)
}
