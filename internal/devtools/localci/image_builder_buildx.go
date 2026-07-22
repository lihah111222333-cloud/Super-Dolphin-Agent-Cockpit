package localci

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	buildxMetadataLimit       = 16 << 20
	buildxManifestMedia       = "application/vnd.docker.distribution.manifest.v2+json"
	buildxConfigMedia         = "application/vnd.docker.container.image.v1+json"
	buildxLayerMedia          = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	candidateImageRepository  = "docker.io/library/super-dolphin-gate-local"
	candidateImageTagPrefix   = "candidate-"
	buildxBuilderNamePrefix   = "super-dolphin-gate-"
	buildxBuilderCPUQuota     = "400000"
	buildxBuilderCPUPeriod    = "100000"
	buildxBuilderMemory       = "8g"
	buildxBuilderMemoryBytes  = "8589934592"
	buildxBuilderPidsLimit    = "512"
	buildxBuilderCleanupLimit = 30 * time.Second
)

var forbiddenBuildArgumentNames = map[string]struct{}{
	"ALL_PROXY": {}, "HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
}

var controlledBuildxBuilderNamePattern = regexp.MustCompile(`^super-dolphin-gate-[0-9a-f]{12}-candidate-[a-z0-9]+$`)

var legacyControlledBuildxBuilderNamePattern = regexp.MustCompile(`^super-dolphin-gate-candidate-[0-9]+$`)

var buildxHistoryNodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

var buildxHistoryRecordReferencePattern = regexp.MustCompile(`^[a-z0-9]{25}$`)

type buildxCommandExecutor interface {
	Run(ctx context.Context, stdin io.Reader, args ...string) (string, error)
}

// DockerBuildxRunner 将已验证的 BuildKit 请求适配为受限 docker buildx build。
type DockerBuildxRunner struct {
	executor  buildxCommandExecutor
	root      string
	workRoot  string
	cacheRoot string
	ownerRoot string
}

type controlledBuildxOwner struct {
	BuilderName   string `json:"builder_name"`
	SourceTreeSHA string `json:"source_tree_sha"`
	InputDigest   string `json:"input_digest"`
}

type execBuildxCommandExecutor struct{}

// NewDockerBuildxRunner 固定私有工作根和隔离 cache 根。
func NewDockerBuildxRunner(trustedRoot string) (*DockerBuildxRunner, error) {
	return newDockerBuildxRunner(execBuildxCommandExecutor{}, trustedRoot)
}

// CandidateImageReference 将受信 manifest digest 绑定到固定本地 repository。
func CandidateImageReference(manifestDigest string) (string, error) {
	if err := validateDigest("candidate platform manifest digest", manifestDigest); err != nil {
		return "", err
	}
	return candidateImageRepository + "@" + manifestDigest, nil
}

func newDockerBuildxRunner(executor buildxCommandExecutor, trustedRoot string) (*DockerBuildxRunner, error) {
	if buildxCommandExecutorIsNil(executor) {
		return nil, errors.New("buildx command executor is required")
	}
	root, err := validateBuildxRoot(trustedRoot)
	if err != nil {
		return nil, err
	}
	workRoot := filepath.Join(root, "work")
	cacheRoot := filepath.Join(root, "cache")
	ownerRoot := filepath.Join(root, "buildx-owners")
	if err := makePrivateDirectories(workRoot, cacheRoot, ownerRoot); err != nil {
		return nil, err
	}
	return &DockerBuildxRunner{executor: executor, root: root, workRoot: workRoot, cacheRoot: cacheRoot, ownerRoot: ownerRoot}, nil
}

// Run 执行 Docker CLI，并在成功时只返回单一 config digest 输出。
func (execBuildxCommandExecutor) Run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	if ctx == nil {
		return "", errors.New("buildx command context is required")
	}
	if stdin == nil {
		return "", errors.New("buildx command stdin is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(args) == 0 {
		return "", errors.New("docker command arguments are required")
	}
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stdin = stdin
	command.Env = sanitizedBuildxEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		subject := strings.Join(args[:min(len(args), 2)], " ")
		return "", fmt.Errorf("docker %s: %w: %s", subject, err, strings.TrimSpace(string(output)))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(output), nil
}

