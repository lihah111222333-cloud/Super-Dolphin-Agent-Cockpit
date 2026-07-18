package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

type probationCallerContextKey struct{}

func TestCandidateHandleReapsCrashedProcess(t *testing.T) {
	handle, identity, err := startCandidateHandleTestProcess("exit")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(identity.TerminationEndpoint) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = handle.Wait(ctx)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("candidate Wait() error = %v, want ExitError", err)
	}
	alive, probeErr := handle.ProcessAlive(identity)
	if alive || !errors.As(probeErr, &exitErr) {
		t.Fatalf("ProcessAlive() = %v, %v, want false with Wait error", alive, probeErr)
	}
	assertCandidateGone(t, identity)
}

func TestCandidateHandleTerminatesAndReapsExactProcess(t *testing.T) {
	handle, identity, err := startCandidateHandleTestProcess("serve")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(identity.TerminationEndpoint) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handle.Stop(ctx, identity); err != nil {
		t.Fatalf("candidate Stop() error = %v", err)
	}
	assertCandidateGone(t, identity)
}

func TestCandidateReadinessWaitsForAuthenticatedDelayedListener(t *testing.T) {
	handle, identity, err := startCandidateHandleTestProcess("delayed_serve")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = handle.cleanupStartFailure(errors.New("test cleanup"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	if err := waitCandidateTerminationReady(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 75*time.Millisecond {
		t.Fatalf("candidate readiness published before delayed listener: %s", elapsed)
	}
	if err := handle.Stop(ctx, identity); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateStartFailureUsesSharedForceReclaim(t *testing.T) {
	handle, identity, err := startCandidateHandleTestProcess("ignore_termination")
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("candidate READY failed")
	injected := errors.New("identity read failed")
	handle.captureStable = func(int) (pidregistry.StableProcessIdentity, error) {
		return pidregistry.StableProcessIdentity{}, injected
	}
	returned, gotErr := failCandidateStart(handle, primary)
	if returned != nil || !errors.Is(gotErr, primary) {
		t.Fatalf("failCandidateStart() = handle %v error %v, want nil handle and primary", returned != nil, gotErr)
	}
	assertCandidateReaped(t, updaterRollbackResult{candidate: handle, identity: identity})
}

func TestCandidateStartFailureRetainsHandleWhenReclaimUnconfirmed(t *testing.T) {
	handle, identity, err := startCandidateHandleTestProcess("serve")
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("candidate identity capture failed")
	injected := errors.New("endpoint cleanup failed")
	handle.cleanupEndpoint = func(string) error { return injected }
	handle.directChild = false
	returned, gotErr := failCandidateStart(handle, primary)
	if returned != handle || !errors.Is(gotErr, primary) || !errors.Is(gotErr, injected) {
		t.Fatalf("failCandidateStart() = handle %v error %v, want retained handle and joined errors", returned == handle, gotErr)
	}
	handle.directChild = true
	handle.cleanupEndpoint = pidregistry.CleanupCooperativeTerminationEndpoint
	if err := handle.Reclaim(context.Background(), identity); err != nil {
		t.Fatalf("cleanup retained candidate: %v", err)
	}
	assertCandidateReaped(t, updaterRollbackResult{candidate: handle, identity: identity})
}

func TestCandidateReclaimUsesIndependentDeadlineAfterCallerCancel(t *testing.T) {
	handle, identity, err := startCandidateHandleTestProcess("serve")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handle.Reclaim(ctx, identity); err != nil {
		t.Fatalf("Reclaim() error = %v", err)
	}
	assertCandidateReaped(t, updaterRollbackResult{candidate: handle, identity: identity})
}

func TestProbationImmediatePostStartFailuresReapCandidate(t *testing.T) {
	for _, failure := range []string{"owner", "lease", "guard", "supervisor", "supervisor_run"} {
		t.Run(failure, func(t *testing.T) { runProbationImmediatePostStartFailure(t, failure) })
	}
}

func runProbationImmediatePostStartFailure(t *testing.T, failure string) {
	h := newRollbackRaceHarness(t, "probation-"+failure)
	h.app.rollbackRestartCallbackFactory = func(recovery.Transaction) (recovery.RollbackRestartResolver, recovery.RollbackRestartLauncher) {
		return h.resolve, h.launch
	}
	var result updaterRollbackResult
	mode := "serve"
	if failure == "supervisor" || failure == "supervisor_run" {
		mode = "ignore_termination"
	}
	h.app.startProbationCandidate = func(context.Context, recovery.Transaction) (*candidateHandle, error) {
		handle, identity, err := startCandidateHandleTestProcess(mode)
		result.candidate, result.identity = handle, identity
		if err == nil {
			injectCandidateRollbackFailure(t, failure, handle, identity)
		}
		return handle, err
	}
	launch := h.launch
	h.launch = func(ctx context.Context, token string) (recovery.RollbackRestartControl, error) {
		assertCandidateReclaimedBeforeRestart(t, result)
		return launch(ctx, token)
	}
	cause := errors.New("force probation " + failure + " failure")
	callerCtx := context.WithValue(t.Context(), probationCallerContextKey{}, failure)
	configureProbationImmediateFailure(t, &h.app, &h.transaction, failure, cause, callerCtx)
	result.err = h.app.runProbationSupervisor(callerCtx, h.transaction)
	registerCandidateCleanup(t, result)
	if !errors.Is(result.err, cause) {
		t.Fatalf("probation %s error = %v, want cause", failure, result.err)
	}
	assertCandidateReaped(t, result)
	h.tokenMu.Lock()
	token := h.startedToken
	h.tokenMu.Unlock()
	assertUpdaterRollbackRestart(t, h, token)
}

func assertCandidateReclaimedBeforeRestart(t *testing.T, result updaterRollbackResult) {
	t.Helper()
	if result.candidate == nil {
		t.Fatal("rollback restart began without candidate handle evidence")
	}
	select {
	case <-result.candidate.done:
	default:
		t.Fatal("rollback restart began before candidate reap")
	}
	assertCandidateGone(t, result.identity)
	if _, err := os.Stat(result.identity.TerminationEndpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback restart began before endpoint cleanup: %v", err)
	}
}

func injectCandidateRollbackFailure(t *testing.T, failure string, handle *candidateHandle, identity recovery.ProcessIdentity) {
	t.Helper()
	injected := errors.New("injected candidate cleanup failure")
	switch failure {
	case "owner":
		if err := os.Remove(identity.TerminationEndpoint); err != nil {
			t.Fatalf("remove candidate endpoint: %v", err)
		}
	case "lease":
		handle.terminate = func(context.Context, recovery.ProcessIdentity) error { return injected }
	case "guard":
		handle.terminate = func(context.Context, recovery.ProcessIdentity) error { return injected }
		handle.captureStable = func(int) (pidregistry.StableProcessIdentity, error) {
			return pidregistry.StableProcessIdentity{}, injected
		}
	case "supervisor", "supervisor_run":
	default:
		t.Fatalf("unknown candidate cleanup injection %q", failure)
	}
}

func configureProbationImmediateFailure(
	t *testing.T,
	app *updaterApp,
	transaction *recovery.Transaction,
	failure string,
	cause error,
	callerCtx context.Context,
) {
	t.Helper()
	switch failure {
	case "owner":
		app.newProbationOwnerID = func() (string, error) { return "", cause }
	case "lease":
		app.acquireProbationLease = func(*recovery.Store, context.Context, recovery.Identity, recovery.ProbationLeaseRequest) (recovery.ProbationLease, error) {
			return recovery.ProbationLease{}, cause
		}
	case "guard":
		transaction.Paths.RecoveryDir = filepath.Join(t.TempDir(), "missing-recovery")
		app.startProbationGuard = func(ctx context.Context, _ recovery.Transaction, _ bool, _ func() error) error {
			if ctx != callerCtx || ctx.Value(probationCallerContextKey{}) != failure {
				t.Errorf("probation Guard context did not preserve caller context")
			}
			return cause
		}
	case "supervisor":
		transaction.Paths.RecoveryDir = t.TempDir()
		app.newProbationSupervisor = func(recovery.ProbationSupervisorConfig) (*recovery.ProbationSupervisor, error) {
			return nil, cause
		}
	case "supervisor_run":
		transaction.Paths.RecoveryDir = t.TempDir()
		app.newProbationSupervisor = func(config recovery.ProbationSupervisorConfig) (*recovery.ProbationSupervisor, error) {
			config.ProcessAlive = func(recovery.ProcessIdentity) (bool, error) { return false, cause }
			return recovery.NewProbationSupervisor(config)
		}
	default:
		t.Fatalf("unknown probation failure %q", failure)
	}
}

func TestUpdaterRollbackEntriesConvergeWithDetachedGuardOnce(t *testing.T) {
	for _, entry := range []string{"pre_factory_failure", "launch_failure", "started_candidate", "claimed_candidate"} {
		t.Run(entry, func(t *testing.T) { runUpdaterRollbackRace(t, entry) })
	}
}

func TestUpdaterRejectsMissingRollbackRestartDeadline(t *testing.T) {
	app := defaultUpdaterApp()
	app.rollbackRestartDeadline = 0
	if err := app.validate(); err == nil {
		t.Fatal("updater accepted a missing rollback restart deadline")
	}
}

func TestCandidateTestEnvReplacesInheritedRaceExitDelay(t *testing.T) {
	env := []string{"HOME=/tmp", "GORACE=atexit_sleep_ms=10000"}
	got := upsertCandidateTestEnv(env, "GORACE", "atexit_sleep_ms=0")
	if len(got) != len(env) {
		t.Fatalf("environment length = %d, want %d", len(got), len(env))
	}
	if got[1] != "GORACE=atexit_sleep_ms=0" {
		t.Fatalf("race environment = %q", got[1])
	}
}

type rollbackRaceHarness struct {
	store             *recovery.Store
	transaction       recovery.Transaction
	cause             error
	process           recovery.RollbackRestartProcess
	app               updaterApp
	resolve           recovery.RollbackRestartResolver
	launch            recovery.RollbackRestartLauncher
	updaterReady      chan struct{}
	guardReady        chan struct{}
	compete           chan struct{}
	launches          atomic.Int32
	untokenizedStarts atomic.Int32
	tokenMu           sync.Mutex
	startedToken      string
}

func newRollbackRaceHarness(t *testing.T, entry string) *rollbackRaceHarness {
	store, transaction := newUpdaterRollbackFixture(t)
	h := &rollbackRaceHarness{
		store: store, transaction: transaction, cause: errors.New("force " + entry),
		process: recovery.RollbackRestartProcess{
			PID: 404, StartToken: "unique-updater-old-start", ExecutableIdentity: "/test/updater-old-release",
			ExecutableSHA256: strings.Repeat("b", 64),
		},
		updaterReady: make(chan struct{}), guardReady: make(chan struct{}), compete: make(chan struct{}),
	}
	h.resolve = func(context.Context, string) (recovery.RollbackRestartControl, bool, error) {
		if h.launches.Load() == 0 {
			return recovery.RollbackRestartControl{}, false, nil
		}
		return updaterRollbackControl(h.process), true, nil
	}
	h.launch = func(_ context.Context, token string) (recovery.RollbackRestartControl, error) {
		h.launches.Add(1)
		h.tokenMu.Lock()
		h.startedToken = token
		h.tokenMu.Unlock()
		return updaterRollbackControl(h.process), nil
	}
	h.app = defaultUpdaterApp()
	if entry == "pre_factory_failure" {
		h.app.rollbackRestartCallbackFactory = nil
		return h
	}
	h.app.rollbackRestartCallbackFactory = func(recovery.Transaction) (recovery.RollbackRestartResolver, recovery.RollbackRestartLauncher) {
		close(h.updaterReady)
		<-h.compete
		return h.resolve, h.launch
	}
	h.app.runRestartCommand = func(...string) (commandResult, error) {
		h.untokenizedStarts.Add(1)
		return commandResult{}, nil
	}
	return h
}

func updaterRollbackControl(process recovery.RollbackRestartProcess) recovery.RollbackRestartControl {
	return recovery.RollbackRestartControl{
		Process:  process,
		Cleanup:  func() error { return nil },
		Prepare:  func(context.Context) error { return nil },
		Activate: func(context.Context) error { return nil },
	}
}

func runUpdaterRollbackRace(t *testing.T, entry string) {
	h := newRollbackRaceHarness(t, entry)
	updaterDone := make(chan updaterRollbackResult, 1)
	guardDone := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		handle, identity, err := invokeUpdaterRollbackEntry(entry, h.app, h.store, h.transaction, h.cause)
		updaterDone <- updaterRollbackResult{err: err, candidate: handle, identity: identity}
	})
	coordinationCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	workers.Go(func() { guardDone <- competeDetachedGuard(coordinationCtx, h) })
	var result updaterRollbackResult
	select {
	case <-h.guardReady:
		close(h.compete)
		result = <-updaterDone
	case result = <-updaterDone:
		cancel()
	case <-coordinationCtx.Done():
		t.Fatalf("rollback race coordination: %v", context.Cause(coordinationCtx))
	}
	registerCandidateCleanup(t, result)
	if !errors.Is(result.err, h.cause) {
		t.Fatalf("rollback error = %v, want cause", result.err)
	}
	if err := <-guardDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	workers.Wait()
	assertCandidateReaped(t, result)
	if entry == "pre_factory_failure" {
		return
	}
	h.tokenMu.Lock()
	token := h.startedToken
	h.tokenMu.Unlock()
	assertUpdaterRollbackRestart(t, h, token)
}

func competeDetachedGuard(ctx context.Context, h *rollbackRaceHarness) error {
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-h.updaterReady:
	}
	close(h.guardReady)
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-h.compete:
	}
	_, err := h.store.ConvergeRollbackRestart(ctx, h.transaction.Identity, h.resolve, h.launch)
	return err
}

