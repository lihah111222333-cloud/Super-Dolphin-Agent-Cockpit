package localci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

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
	ContainerLabels   map[string]string
	Deadline          time.Time
	LifecycleHook     FreshContainerLifecycleHook
}

// FreshContainerLifecyclePhase 标识可持久化且可幂等重放的 Docker 边界。
type FreshContainerLifecyclePhase string

const (
	FreshContainerPhasePrepared FreshContainerLifecyclePhase = "prepared"
	FreshContainerPhaseCreated  FreshContainerLifecyclePhase = "created"
	FreshContainerPhaseStarting FreshContainerLifecyclePhase = "starting"
	FreshContainerPhaseStarted  FreshContainerLifecyclePhase = "started"
	FreshContainerPhaseExited   FreshContainerLifecyclePhase = "exited"
	FreshContainerPhaseRemoved  FreshContainerLifecyclePhase = "removed"
)

// FreshContainerLifecycleEvent 在不可逆 Docker 边界后同步提交 durable owner hook。
type FreshContainerLifecycleEvent struct {
	Phase              FreshContainerLifecyclePhase
	ContainerID        string
	ImageReference     string
	ConfigDigest       string
	SourceSnapshotDir  string
	StartedAt          time.Time
	Deadline           time.Time
	CompletedAt        time.Time
	ExitCode           int
	RemovalProofDigest string
}

// FreshContainerLifecycleHook 在继续下一不可逆动作前持久化 lifecycle 事件。
type FreshContainerLifecycleHook func(context.Context, FreshContainerLifecycleEvent) error

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
	return runner.runPreparedContainer(ctx, request, prepared, result)
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
	container := containerRequest{
		Image: prepared.imageReference, SourceDir: request.SourceSnapshotDir, Command: command,
		Release: request.Profile == gate.ProfileRelease, Labels: request.ContainerLabels,
	}
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