// Build 执行一次隔离候选构建，并从严格绑定的 metadata 返回 platform manifest digest。
func (runner *DockerBuildxRunner) Build(ctx context.Context, request BuildKitBuildRequest) (result BuildKitResult, err error) {
	if err := runner.validateBuild(ctx, request); err != nil {
		return BuildKitResult{}, err
	}
	candidateTag, useCache, err := runner.prepareBuildxExecution(request)
	if err != nil {
		return BuildKitResult{}, err
	}
	workspace, err := runner.createPrivateBuildxWorkspace()
	if err != nil {
		return BuildKitResult{}, err
	}
	defer runner.cleanupPrivateBuildxWorkspace(workspace, &result, &err)
	if err := validateBuildxDockerfileDirectives(request.ContextTar, request.DockerfilePath); err != nil {
		return BuildKitResult{}, err
	}
	builderName, err := runner.createControlledBuilder(ctx, request, workspace)
	if err != nil {
		return BuildKitResult{}, err
	}
	defer runner.cleanupControlledBuilder(ctx, builderName, &result, &err)
	return runner.buildCandidate(ctx, request, workspace, candidateTag, useCache, builderName)
}

// createPrivateBuildxWorkspace 创建仅当前构建可访问的临时工作区。
func (runner *DockerBuildxRunner) createPrivateBuildxWorkspace() (string, error) {
	return createPrivateBuildxWorkspace(runner.workRoot, os.MkdirTemp, os.Chmod, os.RemoveAll)
}

// createPrivateBuildxWorkspace 在权限保护失败时同步回收已创建的工作区。
func createPrivateBuildxWorkspace(
	workRoot string,
	mkdirTemp func(string, string) (string, error),
	chmod func(string, os.FileMode) error,
	removeAll func(string) error,
) (string, error) {
	workspace, err := mkdirTemp(workRoot, "candidate-")
	if err != nil {
		return "", fmt.Errorf("create private buildx workspace: %w", err)
	}
	if err := chmod(workspace, 0o700); err != nil {
		chmodErr := fmt.Errorf("protect buildx workspace: %w", err)
		if cleanupErr := removeAll(workspace); cleanupErr != nil {
			return "", errors.Join(chmodErr, fmt.Errorf("remove rejected private buildx workspace: %w", cleanupErr))
		}
		return "", chmodErr
	}
	return workspace, nil
}

// cleanupPrivateBuildxWorkspace 独立回收候选工作区，并把回收失败并入构建结果。
func (runner *DockerBuildxRunner) cleanupPrivateBuildxWorkspace(workspace string, result *BuildKitResult, buildErr *error) {
	if cleanupErr := os.RemoveAll(workspace); cleanupErr != nil {
		*result = BuildKitResult{}
		*buildErr = errors.Join(*buildErr, fmt.Errorf("remove private buildx workspace: %w", cleanupErr))
	}
}

// cleanupControlledBuilder 独立回收受控 builder，并把回收失败并入构建结果。
func (runner *DockerBuildxRunner) cleanupControlledBuilder(ctx context.Context, builderName string, result *BuildKitResult, buildErr *error) {
	if cleanupErr := runner.removeControlledBuilder(ctx, builderName); cleanupErr != nil {
		*result = BuildKitResult{}
		*buildErr = errors.Join(*buildErr, fmt.Errorf("remove controlled buildx builder: %w", cleanupErr))
	}
}

