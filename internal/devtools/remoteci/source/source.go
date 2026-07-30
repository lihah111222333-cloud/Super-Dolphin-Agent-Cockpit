// Package source builds and verifies content-addressed Git tree deltas for remote CI.
package source

import (
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
	"strings"
)

const (
	manifestVersion = 2
	PatchFormat     = "git-binary-v1"
)

// SourceSpec binds a target tree to the source tree embedded in the accepted CI image.
type SourceSpec struct {
	BaseCommit   string
	BaseTree     string
	TargetCommit string
	TargetTree   string
}

// Manifest records the exact base, target, and patch byte identities.
type Manifest struct {
	Version      int    `json:"version"`
	ObjectFormat string `json:"object_format"`
	PatchFormat  string `json:"patch_format"`
	BaseCommit   string `json:"base_commit"`
	BaseTree     string `json:"base_tree"`
	TargetCommit string `json:"target_commit,omitempty"`
	TargetTree   string `json:"target_tree"`
	PatchSHA256  string `json:"patch_sha256"`
	PatchSize    int64  `json:"patch_size"`
}

// Artifact names the binary-safe patch and manifest created for a source specification.
type Artifact struct {
	PatchPath    string
	ManifestPath string
	Manifest     Manifest
}

// Build 从精确 Git 树导出完整索引二进制补丁，并原子发布其绑定清单。
func Build(ctx context.Context, repositoryRoot string, spec SourceSpec, destination string) (Artifact, error) {
	repo, objectFormat, err := prepareBuild(ctx, repositoryRoot, spec, destination)
	if err != nil {
		return Artifact{}, err
	}
	patchPath, manifestPath, temporaryPatch, err := buildPatch(ctx, repo, spec, destination)
	if err != nil {
		return Artifact{}, err
	}
	defer os.Remove(temporaryPatch)
	manifest, err := manifestForPatch(objectFormat, spec, temporaryPatch)
	if err != nil {
		return Artifact{}, err
	}
	if err := publishArtifact(destination, patchPath, manifestPath, temporaryPatch, manifest); err != nil {
		return Artifact{}, err
	}
	return Artifact{PatchPath: patchPath, ManifestPath: manifestPath, Manifest: manifest}, nil
}

