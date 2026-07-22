package localci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

type recordingBuildKitRunner struct {
	requests []BuildKitBuildRequest
	digest   string
	config   string
	err      error
}

func (runner *recordingBuildKitRunner) Build(_ context.Context, request BuildKitBuildRequest) (BuildKitResult, error) {
	runner.requests = append(runner.requests, request)
	configDigest := runner.config
	if configDigest == "" {
		configDigest = digest("9")
	}
	return BuildKitResult{PlatformManifestDigest: runner.digest, ConfigDigest: configDigest}, runner.err
}

func TestNewImageBuilderRejectsTypedNilRunner(t *testing.T) {
	var runner *recordingBuildKitRunner
	builder, err := NewImageBuilder(runner)
	if err == nil || builder != nil {
		t.Fatal("typed-nil BuildKit runner was accepted")
	}
}

func TestEnsureCandidateUsesDeterministicInputClosure(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	firstRunner := &recordingBuildKitRunner{digest: digest("8")}
	first := mustEnsureCandidate(t, firstRunner, candidateRequest(entries, digest("f"), digest("e")))

	reversed := append([]sourceexport.TreeEntry(nil), entries...)
	slices.Reverse(reversed)
	secondRunner := &recordingBuildKitRunner{digest: digest("8")}
	second := mustEnsureCandidate(t, secondRunner, candidateRequest(reversed, digest("f"), digest("e")))

	if first.InputDigest != second.InputDigest {
		t.Fatalf("input digest drift after reorder: first=%+v second=%+v", first, second)
	}
	if first.ContextDigest != second.ContextDigest {
		t.Fatalf("context digest drift after reorder: first=%+v second=%+v", first, second)
	}
	if first.ToolchainDigest != second.ToolchainDigest {
		t.Fatalf("digest drift after reorder: first=%+v second=%+v", first, second)
	}
	if len(firstRunner.requests) != 1 {
		t.Fatal("first deterministic candidate did not invoke its runner exactly once")
	}
	if len(secondRunner.requests) != 1 {
		t.Fatal("deterministic candidate builds did not invoke each runner exactly once")
	}
	if !bytes.Equal(firstRunner.requests[0].ContextTar, secondRunner.requests[0].ContextTar) {
		t.Fatal("canonical build context changed after source entry reorder")
	}
}

func TestPrepareCandidateSelectsRuntimeDepsImageForRequestedPlatform(t *testing.T) {
	for _, test := range []struct {
		platform string
		digest   string
	}{
		{platform: "linux/amd64", digest: digest("2")},
		{platform: "linux/arm64", digest: digest("5")},
	} {
		t.Run(test.platform, func(t *testing.T) {
			request := candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e"))
			request.Platform = test.platform
			prepared, err := prepareCandidate(request)
			if err != nil {
				t.Fatal(err)
			}
			for _, argument := range prepared.buildRequest.BuildArguments {
				if argument.Name == "RUNTIME_DEPS_IMAGE" {
					want := "ghcr.io/super-dolphin/runtime-deps@" + test.digest
					if argument.Value != want {
						t.Fatalf("runtime dependency image = %q, want %q", argument.Value, want)
					}
					return
				}
			}
			t.Fatal("BuildKit request omitted the requested platform runtime dependency image")
		})
	}
}

func TestEnsureCandidateSeparatesSourceTreeProvenanceFromInputDigest(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	firstRunner := &recordingBuildKitRunner{digest: digest("8")}
	firstRequest := candidateRequest(entries, digest("f"), digest("e"))
	first := mustEnsureCandidate(t, firstRunner, firstRequest)

	secondRunner := &recordingBuildKitRunner{digest: digest("9")}
	secondRequest := candidateRequest(entries, digest("f"), digest("e"))
	secondRequest.SourceTreeSHA = strings.Repeat("c", 40)
	second := mustEnsureCandidate(t, secondRunner, secondRequest)

	if first.InputDigest != second.InputDigest {
		t.Fatal("source tree provenance changed the image input digest")
	}
	if first.ContextDigest != second.ContextDigest {
		t.Fatal("source tree provenance changed the canonical context digest")
	}
	if first.SourceTreeSHA != firstRequest.SourceTreeSHA || second.SourceTreeSHA != secondRequest.SourceTreeSHA {
		t.Fatal("candidate result lost source tree provenance")
	}
	if firstRunner.requests[0].SourceTreeSHA != firstRequest.SourceTreeSHA {
		t.Fatal("first BuildKit request lost source tree provenance")
	}
	if secondRunner.requests[0].SourceTreeSHA != secondRequest.SourceTreeSHA {
		t.Fatal("second BuildKit request lost source tree provenance")
	}
}