// buildCandidate 执行候选构建并验证严格绑定的 metadata。
func (runner *DockerBuildxRunner) buildCandidate(ctx context.Context, request BuildKitBuildRequest, workspace string, candidateTag string, useCache bool, builderName string) (BuildKitResult, error) {
	metadataPath := filepath.Join(workspace, "metadata.json")
	output, err := runner.executor.Run(ctx, bytes.NewReader(request.ContextTar), runner.commandArgs(request, metadataPath, candidateTag, useCache, builderName)...)
	if err != nil {
		return BuildKitResult{}, fmt.Errorf("run candidate buildx command: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return BuildKitResult{}, err
	}
	imageDigest, err := parseBuildxDigestOutput(output)
	if err != nil {
		return BuildKitResult{}, err
	}
	metadataData, err := readBuildxMetadataFile(metadataPath)
	if err != nil {
		return BuildKitResult{}, err
	}
	metadata, err := validateBuildxMetadata(metadataData, request, imageDigest, builderName)
	if err != nil {
		return BuildKitResult{}, err
	}
	configDigest, err := runner.resolveBuildxConfigDigest(ctx, builderName, metadata)
	if err != nil {
		return BuildKitResult{}, err
	}
	return BuildKitResult{PlatformManifestDigest: metadata.ImageDigest, ConfigDigest: configDigest}, nil
}

// createControlledBuilder 将每次候选构建绑定到新建且受资源限制的 BuildKit 容器。
func (runner *DockerBuildxRunner) createControlledBuilder(ctx context.Context, request BuildKitBuildRequest, workspace string) (builderName string, err error) {
	builderName = buildxBuilderName(request.SourceTreeSHA, filepath.Base(workspace))
	if err := runner.recordControlledBuilder(builderName, request); err != nil {
		return "", err
	}
	createArgs := []string{
		"buildx", "create", "--name", builderName, "--driver", "docker-container",
		"--driver-opt=image=" + request.BuildKitImage,
		"--driver-opt=cpu-quota=" + buildxBuilderCPUQuota,
		"--driver-opt=cpu-period=" + buildxBuilderCPUPeriod,
		"--driver-opt=memory=" + buildxBuilderMemory,
	}
	if _, err := runner.executor.Run(ctx, bytes.NewReader(nil), createArgs...); err != nil {
		createErr := fmt.Errorf("create controlled buildx builder: %w", err)
		if cleanupErr := runner.removeControlledBuilder(ctx, builderName); cleanupErr != nil {
			return "", errors.Join(createErr, fmt.Errorf("remove possibly created controlled buildx builder: %w", cleanupErr))
		}
		return "", createErr
	}
	controlledBuilderName := builderName
	created := true
	defer func() {
		if created && err != nil {
			if cleanupErr := runner.removeControlledBuilder(ctx, controlledBuilderName); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove rejected controlled buildx builder: %w", cleanupErr))
			}
		}
	}()
	inspectOutput, err := runner.executor.Run(ctx, bytes.NewReader(nil), "buildx", "inspect", "--builder", builderName, "--bootstrap")
	if err != nil {
		return "", fmt.Errorf("inspect controlled buildx builder: %w", err)
	}
	if err := runner.configureControlledBuilder(ctx, inspectOutput, request.BuildKitVersion, builderName); err != nil {
		return "", err
	}
	if err := runner.validateControlledBuilderResources(ctx, request, builderName); err != nil {
		return "", err
	}
	return builderName, nil
}

func (runner *DockerBuildxRunner) configureControlledBuilder(ctx context.Context, inspectOutput string, buildKitVersion string, builderName string) error {
	if err := validateBuildxInspectVersion(inspectOutput, buildKitVersion); err != nil {
		return err
	}
	return runner.updateControlledBuilderPidsLimit(ctx, builderName)
}

// updateControlledBuilderPidsLimit 对 bootstrap 后的固定 BuildKit 容器施加进程数上限。
func (runner *DockerBuildxRunner) updateControlledBuilderPidsLimit(ctx context.Context, builderName string) error {
	if _, err := runner.executor.Run(ctx, bytes.NewReader(nil), "container", "update", "--pids-limit", buildxBuilderPidsLimit, controlledBuildxContainerName(builderName)); err != nil {
		return fmt.Errorf("update controlled buildx builder PIDs limit: %w", err)
	}
	return nil
}

// validateControlledBuilderResources 验证容器镜像、资源限制和不可变镜像身份一致。
func (runner *DockerBuildxRunner) validateControlledBuilderResources(ctx context.Context, request BuildKitBuildRequest, builderName string) error {
	containerOutput, err := runner.executor.Run(ctx, bytes.NewReader(nil), "container", "inspect", "--format", "{{.Config.Image}}\n{{.Image}}\n{{.HostConfig.CpuQuota}}/{{.HostConfig.CpuPeriod}}/{{.HostConfig.Memory}}/{{.HostConfig.PidsLimit}}", controlledBuildxContainerName(builderName))
	if err != nil {
		return fmt.Errorf("inspect controlled buildx builder container: %w", err)
	}
	imageID, err := validateBuildxContainerIdentity(containerOutput, request.BuildKitImage)
	if err != nil {
		return err
	}
	imageOutput, err := runner.executor.Run(ctx, bytes.NewReader(nil), "image", "inspect", "--format", "{{.Id}}\n{{range .RepoDigests}}{{println .}}{{end}}", request.BuildKitImage)
	if err != nil {
		return fmt.Errorf("inspect controlled buildx builder image: %w", err)
	}
	if err := validateBuildxImageIdentity(imageOutput, request.BuildKitImage, imageID); err != nil {
		return err
	}
	return nil
}

