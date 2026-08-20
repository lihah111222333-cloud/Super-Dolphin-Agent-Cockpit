package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type testPortableSourceBaselineResolver struct {
	body    io.ReadCloser
	size    int64
	receipt PortableSourceBaselineProducerReceipt
	err     error
}

func (resolver testPortableSourceBaselineResolver) Resolve(context.Context, string, string) (io.ReadCloser, int64, PortableSourceBaselineProducerReceipt, error) {
	return resolver.body, resolver.size, resolver.receipt, resolver.err
}

func TestValidatePortableSourceBaselineArtifactRejectsInvalidExpectedIdentity(t *testing.T) {
	err := ValidatePortableSourceBaselineArtifact(context.Background(), testPortableSourceBaselineResolver{}, PortableRunTarget{})
	if err == nil {
		t.Fatal("ValidatePortableSourceBaselineArtifact accepted an invalid expected identity")
	}
}

type testPortableSourceBaselineCloser struct {
	*bytes.Reader
	closed int
}

func (closer *testPortableSourceBaselineCloser) Close() error {
	closer.closed++
	return nil
}

func TestValidatePortableSourceBaselineArtifactValidatesBundleAndReceipt(t *testing.T) {
	fixture := buildPortableSourceBaselineFixture(t)
	closer := &testPortableSourceBaselineCloser{Reader: bytes.NewReader(fixture.bundle)}
	resolver := testPortableSourceBaselineResolver{body: closer, size: int64(len(fixture.bundle)), receipt: fixture.receipt}
	if err := ValidatePortableSourceBaselineArtifact(context.Background(), resolver, fixture.target); err != nil {
		t.Fatalf("ValidatePortableSourceBaselineArtifact() error = %v", err)
	}
	if closer.closed != 1 {
		t.Fatalf("artifact body Close count = %d, want 1", closer.closed)
	}
}

func TestValidatePortableSourceBaselineArtifactClosesBodyOnDigestFailure(t *testing.T) {
	fixture := buildPortableSourceBaselineFixture(t)
	closer := &testPortableSourceBaselineCloser{Reader: bytes.NewReader(fixture.bundle)}
	badTarget := fixture.target
	badTarget.SourceBaseline.ArtifactSHA256 = "sha256:" + strings.Repeat("b", 64)
	resolver := testPortableSourceBaselineResolver{body: closer, size: int64(len(fixture.bundle)), receipt: fixture.receipt}
	if err := ValidatePortableSourceBaselineArtifact(context.Background(), resolver, badTarget); err == nil {
		t.Fatal("ValidatePortableSourceBaselineArtifact() accepted a digest mismatch")
	}
	if closer.closed != 1 {
		t.Fatalf("artifact body Close count = %d, want 1", closer.closed)
	}
}

func TestValidatePortableSourceBaselineArtifactRejectsReceiptRepositoryDrift(t *testing.T) {
	fixture := buildPortableSourceBaselineFixture(t)
	closer := &testPortableSourceBaselineCloser{Reader: bytes.NewReader(fixture.bundle)}
	fixture.receipt.RepositoryID = "repository-2"
	resolver := testPortableSourceBaselineResolver{body: closer, size: int64(len(fixture.bundle)), receipt: fixture.receipt}
	if err := ValidatePortableSourceBaselineArtifact(context.Background(), resolver, fixture.target); err == nil {
		t.Fatal("ValidatePortableSourceBaselineArtifact() accepted a repository identity drift")
	}
	if closer.closed != 1 {
		t.Fatalf("artifact body Close count = %d, want 1", closer.closed)
	}
}

type portableSourceBaselineFixture struct {
	bundle  []byte
	target  PortableRunTarget
	receipt PortableSourceBaselineProducerReceipt
}

func buildPortableSourceBaselineFixture(t *testing.T) portableSourceBaselineFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	repository := filepath.Join(root, "source.git")
	if err := initBareRepository(ctx, root, repository, gate.GitObjectFormatSHA1); err != nil {
		t.Fatalf("initBareRepository() error = %v", err)
	}
	treeOutput, err := runGitOutput(ctx, repository, bytes.NewReader(nil), "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		t.Fatalf("write tree: %v", err)
	}
	tree, err := strictGitLine(treeOutput)
	if err != nil {
		t.Fatalf("tree identity: %v", err)
	}
	commitPayload := deterministicSourceBaselinePayload(tree)
	commitOutput, err := runGitOutput(ctx, repository, bytes.NewReader(commitPayload), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		t.Fatalf("write commit: %v", err)
	}
	commit, err := strictGitLine(commitOutput)
	if err != nil {
		t.Fatalf("commit identity: %v", err)
	}
	refInput := fmt.Sprintf("create %s %s\n", PortableSourceBaselineArtifactRef, commit)
	if err := runGit(ctx, repository, strings.NewReader(refInput), io.Discard, "update-ref", "--stdin"); err != nil {
		t.Fatalf("write ref: %v", err)
	}
	bundlePath := filepath.Join(root, "source.bundle")
	if err := runGit(ctx, repository, nil, io.Discard, "bundle", "create", bundlePath, "--end-of-options", PortableSourceBaselineArtifactRef); err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	digest := sha256.Sum256(bundle)
	artifactDigest := "sha256:" + hex.EncodeToString(digest[:])
	receipt := PortableSourceBaselineProducerReceipt{
		ExecutionProvider: PortableExecutionProvider,
		BuilderDigest:     "sha256:" + strings.Repeat("a", 64),
		ToolchainDigest:   "sha256:" + strings.Repeat("c", 64),
		RepositoryID:      "repository-1",
		ObjectFormat:      string(gate.GitObjectFormatSHA1),
		BaselineCommitSHA: commit,
		BaselineTreeSHA:   tree,
		ArtifactSHA256:    artifactDigest,
		ArtifactSizeBytes: int64(len(bundle)),
	}
	expected := PortableSourceBaseline{
		ObjectFormat:      receipt.ObjectFormat,
		BaselineTreeSHA:   tree,
		BaselineCommitSHA: commit,
		BundleContractID:  PortableBundleContractID,
		ArtifactKind:      PortableSourceBaselineArtifactKind,
		ArtifactKey:       "baseline-key-1",
		ArtifactSHA256:    artifactDigest,
		ArtifactSizeBytes: int64(len(bundle)),
	}
	target := PortableRunTarget{RepositoryID: receipt.RepositoryID, ObjectFormat: receipt.ObjectFormat, SourceBaseline: expected}
	return portableSourceBaselineFixture{bundle: bundle, target: target, receipt: receipt}
}
