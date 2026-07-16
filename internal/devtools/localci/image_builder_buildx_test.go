package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

type buildxCommandCall struct {
	args  []string
	stdin []byte
}

type recordingBuildxCommandExecutor struct {
	calls    []buildxCommandCall
	metadata []byte
	output   string
	err      error
	path     string
}

func (executor *recordingBuildxCommandExecutor) Run(_ context.Context, stdin io.Reader, args ...string) (string, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	executor.calls = append(executor.calls, buildxCommandCall{args: append([]string(nil), args...), stdin: data})
	executor.path = buildxMetadataPath(args)
	if executor.err != nil {
		return executor.output, executor.err
	}
	if executor.path == "" {
		return "", errors.New("metadata path was not provided")
	}
	if err := os.WriteFile(executor.path, executor.metadata, 0o600); err != nil {
		return "", err
	}
	return executor.output, nil
}

func TestDockerBuildxRunnerUsesFixedCommandAndCanonicalStdin(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	imageDigest, err := runner.Build(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if imageDigest != digest("5") {
		t.Fatalf("image digest = %q", imageDigest)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("buildx calls = %d", len(executor.calls))
	}
	call := executor.calls[0]
	if !bytes.Equal(call.stdin, request.ContextTar) {
		t.Fatal("buildx stdin did not receive the canonical context tar")
	}
	assertFixedBuildxArgs(t, call.args, request, root)
	if _, err := os.Stat(filepath.Dir(executor.path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("build temporary directory was not removed: %v", err)
	}
}

func TestNewDockerBuildxRunnerRejectsTypedNilExecutor(t *testing.T) {
	var executor *recordingBuildxCommandExecutor
	runner, err := newDockerBuildxRunner(executor, privateTempRoot(t))
	if err == nil || runner != nil {
		t.Fatal("typed-nil buildx command executor was accepted")
	}
}

func TestNewDockerBuildxRunnerCreatesRealAdapter(t *testing.T) {
	runner, err := NewDockerBuildxRunner(privateTempRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if runner == nil || buildxCommandExecutorIsNil(runner.executor) {
		t.Fatal("real docker buildx adapter was not constructed")
	}
}

func TestDockerBuildxRunnerLocalSmoke(t *testing.T) {
	if os.Getenv("LOCALCI_BUILDX_SMOKE") != "1" {
		t.Skip("set LOCALCI_BUILDX_SMOKE=1 to run the local Docker buildx smoke")
	}
	request := localBuildxSmokeRequest(t)
	runner, err := NewDockerBuildxRunner(privateTempRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	imageDigest, err := runner.Build(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDigest("smoke platform manifest digest", imageDigest); err != nil {
		t.Fatal(err)
	}
}

func TestDockerBuildxRunnerRejectsTypedNilRunner(t *testing.T) {
	var runner *DockerBuildxRunner
	_, err := runner.Build(context.Background(), validBuildxRequest(t))
	if err == nil {
		t.Fatal("typed-nil docker buildx runner was accepted")
	}
}

func TestDockerBuildxRunnerRejectsCanceledContextWithoutCommand(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, _ := newTestDockerBuildxRunner(t, executor)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runner.Build(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled build error = %v", err)
	}
	if len(executor.calls) != 0 {
		t.Fatal("canceled build reached the command executor")
	}
}

func TestDockerBuildxRunnerRejectsSymlinkCacheEscape(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	runner, root := newTestDockerBuildxRunner(t, executor)
	outside := privateTempRoot(t)
	cachePath := filepath.Join(root, "cache", strings.TrimPrefix(request.CacheNamespace, "sha256:"))
	if err := os.Symlink(outside, cachePath); err != nil {
		t.Skipf("create cache symlink: %v", err)
	}
	if _, err := runner.Build(context.Background(), request); err == nil {
		t.Fatal("symlink cache namespace escape was accepted")
	}
	if len(executor.calls) != 0 {
		t.Fatal("symlink cache namespace reached the command executor")
	}
}

func TestDockerBuildxRunnerRejectsUnsafeRequestBeforeCommand(t *testing.T) {
	base := validBuildxRequest(t)
	tests := []struct {
		name   string
		mutate func(*BuildKitBuildRequest)
	}{
		{name: "duplicate build argument", mutate: func(request *BuildKitBuildRequest) {
			request.BuildArguments = append(request.BuildArguments, request.BuildArguments[0])
		}},
		{name: "out of order build arguments", mutate: func(request *BuildKitBuildRequest) {
			request.BuildArguments = []BuildArgument{{Name: "Z_IMAGE", Value: request.BuildArguments[0].Value}, request.BuildArguments[0]}
		}},
		{name: "Dockerfile path escape", mutate: func(request *BuildKitBuildRequest) {
			request.DockerfilePath = "../Dockerfile"
		}},
		{name: "cache namespace escape", mutate: func(request *BuildKitBuildRequest) {
			request.CacheNamespace = "../shared"
		}},
		{name: "host network", mutate: func(request *BuildKitBuildRequest) {
			request.NetworkPolicy = "host"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := cloneBuildxRequest(base)
			test.mutate(&request)
			executor := validBuildxExecutor(t, base)
			runner, _ := newTestDockerBuildxRunner(t, executor)
			if _, err := runner.Build(context.Background(), request); err == nil {
				t.Fatal("unsafe BuildKit request was accepted")
			}
			if len(executor.calls) != 0 {
				t.Fatal("unsafe BuildKit request reached the command executor")
			}
		})
	}
}

func TestDockerBuildxRunnerRejectsCommandFailureAndTrailingOutput(t *testing.T) {
	request := validBuildxRequest(t)
	tests := []struct {
		name   string
		output string
		err    error
	}{
		{name: "command failure", err: errors.New("buildx failed")},
		{name: "trailing output", output: digest("4") + "\nwarning\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := validBuildxExecutor(t, request)
			executor.output = test.output
			executor.err = test.err
			runner, _ := newTestDockerBuildxRunner(t, executor)
			if _, err := runner.Build(context.Background(), request); err == nil {
				t.Fatal("invalid buildx command result was accepted")
			}
		})
	}
}

func TestDockerBuildxRunnerRejectsMissingMetadataFields(t *testing.T) {
	request := validBuildxRequest(t)
	for _, field := range jsonFieldNames(reflect.TypeOf(buildxMetadata{})) {
		t.Run(field, func(t *testing.T) {
			document := validBuildxMetadataDocument(request)
			delete(document, field)
			assertBuildxMetadataRejected(t, request, marshalBuildxMetadata(t, document))
		})
	}
}

func TestDockerBuildxRunnerRejectsUnknownTrailingAndMismatchedMetadata(t *testing.T) {
	request := validBuildxRequest(t)
	tests := []struct {
		name   string
		mutate func(map[string]any) []byte
	}{
		{name: "unknown field", mutate: func(document map[string]any) []byte {
			document["unknown"] = true
			return marshalBuildxMetadata(t, document)
		}},
		{name: "trailing JSON", mutate: func(document map[string]any) []byte {
			return append(marshalBuildxMetadata(t, document), []byte("\n{}")...)
		}},
		{name: "image digest mismatch", mutate: func(document map[string]any) []byte {
			document["containerimage.digest"] = digest("6")
			return marshalBuildxMetadata(t, document)
		}},
		{name: "context binding mismatch", mutate: func(document map[string]any) []byte {
			provenanceArgs(document)["label:org.super-dolphin.context-digest"] = digest("6")
			return marshalBuildxMetadata(t, document)
		}},
		{name: "platform binding mismatch", mutate: func(document map[string]any) []byte {
			provenanceInvocation(document)["environment"] = map[string]any{"platform": "linux/amd64"}
			return marshalBuildxMetadata(t, document)
		}},
		{name: "proxy build argument", mutate: func(document map[string]any) []byte {
			provenanceArgs(document)["build-arg:HTTP_PROXY"] = "http://proxy.invalid"
			return marshalBuildxMetadata(t, document)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertBuildxMetadataRejected(t, request, test.mutate(validBuildxMetadataDocument(request)))
		})
	}
}

func TestDockerBuildxRunnerRejectsEachDriftedBindingLabel(t *testing.T) {
	request := validBuildxRequest(t)
	labels := []string{
		"label:org.super-dolphin.context-digest",
		"label:org.super-dolphin.dockerfile-digest",
		"label:org.super-dolphin.image-input-digest",
		"label:org.super-dolphin.platform",
		"label:org.super-dolphin.source-tree-sha",
		"label:org.super-dolphin.toolchain-digest",
	}
	for _, label := range labels {
		t.Run(label, func(t *testing.T) {
			document := validBuildxMetadataDocument(request)
			provenanceArgs(document)[label] = "drifted"
			assertBuildxMetadataRejected(t, request, marshalBuildxMetadata(t, document))
		})
	}
}

func TestSanitizedBuildxEnvironmentDropsProxyAndLocksMetadataControls(t *testing.T) {
	environment := []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://proxy.invalid",
		"https_proxy=http://proxy.invalid",
		"BUILDX_METADATA_PROVENANCE=disabled",
		"BUILDX_METADATA_WARNINGS=1",
	}
	actual := sanitizedBuildxEnvironment(environment)
	wanted := []string{
		"PATH=/usr/bin",
		"BUILDX_METADATA_PROVENANCE=max",
		"BUILDX_METADATA_WARNINGS=0",
		"BUILDX_NO_DEFAULT_ATTESTATIONS=1",
	}
	if !slices.Equal(actual, wanted) {
		t.Fatalf("buildx environment = %v, want %v", actual, wanted)
	}
}

func validBuildxRequest(t *testing.T) BuildKitBuildRequest {
	t.Helper()
	prepared, err := prepareCandidate(candidateRequest(candidateEntries(validCandidateDockerfile()), digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.buildRequest
}

func localBuildxSmokeRequest(t *testing.T) BuildKitBuildRequest {
	t.Helper()
	var lock toolchainLock
	if err := decodeStrictJSON(readRepoFile(t, toolchainLockPath), &lock); err != nil {
		t.Fatal(err)
	}
	if len(lock.BaseImages) != 1 {
		t.Fatalf("tracked base images = %d", len(lock.BaseImages))
	}
	reference := lock.BaseImages[0].Reference
	dockerfile := strings.Replace(validCandidateDockerfile(), lockedGoImageReference(), reference, 1)
	entries := candidateEntries(dockerfile)
	replaceEntryText(t, entries, toolchainLockPath, lockedGoImageReference(), reference)
	changeEntry(t, entries, "go.sum", "")
	changeEntry(t, entries, "cmd/super-dolphin-gate/main.go", "package main\n\nfunc main() {}\n")
	prepared, err := prepareCandidate(candidateRequest(entries, digest("f"), digest("e")))
	if err != nil {
		t.Fatal(err)
	}
	return prepared.buildRequest
}

func cloneBuildxRequest(request BuildKitBuildRequest) BuildKitBuildRequest {
	request.ContextTar = append([]byte(nil), request.ContextTar...)
	request.BuildArguments = append([]BuildArgument(nil), request.BuildArguments...)
	return request
}

func validBuildxExecutor(t *testing.T, request BuildKitBuildRequest) *recordingBuildxCommandExecutor {
	t.Helper()
	return &recordingBuildxCommandExecutor{
		metadata: marshalBuildxMetadata(t, validBuildxMetadataDocument(request)),
		output:   digest("4") + "\n",
	}
}

func newTestDockerBuildxRunner(t *testing.T, executor buildxCommandExecutor) (*DockerBuildxRunner, string) {
	t.Helper()
	root := privateTempRoot(t)
	runner, err := newDockerBuildxRunner(executor, root)
	if err != nil {
		t.Fatal(err)
	}
	return runner, runner.root
}

func privateTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func assertFixedBuildxArgs(t *testing.T, args []string, request BuildKitBuildRequest, root string) {
	t.Helper()
	if len(args) < 3 || args[0] != "buildx" || args[1] != "build" || args[len(args)-1] != "-" {
		t.Fatalf("buildx command shape = %v", args)
	}
	required := []string{
		"--load", "--progress=quiet", "--provenance=false", "--platform=" + request.Platform,
		"--file=" + request.DockerfilePath, "--network=none",
	}
	for _, argument := range required {
		if !slices.Contains(args, argument) {
			t.Fatalf("buildx command missing %q: %v", argument, args)
		}
	}
	assertBuildxArgumentOrder(t, args, request)
	cacheArgument := "--cache-to=type=local,dest=" + filepath.Join(root, "cache", strings.TrimPrefix(request.CacheNamespace, "sha256:")) + ",mode=max"
	if !slices.Contains(args, cacheArgument) {
		t.Fatalf("buildx cache namespace is not isolated: %v", args)
	}
	forbidden := []string{"--secret", "--ssh", "--allow", "network=host", "security.insecure", "http_proxy", "https_proxy", "all_proxy", "docker.sock"}
	joined := strings.ToLower(strings.Join(args, "\n"))
	for _, fragment := range forbidden {
		if strings.Contains(joined, fragment) {
			t.Fatalf("buildx command contains forbidden fragment %q: %v", fragment, args)
		}
	}
}

func assertBuildxArgumentOrder(t *testing.T, args []string, request BuildKitBuildRequest) {
	t.Helper()
	actualBuildArguments := prefixedArguments(args, "--build-arg=")
	wantedBuildArguments := make([]string, len(request.BuildArguments))
	for index, argument := range request.BuildArguments {
		wantedBuildArguments[index] = "--build-arg=" + argument.Name + "=" + argument.Value
	}
	if !slices.Equal(actualBuildArguments, wantedBuildArguments) {
		t.Fatalf("build arguments = %v, want %v", actualBuildArguments, wantedBuildArguments)
	}
	actualLabels := prefixedArguments(args, "--label=")
	wantedLabels := buildxBindingLabels(request)
	if !slices.Equal(actualLabels, wantedLabels) {
		t.Fatalf("build labels = %v, want %v", actualLabels, wantedLabels)
	}
}

func prefixedArguments(args []string, prefix string) []string {
	var matched []string
	for _, argument := range args {
		if strings.HasPrefix(argument, prefix) {
			matched = append(matched, argument)
		}
	}
	return matched
}

func buildxBindingLabels(request BuildKitBuildRequest) []string {
	labels := []string{
		"--label=org.super-dolphin.context-digest=" + request.ContextDigest,
		"--label=org.super-dolphin.dockerfile-digest=" + request.DockerfileDigest,
		"--label=org.super-dolphin.image-input-digest=" + request.InputDigest,
		"--label=org.super-dolphin.platform=" + request.Platform,
		"--label=org.super-dolphin.source-tree-sha=" + request.SourceTreeSHA,
		"--label=org.super-dolphin.toolchain-digest=" + request.ToolchainDigest,
	}
	sort.Strings(labels)
	return labels
}

func buildxMetadataPath(args []string) string {
	const prefix = "--metadata-file="
	for _, argument := range args {
		if strings.HasPrefix(argument, prefix) {
			return strings.TrimPrefix(argument, prefix)
		}
	}
	return ""
}

func validBuildxMetadataDocument(request BuildKitBuildRequest) map[string]any {
	return map[string]any{
		"buildx.build.provenance": map[string]any{
			"builder":   map[string]any{"id": ""},
			"buildType": "https://mobyproject.org/buildkit@v1",
			"materials": []any{},
			"invocation": map[string]any{
				"configSource": map[string]any{
					"uri":        "http://buildkit-session/test",
					"digest":     map[string]any{"sha256": strings.TrimPrefix(request.ContextDigest, "sha256:")},
					"entryPoint": request.DockerfilePath,
				},
				"parameters":  map[string]any{"frontend": "dockerfile.v0", "args": expectedProvenanceArgs(request)},
				"environment": map[string]any{"platform": request.Platform},
			},
			"buildConfig": map[string]any{"llbDefinition": []any{}},
			"metadata":    map[string]any{"https://mobyproject.org/buildkit@v1#hermetic": true},
		},
		"buildx.build.ref":             "builder/node/invocation",
		"cache.manifest":               map[string]any{"digest": digest("7")},
		"containerimage.config.digest": digest("4"),
		"containerimage.descriptor": map[string]any{
			"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
			"digest":    digest("5"),
			"size":      512,
			"platform":  map[string]any{"architecture": "arm64", "os": "linux"},
		},
		"containerimage.digest": digest("5"),
		"image.name":            "moby-dangling@" + digest("5"),
	}
}

func expectedProvenanceArgs(request BuildKitBuildRequest) map[string]any {
	arguments := map[string]any{"force-network-mode": "none"}
	for _, argument := range request.BuildArguments {
		arguments["build-arg:"+argument.Name] = argument.Value
	}
	for _, label := range buildxBindingLabels(request) {
		name, value, _ := strings.Cut(strings.TrimPrefix(label, "--label="), "=")
		arguments["label:"+name] = value
	}
	return arguments
}

func provenanceInvocation(document map[string]any) map[string]any {
	provenance := document["buildx.build.provenance"].(map[string]any)
	return provenance["invocation"].(map[string]any)
}

func provenanceArgs(document map[string]any) map[string]any {
	parameters := provenanceInvocation(document)["parameters"].(map[string]any)
	return parameters["args"].(map[string]any)
}

func marshalBuildxMetadata(t *testing.T, document map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertBuildxMetadataRejected(t *testing.T, request BuildKitBuildRequest, metadata []byte) {
	t.Helper()
	executor := validBuildxExecutor(t, request)
	executor.metadata = metadata
	runner, _ := newTestDockerBuildxRunner(t, executor)
	if _, err := runner.Build(context.Background(), request); err == nil {
		t.Fatal("invalid buildx metadata was accepted")
	}
}

func jsonFieldNames(documentType reflect.Type) []string {
	fields := make([]string, 0, documentType.NumField())
	for index := 0; index < documentType.NumField(); index++ {
		name := strings.Split(documentType.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}
