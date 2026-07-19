package appupdaterecovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

const (
	artifactProcessBarrierEnv     = "SUPER_DOLPHIN_ARTIFACT_E2E_PROCESS_BARRIER"
	artifactProcessSlotEnv        = "SUPER_DOLPHIN_ARTIFACT_E2E_PROCESS_SLOT"
	artifactResidualCleanEvidence = "artifact residuals: endpoint=absent marker=absent helper=absent"
)

func TestArtifactRollbackFallbackKillCleansEndpointAndSameTokenRetries(t *testing.T) {
	requireArtifactE2EPlatform(t)
	waitForArtifactProcessBarrier(t)
	fixture := newRecoveryGuardCrashFixture(t)
	crashArtifactRetain(t, fixture, false)
	hold, ignoreTerminate := prepareArtifactFallbackMarkers(t, fixture)
	stopArtifactUpdater(t, fixture)
	if alive, err := artifactProcessAlive(fixture.request.Identity.UpdaterProcess); alive ||
		!errors.Is(err, pidregistry.ErrStableProcessNotFound) {
		t.Fatalf("artifact updater after stop alive=%t error=%v", alive, err)
	}
	startArtifactRecoveryGuard(t, fixture)
	t.Cleanup(func() {
		_ = os.Remove(hold)
		_ = os.Remove(ignoreTerminate)
	})
	record, stable := waitForArtifactRollbackLaunch(t, fixture)
	forceArtifactACKWriteFailure(t, fixture, hold)
	assertArtifactRollbackRecordHasNoACK(t, fixture, record.LaunchToken)
	assertArtifactStableIdentityGone(t, stable)
	assertArtifactRollbackEndpointMissing(t, fixture.request.Identity.TransactionID, record.LaunchToken)
	mustRemoveArtifactPath(t, ignoreTerminate)
	retryRecord := retryArtifactRollbackRestart(t, fixture, record.LaunchToken)
	if err := fixture.cleanupRollbackIntent(); err != nil {
		t.Fatal(err)
	}
	assertArtifactStableIdentityGone(t, artifactRollbackTerminationIdentity(
		retryRecord.ACK.Process, fixture.request.Identity.TransactionID, retryRecord.LaunchToken,
	))
	assertArtifactRollbackEndpointMissing(t, fixture.request.Identity.TransactionID, retryRecord.LaunchToken)
	assertArtifactPathMissing(t, hold)
	assertArtifactPathMissing(t, ignoreTerminate)
	t.Log(artifactResidualCleanEvidence)
}

func prepareArtifactFallbackMarkers(t *testing.T, fixture *recoveryGuardCrashFixture) (string, string) {
	t.Helper()
	hold := fixture.holdMarker
	ignoreTerminate := fixture.ignoreMarker
	if err := os.WriteFile(hold, []byte("hold"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoreTerminate, []byte("force fallback"), 0o600); err != nil {
		t.Fatal(err)
	}
	return hold, ignoreTerminate
}

func artifactRollbackMarkerDir(target string, transactionID TransactionID) string {
	return filepath.Join(
		filepath.Dir(TransactionRootForTarget(target)),
		".artifact-e2e-markers-"+string(transactionID),
	)
}

func forceArtifactACKWriteFailure(t *testing.T, fixture *recoveryGuardCrashFixture, hold string) {
	t.Helper()
	journalDir := filepath.Dir(fixture.store.journalPath(fixture.request.Identity.TransactionID))
	blockedDir := journalDir + ".ack-write-blocked"
	if err := os.Rename(journalDir, blockedDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journalDir, []byte("block ACK journal writes"), 0o600); err != nil {
		_ = os.Rename(blockedDir, journalDir)
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			_ = os.Remove(journalDir)
			_ = os.Rename(blockedDir, journalDir)
		}
	})
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	waitForArtifactGuardFailure(t, fixture)
	if err := os.Remove(journalDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(blockedDir, journalDir); err != nil {
		t.Fatal(err)
	}
	restored = true
}

