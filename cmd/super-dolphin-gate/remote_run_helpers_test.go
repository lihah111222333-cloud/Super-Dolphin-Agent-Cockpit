package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func validRemoteRunConfigJSON() string {
	return `{
  "schema_version": 4,
  "aliyun_cli": "aliyun",
  "credential_profile": "super-dolphin-ci",
  "region_id": "cn-shenzhen",
  "vswitch_id": "vsw-test",
  "security_group_id": "sg-test",
  "worker_role_name": "super-dolphin-ci-worker",
  "oss": {"bucket": "super-dolphin-ci-test", "endpoint": "https://oss-cn-shenzhen.aliyuncs.com", "internal_endpoint": "https://oss-cn-shenzhen-internal.aliyuncs.com", "source_prefix": "source-deltas/", "baseline_prefix": "baseline-artifacts/"},
  "runtime": {"image": "registry.example/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "data_cache": {"bucket": "super-dolphin-ci", "path_prefix": "/super-dolphin/ci/baselines", "max_size_gib": 100, "retention_days": 2, "refresh_interval_minutes": 1440},
  "capacity": {"max_shards_per_job": 5, "seed_class": "memory", "resource_policy": {"classes": [{"id": "small", "vcpu": 2, "memory_gib": 4}, {"id": "standard", "vcpu": 4, "memory_gib": 8}, {"id": "memory", "vcpu": 4, "memory_gib": 16}, {"id": "maximum", "vcpu": 8, "memory_gib": 32}], "bootstrap": {"guard": "small", "node_test": "standard", "go_test": "memory"}, "headroom_percent": 25, "min_samples_to_downsize": 5}}
}`
}

func remoteRunBaselineState(t *testing.T, repository string) remoteci.BaselineState {
	t.Helper()
	commit := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^")
	tree := remoteRunGitOutput(t, repository, "rev-parse", commit+"^{tree}")
	created := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	state := remoteRunRunnerIdentityState()
	state.SchemaVersion, state.Generation = remoteci.BaselineStateSchemaVersion, 1
	state.MainCommit, state.MainTree = commit, tree
	state.DataCacheID, state.DataCacheBucket = "edc-baseline1", "super-dolphin-ci"
	state.DataCachePath, state.DataCacheSizeGiB = "/super-dolphin/ci/baselines/1", 20
	state.SourceObjectPrefix = "baseline-artifacts/1/"
	state.CreatedAt, state.AcceptedAt = created, created.Add(time.Minute)
	state.Anchor = remoteci.BaselineCacheRef{Generation: state.Generation, Kind: remoteci.BaselineCacheKindAnchor, ManifestDigest: state.BaselineManifestDigest, MainCommit: state.MainCommit, MainTree: state.MainTree, DataCacheID: state.DataCacheID, DataCacheBucket: state.DataCacheBucket, DataCachePath: state.DataCachePath, SizeGiB: state.DataCacheSizeGiB, SourceObjectPrefix: state.SourceObjectPrefix, AcceptedAt: state.AcceptedAt}
	return state
}

func remoteRunRunnerIdentityState() remoteci.BaselineState {
	return remoteci.BaselineState{Platform: "linux/arm64", PolicyDigest: "sha256:" + strings.Repeat("b", 64), ToolchainDigest: "sha256:" + strings.Repeat("c", 64), RuntimeImage: "registry.example/runtime@sha256:" + strings.Repeat("a", 64), GateBinarySHA256: "sha256:" + strings.Repeat("e", 64), RuntimeSeedSHA256: "sha256:" + strings.Repeat("1", 64), BaselineManifestDigest: "sha256:" + strings.Repeat("d", 64)}
}

func remoteRunRunnerIdentity(state remoteci.BaselineState) string {
	return remoteRunnerIdentityDigest(state, "sha256:"+strings.Repeat("f", 64))
}

func writeRemoteRunConfigFixture(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote-ci.json")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write remote CI config fixture: %v", err)
	}
	return path
}

func initRemoteRunGitFixture(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runRemoteRunGit(t, repository, "init", "--quiet", "-b", "main")
	runRemoteRunGit(t, repository, "config", "user.name", "Remote CI Test")
	runRemoteRunGit(t, repository, "config", "user.email", "remote-ci@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module example.com/remote-run-fixture\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatalf("write Git fixture go.mod: %v", err)
	}
	for index, contents := range []string{"base\n", "head\n"} {
		path := filepath.Join(repository, "fixture.txt")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write Git fixture %d: %v", index, err)
		}
		runRemoteRunGit(t, repository, "add", "fixture.txt", "go.mod")
		runRemoteRunGit(t, repository, "commit", "--quiet", "-m", "测试提交")
	}
	return repository
}

func runRemoteRunGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func remoteRunGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}
