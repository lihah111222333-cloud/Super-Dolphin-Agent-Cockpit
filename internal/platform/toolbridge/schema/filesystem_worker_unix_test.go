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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	client, err := NewClient(ctx, config)
	if client != nil || ErrorCode(err) != CodeProcessStartFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("NewClient(blocked verification) = (%v, %v), code=%q", client, err, ErrorCode(err))
	}
	assertBlockedFilesystemWorkerResult(t, fixture, started)
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
	fixture := installBlockingFilesystemWorker(t)
	workerStarts := 0
	client.workerCommand = func(path string) *exec.Cmd {
		workerStarts++
		cmd := exec.Command(path)
		if workerStarts == 2 {
			cmd.Env = blockingFilesystemWorkerEnvironment(fixture)
		}
		return cmd
	}
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperFixtureTimeout)
	defer cancel()
	result := make(chan error, 1)
	safego.Go(ctx, nil, "toolbridge.schema-blocked-cleanup-test", func(context.Context) {
		_, executeErr := client.Execute(ctx, testInvocation(canonical), allowFence)
		result <- executeErr
	})
	waitForHelperMarker(t, fixture.started)
	cleanupStarted := time.Now()
	select {
	case err = <-result:
	case <-time.After(filesystemSnapshotCleanupTimeout + reapDeadline + time.Second):
		t.Fatal("blocked cleanup worker was not synchronously reaped")
	}
	if ErrorCode(err) != CodeReapFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Execute(blocked cleanup) error=%v code=%q", err, ErrorCode(err))
	}
	if workerStarts != 2 {
		t.Fatalf("filesystem worker starts = %d, want execute and cleanup workers", workerStarts)
	}
	assertBlockedFilesystemWorkerResult(t, fixture, cleanupStarted)
	assertFilesystemSnapshotSetUnchanged(t, snapshotRoot, snapshotsBefore)
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

func TestSweepFilesystemSnapshotsCleansOnlyStaleCompleteStaging(t *testing.T) {
	setFilesystemSnapshotRoot(t)
	active := newTestFilesystemSnapshotIdentity(t)
	activeDirectory := writeCompleteFilesystemSnapshotStaging(t, active)
	stale := newTestFilesystemSnapshotIdentity(t)
	stale.OwnerPID = 1 << 30
	stale.OwnerStartToken = "stale-start"
	stale.OwnerExecutable = "stale-executable"
	staleDirectory := writeCompleteFilesystemSnapshotStaging(t, stale)
	empty := newTestFilesystemSnapshotIdentity(t)
	emptyDirectory, err := createFilesystemSnapshotStagingDirectory(empty)
	if err != nil {
		t.Fatal(err)
	}
	markerOnly := newTestFilesystemSnapshotIdentity(t)
	markerOnlyDirectory, err := createFilesystemSnapshotStagingDirectory(markerOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFilesystemSnapshotMarker(markerOnlyDirectory, markerOnly); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []filesystemSnapshotIdentity{active, stale, empty, markerOnly} {
		t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(identity) })
	}
	if err := sweepStaleFilesystemSnapshots(); err != nil {
		t.Fatal(err)
	}
	assertPathExistence(t, activeDirectory, true)
	assertPathExistence(t, staleDirectory, false)
	assertPathExistence(t, emptyDirectory, true)
	assertPathExistence(t, markerOnlyDirectory, true)
}

func TestSweepFilesystemSnapshotsRejectsAnomalousStaging(t *testing.T) {
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
	cmd, guard := startGuardedUnixTestProcess(t, "sleep 30")
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
	reapGuardedUnixTestProcess(t, cmd)
	if err := closeProcessGuard(guard); err != nil {
		t.Fatal(err)
	}
}

func TestWaitPublishBarrierLeasesGroupAcrossPIDReuseWindow(t *testing.T) {
	cmd, guard := startGuardedUnixTestProcess(t, "sleep 0.05")
	waitCompleted := make(chan struct{})
	releasePublish := make(chan struct{})
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
			&boundedBuffer{limit: 64}, &boundedBuffer{limit: 64}, 0,
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
	case <-time.After(time.Second):
		t.Fatal("cancellation did not terminate the leased process group")
	}
	close(releasePublish)
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

func startGuardedUnixTestProcess(t *testing.T, command string) (*exec.Cmd, *processGuard) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command)
	if err := configureProcess(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	guard, err := attachProcessGuard(cmd)
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Fatal(err)
	}
	return cmd, guard
}

func reapGuardedUnixTestProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatal(err)
	}
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