type updaterRollbackResult struct {
	err       error
	candidate *candidateHandle
	identity  recovery.ProcessIdentity
}

func registerCandidateCleanup(t *testing.T, result updaterRollbackResult) {
	t.Helper()
	if result.candidate == nil {
		return
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := result.candidate.Stop(ctx, result.identity); err != nil {
			t.Errorf("fallback candidate cleanup: %v", err)
		}
		_ = os.Remove(result.identity.TerminationEndpoint)
	})
}

func assertCandidateReaped(t *testing.T, result updaterRollbackResult) {
	t.Helper()
	if result.candidate == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := result.candidate.Wait(ctx); err != nil && !isCandidateExitError(err) {
		t.Fatalf("candidate reap error = %v", err)
	}
	if got := result.candidate.waitCalls.Load(); got != 1 {
		t.Fatalf("candidate cmd.Wait calls = %d, want 1", got)
	}
	assertCandidateGone(t, result.identity)
	if _, err := os.Stat(result.identity.TerminationEndpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate termination endpoint remains: %v", err)
	}
}

func isCandidateExitError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func assertCandidateGone(t *testing.T, identity recovery.ProcessIdentity) {
	t.Helper()
	if _, err := pidregistry.CaptureStableProcessIdentity(identity.PID); !errors.Is(err, pidregistry.ErrStableProcessNotFound) {
		t.Fatalf("candidate remains observable after exact termination and reap: %v", err)
	}
}

