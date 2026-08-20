package remoteci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestSourceBaselineBuilderWorkerBuildsAndPublishesInOrder(t *testing.T) {
	fixture := newSourceBaselineBuilderWorkerFixture(t)
	var calls []string
	worker, err := NewSourceBaselineBuilderWorker(ArtifactIO{
		DownloadInput: func(_ context.Context, key string, destination io.Writer) (int64, error) {
			if key != fixture.input.InputObjectKey {
				return 0, fmt.Errorf("unexpected input key %q", key)
			}
			calls = append(calls, "download")
			file, err := os.Open(fixture.inputPath)
			if err != nil {
				return 0, err
			}
			defer func() { _ = file.Close() }()
			return io.Copy(destination, file)
		},
		UploadArtifact: func(_ context.Context, path, key string, metadata SourceBaselineArtifactMetadata) error {
			calls = append(calls, "artifact")
			return assertSourceBaselineWorkerUpload(t, path, key, metadata)
		},
		UploadReceipt: func(_ context.Context, path, key string, metadata SourceBaselineArtifactMetadata) error {
			calls = append(calls, "receipt")
			return assertSourceBaselineWorkerUpload(t, path, key, metadata)
		},
	}, sourceBaselineBuilderWorkerObserver(fixture.input))
	if err != nil {
		t.Fatalf("NewSourceBaselineBuilderWorker() error = %v", err)
	}
	evidence, err := worker.Build(context.Background(), fixture.input)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if err := evidence.ValidateAgainst(fixture.input); err != nil {
		t.Fatalf("returned evidence validation error = %v", err)
	}
	if got, want := strings.Join(calls, ","), "download,artifact,receipt"; got != want {
		t.Fatalf("IO call order = %q, want %q", got, want)
	}
}

func TestSourceBaselineBuilderWorkerRejectsWrongInputTree(t *testing.T) {
	fixture := newSourceBaselineBuilderWorkerFixture(t)
	input := fixture.input
	input.TreeSHA = strings.Repeat("f", 40)
	worker, err := NewSourceBaselineBuilderWorker(sourceBaselineBuilderWorkerIO(fixture, nil), sourceBaselineBuilderWorkerObserver(input))
	if err != nil {
		t.Fatalf("NewSourceBaselineBuilderWorker() error = %v", err)
	}
	if _, err := worker.Build(context.Background(), input); err == nil {
		t.Fatal("Build() accepted a source commit/tree mismatch")
	}
}

func TestSourceBaselineBuilderWorkerRejectsWrongRefAndPrerequisite(t *testing.T) {
	fixture := newSourceBaselineBuilderWorkerFixture(t)
	cases := map[string]func([]byte) []byte{
		"wrong ref": func(data []byte) []byte {
			return bytes.Replace(data, []byte(sourceBaselineBuilderInputRef), []byte("refs/source/wrong"), 1)
		},
		"prerequisite": func(data []byte) []byte {
			separator := bytes.Index(data, []byte("\n\n"))
			if separator < 0 {
				t.Fatalf("input bundle has no header separator")
			}
			malformed := append([]byte(nil), data[:separator+1]...)
			malformed = append(malformed, []byte("-"+strings.Repeat("0", 40)+"\n")...)
			return append(malformed, data[separator+1:]...)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			variant := rewriteSourceBaselineBuilderWorkerInput(t, fixture, mutate)
			worker, err := NewSourceBaselineBuilderWorker(sourceBaselineBuilderWorkerIO(variant, nil), sourceBaselineBuilderWorkerObserver(variant.input))
			if err != nil {
				t.Fatalf("NewSourceBaselineBuilderWorker() error = %v", err)
			}
			if _, err := worker.Build(context.Background(), variant.input); err == nil {
				t.Fatalf("Build() accepted %s", name)
			}
		})
	}
}

func TestSourceBaselineBuilderWorkerRejectsUnreachableExtraObject(t *testing.T) {
	fixture := newSourceBaselineBuilderWorkerFixture(t)
	extraOutput, err := runGitOutput(context.Background(), fixture.repository, bytes.NewReader([]byte("unreachable")), "hash-object", "-t", "blob", "-w", "--stdin")
	if err != nil {
		t.Fatalf("write unreachable blob: %v", err)
	}
	extra, err := strictGitLine(extraOutput)
	if err != nil {
		t.Fatalf("unreachable blob identity: %v", err)
	}
	pack, err := runGitOutput(context.Background(), fixture.repository, strings.NewReader(fixture.input.CommitSHA+"\n"+extra+"\n"), "pack-objects", "--stdout", "--revs")
	if err != nil {
		t.Fatalf("pack extra object bundle: %v", err)
	}
	original, err := os.ReadFile(fixture.inputPath)
	if err != nil {
		t.Fatalf("read input bundle: %v", err)
	}
	separator := bytes.Index(original, []byte("\n\n"))
	if separator < 0 {
		t.Fatal("input bundle has no header separator")
	}
	malformed := append([]byte(nil), original[:separator+2]...)
	malformed = append(malformed, pack...)
	variant := rewriteSourceBaselineBuilderWorkerInput(t, fixture, func([]byte) []byte { return malformed })
	worker, err := NewSourceBaselineBuilderWorker(sourceBaselineBuilderWorkerIO(variant, nil), sourceBaselineBuilderWorkerObserver(variant.input))
	if err != nil {
		t.Fatalf("NewSourceBaselineBuilderWorker() error = %v", err)
	}
	if _, err := worker.Build(context.Background(), variant.input); err == nil {
		t.Fatal("Build() accepted a bundle with an unreachable extra object")
	}
}

