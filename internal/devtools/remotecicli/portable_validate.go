// Package remotecicli 提供独立的远程 CI descriptor 校验组合根。
package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

const (
	descriptorMaxBytes = 1 << 20
	outputMaxBytes     = 16 << 10
)

// usageText 只描述仍保留的公开 descriptor/execute 入口；云制品命令已退役。
const usageText = "Usage: super-dolphin-remote-ci validate --descriptor <absolute-file> --repository <absolute-root> | execute --request <absolute-file> --repository <absolute-root> --runtime-config <absolute-file>\n"

// Run 只暴露契约许可的窄命令，并保持 stdout 为机器协议流。
func Run(args []string, stdout, stderr io.Writer) int {
	err := dispatchCLI(args, stdout, stderr)
	if err == nil {
		return 0
	}
	return 1
}

// dispatchCLI 分派受支持的 validate 与唯一 production execute 入口。
func dispatchCLI(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newCLIFailure("missing command", nil)
	}
	switch args[0] {
	case "validate":
		return dispatchValidate(args[1:], stdout)
	case "execute":
		return dispatchExecute(args[1:], stdout, stderr)
	default:
		return newCLIFailure("unknown command", nil)
	}
}

// dispatchValidate 校验参数、读取 descriptor 并绑定共享的 typed run。
func dispatchValidate(args []string, stdout io.Writer) error {
	raw, descriptor, repositoryRoot, err := loadPortableDescriptor(args)
	if err != nil {
		return err
	}
	binding, err := bindPortableDescriptor(descriptor, repositoryRoot)
	if err != nil {
		return newCLIFailure("bind portable run", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	expanded, err := expandBoundPortableRepositoryCatalog(ctx, repositoryRoot, binding)
	if err != nil {
		return newCLIFailure("expand portable workload catalog", err)
	}
	output, err := boundedBindingJSON(raw, binding, expanded.Workloads)
	if err != nil {
		return newCLIFailure("render binding", err)
	}
	if err := writeValidationResult(stdout, output); err != nil {
		return newCLIFailure("write result", err)
	}
	return nil
}

func writeValidationResult(writer io.Writer, data []byte) error {
	if writer == nil || len(data) == 0 {
		return errors.New("validation result writer or payload is invalid")
	}
	written, err := writer.Write(data)
	if err != nil || written != len(data) {
		return errors.New("validation result write incomplete")
	}
	return nil
}

func loadPortableDescriptor(args []string) ([]byte, remoteportable.PortableRunInput, string, error) {
	descriptorPath, repositoryRoot, err := parseDescriptorArgs(args)
	if err != nil {
		return nil, remoteportable.PortableRunInput{}, "", err
	}
	raw, err := readDescriptorFile(descriptorPath)
	if err != nil {
		return nil, remoteportable.PortableRunInput{}, "", newCLIFailure("read descriptor", err)
	}
	descriptor, err := remoteportable.DecodePortableRunDescriptor(raw)
	if err != nil {
		return nil, remoteportable.PortableRunInput{}, "", newCLIFailure("decode descriptor", err)
	}
	return raw, descriptor, repositoryRoot, nil
}

func bindPortableDescriptor(descriptor remoteportable.PortableRunInput, repositoryRoot string) (remoteportable.BoundPortableRun, error) {
	engineFacts, err := currentPortableEngineFacts()
	if err != nil {
		return remoteportable.BoundPortableRun{}, fmt.Errorf("compute engine facts: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return remoteportable.BindPortableRun(ctx, descriptor, gitObjectResolver{repositoryRoot: repositoryRoot}, engineFacts)
}

// parseDescriptorArgs 只接受显式 descriptor 与 repository root 路径。
func parseDescriptorArgs(args []string) (string, string, error) {
	if !validDescriptorArgumentShape(args) {
		return "", "", newCLIFailure("usage: "+usageText, nil)
	}
	descriptorPath, repositoryRoot := args[1], args[3]
	if err := validateDescriptorArgumentPath(descriptorPath); err != nil {
		return "", "", newCLIFailure("descriptor path is invalid", err)
	}
	if err := validateAbsolutePathComponents(repositoryRoot, true); err != nil {
		return "", "", newCLIFailure("repository path is invalid", err)
	}
	return descriptorPath, repositoryRoot, nil
}

func validDescriptorArgumentShape(args []string) bool {
	if len(args) != 4 || args[0] != "--descriptor" || args[2] != "--repository" {
		return false
	}
	return args[1] != "" && args[3] != ""
}

// validateDescriptorArgumentPath 校验 descriptor 的 clean absolute path 和控制字符。
func validateDescriptorArgumentPath(value string) error {
	if strings.TrimSpace(value) != value || filepath.Clean(value) != value || !filepath.IsAbs(value) {
		return errors.New("descriptor path must be a clean absolute path")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("descriptor path contains control characters")
		}
	}
	return nil
}

// readDescriptorFile 以 bounded read 打开 regular non-symlink descriptor 文件。
func readDescriptorFile(path string) ([]byte, error) {
	file, openedInfo, err := openDescriptorFile(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, descriptorMaxBytes+1))
	finalInfo, finalStatErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil {
		return nil, errors.New("descriptor file cannot be read")
	}
	if finalStatErr != nil || closeErr != nil {
		return nil, errors.New("descriptor file cannot be read")
	}
	if !sameFileIdentity(openedInfo, finalInfo) {
		return nil, errors.New("descriptor file identity changed during read")
	}
	if len(data) == 0 || len(data) > descriptorMaxBytes {
		return nil, errors.New("descriptor exceeds the bounded size")
	}
	return data, nil
}

// openDescriptorFile 以句柄 identity 核对方式打开 regular descriptor。
func openDescriptorFile(path string) (*os.File, os.FileInfo, error) {
	if err := validateAbsolutePathComponents(path, false); err != nil {
		return nil, nil, errors.New("descriptor path is not trusted")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, errors.New("descriptor file is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, errors.New("descriptor file must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("descriptor file cannot be opened")
	}
	openedInfo, err := file.Stat()
	if err != nil || !sameFileIdentity(info, openedInfo) {
		_ = file.Close()
		return nil, nil, errors.New("descriptor file identity changed before read")
	}
	return file, openedInfo, nil
}

func sameFileIdentity(left, right os.FileInfo) bool {
	if left == nil || right == nil {
		return false
	}
	leftID, leftErr := stableFileIdentity(left)
	rightID, rightErr := stableFileIdentity(right)
	return leftErr == nil && rightErr == nil && leftID == rightID
}

// boundedBindingJSON 只投影 descriptor 与 identity 摘要，拒绝把 binding 原样写到 stdout。
func boundedBindingJSON(descriptor []byte, binding remoteportable.BoundPortableRun, workloads []remoteportable.PortableManifestWorkload) ([]byte, error) {
	descriptorDigest := digestBytes(descriptor)
	catalogFacts, err := remoteportable.DerivePortableWorkloadCatalogFacts(workloads)
	if err != nil {
		return nil, err
	}
	result := remoteportable.PortableValidationResult{
		SchemaVersion: remoteportable.PortableValidationResultSchema, Status: remoteportable.PortableValidationResultStatusValidated,
		ContractID: binding.Manifest.ContractID, Authority: remoteportable.PortableValidationResultAuthority,
		ExecutionPoolContract: remoteportable.PortableManifestContractID, OperationID: binding.OperationID,
		RepositoryID: binding.RepositoryID, ObjectFormat: binding.ObjectFormat, TargetCommitSHA: binding.CommitSHA,
		TargetTreeSHA: binding.TreeSHA, ManifestPath: binding.ManifestPath, ManifestBlobSHA256: binding.ManifestBlobSHA256,
		ManifestDigest: binding.ManifestDigest, CatalogDigest: catalogFacts.CatalogDigest,
		WorkloadCount: catalogFacts.WorkloadCount, LinuxWorkloadCount: catalogFacts.LinuxWorkloadCount, WindowsWorkloadCount: catalogFacts.WindowsWorkloadCount,
		EngineFacts: binding.EngineFacts, DescriptorSHA256: descriptorDigest, BindingSHA256: binding.BindingSHA256,
	}
	if err := remoteportable.ValidatePortableWorkloadCatalogProof(result, workloads); err != nil {
		return nil, err
	}
	data, err := remoteportable.EncodePortableValidationResult(result)
	if err != nil {
		return nil, err
	}
	if len(data) > outputMaxBytes {
		return nil, errors.New("binding result exceeds the bounded size")
	}
	return data, nil
}

// digestBytes 生成 descriptor 原始字节的 bounded 输出摘要。
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// gitObjectResolver 通过 git object database 读取绑定 target tree，不读取当前工作树文件。
type gitObjectResolver struct {
	repositoryRoot string
}

// RepositoryFacts 返回 exact repository 的 semantic object format fact。
func (resolver gitObjectResolver) RepositoryFacts(ctx context.Context) (remoteportable.GitRepositoryFacts, error) {
	output, err := runGitObjectCommand(ctx, resolver.repositoryRoot, "rev-parse", "--show-object-format")
	if err != nil {
		return remoteportable.GitRepositoryFacts{}, errors.New("resolve repository object format failed")
	}
	objectFormat := strings.TrimSpace(string(output))
	if objectFormat != "sha1" && objectFormat != "sha256" {
		return remoteportable.GitRepositoryFacts{}, errors.New("repository object format is invalid")
	}
	return remoteportable.GitRepositoryFacts{ObjectFormat: objectFormat}, nil
}

// ResolveCommitTree 将显式 commit 解析为其 tree，并拒绝空或多行输出。
func (resolver gitObjectResolver) ResolveCommitTree(ctx context.Context, commitSHA string) (string, error) {
	output, err := runGitObjectCommand(ctx, resolver.repositoryRoot, "rev-parse", commitSHA+"^{tree}")
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
func (resolver gitObjectResolver) ReadTreeBlob(ctx context.Context, treeSHA, repoRelativePath string) (remoteportable.GitTreeBlob, error) {
	entry, err := runGitObjectCommand(ctx, resolver.repositoryRoot, "ls-tree", "-z", treeSHA, "--", repoRelativePath)
	if err != nil {
		return remoteportable.GitTreeBlob{}, errors.New("inspect target tree blob failed")
	}
	mode, objectType, err := parseGitTreeEntry(entry, repoRelativePath)
	if err != nil || objectType != "blob" || (mode != "100644" && mode != "100755") {
		return remoteportable.GitTreeBlob{}, errors.New("target tree manifest is not a regular blob")
	}
	output, err := runGitObjectCommand(ctx, resolver.repositoryRoot, "cat-file", "blob", treeSHA+":"+repoRelativePath)
	if err != nil {
		return remoteportable.GitTreeBlob{}, errors.New("read target tree blob failed")
	}
	return remoteportable.GitTreeBlob{Mode: mode, Data: output}, nil
}

// ListTree 返回 exact tree 的 regular blob 文件路径。
func (resolver gitObjectResolver) ListTree(ctx context.Context, treeSHA string) ([]remoteportable.GitTreeEntry, error) {
	output, err := runGitObjectCommand(ctx, resolver.repositoryRoot, "ls-tree", "-r", "-z", "--full-tree", treeSHA, "--")
	if err != nil {
		return nil, errors.New("list exact tree failed")
	}
	return remoteportable.ParseGitTreeEntries(output)
}

// ReadBlob 读取 exact tree 中的一个 regular blob。
func (resolver gitObjectResolver) ReadBlob(ctx context.Context, treeSHA, repoRelativePath string) ([]byte, error) {
	blob, err := resolver.ReadTreeBlob(ctx, treeSHA, repoRelativePath)
	if err != nil {
		return nil, err
	}
	return blob.Data, nil
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

const gitObjectOutputMaxBytes = 1 << 20

// runBoundedGitObjectCommand 执行受控只读 Git 查询并限制 stdout 字节数。
func runBoundedGitObjectCommand(ctx context.Context, repositoryRoot string, maxBytes int, args ...string) ([]byte, error) {
	if err := validateBoundedGitInputs(ctx, repositoryRoot, maxBytes, args); err != nil {
		return nil, err
	}
	identityBefore, err := repositoryRootIdentity(repositoryRoot)
	if err != nil {
		return nil, err
	}
	commandArgs := append([]string{"-C", repositoryRoot}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	if err := remoteportable.ConfigureBoundedCommand(command); err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	output, commandErr := readBoundedGitObjectCommand(command, stdout, maxBytes)
	if err := verifyRepositoryRootIdentity(repositoryRoot, identityBefore); err != nil {
		return nil, err
	}
	return output, commandErr
}

// validateBoundedGitInputs 校验受控 Git 命令的 context、root 和 output 上限。
func validateBoundedGitInputs(ctx context.Context, repositoryRoot string, maxBytes int, args []string) error {
	if ctx == nil || repositoryRoot == "" || maxBytes <= 0 || len(args) == 0 {
		return errors.New("bounded git object command inputs are incomplete")
	}
	return validateAbsolutePathComponents(repositoryRoot, true)
}

func verifyRepositoryRootIdentity(root, expected string) error {
	actual, err := repositoryRootIdentity(root)
	if err != nil || actual != expected {
		return errors.New("repository root identity changed during Git command")
	}
	return nil
}

// repositoryRootIdentity 计算 root、.git、gitdir 和 commondir 的内部稳定身份串。
func repositoryRootIdentity(root string) (string, error) {
	if err := validateAbsolutePathComponents(root, true); err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("repository root identity is unavailable")
	}
	rootID, err := stableFileIdentity(info)
	if err != nil {
		return "", err
	}
	storageID, err := repositoryStorageIdentity(root)
	if err != nil {
		return "", err
	}
	return rootID + "|" + storageID, nil
}

// repositoryStorageIdentity 核对 .git、linked gitdir 和实际 common-dir 的内部句柄身份。
func repositoryStorageIdentity(root string) (string, error) {
	gitPath := filepath.Join(root, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "no-git-directory", nil
		}
		return "", errors.New("Git storage identity is unavailable")
	}
	if info.IsDir() {
		return stableFileIdentity(info)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Git storage identity is invalid")
	}
	gitDir, gitFileID, err := linkedGitDirIdentity(root, gitPath, info)
	if err != nil {
		return "", err
	}
	commonDir, commonFileID, err := linkedGitCommonDirIdentity(gitDir)
	if err != nil {
		return "", err
	}
	commonInfo, err := os.Stat(commonDir)
	if err != nil || !commonInfo.IsDir() {
		return "", errors.New("linked Git common-dir identity is unavailable")
	}
	commonID, err := stableFileIdentity(commonInfo)
	if err != nil {
		return "", err
	}
	return gitFileID + "|" + gitDir + "|" + commonFileID + "|" + commonID, nil
}

// linkedGitDirIdentity 解析 .git gitdir 指针并冻结 linked worktree gitdir 身份。
func linkedGitDirIdentity(root, gitPath string, info os.FileInfo) (string, string, error) {
	data, err := readRegularBoundedFile(gitPath)
	if err != nil || !strings.HasPrefix(string(data), "gitdir: ") {
		return "", "", errors.New("linked Git storage identity is invalid")
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	gitDirInfo, err := os.Stat(gitDir)
	if err != nil || !gitDirInfo.IsDir() {
		return "", "", errors.New("linked Git dir identity is unavailable")
	}
	gitID, err := stableFileIdentity(info)
	if err != nil {
		return "", "", err
	}
	return gitDir, gitID, nil
}

// linkedGitCommonDirIdentity 解析 gitdir/commondir 并冻结 common-dir 文件与目录身份。
func linkedGitCommonDirIdentity(gitDir string) (string, string, error) {
	commondirPath := filepath.Join(gitDir, "commondir")
	info, err := os.Lstat(commondirPath)
	if os.IsNotExist(err) {
		return gitDir, "no-commondir-file", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", "", errors.New("linked Git commondir identity is invalid")
	}
	data, err := readRegularBoundedFile(commondirPath)
	if err != nil {
		return "", "", errors.New("linked Git commondir file is unreadable")
	}
	commonDir := strings.TrimSpace(string(data))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	fileID, err := stableFileIdentity(info)
	if err != nil {
		return "", "", err
	}
	return commonDir, fileID, nil
}

// readBoundedGitObjectCommand 读取有限 stdout，并在超限时终止和回收精确进程。
func readBoundedGitObjectCommand(command *exec.Cmd, stdout io.Reader, maxBytes int) ([]byte, error) {
	output, readErr := io.ReadAll(io.LimitReader(stdout, int64(maxBytes)+1))
	if len(output) > maxBytes {
		killErr := command.Cancel()
		waitErr := command.Wait()
		if killErr != nil || waitErr != nil {
			return nil, errors.New("bounded git object command termination failed")
		}
		return nil, errors.New("bounded git object command output exceeds limit")
	}
	waitErr := command.Wait()
	if readErr != nil || waitErr != nil {
		return nil, errors.New("bounded git object command failed")
	}
	return output, nil
}

// runGitObjectCommand 执行只读 git object 查询，不允许进入旧 gate binary。
func runGitObjectCommand(ctx context.Context, repositoryRoot string, args ...string) ([]byte, error) {
	return runBoundedGitObjectCommand(ctx, repositoryRoot, gitObjectOutputMaxBytes, args...)
}

// readRegularBoundedFile 读取 regular non-symlink 文件并限制内存占用。
func readRegularBoundedFile(path string) ([]byte, error) {
	file, openedInfo, err := openDescriptorFile(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, descriptorMaxBytes+1))
	finalInfo, finalStatErr := file.Stat()
	closeErr := file.Close()
	if err := validateStableBoundedRead(readErr, finalStatErr, closeErr, openedInfo, finalInfo); err != nil {
		return nil, err
	}
	if len(data) > descriptorMaxBytes {
		return nil, errors.New("bound file cannot be read within the size limit")
	}
	return data, nil
}

func validateStableBoundedRead(readErr, statErr, closeErr error, opened, final os.FileInfo) error {
	if readErr != nil || statErr != nil || closeErr != nil || !sameFileIdentity(opened, final) {
		return errors.New("bound file cannot be read within the size limit")
	}
	return nil
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