// prepareBuildxExecution 固定 candidate tag 并校验可选 cache source。
func (runner *DockerBuildxRunner) prepareBuildxExecution(request BuildKitBuildRequest) (string, bool, error) {
	useCache, err := runner.cacheAvailable(request.CacheNamespace)
	if err != nil {
		return "", false, err
	}
	candidateTag, err := candidateImageTag(request.InputDigest)
	if err != nil {
		return "", false, err
	}
	return candidateTag, useCache, nil
}

// validateBuild 在创建临时目录或调用 Docker 前完成全部入口校验。
func (runner *DockerBuildxRunner) validateBuild(ctx context.Context, request BuildKitBuildRequest) error {
	if runner == nil {
		return errors.New("docker buildx runner is required")
	}
	if buildxCommandExecutorIsNil(runner.executor) {
		return errors.New("buildx command executor is required")
	}
	if ctx == nil {
		return errors.New("candidate buildx context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return validateBuildxRequest(request)
}

func (runner *DockerBuildxRunner) commandArgs(request BuildKitBuildRequest, metadataPath string, candidateTag string, useCache bool, builderName string) []string {
	cachePath := filepath.Join(runner.cacheRoot, strings.TrimPrefix(request.CacheNamespace, "sha256:"))
	args := []string{
		"buildx", "build", "--builder=" + builderName, "--output=type=docker,oci-mediatypes=false", "--progress=quiet", "--provenance=false",
		"--platform=" + request.Platform,
		"--file=" + request.DockerfilePath,
		"--network=none",
		"--tag=" + candidateTag,
		"--metadata-file=" + metadataPath,
	}
	if useCache {
		args = append(args, "--cache-from=type=local,src="+cachePath)
	}
	args = append(args, "--cache-to=type=local,dest="+cachePath+",mode=min")
	for _, argument := range request.BuildArguments {
		args = append(args, "--build-arg="+argument.Name+"="+argument.Value)
	}
	for _, label := range sortedBuildxBindingLabels(request) {
		args = append(args, "--label="+label)
	}
	return append(args, "-")
}

// cacheAvailable 校验已有 namespace 不逃逸，并报告是否可作为 cache source。
func (runner *DockerBuildxRunner) cacheAvailable(namespace string) (bool, error) {
	cachePath := filepath.Join(runner.cacheRoot, strings.TrimPrefix(namespace, "sha256:"))
	info, err := os.Lstat(cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect buildx cache namespace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("buildx cache namespace must be a real directory")
	}
	resolved, err := filepath.EvalSymlinks(cachePath)
	if err != nil {
		return false, fmt.Errorf("resolve buildx cache namespace: %w", err)
	}
	if resolved != cachePath {
		return false, errors.New("buildx cache namespace escapes the private cache root")
	}
	if err := validateBuildxCacheLayout(cachePath); err != nil {
		recovered, recoveryErr := recoverEmptyBuildxCacheNamespace(cachePath)
		if recoveryErr != nil {
			return false, errors.Join(err, recoveryErr)
		}
		if recovered {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// recoverEmptyBuildxCacheNamespace 仅回收已通过路径约束且为空的中断导出目录。
func recoverEmptyBuildxCacheNamespace(cachePath string) (bool, error) {
	entries, err := os.ReadDir(cachePath)
	if err != nil {
		return false, fmt.Errorf("inspect incomplete buildx cache namespace: %w", err)
	}
	if len(entries) != 0 {
		return false, nil
	}
	if err := os.Remove(cachePath); err != nil {
		return false, fmt.Errorf("remove empty buildx cache namespace: %w", err)
	}
	return true, nil
}

// validateBuildxCacheLayout 只把完整 local OCI cache 作为 cache source。
func validateBuildxCacheLayout(cachePath string) error {
	files := []string{filepath.Join(cachePath, "index.json"), filepath.Join(cachePath, "oci-layout")}
	for _, filePath := range files {
		info, err := os.Lstat(filePath)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("buildx cache namespace is missing a regular OCI metadata file")
		}
	}
	blobsPath := filepath.Join(cachePath, "blobs", "sha256")
	info, err := os.Lstat(blobsPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("buildx cache namespace is missing a real sha256 blobs directory")
	}
	return nil
}

func candidateImageTag(inputDigest string) (string, error) {
	if err := validateDigest("candidate image input digest", inputDigest); err != nil {
		return "", err
	}
	return candidateImageRepository + ":" + candidateImageTagPrefix + strings.TrimPrefix(inputDigest, "sha256:"), nil
}

// validateBuildxRequest 复验 BuildKit 请求的摘要、路径、平台和锁定参数。
func validateBuildxRequest(request BuildKitBuildRequest) error {
	if err := validateBuildxRequestIdentity(request); err != nil {
		return err
	}
	if err := validateBuildxDigests(request); err != nil {
		return err
	}
	if err := validateBuildxDockerfilePath(request.DockerfilePath); err != nil {
		return err
	}
	if err := validateBuildKitVersion(request.BuildKitVersion); err != nil {
		return err
	}
	if err := validateBuildKitImageReference(request.BuildKitImage); err != nil {
		return err
	}
	if _, err := parseBuildxPlatform(request.Platform); err != nil {
		return err
	}
	return validateBuildxArguments(request.BuildArguments)
}

// validateBuildKitVersion 验证锁定版本是可精确比较的 BuildKit 版本字符串。
func validateBuildKitVersion(version string) error {
	if !strings.HasPrefix(version, "v") {
		return errors.New("buildx BuildKit version must be a canonical v-prefixed version")
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return errors.New("buildx BuildKit version must contain major, minor, and patch components")
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return errors.New("buildx BuildKit version must contain only decimal components")
		}
	}
	return nil
}

func validateBuildKitImageReference(reference string) error {
	const prefix = "docker.io/moby/buildkit@"
	if !strings.HasPrefix(reference, prefix) {
		return errors.New("buildx BuildKit image must use the canonical docker.io/moby/buildkit repository")
	}
	if err := validateDigest("buildx BuildKit image digest", strings.TrimPrefix(reference, prefix)); err != nil {
		return err
	}
	return nil
}

// validateBuildxRequestIdentity 校验无法由单一 digest validator 表达的请求绑定。
func validateBuildxRequestIdentity(request BuildKitBuildRequest) error {
	if !gitObjectPattern.MatchString(request.SourceTreeSHA) {
		return errors.New("buildx source tree must be a canonical Git object ID")
	}
	if err := validateBuildxPolicyBinding(request); err != nil {
		return err
	}
	if len(request.ContextTar) == 0 {
		return errors.New("buildx context tar is required")
	}
	if bytesDigest(request.ContextTar) != request.ContextDigest {
		return errors.New("buildx context tar does not match its digest")
	}
	if request.CacheNamespace != request.InputDigest {
		return errors.New("buildx cache namespace must equal the image input digest")
	}
	if request.NetworkPolicy != "none" && request.NetworkPolicy != "locked-dependencies" {
		return errors.New("buildx network policy is not permitted")
	}
	if request.BuildKitVersion == "" {
		return errors.New("buildx BuildKit version is required")
	}
	if request.DockerfileFrontend != "builtin:dockerfile.v1" {
		return errors.New("buildx Dockerfile frontend must be builtin:dockerfile.v1")
	}
	return nil
}

func validateBuildxPolicyBinding(request BuildKitBuildRequest) error {
	if err := validateDigest("buildx policy digest", request.PolicyDigest); err != nil {
		return err
	}
	if request.ImageSchemaVersion != imageInputSchemaVersion {
		return fmt.Errorf("buildx image schema version %q is unsupported", request.ImageSchemaVersion)
	}
	return nil
}

func validateBuildxDigests(request BuildKitBuildRequest) error {
	digests := []struct {
		name  string
		value string
	}{
		{name: "buildx context digest", value: request.ContextDigest},
		{name: "buildx input manifest digest", value: request.InputManifestDigest},
		{name: "buildx input digest", value: request.InputDigest},
		{name: "buildx toolchain digest", value: request.ToolchainDigest},
		{name: "buildx Dockerfile digest", value: request.DockerfileDigest},
		{name: "buildx cache namespace", value: request.CacheNamespace},
	}
	for _, digestValue := range digests {
		if err := validateDigest(digestValue.name, digestValue.value); err != nil {
			return err
		}
	}
	return nil
}

// validateBuildxArguments 要求工具链参数严格排序、唯一且值类型受锁约束。
func validateBuildxArguments(arguments []BuildArgument) error {
	if len(arguments) == 0 {
		return errors.New("buildx locked build arguments are required")
	}
	previous := ""
	foundSourceDateEpoch := false
	for _, argument := range arguments {
		if !validBuildxArgumentName(argument.Name) {
			return fmt.Errorf("buildx argument name %q is invalid", argument.Name)
		}
		if _, forbidden := forbiddenBuildArgumentNames[strings.ToUpper(argument.Name)]; forbidden {
			return fmt.Errorf("buildx proxy argument %q is forbidden", argument.Name)
		}
		if previous >= argument.Name {
			return errors.New("buildx arguments must be strictly sorted and unique")
		}
		if argument.Name == sourceDateEpochArgument {
			if err := validateSourceDateEpoch(argument.Value); err != nil {
				return err
			}
			foundSourceDateEpoch = true
		} else if !immutableImageReference(argument.Value) {
			return fmt.Errorf("buildx argument %q must be an immutable image reference", argument.Name)
		}
		previous = argument.Name
	}
	if !foundSourceDateEpoch {
		return errors.New("buildx SOURCE_DATE_EPOCH argument is required")
	}
	return nil
}

// validBuildxArgumentName 只接受不触发环境传播的显式大写 ARG 名称。
func validBuildxArgumentName(name string) bool {
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for _, character := range name[1:] {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func immutableImageReference(reference string) bool {
	separator := strings.LastIndex(reference, "@")
	return separator > 0 && validateDigest("buildx image reference", reference[separator+1:]) == nil
}

// validateBuildxDockerfilePath 阻止绝对路径、逃逸路径和 option 注入。
func validateBuildxDockerfilePath(dockerfilePath string) error {
	if dockerfilePath == "" || path.IsAbs(dockerfilePath) || path.Clean(dockerfilePath) != dockerfilePath {
		return errors.New("buildx Dockerfile path must be canonical and relative")
	}
	if dockerfilePath == ".." || strings.HasPrefix(dockerfilePath, "../") {
		return errors.New("buildx Dockerfile path escapes the canonical context")
	}
	if strings.HasPrefix(dockerfilePath, "-") || strings.ContainsAny(dockerfilePath, "\\\x00\r\n") {
		return errors.New("buildx Dockerfile path contains forbidden characters")
	}
	return nil
}

// validateBuildxDockerfileDirectives 在 BuildKit 读取 Dockerfile 前拒绝危险的 parser directive。
func validateBuildxDockerfileDirectives(contextTar []byte, dockerfilePath string) error {
	reader := tar.NewReader(bytes.NewReader(contextTar))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("buildx context is missing the requested Dockerfile")
		}
		if err != nil {
			return fmt.Errorf("read buildx context tar: %w", err)
		}
		if path.Clean(header.Name) != dockerfilePath {
			continue
		}
		if !header.FileInfo().Mode().IsRegular() {
			return errors.New("buildx Dockerfile must be a regular context file")
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return fmt.Errorf("read buildx Dockerfile from context: %w", err)
		}
		return validateBuildxDockerfileContent(data)
	}
}

// validateBuildxDockerfileContent 检查 Dockerfile 指令头，遇到首个构建指令即停止扫描。
func validateBuildxDockerfileContent(data []byte) error {
	for rawLine := range strings.SplitSeq(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(rawLine, "\ufeff"))
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			return nil
		}
		candidate := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		key, _, found := strings.Cut(candidate, "=")
		if found && validBuildxParserDirectiveName(strings.TrimSpace(key)) {
			return fmt.Errorf("buildx Dockerfile parser directive %q is forbidden", strings.TrimSpace(key))
		}
	}
	return nil
}

// validBuildxParserDirectiveName 返回需在候选 Dockerfile 中拒绝的 parser directive 名称。
func validBuildxParserDirectiveName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && character != '-' {
			return false
		}
	}
	return true
}

