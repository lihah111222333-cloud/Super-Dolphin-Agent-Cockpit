package remoteci

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	// PortableSourceBaselineArtifactKind identifies the immutable baseline bundle.
	PortableSourceBaselineArtifactKind = "source-baseline-git-bundle/v1"
	// PortableSourceBaselineArtifactRef is the only ref a baseline bundle may advertise.
	PortableSourceBaselineArtifactRef = "refs/source/baseline"
	portableSourceBaselineMaxBytes    = 1 << 30
	portableSourceBaselineMaxHeader   = 1 << 20
)

// SourceBaselineArtifactResolver 解析不透明 kind/key，返回单次流、大小和 typed receipt。
type SourceBaselineArtifactResolver interface {
	Resolve(ctx context.Context, kind, key string) (io.ReadCloser, int64, SourceBaselineProducerReceipt, error)
}

// ValidatePortableSourceBaselineArtifact 严格校验单个内容寻址 baseline bundle，
// 只使用调用方注入的解析器，不读取任何外部权威状态。
func ValidatePortableSourceBaselineArtifact(ctx context.Context, resolver SourceBaselineArtifactResolver, target PortableRunTarget) (err error) {
	baseline := target.SourceBaseline
	if err := validatePortableSourceBaselineExpected(ctx, resolver, target); err != nil {
		return err
	}
	body, size, receipt, err := resolvePortableSourceBaseline(resolver, ctx, baseline)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			err = errors.Join(err, portableSourceBaselineError("close artifact body"))
		}
	}()
	return validatePortableSourceBaselineStream(ctx, body, size, receipt, target)
}

// ValidateSourceBaselineArtifact 是面向 source owner 的 validator 别名。
func ValidateSourceBaselineArtifact(ctx context.Context, resolver SourceBaselineArtifactResolver, target PortableRunTarget) error {
	return ValidatePortableSourceBaselineArtifact(ctx, resolver, target)
}

// resolvePortableSourceBaseline 获取解析器返回的 body，并确保错误路径也关闭 body。
func resolvePortableSourceBaseline(resolver SourceBaselineArtifactResolver, ctx context.Context, expected PortableSourceBaseline) (io.ReadCloser, int64, SourceBaselineProducerReceipt, error) {
	body, size, receipt, resolveErr := resolver.Resolve(ctx, expected.ArtifactKind, expected.ArtifactKey)
	if resolveErr != nil {
		if body != nil && !interfaceValueIsNil(body) {
			_ = body.Close()
		}
		return nil, 0, SourceBaselineProducerReceipt{}, portableSourceBaselineError("resolve artifact")
	}
	if body == nil || interfaceValueIsNil(body) {
		return nil, 0, SourceBaselineProducerReceipt{}, portableSourceBaselineError("resolver returned no body")
	}
	return body, size, receipt, nil
}

// validatePortableSourceBaselineStream 校验回执、流式物料摘要并复核 Git bundle。
func validatePortableSourceBaselineStream(ctx context.Context, body io.Reader, size int64, receipt SourceBaselineProducerReceipt, target PortableRunTarget) (err error) {
	baseline := target.SourceBaseline
	if err := validatePortableSourceBaselineReceipt(receipt, target); err != nil {
		return err
	}
	if err := validatePortableSourceBaselineSize(size, baseline.ArtifactSizeBytes); err != nil {
		return err
	}
	temporaryRoot, err := createPortableSourceBaselineTemp()
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := removePortableSourceBaselineTemp(temporaryRoot); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	bundlePath := filepath.Join(temporaryRoot, "source-baseline.bundle")
	digest, actualSize, stageErr := stagePortableSourceBaseline(body, bundlePath, size)
	if stageErr != nil {
		return stageErr
	}
	if digest != baseline.ArtifactSHA256 || digest != receipt.ArtifactSHA256 || actualSize != size {
		return portableSourceBaselineError("artifact digest")
	}
	return validatePortableSourceBaselineBundle(ctx, bundlePath, receipt)
}

