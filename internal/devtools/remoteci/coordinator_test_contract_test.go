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
		"ResourcePolicy", "ResourceObservations",
	})
	assertBaselineFields(t, reflect.TypeFor[RunInput](), []string{
		"RepositoryRoot", "RemoteName", "RemoteURL", "RequesterFingerprint", "Commit", "Tree", "Base", "RunnerBaseCommit", "RunnerBaseTree",
		"Source", "Profile", "Entrypoint", "MaxShards", "Platform", "PolicyDigest", "ToolchainDigest",
		"LedgerSnapshot", "LedgerStore", "Inventory", "SelectedTests", "Calibration", "RunnerImage",
		"RunnerIdentityDigest", "BaselineManifestDigest", "RunnerConfigDigest", "GateBinarySHA256",
		"RuntimeSeedSHA256", "DataCacheBucket", "DataCachePath", "AnchorGeneration", "AnchorManifest", "AnchorCommit", "AnchorTree", "BaselineDeltas", "ForceRerun",
	})
	assertBaselineFields(t, reflect.TypeFor[RunResult](), []string{
		"SchemaVersion", "JobID", "RemoteName", "RemoteURL", "RequesterFingerprint", "Entrypoint", "Profile", "PlanDigest", "CatalogDigest", "SourceTreeSHA",
		"RunnerImage", "Status", "Authoritative", "StartedAt", "CompletedAt", "Shards",
		"ReusedWorkloads", "CacheMissWorkloads", "GateExecutions", "DurationSamples", "PerformanceTimings", "OptimizationWarnings", "CleanupComplete",
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
	requests, keys, err := buildShardRequests(sourcePrefix, jobID, []gate.ContainerShard{{Index: 0, IdentityDigest: runnerIdentity, Profile: gate.ProfileLocalFast, PlanDigest: runnerIdentity, SourceTreeSHA: gitObject, GateIDs: []gate.GateID{gate.GateIDWhitespaceCheck}}}, source.Artifact{Manifest: source.Manifest{BaseCommit: runnerBaseCommit, BaseTree: runnerBaseTree, PatchFormat: "git-binary-v1", PatchSHA256: objectDigest}}, jobPrefix+objectDigest+".patch", jobPrefix+objectDigest+".manifest.json", objectDigest, RunInput{BaselineManifestDigest: baselineManifest, AnchorGeneration: 1, AnchorManifest: anchorManifest, AnchorCommit: gitObject, AnchorTree: gitObject, BaselineDeltas: []BaselineDeltaLayer{{Generation: 2, ObjectPrefix: "baseline-artifacts/2/", ManifestDigest: baselineManifest, BaseCommit: gitObject, BaseTree: gitObject, MainCommit: runnerBaseCommit, MainTree: runnerBaseTree}}, RunnerBaseCommit: runnerBaseCommit, RunnerBaseTree: runnerBaseTree})
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
}

func TestCoordinatorCreateRequestBootstrapsLatestDeltaGateBinary(t *testing.T) {
	_, input := remoteRunFixture(t)
	input.BaselineDeltas = []BaselineDeltaLayer{{Generation: 2, ObjectPrefix: "baseline-artifacts/deltas/2"}, {Generation: 3, ObjectPrefix: "baseline-artifacts/deltas/3"}}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	coordinator.config.InternalOSSEndpoint = "https://oss-cn-shenzhen-internal.aliyuncs.com"
	request := coordinator.createRequest("job-0123456789abcdef01234567", gate.ContainerShard{Index: 1, Profile: input.Profile, PlanDigest: input.PolicyDigest, GateIDs: []gate.GateID{"go:test"}}, eci.Resources{CPU: 4, MemoryGiB: 8}, "baseline-artifacts/source-deltas/jobs/job/request.json", input.PolicyDigest, input)
	wantVolume := eci.OSSVolume{Bucket: "ci-bucket", Endpoint: "oss-cn-shenzhen-internal.aliyuncs.com", Path: "/baseline-artifacts/deltas/3/output", RoleName: "worker-role"}
	if request.BootstrapVolume != wantVolume {
		t.Fatalf("BootstrapVolume = %+v, want %+v", request.BootstrapVolume, wantVolume)
	}
	if !reflect.DeepEqual(request.InitContainer.Command, []string{"/bin/sh"}) || !reflect.DeepEqual(request.InitContainer.Args, []string{"-c", remoteCurrentGateBootstrapSH}) {
		t.Fatalf("init command = %v %v", request.InitContainer.Command, request.InitContainer.Args)
	}
	if got := request.InitContainer.Environment[remoteCurrentGateDigestEnv]; got != input.GateBinarySHA256 {
		t.Fatalf("%s = %q, want %q", remoteCurrentGateDigestEnv, got, input.GateBinarySHA256)
	}
	wantTail := []eci.VolumeMount{{Name: "temp-data", MountPath: "/tmp"}, {Name: remoteCurrentGateVolumeName, MountPath: remoteCurrentGateMountPath, ReadOnly: true}}
	if got := request.InitVolumeMounts[len(request.InitVolumeMounts)-len(wantTail):]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("init mount tail = %+v, want %+v", got, wantTail)
	}
}