func buildxBuilderName(sourceTreeSHA string, workspaceBase string) string {
	return buildxBuilderNamePrefix + sourceTreeSHA[:12] + "-" + workspaceBase
}

func controlledBuildxContainerName(builderName string) string {
	return "buildx_buildkit_" + builderName + "0"
}

// validateBuildxInspectVersion 确认 inspect 输出唯一且精确匹配锁定的 BuildKit 版本。
func validateBuildxInspectVersion(output string, expectedVersion string) error {
	const prefix = "BuildKit version:"
	found := false
	for rawLine := range strings.SplitSeq(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if found {
			return errors.New("controlled buildx builder inspection contains duplicate BuildKit version fields")
		}
		actual, err := parseBuildxInspectVersionValue(strings.TrimPrefix(line, prefix))
		if err != nil {
			return err
		}
		if actual != expectedVersion {
			return fmt.Errorf("controlled buildx builder BuildKit version %q does not match lock %q", actual, expectedVersion)
		}
		found = true
	}
	if !found {
		return errors.New("controlled buildx builder inspection is missing the BuildKit version field")
	}
	return nil
}

// parseBuildxInspectVersionValue 接受 Buildx 列对齐空白并拒绝版本值内的空白漂移。
func parseBuildxInspectVersionValue(value string) (string, error) {
	if value == "" || (value[0] != ' ' && value[0] != '\t') {
		return "", errors.New("controlled buildx builder inspection has an invalid BuildKit version field")
	}
	actual := strings.TrimLeft(value, " \t")
	if actual == "" || strings.ContainsAny(actual, " \t") {
		return "", errors.New("controlled buildx builder inspection has an invalid BuildKit version field")
	}
	return actual, nil
}

