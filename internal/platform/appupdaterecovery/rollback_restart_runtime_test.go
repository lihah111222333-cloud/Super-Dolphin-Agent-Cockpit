package appupdaterecovery

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	"go.uber.org/goleak"
)

func TestRollbackRestartLauncherReapsEveryPostStartFailure(t *testing.T) {
	for _, failure := range []string{"capture", "validation"} {
		t.Run(failure, func(t *testing.T) { runRollbackRestartLauncherFailure(t, failure) })
	}
}

func TestRollbackRestartReadinessRetriesRefusedPublishedEndpoint(t *testing.T) {
	endpoint := newRefusedRollbackRestartEndpoint(t)
	token := "rollback-restart-readiness"
	exact := currentRollbackRestartExactProcess(t, endpoint, token)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	result := make(chan rollbackEndpointWaitResult, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		identity, err := waitRollbackRestartEndpoint(ctx, exact)
		result <- rollbackEndpointWaitResult{endpoint: identity, err: err}
	})
	t.Cleanup(func() {
		cancel()
		workers.Wait()
	})
	select {
	case early := <-result:
		t.Fatalf("wait returned before listener became ready: %v", early.err)
	case <-time.After(3 * rollbackRestartEndpointPoll):
	}
	startReadyRollbackRestartEndpoint(t, endpoint, token)
	assertRollbackRestartEndpointReady(t, ctx, result, endpoint)
}

func TestRollbackRestartReadinessRetriesEndpointIdentityTransition(t *testing.T) {
	first := pidregistry.CooperativeEndpointIdentity{Device: 1, Inode: 1, UID: 1, Mode: 0o600}
	second := pidregistry.CooperativeEndpointIdentity{Device: 1, Inode: 2, UID: 1, Mode: 0o600}
	captures := 0
	probes := 0
	ctx, cancel := context.WithTimeout(t.Context(), 5*rollbackRestartEndpointPoll)
	defer cancel()

	identity, err := waitRollbackRestartEndpointWithOperations(
		ctx,
		pidregistry.StableProcessIdentity{TerminationEndpoint: "/tmp/replaced.sock"},
		func(string) (pidregistry.CooperativeEndpointIdentity, error) {
			captures++
			if captures == 1 {
				return first, nil
			}
			return second, nil
		},
		func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			probes++
			if probes == 1 {
				return pidregistry.ErrCooperativeEndpointIdentityMismatch
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("wait for transitioned rollback endpoint: %v", err)
	}
	if identity != second {
		t.Fatalf("ready endpoint identity = %+v, want %+v", identity, second)
	}
	if captures != 2 || probes != 2 {
		t.Fatalf("capture/probe attempts = %d/%d, want 2/2", captures, probes)
	}
}

func TestRollbackRestartReadinessIdentityTransitionHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*rollbackRestartEndpointPoll)
	defer cancel()
	_, err := waitRollbackRestartEndpointWithOperations(
		ctx,
		pidregistry.StableProcessIdentity{TerminationEndpoint: "/tmp/replaced.sock"},
		func(string) (pidregistry.CooperativeEndpointIdentity, error) {
			return pidregistry.CooperativeEndpointIdentity{Device: 1, Inode: 1, UID: 1, Mode: 0o600}, nil
		},
		func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			return pidregistry.ErrCooperativeEndpointIdentityMismatch
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}
}

type rollbackEndpointWaitResult struct {
	endpoint pidregistry.CooperativeEndpointIdentity
	err      error
}

func startReadyRollbackRestartEndpoint(t *testing.T, endpoint, token string) {
	t.Helper()
	if err := os.Remove(endpoint); err != nil {
		t.Fatalf("remove refused rollback endpoint: %v", err)
	}
	server, err := pidregistry.StartCooperativeTerminationServer(endpoint, token, func() {})
	if err != nil {
		t.Fatalf("start ready rollback endpoint: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close ready rollback endpoint: %v", err)
		}
	})
}

