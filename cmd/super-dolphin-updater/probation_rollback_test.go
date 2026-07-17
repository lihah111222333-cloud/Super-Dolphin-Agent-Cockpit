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

func TestUpdaterRollbackEntriesConvergeWithDetachedGuardOnce(t *testing.T) {
	for _, entry := range []string{"launch_failure", "started_candidate", "claimed_candidate"} {
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
	h.resolve = func(string) (recovery.RollbackRestartProcess, bool, error) {
		return recovery.RollbackRestartProcess{}, false, nil
	}
	h.launch = func(token string) (recovery.RollbackRestartProcess, error) {
		h.launches.Add(1)
		h.tokenMu.Lock()
		h.startedToken = token
		h.tokenMu.Unlock()
		return h.process, nil
	}
	h.app = defaultUpdaterApp()
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

func runUpdaterRollbackRace(t *testing.T, entry string) {
	h := newRollbackRaceHarness(t, entry)
	updaterDone := make(chan updaterRollbackResult, 1)
	guardDone := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() {
		handle, identity, err := invokeUpdaterRollbackEntry(entry, h.app, h.store, h.transaction, h.cause)
		updaterDone <- updaterRollbackResult{err: err, candidate: handle, identity: identity}
	})
	workers.Go(func() { guardDone <- competeDetachedGuard(h) })
	<-h.guardReady
	close(h.compete)
	result := <-updaterDone
	registerCandidateCleanup(t, result)
	if !errors.Is(result.err, h.cause) {
		t.Fatalf("rollback error = %v, want cause", result.err)
	}
	if err := <-guardDone; err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	assertCandidateReaped(t, result)
	h.tokenMu.Lock()
	token := h.startedToken
	h.tokenMu.Unlock()
	assertUpdaterRollbackRestart(t, h, token)
}

func competeDetachedGuard(h *rollbackRaceHarness) error {
	<-h.updaterReady
	close(h.guardReady)
	<-h.compete
	_, err := h.store.ConvergeRollbackRestart(context.Background(), h.transaction.Identity, h.resolve, h.launch)
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
	if err := result.candidate.Wait(ctx); err != nil {
		t.Fatalf("candidate reap error = %v", err)
	}
	assertCandidateGone(t, result.identity)
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
	server, err := pidregistry.StartCooperativeTerminationServer(
		os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_ENDPOINT"),
		os.Getenv("SUPER_DOLPHIN_TEST_TERMINATION_TOKEN"),
		func() { os.Exit(0) },
	)
	if mode != "serve" || err != nil {
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
	cmd.Env = append(os.Environ(),
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
	for mode == "serve" {
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
