package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestWorkerCLIRejectsNonCanonicalInvocation(t *testing.T) {
	t.Setenv(gate.ExecutorWorkloadTimeoutEnvironment, (10 * time.Minute).String())
	for _, args := range [][]string{
		nil,
		{"run", "--gate"},
		{"run", "--gate", "unknown:gate"},
		{"run", "--gate", string(gate.GateIDCodemapCheck), "extra"},
		{"race-package-patterns"},
		{"validate-go-distribution"},
	} {
		stderr := &bytes.Buffer{}
		if code := runWorkerCLI(args, &bytes.Buffer{}, stderr); code == 0 {
			t.Fatalf("worker unexpectedly accepted arguments %q", args)
		}
		if !strings.Contains(stderr.String(), "super-dolphin-gate:") {
			t.Fatalf("worker stderr = %q, want CLI error prefix", stderr.String())
		}
	}
}

func TestWorkerCanonicalInvocationEntersProductionPreflight(t *testing.T) {
	t.Setenv(gate.ExecutorWorkloadTimeoutEnvironment, (10 * time.Minute).String())
	stderr := &bytes.Buffer{}
	code := runWorkerCLI(
		[]string{"run", "--gate", string(gate.GateIDWhitespaceCheck)},
		&bytes.Buffer{},
		stderr,
	)
	if code == 0 {
		t.Fatal("canonical invocation unexpectedly bypassed production preflight")
	}
	if strings.Contains(stderr.String(), "usage:") || strings.Contains(stderr.String(), "unknown gate") {
		t.Fatalf("canonical invocation did not enter executor preflight: %s", stderr.String())
	}
}

func TestRunCLIDispatchesWorkerNamespace(t *testing.T) {
	t.Setenv(gate.ExecutorWorkloadTimeoutEnvironment, (10 * time.Minute).String())
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runCLI([]string{"worker", "run", "--gate"}, stdout, stderr)
	if code == 0 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("code=%d stderr=%q, want worker protocol failure", code, stderr.String())
	}
}

func TestWorkerCLIIdentityRequiresLinkedCompileIdentity(t *testing.T) {
	previousSource, previousToolchain := gateSourceDigest, gateToolchainDigest
	t.Cleanup(func() { gateSourceDigest, gateToolchainDigest = previousSource, previousToolchain })

	gateSourceDigest, gateToolchainDigest = "", ""
	stderr := &bytes.Buffer{}
	if code := runWorkerCLI([]string{"cli-identity"}, &bytes.Buffer{}, stderr); code != int(gate.ExitInfrastructure) ||
		!strings.Contains(stderr.String(), "build identity is not linked") {
		t.Fatalf("unlinked identity code=%d stderr=%q", code, stderr.String())
	}

	gateSourceDigest = "sha256:" + strings.Repeat("a", 64)
	gateToolchainDigest = "sha256:" + strings.Repeat("b", 64)
	stdout := &bytes.Buffer{}
	if code := runWorkerCLI([]string{"cli-identity"}, stdout, &bytes.Buffer{}); code != int(gate.ExitOK) {
		t.Fatalf("linked identity code=%d", code)
	}
	want := "gate_source_sha256=" + gateSourceDigest + "\nplatform=" + runtime.GOOS + "/" + runtime.GOARCH + "\ntoolchain_digest=" + gateToolchainDigest + "\n"
	if stdout.String() != want {
		t.Fatalf("identity output = %q, want %q", stdout.String(), want)
	}
	if code := runWorkerCLI([]string{"cli-identity", "extra"}, &bytes.Buffer{}, &bytes.Buffer{}); code != int(gate.ExitProtocol) {
		t.Fatalf("identity accepted extra arguments with code %d", code)
	}
}

func TestSignalExitCode(t *testing.T) {
	if code := signalExitCode(syscall.SIGTERM); code != 143 {
		t.Fatalf("SIGTERM exit code = %d, want 143", code)
	}
	if code := signalExitCode(syscall.SIGINT); code != 130 {
		t.Fatalf("SIGINT exit code = %d, want 130", code)
	}
}

func TestWorkerExecutionContextReservesSetupBeforeWorkloadDeadline(t *testing.T) {
	t.Setenv(gate.ExecutorWorkloadTimeoutEnvironment, (10 * time.Minute).String())
	started := time.Now()
	ctx, cancel, err := workerExecutionContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	deadline, ok := ctx.Deadline()
	total := 10*time.Minute + remoteWorkerSetupAllowance
	if !ok || deadline.Before(started.Add(total-time.Second)) ||
		deadline.After(started.Add(total+time.Second)) {
		t.Fatalf("worker deadline = %v, ok=%t", deadline, ok)
	}
}

func TestWorkerExecutionContextRejectsUnregisteredTimeout(t *testing.T) {
	t.Setenv(gate.ExecutorWorkloadTimeoutEnvironment, "101s")
	ctx, cancel, err := workerExecutionContext(context.Background())
	defer cancel()
	if err == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("workerExecutionContext() context=%v error=%v", ctx.Err(), err)
	}
}

func TestWorkerExecutionContextRejectsMissingTimeout(t *testing.T) {
	t.Setenv(gate.ExecutorWorkloadTimeoutEnvironment, "")
	if err := os.Unsetenv(gate.ExecutorWorkloadTimeoutEnvironment); err != nil {
		t.Fatal(err)
	}
	ctx, cancel, err := workerExecutionContext(context.Background())
	defer cancel()
	if err == nil || !errors.Is(ctx.Err(), context.Canceled) || !strings.Contains(err.Error(), gate.ExecutorWorkloadTimeoutEnvironment) {
		t.Fatalf("workerExecutionContext() context=%v error=%v, want canceled context and missing-timeout error", ctx.Err(), err)
	}
}
