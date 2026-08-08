package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func TestLoadRemoteMaterializeConfigAcceptsNestedRequestKey(t *testing.T) {
	values := map[string]string{
		remoteWorkerRoleEnv:       "worker-role",
		remoteOSSEndpointEnv:      "oss-cn-shenzhen-internal.aliyuncs.com",
		remoteOSSBucketEnv:        "ci-bucket",
		remoteRequestKeyEnv:       "baseline-artifacts/source-bundles/job-1234/shard-00.request.json",
		remoteRequestSHA256Env:    strings.Repeat("a", sha256.Size*2),
		remoteAgentTokenDigestEnv: "sha256:" + strings.Repeat("b", sha256.Size*2),
	}
	config, err := loadRemoteMaterializeConfig(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadRemoteMaterializeConfig() error = %v", err)
	}
	if config.RequestKey != values[remoteRequestKeyEnv] {
		t.Fatalf("remote request key = %q", config.RequestKey)
	}
	if config.AgentTokenDigest != values[remoteAgentTokenDigestEnv] {
		t.Fatalf("remote agent token digest = %q", config.AgentTokenDigest)
	}
}

func TestLoadRemoteMaterializeConfigRejectsMissingAgentTokenDigest(t *testing.T) {
	values := validRemoteMaterializeEnvironment()
	delete(values, remoteAgentTokenDigestEnv)
	_, err := loadRemoteMaterializeConfig(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if err == nil || !strings.Contains(err.Error(), remoteAgentTokenDigestEnv+" is required and canonical") {
		t.Fatalf("loadRemoteMaterializeConfig() error = %v", err)
	}
}

func TestLoadRemoteBootstrapShardRequestRequiresMatchingAgentTokenDigest(t *testing.T) {
	request := validRemoteMaterializeShardRequest(t)
	data, requestSHA256, err := remoteci.EncodeBootstrapShardRequest(request)
	if err != nil {
		t.Fatalf("EncodeBootstrapShardRequest() error = %v", err)
	}
	for name, agentTokenDigest := range map[string]string{
		"matching canonical digest":   request.AgentTokenDigest,
		"mismatched canonical digest": "sha256:" + strings.Repeat("f", sha256.Size*2),
	} {
		t.Run(name, func(t *testing.T) {
			config := remoteMaterializeConfig{
				RequestKey:       "source-bundles/job-123/request.request.json",
				RequestSHA256:    requestSHA256,
				AgentTokenDigest: agentTokenDigest,
			}
			got, loadErr := loadRemoteBootstrapShardRequest(context.Background(), config, func(_ context.Context, key string, _ int64, destination io.Writer) (int64, error) {
				if key != config.RequestKey {
					t.Fatalf("request key = %q, want %q", key, config.RequestKey)
				}
				written, writeErr := destination.Write(data)
				return int64(written), writeErr
			})
			if agentTokenDigest == request.AgentTokenDigest {
				if loadErr != nil {
					t.Fatalf("loadRemoteBootstrapShardRequest() error = %v", loadErr)
				}
				if got.AgentTokenDigest != config.AgentTokenDigest {
					t.Fatalf("request agent token digest = %q, want %q", got.AgentTokenDigest, config.AgentTokenDigest)
				}
				return
			}
			if loadErr == nil || !strings.Contains(loadErr.Error(), "agent token digest does not match init environment") {
				t.Fatalf("loadRemoteBootstrapShardRequest() error = %v", loadErr)
			}
		})
	}
}

func TestLoadRemoteBootstrapShardRequestRejectsObjectDirectoryDrift(t *testing.T) {
	request := validRemoteMaterializeShardRequest(t)
	data, requestSHA256, err := remoteci.EncodeBootstrapShardRequest(request)
	if err != nil {
		t.Fatalf("EncodeBootstrapShardRequest() error = %v", err)
	}
	config := remoteMaterializeConfig{
		RequestKey:       "source-bundles/other-job/request.request.json",
		RequestSHA256:    requestSHA256,
		AgentTokenDigest: request.AgentTokenDigest,
	}
	_, err = loadRemoteBootstrapShardRequest(context.Background(), config, func(_ context.Context, _ string, _ int64, destination io.Writer) (int64, error) {
		written, writeErr := destination.Write(data)
		return int64(written), writeErr
	})
	if err == nil || !strings.Contains(err.Error(), "object directory does not match") {
		t.Fatalf("loadRemoteBootstrapShardRequest() error = %v", err)
	}
}

func TestLoadRemoteBootstrapShardRequestRejectsCurrentV2NestedCompileGroup(t *testing.T) {
	request := validRemoteMaterializeShardRequest(t)
	data, _, err := remoteci.EncodeShardRequest(request)
	if err != nil {
		t.Fatalf("EncodeShardRequest() error = %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["compile_groups"] = []any{map[string]any{
		"group_id": "sha256:" + strings.Repeat("a", 64), "package_target": "./internal/archtest",
		"semantic_key": gate.CompileGroupSemanticGoTestNormal, "shared_input_digest": "sha256:" + strings.Repeat("b", 64),
		"profile_digest": "sha256:" + strings.Repeat("c", 64), "resource_class_id": "small",
		"workload_ids": []any{string(gate.GateIDBackendTestWithGuard)}, "selector_estimates": []any{},
		"compile_estimate_ms": 1, "body_estimate_ms": 1, "estimated_duration_ms": 2,
	}}
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	config := remoteMaterializeConfig{
		RequestKey:       "source-bundles/job-123/request.request.json",
		RequestSHA256:    digestBytes(data),
		AgentTokenDigest: request.AgentTokenDigest,
	}
	if _, err := loadRemoteBootstrapShardRequest(context.Background(), config, func(_ context.Context, _ string, _ int64, destination io.Writer) (int64, error) {
		written, writeErr := destination.Write(data)
		return int64(written), writeErr
	}); err == nil || !strings.Contains(err.Error(), "decode accepted bootstrap shard request") {
		t.Fatalf("loadRemoteBootstrapShardRequest() error = %v", err)
	}
}

func validRemoteMaterializeEnvironment() map[string]string {
	return map[string]string{
		remoteWorkerRoleEnv:       "worker-role",
		remoteOSSEndpointEnv:      "oss-cn-shenzhen-internal.aliyuncs.com",
		remoteOSSBucketEnv:        "ci-bucket",
		remoteRequestKeyEnv:       "source-bundles/job-123/request.request.json",
		remoteRequestSHA256Env:    strings.Repeat("a", sha256.Size*2),
		remoteAgentTokenDigestEnv: "sha256:" + strings.Repeat("b", sha256.Size*2),
	}
}

func validRemoteMaterializeShardRequest(t *testing.T) remoteci.ShardRequest {
	t.Helper()
	const jobID = "job-123"
	tree := strings.Repeat("a", 40)
	toolchain := "sha256:" + strings.Repeat("b", sha256.Size*2)
	image := "registry.example/runtime@sha256:" + strings.Repeat("c", sha256.Size*2)
	prefix := "source-bundles/" + jobID + "/"
	request := remoteci.ShardRequest{
		SchemaVersion:           remoteci.ShardRequestSchemaVersion,
		AgentTokenDigest:        "sha256:" + strings.Repeat("d", sha256.Size*2),
		JobID:                   jobID,
		ShardIdentity:           "sha256:" + strings.Repeat("e", sha256.Size*2),
		Profile:                 gate.ProfileLocalFast,
		PlanDigest:              "sha256:" + strings.Repeat("f", sha256.Size*2),
		BaselineManifest:        "sha256:" + strings.Repeat("0", sha256.Size*2),
		ImageCacheSnapshotID:    "snapshot-123",
		RunnerBaseTree:          tree,
		BaselineRuntimeImage:    image,
		BaselineToolchainDigest: toolchain,
		Source: gate.SourceSpec{
			Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
			Commit: &gate.CommitSource{SHA: strings.Repeat("9", 40)}, SourceTreeSHA: tree,
		},
		SourceTreeSHA:                tree,
		SourceBundleKey:              prefix + "source.bundle",
		SourceBundleSHA256:           strings.Repeat("1", sha256.Size*2),
		SourceBundleSize:             1,
		ManifestKey:                  prefix + "source.manifest.json",
		ManifestSHA256:               strings.Repeat("2", sha256.Size*2),
		CandidateGateSourceSHA256:    "sha256:" + strings.Repeat("3", sha256.Size*2),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("4", sha256.Size*2),
		GateIDs:                      []gate.GateID{gate.GateIDBackendTestWithGuard},
		ResourceClass:                shardresource.Class{ID: "small", VCPU: 2, MemoryGiB: 4},
		OCIProjectCache: &remoteci.BaselineOCIProjectCache{
			Image: image, ContentManifestSHA256: "sha256:" + strings.Repeat("5", sha256.Size*2),
			MainTree: tree, ToolchainDigest: toolchain, Platform: "linux/amd64", CachePath: remoteci.OCIProjectGoBuildCachePath,
		},
	}
	digest, err := request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatalf("compute remote materialize shard manifest digest: %v", err)
	}
	request.ShardExecutionManifestDigest = digest
	return request
}

