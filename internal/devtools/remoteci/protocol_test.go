package remoteci

import (
	"encoding/json"
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
		{"missing OCI cache", func(request *ShardRequest) { request.OCIProjectCache = nil }},
		{"OCI cache tree drift", func(request *ShardRequest) {
			request.OCIProjectCache = validBaselineOCIProjectCache(strings.Repeat("f", 40), "sha256:"+strings.Repeat("a", 64), "linux/amd64", "registry.example/runtime@sha256:"+strings.Repeat("b", 64))
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

func TestDecodeShardRequestRejectsValidatedFieldDrift(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"schema":     func(wire map[string]any) { wire["schema_version"] = float64(1) },
		"tree":       func(wire map[string]any) { wire["runner_base_tree"] = strings.Repeat("f", 40) },
		"toolchain":  func(wire map[string]any) { wire["baseline_toolchain_digest"] = "sha256:" + strings.Repeat("f", 64) },
		"OCI cache":  func(wire map[string]any) { wire["oci_project_cache"].(map[string]any)["cache_path"] = "/tmp/cache" },
		"patch path": func(wire map[string]any) { wire["patch_key"] = "../escape.patch" },
	} {
		t.Run(name, func(t *testing.T) {
			data, _, err := EncodeShardRequest(validShardRequest())
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			mutate(wire)
			data, err = json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeShardRequest(data); err == nil {
				t.Fatal("DecodeShardRequest() accepted validation drift")
			}
		})
	}
}

func validShardRequest() ShardRequest {
	digest := "sha256:" + strings.Repeat("a", 64)
	return ShardRequest{
		SchemaVersion: ShardRequestSchemaVersion,
		JobID:         "job-1234", ShardIdentity: digest,
		Profile: gate.ProfileLocalFast, PlanDigest: digest, BaselineManifest: digest,
		RunnerBaseCommit: strings.Repeat("c", 40), RunnerBaseTree: strings.Repeat("d", 40),
		BaselineRuntimeImage: "registry.example/runtime@sha256:" + strings.Repeat("b", 64), BaselineToolchainDigest: "sha256:" + strings.Repeat("a", 64),
		OCIProjectCache: validBaselineOCIProjectCache(strings.Repeat("d", 40), "sha256:"+strings.Repeat("a", 64), "linux/amd64", "registry.example/runtime@sha256:"+strings.Repeat("b", 64)),
		SourceTreeSHA:   strings.Repeat("c", 40), PatchFormat: "git-binary-v1",
		PatchKey: "baseline-artifacts/source-deltas/job-1234/source.patch", PatchSHA256: strings.Repeat("d", 64), PatchSize: 42,
		ManifestKey: "baseline-artifacts/source-deltas/job-1234/source.manifest.json", ManifestSHA256: strings.Repeat("e", 64),
		CandidateCLI: CandidateCLIArtifactRef{
			CandidateTree: strings.Repeat("c", 40), SourceSHA256: digest, ToolchainSHA256: digest, Platform: "linux/amd64",
			ManifestKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("f", 64) + ".manifest.json", ManifestSHA256: strings.Repeat("f", 64),
			BinaryKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("1", 64) + ".candidate-cli", BinarySHA256: strings.Repeat("1", 64), BinarySize: 42, CLIIdentity: CandidateCLIIdentity(digest, digest),
		},
		CandidateTestBinaries: []CandidateTestBinaryArtifactRef{{
			CandidateTree: strings.Repeat("c", 40), Package: "example.invalid/test", Mode: "test", Platform: "linux/amd64", GoToolchain: "go1.26.5", CGOEnabled: true, ToolchainSHA256: digest, BuildFlags: []string{"-trimpath"}, CompileClosureSHA256: digest,
			ManifestKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("2", 64) + ".manifest.json", ManifestSHA256: strings.Repeat("2", 64), BinaryKey: "baseline-artifacts/source-deltas/job-1234/" + strings.Repeat("3", 64) + ".test-bin", BinarySHA256: strings.Repeat("3", 64), BinarySize: 42,
		}},
		GateIDs: []gate.GateID{"go:format"},
	}
}
