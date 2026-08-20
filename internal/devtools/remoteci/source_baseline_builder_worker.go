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

// SourceBaselineArtifactMetadata is the content identity passed to upload adapters.
type SourceBaselineArtifactMetadata struct {
	SHA256    string
	SizeBytes int64
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
	artifactKey, receiptKey, err := CanonicalSourceBaselineBuilderKeys(input.RepositoryID, input.ObjectFormat, input.TreeSHA, result.Receipt.ArtifactSHA256)
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
	if result.Receipt.BuilderDigest != input.BuilderDigest || result.Receipt.ToolchainDigest != input.ToolchainDigest ||
		result.Receipt.RepositoryID != input.RepositoryID || result.Receipt.ObjectFormat != input.ObjectFormat ||
		result.Receipt.BaselineTreeSHA != input.TreeSHA {
		return errors.New("source baseline builder worker receipt does not match input")
	}
	return nil
}

// SourceBaselineBuilderWorker builds and publishes one immutable source baseline.
type SourceBaselineBuilderWorker struct {
	IO ArtifactIO
}

// NewSourceBaselineBuilderWorker 创建并校验注入的 artifact IO 边界。
func NewSourceBaselineBuilderWorker(artifactIO ArtifactIO) (*SourceBaselineBuilderWorker, error) {
	worker := &SourceBaselineBuilderWorker{IO: artifactIO}
	if err := worker.validateIO(); err != nil {
		return nil, err
	}
	return worker, nil
}

// Build 下载、验证、构建、复核并发布一个 source baseline。
func (worker *SourceBaselineBuilderWorker) Build(ctx context.Context, input SourceBaselineBuildInput) (result SourceBaselineBuilderWorkerResult, err error) {
	if worker == nil {
		return SourceBaselineBuilderWorkerResult{}, errors.New("source baseline builder worker is nil")
	}
	if err := worker.validateIO(); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	if ctx == nil {
		return SourceBaselineBuilderWorkerResult{}, errors.New("source baseline builder context is nil")
	}
	if err := ctx.Err(); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	if err := input.Validate(); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}

	temporaryRoot, err := createSourceBaselineBuilderTemp()
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	defer func() {
		err = errors.Join(err, removeSourceBaselineBuilderTemp(temporaryRoot))
	}()

	return worker.buildWithinTemp(ctx, input, temporaryRoot)
}

// buildWithinTemp 在一次私有临时根中完成制品验证与发布。
func (worker *SourceBaselineBuilderWorker) buildWithinTemp(ctx context.Context, input SourceBaselineBuildInput, temporaryRoot string) (SourceBaselineBuilderWorkerResult, error) {
	artifactPath, baselineCommit, artifactDigest, artifactSize, err := worker.buildSourceBaselineBuilderArtifact(ctx, input, temporaryRoot)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}

	artifactKey, receiptKey, err := CanonicalSourceBaselineBuilderKeys(input.RepositoryID, input.ObjectFormat, input.TreeSHA, artifactDigest)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	receipt := SourceBaselineProducerReceipt{
		SchemaVersion:     SourceBaselineProducerReceiptSchema,
		ExecutionProvider: PortableExecutionProvider,
		BuilderDigest:     input.BuilderDigest,
		ToolchainDigest:   input.ToolchainDigest,
		RepositoryID:      input.RepositoryID,
		ObjectFormat:      input.ObjectFormat,
		BaselineCommitSHA: baselineCommit,
		BaselineTreeSHA:   input.TreeSHA,
		ArtifactSHA256:    artifactDigest,
		ArtifactSizeBytes: artifactSize,
	}
	receiptBytes, err := receipt.CanonicalEncode()
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	receiptPath := filepath.Join(temporaryRoot, "source-baseline-receipt.json")
	if err := writeSourceBaselineBuilderReceipt(receiptPath, receiptBytes); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	if err := ValidatePortableSourceBaselineArtifact(ctx, sourceBaselineBuilderResolver{
		path: artifactPath,
		receipt: SourceBaselineProducerReceipt{
			SchemaVersion:     SourceBaselineProducerReceiptSchema,
			ExecutionProvider: PortableExecutionProvider,
			BuilderDigest:     input.BuilderDigest,
			ToolchainDigest:   input.ToolchainDigest,
			RepositoryID:      input.RepositoryID,
			ObjectFormat:      input.ObjectFormat,
			BaselineCommitSHA: baselineCommit,
			BaselineTreeSHA:   input.TreeSHA,
			ArtifactSHA256:    artifactDigest,
			ArtifactSizeBytes: artifactSize,
		},
	}, PortableRunTarget{
		RepositoryID: input.RepositoryID,
		ObjectFormat: input.ObjectFormat,
		SourceBaseline: PortableSourceBaseline{
			ObjectFormat:      input.ObjectFormat,
			BaselineTreeSHA:   input.TreeSHA,
			BaselineCommitSHA: baselineCommit,
			BundleContractID:  PortableBundleContractID,
			ArtifactKind:      PortableSourceBaselineArtifactKind,
			ArtifactKey:       artifactKey,
			ArtifactSHA256:    artifactDigest,
			ArtifactSizeBytes: artifactSize,
		},
	}); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}

	return worker.publishSourceBaselineBuilder(ctx, artifactPath, artifactKey, receiptPath, receiptKey, artifactDigest, artifactSize, receipt, receiptBytes, input)
}

