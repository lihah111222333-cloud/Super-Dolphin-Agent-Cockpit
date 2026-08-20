package remoteci

import (
	"bufio"
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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	// sourceBaselineBuilderInputRef is the only ref accepted in a builder input bundle.
	sourceBaselineBuilderInputRef   = "refs/source/input"
	sourceBaselineBuilderTempPrefix = ".source-baseline-builder-"
)

// SourceBaselineBuilderStage is the stable, non-sensitive worker failure stage.
// Its token is safe to project to progress and JSONL error_code fields.
type SourceBaselineBuilderStage string

const (
	SourceBaselineBuilderStageDownloadInput    SourceBaselineBuilderStage = "download_input"
	SourceBaselineBuilderStageImportInput      SourceBaselineBuilderStage = "import_input"
	SourceBaselineBuilderStageBuildRepository  SourceBaselineBuilderStage = "build_repository"
	SourceBaselineBuilderStageBuildBundle      SourceBaselineBuilderStage = "build_bundle"
	SourceBaselineBuilderStageObserveIdentity  SourceBaselineBuilderStage = "observe_identity"
	SourceBaselineBuilderStageReceipt          SourceBaselineBuilderStage = "receipt"
	SourceBaselineBuilderStageValidateArtifact SourceBaselineBuilderStage = "validate_artifact"
	SourceBaselineBuilderStageUploadArtifact   SourceBaselineBuilderStage = "upload_artifact"
	SourceBaselineBuilderStageUploadReceipt    SourceBaselineBuilderStage = "upload_receipt"
	SourceBaselineBuilderStageResult           SourceBaselineBuilderStage = "result"
)

// SourceBaselineBuilderStageError reports only a stable stage token. It does
// not retain or unwrap the underlying error, so paths, credentials and raw
// provider diagnostics cannot cross the worker boundary.
type SourceBaselineBuilderStageError struct {
	Stage SourceBaselineBuilderStage
}

// Error 返回只含稳定阶段且不携带底层原因的有界诊断。
func (err SourceBaselineBuilderStageError) Error() string {
	return "source baseline builder failed stage=" + string(err.Stage)
}

func sourceBaselineBuilderStageValid(stage SourceBaselineBuilderStage) bool {
	switch stage {
	case SourceBaselineBuilderStageDownloadInput, SourceBaselineBuilderStageImportInput,
		SourceBaselineBuilderStageBuildRepository, SourceBaselineBuilderStageBuildBundle,
		SourceBaselineBuilderStageObserveIdentity, SourceBaselineBuilderStageReceipt,
		SourceBaselineBuilderStageValidateArtifact, SourceBaselineBuilderStageUploadArtifact,
		SourceBaselineBuilderStageUploadReceipt, SourceBaselineBuilderStageResult:
		return true
	default:
		return false
	}
}

func sourceBaselineBuilderStageFailure(stage SourceBaselineBuilderStage, _ error) error {
	if !sourceBaselineBuilderStageValid(stage) {
		stage = SourceBaselineBuilderStageResult
	}
	return SourceBaselineBuilderStageError{Stage: stage}
}

// SourceBaselineBuilderFailureStage 返回 worker 错误的稳定阶段；未知错误统一投影为 result。
func SourceBaselineBuilderFailureStage(err error) SourceBaselineBuilderStage {
	var stageErr SourceBaselineBuilderStageError
	if errors.As(err, &stageErr) && sourceBaselineBuilderStageValid(stageErr.Stage) {
		return stageErr.Stage
	}
	var stageErrPtr *SourceBaselineBuilderStageError
	if errors.As(err, &stageErrPtr) && stageErrPtr != nil && sourceBaselineBuilderStageValid(stageErrPtr.Stage) {
		return stageErrPtr.Stage
	}
	return SourceBaselineBuilderStageResult
}

