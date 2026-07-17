package appupdaterecovery

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

func TestIndependentArtifactCrashRollbackReopensOldRelease(t *testing.T) {
	requireArtifactE2EPlatform(t)
	fixture := newArtifactE2EFixture(t)
	mustInstallArtifactCandidate(t, fixture)
	candidate := filepath.Join(fixture.target, "Contents", "MacOS", "agent-terminal")
	cmd := exec.Command(candidate)
	cmd.Env = append(os.Environ(), "ARTIFACT_MODE=crash")
	if err := cmd.Run(); err == nil {
		t.Fatal("candidate crash process error = nil")
	}
	if _, err := fixture.store.RollbackUnclaimedProbation(context.Background(), fixture.request.Identity); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(filepath.Join(fixture.target, "Contents", "MacOS", "agent-terminal")).CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "old:launcher" {
		t.Fatalf("reopen old release output=%q error=%v", output, err)
	}
	assertResolvedTrust(t, fixture.target, fixture.oldTrust, fixture.oldGeneration)
	logArtifactEvidence(t, fixture)
}

func TestIndependentArtifactHealthyACKObservationCommitsTrust(t *testing.T) {
	requireArtifactE2EPlatform(t)
	fixture := newArtifactE2EFixture(t)
	mustInstallArtifactCandidate(t, fixture)
	candidate := filepath.Join(fixture.target, "Contents", "MacOS", "agent-terminal")
	cmd := exec.Command(candidate)
	cmd.Env = append(os.Environ(), "ARTIFACT_MODE=serve")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer reapArtifactProcess(cmd)
	process := captureArtifactProcess(t, cmd.Process.Pid, candidate)
	lease, err := fixture.store.AcquireProbationLease(context.Background(), fixture.request.Identity, ProbationLeaseRequest{OwnerID: "artifact-e2e", Process: process, TTL: time.Minute})
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
	supervisor, err := NewProbationSupervisor(ProbationSupervisorConfig{
		Store: fixture.store, Identity: fixture.request.Identity, Lease: lease,
		ProcessAlive: artifactProcessAlive, StopCandidate: func(context.Context, ProcessIdentity) error { return errors.New("unexpected stop") },
		RestartOldRelease: func(context.Context, Transaction) error { return errors.New("unexpected restart") },
		ObservationPeriod: 20 * time.Millisecond, PollInterval: 5 * time.Millisecond, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := supervisor.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertTrustPathMissing(t, fixture.request.Paths.Backup)
	assertResolvedTrust(t, fixture.target, fixture.candidateTrust, fixture.candidateGeneration)
	logArtifactEvidence(t, fixture)
}

func TestRecoveryGuardProcessRestoresBackupRetainedAfterUpdaterCrash(t *testing.T) {
	runRecoveryGuardCrashE2E(t, false)
}

func TestRecoveryGuardProcessRestoresAfterRetainEffectCrash(t *testing.T) {
	runRecoveryGuardCrashE2E(t, true)
}

func TestArtifactRollbackFallbackCleanupReapsAfterValidationFailure(t *testing.T) {
	requireArtifactE2EPlatform(t)
	fixture := newArtifactE2EFixture(t)
	executable := filepath.Join(fixture.target, "Contents", "MacOS", "agent-terminal")
	token := strings.Repeat("d", 64)
	transactionID := fixture.request.Identity.TransactionID
	cmd := exec.Command(executable, "--super-dolphin-rollback-launch-token="+token)
	env, err := runtimeenv.AppendRecoveryLaunchEnvironment(os.Environ(), runtimeenv.RecoveryLaunch{
		TransactionRoot: TransactionRootForTarget(fixture.target), TransactionID: string(transactionID),
		ExecutableIdentity: executable, ExecutableSHA256: mustArtifactDigest(t, executable),
		TerminationEndpoint: artifactRollbackEndpoint(transactionID, token), TerminationToken: token, ContractPresent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	process := RollbackRestartProcess{
		PID: stable.PID, StartToken: stable.ProcessStartToken, ExecutableIdentity: stable.ExecutableIdentity,
		ExecutableSHA256: mustArtifactDigest(t, executable),
	}
	termination := artifactRollbackTerminationIdentity(process, transactionID, token)
	waitDone := make(chan struct{})
	var waitErr error
	var waitOnce sync.Once
	var waiter sync.WaitGroup
	startWait := func() {
		waitOnce.Do(func() {
			waiter.Go(func() {
				waitErr = cmd.Wait()
				close(waitDone)
			})
		})
	}
	t.Cleanup(func() {
		startWait()
		if err := cleanupArtifactRollbackProcess(termination); err != nil {
			t.Errorf("fallback cleanup after validation failure: %v", err)
		}
		<-waitDone
		waiter.Wait()
		if waitErr != nil {
			t.Errorf("fallback wait/reap after validation failure: %v", waitErr)
		}
	})
	if err := validateArtifactRollbackProcess(process, executable, strings.Repeat("0", 64)); err == nil {
		t.Fatal("artifact process validation unexpectedly accepted the wrong digest")
	}
	startWait()
	if err := cleanupArtifactRollbackProcess(termination); err != nil {
		t.Fatal(err)
	}
	<-waitDone
	if waitErr != nil {
		t.Fatalf("artifact helper wait/reap error = %v", waitErr)
	}
	if cmd.ProcessState == nil {
		t.Fatal("artifact helper was not reaped")
	}
}

func runRecoveryGuardCrashE2E(t *testing.T, crashAfterEffect bool) {
	requireArtifactE2EPlatform(t)
	fixture := newRecoveryGuardCrashFixture(t)
	startArtifactRecoveryGuard(t, fixture)
	crashArtifactRetain(t, fixture, crashAfterEffect)
	assertTrustPathMissing(t, fixture.target)
	waitForArtifactGuardState(t, fixture, StateBackupRetained)
	assertArtifactGuardKeepsBackupRetained(t, fixture)
	stopArtifactUpdater(t, fixture)
	waitForArtifactGuardState(t, fixture, StateRolledBack)
	waitForArtifactGuardExit(t, fixture)
	assertResolvedTrust(t, fixture.target, fixture.oldTrust, fixture.oldGeneration)
	assertArtifactRollbackRestartAliveAndTerminate(t, fixture)
	t.Logf(
		"backup_retained_guard=%s updater=%s old_release=%s signer=%s",
		fixture.request.Identity.OldHelpers.GuardSHA256,
		fixture.request.Identity.OldHelpers.UpdaterSHA256,
		fixture.request.Identity.OldRelease.SHA256,
		fixture.request.Trust.PackageSigner,
	)
}

type recoveryGuardCrashFixture struct {
	store         *Store
	request       CreateRequest
	target        string
	oldTrust      PackageTrust
	oldGeneration string
	updater       *exec.Cmd
	guard         *exec.Cmd
	guardDone     chan struct{}
	guardErr      error
	guardStderr   bytes.Buffer
	guardWait     sync.WaitGroup
	cleanupOnce   sync.Once
	cleanupErr    error
}

func newRecoveryGuardCrashFixture(t *testing.T) *recoveryGuardCrashFixture {
	t.Helper()
	root := canonicalTestTempDir(t)
	source := filepath.Join(root, "artifact_main.go")
	if err := os.WriteFile(source, []byte(artifactProgramSource), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "Super Dolphin.app")
	id := TransactionID("bbccddeeff00112233445566778899aa")
	paths, err := PathsFor(target, id)
	if err != nil {
		t.Fatal(err)
	}
	oldTrust, oldGeneration, oldRelease, oldHelpers := buildGuardedArtifactRelease(t, source, target, "old")
	_, candidateGeneration, candidateRelease, candidateHelpers := buildArtifactRelease(t, source, paths.Staging, "new")
	updaterPath := filepath.Join(target, "Contents", "Resources", "bin", "super-dolphin-updater")
	updater := exec.Command(updaterPath)
	updater.Env = append(os.Environ(), "ARTIFACT_MODE=serve")
	if err := updater.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reapArtifactProcess(updater) })
	writeArtifactCapsule(t, paths, target)
	store, err := NewStore(TransactionRootForTarget(target))
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Identity: Identity{
			TransactionID: id, AttemptID: "backup-retained-crash", OldRelease: oldRelease, CandidateRelease: candidateRelease,
			OldHelpers: oldHelpers, CandidateHelpers: candidateHelpers, UpdaterProcess: captureArtifactProcess(t, updater.Process.Pid, updaterPath),
		},
		Paths: paths,
		Trust: TrustGeneration{PreviousGeneration: oldGeneration, Generation: candidateGeneration, PackageSigner: "TEAM-E2E", State: TrustPending},
	}
	if _, err := store.Create(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	return &recoveryGuardCrashFixture{
		store: store, request: request, target: target, oldTrust: oldTrust,
		oldGeneration: oldGeneration, updater: updater,
	}
}

func startArtifactRecoveryGuard(t *testing.T, fixture *recoveryGuardCrashFixture) {
	t.Helper()
	request := fixture.request
	guard := exec.Command(
		filepath.Join(request.Paths.RecoveryDir, "super-dolphin-guard"),
		TransactionRootForTarget(fixture.target),
		string(request.Identity.TransactionID),
		"old",
	)
	guard.Stderr = &fixture.guardStderr
	ready, err := guard.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Start(); err != nil {
		t.Fatal(err)
	}
	fixture.guard = guard
	t.Cleanup(func() {
		if err := fixture.cleanupRollbackIntent(); err != nil {
			t.Errorf("fixture rollback intent cleanup: %v", err)
		}
	})
	fixture.guardDone = make(chan struct{})
	fixture.guardWait.Go(func() {
		fixture.guardErr = guard.Wait()
		close(fixture.guardDone)
	})
	t.Cleanup(func() {
		select {
		case <-fixture.guardDone:
			fixture.guardWait.Wait()
			return
		default:
		}
		_ = fixture.guard.Process.Kill()
		<-fixture.guardDone
		fixture.guardWait.Wait()
	})
	receipt := readArtifactGuardReceipt(t, ready)
	if receipt.TransactionID != request.Identity.TransactionID ||
		receipt.AttemptID != request.Identity.AttemptID ||
		receipt.Phase != GuardReceiptPhaseArmed ||
		receipt.Process.ExecutableSHA256 != request.Identity.OldHelpers.GuardSHA256 {
		t.Fatalf("Guard readiness receipt = %+v", receipt)
	}
	if err := ready.Close(); err != nil {
		t.Fatal(err)
	}
}

func crashArtifactRetain(t *testing.T, fixture *recoveryGuardCrashFixture, crashAfterEffect bool) {
	t.Helper()
	if !crashAfterEffect {
		if err := retainArtifactBackup(fixture.store, fixture.request.Identity); err != nil {
			t.Fatal(err)
		}
		return
	}
	crashErr := errors.New("simulated updater crash after retain effect")
	fixture.store.afterEffect = func(state State) error {
		if state == StateBackupPending {
			return crashErr
		}
		return nil
	}
	if err := retainArtifactBackup(fixture.store, fixture.request.Identity); !errors.Is(err, crashErr) {
		t.Fatalf("RetainBackup() error = %v, want simulated after-effect crash", err)
	}
}

func stopArtifactUpdater(t *testing.T, fixture *recoveryGuardCrashFixture) {
	t.Helper()
	if err := fixture.updater.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = fixture.updater.Wait()
}

func waitForArtifactGuardState(t *testing.T, fixture *recoveryGuardCrashFixture, want State) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	poll := time.NewTicker(25 * time.Millisecond)
	defer deadline.Stop()
	defer poll.Stop()
	for {
		select {
		case <-fixture.guardDone:
			fixture.guardWait.Wait()
			transaction, loadErr := fixture.store.Load(context.Background(), fixture.request.Identity)
			if artifactGuardExitedAtState(fixture, transaction, loadErr, want) {
				return
			}
			t.Fatalf("Guard exited before state %q: guard=%v stderr=%q state=%q load=%v", want, fixture.guardErr, fixture.guardStderr.String(), transaction.State, loadErr)
		case <-poll.C:
			transaction, err := fixture.store.Load(context.Background(), fixture.request.Identity)
			if err == nil && transaction.State == want {
				return
			}
		case <-deadline.C:
			transaction, err := fixture.store.Load(context.Background(), fixture.request.Identity)
			t.Fatalf("transaction state = %q error=%v, want %q", transaction.State, err, want)
		}
	}
}

func artifactGuardExitedAtState(fixture *recoveryGuardCrashFixture, transaction Transaction, loadErr error, want State) bool {
	return fixture.guardErr == nil && loadErr == nil && transaction.State == want && want == StateRolledBack
}

func waitForArtifactGuardExit(t *testing.T, fixture *recoveryGuardCrashFixture) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	select {
	case <-fixture.guardDone:
		fixture.guardWait.Wait()
	case <-timeout.C:
		killErr := fixture.guard.Process.Kill()
		<-fixture.guardDone
		fixture.guardWait.Wait()
		t.Fatalf("Guard exit timeout: kill=%v guard=%v stderr=%q", killErr, fixture.guardErr, fixture.guardStderr.String())
	}
	if fixture.guardErr != nil {
		t.Fatalf("Guard exit error = %v stderr=%q", fixture.guardErr, fixture.guardStderr.String())
	}
}

