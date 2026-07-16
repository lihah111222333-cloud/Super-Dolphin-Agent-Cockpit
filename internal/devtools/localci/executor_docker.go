package localci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const (
	normalExecutionTimeout  = 10 * time.Minute
	releaseExecutionTimeout = 30 * time.Minute
)

type dockerRunner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

type execDockerRunner struct{}

// Run 执行单个 Docker CLI 操作并保留标准错误上下文。
func (execDockerRunner) Run(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

type dockerExecutor struct {
	runner            dockerRunner
	seccompPath       string
	seccompDigest     string
	trustedSourceRoot string
}

type containerRequest struct {
	Image     string
	SourceDir string
	Command   []string
	Release   bool
}

type containerLifecycleEvidence struct {
	ContainerID   string
	ExitCode      int
	Removed       bool
	Killed        bool
	Deadline      time.Time
	SeccompDigest string
}

// newDockerExecutor 固定 seccomp 摘要和受信源码临时根。
func newDockerExecutor(runner dockerRunner, seccompPath string, trustedSourceRoot string) (*dockerExecutor, error) {
	if runner == nil {
		return nil, errors.New("docker runner is required")
	}
	if !filepath.IsAbs(seccompPath) || filepath.Clean(seccompPath) != seccompPath {
		return nil, errors.New("seccomp profile path must be canonical and absolute")
	}
	if info, err := os.Stat(seccompPath); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("seccomp profile must be an existing regular file")
	}
	seccompDigest, err := fileDigest(seccompPath)
	if err != nil {
		return nil, fmt.Errorf("digest seccomp profile: %w", err)
	}
	root, err := trustedDirectory(trustedSourceRoot)
	if err != nil {
		return nil, fmt.Errorf("trusted source root: %w", err)
	}
	if root == string(filepath.Separator) {
		return nil, errors.New("trusted source root cannot be the filesystem root")
	}
	return &dockerExecutor{runner: runner, seccompPath: seccompPath, seccompDigest: seccompDigest, trustedSourceRoot: root}, nil
}

// Run 创建一次性容器，执行 gate，并返回清理结果证据。
func (executor *dockerExecutor) Run(ctx context.Context, request containerRequest) (containerLifecycleEvidence, error) {
	var evidence containerLifecycleEvidence
	if err := executor.validateContainerRequest(request); err != nil {
		return evidence, err
	}
	if err := executor.verifySeccomp(); err != nil {
		return evidence, err
	}
	runContext, cancel := platformconfig.WithTimeout(ctx, executionTimeout(request.Release))
	defer cancel()
	evidence.SeccompDigest = executor.seccompDigest
	evidence.Deadline, _ = runContext.Deadline()
	containerID, err := executor.create(runContext, request)
	evidence.ContainerID = containerID
	if evidence.ContainerID == "" {
		return evidence, err
	}
	if err != nil {
		return executor.removeAndJoin(ctx, evidence, err)
	}
	if _, err := executor.runner.Run(runContext, "start", evidence.ContainerID); err != nil {
		return executor.removeAndJoin(ctx, evidence, fmt.Errorf("start gate container: %w", err))
	}
	evidence, err = executor.wait(runContext, ctx, evidence)
	return executor.removeAndJoin(ctx, evidence, err)
}

func (executor *dockerExecutor) wait(runContext context.Context, parentContext context.Context, evidence containerLifecycleEvidence) (containerLifecycleEvidence, error) {
	exitCode, err := executor.runner.Run(runContext, "wait", evidence.ContainerID)
	if err != nil {
		killContext, killCancel := platformconfig.WithTimeout(context.WithoutCancel(parentContext), 30*time.Second)
		defer killCancel()
		if _, killErr := executor.runner.Run(killContext, "kill", evidence.ContainerID); killErr != nil {
			return evidence, errors.Join(fmt.Errorf("wait for gate container: %w", err), fmt.Errorf("kill gate container: %w", killErr))
		}
		evidence.Killed = true
		return evidence, fmt.Errorf("wait for gate container: %w", err)
	}
	parsedExitCode, err := strconv.Atoi(strings.TrimSpace(exitCode))
	if err != nil {
		return evidence, fmt.Errorf("parse gate container exit code %q: %w", strings.TrimSpace(exitCode), err)
	}
	evidence.ExitCode = parsedExitCode
	if parsedExitCode != 0 {
		return evidence, fmt.Errorf("gate container exited with code %d", parsedExitCode)
	}
	return evidence, nil
}