// validateBuildxContainerIdentity 验证受控容器使用锁定镜像并保持设定的资源限制。
func validateBuildxContainerIdentity(output string, expectedImage string) (string, error) {
	lines := strings.Split(output, "\n")
	if len(lines) != 4 || lines[3] != "" {
		return "", errors.New("controlled buildx builder container identity output is malformed")
	}
	if lines[0] != expectedImage {
		return "", errors.New("controlled buildx builder container image is not the locked immutable reference")
	}
	if err := validateDigest("controlled buildx builder container image ID", lines[1]); err != nil {
		return "", err
	}
	if lines[2] != buildxBuilderCPUQuota+"/"+buildxBuilderCPUPeriod+"/"+buildxBuilderMemoryBytes+"/"+buildxBuilderPidsLimit {
		return "", errors.New("controlled buildx builder resources do not match the 4 CPU / 8 GiB / 512 pids contract")
	}
	return lines[1], nil
}

// validateBuildxImageIdentity 验证容器镜像 ID 与不可变 RepoDigest 均精确绑定到锁定镜像。
func validateBuildxImageIdentity(output string, expectedImage string, containerImageID string) error {
	lines := strings.Split(output, "\n")
	if len(lines) < 3 || lines[len(lines)-1] != "" || lines[0] != containerImageID {
		return errors.New("controlled buildx builder image identity output is malformed")
	}
	found := 0
	for _, reference := range lines[1 : len(lines)-1] {
		if buildxRepoDigestMatchesLock(reference, expectedImage) {
			found++
		}
	}
	if found != 1 {
		return errors.New("controlled buildx builder image does not retain the locked immutable repository digest")
	}
	return nil
}

