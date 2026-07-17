package main

import (
	"context"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRunAcceptsOnlyCanonicalGateInvocation(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"run", "--gate"},
		{"run", "--gate", "unknown:gate"},
		{"run", "--gate", string(gate.GateIDCodemapCheck), "extra"},
	} {
		if err := run(context.Background(), args); err == nil {
			t.Fatalf("run unexpectedly accepted arguments %q", args)
		}
	}
}

func TestRunCanonicalInvocationEntersProductionPreflight(t *testing.T) {
	err := run(context.Background(), []string{"run", "--gate", string(gate.GateIDWhitespaceCheck)})
	if err == nil {
		t.Fatal("canonical invocation unexpectedly bypassed production preflight")
	}
	if strings.Contains(err.Error(), "usage:") || strings.Contains(err.Error(), "unknown gate") {
		t.Fatalf("canonical invocation did not enter executor preflight: %v", err)
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