func (executor *dockerExecutor) create(ctx context.Context, request containerRequest) (string, error) {
	createOutput, err := executor.runner.Run(ctx, executor.createArgs(request)...)
	if err != nil {
		return "", fmt.Errorf("create gate container: %w", err)
	}
	fields := strings.Fields(createOutput)
	if len(fields) == 0 || !isContainerID(fields[0]) {
		return "", errors.New("docker create returned no verifiable container ID")
	}
	if len(fields) != 1 {
		return fields[0], errors.New("docker create returned trailing output after container ID")
	}
	return fields[0], nil
}

func (executor *dockerExecutor) remove(parentContext context.Context, containerID string) error {
	cleanupContext, cleanupCancel := platformconfig.WithTimeout(context.WithoutCancel(parentContext), 30*time.Second)
	defer cleanupCancel()
	_, err := executor.runner.Run(cleanupContext, "rm", "--force", containerID)
	return err
}

func (executor *dockerExecutor) removeAndJoin(ctx context.Context, evidence containerLifecycleEvidence, runErr error) (containerLifecycleEvidence, error) {
	if cleanupErr := executor.remove(ctx, evidence.ContainerID); cleanupErr != nil {
		return evidence, errors.Join(runErr, fmt.Errorf("remove gate container: %w", cleanupErr))
	}
	evidence.Removed = true
	return evidence, runErr
}

func (executor *dockerExecutor) verifySeccomp() error {
	currentDigest, err := fileDigest(executor.seccompPath)
	if err != nil {
		return fmt.Errorf("verify seccomp profile: %w", err)
	}
	if currentDigest != executor.seccompDigest {
		return errors.New("seccomp profile changed after executor construction")
	}
	return nil
}

func (executor *dockerExecutor) createArgs(request containerRequest) []string {
	args := []string{
		"create",
		"--cpus=" + strconv.FormatInt(workloadLogicalCPUs, 10),
		"--memory=" + strconv.FormatInt(workloadMemoryGiB, 10) + "g",
		"--pids-limit=512", "--storage-opt=size=10G", "--read-only", "--user=65532:65532",
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--security-opt=seccomp=" + executor.seccompPath,
		"--network=none", "--mount=type=bind,src=" + request.SourceDir + ",dst=/workspace/source,readonly",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=2147483648", "--log-driver=local", "--log-opt=max-size=10m", "--log-opt=max-file=3",
		request.Image,
	}
	return append(args, request.Command...)
}

// validateContainerRequest 校验不可变镜像、受信源码挂载和 gate 命令。
func (executor *dockerExecutor) validateContainerRequest(request containerRequest) error {
	if !strings.Contains(request.Image, "@sha256:") || !sha256DigestPattern.MatchString(request.Image[strings.LastIndex(request.Image, "@")+1:]) {
		return errors.New("container image must use an immutable platform manifest digest")
	}
	if err := executor.validateSourceDirectory(request.SourceDir); err != nil {
		return err
	}
	if len(request.Command) == 0 || request.Command[0] == "" {
		return errors.New("gate command is required")
	}
	return nil
}

// validateSourceDirectory 将源码限制在受信临时根的真实子目录中。
func (executor *dockerExecutor) validateSourceDirectory(sourceDirectory string) error {
	if strings.ContainsAny(sourceDirectory, ",\x00\r\n") {
		return errors.New("source directory contains Docker mount CSV control characters")
	}
	sourceDir, err := trustedDirectory(sourceDirectory)
	if err != nil {
		return fmt.Errorf("source directory: %w", err)
	}
	relative, err := filepath.Rel(executor.trustedSourceRoot, sourceDir)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("source directory must be a child of the trusted temporary root")
	}
	return nil
}

// trustedDirectory 解析真实目录，阻止符号链接逃逸。
func trustedDirectory(directory string) (string, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return "", errors.New("path must be canonical and absolute")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path must be a directory")
	}
	return resolved, nil
}

func executionTimeout(release bool) time.Duration {
	if release {
		return releaseExecutionTimeout
	}
	return normalExecutionTimeout
}

