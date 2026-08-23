//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedGateCommandCancellationKillsInheritedPipeProcess(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fork")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/sleep 2 &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runCommandWithEnvironmentContext(ctx, t.TempDir(), script, []string{"PATH=/usr/bin:/bin"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("bounded owned command error=%v elapsed=%v", err, time.Since(started))
	}
}

func TestTrustedOwnedGateToolsIgnoreAmbientPath(t *testing.T) {
	fakeRoot := t.TempDir()
	for _, name := range []string{"go", "git", "make"} {
		if err := os.WriteFile(filepath.Join(fakeRoot, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fakeRoot)
	plan := gatePlan{executionRoot: t.TempDir()}
	for _, test := range []struct {
		tool      string
		arguments []string
	}{
		{tool: "go", arguments: []string{"version"}},
		{tool: "git", arguments: []string{"--version"}},
		{tool: "make", arguments: []string{"--version"}},
	} {
		if err := runTrustedOwnedGateCommand(plan, test.tool, test.arguments...); err != nil {
			t.Fatalf("trusted %s: %v", test.tool, err)
		}
	}
}