// validatePortableSourceBaselineExpected 校验上下文、resolver 和 descriptor 形状。
func validatePortableSourceBaselineExpected(ctx context.Context, resolver SourceBaselineArtifactResolver, target PortableRunTarget) error {
	if ctx == nil {
		return portableSourceBaselineError("context")
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(portableSourceBaselineError("context"), err)
	}
	if resolver == nil || interfaceValueIsNil(resolver) {
		return portableSourceBaselineError("resolver")
	}
	expected := target.SourceBaseline
	if target.RepositoryID == "" || target.ObjectFormat == "" || target.ObjectFormat != expected.ObjectFormat {
		return portableSourceBaselineError("target repository identity")
	}
	if err := validatePortableSourceBaselineArtifactShape(expected); err != nil {
		return err
	}
	if err := validatePortableSourceBaselineObjectIdentity(expected); err != nil {
		return err
	}
	return nil
}

// validatePortableSourceBaselineArtifactShape 校验内容寻址 artifact 字段。
func validatePortableSourceBaselineArtifactShape(expected PortableSourceBaseline) error {
	if expected.BundleContractID != PortableBundleContractID || expected.ArtifactKind != PortableSourceBaselineArtifactKind ||
		!validPortableToken(expected.ArtifactKey) || !validPortableDigest(expected.ArtifactSHA256) || expected.ArtifactSizeBytes <= 0 {
		return portableSourceBaselineError("expected identity")
	}
	return nil
}

// validatePortableSourceBaselineObjectIdentity 校验 object format 与 commit/tree OID。
func validatePortableSourceBaselineObjectIdentity(expected PortableSourceBaseline) error {
	format := gate.GitObjectFormat(expected.ObjectFormat)
	if !validOID(expected.BaselineCommitSHA, format) || !validOID(expected.BaselineTreeSHA, format) {
		return portableSourceBaselineError("expected object identity")
	}
	return nil
}

// validatePortableSourceBaselineSize 校验 resolver 声明的大小与 descriptor 一致。
func validatePortableSourceBaselineSize(actual, expected int64) error {
	if actual != expected || actual <= 0 || actual > portableSourceBaselineMaxBytes {
		return portableSourceBaselineError("artifact size")
	}
	return nil
}

// createPortableSourceBaselineTemp 创建仅 owner 可访问的 staging 根目录。
func createPortableSourceBaselineTemp() (string, error) {
	temporaryRoot, err := os.MkdirTemp("", ".source-baseline-validator-")
	if err != nil {
		return "", portableSourceBaselineError("create private temporary root")
	}
	if err := os.Chmod(temporaryRoot, 0o700); err != nil {
		_ = os.RemoveAll(temporaryRoot)
		return "", portableSourceBaselineError("protect private temporary root")
	}
	return temporaryRoot, nil
}

// validatePortableSourceBaselineReceipt 逐字段校验 producer receipt 与 expected。
func validatePortableSourceBaselineReceipt(receipt SourceBaselineProducerReceipt, target PortableRunTarget) error {
	expected := target.SourceBaseline
	if err := receipt.Validate(); err != nil || receipt.RepositoryID != target.RepositoryID {
		return portableSourceBaselineError("producer identity")
	}
	if receipt.ObjectFormat != expected.ObjectFormat || receipt.BaselineCommitSHA != expected.BaselineCommitSHA ||
		receipt.BaselineTreeSHA != expected.BaselineTreeSHA || receipt.ArtifactSHA256 != expected.ArtifactSHA256 ||
		receipt.ArtifactSizeBytes != expected.ArtifactSizeBytes {
		return portableSourceBaselineError("producer receipt")
	}
	return nil
}

// stagePortableSourceBaseline 将 body 有界复制到私有文件并计算 SHA-256。
func stagePortableSourceBaseline(body io.Reader, bundlePath string, claimedSize int64) (string, int64, error) {
	file, err := os.OpenFile(bundlePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, portableSourceBaselineError("create staged artifact")
	}
	hash := sha256.New()
	limited := io.LimitReader(body, portableSourceBaselineMaxBytes+1)
	count, copyErr := io.Copy(io.MultiWriter(file, hash), limited)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", 0, portableSourceBaselineError("read artifact")
	}
	if count <= 0 || count > portableSourceBaselineMaxBytes || count != claimedSize {
		return "", count, portableSourceBaselineError("artifact bounds")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), count, nil
}