func assertArtifactGuardKeepsBackupRetained(t *testing.T, fixture *recoveryGuardCrashFixture) {
	t.Helper()
	observation := time.NewTimer(6 * 100 * time.Millisecond)
	poll := time.NewTicker(25 * time.Millisecond)
	defer observation.Stop()
	defer poll.Stop()
	checks := 0
	for {
		select {
		case <-poll.C:
			transaction, err := fixture.store.Load(context.Background(), fixture.request.Identity)
			if errors.Is(err, ErrTransactionBusy) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if transaction.State != StateBackupRetained {
				t.Fatalf("live updater after bundle rename changed state to %q", transaction.State)
			}
			checks++
		case <-observation.C:
			if checks < 3 {
				t.Fatalf("observed only %d Guard polls after bundle rename", checks)
			}
			return
		}
	}
}

type artifactE2EFixture struct {
	store               *Store
	request             CreateRequest
	target              string
	oldTrust            PackageTrust
	candidateTrust      PackageTrust
	oldGeneration       string
	candidateGeneration string
}

func newArtifactE2EFixture(t *testing.T) artifactE2EFixture {
	t.Helper()
	root := canonicalTestTempDir(t)
	source := filepath.Join(root, "artifact_main.go")
	if err := os.WriteFile(source, []byte(artifactProgramSource), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "Super Dolphin.app")
	id := TransactionID("aabbccddeeff00112233445566778899")
	paths, err := PathsFor(target, id)
	if err != nil {
		t.Fatal(err)
	}
	oldTrust, oldGeneration, oldRelease, oldHelpers := buildArtifactRelease(t, source, target, "old")
	candidateTrust, candidateGeneration, candidateRelease, candidateHelpers := buildArtifactRelease(t, source, paths.Staging, "new")
	writeArtifactCapsule(t, paths, target)
	store, err := NewStore(TransactionRootForTarget(target))
	if err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Identity: Identity{
			TransactionID: id, AttemptID: "artifact-e2e", OldRelease: oldRelease, CandidateRelease: candidateRelease,
			OldHelpers: oldHelpers, CandidateHelpers: candidateHelpers, UpdaterProcess: fixtureUpdaterProcess(),
		},
		Paths: paths,
		Trust: TrustGeneration{PreviousGeneration: oldGeneration, Generation: candidateGeneration, PackageSigner: "TEAM-E2E", State: TrustPending},
	}
	if _, err := store.Create(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	return artifactE2EFixture{store: store, request: request, target: target, oldTrust: oldTrust, candidateTrust: candidateTrust, oldGeneration: oldGeneration, candidateGeneration: candidateGeneration}
}