// SourceBaselineBuilderFailureStageFromLog 只从有界日志提取白名单阶段，不向调用方返回原始日志。
func SourceBaselineBuilderFailureStageFromLog(logs string) (SourceBaselineBuilderStage, bool) {
	const prefix = "progress: artifact build failed stage="
	for line := range strings.SplitSeq(logs, "\n") {
		line = strings.TrimSpace(line)
		token, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		stage := SourceBaselineBuilderStage(token)
		if sourceBaselineBuilderStageValid(stage) {
			return stage, true
		}
	}
	return SourceBaselineBuilderStageResult, false
}

// SourceBaselineArtifactMetadata is the content identity passed to upload adapters.
type SourceBaselineArtifactMetadata struct {
	SHA256                 string
	SizeBytes              int64
	ArtifactIdentityDigest string
}

// ArtifactIO is the narrow, injected storage boundary used by the builder worker.
// The fields are exact function values rather than a cloud client. Upload adapters
// own create-only/read-back semantics and receive the worker-computed identity.
type ArtifactIO struct {
	DownloadInput  func(context.Context, string, io.Writer) (int64, error)
	UploadArtifact func(context.Context, string, string, SourceBaselineArtifactMetadata) error
	UploadReceipt  func(context.Context, string, string, SourceBaselineArtifactMetadata) error
}

// SourceBaselineBuilderWorkerResult 是 worker 产出的不含 live group 的 bounded 结果。
type SourceBaselineBuilderWorkerResult struct {
	ArtifactKey string
	ReceiptKey  string
	Receipt     SourceBaselineProducerReceipt
	InputDigest string
}

// ValidateAgainst 校验 worker 结果与本次显式输入的绑定关系。
func (result SourceBaselineBuilderWorkerResult) ValidateAgainst(input SourceBaselineBuildInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	if err := result.Receipt.Validate(); err != nil {
		return err
	}
	if err := result.validateInputDigest(input); err != nil {
		return err
	}
	if err := result.validateObjectKeys(input); err != nil {
		return err
	}
	return result.validateReceiptBinding(input)
}

// validateInputDigest 校验输入摘要绑定。
func (result SourceBaselineBuilderWorkerResult) validateInputDigest(input SourceBaselineBuildInput) error {
	expectedDigest, err := SourceBaselineBuilderInputDigest(input)
	if err != nil {
		return err
	}
	if result.InputDigest != expectedDigest {
		return errors.New("source baseline builder worker input digest does not match input")
	}
	return nil
}

// validateObjectKeys 校验 artifact/receipt 键绑定到内容身份。
func (result SourceBaselineBuilderWorkerResult) validateObjectKeys(input SourceBaselineBuildInput) error {
	artifactKey, receiptKey, err := CanonicalSourceBaselineBuilderKeys(input.ArtifactPrefix, input.RepositoryID, input.ObjectFormat, input.TreeSHA, result.Receipt.ArtifactIdentityDigest)
	if err != nil {
		return err
	}
	if result.ArtifactKey != artifactKey || result.ReceiptKey != receiptKey {
		return errors.New("source baseline builder worker object keys do not match receipt artifact identity")
	}
	return nil
}

// validateReceiptBinding 校验 typed receipt 与显式输入字段一致。
func (result SourceBaselineBuilderWorkerResult) validateReceiptBinding(input SourceBaselineBuildInput) error {
	checks := []func() bool{
		func() bool {
			return result.Receipt.ExpectedBuilderDigest == input.ExpectedBuilderDigest && result.Receipt.ExpectedToolchainDigest == input.ExpectedToolchainDigest
		},
		func() bool {
			return result.Receipt.ObservedBuilderDigest == input.ExpectedBuilderDigest && result.Receipt.ObservedToolchainDigest == input.ExpectedToolchainDigest
		},
		func() bool {
			return result.Receipt.ProtocolABIDigest == input.ProtocolABIDigest && result.Receipt.WorkloadManifestABIDigest == input.WorkloadManifestABIDigest
		},
		func() bool {
			return result.Receipt.RepositoryID == input.RepositoryID && result.Receipt.ObjectFormat == input.ObjectFormat
		},
		func() bool {
			return result.Receipt.SourceCommitSHA == input.CommitSHA && result.Receipt.SourceTreeSHA == input.TreeSHA
		},
		func() bool { return result.Receipt.BaselineTreeSHA == input.TreeSHA },
	}
	for _, check := range checks {
		if !check() {
			return errors.New("source baseline builder worker receipt does not match input")
		}
	}
	return nil
}

