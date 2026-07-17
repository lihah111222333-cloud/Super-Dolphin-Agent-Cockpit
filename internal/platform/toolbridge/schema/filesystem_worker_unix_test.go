//go:build unix

package schema

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if elapsed := time.Since(started); elapsed > 2*time.Second {
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