// Verify 在干净基线仓库应用补丁，并发布可复核的 detached materialized/base refs。
func Verify(ctx context.Context, manifestPath string, patchPath string, verificationRoot string) (Manifest, error) {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	if err := verifyPatchIdentity(patchPath, manifest); err != nil {
		return Manifest{}, err
	}
	if err := validateBaseRepository(ctx, verificationRoot, manifest); err != nil {
		return Manifest{}, err
	}
	if err := materializeSource(ctx, verificationRoot, patchPath, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// prepareBuild 解析源仓库并验证待导出的 Git 身份及目标目录。
func prepareBuild(ctx context.Context, repositoryRoot string, spec SourceSpec, destination string) (string, string, error) {
	repo, err := resolveRepositoryRoot(ctx, repositoryRoot)
	if err != nil {
		return "", "", err
	}
	if err := ensureDirectory(destination); err != nil {
		return "", "", err
	}
	objectFormat, objectIDLength, err := repositoryObjectFormat(ctx, repo)
	if err != nil {
		return "", "", err
	}
	if err := validateSourceSpec(ctx, repo, spec, objectIDLength); err != nil {
		return "", "", err
	}
	return repo, objectFormat, nil
}

// buildPatch 为已经验证的源规范生成尚未发布的二进制补丁。
func buildPatch(ctx context.Context, repo string, spec SourceSpec, destination string) (string, string, string, error) {
	patchPath := filepath.Join(destination, spec.TargetTree+".patch")
	manifestPath := filepath.Join(destination, spec.TargetTree+".manifest.json")
	if err := ensureAbsent(patchPath); err != nil {
		return "", "", "", err
	}
	if err := ensureAbsent(manifestPath); err != nil {
		return "", "", "", err
	}
	temporaryPatch, err := temporaryPath(destination, ".source-delta-*")
	if err != nil {
		return "", "", "", err
	}
	if err := gitWriteFile(ctx, repo, temporaryPatch,
		"diff", "--binary", "--full-index", "--no-renames", "--no-color", "--no-ext-diff",
		spec.BaseTree, spec.TargetTree, "--"); err != nil {
		return "", "", "", errors.Join(err, os.Remove(temporaryPatch))
	}
	return patchPath, manifestPath, temporaryPatch, nil
}

// manifestForPatch 为临时补丁计算严格的字节身份并构造校验后的清单。
func manifestForPatch(objectFormat string, spec SourceSpec, patchPath string) (Manifest, error) {
	digest, size, err := fileSHA256(patchPath)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Version: manifestVersion, ObjectFormat: objectFormat, PatchFormat: PatchFormat,
		BaseCommit: spec.BaseCommit, BaseTree: spec.BaseTree,
		TargetCommit: spec.TargetCommit, TargetTree: spec.TargetTree,
		PatchSHA256: digest, PatchSize: size,
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// publishArtifact 原子发布补丁及其已经验证身份的清单。
func publishArtifact(destination string, patchPath string, manifestPath string, temporaryPatch string, manifest Manifest) error {
	temporaryManifest, err := temporaryPath(destination, ".source-manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporaryManifest)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode source delta manifest: %w", err)
	}
	if err := os.WriteFile(temporaryManifest, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write source delta manifest: %w", err)
	}
	if err := os.Rename(temporaryPatch, patchPath); err != nil {
		return fmt.Errorf("publish source delta: %w", err)
	}
	if err := os.Rename(temporaryManifest, manifestPath); err != nil {
		return fmt.Errorf("publish source delta manifest: %w", err)
	}
	return nil
}

// verifyPatchIdentity 校验补丁文件的 SHA-256 和大小与清单完全一致。
func verifyPatchIdentity(patchPath string, manifest Manifest) error {
	digest, size, err := fileSHA256(patchPath)
	if err != nil {
		return err
	}
	if digest != manifest.PatchSHA256 || size != manifest.PatchSize {
		return errors.New("source patch bytes do not match manifest")
	}
	return nil
}

// materializeSource 在已验证的基线仓库应用补丁并发布可复核引用。
func materializeSource(ctx context.Context, root string, patchPath string, manifest Manifest) error {
	baseCommit, err := gitOutput(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if manifest.PatchSize > 0 {
		if err := gitRun(ctx, root, "apply", "--binary", "--index", "--whitespace=nowarn", patchPath); err != nil {
			return fmt.Errorf("apply source patch: %w", err)
		}
	}
	targetTree, err := gitOutput(ctx, root, "write-tree")
	if err != nil {
		return err
	}
	if targetTree != manifest.TargetTree {
		return fmt.Errorf("materialized tree %q does not match target tree %q", targetTree, manifest.TargetTree)
	}
	targetCommit, err := commitMaterializedTree(ctx, root, targetTree, baseCommit)
	if err != nil {
		return err
	}
	if err := publishMaterializedRefs(ctx, root, baseCommit, targetCommit); err != nil {
		return err
	}
	return verifyMaterializedRepository(ctx, root, targetCommit)
}

// publishMaterializedRefs 记录基线和物化提交的稳定引用。
func publishMaterializedRefs(ctx context.Context, root string, baseCommit string, targetCommit string) error {
	if err := gitRun(ctx, root, "update-ref", "refs/source/base", baseCommit); err != nil {
		return err
	}
	return gitRun(ctx, root, "update-ref", "refs/source/materialized", targetCommit)
}

// verifyMaterializedRepository 切换至物化提交并要求工作区保持清洁。
func verifyMaterializedRepository(ctx context.Context, root string, targetCommit string) error {
	if err := gitRun(ctx, root, "checkout", "--quiet", "--detach", targetCommit); err != nil {
		return err
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return errors.New("materialized source repository is not clean")
	}
	return nil
}

// LoadManifest 严格解码并校验源增量清单，拒绝未知字段与尾随数据。
func LoadManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open source delta manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode source delta manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("source delta manifest has trailing JSON payload")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// validateBaseRepository 校验镜像内基线仓库的树身份和工作区清洁状态。
func validateBaseRepository(ctx context.Context, root string, manifest Manifest) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat base source repository: %w", err)
	}
	if !info.IsDir() {
		return errors.New("base source repository must be a directory")
	}
	tree, err := gitOutput(ctx, root, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return fmt.Errorf("resolve base source tree: %w", err)
	}
	if tree != manifest.BaseTree {
		return fmt.Errorf("image base tree %q does not match manifest base tree %q", tree, manifest.BaseTree)
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("verify base source status: %w", err)
	}
	if status != "" {
		return errors.New("image base source repository is not clean")
	}
	return nil
}

// validateSourceSpec 验证提交与树对象在同一仓库中的精确绑定关系。
func validateSourceSpec(ctx context.Context, repo string, spec SourceSpec, objectIDLength int) error {
	if err := validateSourceIdentity(spec, objectIDLength); err != nil {
		return err
	}
	if err := validateCommitTree(ctx, repo, "base", spec.BaseCommit, spec.BaseTree); err != nil {
		return err
	}
	if spec.TargetCommit == "" {
		return validateTreeObject(ctx, repo, spec.TargetTree)
	}
	return validateCommitTree(ctx, repo, "target", spec.TargetCommit, spec.TargetTree)
}

// validateManifest 校验源增量清单的格式、对象身份和补丁边界。
func validateManifest(manifest Manifest) error {
	objectIDLength, err := manifestObjectIDLength(manifest)
	if err != nil {
		return err
	}
	if err := validateSourceIdentity(manifest.sourceSpec(), objectIDLength); err != nil {
		return err
	}
	if err := validateSHA256(manifest.PatchSHA256); err != nil {
		return err
	}
	if manifest.PatchSize < 0 || manifest.PatchSize > 1<<30 {
		return errors.New("source delta patch_size is invalid")
	}
	return nil
}

// sourceSpec 返回清单中受对象格式约束的 Git 身份字段。
func (manifest Manifest) sourceSpec() SourceSpec {
	return SourceSpec{
		BaseCommit: manifest.BaseCommit, BaseTree: manifest.BaseTree,
		TargetCommit: manifest.TargetCommit, TargetTree: manifest.TargetTree,
	}
}

// manifestObjectIDLength 校验清单版本和格式，并返回对象 ID 的固定长度。
func manifestObjectIDLength(manifest Manifest) (int, error) {
	if manifest.Version != manifestVersion {
		return 0, fmt.Errorf("source delta manifest version must equal %d", manifestVersion)
	}
	if manifest.PatchFormat != PatchFormat {
		return 0, fmt.Errorf("source delta patch_format must equal %q", PatchFormat)
	}
	switch manifest.ObjectFormat {
	case "sha1":
		return 40, nil
	case "sha256":
		return 64, nil
	default:
		return 0, fmt.Errorf("source delta object format %q is not supported", manifest.ObjectFormat)
	}
}

// validateSourceIdentity 校验源规范中全部 Git 对象 ID 的字面身份。
func validateSourceIdentity(spec SourceSpec, objectIDLength int) error {
	identities := []struct {
		field string
		value string
	}{
		{field: "base_commit", value: spec.BaseCommit},
		{field: "base_tree", value: spec.BaseTree},
		{field: "target_tree", value: spec.TargetTree},
	}
	for _, identity := range identities {
		if err := validateObjectID(identity.field, identity.value, objectIDLength); err != nil {
			return err
		}
	}
	if spec.TargetCommit == "" {
		return nil
	}
	return validateObjectID("target_commit", spec.TargetCommit, objectIDLength)
}

// validateCommitTree 要求提交解析出的树与声明树完全一致。
func validateCommitTree(ctx context.Context, repo string, kind string, commit string, tree string) error {
	resolvedTree, err := gitOutput(ctx, repo, "rev-parse", "--verify", commit+"^{tree}")
	if err != nil {
		return err
	}
	if resolvedTree != tree {
		return fmt.Errorf("%s tree does not match %s commit", kind, kind)
	}
	return nil
}

// validateTreeObject 要求声明的无提交目标树确实存在于源仓库。
func validateTreeObject(ctx context.Context, repo string, tree string) error {
	_, err := gitOutput(ctx, repo, "cat-file", "-e", tree+"^{tree}")
	return err
}

// repositoryObjectFormat 读取仓库对象格式及其严格对象 ID 长度。
func repositoryObjectFormat(ctx context.Context, repo string) (string, int, error) {
	objectFormat, err := gitOutput(ctx, repo, "rev-parse", "--show-object-format")
	if err != nil {
		return "", 0, err
	}
	switch objectFormat {
	case "sha1":
		return objectFormat, 40, nil
	case "sha256":
		return objectFormat, 64, nil
	default:
		return "", 0, fmt.Errorf("unsupported Git object format %q", objectFormat)
	}
}

// commitMaterializedTree 为物化树创建具有固定身份信息的提交。
func commitMaterializedTree(ctx context.Context, repo string, tree string, parent string) (string, error) {
	commandArgs := []string{"commit-tree", tree, "-p", parent, "-m", "materialized remote CI source"}
	command := gitExecCommand(ctx, repo, commandArgs...)
	command.Env = append(command.Env,
		"GIT_AUTHOR_NAME=Super Dolphin CI", "GIT_AUTHOR_EMAIL=ci@super-dolphin.invalid",
		"GIT_COMMITTER_NAME=Super Dolphin CI", "GIT_COMMITTER_EMAIL=ci@super-dolphin.invalid",
		"GIT_AUTHOR_DATE=@0 +0000", "GIT_COMMITTER_DATE=@0 +0000",
	)
	output, err := command.Output()
	if err != nil {
		return "", gitCommandError(commandArgs, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// resolveRepositoryRoot 解析路径并要求其恰为 Git 顶层目录。
func resolveRepositoryRoot(ctx context.Context, repositoryRoot string) (string, error) {
	if strings.TrimSpace(repositoryRoot) == "" {
		return "", errors.New("repository root must not be empty")
	}
	requested, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	requested, err = filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	actual, err := gitOutput(ctx, requested, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	actual, err = filepath.EvalSymlinks(actual)
	if err != nil {
		return "", fmt.Errorf("resolve Git repository root: %w", err)
	}
	if actual != requested {
		return "", errors.New("repository root must name the Git top-level directory")
	}
	return requested, nil
}

// validateObjectID 校验小写十六进制 Git 对象 ID 的长度与字符集。
func validateObjectID(field string, value string, expectedLength int) error {
	if len(value) != expectedLength {
		return fmt.Errorf("%s must be a %d-character lowercase object ID", field, expectedLength)
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return fmt.Errorf("%s must be a %d-character lowercase object ID", field, expectedLength)
		}
	}
	return nil
}

// validateSHA256 校验补丁 SHA-256 摘要的固定格式。
func validateSHA256(value string) error {
	if len(value) != sha256.Size*2 {
		return errors.New("source delta patch_sha256 must be a lowercase SHA-256 digest")
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return errors.New("source delta patch_sha256 must be a lowercase SHA-256 digest")
		}
	}
	return nil
}

// ensureDirectory 要求路径存在且为目录。
func ensureDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat destination directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("destination must be a directory")
	}
	return nil
}

// ensureAbsent 要求发布目标尚未存在，避免覆盖既有产物。
func ensureAbsent(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat path: %w", err)
	}
	return fmt.Errorf("path already exists: %s", path)
}

