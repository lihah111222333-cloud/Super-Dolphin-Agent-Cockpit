//go:build unix

package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	blockingFilesystemWorkerEnv      = "REASONIX_TEST_BLOCKING_SCHEMA_FS_WORKER"
	blockingFilesystemWorkerFIFOEnv  = "REASONIX_TEST_BLOCKING_SCHEMA_FS_FIFO"
	blockingFilesystemWorkerStartEnv = "REASONIX_TEST_BLOCKING_SCHEMA_FS_STARTED"
	blockingFilesystemWorkerDoneEnv  = "REASONIX_TEST_BLOCKING_SCHEMA_FS_FINISHED"
)

type blockingFilesystemWorkerFixture struct {
	fifo          string
	started       string
	finished      string
	helperStarted string
}

func runBlockingFilesystemWorkerFixture() bool {
	if os.Getenv(blockingFilesystemWorkerEnv) != "1" {
		return false
	}
	if err := os.WriteFile(os.Getenv(blockingFilesystemWorkerStartEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(21)
	}
	file, err := os.Open(os.Getenv(blockingFilesystemWorkerFIFOEnv))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(22)
	}
	if err := file.Close(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(23)
	}
	if err := os.WriteFile(os.Getenv(blockingFilesystemWorkerDoneEnv), []byte("finished"), 0o600); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(24)
	}
	return true
}

func TestNewClientDeadlineKillsBlockedPackageVerification(t *testing.T) {
	config := newBlockingFilesystemWorkerClientConfig(t)
	fixture := installBlockingFilesystemWorker(t)
	setBlockingFilesystemWorkerEnvironment(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type newClientResult struct {
		client *Client
		err    error
	}
	result := make(chan newClientResult, 1)
	safego.Go(context.Background(), nil, "toolbridge.schema-blocked-verification-test", func(context.Context) {
		client, err := NewClient(ctx, config)
		result <- newClientResult{client: client, err: err}
	})
	waitForHelperMarker(t, fixture.started)
	cancelledAt := time.Now()
	cancel()
	var got newClientResult
	select {
	case got = <-result:
	case <-time.After(filesystemSnapshotCleanupTimeout + reapDeadline + time.Second):
		t.Fatal("NewClient blocked verification worker was not synchronously reaped")
	}
	if got.client != nil || ErrorCode(got.err) != CodeProcessStartFailed || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("NewClient(blocked verification) = (%v, %v), code=%q", got.client, got.err, ErrorCode(got.err))
	}
	assertBlockedFilesystemWorkerResult(t, fixture, cancelledAt)
}

func TestExecuteDeadlineKillsBlockedSnapshotBeforeHelperLaunch(t *testing.T) {
	snapshotRoot := setFilesystemSnapshotRoot(t)
	snapshotsBefore := filesystemSnapshotDirectoryNames(t, snapshotRoot)
	client := newSchemaTestClient(t, os.Args[0])
	fixture := installBlockingFilesystemWorker(t)
	client.workerEnv = blockingFilesystemWorkerEnvironment(fixture)
	client.workerEnv = append(client.workerEnv,
		"REASONIX_SCHEMA_HELPER_FIXTURE=sleep",
		"REASONIX_SCHEMA_HELPER_MARKER="+fixture.helperStarted,
	)
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	_, err = client.Execute(ctx, testInvocation(canonical), allowFence)
	if ErrorCode(err) != CodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Execute(blocked snapshot) error=%v code=%q", err, ErrorCode(err))
	}
	assertBlockedFilesystemWorkerResult(t, fixture, started)
	assertFilesystemSnapshotSetUnchanged(t, snapshotRoot, snapshotsBefore)
}

