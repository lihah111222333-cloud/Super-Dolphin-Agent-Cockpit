package localci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	calls             []buildxCommandCall
	contextErrs       []error
	metadata          []byte
	output            string
	err               error
	path              string
	builderName       string
	request           BuildKitBuildRequest
	inspectOutput     string
	inspectOutputs    []string
	inspectCalls      int
	containerOutput   string
	imageOutput       string
	attachmentOutput  string
	attachmentErr     error
	onBuild           func()
	runtimeBuildErrs  []error
	runtimeBuildCalls int
	createErr         error
	updateErr         error
	removeErr         error
	createSideEffect  bool
	builders          map[string]bool
	containers        map[string]bool
}

func (executor *recordingBuildxCommandExecutor) Run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	executor.calls = append(executor.calls, buildxCommandCall{args: append([]string(nil), args...), stdin: data})
	executor.contextErrs = append(executor.contextErrs, ctx.Err())
	command := buildxTestCommand(args)
	if output, handled := executor.controlledCommandOutput(command, args); handled {
		return output, executor.controlledCommandError(command)
	}
	return executor.runCandidateBuild(args)
}

func (executor *recordingBuildxCommandExecutor) controlledCommandOutput(command string, args []string) (string, bool) {
	handlers := map[string]func([]string) string{
		"buildx create":     executor.recordBuildxCreate,
		"buildx inspect":    executor.recordBuildxInspect,
		"buildx history":    executor.recordBuildxHistoryAttachment,
		"buildx rm":         executor.recordBuildxRemove,
		"buildx ls":         executor.recordBuildxList,
		"container update":  executor.recordContainerUpdate,
		"container inspect": executor.recordContainerInspect,
		"container rm":      executor.recordContainerRemove,
		"container ls":      executor.recordContainerList,
		"image inspect":     executor.recordImageInspect,
	}
	handler, found := handlers[command]
	if !found {
		return "", false
	}
	return handler(args), true
}

func (executor *recordingBuildxCommandExecutor) controlledCommandError(command string) error {
	switch command {
	case "buildx create":
		if executor.createErr != nil {
			return executor.createErr
		}
	case "buildx rm":
		if executor.removeErr != nil {
			return executor.removeErr
		}
	case "buildx history":
		if executor.attachmentErr != nil {
			return executor.attachmentErr
		}
	case "container update":
		if executor.updateErr != nil {
			return executor.updateErr
		}
	}
	return executor.err
}

func (executor *recordingBuildxCommandExecutor) recordBuildxCreate(args []string) string {
	executor.builderName = valueAfter(args, "--name")
	if !executor.createSideEffect {
		return ""
	}
	executor.ensureManagedState()
	executor.builders[executor.builderName] = true
	executor.containers[executor.builderName] = true
	return ""
}

func (executor *recordingBuildxCommandExecutor) ensureManagedState() {
	if executor.builders != nil {
		return
	}
	executor.builders = make(map[string]bool)
	executor.containers = make(map[string]bool)
}

func (executor *recordingBuildxCommandExecutor) recordBuildxInspect([]string) string {
	if executor.inspectCalls < len(executor.inspectOutputs) {
		output := executor.inspectOutputs[executor.inspectCalls]
		executor.inspectCalls++
		return output
	}
	executor.inspectCalls++
	return testBuildxOutput(executor.inspectOutput, "Name: "+executor.builderName+"\nDriver: docker-container\nNodes:\n  Name: node\n  Status: running\n  BuildKit version: "+executor.request.BuildKitVersion+"\n")
}

func (executor *recordingBuildxCommandExecutor) recordContainerUpdate([]string) string {
	return ""
}

func (executor *recordingBuildxCommandExecutor) recordContainerInspect([]string) string {
	return testBuildxOutput(executor.containerOutput, executor.request.BuildKitImage+"\n"+digest("a")+"\n"+buildxBuilderCPUQuota+"/"+buildxBuilderCPUPeriod+"/"+buildxBuilderMemoryBytes+"/"+buildxBuilderPidsLimit+"\n")
}

