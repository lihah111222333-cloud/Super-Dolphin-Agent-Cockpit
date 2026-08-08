package remoteci

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func TestShardRequestRequiresCanonicalSourceBundleFields(t *testing.T) {
	valid := testSourceBundleShardRequest(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid source bundle request rejected: %v", err)
	}
	for name, mutate := range map[string]func(*ShardRequest){
		"missing bundle key": func(request *ShardRequest) { request.SourceBundleKey = "" },
		"bundle uses retired patch suffix": func(request *ShardRequest) {
			request.SourceBundleKey = strings.Replace(request.SourceBundleKey, ".bundle", ".patch", 1)
		},
		"source tree drift":   func(request *ShardRequest) { request.Source.SourceTreeSHA = strings.Repeat("b", 40) },
		"bundle size missing": func(request *ShardRequest) { request.SourceBundleSize = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatalf("mutated source bundle request unexpectedly passed: %#v", request)
			}
		})
	}
}

func TestShardRequestRoundTripsFourHundredElevenLongSelectorIDsWithinByteLimit(t *testing.T) {
	request := buildLongSelectorShardRequest(t, 411)
	manifestDigest, err := request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatalf("compute large shard manifest digest: %v", err)
	}
	request.ShardExecutionManifestDigest = manifestDigest
	data, _, err := EncodeShardRequest(request)
	if err != nil {
		t.Fatalf("encode large shard request: %v", err)
	}
	if len(data) <= 64<<10 || len(data) > ShardRequestMaxBytes {
		t.Fatalf("large shard request bytes = %d, want >64KiB and <=%d", len(data), ShardRequestMaxBytes)
	}
	decoded, err := DecodeShardRequest(data)
	if err != nil {
		t.Fatalf("decode large shard request: %v", err)
	}
	if len(decoded.GateIDs) != 411 || len(decoded.CompileGroups) != 1 || len(decoded.CompileGroups[0].WorkloadIDs) != 411 {
		t.Fatalf("decoded large shard coverage = gates %d groups %d", len(decoded.GateIDs), len(decoded.CompileGroups))
	}
}

func buildLongSelectorShardRequest(t *testing.T, count int) ShardRequest {
	t.Helper()
	request := testSourceBundleShardRequest(t)
	request.GateIDs = make([]gate.GateID, 0, count)
	for index := range count {
		name := "Test" + strings.Repeat("Long", 20) + fmt.Sprintf("%03d", index)
		workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", name, 1)
		if err != nil {
			t.Fatalf("build long selector %d: %v", index, err)
		}
		workloadID := gate.GateID(workload.ID)
		request.GateIDs = append(request.GateIDs, workloadID)
	}
	group := gate.CompileGroup{
		PackageTarget: "./internal/devtools/gate", SemanticKey: gate.CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("a", 64), ProfileDigest: "sha256:" + strings.Repeat("b", 64),
		ResourceClassID: "small", WorkloadIDs: append([]gate.GateID(nil), request.GateIDs...), CompileEstimateMS: 1, BodyEstimateMS: int64(count), EstimatedDurationMS: int64(count + 1),
	}
	finalizeTestCompileGroup(t, &group)
	request.CompileGroups = []gate.CompileGroup{group}
	return request
}

func testSourceBundleShardRequest(t *testing.T) ShardRequest {
	t.Helper()
	const (
		jobID = "job-source-bundle"
		tree  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	request := ShardRequest{
		SchemaVersion:        ShardRequestSchemaVersion,
		AgentTokenDigest:     "sha256:" + strings.Repeat("1", 64),
		JobID:                jobID,
		ShardIdentity:        "sha256:" + strings.Repeat("2", 64),
		Profile:              gate.ProfileLocalFast,
		PlanDigest:           "sha256:" + strings.Repeat("3", 64),
		BaselineManifest:     "sha256:" + strings.Repeat("4", 64),
		ImageCacheSnapshotID: "snapshot-source-bundle",
		OCIProjectCache: &BaselineOCIProjectCache{
			Image:                 "registry.example/runner@sha256:" + strings.Repeat("5", 64),
			ContentManifestSHA256: "sha256:" + strings.Repeat("6", 64),
			MainTree:              tree,
			ToolchainDigest:       "sha256:" + strings.Repeat("7", 64),
			Platform:              "linux/amd64",
			CachePath:             OCIProjectGoBuildCachePath,
		},
		RunnerBaseTree:          tree,
		BaselineRuntimeImage:    "registry.example/runner@sha256:" + strings.Repeat("5", 64),
		BaselineToolchainDigest: "sha256:" + strings.Repeat("7", 64),
		Source: gate.SourceSpec{
			Kind:          gate.SourceKindCommit,
			ObjectFormat:  gate.GitObjectFormatSHA1,
			Commit:        &gate.CommitSource{SHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			SourceTreeSHA: tree,
		},
		SourceTreeSHA:                tree,
		SourceBundleKey:              "source-bundles/" + jobID + "/" + strings.Repeat("8", 64) + ".bundle",
		SourceBundleSHA256:           strings.Repeat("8", 64),
		SourceBundleSize:             42,
		ManifestKey:                  "source-bundles/" + jobID + "/" + strings.Repeat("9", 64) + ".manifest.json",
		ManifestSHA256:               strings.Repeat("9", 64),
		CandidateGateSourceSHA256:    "sha256:" + strings.Repeat("a", 64),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("b", 64),
		GateIDs:                      []gate.GateID{gate.GateIDBackendTestWithGuard},
		ResourceClass:                shardresource.Class{ID: "small", VCPU: 2, MemoryGiB: 4},
	}
	digest, err := request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatalf("compute source bundle shard manifest digest: %v", err)
	}
	request.ShardExecutionManifestDigest = digest
	return request
}