func TestExecuteReapsBlockedCleanupWorkerAndPreservesCleanupFailure(t *testing.T) {
	previousLimiter := globalHelperLimiter
	globalHelperLimiter = newHelperLimiter(maxLiveHelpers)
	t.Cleanup(func() { globalHelperLimiter = previousLimiter })
	snapshotRoot := setFilesystemSnapshotRoot(t)
	snapshotsBefore := filesystemSnapshotDirectoryNames(t, snapshotRoot)
	client := newSchemaTestClient(t, os.Args[0])
	client.operationTimeout = helperFixtureTimeout
	client.workerEnv = []string{"REASONIX_SCHEMA_MALICIOUS_HELPER=success"}
	fixtures := make([]blockingFilesystemWorkerFixture, maxLiveHelpers)
	for index := range fixtures {
		fixtures[index] = installBlockingFilesystemWorker(t)
	}
	workerStarts := 0
	client.workerCommand = func(path string) *exec.Cmd {
		workerStarts++
		cmd := exec.Command(path)
		if workerStarts%2 == 0 && workerStarts <= 2*maxLiveHelpers {
			cmd.Env = blockingFilesystemWorkerEnvironment(fixtures[workerStarts/2-1])
		}
		return cmd
	}
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	for index, fixture := range fixtures {
		assertBlockedCleanupReaped(t, client, testInvocation(canonical), fixture, index)
	}
	if _, err = client.Execute(context.Background(), testInvocation(canonical), allowFence); err != nil {
		t.Fatalf("Execute(after %d cleanup timeouts) error = %v", maxLiveHelpers, err)
	}
	if workerStarts != 2*(maxLiveHelpers+1) {
		t.Fatalf("filesystem worker starts = %d, want %d", workerStarts, 2*(maxLiveHelpers+1))
	}
	assertFilesystemSnapshotSetUnchanged(t, snapshotRoot, snapshotsBefore)
}

func assertBlockedCleanupReaped(
	t *testing.T,
	client *Client,
	invocation Invocation,
	fixture blockingFilesystemWorkerFixture,
	index int,
) {
	t.Helper()
	result := make(chan error, 1)
	safego.Go(context.Background(), nil, "toolbridge.schema-blocked-cleanup-test", func(context.Context) {
		_, err := client.Execute(context.Background(), invocation, allowFence)
		result <- err
	})
	waitForHelperMarker(t, fixture.started)
	cleanupStarted := time.Now()
	var err error
	select {
	case err = <-result:
	case <-time.After(filesystemSnapshotCleanupTimeout + reapDeadline + time.Second):
		t.Fatalf("blocked cleanup worker %d was not synchronously reaped", index)
	}
	if ErrorCode(err) != CodeTimeout || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Execute(blocked cleanup %d) error=%v code=%q", index, err, ErrorCode(err))
	}
	if errorTreeContainsCode(err, CodeReapFailed) {
		t.Fatalf("Execute(blocked cleanup %d) mislabeled a reaped worker: %v", index, err)
	}
	assertBlockedFilesystemWorkerResult(t, fixture, cleanupStarted)
}

func TestSweepFilesystemSnapshotsRequiresExactIdentityAndStaleOwner(t *testing.T) {
	root := setFilesystemSnapshotRoot(t)
	owner, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	active, err := newFilesystemSnapshotIdentity(runtime.GOOS, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeExecutableSnapshot([]byte("active"), active); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(active) })
	stale := filesystemSnapshotIdentity{
		Version: filesystemSnapshotVersion,
		Token:   "00112233445566778899aabbccddeeff", HelperGOOS: runtime.GOOS,
		OwnerPID: 1 << 30, OwnerStartToken: "stale-start", OwnerExecutable: "stale-executable",
	}
	stale.Directory = filepath.Join(root, filesystemSnapshotPrefix+stale.Token)
	if _, err := writeExecutableSnapshot([]byte("stale"), stale); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, filesystemSnapshotPrefix+"legacy-unowned")
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := sweepStaleFilesystemSnapshots(); err != nil {
		t.Fatal(err)
	}
	assertPathExistence(t, stale.Directory, false)
	assertPathExistence(t, active.Directory, true)
	assertPathExistence(t, legacy, true)
}