func assertRollbackRestartEndpointReady(
	t *testing.T,
	ctx context.Context,
	result <-chan rollbackEndpointWaitResult,
	endpoint string,
) {
	t.Helper()
	select {
	case ready := <-result:
		if ready.err != nil {
			t.Fatalf("wait for ready rollback endpoint: %v", ready.err)
		}
		current, err := pidregistry.CaptureCooperativeEndpointIdentity(endpoint)
		if err != nil {
			t.Fatalf("capture ready rollback endpoint: %v", err)
		}
		if ready.endpoint != current {
			t.Fatalf("ready endpoint identity = %+v, want %+v", ready.endpoint, current)
		}
	case <-ctx.Done():
		t.Fatalf("wait for ready rollback endpoint: %v", context.Cause(ctx))
	}
}

func TestRollbackRestartReadinessRefusedEndpointHonorsDeadline(t *testing.T) {
	endpoint := newRefusedRollbackRestartEndpoint(t)
	exact := currentRollbackRestartExactProcess(t, endpoint, "rollback-restart-deadline")
	ctx, cancel := context.WithTimeout(t.Context(), 3*rollbackRestartEndpointPoll)
	defer cancel()
	started := time.Now()
	_, err := waitRollbackRestartEndpoint(ctx, exact)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deadline wait elapsed = %v, want <= 1s", elapsed)
	}
}

func newRefusedRollbackRestartEndpoint(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("cooperative rollback endpoint requires Darwin")
	}
	file, err := os.CreateTemp("/tmp", "sd-rollback-ready-")
	if err != nil {
		t.Fatalf("create refused rollback endpoint path: %v", err)
	}
	endpoint := file.Name() + ".sock"
	if err := file.Close(); err != nil {
		t.Fatalf("close refused rollback endpoint path: %v", err)
	}
	if err := os.Remove(file.Name()); err != nil {
		t.Fatalf("remove refused rollback endpoint path: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove refused rollback endpoint: %v", err)
		}
	})
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		t.Fatalf("listen refused rollback endpoint: %v", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		t.Fatalf("chmod refused rollback endpoint: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close refused rollback listener: %v", err)
	}
	return endpoint
}

func currentRollbackRestartExactProcess(
	t *testing.T,
	endpoint string,
	token string,
) pidregistry.StableProcessIdentity {
	t.Helper()
	exact, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("capture current process identity: %v", err)
	}
	exact.TerminationEndpoint = endpoint
	exact.TerminationToken = token
	return exact
}

func runRollbackRestartLauncherFailure(t *testing.T, failure string) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	transaction := newRollbackRuntimeTransaction(t)
	fixture := &rollbackRuntimeFailureFixture{
		failure: failure, primary: errors.New(failure + " failed"),
		terminationFailure: errors.New("termination evidence"), waitFailure: errors.New("wait evidence"),
	}
	runtime := fixture.runtime()
	_, launch := rollbackRestartCallbacksWithRuntime(transaction, runtime)
	_, launchErr := launch(t.Context(), transaction.RollbackRestart.LaunchToken)
	wants := []error{fixture.primary, fixture.waitFailure}
	if failure != "capture" {
		wants = append(wants, fixture.terminationFailure)
	}
	for _, want := range wants {
		if !errors.Is(launchErr, want) {
			t.Fatalf("launch error %v does not retain %v", launchErr, want)
		}
	}
	if fixture.started == nil || !fixture.reaped.Load() {
		t.Fatal("started rollback process was not bounded-waited and reaped")
	}
	if _, err := pidregistry.CaptureStableProcessIdentity(fixture.startedPID); !errors.Is(err, pidregistry.ErrStableProcessNotFound) {
		t.Fatalf("started rollback process remains observable: %v", err)
	}
}

type rollbackRuntimeFailureFixture struct {
	failure                     string
	primary, terminationFailure error
	waitFailure                 error
	started                     *exec.Cmd
	startedPID                  int
	captures                    atomic.Int32
	reaped                      atomic.Bool
}

func (fixture *rollbackRuntimeFailureFixture) runtime() rollbackRestartRuntime {
	return rollbackRestartRuntime{
		cleanupLimits: rollbackRestartCleanupLimits{total: 500 * time.Millisecond, terminate: 100 * time.Millisecond, firstWait: 100 * time.Millisecond},
		start:         fixture.start,
		waitReady: func(context.Context, pidregistry.StableProcessIdentity) (pidregistry.CooperativeEndpointIdentity, error) {
			return fixtureCooperativeEndpointIdentity(), nil
		},
		capture: fixture.capture, validate: fixture.validate, release: fixture.release,
		prepare: func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			return nil
		},
		activate: func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			return nil
		},
		requestTerminate: fixture.requestTerminate,
		terminate: func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			return nil
		},
		kill: fixture.kill, waitChild: fixture.waitChild,
		cleanupEndpoint: func(string, pidregistry.CooperativeEndpointIdentity) error { return nil },
	}
}