func fileDigest(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// isContainerID 只接受 Docker 返回的十六进制容器标识。
func isContainerID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

const noContainerNetwork = "none"

// FreshContainerImageTruth binds the inspected image labels to accepted build truth.
type FreshContainerImageTruth struct {
	PolicyDigest       string
	BuildSourceTreeSHA string
	InputDigest        string
	ToolchainDigest    string
	SchemaVersion      string
}

// FreshContainerRequest contains only canonical execution authority and source inputs.
type FreshContainerRequest struct {
	Image             gate.ImageIdentity
	ImageTruth        FreshContainerImageTruth
	SourceTreeSHA     string
	SourceSnapshotDir string
	Profile           gate.Profile
	Plan              gate.GatePlan
	GateID            gate.GateID
}

// FreshContainerResult is unsigned, directly observed evidence for a later coordinator.
type FreshContainerResult struct {
	Status             gate.ResultStatus
	ImageReference     string
	Container          gate.ContainerEvidence
	Evidence           []gate.Evidence
	GateResult         *gate.GateResult
	ExitCode           int
	StartedAt          time.Time
	CompletedAt        time.Time
	Deadline           time.Time
	Killed             bool
	KillProofDigest    string
	LogDigest          string
	RemovalProofDigest string
}

// FreshContainerRunner creates one new container for every RunFreshContainer call.
type FreshContainerRunner struct {
	docker *dockerExecutor
	now    func() time.Time
}

// NewFreshContainerRunner 构造生产使用的 Docker 一次性容器执行边界。
func NewFreshContainerRunner(seccompPath string, trustedSourceRoot string) (*FreshContainerRunner, error) {
	return newFreshContainerRunner(execDockerRunner{}, seccompPath, trustedSourceRoot)
}

func newFreshContainerRunner(dockerRunner dockerRunner, seccompPath string, trustedSourceRoot string) (*FreshContainerRunner, error) {
	if isTypedNil(dockerRunner) {
		return nil, errors.New("docker runner is required")
	}
	executor, err := newDockerExecutor(dockerRunner, seccompPath, trustedSourceRoot)
	if err != nil {
		return nil, err
	}
	return &FreshContainerRunner{docker: executor, now: time.Now}, nil
}

// RunFreshContainer 验证镜像真值，执行一个规范 gate，并证明容器已销毁。
func (runner *FreshContainerRunner) RunFreshContainer(ctx context.Context, request FreshContainerRequest) (FreshContainerResult, error) {
	result := FreshContainerResult{Status: gate.ResultStatusInfraFailed, ExitCode: -1}
	if isTypedNil(ctx) {
		return result, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		result.Status = statusForContext(err)
		return result, err
	}
	prepared, err := runner.prepareRequest(request)
	if err != nil {
		return result, err
	}
	result.ImageReference = prepared.imageReference
	imageDigest, err := runner.inspectAndVerifyImage(ctx, prepared)
	if err != nil {
		return result, err
	}
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: imageDigest})
	runContext, cancel := platformconfig.WithTimeout(ctx, profileExecutionTimeout(request.Profile))
	defer cancel()
	result.Deadline, _ = runContext.Deadline()
	return runner.runPreparedContainer(runContext, ctx, request, prepared, result)
}

type preparedFreshContainerRequest struct {
	imageReference     string
	command            []string
	expectedImage      expectedImageMetadata
	expectedImageIndex string
	expectedIdentity   gate.ImageIdentity
}