func buildArtifactRelease(t *testing.T, source, release, version string) (PackageTrust, string, ReleaseIdentity, HelperIdentity) {
	t.Helper()
	macOS := filepath.Join(release, "Contents", "MacOS")
	bin := filepath.Join(release, "Contents", "Resources", "bin")
	if err := os.MkdirAll(macOS, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	buildIndependentArtifact(t, source, filepath.Join(macOS, "agent-terminal"), version, "launcher")
	updater := filepath.Join(bin, "super-dolphin-updater")
	guard := filepath.Join(bin, "super-dolphin-guard")
	buildIndependentArtifact(t, source, updater, version, "updater")
	buildIndependentArtifact(t, source, guard, version, "guard")
	updaterDigest := mustArtifactDigest(t, updater)
	guardDigest := mustArtifactDigest(t, guard)
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := PackageTrust{
		SchemaVersion: PackageTrustSchemaVersion, Enabled: true, Production: true, Platform: "darwin-arm64",
		Source:            UpdateSource{Kind: UpdateSourceGitHub, Value: "super-dolphin/releases"},
		ManifestPublicKey: base64.StdEncoding.EncodeToString(publicKey), Channel: "gray",
		SignerPolicy: PackageSignerPolicyExact, SignerIdentity: "TEAM-E2E",
		UpdaterSHA256: updaterDigest, GuardSHA256: guardDigest,
	}
	writePackageTrustFixture(t, filepath.Join(release, "Contents", "Resources"), trust)
	_, generation, err := LoadPackageTrust(filepath.Join(release, "Contents", "Resources"), "darwin-arm64")
	if err != nil {
		t.Fatal(err)
	}
	releaseIdentity := releaseIdentityForTest(t, release, "TEAM-E2E")
	return trust, generation, releaseIdentity, HelperIdentity{UpdaterSHA256: updaterDigest, GuardSHA256: guardDigest}
}

func buildGuardedArtifactRelease(t *testing.T, source, release, version string) (PackageTrust, string, ReleaseIdentity, HelperIdentity) {
	t.Helper()
	trust, _, _, helpers := buildArtifactRelease(t, source, release, version)
	guard := filepath.Join(release, "Contents", "Resources", "bin", "super-dolphin-guard")
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(workDir, "..", "..", ".."))
	cmd := exec.Command("go", "build", "-trimpath", "-o", guard, "./cmd/super-dolphin-guard")
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build package-owned Guard: %v: %s", err, output)
	}
	trust.GuardSHA256 = mustArtifactDigest(t, guard)
	writePackageTrustFixture(t, filepath.Join(release, "Contents", "Resources"), trust)
	_, generation, err := LoadPackageTrust(filepath.Join(release, "Contents", "Resources"), "darwin-arm64")
	if err != nil {
		t.Fatal(err)
	}
	helpers.GuardSHA256 = trust.GuardSHA256
	return trust, generation, releaseIdentityForTest(t, release, "TEAM-E2E"), helpers
}

