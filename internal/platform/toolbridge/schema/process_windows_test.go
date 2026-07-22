//go:build windows

package schema

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestStopAndReapClosesWindowsGuardOnTimeout(t *testing.T) {
	cmd := exec.Command("ping.exe", "-n", "30", "127.0.0.1")
	guard, err := prepareProcessGuard(cmd)
	if err != nil {
		t.Fatalf("prepareProcessGuard() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		_ = closeProcessGuard(guard)
		t.Fatalf("cmd.Start() error = %v", err)
	}
	if err := attachProcessGuard(cmd, guard); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = closeProcessGuard(guard)
		t.Fatalf("attachProcessGuard() error = %v", err)
	}
	t.Cleanup(func() {
		_ = closeProcessGuard(guard)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	err = stopAndReap(
		cmd,
		guard,
		make(chan error),
		CodeTimeout,
		"fixture timed out",
		context.DeadlineExceeded,
		nil,
	)
	if ErrorCode(err) != CodeReapFailed {
		t.Fatalf("stopAndReap() code = %q, want %q; error=%v", ErrorCode(err), CodeReapFailed, err)
	}
	if guard.handle != 0 {
		t.Fatalf("guard handle = %v, want closed handle", guard.handle)
	}
	if err := closeProcessGuard(guard); err != nil {
		t.Fatalf("closeProcessGuard() after timeout error = %v", err)
	}
	waitResult := make(chan error, 1)
	safego.Go(context.Background(), nil, "toolbridge.schema.windows-test.wait", func(context.Context) {
		waitResult <- cmd.Wait()
	})
	select {
	case waitErr := <-waitResult:
		if waitErr == nil {
			t.Fatal("guarded process exited successfully after forced termination")
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("guarded process remained alive after Job Object termination and close")
	}
}

func TestWindowsInternalAttachFailuresReapPreparedBoundary(t *testing.T) {
	for _, stage := range []processGuardAttachStage{
		processGuardAttachOpenProcess,
		processGuardAttachAssignJob,
	} {
		t.Run(string(stage), func(t *testing.T) {
			cmd := exec.Command("ping.exe", "-n", "30", "127.0.0.1")
			guard, err := prepareProcessGuard(cmd)
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				_ = closeProcessGuard(guard)
				t.Fatal(err)
			}
			attachErr := errors.New("injected internal attach failure")
			err = attachProcessGuardWithProbe(cmd, guard, func(current processGuardAttachStage) error {
				if current == stage {
					return attachErr
				}
				return nil
			})
			if !errors.Is(err, attachErr) {
				t.Fatalf("attachProcessGuardWithProbe() error = %v", err)
			}
			if err := terminateUnattachedProcessTree(cmd, guard); err != nil {
				t.Fatalf("terminateUnattachedProcessTree() error = %v", err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("terminated suspended process exited successfully")
			}
			if err := closeProcessGuard(guard); err != nil {
				t.Fatalf("closeProcessGuard() error = %v", err)
			}
		})
	}
}
