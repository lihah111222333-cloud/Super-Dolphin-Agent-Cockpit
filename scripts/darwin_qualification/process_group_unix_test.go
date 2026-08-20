//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	ownedCommandTestTimeout = 4 * time.Second
	ownedCommandTestPoll    = 10 * time.Millisecond
)

func TestRunOwnedCommandUnixZeroExitReapsRoot(t *testing.T) {
	command, rootPIDPath, _ := newOwnedCommandUnix(t, `printf '%s\n' "$$" > "$RUN_OWNED_ROOT_PID"`)
	ctx, cancel := context.WithTimeout(context.Background(), ownedCommandTestTimeout)
	defer cancel()
	if err := runOwnedCommand(ctx, command); err != nil {
		t.Fatalf("runOwnedCommand() error = %v, want nil", err)
	}
	waitOwnedPIDGone(t, readOwnedPID(t, rootPIDPath))
}

func TestRunOwnedCommandUnixNonZeroExitPreservesCodeAndReapsRoot(t *testing.T) {
	command, rootPIDPath, _ := newOwnedCommandUnix(t, `printf '%s\n' "$$" > "$RUN_OWNED_ROOT_PID"; exit 23`)
	ctx, cancel := context.WithTimeout(context.Background(), ownedCommandTestTimeout)
	defer cancel()
	err := runOwnedCommand(ctx, command)
	if err == nil {
		t.Fatal("runOwnedCommand() error = nil, want exit 23")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("runOwnedCommand() error = %v, want *exec.ExitError", err)
	}
	if got := exitErr.ExitCode(); got != 23 {
		t.Fatalf("runOwnedCommand() exit code = %d, want 23", got)
	}
	waitOwnedPIDGone(t, readOwnedPID(t, rootPIDPath))
}

func TestRunOwnedCommandUnixTimeoutKillsRootAndDescendant(t *testing.T) {
	command, rootPIDPath, descendantPIDPath := newOwnedCommandUnix(t, `printf '%s\n' "$$" > "$RUN_OWNED_ROOT_PID"; /bin/sleep 30 & printf '%s\n' "$!" > "$RUN_OWNED_DESCENDANT_PID"; wait`)
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	err := runOwnedCommand(ctx, command)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runOwnedCommand() error = %v, want context deadline exceeded", err)
	}
	waitOwnedPIDGone(t, readOwnedPID(t, rootPIDPath))
	waitOwnedPIDGone(t, readOwnedPID(t, descendantPIDPath))
}

func TestRunOwnedCommandUnixSuccessfulRootWithDescendantReportsAndCleans(t *testing.T) {
	command, rootPIDPath, descendantPIDPath := newOwnedCommandUnix(t, `printf '%s\n' "$$" > "$RUN_OWNED_ROOT_PID"; /bin/sleep 30 & printf '%s\n' "$!" > "$RUN_OWNED_DESCENDANT_PID"; exit 0`)
	ctx, cancel := context.WithTimeout(context.Background(), ownedCommandTestTimeout)
	defer cancel()
	err := runOwnedCommand(ctx, command)
	if err == nil {
		t.Fatal("runOwnedCommand() error = nil, want descendant ownership failure")
	}
	waitOwnedPIDGone(t, readOwnedPID(t, rootPIDPath))
	waitOwnedPIDGone(t, readOwnedPID(t, descendantPIDPath))
}

func TestRunOwnedCommandUnixRejectsNilAndNonCanonicalOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), ownedCommandTestTimeout)
	defer cancel()
	if err := runOwnedCommand(ctx, nil); err == nil {
		t.Fatal("runOwnedCommand(nil) error = nil, want fail-fast")
	}
	for name, attributes := range map[string]*syscall.SysProcAttr{
		"setsid preconfigured":  {Setsid: true},
		"setpgid preconfigured": {Setpgid: true},
	} {
		command := exec.Command("/bin/sh", "-c", "exit 0")
		command.SysProcAttr = attributes
		if err := runOwnedCommand(ctx, command); err == nil {
			t.Errorf("runOwnedCommand(%s) error = nil, want noncanonical ownership rejection", name)
		}
	}
}

func TestRunOwnedCommandUnixRepeatedRunsLeaveNoProcessOrGoroutineLeak(t *testing.T) {
	runtime.GC()
	baselineGoroutines := runtime.NumGoroutine()
	for index := range 16 {
		command, rootPIDPath, _ := newOwnedCommandUnix(t, `printf '%s\n' "$$" > "$RUN_OWNED_ROOT_PID"`)
		ctx, cancel := context.WithTimeout(context.Background(), ownedCommandTestTimeout)
		err := runOwnedCommand(ctx, command)
		cancel()
		if err != nil {
			t.Fatalf("runOwnedCommand() iteration %d error = %v, want nil", index, err)
		}
		waitOwnedPIDGone(t, readOwnedPID(t, rootPIDPath))
	}
	deadline := time.Now().Add(ownedCommandTestTimeout)
	for {
		runtime.GC()
		if runtime.NumGoroutine() <= baselineGoroutines+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine count after repeated runs = %d, baseline = %d", runtime.NumGoroutine(), baselineGoroutines)
		}
		time.Sleep(ownedCommandTestPoll)
	}
}

func newOwnedCommandUnix(t *testing.T, script string) (*exec.Cmd, string, string) {
	t.Helper()
	root := t.TempDir()
	rootPIDPath := filepath.Join(root, "root.pid")
	descendantPIDPath := filepath.Join(root, "descendant.pid")
	command := exec.Command("/bin/sh", "-c", script)
	command.Env = ownedCommandTestEnvironment(rootPIDPath, descendantPIDPath)
	return command, rootPIDPath, descendantPIDPath
}

func ownedCommandTestEnvironment(rootPIDPath, descendantPIDPath string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "RUN_OWNED_ROOT_PID=") || strings.HasPrefix(entry, "RUN_OWNED_DESCENDANT_PID=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "RUN_OWNED_ROOT_PID="+rootPIDPath, "RUN_OWNED_DESCENDANT_PID="+descendantPIDPath)
}

func readOwnedPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(ownedCommandTestTimeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			value := strings.TrimSuffix(string(data), "\n")
			pid, parseErr := strconv.Atoi(value)
			if parseErr == nil && pid > 1 && strconv.Itoa(pid) == value {
				return pid
			}
			t.Fatalf("PID file %q contains noncanonical value %q", path, value)
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read PID file %q: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("PID file %q was not published", path)
		}
		time.Sleep(ownedCommandTestPoll)
	}
}

func waitOwnedPIDGone(t *testing.T, pid int) {
	t.Helper()
	if pid <= 1 {
		t.Fatalf("invalid owned PID %d", pid)
	}
	deadline := time.Now().Add(ownedCommandTestTimeout)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil && !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EPERM) {
			t.Fatalf("probe owned PID %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("owned PID %d remained after bounded cleanup", pid)
		}
		time.Sleep(ownedCommandTestPoll)
	}
}
