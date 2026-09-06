package identity

import (
	"errors"
	"testing"
)

func TestGetActiveSnapshotReadsOneValidatedIdentityChain(t *testing.T) {
	db := openIdentityTestDB(t)
	store := mustIdentityStore(t, db)
	chain := createIdentityChain(t, db, store, "active-snapshot", 1)
	if _, err := store.PrepareBinding(t.Context(), chain.prep); err != nil {
		t.Fatalf("PrepareBinding() error = %v", err)
	}
	active, err := store.FinalizeBinding(t.Context(), FinalizeBindingRequest{
		PreparationID: chain.prep.PreparationID, AgentID: chain.reservation.AgentID,
		ExpectedPreparationGeneration: chain.prep.PreparationGeneration, ExpectedBindingVersion: 0,
		IncarnationID: chain.incarnation.IncarnationID, ExternalIdentityRef: chain.incarnation.ExternalIdentityRef,
		IdentityKind: chain.reservation.IdentityKind, SourceFenceRef: chain.reservation.SourceFenceRef,
		ActiveProcessReceiptRef: "receipt-active-snapshot", BindingDigest: chain.prep.BindingDigest,
	})
	if err != nil {
		t.Fatalf("FinalizeBinding() error = %v", err)
	}
	snapshot, err := store.GetActiveSnapshot(t.Context(), active.AgentID)
	if err != nil {
		t.Fatalf("GetActiveSnapshot() error = %v", err)
	}
	if snapshot.Binding.State != "active" {
		t.Fatalf("snapshot binding state = %q", snapshot.Binding.State)
	}
	if snapshot.Reservation.State != "activated" {
		t.Fatalf("snapshot reservation state = %q", snapshot.Reservation.State)
	}
	assertSnapshotIdentity(t, snapshot, active)
	if _, err := store.RevokeBinding(t.Context(), RevokeBindingRequest{AgentID: active.AgentID, ExpectedBindingVersion: active.BindingVersion, IncarnationID: active.IncarnationID, SourceFenceRef: active.SourceFenceRef}); err != nil {
		t.Fatalf("RevokeBinding() error = %v", err)
	}
	if _, err := store.GetActiveSnapshot(t.Context(), active.AgentID); !errors.Is(err, ErrIdentityFence) {
		t.Fatalf("GetActiveSnapshot(revoked) error = %v, want ErrIdentityFence", err)
	}
}

func assertSnapshotIdentity(t *testing.T, snapshot ActiveSnapshot, active CurrentBinding) {
	t.Helper()
	if snapshot.Reservation.ReservationID != active.ReservationID {
		t.Fatalf("snapshot reservation = %q, want %q", snapshot.Reservation.ReservationID, active.ReservationID)
	}
	if snapshot.Incarnation.IncarnationID != active.IncarnationID {
		t.Fatalf("snapshot incarnation = %q, want %q", snapshot.Incarnation.IncarnationID, active.IncarnationID)
	}
	if snapshot.Binding.BindingVersion != active.BindingVersion {
		t.Fatalf("snapshot binding version = %d, want %d", snapshot.Binding.BindingVersion, active.BindingVersion)
	}
}