func invokeUpdaterRollbackEntry(entry string, app updaterApp, store *recovery.Store, transaction recovery.Transaction, cause error) (*candidateHandle, recovery.ProcessIdentity, error) {
	if entry == "launch_failure" {
		return nil, recovery.ProcessIdentity{}, app.rollbackLaunchFailure(context.Background(), store, transaction, cause)
	}
	handle, process, err := startCandidateHandleTestProcess("serve")
	if err != nil {
		return nil, recovery.ProcessIdentity{}, err
	}
	if entry == "started_candidate" {
		return handle, process, app.rollbackStartedCandidate(context.Background(), store, transaction, handle, cause)
	}
	lease, err := store.AcquireProbationLease(context.Background(), transaction.Identity, recovery.ProbationLeaseRequest{
		OwnerID: "updater-entry-test", Process: process, TTL: time.Minute,
	})
	if err != nil {
		return handle, process, err
	}
	return handle, process, app.rollbackClaimedCandidate(context.Background(), store, transaction, lease, handle, cause)
}

func newUpdaterRollbackFixture(t *testing.T) (*recovery.Store, recovery.Transaction) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows filesystems do not preserve macOS launcher execute bits in this fixture")
	}
	stubSuccessfulDitto(t)
	stagedParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	targetParent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	staged := createAppBundle(t, filepath.Join(stagedParent, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(targetParent, "Super Dolphin.app"))
	transaction, transactional, err := defaultUpdaterApp().replaceTargetAppTransaction(staged, target, "", true, true)
	if err != nil || !transactional || transaction.State != recovery.StateProbation {
		t.Fatalf("probation fixture = transactional %t state %q error %v", transactional, transaction.State, err)
	}
	store, err := recovery.NewStore(filepath.Join(targetParent, updateTransactionDirName))
	if err != nil {
		t.Fatal(err)
	}
	return store, transaction
}

