package appupdaterecovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveTrustPreparedAndBackupIntentWindows(t *testing.T) {
	prepared := newTrustStateFixture(t)
	prepared.assertOld(t)
	mustAdvanceTrustState(t, prepared, TriggerRetainBackup)
	prepared.assertOld(t)

	afterEffect := newTrustStateFixture(t)
	afterEffect.store.afterEffect = func(state State) error { return errors.New(string(state)) }
	if _, err := afterEffect.store.RetainBackup(context.Background(), afterEffect.request.Identity); err == nil {
		t.Fatal("RetainBackup() crash error = nil")
	}
	assertTrustPathMissing(t, afterEffect.target)
	afterEffect.assertOld(t)
}

func TestResolveTrustBackupRetainedInstallAndProbationWindows(t *testing.T) {
	retained := newTrustStateFixture(t)
	mustRetainTrustBackup(t, retained)
	assertTrustPathMissing(t, retained.target)
	retained.assertOld(t)
	mustAdvanceTrustState(t, retained, TriggerInstallCandidate)
	retained.assertOld(t)

	afterInstall := newTrustStateFixture(t)
	mustRetainTrustBackup(t, afterInstall)
	afterInstall.store.afterEffect = func(state State) error { return errors.New(string(state)) }
	if _, err := afterInstall.store.InstallCandidate(context.Background(), afterInstall.request.Identity); err == nil {
		t.Fatal("InstallCandidate() crash error = nil")
	}
	afterInstall.assertOld(t)

	probation := newTrustStateFixture(t)
	mustInstallTrustCandidate(t, probation)
	probation.assertOld(t)
}

func TestResolveTrustRollbackIntentAndTerminalWindows(t *testing.T) {
	pending := newTrustStateFixture(t)
	mustInstallTrustCandidate(t, pending)
	mustAdvanceTrustState(t, pending, TriggerRollbackRequested)
	pending.assertOld(t)

	afterEffect := newTrustStateFixture(t)
	mustInstallTrustCandidate(t, afterEffect)
	afterEffect.store.afterEffect = func(state State) error { return errors.New(string(state)) }
	if _, err := afterEffect.store.RollbackUnclaimedProbation(context.Background(), afterEffect.request.Identity); err == nil {
		t.Fatal("RollbackUnclaimedProbation() crash error = nil")
	}
	afterEffect.assertOld(t)

	rolledBack := newTrustStateFixture(t)
	mustInstallTrustCandidate(t, rolledBack)
	if _, err := rolledBack.store.RollbackUnclaimedProbation(context.Background(), rolledBack.request.Identity); err != nil {
		t.Fatal(err)
	}
	assertTrustPathMissing(t, rolledBack.request.Paths.RecoveryDir)
	rolledBack.assertOld(t)
}

func TestResolveTrustCommitIntentReturnsOldUntilCommitted(t *testing.T) {
	pending := newTrustStateFixture(t)
	lease := mustClaimHealthyTrustCandidate(t, pending)
	mustAdvanceTrustState(t, pending, TriggerHealthy)
	pending.assertOld(t)

	afterEffect := newTrustStateFixture(t)
	lease = mustClaimHealthyTrustCandidate(t, afterEffect)
	afterEffect.store.afterEffect = func(state State) error { return errors.New(string(state)) }
	if _, err := afterEffect.store.commitHealthyClaimed(context.Background(), afterEffect.request.Identity, lease); err == nil {
		t.Fatal("commitHealthyClaimed() crash error = nil")
	}
	afterEffect.assertOld(t)

	committed := newTrustStateFixture(t)
	lease = mustClaimHealthyTrustCandidate(t, committed)
	if _, err := committed.store.commitHealthyClaimed(context.Background(), committed.request.Identity, lease); err != nil {
		t.Fatal(err)
	}
	assertTrustPathMissing(t, committed.request.Paths.RecoveryDir)
	committed.assertCandidate(t)
}

type trustStateFixture struct {
	store               *Store
	request             CreateRequest
	target              string
	oldTrust            PackageTrust
	candidateTrust      PackageTrust
	oldGeneration       string
	candidateGeneration string
}

