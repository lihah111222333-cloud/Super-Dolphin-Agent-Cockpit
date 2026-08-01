package remoteci

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/source"
)

func TestRemoteRunContractFieldRegistry(t *testing.T) {
	assertBaselineFields(t, reflect.TypeFor[CoordinatorConfig](), []string{
		"Bucket", "SourcePrefix", "WorkloadCachePrefix", "InternalOSSEndpoint", "WorkerRoleName", "WorkerTimeout", "PollInterval", "CleanupTimeout",
		"ResourcePolicy", "ResourceObservations", "CandidateCLIBuilder",
	})
	assertBaselineFields(t, reflect.TypeFor[RunInput](), []string{
		"RepositoryRoot", "RemoteName", "RemoteURL", "RequesterFingerprint", "Commit", "Tree", "Base", "RunnerBaseCommit", "RunnerBaseTree",
		"Source", "Profile", "Entrypoint", "MaxShards", "Platform", "PolicyDigest", "ToolchainDigest",
		"LedgerSnapshot", "LedgerStore", "Inventory", "SelectedTests", "Calibration", "RunnerImage",
		"RunnerIdentityDigest", "BaselineManifestDigest", "RunnerConfigDigest", "GateBinarySHA256",
		"CandidateGateSourceSHA256", "CandidateGateToolchainSHA256", "ReuseBaselineGateCLI",
		"RuntimeSeedSHA256", "DataCacheBucket", "DataCachePath", "AnchorGeneration", "AnchorManifest", "AnchorCommit", "AnchorTree", "BaselineDeltas", "ForceRerun",
	})
	assertBaselineFields(t, reflect.TypeFor[RunResult](), []string{
		"SchemaVersion", "JobID", "RemoteName", "RemoteURL", "RequesterFingerprint", "Entrypoint", "Profile", "PlanDigest", "CatalogDigest", "SourceTreeSHA",
		"CandidateCLIManifestSHA256", "RunnerImage", "Status", "Authoritative", "StartedAt", "CompletedAt", "Shards",
		"ReusedWorkloads", "CacheMissWorkloads", "GateExecutions", "DurationSamples", "PerformanceTimings", "OptimizationWarnings", "CleanupComplete",
	})
	assertBaselineFields(t, reflect.TypeFor[ShardRequest](), []string{
		"SchemaVersion", "JobID", "ShardIdentity", "Profile", "PlanDigest", "BaselineManifest", "AnchorGeneration", "AnchorManifest", "AnchorCommit", "AnchorTree", "BaselineDeltas", "RunnerBaseCommit", "RunnerBaseTree", "SourceTreeSHA", "PatchFormat", "PatchKey", "PatchSHA256", "PatchSize", "ManifestKey", "ManifestSHA256", "CandidateCLI", "GateIDs",
	})
	assertBaselineFields(t, reflect.TypeFor[CandidateCLIArtifactRef](), []string{
		"CandidateTree", "SourceSHA256", "ToolchainSHA256", "Platform", "ManifestKey", "ManifestSHA256", "BinaryKey", "BinarySHA256", "BinarySize", "CLIIdentity",
	})
}

func TestNewRunResultEchoesRequesterFingerprint(t *testing.T) {
	startedAt := time.Date(2026, time.July, 30, 1, 2, 3, 0, time.UTC)
	fingerprint := gate.RequesterFingerprint("sha256:" + strings.Repeat("a", 64))
	coordinator := &Coordinator{now: func() time.Time { return startedAt }}
	result := coordinator.newRunResult(gate.GatePlan{Profile: gate.ProfileLocalFast, Source: gate.SourceSpec{SourceTreeSHA: strings.Repeat("1", 40)}, PlanDigest: "sha256:plan"}, "sha256:"+strings.Repeat("2", 64), gate.WorkloadCatalog{Authoritative: true}, gate.CIEntrypoint{ID: gate.CIEntrypointGitPreCommit, Authoritative: true}, RunInput{RequesterFingerprint: fingerprint, RunnerImage: "ubuntu:22.04"}, "job-requester-echo")
	if result.RequesterFingerprint != fingerprint {
		t.Fatalf("result requester fingerprint = %q, want %q", result.RequesterFingerprint, fingerprint)
	}
}

func TestBuildShardRequestsBindsExactBaselineManifest(t *testing.T) {
	requests, keys, baselineManifest, anchorManifest, runnerBaseCommit, runnerBaseTree, runnerIdentity := baselineBoundShardRequests(t)
	if len(requests) != 1 || len(keys) != 1 {
		t.Fatalf("shard requests = %#v, keys = %v", requests, keys)
	}
	request := requests[0]
	assertBaselineBoundShardRequest(t, request, baselineManifest, anchorManifest, runnerBaseCommit, runnerBaseTree, runnerIdentity)
}