func assertUpdaterRollbackRestart(t *testing.T, h *rollbackRaceHarness, token string) {
	t.Helper()
	got, err := h.store.Load(context.Background(), h.transaction.Identity)
	if err != nil {
		t.Fatal(err)
	}
	record := got.RollbackRestart
	if h.launches.Load() != 1 || h.untokenizedStarts.Load() != 0 || token == "" || !record.ACKPresent ||
		record.ACK.LaunchToken != token || record.LaunchToken != token || record.ACK.Process != h.process {
		t.Fatalf("launches=%d untokenized=%d token=%q restart=%+v", h.launches.Load(), h.untokenizedStarts.Load(), token, record)
	}
}

func TestProbationCandidateProcess(t *testing.T) {
	mode := os.Getenv("SUPER_DOLPHIN_TEST_PROBATION_CANDIDATE")
	if mode == "" {
		return
	}
	if mode == "exit" {
		time.Sleep(200 * time.Millisecond)
		os.Exit(17)
	}
	if mode == "delayed_serve" {
		time.Sleep(100 * time.Millisecond)
		mode = "serve"
	}
	stop := func() { os.Exit(0) }
	if mode == "ignore_termination" {
		stop = func() {}
	}
	server, err := pidregistry.StartCooperativeTerminationServer(
		os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT"),
		os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_TOKEN"),
		stop,
	)
	if (mode != "serve" && mode != "ignore_termination") || err != nil {
		os.Exit(20)
	}
	defer server.Close()
	for {
		time.Sleep(time.Hour)
	}
}

