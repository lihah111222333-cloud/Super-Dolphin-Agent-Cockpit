package localci

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestControlledBuildxOwnerFieldRegistration(t *testing.T) {
	if fields := jsonFieldNames(reflect.TypeFor[controlledBuildxOwner]()); !slices.Equal(fields, []string{"builder_name", "input_digest", "source_tree_sha"}) {
		t.Fatalf("controlled buildx owner fields = %v", fields)
	}
}

func TestBuildxEvidenceFieldRegistration(t *testing.T) {
	tests := []struct {
		name     string
		fields   []string
		expected []string
	}{
		{name: "manifest", fields: jsonFieldNames(reflect.TypeFor[buildxManifestAttachment]()), expected: []string{"config", "layers", "mediaType", "schemaVersion"}},
		{name: "descriptor", fields: jsonFieldNames(reflect.TypeFor[buildxManifestContentDescriptor]()), expected: []string{"digest", "mediaType", "size"}},
		{name: "material", fields: jsonFieldNames(reflect.TypeFor[buildxMaterial]()), expected: []string{"digest", "uri"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !slices.Equal(test.fields, test.expected) {
				t.Fatalf("buildx %s evidence fields = %v", test.name, test.fields)
			}
		})
	}
}

func TestDockerBuildxRunnerRejectsMissingMetadataFields(t *testing.T) {
	request := validBuildxRequest(t)
	for _, field := range jsonFieldNames(reflect.TypeFor[buildxMetadata]()) {
		if field == "containerimage.config.digest" {
			continue
		}
		t.Run(field, func(t *testing.T) {
			document := validBuildxMetadataDocument(t, request)
			delete(document, field)
			assertBuildxMetadataRejected(t, request, marshalBuildxMetadata(t, document))
		})
	}
}