func (fixture *rollbackRuntimeFailureFixture) start(context.Context, string, string, []string) (*exec.Cmd, error) {
	fixture.started = exec.Command(os.Args[0], "-test.run=TestRollbackRestartRuntimeChild")
	fixture.started.Env = append(os.Environ(), "SUPER_DOLPHIN_TEST_ROLLBACK_RUNTIME_CHILD=1")
	if err := fixture.started.Start(); err != nil {
		return nil, err
	}
	fixture.startedPID = fixture.started.Process.Pid
	return fixture.started, nil
}

func (fixture *rollbackRuntimeFailureFixture) capture(ctx context.Context, pid int) (pidregistry.StableProcessIdentity, error) {
	if fixture.failure == "capture" && fixture.captures.Add(1) == 1 {
		return pidregistry.StableProcessIdentity{}, fixture.primary
	}
	if err := context.Cause(ctx); err != nil {
		return pidregistry.StableProcessIdentity{}, err
	}
	return pidregistry.CaptureStableProcessIdentity(pid)
}

func (fixture *rollbackRuntimeFailureFixture) validate(
	ctx context.Context,
	stable pidregistry.StableProcessIdentity,
	expectedExecutable string,
) (RollbackRestartProcess, error) {
	if fixture.failure == "validation" {
		return RollbackRestartProcess{}, fixture.primary
	}
	if err := context.Cause(ctx); err != nil {
		return RollbackRestartProcess{}, err
	}
	digest, err := ComputeReleaseDigest(expectedExecutable)
	if err != nil {
		return RollbackRestartProcess{}, err
	}
	return RollbackRestartProcess{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: expectedExecutable, ExecutableSHA256: digest,
	}, nil
}

func (fixture *rollbackRuntimeFailureFixture) release(process *os.Process) error {
	if fixture.failure == "release" {
		return errors.Join(fixture.primary, process.Release())
	}
	return process.Release()
}

func (fixture *rollbackRuntimeFailureFixture) requestTerminate(
	_ context.Context,
	identity pidregistry.StableProcessIdentity,
	_ pidregistry.CooperativeEndpointIdentity,
) error {
	process, err := os.FindProcess(identity.PID)
	if err != nil {
		return errors.Join(err, fixture.terminationFailure)
	}
	return errors.Join(process.Signal(os.Kill), fixture.terminationFailure)
}

func (fixture *rollbackRuntimeFailureFixture) kill(process *os.Process) error {
	return process.Kill()
}

