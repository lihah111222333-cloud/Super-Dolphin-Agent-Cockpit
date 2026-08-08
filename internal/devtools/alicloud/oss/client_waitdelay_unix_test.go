//go:build !windows

package oss

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestExecRunner_CapturesStderrAndWrapsExitError(t *testing.T) {
	_, stderr, err := (execRunner{}).Run(context.Background(), "sh", "-c", "echo runner-stderr >&2; exit 7")
	if err == nil || string(stderr) != "runner-stderr\n" {
		t.Fatalf("Run() stderr=%q error=%v, want captured stderr", stderr, err)
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("Run() error = %T, want wrapped *exec.ExitError", err)
	}
}

// TestExecRunnerBoundsInheritedOutputPipeWait 验证 CLI 后代持有输出管道时仍会按看门狗有界返回。
func TestExecRunnerBoundsInheritedOutputPipeWait(t *testing.T) {
	startedAt := time.Now()
	_, _, err := (execRunner{}).Run(context.Background(), "sh", "-c", "sleep 5 &")
	if err == nil || !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("Run() error = %v, want exec.ErrWaitDelay", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 2*cliProcessWaitDelay {
		t.Fatalf("Run() elapsed = %s, want bounded inherited-pipe wait", elapsed)
	}
}
