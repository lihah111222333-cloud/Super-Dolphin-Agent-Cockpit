package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

type recordingBuildKitRunner struct {
	requests []BuildKitBuildRequest
	digest   string
	err      error
}

func (runner *recordingBuildKitRunner) Build(_ context.Context, request BuildKitBuildRequest) (string, error) {
	runner.requests = append(runner.requests, request)
	return runner.digest, runner.err
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
	if !built.Built || len(runner.requests) != 1 {
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
	if reused.ImageDigest != digest("9") {
		t.Fatalf("reused image digest = %q", reused.ImageDigest)
	}
	if len(runner.requests) != 1 {
		t.Fatalf("matching input digest did not reuse immutable accepted image: %+v", reused)
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
	changeEntry(t, entries, "go.mod", "module example.invalid/changed\n")
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

func TestEnsureCandidateRejectsMissingOrDriftedBaseImageArgDefault(t *testing.T) {
	missingDefault := strings.Replace(validCandidateDockerfile(), "ARG GO_IMAGE="+lockedGoImageReference(), "ARG GO_IMAGE", 1)
	assertRejectedDockerfile(t, missingDefault)
	driftedDefault := strings.Replace(validCandidateDockerfile(), lockedGoImageReference(), "golang@"+digest("c"), 1)
	assertRejectedDockerfile(t, driftedDefault)
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
}

func candidateRequest(entries []sourceexport.TreeEntry, acceptedInput string, acceptedImage string) CandidateRequest {
	return CandidateRequest{
		SourceTreeSHA:       strings.Repeat("a", 40),
		SourceEntries:       entries,
		Platform:            "linux/arm64",
		AcceptedInputDigest: acceptedInput,
		AcceptedImageDigest: acceptedImage,
	}
}

func candidateEntries(dockerfile string) []sourceexport.TreeEntry {
	manifest := `{
  "schema_version": "1",
  "dockerfile": "build/gate/Dockerfile",
  "inputs": [
    "build/gate/Dockerfile",
    "build/gate/inputs.json",
    "build/gate/toolchain.lock",
    "cmd/super-dolphin-gate/main.go",
    "go.mod",
    "go.sum"
  ]
}`
	toolchain := `{
  "schema_version": "1",
  "buildkit_version": "v0.26.2",
  "dockerfile_frontend": "builtin:dockerfile.v1",
  "target_platforms": ["linux/arm64"],
  "base_images": [{"name":"GO_IMAGE","reference":"golang@sha256:` + strings.Repeat("b", 64) + `"}],
  "dependency_sources": ["go.sum"],
  "network_policy": "locked-dependencies"
}`
	return []sourceexport.TreeEntry{
		contextEntry("go.sum", "100644", "sum\n"),
		contextEntry("build/gate/toolchain.lock", "100644", toolchain+"\n"),
		contextEntry("cmd/super-dolphin-gate/main.go", "100644", "package main\n"),
		contextEntry("build/gate/Dockerfile", "100644", dockerfile),
		contextEntry("go.mod", "100644", "module example.invalid/gate\n"),
		contextEntry("build/gate/inputs.json", "100644", manifest+"\n"),
	}
}

func validCandidateDockerfile() string {
	return "ARG GO_IMAGE=" + lockedGoImageReference() + "\nFROM ${GO_IMAGE} AS build\nCOPY go.mod go.sum ./\nCOPY cmd/super-dolphin-gate/main.go ./cmd/super-dolphin-gate/main.go\nRUN --network=none go build -o /out/gate ./cmd/super-dolphin-gate\nFROM scratch\nCOPY --from=build /out/gate /gate\nENTRYPOINT [\"/gate\"]\n"
}

func forwardStageDockerfile() string {
	return "ARG GO_IMAGE=" + lockedGoImageReference() + "\nFROM ${GO_IMAGE} AS build\nCOPY --from=later /tool /tool\nFROM scratch AS later\nCOPY --from=build /out/gate /gate\nENTRYPOINT [\"/gate\"]\n"
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
		name := strings.Split(producerType.Field(index).Tag.Get("json"), ",")[0]
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