func TestExecutableSnapshotPublicationSurvivesConcurrentStartupSweep(t *testing.T) {
	root := setFilesystemSnapshotRoot(t)
	identity := newTestFilesystemSnapshotIdentity(t)
	staged := make(chan string, 1)
	continuePublish := make(chan struct{}, 1)
	t.Cleanup(func() { continuePublish <- struct{}{} })
	result := make(chan error, 1)
	safego.Go(context.Background(), nil, "toolbridge.schema-snapshot-publish-test", func(context.Context) {
		directory, createErr := createFilesystemSnapshotStagingDirectory(identity)
		if createErr != nil {
			result <- createErr
			return
		}
		staged <- directory
		<-continuePublish
		_, publishErr := publishExecutableSnapshot([]byte("snapshot"), identity, directory)
		result <- publishErr
	})
	var directory string
	select {
	case directory = <-staged:
	case createErr := <-result:
		t.Fatalf("create snapshot staging directory: %v", createErr)
	}
	if directory == identity.Directory {
		t.Fatalf("writer exposed final snapshot directory before publication: %s", directory)
	}
	assertPathExistence(t, directory, true)
	assertPathExistence(t, identity.Directory, false)
	if err := sweepStaleFilesystemSnapshots(); err != nil {
		t.Fatal(err)
	}
	assertPathExistence(t, directory, true)
	continuePublish <- struct{}{}
	if err := <-result; err != nil {
		t.Fatalf("publish snapshot after concurrent startup sweep: %v", err)
	}
	t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(identity) })
	if filepath.Dir(directory) != root {
		t.Fatalf("staging parent = %q, want %q", filepath.Dir(directory), root)
	}
	assertPathExistence(t, directory, false)
	assertPathExistence(t, identity.Directory, true)
	assertPublishedFilesystemSnapshot(t, identity, "snapshot")
}

func TestRemoveOwnedFilesystemSnapshotCleansAbandonedStaging(t *testing.T) {
	setFilesystemSnapshotRoot(t)
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			identity := newTestFilesystemSnapshotIdentity(t)
			directory, err := createFilesystemSnapshotStagingDirectory(identity)
			if err != nil {
				t.Fatal(err)
			}
			if complete {
				if err := writeFilesystemSnapshotMarker(directory, identity); err != nil {
					t.Fatal(err)
				}
				if err := writeExclusiveRegularFile(
					filepath.Join(directory, HelperFileName(identity.HelperGOOS)),
					[]byte("abandoned"),
					0o700,
				); err != nil {
					t.Fatal(err)
				}
			}
			if err := removeOwnedFilesystemSnapshot(identity); err != nil {
				t.Fatal(err)
			}
			assertPathExistence(t, directory, false)
			assertPathExistence(t, identity.Directory, false)
		})
	}
}

