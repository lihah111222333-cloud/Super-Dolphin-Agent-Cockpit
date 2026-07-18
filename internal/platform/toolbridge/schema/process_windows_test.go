//go:build windows

package schema

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestStopAndReapClosesWindowsGuardOnTimeout(t *testing.T) {
	cmd := exec.Command("ping.exe", "-n", "30", "127.0.0.1")
	if err := configureProcess(cmd); err != nil {
		t.Fatalf("configureProcess() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}
	guard, err := attachProcessGuard(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
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
