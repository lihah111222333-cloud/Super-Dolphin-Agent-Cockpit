package localci

import (
	"context"
	"encoding/json"
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
	PlanExecution     bool
	ShardGateIDs      []gate.GateID
	ShardIdentity     string
	ContainerLabels   map[string]string
	Deadline          time.Time
	ClaimDeadline     func(context.Context, time.Time) (time.Time, error)
	LifecycleHook     FreshContainerLifecycleHook
}

// FreshContainerLifecyclePhase 标识可持久化且可幂等重放的 Docker 边界。
type FreshContainerLifecyclePhase string

const (
	FreshContainerPhasePrepared       FreshContainerLifecyclePhase = "prepared"
	FreshContainerPhaseCreating       FreshContainerLifecyclePhase = "creating"
	FreshContainerPhaseCreated        FreshContainerLifecyclePhase = "created"
	FreshContainerPhaseStarting       FreshContainerLifecyclePhase = "starting"
	FreshContainerPhaseStarted        FreshContainerLifecyclePhase = "started"
	FreshContainerPhaseExited         FreshContainerLifecyclePhase = "exited"
	FreshContainerPhaseRemovalPending FreshContainerLifecyclePhase = "removal_pending"
	FreshContainerPhaseRemoved        FreshContainerLifecyclePhase = "removed"
)

// FreshContainerLifecycleEvent 在不可逆 Docker 边界后同步提交 durable owner hook。
type FreshContainerLifecycleEvent struct {
	Phase                 FreshContainerLifecyclePhase
	ContainerID           string
	ImageReference        string
	ConfigDigest          string
	HostConfigDigest      string
	ResourceWitness       gate.ContainerResourceWitness
	ResourceWitnessDigest string
	SourceSnapshotDir     string
	StartedAt             time.Time
	Deadline              time.Time
	ExitedAt              time.Time
	CompletedAt           time.Time
	ExitCode              int
	RemovalProofDigest    string
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
	PlanGateResults    []FreshPlanGateResult
	ExitCode           int
	StartedAt          time.Time
	ExitedAt           time.Time
	CompletedAt        time.Time
	Deadline           time.Time
	Killed             bool
	KillProofDigest    string
	LogOutput          []byte
	LogDigest          string
	RemovalProofDigest string
}

// FreshPlanGateResult 将受信 executor 的单 gate 观察绑定到有界原始日志。
type FreshPlanGateResult struct {
	GateResult gate.GateResult
	Status     gate.ResultStatus
	LogOutput  []byte
}

// MaxFreshContainerLogBytes 限制单个 fresh container 可进入持久化证据边界的日志大小。
const MaxFreshContainerLogBytes = 1 << 20

const maxFreshPlanContainerLogBytes = 16 << 20

// freshContainerPreStartTimeout bounds image inspection, create, and the
// durable pre-start transition. The execution clock remains anchored at Starting.
const freshContainerPreStartTimeout = 5 * time.Minute

const freshContainerLifecycleCleanupTimeout = 30 * time.Second

const (
	freshContainerOperationIdentityPrefix = "super-dolphin-create-"
	creatingAbsenceProofs                 = 3
	creatingAbsenceRetry                  = 10 * time.Millisecond
)

// FreshContainerRunner creates one new container for every RunFreshContainer call.
type FreshContainerRunner struct {
	docker                  *dockerExecutor
	now                     func() time.Time
	lifecycleCleanupTimeout time.Duration
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
	return &FreshContainerRunner{docker: executor, now: time.Now, lifecycleCleanupTimeout: freshContainerLifecycleCleanupTimeout}, nil
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
	provisionCtx, cancelProvision := platformconfig.WithTimeout(ctx, freshContainerPreStartTimeout)
	defer cancelProvision()
	prepared, err := runner.prepareRequest(request)
	if err != nil {
		return result, err
	}
	result.ImageReference = prepared.imageReference
	imageDigest, err := runner.inspectAndVerifyImage(provisionCtx, prepared)
	if err != nil {
		return result, err
	}
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: imageDigest})
	return runner.runPreparedContainer(provisionCtx, ctx, request, prepared, result)
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
	prepared.expectedImage = freshContainerExpectedImageMetadata(request)
	container := newContainerRequest(prepared.imageReference, request.SourceSnapshotDir, command, request.Profile == gate.ProfileRelease, request.ContainerLabels)
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
	if err := validateFreshContainerPlanBinding(request); err != nil {
		return nil, err
	}
	if err := validateFreshContainerShardContract(request); err != nil {
		return nil, err
	}
	command, err := freshContainerCommand(request)
	if err != nil {
		return nil, err
	}
	identity := gateImageIdentityReader{ImageIdentity: request.Image}
	expected := freshContainerExpectedImageMetadata(request)
	if err := validateImageIdentity(identity, expected.labels(), expected); err != nil {
		return nil, err
	}
	return command, nil
}