// buildxRepoDigestMatchesLock accepts Docker Hub's sole prefix normalization while preserving repository and digest identity.
func buildxRepoDigestMatchesLock(reference string, expectedImage string) bool {
	if reference == expectedImage {
		return true
	}
	const dockerHubPrefix = "docker.io/"
	return strings.HasPrefix(expectedImage, dockerHubPrefix) && reference == strings.TrimPrefix(expectedImage, dockerHubPrefix)
}

type buildxPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

// parseBuildxPlatform 将固定平台解析为 metadata 可比较结构。
func parseBuildxPlatform(value string) (buildxPlatform, error) {
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return buildxPlatform{}, errors.New("buildx platform must be os/architecture[/variant]")
	}
	for _, part := range parts {
		if !safeBuildxPlatformPart(part) {
			return buildxPlatform{}, errors.New("buildx platform contains invalid characters")
		}
	}
	platform := buildxPlatform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		platform.Variant = parts[2]
	}
	return platform, nil
}

// safeBuildxPlatformPart 拒绝平台参数中的分隔符和 option 字符。
func safeBuildxPlatformPart(part string) bool {
	if part == "" {
		return false
	}
	for _, character := range part {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && !strings.ContainsRune("_.-", character) {
			return false
		}
	}
	return true
}