// validatePortableSourceBaselineBundle 在隔离 bare repo 中复核 bundle 头、导入和 refs。
func validatePortableSourceBaselineBundle(ctx context.Context, bundlePath string, receipt SourceBaselineProducerReceipt) error {
	format := gate.GitObjectFormat(receipt.ObjectFormat)
	advertised, err := readPortableSourceBaselineHeader(bundlePath, format)
	if err != nil {
		return err
	}
	temporaryRoot := filepath.Dir(bundlePath)
	bareRoot := filepath.Join(temporaryRoot, "verify.git")
	if err := initBareRepository(ctx, temporaryRoot, bareRoot, format); err != nil {
		return portableSourceBaselineError("initialize isolated repository")
	}
	if err := runPortableGit(ctx, bareRoot, nil, io.Discard, "bundle", "verify", bundlePath); err != nil {
		return portableSourceBaselineError("verify bundle")
	}
	listHeads, err := runPortableGitOutput(ctx, bareRoot, "bundle", "list-heads", bundlePath)
	if err != nil || strictPortableSourceBaselineHead(listHeads, format) != advertised {
		return portableSourceBaselineError("bundle heads")
	}
	unbundled, err := runPortableGitOutput(ctx, bareRoot, "bundle", "unbundle", bundlePath)
	if err != nil || strictPortableSourceBaselineHead(unbundled, format) != advertised {
		return portableSourceBaselineError("unbundle")
	}
	refInput := "create " + PortableSourceBaselineArtifactRef + " " + receipt.BaselineCommitSHA + "\n"
	if err := runPortableGit(ctx, bareRoot, strings.NewReader(refInput), io.Discard, "update-ref", "--stdin"); err != nil {
		return portableSourceBaselineError("install isolated ref")
	}
	if err := validatePortableSourceBaselineObjects(ctx, bareRoot, receipt, format); err != nil {
		return err
	}
	return nil
}

