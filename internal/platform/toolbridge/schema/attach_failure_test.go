//go:build unix

package schema

import (
	"bytes"
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
)

func TestCleanupUnattachedProcessKillsDescendantsBeforeReap(t *testing.T) {
	cmd, guard, processIDs := startAttachFailureProcessTree(t)
	if err := attachProcessGuard(cmd, guard); err != nil {
		t.Fatalf("attachProcessGuard() error = %v", err)
	}

	err := cleanupUnattachedProcessTree(cmd, guard, errors.New("injected attach failure"))
	if ErrorCode(err) != CodeProcessStartFailed {
		t.Fatalf("cleanupUnattachedProcessTree() code = %q, want %q; error=%v", ErrorCode(err), CodeProcessStartFailed, err)
	}
	for _, pid := range processIDs {
		assertAttachFailureProcessGone(t, pid)
	}
}

func TestPrepareProcessGuardLeaseFailurePreventsWorkerStart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command("sh", "-c", `printf started > "$1"`, "sh", marker)
	leaseErr := errors.New("injected process-group lease acquisition failure")

	guard, err := prepareProcessGuardWithLease(
		cmd,
		func() (*exec.Cmd, *os.File, int, error) { return nil, nil, 0, leaseErr },
	)
	if guard != nil || !errors.Is(err, leaseErr) {
		t.Fatalf("prepareProcessGuardWithLease() guard=%v error=%v", guard, err)
	}
	if cmd.Process != nil {
		t.Fatalf("worker process started despite lease failure: pid=%d", cmd.Process.Pid)
	}
	if _, statErr := os.Lstat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("worker command ran despite lease failure: %v", statErr)
	}
}

func TestCleanupUnattachedProcessRejectsUnownedProcessGroup(t *testing.T) {
	cmd, _, processIDs := startAttachFailureProcessTree(t)

	err := cleanupUnattachedProcessTree(cmd, nil, errors.New("injected attach failure"))
	if ErrorCode(err) != CodeReapFailed {
		t.Fatalf("cleanupUnattachedProcessTree(unowned) code = %q, want %q; error=%v", ErrorCode(err), CodeReapFailed, err)
	}
	for _, pid := range processIDs {
		if signalErr := syscall.Kill(pid, 0); signalErr != nil {
			t.Fatalf("process %d unexpectedly exited without an ownership lease: %v", pid, signalErr)
		}
	}
}

func TestFilesystemWorkerInternalAttachFailuresReapTreeAndRemoveSnapshot(t *testing.T) {
	for _, stage := range []processGuardAttachStage{
		processGuardAttachCaptureIdentity,
		processGuardAttachValidateProcessGroup,
		processGuardAttachValidateOwnership,
	} {
		t.Run(string(stage), func(t *testing.T) {
			snapshotRoot := setFilesystemSnapshotRoot(t)
			snapshotsBefore := filesystemSnapshotDirectoryNames(t, snapshotRoot)
			fixture := newFilesystemAttachFailureFixture(t, stage)
			request := filesystemWorkerRequest{
				Version: filesystemWorkerVersion, Operation: filesystemWorkerExecute,
				Snapshot: fixture.snapshot,
			}

			_, err := runFilesystemWorkerWithAttacher(
				context.Background(), context.Background(), os.Args[0], fixture.command, nil,
				request, nil, 0, fixture.attach, nil,
			)
			if ErrorCode(err) != CodeProcessStartFailed ||
				errorTreeContainsCode(err, CodeReapFailed) ||
				!errors.Is(err, fixture.attachErr) {
				t.Fatalf("runFilesystemWorkerWithAttacher() error=%v code=%q", err, ErrorCode(err))
			}
			if fixture.workerStarts != 2 {
				t.Fatalf("filesystem worker starts = %d, want execute and cleanup workers", fixture.workerStarts)
			}
			for _, identity := range fixture.processIdentities {
				assertStableProcessIdentityGone(t, identity)
			}
			assertFilesystemSnapshotSetUnchanged(t, snapshotRoot, snapshotsBefore)
		})
	}
}

type filesystemAttachFailureFixture struct {
	processMarker     string
	snapshot          filesystemSnapshotIdentity
	attachErr         error
	failStage         processGuardAttachStage
	workerStarts      int
	processIdentities []pidregistry.StableProcessIdentity
}

func newFilesystemAttachFailureFixture(t *testing.T, failStage processGuardAttachStage) *filesystemAttachFailureFixture {
	t.Helper()
	owner, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := newFilesystemSnapshotIdentity(runtime.GOOS, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeExecutableSnapshot([]byte("snapshot"), snapshot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(snapshot) })
	return &filesystemAttachFailureFixture{
		processMarker: filepath.Join(t.TempDir(), "processes"),
		snapshot:      snapshot,
		attachErr:     fmt.Errorf("injected attach failure at %s", failStage),
		failStage:     failStage,
	}
}

func (fixture *filesystemAttachFailureFixture) command(path string) *exec.Cmd {
	fixture.workerStarts++
	if fixture.workerStarts != 1 {
		return exec.Command(path)
	}
	return exec.Command(
		"sh",
		"-c",
		`sleep 30 & child=$!; printf '%s %s' "$$" "$child" > "$1"; wait`,
		"sh",
		fixture.processMarker,
	)
}