func startCandidateHandleTestProcess(mode string) (*candidateHandle, recovery.ProcessIdentity, error) {
	endpoint, err := candidateTestEndpoint()
	if err != nil {
		return nil, recovery.ProcessIdentity{}, err
	}
	token := strings.Repeat("c", 64)
	cmd := exec.Command(os.Args[0], "-test.run=TestProbationCandidateProcess")
	// 测试子进程的 os.Exit 表示协议终止，不让 race runtime 追加退出等待。
	cmd.Env = append(upsertCandidateTestEnv(os.Environ(), "GORACE", "atexit_sleep_ms=0"),
		"SUPER_DOLPHIN_TEST_PROBATION_CANDIDATE="+mode,
		"SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT="+endpoint,
		"SUPER_DOLPHIN_TEST_TERMINATION_TOKEN="+token,
	)
	if err := cmd.Start(); err != nil {
		return nil, recovery.ProcessIdentity{}, err
	}
	if err := waitCandidateEndpoint(cmd, mode, endpoint); err != nil {
		return nil, recovery.ProcessIdentity{}, err
	}
	stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, recovery.ProcessIdentity{}, err
	}
	identity := recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: strings.Repeat("a", 64),
		TerminationEndpoint: endpoint, TerminationToken: token,
	}
	return newCandidateHandle(cmd, identity), identity, nil
}

func upsertCandidateTestEnv(env []string, key, value string) []string {
	prefix := key + "="
	entry := prefix + value
	for i, current := range env {
		if strings.HasPrefix(current, prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func candidateTestEndpoint() (string, error) {
	file, err := os.CreateTemp("/tmp", "sd-candidate-")
	if err != nil {
		return "", err
	}
	endpoint := file.Name() + ".sock"
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(file.Name()); err != nil {
		return "", err
	}
	return endpoint, nil
}

func waitCandidateEndpoint(cmd *exec.Cmd, mode, endpoint string) error {
	deadline := time.Now().Add(3 * time.Second)
	for mode == "serve" || mode == "ignore_termination" {
		if _, err := os.Stat(endpoint); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return reapFailedCandidateStart(cmd, err)
		}
		if time.Now().After(deadline) {
			return reapFailedCandidateStart(cmd, errors.New("candidate termination endpoint readiness timeout"))
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func reapFailedCandidateStart(cmd *exec.Cmd, primary error) error {
	return errors.Join(primary, cmd.Process.Kill(), cmd.Wait())
}
