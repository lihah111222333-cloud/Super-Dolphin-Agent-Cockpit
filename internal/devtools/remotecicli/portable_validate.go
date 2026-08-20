// Package remotecicli 提供独立的远程 CI descriptor 校验组合根。
package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	descriptorMaxBytes = 1 << 20
	outputMaxBytes     = 16 << 10
)

const usageText = "Usage: super-dolphin-remote-ci validate --descriptor <absolute-file>\n"

// ErrExecutionContractUpgradeRequired 表示 execute 尚未获得可验证的执行合约。
var ErrExecutionContractUpgradeRequired = errors.New("execution contract upgrade required")

// Run 只暴露 descriptor 校验入口，并保持 stdout 为机器协议流。
func Run(args []string, stdout, stderr io.Writer) int {
	err := dispatchCLI(args, stdout, stderr)
	if err == nil {
		return 0
	}
	if _, writeErr := fmt.Fprintln(stderr, "super-dolphin-remote-ci:", err); writeErr != nil {
		return 3
	}
	return 1
}

// dispatchCLI 分派受支持的窄命令，execute 只返回升级哨兵错误。
func dispatchCLI(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newCLIFailure("missing command", nil)
	}
	switch args[0] {
	case "validate":
		return dispatchValidate(args[1:], stdout, stderr)
	case "execute":
		return ErrExecutionContractUpgradeRequired
	default:
		return newCLIFailure("unknown command", nil)
	}
}

// dispatchValidate 校验参数、读取 descriptor 并绑定共享的 typed run。
func dispatchValidate(args []string, stdout, stderr io.Writer) error {
	descriptorPath, err := parseDescriptorPath(args)
	if err != nil {
		return err
	}
	if err := writeProgress(stderr, "descriptor path accepted"); err != nil {
		return newCLIFailure("write progress", err)
	}
	raw, err := readDescriptorFile(descriptorPath)
	if err != nil {
		return newCLIFailure("read descriptor", err)
	}
	if err := writeProgress(stderr, "descriptor bytes read"); err != nil {
		return newCLIFailure("write progress", err)
	}
	descriptor, err := remoteci.DecodePortableRunDescriptor(raw)
	if err != nil {
		return newCLIFailure("decode descriptor", err)
	}
	if err := writeProgress(stderr, "descriptor decoded"); err != nil {
		return newCLIFailure("write progress", err)
	}
	binding, err := remoteci.BindPortableRun(
		context.Background(),
		descriptor,
		gitObjectResolver{},
		fileIdentityReader{},
	)
	if err != nil {
		return newCLIFailure("bind portable run", err)
	}
	output, err := boundedBindingJSON(raw, binding)
	if err != nil {
		return newCLIFailure("render binding", err)
	}
	if _, err := stdout.Write(output); err != nil {
		return newCLIFailure("write result", err)
	}
	return writeProgress(stderr, "portable run bound")
}

// parseDescriptorPath 只接受一个显式、clean、绝对 descriptor 路径。
func parseDescriptorPath(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--descriptor" || args[1] == "" {
		return "", newCLIFailure("usage: "+usageText, nil)
	}
	path := args[1]
	if strings.TrimSpace(path) != path || filepath.Clean(path) != path || !filepath.IsAbs(path) {
		return "", newCLIFailure("descriptor path must be a clean absolute path", nil)
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return "", newCLIFailure("descriptor path contains control characters", nil)
		}
	}
	return path, nil
}