func TestCandidatePolicyAndSchemaBindImageInput(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	firstRequest := candidateRequest(entries, digest("f"), digest("e"))
	first, err := prepareCandidate(firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := candidateRequest(entries, digest("f"), digest("e"))
	secondRequest.PolicyDigest = digest("c")
	second, err := prepareCandidate(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.result.InputDigest == second.result.InputDigest {
		t.Fatal("policy digest did not change the candidate image input digest")
	}
	if first.buildRequest.PolicyDigest != firstRequest.PolicyDigest || first.buildRequest.ImageSchemaVersion != imageInputSchemaVersion {
		t.Fatalf("BuildKit request lost policy/schema binding: %#v", first.buildRequest)
	}
	firstRequest.ImageSchemaVersion = ""
	if _, err := prepareCandidate(firstRequest); err == nil {
		t.Fatal("candidate accepted a missing image schema version")
	}
}

func TestCandidateAndBuildKitFieldRegistriesAreComplete(t *testing.T) {
	assertRegisteredFields(t, reflect.TypeFor[CandidateRequest](), map[string]string{
		"SourceTreeSHA":      "source provenance",
		"PolicyDigest":       "input digest and image label",
		"ImageSchemaVersion": "input digest and image label", "SourceEntries": "canonical context",
		"Platform": "input digest and platform", "AcceptedInputDigest": "reuse decision",
		"AcceptedPolicyDigest": "policy reuse decision",
		"AcceptedImageDigest":  "reuse result",
		"AcceptedConfigDigest": "reuse result",
	})
	assertRegisteredFields(t, reflect.TypeFor[BuildKitBuildRequest](), map[string]string{
		"SourceTreeSHA": "source label", "PolicyDigest": "policy label", "ImageSchemaVersion": "schema label",
		"ContextTar": "build stdin", "ContextDigest": "context validation and label",
		"InputManifestDigest": "provenance digest", "InputDigest": "tag cache and label",
		"ToolchainDigest": "toolchain label", "DockerfilePath": "build file",
		"DockerfileDigest": "Dockerfile label", "Platform": "build platform and label",
		"BuildKitImage": "immutable builder image and identity binding", "BuildKitVersion": "builder binding",
		"DockerfileFrontend": "frontend binding",
		"BuildArguments":     "locked toolchain arguments", "NetworkPolicy": "network contract",
		"CacheNamespace": "isolated cache",
	})
}

func TestEnsureCandidateBuildsOnlyWhenInputDigestChanges(t *testing.T) {
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	entries := candidateEntries(validCandidateDockerfile())
	built, err := builder.EnsureCandidate(context.Background(), candidateRequest(entries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	if !built.Built || built.ImageDigest != digest("8") || built.ConfigDigest != digest("9") || len(runner.requests) != 1 {
		t.Fatal("changed input digest did not trigger one candidate build")
	}
}

func TestEnsureCandidateReusesMatchingInputDigest(t *testing.T) {
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	entries := candidateEntries(validCandidateDockerfile())
	built, err := builder.EnsureCandidate(context.Background(), candidateRequest(entries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	reused, err := builder.EnsureCandidate(context.Background(), candidateRequest(entries, built.InputDigest, digest("9")))
	if err != nil {
		t.Fatal(err)
	}
	if reused.Built {
		t.Fatal("matching input digest started a candidate build")
	}
	if reused.ImageDigest != digest("9") || reused.ConfigDigest != digest("9") {
		t.Fatalf("reused image identity = %#v", reused)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("matching input digest did not reuse immutable accepted image: %+v", reused)
	}
}

func TestEnsureCandidateBuildsWhenOnlyPolicyDigestChanges(t *testing.T) {
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	request := candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e"))
	prepared, err := prepareCandidate(request)
	if err != nil {
		t.Fatal(err)
	}
	request.AcceptedInputDigest = prepared.result.InputDigest
	request.AcceptedPolicyDigest = digest("c")
	result, err := builder.EnsureCandidate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Built || len(runner.requests) != 1 {
		t.Fatalf("policy-only change did not build candidate: result=%+v builds=%d", result, len(runner.requests))
	}
}

func TestEnsureCandidateRejectsCanceledContextBeforeCacheReuse(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	runner := &recordingBuildKitRunner{digest: digest("8")}
	built := mustEnsureCandidate(t, runner, candidateRequest(entries, digest("f"), digest("e")))
	runner.requests = nil
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = builder.EnsureCandidate(ctx, candidateRequest(entries, built.InputDigest, digest("9")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled candidate context error = %v", err)
	}
	if len(runner.requests) != 0 {
		t.Fatal("canceled candidate request reached BuildKit")
	}
}

func TestEnsureCandidateRebuildsChangedSource(t *testing.T) {
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	entries := candidateEntries(validCandidateDockerfile())
	built, err := builder.EnsureCandidate(context.Background(), candidateRequest(entries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	changeCandidateInput(t, entries, "go.mod", "module example.invalid/changed\n")
	changed, err := builder.EnsureCandidate(context.Background(), candidateRequest(entries, built.InputDigest, digest("9")))
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Built {
		t.Fatal("source input change did not build")
	}
	if changed.InputDigest == built.InputDigest {
		t.Fatal("source input change did not change input digest")
	}
	if len(runner.requests) != 2 {
		t.Fatal("source input change did not trigger a new candidate build")
	}
}

func TestEnsureCandidateRejectsForbiddenBuildCapabilities(t *testing.T) {
	assertForbiddenBuildCapability(t, "RUN --mount=type=secret echo denied")
	assertForbiddenBuildCapability(t, "RUN --mount=type=ssh echo denied")
	assertForbiddenBuildCapability(t, "RUN --network=host echo denied")
	assertForbiddenBuildCapability(t, "RUN --security=insecure echo denied")
	assertForbiddenBuildCapability(t, "RUN test ! -e /var/run/docker.sock")
}

func TestEnsureCandidateRejectsUndeclaredCopyAndMutableOutput(t *testing.T) {
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	entries := append(candidateEntries(validCandidateDockerfile()+"COPY hidden.txt /tmp/hidden.txt\n"), contextEntry("hidden.txt", "100644", "hidden\n"))
	if _, err := builder.EnsureCandidate(context.Background(), candidateRequest(entries, digest("f"), digest("e"))); err == nil {
		t.Fatal("EnsureCandidate() accepted COPY source outside the declared closure")
	}
	if len(runner.requests) != 0 {
		t.Fatal("BuildKit runner was called for an undeclared COPY source")
	}

	runner.digest = "candidate:latest"
	if _, err := builder.EnsureCandidate(context.Background(), candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e"))); err == nil {
		t.Fatal("EnsureCandidate() accepted mutable BuildKit output")
	}
}

func TestEnsureCandidateRejectsExternalAndUnknownCopyFrom(t *testing.T) {
	assertForbiddenCopyFrom(t, "alpine:latest")
	assertForbiddenCopyFrom(t, "alpine@"+digest("a"))
	assertForbiddenCopyFrom(t, "missing-stage")
	assertRejectedDockerfile(t, forwardStageDockerfile())
}

func TestEnsureCandidateRejectsMissingManifestField(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	replaceEntryText(t, entries, buildInputManifestPath, "  \"schema_version\": \"1\",\n", "")
	assertCandidateRejectedBeforeBuild(t, entries)
}

func TestEnsureCandidateRejectsUnknownManifestField(t *testing.T) {
	entries := candidateEntries(validCandidateDockerfile())
	replaceEntryText(t, entries, buildInputManifestPath, "\n}\n", ",\n  \"unknown\": true\n}\n")
	assertCandidateRejectedBeforeBuild(t, entries)
}

func TestEnsureCandidateRejectsStaticRuntimeDepsImageArgDefault(t *testing.T) {
	staticDefault := strings.Replace(validCandidateDockerfile(), "ARG RUNTIME_DEPS_IMAGE\n", "ARG RUNTIME_DEPS_IMAGE=ghcr.io/super-dolphin/runtime-deps@"+digest("c")+"\n", 1)
	assertRejectedDockerfile(t, staticDefault)
}

func TestEnsureCandidateRejectsMissingOrDriftedSourceDateEpoch(t *testing.T) {
	missingDefault := strings.Replace(validCandidateDockerfile(), "ARG SOURCE_DATE_EPOCH=0", "ARG SOURCE_DATE_EPOCH", 1)
	assertRejectedDockerfile(t, missingDefault)
	driftedDefault := strings.Replace(validCandidateDockerfile(), "ARG SOURCE_DATE_EPOCH=0", "ARG SOURCE_DATE_EPOCH=1", 1)
	assertRejectedDockerfile(t, driftedDefault)

	missingLock := candidateEntries(validCandidateDockerfile())
	replaceCandidateInputText(t, missingLock, toolchainLockPath, "  \"source_date_epoch\": \"0\",\n", "")
	assertCandidateRejectedBeforeBuild(t, missingLock)
	nonCanonicalLock := candidateEntries(validCandidateDockerfile())
	replaceCandidateInputText(t, nonCanonicalLock, toolchainLockPath, "\"source_date_epoch\": \"0\"", "\"source_date_epoch\": \"00\"")
	assertCandidateRejectedBeforeBuild(t, nonCanonicalLock)
}

func TestEnsureCandidateRejectsMissingDriftedOrUnclosedRuntimeDepsLock(t *testing.T) {
	missingField := candidateEntries(validCandidateDockerfile())
	replaceCandidateInputText(t, missingField, toolchainLockPath, "  \"runtime_deps_lock\": \"build/gate/runtime-deps.lock\",\n", "")
	assertCandidateRejectedBeforeBuild(t, missingField)

	driftedPath := candidateEntries(validCandidateDockerfile())
	replaceCandidateInputText(t, driftedPath, toolchainLockPath, runtimeDepsLockPath, "build/gate/runtime-deps.other.lock")
	assertCandidateRejectedBeforeBuild(t, driftedPath)

	outsideClosure := candidateEntries(validCandidateDockerfile())
	outsideClosure = slices.DeleteFunc(outsideClosure, func(entry sourceexport.TreeEntry) bool {
		return entry.Path == runtimeDepsLockPath
	})
	assertCandidateRejectedBeforeBuild(t, outsideClosure)
}

func TestPrepareCandidateAcceptsRuntimeDepsSchema3Closure(t *testing.T) {
	if _, err := prepareCandidate(candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e"))); err != nil {
		t.Fatalf("schema3 runtime dependency closure: %v", err)
	}
}

func TestPrepareCandidateRejectsRuntimeDepsCrossPlatformIdentityTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, map[string]any)
	}{
		{name: "authenticated pull policy", mutate: func(_ *testing.T, lock map[string]any) { lock["registry_pull_policy"] = "authenticated" }},
		{name: "private DNS registry", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 0)["registry"] = "runtime-deps.corp.internal/runtime-deps"
		}},
		{name: "public non-GHCR registry", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 0)["registry"] = "registry.example.com/runtime-deps"
		}},
		{name: "explicit registry port", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 0)["registry"] = "ghcr.io:443/runtime-deps"
		}},
		{name: "uppercase registry host", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 0)["registry"] = "GHCR.io/runtime-deps"
		}},
		{name: "trailing-dot registry host", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 0)["registry"] = "ghcr.io./runtime-deps"
		}},
		{name: "repository mismatch", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 1)["registry"] = "registry.example.invalid/other"
		}},
		{name: "index mismatch", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 1)["oci_index_digest"] = digest("8")
		}},
		{name: "duplicate manifest", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 1)["platform_manifest_digest"] = runtimeDepsLockImage(t, lock, 0)["platform_manifest_digest"]
		}},
		{name: "duplicate config", mutate: func(t *testing.T, lock map[string]any) {
			runtimeDepsLockImage(t, lock, 1)["config_digest"] = runtimeDepsLockImage(t, lock, 0)["config_digest"]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := candidateEntries(validCandidateDockerfile())
			mutateRuntimeDepsLockDocument(t, entries, func(lock map[string]any) { test.mutate(t, lock) })
			assertCandidateRejectedBeforeBuild(t, entries)
		})
	}
}