// SourceBaselineBuilderWorker builds and publishes one immutable source baseline.
type SourceBaselineBuilderWorker struct {
	IO       ArtifactIO
	Observer ArtifactBuilderIdentityObserver
}

// NewSourceBaselineBuilderWorker 创建并校验注入的 artifact IO 边界。
func NewSourceBaselineBuilderWorker(artifactIO ArtifactIO, observer ArtifactBuilderIdentityObserver) (*SourceBaselineBuilderWorker, error) {
	worker := &SourceBaselineBuilderWorker{IO: artifactIO, Observer: observer}
	if err := worker.validateIO(); err != nil {
		return nil, err
	}
	return worker, nil
}

// Build 下载、验证、构建、复核并发布一个 source baseline。
func (worker *SourceBaselineBuilderWorker) Build(ctx context.Context, input SourceBaselineBuildInput) (result SourceBaselineBuilderWorkerResult, err error) {
	if worker == nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, errors.New("source baseline builder worker is nil"))
	}
	if err := worker.validateIO(); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, err)
	}
	if ctx == nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, errors.New("source baseline builder context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, err)
	}
	if err := input.Validate(); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, err)
	}

	temporaryRoot, err := createSourceBaselineBuilderTemp()
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	defer func() {
		if cleanupErr := removeSourceBaselineBuilderTemp(temporaryRoot); cleanupErr != nil && err == nil {
			result = SourceBaselineBuilderWorkerResult{}
			err = sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, cleanupErr)
		}
	}()

	return worker.buildWithinTemp(ctx, input, temporaryRoot)
}

// buildWithinTemp 在一次私有临时根中完成制品验证与发布。
func (worker *SourceBaselineBuilderWorker) buildWithinTemp(ctx context.Context, input SourceBaselineBuildInput, temporaryRoot string) (SourceBaselineBuilderWorkerResult, error) {
	artifactPath, baselineCommit, artifactDigest, artifactSize, err := worker.buildSourceBaselineBuilderArtifact(ctx, input, temporaryRoot)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	observed, err := worker.observeIdentity(ctx, input)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageObserveIdentity, err)
	}
	artifactIdentityDigest, err := ArtifactIdentityDigest(input.RepositoryID, input.ObjectFormat, input.CommitSHA, input.TreeSHA, artifactDigest, artifactSize, observed, input.ProtocolABIDigest, input.WorkloadManifestABIDigest)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, err)
	}
	artifactKey, receiptKey, err := CanonicalSourceBaselineBuilderKeys(input.ArtifactPrefix, input.RepositoryID, input.ObjectFormat, input.TreeSHA, artifactIdentityDigest)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, err)
	}
	receipt, receiptBytes, err := buildSourceBaselineBuilderReceipt(input, observed, baselineCommit, artifactDigest, artifactSize, artifactIdentityDigest)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageReceipt, err)
	}
	receiptPath := filepath.Join(temporaryRoot, "source-baseline-receipt.json")
	if err := writeSourceBaselineBuilderReceipt(receiptPath, receiptBytes); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageReceipt, err)
	}
	if err := validateBuiltSourceBaselineArtifact(ctx, artifactPath, input, receipt, artifactKey, artifactDigest, artifactSize); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageValidateArtifact, err)
	}
	return worker.publishSourceBaselineBuilder(ctx, artifactPath, artifactKey, receiptPath, receiptKey, artifactDigest, artifactSize, receipt, receiptBytes, input)
}