func (executor *recordingBuildxCommandExecutor) recordImageInspect([]string) string {
	normalized := strings.TrimPrefix(executor.request.BuildKitImage, "docker.io/")
	return testBuildxOutput(executor.imageOutput, digest("a")+"\n"+normalized+"\n")
}

func (executor *recordingBuildxCommandExecutor) recordBuildxHistoryAttachment([]string) string {
	if executor.attachmentErr != nil {
		return ""
	}
	attachment, err := buildValidBuildxManifestAttachment()
	if err != nil {
		executor.attachmentErr = fmt.Errorf("build valid buildx manifest attachment: %w", err)
		return ""
	}
	return testBuildxOutput(executor.attachmentOutput, string(attachment))
}

func (executor *recordingBuildxCommandExecutor) recordBuildxRemove(args []string) string {
	if executor.removeErr != nil {
		return ""
	}
	name := args[len(args)-1]
	delete(executor.builders, name)
	delete(executor.containers, name)
	return ""
}

func (executor *recordingBuildxCommandExecutor) recordContainerRemove(args []string) string {
	builderName, found := controlledBuildxBuilderName(args[len(args)-1])
	if found {
		delete(executor.containers, builderName)
	}
	return ""
}

func controlledBuildxBuilderName(containerName string) (string, bool) {
	builderName, found := strings.CutPrefix(containerName, "buildx_buildkit_")
	if !found {
		return "", false
	}
	return strings.CutSuffix(builderName, "0")
}

func (executor *recordingBuildxCommandExecutor) recordBuildxList([]string) string {
	return strings.Join(buildxTestManagedNames(executor.builders), "\n")
}

func (executor *recordingBuildxCommandExecutor) recordContainerList([]string) string {
	builderNames := buildxTestManagedNames(executor.containers)
	for index, builderName := range builderNames {
		builderNames[index] = controlledBuildxContainerName(builderName)
	}
	return strings.Join(builderNames, "\n")
}

