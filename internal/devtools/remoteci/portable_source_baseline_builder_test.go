package remoteci

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
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

func TestSourceBaselineBuildEvidenceTerminalFieldsRoundTripAndDynamicGuard(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	evidence := testSourceBaselineBuildEvidence(t, input)
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("json.Marshal(SourceBaselineBuildEvidence) error = %v", err)
	}
	assertSourceBaselineBuildEvidenceJSONContract(t, encoded, evidence)
}

func assertSourceBaselineBuildEvidenceJSONContract(t *testing.T, encoded []byte, evidence SourceBaselineBuildEvidence) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("json.Unmarshal(SourceBaselineBuildEvidence) error = %v", err)
	}
	typeOfEvidence := reflect.TypeFor[SourceBaselineBuildEvidence]()
	assertSourceBaselineBuildEvidenceJSONFields(t, fields, typeOfEvidence)
	assertSourceBaselineBuildEvidenceRoundTrip(t, encoded, evidence)
}

func assertSourceBaselineBuildEvidenceJSONFields(t *testing.T, fields map[string]json.RawMessage, typeOfEvidence reflect.Type) {
	t.Helper()
	if len(fields) != typeOfEvidence.NumField() {
		t.Fatalf("production fields = %d, JSON fields = %d", typeOfEvidence.NumField(), len(fields))
	}
	for field := range typeOfEvidence.Fields() {
		name, _, hasOptions := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" || hasOptions {
			t.Fatalf("field %q has invalid JSON contract", field.Name)
		}
		if _, ok := fields[name]; !ok {
			t.Fatalf("JSON omitted production field %q", field.Name)
		}
	}
	for _, name := range []string{"container_group_status", "container_exit_code", "started_at", "finished_at"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("JSON omitted terminal provider field %q", name)
		}
	}
}

func assertSourceBaselineBuildEvidenceRoundTrip(t *testing.T, encoded []byte, evidence SourceBaselineBuildEvidence) {
	t.Helper()
	var decoded SourceBaselineBuildEvidence
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("terminal evidence round-trip decode error = %v", err)
	}
	if !reflect.DeepEqual(decoded, evidence) {
		t.Fatalf("terminal evidence round-trip = %#v, want %#v", decoded, evidence)
	}
}

func TestSourceBaselineBuildEvidenceRejectsTerminalProviderDrift(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	for name, mutate := range map[string]func(*SourceBaselineBuildEvidence){
		"status":         func(evidence *SourceBaselineBuildEvidence) { evidence.ContainerGroupStatus = "Failed" },
		"exit":           func(evidence *SourceBaselineBuildEvidence) { evidence.ContainerExitCode = 1 },
		"start_format":   func(evidence *SourceBaselineBuildEvidence) { evidence.StartedAt = "1970-01-01T00:00:01+00:00" },
		"finish_order":   func(evidence *SourceBaselineBuildEvidence) { evidence.FinishedAt = evidence.StartedAt },
		"finish_missing": func(evidence *SourceBaselineBuildEvidence) { evidence.FinishedAt = "" },
	} {
		t.Run(name, func(t *testing.T) {
			evidence := testSourceBaselineBuildEvidence(t, input)
			mutate(&evidence)
			if err := evidence.ValidateAgainst(input); err == nil {
				t.Fatal("SourceBaselineBuildEvidence.ValidateAgainst() accepted terminal provider drift")
			}
		})
	}
}

