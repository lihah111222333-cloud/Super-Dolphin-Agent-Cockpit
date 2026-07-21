package appupdaterecovery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestSelectForTargetUsesGenerationAcrossClockRollback(t *testing.T) {
	selection := targetTransactionSelection{target: "/Applications/Super Dolphin.app"}
	olderGenerationWithFutureClock := Transaction{
		Identity: Identity{TransactionID: "11111111111111111111111111111111"},
		Paths:    Paths{Target: selection.target}, State: StateCommitted, TargetGeneration: 41,
		UpdatedAt: "2099-01-01T00:00:00Z",
	}
	newerGenerationAfterClockRollback := Transaction{
		Identity: Identity{TransactionID: "22222222222222222222222222222222"},
		Paths:    Paths{Target: selection.target}, State: StateRolledBack, TargetGeneration: 42,
		UpdatedAt: "2000-01-01T00:00:00Z",
	}
	if err := selection.add(olderGenerationWithFutureClock); err != nil {
		t.Fatal(err)
	}
	if err := selection.add(newerGenerationAfterClockRollback); err != nil {
		t.Fatal(err)
	}
	if selection.latest.Identity.TransactionID != newerGenerationAfterClockRollback.Identity.TransactionID {
		t.Fatalf("selected transaction = %s, want generation 42 despite wall-clock rollback", selection.latest.Identity.TransactionID)
	}
}

func TestCreateAllocatesMonotonicTargetGeneration(t *testing.T) {
	store, identity, paths := createPreparedTransaction(t)
	first := loadTransaction(t, store, identity)
	if first.TargetGeneration != 1 {
		t.Fatalf("first target generation = %d, want 1", first.TargetGeneration)
	}
	if _, err := store.Rollback(context.Background(), identity); err != nil {
		t.Fatal(err)
	}

	secondID := TransactionID("33333333333333333333333333333333")
	secondPaths, err := PathsFor(paths.Target, secondID)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, secondPaths.Staging, "candidate release 2")
	secondIdentity := identity
	secondIdentity.TransactionID = secondID
	secondIdentity.AttemptID = "attempt-2"
	secondIdentity.OldRelease = ReleaseIdentity{SHA256: digestText("old release"), SignerIdentity: "TEAM-OLD"}
	secondIdentity.CandidateRelease = ReleaseIdentity{SHA256: digestText("candidate release 2"), SignerIdentity: "TEAM-NEW"}
	transaction, err := store.Create(context.Background(), CreateRequest{
		Identity: secondIdentity,
		Paths:    secondPaths,
		Trust: TrustGeneration{
			PreviousGeneration: "trust-generation-2", Generation: "trust-generation-3",
			PackageSigner: "TEAM-NEW", State: TrustPending,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.TargetGeneration != 2 {
		t.Fatalf("second target generation = %d, want 2", transaction.TargetGeneration)
	}
}

func TestReconcileBackupEffectClassifiesAmbiguousState(t *testing.T) {
	root := t.TempDir()
	err := reconcileBackupEffect(context.Background(), journalPayload{Paths: Paths{
		Target: filepath.Join(root, "missing-target"),
		Backup: filepath.Join(root, "missing-backup"),
	}})
	if !errors.Is(err, contract.ErrUpdateTransactionAmbiguous) {
		t.Fatalf("reconcileBackupEffect() error = %v, want ErrUpdateTransactionAmbiguous", err)
	}
}