func TestSweepFilesystemSnapshotsUsesExactOwnerForEveryStagingState(t *testing.T) {
	setFilesystemSnapshotRoot(t)
	type stagingState int
	const (
		stagingEmpty stagingState = iota
		stagingMarkerOnly
		stagingComplete
	)
	tests := []struct {
		name        string
		state       stagingState
		mutateOwner func(*filesystemSnapshotIdentity)
		wantExists  bool
	}{
		{name: "active empty", state: stagingEmpty, wantExists: true},
		{name: "stale empty", state: stagingEmpty, mutateOwner: makeStaleFilesystemSnapshotOwner, wantExists: false},
		{name: "PID reuse empty", state: stagingEmpty, mutateOwner: makeReusedFilesystemSnapshotOwner, wantExists: false},
		{name: "active marker only", state: stagingMarkerOnly, wantExists: true},
		{name: "stale marker only", state: stagingMarkerOnly, mutateOwner: makeStaleFilesystemSnapshotOwner, wantExists: false},
		{name: "PID reuse marker only", state: stagingMarkerOnly, mutateOwner: makeReusedFilesystemSnapshotOwner, wantExists: false},
		{name: "active complete", state: stagingComplete, wantExists: true},
		{name: "stale complete", state: stagingComplete, mutateOwner: makeStaleFilesystemSnapshotOwner, wantExists: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := newTestFilesystemSnapshotIdentity(t)
			if test.mutateOwner != nil {
				test.mutateOwner(&identity)
			}
			directory, err := createFilesystemSnapshotStagingDirectory(identity)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(identity) })
			if test.state >= stagingMarkerOnly {
				if err := writeFilesystemSnapshotMarker(directory, identity); err != nil {
					t.Fatal(err)
				}
			}
			if test.state == stagingComplete {
				if err := writeExclusiveRegularFile(
					filepath.Join(directory, HelperFileName(identity.HelperGOOS)),
					[]byte("staged"),
					0o700,
				); err != nil {
					t.Fatal(err)
				}
			}
			if err := sweepStaleFilesystemSnapshots(); err != nil {
				t.Fatal(err)
			}
			assertPathExistence(t, directory, test.wantExists)
		})
	}
}

func makeStaleFilesystemSnapshotOwner(identity *filesystemSnapshotIdentity) {
	identity.OwnerPID = 1 << 30
	identity.OwnerStartToken = "stale-start"
	identity.OwnerExecutable = "stale-executable"
}

func makeReusedFilesystemSnapshotOwner(identity *filesystemSnapshotIdentity) {
	identity.OwnerStartToken += "-reused"
}

func TestSweepFilesystemSnapshotsRejectsMissingOwnerBinding(t *testing.T) {
	setFilesystemSnapshotRoot(t)
	identity := newTestFilesystemSnapshotIdentity(t)
	directory := identity.Directory + filesystemSnapshotStagingSuffix
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := sweepStaleFilesystemSnapshots(); err == nil {
		t.Fatal("sweep staging without owner binding error = nil")
	}
	assertPathExistence(t, directory, true)
}

