package localci

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type acceptedImageLoaderStub struct {
	record gate.AcceptedImageRecord
	err    error
	loads  int
}

func (loader *acceptedImageLoaderStub) Load(context.Context) (gate.AcceptedImageRecord, error) {
	loader.loads++
	return loader.record, loader.err
}

type candidateImageBuilderStub struct {
	requests []CandidateRequest
	result   CandidateResult
	err      error
}

func (builder *candidateImageBuilderStub) EnsureCandidate(_ context.Context, request CandidateRequest) (CandidateResult, error) {
	builder.requests = append(builder.requests, request)
	return builder.result, builder.err
}

func TestTruthImageEnsurerReusesAcceptedImageAcrossOrdinarySourceChange(t *testing.T) {
	baseTree := readOnlyImageTree(t, candidateEntries(validCandidateDockerfile()))
	baseInputs := mustResolveGateImageInputs(t, baseTree)
	jobEntries := append(cloneTreeEntries(baseTree.Entries), contextEntry("internal/module/example/service.go", "100644", "package example\n"))
	jobTree := readOnlyImageTree(t, jobEntries)

	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	accepted := acceptedImageRecordForEnsure(baseTree.Source.SourceTreeSHA, baseInputs.ImageInputDigest)
	ensurer := mustTruthImageEnsurer(t, &acceptedImageLoaderStub{record: accepted}, builder)
	result, err := ensurer.EnsureImage(context.Background(), truthImageRequest(jobTree))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != TruthImageEnsureAccepted || len(runner.requests) != 0 {
		t.Fatalf("ordinary source change rebuilt accepted image: result=%+v builds=%d", result, len(runner.requests))
	}
	if result.SubmittedJobSourceTree != jobTree.Source.SourceTreeSHA {
		t.Fatal("ensure result lost submitted job tree")
	}
	if result.AcceptedImageBuildSourceTree != baseTree.Source.SourceTreeSHA {
		t.Fatal("ensure result replaced accepted build provenance with job provenance")
	}
	if result.SubmittedJobSourceTree == result.AcceptedImageBuildSourceTree {
		t.Fatal("ordinary source fixture did not exercise distinct job/build trees")
	}
	if result.Image.PlatformManifestDigest != accepted.Image.PlatformManifestDigest {
		t.Fatal("accepted immutable image identity was not returned")
	}
}