// validateFreshContainerPlanBinding 校验 profile、计划与源码树的同一请求绑定。
func validateFreshContainerPlanBinding(request FreshContainerRequest) error {
	if err := request.Profile.Validate(); err != nil {
		return err
	}
	if err := request.Plan.Validate(); err != nil {
		return fmt.Errorf("gate plan: %w", err)
	}
	if request.Plan.Profile != request.Profile {
		return errors.New("request profile does not match gate plan")
	}
	if !gitObjectPattern.MatchString(request.SourceTreeSHA) || request.Plan.Source.SourceTreeSHA != request.SourceTreeSHA {
		return errors.New("request source_tree_sha does not match gate plan")
	}
	return nil
}

// validateFreshContainerShardContract 确保 shard 三字段要么全部缺失，要么完整可执行。
func validateFreshContainerShardContract(request FreshContainerRequest) error {
	hasShardFields := len(request.ShardGateIDs) != 0 || request.ShardIdentity != "" || request.ClaimDeadline != nil
	if hasShardFields && (request.PlanExecution || len(request.ShardGateIDs) == 0 || request.ShardIdentity == "" || request.ClaimDeadline == nil) {
		return errors.New("shard execution requires exact gates, identity, and durable deadline claim")
	}
	return nil
}

func freshContainerExpectedImageMetadata(request FreshContainerRequest) expectedImageMetadata {
	return expectedImageMetadata{
		PolicyDigest: request.ImageTruth.PolicyDigest, SourceTreeSHA: request.ImageTruth.BuildSourceTreeSHA,
		InputDigest: request.ImageTruth.InputDigest, ToolchainDigest: request.ImageTruth.ToolchainDigest,
		SchemaVersion: request.ImageTruth.SchemaVersion, OS: request.Image.OS,
		Architecture: request.Image.Architecture, Variant: request.Image.Variant,
	}
}

func newContainerRequest(image string, sourceDir string, command []string, release bool, labels map[string]string) containerRequest {
	return containerRequest{
		Image: image, SourceDir: sourceDir, Command: command,
		Release: release, Labels: labels,
	}
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

func isTypedNil(value any) bool {
	if value == nil {
		return true
	}
	kind := reflect.ValueOf(value).Kind()
	return slices.Contains([]reflect.Kind{reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice}, kind) && reflect.ValueOf(value).IsNil()
}

// runPreparedContainer 按 prepared、create、start、exit、remove 顺序持久化单次执行。
func (runner *FreshContainerRunner) runPreparedContainer(provisionContext context.Context, parentContext context.Context, request FreshContainerRequest, prepared preparedFreshContainerRequest, result FreshContainerResult) (FreshContainerResult, error) {
	result, err := runner.createPreparedContainer(provisionContext, parentContext, request, prepared, result)
	if err != nil {
		return result, err
	}
	return runner.startPreparedContainer(provisionContext, parentContext, request, prepared, result)
}

// createPreparedContainer 按顺序写入创建生命周期证据并验证已创建容器。
func (runner *FreshContainerRunner) createPreparedContainer(provisionContext context.Context, parentContext context.Context, request FreshContainerRequest, prepared preparedFreshContainerRequest, result FreshContainerResult) (FreshContainerResult, error) {
	if err := runner.emitLifecycle(provisionContext, request, result, FreshContainerPhasePrepared); err != nil {
		return result, err
	}
	if err := runner.emitLifecycle(provisionContext, request, result, FreshContainerPhaseCreating); err != nil {
		return result, err
	}
	containerID, err := runner.docker.create(provisionContext, newContainerRequest(prepared.imageReference, request.SourceSnapshotDir, prepared.command, request.Profile == gate.ProfileRelease, request.ContainerLabels))
	result.setContainerID(containerID)
	if containerID == "" {
		return result, err
	}
	if hookErr := runner.emitLifecycle(provisionContext, request, result, FreshContainerPhaseCreated); hookErr != nil {
		return runner.removeContainer(parentContext, result, request, errors.Join(err, hookErr))
	}
	if err != nil {
		return runner.removeContainer(parentContext, result, request, err)
	}
	createdEvidence, err := runner.inspectCreatedContainer(
		provisionContext, containerID, prepared.imageReference, request.Image.ConfigDigest,
		request.SourceSnapshotDir, prepared.command, request.ContainerLabels,
	)
	if err != nil {
		return runner.removeContainer(parentContext, result, request, err)
	}
	result.Container.HostConfigDigest = createdEvidence.hostConfigDigest
	result.Container.ResourceWitness = createdEvidence.resourceWitness
	result.Container.ResourceWitnessDigest = createdEvidence.resourceWitnessDigest
	result.Container.NetworkPolicyDigest = digestBytes([]byte("network=none\n"))
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: createdEvidence.inspectDigest})
	return result, nil
}