func TestInstallAcceptedBootstrapManifestPublishesV1BeforeHandoff(t *testing.T) {
	bootstrap := acceptedBootstrapManifestFixture(t)
	root := t.TempDir()
	var chmodPaths []string
	var chownPaths []string
	if err := installAcceptedBootstrapManifestWithOwnership(root, bootstrap,
		func(path string, _ os.FileMode) error {
			chmodPaths = append(chmodPaths, path)
			return nil
		},
		func(path string, _, _ int) error {
			chownPaths = append(chownPaths, path)
			return nil
		},
	); err != nil {
		t.Fatalf("installAcceptedBootstrapManifestWithOwnership() error = %v", err)
	}
	assertAcceptedBootstrapManifestFixture(t, root, bootstrap, chmodPaths, chownPaths)
}

func acceptedBootstrapManifestFixture(t *testing.T) remoteci.BootstrapShardRequest {
	t.Helper()
	request := validRemoteMaterializeShardRequest(t)
	data, _, err := remoteci.EncodeBootstrapShardRequest(request)
	if err != nil {
		t.Fatalf("EncodeBootstrapShardRequest() error = %v", err)
	}
	bootstrap, err := remoteci.DecodeBootstrapShardRequest(data)
	if err != nil {
		t.Fatalf("DecodeBootstrapShardRequest() error = %v", err)
	}
	return bootstrap
}

