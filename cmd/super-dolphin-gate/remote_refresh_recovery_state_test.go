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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestParseRemoteBaselineRefreshOptions(t *testing.T) {
	options, err := parseRemoteBaselineRefreshOptions([]string{
		"--config", "/tmp/remote-ci.json",
		"--ledger", "/tmp/duration-ledger.sqlite",
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

func TestAcceptRemoteBaselineReverifyFailureKeepsAcceptedResources(t *testing.T) {
	accepted := remoteBaselineStateFixture()
	stage := remoteBaselineArtifactStage{generation: accepted.Generation + 1, generationPrefix: "baseline-artifacts/4/", createdAt: time.Now().UTC()}
	manifest := remoteBaselineCapacityManifest(3 * remoteDataCacheGiB)
	manifest.Generation = stage.generation
	manifest.MainCommit, manifest.MainTree = repeatRemoteHex("c", 40), repeatRemoteHex("d", 40)
	manifest.Platform, manifest.PolicyDigest = accepted.Platform, accepted.PolicyDigest
	manifest.ToolchainDigest, manifest.RuntimeImage = accepted.ToolchainDigest, accepted.RuntimeImage
	session := remoteBaselineRefreshSession{
		accepted:   accepted,
		input:      remoteBaselineRefreshInput{Identity: remoteci.BaselineIdentity{MainCommit: manifest.MainCommit, MainTree: manifest.MainTree, Platform: manifest.Platform, PolicyDigest: manifest.PolicyDigest, ToolchainDigest: manifest.ToolchainDigest, RuntimeImage: manifest.RuntimeImage}},
		resolveRef: func(string, string, string) (string, error) { return "", errors.New("injected main reverify failure") },
	}
	cache := datacache.DataCache{ID: "edc-candidate", Status: datacache.StatusAvailable, Bucket: accepted.DataCacheBucket, Path: "/super-dolphin/ci/baselines/4", SizeGiB: 20}
	if _, err := acceptRemoteBaseline(session, stage, manifest, "sha256:"+strings.Repeat("f", 64), cache); err == nil {
		t.Fatal("acceptRemoteBaseline() accepted a failed main reverify")
	}
	if accepted.DataCacheID != "edc-anchor" || accepted.RetiredAnchor != nil || len(accepted.RetiredDeltas) != 0 {
		t.Fatalf("failed reverify changed accepted state: %#v", accepted)
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
	root := t.TempDir()
	configPath := filepath.Join(root, "remote-ci.json")
	ledgerPath := filepath.Join(root, "duration-ledger.json")
	if _, err := loadAcceptedRemoteBaseline(configPath, "", ledgerPath); err == nil {
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
