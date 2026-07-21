package appupdaterecovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateReturnsPublishedTransactionWithPostReleaseError(t *testing.T) {
	store, req := newCreatePublicationFixture(t)
	wantErr := errors.New("generation lock release failed")
	store.afterEffect = func(state State) error {
		if state == StatePrepared {
			return wantErr
		}
		return nil
	}
	created, err := store.Create(context.Background(), req)
	if !errors.Is(err, wantErr) || !exactStorePreparedCreateResult(created, req) {
		t.Fatalf("Create() = (%+v, %v), want exact published transaction plus release error", created, err)
	}
	loaded, err := store.LoadByID(context.Background(), req.Identity.TransactionID)
	if err != nil || loaded != created {
		t.Fatalf("LoadByID() = (%+v, %v), want %+v", loaded, err, created)
	}
}

func exactStorePreparedCreateResult(transaction Transaction, req CreateRequest) bool {
	return transaction.Identity == req.Identity && transaction.Paths == req.Paths &&
		transaction.State == StatePrepared && transaction.Trust == req.Trust &&
		transaction.TargetGeneration > 0 && transaction.Revision == 1 &&
		transaction.CreatedAt != "" && transaction.UpdatedAt == transaction.CreatedAt
}

func TestResolveCreateWriteErrorDistinguishesPublicationState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		seed      func(*testing.T, *Store, journalPayload)
		published bool
	}{
		{name: "definitely absent"},
		{name: "exact journal", published: true, seed: seedExactCreateJournal},
		{name: "unknown corrupt journal", published: true, seed: seedCorruptCreateJournal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, req := newCreatePublicationFixture(t)
			journal := newJournal(req, 1, time.Now())
			if tc.seed != nil {
				tc.seed(t, store, journal)
			}
			wantErr := errors.New("journal directory fsync failed")
			created, err := store.resolveCreateWriteError(journal, wantErr)
			if !errors.Is(err, wantErr) {
				t.Fatalf("resolveCreateWriteError() error = %v, want source error", err)
			}
			if got := created.Identity.TransactionID != ""; got != tc.published {
				t.Fatalf("published result = %v, want %v", got, tc.published)
			}
		})
	}
}

func seedExactCreateJournal(t *testing.T, store *Store, journal journalPayload) {
	t.Helper()
	if err := os.MkdirAll(store.transactionDir(journal.Identity.TransactionID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.writeLocked(journal); err != nil {
		t.Fatal(err)
	}
}

func seedCorruptCreateJournal(t *testing.T, store *Store, journal journalPayload) {
	t.Helper()
	if err := os.MkdirAll(store.transactionDir(journal.Identity.TransactionID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.journalPath(journal.Identity.TransactionID), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newCreatePublicationFixture(t *testing.T) (*Store, CreateRequest) {
	t.Helper()
	parent := t.TempDir()
	store, err := NewStore(filepath.Join(parent, ".update-transactions"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := NewTransactionID()
	if err != nil {
		t.Fatal(err)
	}
	paths, err := PathsFor(filepath.Join(parent, "Super Dolphin.app"), id)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, paths.Target, "old release")
	writeFixture(t, paths.Staging, "candidate release")
	return store, CreateRequest{
		Identity: Identity{
			TransactionID: id, AttemptID: "create-publication-attempt",
			OldRelease:       ReleaseIdentity{SHA256: digestText("old release"), SignerIdentity: "TEAM-OLD"},
			CandidateRelease: ReleaseIdentity{SHA256: digestText("candidate release"), SignerIdentity: "TEAM-NEW"},
			OldHelpers:       fixtureHelperIdentity("old"), CandidateHelpers: fixtureHelperIdentity("candidate"),
			UpdaterProcess: fixtureUpdaterProcess(),
		},
		Paths: paths,
		Trust: TrustGeneration{
			PreviousGeneration: "trust-generation-1", Generation: "trust-generation-2",
			PackageSigner: "TEAM-NEW", State: TrustPending,
		},
	}
}
