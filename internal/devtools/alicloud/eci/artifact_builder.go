package eci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	ArtifactBuilderCommand = "/usr/local/bin/super-dolphin-remote-ci"
	// ArtifactBuilderSubcommand 是同一独立远程 CI 二进制的内部 builder worker 入口。
	ArtifactBuilderSubcommand = "_artifact-build"
	// ArtifactBuilderScheduleStrategy 是 builder 请求固定的多可用区调度策略。
	ArtifactBuilderScheduleStrategy = cicontract.ECIMultiZoneScheduleStrategy
	// ArtifactBuilderEphemeralStorageGiB 是每个 builder ECI group 的固定临时盘容量。
	ArtifactBuilderEphemeralStorageGiB = cicontract.ECIEphemeralStorageGiB
	// ArtifactBuilderCPU 和 ArtifactBuilderMemoryGiB 是 builder 独立使用的固定资源规格。
	ArtifactBuilderCPU       = cicontract.CalibrationResourceCPU
	ArtifactBuilderMemoryGiB = cicontract.CalibrationResourceMemoryGiB
	// ArtifactBuilderMaxLogBytes 是 builder 日志观察的有界上限。
	ArtifactBuilderMaxLogBytes = 1 << 20
	// ArtifactBuilderRequestDigestTag 与 ArtifactBuilderInputDigestTag 是 live group
	// 唯一允许携带的内容寻址 tag；原始凭据和其他 secret tag 均不属于协议。
	ArtifactBuilderRequestDigestTag = "request-digest"
	ArtifactBuilderInputDigestTag   = "input-digest"
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

// ErrArtifactBuilderIdentityDrift 表示 live group 与绑定 request 的身份漂移。
var ErrArtifactBuilderIdentityDrift = errors.New("artifact builder live identity drift")

// ErrArtifactBuilderExecution 表示 provider 返回了不能形成成功证据的错误终态。
var ErrArtifactBuilderExecution = errors.New("artifact builder execution failed")

// ErrArtifactBuilderCancelled 表示 builder 在成功证据形成前被取消。
var ErrArtifactBuilderCancelled = errors.New("artifact builder execution cancelled")

// ArtifactBuilderInput 绑定输入对象及 portable baseline 的全部内容身份。
type ArtifactBuilderInput struct {
	ArtifactPrefix            string `json:"artifact_prefix"`
	Key                       string `json:"input_key"`
	SHA256                    string `json:"input_sha256"`
	SizeBytes                 int64  `json:"input_size_bytes"`
	RepositoryID              string `json:"repository_id"`
	ObjectFormat              string `json:"object_format"`
	SourceCommitSHA           string `json:"source_commit_sha"`
	SourceTreeSHA             string `json:"source_tree_sha"`
	ExpectedBuilderDigest     string `json:"expected_builder_digest"`
	ExpectedToolchainDigest   string `json:"expected_toolchain_digest"`
	ProtocolABIDigest         string `json:"protocol_abi_digest"`
	WorkloadManifestABIDigest string `json:"workload_manifest_abi_digest"`
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
	OSSBucket           string
	OSSInternalEndpoint string
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
	OSSBucket           string
	OSSInternalEndpoint string
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
		OSSBucket:           config.OSSBucket,
		OSSInternalEndpoint: config.OSSInternalEndpoint,
		ArtifactKind:        ArtifactBuilderArtifactKind,
		ArtifactRef:         ArtifactBuilderArtifactRef,
		Command:             []string{ArtifactBuilderCommand, ArtifactBuilderSubcommand},
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
	return validateArtifactBuilderOSSIdentity(request.OSSBucket, request.OSSInternalEndpoint)
}

// validateProtocol 校验同一独立 binary 的内部 builder 子命令与 artifact 协议。
func (request ArtifactBuilderRequest) validateProtocol() error {
	if request.ArtifactKind != ArtifactBuilderArtifactKind || request.ArtifactRef != ArtifactBuilderArtifactRef {
		return errors.New("artifact builder kind or ref is not the portable source-baseline contract")
	}
	if len(request.Command) != 2 || request.Command[0] != ArtifactBuilderCommand || request.Command[1] != ArtifactBuilderSubcommand {
		return errors.New("artifact builder command must be the fixed portable source-baseline builder")
	}
	return nil
}

// Validate 校验输入对象 key、Git object identity 与 producer/toolchain digest。
func (input ArtifactBuilderInput) Validate() error {
	if err := validateArtifactBuilderPrefix(input.ArtifactPrefix); err != nil {
		return err
	}
	if !validArtifactBuilderObjectKey(input.Key) {
		return errors.New("artifact builder input key must be a clean repository-relative object key")
	}
	if !strings.HasPrefix(input.Key, input.ArtifactPrefix) {
		return errors.New("artifact builder input key must use artifact prefix")
	}
	if err := validateArtifactBuilderInputDigests(input); err != nil {
		return err
	}
	if err := validateArtifactBuilderInputSize(input.SizeBytes); err != nil {
		return err
	}
	return input.validateGitIdentity()
}

