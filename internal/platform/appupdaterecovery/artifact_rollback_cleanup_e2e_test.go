package appupdaterecovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

func TestArtifactRollbackFallbackKillCleansEndpointAndSameTokenRetries(t *testing.T) {
	requireArtifactE2EPlatform(t)
	fixture := newRecoveryGuardCrashFixture(t)
	crashArtifactRetain(t, fixture, false)
	hold, ignoreTerminate := prepareArtifactFallbackMarkers(t, fixture.request.Identity.TransactionID)
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
	assertArtifactRollbackEndpointMissing(t, fixture, record.LaunchToken)
	mustRemoveArtifactPath(t, ignoreTerminate)
	retryArtifactRollbackRestart(t, fixture, record.LaunchToken)
	if err := fixture.cleanupRollbackIntent(); err != nil {
		t.Fatal(err)
	}
}

func prepareArtifactFallbackMarkers(t *testing.T, transactionID TransactionID) (string, string) {
	t.Helper()
	hold := artifactRollbackHoldPath(transactionID)
	ignoreTerminate := artifactRollbackIgnoreTerminatePath(transactionID)
	if err := os.WriteFile(hold, []byte("hold"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoreTerminate, []byte("force fallback"), 0o600); err != nil {
		t.Fatal(err)
	}
	return hold, ignoreTerminate
}

func forceArtifactACKWriteFailure(t *testing.T, fixture *recoveryGuardCrashFixture, hold string) {
	t.Helper()
	journalDir := filepath.Dir(fixture.store.journalPath(fixture.request.Identity.TransactionID))
	info, err := os.Stat(journalDir)
	if err != nil {
		t.Fatal(err)
	}
	originalMode := info.Mode().Perm()
	if err := os.Chmod(journalDir, 0o500); err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			_ = os.Chmod(journalDir, originalMode)
		}
	})
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	waitForArtifactGuardFailure(t, fixture)
	if err := os.Chmod(journalDir, originalMode); err != nil {
		t.Fatal(err)
	}
	restored = true
}

func assertArtifactRollbackEndpointMissing(t *testing.T, fixture *recoveryGuardCrashFixture, launchToken string) {
	t.Helper()
	endpoint := artifactRollbackEndpoint(fixture.request.Identity.TransactionID, launchToken)
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

func artifactRollbackHoldPath(transactionID TransactionID) string {
	return filepath.Join(os.TempDir(), "sd-art-hold-"+string(transactionID))
}

func artifactRollbackIgnoreTerminatePath(transactionID TransactionID) string {
	return filepath.Join(os.TempDir(), "sd-art-ignore-terminate-"+string(transactionID))
}

func retryArtifactRollbackRestart(t *testing.T, fixture *recoveryGuardCrashFixture, wantToken string) {
	t.Helper()
	transaction, err := fixture.store.Load(t.Context(), fixture.request.Identity)
	if err != nil {
		t.Fatal(err)
	}
	resolve, launch := RollbackRestartCallbacks(transaction)
	converged, err := fixture.store.ConvergeRollbackRestart(t.Context(), transaction.Identity, resolve, launch)
	if err != nil {
		t.Fatal(err)
	}
	if !converged.RollbackRestart.ACKPresent || converged.RollbackRestart.LaunchToken != wantToken ||
		converged.RollbackRestart.ACK.LaunchToken != wantToken {
		t.Fatalf("same-token retry did not ACK: %+v", converged.RollbackRestart)
	}
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
