package eci

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	// ArtifactBuilderProvider 是 portable source-baseline builder 唯一允许的执行提供方。
	ArtifactBuilderProvider = cicontract.ExecutionProviderID
	// ArtifactBuilderArtifactKind 是 builder 产出的唯一 portable artifact byte domain。
	ArtifactBuilderArtifactKind = "source-baseline-git-bundle/v1"
	// ArtifactBuilderArtifactRef 是 baseline bundle 唯一允许广告的 ref。
	ArtifactBuilderArtifactRef = "refs/source/baseline"
	// ArtifactBuilderCommand 是镜像内唯一允许执行的 source-baseline builder 命令。
	ArtifactBuilderCommand = "/usr/local/bin/super-dolphin-remote-ci-artifact-builder"
	// ArtifactBuilderScheduleStrategy 是 builder 请求固定的多可用区调度策略。
	ArtifactBuilderScheduleStrategy = cicontract.ECIMultiZoneScheduleStrategy
	// ArtifactBuilderEphemeralStorageGiB 是每个 builder ECI group 的固定临时盘容量。
	ArtifactBuilderEphemeralStorageGiB = cicontract.ECIEphemeralStorageGiB
	// ArtifactBuilderCPU 和 ArtifactBuilderMemoryGiB 是 builder 独立使用的固定资源规格。
	ArtifactBuilderCPU       = cicontract.CalibrationResourceCPU
	ArtifactBuilderMemoryGiB = cicontract.CalibrationResourceMemoryGiB
	// ArtifactBuilderMaxLogBytes 是 builder 日志观察的有界上限。
	ArtifactBuilderMaxLogBytes = 1 << 20
)