func TestSourceBaselineBuilderWorkerUploadsReceiptOnlyAfterArtifact(t *testing.T) {
	fixture := newSourceBaselineBuilderWorkerFixture(t)
	beforeTemps := sourceBaselineBuilderWorkerTempNames(t)
	var calls []string
	worker, err := NewSourceBaselineBuilderWorker(ArtifactIO{
		DownloadInput: func(_ context.Context, _ string, destination io.Writer) (int64, error) {
			calls = append(calls, "download")
			return copySourceBaselineWorkerFixture(destination, fixture.inputPath)
		},
		UploadArtifact: func(context.Context, string, string, SourceBaselineArtifactMetadata) error {
			calls = append(calls, "artifact")
			return errorsSourceBaselineBuilderWorkerUpload
		},
		UploadReceipt: func(context.Context, string, string, SourceBaselineArtifactMetadata) error {
			calls = append(calls, "receipt")
			return nil
		},
	}, sourceBaselineBuilderWorkerObserver(fixture.input))
	if err != nil {
		t.Fatalf("NewSourceBaselineBuilderWorker() error = %v", err)
	}
	if _, err := worker.Build(context.Background(), fixture.input); err == nil {
		t.Fatal("Build() accepted an artifact upload failure")
	}
	if got, want := strings.Join(calls, ","), "download,artifact"; got != want {
		t.Fatalf("IO call order after artifact failure = %q, want %q", got, want)
	}
	for name := range sourceBaselineBuilderWorkerTempNames(t) {
		if !beforeTemps[name] {
			t.Fatalf("builder temporary root %q was not cleaned up", name)
		}
	}
}

func TestSourceBaselineBuilderWorkerFailureReportsTypedStageWithoutCause(t *testing.T) {
	fixture := newSourceBaselineBuilderWorkerFixture(t)
	worker, err := NewSourceBaselineBuilderWorker(ArtifactIO{
		DownloadInput: func(_ context.Context, _ string, destination io.Writer) (int64, error) {
			return copySourceBaselineWorkerFixture(destination, fixture.inputPath)
		},
		UploadArtifact: func(context.Context, string, string, SourceBaselineArtifactMetadata) error {
			return errorsSourceBaselineBuilderWorkerUpload
		},
		UploadReceipt: func(context.Context, string, string, SourceBaselineArtifactMetadata) error { return nil },
	}, sourceBaselineBuilderWorkerObserver(fixture.input))
	if err != nil {
		t.Fatalf("NewSourceBaselineBuilderWorker() error = %v", err)
	}
	_, err = worker.Build(context.Background(), fixture.input)
	if err == nil {
		t.Fatal("Build() accepted injected artifact upload failure")
	}
	var stageErr SourceBaselineBuilderStageError
	if !errors.As(err, &stageErr) {
		t.Fatalf("Build() error type = %T, want SourceBaselineBuilderStageError", err)
	}
	if stageErr.Stage != SourceBaselineBuilderStageUploadArtifact {
		t.Fatalf("Build() failure stage = %q, want %q", stageErr.Stage, SourceBaselineBuilderStageUploadArtifact)
	}
	if strings.Contains(err.Error(), "injected artifact upload failure") {
		t.Fatalf("Build() error leaked raw cause: %q", err)
	}
}

var errorsSourceBaselineBuilderWorkerUpload = fmt.Errorf("injected artifact upload failure")

type sourceBaselineBuilderWorkerFixture struct {
	input      SourceBaselineBuildInput
	inputPath  string
	repository string
}

func newSourceBaselineBuilderWorkerFixture(t *testing.T) sourceBaselineBuilderWorkerFixture {
	t.Helper()
	_, repository, bundlePath, tree, commit := buildSourceBaselineWorkerBundle(t)
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read source input bundle: %v", err)
	}
	digest := sha256.Sum256(bundle)
	protocolABI, err := BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatalf("builder protocol ABI fixture: %v", err)
	}
	workloadABI, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatalf("workload manifest ABI fixture: %v", err)
	}
	input := SourceBaselineBuildInput{SchemaVersion: SourceBaselineBuilderInputSchema, ArtifactPrefix: "worker/", RepositoryID: "repository-1", ObjectFormat: string(gate.GitObjectFormatSHA1), CommitSHA: commit, TreeSHA: tree,
		InputObjectKey: "worker/inputs/input-1", InputSHA256: "sha256:" + hex.EncodeToString(digest[:]), InputSizeBytes: int64(len(bundle)), ExpectedBuilderDigest: "sha256:" + strings.Repeat("b", 64), ExpectedToolchainDigest: "sha256:" + strings.Repeat("c", 64), ProtocolABIDigest: protocolABI, WorkloadManifestABIDigest: workloadABI}
	return sourceBaselineBuilderWorkerFixture{input: input, inputPath: bundlePath, repository: repository}
}

