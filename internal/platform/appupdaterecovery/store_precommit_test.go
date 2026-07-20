package appupdaterecovery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestCreateWithPreCommitDoesNotRunCallbackBeforeTrustValidation(t *testing.T) {
	store, req := newPreCommitFixture(t)
	writeFixture(t, req.Paths.Staging, "tampered candidate")
	called := false
	_, err := store.CreateWithPreCommit(context.Background(), req, func() error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUpdateIntegrityInvalid) {
		t.Fatalf("CreateWithPreCommit() error = %v, want integrity failure", err)
	}
	if called {
		t.Fatal("pre-commit callback ran before trust validation")
	}
	assertPathMissing(t, store.journalPath(req.Identity.TransactionID))
}

func TestCreateWithPreCommitCallbackFailureDoesNotWriteJournal(t *testing.T) {
	store, req := newPreCommitFixture(t)
	wantErr := errors.New("clear pre-journal failed")
	_, err := store.CreateWithPreCommit(context.Background(), req, func() error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("CreateWithPreCommit() error = %v, want callback failure", err)
	}
	assertPathMissing(t, store.journalPath(req.Identity.TransactionID))
}

func TestCreateWithPreCommitRunsOnceBeforeJournalPublication(t *testing.T) {
	store, req := newPreCommitFixture(t)
	calls := 0
	created, err := store.CreateWithPreCommit(context.Background(), req, func() error {
		calls++
		assertPathMissing(t, store.journalPath(req.Identity.TransactionID))
		return nil
	})
	if err != nil {
		t.Fatalf("CreateWithPreCommit() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("callback calls = %d, want 1", calls)
	}
	if _, err := store.RetainBackup(context.Background(), created.Identity); err != nil {
		t.Fatalf("RetainBackup() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("callback calls after Create = %d, want no post-create access", calls)
	}
}

func newPreCommitFixture(t *testing.T) (*Store, CreateRequest) {
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
			TransactionID: id,
			AttemptID:     "pre-commit-attempt",
			OldRelease:    ReleaseIdentity{SHA256: digestText("old release"), SignerIdentity: "TEAM-OLD"},
			CandidateRelease: ReleaseIdentity{
				SHA256: digestText("candidate release"), SignerIdentity: "TEAM-NEW",
			},
			OldHelpers:       fixtureHelperIdentity("old"),
			CandidateHelpers: fixtureHelperIdentity("candidate"),
			UpdaterProcess:   fixtureUpdaterProcess(),
		},
		Paths: paths,
		Trust: TrustGeneration{
			PreviousGeneration: "trust-generation-1",
			Generation:         "trust-generation-2",
			PackageSigner:      "TEAM-NEW",
			State:              TrustPending,
		},
	}
}
