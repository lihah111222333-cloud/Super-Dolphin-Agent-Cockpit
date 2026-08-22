package remoteci

import (
	"bytes"
	"strings"
	"testing"
)

func TestRuntimeArtifactBuilderInputCanonicalRoundTrip(t *testing.T) {
	input := newRuntimeArtifactBuildInput(t)
	encoded, err := input.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	decoded, err := DecodeRuntimeArtifactBuilderInput(encoded)
	if err != nil {
		t.Fatalf("DecodeRuntimeArtifactBuilderInput() error = %v", err)
	}
	if decoded != input {
		t.Fatalf("decoded input = %#v, want %#v", decoded, input)
	}
}

func TestRuntimeArtifactBuilderInputStrictDecoderRejectsInvalidDocuments(t *testing.T) {
	encoded, err := newRuntimeArtifactBuildInput(t).CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	for name, data := range map[string][]byte{
		"unknown":   bytes.Replace(encoded, []byte(`"artifact_prefix":`), []byte(`"unknown":1,"artifact_prefix":`), 1),
		"duplicate": bytes.Replace(encoded, []byte(`"artifact_prefix":`), []byte(`"schema_version":"portable-runtime-artifact-builder-input/v1","artifact_prefix":`), 1),
		"null":      []byte("null"),
		"trailing":  append(append([]byte(nil), encoded...), []byte("\n{}")...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRuntimeArtifactBuilderInput(data); err == nil {
				t.Fatal("strict decoder accepted invalid input")
			}
		})
	}
}

func TestRuntimeArtifactBuilderInputConsumerFieldGuardRejectsMissingAndStale(t *testing.T) {
	fields := RuntimeArtifactBuilderInputConsumerFields()
	if err := ValidateRuntimeArtifactBuilderInputConsumerFields(fields); err != nil {
		t.Fatalf("complete consumer registry error = %v", err)
	}
	if err := ValidateRuntimeArtifactBuilderInputConsumerFields(fields[:len(fields)-1]); err == nil {
		t.Fatal("field guard accepted a missing consumer")
	}
	if err := ValidateRuntimeArtifactBuilderInputConsumerFields(append(fields, "stale_field")); err == nil {
		t.Fatal("field guard accepted a stale consumer")
	}
}

func newRuntimeArtifactBuildInput(t *testing.T) RuntimeArtifactBuildInput {
	t.Helper()
	protocolABI, err := PortableExecutionProtocolABIDigest()
	if err != nil {
		t.Fatalf("portable execution ABI: %v", err)
	}
	workloadABI, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatalf("workload manifest ABI: %v", err)
	}
	return RuntimeArtifactBuildInput{
		SchemaVersion: RuntimeArtifactBuilderInputSchema, ContractID: PortableExecutionRuntimeArtifactContractID,
		ArtifactKind: PortableExecutionRuntimeArtifactKind, ExecutionProvider: PortableExecutionProvider,
		ArtifactPrefix: "runtime/", RepositoryID: "engine-1", ObjectFormat: "sha1",
		CommitSHA: strings.Repeat("a", 40), TreeSHA: strings.Repeat("b", 40),
		InputObjectKey: "runtime/inputs/engine.bundle", InputSHA256: "sha256:" + strings.Repeat("c", 64), InputSizeBytes: 123,
		ExpectedBuilderDigest: "sha256:" + strings.Repeat("d", 64), ExpectedToolchainDigest: "sha256:" + strings.Repeat("e", 64),
		ProtocolABIDigest: protocolABI, WorkloadManifestABIDigest: workloadABI,
	}
}
