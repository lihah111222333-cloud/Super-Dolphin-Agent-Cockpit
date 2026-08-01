package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestShardRequestRoundTrip(t *testing.T) {
	request := validShardRequest()
	data, digest, err := EncodeShardRequest(request)
	if err != nil {
		t.Fatalf("EncodeShardRequest() error = %v", err)
	}
	if len(digest) != 64 {
		t.Fatalf("request digest length = %d", len(digest))
	}
	decoded, err := DecodeShardRequest(data)
	if err != nil {
		t.Fatalf("DecodeShardRequest() error = %v", err)
	}
	if decoded.JobID != request.JobID || decoded.PatchKey != request.PatchKey {
		t.Fatalf("decoded request = %#v", decoded)
	}
}

func TestShardRequestRejectsDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ShardRequest)
	}{
		{"duplicate gate", func(request *ShardRequest) { request.GateIDs = append(request.GateIDs, request.GateIDs[0]) }},
		{"previous schema", func(request *ShardRequest) { request.SchemaVersion = ShardRequestSchemaVersion - 1 }},
		{"tag digest", func(request *ShardRequest) { request.PlanDigest = "latest" }},
		{"outside prefix", func(request *ShardRequest) { request.PatchKey = "results/source.patch" }},
		{"unclean key", func(request *ShardRequest) { request.ManifestKey = "source-deltas/a/../source.manifest.json" }},
		{"cross-job manifest", func(request *ShardRequest) {
			request.ManifestKey = "baseline-artifacts/source-deltas/job-5678/source.manifest.json"
		}},
		{"cross-job candidate binary", func(request *ShardRequest) {
			request.CandidateCLI.BinaryKey = "baseline-artifacts/source-deltas/job-5678/candidate.candidate-cli"
		}},
		{"uppercase digest", func(request *ShardRequest) { request.PatchSHA256 = strings.ToUpper(request.PatchSHA256) }},
		{"negative size", func(request *ShardRequest) { request.PatchSize = -1 }},
		{"wrong format", func(request *ShardRequest) { request.PatchFormat = "text" }},
		{"discontinuous delta", func(request *ShardRequest) { request.BaselineDeltas[0].BaseTree = strings.Repeat("f", 40) }},
		{"delta chain misses runner base", func(request *ShardRequest) { request.RunnerBaseCommit = strings.Repeat("f", 40) }},
		{"too many deltas", func(request *ShardRequest) {
			request.BaselineDeltas = append(request.BaselineDeltas, request.BaselineDeltas[0], request.BaselineDeltas[0], request.BaselineDeltas[0], request.BaselineDeltas[0])
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := validShardRequest()
			testCase.mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestShardRequestAcceptsCommitOnlyBaselineDelta(t *testing.T) {
	request := validShardRequest()
	request.AnchorTree = request.BaselineDeltas[0].MainTree
	request.BaselineDeltas[0].BaseTree = request.AnchorTree
	if err := request.Validate(); err != nil {
		t.Fatalf("commit-only baseline delta rejected: %v", err)
	}
}

func TestDecodeShardRequestRejectsUnknownField(t *testing.T) {
	data, _, err := EncodeShardRequest(validShardRequest())
	if err != nil {
		t.Fatalf("EncodeShardRequest() error = %v", err)
	}
	data = append(data[:len(data)-1], []byte(`,"unknown":true}`)...)
	if _, err := DecodeShardRequest(data); err == nil {
		t.Fatal("DecodeShardRequest() error = nil")
	}
}

func TestDecodeShardRequestRejectsUnknownCandidateCLIField(t *testing.T) {
	data, _, err := EncodeShardRequest(validShardRequest())
	if err != nil {
		t.Fatalf("EncodeShardRequest() error = %v", err)
	}
	data = []byte(strings.Replace(string(data), `"candidate_cli_artifact":{`, `"candidate_cli_artifact":{"unknown":true,`, 1))
	if _, err := DecodeShardRequest(data); err == nil {
		t.Fatal("DecodeShardRequest() accepted unknown candidate CLI field")
	}
}

func validShardRequest() ShardRequest {
	digest := "sha256:" + strings.Repeat("a", 64)
	return ShardRequest{
		SchemaVersion: ShardRequestSchemaVersion,
		JobID:         "job-1234", ShardIdentity: digest,
		Profile: gate.ProfileLocalFast, PlanDigest: digest, BaselineManifest: digest,
		AnchorGeneration: 1, AnchorManifest: digest,
		AnchorCommit: strings.Repeat("a", 40), AnchorTree: strings.Repeat("b", 40),
		BaselineDeltas: []BaselineDeltaLayer{{
			Generation: 2, ObjectPrefix: "baseline-artifacts/2/", ManifestDigest: digest,
			BaseCommit: strings.Repeat("a", 40), BaseTree: strings.Repeat("b", 40),
			MainCommit: strings.Repeat("c", 40), MainTree: strings.Repeat("d", 40),
		}},
		RunnerBaseCommit: strings.Repeat("c", 40), RunnerBaseTree: strings.Repeat("d", 40),
		SourceTreeSHA: strings.Repeat("c", 40), PatchFormat: "git-binary-v1",
		PatchKey: "baseline-artifacts/source-deltas/job-1234/source.patch", PatchSHA256: strings.Repeat("d", 64), PatchSize: 42,
		ManifestKey: "baseline-artifacts/source-deltas/job-1234/source.manifest.json", ManifestSHA256: strings.Repeat("e", 64),
		CandidateCLI: CandidateCLIArtifactRef{
			CandidateTree: strings.Repeat("c", 40), SourceSHA256: digest, ToolchainSHA256: digest, Platform: "linux/amd64",
			ManifestKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("f", 64) + ".manifest.json", ManifestSHA256: strings.Repeat("f", 64),
			BinaryKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("1", 64) + ".candidate-cli", BinarySHA256: strings.Repeat("1", 64), BinarySize: 42, CLIIdentity: CandidateCLIIdentity(digest, digest),
		},
		CandidateTestBinaries: []CandidateTestBinaryArtifactRef{{
			CandidateTree: strings.Repeat("c", 40), Package: "example.invalid/test", Mode: "test", Platform: "linux/amd64", GoToolchain: "go1.25.7", CGOEnabled: true, ToolchainSHA256: digest, BuildFlags: []string{"-trimpath"}, CompileClosureSHA256: digest,
			ManifestKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("2", 64) + ".manifest.json", ManifestSHA256: strings.Repeat("2", 64), BinaryKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("3", 64) + ".test-bin", BinarySHA256: strings.Repeat("3", 64), BinarySize: 42,
		}},
		GateIDs: []gate.GateID{"go:format"},
	}
}