// observeIdentity 在任何 artifact/receipt 上传前读取并核对实际 builder 身份。
func (worker *SourceBaselineBuilderWorker) observeIdentity(ctx context.Context, input SourceBaselineBuildInput) (ObservedArtifactBuilderIdentity, error) {
	observed, err := worker.Observer(ctx)
	if err != nil {
		return ObservedArtifactBuilderIdentity{}, fmt.Errorf("observe artifact builder identity: %w", err)
	}
	if err := validateObservedArtifactBuilderIdentity(observed, input); err != nil {
		return ObservedArtifactBuilderIdentity{}, err
	}
	return observed, nil
}

// buildSourceBaselineBuilderReceipt 生成包含 expected/observed 与 artifact identity 的 receipt。
func buildSourceBaselineBuilderReceipt(input SourceBaselineBuildInput, observed ObservedArtifactBuilderIdentity, baselineCommit, artifactDigest string, artifactSize int64, artifactIdentityDigest string) (SourceBaselineProducerReceipt, []byte, error) {
	receipt := SourceBaselineProducerReceipt{SchemaVersion: SourceBaselineProducerReceiptSchema, ExecutionProvider: PortableExecutionProvider,
		ExpectedBuilderDigest: input.ExpectedBuilderDigest, ExpectedToolchainDigest: input.ExpectedToolchainDigest, ObservedBuilderDigest: observed.BuilderDigest, ObservedToolchainDigest: observed.ToolchainDigest,
		ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest, RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat,
		SourceCommitSHA: input.CommitSHA, SourceTreeSHA: input.TreeSHA, BaselineCommitSHA: baselineCommit, BaselineTreeSHA: input.TreeSHA,
		ArtifactSHA256: artifactDigest, ArtifactSizeBytes: artifactSize, ArtifactIdentityDigest: artifactIdentityDigest}
	data, err := receipt.CanonicalEncode()
	return receipt, data, err
}

// validateBuiltSourceBaselineArtifact 用 receipt 与 exact target 复核生成的 bundle。
func validateBuiltSourceBaselineArtifact(ctx context.Context, artifactPath string, input SourceBaselineBuildInput, receipt SourceBaselineProducerReceipt, artifactKey, artifactDigest string, artifactSize int64) error {
	return ValidatePortableSourceBaselineArtifact(ctx, sourceBaselineBuilderResolver{path: artifactPath, receipt: receipt}, PortableRunTarget{RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat, SourceBaseline: PortableSourceBaseline{ObjectFormat: input.ObjectFormat, BaselineTreeSHA: input.TreeSHA, BaselineCommitSHA: receipt.BaselineCommitSHA, BundleContractID: PortableBundleContractID, ArtifactKind: PortableSourceBaselineArtifactKind, ArtifactKey: artifactKey, ArtifactSHA256: artifactDigest, ArtifactSizeBytes: artifactSize}})
}

func (worker *SourceBaselineBuilderWorker) buildSourceBaselineBuilderArtifact(ctx context.Context, input SourceBaselineBuildInput, temporaryRoot string) (string, string, string, int64, error) {
	inputPath := filepath.Join(temporaryRoot, "builder-input.bundle")
	if err := worker.downloadAndVerifyInput(ctx, input, inputPath); err != nil {
		return "", "", "", 0, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageDownloadInput, err)
	}
	inputRepository := filepath.Join(temporaryRoot, "builder-input.git")
	if err := importSourceBaselineBuilderInput(ctx, inputPath, inputRepository, temporaryRoot, input); err != nil {
		return "", "", "", 0, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageImportInput, err)
	}
	baselineRepository := filepath.Join(temporaryRoot, "baseline.git")
	baselineCommit, err := buildSourceBaselineBuilderRepository(ctx, inputRepository, baselineRepository, temporaryRoot, input)
	if err != nil {
		return "", "", "", 0, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageBuildRepository, err)
	}
	artifactPath := filepath.Join(temporaryRoot, "source-baseline.bundle")
	artifactDigest, artifactSize, err := createSourceBaselineBuilderBundle(ctx, baselineRepository, artifactPath, input, baselineCommit)
	if err != nil {
		return "", "", "", 0, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageBuildBundle, err)
	}
	return artifactPath, baselineCommit, artifactDigest, artifactSize, nil
}

