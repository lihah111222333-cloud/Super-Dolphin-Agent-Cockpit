package remotecicli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestLoadAndBindBuilderSimulationRequestRealGitRepositories(t *testing.T) {
	t.Parallel()

	engineRoot, engineCommit, engineTree := builderSimulationTestRepository(t, "engine", []byte("package engine\n"))
	manifest := builderSimulationTestManifest(t, "target-repository")
	targetRoot, _, _ := builderSimulationTestRepository(t, "target", manifest)
	manifestPath := filepath.Join(targetRoot, ".super-dolphin", "remote-ci-workloads.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, targetRoot, "add", ".super-dolphin/remote-ci-workloads.json")
	runGitFixture(t, targetRoot, "commit", "-m", "测试 builder simulation manifest")
	targetCommit := runGitFixture(t, targetRoot, "rev-parse", "HEAD")
	targetTree := runGitFixture(t, targetRoot, "rev-parse", "HEAD^{tree}")

	request := builderSimulationTestRequest(t, engineRoot, engineCommit, engineTree, targetRoot, targetCommit, targetTree, manifest)
	requestPath := filepath.Join(t.TempDir(), "builder-simulation-request.json")
	builderSimulationWriteRequest(t, requestPath, request)

	bound, err := LoadAndBindBuilderSimulationRequest(context.Background(), requestPath)
	if err != nil {
		t.Fatalf("LoadAndBindBuilderSimulationRequest() error = %v", err)
	}
	if bound.Request.Engine.RepositoryRoot != engineRoot || bound.Request.Target.RepositoryRoot != targetRoot {
		t.Fatalf("bound roots = %#v, want engine=%q target=%q", bound.Request.Engine.RepositoryRoot, engineRoot, targetRoot)
	}
	if !isDigest(bound.RequestDigest) || !isDigest(bound.EngineBindingDigest) || !isDigest(bound.TargetBindingDigest) {
		t.Fatalf("bound digests = %#v", bound)
	}
}

func TestLoadAndBindBuilderSimulationRequestRejectsUnsafeRequestPaths(t *testing.T) {
	t.Parallel()

	request, requestPath := builderSimulationTestRequestFile(t)
	secret := request.ECI.CredentialProfile
	requestBytes, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	otherDir := t.TempDir()
	parentLink := filepath.Join(otherDir, "parent-link")
	if err := os.Symlink(filepath.Dir(requestPath), parentLink); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	finalLink := filepath.Join(otherDir, "final-link.json")
	if err := os.Symlink(requestPath, finalLink); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	nonCanonical := filepath.Dir(requestPath) + string(os.PathSeparator) + "." + string(os.PathSeparator) + filepath.Base(requestPath)
	cases := map[string]string{
		"relative":       filepath.Base(requestPath),
		"noncanonical":   nonCanonical,
		"final symlink":  finalLink,
		"parent symlink": filepath.Join(parentLink, filepath.Base(requestPath)),
		"directory":      t.TempDir(),
		"empty":          "",
		"missing":        filepath.Join(filepath.Dir(requestPath), "missing-request.json"),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadAndBindBuilderSimulationRequest(context.Background(), path)
			if err == nil {
				t.Fatalf("LoadAndBindBuilderSimulationRequest(%q) unexpectedly succeeded", path)
			}
			assertBuilderSimulationLoaderErrorRedacted(t, err, path, secret, string(requestBytes))
		})
	}
}

func TestLoadAndBindBuilderSimulationRequestRejectsOversizedAndStrictJSON(t *testing.T) {
	t.Parallel()

	request, _ := builderSimulationTestRequestFile(t)
	canonical := mustBuilderSimulationTestJSON(t, request)
	cases := map[string][]byte{
		"unknown field": append(append([]byte(nil), canonical[:len(canonical)-1]...), []byte(`,"unknown":1}`)...),
		"malformed":     []byte(`{"schema_version":"remote-ci-builder-simulation-request/v1","secret":"not-for-errors"}`),
		"oversized":     append([]byte("{"), make([]byte, descriptorMaxBytes+1)...),
	}
	cases["trailing"] = append(append([]byte(nil), canonical...), []byte("{}")...)
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadAndBindBuilderSimulationRequest(context.Background(), path)
			if err == nil {
				t.Fatalf("loader accepted %s", name)
			}
			assertBuilderSimulationLoaderErrorRedacted(t, err, path, request.ECI.CredentialProfile, string(payload))
		})
	}
}