func baselineBoundShardRequests(t *testing.T) ([]ShardRequest, []string, string, string, string, string, string) {
	t.Helper()
	const jobID = "job-0123456789abcdef01234567"
	const sourcePrefix = "baseline-artifacts/source-deltas/"
	runnerIdentity := "sha256:" + strings.Repeat("a", 64)
	anchorManifest := "sha256:" + strings.Repeat("b", 64)
	baselineManifest := "sha256:" + strings.Repeat("e", 64)
	objectDigest := strings.Repeat("c", 64)
	gitObject := strings.Repeat("d", 40)
	runnerBaseCommit := strings.Repeat("e", 40)
	runnerBaseTree := strings.Repeat("f", 40)
	jobPrefix := sourcePrefix + jobID + "/"
	candidateManifest := strings.Repeat("9", 64)
	candidateCLI := CandidateCLIArtifactRef{CandidateTree: gitObject, SourceSHA256: runnerIdentity, ToolchainSHA256: runnerIdentity, Platform: "linux/amd64", ManifestKey: jobPrefix + candidateManifest + ".manifest.json", ManifestSHA256: candidateManifest, BinaryKey: jobPrefix + strings.Repeat("8", 64) + ".candidate-cli", BinarySHA256: strings.Repeat("8", 64), BinarySize: 42, CLIIdentity: CandidateCLIIdentity(runnerIdentity, runnerIdentity)}
	requests, keys, err := buildShardRequestsWithCandidate(sourcePrefix, jobID, []gate.ContainerShard{{Index: 0, IdentityDigest: runnerIdentity, Profile: gate.ProfileLocalFast, PlanDigest: runnerIdentity, SourceTreeSHA: gitObject, GateIDs: []gate.GateID{gate.GateIDWhitespaceCheck}}}, source.Artifact{Manifest: source.Manifest{BaseCommit: runnerBaseCommit, BaseTree: runnerBaseTree, PatchFormat: "git-binary-v1", PatchSHA256: objectDigest}}, jobPrefix+objectDigest+".patch", jobPrefix+objectDigest+".manifest.json", objectDigest, candidateCLI, RunInput{BaselineManifestDigest: baselineManifest, AnchorGeneration: 1, AnchorManifest: anchorManifest, AnchorCommit: gitObject, AnchorTree: gitObject, BaselineDeltas: []BaselineDeltaLayer{{Generation: 2, ObjectPrefix: "baseline-artifacts/2/", ManifestDigest: baselineManifest, BaseCommit: gitObject, BaseTree: gitObject, MainCommit: runnerBaseCommit, MainTree: runnerBaseTree}}, RunnerBaseCommit: runnerBaseCommit, RunnerBaseTree: runnerBaseTree})
	if err != nil {
		t.Fatalf("buildShardRequests() error = %v", err)
	}
	return requests, keys, baselineManifest, anchorManifest, runnerBaseCommit, runnerBaseTree, runnerIdentity
}

func assertBaselineBoundShardRequest(t *testing.T, request ShardRequest, baselineManifest string, anchorManifest string, runnerBaseCommit string, runnerBaseTree string, runnerIdentity string) {
	t.Helper()
	if request.BaselineManifest != baselineManifest || request.AnchorManifest != anchorManifest || len(request.BaselineDeltas) != 1 {
		t.Fatalf("shard request baseline = %#v", request)
	}
	if request.BaselineDeltas[0].ManifestDigest != baselineManifest || request.RunnerBaseCommit != runnerBaseCommit || request.RunnerBaseTree != runnerBaseTree || request.BaselineManifest == runnerIdentity {
		t.Fatalf("shard request identity = %#v", request)
	}
	if err := request.CandidateCLI.Validate("baseline-artifacts/source-deltas/job-0123456789abcdef01234567/", request.SourceTreeSHA); err != nil {
		t.Fatalf("shard request candidate CLI = %#v: %v", request.CandidateCLI, err)
	}
}