// temporaryPath 在目标目录预留一个尚未存在的临时文件路径。
func temporaryPath(directory string, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", fmt.Errorf("create temporary source artifact: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close temporary source artifact: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("prepare temporary source artifact: %w", err)
	}
	return path, nil
}

// fileSHA256 计算文件内容的 SHA-256 摘要和准确字节数。
func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open source delta: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, fmt.Errorf("hash source delta: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// gitWriteFile 将 Git 子命令的标准输出以独占方式写入文件。
func gitWriteFile(ctx context.Context, repo string, path string, args ...string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source delta: %w", err)
	}
	command := gitExecCommand(ctx, repo, args...)
	command.Stdout = file
	runErr := command.Run()
	closeErr := file.Close()
	if runErr != nil {
		return errors.Join(gitCommandError(args, runErr), closeErr)
	}
	return closeErr
}

// gitOutput 执行 Git 子命令并返回去除首尾空白的标准输出。
func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	command := gitExecCommand(ctx, repo, args...)
	output, err := command.Output()
	if err != nil {
		return "", gitCommandError(args, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// gitRun 执行不需要标准输出的 Git 子命令。
func gitRun(ctx context.Context, repo string, args ...string) error {
	command := gitExecCommand(ctx, repo, args...)
	if err := command.Run(); err != nil {
		return gitCommandError(args, err)
	}
	return nil
}

// gitExecCommand 构造禁用交互式凭据提示的 Git 命令。
func gitExecCommand(ctx context.Context, repo string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-c", "credential.interactive=never", "-C", repo}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	return command
}

// gitCommandError 保留 Git 子命令和退出错误的失败上下文。
func gitCommandError(args []string, err error) error {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(exitError.Stderr)))
	}
	return fmt.Errorf("git %s: %w", args[0], err)
}