// runPreparedContainer 按 prepared、create、start、exit、remove 顺序持久化单次执行。
func (runner *FreshContainerRunner) runPreparedContainer(parentContext context.Context, request FreshContainerRequest, prepared preparedFreshContainerRequest, result FreshContainerResult) (FreshContainerResult, error) {
	container := containerRequest{
		Image: prepared.imageReference, SourceDir: request.SourceSnapshotDir, Command: prepared.command,
		Release: request.Profile == gate.ProfileRelease, Labels: request.ContainerLabels,
	}
	if err := runner.emitLifecycle(parentContext, request, result, FreshContainerPhasePrepared); err != nil {
		return result, err
	}
	containerID, err := runner.docker.create(parentContext, container)
	result.setContainerID(containerID)
	if containerID == "" {
		return result, err
	}
	if err != nil {
		return runner.removeContainer(parentContext, result, request, err)
	}
	if hookErr := runner.emitLifecycle(parentContext, request, result, FreshContainerPhaseCreated); hookErr != nil {
		return runner.removeContainer(parentContext, result, request, hookErr)
	}
	hostDigest, inspectDigest, err := runner.inspectCreatedContainer(
		parentContext, containerID, prepared.imageReference, request.Image.ConfigDigest,
		request.SourceSnapshotDir, prepared.command, request.ContainerLabels,
	)
	if err != nil {
		return runner.removeContainer(parentContext, result, request, err)
	}
	result.Container.HostConfigDigest = hostDigest
	result.Container.NetworkPolicyDigest = digestBytes([]byte("network=none\n"))
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: inspectDigest})
	result.StartedAt = runner.now().UTC()
	result.Deadline = request.Deadline.UTC()
	if result.Deadline.IsZero() {
		result.Deadline = result.StartedAt.Add(profileExecutionTimeout(request.Profile))
	}
	if hookErr := runner.emitLifecycle(parentContext, request, result, FreshContainerPhaseStarting); hookErr != nil {
		return runner.removeContainer(parentContext, result, request, hookErr)
	}
	runContext, cancel := context.WithDeadline(parentContext, result.Deadline)
	defer cancel()
	if _, err := runner.docker.runner.Run(runContext, "start", containerID); err != nil {
		return runner.finishContainer(parentContext, result, prepared, request, gate.ResultStatusInfraFailed, fmt.Errorf("start gate container: %w", err))
	}
	if hookErr := runner.emitLifecycle(parentContext, request, result, FreshContainerPhaseStarted); hookErr != nil {
		exitCode, terminateErr := runner.killAndWait(parentContext, &result)
		result.ExitCode = exitCode
		return runner.finishContainer(parentContext, result, prepared, request, gate.ResultStatusInfraFailed, errors.Join(hookErr, terminateErr))
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
	if hookErr := runner.emitLifecycle(parentContext, request, result, FreshContainerPhaseExited); hookErr != nil {
		result.Status = gate.ResultStatusInfraFailed
		result.GateResult = nil
		runErr = errors.Join(runErr, hookErr)
	}
	if evidenceErr := errors.Join(logErr, inspectErr); evidenceErr != nil {
		result.Status = gate.ResultStatusInfraFailed
		result.GateResult = nil
		runErr = errors.Join(runErr, evidenceErr)
	}
	result, cleanupErr := runner.removeContainer(parentContext, result, request, runErr)
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

// removeContainer 只有在 Docker 列表证明容器消失后才写入 removal proof。
func (runner *FreshContainerRunner) removeContainer(parentContext context.Context, result FreshContainerResult, request FreshContainerRequest, runErr error) (FreshContainerResult, error) {
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
	if hookErr := runner.emitLifecycle(context.WithoutCancel(parentContext), request, result, FreshContainerPhaseRemoved); hookErr != nil {
		result.Status = gate.ResultStatusInfraFailed
		return result, errors.Join(runErr, hookErr)
	}
	return result, runErr
}

func (runner *FreshContainerRunner) emitLifecycle(
	ctx context.Context,
	request FreshContainerRequest,
	result FreshContainerResult,
	phase FreshContainerLifecyclePhase,
) error {
	if request.LifecycleHook == nil {
		return nil
	}
	event := FreshContainerLifecycleEvent{
		Phase: phase, ContainerID: result.Container.ContainerID,
		ImageReference: result.ImageReference, ConfigDigest: request.Image.ConfigDigest,
		SourceSnapshotDir: request.SourceSnapshotDir, StartedAt: result.StartedAt,
		Deadline: result.Deadline, CompletedAt: result.CompletedAt, ExitCode: result.ExitCode,
		RemovalProofDigest: result.RemovalProofDigest,
	}
	if err := request.LifecycleHook(ctx, event); err != nil {
		return fmt.Errorf("persist container lifecycle phase %q: %w", phase, err)
	}
	return nil
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

// FreshContainerRecoveryRequest 是重启后观察既有容器所需的完整 immutable identity。
type FreshContainerRecoveryRequest struct {
	ContainerID       string
	ContainerLabels   map[string]string
	ImageReference    string
	ConfigDigest      string
	SourceSnapshotDir string
	Command           []string
	Profile           gate.Profile
	GateID            gate.GateID
	StartedAt         time.Time
	Deadline          time.Time
	LifecycleHook     FreshContainerLifecycleHook
}

// FreshContainerRecoveryObservation 是已验证身份容器的只读状态。
type FreshContainerRecoveryObservation struct {
	ContainerID string
	Status      string
}

// FreshContainerCleanupRequest 提供定位和销毁未证明容器所需的最小身份闭包。
type FreshContainerCleanupRequest struct {
	ContainerID       string
	ContainerLabels   map[string]string
	ImageReference    string
	ConfigDigest      string
	SourceSnapshotDir string
	Command           []string
	Profile           gate.Profile
	GateID            gate.GateID
	LifecycleHook     FreshContainerLifecycleHook
}

// ProbeFreshContainerRecovery 只探测并验证原容器，不推进执行或终态。
func (runner *FreshContainerRunner) ProbeFreshContainerRecovery(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
) (FreshContainerRecoveryObservation, error) {
	if ctx == nil || runner == nil || runner.docker == nil {
		return FreshContainerRecoveryObservation{}, errors.New("recovery runner and context are required")
	}
	if err := runner.validateRecoveryRequest(request); err != nil {
		return FreshContainerRecoveryObservation{}, err
	}
	containerID, err := runner.resolveRecoveryContainer(ctx, request)
	if err != nil {
		return FreshContainerRecoveryObservation{}, err
	}
	document, err := runner.inspectContainer(ctx, containerID)
	if err != nil {
		return FreshContainerRecoveryObservation{}, err
	}
	if err := runner.validateRecoveryContainer(document, request); err != nil {
		return FreshContainerRecoveryObservation{}, err
	}
	return FreshContainerRecoveryObservation{ContainerID: containerID, Status: document.State.Status}, nil
}

// CleanupUnprovedFreshContainer 对无法接管的旧容器执行 kill、wait、remove。
func (runner *FreshContainerRunner) CleanupUnprovedFreshContainer(
	ctx context.Context,
	request FreshContainerCleanupRequest,
) (FreshContainerResult, error) {
	recovery := FreshContainerRecoveryRequest{
		ContainerID: request.ContainerID, ContainerLabels: request.ContainerLabels,
		ImageReference: request.ImageReference, ConfigDigest: request.ConfigDigest,
		SourceSnapshotDir: request.SourceSnapshotDir, Command: request.Command,
		Profile: request.Profile, GateID: request.GateID, LifecycleHook: request.LifecycleHook,
	}
	result := FreshContainerResult{Status: gate.ResultStatusInfraFailed, ImageReference: request.ImageReference, ExitCode: -1}
	container := containerRequest{
		Image: request.ImageReference, SourceDir: request.SourceSnapshotDir, Command: request.Command,
		Release: request.Profile == gate.ProfileRelease, Labels: request.ContainerLabels,
	}
	if ctx == nil || runner == nil || runner.docker == nil {
		return result, errors.New("cleanup runner and context are required")
	}
	if err := runner.docker.validateContainerRequest(container); err != nil {
		return result, err
	}
	containerID, err := runner.resolveRecoveryContainer(ctx, recovery)
	if err != nil {
		return result, err
	}
	result.setContainerID(containerID)
	document, err := runner.inspectContainer(ctx, containerID)
	if err == nil {
		err = runner.validateRecoveryContainerIdentity(document, recovery)
	}
	return runner.terminateUnprovedRecovery(ctx, recovery, result, err)
}

// RecoverFreshContainer 只接管仍由完整身份闭包证明的容器，不创建替代执行。
func (runner *FreshContainerRunner) RecoverFreshContainer(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
) (FreshContainerResult, error) {
	result := FreshContainerResult{
		Status: gate.ResultStatusInfraFailed, ImageReference: request.ImageReference,
		ExitCode: -1, StartedAt: request.StartedAt.UTC(), Deadline: request.Deadline.UTC(),
	}
	if err := requireRecoveryRunner(ctx, runner); err != nil {
		return result, err
	}
	if err := runner.validateRecoveryRequest(request); err != nil {
		return result, err
	}
	containerID, err := runner.resolveRecoveryContainer(ctx, request)
	if err != nil {
		return result, err
	}
	result.setContainerID(containerID)
	document, err := runner.inspectContainer(ctx, containerID)
	if err != nil {
		return result, err
	}
	if err := runner.validateRecoveryContainer(document, request); err != nil {
		return runner.terminateUnprovedRecovery(ctx, request, result, err)
	}
	freshRequest := FreshContainerRequest{
		Image:             gate.ImageIdentity{ConfigDigest: request.ConfigDigest},
		SourceSnapshotDir: request.SourceSnapshotDir, Profile: request.Profile,
		GateID: request.GateID, ContainerLabels: request.ContainerLabels,
		Deadline: request.Deadline, LifecycleHook: request.LifecycleHook,
	}
	prepared := preparedFreshContainerRequest{
		imageReference: request.ImageReference, command: append([]string(nil), request.Command...),
	}
	switch document.State.Status {
	case "running":
		waitContext, cancel := context.WithDeadline(ctx, request.Deadline)
		defer cancel()
		status, exitCode, waitErr := runner.waitForContainer(waitContext, ctx, &result)
		result.ExitCode = exitCode
		return runner.finishContainer(ctx, result, prepared, freshRequest, status, waitErr)
	case "exited":
		output, waitErr := runner.runCleanup(ctx, "wait", containerID)
		if waitErr != nil {
			return runner.terminateUnprovedRecovery(ctx, request, result, waitErr)
		}
		exitCode, status, runErr, parseErr := parseRecoveredExit(output)
		if parseErr != nil {
			return runner.terminateUnprovedRecovery(ctx, request, result, parseErr)
		}
		result.ExitCode = exitCode
		return runner.finishContainer(ctx, result, prepared, freshRequest, status, runErr)
	default:
		return runner.terminateUnprovedRecovery(
			ctx, request, result, fmt.Errorf("recovered container state %q is not observable", document.State.Status),
		)
	}
}

func parseRecoveredExit(output string) (int, gate.ResultStatus, error, error) {
	exitCode, err := parseContainerExitCode(output)
	if err != nil {
		return -1, gate.ResultStatusInfraFailed, nil, err
	}
	if exitCode != 0 {
		return exitCode, gate.ResultStatusFailed, fmt.Errorf("recovered gate container exited with code %d", exitCode), nil
	}
	return exitCode, gate.ResultStatusPassed, nil, nil
}

// validateRecoveryRequest 校验恢复时钟与不可变 Docker 请求，不接受重算 deadline。
func (runner *FreshContainerRunner) validateRecoveryRequest(request FreshContainerRecoveryRequest) error {
	if err := request.Profile.Validate(); err != nil {
		return err
	}
	if err := validateRecoveryClock(request); err != nil {
		return err
	}
	if request.ContainerID != "" && !isContainerID(request.ContainerID) {
		return errors.New("recovery container ID is invalid")
	}
	container := containerRequest{
		Image: request.ImageReference, SourceDir: request.SourceSnapshotDir,
		Command: request.Command, Release: request.Profile == gate.ProfileRelease,
		Labels: request.ContainerLabels,
	}
	if err := runner.docker.validateContainerRequest(container); err != nil {
		return err
	}
	if err := validateDigest("recovery config digest", request.ConfigDigest); err != nil {
		return err
	}
	if len(request.ContainerLabels) == 0 {
		return errors.New("recovery container labels are required")
	}
	return nil
}

// validateRecoveryClock 强制沿用首次启动时按 profile 计算的 UTC deadline。
func validateRecoveryClock(request FreshContainerRecoveryRequest) error {
	utc := request.StartedAt.Equal(request.StartedAt.UTC()) && request.Deadline.Equal(request.Deadline.UTC())
	if !utc || request.StartedAt.IsZero() || request.Deadline.IsZero() {
		return errors.New("recovery started_at and deadline must be non-zero UTC timestamps")
	}
	if !request.Deadline.Equal(request.StartedAt.Add(profileExecutionTimeout(request.Profile))) {
		return errors.New("recovery deadline does not match the original profile timeout")
	}
	return nil
}

// resolveRecoveryContainer 使用持久 ID 或全部 labels 唯一定位原容器。
func (runner *FreshContainerRunner) resolveRecoveryContainer(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
) (string, error) {
	if request.ContainerID != "" {
		return request.ContainerID, nil
	}
	keys := make([]string, 0, len(request.ContainerLabels))
	for key := range request.ContainerLabels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	args := []string{"ps", "--all", "--no-trunc"}
	for _, key := range keys {
		args = append(args, "--filter=label="+key+"="+request.ContainerLabels[key])
	}
	args = append(args, "--format={{.ID}}")
	output, err := runner.docker.runner.Run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("discover recovery container: %w", err)
	}
	lines := strings.Fields(output)
	if len(lines) != 1 || !isContainerID(lines[0]) {
		return "", fmt.Errorf("recovery labels resolved %d canonical containers, want 1", len(lines))
	}
	return lines[0], nil
}

func (runner *FreshContainerRunner) validateRecoveryContainer(
	document containerInspectDocument,
	request FreshContainerRecoveryRequest,
) error {
	if err := runner.validateRecoveryContainerIdentity(document, request); err != nil {
		return err
	}
	if document.State == nil || (!document.State.Running && document.State.Status != "exited") {
		return errors.New("recovery container is not alive or exited")
	}
	return nil
}

// validateRecoveryContainerIdentity 验证镜像、命令、挂载、隔离与 labels 的闭包。
func (runner *FreshContainerRunner) validateRecoveryContainerIdentity(
	document containerInspectDocument,
	request FreshContainerRecoveryRequest,
) error {
	expectedContainerID := document.ID
	if request.ContainerID != "" {
		expectedContainerID = request.ContainerID
	}
	if err := validateContainerIdentity(
		document, expectedContainerID, request.ImageReference, request.ConfigDigest, request.Command,
	); err != nil {
		return err
	}
	if err := validateContainerHostIsolation(document.HostConfig); err != nil {
		return err
	}
	if err := validateContainerMount(document, request.SourceSnapshotDir); err != nil {
		return err
	}
	if document.Config == nil {
		return errors.New("recovery container config is missing")
	}
	for key, expected := range request.ContainerLabels {
		if document.Config.Labels[key] != expected {
			return fmt.Errorf("recovery container label %q drifted", key)
		}
	}
	return nil
}

func (runner *FreshContainerRunner) terminateUnprovedRecovery(
	ctx context.Context,
	request FreshContainerRecoveryRequest,
	result FreshContainerResult,
	cause error,
) (FreshContainerResult, error) {
	cleanupContext, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_, killErr := runner.docker.runner.Run(cleanupContext, "kill", result.Container.ContainerID)
	if killErr == nil {
		result.Killed = true
		result.KillProofDigest = digestBytes([]byte("killed\n" + result.Container.ContainerID + "\n"))
	}
	_, waitErr := runner.docker.runner.Run(cleanupContext, "wait", result.Container.ContainerID)
	freshRequest := FreshContainerRequest{
		Image:             gate.ImageIdentity{ConfigDigest: request.ConfigDigest},
		SourceSnapshotDir: request.SourceSnapshotDir, Profile: request.Profile,
		GateID: request.GateID, ContainerLabels: request.ContainerLabels,
		Deadline: request.Deadline, LifecycleHook: request.LifecycleHook,
	}
	result, removeErr := runner.removeContainer(cleanupContext, result, freshRequest, nil)
	return result, errors.Join(cause, killErr, waitErr, removeErr)
}
