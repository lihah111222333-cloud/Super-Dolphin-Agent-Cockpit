package remoteci

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RuntimeArtifactCompiler 只在 ECI worker 内把 exact engine tree 编译为固定 executable。
type RuntimeArtifactCompiler func(context.Context, string, string) error

// RuntimeArtifactBuilderWorker 复用唯一 OSS IO seam 生产 executable/receipt pair。
type RuntimeArtifactBuilderWorker struct {
	IO       ArtifactIO
	Observer ArtifactBuilderIdentityObserver
	Compiler RuntimeArtifactCompiler
}

// NewRuntimeArtifactBuilderWorker 校验 worker 的唯一 IO、identity 与 compiler 端口。
func NewRuntimeArtifactBuilderWorker(artifactIO ArtifactIO, observer ArtifactBuilderIdentityObserver, compiler RuntimeArtifactCompiler) (*RuntimeArtifactBuilderWorker, error) {
	worker := &RuntimeArtifactBuilderWorker{IO: artifactIO, Observer: observer, Compiler: compiler}
	if err := worker.validate(); err != nil {
		return nil, err
	}
	return worker, nil
}

// validate 校验 worker IO、identity observer 与 compiler 均为显式非空依赖。
func (worker *RuntimeArtifactBuilderWorker) validate() error {
	if worker == nil || worker.IO.DownloadInput == nil || worker.IO.UploadArtifact == nil || worker.IO.UploadReceipt == nil || worker.Observer == nil || worker.Compiler == nil {
		return errors.New("runtime artifact builder worker dependencies are incomplete")
	}
	return nil
}

// Build 在私有临时根内下载 exact engine bundle、编译、复核并顺序发布 pair。
func (worker *RuntimeArtifactBuilderWorker) Build(ctx context.Context, input RuntimeArtifactBuildInput) (result RuntimeArtifactBuilderWorkerResult, err error) {
	if err := worker.validate(); err != nil {
		return result, err
	}
	if ctx == nil {
		return result, errors.New("runtime artifact builder context is nil")
	}
	if err := input.Validate(); err != nil {
		return result, err
	}
	root, err := os.MkdirTemp("", ".runtime-artifact-builder-")
	if err != nil {
		return result, errors.New("create runtime artifact temporary root")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return result, errors.New("protect runtime artifact temporary root")
	}
	defer func() {
		if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
			result = RuntimeArtifactBuilderWorkerResult{}
			err = errors.Join(err, errors.New("cleanup runtime artifact temporary root"))
		}
	}()
	return worker.build(ctx, input, root)
}

// build 在私有根中导入 exact commit/tree，并只调用注入的 ECI compiler。
func (worker *RuntimeArtifactBuilderWorker) build(ctx context.Context, input RuntimeArtifactBuildInput, root string) (RuntimeArtifactBuilderWorkerResult, error) {
	sourceInput := runtimeArtifactSourceInput(input)
	bundlePath := filepath.Join(root, "engine-input.bundle")
	sourceWorker := &SourceBaselineBuilderWorker{IO: worker.IO}
	if err := sourceWorker.downloadAndVerifyInput(ctx, sourceInput, bundlePath); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	repository := filepath.Join(root, "engine.git")
	if err := importSourceBaselineBuilderInput(ctx, bundlePath, repository, root, sourceInput); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	sourceRoot := filepath.Join(root, "engine")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, errors.New("create engine materialization root")
	}
	if err := runGit(ctx, repository, nil, io.Discard, "--work-tree", sourceRoot, "checkout", "--force", input.CommitSHA); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, errors.New("materialize exact engine tree")
	}
	artifactPath := filepath.Join(root, RuntimeArtifactExecutableName)
	if err := worker.Compiler(ctx, sourceRoot, artifactPath); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, errors.New("compile runtime artifact")
	}
	artifactDigest, artifactSize, err := validateRuntimeArtifactExecutable(artifactPath)
	if err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	observed, err := worker.Observer(ctx)
	if err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, errors.New("observe runtime artifact builder identity")
	}
	if err := validateRuntimeArtifactObserved(input, observed); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	return worker.publish(ctx, input, observed, artifactPath, artifactDigest, artifactSize, root)
}

func runtimeArtifactSourceInput(input RuntimeArtifactBuildInput) SourceBaselineBuildInput {
	return SourceBaselineBuildInput{ArtifactPrefix: input.ArtifactPrefix, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat,
		CommitSHA: input.CommitSHA, TreeSHA: input.TreeSHA, InputObjectKey: input.InputObjectKey,
		InputSHA256: input.InputSHA256, InputSizeBytes: input.InputSizeBytes}
}

func validateRuntimeArtifactObserved(input RuntimeArtifactBuildInput, observed ObservedArtifactBuilderIdentity) error {
	if err := validateBuilderDigests(observed.BuilderDigest, observed.ToolchainDigest, "runtime artifact observed"); err != nil {
		return err
	}
	if observed.BuilderDigest != input.ExpectedBuilderDigest || observed.ToolchainDigest != input.ExpectedToolchainDigest {
		return errors.New("runtime artifact observed builder identity drifted")
	}
	return nil
}

