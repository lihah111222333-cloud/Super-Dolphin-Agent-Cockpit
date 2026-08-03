package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func TestShardRequestRequiresCanonicalSourceBundleFields(t *testing.T) {
	valid := testSourceBundleShardRequest()
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

func testSourceBundleShardRequest() ShardRequest {
	const (
		jobID = "job-source-bundle"
		tree  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	return ShardRequest{
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
		GateIDs:                      []gate.GateID{"guard"},
		ResourceClass:                shardresource.Class{ID: "small", VCPU: 2, MemoryGiB: 4},
	}
}
