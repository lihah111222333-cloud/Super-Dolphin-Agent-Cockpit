package remoteci

import (
	"encoding/json"
	"reflect"
	"testing"
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
		"schema": func(v *OCIBaselineBuilderRequest) { v.SchemaVersion++ }, "context digest": func(v *OCIBaselineBuilderRequest) { v.ContextSHA256 = "bad" },
		"parent image": func(v *OCIBaselineBuilderRequest) { v.ParentImage = "repo:latest" }, "tree": func(v *OCIBaselineBuilderRequest) { v.MainTree = "bad" },
		"platform": func(v *OCIBaselineBuilderRequest) { v.Platform = "darwin/arm64" }, "runtime dependency": func(v *OCIBaselineBuilderRequest) { v.RuntimeDependencyDigest = "bad" },
		"job key": func(v *OCIBaselineBuilderRequest) { v.JobKey = "x.job.json" },
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
		"image": func(v *OCIBaselineBuilderResult) { v.Image = "registry.example/baseline:latest" }, "config": func(v *OCIBaselineBuilderResult) { v.ConfigDigest = "bad" },
		"input":           func(v *OCIBaselineBuilderResult) { v.InputDigest = "bad" },
		"context binding": func(v *OCIBaselineBuilderResult) { v.ContextSHA256 = digest("e") }, "runtime dependency binding": func(v *OCIBaselineBuilderResult) { v.RuntimeDependencyDigest = digest("e") },
	} {
		t.Run(name, func(t *testing.T) {
			result := validOCIBaselineBuilderResult(request)
			mutate(&result)
			if err := result.Validate(); err == nil && name != "context binding" && name != "runtime dependency binding" {
				t.Fatal("Validate accepted invalid result")
			}
			if err := result.ValidateAgainst(request); err == nil {
				t.Fatal("ValidateAgainst accepted invalid result")
			}
		})
	}
}

func TestOCIBaselineBuilderProtocolStrictJSON(t *testing.T) {
	data, err := json.Marshal(validOCIBaselineBuilderRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{append(data[:len(data)-1], []byte(`,"unknown":true}`)...), append(data, []byte(` {}`)...)} {
		if _, err := DecodeOCIBaselineBuilderRequest(invalid); err == nil {
			t.Fatal("strict request decode accepted invalid JSON")
		}
	}
}

func TestOCIBaselineBuilderProtocolFieldRegistry(t *testing.T) {
	assertBaselineFields(t, reflect.TypeFor[OCIBaselineBuilderRequest](), []string{"SchemaVersion", "JobID", "ContextKey", "ContextSHA256", "SourceArchiveSize", "RegistryRepository", "ACRInstanceID", "ACRRegionID", "ParentImage", "MainCommit", "MainTree", "ToolchainDigest", "Platform", "RuntimeDependencyDigest", "JobKey"})
	assertBaselineFields(t, reflect.TypeFor[OCIBaselineBuilderResult](), []string{"SchemaVersion", "JobID", "ContextKey", "ContextSHA256", "RegistryRepository", "ACRInstanceID", "ACRRegionID", "ParentImage", "MainCommit", "MainTree", "ToolchainDigest", "Platform", "RuntimeDependencyDigest", "JobKey", "Repository", "Image", "ConfigDigest", "InputDigest"})
}

func validOCIBaselineBuilderRequest() OCIBaselineBuilderRequest {
	jobID := "oci-baseline-123"
	prefix := "remote-ci/oci-baselines/" + jobID + "/"
	return OCIBaselineBuilderRequest{SchemaVersion: OCIBaselineBuilderRequestSchemaVersion, JobID: jobID, ContextKey: prefix + "source.context.tar", ContextSHA256: digest("a"), SourceArchiveSize: 1024, RegistryRepository: "registry.example/super-dolphin/baseline", ACRInstanceID: "cri-123", ACRRegionID: "cn-shenzhen", ParentImage: "registry.example/super-dolphin/parent@" + digest("b"), MainCommit: stringsRepeat("c", 40), MainTree: stringsRepeat("d", 40), ToolchainDigest: digest("e"), Platform: "linux/amd64", RuntimeDependencyDigest: digest("1"), JobKey: prefix + "build.job.json"}
}

func validOCIBaselineBuilderResult(request OCIBaselineBuilderRequest) OCIBaselineBuilderResult {
	return OCIBaselineBuilderResult{SchemaVersion: OCIBaselineBuilderResultSchemaVersion, JobID: request.JobID, ContextKey: request.ContextKey, ContextSHA256: request.ContextSHA256, RegistryRepository: request.RegistryRepository, ACRInstanceID: request.ACRInstanceID, ACRRegionID: request.ACRRegionID, ParentImage: request.ParentImage, MainCommit: request.MainCommit, MainTree: request.MainTree, ToolchainDigest: request.ToolchainDigest, Platform: request.Platform, RuntimeDependencyDigest: request.RuntimeDependencyDigest, JobKey: request.JobKey, Repository: request.RegistryRepository, Image: request.RegistryRepository + "@" + digest("9"), ConfigDigest: digest("a"), InputDigest: request.ContextSHA256}
}

func stringsRepeat(value string, count int) string {
	result := ""
	for range count {
		result += value
	}
	return result
}
