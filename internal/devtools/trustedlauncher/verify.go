package trustedlauncher

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	gateclosure "github.com/lihah111222333-cloud/super-dolphin-agent/build/gate/closure"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// VerifyOptions binds the running executable to one receipt and one Git tree.
type VerifyOptions struct {
	RepositoryRoot string
	Tree           string
	ReceiptPath    string
	Linked         LinkedIdentity
}

// Verify 从精确树与当前 executable 重算每一项回执身份。
func Verify(ctx context.Context, options VerifyOptions) error {
	if ctx == nil {
		return errors.New("trusted launcher verify context is required")
	}
	if !treePattern.MatchString(options.Tree) || options.Linked.Tree != options.Tree {
		return errors.New("linked launcher tree does not match the requested exact tree")
	}
	executable, receiptPath, err := canonicalVerificationPaths(options.ReceiptPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		return fmt.Errorf("read launcher receipt: %w", err)
	}
	receipt, err := DecodeReceipt(data)
	if err != nil {
		return err
	}
	if err := verifyReceiptIdentity(ctx, options, receipt); err != nil {
		return err
	}
	binaryDigest, err := digestFile(executable)
	if err != nil {
		return fmt.Errorf("digest trusted launcher executable: %w", err)
	}
	if binaryDigest != receipt.BinarySHA256 {
		return errors.New("trusted launcher executable digest does not match its receipt")
	}
	return nil
}