func sortedBuildxBindingLabels(request BuildKitBuildRequest) []string {
	labels := []string{
		"org.super-dolphin.context-digest=" + request.ContextDigest,
		"org.super-dolphin.dockerfile-digest=" + request.DockerfileDigest,
		"org.super-dolphin.image-input-digest=" + request.InputDigest,
		"org.super-dolphin.platform=" + request.Platform,
		"org.super-dolphin.policy-sha=" + request.PolicyDigest,
		"org.super-dolphin.schema-version=" + request.ImageSchemaVersion,
		"org.super-dolphin.source-tree-sha=" + request.SourceTreeSHA,
		"org.super-dolphin.toolchain-digest=" + request.ToolchainDigest,
	}
	sort.Strings(labels)
	return labels
}

// validateBuildxRoot 要求真实、私有且不含 cache CSV 控制字符的目录。
func validateBuildxRoot(trustedRoot string) (string, error) {
	if strings.ContainsAny(trustedRoot, ",\x00\r\n") {
		return "", errors.New("buildx root contains cache CSV control characters")
	}
	root, err := trustedDirectory(trustedRoot)
	if err != nil {
		return "", fmt.Errorf("trusted buildx root: %w", err)
	}
	if root == string(filepath.Separator) {
		return "", errors.New("trusted buildx root cannot be the filesystem root")
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("trusted buildx root must be private")
	}
	return root, nil
}

func makePrivateDirectories(paths ...string) error {
	for _, directory := range paths {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create private buildx directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("protect private buildx directory: %w", err)
		}
	}
	return nil
}

// readBuildxMetadataFile 只读取受限大小的普通 metadata 文件。
func readBuildxMetadataFile(metadataPath string) ([]byte, error) {
	info, err := os.Lstat(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("read buildx metadata file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > buildxMetadataLimit {
		return nil, errors.New("buildx metadata must be a bounded regular file")
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("read buildx metadata file: %w", err)
	}
	return data, nil
}

func parseBuildxDigestOutput(output string) (string, error) {
	trimmed := strings.TrimSuffix(output, "\n")
	trimmed = strings.TrimSuffix(trimmed, "\r")
	if trimmed == "" || strings.TrimSpace(trimmed) != trimmed || strings.ContainsAny(trimmed, "\r\n") {
		return "", errors.New("buildx command output must contain one digest and no trailing output")
	}
	if err := validateDigest("buildx manifest output", trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}

func buildxCommandExecutorIsNil(executor buildxCommandExecutor) bool {
	if executor == nil {
		return true
	}
	value := reflect.ValueOf(executor)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func sanitizedBuildxEnvironment(environment []string) []string {
	blocked := map[string]struct{}{
		"ALL_PROXY": {}, "HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
		"BUILDX_METADATA_PROVENANCE": {}, "BUILDX_METADATA_WARNINGS": {}, "BUILDX_NO_DEFAULT_ATTESTATIONS": {},
		"BUILDX_BUILDER": {}, "DOCKER_CONTEXT": {}, "DOCKER_HOST": {}, "DOCKER_TLS_VERIFY": {}, "DOCKER_CERT_PATH": {},
	}
	filtered := make([]string, 0, len(environment)+3)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, forbidden := blocked[strings.ToUpper(name)]; !forbidden {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, "BUILDX_METADATA_PROVENANCE=max", "BUILDX_METADATA_WARNINGS=0", "BUILDX_NO_DEFAULT_ATTESTATIONS=1")
}