func TestSourceBaselineBuildEvidenceValidationFieldGuard(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	mutators := map[string]func(*SourceBaselineBuildEvidence){
		"Request":              func(evidence *SourceBaselineBuildEvidence) { evidence.Request.RepositoryID = "" },
		"InputDigest":          func(evidence *SourceBaselineBuildEvidence) { evidence.InputDigest = "" },
		"Provider":             func(evidence *SourceBaselineBuildEvidence) { evidence.Provider = "other/v1" },
		"ContainerGroupID":     func(evidence *SourceBaselineBuildEvidence) { evidence.ContainerGroupID = "" },
		"ContainerGroupStatus": func(evidence *SourceBaselineBuildEvidence) { evidence.ContainerGroupStatus = "Failed" },
		"ContainerExitCode":    func(evidence *SourceBaselineBuildEvidence) { evidence.ContainerExitCode = 1 },
		"StartedAt":            func(evidence *SourceBaselineBuildEvidence) { evidence.StartedAt = "1970-01-01T00:00:01+00:00" },
		"FinishedAt":           func(evidence *SourceBaselineBuildEvidence) { evidence.FinishedAt = evidence.StartedAt },
		"ArtifactKey":          func(evidence *SourceBaselineBuildEvidence) { evidence.ArtifactKey += ".drift" },
		"ReceiptKey":           func(evidence *SourceBaselineBuildEvidence) { evidence.ReceiptKey += ".drift" },
		"Receipt":              func(evidence *SourceBaselineBuildEvidence) { evidence.Receipt.SchemaVersion = "" },
	}
	typeOfEvidence := reflect.TypeFor[SourceBaselineBuildEvidence]()
	seen := make(map[string]struct{}, typeOfEvidence.NumField())
	for field := range typeOfEvidence.Fields() {
		mutate, ok := mutators[field.Name]
		if !ok {
			t.Fatalf("evidence field %q has no validation guard mutator", field.Name)
		}
		seen[field.Name] = struct{}{}
		evidence := testSourceBaselineBuildEvidence(t, input)
		mutate(&evidence)
		if err := evidence.ValidateAgainst(input); err == nil {
			t.Fatalf("evidence field %q mutation bypassed ValidateAgainst", field.Name)
		}
	}
	for name := range mutators {
		if _, ok := seen[name]; !ok {
			t.Fatalf("validation guard mutator %q is stale", name)
		}
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

func TestSourceBaselineBuilderInputRejectsEveryZeroField(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	value := reflect.ValueOf(&input).Elem()
	typeOfInput := value.Type()
	for index := 0; index < value.NumField(); index++ {
		name := typeOfInput.Field(index).Name
		t.Run(name, func(t *testing.T) {
			candidate := testSourceBaselineBuildInput(t)
			field := reflect.ValueOf(&candidate).Elem().Field(index)
			field.Set(reflect.Zero(field.Type()))
			if err := candidate.Validate(); err == nil {
				t.Fatalf("SourceBaselineBuildInput.Validate() accepted zero field %s", name)
			}
		})
	}
}

func TestArtifactIdentityDigestBindsExactSourceCommitTreeAndObservedABI(t *testing.T) {
	input := testSourceBaselineBuildInput(t)
	observed := ObservedArtifactBuilderIdentity{BuilderDigest: input.ExpectedBuilderDigest, ToolchainDigest: input.ExpectedToolchainDigest}
	first, err := ArtifactIdentityDigest(input.RepositoryID, input.ObjectFormat, input.CommitSHA, input.TreeSHA, "sha256:"+strings.Repeat("a", 64), 7, observed, input.ProtocolABIDigest, input.WorkloadManifestABIDigest)
	if err != nil {
		t.Fatal(err)
	}
	cases := []func(SourceBaselineBuildInput) (string, error){
		func(value SourceBaselineBuildInput) (string, error) {
			value.CommitSHA = strings.Repeat("3", 40)
			return ArtifactIdentityDigest(value.RepositoryID, value.ObjectFormat, value.CommitSHA, value.TreeSHA, "sha256:"+strings.Repeat("a", 64), 7, observed, value.ProtocolABIDigest, value.WorkloadManifestABIDigest)
		},
		func(value SourceBaselineBuildInput) (string, error) {
			value.TreeSHA = strings.Repeat("4", 40)
			return ArtifactIdentityDigest(value.RepositoryID, value.ObjectFormat, value.CommitSHA, value.TreeSHA, "sha256:"+strings.Repeat("a", 64), 7, observed, value.ProtocolABIDigest, value.WorkloadManifestABIDigest)
		},
		func(value SourceBaselineBuildInput) (string, error) {
			value.ProtocolABIDigest = "sha256:" + strings.Repeat("5", 64)
			return ArtifactIdentityDigest(value.RepositoryID, value.ObjectFormat, value.CommitSHA, value.TreeSHA, "sha256:"+strings.Repeat("a", 64), 7, observed, value.ProtocolABIDigest, value.WorkloadManifestABIDigest)
		},
	}
	for index, mutate := range cases {
		variant, err := mutate(input)
		if err != nil {
			t.Fatalf("variant %d: %v", index, err)
		}
		if variant == first {
			t.Fatalf("variant %d did not change artifact identity", index)
		}
	}
}

// TestValidateSourceBaselineBuilderReceiptKeyRequiresNestedIdentity 锁定 builder receipt 的唯一嵌套身份形状。
func TestValidateSourceBaselineBuilderReceiptKeyRequiresNestedIdentity(t *testing.T) {
	prefix := "baseline-artifacts/builder-simulation/test/"
	_, key, err := CanonicalSourceBaselineBuilderKeys(prefix, "repository-1", "sha1", strings.Repeat("1", 40), "sha256:"+strings.Repeat("2", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSourceBaselineBuilderReceiptKey(key, prefix); err != nil {
		t.Fatalf("ValidateSourceBaselineBuilderReceiptKey() error = %v", err)
	}
	for _, invalid := range []string{
		prefix + "receipts/" + strings.Repeat("2", 64) + ".json",
		strings.Replace(key, "/sha1/", "/sha512/", 1),
		strings.TrimSuffix(key, ".json") + ".receipt",
	} {
		if err := ValidateSourceBaselineBuilderReceiptKey(invalid, prefix); err == nil {
			t.Fatalf("ValidateSourceBaselineBuilderReceiptKey() accepted %q", invalid)
		}
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
	protocolABI, err := BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatalf("builder protocol ABI fixture: %v", err)
	}
	workloadABI, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatalf("workload manifest ABI fixture: %v", err)
	}
	return SourceBaselineBuildInput{
		SchemaVersion:             SourceBaselineBuilderInputSchema,
		ArtifactPrefix:            "builder/",
		RepositoryID:              "repository-1",
		ObjectFormat:              string(gate.GitObjectFormatSHA1),
		CommitSHA:                 strings.Repeat("2", 40),
		TreeSHA:                   tree,
		InputObjectKey:            "builder/inputs/input-1",
		InputSHA256:               "sha256:" + strings.Repeat("a", 64),
		InputSizeBytes:            123,
		ExpectedBuilderDigest:     "sha256:" + strings.Repeat("b", 64),
		ExpectedToolchainDigest:   "sha256:" + strings.Repeat("c", 64),
		ProtocolABIDigest:         protocolABI,
		WorkloadManifestABIDigest: workloadABI,
	}
}

func testSourceBaselineBuildEvidence(t *testing.T, input SourceBaselineBuildInput) SourceBaselineBuildEvidence {
	t.Helper()
	receipt := testSourceBaselineProducerReceipt(t)
	artifactKey, receiptKey, err := CanonicalSourceBaselineBuilderKeys(
		input.ArtifactPrefix,
		receipt.RepositoryID,
		receipt.ObjectFormat,
		receipt.BaselineTreeSHA,
		receipt.ArtifactIdentityDigest,
	)
	if err != nil {
		t.Fatalf("canonical source baseline builder keys: %v", err)
	}
	inputDigest, err := SourceBaselineBuilderInputDigest(input)
	if err != nil {
		t.Fatalf("source baseline builder input digest: %v", err)
	}
	return SourceBaselineBuildEvidence{
		Request:              input,
		InputDigest:          inputDigest,
		Provider:             PortableExecutionProvider,
		ContainerGroupID:     "group-1",
		ContainerGroupStatus: "Succeeded",
		ContainerExitCode:    0,
		StartedAt:            "1970-01-01T00:00:01Z",
		FinishedAt:           "1970-01-01T00:00:02Z",
		ArtifactKey:          artifactKey,
		ReceiptKey:           receiptKey,
		Receipt:              receipt,
	}
}

func testSourceBaselineProducerReceipt(t *testing.T) SourceBaselineProducerReceipt {
	t.Helper()
	input := testSourceBaselineBuildInput(t)
	baselineCommit, err := DeterministicSourceBaselineCommitSHA(input.TreeSHA, gate.GitObjectFormat(input.ObjectFormat))
	if err != nil {
		t.Fatalf("deterministic receipt commit fixture: %v", err)
	}
	receipt := SourceBaselineProducerReceipt{
		SchemaVersion:             SourceBaselineProducerReceiptSchema,
		ExecutionProvider:         PortableExecutionProvider,
		ExpectedBuilderDigest:     input.ExpectedBuilderDigest,
		ExpectedToolchainDigest:   input.ExpectedToolchainDigest,
		ObservedBuilderDigest:     input.ExpectedBuilderDigest,
		ObservedToolchainDigest:   input.ExpectedToolchainDigest,
		ProtocolABIDigest:         input.ProtocolABIDigest,
		WorkloadManifestABIDigest: input.WorkloadManifestABIDigest,
		RepositoryID:              input.RepositoryID,
		ObjectFormat:              input.ObjectFormat,
		SourceCommitSHA:           input.CommitSHA,
		SourceTreeSHA:             input.TreeSHA,
		BaselineCommitSHA:         baselineCommit,
		BaselineTreeSHA:           input.TreeSHA,
		ArtifactSHA256:            "sha256:" + strings.Repeat("d", 64),
		ArtifactSizeBytes:         456,
	}
	receipt.ArtifactIdentityDigest, err = ArtifactIdentityDigest(receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceCommitSHA, receipt.SourceTreeSHA, receipt.ArtifactSHA256, receipt.ArtifactSizeBytes, ObservedArtifactBuilderIdentity{BuilderDigest: receipt.ObservedBuilderDigest, ToolchainDigest: receipt.ObservedToolchainDigest}, receipt.ProtocolABIDigest, receipt.WorkloadManifestABIDigest)
	if err != nil {
		t.Fatalf("artifact identity digest fixture: %v", err)
	}
	return receipt
}
