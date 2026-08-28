//go:build !windows

package appupdaterecovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	"go.uber.org/goleak"
)

func TestPackageTrustRejectsTrustFileAlias(t *testing.T) {
	root := canonicalTestTempDir(t)
	resources := filepath.Join(root, "Resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodePackageTrust(testPackageTrust(t, "darwin-arm64"))
	if err != nil {
		t.Fatal(err)
	}
	realTrust := filepath.Join(root, "attacker-trust.json")
	if err := os.WriteFile(realTrust, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	requireSymlink(t, realTrust, filepath.Join(resources, PackageTrustFilename))
	if _, _, err := LoadPackageTrust(resources, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "trust file alias") {
		t.Fatalf("LoadPackageTrust(trust alias) error = %v", err)
	}
}

func TestPackageTrustRejectsTransactionTargetAlias(t *testing.T) {
	root := canonicalTestTempDir(t)
	realTarget := filepath.Join(root, "Real.app")
	resources := filepath.Join(realTarget, "Contents", "Resources")
	if err := os.MkdirAll(resources, 0o700); err != nil {
		t.Fatal(err)
	}
	writePackageTrustFixture(t, resources, testPackageTrust(t, "darwin-arm64"))
	aliasTarget := filepath.Join(root, "Super Dolphin.app")
	requireSymlink(t, realTarget, aliasTarget)
	if _, _, err := ResolveTransactionBoundPackageTrust(context.Background(), aliasTarget, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "target alias") {
		t.Fatalf("ResolveTransactionBoundPackageTrust(target alias) error = %v", err)
	}
}

func TestRollbackRestartLauncherReapsEveryPostStartFailure(t *testing.T) {
	for _, failure := range []string{"capture", "validation"} {
		t.Run(failure, func(t *testing.T) { runRollbackRestartLauncherFailure(t, failure) })
	}
}

func TestRollbackRestartCleanupReturnsWhenTerminationAndKillDoNotRespond(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	cmd := startRollbackRuntimeChild(t)
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("primary failure")
	terminationFailure := errors.New("termination did not respond")
	killFailure := errors.New("kill had no effect")
	runtime := rollbackRestartRuntime{
		cleanupLimits: rollbackRestartCleanupLimits{total: 80 * time.Millisecond, terminate: 20 * time.Millisecond, firstWait: 20 * time.Millisecond},
		capture:       func(context.Context, int) (pidregistry.StableProcessIdentity, error) { return stable, nil },
		release:       func(*os.Process) error { return nil },
		requestTerminate: func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			return terminationFailure
		},
		kill:            func(*os.Process) error { return killFailure },
		cleanupEndpoint: func(string, pidregistry.CooperativeEndpointIdentity) error { return nil },
		waitChild: func(ctx context.Context, _ int) error {
			<-ctx.Done()
			return context.Cause(ctx)
		},
	}
	contract := runtimeenv.RecoveryLaunch{
		TerminationEndpoint: filepath.Join(t.TempDir(), "unused.sock"),
		TerminationToken:    testLowerHex("cleanup-token"), ContractPresent: true,
	}
	startedAt := time.Now()
	cleanupErr := cleanupStartedRollbackProcess(
		runtime, cmd, cmd.Process.Pid, stable, fixtureCooperativeEndpointIdentity(), contract, primary,
	)
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded cleanup elapsed %s", elapsed)
	}
	for _, want := range []error{primary, terminationFailure, killFailure, errRollbackRestartCleanupTimeout} {
		if !errors.Is(cleanupErr, want) {
			t.Fatalf("cleanup error %v does not retain %v", cleanupErr, want)
		}
	}
	current, err := pidregistry.CaptureStableProcessIdentity(stable.PID)
	if err != nil {
		t.Fatalf("sentinel process disappeared unexpectedly: %v", err)
	}
	if current.ProcessStartToken != stable.ProcessStartToken || current.ExecutableIdentity != stable.ExecutableIdentity {
		t.Fatal("cleanup targeted a reused or different PID")
	}
	boundedKillAndReapRollbackChild(t, cmd)
}

func TestRollbackRestartDeadACKReplacementWriteFailureCleansNewHelper(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rollbackHelperLifecycle{t: t}
	first, err := store.ConvergeRollbackRestart(t.Context(), identity, fixture.resolve, fixture.launch)
	if err != nil {
		t.Fatal(err)
	}
	fixture.live = false
	journalDir := filepath.Dir(store.journalPath(identity.TransactionID))
	replacement := first.RollbackRestart.ACK.Process
	replacement.StartToken = "replacement-write-failure"
	replacementLive := false
	cleanups := 0
	control := func() RollbackRestartControl {
		return RollbackRestartControl{
			Process: replacement,
			Cleanup: func() error {
				cleanups++
				replacementLive = false
				return os.Chmod(journalDir, 0o700)
			},
			Prepare:  func(context.Context) error { return os.Chmod(journalDir, 0o500) },
			Activate: func(context.Context) error { t.Fatal("replacement activated without durable ACK"); return nil },
		}
	}
	t.Cleanup(func() { _ = os.Chmod(journalDir, 0o700) })
	_, convergeErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return control(), replacementLive, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			replacementLive = true
			return control(), nil
		},
	)
	got := loadTransaction(t, store, identity)
	if convergeErr == nil || cleanups != 1 || replacementLive || got.RollbackRestart.ACK.Process != first.RollbackRestart.ACK.Process {
		t.Fatalf("replacement write failure error=%v cleanups=%d live=%t ACK=%+v", convergeErr, cleanups, replacementLive, got.RollbackRestart.ACK)
	}
}

func TestRollbackRestartACKWriteFailureCleansParkedHelper(t *testing.T) {
	t.Parallel()
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	journalDir := filepath.Dir(store.journalPath(identity.TransactionID))
	permissionRestored := false
	cleanups := 0
	activations := 0
	process := fixtureRollbackRestartProcess()
	live := true
	control := func() RollbackRestartControl {
		return RollbackRestartControl{
			Process: process,
			Cleanup: func() error {
				cleanups++
				live = false
				permissionRestored = true
				return os.Chmod(journalDir, 0o700)
			},
			Prepare: func(context.Context) error { return os.Chmod(journalDir, 0o500) },
			Activate: func(context.Context) error {
				activations++
				return nil
			},
		}
	}
	t.Cleanup(func() { _ = os.Chmod(journalDir, 0o700) })
	_, convergeErr := store.ConvergeRollbackRestart(
		t.Context(), transaction.Identity,
		func(context.Context, string) (RollbackRestartControl, bool, error) {
			return control(), live, nil
		},
		func(context.Context, string) (RollbackRestartControl, error) {
			t.Fatal("launcher called despite live parked helper")
			return RollbackRestartControl{}, nil
		},
	)
	if convergeErr == nil || cleanups != 1 || activations != 0 || !permissionRestored || live {
		t.Fatalf("ACK write failure error=%v cleanups=%d activations=%d restored=%t live=%t", convergeErr, cleanups, activations, permissionRestored, live)
	}
	if got := loadTransaction(t, store, identity); got.RollbackRestart.ACKPresent {
		t.Fatalf("ACK write failure persisted dead ACK: %+v", got.RollbackRestart.ACK)
	}
}