func assertArtifactRollbackEndpointMissing(t *testing.T, transactionID TransactionID, launchToken string) {
	t.Helper()
	endpoint := artifactRollbackEndpoint(transactionID, launchToken)
	if _, err := os.Lstat(endpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fallback endpoint remains after reaped child cleanup: %v", err)
	}
}

func mustRemoveArtifactPath(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func assertArtifactPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact fixture path remains %q: %v", path, err)
	}
}

func retryArtifactRollbackRestart(t *testing.T, fixture *recoveryGuardCrashFixture, wantToken string) RollbackRestartRecord {
	t.Helper()
	transaction, err := fixture.store.Load(t.Context(), fixture.request.Identity)
	if err != nil {
		t.Fatal(err)
	}
	resolve, launch := RollbackRestartCallbacks(transaction)
	converged := convergeArtifactRollbackRestart(t, fixture, transaction, resolve, launch)
	if !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.LaunchToken != wantToken ||
		converged.RollbackRestart.ACK.LaunchToken != wantToken {
		t.Fatalf("same-token retry did not ACK: %+v", converged.RollbackRestart)
	}
	return converged.RollbackRestart
}

func convergeArtifactRollbackRestart(
	t *testing.T,
	fixture *recoveryGuardCrashFixture,
	transaction Transaction,
	resolve RollbackRestartResolver,
	launch RollbackRestartLauncher,
) Transaction {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		converged, err := fixture.store.ConvergeRollbackRestart(t.Context(), transaction.Identity, resolve, launch)
		if err == nil {
			return converged
		}
		if !errors.Is(err, pidregistry.ErrStableProcessIdentityRead) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestArtifactRollbackEvidenceIsolationAcrossProcesses(t *testing.T) {
	requireArtifactE2EPlatform(t)
	repoRoot := filepath.Clean(filepath.Join(mustArtifactWorkingDirectory(t), "..", "..", ".."))
	barrier := filepath.Join(canonicalTestTempDir(t), "artifact-process-barrier")
	readyPaths := []string{barrier + ".0.ready", barrier + ".1.ready"}
	processes := startArtifactEvidenceProcesses(t, repoRoot, barrier, len(readyPaths))
	waitForArtifactReadyFiles(t, readyPaths)
	if err := os.WriteFile(barrier, []byte("start"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForArtifactEvidenceProcesses(t, processes)
}

type artifactEvidenceProcess struct {
	command *exec.Cmd
	output  bytes.Buffer
}

func startArtifactEvidenceProcesses(t *testing.T, repoRoot, barrier string, count int) []*artifactEvidenceProcess {
	t.Helper()
	pattern := "^(TestArtifactRollbackFallbackCleanupReapsAfterValidationFailure|TestArtifactRollbackFallbackKillCleansEndpointAndSameTokenRetries)$"
	processes := make([]*artifactEvidenceProcess, count)
	t.Cleanup(func() {
		for _, process := range processes {
			if process != nil && process.command.Process != nil && process.command.ProcessState == nil {
				_ = process.command.Process.Kill()
				_ = process.command.Wait()
			}
		}
	})
	for index := range processes {
		process := &artifactEvidenceProcess{command: exec.Command(
			"go", "test", "-v", "./internal/platform/appupdaterecovery", "-run", pattern, "-count=1",
		)}
		process.command.Dir = repoRoot
		process.command.Env = append(os.Environ(),
			artifactProcessBarrierEnv+"="+barrier,
			artifactProcessSlotEnv+"="+string(rune('0'+index)),
		)
		process.command.Stdout = &process.output
		process.command.Stderr = &process.output
		processes[index] = process
		if err := process.command.Start(); err != nil {
			t.Fatalf("start artifact evidence process %d: %v", index, err)
		}
	}
	return processes
}

func waitForArtifactEvidenceProcesses(t *testing.T, processes []*artifactEvidenceProcess) {
	t.Helper()
	errs := make([]error, len(processes))
	var waits sync.WaitGroup
	for index := range processes {
		waits.Go(func() { errs[index] = processes[index].command.Wait() })
	}
	waits.Wait()
	for index, err := range errs {
		output := processes[index].output.String()
		if err != nil {
			t.Fatalf("artifact evidence process %d failed: %v\n%s", index, err, output)
		}
		if !strings.Contains(output, "PASS\n") || strings.Count(output, artifactResidualCleanEvidence) != 2 {
			t.Fatalf("artifact evidence process %d lacked PASS or zero-residual proof:\n%s", index, output)
		}
	}
}

func mustArtifactWorkingDirectory(t *testing.T) string {
	t.Helper()
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return workDir
}

func waitForArtifactProcessBarrier(t *testing.T) {
	t.Helper()
	barrier := os.Getenv(artifactProcessBarrierEnv)
	if barrier == "" {
		return
	}
	slot := os.Getenv(artifactProcessSlotEnv)
	if slot == "" {
		t.Fatalf("%s is required with %s", artifactProcessSlotEnv, artifactProcessBarrierEnv)
	}
	ready := barrier + "." + slot + ".ready"
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(ready) })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(barrier); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("artifact process barrier timed out")
}

func waitForArtifactReadyFiles(t *testing.T, readyPaths []string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ready := 0
		for _, path := range readyPaths {
			if _, err := os.Stat(path); err == nil {
				ready++
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
		if ready == len(readyPaths) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("artifact evidence processes did not reach barrier")
}

func waitForArtifactRollbackLaunch(t *testing.T, fixture *recoveryGuardCrashFixture) (RollbackRestartRecord, pidregistry.StableProcessIdentity) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	executable := filepath.Join(fixture.target, "Contents", "MacOS", "agent-terminal")
	var lastRecord RollbackRestartRecord
	var lastReadErr, lastFindErr error
	for time.Now().Before(deadline) {
		record, err := readArtifactRollbackRecord(fixture)
		lastRecord, lastReadErr = record, err
		if err == nil && record.IntentPresent {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			stable, found, findErr := pidregistry.FindStableProcessByArgumentContext(
				ctx, "--super-dolphin-rollback-launch-token="+record.LaunchToken, executable,
			)
			cancel()
			lastFindErr = findErr
			if findErr == nil && found {
				return record, stable
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-fixture.guardDone:
		fixture.guardWait.Wait()
	default:
	}
	t.Fatalf(
		"rollback helper was not launched before ACK: record=%+v read=%v find=%v guard=%v stderr=%q",
		lastRecord, lastReadErr, lastFindErr, fixture.guardErr, fixture.guardStderr.String(),
	)
	return RollbackRestartRecord{}, pidregistry.StableProcessIdentity{}
}

func readArtifactRollbackRecord(fixture *recoveryGuardCrashFixture) (RollbackRestartRecord, error) {
	raw, err := os.ReadFile(fixture.store.journalPath(fixture.request.Identity.TransactionID))
	if err != nil {
		return RollbackRestartRecord{}, err
	}
	journal, err := decodeJournal(raw)
	if err != nil {
		return RollbackRestartRecord{}, err
	}
	return journal.RollbackRestart, nil
}

func waitForArtifactGuardFailure(t *testing.T, fixture *recoveryGuardCrashFixture) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-fixture.guardDone:
		fixture.guardWait.Wait()
	case <-timer.C:
		t.Fatal("Guard did not return after rollback ACK write failure")
	}
	if fixture.guardErr == nil {
		t.Fatal("Guard returned success after rollback ACK write failure")
	}
}

func assertArtifactRollbackRecordHasNoACK(t *testing.T, fixture *recoveryGuardCrashFixture, launchToken string) {
	t.Helper()
	record, err := readArtifactRollbackRecord(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if record.LaunchToken != launchToken || record.ACKPresent || record.ACK != (RollbackRestartACK{}) {
		t.Fatalf("rollback record mutated after ACK write failure: %+v", record)
	}
}

func assertArtifactStableIdentityGone(t *testing.T, identity pidregistry.StableProcessIdentity) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err := pidregistry.CaptureStableProcessIdentity(identity.PID)
		if errors.Is(err, pidregistry.ErrStableProcessNotFound) {
			return
		}
		if err == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		t.Fatal(err)
	}
	t.Fatal("rollback helper identity remained after ACK write failure cleanup")
}