func (worker *SourceBaselineBuilderWorker) buildSourceBaselineBuilderArtifact(ctx context.Context, input SourceBaselineBuildInput, temporaryRoot string) (string, string, string, int64, error) {
	inputPath := filepath.Join(temporaryRoot, "builder-input.bundle")
	if err := worker.downloadAndVerifyInput(ctx, input, inputPath); err != nil {
		return "", "", "", 0, err
	}
	inputRepository := filepath.Join(temporaryRoot, "builder-input.git")
	if err := importSourceBaselineBuilderInput(ctx, inputPath, inputRepository, temporaryRoot, input); err != nil {
		return "", "", "", 0, err
	}
	baselineRepository := filepath.Join(temporaryRoot, "baseline.git")
	baselineCommit, err := buildSourceBaselineBuilderRepository(ctx, inputRepository, baselineRepository, temporaryRoot, input)
	if err != nil {
		return "", "", "", 0, err
	}
	artifactPath := filepath.Join(temporaryRoot, "source-baseline.bundle")
	artifactDigest, artifactSize, err := createSourceBaselineBuilderBundle(ctx, baselineRepository, artifactPath, input, baselineCommit)
	if err != nil {
		return "", "", "", 0, err
	}
	return artifactPath, baselineCommit, artifactDigest, artifactSize, nil
}

func (worker *SourceBaselineBuilderWorker) publishSourceBaselineBuilder(ctx context.Context, artifactPath, artifactKey, receiptPath, receiptKey, artifactDigest string, artifactSize int64, receipt SourceBaselineProducerReceipt, receiptBytes []byte, input SourceBaselineBuildInput) (SourceBaselineBuilderWorkerResult, error) {
	artifactMetadata := SourceBaselineArtifactMetadata{SHA256: artifactDigest, SizeBytes: artifactSize}
	if err := worker.uploadArtifact(ctx, artifactPath, artifactKey, artifactMetadata); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	receiptMetadata := SourceBaselineArtifactMetadata{SHA256: bytesDigest(receiptBytes), SizeBytes: int64(len(receiptBytes))}
	if err := worker.uploadReceipt(ctx, receiptPath, receiptKey, receiptMetadata); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}

	inputDigest, err := SourceBaselineBuilderInputDigest(input)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	result := SourceBaselineBuilderWorkerResult{
		ArtifactKey: artifactKey,
		ReceiptKey:  receiptKey,
		Receipt:     receipt,
		InputDigest: inputDigest,
	}
	if err := result.ValidateAgainst(input); err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	return result, nil
}

// BuildSourceBaselineArtifact 使用一次性注入端口构建 source baseline。
func BuildSourceBaselineArtifact(ctx context.Context, artifactIO ArtifactIO, input SourceBaselineBuildInput) (SourceBaselineBuilderWorkerResult, error) {
	worker, err := NewSourceBaselineBuilderWorker(artifactIO)
	if err != nil {
		return SourceBaselineBuilderWorkerResult{}, err
	}
	return worker.Build(ctx, input)
}

// validateIO 在 Git 操作前拒绝不完整或不支持的注入端口。
func (worker *SourceBaselineBuilderWorker) validateIO() error {
	if worker == nil {
		return errors.New("source baseline builder worker is nil")
	}
	if worker.IO.DownloadInput == nil || worker.IO.UploadArtifact == nil || worker.IO.UploadReceipt == nil {
		return errors.New("source baseline builder artifact IO is incomplete")
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
