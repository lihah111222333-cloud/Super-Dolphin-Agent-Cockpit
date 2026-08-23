// Package toolchainidentity computes content identities for trusted compiler
// inputs without depending on caller paths, environment discovery, or cwd.
package toolchainidentity

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const (
	closureManifestDomain = "toolchain-closure/v1"
	maxManifestEntries    = 1 << 20
	maxRelativePathBytes  = 4096
	maxDigestFieldBytes   = 128
)

type failureStageError struct{ stage string }

// Error 只返回稳定工具链测量阶段，不泄露绝对路径。
func (failure *failureStageError) Error() string {
	return "toolchain identity stage failed: " + failure.stage
}

func stageFailure(stage string, cause error) error {
	failure := &failureStageError{stage: stage}
	if cause == nil {
		return failure
	}
	return errors.Join(failure, cause)
}

// FailureStage 返回安全、稳定的工具链测量失败阶段。
func FailureStage(err error) string {
	var failure *failureStageError
	if errors.As(err, &failure) {
		return failure.stage
	}
	return "unknown"
}

// DigestExecutable 对已打开的可执行文件字节流计算摘要，不解析或暴露文件路径。
func DigestExecutable(reader io.Reader) (string, error) {
	if isNilReader(reader) {
		return "", errors.New("identity executable reader is required")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", errors.New("identity executable could not be read")
	}
	return formatDigest(hash.Sum(nil)), nil
}

// DigestFile 对显式提供的 regular、非符号链接文件计算摘要。
func DigestFile(filePath string) (string, error) {
	if filePath == "" {
		return "", errors.New("identity file path is required")
	}
	info, err := os.Lstat(filePath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("identity file must be a regular non-symlink file")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", errors.New("identity file cannot be opened")
	}
	digest, digestErr := DigestExecutable(file)
	closeErr := file.Close()
	if digestErr != nil {
		return "", digestErr
	}
	if closeErr != nil {
		return "", errors.New("identity file cannot be closed")
	}
	return digest, nil
}

// DigestDirectory 对完整 regular 文件闭包按 slash 相对路径排序并计算 canonical 摘要。
func DigestDirectory(root string) (string, error) {
	if !cleanAbsolutePath(root) {
		return "", stageFailure("closure_root", nil)
	}
	if err := validateClosureRoot(root); err != nil {
		return "", stageFailure("closure_root", err)
	}
	entries, err := collectClosureEntries(root)
	if err != nil {
		return "", stageFailure("closure_walk", err)
	}
	digest, err := digestClosureManifest(entries)
	if err != nil {
		return "", stageFailure("closure_digest", err)
	}
	return digest, nil
}

// CompilerClosureDigest 运行调用方显式 compiler 解析 GOROOT 并计算 regular 文件闭包摘要。
func CompilerClosureDigest(ctx context.Context, compilerPath string) (string, error) {
	if ctx == nil {
		return "", stageFailure("preflight", nil)
	}
	if err := validateCompilerPath(compilerPath); err != nil {
		return "", stageFailure("compiler_path", err)
	}
	root, err := compilerGOROOT(ctx, compilerPath)
	if err != nil {
		return "", stageFailure("compiler_goroot", err)
	}
	return DigestDirectory(root)
}

type closureEntry struct {
	relative string
	filePath string
}

func cleanAbsolutePath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value
}

// isNilReader 拒绝 nil interface 以及承载 nil 指针的 typed-nil reader。
func isNilReader(reader io.Reader) bool {
	if reader == nil {
		return true
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// validateClosureRoot 校验闭包根目录本身是非符号链接目录。
func validateClosureRoot(root string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("identity closure root must be a regular non-symlink directory")
	}
	return nil
}

// collectClosureEntries 使用 Lstat 收集并排序闭包中的 regular 文件。
func collectClosureEntries(root string) ([]closureEntry, error) {
	entries := make([]closureEntry, 0, 1024)
	err := filepath.WalkDir(root, func(filePath string, _ os.DirEntry, walkErr error) error {
		return appendClosureEntry(root, filePath, walkErr, &entries)
	})
	if err != nil {
		return nil, errors.New("walk identity closure failed")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].relative < entries[j].relative })
	return entries, nil
}

