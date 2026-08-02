package remoteci

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestOCIBaselineBuilderProtocolRoundTrip(t *testing.T) {
	request := validOCIBaselineBuilderRequest()
	data, requestDigest, err := EncodeOCIBaselineBuilderRequest(request)
	if err != nil || requestDigest == "" {
		t.Fatalf("EncodeOCIBaselineBuilderRequest() = %q, %v", requestDigest, err)
	}
	if decoded, err := DecodeOCIBaselineBuilderRequest(data); err != nil || !reflect.DeepEqual(decoded, request) {
		t.Fatalf("request round trip = %#v, %v", decoded, err)
	}
	result := validOCIBaselineBuilderResult(request)
	data, resultDigest, err := EncodeOCIBaselineBuilderResult(result)
	if err != nil || resultDigest == "" {
		t.Fatalf("EncodeOCIBaselineBuilderResult() = %q, %v", resultDigest, err)
	}
	if decoded, err := DecodeOCIBaselineBuilderResult(data, request); err != nil || !reflect.DeepEqual(decoded, result) {
		t.Fatalf("result round trip = %#v, %v", decoded, err)
	}
}

func TestOCIBaselineBuilderRequestRejectsInvalidIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*OCIBaselineBuilderRequest){
		"schema": func(v *OCIBaselineBuilderRequest) { v.SchemaVersion++ }, "full transfer": func(v *OCIBaselineBuilderRequest) { v.TransferMode = "full" }, "legacy transfer": func(v *OCIBaselineBuilderRequest) { v.TransferMode = "accepted_snapshot_delta_only" }, "delta digest": func(v *OCIBaselineBuilderRequest) { v.DeltaArchiveSHA256 = "bad" },
		"parent image": func(v *OCIBaselineBuilderRequest) { v.ParentImage = "repo:latest" }, "ACR parent image": func(v *OCIBaselineBuilderRequest) {
			v.ParentImage = "registry.cn-shenzhen.aliyuncs.com/repo/image@" + digest("b")
		}, "ACR parent image port": func(v *OCIBaselineBuilderRequest) {
			v.ParentImage = "registry.cn-shenzhen.aliyuncs.com:5000/repo/image@" + digest("b")
		}, "ACR output repository": func(v *OCIBaselineBuilderRequest) {
			v.OutputRepository = "registry.cn-shenzhen.aliyuncs.com/repo/image"
		}, "ACR output repository trailing dot": func(v *OCIBaselineBuilderRequest) {
			v.OutputRepository = "registry.cn-shenzhen.aliyuncs.com./repo/image"
		}, "image cache": func(v *OCIBaselineBuilderRequest) { v.ParentImageCacheID = "" }, "tree": func(v *OCIBaselineBuilderRequest) { v.TargetTree = "bad" },
		"platform": func(v *OCIBaselineBuilderRequest) { v.Platform = "darwin/arm64" }, "runtime dependency": func(v *OCIBaselineBuilderRequest) { v.RuntimeDependencyDigest = "bad" },
		"job key": func(v *OCIBaselineBuilderRequest) { v.JobKey = "x.job.json" }, "parent snapshot": func(v *OCIBaselineBuilderRequest) { v.ParentSourceManifest = "bad" }, "source path": func(v *OCIBaselineBuilderRequest) { v.ParentSourceImagePath = "/tmp/source-snapshot/manifest.json" },
	} {
		t.Run(name, func(t *testing.T) {
			request := validOCIBaselineBuilderRequest()
			mutate(&request)
			if err := request.Validate(); err == nil {
				t.Fatal("Validate accepted invalid request")
			}
		})
	}
}

func TestOCIBaselineBuilderResultRejectsInvalidIdentityAndBinding(t *testing.T) {
	request := validOCIBaselineBuilderRequest()
	for name, mutate := range map[string]func(*OCIBaselineBuilderResult){
		"schema": func(v *OCIBaselineBuilderResult) { v.SchemaVersion++ }, "repository": func(v *OCIBaselineBuilderResult) { v.Repository = "Repo" },
		"image": func(v *OCIBaselineBuilderResult) { v.Image = "registry.example/baseline:latest" }, "ACR image": func(v *OCIBaselineBuilderResult) {
			v.Image = "registry.cn-shenzhen.aliyuncs.com/repo/image@" + digest("9")
		}, "ACR image uppercase": func(v *OCIBaselineBuilderResult) {
			v.Image = "REGISTRY.CN-SHENZHEN.ALIYUNCS.COM/repo/image@" + digest("9")
		}, "config": func(v *OCIBaselineBuilderResult) { v.ConfigDigest = "bad" },
		"missing check receipts": func(v *OCIBaselineBuilderResult) { v.RefreshReceipts = nil },
		"forged check receipt":   func(v *OCIBaselineBuilderResult) { v.RefreshReceipts[0].SourceTree = stringsRepeat("f", 40) },
		"target binding":         func(v *OCIBaselineBuilderResult) { v.TargetSourceClosure = digest("e") }, "runtime dependency binding": func(v *OCIBaselineBuilderResult) { v.RuntimeDependencyDigest = digest("e") },
	} {
		t.Run(name, func(t *testing.T) {
			result := validOCIBaselineBuilderResult(request)
			mutate(&result)
			if err := result.Validate(); err == nil && name != "target binding" && name != "runtime dependency binding" {
				t.Fatal("Validate accepted invalid result")
			}
			if err := result.ValidateAgainst(request); err == nil {
				t.Fatal("ValidateAgainst accepted invalid result")
			}
		})
	}
}

