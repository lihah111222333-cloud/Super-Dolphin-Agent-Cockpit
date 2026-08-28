//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigureRunnerCommandKillsInheritedPipeProcess(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fork")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n/bin/sleep 2 &\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, script)
	if err := configureRunnerCommand(command, []string{"PATH=/usr/bin:/bin"}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := runRunnerCommand(ctx, command)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("bounded runner command error=%v elapsed=%v", err, time.Since(started))
	}
}