func assertAcceptedBootstrapManifestFixture(t *testing.T, root string, bootstrap remoteci.BootstrapShardRequest, chmodPaths, chownPaths []string) {
	t.Helper()
	manifestPath := filepath.Join(root, filepath.Base(gate.ExecutorShardExecutionManifestPath))
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteci.ValidateAcceptedBootstrapManifestBytes(manifestData, bootstrap.ShardExecutionManifestDigest); err != nil {
		t.Fatalf("ValidateAcceptedBootstrapManifestBytes() error = %v", err)
	}
	if len(chmodPaths) != 2 || len(chownPaths) != 2 || chmodPaths[0] != manifestPath || chmodPaths[1] != root || chownPaths[0] != manifestPath || chownPaths[1] != root {
		t.Fatalf("handoff ownership calls chmod=%v chown=%v", chmodPaths, chownPaths)
	}
}

func TestDownloadVerifiedFileCleansFailedStagingFile(t *testing.T) {
	root := t.TempDir()
	objectPath := filepath.Join(root, "source.bundle")
	expected := digestBytes([]byte("expected"))
	err := downloadVerifiedFile(context.Background(), func(context.Context, string, int64, io.Writer) (int64, error) {
		return 0, errors.New("temporary OSS failure")
	}, "source.bundle", expected, 1024, objectPath)
	if err == nil || !strings.Contains(err.Error(), "temporary OSS failure") {
		t.Fatalf("downloadVerifiedFile() error = %v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed download left staging files: %v", entries)
	}
}

