//go:build darwin || linux || windows

package standalone

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestInstanceLockContract(t *testing.T) {
	if err := (*InstanceLock)(nil).ValidateStateDir(); err == nil {
		t.Fatal("nil instance lock passed state-root validation")
	}
	t.Run("one_owner_and_reacquire", testOneInstanceOwner)
	t.Run("data_directories_are_independent", func(t *testing.T) {
		first := acquireTestInstance(t, privateStateDir(t))
		second := acquireTestInstance(t, privateStateDir(t))
		closeTestInstance(t, first)
		closeTestInstance(t, second)
	})
	t.Run("invalid_path_has_no_effect", func(t *testing.T) {
		for _, path := range []string{"", "relative-state", filepath.Join(t.TempDir(), "missing")} {
			if lock, err := AcquireInstance(path); err == nil || lock != nil {
				t.Fatalf("AcquireInstance(%q) = %v, %v; want failure", path, lock, err)
			}
		}
	})
	t.Run("cross_process_and_crash_release", testProcessInstanceLock)
}

func TestInstanceLockValidateStateDirRejectsClosedLock(t *testing.T) {
	lock := acquireTestInstance(t, privateStateDir(t))
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lock.ValidateStateDir(); err == nil {
		t.Fatal("closed instance lock passed state-root validation")
	}
}

// TestInstanceLockIndependentDirectoriesCanAcquireInParallel 保证单实例边界按状态域隔离，而非进程全局互斥。
func TestInstanceLockIndependentDirectoriesCanAcquireInParallel(t *testing.T) {
	const count = 8
	dirs := make([]string, count)
	for i := range dirs {
		dirs[i] = privateStateDir(t)
	}
	start := make(chan struct{})
	locks := make([]*InstanceLock, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := range dirs {
		wg.Add(1)
		safego.Go(t.Context(), nil, "standalone.instance-test.parallel-acquire", func(context.Context) {
			defer wg.Done()
			<-start
			lock, err := AcquireInstance(dirs[i])
			if err != nil {
				errs <- fmt.Errorf("directory %d: %w", i, err)
				return
			}
			locks[i] = lock
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := make(map[string]int, count)
	for i, lock := range locks {
		if lock == nil {
			continue
		}
		canonical := lock.CanonicalStateDir()
		if previous, exists := seen[canonical]; exists {
			t.Errorf("directories %d and %d share canonical state dir %q", previous, i, canonical)
		}
		seen[canonical] = i
		if err := lock.Close(); err != nil {
			t.Errorf("close directory %d: %v", i, err)
		}
	}
}

func testOneInstanceOwner(t *testing.T) {
	t.Helper()
	dir := privateStateDir(t)
	first := acquireTestInstance(t, dir)
	second, err := AcquireInstance(dir)
	if second != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second owner = %v, %v; want ErrAlreadyRunning", second, err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	closeTestInstance(t, first)
	closeTestInstance(t, first)
	third := acquireTestInstance(t, dir)
	after, err := os.Stat(dir)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("state directory identity changed after release: %v", err)
	}
	closeTestInstance(t, third)
}

func privateStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func acquireTestInstance(t *testing.T, dir string) *InstanceLock {
	t.Helper()
	lock, err := AcquireInstance(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeTestInstance(t, lock) })
	return lock
}

func closeTestInstance(t *testing.T, lock *InstanceLock) {
	t.Helper()
	if err := lock.Close(); err != nil {
		t.Error(err)
	}
}

// TestInstanceLockHelper 是持锁 subprocess；不继承任何产品数据目录或凭据。
func TestInstanceLockHelper(t *testing.T) {
	if os.Getenv("ORCH_INSTANCE_LOCK_TEST_HELPER") != "1" {
		return
	}
	lock, err := AcquireInstance(os.Getenv("ORCH_INSTANCE_LOCK_TEST_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestInstance(t, lock)
	if _, err := fmt.Fprintln(os.Stdout, "orch-lock-acquired"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func testProcessInstanceLock(t *testing.T) {
	t.Helper()
	dir := privateStateDir(t)
	cmd, stdout := startInstanceLockHelper(t, dir)
	signal, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || signal != "orch-lock-acquired\n" {
		t.Fatalf("helper readiness = %q, %v", signal, err)
	}
	if contender, err := AcquireInstance(dir); contender != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("cross-process contender = %v, %v", contender, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed subprocess unexpectedly exited successfully")
	}
	closeTestInstance(t, acquireTestInstance(t, dir))
}

func startInstanceLockHelper(t *testing.T, dir string) (*exec.Cmd, io.ReadCloser) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInstanceLockHelper$")
	cmd.Env = append(os.Environ(), "ORCH_INSTANCE_LOCK_TEST_HELPER=1", "ORCH_INSTANCE_LOCK_TEST_DIR="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cancel()
			if err := cmd.Wait(); err != nil && ctx.Err() == nil {
				t.Error(err)
			}
		}
	})
	return cmd, stdout
}
