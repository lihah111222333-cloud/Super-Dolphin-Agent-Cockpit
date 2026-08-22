package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestRuntimeArtifactBuilderWorkerPublishesLinuxAMD64ExecutablePair(t *testing.T) {
	fixture := newSourceBaselineBuilderWorkerFixture(t)
	input := runtimeArtifactInputFromSourceFixture(t, fixture)
	worker, capture := newRuntimeArtifactTestWorker(t, fixture, input)
	result, err := worker.Build(context.Background(), input)
	requireNoRuntimeArtifactTestError(t, "Build", err)
	assertRuntimeArtifactWorkerResult(t, input, result, capture)
}

type runtimeArtifactWorkerCapture struct {
	calls                  []string
	artifactBytes, receipt []byte
}

func newRuntimeArtifactTestWorker(t *testing.T, fixture sourceBaselineBuilderWorkerFixture, input RuntimeArtifactBuildInput) (*RuntimeArtifactBuilderWorker, *runtimeArtifactWorkerCapture) {
	t.Helper()
	capture := &runtimeArtifactWorkerCapture{}
	worker, err := NewRuntimeArtifactBuilderWorker(sourceBaselineBuilderWorkerIO(fixture, &capture.calls), sourceBaselineBuilderWorkerObserverForRuntime(input), func(_ context.Context, sourceRoot, outputPath string) error {
		if sourceRoot == "" || outputPath == "" {
			return os.ErrInvalid
		}
		capture.calls = append(capture.calls, "compile")
		return os.WriteFile(outputPath, fakeLinuxAMD64Executable(), 0o500)
	})
	requireNoRuntimeArtifactTestError(t, "NewRuntimeArtifactBuilderWorker", err)
	worker.IO.UploadArtifact = func(_ context.Context, path, key string, metadata SourceBaselineArtifactMetadata) error {
		capture.calls = append(capture.calls, "artifact")
		if !strings.HasSuffix(key, "/super-dolphin-remote-ci-linux-amd64") {
			t.Fatalf("artifact key = %q", key)
		}
		capture.artifactBytes = mustReadRuntimeArtifactFile(t, path, metadata)
		return nil
	}
	worker.IO.UploadReceipt = func(_ context.Context, path, key string, metadata SourceBaselineArtifactMetadata) error {
		capture.calls = append(capture.calls, "receipt")
		if !strings.HasSuffix(key, ".json") {
			t.Fatalf("receipt key = %q", key)
		}
		capture.receipt = mustReadRuntimeArtifactFile(t, path, metadata)
		return nil
	}
	return worker, capture
}

func assertRuntimeArtifactWorkerResult(t *testing.T, input RuntimeArtifactBuildInput, result RuntimeArtifactBuilderWorkerResult, capture *runtimeArtifactWorkerCapture) {
	t.Helper()
	if got, want := strings.Join(capture.calls, ","), "download,compile,artifact,receipt"; got != want {
		t.Fatalf("worker calls = %q, want %q", got, want)
	}
	if bytes.HasPrefix(capture.artifactBytes, []byte("# v2 git bundle")) || !bytes.Equal(capture.artifactBytes, fakeLinuxAMD64Executable()) {
		t.Fatalf("artifact is not the compiled executable: %x", capture.artifactBytes)
	}
	receipt, err := DecodeRuntimeArtifactProducerReceipt(capture.receipt)
	requireNoRuntimeArtifactTestError(t, "DecodeRuntimeArtifactProducerReceipt", err)
	requireNoRuntimeArtifactTestError(t, "result validation", result.ValidateAgainst(input))
	if receipt.ArtifactKey != result.ArtifactKey || receipt.ArtifactSHA256 != result.ArtifactSHA256 || receipt.ArtifactSizeBytes != int64(len(capture.artifactBytes)) {
		t.Fatalf("receipt/result artifact binding drifted: receipt=%#v result=%#v", receipt, result)
	}
}

func runtimeArtifactInputFromSourceFixture(t *testing.T, fixture sourceBaselineBuilderWorkerFixture) RuntimeArtifactBuildInput {
	t.Helper()
	input := newRuntimeArtifactBuildInput(t)
	input.RepositoryID = fixture.input.RepositoryID
	input.ArtifactPrefix = fixture.input.ArtifactPrefix
	input.ObjectFormat = fixture.input.ObjectFormat
	input.CommitSHA = fixture.input.CommitSHA
	input.TreeSHA = fixture.input.TreeSHA
	input.InputObjectKey = fixture.input.InputObjectKey
	input.InputSHA256 = fixture.input.InputSHA256
	input.InputSizeBytes = fixture.input.InputSizeBytes
	input.ExpectedBuilderDigest = fixture.input.ExpectedBuilderDigest
	input.ExpectedToolchainDigest = fixture.input.ExpectedToolchainDigest
	return input
}

func sourceBaselineBuilderWorkerObserverForRuntime(input RuntimeArtifactBuildInput) ArtifactBuilderIdentityObserver {
	return func(context.Context) (ObservedArtifactBuilderIdentity, error) {
		return ObservedArtifactBuilderIdentity{BuilderDigest: input.ExpectedBuilderDigest, ToolchainDigest: input.ExpectedToolchainDigest}, nil
	}
}

func fakeLinuxAMD64Executable() []byte {
	data := make([]byte, 64)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	data[16], data[17] = 2, 0
	data[18], data[19] = 62, 0
	copy(data[32:], []byte("super-dolphin-remote-ci"))
	return data
}

func mustReadRuntimeArtifactFile(t *testing.T, path string, metadata SourceBaselineArtifactMetadata) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read upload file: %v", err)
	}
	hash := sha256.Sum256(data)
	if got := "sha256:" + hex.EncodeToString(hash[:]); got != metadata.SHA256 || int64(len(data)) != metadata.SizeBytes {
		t.Fatalf("upload metadata = %#v, bytes=%d digest=%s", metadata, len(data), got)
	}
	return data
}

func requireNoRuntimeArtifactTestError(t *testing.T, scope string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", scope, err)
	}
}