// prepareRequest 将请求收敛为不可变镜像引用和计划内固定命令。
func (runner *FreshContainerRunner) prepareRequest(request FreshContainerRequest) (preparedFreshContainerRequest, error) {
	var prepared preparedFreshContainerRequest
	if runner == nil || runner.docker == nil {
		return prepared, errors.New("fresh container runner is required")
	}
	command, err := validateFreshContainerRequest(request)
	if err != nil {
		return prepared, err
	}
	prepared.imageReference, err = executionImageReference(request.Image)
	if err != nil {
		return prepared, err
	}
	prepared.command = command
	prepared.expectedImageIndex = request.Image.OCIIndexDigest
	prepared.expectedIdentity = request.Image
	prepared.expectedImage = expectedImageMetadata{
		PolicyDigest: request.ImageTruth.PolicyDigest, SourceTreeSHA: request.ImageTruth.BuildSourceTreeSHA,
		InputDigest: request.ImageTruth.InputDigest, ToolchainDigest: request.ImageTruth.ToolchainDigest,
		SchemaVersion: request.ImageTruth.SchemaVersion, OS: request.Image.OS,
		Architecture: request.Image.Architecture, Variant: request.Image.Variant,
	}
	container := containerRequest{Image: prepared.imageReference, SourceDir: request.SourceSnapshotDir, Command: command, Release: request.Profile == gate.ProfileRelease}
	if err := runner.docker.validateContainerRequest(container); err != nil {
		return prepared, err
	}
	if err := validatePrivateSnapshot(request.SourceSnapshotDir); err != nil {
		return prepared, err
	}
	if err := runner.docker.verifySeccomp(); err != nil {
		return prepared, err
	}
	return prepared, nil
}

// validateFreshContainerRequest 校验 profile、计划、源码树与完整镜像身份闭包。
func validateFreshContainerRequest(request FreshContainerRequest) ([]string, error) {
	if err := request.Profile.Validate(); err != nil {
		return nil, err
	}
	if err := request.Plan.Validate(); err != nil {
		return nil, fmt.Errorf("gate plan: %w", err)
	}
	if request.Plan.Profile != request.Profile {
		return nil, errors.New("request profile does not match gate plan")
	}
	if !gitObjectPattern.MatchString(request.SourceTreeSHA) || request.Plan.Source.SourceTreeSHA != request.SourceTreeSHA {
		return nil, errors.New("request source_tree_sha does not match gate plan")
	}
	command, err := commandFromPlan(request.Plan, request.GateID)
	if err != nil {
		return nil, err
	}
	identity := gateImageIdentityReader{ImageIdentity: request.Image}
	expected := expectedImageMetadata{
		PolicyDigest: request.ImageTruth.PolicyDigest, SourceTreeSHA: request.ImageTruth.BuildSourceTreeSHA,
		InputDigest: request.ImageTruth.InputDigest, ToolchainDigest: request.ImageTruth.ToolchainDigest,
		SchemaVersion: request.ImageTruth.SchemaVersion, OS: request.Image.OS,
		Architecture: request.Image.Architecture, Variant: request.Image.Variant,
	}
	if err := validateImageIdentity(identity, expected.labels(), expected); err != nil {
		return nil, err
	}
	return command, nil
}

func commandFromPlan(plan gate.GatePlan, gateID gate.GateID) ([]string, error) {
	for _, spec := range plan.Gates {
		if spec.ID == gateID {
			return append([]string(nil), spec.Argv...), nil
		}
	}
	return nil, fmt.Errorf("gate %q is not present in the canonical plan", gateID)
}

// executionImageReference 只从 registry 与平台 manifest digest 派生执行引用。
func executionImageReference(identity gate.ImageIdentity) (string, error) {
	registry := identity.Registry
	if registry == "" || strings.TrimSpace(registry) != registry || strings.ContainsAny(registry, "@\x00\r\n\t ") {
		return "", errors.New("image registry must be a canonical repository without tag or digest")
	}
	lastSlash := strings.LastIndex(registry, "/")
	if strings.Contains(registry[lastSlash+1:], ":") || strings.HasSuffix(registry, "/") {
		return "", errors.New("image registry must not contain a tag")
	}
	if err := validateDigest("platform manifest digest", identity.PlatformManifestDigest); err != nil {
		return "", err
	}
	return registry + "@" + identity.PlatformManifestDigest, nil
}

func validatePrivateSnapshot(directory string) error {
	resolved, err := trustedDirectory(directory)
	if err != nil {
		return fmt.Errorf("source snapshot directory: %w", err)
	}
	if resolved != directory {
		return errors.New("source snapshot directory must be a canonical real path")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat source snapshot directory: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("source snapshot directory must be private to its owner")
	}
	return nil
}

func profileExecutionTimeout(profile gate.Profile) time.Duration {
	return executionTimeout(profile == gate.ProfileRelease)
}