func TestLoadAndBindBuilderSimulationRequestContextIsTypedAndFailFast(t *testing.T) {
	t.Parallel()

	_, requestPath := builderSimulationTestRequestFile(t)
	var nilContext context.Context
	if _, err := LoadAndBindBuilderSimulationRequest(nilContext, requestPath); err == nil {
		t.Fatal("loader accepted a nil context")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadAndBindBuilderSimulationRequest(canceled, requestPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled loader error = %v, want context.Canceled", err)
	}
	deadline, cancel := context.WithDeadline(context.Background(), immediatePast())
	defer cancel()
	if _, err := LoadAndBindBuilderSimulationRequest(deadline, requestPath); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline loader error = %v, want context.DeadlineExceeded", err)
	}
}

func TestGitObjectResolverResolvesCanonicalObjectFormat(t *testing.T) {
	t.Parallel()

	root, _, _ := builderSimulationTestRepository(t, "object-format", []byte("package objectformat\n"))
	format, err := (gitObjectResolver{}).ResolveObjectFormat(context.Background(), root)
	if err != nil {
		t.Fatalf("ResolveObjectFormat() error = %v", err)
	}
	if format != "sha1" && format != "sha256" {
		t.Fatalf("ResolveObjectFormat() = %q, want sha1 or sha256", format)
	}
}

func TestLoadAndBindBuilderSimulationRequestRejectsWrongBoundObjects(t *testing.T) {
	t.Parallel()

	request, _ := builderSimulationTestRequestFile(t)
	cases := map[string]func(*remoteci.BuilderSimulationRequest){
		"engine object format": func(value *remoteci.BuilderSimulationRequest) {
			value.Engine.ObjectFormat = "sha256"
			value.Engine.CommitSHA = strings.Repeat("a", 64)
			value.Engine.TreeSHA = strings.Repeat("b", 64)
		},
		"target tree": func(value *remoteci.BuilderSimulationRequest) {
			value.Target.TreeSHA = strings.Repeat("c", 40)
		},
		"manifest digest": func(value *remoteci.BuilderSimulationRequest) {
			value.Target.WorkloadManifest.SHA256 = builderSimulationTestDigest("f")
		},
		"manifest path": func(value *remoteci.BuilderSimulationRequest) {
			value.Target.WorkloadManifest.RepoRelativePath = "missing/workload.json"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := request
			mutate(&candidate)
			path := filepath.Join(t.TempDir(), "request.json")
			builderSimulationWriteRequest(t, path, candidate)
			_, err := LoadAndBindBuilderSimulationRequest(context.Background(), path)
			if err == nil {
				t.Fatalf("loader accepted wrong %s", name)
			}
			assertBuilderSimulationLoaderErrorRedacted(t, err, path, candidate.ECI.CredentialProfile, string(mustBuilderSimulationTestJSON(t, candidate)))
		})
	}
}

func builderSimulationTestRequestFile(t *testing.T) (remoteci.BuilderSimulationRequest, string) {
	t.Helper()
	engineRoot, engineCommit, engineTree := builderSimulationTestRepository(t, "engine", []byte("package engine\n"))
	manifest := builderSimulationTestManifest(t, "target-repository")
	targetRoot, _, _ := builderSimulationTestRepository(t, "target", manifest)
	manifestPath := filepath.Join(targetRoot, ".super-dolphin", "remote-ci-workloads.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, targetRoot, "add", ".super-dolphin/remote-ci-workloads.json")
	runGitFixture(t, targetRoot, "commit", "-m", "测试 builder simulation manifest")
	targetCommit := runGitFixture(t, targetRoot, "rev-parse", "HEAD")
	targetTree := runGitFixture(t, targetRoot, "rev-parse", "HEAD^{tree}")
	request := builderSimulationTestRequest(t, engineRoot, engineCommit, engineTree, targetRoot, targetCommit, targetTree, manifest)
	requestPath := filepath.Join(t.TempDir(), "builder-simulation-request.json")
	builderSimulationWriteRequest(t, requestPath, request)
	return request, requestPath
}

func builderSimulationTestRepository(t *testing.T, name string, initial []byte) (string, string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, root, "init")
	runGitFixture(t, root, "config", "user.email", "builder-simulation@example.invalid")
	runGitFixture(t, root, "config", "user.name", "Builder Simulation")
	if err := os.WriteFile(filepath.Join(root, "source.txt"), initial, 0o600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, root, "add", "source.txt")
	runGitFixture(t, root, "commit", "-m", "测试 builder simulation source")
	return root, runGitFixture(t, root, "rev-parse", "HEAD"), runGitFixture(t, root, "rev-parse", "HEAD^{tree}")
}