// validateArtifactBuilderInputDigests 校验 raw、expected 与 ABI 内容身份摘要。
func validateArtifactBuilderInputDigests(input ArtifactBuilderInput) error {
	for _, digest := range []string{input.SHA256, input.ExpectedBuilderDigest, input.ExpectedToolchainDigest, input.ProtocolABIDigest, input.WorkloadManifestABIDigest} {
		if !validArtifactBuilderDigest(digest) {
			return errors.New("artifact builder input, expected builder/toolchain, and ABI digests must be canonical sha256 values")
		}
	}
	return nil
}

// validateArtifactBuilderInputSize 校验输入 bundle 的有界字节数。
func validateArtifactBuilderInputSize(size int64) error {
	if size <= 0 || size > 1<<30 {
		return errors.New("artifact builder input size must be between 1 byte and 1 GiB")
	}
	return nil
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
		request.Command[0], request.Command[1],
		"--execution-provider", request.Provider,
		"--artifact-kind", request.ArtifactKind,
		"--artifact-ref", request.ArtifactRef,
		"--artifact-prefix", input.ArtifactPrefix,
		"--input-key", input.Key,
		"--input-sha256", input.SHA256,
		"--input-size-bytes", strconv.FormatInt(input.SizeBytes, 10),
		"--repository-id", input.RepositoryID,
		"--object-format", input.ObjectFormat,
		"--source-commit-sha", input.SourceCommitSHA,
		"--source-tree-sha", input.SourceTreeSHA,
		"--expected-builder-digest", input.ExpectedBuilderDigest,
		"--expected-toolchain-digest", input.ExpectedToolchainDigest,
		"--protocol-abi-digest", input.ProtocolABIDigest,
		"--workload-manifest-abi-digest", input.WorkloadManifestABIDigest,
		"--oss-bucket", request.OSSBucket,
		"--oss-endpoint", request.OSSInternalEndpoint,
		"--ram-role-name", request.RAMRoleName,
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
	return !allArtifactBuilderOIDZero(value)
}