func statusForContext(err error) gate.ResultStatus {
	if errors.Is(err, context.Canceled) {
		return gate.ResultStatusCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return gate.ResultStatusTimeout
	}
	return gate.ResultStatusInfraFailed
}

func isTypedNil(value any) bool {
	if value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	return slices.Contains([]reflect.Kind{reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice}, kind) && reflect.ValueOf(value).IsNil()
}

func (runner *FreshContainerRunner) runPreparedContainer(runContext context.Context, parentContext context.Context, request FreshContainerRequest, prepared preparedFreshContainerRequest, result FreshContainerResult) (FreshContainerResult, error) {
	container := containerRequest{Image: prepared.imageReference, SourceDir: request.SourceSnapshotDir, Command: prepared.command, Release: request.Profile == gate.ProfileRelease}
	containerID, err := runner.docker.create(runContext, container)
	result.setContainerID(containerID)
	if containerID == "" {
		return result, err
	}
	if err != nil {
		return runner.removeContainer(parentContext, result, err)
	}
	hostDigest, inspectDigest, err := runner.inspectCreatedContainer(runContext, containerID, prepared.imageReference, request.Image.ConfigDigest, request.SourceSnapshotDir, prepared.command)
	if err != nil {
		return runner.removeContainer(parentContext, result, err)
	}
	result.Container.HostConfigDigest = hostDigest
	result.Container.NetworkPolicyDigest = digestBytes([]byte("network=none\n"))
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: inspectDigest})
	result.StartedAt = runner.now().UTC()
	if _, err := runner.docker.runner.Run(runContext, "start", containerID); err != nil {
		return runner.finishContainer(parentContext, result, prepared, request, gate.ResultStatusInfraFailed, fmt.Errorf("start gate container: %w", err))
	}
	status, exitCode, waitErr := runner.waitForContainer(runContext, parentContext, &result)
	result.ExitCode = exitCode
	return runner.finishContainer(parentContext, result, prepared, request, status, waitErr)
}

func (result *FreshContainerResult) setContainerID(containerID string) {
	result.Container.ContainerID = containerID
	if containerID != "" {
		result.Container.NetworkID = noContainerNetwork
		result.Container.NetworkRemoved = true
	}
}

func (runner *FreshContainerRunner) waitForContainer(runContext context.Context, parentContext context.Context, result *FreshContainerResult) (gate.ResultStatus, int, error) {
	output, err := runner.docker.runner.Run(runContext, "wait", result.Container.ContainerID)
	if err == nil {
		exitCode, parseErr := parseContainerExitCode(output)
		if parseErr != nil {
			return gate.ResultStatusInfraFailed, -1, parseErr
		}
		if exitCode != 0 {
			return gate.ResultStatusFailed, exitCode, fmt.Errorf("gate container exited with code %d", exitCode)
		}
		return gate.ResultStatusPassed, exitCode, nil
	}
	status := statusForContext(runContext.Err())
	exitCode, terminateErr := runner.killAndWait(parentContext, result)
	if terminateErr != nil {
		return gate.ResultStatusInfraFailed, exitCode, errors.Join(fmt.Errorf("wait for gate container: %w", err), terminateErr)
	}
	return status, exitCode, fmt.Errorf("wait for gate container: %w", err)
}

func (runner *FreshContainerRunner) killAndWait(parentContext context.Context, result *FreshContainerResult) (int, error) {
	cleanupContext, cancel := platformconfig.WithTimeout(context.WithoutCancel(parentContext), 30*time.Second)
	defer cancel()
	if _, err := runner.docker.runner.Run(cleanupContext, "kill", result.Container.ContainerID); err != nil {
		return -1, fmt.Errorf("kill gate container: %w", err)
	}
	result.Killed = true
	result.KillProofDigest = digestBytes([]byte("killed\n" + result.Container.ContainerID + "\n"))
	output, err := runner.docker.runner.Run(cleanupContext, "wait", result.Container.ContainerID)
	if err != nil {
		return -1, fmt.Errorf("wait for killed gate container: %w", err)
	}
	return parseContainerExitCode(output)
}