func (fixture *filesystemAttachFailureFixture) attach(cmd *exec.Cmd, guard *processGuard) error {
	processIDs, err := waitForAttachFailureProcessIDsWithin(fixture.processMarker, 5*time.Second)
	if err != nil {
		return err
	}
	fixture.processIdentities, err = captureAttachFailureProcessIdentities(processIDs)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(fixture.snapshot.Directory); err != nil {
		return fmt.Errorf("snapshot missing before attach failure: %w", err)
	}
	return attachProcessGuardWithProbe(cmd, guard, func(stage processGuardAttachStage) error {
		if stage == fixture.failStage {
			return fixture.attachErr
		}
		return nil
	})
}

func TestCleanupUnattachedProcessFailsFastWhenTreeIsUnreaped(t *testing.T) {
	cmd, attachedGuard, processIDs := startAttachFailureProcessTree(t)
	killErr := errors.New("injected process-group kill failure")
	if err := attachProcessGuard(cmd, attachedGuard); err != nil {
		t.Fatalf("attachProcessGuard() error = %v", err)
	}
	realKillGroup := attachedGuard.killGroup
	attachedGuard.killGroup = func(int, syscall.Signal) error { return killErr }

	err := cleanupUnattachedProcessTree(cmd, attachedGuard, errors.New("injected attach failure"))
	if ErrorCode(err) != CodeReapFailed || !errors.Is(err, killErr) {
		t.Fatalf("cleanupUnattachedProcessTree(unreaped) error=%v code=%q", err, ErrorCode(err))
	}
	for _, pid := range processIDs {
		if signalErr := syscall.Kill(pid, 0); signalErr != nil {
			t.Fatalf("process %d unexpectedly exited before fail-fast return: %v", pid, signalErr)
		}
	}
	attachedGuard.killGroup = realKillGroup
	if err := realKillGroup(-attachedGuard.groupID, syscall.SIGKILL); err != nil {
		t.Fatalf("release unreaped process tree: %v", err)
	}
	attachedGuard.groupKilled = true
}

func TestAttachFailureCapacityReleasesOnlyAfterLateReap(t *testing.T) {
	cmd, guard, _ := startAttachFailureProcessTree(t)
	if err := attachProcessGuard(cmd, guard); err != nil {
		t.Fatalf("attachProcessGuard() error = %v", err)
	}
	limiter := newHelperLimiter(1)
	if err := limiter.acquire(context.Background()); err != nil {
		t.Fatalf("acquire initial capacity: %v", err)
	}
	capacity := &helperCapacityTracker{release: func() { <-limiter.slots }, pending: 1}
	realKillGroup := guard.killGroup
	guard.killGroup = func(int, syscall.Signal) error { return errors.New("injected process-group kill failure") }
	err := cleanupUnattachedProcessTreeWithCapacity(cmd, guard, errors.New("injected attach failure"), capacity)
	if ErrorCode(err) != CodeReapFailed {
		t.Fatalf("cleanupUnattachedProcessTreeWithCapacity() code=%q error=%v", ErrorCode(err), err)
	}
	reapFailures, complete := errorTreeCodeCount(err, CodeReapFailed)
	capacity.finish(reapFailures, complete)
	assertLimiterCapacityExhausted(t, limiter, "before attach-failure late reap")
	guard.killGroup = realKillGroup
	if err := realKillGroup(-guard.groupID, syscall.SIGKILL); err != nil {
		t.Fatalf("release attach-failure process tree: %v", err)
	}
	guard.groupKilled = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := limiter.acquire(ctx); err != nil {
		t.Fatalf("capacity was not released after attach-failure late reap: %v", err)
	}
	<-limiter.slots
}

func startAttachFailureProcessTree(t *testing.T) (*exec.Cmd, *processGuard, []int) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "processes")
	cmd := exec.Command("sh", "-c", `sleep 30 & child=$!; printf '%s %s' "$$" "$child" > "$1"; wait`, "sh", marker)
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}
	guard, err := prepareProcessGuard(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		_ = closeProcessGuard(guard)
		t.Fatal(err)
	}
	processIDs := waitForAttachFailureProcessIDs(t, marker)
	t.Cleanup(func() {
		_ = guard.killGroup(-guard.groupID, syscall.SIGKILL)
		guard.groupKilled = true
		_ = closeProcessGuard(guard)
		for _, pid := range processIDs {
			waitForAttachFailureProcessGone(t, pid)
		}
	})
	return cmd, guard, processIDs
}

func waitForAttachFailureProcessIDs(t *testing.T, marker string) []int {
	t.Helper()
	processIDs, err := waitForAttachFailureProcessIDsWithin(marker, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return processIDs
}

func waitForAttachFailureProcessIDsWithin(marker string, timeout time.Duration) ([]int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(marker)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read process marker: %w", err)
		}
		processIDs, complete, parseErr := parseAttachFailureProcessIDs(raw)
		if parseErr != nil {
			return nil, parseErr
		}
		if complete {
			return processIDs, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, errors.New("timed out waiting for process marker")
}

func parseAttachFailureProcessIDs(raw []byte) ([]int, bool, error) {
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		return nil, false, nil
	}
	processIDs := make([]int, 0, len(fields))
	for _, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil {
			return nil, false, fmt.Errorf("parse process PID %q: %w", field, err)
		}
		processIDs = append(processIDs, pid)
	}
	return processIDs, true, nil
}

func captureAttachFailureProcessIdentities(processIDs []int) ([]pidregistry.StableProcessIdentity, error) {
	identities := make([]pidregistry.StableProcessIdentity, 0, len(processIDs))
	for _, pid := range processIDs {
		identity, err := pidregistry.CaptureStableProcessIdentity(pid)
		if err != nil {
			return nil, fmt.Errorf("capture process %d identity: %w", pid, err)
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

func assertAttachFailureProcessGone(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process %d remains after attach failure cleanup: %v", pid, err)
	}
}

func waitForAttachFailureProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