func allArtifactBuilderOIDZero(value string) bool {
	for _, char := range value {
		if char != '0' {
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

// validateArtifactBuilderPrefix 校验 caller 提供的 clean、repository-relative slash prefix。
func validateArtifactBuilderPrefix(value string) error {
	if err := validateArtifactBuilderPrefixShape(value); err != nil {
		return err
	}
	if err := validateArtifactBuilderPrefixBoundary(value); err != nil {
		return err
	}
	trimmed := strings.TrimSuffix(value, "/")
	if err := validateArtifactBuilderPrefixCharacters(trimmed); err != nil {
		return err
	}
	if err := validateArtifactBuilderPrefixClean(value, trimmed); err != nil {
		return err
	}
	return nil
}

func validateArtifactBuilderPrefixShape(value string) error {
	if value == "" || strings.TrimSpace(value) != value || !strings.HasSuffix(value, "/") {
		return errors.New("artifact builder artifact prefix must be non-empty and end with slash")
	}
	return nil
}

// validateArtifactBuilderPrefixBoundary 校验 prefix 不携带绝对路径或跨协议边界。
func validateArtifactBuilderPrefixBoundary(value string) error {
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || strings.Contains(value, "\\") || strings.Contains(value, "://") || (len(value) >= 2 && value[1] == ':') {
		return errors.New("artifact builder artifact prefix must be repository-relative")
	}
	return nil
}

// validateArtifactBuilderPrefixCharacters 校验 prefix 不含空白或控制字符。
func validateArtifactBuilderPrefixCharacters(value string) error {
	if strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return errors.New("artifact builder artifact prefix contains whitespace or control characters")
	}
	return nil
}

// validateArtifactBuilderPrefixClean 校验 prefix 的路径组件规范化后保持不变。
func validateArtifactBuilderPrefixClean(value, trimmed string) error {
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean+"/" != value {
		return errors.New("artifact builder artifact prefix must be clean and must not escape")
	}
	return nil
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

type artifactBuilderRequestDigestMaterial struct {
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
	OSSBucket           string
	OSSInternalEndpoint string
	ArtifactKind        string
	ArtifactRef         string
	Command             []string
	InputDigest         string
}

func artifactBuilderInputDigest(input ArtifactBuilderInput) (string, error) {
	return artifactBuilderCanonicalDigest("artifact-builder-input/v1", input)
}

func artifactBuilderRequestDigest(request ArtifactBuilderRequest) (string, error) {
	inputDigest, err := artifactBuilderInputDigest(request.Input)
	if err != nil {
		return "", err
	}
	material := artifactBuilderRequestDigestMaterial{
		Provider: request.Provider, RegionID: request.RegionID,
		ContainerGroupName: request.ContainerGroupName, ContainerName: request.ContainerName,
		Image: request.Image, ImageSnapshotID: request.ImageSnapshotID,
		EphemeralStorageGiB: request.EphemeralStorageGiB, CPU: request.CPU,
		MemoryGiB: request.MemoryGiB, VSwitches: request.VSwitches,
		ScheduleStrategy: request.ScheduleStrategy, SecurityGroupID: request.SecurityGroupID,
		RAMRoleName: request.RAMRoleName, OSSBucket: request.OSSBucket,
		OSSInternalEndpoint: request.OSSInternalEndpoint, ArtifactKind: request.ArtifactKind,
		ArtifactRef: request.ArtifactRef, Command: request.Command, InputDigest: inputDigest,
	}
	return artifactBuilderCanonicalDigest("artifact-builder-request/v1", material)
}

func artifactBuilderCanonicalDigest(domain string, material any) (string, error) {
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("artifact builder canonical digest: %w", err)
	}
	payload := append([]byte(domain+"\x00"), encoded...)
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func artifactBuilderExpectedTags(request ArtifactBuilderRequest) (map[string]string, error) {
	requestDigest, err := artifactBuilderRequestDigest(request)
	if err != nil {
		return nil, err
	}
	inputDigest, err := artifactBuilderInputDigest(request.Input)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		ArtifactBuilderRequestDigestTag: requestDigest,
		ArtifactBuilderInputDigestTag:   inputDigest,
	}, nil
}

// ArtifactBuilderRequestMatch 是按 request name+tags 回查得到的唯一匹配。
// Present=false 只表示本次 provider 查询明确返回了零个匹配；它不依赖 actor 内存状态。
type ArtifactBuilderRequestMatch struct {
	Present bool
	GroupID string
	Group   ArtifactBuilderGroup
}

// ArtifactBuilderActor 是未来 ECI adapter 的最小生命周期 port；本文件不提供云实现。
type ArtifactBuilderActor interface {
	Create(context.Context, ArtifactBuilderRequest) (ArtifactBuilderGroup, error)
	ReconcileRequest(context.Context, ArtifactBuilderRequest) (ArtifactBuilderRequestMatch, error)
	Describe(context.Context, string) (ArtifactBuilderGroup, error)
	Logs(context.Context, string, string) (string, error)
	Delete(context.Context, string) error
	ConfirmAbsent(context.Context, string) (bool, error)
}

// ArtifactBuilderGroup 是 builder lifecycle 所需的最小 live identity 与状态证据。
type ArtifactBuilderGroup struct {
	ID                  string
	Name                string
	Provider            string
	RegionID            string
	Image               string
	ImageSnapshotID     string
	Command             []string
	Input               ArtifactBuilderInput
	EphemeralStorageGiB int64
	CPU                 float64
	MemoryGiB           float64
	VSwitches           []cicontract.ECIVSwitch
	ScheduleStrategy    string
	SecurityGroupID     string
	RAMRoleName         string
	OSSBucket           string
	OSSInternalEndpoint string
	Tags                map[string]string
	Status              string
	Container           ArtifactBuilderContainer
}

// ArtifactBuilderContainer 是 builder 容器的状态、退出码和真实时间证据。
type ArtifactBuilderContainer struct {
	Name       string
	Image      string
	Command    []string
	Args       []string
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
	// ArtifactBuilderGroupError 表示 provider 返回了错误终态但没有成功证据。
	ArtifactBuilderGroupError = "Error"
	// ArtifactBuilderGroupCancelled 表示 builder 在成功证据形成前被取消。
	ArtifactBuilderGroupCancelled = "Cancelled"
	// ArtifactBuilderGroupIdentityDrift 表示 provider 明确报告身份漂移。
	ArtifactBuilderGroupIdentityDrift = "IdentityDrift"

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
	// ArtifactBuilderStateIdentityDrift 表示 live identity 已漂移，必须清理而不能重试或成功。
	ArtifactBuilderStateIdentityDrift ArtifactBuilderState = "identity_drift"
	// ArtifactBuilderStateError 表示 provider 或证据校验失败，必须清理而不能成功。
	ArtifactBuilderStateError ArtifactBuilderState = "error"
	// ArtifactBuilderStateCancelled 表示 builder 被取消，必须清理而不能成功。
	ArtifactBuilderStateCancelled ArtifactBuilderState = "cancelled"
	// ArtifactBuilderStateDeleteRequested 表示已发起唯一 group cleanup。
	ArtifactBuilderStateDeleteRequested ArtifactBuilderState = "delete_requested"
	// ArtifactBuilderStateAbsent 表示 provider 已明确确认 group 消失。
	ArtifactBuilderStateAbsent ArtifactBuilderState = "absent"
)