func TestCoordinatorCreateRequestAlwaysMountsWritableMaterializerTemp(t *testing.T) {
	_, input := remoteRunFixture(t)
	input.BaselineDeltas = nil
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	request := coordinator.createRequest("job-0123456789abcdef01234567", gate.ContainerShard{Index: 1, Profile: input.Profile, PlanDigest: input.PolicyDigest, GateIDs: []gate.GateID{"go:test"}}, eci.Resources{CPU: 4, MemoryGiB: 8}, "baseline-artifacts/source-deltas/jobs/job/request.json", input.PolicyDigest, testCandidateCLI(input), input)
	if request.BootstrapVolume.Path != "/baseline-artifacts/source-deltas/job-0123456789abcdef01234567" {
		t.Fatalf("BootstrapVolume = %+v, want candidate job directory", request.BootstrapVolume)
	}
	if got := request.InitContainer.Environment["TMPDIR"]; got != remoteWritableTempMountPath {
		t.Fatalf("TMPDIR = %q, want %q", got, remoteWritableTempMountPath)
	}
	if got := request.InitContainer.Environment["PATH"]; got != remoteInitSearchPath {
		t.Fatalf("init PATH = %q, want bootstrap-only %q", got, remoteInitSearchPath)
	}
	if !reflect.DeepEqual(request.InitContainer.Command, []string{"/bin/sh"}) ||
		!reflect.DeepEqual(request.InitContainer.Args, []string{"-c", remoteCandidateGateBootstrapSH}) {
		t.Fatalf("init command = %v %v", request.InitContainer.Command, request.InitContainer.Args)
	}
	assertCandidateGateEnvironment(t, request, input)
	assertWritableMaterializerTempMount(t, request)
}

func TestRemoteCandidateCLIArtifactMaterializesWithoutShardBuild(t *testing.T) {
	for _, required := range []string{"sha256sum", "wc -c", "chmod 0755", "worker cli-identity", "_remote-materialize"} {
		if !strings.Contains(remoteCandidateGateBootstrapSH, required) {
			t.Fatalf("candidate artifact materializer missing %q: %q", required, remoteCandidateGateBootstrapSH)
		}
	}
	previous := -1
	for _, step := range []string{"sha256sum", "wc -c", "chmod 0755", "worker cli-identity", "_remote-materialize"} {
		current := strings.Index(remoteCandidateGateBootstrapSH, step)
		if current <= previous {
			t.Fatalf("candidate artifact bootstrap order = %q, %q appears out of order", remoteCandidateGateBootstrapSH, step)
		}
		previous = current
	}
	for _, forbidden := range []string{"go build", "go install", "apt-get", "apk add", "curl ", "wget ", "GOPROXY=http", "current-gate", "/bootstrap/bin/super-dolphin-gate"} {
		if strings.Contains(remoteCandidateGateBootstrapSH, forbidden) {
			t.Fatalf("candidate artifact materializer violates immutable artifact contract through %q", forbidden)
		}
	}
}

func TestRemoteInitSearchPathUsesMaterializedRuntimeUnderECILimit(t *testing.T) {
	searchPath := strings.Split(remoteInitSearchPath, ":")
	if searchPath[0] != gate.ExecutorRuntimeSeedRoot+"/bin" {
		t.Fatalf("init PATH = %q, want materialized runtime tools first", remoteInitSearchPath)
	}
	if len(remoteInitSearchPath) > 256 {
		t.Fatalf("init PATH length = %d, exceeds ECI environment limit", len(remoteInitSearchPath))
	}
	if strings.Contains(remoteInitSearchPath, gate.ExecutorPortableGoRoot+"/bin") || strings.Contains(remoteInitSearchPath, gate.ExecutorRuntimeSeedRoot+"/node/bin") {
		t.Fatalf("init PATH = %q, must not include worker-only toolchains", remoteInitSearchPath)
	}
}

func TestRemoteCandidateBootstrapExecutesOutsideEmptyMaterializationRoot(t *testing.T) {
	if !strings.Contains(remoteCandidateGateBootstrapSH, `bootstrap_cli="$TMPDIR/candidate-super-dolphin-gate"`) ||
		!strings.Contains(remoteCandidateGateBootstrapSH, `exec "$bootstrap_cli" _remote-materialize`) {
		t.Fatalf("candidate bootstrap must execute from the writable temp volume: %q", remoteCandidateGateBootstrapSH)
	}
	if strings.Contains(remoteCandidateGateBootstrapSH, `cp "$candidate" /opt/super-dolphin-gate`) {
		t.Fatalf("candidate bootstrap must preserve the empty materialization root: %q", remoteCandidateGateBootstrapSH)
	}
}