// VerifyArtifact 在不读取仓库树的情况下校验已安装二进制和回执。
func VerifyArtifact(ctx context.Context, receiptPath string, linked LinkedIdentity) error {
	if ctx == nil {
		return errors.New("trusted launcher artifact context is required")
	}
	executable, canonicalReceipt, err := canonicalVerificationPaths(receiptPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(canonicalReceipt)
	if err != nil {
		return fmt.Errorf("read launcher receipt: %w", err)
	}
	receipt, err := DecodeReceipt(data)
	if err != nil {
		return err
	}
	if err := verifyArtifactReceipt(receipt, linked); err != nil {
		return err
	}
	return verifyArtifactDigest(executable, receipt)
}

func verifyArtifactReceipt(receipt Receipt, linked LinkedIdentity) error {
	if linkedIdentityFromReceipt(receipt) != linked {
		return errors.New("trusted launcher artifact receipt does not match linked identity")
	}
	if receipt.GoVersion != runtime.Version() || receipt.GOOS != runtime.GOOS || receipt.GOARCH != runtime.GOARCH {
		return errors.New("trusted launcher artifact runtime identity does not match its receipt")
	}
	return nil
}

func verifyArtifactDigest(executable string, receipt Receipt) error {
	binaryDigest, err := digestFile(executable)
	if err != nil || binaryDigest != receipt.BinarySHA256 {
		return errors.Join(errors.New("trusted launcher artifact digest does not match its receipt"), err)
	}
	return nil
}

func verifyReceiptIdentity(ctx context.Context, options VerifyOptions, receipt Receipt) error {
	if err := verifyReceiptLinkedIdentity(options, receipt); err != nil {
		return err
	}
	if err := verifyReceiptRuntimeIdentity(receipt); err != nil {
		return err
	}
	if err := verifyReceiptCompilerIdentity(ctx, receipt); err != nil {
		return err
	}
	if err := verifyReceiptCompileClosure(ctx, options, receipt); err != nil {
		return err
	}
	return verifyReceiptClosureProvenance(options, receipt)
}

func verifyReceiptLinkedIdentity(options VerifyOptions, receipt Receipt) error {
	if receipt.Tree != options.Tree || linkedIdentityFromReceipt(receipt) != options.Linked {
		return errors.New("trusted launcher receipt does not match linked build identity")
	}
	return nil
}

func verifyReceiptRuntimeIdentity(receipt Receipt) error {
	if receipt.GoVersion != runtime.Version() || receipt.GOOS != runtime.GOOS || receipt.GOARCH != runtime.GOARCH {
		return errors.New("trusted launcher runtime identity does not match its receipt")
	}
	return nil
}

func verifyReceiptCompilerIdentity(ctx context.Context, receipt Receipt) error {
	if err := verifyCompilerIdentity(ctx, receipt.CompilerPath, receipt.GoVersion, receipt.GOOS, receipt.GOARCH); err != nil {
		return err
	}
	return verifyReceiptCompilerDigests(ctx, receipt)
}

func verifyReceiptCompilerDigests(ctx context.Context, receipt Receipt) error {
	compilerDigest, err := digestFile(receipt.CompilerPath)
	if err != nil || compilerDigest != receipt.CompilerSHA256 {
		return errors.Join(errors.New("trusted launcher compiler digest does not match its receipt"), err)
	}
	compilerClosure, err := compilerClosureDigest(ctx, receipt.CompilerPath)
	if err != nil || compilerClosure != receipt.CompilerClosureSHA256 {
		return errors.Join(errors.New("trusted launcher compiler closure does not match its receipt"), err)
	}
	return nil
}

func verifyReceiptCompileClosure(ctx context.Context, options VerifyOptions, receipt Receipt) error {
	sourceDigest, toolchainDigest, _, err := remoteci.LoadGateCLICompileClosure(ctx, options.RepositoryRoot, options.Tree)
	if err != nil {
		return fmt.Errorf("recompute trusted launcher compile closure: %w", err)
	}
	if sourceDigest != receipt.SourceSHA256 || toolchainDigest != receipt.ToolchainSHA256 {
		return errors.New("trusted launcher exact-tree compile closure does not match its receipt")
	}
	return nil
}

func verifyReceiptClosureProvenance(options VerifyOptions, receipt Receipt) error {
	compiledProvenance, err := gateclosure.GeneratorProvenance()
	if err != nil {
		return fmt.Errorf("compute compiled launcher closure provenance: %w", err)
	}
	treeProvenance, err := gateclosure.GeneratorProvenanceForTree(options.RepositoryRoot, options.Tree)
	if err != nil {
		return fmt.Errorf("compute exact-tree launcher closure provenance: %w", err)
	}
	if compiledProvenance != receipt.ClosureProvenance || treeProvenance != receipt.ClosureProvenance {
		return errors.New("trusted launcher closure provenance does not match the exact tree")
	}
	return nil
}

// canonicalVerificationPaths 解析并检查非可写、同目录的固定二进制和回执路径。
func canonicalVerificationPaths(receiptPath string) (string, string, error) {
	if !filepath.IsAbs(receiptPath) || filepath.Base(receiptPath) != ReceiptName {
		return "", "", errors.New("trusted launcher receipt path must be an absolute canonical receipt path")
	}
	executable, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("resolve trusted launcher executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", "", fmt.Errorf("resolve trusted launcher executable path: %w", err)
	}
	receiptPath, err = filepath.EvalSymlinks(receiptPath)
	if err != nil {
		return "", "", fmt.Errorf("resolve trusted launcher receipt path: %w", err)
	}
	if err := validateCanonicalArtifactPair(executable, receiptPath); err != nil {
		return "", "", err
	}
	return executable, receiptPath, nil
}

// validateCanonicalArtifactPair 验证二进制与回执是固定同目录且不可写的一对。
func validateCanonicalArtifactPair(executable, receiptPath string) error {
	if filepath.Base(executable) != BinaryName || filepath.Dir(executable) != filepath.Dir(receiptPath) {
		return errors.New("trusted launcher executable and receipt must be canonical siblings")
	}
	for _, path := range []string{executable, receiptPath} {
		if err := validateNonWritableArtifactPath(path); err != nil {
			return err
		}
	}
	info, err := os.Stat(executable)
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return errors.Join(errors.New("trusted launcher executable is not executable"), err)
	}
	return nil
}

func validateNonWritableArtifactPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return errors.Join(fmt.Errorf("trusted launcher path %s must be a regular non-writable file", path), err)
	}
	return nil
}

func digestFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + fmt.Sprintf("%x", sum[:]), nil
}

// writeReceiptFile 以 create-only、fsync 与只读权限持久化严格回执。
func writeReceiptFile(path string, receipt Receipt) error {
	data, err := encodeReceipt(receipt)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create launcher receipt: %w", err)
	}
	writeErr := func() error {
		if _, err := file.Write(data); err != nil {
			return fmt.Errorf("write launcher receipt: %w", err)
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync launcher receipt: %w", err)
		}
		return nil
	}()
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(writeErr, closeErr)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		return fmt.Errorf("restrict launcher receipt mode: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open launcher directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("sync launcher directory: %w", syncErr), closeErr)
	}
	return nil
}
