//go:build darwin

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

const replacementGeneration = "ffeeddccbbaa99887766554433221100"

func TestJournalPublicationFailurePhasesRetainMatchingSignal(t *testing.T) {
	for _, phase := range []string{"journal temp", "journal write", "journal fsync", "journal rename"} {
		t.Run(phase, func(t *testing.T) {
			testJournalPublicationFailurePhase(t, phase)
		})
	}
}

func testJournalPublicationFailurePhase(t *testing.T, phase string) {
	t.Helper()
	stubSuccessfulDitto(t)
	stageDir, _ := testUpdaterStage(t)
	beginUpdaterAttempt(t, stageDir)
	staged := createAppBundle(t, filepath.Join(realUpdaterTempDir(t), "Super Dolphin.app"))
	parent := realUpdaterTempDir(t)
	target := createAppBundle(t, filepath.Join(parent, "Super Dolphin.app"))
	beforeDigest, err := recovery.ComputeReleaseDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New(phase + " failed")
	helperStarted := false
	app := defaultUpdaterApp()
	app.createRecoveryTransaction = func(*recovery.Store, context.Context, recovery.CreateRequest) (recovery.Transaction, error) {
		return recovery.Transaction{}, wantErr
	}
	app.startProbationCandidate = func(context.Context, recovery.Transaction) (*candidateHandle, error) {
		helperStarted = true
		return nil, errors.New("unexpected helper start")
	}
	_, _, err = app.replaceTargetAppTransactionContextWithStageDir(
		context.Background(), staged, target, "", true, true, stageDir, recoveryTestGeneration,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("replaceTargetAppTransactionContextWithStageDir() error = %v, want %v", err, wantErr)
	}
	assertJournalPublicationFailureState(t, stageDir, parent, target, beforeDigest, helperStarted)
}

func assertJournalPublicationFailureState(t *testing.T, stageDir string, parent string, target string, beforeDigest string, helperStarted bool) {
	t.Helper()
	if helperStarted {
		t.Fatal("probation helper started after journal publication failure")
	}
	failure, exists, err := appupdatefailure.ReadFailure(stageDir)
	if err != nil || !exists || failure.Code != "UPDATE_INTEGRITY_INVALID" {
		t.Fatalf("ReadFailure() = (%#v, %v, %v), want matching integrity failure", failure, exists, err)
	}
	journals, err := filepath.Glob(filepath.Join(parent, updateTransactionDirName, "*", "journal.json"))
	if err != nil || len(journals) != 0 {
		t.Fatalf("transaction journals = %v, error = %v, want none", journals, err)
	}
	candidates, err := filepath.Glob(filepath.Join(parent, ".Super Dolphin.staging-*.app"))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("prepared candidates = %v, error = %v, want none", candidates, err)
	}
	assertTargetDigestUnchanged(t, target, beforeDigest, "journal failure")
}

func assertTargetDigestUnchanged(t *testing.T, target string, beforeDigest string, operation string) {
	t.Helper()
	afterDigest, err := recovery.ComputeReleaseDigest(target)
	if err != nil || afterDigest != beforeDigest {
		t.Fatalf("target digest after %s = %q, %v, want unchanged %q", operation, afterDigest, err, beforeDigest)
	}
}

func TestCrashBetweenJournalAndClearRecoversFromJournal(t *testing.T) {
	stageDir, target, created, storeRoot := createPreparedJournalWithPendingSidecar(t)
	if _, exists, err := appupdatefailure.ReadFailure(stageDir); err != nil || exists {
		t.Fatalf("ReadFailure() before simulated crash = (_, %v, %v), want pending hidden", exists, err)
	}
	reopened, err := recovery.NewStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	selected, found, err := reopened.SelectForTarget(context.Background(), target)
	if err != nil || !found || selected.Identity != created.Identity || selected.State != recovery.StatePrepared {
		t.Fatalf("SelectForTarget() = (%+v, %v, %v), want prepared journal %+v", selected, found, err, created.Identity)
	}
}

func TestClearGenerationMismatchPreservesNewSidecarAndOldJournal(t *testing.T) {
	stageDir, target, created, storeRoot := createPreparedJournalWithPendingSidecar(t)
	if err := appupdatefailure.Begin(stageDir, replacementGeneration); err != nil {
		t.Fatal(err)
	}
	if err := clearPreJournalFailure(stageDir, recoveryTestGeneration); err == nil {
		t.Fatal("clearPreJournalFailure(old generation) error = nil, want mismatch")
	}
	if err := appupdatefailure.FailCode(stageDir, replacementGeneration, "UPDATE_INTEGRITY_INVALID"); err != nil {
		t.Fatalf("FailCode(new generation) error = %v, want new sidecar preserved", err)
	}
	assertUpdaterSidecar(t, stageDir, "UPDATE_INTEGRITY_INVALID")
	reopened, err := recovery.NewStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	selected, found, err := reopened.SelectForTarget(context.Background(), target)
	if err != nil || !found || selected.Identity != created.Identity {
		t.Fatalf("SelectForTarget() = (%+v, %v, %v), want old journal %+v", selected, found, err, created.Identity)
	}
}

func createPreparedJournalWithPendingSidecar(t *testing.T) (string, string, recovery.Transaction, string) {
	t.Helper()
	stubSuccessfulDitto(t)
	stageDir, _ := testUpdaterStage(t)
	beginUpdaterAttempt(t, stageDir)
	staged := createAppBundle(t, filepath.Join(realUpdaterTempDir(t), "Super Dolphin.app"))
	parent := realUpdaterTempDir(t)
	target := createAppBundle(t, filepath.Join(parent, "Super Dolphin.app"))
	request, _, err := defaultUpdaterApp().prepareReleaseTransaction(context.Background(), staged, target, "", true)
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := filepath.Join(parent, updateTransactionDirName)
	store, err := recovery.NewStore(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return stageDir, target, created, storeRoot
}