func TestCoordinatorCreateRequestMountsCandidateArtifactFromValidOSSVolume(t *testing.T) {
	_, input := remoteRunFixture(t)
	input.BaselineDeltas = []BaselineDeltaLayer{{Generation: 2, ObjectPrefix: "baseline-artifacts/deltas/2"}, {Generation: 3, ObjectPrefix: "baseline-artifacts/deltas/3"}}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	coordinator.config.InternalOSSEndpoint = "https://oss-cn-shenzhen-internal.aliyuncs.com"
	request := coordinator.createRequest("job-0123456789abcdef01234567", gate.ContainerShard{Index: 1, Profile: input.Profile, PlanDigest: input.PolicyDigest, GateIDs: []gate.GateID{"go:test"}}, eci.Resources{CPU: 4, MemoryGiB: 8}, "baseline-artifacts/source-deltas/jobs/job/request.json", input.PolicyDigest, testCandidateCLI(input), input)
	if request.BootstrapVolume.Bucket != coordinator.config.Bucket ||
		request.BootstrapVolume.Endpoint != "oss-cn-shenzhen-internal.aliyuncs.com" ||
		request.BootstrapVolume.Path != "/baseline-artifacts/source-deltas/job-0123456789abcdef01234567" ||
		request.BootstrapVolume.RoleName != coordinator.config.WorkerRoleName {
		t.Fatalf("BootstrapVolume = %+v, want candidate job directory", request.BootstrapVolume)
	}
	if !reflect.DeepEqual(request.InitContainer.Command, []string{"/bin/sh"}) || !reflect.DeepEqual(request.InitContainer.Args, []string{"-c", remoteCandidateGateBootstrapSH}) {
		t.Fatalf("init command = %v %v", request.InitContainer.Command, request.InitContainer.Args)
	}
	assertCandidateGateEnvironment(t, request, input)
	assertWritableMaterializerTempMount(t, request)
	assertCandidateArtifactMount(t, request)
}

func assertCandidateArtifactMount(t *testing.T, request eci.CreateRequest) {
	t.Helper()
	foundCandidateMount := false
	for _, mount := range request.InitVolumeMounts {
		if mount.Name == remoteCurrentGateVolumeName && mount.MountPath == "/candidate-bootstrap" && mount.ReadOnly {
			foundCandidateMount = true
		}
		if mount.MountPath == remoteCurrentGateMountPath {
			t.Fatalf("init mount unexpectedly retains legacy current-gate path: %+v", mount)
		}
	}
	if !foundCandidateMount {
		t.Fatal("init mounts do not bind the candidate artifact OSS volume")
	}
}

func testCandidateCLI(input RunInput) CandidateCLIArtifactRef {
	keyPrefix := "baseline-artifacts/source-deltas/job-0123456789abcdef01234567/"
	digest := strings.Repeat("a", 64)
	return CandidateCLIArtifactRef{CandidateTree: input.Tree, SourceSHA256: input.CandidateGateSourceSHA256, ToolchainSHA256: input.CandidateGateToolchainSHA256, Platform: "linux/amd64", ManifestKey: keyPrefix + digest + ".manifest.json", ManifestSHA256: digest, BinaryKey: keyPrefix + strings.Repeat("b", 64) + ".candidate-cli", BinarySHA256: strings.Repeat("b", 64), BinarySize: 42, CLIIdentity: CandidateCLIIdentity(input.CandidateGateSourceSHA256, input.CandidateGateToolchainSHA256)}
}

func assertCandidateGateEnvironment(t *testing.T, request eci.CreateRequest, input RunInput) {
	t.Helper()
	want := map[string]string{remoteBaselineManifestEnvironment: input.AnchorManifest, remoteCandidateGateSourceEnv: input.CandidateGateSourceSHA256, remoteCandidateGateToolchainEnv: input.CandidateGateToolchainSHA256}
	for name, value := range want {
		if got := request.InitContainer.Environment[name]; got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
	for _, name := range []string{remoteCurrentGateDigestEnv, remoteReuseBaselineGateEnv} {
		if _, found := request.InitContainer.Environment[name]; found {
			t.Fatalf("init environment unexpectedly retains deprecated gate setting %q", name)
		}
	}
}

func assertWritableMaterializerTempMount(t *testing.T, request eci.CreateRequest) {
	t.Helper()
	count := 0
	for _, mount := range request.InitVolumeMounts {
		if mount.Name != "temp-data" {
			continue
		}
		count++
		if mount.MountPath != remoteWritableTempMountPath || mount.ReadOnly {
			t.Fatalf("materializer temp mount = %+v, want writable %q", mount, remoteWritableTempMountPath)
		}
	}
	if count != 1 {
		t.Fatalf("materializer temp mount count = %d, want 1; mounts=%+v", count, request.InitVolumeMounts)
	}
}