func newTrustStateFixture(t *testing.T) trustStateFixture {
	t.Helper()
	root := canonicalTestTempDir(t)
	target := filepath.Join(root, "Super Dolphin.app")
	id := TransactionID("11223344556677889900aabbccddeeff")
	paths, err := PathsFor(target, id)
	if err != nil {
		t.Fatal(err)
	}
	oldTrust, oldGeneration, oldRelease, oldHelpers := writeExactTrustedRelease(t, target, "old")
	candidateTrust, candidateGeneration, candidateRelease, candidateHelpers := writeExactTrustedRelease(t, paths.Staging, "candidate")
	if err := os.MkdirAll(paths.RecoveryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	copyTrustFixture(t, filepath.Join(target, "Contents", "Resources", PackageTrustFilename), filepath.Join(paths.RecoveryDir, PackageTrustFilename))
	copyTrustFixture(t, filepath.Join(target, "Contents", "Resources", "bin", "super-dolphin-updater"), filepath.Join(paths.RecoveryDir, "super-dolphin-updater"))
	copyTrustFixture(t, filepath.Join(target, "Contents", "Resources", "bin", "super-dolphin-guard"), filepath.Join(paths.RecoveryDir, "super-dolphin-guard"))
	store, err := NewStore(TransactionRootForTarget(target))
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Identity: Identity{
			TransactionID: id, AttemptID: "state-matrix", OldRelease: oldRelease, CandidateRelease: candidateRelease,
			OldHelpers: oldHelpers, CandidateHelpers: candidateHelpers, UpdaterProcess: fixtureUpdaterProcess(),
		},
		Paths: paths,
		Trust: TrustGeneration{PreviousGeneration: oldGeneration, Generation: candidateGeneration, PackageSigner: "TEAM-A", State: TrustPending},
	}
	if _, err := store.Create(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	return trustStateFixture{store: store, request: request, target: target, oldTrust: oldTrust, candidateTrust: candidateTrust, oldGeneration: oldGeneration, candidateGeneration: candidateGeneration}
}

func writeExactTrustedRelease(t *testing.T, release, label string) (PackageTrust, string, ReleaseIdentity, HelperIdentity) {
	t.Helper()
	bin := filepath.Join(release, "Contents", "Resources", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	updater := filepath.Join(bin, "super-dolphin-updater")
	guard := filepath.Join(bin, "super-dolphin-guard")
	if err := os.WriteFile(updater, []byte(label+" updater artifact"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard, []byte(label+" guard artifact"), 0o700); err != nil {
		t.Fatal(err)
	}
	updaterDigest, err := ComputeReleaseDigest(updater)
	if err != nil {
		t.Fatal(err)
	}
	guardDigest, err := ComputeReleaseDigest(guard)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := testPackageTrust(t, "darwin-arm64")
	trust.ManifestPublicKey = base64.StdEncoding.EncodeToString(publicKey)
	trust.UpdaterSHA256 = updaterDigest
	trust.GuardSHA256 = guardDigest
	writePackageTrustFixture(t, filepath.Join(release, "Contents", "Resources"), trust)
	_, generation, err := LoadPackageTrust(filepath.Join(release, "Contents", "Resources"), "darwin-arm64")
	if err != nil {
		t.Fatal(err)
	}
	releaseIdentity := releaseIdentityForTest(t, release, "TEAM-A")
	return trust, generation, releaseIdentity, HelperIdentity{UpdaterSHA256: updaterDigest, GuardSHA256: guardDigest}
}

func (fixture trustStateFixture) assertOld(t *testing.T) {
	t.Helper()
	assertResolvedTrust(t, fixture.target, fixture.oldTrust, fixture.oldGeneration)
}

func (fixture trustStateFixture) assertCandidate(t *testing.T) {
	t.Helper()
	assertResolvedTrust(t, fixture.target, fixture.candidateTrust, fixture.candidateGeneration)
}

func mustAdvanceTrustState(t *testing.T, fixture trustStateFixture, trigger Trigger) {
	t.Helper()
	if _, err := fixture.store.advance(context.Background(), fixture.request.Identity, trigger); err != nil {
		t.Fatal(err)
	}
}

func mustRetainTrustBackup(t *testing.T, fixture trustStateFixture) {
	t.Helper()
	if _, err := fixture.store.RetainBackup(context.Background(), fixture.request.Identity); err != nil {
		t.Fatal(err)
	}
}

func mustInstallTrustCandidate(t *testing.T, fixture trustStateFixture) {
	t.Helper()
	mustRetainTrustBackup(t, fixture)
	if _, err := fixture.store.InstallCandidate(context.Background(), fixture.request.Identity); err != nil {
		t.Fatal(err)
	}
}

func mustClaimHealthyTrustCandidate(t *testing.T, fixture trustStateFixture) ProbationLease {
	t.Helper()
	mustInstallTrustCandidate(t, fixture)
	process := ProcessIdentity{PID: 42, StartToken: "candidate", ExecutableIdentity: "/candidate", ExecutableSHA256: fixture.request.Identity.CandidateRelease.SHA256}
	lease, err := fixture.store.AcquireProbationLease(context.Background(), fixture.request.Identity, ProbationLeaseRequest{OwnerID: "supervisor", Process: process, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := fixture.store.Load(context.Background(), fixture.request.Identity)
	if err != nil {
		t.Fatal(err)
	}
	ack := BuildHealthyACK(transaction, process, time.Now())
	if _, err := fixture.store.RecordHealthyACK(context.Background(), fixture.request.Identity, lease, ack); err != nil {
		t.Fatal(err)
	}
	return lease
}

func assertTrustPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %s error = %v, want not exist", path, err)
	}
}

func copyTrustFixture(t *testing.T, source, target string) {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