func builderSimulationTestManifest(t *testing.T, repositoryID string) []byte {
	t.Helper()
	manifest := remoteci.RepositoryWorkloadManifest{
		SchemaVersion:         remoteci.PortableWorkloadManifestSchemaVersion,
		RepositoryID:          repositoryID,
		Modules:               []remoteci.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/target"}},
		ExcludedNestedModules: []string{},
		Profiles:              []remoteci.RepositoryWorkloadProfile{{Profile: "local-fast", RequiredWorkloadIDs: []string{"go-test/builder-simulation"}}},
		Workloads: []remoteci.RepositoryWorkload{{
			ID: "go-test/builder-simulation", Kind: remoteci.RepositoryWorkloadKindGoTest,
			ModuleRoot: ".", Package: "./target", Name: "TestBuilderSimulation",
			Platform: remoteci.RepositoryWorkloadPlatformLinuxAMD64, Runtime: remoteci.RepositoryWorkloadRuntimeUbuntuContainer,
			BootstrapEstimateMS: 1_000, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: remoteci.RepositoryWorkloadExecutionPolicyNormal,
		}},
	}
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func builderSimulationTestRequest(t *testing.T, engineRoot, engineCommit, engineTree, targetRoot, targetCommit, targetTree string, manifest []byte) remoteci.BuilderSimulationRequest {
	t.Helper()
	protocolABI, err := remoteci.BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	manifestABI, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	return remoteci.BuilderSimulationRequest{
		SchemaVersion: remoteci.BuilderSimulationRequestSchema, ContractID: remoteci.BuilderSimulationContractID,
		OperationID: "operation-loader-test", ExecutionProvider: cicontract.ExecutionProviderID,
		Engine: remoteci.BuilderSimulationEngine{
			RepositoryRoot: engineRoot, RepositoryID: "engine-repository", ObjectFormat: "sha1", CommitSHA: engineCommit, TreeSHA: engineTree,
			ExpectedBuilderDigest: builderSimulationTestDigest("a"), ExpectedToolchainDigest: builderSimulationTestDigest("b"), ProtocolABIDigest: protocolABI,
		},
		Target: remoteci.BuilderSimulationTarget{
			RepositoryRoot: targetRoot, RepositoryID: "target-repository", ObjectFormat: "sha1", CommitSHA: targetCommit, TreeSHA: targetTree,
			WorkloadManifest: remoteci.PortableWorkloadManifest{
				RepoRelativePath: ".super-dolphin/remote-ci-workloads.json", SHA256: digestBytes(manifest), ABIDigest: manifestABI,
				SchemaVersion: remoteci.PortableWorkloadManifestSchemaVersion, RepositoryID: "target-repository",
			},
		},
		Artifact: remoteci.BuilderSimulationArtifact{Prefix: "portable-engine/operation-loader-test/"},
		ECI: remoteci.BuilderSimulationECI{
			AliyunCLI: "/usr/local/bin/aliyun", CredentialProfile: "profile-loader-secret", RegionID: "cn-test", CompilerPath: "/usr/local/go/bin/go",
			VSwitches:       []cicontract.ECIVSwitch{{ID: "vsw-zone-a", ZoneID: "cn-test-a"}, {ID: "vsw-zone-b", ZoneID: "cn-test-b"}},
			SecurityGroupID: "sg-test", RAMRoleName: "role-test", ImageCacheID: "cache-test", ImageSnapshotID: "snapshot-test",
			Image: "registry.example/remote@" + builderSimulationTestDigest("c"),
		},
		OSS: remoteci.BuilderSimulationOSS{Bucket: "remote-ci-bucket", Endpoint: "https://oss.example", InternalEndpoint: "https://oss-internal.example"},
	}
}

func builderSimulationWriteRequest(t *testing.T, path string, request remoteci.BuilderSimulationRequest) {
	t.Helper()
	data := mustBuilderSimulationTestJSON(t, request)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustBuilderSimulationTestJSON(t *testing.T, request remoteci.BuilderSimulationRequest) []byte {
	t.Helper()
	data, err := request.CanonicalEncode()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func builderSimulationTestDigest(value string) string {
	return "sha256:" + strings.Repeat(value, 64)
}

func assertBuilderSimulationLoaderErrorRedacted(t *testing.T, err error, path, profile, payload string) {
	t.Helper()
	if (path != "" && strings.Contains(err.Error(), path)) || (profile != "" && strings.Contains(err.Error(), profile)) || (payload != "" && strings.Contains(err.Error(), payload)) {
		t.Fatalf("loader error leaked sensitive input: %v", err)
	}
}

func immediatePast() time.Time {
	return time.Now().Add(-time.Second)
}