func (worker *SourceBaselineBuilderWorker) publishSourceBaselineBuilder(ctx context.Context, artifactPath, artifactKey, receiptPath, receiptKey, artifactDigest string, artifactSize int64, receipt SourceBaselineProducerReceipt, receiptBytes []byte, input SourceBaselineBuildInput) (SourceBaselineBuilderWorkerResult, error) {
	artifactMetadata := SourceBaselineArtifactMetadata{SHA256: artifactDigest, SizeBytes: artifactSize, ArtifactIdentityDigest: receipt.ArtifactIdentityDigest}
	if err := worker.uploadArtifact(ctx, artifactPath, artifactKey, artifactMetadata); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageUploadArtifact, err)
	}
	receiptMetadata := SourceBaselineArtifactMetadata{SHA256: bytesDigest(receiptBytes), SizeBytes: int64(len(receiptBytes)), ArtifactIdentityDigest: receipt.ArtifactIdentityDigest}
	if err := worker.uploadReceipt(ctx, receiptPath, receiptKey, receiptMetadata); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageUploadReceipt, err)
	}

	inputDigest, err := SourceBaselineBuilderInputDigest(input)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, err)
	}
	result := SourceBaselineBuilderWorkerResult{
		ArtifactKey: artifactKey,
		ReceiptKey:  receiptKey,
		Receipt:     receipt,
		InputDigest: inputDigest,
	}
	if err := result.ValidateAgainst(input); err != nil {
		return SourceBaselineBuilderWorkerResult{}, sourceBaselineBuilderStageFailure(SourceBaselineBuilderStageResult, err)
	}
	return result, nil
}

// BuildSourceBaselineArtifact 使用一次性注入端口构建 source baseline。
func BuildSourceBaselineArtifact(ctx context.Context, artifactIO ArtifactIO, input SourceBaselineBuildInput, observer ArtifactBuilderIdentityObserver) (SourceBaselineBuilderWorkerResult, error) {
	worker, err := NewSourceBaselineBuilderWorker(artifactIO, observer)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	return worker.Build(ctx, input)
}

func validateObservedArtifactBuilderIdentity(observed ObservedArtifactBuilderIdentity, input SourceBaselineBuildInput) error {
	if err := validateBuilderDigests(observed.BuilderDigest, observed.ToolchainDigest, "observed artifact builder"); err != nil {
		return err
	}
	if observed.BuilderDigest != input.ExpectedBuilderDigest || observed.ToolchainDigest != input.ExpectedToolchainDigest {
		return errors.New("observed artifact builder identity does not match expected identity")
	}
	return nil
}

// validateIO 在 Git 操作前拒绝不完整或不支持的注入端口。
func (worker *SourceBaselineBuilderWorker) validateIO() error {
	if worker == nil {
		return errors.New("source baseline builder worker is nil")
	}
	if worker.IO.DownloadInput == nil || worker.IO.UploadArtifact == nil || worker.IO.UploadReceipt == nil {
		return errors.New("source baseline builder artifact IO is incomplete")
	}
	if worker.Observer == nil {
		return errors.New("source baseline builder observed identity observer is required")
	}
	return nil
}

// downloadAndVerifyInput 流式下载输入并复核声明的摘要与大小。
func (worker *SourceBaselineBuilderWorker) downloadAndVerifyInput(ctx context.Context, input SourceBaselineBuildInput, path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create builder input staging file: %w", err)
	}
	staging := &sourceBaselineBuilderWriter{file: file, hash: sha256.New(), limit: SourceBaselineBuilderArtifactMaxBytes}
	declaredSize, downloadErr := worker.IO.DownloadInput(ctx, input.InputObjectKey, staging)
	closeErr := file.Close()
	if downloadErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("download builder input: %w", downloadErr), closeErr)
	}
	if declaredSize < 0 || declaredSize != staging.size {
		return errors.New("builder input download size does not match streamed size")
	}
	if staging.size != input.InputSizeBytes || "sha256:"+hex.EncodeToString(staging.hash.Sum(nil)) != input.InputSHA256 {
		return errors.New("builder input bytes do not match declared SHA-256 and size")
	}
	return nil
}

