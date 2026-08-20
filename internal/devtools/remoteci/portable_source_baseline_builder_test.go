package remoteci

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestSourceBaselineBuilderProtocolValidatesDeterministicInput(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	if err := input.Validate(); err != nil {
		t.Fatalf("SourceBaselineBuildInput.Validate() error = %v", err)
	}
	commit, err := DeterministicSourceBaselineCommitSHA(input.TreeSHA, gate.GitObjectFormat(input.ObjectFormat))
	if err != nil {
		t.Fatalf("DeterministicSourceBaselineCommitSHA() error = %v", err)
	}
	if input.CommitSHA == commit {
		t.Fatalf("SourceBaselineBuildInput.CommitSHA unexpectedly equals deterministic baseline %q", commit)
	}
	receipt := testSourceBaselineProducerReceipt(t)
	if receipt.BaselineCommitSHA != commit {
		t.Fatalf("receipt baseline commit = %q, want deterministic %q", receipt.BaselineCommitSHA, commit)
	}
}

func TestSourceBaselineBuilderReceiptStrictCodec(t *testing.T) {
	receipt := testSourceBaselineProducerReceipt(t)
	encoded, err := receipt.CanonicalEncode()
	if err != nil {
		t.Fatalf("SourceBaselineProducerReceipt.CanonicalEncode() error = %v", err)
	}
	digest, err := SourceBaselineProducerReceiptDigest(receipt)
	if err != nil {
		t.Fatalf("SourceBaselineProducerReceiptDigest() error = %v", err)
	}
	if len(encoded) == 0 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("receipt encoding = %q, digest = %q", encoded, digest)
	}
	decoded, err := DecodeSourceBaselineProducerReceipt(encoded)
	if err != nil {
		t.Fatalf("DecodeSourceBaselineProducerReceipt() error = %v", err)
	}
	if decoded != receipt {
		t.Fatalf("decoded receipt = %#v, want %#v", decoded, receipt)
	}
	for name, malformed := range map[string][]byte{
		"unknown":   append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(`,"extra":"x"}`)...),
		"null":      []byte(`null`),
		"duplicate": []byte(`{"provider":"aliyun-eci/v1","provider":"aliyun-eci/v1"}`),
		"trailing":  append(append([]byte(nil), encoded...), []byte(` {}`)...),
	} {
		assertSourceBaselineReceiptDecodeRejects(t, name, malformed)
	}
	assertSourceBaselineReceiptDecodeRejects(t, "oversized", bytes.Repeat([]byte("x"), 1<<20+1))
	assertSourceBaselineReceiptDecodeRejects(t, "incomplete", []byte(`{"provider":"aliyun-eci/v1"}`))
}

func assertSourceBaselineReceiptDecodeRejects(t *testing.T, name string, data []byte) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		if _, err := DecodeSourceBaselineProducerReceipt(data); err == nil {
			t.Fatalf("DecodeSourceBaselineProducerReceipt() accepted %s JSON", name)
		}
	})
}

func TestSourceBaselineBuilderEvidenceBindsInput(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	evidence := testSourceBaselineBuildEvidence(t, input)
	if err := evidence.ValidateAgainst(input); err != nil {
		t.Fatalf("SourceBaselineBuildEvidence.ValidateAgainst() error = %v", err)
	}
	evidence.Provider = "other/v1"
	if err := evidence.ValidateAgainst(input); err == nil {
		t.Fatal("SourceBaselineBuildEvidence.ValidateAgainst() accepted a non-ECI provider")
	}
}

func TestSourceBaselineBuildEvidenceRejectsArtifactReceiptKeyDigestDrift(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	for name, mutate := range map[string]func(*SourceBaselineBuildEvidence){
		"artifact": func(evidence *SourceBaselineBuildEvidence) { evidence.ArtifactKey += ".drift" },
		"receipt":  func(evidence *SourceBaselineBuildEvidence) { evidence.ReceiptKey += ".drift" },
	} {
		t.Run(name, func(t *testing.T) {
			evidence := testSourceBaselineBuildEvidence(t, input)
			mutate(&evidence)
			if err := evidence.ValidateAgainst(input); err == nil {
				t.Fatal("SourceBaselineBuildEvidence.ValidateAgainst() accepted artifact/receipt key digest drift")
			}
		})
	}
}

