package localci

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func validBuildxMetadataDocument(request BuildKitBuildRequest) map[string]any {
	return map[string]any{
		"buildx.build.provenance":      validBuildxProvenanceDocument(request),
		"buildx.build.ref":             testBuildxBuilderPlaceholder + "/node/invocation",
		"cache.manifest":               map[string]any{"digest": digest("7")},
		"containerimage.config.digest": digest("4"),
		"containerimage.descriptor":    validBuildxDescriptor(),
		"containerimage.digest":        digest("5"),
		"image.name":                   expectedCandidateImageTag(request),
	}
}

func validBuildxProvenanceDocument(request BuildKitBuildRequest) map[string]any {
	return map[string]any{
		"builder":   map[string]any{"id": buildxProvenanceBuilderID},
		"buildType": "https://mobyproject.org/buildkit@v1",
		"materials": []any{},
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
	}
}

func validBuildxConfigSource(request BuildKitBuildRequest) map[string]any {
	return map[string]any{
		"uri":        "http://buildkit-session/test",
		"digest":     map[string]any{"sha256": strings.TrimPrefix(request.ContextDigest, "sha256:")},
		"entryPoint": request.DockerfilePath,
	}
}

func validBuildxDescriptor() map[string]any {
	return map[string]any{
		"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
		"digest":    digest("5"),
		"size":      512,
		"platform":  map[string]any{"architecture": "arm64", "os": "linux"},
	}
}

func expectedCandidateImageTag(request BuildKitBuildRequest) string {
	return "docker.io/library/super-dolphin-gate-local:candidate-" + strings.TrimPrefix(request.InputDigest, "sha256:")
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
		name, _, _ := strings.Cut(documentType.Field(index).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}