func TestOCIBaselineBuilderProtocolStrictJSON(t *testing.T) {
	request := validOCIBaselineBuilderRequest()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{append(data[:len(data)-1], []byte(`,"context_sha256":"legacy"}`)...), append(data, []byte(` {}`)...)} {
		if _, err := DecodeOCIBaselineBuilderRequest(invalid); err == nil {
			t.Fatal("strict request decode accepted invalid JSON")
		}
	}
	resultData, err := json.Marshal(validOCIBaselineBuilderResult(request))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeOCIBaselineBuilderResult(append(resultData[:len(resultData)-1], []byte(`,"input_digest":"legacy"}`)...), request); err == nil {
		t.Fatal("strict result decode accepted legacy input digest")
	}
}

func TestOCIBuilderRefreshReceiptArtifactIsStrictAndRequestBound(t *testing.T) {
	request := validOCIBaselineBuilderRequest()
	artifact := OCIBuilderRefreshReceiptArtifact{
		SchemaVersion:      OCIBuilderRefreshReceiptArtifactSchemaVersion,
		SourceTree:         request.TargetTree,
		AcceptedSnapshotID: request.ParentImageSnapshotID,
		PlanDigest:         request.ImageInputDigest,
		RefreshReceipts:    validRefreshCheckReceipts(request),
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if receipts, err := DecodeOCIBuilderRefreshReceiptArtifact(data, request); err != nil || !reflect.DeepEqual(receipts, artifact.RefreshReceipts) {
		t.Fatalf("DecodeOCIBuilderRefreshReceiptArtifact() = %#v, %v", receipts, err)
	}
	for _, invalid := range [][]byte{
		append(data[:len(data)-1], []byte(`,"unknown":true}`)...),
		append(data, []byte(` {}`)...),
		[]byte(strings.Replace(string(data), `"refresh_receipts"`, `"check_receipts"`, 1)),
	} {
		if _, err := DecodeOCIBuilderRefreshReceiptArtifact(invalid, request); err == nil {
			t.Fatal("strict artifact decode accepted invalid JSON")
		}
	}
	artifact.RefreshReceipts[0].ReceiptSHA256 = digest("0")
	data, err = json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeOCIBuilderRefreshReceiptArtifact(data, request); err == nil {
		t.Fatal("artifact decode accepted a forged receipt digest")
	}
}

func TestOCIBaselineBuilderProtocolFieldRegistry(t *testing.T) {
	assertBaselineFields(t, reflect.TypeFor[OCIBaselineBuilderRequest](), []string{"SchemaVersion", "JobID", "TransferMode", "ParentGeneration", "ParentStateSHA256", "OutputRepository", "ParentImage", "ParentImageCacheID", "ParentImageSnapshotID", "ParentSourceManifest", "ParentSourceImagePath", "ParentSourceClosure", "TargetCommit", "TargetTree", "TargetSourceManifest", "TargetSourceClosure", "ImageInputDigest", "PolicyDigest", "ToolchainDigest", "Platform", "RuntimeDependencyDigest", "DeltaArchiveKey", "DeltaArchiveSHA256", "DeltaArchiveSize", "JobKey"})
	assertBaselineFields(t, reflect.TypeFor[OCIBaselineBuilderResult](), []string{"SchemaVersion", "JobID", "TransferMode", "ParentGeneration", "ParentStateSHA256", "OutputRepository", "ParentImage", "ParentImageCacheID", "ParentImageSnapshotID", "ParentSourceManifest", "ParentSourceImagePath", "ParentSourceClosure", "TargetCommit", "TargetTree", "TargetSourceManifest", "TargetSourceClosure", "ImageInputDigest", "PolicyDigest", "ToolchainDigest", "Platform", "RuntimeDependencyDigest", "DeltaArchiveKey", "DeltaArchiveSHA256", "DeltaArchiveSize", "RefreshReceipts", "JobKey", "Repository", "Image", "ConfigDigest"})
	assertBaselineFields(t, reflect.TypeFor[OCIBuilderRefreshReceiptArtifact](), []string{"SchemaVersion", "SourceTree", "AcceptedSnapshotID", "PlanDigest", "RefreshReceipts"})
}

func validOCIBaselineBuilderRequest() OCIBaselineBuilderRequest {
	jobID := "oci-baseline-123"
	prefix := "remote-ci/oci-baselines/" + jobID + "/"
	return OCIBaselineBuilderRequest{SchemaVersion: OCIBaselineBuilderRequestSchemaVersion, JobID: jobID, TransferMode: cicontract.RefreshTransferAcceptedSnapshotDelta, ParentGeneration: 2, ParentStateSHA256: digest("0"), OutputRepository: "registry.example/super-dolphin/baseline", ParentImage: "registry.example/super-dolphin/parent@" + digest("b"), ParentImageCacheID: "imc-baseline", ParentImageSnapshotID: "snap-baseline", ParentSourceManifest: digest("c"), ParentSourceImagePath: cicontract.SourceSnapshotManifestPath, ParentSourceClosure: digest("d"), TargetCommit: stringsRepeat("c", 40), TargetTree: stringsRepeat("d", 40), TargetSourceManifest: digest("e"), TargetSourceClosure: digest("f"), ImageInputDigest: digest("7"), PolicyDigest: digest("8"), ToolchainDigest: digest("e"), Platform: "linux/amd64", RuntimeDependencyDigest: digest("1"), DeltaArchiveKey: prefix + "source.snapshot.delta.tar", DeltaArchiveSHA256: digest("a"), DeltaArchiveSize: 1024, JobKey: prefix + "build.job.json"}
}

func validOCIBaselineBuilderResult(request OCIBaselineBuilderRequest) OCIBaselineBuilderResult {
	receipts := validRefreshCheckReceipts(request)
	return OCIBaselineBuilderResult{SchemaVersion: OCIBaselineBuilderResultSchemaVersion, JobID: request.JobID, TransferMode: request.TransferMode, ParentGeneration: request.ParentGeneration, ParentStateSHA256: request.ParentStateSHA256, OutputRepository: request.OutputRepository, ParentImage: request.ParentImage, ParentImageCacheID: request.ParentImageCacheID, ParentImageSnapshotID: request.ParentImageSnapshotID, ParentSourceManifest: request.ParentSourceManifest, ParentSourceImagePath: request.ParentSourceImagePath, ParentSourceClosure: request.ParentSourceClosure, TargetCommit: request.TargetCommit, TargetTree: request.TargetTree, TargetSourceManifest: request.TargetSourceManifest, TargetSourceClosure: request.TargetSourceClosure, ImageInputDigest: request.ImageInputDigest, PolicyDigest: request.PolicyDigest, ToolchainDigest: request.ToolchainDigest, Platform: request.Platform, RuntimeDependencyDigest: request.RuntimeDependencyDigest, DeltaArchiveKey: request.DeltaArchiveKey, DeltaArchiveSHA256: request.DeltaArchiveSHA256, DeltaArchiveSize: request.DeltaArchiveSize, RefreshReceipts: receipts, JobKey: request.JobKey, Repository: request.OutputRepository, Image: request.OutputRepository + "@" + digest("9"), ConfigDigest: digest("a")}
}

func validRefreshCheckReceipts(request OCIBaselineBuilderRequest) []cicontract.RefreshCheckObservation {
	receipts := make([]cicontract.RefreshCheckObservation, 0, len(cicontract.RefreshChecks()))
	for _, check := range cicontract.RefreshChecks() {
		receipt := cicontract.RefreshCheckObservation{Check: check, Executed: true, Passed: true, SourceTree: request.TargetTree, AcceptedSnapshotID: request.ParentImageSnapshotID, PlanDigest: request.ImageInputDigest, StartedAtUnixMS: 100, CompletedAtUnixMS: 101, DurationMS: 1, TestBodyNotApplicable: true}
		if check == cicontract.RefreshCheckDependency {
			receipt.CandidateCompileNotApplicable = true
		} else {
			receipt.CandidateCompileMS = 1
		}
		digest, err := cicontract.RefreshCheckObservationReceiptDigest(receipt)
		if err != nil {
			panic(err)
		}
		receipt.ReceiptSHA256 = digest
		receipts = append(receipts, receipt)
	}
	return receipts
}

func stringsRepeat(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