// finishContainer 收集日志与终态 inspect，并在销毁证明成功后才生成 gate 结果。
func (runner *FreshContainerRunner) finishContainer(parentContext context.Context, result FreshContainerResult, prepared preparedFreshContainerRequest, request FreshContainerRequest, status gate.ResultStatus, runErr error) (FreshContainerResult, error) {
	result.Status = status
	logOutput, logErr := runner.runCleanup(parentContext, "logs", "--timestamps", result.Container.ContainerID)
	if logErr == nil {
		result.LogDigest = digestBytes([]byte(logOutput))
		result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindLog, Digest: result.LogDigest})
	}
	stateDigest, inspectErr := runner.inspectFinishedContainer(parentContext, result.Container.ContainerID, prepared.imageReference, request.Image.ConfigDigest, result.ExitCode)
	if inspectErr == nil {
		result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: stateDigest})
	}
	result.CompletedAt = runner.now().UTC()
	if logErr != nil || inspectErr != nil {
		result.Status = gate.ResultStatusInfraFailed
		result.GateResult = nil
		runErr = errors.Join(runErr, logErr, inspectErr)
	}
	result, cleanupErr := runner.removeContainer(parentContext, result, runErr)
	if !result.Container.Removed || result.Status == gate.ResultStatusInfraFailed {
		result.Status = gate.ResultStatusInfraFailed
		result.GateResult = nil
		return result, cleanupErr
	}
	if result.Status == gate.ResultStatusPassed || result.Status == gate.ResultStatusFailed {
		gateResult, gateResultErr := buildGateResult(request.GateID, prepared.command, result)
		if gateResultErr != nil {
			result.Status = gate.ResultStatusInfraFailed
			return result, errors.Join(runErr, gateResultErr)
		}
		result.GateResult = gateResult
	}
	return result, runErr
}

func (runner *FreshContainerRunner) removeContainer(parentContext context.Context, result FreshContainerResult, runErr error) (FreshContainerResult, error) {
	if result.Container.ContainerID == "" {
		return result, runErr
	}
	if err := runner.docker.remove(parentContext, result.Container.ContainerID); err != nil {
		result.Status = gate.ResultStatusInfraFailed
		return result, errors.Join(runErr, fmt.Errorf("remove gate container: %w", err))
	}
	output, err := runner.runCleanup(parentContext, "ps", "--all", "--no-trunc", "--filter=id="+result.Container.ContainerID, "--format={{.ID}}")
	if err != nil {
		result.Status = gate.ResultStatusInfraFailed
		return result, errors.Join(runErr, fmt.Errorf("prove gate container removal: %w", err))
	}
	if strings.TrimSpace(output) != "" {
		result.Status = gate.ResultStatusInfraFailed
		return result, errors.Join(runErr, errors.New("removed gate container is still listed by Docker"))
	}
	result.Container.Removed = true
	result.RemovalProofDigest = digestBytes([]byte("removed\n" + result.Container.ContainerID + "\n"))
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: result.RemovalProofDigest})
	return result, runErr
}

func (runner *FreshContainerRunner) runCleanup(parentContext context.Context, args ...string) (string, error) {
	cleanupContext, cancel := platformconfig.WithTimeout(context.WithoutCancel(parentContext), 30*time.Second)
	defer cancel()
	return runner.docker.runner.Run(cleanupContext, args...)
}

func parseContainerExitCode(output string) (int, error) {
	value := strings.TrimSpace(output)
	exitCode, err := strconv.Atoi(value)
	if err != nil {
		return -1, fmt.Errorf("parse gate container exit code %q: %w", value, err)
	}
	if exitCode < 0 || exitCode > 255 {
		return -1, fmt.Errorf("gate container exit code %d is outside [0,255]", exitCode)
	}
	return exitCode, nil
}

func buildGateResult(gateID gate.GateID, command []string, result FreshContainerResult) (*gate.GateResult, error) {
	status := gate.GateStatusFailed
	if result.Status == gate.ResultStatusPassed {
		status = gate.GateStatusPassed
	}
	argvDigest, err := digestJSON(command)
	if err != nil {
		return nil, fmt.Errorf("digest gate command: %w", err)
	}
	return &gate.GateResult{
		GateID: string(gateID), Status: status, ExitCode: result.ExitCode,
		StartedAt: result.StartedAt, CompletedAt: result.CompletedAt,
		ArgvDigest: argvDigest, LogDigest: result.LogDigest,
	}, nil
}