func (worker *SourceBaselineBuilderWorker) uploadArtifact(ctx context.Context, localPath, key string, metadata SourceBaselineArtifactMetadata) error {
	return worker.IO.UploadArtifact(ctx, localPath, key, metadata)
}

func (worker *SourceBaselineBuilderWorker) uploadReceipt(ctx context.Context, localPath, key string, metadata SourceBaselineArtifactMetadata) error {
	return worker.IO.UploadReceipt(ctx, localPath, key, metadata)
}

func createSourceBaselineBuilderTemp() (string, error) {
	root, err := os.MkdirTemp("", sourceBaselineBuilderTempPrefix)
	if err != nil {
		return "", fmt.Errorf("create private builder temporary root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return "", fmt.Errorf("protect private builder temporary root: %w", err)
	}
	return root, nil
}

func removeSourceBaselineBuilderTemp(root string) error {
	if root == "" {
		return nil
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("cleanup private builder temporary root: %w", err)
	}
	return nil
}

// importSourceBaselineBuilderInput 导入并严格验证 self-contained builder input bundle。
func importSourceBaselineBuilderInput(ctx context.Context, bundlePath, repository, commandRoot string, input SourceBaselineBuildInput) error {
	if err := initBareRepository(ctx, commandRoot, repository, gateObjectFormat(input)); err != nil {
		return fmt.Errorf("initialize builder input repository: %w", err)
	}
	if err := readSourceBaselineBuilderHeader(bundlePath, gateObjectFormat(input), sourceBaselineBuilderInputRef, input.CommitSHA); err != nil {
		return err
	}
	if _, err := runGitOutput(ctx, repository, nil, "bundle", "verify", bundlePath); err != nil {
		return fmt.Errorf("verify builder input bundle: %w", err)
	}
	if err := runGit(ctx, repository, nil, io.Discard, "bundle", "unbundle", bundlePath); err != nil {
		return fmt.Errorf("import builder input bundle: %w", err)
	}
	refInput := fmt.Sprintf("create %s %s\n", sourceBaselineBuilderInputRef, input.CommitSHA)
	if err := runGit(ctx, repository, strings.NewReader(refInput), io.Discard, "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("install builder input ref: %w", err)
	}
	return validateSourceBaselineBuilderInputRepository(ctx, repository, input)
}

// validateSourceBaselineBuilderInputRepository 复核 input ref、commit/tree 和完整闭包。
func validateSourceBaselineBuilderInputRepository(ctx context.Context, repository string, input SourceBaselineBuildInput) error {
	refs, err := runGitOutput(ctx, repository, nil, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil || string(refs) != input.CommitSHA+" "+sourceBaselineBuilderInputRef+"\n" {
		return errors.New("builder input bundle refs are not unique")
	}
	object, err := readSourceObject(ctx, repository, input.CommitSHA)
	if err != nil {
		return fmt.Errorf("read builder input source commit: %w", err)
	}
	tree, parents, err := parseCommitObject(object, input.TreeSHA)
	if err != nil || tree != input.TreeSHA {
		return errors.New("builder input source commit does not resolve to the declared tree")
	}
	if len(parents) > 0 {
		// A source commit may carry its real history; self-contained means that
		// every parent object is in this bundle, which fsck below proves.
		for _, parent := range parents {
			if !validOID(parent, gateObjectFormat(input)) {
				return errors.New("builder input source parent identity is invalid")
			}
		}
	}
	return verifySourceBaselineBuilderClosure(ctx, repository, sourceBaselineBuilderInputRef)
}

// buildSourceBaselineBuilderRepository 物化 tree/blob 闭包并写入 deterministic baseline commit。
func buildSourceBaselineBuilderRepository(ctx context.Context, inputRepository, repository, commandRoot string, input SourceBaselineBuildInput) (string, error) {
	format := gateObjectFormat(input)
	if err := initBareRepository(ctx, commandRoot, repository, format); err != nil {
		return "", fmt.Errorf("initialize source baseline repository: %w", err)
	}
	if err := transferTreeClosure(ctx, inputRepository, repository, input.TreeSHA); err != nil {
		return "", fmt.Errorf("transfer source baseline tree closure: %w", err)
	}
	commit, err := DeterministicSourceBaselineCommitSHA(input.TreeSHA, format)
	if err != nil {
		return "", err
	}
	output, err := runGitOutput(ctx, repository, bytes.NewReader(deterministicSourceBaselinePayload(input.TreeSHA)), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("write deterministic source baseline commit: %w", err)
	}
	actual, err := strictGitLine(output)
	if err != nil || actual != commit {
		return "", errors.New("deterministic source baseline commit identity drifted")
	}
	refInput := fmt.Sprintf("create %s %s\n", PortableSourceBaselineArtifactRef, commit)
	if err := runGit(ctx, repository, strings.NewReader(refInput), io.Discard, "update-ref", "--stdin"); err != nil {
		return "", fmt.Errorf("install source baseline ref: %w", err)
	}
	if err := verifySourceBaselineBuilderClosure(ctx, repository, PortableSourceBaselineArtifactRef); err != nil {
		return "", err
	}
	return commit, nil
}

// createSourceBaselineBuilderBundle 生成有界的 self-contained source baseline bundle。
func createSourceBaselineBuilderBundle(ctx context.Context, repository, bundlePath string, input SourceBaselineBuildInput, commit string) (string, int64, error) {
	if err := runGit(ctx, repository, nil, io.Discard, "bundle", "create", bundlePath, "--end-of-options", PortableSourceBaselineArtifactRef); err != nil {
		return "", 0, fmt.Errorf("create source baseline bundle: %w", err)
	}
	if err := os.Chmod(bundlePath, 0o600); err != nil {
		return "", 0, fmt.Errorf("protect source baseline bundle: %w", err)
	}
	digest, size, err := digestSourceBaselineBuilderFile(bundlePath)
	if err != nil {
		return "", 0, err
	}
	wantCommit, err := DeterministicSourceBaselineCommitSHA(input.TreeSHA, gateObjectFormat(input))
	if err != nil || commit != wantCommit {
		return "", 0, errors.New("source baseline bundle commit is not deterministic")
	}
	return digest, size, nil
}

// digestSourceBaselineBuilderFile 流式计算本地制品 SHA-256 和大小。
func digestSourceBaselineBuilderFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open staged source baseline bundle: %w", err)
	}
	hash := sha256.New()
	count, readErr := io.Copy(hash, io.LimitReader(file, SourceBaselineBuilderArtifactMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return "", count, errors.Join(readErr, closeErr)
	}
	if count <= 0 || count > SourceBaselineBuilderArtifactMaxBytes {
		return "", count, errors.New("source baseline bundle exceeds bounded size")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), count, nil
}

// readSourceBaselineBuilderHeader 解析唯一 ref 并拒绝 prerequisites 或额外 ref。
func readSourceBaselineBuilderHeader(path string, format gate.GitObjectFormat, expectedRef, expectedOID string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open builder input bundle: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(io.LimitReader(file, SourceBaselineBuilderProtocolMaxBytes+1))
	first, err := reader.ReadString('\n')
	if err != nil || first != "# v2 git bundle\n" {
		return errors.New("builder input bundle header is invalid")
	}
	bytesRead := len(first)
	advertised := ""
	for {
		line, readErr := reader.ReadString('\n')
		bytesRead += len(line)
		next, done, lineErr := parseSourceBaselineBuilderHeaderLine(line, readErr, bytesRead, format, expectedRef, advertised)
		if lineErr != nil {
			return lineErr
		}
		if done {
			break
		}
		advertised = next
	}
	if advertised != expectedOID {
		return errors.New("builder input bundle ref does not match the declared source commit")
	}
	return nil
}

// parseSourceBaselineBuilderHeaderLine 解析一个 header 行并标记 header 结束。
func parseSourceBaselineBuilderHeaderLine(line string, readErr error, bytesRead int, format gate.GitObjectFormat, expectedRef, advertised string) (string, bool, error) {
	if bytesRead > SourceBaselineBuilderProtocolMaxBytes {
		return "", false, errors.New("builder input bundle header exceeds bounded size")
	}
	if readErr != nil {
		return "", false, errors.New("builder input bundle header is unterminated")
	}
	if line == "\n" {
		return advertised, true, nil
	}
	if strings.HasPrefix(line, "-") {
		return "", false, errors.New("builder input bundle has prerequisites")
	}
	fields := strings.Fields(strings.TrimSuffix(line, "\n"))
	if len(fields) != 2 || fields[1] != expectedRef || advertised != "" || !validOID(fields[0], format) {
		return "", false, errors.New("builder input bundle must advertise one exact input ref")
	}
	return fields[0], false, nil
}

func verifySourceBaselineBuilderClosure(ctx context.Context, repository, root string) error {
	output, err := runGitOutput(ctx, repository, nil, "fsck", "--full", "--strict", "--no-reflogs", "--unreachable", "--no-progress", root)
	if err != nil {
		return fmt.Errorf("verify builder Git closure: %w", err)
	}
	if len(bytes.TrimSpace(output)) != 0 {
		return errors.New("builder Git bundle contains unreachable extra objects")
	}
	return nil
}

// writeSourceBaselineBuilderReceipt 以私有权限写入 canonical receipt。
func writeSourceBaselineBuilderReceipt(path string, data []byte) error {
	if len(data) == 0 || len(data) > SourceBaselineBuilderProtocolMaxBytes {
		return errors.New("source baseline receipt is outside the bounded protocol size")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source baseline receipt: %w", err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	return nil
}

func gateObjectFormat(input SourceBaselineBuildInput) gate.GitObjectFormat {
	return gate.GitObjectFormat(input.ObjectFormat)
}

type sourceBaselineBuilderWriter struct {
	file  *os.File
	hash  hashWriter
	size  int64
	limit int64
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

// Write 将输入字节写入私有 staging 文件并同步计算摘要。
func (writer *sourceBaselineBuilderWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.limit-writer.size {
		return 0, errors.New("builder input exceeds bounded size")
	}
	written, err := writer.file.Write(data)
	if written > 0 {
		_, _ = writer.hash.Write(data[:written])
		writer.size += int64(written)
	}
	if err == nil && written != len(data) {
		return written, io.ErrShortWrite
	}
	return written, err
}

type sourceBaselineBuilderResolver struct {
	path    string
	receipt SourceBaselineProducerReceipt
}

// Resolve 为 portable validator 提供一次有界的本地 artifact 流。
func (resolver sourceBaselineBuilderResolver) Resolve(_ context.Context, kind, key string) (io.ReadCloser, int64, SourceBaselineProducerReceipt, error) {
	if kind != PortableSourceBaselineArtifactKind {
		return nil, 0, SourceBaselineProducerReceipt{}, errors.New("source baseline resolver kind is invalid")
	}
	file, err := os.Open(resolver.path)
	if err != nil {
		return nil, 0, SourceBaselineProducerReceipt{}, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, SourceBaselineProducerReceipt{}, err
	}
	if info.Size() != resolver.receipt.ArtifactSizeBytes || key == "" {
		_ = file.Close()
		return nil, 0, SourceBaselineProducerReceipt{}, errors.New("source baseline resolver artifact identity is invalid")
	}
	return file, info.Size(), resolver.receipt, nil
}
