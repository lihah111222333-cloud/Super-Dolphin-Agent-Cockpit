//go:build !windows

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

const (
	guardTreeFixtureEnv         = "SUPER_DOLPHIN_TEST_GUARD_TREE_FIXTURE"
	guardDigestBlockEnv         = "SUPER_DOLPHIN_TEST_GUARD_DIGEST_BLOCK"
	guardDigestTargetEnv        = "SUPER_DOLPHIN_TEST_GUARD_DIGEST_TARGET"
	guardDigestFIFOEnv          = "SUPER_DOLPHIN_TEST_GUARD_DIGEST_FIFO"
	guardDigestHelperPIDEnv     = "SUPER_DOLPHIN_TEST_GUARD_DIGEST_HELPER_PID"
	guardDigestHelperDirEnv     = "SUPER_DOLPHIN_TEST_GUARD_DIGEST_HELPER_DIR"
	releaseFilesystemHelperEnv  = "SUPER_DOLPHIN_RELEASE_FS_HELPER"
	releaseFilesystemHelperPath = "SUPER_DOLPHIN_RELEASE_FS_HELPER_EXECUTABLE"
)

func runGuardProcessTreeFixtureIfRequested() (bool, int) {
	if os.Getenv(guardDigestBlockEnv) == "1" && os.Getenv(releaseFilesystemHelperEnv) == "1" {
		return true, runBlockedGuardDigestHelperFixture()
	}
	if os.Getenv(guardTreeFixtureEnv) == "1" {
		return true, runGuardTreeFixture()
	}
	return false, 0
}

func TestGuardReadyTimeoutReapsBlockedDigestHelperTree(t *testing.T) {
	fixture := startGuardTreeTestProcess(t)
	helperPID := waitGuardFixturePID(t, fixture.helperPIDPath)
	helperDir := waitGuardFixtureText(t, fixture.helperDirPath)
	if _, err := waitGuardReadyReceipt(fixture.stdout, 50*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitGuardReadyReceipt() error = %v, want timeout", err)
	}
	if err := stopStartedGuard(fixture.cmd, fixture.lease); err != nil {
		t.Fatal(err)
	}
	fixture.lease = nil

	assertGuardFixturePIDGone(t, fixture.cmd.Process.Pid)
	assertGuardFixturePIDGone(t, helperPID)
	if _, err := os.Stat(helperDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("guard helper directory %q still exists: %v", helperDir, err)
	}
}

type guardTreeTestProcess struct {
	cmd           *exec.Cmd
	stdout        io.ReadCloser
	lease         *guardProcessTreeLease
	helperPIDPath string
	helperDirPath string
}

func startGuardTreeTestProcess(t *testing.T) *guardTreeTestProcess {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "guard")
	if err := os.WriteFile(target, []byte("guard fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(root, "blocked-digest.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &guardTreeTestProcess{
		helperPIDPath: filepath.Join(root, "helper.pid"),
		helperDirPath: filepath.Join(root, "helper.dir"),
	}
	fixture.cmd = exec.Command(os.Args[0], "-test.run=^$")
	fixture.cmd.Env = append(os.Environ(),
		guardTreeFixtureEnv+"=1",
		guardDigestBlockEnv+"=1",
		guardDigestTargetEnv+"="+target,
		guardDigestFIFOEnv+"="+fifo,
		guardDigestHelperPIDEnv+"="+fixture.helperPIDPath,
		guardDigestHelperDirEnv+"="+fixture.helperDirPath,
	)
	stdout, err := fixture.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	fixture.stdout = stdout
	if err := configureGuardProcessTree(fixture.cmd); err != nil {
		t.Fatal(err)
	}
	if err := fixture.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fixture.lease, err = attachGuardProcessTree(fixture.cmd)
	if err != nil {
		_ = fixture.cmd.Process.Kill()
		_ = fixture.cmd.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if fixture.lease != nil {
			_ = stopStartedGuard(fixture.cmd, fixture.lease)
		}
	})
	return fixture
}

func TestGuardProcessTreeLeaseRejectsPIDReuseHandle(t *testing.T) {
	cmd := exec.Command("sh", "-c", "while :; do sleep 1; done")
	if err := configureGuardProcessTree(cmd); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lease, err := attachGuardProcessTree(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stopStartedGuard(cmd, lease) })

	reusedHandle, err := os.FindProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	forged := &exec.Cmd{Process: reusedHandle}
	if err := stopStartedGuard(forged, lease); err == nil ||
		!strings.Contains(err.Error(), "direct-child ownership") {
		t.Fatalf("stopStartedGuard() error = %v, want direct-child ownership error", err)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("original Guard process was signaled through reused handle: %v", err)
	}
}

func runGuardTreeFixture() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	cleanup, err := recovery.PrepareReleaseFilesystemHelper()
	if err != nil {
		return 21
	}
	helperPath := os.Getenv(releaseFilesystemHelperPath)
	if helperPath == "" {
		_ = cleanup()
		return 22
	}
	if err := os.WriteFile(os.Getenv(guardDigestHelperDirEnv), []byte(filepath.Dir(helperPath)), 0o600); err != nil {
		_ = cleanup()
		return 23
	}
	_, digestErr := recovery.ComputeReleaseDigestContext(ctx, os.Getenv(guardDigestTargetEnv))
	cleanupErr := cleanup()
	if context.Cause(ctx) == nil || cleanupErr != nil || !errors.Is(digestErr, context.Canceled) {
		return 24
	}
	return 0
}

func runBlockedGuardDigestHelperFixture() int {
	pidPath := os.Getenv(guardDigestHelperPIDEnv)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return 31
	}
	file, err := os.OpenFile(os.Getenv(guardDigestFIFOEnv), os.O_RDONLY, 0)
	if err != nil {
		return 32
	}
	defer file.Close()
	return 33
}

func waitGuardFixturePID(t *testing.T, path string) int {
	t.Helper()
	raw := waitGuardFixtureText(t, path)
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		t.Fatalf("parse fixture PID %q: %v", raw, err)
	}
	return pid
}

func waitGuardFixtureText(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			return string(raw)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read Guard fixture marker %q: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Guard fixture marker %q was not published", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertGuardFixturePIDGone(t *testing.T, pid int) {
	t.Helper()
	if pid <= 0 {
		t.Fatalf("invalid fixture PID %d", pid)
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.Signal(0)); !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("fixture PID %d is still present: %v", pid, err)
	}
}