func (fixture *rollbackRuntimeFailureFixture) waitChild(ctx context.Context, childPID int) error {
	err := waitRollbackRestartChild(ctx, childPID)
	if err == nil {
		fixture.reaped.Store(true)
	}
	return errors.Join(err, fixture.waitFailure)
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

func TestRollbackRestartResolverCleanupUsesFrozenExactContract(t *testing.T) {
	transaction := newRollbackRuntimeTransaction(t)
	executable := filepath.Join(transaction.Paths.Target, "Contents", "MacOS", "agent-terminal")
	stable := pidregistry.StableProcessIdentity{
		PID: 4321, ProcessStartToken: "resolver-start", ExecutableIdentity: executable,
	}
	var terminated pidregistry.StableProcessIdentity
	runtime := rollbackRestartRuntime{
		cleanupLimits: rollbackRestartCleanupLimits{total: 500 * time.Millisecond, terminate: 100 * time.Millisecond, firstWait: 100 * time.Millisecond},
		find: func(context.Context, string, string) (pidregistry.StableProcessIdentity, bool, error) {
			return stable, true, nil
		},
		validate: func(context.Context, pidregistry.StableProcessIdentity, string) (RollbackRestartProcess, error) {
			return fixtureRollbackRestartProcess(), nil
		},
		waitReady: func(context.Context, pidregistry.StableProcessIdentity) (pidregistry.CooperativeEndpointIdentity, error) {
			return fixtureCooperativeEndpointIdentity(), nil
		},
		prepare: func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			return nil
		},
		activate: func(context.Context, pidregistry.StableProcessIdentity, pidregistry.CooperativeEndpointIdentity) error {
			return nil
		},
		terminate: func(_ context.Context, identity pidregistry.StableProcessIdentity, _ pidregistry.CooperativeEndpointIdentity) error {
			terminated = identity
			return nil
		},
		cleanupEndpoint: func(string, pidregistry.CooperativeEndpointIdentity) error { return nil },
	}
	resolve, _ := rollbackRestartCallbacksWithRuntime(transaction, runtime)
	control, found, err := resolve(t.Context(), transaction.RollbackRestart.LaunchToken)
	if err != nil || !found || control.Cleanup == nil {
		t.Fatalf("resolve found=%t cleanup=%t error=%v", found, control.Cleanup != nil, err)
	}
	if err := control.Cleanup(); err != nil {
		t.Fatal(err)
	}
	contract, err := rollbackRestartRecoveryLaunch(t.Context(), transaction, transaction.RollbackRestart.LaunchToken, executable)
	if err != nil {
		t.Fatal(err)
	}
	expected := stable
	expected.TerminationEndpoint = contract.TerminationEndpoint
	expected.TerminationToken = contract.TerminationToken
	if terminated != expected {
		t.Fatalf("resolver cleanup identity = %+v, want frozen contract %+v", terminated, contract)
	}
}

func fixtureCooperativeEndpointIdentity() pidregistry.CooperativeEndpointIdentity {
	return pidregistry.CooperativeEndpointIdentity{
		Device: 1, Inode: 2, UID: uint32(os.Geteuid()), Mode: 0o140600,
		CreationTimeSec: 1, CreationTimeNsec: 2,
	}
}

func startRollbackRuntimeChild(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestRollbackRestartRuntimeChild")
	cmd.Env = append(os.Environ(), "SUPER_DOLPHIN_TEST_ROLLBACK_RUNTIME_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		childPID := cmd.Process.Pid
		_ = cmd.Process.Kill()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = waitRollbackRestartChild(ctx, childPID)
	})
	return cmd
}

func boundedKillAndReapRollbackChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	childPID := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitRollbackRestartChild(ctx, childPID); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackRestartRuntimeChild(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_TEST_ROLLBACK_RUNTIME_CHILD") != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}

func newRollbackRuntimeTransaction(t *testing.T) Transaction {
	t.Helper()
	store, id, paths := newRollbackRuntimeStore(t)
	writeRollbackRuntimeBundle(t, paths.Target, "old")
	writeRollbackRuntimeBundle(t, paths.Staging, "candidate")
	oldDigest, err := ComputeReleaseDigest(paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	candidateDigest, err := ComputeReleaseDigest(paths.Staging)
	if err != nil {
		t.Fatal(err)
	}
	identity := Identity{
		TransactionID: id, AttemptID: "rollback-runtime",
		OldRelease:       ReleaseIdentity{SHA256: oldDigest, SignerIdentity: "TEAM-OLD"},
		CandidateRelease: ReleaseIdentity{SHA256: candidateDigest, SignerIdentity: "TEAM-NEW"},
		OldHelpers:       fixtureHelperIdentity("old"), CandidateHelpers: fixtureHelperIdentity("candidate"),
		UpdaterProcess: fixtureUpdaterProcess(),
	}
	if _, err := store.Create(t.Context(), CreateRequest{Identity: identity, Paths: paths, Trust: TrustGeneration{
		PreviousGeneration: "trust-1", Generation: "trust-2", PackageSigner: "TEAM-NEW", State: TrustPending,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetainBackup(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InstallCandidate(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	transaction.Paths.Target, err = CanonicalExistingPath(transaction.Paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	return transaction
}

func newRollbackRuntimeStore(t *testing.T) (*Store, TransactionID, Paths) {
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
	return store, id, paths
}

func writeRollbackRuntimeBundle(t *testing.T, root, content string) {
	t.Helper()
	executable := filepath.Join(root, "Contents", "MacOS", "agent-terminal")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func testLowerHex(seed string) string {
	return digestText(seed)
}