// appendClosureEntry 将一个已 Lstat 的 regular 文件加入闭包清单。
func appendClosureEntry(root, filePath string, walkErr error, entries *[]closureEntry) error {
	if walkErr != nil {
		return errors.New("identity closure walk failed")
	}
	info, err := os.Lstat(filePath)
	if err != nil {
		return errors.New("identity closure entry cannot be inspected")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("identity closure contains a symlink")
	}
	if info.IsDir() {
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("identity closure contains a non-regular file")
	}
	relative, err := filepath.Rel(root, filePath)
	if err != nil {
		return errors.New("identity closure relative path cannot be computed")
	}
	relative = filepath.ToSlash(relative)
	if !validRelativePath(relative) {
		return errors.New("identity closure contains an invalid relative path")
	}
	if len(*entries) >= maxManifestEntries {
		return errors.New("identity closure contains too many files")
	}
	*entries = append(*entries, closureEntry{relative: relative, filePath: filePath})
	return nil
}

// digestClosureManifest 对排序后的路径与摘要执行 domain-separated framing。
func digestClosureManifest(entries []closureEntry) (string, error) {
	hash := sha256.New()
	if _, err := hash.Write([]byte(closureManifestDomain)); err != nil {
		return "", errors.New("identity manifest domain could not be written")
	}
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(entries)))
	if _, err := hash.Write(count[:]); err != nil {
		return "", errors.New("identity manifest count could not be written")
	}
	for _, entry := range entries {
		digest, err := DigestFile(entry.filePath)
		if err != nil {
			return "", errors.New("read identity closure file failed")
		}
		if err := writeManifestField(hash, []byte(entry.relative), maxRelativePathBytes); err != nil {
			return "", errors.New("identity manifest path could not be written")
		}
		if err := writeManifestField(hash, []byte(digest), maxDigestFieldBytes); err != nil {
			return "", errors.New("identity manifest digest could not be written")
		}
	}
	return formatDigest(hash.Sum(nil)), nil
}

// validateCompilerPath 强制 compiler 是调用方显式提供的 clean executable。
func validateCompilerPath(compilerPath string) error {
	if !cleanAbsolutePath(compilerPath) {
		return errors.New("compiler path must be a clean absolute path")
	}
	info, err := os.Lstat(compilerPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("compiler path must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("compiler path must be executable")
	}
	return nil
}

// compilerGOROOT 以固定 GOENV/GOTOOLCHAIN/GOPROXY 环境运行 compiler env GOROOT。
func compilerGOROOT(ctx context.Context, compilerPath string) (string, error) {
	command := exec.CommandContext(ctx, compilerPath, "env", "GOROOT")
	command.Env = compilerEnvironment()
	if err := gateprivate.ConfigureBoundedCommand(command); err != nil {
		return "", errors.New("compiler closure command could not be bounded")
	}
	output, err := command.Output()
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", errors.Join(errors.New("compiler GOROOT command failed"), contextErr)
		}
		return "", errors.New("compiler GOROOT command failed")
	}
	root := strings.TrimSpace(string(output))
	if !cleanAbsolutePath(root) {
		return "", errors.New("compiler GOROOT must be a clean absolute path")
	}
	return root, nil
}

func validRelativePath(value string) bool {
	if !relativePathShapeIsValid(value) {
		return false
	}
	if !relativePathClean(value) {
		return false
	}
	return slashPathIsValid(value)
}

// relativePathShapeIsValid 校验相对路径的基础形状和长度。
func relativePathShapeIsValid(value string) bool {
	if value == "" || value == "." {
		return false
	}
	if filepath.IsAbs(value) || len([]byte(value)) > maxRelativePathBytes {
		return false
	}
	return strings.IndexByte(value, 0) < 0
}

// relativePathClean 校验宿主平台上的相对路径没有逃逸或隐式清理。
func relativePathClean(value string) bool {
	clean := filepath.Clean(value)
	if clean != value || clean == ".." {
		return false
	}
	return !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// slashPathIsValid 校验 canonical manifest 使用 slash 分隔且无路径逃逸。
func slashPathIsValid(value string) bool {
	slash := filepath.ToSlash(value)
	if slash == "" || slash == "." {
		return false
	}
	if path.Clean(slash) != slash {
		return false
	}
	return !strings.HasPrefix(slash, "../")
}

func writeManifestField(writer io.Writer, field []byte, maxBytes int) error {
	if maxBytes <= 0 || len(field) > maxBytes {
		return errors.New("identity manifest field is too long")
	}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	if _, err := writer.Write(length[:]); err != nil {
		return err
	}
	_, err := writer.Write(field)
	return err
}

// compilerEnvironment 从继承环境中移除会改变 toolchain 解析的变量并固定协议变量。
func compilerEnvironment() []string {
	inherited := os.Environ()
	environment := make([]string, 0, len(inherited)+3)
	for _, entry := range inherited {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || strings.HasPrefix(name, "GO") || name == "CGO_ENABLED" {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "GOENV=off", "GOTOOLCHAIN=local", "GOPROXY=off")
}

func formatDigest(sum []byte) string {
	return "sha256:" + hex.EncodeToString(sum)
}