func buildIndependentArtifact(t *testing.T, source, output, version, role string) {
	t.Helper()
	ldflags := fmt.Sprintf("-X main.release=%s -X main.role=%s", version, role)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", output, source)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build independent artifact %s/%s: %v: %s", version, role, err, output)
	}
}

func writeArtifactCapsule(t *testing.T, paths Paths, oldRelease string) {
	t.Helper()
	if err := os.MkdirAll(paths.RecoveryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	copyTrustFixture(t, filepath.Join(oldRelease, "Contents", "Resources", PackageTrustFilename), filepath.Join(paths.RecoveryDir, PackageTrustFilename))
	copyTrustFixture(t, filepath.Join(oldRelease, "Contents", "Resources", "bin", "super-dolphin-updater"), filepath.Join(paths.RecoveryDir, "super-dolphin-updater"))
	copyTrustFixture(t, filepath.Join(oldRelease, "Contents", "Resources", "bin", "super-dolphin-guard"), filepath.Join(paths.RecoveryDir, "super-dolphin-guard"))
	if err := os.Chmod(filepath.Join(paths.RecoveryDir, "super-dolphin-updater"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(paths.RecoveryDir, "super-dolphin-guard"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustInstallArtifactCandidate(t *testing.T, fixture artifactE2EFixture) {
	t.Helper()
	if _, err := fixture.store.RetainBackup(context.Background(), fixture.request.Identity); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.InstallCandidate(context.Background(), fixture.request.Identity); err != nil {
		t.Fatal(err)
	}
}

func captureArtifactProcess(t *testing.T, pid int, executable string) ProcessIdentity {
	t.Helper()
	stable, err := pidregistry.CaptureStableProcessIdentity(pid)
	if err != nil {
		t.Fatal(err)
	}
	return ProcessIdentity{PID: stable.PID, StartToken: stable.ProcessStartToken, ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: mustArtifactDigest(t, executable)}
}

func artifactProcessAlive(process ProcessIdentity) (bool, error) {
	stable, err := pidregistry.CaptureStableProcessIdentity(process.PID)
	if err != nil {
		return false, err
	}
	return stable.ProcessStartToken == process.StartToken && stable.ExecutableIdentity == process.ExecutableIdentity, nil
}

func assertArtifactRollbackRestartAliveAndTerminate(t *testing.T, fixture *recoveryGuardCrashFixture) {
	t.Helper()
	transaction, err := fixture.store.Load(context.Background(), fixture.request.Identity)
	if err != nil {
		t.Fatal(err)
	}
	record := transaction.RollbackRestart
	if !record.ACKPresent || record.ACK.LaunchToken != record.LaunchToken {
		t.Fatalf("rollback restart ACK = %+v, intent = %+v", record.ACK, record)
	}
	executable := filepath.Join(fixture.target, "Contents", "MacOS", "agent-terminal")
	process := record.ACK.Process
	if err := validateArtifactRollbackProcess(process, executable, mustArtifactDigest(t, executable)); err != nil {
		t.Fatal(err)
	}
	alive, err := artifactProcessAlive(ProcessIdentity{
		PID: process.PID, StartToken: process.StartToken,
		ExecutableIdentity: process.ExecutableIdentity, ExecutableSHA256: process.ExecutableSHA256,
	})
	if err != nil || !alive {
		t.Fatalf("rollback restart exact process alive = %t, error = %v, ACK = %+v", alive, err, record.ACK)
	}
	if err := fixture.cleanupRollbackIntent(); err != nil {
		t.Fatal(err)
	}
}

func validateArtifactRollbackProcess(process RollbackRestartProcess, executable, digest string) error {
	if process.ExecutableIdentity != executable || process.ExecutableSHA256 != digest {
		return fmt.Errorf("rollback restart process = %+v, executable = %q digest = %q", process, executable, digest)
	}
	return nil
}

func artifactRollbackTerminationIdentity(process RollbackRestartProcess, transactionID TransactionID, launchToken string) pidregistry.StableProcessIdentity {
	return pidregistry.StableProcessIdentity{
		PID: process.PID, ProcessStartToken: process.StartToken, ExecutableIdentity: process.ExecutableIdentity,
		TerminationEndpoint: artifactRollbackEndpoint(transactionID, launchToken),
		TerminationToken:    launchToken,
	}
}

func artifactRollbackEndpoint(transactionID TransactionID, launchToken string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf(
		"sd-rr-%s-%s.sock", string(transactionID)[:8], launchToken[:rollbackRestartEndpointNameBytes],
	))
}

func (fixture *recoveryGuardCrashFixture) cleanupRollbackIntent() error {
	fixture.cleanupOnce.Do(func() {
		fixture.cleanupErr = cleanupArtifactRollbackIntent(fixture)
	})
	return fixture.cleanupErr
}

func cleanupArtifactRollbackIntent(fixture *recoveryGuardCrashFixture) error {
	transaction, err := fixture.store.Load(context.Background(), fixture.request.Identity)
	if err != nil {
		return err
	}
	record := transaction.RollbackRestart
	if !record.IntentPresent {
		return nil
	}
	executable := filepath.Join(fixture.target, "Contents", "MacOS", "agent-terminal")
	if record.ACKPresent {
		return cleanupArtifactRollbackProcess(artifactRollbackTerminationIdentity(
			record.ACK.Process, transaction.Identity.TransactionID, record.LaunchToken,
		))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stable, found, err := pidregistry.FindStableProcessByArgumentContext(
		ctx, "--super-dolphin-rollback-launch-token="+record.LaunchToken, executable,
	)
	if err != nil || !found {
		return err
	}
	stable.TerminationEndpoint = artifactRollbackEndpoint(transaction.Identity.TransactionID, record.LaunchToken)
	stable.TerminationToken = record.LaunchToken
	return cleanupArtifactRollbackProcess(stable)
}

func cleanupArtifactRollbackProcess(identity pidregistry.StableProcessIdentity) error {
	stable, err := pidregistry.CaptureStableProcessIdentity(identity.PID)
	if errors.Is(err, pidregistry.ErrStableProcessNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if stable.ProcessStartToken != identity.ProcessStartToken || stable.ExecutableIdentity != identity.ExecutableIdentity {
		return pidregistry.ErrStableProcessIdentityMismatch
	}
	if err := waitForArtifactTerminationEndpoint(identity.TerminationEndpoint); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return pidregistry.TerminateExactProcess(ctx, identity)
}

func waitForArtifactTerminationEndpoint(endpoint string) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(endpoint); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("artifact termination endpoint readiness timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustArtifactDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := ComputeReleaseDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func reapArtifactProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func readArtifactGuardReceipt(t *testing.T, reader io.Reader) GuardReadyReceipt {
	t.Helper()
	deadlineReader, ok := reader.(interface {
		SetReadDeadline(time.Time) error
	})
	if !ok {
		t.Fatal("Guard readiness pipe does not support a bounded read deadline")
	}
	if err := deadlineReader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(io.LimitReader(reader, 64*1024)).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read bounded Guard readiness receipt: %v", err)
	}
	receipt, err := DecodeGuardReadyReceipt(line)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func retainArtifactBackup(store *Store, identity Identity) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.RetainBackup(context.Background(), identity); err == nil {
			return nil
		} else if !errors.Is(err, ErrTransactionBusy) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("timed out retaining artifact backup after Guard armed")
}

func requireArtifactE2EPlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("independent artifact process E2E is declared only for darwin-arm64")
	}
}

func logArtifactEvidence(t *testing.T, fixture artifactE2EFixture) {
	t.Helper()
	t.Logf("old_release=%s candidate_release=%s old_updater=%s candidate_updater=%s old_guard=%s candidate_guard=%s signer=%s",
		fixture.request.Identity.OldRelease.SHA256, fixture.request.Identity.CandidateRelease.SHA256,
		fixture.request.Identity.OldHelpers.UpdaterSHA256, fixture.request.Identity.CandidateHelpers.UpdaterSHA256,
		fixture.request.Identity.OldHelpers.GuardSHA256, fixture.request.Identity.CandidateHelpers.GuardSHA256,
		fixture.request.Trust.PackageSigner)
}