// startPreparedContainer 先认领共享 deadline，再启动并完成容器执行。
func (runner *FreshContainerRunner) startPreparedContainer(provisionContext context.Context, parentContext context.Context, request FreshContainerRequest, prepared preparedFreshContainerRequest, result FreshContainerResult) (FreshContainerResult, error) {
	runner.initializeExecutionTiming(&result, request)
	if request.ClaimDeadline != nil {
		deadline, claimErr := request.ClaimDeadline(provisionContext, result.StartedAt)
		if claimErr != nil {
			return runner.removeContainer(parentContext, result, request, claimErr)
		}
		result.Deadline = deadline
	}
	if hookErr := runner.emitLifecycle(provisionContext, request, result, FreshContainerPhaseStarting); hookErr != nil {
		return runner.removeContainer(parentContext, result, request, hookErr)
	}
	runContext, cancel := context.WithDeadline(parentContext, result.Deadline)
	defer cancel()
	if _, err := runner.docker.runner.Run(runContext, "start", result.Container.ContainerID); err != nil {
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

// initializeExecutionTiming starts the execution clock only after the created container is verified.
func (runner *FreshContainerRunner) initializeExecutionTiming(result *FreshContainerResult, request FreshContainerRequest) {
	result.StartedAt = runner.now().UTC()
	result.Deadline = request.Deadline.UTC()
	if result.Deadline.IsZero() {
		result.Deadline = result.StartedAt.Add(executionTimeout(request.Profile == gate.ProfileRelease))
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
	evidenceErr := runner.collectFinishedContainerEvidence(parentContext, &result, prepared, request)
	result.CompletedAt = runner.now().UTC()
	hookErr := runner.emitCleanupLifecycle(parentContext, request, result, FreshContainerPhaseExited)
	if hookErr != nil || evidenceErr != nil {
		result.Status = gate.ResultStatusInfraFailed
		result.GateResult = nil
		runErr = errors.Join(runErr, hookErr, evidenceErr)
	}
	result, cleanupErr := runner.removeContainer(parentContext, result, request, runErr)
	if !result.Container.Removed || result.Status == gate.ResultStatusInfraFailed {
		result.Status = gate.ResultStatusInfraFailed
		result.GateResult = nil
		return result, cleanupErr
	}
	if canonicalReportExecution(request) {
		return finishCanonicalReport(result, request, runErr)
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

// collectFinishedContainerEvidence 收集有界日志、plan report 与不可变容器终态证据。
func (runner *FreshContainerRunner) collectFinishedContainerEvidence(
	ctx context.Context,
	result *FreshContainerResult,
	prepared preparedFreshContainerRequest,
	request FreshContainerRequest,
) error {
	logErr := runner.collectContainerLog(ctx, result, canonicalReportExecution(request))
	if logErr == nil {
		logErr = collectFinishedPlanGateResults(result, request)
	}
	stateDigest, exitedAt, inspectErr := runner.inspectFinishedContainer(
		ctx, result.Container.ContainerID, prepared.imageReference, request.Image.ConfigDigest, result.ExitCode,
	)
	if !exitedAt.IsZero() {
		result.ExitedAt = exitedAt
	}
	if inspectErr == nil {
		result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: stateDigest})
	}
	return errors.Join(logErr, inspectErr)
}

func (runner *FreshContainerRunner) collectContainerLog(parentContext context.Context, result *FreshContainerResult, reportExecution bool) error {
	logOutput, err := runner.runCleanup(parentContext, "logs", "--timestamps", result.Container.ContainerID)
	if err != nil {
		return err
	}
	limit := MaxFreshContainerLogBytes
	if reportExecution {
		limit = maxFreshPlanContainerLogBytes
	}
	result.LogOutput, err = validateFreshContainerLogLimit([]byte(logOutput), limit)
	if err != nil {
		return err
	}
	result.LogDigest = digestBytes(result.LogOutput)
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindLog, Digest: result.LogDigest})
	return nil
}

func validateFreshContainerLogLimit(logOutput []byte, limit int) ([]byte, error) {
	if len(logOutput) > limit {
		return nil, fmt.Errorf("container log exceeds %d-byte evidence limit", limit)
	}
	return append([]byte(nil), logOutput...), nil
}

// removeContainer 只有在 Docker 列表证明容器消失后才写入 removal proof。
func (runner *FreshContainerRunner) removeContainer(parentContext context.Context, result FreshContainerResult, request FreshContainerRequest, runErr error) (FreshContainerResult, error) {
	if result.Container.ContainerID == "" {
		return result, runErr
	}
	if hookErr := runner.emitCleanupLifecycle(parentContext, request, result, FreshContainerPhaseRemovalPending); hookErr != nil {
		result.Status = gate.ResultStatusInfraFailed
		return result, errors.Join(runErr, hookErr)
	}
	if err := runner.docker.remove(parentContext, result.Container.ContainerID); err != nil {
		return failContainerRemoval(result, runErr, fmt.Errorf("remove gate container: %w", err))
	}
	output, err := runner.runCleanup(parentContext, "ps", "--all", "--no-trunc", "--filter=id="+result.Container.ContainerID, "--format={{.ID}}")
	if err != nil {
		return failContainerRemoval(result, runErr, fmt.Errorf("prove gate container removal: %w", err))
	}
	if strings.TrimSpace(output) != "" {
		return failContainerRemoval(result, runErr, errors.New("removed gate container is still listed by Docker"))
	}
	result.Container.Removed = true
	result.RemovalProofDigest = digestBytes([]byte("removed\n" + result.Container.ContainerID + "\n"))
	result.Evidence = append(result.Evidence, gate.Evidence{Kind: gate.EvidenceKindDocker, Digest: result.RemovalProofDigest})
	if hookErr := runner.emitCleanupLifecycle(parentContext, request, result, FreshContainerPhaseRemoved); hookErr != nil {
		result.Status = gate.ResultStatusInfraFailed
		return result, errors.Join(runErr, hookErr)
	}
	return result, runErr
}

func failContainerRemoval(result FreshContainerResult, runErr error, cause error) (FreshContainerResult, error) {
	result.Status = gate.ResultStatusInfraFailed
	return result, errors.Join(runErr, cause)
}

// emitLifecycle 在 Docker 边界后同步提交可持久化 lifecycle 事件。
func (runner *FreshContainerRunner) emitLifecycle(
	ctx context.Context,
	request FreshContainerRequest,
	result FreshContainerResult,
	phase FreshContainerLifecyclePhase,
) error {
	containerID, err := freshContainerLifecycleIdentity(request, result, phase)
	if err != nil {
		return err
	}
	event := FreshContainerLifecycleEvent{
		Phase: phase, ContainerID: containerID,
		ImageReference: result.ImageReference, ConfigDigest: request.Image.ConfigDigest,
		HostConfigDigest: result.Container.HostConfigDigest,
		ResourceWitness:  result.Container.ResourceWitness, ResourceWitnessDigest: result.Container.ResourceWitnessDigest,
		SourceSnapshotDir: request.SourceSnapshotDir, StartedAt: result.StartedAt,
		Deadline: result.Deadline, ExitedAt: result.ExitedAt, CompletedAt: result.CompletedAt, ExitCode: result.ExitCode,
		RemovalProofDigest: result.RemovalProofDigest,
	}
	if err := validateFreshContainerLifecycleEvent(event, result.Status); err != nil {
		return err
	}
	if request.LifecycleHook == nil {
		return nil
	}
	if err := request.LifecycleHook(ctx, event); err != nil {
		return fmt.Errorf("persist container lifecycle phase %q: %w", phase, err)
	}
	return nil
}

// freshContainerLifecycleIdentity persists an operation identity before Docker
// can return the runtime container ID, and reuses it for absence completion.
func freshContainerLifecycleIdentity(
	request FreshContainerRequest,
	result FreshContainerResult,
	phase FreshContainerLifecyclePhase,
) (string, error) {
	if phase == FreshContainerPhaseCreating {
		return FreshContainerOperationIdentity(request.ContainerLabels)
	}
	return result.Container.ContainerID, nil
}

// FreshContainerOperationIdentity 从完整不可变 labels 闭包派生创建前操作身份。
func FreshContainerOperationIdentity(labels map[string]string) (string, error) {
	if len(labels) == 0 {
		return "", errors.New("container labels are required for operation identity")
	}
	for key, value := range labels {
		if key == "" || value == "" {
			return "", errors.New("container operation identity labels must be non-empty")
		}
	}
	canonical, err := json.Marshal(labels)
	if err != nil {
		return "", fmt.Errorf("encode container operation identity labels: %w", err)
	}
	return freshContainerOperationIdentityPrefix + strings.TrimPrefix(digestBytes(canonical), "sha256:"), nil
}

// IsFreshContainerOperationIdentity 判断持久身份是否表示未完成的创建操作。
func IsFreshContainerOperationIdentity(value string) bool {
	if !strings.HasPrefix(value, freshContainerOperationIdentityPrefix) {
		return false
	}
	digest := strings.TrimPrefix(value, freshContainerOperationIdentityPrefix)
	if len(digest) != 64 {
		return false
	}
	for _, character := range digest {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
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

// buildGateResult 将容器终态映射为命令与日志摘要闭合的单 gate 结果。
func buildGateResult(gateID gate.GateID, command []string, result FreshContainerResult) (*gate.GateResult, error) {
	var status gate.GateStatus
	switch result.Status {
	case gate.ResultStatusPassed:
		status = gate.GateStatusPassed
	case gate.ResultStatusFailed:
		status = gate.GateStatusFailed
	case gate.ResultStatusCancelled:
		status = gate.GateStatusCancelled
	case gate.ResultStatusTimeout:
		status = gate.GateStatusTimeout
	default:
		return nil, fmt.Errorf("unsupported gate result status %q", result.Status)
	}
	encodedArgv, err := json.Marshal(command)
	if err != nil {
		return nil, fmt.Errorf("digest gate command: %w", err)
	}
	return &gate.GateResult{
		GateID: string(gateID), Status: status, ExitCode: result.ExitCode,
		StartedAt: result.StartedAt, CompletedAt: result.CompletedAt,
		ArgvDigest: digestBytes(encodedArgv), LogDigest: result.LogDigest,
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
	RemovalPending    bool
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
	freshRequest := freshContainerRequestForRecovery(request)
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
		exitCode, parseErr := parseContainerExitCode(output)
		if parseErr != nil {
			return runner.terminateUnprovedRecovery(ctx, request, result, parseErr)
		}
		status, runErr := recoveredContainerExit(exitCode)
		result.ExitCode = exitCode
		return runner.finishContainer(ctx, result, prepared, freshRequest, status, runErr)
	default:
		return runner.terminateUnprovedRecovery(
			ctx, request, result, fmt.Errorf("recovered container state %q is not observable", document.State.Status),
		)
	}
}

func recoveredContainerExit(exitCode int) (gate.ResultStatus, error) {
	if exitCode != 0 {
		return gate.ResultStatusFailed, fmt.Errorf("recovered gate container exited with code %d", exitCode)
	}
	return gate.ResultStatusPassed, nil
}
