package credentialactivation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"
	"testing"
)

type receiptOwnerStub struct {
	err   error
	calls int
}

func (s *receiptOwnerStub) VerifyActiveReceipt(context.Context, ActivationRequest, ActiveCredentialReceipt) error {
	s.calls++
	return s.err
}

type bindingReaderStub struct {
	value CurrentBinding
	err   error
	calls int
}

func (s *bindingReaderStub) ReadCurrentBinding(context.Context, string, string) (CurrentBinding, error) {
	s.calls++
	return s.value, s.err
}

type clockOwnerStub struct {
	value SecurityTimeObservation
	err   error
	calls int
}

func (s *clockOwnerStub) ObserveSecurityTime(context.Context, SecurityTimeQuery) (SecurityTimeObservation, error) {
	s.calls++
	return s.value, s.err
}

type replayOwnerStub struct {
	value AntiReplayReceipt
	err   error
	calls int
	mu    sync.Mutex
}

func (s *replayOwnerStub) ConsumeNonce(context.Context, AntiReplayRequest) (AntiReplayReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.value, s.err
}

func TestCandidateActivatorRequiresAllExternalOwners(t *testing.T) {
	if _, err := NewCandidateActivator(nil, &bindingReaderStub{}, &clockOwnerStub{}, &replayOwnerStub{}); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("nil receipt owner error = %v", err)
	}
	if _, err := NewCandidateActivator(&receiptOwnerStub{}, nil, &clockOwnerStub{}, &replayOwnerStub{}); !errors.Is(err, ErrActivationUnavailable) {
		t.Fatalf("nil binding owner error = %v", err)
	}
}