func buildSourceBaselineWorkerBundle(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "source.git")
	if err := initBareRepository(context.Background(), root, repository, gate.GitObjectFormatSHA1); err != nil {
		t.Fatalf("initBareRepository() error = %v", err)
	}
	treeOutput, err := runGitOutput(context.Background(), repository, bytes.NewReader(nil), "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		t.Fatalf("write source tree: %v", err)
	}
	tree, err := strictGitLine(treeOutput)
	if err != nil {
		t.Fatalf("source tree identity: %v", err)
	}
	commitOutput, err := runGitOutput(context.Background(), repository, bytes.NewReader(deterministicSourceBaselinePayload(tree)), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		t.Fatalf("write source commit: %v", err)
	}
	commit, err := strictGitLine(commitOutput)
	if err != nil {
		t.Fatalf("source commit identity: %v", err)
	}
	if err := runGit(context.Background(), repository, strings.NewReader(fmt.Sprintf("create %s %s\n", sourceBaselineBuilderInputRef, commit)), io.Discard, "update-ref", "--stdin"); err != nil {
		t.Fatalf("install source input ref: %v", err)
	}
	bundlePath := filepath.Join(root, "builder-input.bundle")
	if err := runGit(context.Background(), repository, nil, io.Discard, "bundle", "create", bundlePath, "--end-of-options", sourceBaselineBuilderInputRef); err != nil {
		t.Fatalf("create source input bundle: %v", err)
	}
	return root, repository, bundlePath, tree, commit
}

func sourceBaselineBuilderWorkerObserver(input SourceBaselineBuildInput) ArtifactBuilderIdentityObserver {
	return func(context.Context) (ObservedArtifactBuilderIdentity, error) {
		return ObservedArtifactBuilderIdentity{BuilderDigest: input.ExpectedBuilderDigest, ToolchainDigest: input.ExpectedToolchainDigest}, nil
	}
}

func rewriteSourceBaselineBuilderWorkerInput(t *testing.T, fixture sourceBaselineBuilderWorkerFixture, mutate func([]byte) []byte) sourceBaselineBuilderWorkerFixture {
	t.Helper()
	original, err := os.ReadFile(fixture.inputPath)
	if err != nil {
		t.Fatalf("read source input bundle: %v", err)
	}
	data := mutate(original)
	path := filepath.Join(t.TempDir(), "builder-input-variant.bundle")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write source input bundle variant: %v", err)
	}
	digest := sha256.Sum256(data)
	fixture.input.InputSHA256 = "sha256:" + hex.EncodeToString(digest[:])
	fixture.input.InputSizeBytes = int64(len(data))
	fixture.inputPath = path
	return fixture
}

func sourceBaselineBuilderWorkerIO(fixture sourceBaselineBuilderWorkerFixture, order *[]string) ArtifactIO {
	return ArtifactIO{
		DownloadInput: func(_ context.Context, _ string, destination io.Writer) (int64, error) {
			if order != nil {
				*order = append(*order, "download")
			}
			return copySourceBaselineWorkerFixture(destination, fixture.inputPath)
		},
		UploadArtifact: func(context.Context, string, string, SourceBaselineArtifactMetadata) error { return nil },
		UploadReceipt:  func(context.Context, string, string, SourceBaselineArtifactMetadata) error { return nil },
	}
}

func copySourceBaselineWorkerFixture(destination io.Writer, path string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	return io.Copy(destination, file)
}

func assertSourceBaselineWorkerUpload(t *testing.T, path, key string, metadata SourceBaselineArtifactMetadata) error {
	t.Helper()
	if path == "" || key == "" || metadata.SizeBytes <= 0 || !validPortableDigest(metadata.SHA256) {
		return fmt.Errorf("invalid upload metadata path=%q key=%q metadata=%#v", path, key, metadata)
	}
	digest, size, err := digestSourceBaselineBuilderFile(path)
	if err != nil {
		return err
	}
	if digest != metadata.SHA256 || size != metadata.SizeBytes {
		return fmt.Errorf("upload metadata digest/size mismatch")
	}
	return nil
}

func sourceBaselineBuilderWorkerTempNames(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read system temporary directory: %v", err)
	}
	names := make(map[string]bool)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), sourceBaselineBuilderTempPrefix) {
			names[entry.Name()] = true
		}
	}
	return names
}