func buildxTestManagedNames(values map[string]bool) []string {
	names := make([]string, 0, len(values))
	for name, present := range values {
		if present {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func buildxTestCommand(args []string) string {
	if len(args) < 2 {
		return ""
	}
	return args[0] + " " + args[1]
}

func testBuildxOutput(output string, fallback string) string {
	if output == "" {
		return fallback
	}
	return output
}

func (executor *recordingBuildxCommandExecutor) runCandidateBuild(args []string) (string, error) {
	if slices.Contains(args, "--file=build/gate/runtime-deps.Dockerfile") {
		return executor.runRuntimeDepsBuild(args)
	}
	executor.path = buildxMetadataPath(args)
	if executor.onBuild != nil {
		executor.onBuild()
	}
	if executor.err != nil {
		return executor.output, executor.err
	}
	if executor.path == "" {
		return "", errors.New("metadata path was not provided")
	}
	metadata, err := executor.candidateMetadata()
	if err != nil {
		return "", err
	}
	metadata = bytes.ReplaceAll(metadata, []byte(testBuildxBuilderPlaceholder), []byte(executor.builderName))
	if err := os.WriteFile(executor.path, metadata, 0o600); err != nil {
		return "", err
	}
	return executor.output, nil
}

func (executor *recordingBuildxCommandExecutor) runRuntimeDepsBuild(args []string) (string, error) {
	call := executor.runtimeBuildCalls
	executor.runtimeBuildCalls++
	if call < len(executor.runtimeBuildErrs) {
		return "", executor.runtimeBuildErrs[call]
	}
	if executor.err != nil {
		return "", executor.err
	}
	metadataPath := buildxMetadataPath(args)
	if metadataPath == "" {
		return "", errors.New("runtime dependencies metadata path was not provided")
	}
	metadata, err := json.Marshal(validRuntimeDepsBuildxMetadata(executor.request.Platform))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(metadataPath, metadata, 0o600); err != nil {
		return "", err
	}
	return "", nil
}

func (executor *recordingBuildxCommandExecutor) candidateMetadata() ([]byte, error) {
	if executor.metadata != nil {
		return executor.metadata, nil
	}
	document, err := buildValidBuildxMetadataDocument(executor.request)
	if err != nil {
		return nil, fmt.Errorf("build valid buildx metadata: %w", err)
	}
	metadata, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("marshal valid buildx metadata: %w", err)
	}
	return metadata, nil
}

func TestDockerBuildxRunnerFailsClosedWhenPidsUpdateResponseIsLost(t *testing.T) {
	request := validBuildxRequest(t)
	updateErr := errors.New("container update response lost")
	for _, test := range []struct {
		name         string
		removeErr    error
		wantOwner    bool
		wantResource bool
	}{
		{name: "cleanup confirmed"},
		{name: "cleanup unconfirmed", removeErr: errors.New("builder removal rejected"), wantOwner: true, wantResource: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := validBuildxExecutor(t, request)
			executor.createSideEffect = true
			executor.updateErr = updateErr
			executor.removeErr = test.removeErr
			runner, _ := newTestDockerBuildxRunner(t, executor)
			if _, err := runner.Build(context.Background(), request); !errors.Is(err, updateErr) {
				t.Fatalf("PIDs update failure error = %v", err)
			}
			assertExactBuildxCommand(t, executor.calls[2].args, []string{"container", "update", "--pids-limit", buildxBuilderPidsLimit, controlledBuildxContainerName(executor.builderName)}, "controlled builder PIDs update command")
			names, err := runner.recordedControlledBuilderNames()
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(names, executor.builderName) != test.wantOwner {
				t.Fatalf("PIDs update failure ownership names = %v, want owner = %t", names, test.wantOwner)
			}
			if executor.builders[executor.builderName] != test.wantResource {
				t.Fatalf("PIDs update failure builder present = %t, want %t", executor.builders[executor.builderName], test.wantResource)
			}
		})
	}
}

func buildValidBuildxMetadataDocument(request BuildKitBuildRequest) (map[string]any, error) {
	provenance, err := buildValidBuildxProvenanceDocument(request)
	if err != nil {
		return nil, fmt.Errorf("build provenance: %w", err)
	}
	descriptor, err := buildValidBuildxDescriptor()
	if err != nil {
		return nil, fmt.Errorf("build descriptor: %w", err)
	}
	manifestDigest, err := buildValidBuildxManifestDigest()
	if err != nil {
		return nil, fmt.Errorf("build manifest digest: %w", err)
	}
	return map[string]any{
		"buildx.build.provenance":      provenance,
		"buildx.build.ref":             testBuildxBuilderPlaceholder + "/node/" + testBuildxHistoryRecordReference,
		"cache.manifest":               map[string]any{"digest": digest("7")},
		"containerimage.config.digest": digest("4"),
		"containerimage.descriptor":    descriptor,
		"containerimage.digest":        manifestDigest,
		"image.name":                   expectedCandidateImageTag(request),
	}, nil
}

func validBuildxMetadataDocument(t *testing.T, request BuildKitBuildRequest) map[string]any {
	t.Helper()
	document, err := buildValidBuildxMetadataDocument(request)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func buildValidBuildxProvenanceDocument(request BuildKitBuildRequest) (map[string]any, error) {
	materials, err := buildValidBuildxMaterialsDocument(request)
	if err != nil {
		return nil, fmt.Errorf("build materials: %w", err)
	}
	return map[string]any{
		"builder":   map[string]any{"id": buildxProvenanceBuilderID},
		"buildType": "https://mobyproject.org/buildkit@v1",
		"materials": materials,
		"invocation": map[string]any{
			"configSource": validBuildxConfigSource(request),
			"parameters": map[string]any{
				"frontend": "dockerfile.v0",
				"args":     expectedProvenanceArgs(request),
			},
			"environment": map[string]any{"platform": request.Platform},
		},
		"buildConfig": map[string]any{"llbDefinition": []any{}},
		"metadata":    map[string]any{"https://mobyproject.org/buildkit@v1#hermetic": true},
	}, nil
}

func buildValidBuildxMaterialsDocument(request BuildKitBuildRequest) ([]any, error) {
	materials := make([]any, 0, len(request.BuildArguments))
	runtimeMaterial, err := expectedRuntimeDepsBuildxMaterial(digest("7"), request.Platform)
	if err != nil {
		return nil, err
	}
	materials = append(materials, map[string]any{
		"uri": runtimeMaterial.URI, "digest": map[string]any{"sha256": runtimeMaterial.Digest.SHA256},
	})
	for _, argument := range request.BuildArguments {
		if argument.Name == sourceDateEpochArgument || argument.Name == "RUNTIME_DEPS_IMAGE" {
			continue
		}
		material, err := expectedBuildxImageMaterial(argument.Value, request.Platform)
		if err != nil {
			return nil, fmt.Errorf("build image material for %q: %w", argument.Value, err)
		}
		materials = append(materials, map[string]any{
			"uri": material.URI, "digest": map[string]any{"sha256": material.Digest.SHA256},
		})
	}
	return append(materials, map[string]any{
		"uri":    "http://buildkit-session/test",
		"digest": map[string]any{"sha256": strings.TrimPrefix(request.ContextDigest, "sha256:")},
	}), nil
}

func validRuntimeDepsBuildxMetadata(platformValue string) map[string]any {
	platform, _ := parseBuildxPlatform(platformValue)
	return map[string]any{
		"buildx.build.provenance":      map[string]any{"runtime": true},
		"buildx.build.ref":             testBuildxHistoryRecordReference,
		"containerimage.config.digest": digest("6"),
		"containerimage.descriptor": map[string]any{
			"mediaType": runtimeDepsManifestMedia,
			"digest":    digest("7"),
			"size":      512,
			"platform":  platform,
		},
		"containerimage.digest": digest("7"),
	}
}

func validBuildxConfigSource(request BuildKitBuildRequest) map[string]any {
	return map[string]any{
		"uri":        "http://buildkit-session/test",
		"digest":     map[string]any{"sha256": strings.TrimPrefix(request.ContextDigest, "sha256:")},
		"entryPoint": request.DockerfilePath,
	}
}

func buildValidBuildxDescriptor() (map[string]any, error) {
	manifestDigest, err := buildValidBuildxManifestDigest()
	if err != nil {
		return nil, fmt.Errorf("build manifest digest: %w", err)
	}
	return map[string]any{
		"mediaType":   "application/vnd.docker.distribution.manifest.v2+json",
		"digest":      manifestDigest,
		"size":        512,
		"annotations": map[string]string{"org.opencontainers.image.created": buildxImportedImageCreated},
		"platform":    map[string]any{"architecture": "arm64", "os": "linux"},
	}, nil
}

func buildValidBuildxManifestAttachment() ([]byte, error) {
	document := buildxManifestAttachment{
		SchemaVersion: 2,
		MediaType:     buildxManifestMedia,
		Config: buildxManifestContentDescriptor{
			MediaType: buildxConfigMedia,
			Digest:    digest("4"),
			Size:      256,
		},
		Layers: []buildxManifestContentDescriptor{{
			MediaType: buildxLayerMedia,
			Digest:    digest("8"),
			Size:      512,
		}},
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest attachment: %w", err)
	}
	return data, nil
}

func validBuildxManifestAttachment(t *testing.T) []byte {
	t.Helper()
	attachment, err := buildValidBuildxManifestAttachment()
	if err != nil {
		t.Fatal(err)
	}
	return attachment
}

func buildValidBuildxManifestDigest() (string, error) {
	attachment, err := buildValidBuildxManifestAttachment()
	if err != nil {
		return "", fmt.Errorf("build manifest attachment: %w", err)
	}
	return buildxAttachmentDigest(attachment), nil
}

func validBuildxManifestDigest(t *testing.T) string {
	t.Helper()
	digest, err := buildValidBuildxManifestDigest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func expectedCandidateImageTag(request BuildKitBuildRequest) string {
	return "docker.io/library/super-dolphin-gate-local:candidate-" + strings.TrimPrefix(request.InputDigest, "sha256:")
}

func expectedProvenanceArgs(request BuildKitBuildRequest) map[string]any {
	arguments := map[string]any{
		"force-network-mode":   "none",
		"frontend.caps":        buildxNamedContextCaps,
		"context:runtime-deps": "oci-layout://" + testBuildxHistoryRecordReference + ":latest@" + digest("7"),
	}
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
		name, _, _ := strings.Cut(documentType.Field(index).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}