func TestSweepFilesystemSnapshotsRejectsAnomalousStaging(t *testing.T) {
	t.Run("marker owner mismatch", func(t *testing.T) {
		setFilesystemSnapshotRoot(t)
		identity := newTestFilesystemSnapshotIdentity(t)
		directory, err := createFilesystemSnapshotStagingDirectory(identity)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(identity) })
		recorded := identity
		makeReusedFilesystemSnapshotOwner(&recorded)
		if err := writeFilesystemSnapshotMarker(directory, recorded); err != nil {
			t.Fatal(err)
		}
		if err := sweepStaleFilesystemSnapshots(); err == nil {
			t.Fatal("sweep staging with marker owner mismatch error = nil")
		}
		assertPathExistence(t, directory, true)
	})
	t.Run("unexpected entry", func(t *testing.T) {
		setFilesystemSnapshotRoot(t)
		identity := newTestFilesystemSnapshotIdentity(t)
		directory, err := createFilesystemSnapshotStagingDirectory(identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeFilesystemSnapshotMarker(directory, identity); err != nil {
			t.Fatal(err)
		}
		if err := writeExclusiveRegularFile(filepath.Join(directory, "unexpected"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := sweepStaleFilesystemSnapshots(); err == nil {
			t.Fatal("sweep staging with unexpected entry error = nil")
		}
		assertPathExistence(t, directory, true)
	})
	t.Run("malformed complete marker", func(t *testing.T) {
		setFilesystemSnapshotRoot(t)
		identity := newTestFilesystemSnapshotIdentity(t)
		directory := writeCompleteFilesystemSnapshotStaging(t, identity)
		if err := os.WriteFile(filepath.Join(directory, filesystemSnapshotMarker), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := sweepStaleFilesystemSnapshots(); err == nil {
			t.Fatal("sweep staging with malformed complete marker error = nil")
		}
		assertPathExistence(t, directory, true)
	})
}

func newTestFilesystemSnapshotIdentity(t *testing.T) filesystemSnapshotIdentity {
	t.Helper()
	owner, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := newFilesystemSnapshotIdentity(runtime.GOOS, owner)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func writeCompleteFilesystemSnapshotStaging(
	t *testing.T,
	identity filesystemSnapshotIdentity,
) string {
	t.Helper()
	directory, err := createFilesystemSnapshotStagingDirectory(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFilesystemSnapshotMarker(directory, identity); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusiveRegularFile(
		filepath.Join(directory, HelperFileName(identity.HelperGOOS)),
		[]byte("staged"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	return directory
}

func assertPublishedFilesystemSnapshot(
	t *testing.T,
	identity filesystemSnapshotIdentity,
	wantHelper string,
) {
	t.Helper()
	if err := verifyFilesystemSnapshotMarker(identity); err != nil {
		t.Fatalf("verify published snapshot marker: %v", err)
	}
	helper, err := os.ReadFile(filepath.Join(identity.Directory, HelperFileName(identity.HelperGOOS)))
	if err != nil {
		t.Fatal(err)
	}
	if string(helper) != wantHelper {
		t.Fatalf("published helper = %q, want %q", helper, wantHelper)
	}
}

func TestTerminateProcessTreeSignalsLeasedGroup(t *testing.T) {
	cmd, guard := startGuardedUnixTestProcess(t, "30")
	killCalled := false
	guard.killGroup = func(pid int, signal syscall.Signal) error {
		killCalled = true
		if pid != -guard.groupID || signal != syscall.SIGKILL {
			t.Errorf("kill group = (%d, %v), want (%d, %v)", pid, signal, -guard.groupID, syscall.SIGKILL)
		}
		return nil
	}
	if err := terminateProcessTree(cmd, guard); err != nil {
		t.Fatal(err)
	}
	if !killCalled {
		t.Fatal("leased process group was not terminated")
	}
	reapGuardedUnixTestProcess(t, cmd, guard)
	if err := closeProcessGuard(guard); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupWorkerLateReapReleasesLimiterCapacity(t *testing.T) {
	releaseWaitResult := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseWaitResult)
		}
	}()
	operationCtx, cancel := context.WithCancel(context.Background())
	limiter := newHelperLimiter(1)
	if _, err := limiter.run(context.Background(), func(capacity *helperCapacityTracker) (Result, error) {
		_, runErr := runFilesystemWorkerWithAttacher(
			context.Background(), operationCtx, "ignored",
			func(string) *exec.Cmd { return exec.Command("sleep", "30") },
			nil, filesystemWorkerRequest{Version: filesystemWorkerVersion, Operation: filesystemWorkerCleanup},
			nil, 0,
			func(cmd *exec.Cmd, guard *processGuard) error {
				if err := attachProcessGuard(cmd, guard); err != nil {
					return err
				}
				guard.beforeWaitResultPublish = func() { <-releaseWaitResult }
				cancel()
				return nil
			},
			capacity,
		)
		return Result{}, runErr
	}); ErrorCode(err) != CodeReapFailed {
		t.Fatalf("limiter.run() code = %q, want %q; error=%v", ErrorCode(err), CodeReapFailed, err)
	}
	assertLimiterCapacityExhausted(t, limiter, "before cleanup late reap")
	close(releaseWaitResult)
	released = true
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, nil
	}); err != nil {
		t.Fatalf("limiter.run() after cleanup late reap error = %v", err)
	}
}

func TestLimiterWaitsForMainAndCleanupLateReaps(t *testing.T) {
	mainCmd, mainGuard := startGuardedUnixTestProcess(t, "30")
	cleanupCmd, cleanupGuard := startGuardedUnixTestProcess(t, "30")
	mainRelease := make(chan struct{})
	cleanupRelease := make(chan struct{})
	mainReleased, cleanupReleased := false, false
	defer func() {
		if !mainReleased {
			close(mainRelease)
		}
		if !cleanupReleased {
			close(cleanupRelease)
		}
	}()
	lateWait := func(cmd *exec.Cmd, guard *processGuard, release <-chan struct{}) <-chan error {
		guard.beforeWaitResultPublish = func() { <-release }
		result := make(chan error, 1)
		safego.Go(context.Background(), nil, "toolbridge.schema-multiple-late-reaps-test.wait", func(context.Context) {
			result <- waitGuardedProcess(cmd, guard)
		})
		return result
	}
	mainWait := lateWait(mainCmd, mainGuard, mainRelease)
	cleanupWait := lateWait(cleanupCmd, cleanupGuard, cleanupRelease)
	limiter := newHelperLimiter(1)
	_, err := limiter.run(context.Background(), func(capacity *helperCapacityTracker) (Result, error) {
		mainErr := stopAndReap(mainCmd, mainGuard, mainWait, CodeTimeout, "main timed out", context.DeadlineExceeded, capacity)
		cleanupErr := stopAndReap(cleanupCmd, cleanupGuard, cleanupWait, CodeTimeout, "cleanup timed out", context.DeadlineExceeded, capacity)
		return Result{}, errors.Join(mainErr, cleanupErr)
	})
	if !errorTreeContainsCode(err, CodeReapFailed) {
		t.Fatalf("combined worker error = %v, want %q", err, CodeReapFailed)
	}
	assertLimiterCapacityExhausted(t, limiter, "before either late reap")
	close(mainRelease)
	mainReleased = true
	assertLimiterCapacityExhausted(t, limiter, "after only main late reap")
	close(cleanupRelease)
	cleanupReleased = true
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, nil
	}); err != nil {
		t.Fatalf("limiter.run() after all late reaps error = %v", err)
	}
}

func assertLimiterCapacityExhausted(t *testing.T, limiter *helperLimiter, stage string) {
	t.Helper()
	started := false
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started || ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("%s: operation started=%v code=%q error=%v", stage, started, ErrorCode(err), err)
	}
}