func TestCandidateActivatorRequiresExternalProofChain(t *testing.T) {
	fixture := activationFixture(t)
	activator, err := NewCandidateActivator(fixture.receipts, fixture.binding, fixture.clock, fixture.replay)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := activator.Activate(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
	if fixture.receipts.calls != 1 || fixture.binding.calls != 1 || fixture.clock.calls != 1 || fixture.replay.calls != 1 {
		t.Fatalf("owner calls = %d/%d/%d/%d, want one each", fixture.receipts.calls, fixture.binding.calls, fixture.clock.calls, fixture.replay.calls)
	}
	if reflect.TypeOf(candidate).NumField() == 0 {
		t.Fatal("candidate has no proof fields")
	}
}

func TestCandidateActivatorFailsBeforeLaterOwners(t *testing.T) {
	fixture := activationFixture(t)
	fixture.receipts.err = errors.New("receipt unavailable")
	activator, err := NewCandidateActivator(fixture.receipts, fixture.binding, fixture.clock, fixture.replay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activator.Activate(context.Background(), fixture.request); !errors.Is(err, ErrActivationRejected) {
		t.Fatalf("receipt failure = %v", err)
	}
	if fixture.binding.calls != 0 || fixture.clock.calls != 0 || fixture.replay.calls != 0 {
		t.Fatal("later owners called after receipt failure")
	}
}

func TestCandidateActivatorRejectsPreparedOnlyOrBindingDrift(t *testing.T) {
	fixture := activationFixture(t)
	fixture.request.ActiveReceipt.State = StatePrepared
	activator, _ := NewCandidateActivator(fixture.receipts, fixture.binding, fixture.clock, fixture.replay)
	if _, err := activator.Activate(context.Background(), fixture.request); !errors.Is(err, ErrInvalidActiveReceipt) {
		t.Fatalf("prepared receipt error = %v", err)
	}

	fixture = activationFixture(t)
	fixture.binding.value.BindingDigest = testDigest("drift")
	activator, _ = NewCandidateActivator(fixture.receipts, fixture.binding, fixture.clock, fixture.replay)
	if _, err := activator.Activate(context.Background(), fixture.request); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("binding drift error = %v", err)
	}
	if fixture.clock.calls != 0 || fixture.replay.calls != 0 {
		t.Fatal("security or replay owner called after binding drift")
	}
}

func TestCandidateActivatorRejectsExpiredSecurityTimeAndReplayMismatch(t *testing.T) {
	fixture := activationFixture(t)
	fixture.clock.value.ObservedAt = "2035-01-02T04:00:00Z"
	activator, _ := NewCandidateActivator(fixture.receipts, fixture.binding, fixture.clock, fixture.replay)
	if _, err := activator.Activate(context.Background(), fixture.request); !errors.Is(err, ErrInvalidSecurityTime) {
		t.Fatalf("expired security time error = %v", err)
	}

	fixture = activationFixture(t)
	fixture.replay.value.NonceSlot = "foreign-slot"
	activator, _ = NewCandidateActivator(fixture.receipts, fixture.binding, fixture.clock, fixture.replay)
	if _, err := activator.Activate(context.Background(), fixture.request); !errors.Is(err, ErrInvalidAntiReplay) {
		t.Fatalf("replay mismatch error = %v", err)
	}
}

type activationFixtureData struct {
	request  ActivationRequest
	receipts *receiptOwnerStub
	binding  *bindingReaderStub
	clock    *clockOwnerStub
	replay   *replayOwnerStub
}

func activationFixture(t *testing.T) activationFixtureData {
	t.Helper()
	prepared := PreparedCredential{SchemaVersion: SchemaVersion, State: StatePrepared, CredentialLeaseID: "lease/1", CredentialDigest: testDigest("credential"), AgentID: "agent/1", IncarnationID: "incarnation/1", PreparationRef: "prep/1", PreparationDigest: testDigest("prep"), BindingVersion: 2, BindingDigest: testDigest("binding"), ActiveProcessReceiptRef: "process/1", ConnectionNonceDigest: testDigest("nonce")}
	receipt := ActiveCredentialReceipt{SchemaVersion: SchemaVersion, State: StateActive, ReceiptRef: "active/1", CredentialLeaseID: prepared.CredentialLeaseID, CredentialDigest: prepared.CredentialDigest, PreparationRef: prepared.PreparationRef, PreparationDigest: prepared.PreparationDigest, AgentID: prepared.AgentID, IncarnationID: prepared.IncarnationID, BindingVersion: prepared.BindingVersion, BindingDigest: prepared.BindingDigest, ActiveProcessReceiptRef: prepared.ActiveProcessReceiptRef, ConnectionNonceDigest: prepared.ConnectionNonceDigest, NonceSlot: "nonce-slot/1", IssuedAt: "2035-01-02T03:00:00Z", ExpiresAt: "2035-01-02T05:00:00Z", SecurityTimeLineageDigest: testDigest("lineage"), SecurityTimeGeneration: 4, SecurityTimeRevision: 9}
	receipt.ReceiptDigest, _ = ComputeActiveReceiptDigest(receipt)
	request := ActivationRequest{SchemaVersion: SchemaVersion, OperationID: "activate/1", Prepared: prepared, ActiveReceipt: receipt, ExpectedBindingDigest: prepared.BindingDigest, ExpectedBindingVersion: prepared.BindingVersion, ExpectedLineageDigest: receipt.SecurityTimeLineageDigest, ExpectedSecurityGeneration: receipt.SecurityTimeGeneration, ExpectedSecurityRevision: receipt.SecurityTimeRevision, NonceSlot: receipt.NonceSlot}
	request.RequestDigest, _ = ComputeActivationRequestDigest(request)
	binding := &bindingReaderStub{value: CurrentBinding{SchemaVersion: SchemaVersion, State: StateActive, AgentID: prepared.AgentID, IncarnationID: prepared.IncarnationID, BindingVersion: 2, BindingDigest: prepared.BindingDigest, ActiveProcessReceiptRef: "process/1"}}
	observation := SecurityTimeObservation{SchemaVersion: SchemaVersion, LineageDigest: receipt.SecurityTimeLineageDigest, Generation: receipt.SecurityTimeGeneration, Revision: receipt.SecurityTimeRevision, ObservedAt: "2035-01-02T03:30:00Z", ReceiptRef: "security/1"}
	observation.ReceiptDigest, _ = ComputeSecurityTimeObservationDigest(observation)
	replay := AntiReplayReceipt{SchemaVersion: SchemaVersion, ReceiptRef: "replay/1", OperationID: request.OperationID, RequestDigest: request.RequestDigest, CredentialLeaseID: prepared.CredentialLeaseID, CredentialDigest: prepared.CredentialDigest, ConnectionNonceDigest: prepared.ConnectionNonceDigest, NonceSlot: request.NonceSlot, ConsumedAt: "2035-01-02T03:31:00Z"}
	replay.ReceiptDigest, _ = ComputeAntiReplayReceiptDigest(replay)
	return activationFixtureData{request: request, receipts: &receiptOwnerStub{}, binding: binding, clock: &clockOwnerStub{value: observation}, replay: &replayOwnerStub{value: replay}}
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