func TestPrepareCandidateRejectsLegacyIncompleteAndDriftedRuntimeDepsInputs(t *testing.T) {
	legacy := candidateEntries(validCandidateDockerfile())
	replaceEntryText(t, legacy, runtimeDepsLockPath, `"schema_version":"3"`, `"schema_version":"1"`)
	assertCandidateRejectedBeforeBuild(t, legacy)

	missingGoMod := candidateEntries(validCandidateDockerfile())
	mutateRuntimeDepsLock(t, missingGoMod, func(inputs map[string]any) { delete(inputs, "go_mod_sha256") })
	assertCandidateRejectedBeforeBuild(t, missingGoMod)

	unknownInput := candidateEntries(validCandidateDockerfile())
	mutateRuntimeDepsLock(t, unknownInput, func(inputs map[string]any) { inputs["ignored_sha256"] = digest("9") })
	assertCandidateRejectedBeforeBuild(t, unknownInput)

	driftedNilnessRunner := candidateEntries(validCandidateDockerfile())
	changeEntry(t, driftedNilnessRunner, "internal/devtools/nilnessrunner/runner.go", "package nilnessrunner\n// drift\n")
	assertCandidateRejectedBeforeBuild(t, driftedNilnessRunner)

	driftedNilnessGuard := candidateEntries(validCandidateDockerfile())
	changeEntry(t, driftedNilnessGuard, "scripts/nilness_guard.go", "package main\n// drift\n")
	assertCandidateRejectedBeforeBuild(t, driftedNilnessGuard)
}