// publish 先构造并上传 executable，再上传绑定同一 identity 的 canonical receipt。
func (worker *RuntimeArtifactBuilderWorker) publish(ctx context.Context, input RuntimeArtifactBuildInput, observed ObservedArtifactBuilderIdentity, artifactPath, artifactDigest string, artifactSize int64, root string) (RuntimeArtifactBuilderWorkerResult, error) {
	binding := PortableExecutionArtifact{ContractID: input.ContractID, Kind: input.ArtifactKind, ArtifactSHA256: artifactDigest, ArtifactSizeBytes: artifactSize, BuilderDigest: observed.BuilderDigest, ToolchainDigest: observed.ToolchainDigest, ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest}
	identity, err := PortableExecutionRuntimeArtifactIdentityDigest(input.RepositoryID, input.ObjectFormat, input.CommitSHA, input.TreeSHA, binding)
	if err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	artifactKey, receiptKey, err := CanonicalRuntimeArtifactBuilderKeys(input.ArtifactPrefix, input.RepositoryID, input.ObjectFormat, input.TreeSHA, identity)
	if err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	receipt := RuntimeArtifactProducerReceipt{SchemaVersion: RuntimeArtifactBuilderReceiptSchema, ContractID: input.ContractID, ArtifactKind: input.ArtifactKind, ExecutionProvider: input.ExecutionProvider, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat, SourceCommitSHA: input.CommitSHA, SourceTreeSHA: input.TreeSHA, InputSHA256: input.InputSHA256, InputSizeBytes: input.InputSizeBytes, ObservedBuilderDigest: observed.BuilderDigest, ObservedToolchainDigest: observed.ToolchainDigest, ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest, ArtifactKey: artifactKey, ArtifactSHA256: artifactDigest, ArtifactSizeBytes: artifactSize, ArtifactIdentityDigest: identity}
	receiptBytes, receiptDigest, err := EncodeRuntimeArtifactProducerReceipt(receipt)
	if err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	receiptPath := filepath.Join(root, "runtime-artifact-receipt.json")
	if err := os.WriteFile(receiptPath, receiptBytes, 0o600); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, errors.New("write runtime artifact receipt")
	}
	metadata := SourceBaselineArtifactMetadata{SHA256: artifactDigest, SizeBytes: artifactSize, ArtifactIdentityDigest: identity}
	if err := worker.IO.UploadArtifact(ctx, artifactPath, artifactKey, metadata); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	receiptMetadata := SourceBaselineArtifactMetadata{SHA256: bytesDigest(receiptBytes), SizeBytes: int64(len(receiptBytes)), ArtifactIdentityDigest: identity}
	if err := worker.IO.UploadReceipt(ctx, receiptPath, receiptKey, receiptMetadata); err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	inputDigest, err := RuntimeArtifactBuilderInputDigest(input)
	if err != nil {
		return RuntimeArtifactBuilderWorkerResult{}, err
	}
	result := RuntimeArtifactBuilderWorkerResult{ArtifactKey: artifactKey, ReceiptKey: receiptKey, ArtifactSHA256: artifactDigest, ArtifactSizeBytes: artifactSize, ReceiptSHA256: receiptDigest, ArtifactIdentityDigest: identity, InputDigest: inputDigest}
	return result, result.ValidateAgainst(input)
}

// ValidateRuntimeArtifactExecutable 只接受有界 ELF64 little-endian x86-64 executable。
func ValidateRuntimeArtifactExecutable(path string) (string, int64, error) {
	return validateRuntimeArtifactExecutable(path)
}

// validateRuntimeArtifactExecutable 复核文件形状、ELF header、执行位、摘要与大小。
func validateRuntimeArtifactExecutable(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, errors.New("open runtime artifact")
	}
	header := make([]byte, 20)
	_, readErr := io.ReadFull(file, header)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return "", 0, errors.New("read runtime artifact ELF header")
	}
	if err := validateRuntimeArtifactELFHeader(header); err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, errors.New("stat runtime artifact")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", 0, errors.New("runtime artifact is not a regular executable")
	}
	digest, size, err := digestSourceBaselineBuilderFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("digest runtime artifact: %w", err)
	}
	return digest, size, nil
}

// validateRuntimeArtifactELFHeader 校验 ELF magic、class、endianness 与 x86-64 machine。
func validateRuntimeArtifactELFHeader(header []byte) error {
	if len(header) < 20 || !bytesEqualRuntime(header[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return errors.New("runtime artifact ELF magic is invalid")
	}
	if header[4] != 2 || header[5] != 1 || binary.LittleEndian.Uint16(header[18:20]) != 62 {
		return errors.New("runtime artifact is not linux/amd64 ELF")
	}
	return nil
}

func bytesEqualRuntime(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