func TestDockerBuildxRunnerAcceptsMissingOptionalMetadataConfigDigest(t *testing.T) {
	request := validBuildxRequest(t)
	document := validBuildxMetadataDocument(t, request)
	delete(document, "containerimage.config.digest")
	executor := validBuildxExecutor(t, request)
	executor.metadata = marshalBuildxMetadata(t, document)
	runner, _ := newTestDockerBuildxRunner(t, executor)
	result, err := runner.Build(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigDigest != digest("4") {
		t.Fatalf("config digest = %q", result.ConfigDigest)
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
		{name: "descriptor annotation drift", mutate: func(document map[string]any) []byte {
			descriptor := document["containerimage.descriptor"].(map[string]any)
			descriptor["annotations"] = map[string]string{"org.opencontainers.image.created": "2026-01-01T00:00:00Z"}
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
		{name: "builder identity mismatch", mutate: func(document map[string]any) []byte {
			provenance := document["buildx.build.provenance"].(map[string]any)
			provenance["builder"] = map[string]any{"id": "https://attacker.invalid/buildkit"}
			return marshalBuildxMetadata(t, document)
		}},
		{name: "external material", mutate: func(document map[string]any) []byte {
			provenance := document["buildx.build.provenance"].(map[string]any)
			provenance["materials"] = []any{map[string]any{"uri": "https://attacker.invalid/material"}}
			return marshalBuildxMetadata(t, document)
		}},
		{name: "frontend mismatch", mutate: func(document map[string]any) []byte {
			provenanceInvocation(document)["parameters"] = map[string]any{"frontend": "gateway.v0", "args": expectedProvenanceArgs(request)}
			return marshalBuildxMetadata(t, document)
		}},
		{name: "optional config mismatch", mutate: func(document map[string]any) []byte {
			document["containerimage.config.digest"] = digest("9")
			return marshalBuildxMetadata(t, document)
		}},
		{name: "proxy build argument", mutate: func(document map[string]any) []byte {
			provenanceArgs(document)["build-arg:HTTP_PROXY"] = "http://proxy.invalid"
			return marshalBuildxMetadata(t, document)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertBuildxMetadataRejected(t, request, test.mutate(validBuildxMetadataDocument(t, request)))
		})
	}
}

func TestValidateBuildxManifestAttachmentRejectsDrift(t *testing.T) {
	valid := func() map[string]any {
		var document map[string]any
		if err := json.Unmarshal(validBuildxManifestAttachment(t), &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	marshal := func(document map[string]any) []byte {
		data, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	tests := []struct {
		name   string
		mutate func(map[string]any) []byte
	}{
		{name: "unknown field", mutate: func(document map[string]any) []byte {
			document["unknown"] = true
			return marshal(document)
		}},
		{name: "schema", mutate: func(document map[string]any) []byte {
			document["schemaVersion"] = float64(1)
			return marshal(document)
		}},
		{name: "config media", mutate: func(document map[string]any) []byte {
			document["config"].(map[string]any)["mediaType"] = "application/octet-stream"
			return marshal(document)
		}},
		{name: "missing layers", mutate: func(document map[string]any) []byte {
			document["layers"] = []any{}
			return marshal(document)
		}},
		{name: "trailing JSON", mutate: func(document map[string]any) []byte {
			return append(marshal(document), []byte("\n{}")...)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := test.mutate(valid())
			if _, err := validateBuildxManifestAttachment(data, buildxAttachmentDigest(data)); err == nil {
				t.Fatal("drifted manifest attachment was accepted")
			}
		})
	}
	if _, err := validateBuildxManifestAttachment(validBuildxManifestAttachment(t), digest("9")); err == nil {
		t.Fatal("manifest attachment with a mismatched content digest was accepted")
	}
}

func TestDockerBuildxRunnerRejectsManifestAttachmentFailure(t *testing.T) {
	request := validBuildxRequest(t)
	executor := validBuildxExecutor(t, request)
	executor.attachmentErr = errors.New("attachment unavailable")
	runner, _ := newTestDockerBuildxRunner(t, executor)
	if _, err := runner.Build(context.Background(), request); !errors.Is(err, executor.attachmentErr) {
		t.Fatalf("attachment failure error = %v", err)
	}
}

func TestResolveDockerDescriptorConfigDigestAcceptsOnlyCanonicalWitnesses(t *testing.T) {
	inputDigest := digest("8")
	configDigest := digest("4")
	candidateTag := expectedCandidateImageTag(BuildKitBuildRequest{InputDigest: inputDigest})
	tagName := strings.TrimPrefix(candidateTag, candidateImageRepository+":")
	containerdAnnotations := map[string]string{
		"io.containerd.image.name":          candidateTag,
		"org.opencontainers.image.created":  buildxImportedImageCreated,
		"org.opencontainers.image.ref.name": tagName,
	}
	for _, test := range []struct {
		name        string
		annotations map[string]string
		repository  string
		wantError   bool
	}{
		{name: "direct Docker config", annotations: map[string]string{"config.digest": configDigest}, repository: "local/bootstrap"},
		{name: "controlled containerd import", annotations: containerdAnnotations, repository: candidateImageRepository},
		{name: "extra direct annotation", annotations: map[string]string{"config.digest": configDigest, "extra": "value"}, repository: "local/bootstrap", wantError: true},
		{name: "wrong imported repository", annotations: containerdAnnotations, repository: "attacker.invalid/image", wantError: true},
		{name: "wrong imported tag", annotations: map[string]string{
			"io.containerd.image.name":          candidateTag + "-drift",
			"org.opencontainers.image.created":  buildxImportedImageCreated,
			"org.opencontainers.image.ref.name": tagName,
		}, repository: candidateImageRepository, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := resolveDockerDescriptorConfigDigest(test.annotations, test.repository, inputDigest, configDigest)
			if (err != nil) != test.wantError {
				t.Fatalf("resolve config digest error = %v", err)
			}
			if !test.wantError && actual != configDigest {
				t.Fatalf("resolved config digest = %q", actual)
			}
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
		"label:org.super-dolphin.policy-sha",
		"label:org.super-dolphin.schema-version",
		"label:org.super-dolphin.source-tree-sha",
		"label:org.super-dolphin.toolchain-digest",
	}
	for _, label := range labels {
		t.Run(label, func(t *testing.T) {
			document := validBuildxMetadataDocument(t, request)
			provenanceArgs(document)[label] = "drifted"
			assertBuildxMetadataRejected(t, request, marshalBuildxMetadata(t, document))
		})
	}
}