func TestTruthImageEnsurerBuildsChangedInputAndAwaitsTrustedRef(t *testing.T) {
	baseTree := readOnlyImageTree(t, candidateEntries(validCandidateDockerfile()))
	baseInputs := mustResolveGateImageInputs(t, baseTree)
	changedEntries := cloneTreeEntries(baseTree.Entries)
	changeEntry(t, changedEntries, "go.mod", "module example.invalid/changed\n")
	changedTree := readOnlyImageTree(t, changedEntries)
	accepted := acceptedImageRecordForEnsure(baseTree.Source.SourceTreeSHA, baseInputs.ImageInputDigest)

	runner := &recordingBuildKitRunner{digest: digest("8")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	loader := &acceptedImageLoaderStub{record: accepted}
	ensurer := mustTruthImageEnsurer(t, loader, builder)
	result, err := ensurer.EnsureImage(context.Background(), truthImageRequest(changedTree))
	if !errors.Is(err, ErrTruthImageAwaitingTrustedRef) {
		t.Fatalf("changed input ensure error = %v", err)
	}
	if result.Status != TruthImageEnsureAwaitingTrustedRef || len(runner.requests) != 1 {
		t.Fatalf("changed input did not build one awaiting candidate: result=%+v builds=%d", result, len(runner.requests))
	}
	if result.CandidateImageBuildSourceTree != changedTree.Source.SourceTreeSHA {
		t.Fatal("candidate result lost candidate build provenance")
	}
	if result.CandidatePlatformManifestDigest != digest("8") || result.Image.Registry != "" {
		t.Fatal("awaiting candidate exposed a mutable or runnable image identity")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("awaiting result validation error = %v", err)
	}
	if !reflect.DeepEqual(loader.record, accepted) {
		t.Fatal("candidate build mutated the accepted image record")
	}
}

func TestTruthImageEnsurerBuildFailureKeepsAcceptedRecord(t *testing.T) {
	tree := readOnlyImageTree(t, candidateEntries(validCandidateDockerfile()))
	inputs := mustResolveGateImageInputs(t, tree)
	accepted := acceptedImageRecordForEnsure(tree.Source.SourceTreeSHA, inputs.ImageInputDigest)
	changedEntries := cloneTreeEntries(tree.Entries)
	changeEntry(t, changedEntries, "go.mod", "module example.invalid/failure\n")
	changedTree := readOnlyImageTree(t, changedEntries)
	loader := &acceptedImageLoaderStub{record: accepted}
	runner := &recordingBuildKitRunner{err: errors.New("build failed")}
	builder, err := NewImageBuilder(runner)
	if err != nil {
		t.Fatal(err)
	}
	ensurer := mustTruthImageEnsurer(t, loader, builder)
	if _, err := ensurer.EnsureImage(context.Background(), truthImageRequest(changedTree)); err == nil {
		t.Fatal("candidate build failure was accepted")
	}
	if !reflect.DeepEqual(loader.record, accepted) {
		t.Fatal("candidate build failure overwrote accepted state")
	}
}

func TestTruthImageEnsurerRequiresBootstrapTrustRoot(t *testing.T) {
	builder := &candidateImageBuilderStub{}
	ensurer := mustTruthImageEnsurer(t, &acceptedImageLoaderStub{err: ErrAcceptedImageStateNotFound}, builder)
	_, err := ensurer.EnsureImage(context.Background(), truthImageRequest(readOnlyImageTree(t, candidateEntries(validCandidateDockerfile()))))
	if !errors.Is(err, ErrTruthImageBootstrapTrustRoot) {
		t.Fatalf("missing bootstrap trust root error = %v", err)
	}
	if len(builder.requests) != 0 {
		t.Fatal("missing accepted trust root attempted to bootstrap from the submitted checkout")
	}
}

func TestTruthImageEnsureFieldRegistriesAreComplete(t *testing.T) {
	assertRegisteredFields(t, reflect.TypeFor[TruthImageEnsureRequest](), map[string]string{
		"Tree": "verified job tree", "PolicyDigest": "policy binding", "Platform": "platform binding",
	})
	assertRegisteredFields(t, reflect.TypeFor[TruthImageEnsureResult](), map[string]string{
		"Status": "coordinator decision", "SubmittedJobSourceTree": "job provenance",
		"AcceptedRecord":                "accepted authority state",
		"AcceptedImageBuildSourceTree":  "accepted build provenance",
		"CandidateImageBuildSourceTree": "candidate build provenance",
		"PolicyDigest":                  "policy binding", "ImageSchemaVersion": "image schema binding",
		"ImageInputDigest": "reuse evidence", "ContextDigest": "context evidence",
		"InputManifestDigest": "manifest evidence", "ToolchainDigest": "toolchain evidence",
		"DockerfileDigest":                "Dockerfile evidence",
		"CandidatePlatformManifestDigest": "promotion candidate", "Image": "accepted execution identity",
	})
}

func truthImageRequest(tree ReadOnlyGitTree) TruthImageEnsureRequest {
	return TruthImageEnsureRequest{Tree: tree, PolicyDigest: digest("d"), Platform: "linux/arm64"}
}

func acceptedImageRecordForEnsure(sourceTree string, inputDigest string) gate.AcceptedImageRecord {
	now := time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC)
	signer := gate.SignerIdentity{KeyID: "truth-image-test", KeyEpoch: 1, Algorithm: gate.SignatureAlgorithmEd25519}
	return gate.AcceptedImageRecord{
		SchemaVersion: gate.AcceptedImageRecordSchemaVersion,
		RepoID:        "example/repository", TrustedRef: "refs/heads/main",
		TrustedCommit: strings.Repeat("b", 40), SourceTree: sourceTree,
		PolicyDigest: digest("d"), ImageInputDigest: inputDigest,
		Image: gate.ImageIdentity{
			Registry: "registry.invalid/super-dolphin/gate", OCIIndexDigest: digest("1"),
			PlatformManifestDigest: digest("2"), ConfigDigest: digest("3"),
			RootFSDiffIDs: []string{digest("4")}, OS: "linux", Architecture: "arm64",
		},
		Runner: gate.TrustedRunnerIdentity{
			BinaryDigest: digest("5"), Signer: signer, PolicyDigest: digest("d"),
		},
		Generation: 1, AcceptedAt: now, Signer: signer, Signature: "signed-test-record",
	}
}

func mustTruthImageEnsurer(t *testing.T, accepted AcceptedImageLoader, builder CandidateImageBuilder) *TruthImageEnsurer {
	t.Helper()
	ensurer, err := NewTruthImageEnsurer(accepted, builder)
	if err != nil {
		t.Fatal(err)
	}
	return ensurer
}