var (
	artifactBuilderTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	artifactBuilderNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	artifactBuilderImagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*@sha256:[0-9a-f]{64}$`)
)

// ErrArtifactBuilderInvalidState 表示 builder lifecycle 触发了非法迁移。
var ErrArtifactBuilderInvalidState = errors.New("artifact builder lifecycle state transition is invalid")

// ErrArtifactBuilderStillPresent 表示 cleanup 的 absence proof 尚未成立。
var ErrArtifactBuilderStillPresent = errors.New("artifact builder container group is still present")

// ArtifactBuilderInput 绑定输入对象及 portable baseline 的全部内容身份。
type ArtifactBuilderInput struct {
	Key             string `json:"input_key"`
	SHA256          string `json:"input_sha256"`
	SizeBytes       int64  `json:"input_size_bytes"`
	RepositoryID    string `json:"repository_id"`
	ObjectFormat    string `json:"object_format"`
	SourceCommitSHA string `json:"source_commit_sha"`
	SourceTreeSHA   string `json:"source_tree_sha"`
	BuilderDigest   string `json:"builder_digest"`
	ToolchainDigest string `json:"toolchain_digest"`
}

// ArtifactBuilderConfig 是构造请求所需的显式、非 secret ECI 绑定输入。
// 固定协议字段由 NewArtifactBuilderRequest 写入，身份、资源和输入字段不得省略。
type ArtifactBuilderConfig struct {
	Provider            string
	RegionID            string
	ContainerGroupName  string
	ContainerName       string
	Image               string
	ImageSnapshotID     string
	EphemeralStorageGiB int64
	CPU                 float64
	MemoryGiB           float64
	VSwitches           []cicontract.ECIVSwitch
	SecurityGroupID     string
	RAMRoleName         string
	Input               ArtifactBuilderInput
}

// ArtifactBuilderRequest 是独立于 normal shard Runtime 的 builder-specific typed request。
// 它不携带凭据、签名 URL、宿主路径、workload 命令或任何 fallback 选择。
type ArtifactBuilderRequest struct {
	Provider            string
	RegionID            string
	ContainerGroupName  string
	ContainerName       string
	Image               string
	ImageSnapshotID     string
	EphemeralStorageGiB int64
	CPU                 float64
	MemoryGiB           float64
	VSwitches           []cicontract.ECIVSwitch
	ScheduleStrategy    string
	SecurityGroupID     string
	RAMRoleName         string
	ArtifactKind        string
	ArtifactRef         string
	Command             []string
	Input               ArtifactBuilderInput
}

// NewArtifactBuilderRequest 构造并校验一个固定 source-baseline builder 请求。
// 所有运行时身份来自 config；固定 kind/ref/command 不是可变默认值。
func NewArtifactBuilderRequest(config ArtifactBuilderConfig) (ArtifactBuilderRequest, error) {
	request := ArtifactBuilderRequest{
		Provider:            config.Provider,
		RegionID:            config.RegionID,
		ContainerGroupName:  config.ContainerGroupName,
		ContainerName:       config.ContainerName,
		Image:               config.Image,
		ImageSnapshotID:     config.ImageSnapshotID,
		EphemeralStorageGiB: config.EphemeralStorageGiB,
		CPU:                 config.CPU,
		MemoryGiB:           config.MemoryGiB,
		VSwitches:           append([]cicontract.ECIVSwitch(nil), config.VSwitches...),
		ScheduleStrategy:    ArtifactBuilderScheduleStrategy,
		SecurityGroupID:     config.SecurityGroupID,
		RAMRoleName:         config.RAMRoleName,
		ArtifactKind:        ArtifactBuilderArtifactKind,
		ArtifactRef:         ArtifactBuilderArtifactRef,
		Command:             []string{ArtifactBuilderCommand},
		Input:               config.Input,
	}
	if err := request.Validate(); err != nil {
		return ArtifactBuilderRequest{}, err
	}
	return request, nil
}

// BuildArtifactBuilderRequest 是 NewArtifactBuilderRequest 的语义化别名。
func BuildArtifactBuilderRequest(config ArtifactBuilderConfig) (ArtifactBuilderRequest, error) {
	return NewArtifactBuilderRequest(config)
}

// ValidateArtifactBuilderRequest 校验 builder request 的 provider、资源和输入身份。
func ValidateArtifactBuilderRequest(request ArtifactBuilderRequest) error {
	return request.Validate()
}

// Validate 校验一个 builder request，不执行任何云调用或 workload。
func (request ArtifactBuilderRequest) Validate() error {
	checks := []func() error{
		request.validateProvider,
		request.validateNamesAndImage,
		request.validateResources,
		request.validateScheduling,
		request.validateInfrastructureIdentity,
		request.validateProtocol,
		request.Input.Validate,
	}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (request ArtifactBuilderRequest) validateProvider() error {
	if request.Provider != ArtifactBuilderProvider {
		return fmt.Errorf("artifact builder provider must be %q", ArtifactBuilderProvider)
	}
	if !artifactBuilderTokenPattern.MatchString(request.RegionID) {
		return errors.New("artifact builder region is required and must be a canonical token")
	}
	return nil
}

// validateNamesAndImage 校验 builder group/container 名称及 immutable image 身份。
func (request ArtifactBuilderRequest) validateNamesAndImage() error {
	if !artifactBuilderNamePattern.MatchString(request.ContainerGroupName) || !artifactBuilderNamePattern.MatchString(request.ContainerName) || request.ContainerGroupName == request.ContainerName {
		return errors.New("artifact builder container group and container names are invalid or not distinct")
	}
	if !artifactBuilderImagePattern.MatchString(request.Image) || strings.ContainsAny(request.Image, "?#") {
		return errors.New("artifact builder image must be an immutable sha256 digest reference")
	}
	if !validArtifactBuilderSnapshot(request.ImageSnapshotID) {
		return errors.New("artifact builder image snapshot identity is required")
	}
	return nil
}

func validArtifactBuilderSnapshot(value string) bool {
	if !artifactBuilderTokenPattern.MatchString(value) {
		return false
	}
	switch strings.ToLower(value) {
	case "latest", "current", "default", "auto":
		return false
	default:
		return true
	}
}

func (request ArtifactBuilderRequest) validateResources() error {
	if request.EphemeralStorageGiB != ArtifactBuilderEphemeralStorageGiB || request.CPU != ArtifactBuilderCPU || request.MemoryGiB != ArtifactBuilderMemoryGiB {
		return errors.New("artifact builder resources must be exactly 4 CPU, 8 GiB memory, and 100 GiB ephemeral storage")
	}
	return nil
}

func (request ArtifactBuilderRequest) validateScheduling() error {
	if err := cicontract.ValidateECIMultiZoneScheduling(request.ScheduleStrategy, request.VSwitches); err != nil {
		return fmt.Errorf("artifact builder scheduling: %w", err)
	}
	return nil
}

func (request ArtifactBuilderRequest) validateInfrastructureIdentity() error {
	for name, value := range map[string]string{
		"security group ID": request.SecurityGroupID,
		"RAM role name":     request.RAMRoleName,
	} {
		if !artifactBuilderTokenPattern.MatchString(value) {
			return fmt.Errorf("artifact builder %s is required and must be a canonical token", name)
		}
	}
	return nil
}

func (request ArtifactBuilderRequest) validateProtocol() error {
	if request.ArtifactKind != ArtifactBuilderArtifactKind || request.ArtifactRef != ArtifactBuilderArtifactRef {
		return errors.New("artifact builder kind or ref is not the portable source-baseline contract")
	}
	if len(request.Command) != 1 || request.Command[0] != ArtifactBuilderCommand {
		return errors.New("artifact builder command must be the fixed portable source-baseline builder")
	}
	return nil
}

// Validate 校验输入对象 key、Git object identity 与 producer/toolchain digest。
func (input ArtifactBuilderInput) Validate() error {
	if !validArtifactBuilderObjectKey(input.Key) {
		return errors.New("artifact builder input key must be a clean repository-relative object key")
	}
	if !validArtifactBuilderDigest(input.SHA256) || !validArtifactBuilderDigest(input.BuilderDigest) || !validArtifactBuilderDigest(input.ToolchainDigest) {
		return errors.New("artifact builder input, builder, and toolchain digests must be canonical sha256 values")
	}
	if input.SizeBytes <= 0 || input.SizeBytes > 1<<30 {
		return errors.New("artifact builder input size must be between 1 byte and 1 GiB")
	}
	return input.validateGitIdentity()
}

// validateGitIdentity 校验 caller exact source commit/tree 与 object format。
func (input ArtifactBuilderInput) validateGitIdentity() error {
	if !artifactBuilderTokenPattern.MatchString(input.RepositoryID) {
		return errors.New("artifact builder repository identity is required")
	}
	objectLength := 0
	switch input.ObjectFormat {
	case "sha1":
		objectLength = 40
	case "sha256":
		objectLength = 64
	default:
		return errors.New("artifact builder object format must be sha1 or sha256")
	}
	if !validArtifactBuilderOID(input.SourceCommitSHA, objectLength) || !validArtifactBuilderOID(input.SourceTreeSHA, objectLength) {
		return errors.New("artifact builder source commit and tree identities are invalid")
	}
	return nil
}

// CommandLine 返回固定 builder 命令及其 typed input 参数。
func (request ArtifactBuilderRequest) CommandLine() ([]string, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	input := request.Input
	args := []string{
		request.Command[0],
		"--artifact-kind", request.ArtifactKind,
		"--artifact-ref", request.ArtifactRef,
		"--input-key", input.Key,
		"--input-sha256", input.SHA256,
		"--input-size-bytes", strconv.FormatInt(input.SizeBytes, 10),
		"--repository-id", input.RepositoryID,
		"--object-format", input.ObjectFormat,
		"--source-commit-sha", input.SourceCommitSHA,
		"--source-tree-sha", input.SourceTreeSHA,
		"--builder-digest", input.BuilderDigest,
		"--toolchain-digest", input.ToolchainDigest,
	}
	return args, nil
}

// validArtifactBuilderDigest 判断摘要是否为 canonical 小写 SHA-256。
func validArtifactBuilderDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// validArtifactBuilderOID 判断 Git OID 是否为指定长度的非零小写十六进制。
func validArtifactBuilderOID(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validArtifactBuilderObjectKey(value string) bool {
	if !validArtifactBuilderObjectKeyShape(value) || !validArtifactBuilderObjectKeyRelative(value) {
		return false
	}
	return !artifactBuilderObjectKeyContainsSecretMarker(value)
}

func validArtifactBuilderObjectKeyShape(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsAny(value, "=\\\x00\r\n\t?#%") && !strings.Contains(value, "://")
}

// validArtifactBuilderObjectKeyRelative 判断 OSS key 不含路径逃逸或空组件。
func validArtifactBuilderObjectKeyRelative(value string) bool {
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") {
		return false
	}
	if len(value) >= 2 && value[1] == ':' {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func artifactBuilderObjectKeyContainsSecretMarker(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"credential", "password", "secret", "token"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ArtifactBuilderActor 是未来 ECI adapter 的最小生命周期 port；本文件不提供云实现。
type ArtifactBuilderActor interface {
	Create(context.Context, ArtifactBuilderRequest) (ArtifactBuilderGroup, error)
	Describe(context.Context, string) (ArtifactBuilderGroup, error)
	Logs(context.Context, string, string) (string, error)
	Delete(context.Context, string) error
	ConfirmAbsent(context.Context, string) (bool, error)
}

// ArtifactBuilderGroup 是 builder lifecycle 所需的最小 live identity 与状态证据。
type ArtifactBuilderGroup struct {
	ID              string
	Name            string
	Provider        string
	RegionID        string
	Image           string
	ImageSnapshotID string
	Status          string
	Container       ArtifactBuilderContainer
}

// ArtifactBuilderContainer 是 builder 容器的状态、退出码和真实时间证据。
type ArtifactBuilderContainer struct {
	Name       string
	Image      string
	State      string
	ExitCode   *int64
	StartTime  time.Time
	FinishTime time.Time
}

const (
	// ArtifactBuilderGroupPending 表示 provider 尚未开始运行 builder。
	ArtifactBuilderGroupPending = "Pending"
	// ArtifactBuilderGroupCreating 表示 provider 正在创建 builder group。
	ArtifactBuilderGroupCreating = "Creating"
	// ArtifactBuilderGroupRunning 表示 builder 已开始运行。
	ArtifactBuilderGroupRunning = "Running"
	// ArtifactBuilderGroupSucceeded 表示 builder group 成功终态。
	ArtifactBuilderGroupSucceeded = "Succeeded"
	// ArtifactBuilderGroupFailed 表示 builder group 失败终态。
	ArtifactBuilderGroupFailed = "Failed"
	// ArtifactBuilderGroupTerminated 表示 provider 返回容器终态。
	ArtifactBuilderGroupTerminated = "Terminated"

	// ArtifactBuilderContainerRunning 表示容器仍在运行。
	ArtifactBuilderContainerRunning = "Running"
	// ArtifactBuilderContainerTerminated 表示容器已终止并应携带 exit evidence。
	ArtifactBuilderContainerTerminated = "Terminated"
)

// ArtifactBuilderState 是 builder 单 group 生命周期状态。
type ArtifactBuilderState string

const (
	// ArtifactBuilderStateNew 表示尚未取得 provider group identity。
	ArtifactBuilderStateNew ArtifactBuilderState = "new"
	// ArtifactBuilderStateCreated 表示 group 已创建但尚未运行。
	ArtifactBuilderStateCreated ArtifactBuilderState = "created"
	// ArtifactBuilderStateRunning 表示 builder 正在运行。
	ArtifactBuilderStateRunning ArtifactBuilderState = "running"
	// ArtifactBuilderStateSucceeded 表示 builder 已以零 exit code 成功完成。
	ArtifactBuilderStateSucceeded ArtifactBuilderState = "succeeded"
	// ArtifactBuilderStateFailed 表示 builder 已进入失败终态。
	ArtifactBuilderStateFailed ArtifactBuilderState = "failed"
	// ArtifactBuilderStateDeleteRequested 表示已发起唯一 group cleanup。
	ArtifactBuilderStateDeleteRequested ArtifactBuilderState = "delete_requested"
	// ArtifactBuilderStateAbsent 表示 provider 已明确确认 group 消失。
	ArtifactBuilderStateAbsent ArtifactBuilderState = "absent"
)

// ArtifactBuilderLifecycle 只保存单个 builder group 的本地状态观察，不调用 provider。
type ArtifactBuilderLifecycle struct {
	request ArtifactBuilderRequest
	group   ArtifactBuilderGroup
	logs    string
	state   ArtifactBuilderState
}

// NewArtifactBuilderLifecycle 创建一个尚未产生云副作用的 builder 状态机。
func NewArtifactBuilderLifecycle(request ArtifactBuilderRequest) (*ArtifactBuilderLifecycle, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return &ArtifactBuilderLifecycle{request: cloneArtifactBuilderRequest(request), state: ArtifactBuilderStateNew}, nil
}

// Request 返回状态机绑定的不可变 request 副本。
func (lifecycle *ArtifactBuilderLifecycle) Request() ArtifactBuilderRequest {
	if lifecycle == nil {
		return ArtifactBuilderRequest{}
	}
	return cloneArtifactBuilderRequest(lifecycle.request)
}

// State 返回当前 builder lifecycle 状态。
func (lifecycle *ArtifactBuilderLifecycle) State() ArtifactBuilderState {
	if lifecycle == nil {
		return ""
	}
	return lifecycle.state
}

// Group 返回最近一次通过 live identity 校验的 group 副本。
func (lifecycle *ArtifactBuilderLifecycle) Group() ArtifactBuilderGroup {
	if lifecycle == nil {
		return ArtifactBuilderGroup{}
	}
	return cloneArtifactBuilderGroup(lifecycle.group)
}

// Logs 返回最近一次通过有界校验的日志副本。
func (lifecycle *ArtifactBuilderLifecycle) Logs() string {
	if lifecycle == nil {
		return ""
	}
	return lifecycle.logs
}

// ObserveCreated 接收 Create 的单个 group 结果并校验 live identity。
func (lifecycle *ArtifactBuilderLifecycle) ObserveCreated(group ArtifactBuilderGroup) error {
	if lifecycle == nil || lifecycle.state != ArtifactBuilderStateNew {
		return ErrArtifactBuilderInvalidState
	}
	next, err := lifecycle.observeGroup(group)
	if err != nil {
		return err
	}
	lifecycle.group = cloneArtifactBuilderGroup(group)
	lifecycle.state = next
	return nil
}

// Observe 接收后续 Describe 的单个 group 结果并拒绝回退或第二个 group。
func (lifecycle *ArtifactBuilderLifecycle) Observe(group ArtifactBuilderGroup) error {
	if lifecycle == nil || (lifecycle.state != ArtifactBuilderStateCreated && lifecycle.state != ArtifactBuilderStateRunning) {
		return ErrArtifactBuilderInvalidState
	}
	if group.ID != lifecycle.group.ID {
		return errors.New("artifact builder lifecycle cannot observe a second container group")
	}
	next, err := lifecycle.observeGroup(group)
	if err != nil {
		return err
	}
	if !validArtifactBuilderObservationTransition(lifecycle.state, next) {
		return fmt.Errorf("%w: %s to %s", ErrArtifactBuilderInvalidState, lifecycle.state, next)
	}
	lifecycle.group = cloneArtifactBuilderGroup(group)
	lifecycle.state = next
	return nil
}

// ObserveLogs 接收 bounded builder logs；超限日志必须失败而不能静默截断。
func (lifecycle *ArtifactBuilderLifecycle) ObserveLogs(logs string) error {
	if lifecycle == nil || lifecycle.state == ArtifactBuilderStateNew || lifecycle.state == ArtifactBuilderStateAbsent || lifecycle.state == ArtifactBuilderStateDeleteRequested {
		return ErrArtifactBuilderInvalidState
	}
	if len([]byte(logs)) > ArtifactBuilderMaxLogBytes {
		return errors.New("artifact builder logs exceed bounded byte limit")
	}
	lifecycle.logs = logs
	return nil
}

// MarkDeleteRequested 记录成功调用 Delete 后的 cleanup 阶段。
func (lifecycle *ArtifactBuilderLifecycle) MarkDeleteRequested() error {
	if lifecycle == nil || (lifecycle.state != ArtifactBuilderStateSucceeded && lifecycle.state != ArtifactBuilderStateFailed) {
		return ErrArtifactBuilderInvalidState
	}
	lifecycle.state = ArtifactBuilderStateDeleteRequested
	return nil
}

// ConfirmAbsent 只接受 provider 明确返回的 absence proof。
func (lifecycle *ArtifactBuilderLifecycle) ConfirmAbsent(absent bool) error {
	if lifecycle == nil || lifecycle.state != ArtifactBuilderStateDeleteRequested {
		return ErrArtifactBuilderInvalidState
	}
	if !absent {
		return ErrArtifactBuilderStillPresent
	}
	lifecycle.state = ArtifactBuilderStateAbsent
	return nil
}

// observeGroup 校验 live identity、终态时间和 exit code，并返回下一个本地状态。
func (lifecycle *ArtifactBuilderLifecycle) observeGroup(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if err := validateArtifactBuilderGroupIdentity(lifecycle.request, group); err != nil {
		return "", err
	}
	switch group.Status {
	case ArtifactBuilderGroupPending, ArtifactBuilderGroupCreating:
		return artifactBuilderPendingState(group)
	case ArtifactBuilderGroupRunning:
		return artifactBuilderRunningState(group)
	case ArtifactBuilderGroupSucceeded:
		return artifactBuilderTerminalState(group, true)
	case ArtifactBuilderGroupFailed:
		return artifactBuilderTerminalState(group, false)
	case ArtifactBuilderGroupTerminated:
		return artifactBuilderTerminatedState(group)
	default:
		return "", fmt.Errorf("artifact builder group status %q is unsupported", group.Status)
	}
}

func artifactBuilderPendingState(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if group.Container.State != "" {
		return "", errors.New("artifact builder pending group must not claim a container state")
	}
	return ArtifactBuilderStateCreated, nil
}

func artifactBuilderRunningState(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if group.Container.State != ArtifactBuilderContainerRunning {
		return "", errors.New("artifact builder running group must report a running container")
	}
	return ArtifactBuilderStateRunning, nil
}

func artifactBuilderTerminalState(group ArtifactBuilderGroup, success bool) (ArtifactBuilderState, error) {
	if err := validateArtifactBuilderTerminal(group, success); err != nil {
		return "", err
	}
	if success {
		return ArtifactBuilderStateSucceeded, nil
	}
	return ArtifactBuilderStateFailed, nil
}

func artifactBuilderTerminatedState(group ArtifactBuilderGroup) (ArtifactBuilderState, error) {
	if err := validateArtifactBuilderTerminal(group, group.Container.ExitCode != nil && *group.Container.ExitCode == 0); err != nil {
		return "", err
	}
	if *group.Container.ExitCode == 0 {
		return ArtifactBuilderStateSucceeded, nil
	}
	return ArtifactBuilderStateFailed, nil
}

// validateArtifactBuilderGroupIdentity 校验实时 group 与固定 request 的不可变身份一致。
func validateArtifactBuilderGroupIdentity(request ArtifactBuilderRequest, group ArtifactBuilderGroup) error {
	identity := []struct {
		name string
		got  string
		want string
	}{
		{"group ID", group.ID, ""},
		{"group name", group.Name, request.ContainerGroupName},
		{"provider", group.Provider, request.Provider},
		{"region", group.RegionID, request.RegionID},
		{"image", group.Image, request.Image},
		{"image snapshot", group.ImageSnapshotID, request.ImageSnapshotID},
	}
	if !artifactBuilderTokenPattern.MatchString(group.ID) {
		return errors.New("artifact builder live group ID is invalid")
	}
	for _, field := range identity[1:] {
		if field.got != field.want {
			return fmt.Errorf("artifact builder live %s identity does not match the bound request", field.name)
		}
	}
	if group.Container.Name != request.ContainerName {
		return errors.New("artifact builder live container identity does not match the bound request")
	}
	if group.Container.Image != request.Image {
		return errors.New("artifact builder live container image does not match the bound request")
	}
	return nil
}

// validateArtifactBuilderTerminal 校验终态 exit code 与时间区间证据。
func validateArtifactBuilderTerminal(group ArtifactBuilderGroup, success bool) error {
	if group.Container.State != ArtifactBuilderContainerTerminated || group.Container.ExitCode == nil {
		return errors.New("artifact builder terminal group must include terminated state and exit code")
	}
	if group.Container.StartTime.IsZero() || group.Container.FinishTime.IsZero() || group.Container.FinishTime.Before(group.Container.StartTime) {
		return errors.New("artifact builder terminal group must include an ordered live time interval")
	}
	if success && *group.Container.ExitCode != 0 {
		return errors.New("artifact builder succeeded group must have exit code zero")
	}
	return nil
}

// validArtifactBuilderObservationTransition 判断 Describe 观察状态是否单向推进。
func validArtifactBuilderObservationTransition(current, next ArtifactBuilderState) bool {
	switch current {
	case ArtifactBuilderStateCreated:
		return next == ArtifactBuilderStateCreated || next == ArtifactBuilderStateRunning || next == ArtifactBuilderStateSucceeded || next == ArtifactBuilderStateFailed
	case ArtifactBuilderStateRunning:
		return next == ArtifactBuilderStateRunning || next == ArtifactBuilderStateSucceeded || next == ArtifactBuilderStateFailed
	default:
		return false
	}
}

func cloneArtifactBuilderRequest(request ArtifactBuilderRequest) ArtifactBuilderRequest {
	request.VSwitches = append([]cicontract.ECIVSwitch(nil), request.VSwitches...)
	request.Command = append([]string(nil), request.Command...)
	return request
}

func cloneArtifactBuilderGroup(group ArtifactBuilderGroup) ArtifactBuilderGroup {
	if group.Container.ExitCode != nil {
		exitCode := *group.Container.ExitCode
		group.Container.ExitCode = &exitCode
	}
	return group
}