func TestWaitPublishBarrierLeasesGroupAcrossPIDReuseWindow(t *testing.T) {
	cmd, guard := startGuardedUnixTestProcess(t, "0.05")
	waitCompleted := make(chan struct{})
	releasePublish := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releasePublish)
		}
	}()
	guard.beforeWaitResultPublish = func() {
		close(waitCompleted)
		<-releasePublish
	}
	killCalled := make(chan struct{})
	guard.killGroup = func(pid int, signal syscall.Signal) error {
		if pid != -guard.groupID || signal != syscall.SIGKILL {
			t.Errorf("kill group = (%d, %v), want (%d, %v)", pid, signal, -guard.groupID, syscall.SIGKILL)
		}
		close(killCalled)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	safego.Go(ctx, nil, "toolbridge.schema-wait-publish-barrier-test", func(context.Context) {
		_, err := waitFilesystemWorker(
			ctx, ctx, filesystemWorkerSweep, cmd, guard,
			&boundedBuffer{limit: 64}, &boundedBuffer{limit: 64}, 0, nil,
		)
		result <- err
	})
	<-waitCompleted
	leaseGroupID, err := syscall.Getpgid(guard.leaseProcess.Process.Pid)
	if err != nil {
		t.Fatalf("get process-group lease PGID: %v", err)
	}
	if leaseGroupID != guard.groupID {
		t.Fatalf("process-group lease PGID = %d, want %d", leaseGroupID, guard.groupID)
	}
	cancel()
	select {
	case <-killCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not terminate the leased process group")
	}
	close(releasePublish)
	released = true
	if err := <-result; ErrorCode(err) != CodeCancelled {
		t.Fatalf("wait barrier error = %v, code=%q", err, ErrorCode(err))
	}
}

func setFilesystemSnapshotRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, root)
	}
	return root
}

func assertPathExistence(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if want && err != nil {
		t.Fatalf("expected path %s: %v", path, err)
	}
	if !want && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected path %s: %v", path, err)
	}
}

func startGuardedUnixTestProcess(t *testing.T, sleepDuration string) (*exec.Cmd, *processGuard) {
	t.Helper()
	cmd := exec.Command("sleep", sleepDuration)
	guard, err := prepareProcessGuard(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = closeProcessGuard(guard)
		t.Fatal(err)
	}
	if err := attachProcessGuard(cmd, guard); err != nil {
		_ = guard.killGroup(-guard.groupID, syscall.SIGKILL)
		guard.groupKilled = true
		_ = cmd.Wait()
		_ = closeProcessGuard(guard)
		t.Fatal(err)
	}
	return cmd, guard
}

func reapGuardedUnixTestProcess(t *testing.T, cmd *exec.Cmd, guard *processGuard) {
	t.Helper()
	if err := syscall.Kill(-guard.groupID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatal(err)
	}
	guard.groupKilled = true
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
	}
}

func newBlockingFilesystemWorkerClientConfig(t *testing.T) ClientConfig {
	t.Helper()
	dir := t.TempDir()
	helper := filepath.Join(dir, HelperFileName(runtime.GOOS))
	image, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, image, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, testHelperIdentity()); err != nil {
		t.Fatal(err)
	}
	return ClientConfig{
		HelperPath: helper, ManifestPath: manifest, FilesystemWorkerPath: os.Args[0], Identity: testHelperIdentity(),
	}
}

func installBlockingFilesystemWorker(t *testing.T) blockingFilesystemWorkerFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := blockingFilesystemWorkerFixture{
		fifo:          filepath.Join(dir, "blocked-open.fifo"),
		started:       filepath.Join(dir, "worker-started"),
		finished:      filepath.Join(dir, "worker-finished"),
		helperStarted: filepath.Join(dir, "helper-started"),
	}
	if err := syscall.Mkfifo(fixture.fifo, 0o600); err != nil {
		t.Fatalf("create blocking schema filesystem FIFO: %v", err)
	}
	return fixture
}

func setBlockingFilesystemWorkerEnvironment(t *testing.T, fixture blockingFilesystemWorkerFixture) {
	t.Helper()
	for _, item := range blockingFilesystemWorkerEnvironment(fixture) {
		name, value, _ := strings.Cut(item, "=")
		t.Setenv(name, value)
	}
}

func blockingFilesystemWorkerEnvironment(fixture blockingFilesystemWorkerFixture) []string {
	return []string{
		blockingFilesystemWorkerEnv + "=1",
		blockingFilesystemWorkerFIFOEnv + "=" + fixture.fifo,
		blockingFilesystemWorkerStartEnv + "=" + fixture.started,
		blockingFilesystemWorkerDoneEnv + "=" + fixture.finished,
	}
}

func assertBlockedFilesystemWorkerResult(t *testing.T, fixture blockingFilesystemWorkerFixture, started time.Time) {
	t.Helper()
	if elapsed := time.Since(started); elapsed > filesystemSnapshotCleanupTimeout+reapDeadline+time.Second {
		t.Fatalf("blocked schema filesystem worker returned after %s", elapsed)
	}
	rawPID, err := os.ReadFile(fixture.started)
	if err != nil {
		t.Fatalf("read blocked worker PID: %v", err)
	}
	pid, err := strconv.Atoi(string(rawPID))
	if err != nil || pid <= 0 {
		t.Fatalf("parse blocked worker PID %q: %v", rawPID, err)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("blocked schema filesystem worker was not reaped: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	for _, path := range []string{fixture.finished, fixture.helperStarted} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("late filesystem write or helper launch at %s: %v", path, err)
		}
	}
}