func TestEnsureCandidateRejectsMissingOrDriftedSqruffArtifactLock(t *testing.T) {
	missingDigest := candidateEntries(validCandidateDockerfile())
	replaceCandidateInputText(t, missingDigest, toolchainLockPath, "d96a06daca2a214eb0b6c07b2821e9cdb1379086041bcca6f8bab031b6eb8026", "")
	assertCandidateRejectedBeforeBuild(t, missingDigest)

	driftedURL := candidateEntries(validCandidateDockerfile())
	replaceCandidateInputText(t, driftedURL, toolchainLockPath, "/releases/download/v0.38.0/", "/releases/latest/download/")
	assertCandidateRejectedBeforeBuild(t, driftedURL)
}

func TestSourceDateEpochBindsCandidateInputAndBuildRequest(t *testing.T) {
	first, err := prepareCandidate(candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	changedEntries := candidateEntries(strings.Replace(validCandidateDockerfile(), "ARG SOURCE_DATE_EPOCH=0", "ARG SOURCE_DATE_EPOCH=1", 1))
	replaceCandidateInputText(t, changedEntries, toolchainLockPath, "\"source_date_epoch\": \"0\"", "\"source_date_epoch\": \"1\"")
	second, err := prepareCandidate(candidateRequest(changedEntries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	if first.result.InputDigest == second.result.InputDigest {
		t.Fatal("source_date_epoch change did not invalidate candidate input digest")
	}
	wanted := BuildArgument{Name: sourceDateEpochArgument, Value: "1"}
	if !slices.Contains(second.buildRequest.BuildArguments, wanted) {
		t.Fatalf("BuildKit request lost source date epoch: %+v", second.buildRequest.BuildArguments)
	}
}

func TestBuildKitImageBindsCandidateInputAndBuildRequest(t *testing.T) {
	first, err := prepareCandidate(candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	changedEntries := candidateEntries(validCandidateDockerfile())
	firstImage := "docker.io/moby/buildkit@sha256:" + strings.Repeat("c", 64)
	secondImage := "docker.io/moby/buildkit@sha256:" + strings.Repeat("d", 64)
	replaceCandidateInputText(t, changedEntries, toolchainLockPath, firstImage, secondImage)
	second, err := prepareCandidate(candidateRequest(changedEntries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	if first.result.InputDigest == second.result.InputDigest {
		t.Fatal("BuildKit image change did not invalidate candidate input digest")
	}
	if second.buildRequest.BuildKitImage != secondImage {
		t.Fatalf("BuildKit request image = %q, want %q", second.buildRequest.BuildKitImage, secondImage)
	}
}

func TestLockedImageArgumentDefaultsRejectsArgAfterTabSeparatedFrom(t *testing.T) {
	lines := []string{
		"FROM\t${GO_IMAGE} AS build",
		"ARG GO_IMAGE=" + lockedGoImageReference(),
	}
	err := validateLockedImageArgumentDefaults(lines, map[string]string{"GO_IMAGE": lockedGoImageReference()})
	if err == nil {
		t.Fatal("locked image ARG declared after tab-separated FROM was accepted")
	}
}

func TestTrackedBuildConfigurationMatchesProducerFields(t *testing.T) {
	manifestData := readRepoFile(t, buildInputManifestPath)
	assertJSONFieldsMatchProducer(t, manifestData, buildInputManifest{})
	lockData := readRepoFile(t, toolchainLockPath)
	assertJSONFieldsMatchProducer(t, lockData, toolchainLock{})

	var rawLock map[string]json.RawMessage
	if err := json.Unmarshal(lockData, &rawLock); err != nil {
		t.Fatal(err)
	}
	var baseImages []json.RawMessage
	if err := json.Unmarshal(rawLock["base_images"], &baseImages); err != nil {
		t.Fatal(err)
	}
	if len(baseImages) == 0 {
		t.Fatal("tracked toolchain lock has no base image producer")
	}
	assertJSONFieldsMatchProducer(t, baseImages[0], lockedBaseImage{})
	assertJSONFieldsMatchProducer(t, rawLock["runtime_tools"], lockedRuntimeTools{})

	runtimeLockData := readRepoFile(t, runtimeDepsLockPath)
	assertJSONFieldsMatchProducer(t, runtimeLockData, runtimeDepsLock{})
	var rawRuntimeLock map[string]json.RawMessage
	if err := json.Unmarshal(runtimeLockData, &rawRuntimeLock); err != nil {
		t.Fatal(err)
	}
	assertJSONFieldsMatchProducer(t, rawRuntimeLock["inputs"], runtimeDepsInputs{})
	assertJSONFieldsMatchProducer(t, rawRuntimeLock["paths"], runtimeDepsPaths{})
}

func candidateRequest(entries []sourceexport.TreeEntry, acceptedInput string, acceptedImage string) CandidateRequest {
	return CandidateRequest{
		SourceTreeSHA:        strings.Repeat("a", 40),
		PolicyDigest:         digest("d"),
		ImageSchemaVersion:   imageInputSchemaVersion,
		SourceEntries:        entries,
		Platform:             "linux/arm64",
		AcceptedInputDigest:  acceptedInput,
		AcceptedPolicyDigest: digest("d"),
		AcceptedImageDigest:  acceptedImage,
		AcceptedConfigDigest: digest("9"),
	}
}

func testRuntimeDepsLock(files map[string]string) string {
	lock := runtimeDepsLock{
		SchemaVersion: "3", RegistryPullPolicy: "anonymous",
		Images: []runtimeDepsImage{
			{Platform: "linux/amd64", Image: gate.ImageIdentity{Registry: "ghcr.io/super-dolphin/runtime-deps", OCIIndexDigest: digest("1"), PlatformManifestDigest: digest("2"), ConfigDigest: digest("3"), RootFSDiffIDs: []string{digest("4")}, OS: "linux", Architecture: "amd64"}, ImageSize: 1},
			{Platform: "linux/arm64", Image: gate.ImageIdentity{Registry: "ghcr.io/super-dolphin/runtime-deps", OCIIndexDigest: digest("1"), PlatformManifestDigest: digest("5"), ConfigDigest: digest("6"), RootFSDiffIDs: []string{digest("7")}, OS: "linux", Architecture: "arm64"}, ImageSize: 1},
		},
		Inputs: runtimeDepsInputs{
			Dockerfile:    contentDigest(files["build/gate/runtime-deps.Dockerfile"]),
			ToolchainLock: contentDigest(files["build/gate/toolchain.lock"]),
			GoMod:         contentDigest(files["go.mod"]), GoSum: contentDigest(files["go.sum"]),
			NilnessRunner:       contentDigest(files["internal/devtools/nilnessrunner/runner.go"]),
			NilnessGuard:        contentDigest(files["scripts/nilness_guard.go"]),
			FrontendPackageLock: contentDigest(files["frontend-app/package-lock.json"]),
			LSPPackageLock:      contentDigest(files["build/gate/runtime-lsp/package-lock.json"]),
			ProxyGoMod:          contentDigest(files["build/gate/runtime-proxy/go.mod"]),
			ProxyGoSum:          contentDigest(files["build/gate/runtime-proxy/go.sum"]),
			ToolsGoMod:          contentDigest(files["build/gate/runtime-tools/go.mod"]),
			ToolsGoSum:          contentDigest(files["build/gate/runtime-tools/go.sum"]),
			ManifestBuilder:     contentDigest(files["build/gate/cmd/runtime-seed-manifest/main.go"]),
			ManifestAPI:         contentDigest(files["internal/devtools/gate/executor_seed.go"]),
		},
		Paths: canonicalRuntimeDepsPaths(),
	}
	data, _ := json.Marshal(lock)
	return string(data) + "\n"
}

func runtimeDepsTestInputPaths() []string {
	return []string{
		"build/gate/runtime-deps.Dockerfile", "build/gate/toolchain.lock", "go.mod", "go.sum",
		"internal/devtools/nilnessrunner/runner.go", "scripts/nilness_guard.go",
		"frontend-app/package-lock.json", "build/gate/runtime-lsp/package-lock.json",
		"build/gate/runtime-proxy/go.mod", "build/gate/runtime-proxy/go.sum",
		"build/gate/runtime-tools/go.mod", "build/gate/runtime-tools/go.sum",
		"build/gate/cmd/runtime-seed-manifest/main.go", "internal/devtools/gate/executor_seed.go",
	}
}

func contentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validCandidateDockerfile() string {
	return "ARG RUNTIME_DEPS_IMAGE\nARG SOURCE_DATE_EPOCH=0\nFROM ${RUNTIME_DEPS_IMAGE} AS build\nUSER root\nCOPY go.mod go.sum ./\nCOPY cmd/super-dolphin-gate/main.go ./cmd/super-dolphin-gate/main.go\nRUN --network=none go build -o /out/gate ./cmd/super-dolphin-gate\nFROM scratch\nCOPY --from=build /out/gate /gate\nENTRYPOINT [\"/gate\"]\n"
}

func forwardStageDockerfile() string {
	return "ARG RUNTIME_DEPS_IMAGE\nARG SOURCE_DATE_EPOCH=0\nFROM ${RUNTIME_DEPS_IMAGE} AS build\nCOPY --from=later /tool /tool\nFROM scratch AS later\nCOPY --from=build /out/gate /gate\nENTRYPOINT [\"/gate\"]\n"
}

func lockedGoImageReference() string {
	return "golang@" + digest("b")
}

func assertForbiddenBuildCapability(t *testing.T, instruction string) {
	t.Helper()
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := validCandidateDockerfile() + instruction + "\n"
	_, err = builder.EnsureCandidate(context.Background(), candidateRequest(candidateEntries(dockerfile), digest("f"), digest("e")))
	if err == nil || len(runner.requests) != 0 {
		t.Fatalf("EnsureCandidate() accepted forbidden capability %q", instruction)
	}
}

func assertForbiddenCopyFrom(t *testing.T, reference string) {
	t.Helper()
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := validCandidateDockerfile() + "COPY --from=" + reference + " /bin/tool /bin/tool\n"
	_, err = builder.EnsureCandidate(context.Background(), candidateRequest(candidateEntries(dockerfile), digest("f"), digest("e")))
	if err == nil || len(runner.requests) != 0 {
		t.Fatalf("EnsureCandidate() accepted external or unknown COPY --from=%s", reference)
	}
}

func assertCandidateRejectedBeforeBuild(t *testing.T, entries []sourceexport.TreeEntry) {
	t.Helper()
	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = builder.EnsureCandidate(context.Background(), candidateRequest(entries, digest("f"), digest("e")))
	if err == nil || len(runner.requests) != 0 {
		t.Fatal("invalid build configuration reached BuildKit")
	}
}

func assertRejectedDockerfile(t *testing.T, dockerfile string) {
	t.Helper()
	assertCandidateRejectedBeforeBuild(t, candidateEntries(dockerfile))
}

func mustEnsureCandidate(t *testing.T, runner BuildKitRunner, request CandidateRequest) CandidateResult {
	t.Helper()
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	result, err := builder.EnsureCandidate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func changeEntry(t *testing.T, entries []sourceexport.TreeEntry, name string, content string) {
	t.Helper()
	for index := range entries {
		if entries[index].Path != name {
			continue
		}
		entries[index] = contextEntry(name, entries[index].Mode, content)
		return
	}
	t.Fatalf("entry %q not found", name)
}

func changeCandidateInput(t *testing.T, entries []sourceexport.TreeEntry, name string, content string) {
	t.Helper()
	changeEntry(t, entries, name, content)
	refreshRuntimeDepsLock(t, entries)
}

func replaceCandidateInputText(t *testing.T, entries []sourceexport.TreeEntry, name string, oldText string, newText string) {
	t.Helper()
	replaceEntryText(t, entries, name, oldText, newText)
	refreshRuntimeDepsLock(t, entries)
}

func refreshRuntimeDepsLock(t *testing.T, entries []sourceexport.TreeEntry) {
	t.Helper()
	files := make(map[string]string, len(runtimeDepsTestInputPaths()))
	for _, path := range runtimeDepsTestInputPaths() {
		files[path] = string(candidateEntry(t, entries, path).Data)
	}
	changeEntry(t, entries, runtimeDepsLockPath, testRuntimeDepsLock(files))
}

func candidateEntry(t *testing.T, entries []sourceexport.TreeEntry, name string) sourceexport.TreeEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.Path == name {
			return entry
		}
	}
	t.Fatalf("entry %q not found", name)
	return sourceexport.TreeEntry{}
}

func mutateRuntimeDepsLock(t *testing.T, entries []sourceexport.TreeEntry, mutate func(map[string]any)) {
	t.Helper()
	mutateRuntimeDepsLockDocument(t, entries, func(document map[string]any) {
		inputs, ok := document["inputs"].(map[string]any)
		if !ok {
			t.Fatal("runtime dependency lock inputs are not an object")
		}
		mutate(inputs)
	})
}

func mutateRuntimeDepsLockDocument(t *testing.T, entries []sourceexport.TreeEntry, mutate func(map[string]any)) {
	t.Helper()
	for _, entry := range entries {
		if entry.Path != runtimeDepsLockPath {
			continue
		}
		var document map[string]any
		if err := json.Unmarshal(entry.Data, &document); err != nil {
			t.Fatal(err)
		}
		mutate(document)
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		changeEntry(t, entries, runtimeDepsLockPath, string(data)+"\n")
		return
	}
	t.Fatal("runtime dependency lock fixture is missing")
}

func runtimeDepsLockImage(t *testing.T, document map[string]any, index int) map[string]any {
	t.Helper()
	images, ok := document["images"].([]any)
	if !ok || index < 0 || index >= len(images) {
		t.Fatal("runtime dependency lock images are invalid")
	}
	entry, ok := images[index].(map[string]any)
	if !ok {
		t.Fatal("runtime dependency lock image entry is invalid")
	}
	identity, ok := entry["image"].(map[string]any)
	if !ok {
		t.Fatal("runtime dependency image identity is invalid")
	}
	return identity
}

func replaceEntryText(t *testing.T, entries []sourceexport.TreeEntry, name string, oldText string, newText string) {
	t.Helper()
	for index := range entries {
		if entries[index].Path != name {
			continue
		}
		content := strings.Replace(string(entries[index].Data), oldText, newText, 1)
		if content == string(entries[index].Data) {
			t.Fatalf("entry %q does not contain replacement source", name)
		}
		entries[index] = contextEntry(name, entries[index].Mode, content)
		return
	}
	t.Fatalf("entry %q not found", name)
}

func readRepoFile(t *testing.T, relativePath string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", relativePath))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertJSONFieldsMatchProducer(t *testing.T, data []byte, producer any) {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	producerType := reflect.TypeOf(producer)
	wanted := make([]string, 0, producerType.NumField())
	for index := 0; index < producerType.NumField(); index++ {
		name, _, _ := strings.Cut(producerType.Field(index).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			wanted = append(wanted, name)
		}
	}
	actual := make([]string, 0, len(document))
	for name := range document {
		actual = append(actual, name)
	}
	sort.Strings(wanted)
	sort.Strings(actual)
	if !slices.Equal(actual, wanted) {
		t.Fatalf("JSON fields = %v, producer fields = %v", actual, wanted)
	}
}