// readPortableSourceBaselineHeader 解析受限长度的 bundle header，禁止 prerequisite。
func readPortableSourceBaselineHeader(bundlePath string, format gate.GitObjectFormat) (string, error) {
	file, err := os.Open(bundlePath)
	if err != nil {
		return "", portableSourceBaselineError("open staged artifact")
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(io.LimitReader(file, portableSourceBaselineMaxHeader+1))
	first, err := reader.ReadString('\n')
	if err != nil || first != "# v2 git bundle\n" {
		return "", portableSourceBaselineError("bundle header")
	}
	bytesRead := len(first)
	var advertised string
	for {
		line, readErr := reader.ReadString('\n')
		bytesRead += len(line)
		if bytesRead > portableSourceBaselineMaxHeader {
			return "", portableSourceBaselineError("bundle header bounds")
		}
		if readErr != nil {
			return "", portableSourceBaselineError("bundle header terminator")
		}
		if line == "\n" {
			break
		}
		next, parseErr := parsePortableSourceBaselineHeaderLine(line, format, advertised)
		if parseErr != nil {
			return "", parseErr
		}
		advertised = next
	}
	if advertised == "" {
		return "", portableSourceBaselineError("bundle ref")
	}
	return advertised, nil
}

// parsePortableSourceBaselineHeaderLine 校验一个唯一 ref header 行。
func parsePortableSourceBaselineHeaderLine(line string, format gate.GitObjectFormat, previous string) (string, error) {
	line = strings.TrimSuffix(line, "\n")
	if strings.HasPrefix(line, "-") {
		return "", portableSourceBaselineError("bundle prerequisite")
	}
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[1] != PortableSourceBaselineArtifactRef || !validOID(fields[0], format) || previous != "" {
		return "", portableSourceBaselineError("bundle ref")
	}
	return fields[0], nil
}

func strictPortableSourceBaselineHead(output []byte, format gate.GitObjectFormat) string {
	line, err := strictGitLine(output)
	if err != nil {
		return ""
	}
	fields := strings.Fields(line)
	if len(fields) != 2 || fields[1] != PortableSourceBaselineArtifactRef || !validOID(fields[0], format) {
		return ""
	}
	return fields[0]
}

// validatePortableSourceBaselineObjects 校验隔离仓身份、commit/tree 和完整闭包。
func validatePortableSourceBaselineObjects(ctx context.Context, bareRoot string, receipt SourceBaselineProducerReceipt, format gate.GitObjectFormat) error {
	if err := validatePortableSourceBaselineRepository(ctx, bareRoot, receipt, format); err != nil {
		return err
	}
	if err := validatePortableSourceBaselineCommit(ctx, bareRoot, receipt, format); err != nil {
		return err
	}
	return validatePortableSourceBaselineClosure(ctx, bareRoot)
}

// validatePortableSourceBaselineRepository 校验 object format 与唯一 ref。
func validatePortableSourceBaselineRepository(ctx context.Context, bareRoot string, receipt SourceBaselineProducerReceipt, format gate.GitObjectFormat) error {
	objectFormat, err := runPortableGitOutput(ctx, bareRoot, "rev-parse", "--show-object-format")
	if err != nil || string(objectFormat) != string(format)+"\n" {
		return portableSourceBaselineError("object format")
	}
	refs, err := runPortableGitOutput(ctx, bareRoot, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil || strictPortableSourceBaselineHead(refs, format) != receipt.BaselineCommitSHA {
		return portableSourceBaselineError("isolated refs")
	}
	return nil
}

// validatePortableSourceBaselineCommit 校验 parentless deterministic commit 和 tree。
func validatePortableSourceBaselineCommit(ctx context.Context, bareRoot string, receipt SourceBaselineProducerReceipt, format gate.GitObjectFormat) error {
	commit, err := readSourceObject(ctx, bareRoot, receipt.BaselineCommitSHA)
	if err != nil || commit.kind != "commit" {
		return portableSourceBaselineError("baseline commit")
	}
	tree, parents, err := parseCommitObject(commit, receipt.BaselineTreeSHA)
	if err != nil || tree != receipt.BaselineTreeSHA || len(parents) != 0 {
		return portableSourceBaselineError("baseline commit shape")
	}
	treeObject, err := readSourceObject(ctx, bareRoot, receipt.BaselineTreeSHA)
	if err != nil || treeObject.kind != "tree" {
		return portableSourceBaselineError("baseline tree")
	}
	expectedCommit, err := DeterministicSourceBaselineCommitSHA(receipt.BaselineTreeSHA, format)
	if err != nil || expectedCommit != receipt.BaselineCommitSHA {
		return portableSourceBaselineError("baseline determinism")
	}
	return nil
}

// validatePortableSourceBaselineClosure 要求 fsck 无 broken 或 unreachable object。
func validatePortableSourceBaselineClosure(ctx context.Context, bareRoot string) error {
	fsckOutput, err := runPortableGitOutput(ctx, bareRoot, "fsck", "--full", "--strict", "--no-reflogs", "--unreachable", "--no-progress", PortableSourceBaselineArtifactRef)
	if err != nil || len(bytes.TrimSpace(fsckOutput)) != 0 {
		return portableSourceBaselineError("object closure")
	}
	return nil
}

func runPortableGit(ctx context.Context, repoRoot string, stdin io.Reader, stdout io.Writer, args ...string) error {
	if err := runGit(ctx, repoRoot, stdin, stdout, args...); err != nil {
		return portableSourceBaselineError("git operation")
	}
	return nil
}

func runPortableGitOutput(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	var output bytes.Buffer
	if err := runPortableGit(ctx, repoRoot, nil, &output, args...); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func removePortableSourceBaselineTemp(path string) error {
	if path == "" {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return portableSourceBaselineError("cleanup temporary root")
	}
	return nil
}

func portableSourceBaselineError(reason string) error {
	return errors.New("source baseline artifact validation failed: " + reason)
}