func TestVerifyRemoteMaterializedGateCLICompileClosureRejectsTransitiveSourceDrift(t *testing.T) {
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runRemoteMaterializeGit(t, repository, "init")
	runRemoteMaterializeGit(t, repository, "config", "user.email", "test@example.invalid")
	runRemoteMaterializeGit(t, repository, "config", "user.name", "Remote Materialize Test")
	for name, source := range map[string]string{
		"go.mod":                         "module example.invalid/gate\n\ngo 1.24.0\n",
		"go.sum":                         "example.invalid/dependency v1.0.0 h1:abc\n",
		"build/gate/toolchain.lock":      "go=1.24.0\n",
		"cmd/super-dolphin-gate/main.go": "package main\n\nimport \"example.invalid/gate/internal/dep\"\n\nfunc main() { dep.Run() }\n",
		"internal/dep/dep.go":            "package dep\n\nfunc Run() { println(\"base\") }\n",
	} {
		filePath := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runRemoteMaterializeGit(t, repository, "add", ".")
	runRemoteMaterializeGit(t, repository, "commit", "-m", "base")
	baseTree := strings.TrimSpace(runRemoteMaterializeGit(t, repository, "rev-parse", "HEAD^{tree}"))
	sourceDigest, toolchainDigest, _, err := remoteci.LoadGateCLICompileClosure(context.Background(), repository, baseTree)
	if err != nil {
		t.Fatalf("load base compile closure: %v", err)
	}
	request := remoteci.ShardRequest{SourceTreeSHA: baseTree, CandidateGateSourceSHA256: sourceDigest, CandidateGateToolchainSHA256: toolchainDigest}
	if err := verifyRemoteMaterializedGateCLICompileClosure(context.Background(), repository, request); err != nil {
		t.Fatalf("verify base compile closure: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "internal/dep/dep.go"), []byte("package dep\n\nfunc Run() { println(\"changed\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRemoteMaterializeGit(t, repository, "add", ".")
	runRemoteMaterializeGit(t, repository, "commit", "-m", "imported source drift")
	request.SourceTreeSHA = strings.TrimSpace(runRemoteMaterializeGit(t, repository, "rev-parse", "HEAD^{tree}"))
	if err := verifyRemoteMaterializedGateCLICompileClosure(context.Background(), repository, request); err == nil || !strings.Contains(err.Error(), "does not match shard request") {
		t.Fatalf("transitive source drift error = %v", err)
	}
}

func TestVerifyRemoteMaterializedSourceUsesAcceptedBaselineAndChecksTransportTree(t *testing.T) {
	fixture := newRemoteSourceBaselineFixture(t)
	if err := verifyRemoteMaterializedSourceAtBaselineRoot(
		context.Background(), fixture.sourceRoot, fixture.artifactRoot, fixture.request, fixture.baselineRoot,
	); err != nil {
		t.Fatalf("verifyRemoteMaterializedSourceAtBaselineRoot() error = %v", err)
	}
	if got := strings.TrimSpace(runRemoteMaterializeGit(t, fixture.sourceRoot, "rev-parse", "HEAD^{tree}")); got != fixture.request.SourceTreeSHA {
		t.Fatalf("materialized source tree = %s, want %s", got, fixture.request.SourceTreeSHA)
	}
	syntheticBase := strings.TrimSpace(runRemoteMaterializeGit(t, fixture.sourceRoot, "rev-parse", "refs/source/base^{commit}"))
	expectedTransport, err := remoteci.DeterministicSourceTransportCommitSHA(
		fixture.request.SourceTreeSHA,
		syntheticBase,
		fixture.request.Source.ObjectFormat,
	)
	if err != nil {
		t.Fatalf("DeterministicSourceTransportCommitSHA() error = %v", err)
	}
	if got := strings.TrimSpace(runRemoteMaterializeGit(t, fixture.sourceRoot, "rev-parse", "HEAD")); got != expectedTransport {
		t.Fatalf("materialized source commit = %s, want %s", got, expectedTransport)
	}
}

func TestVerifyRemoteSourceManifestCommitBindingRejectsCommitDrift(t *testing.T) {
	fixture := newRemoteSourceBaselineFixture(t)
	syntheticBase, err := remoteci.DeterministicSourceSyntheticBaseCommitSHA(
		fixture.baseline.TreeSHA,
		fixture.baseline.CommitSHA,
		fixture.baseline.ObjectFormat,
	)
	if err != nil {
		t.Fatalf("derive synthetic base commit: %v", err)
	}
	for _, test := range []struct {
		name     string
		manifest remoteci.SourceMaterializationManifest
		wantErr  string
	}{
		{
			name: "synthetic base",
			manifest: remoteci.SourceMaterializationManifest{
				SyntheticBaseTreeSHA:   fixture.baseline.TreeSHA,
				SyntheticBaseCommitSHA: strings.Repeat("0", 40),
			},
			wantErr: "synthetic base commit",
		},
		{
			name: "transport",
			manifest: remoteci.SourceMaterializationManifest{
				SyntheticBaseTreeSHA:   fixture.baseline.TreeSHA,
				SyntheticBaseCommitSHA: syntheticBase,
				TransportCommitSHA:     strings.Repeat("0", 40),
			},
			wantErr: "transport commit",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyRemoteSourceManifestCommitBinding(fixture.request, test.manifest, fixture.baseline)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("verifyRemoteSourceManifestCommitBinding() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestVerifyRemoteMaterializedSourceRejectsInvalidAcceptedBaseline(t *testing.T) {
	for _, test := range []struct {
		name string
		root func(t *testing.T, fixture remoteSourceBaselineFixture) string
	}{
		{name: "missing", root: func(t *testing.T, fixture remoteSourceBaselineFixture) string {
			return filepath.Join(t.TempDir(), "missing-baseline.git")
		}},
		{name: "tree drift", root: func(t *testing.T, fixture remoteSourceBaselineFixture) string {
			candidateTree := strings.TrimSpace(runRemoteMaterializeGit(t, fixture.repositoryRoot, "rev-parse", "HEAD^{tree}"))
			root := canonicalRemoteMaterializeDir(t)
			if _, err := remoteci.BuildSourceBaseline(context.Background(), fixture.repositoryRoot, candidateTree, root, gate.GitObjectFormatSHA1); err != nil {
				t.Fatalf("BuildSourceBaseline() drift fixture error = %v", err)
			}
			return root
		}},
		{name: "writable", root: func(t *testing.T, fixture remoteSourceBaselineFixture) string {
			if err := os.Chmod(filepath.Join(fixture.baselineRoot, "HEAD"), 0o644); err != nil {
				t.Fatalf("chmod accepted baseline: %v", err)
			}
			return fixture.baselineRoot
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRemoteSourceBaselineFixture(t)
			baselineRoot := test.root(t, fixture)
			err := verifyRemoteMaterializedSourceAtBaselineRoot(
				context.Background(), fixture.sourceRoot, fixture.artifactRoot, fixture.request, baselineRoot,
			)
			if err == nil {
				t.Fatal("verifyRemoteMaterializedSourceAtBaselineRoot() unexpectedly passed invalid baseline")
			}
		})
	}
}

func TestVerifyRemoteMaterializedSourceRejectsSourceSpecRequestDrift(t *testing.T) {
	fixture := newRemoteSourceBaselineFixture(t)
	fixture.request.Source.SourceTreeSHA = fixture.baseline.TreeSHA
	if err := verifyRemoteMaterializedSourceAtBaselineRoot(
		context.Background(), fixture.sourceRoot, fixture.artifactRoot, fixture.request, fixture.baselineRoot,
	); err == nil || !strings.Contains(err.Error(), "SourceSpec and source tree identity") {
		t.Fatalf("SourceSpec/request drift error = %v", err)
	}
}

type remoteSourceBaselineFixture struct {
	repositoryRoot string
	baselineRoot   string
	artifactRoot   string
	sourceRoot     string
	baseline       remoteci.SourceBaseline
	request        remoteci.ShardRequest
}

func newRemoteSourceBaselineFixture(t *testing.T) remoteSourceBaselineFixture {
	t.Helper()
	repository := canonicalRemoteMaterializeDir(t)
	runRemoteMaterializeGit(t, repository, "init", "--quiet", "--initial-branch=main")
	runRemoteMaterializeGit(t, repository, "config", "user.email", "remote-materialize@example.invalid")
	runRemoteMaterializeGit(t, repository, "config", "user.name", "Remote Materialize Fixture")
	for name, source := range map[string]string{
		"go.mod":                         "module example.invalid/gate\n\ngo 1.24.0\n",
		"go.sum":                         "example.invalid/dependency v1.0.0 h1:abc\n",
		"build/gate/toolchain.lock":      "go=1.24.0\n",
		"cmd/super-dolphin-gate/main.go": "package main\n\nimport \"example.invalid/gate/internal/dep\"\n\nfunc main() { dep.Run() }\n",
		"internal/dep/dep.go":            "package dep\n\nfunc Run() { println(\"base\") }\n",
	} {
		filePath := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(filePath, []byte(source), 0o644); err != nil {
			t.Fatalf("write fixture source: %v", err)
		}
	}
	runRemoteMaterializeGit(t, repository, "add", ".")
	runRemoteMaterializeGit(t, repository, "commit", "--quiet", "-m", "baseline")
	baselineTree := strings.TrimSpace(runRemoteMaterializeGit(t, repository, "rev-parse", "HEAD^{tree}"))
	baselineRoot := canonicalRemoteMaterializeDir(t)
	baseline, err := remoteci.BuildSourceBaseline(context.Background(), repository, baselineTree, baselineRoot, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("BuildSourceBaseline() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "internal/dep/dep.go"), []byte("package dep\n\nfunc Run() { println(\"candidate\") }\n"), 0o644); err != nil {
		t.Fatalf("write candidate source: %v", err)
	}
	runRemoteMaterializeGit(t, repository, "add", ".")
	candidateTree := strings.TrimSpace(runRemoteMaterializeGit(t, repository, "write-tree"))
	candidateCommit := createRemoteMaterializeCommit(t, repository, candidateTree, baseline)
	runRemoteMaterializeGit(t, repository, "update-ref", "refs/heads/main", candidateCommit)
	sourceTree := strings.TrimSpace(runRemoteMaterializeGit(t, repository, "rev-parse", "HEAD^{tree}"))
	sourceDigest, toolchainDigest, _, err := remoteci.LoadGateCLICompileClosure(context.Background(), repository, sourceTree)
	if err != nil {
		t.Fatalf("load fixture compile closure: %v", err)
	}
	spec := gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: candidateCommit}, SourceTreeSHA: sourceTree,
	}
	artifactRoot := canonicalRemoteMaterializeDir(t)
	if _, err := remoteci.MaterializeSource(context.Background(), repository, spec, artifactRoot, baseline); err != nil {
		t.Fatalf("MaterializeSource() error = %v", err)
	}
	request := validRemoteMaterializeShardRequest(t)
	request.RunnerBaseTree = baselineTree
	request.Source = spec
	request.SourceTreeSHA = sourceTree
	request.CandidateGateSourceSHA256 = sourceDigest
	request.CandidateGateToolchainSHA256 = toolchainDigest
	return remoteSourceBaselineFixture{
		repositoryRoot: repository,
		baselineRoot:   baselineRoot,
		artifactRoot:   artifactRoot,
		sourceRoot:     canonicalRemoteMaterializeDir(t),
		baseline:       baseline,
		request:        request,
	}
}

func createRemoteMaterializeCommit(t *testing.T, repository string, tree string, baseline remoteci.SourceBaseline) string {
	t.Helper()
	command := exec.Command("git", "-C", repository, "commit-tree", tree, "-p", baseline.CommitSHA, "-m", "candidate")
	command.Env = append(os.Environ(), "GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(baseline.RepositoryRoot, "objects"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit-tree: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func canonicalRemoteMaterializeDir(t *testing.T) string {
	t.Helper()
	dirPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize fixture directory: %v", err)
	}
	if err := os.Chmod(dirPath, 0o700); err != nil {
		t.Fatalf("protect fixture directory: %v", err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(dirPath, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return os.Chmod(path, 0o600)
		})
	})
	return dirPath
}

func runRemoteMaterializeGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