func TestSourceBaselineBuilderRejectsAllZeroOID(t *testing.T) {
	for name, mutate := range map[string]func(*SourceBaselineBuildInput){
		"commit": func(input *SourceBaselineBuildInput) { input.CommitSHA = strings.Repeat("0", 40) },
		"tree":   func(input *SourceBaselineBuildInput) { input.TreeSHA = strings.Repeat("0", 40) },
	} {
		t.Run(name, func(t *testing.T) {
			input := testSourceBaselineBuildInput(t)
			mutate(&input)
			if err := input.Validate(); err == nil {
				t.Fatal("SourceBaselineBuildInput.Validate() accepted an all-zero OID")
			}
		})
	}
}

func TestArtifactBuilderRuntimeIsOrchestrationOnly(t *testing.T) {
	var runtime SourceBaselineArtifactBuilderRuntime = testArtifactBuilderRuntime{}
	evidence, err := runtime.Build(context.Background(), testSourceBaselineBuildInput(t))
	if err != nil {
		t.Fatalf("SourceBaselineArtifactBuilderRuntime.Build() error = %v", err)
	}
	if evidence.Provider != PortableExecutionProvider {
		t.Fatalf("Build() provider = %q", evidence.Provider)
	}
}

type testArtifactBuilderRuntime struct{}

func (testArtifactBuilderRuntime) Build(context.Context, SourceBaselineBuildInput) (SourceBaselineBuildEvidence, error) {
	return SourceBaselineBuildEvidence{Provider: PortableExecutionProvider}, nil
}

func testSourceBaselineBuildInput(t *testing.T) SourceBaselineBuildInput {
	t.Helper()
	tree := strings.Repeat("1", 40)
	return SourceBaselineBuildInput{
		SchemaVersion:   SourceBaselineBuilderInputSchema,
		RepositoryID:    "repository-1",
		ObjectFormat:    string(gate.GitObjectFormatSHA1),
		CommitSHA:       strings.Repeat("2", 40),
		TreeSHA:         tree,
		InputObjectKey:  "input-1",
		InputSHA256:     "sha256:" + strings.Repeat("a", 64),
		InputSizeBytes:  123,
		BuilderDigest:   "sha256:" + strings.Repeat("b", 64),
		ToolchainDigest: "sha256:" + strings.Repeat("c", 64),
	}
}

func testSourceBaselineBuildEvidence(t *testing.T, input SourceBaselineBuildInput) SourceBaselineBuildEvidence {
	t.Helper()
	receipt := testSourceBaselineProducerReceipt(t)
	artifactKey, receiptKey, err := CanonicalSourceBaselineBuilderKeys(
		receipt.RepositoryID,
		receipt.ObjectFormat,
		receipt.BaselineTreeSHA,
		receipt.ArtifactSHA256,
	)
	if err != nil {
		t.Fatalf("canonical source baseline builder keys: %v", err)
	}
	inputDigest, err := SourceBaselineBuilderInputDigest(input)
	if err != nil {
		t.Fatalf("source baseline builder input digest: %v", err)
	}
	return SourceBaselineBuildEvidence{
		Request:          input,
		InputDigest:      inputDigest,
		Provider:         PortableExecutionProvider,
		ContainerGroupID: "group-1",
		ArtifactKey:      artifactKey,
		ReceiptKey:       receiptKey,
		Receipt:          receipt,
	}
}

func testSourceBaselineProducerReceipt(t *testing.T) SourceBaselineProducerReceipt {
	t.Helper()
	input := testSourceBaselineBuildInput(t)
	baselineCommit, err := DeterministicSourceBaselineCommitSHA(input.TreeSHA, gate.GitObjectFormat(input.ObjectFormat))
	if err != nil {
		t.Fatalf("deterministic receipt commit fixture: %v", err)
	}
	return SourceBaselineProducerReceipt{
		SchemaVersion:     SourceBaselineProducerReceiptSchema,
		ExecutionProvider: PortableExecutionProvider,
		BuilderDigest:     input.BuilderDigest,
		ToolchainDigest:   input.ToolchainDigest,
		RepositoryID:      input.RepositoryID,
		ObjectFormat:      input.ObjectFormat,
		BaselineCommitSHA: baselineCommit,
		BaselineTreeSHA:   input.TreeSHA,
		ArtifactSHA256:    "sha256:" + strings.Repeat("d", 64),
		ArtifactSizeBytes: 456,
	}
}
