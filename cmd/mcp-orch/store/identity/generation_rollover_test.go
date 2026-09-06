package identity

import (
	"errors"
	"testing"
)

func TestIdentityAllowsIndependentReservationAndIncarnationGenerations(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	seedCommandIntent(t, db, "cmd-independent-generations")
	reservation, err := store.Reserve(t.Context(), commandReservationRequest("reservation-independent-generations", "cmd-independent-generations", "agent-independent-generations", 3))
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	incarnationRequest := incarnationRequestFor(reservation, "incarnation-independent-generations", "external-independent-generations", "epoch-independent-generations")
	incarnationRequest.IncarnationGeneration = 4
	incarnation, err := store.CreateIncarnation(t.Context(), incarnationRequest)
	if err != nil {
		t.Fatalf("CreateIncarnation(rollover) error = %v", err)
	}
	preparationRequest := BindingPreparationRequest{
		PreparationID: "preparation-independent-generations", ReservationID: reservation.ReservationID,
		IncarnationID: incarnation.IncarnationID, AgentID: reservation.AgentID, IdentityKind: reservation.IdentityKind,
		SourceFenceRef: reservation.SourceFenceRef, BindingDigest: testDigest('9'), PreparationGeneration: 1,
		ExpectedBindingVersion: 0, ExpectedIncarnationGeneration: incarnation.IncarnationGeneration,
	}
	if _, err := store.PrepareBinding(t.Context(), preparationRequest); err != nil {
		t.Fatalf("PrepareBinding(rollover) error = %v", err)
	}
	active, err := store.FinalizeBinding(t.Context(), FinalizeBindingRequest{
		PreparationID: preparationRequest.PreparationID, AgentID: reservation.AgentID,
		ExpectedPreparationGeneration: preparationRequest.PreparationGeneration, ExpectedBindingVersion: 0,
		IncarnationID: incarnation.IncarnationID, ExternalIdentityRef: incarnation.ExternalIdentityRef,
		IdentityKind: incarnation.IdentityKind, SourceFenceRef: incarnation.SourceFenceRef,
		ActiveProcessReceiptRef: "receipt-independent-generations", BindingDigest: preparationRequest.BindingDigest,
	})
	if err != nil {
		t.Fatalf("FinalizeBinding(rollover) error = %v", err)
	}
	if active.ReservationGeneration != 3 || active.IncarnationGeneration != 4 {
		t.Fatalf("active rollover generations = reservation:%d incarnation:%d, want 3:4", active.ReservationGeneration, active.IncarnationGeneration)
	}

	seedCommandIntent(t, db, "cmd-independent-generations-next")
	nextReservation, err := store.Reserve(t.Context(), commandReservationRequest("reservation-independent-generations-next", "cmd-independent-generations-next", reservation.AgentID, 4))
	if err != nil {
		t.Fatalf("Reserve(next generation) error = %v", err)
	}
	staleIncarnationRequest := incarnationRequestFor(nextReservation, "incarnation-independent-generations-stale", "external-independent-generations-stale", "epoch-independent-generations-stale")
	staleIncarnationRequest.IncarnationGeneration = 3
	if _, err := store.CreateIncarnation(t.Context(), staleIncarnationRequest); !errors.Is(err, ErrIdentityFence) {
		t.Fatalf("CreateIncarnation(stale generation) error = %v, want ErrIdentityFence", err)
	}
}