// readDescriptorFile 以 bounded read 打开 regular non-symlink descriptor 文件。
func readDescriptorFile(path string) ([]byte, error) {
	if err := validateAbsolutePathComponents(path, false); err != nil {
		return nil, errors.New("descriptor path is not trusted")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("descriptor file is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("descriptor file must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("descriptor file cannot be opened")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, descriptorMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.New("descriptor file cannot be read")
	}
	if len(data) == 0 || len(data) > descriptorMaxBytes {
		return nil, errors.New("descriptor exceeds the bounded size")
	}
	return data, nil
}

// boundedBindingJSON 只投影 descriptor 与 identity 摘要，拒绝把 binding 原样写到 stdout。
func boundedBindingJSON(descriptor []byte, binding remoteci.BoundPortableRun) ([]byte, error) {
	descriptorDigest := digestBytes(descriptor)
	if !isDigest(binding.BindingSHA256) {
		return nil, errors.New("portable binding omitted canonical identity digest")
	}
	result := struct {
		Schema                 string `json:"schema"`
		Status                 string `json:"status"`
		ExecutionProvider      string `json:"execution_provider"`
		RepositoryID           string `json:"repository_id"`
		TargetCommitSHA        string `json:"target_commit_sha"`
		TargetTreeSHA          string `json:"target_tree_sha"`
		WorkloadManifestSHA256 string `json:"workload_manifest_sha256"`
		EngineArtifactSHA256   string `json:"engine_artifact_sha256"`
		DescriptorDigest       string `json:"descriptor_sha256"`
		BindingDigest          string `json:"binding_sha256"`
	}{
		Schema: "portable-remote-ci-validation/v1", Status: "NOT_VERIFIED",
		ExecutionProvider: binding.ExecutionProvider, RepositoryID: binding.Target.RepositoryID,
		TargetCommitSHA: binding.Target.CommitSHA, TargetTreeSHA: binding.Target.TreeSHA,
		WorkloadManifestSHA256: binding.WorkloadManifest.SHA256,
		EngineArtifactSHA256:   binding.Engine.RuntimeArtifactSHA256,
		DescriptorDigest:       descriptorDigest, BindingDigest: binding.BindingSHA256,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, errors.New("encode bounded binding result")
	}
	if len(data) > outputMaxBytes {
		return nil, errors.New("binding result exceeds the bounded size")
	}
	data = append(data, '\n')
	return data, nil
}

// isDigest 判断输出摘要是否为固定 sha256 wire 形状。
func isDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

// digestBytes 生成 descriptor 原始字节的 bounded 输出摘要。
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// gitObjectResolver 通过 git object database 读取绑定 target tree，不读取当前工作树文件。
type gitObjectResolver struct{}

// ResolveCommitTree 将显式 commit 解析为其 tree，并拒绝空或多行输出。
func (gitObjectResolver) ResolveCommitTree(ctx context.Context, repositoryRoot, commitSHA string) (string, error) {
	output, err := runGitObjectCommand(ctx, repositoryRoot, "rev-parse", commitSHA+"^{tree}")
	if err != nil {
		return "", errors.New("resolve commit tree failed")
	}
	tree := strings.TrimSpace(string(output))
	if tree == "" || strings.ContainsAny(tree, "\r\n") {
		return "", errors.New("resolve commit tree returned invalid output")
	}
	return tree, nil
}

// ReadTreeBlob 只读取 target tree 中的 repo-relative manifest blob。
func (gitObjectResolver) ReadTreeBlob(ctx context.Context, repositoryRoot, treeSHA, repoRelativePath string) (remoteci.GitTreeBlob, error) {
	entry, err := runGitObjectCommand(ctx, repositoryRoot, "ls-tree", "-z", treeSHA, "--", repoRelativePath)
	if err != nil {
		return remoteci.GitTreeBlob{}, errors.New("inspect target tree blob failed")
	}
	mode, objectType, err := parseGitTreeEntry(entry, repoRelativePath)
	if err != nil || objectType != "blob" || (mode != "100644" && mode != "100755") {
		return remoteci.GitTreeBlob{}, errors.New("target tree manifest is not a regular blob")
	}
	output, err := runGitObjectCommand(ctx, repositoryRoot, "cat-file", "blob", treeSHA+":"+repoRelativePath)
	if err != nil {
		return remoteci.GitTreeBlob{}, errors.New("read target tree blob failed")
	}
	return remoteci.GitTreeBlob{Mode: mode, Data: output}, nil
}

// parseGitTreeEntry 严格解析单个 NUL 终止的 git ls-tree 记录。
func parseGitTreeEntry(raw []byte, expectedPath string) (string, string, error) {
	if len(raw) == 0 || raw[len(raw)-1] != 0 || bytes.Count(raw, []byte{0}) != 1 {
		return "", "", errors.New("git tree entry is not singular")
	}
	metadata, pathBytes, ok := bytes.Cut(raw[:len(raw)-1], []byte{'\t'})
	if !ok || string(pathBytes) != expectedPath {
		return "", "", errors.New("git tree entry path drift")
	}
	fields := strings.Fields(string(metadata))
	if len(fields) != 3 || fields[2] == "" {
		return "", "", errors.New("git tree entry metadata is invalid")
	}
	return fields[0], fields[1], nil
}

// runGitObjectCommand 执行只读 git object 查询，不允许进入旧 gate binary。
func runGitObjectCommand(ctx context.Context, repositoryRoot string, args ...string) ([]byte, error) {
	if ctx == nil || repositoryRoot == "" || len(args) == 0 {
		return nil, errors.New("git object command inputs are incomplete")
	}
	if err := validateAbsolutePathComponents(repositoryRoot, true); err != nil {
		return nil, err
	}
	commandArgs := append([]string{"-C", repositoryRoot}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

// fileIdentityReader 只读取 descriptor 指定 authority 文件的 regular-file identity。
type fileIdentityReader struct{}

// ReadFileIdentity 返回 bounded、稳定的文件身份，不打开数据库或读取其内容。
func (fileIdentityReader) ReadFileIdentity(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		return "", errors.New("file identity context is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateAbsolutePathComponents(path, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("authority file is not a regular non-symlink file")
	}
	identity, err := stableFileIdentity(info)
	if err != nil {
		return "", err
	}
	return identity, nil
}

// ReadFileSHA256 返回 descriptor 指定 config 文件的 bounded 内容摘要。
func (fileIdentityReader) ReadFileSHA256(ctx context.Context, path string) (string, error) {
	if ctx == nil {
		return "", errors.New("file digest context is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := readRegularBoundedFile(path)
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

// readRegularBoundedFile 读取 regular non-symlink 文件并限制内存占用。
func readRegularBoundedFile(path string) ([]byte, error) {
	if err := validateAbsolutePathComponents(path, false); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("bound file is not a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("bound file cannot be opened")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, descriptorMaxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > descriptorMaxBytes {
		return nil, errors.New("bound file cannot be read within the size limit")
	}
	return data, nil
}

// validateAbsolutePathComponents 拒绝路径任一现存组件的 symlink 与类型漂移。
func validateAbsolutePathComponents(value string, wantDirectory bool) error {
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return errors.New("path must be clean and absolute")
	}
	if err := rejectSymlinkPathComponents(value); err != nil {
		return err
	}
	return validateAbsolutePathType(value, wantDirectory)
}

// rejectSymlinkPathComponents 拒绝绝对路径中任一现存组件的符号链接。
func rejectSymlinkPathComponents(value string) error {
	current := string(filepath.Separator)
	volume := filepath.VolumeName(value)
	if volume != "" {
		current = volume + string(filepath.Separator)
	}
	remainder := strings.TrimPrefix(value, current)
	for component := range strings.SplitSeq(remainder, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return errors.New("path component is unavailable")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("path component must not be a symbolic link")
		}
	}
	return nil
}

// validateAbsolutePathType 校验绝对路径最终对象的目录或 regular-file 类型。
func validateAbsolutePathType(value string, wantDirectory bool) error {
	info, err := os.Lstat(value)
	if err != nil {
		return errors.New("path is unavailable")
	}
	if wantDirectory && !info.IsDir() {
		return errors.New("repository root must be a directory")
	}
	if !wantDirectory && !info.Mode().IsRegular() {
		return errors.New("authority path must be a regular file")
	}
	return nil
}

// stableFileIdentity 读取 Unix/Windows FileInfo 中的设备与 inode 等价身份。
func stableFileIdentity(info os.FileInfo) (string, error) {
	system := info.Sys()
	if system == nil {
		return "", errors.New("authority file identity is unavailable")
	}
	value := reflect.ValueOf(system)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", errors.New("authority file identity is unavailable")
		}
		value = value.Elem()
	}
	device, ok := integerFileIdentityField(value, "Dev", "VolumeSerialNumber")
	if !ok {
		return "", errors.New("authority file device identity is unsupported")
	}
	inode, ok := integerFileIdentityField(value, "Ino")
	if !ok {
		high, highOK := integerFileIdentityField(value, "FileIndexHigh")
		low, lowOK := integerFileIdentityField(value, "FileIndexLow")
		if !highOK || !lowOK {
			return "", errors.New("authority file inode identity is unsupported")
		}
		inode = (high << 32) | low
	}
	return fmt.Sprintf("dev:%d:ino:%d:size:%d", device, inode, info.Size()), nil
}

// integerFileIdentityField 读取跨平台 stat 结构中的无符号整数型身份字段。
func integerFileIdentityField(value reflect.Value, names ...string) (uint64, bool) {
	for _, name := range names {
		field := value.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		if field.CanUint() {
			return field.Uint(), true
		}
		if field.CanInt() && field.Int() >= 0 {
			return uint64(field.Int()), true
		}
	}
	return 0, false
}

// writeProgress 写入固定阶段文本，禁止携带 descriptor、配置或路径内容。
func writeProgress(stderr io.Writer, stage string) error {
	_, err := fmt.Fprintln(stderr, "progress:", stage)
	return err
}

type cliFailure struct {
	message string
	cause   error
}

// newCLIFailure 构造不携带敏感输入的稳定 CLI 诊断。
func newCLIFailure(message string, cause error) error {
	return cliFailure{message: message, cause: cause}
}

// Error 返回不携带敏感输入的 CLI 诊断文本。
func (e cliFailure) Error() string {
	return e.message
}

// Unwrap 保留内部根因供 errors.Is 检查，同时不直接渲染根因。
func (e cliFailure) Unwrap() error {
	return e.cause
}
