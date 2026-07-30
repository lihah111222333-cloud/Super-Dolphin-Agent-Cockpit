package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRemoteBaselineRefreshOptions(t *testing.T) {
	options, err := parseRemoteBaselineRefreshOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--repository", "/tmp/repository",
		"--remote", "upstream",
		"--ref", "refs/heads/main",
		"--platform", "linux/amd64",
	})
	if err != nil {
		t.Fatalf("parseRemoteBaselineRefreshOptions() error = %v", err)
	}
	if options.Remote != "upstream" || options.Ref != "refs/heads/main" ||
		options.Platform != "linux/amd64" {
		t.Fatalf("parseRemoteBaselineRefreshOptions() = %#v", options)
	}
}

func TestRemoteBaselineStateRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline-state.json")
	state := remoteBaselineStateFixture()
	if err := writeRemoteBaselineState(statePath, state); err != nil {
		t.Fatalf("writeRemoteBaselineState() error = %v", err)
	}
	loaded, err := loadRemoteBaselineState(statePath, false)
	if err != nil {
		t.Fatalf("loadRemoteBaselineState() error = %v", err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatalf("loaded.Validate() error = %v", err)
	}
	if loaded.Generation != state.Generation || loaded.DataCacheID != state.DataCacheID ||
		loaded.PreviousAnchor == nil || state.PreviousAnchor == nil ||
		loaded.PreviousAnchor.DataCacheID != state.PreviousAnchor.DataCacheID {
		t.Fatalf("loaded state = %#v, want %#v", loaded, state)
	}
}
func TestRemoteBaselineRefreshLockSerializesWorktrees(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline-state.json")
	first, err := acquireRemoteBaselineRefreshLock(context.Background(), statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	waitCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := acquireRemoteBaselineRefreshLock(waitCtx, statePath); err == nil {
		t.Fatal("second refresh lock unexpectedly succeeded")
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireRemoteBaselineRefreshLock(context.Background(), statePath)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if err := second.close(); err != nil {
		t.Fatal(err)
	}
}
func TestLoadAcceptedRemoteBaselineRequiresStateLedger(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "remote-ci.json")
	if _, err := loadAcceptedRemoteBaseline(configPath, ""); err == nil {
		t.Fatal("loadAcceptedRemoteBaseline() unexpectedly accepted missing state")
	}
}
func TestRemoteBaselineRunLoggedPreservesFailureStatus(t *testing.T) {
	const functionStart = "run_logged() {"
	const functionEnd = "\n}\n\n# build_python_runtime"
	start := strings.Index(remoteBaselineSeedScript, functionStart)
	if start < 0 {
		t.Fatal("seed script is missing run_logged")
	}
	relativeEnd := strings.Index(remoteBaselineSeedScript[start:], functionEnd)
	if relativeEnd < 0 {
		t.Fatal("seed script run_logged terminator is missing")
	}
	runLogged := remoteBaselineSeedScript[start : start+relativeEnd+len("\n}")]
	harness := "#!/bin/sh\nset -eu\nstage=$1\n" + runLogged +
		"\nrun_logged forced-failure sh -c 'exit 23'\n"
	path := filepath.Join(t.TempDir(), "run-logged.sh")
	if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("sh", path, t.TempDir()).CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("run_logged exit = %v, want 23; output:\n%s", err, output)
	}
	if !strings.Contains(string(output), "seed stage failed: forced-failure") {
		t.Fatalf("run_logged output is missing failure evidence:\n%s", output)
	}
}
