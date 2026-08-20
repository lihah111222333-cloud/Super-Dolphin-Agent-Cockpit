package remotecicli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	builderSimulationInputTempPrefix   = ".builder-simulate-input-"
	builderSimulationECIDeadline       = 2 * time.Hour
	builderSimulationPollInterval      = time.Second
	builderSimulationPollTimeout       = 2 * time.Hour
	builderSimulationCleanupTimeout    = 2 * time.Minute
	builderSimulationValidationTimeout = time.Minute
	builderSimulationGroupNamePrefix   = "sd-builder-"
	builderSimulationContainerName     = "builder"
	builderSimulationContractErrorCode = "request"
	builderSimulationInputErrorCode    = "input_bundle"
	builderSimulationStagingErrorCode  = "oss_staging"
	builderSimulationCloudErrorCode    = "eci_builder"
	builderSimulationArtifactErrorCode = "artifact"
	builderSimulationCleanupErrorCode  = "cleanup"
	builderSimulationStderrMaxBytes    = 1 << 20
)

// builderSimulationExitError 携带冻结退出码，阻止通用 CLI renderer 追加自由文本。
type builderSimulationExitError struct{ code int }

// Error 返回不含内部错误文本的稳定 CLI 退出摘要。
func (err builderSimulationExitError) Error() string { return "builder simulation exited" }

// builderSimulationEventWriter 仅写入 canonical event JSONL 记录。
type builderSimulationEventWriter struct {
	writer        io.Writer
	operationID   string
	requestDigest string
	sequence      uint64
	clock         func() time.Time
	writeErr      error
	bytesWritten  int
	cleanupLocal  func() error
}

// newBuilderSimulationEventWriter 创建有界的可观察事件输出端。
func newBuilderSimulationEventWriter(writer io.Writer, operationID, requestDigest string) *builderSimulationEventWriter {
	result := &builderSimulationEventWriter{writer: writer, operationID: operationID, requestDigest: requestDigest, clock: time.Now}
	if isNilRuntimeDependency(writer) {
		result.writeErr = errors.New("builder simulation stderr writer is required")
	}
	return result
}

// emit 追加一条安全事件并记录通道失败，不暴露底层错误。
func (writer *builderSimulationEventWriter) emit(stage, state, resourceID, resourceDigest, errorCode string) {
	if writer == nil || writer.writeErr != nil {
		return
	}
	writer.sequence++
	event := remoteci.BuilderSimulationEvent{
		SchemaVersion:  remoteci.BuilderSimulationEventSchema,
		OperationID:    writer.operationID,
		Seq:            writer.sequence,
		Time:           writer.clock().UTC().Format(time.RFC3339Nano),
		Stage:          stage,
		State:          state,
		RequestDigest:  writer.requestDigest,
		ResourceID:     resourceID,
		ResourceDigest: resourceDigest,
		ErrorCode:      errorCode,
	}
	payload, err := remoteci.EncodeBuilderSimulationEventJSONL(event)
	if err != nil {
		writer.writeErr = err
		return
	}
	if writer.bytesWritten+len(payload) > builderSimulationStderrMaxBytes {
		writer.writeErr = errors.New("builder simulation stderr exceeds its bounded size")
		return
	}
	written, err := writer.writer.Write(payload)
	if err != nil || written != len(payload) {
		writer.writeErr = errors.New("builder simulation stderr write failed")
		return
	}
	writer.bytesWritten += written
}

// runLocalCleanup 在 terminal 输出前完成私有 host bundle 清理。
func (writer *builderSimulationEventWriter) runLocalCleanup() error {
	if writer == nil || writer.cleanupLocal == nil {
		return nil
	}
	cleanup := writer.cleanupLocal
	writer.cleanupLocal = nil
	return cleanup()
}

// builderSimulationRecorder 将 runtime 事件映射为公开 Event schema。
type builderSimulationRecorder struct {
	writer                *builderSimulationEventWriter
	groupID               string
	lastStage             string
	lastStatus            string
	group                 eci.ArtifactBuilderGroup
	groupIDAbsent         bool
	requestReconciled     bool
	requestAbsentRounds   int
	requestReconcileError bool
	createAttempted       bool
	identityDrift         bool
}

// observe 翻译内部 runtime 观测，不泄露原始输出。
func (recorder *builderSimulationRecorder) observe(event ArtifactBuilderRuntimeEvent) {
	if recorder == nil || recorder.writer == nil {
		return
	}
	if !recorder.captureRuntimeEvent(event) {
		return
	}
	recorder.emitRuntimeEvent(event)
}

// emitRuntimeEvent 将变化的内部阶段投影为有界公开事件。
func (recorder *builderSimulationRecorder) emitRuntimeEvent(event ArtifactBuilderRuntimeEvent) {
	switch event.Stage {
	case "create":
		recorder.writer.emit(remoteci.BuilderSimulationEventStageECICreateRequested, remoteci.BuilderSimulationEventStateSucceeded, event.GroupID, "", "")
	case "describe":
		recorder.writer.emit(remoteci.BuilderSimulationEventStageECIGroupObserved, remoteci.BuilderSimulationEventStateSucceeded, event.GroupID, "", "")
	case "receipt":
		recorder.writer.emit(remoteci.BuilderSimulationEventStageECIGroupObserved, remoteci.BuilderSimulationEventStateSucceeded, event.GroupID, event.Digest, "")
	case "artifact":
		recorder.writer.emit(remoteci.BuilderSimulationEventStageArtifactValidated, remoteci.BuilderSimulationEventStateSucceeded, event.GroupID, event.Digest, "")
	case "failure_log":
		errorCode := string(remoteci.SourceBaselineBuilderStageResult)
		if event.Status != "" {
			stage := remoteci.SourceBaselineBuilderStage(event.Status)
			if _, ok := remoteci.SourceBaselineBuilderFailureStageFromLog("progress: artifact build failed stage=" + string(stage)); ok {
				errorCode = event.Status
			}
		}
		recorder.writer.emit(remoteci.BuilderSimulationEventStageECIGroupObserved, remoteci.BuilderSimulationEventStateFailed, event.GroupID, event.Digest, errorCode)
	case "cleanup":
		recorder.writer.emit(remoteci.BuilderSimulationEventStageCleanupStarted, remoteci.BuilderSimulationEventStateStarted, event.GroupID, "", "")
	}
}

// captureRuntimeEvent 更新安全生命周期身份并抑制重复轮询。
func (recorder *builderSimulationRecorder) captureRuntimeEvent(event ArtifactBuilderRuntimeEvent) bool {
	if event.GroupID != "" {
		recorder.groupID = event.GroupID
	}
	if event.Stage == "describe" && event.Stage == recorder.lastStage && event.Status == recorder.lastStatus {
		return false
	}
	recorder.lastStage, recorder.lastStatus = event.Stage, event.Status
	return true
}

// captureGroup 仅保留 provider 返回的终态字段供安全结果使用。
func (recorder *builderSimulationRecorder) captureGroup(group eci.ArtifactBuilderGroup) {
	if recorder == nil || group.ID == "" {
		return
	}
	if recorder.groupID != "" && recorder.groupID != group.ID {
		recorder.identityDrift = true
	}
	recorder.group = group
	recorder.groupID = group.ID
	recorder.groupIDAbsent = false
}

// recordRequestReconcile 记录连续两次 request identity 不存在观测。
func (recorder *builderSimulationRecorder) recordRequestReconcile(present bool) {
	recorder.requestReconciled = true
	if present {
		recorder.requestAbsentRounds = 0
		recorder.groupIDAbsent = false
		return
	}
	recorder.requestAbsentRounds++
}

// recordRequestReconcileError 阻止 provider 错误被误当作不存在证明。
func (recorder *builderSimulationRecorder) recordRequestReconcileError() {
	recorder.requestReconciled = true
	recorder.requestReconcileError = true
	recorder.requestAbsentRounds = 0
	recorder.groupIDAbsent = false
}

// groupAbsenceProof 返回 runtime 完整的 request 与 ID 不存在证明。
func (recorder *builderSimulationRecorder) groupAbsenceProof() bool {
	if recorder.identityDrift || recorder.requestReconcileError {
		return false
	}
	if !recorder.requestReconciled {
		return !recorder.createAttempted && recorder.groupID == ""
	}
	return recorder.requestAbsentRounds >= 2 && (recorder.groupID == "" || recorder.groupIDAbsent)
}

// observingArtifactBuilderActor 记录 live provider 证据并逐一委托调用。
type observingArtifactBuilderActor struct {
	delegate eci.ArtifactBuilderActor
	recorder *builderSimulationRecorder
}

// Create 委托唯一 create 调用并记录返回的 live group。
func (actor *observingArtifactBuilderActor) Create(ctx context.Context, request eci.ArtifactBuilderRequest) (eci.ArtifactBuilderGroup, error) {
	actor.recorder.createAttempted = true
	group, err := actor.delegate.Create(ctx, request)
	actor.recorder.captureGroup(group)
	return group, err
}

// ReconcileRequest 委托 identity reconcile 并记录匹配 group。
func (actor *observingArtifactBuilderActor) ReconcileRequest(ctx context.Context, request eci.ArtifactBuilderRequest) (eci.ArtifactBuilderRequestMatch, error) {
	match, err := actor.delegate.ReconcileRequest(ctx, request)
	if err != nil {
		actor.recorder.recordRequestReconcileError()
		return match, err
	}
	actor.recorder.recordRequestReconcile(match.Present)
	if match.Present {
		actor.recorder.captureGroup(match.Group)
	}
	return match, err
}

// Describe 委托 live provider 观测并记录返回 group。
func (actor *observingArtifactBuilderActor) Describe(ctx context.Context, groupID string) (eci.ArtifactBuilderGroup, error) {
	group, err := actor.delegate.Describe(ctx, groupID)
	actor.recorder.captureGroup(group)
	return group, err
}

// Logs 委托有界 builder 日志读取，不保留原始日志文本。
func (actor *observingArtifactBuilderActor) Logs(ctx context.Context, groupID, containerName string) (string, error) {
	return actor.delegate.Logs(ctx, groupID, containerName)
}

// Delete 委托 group 清理。
func (actor *observingArtifactBuilderActor) Delete(ctx context.Context, groupID string) error {
	return actor.delegate.Delete(ctx, groupID)
}

// ConfirmAbsent 委托 provider 不存在证明。
func (actor *observingArtifactBuilderActor) ConfirmAbsent(ctx context.Context, groupID string) (bool, error) {
	absent, err := actor.delegate.ConfirmAbsent(ctx, groupID)
	if absent && err == nil {
		if actor.recorder.groupID != "" && actor.recorder.groupID != groupID {
			actor.recorder.identityDrift = true
		}
		actor.recorder.groupIDAbsent = true
	}
	return absent, err
}

type builderSimulationObjectReader interface {
	Read(context.Context, string, int64) ([]byte, error)
}

// builderSimulationRuntimeStore 对 builder nested receipt 使用 bounded raw read，其余生命周期委托严格 portable store。
type builderSimulationRuntimeStore struct {
	portable *oss.PortableArtifactStore
	objects  builderSimulationObjectReader
	prefix   string
}

// ReadReceipt 校验 builder receipt key 并从实际字节派生元数据。
func (store *builderSimulationRuntimeStore) ReadReceipt(ctx context.Context, key string, maxBytes int64) ([]byte, oss.PortableArtifactMetadata, error) {
	if store == nil || store.portable == nil || store.objects == nil {
		return nil, oss.PortableArtifactMetadata{}, errors.New("builder simulation runtime store is incomplete")
	}
	if err := remoteci.ValidateSourceBaselineBuilderReceiptKey(key, store.prefix); err != nil {
		return nil, oss.PortableArtifactMetadata{}, err
	}
	payload, err := store.objects.Read(ctx, key, maxBytes)
	if err != nil {
		return nil, oss.PortableArtifactMetadata{}, err
	}
	return payload, oss.PortableArtifactMetadata{SHA256: runtimeDigestBytes(payload), SizeBytes: int64(len(payload))}, nil
}

// Resolve 委托 final artifact 的严格解析。
func (store *builderSimulationRuntimeStore) Resolve(ctx context.Context, kind, key string) (io.ReadCloser, int64, oss.PortableArtifactMetadata, error) {
	return store.portable.Resolve(ctx, kind, key)
}

// Delete 委托 input staging 清理。
func (store *builderSimulationRuntimeStore) Delete(ctx context.Context, key string) error {
	return store.portable.Delete(ctx, key)
}

// ConfirmAbsent 委托 input staging 不存在证明。
func (store *builderSimulationRuntimeStore) ConfirmAbsent(ctx context.Context, key string) (bool, error) {
	return store.portable.ConfirmAbsent(ctx, key)
}

// observingArtifactBuilderStore 记录既有 runtime 的 input 不存在证明。
type observingArtifactBuilderStore struct {
	delegate    ArtifactBuilderRuntimeStore
	inputAbsent bool
}

// ReadReceipt 委托 strict receipt 读取。
func (store *observingArtifactBuilderStore) ReadReceipt(ctx context.Context, key string, maxBytes int64) ([]byte, oss.PortableArtifactMetadata, error) {
	return store.delegate.ReadReceipt(ctx, key, maxBytes)
}

// Resolve 委托 final artifact 解析。
func (store *observingArtifactBuilderStore) Resolve(ctx context.Context, kind, key string) (io.ReadCloser, int64, oss.PortableArtifactMetadata, error) {
	return store.delegate.Resolve(ctx, kind, key)
}

// Delete 委托 input-only 清理。
func (store *observingArtifactBuilderStore) Delete(ctx context.Context, key string) error {
	return store.delegate.Delete(ctx, key)
}

// ConfirmAbsent 记录并委托既有 input 不存在证明。
func (store *observingArtifactBuilderStore) ConfirmAbsent(ctx context.Context, key string) (bool, error) {
	absent, err := store.delegate.ConfirmAbsent(ctx, key)
	if absent && err == nil {
		store.inputAbsent = true
	}
	return absent, err
}

// runBuilderSimulationCLI 执行 additive 非权威公开命令。
func runBuilderSimulationCLI(args []string, stdout, stderr io.Writer) error {
	requestPath, err := parseBuilderSimulationRequestArg(args)
	if err != nil {
		return builderSimulationContractFailure(stdout, stderr, "", "", builderSimulationContractErrorCode)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bound, err := LoadAndBindBuilderSimulationRequest(ctx, requestPath)
	if err != nil {
		return builderSimulationContractFailure(stdout, stderr, "", "", builderSimulationContractErrorCode)
	}
	writer := newBuilderSimulationEventWriter(stderr, bound.Request.OperationID, bound.RequestDigest)
	writer.emit(remoteci.BuilderSimulationEventStageRequestValidated, remoteci.BuilderSimulationEventStateSucceeded, "", "", "")
	writer.emit(remoteci.BuilderSimulationEventStageTargetManifestBound, remoteci.BuilderSimulationEventStateSucceeded, "", bound.TargetBindingDigest, "")
	return runBoundBuilderSimulation(ctx, bound, stdout, writer)
}

// parseBuilderSimulationRequestArg 只接受一个显式 clean absolute 路径。
func parseBuilderSimulationRequestArg(args []string) (string, error) {
	if len(args) != 2 || args[0] != "--request" {
		return "", errors.New("builder simulation request usage is invalid")
	}
	return validateBuilderSimulationRequestArgument(args[1])
}

// validateBuilderSimulationRequestArgument 保持 CLI 路径校验 fail-fast。
func validateBuilderSimulationRequestArgument(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || filepath.Clean(value) != value || !filepath.IsAbs(value) {
		return "", errors.New("builder simulation request path is invalid")
	}
	for _, runeValue := range value {
		if runeValue < 0x20 || runeValue == 0x7f {
			return "", errors.New("builder simulation request path contains control characters")
		}
	}
	return value, nil
}

// runBoundBuilderSimulation 执行校验、staging、一次 ECI build 与证明。
func runBoundBuilderSimulation(ctx context.Context, bound remoteci.BoundBuilderSimulationRequest, stdout io.Writer, writer *builderSimulationEventWriter) error {
	request := bound.Request
	client, cache, err := newBuilderSimulationECIClient(ctx, request)
	if err != nil {
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, nil, remoteci.BuilderSimulationExitCodeContractFailure, builderSimulationContractErrorCode, remoteci.BuilderSimulationEventStageEngineBound, true, true, remoteci.BuilderSimulationPublicationStateNotApplicable)
	}
	if err := validateBuilderSimulationImageCache(cache, request); err != nil {
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, nil, remoteci.BuilderSimulationExitCodeContractFailure, builderSimulationContractErrorCode, remoteci.BuilderSimulationEventStageEngineBound, true, true, remoteci.BuilderSimulationPublicationStateNotApplicable)
	}
	writer.emit(remoteci.BuilderSimulationEventStageEngineBound, remoteci.BuilderSimulationEventStateSucceeded, "", digestText(cache.ID), "")
	inputPath, cleanupLocal, err := createBuilderSimulationInputPath()
	if err != nil {
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, nil, remoteci.BuilderSimulationExitCodeBuildFailure, builderSimulationInputErrorCode, remoteci.BuilderSimulationEventStageInputBundleBuilt, true, true, remoteci.BuilderSimulationPublicationStateNotApplicable)
	}
	writer.cleanupLocal = cleanupLocal
	metadata, err := remoteci.BuildPortableBuilderInputBundle(ctx, remoteci.PortableBuilderInputBundleRequest{
		RepositoryRoot: request.Engine.RepositoryRoot,
		CommitSHA:      request.Engine.CommitSHA,
		TreeSHA:        request.Engine.TreeSHA,
		ObjectFormat:   gate.GitObjectFormat(request.Engine.ObjectFormat),
	}, inputPath)
	if err != nil {
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, nil, remoteci.BuilderSimulationExitCodeBuildFailure, builderSimulationInputErrorCode, remoteci.BuilderSimulationEventStageInputBundleBuilt, true, true, remoteci.BuilderSimulationPublicationStateNotApplicable)
	}
	writer.emit(remoteci.BuilderSimulationEventStageInputBundleBuilt, remoteci.BuilderSimulationEventStateSucceeded, "", metadata.SHA256, "")
	return executeBuilderSimulationBuild(ctx, bound, client, inputPath, metadata, stdout, writer)
}

// newBuilderSimulationECIClient 创建显式 ECI client 且不创建资源。
func newBuilderSimulationECIClient(ctx context.Context, request remoteci.BuilderSimulationRequest) (*eci.Client, eci.ImageCache, error) {
	config := eci.Config{
		Binary: request.ECI.AliyunCLI, RegionID: request.ECI.RegionID, VSwitches: request.ECI.VSwitches,
		SecurityGroupID: request.ECI.SecurityGroupID, WorkerRoleName: request.ECI.RAMRoleName,
		Profile: request.ECI.CredentialProfile, Deadline: builderSimulationECIDeadline,
		SpotStrategy: eci.SpotStrategyNoSpot, SpotDurationHours: 0,
		RegistryCredentialLoader: func() (eci.RegistryCredential, error) {
			return eci.RegistryCredential{}, errors.New("artifact builder does not use registry credentials")
		},
	}
	client, err := eci.New(config)
	if err != nil {
		return nil, eci.ImageCache{}, err
	}
	validationCtx, cancel := context.WithTimeout(ctx, builderSimulationValidationTimeout)
	defer cancel()
	cache, err := client.DescribeImageCache(validationCtx, request.ECI.ImageCacheID)
	if err != nil {
		return nil, eci.ImageCache{}, err
	}
	return client, cache, nil
}

// validateBuilderSimulationImageCache 在 staging 前绑定 live Ready cache 身份。
func validateBuilderSimulationImageCache(cache eci.ImageCache, request remoteci.BuilderSimulationRequest) error {
	if err := validateBuilderSimulationImageCacheHeader(cache, request); err != nil {
		return err
	}
	return validateBuilderSimulationImageList(cache.Images, request.ECI.Image)
}

// validateBuilderSimulationImageCacheHeader 校验 live cache 的 caller 绑定字段。
func validateBuilderSimulationImageCacheHeader(cache eci.ImageCache, request remoteci.BuilderSimulationRequest) error {
	if cache.RegionID == "" || cache.RegionID != request.ECI.RegionID {
		return errors.New("ECI ImageCache region identity differs from the request")
	}
	if cache.ID != request.ECI.ImageCacheID || cache.Status != "Ready" || cache.SnapshotID != request.ECI.ImageSnapshotID {
		return errors.New("ECI ImageCache is not the requested Ready snapshot")
	}
	return nil
}

// validateBuilderSimulationImageList 校验 cache 的不可变镜像集合和精确匹配。
func validateBuilderSimulationImageList(images []string, expected string) error {
	if len(images) == 0 {
		return errors.New("ECI ImageCache has no immutable image")
	}
	matched := 0
	for _, image := range images {
		if !validBuilderSimulationImmutableImage(image) {
			return errors.New("ECI ImageCache contains a non-immutable image")
		}
		if image == expected {
			matched++
		}
	}
	if matched != 1 {
		return errors.New("ECI ImageCache image does not match the request exactly once")
	}
	return nil
}

// validBuilderSimulationImmutableImage 只接受一个 canonical OCI digest 引用。
func validBuilderSimulationImmutableImage(value string) bool {
	image, digest, found := strings.Cut(value, "@sha256:")
	if !found || image == "" || len(digest) != sha256.Size*2 {
		return false
	}
	if _, err := hex.DecodeString(digest); err != nil || strings.ToLower(digest) != digest {
		return false
	}
	return !strings.ContainsAny(image, " \t\r\n?#")
}

// createBuilderSimulationInputPath 分配私有有界 host 输出路径。
func createBuilderSimulationInputPath() (string, func() error, error) {
	temporaryRoot, err := canonicalBuilderSimulationTempRoot()
	if err != nil {
		return "", func() error { return nil }, err
	}
	root, err := os.MkdirTemp(temporaryRoot, builderSimulationInputTempPrefix)
	if err != nil {
		return "", func() error { return nil }, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return "", func() error { return nil }, err
	}
	return filepath.Join(root, "input.bundle"), func() error { return os.RemoveAll(root) }, nil
}

// canonicalBuilderSimulationTempRoot 解析并校验 OS 临时目录的真实 canonical 根。
func canonicalBuilderSimulationTempRoot() (string, error) {
	configured := os.TempDir()
	if configured == "" {
		return "", errors.New("OS temporary directory is empty")
	}
	resolved, err := filepath.EvalSymlinks(configured)
	if err != nil {
		return "", errors.New("OS temporary directory cannot be canonicalized")
	}
	if err := validateAbsolutePathComponents(resolved, true); err != nil {
		return "", errors.New("canonical OS temporary directory is not trusted")
	}
	return resolved, nil
}

type builderSimulationPreparedBuild struct {
	store          *oss.PortableArtifactStore
	runtime        *ArtifactBuilderRuntime
	observingStore *observingArtifactBuilderStore
	recorder       *builderSimulationRecorder
	input          remoteci.SourceBaselineBuildInput
	inputKey       string
}

// executeBuilderSimulationBuild staging 一个 input 并且只调用一次 runtime。
func executeBuilderSimulationBuild(ctx context.Context, bound remoteci.BoundBuilderSimulationRequest, client *eci.Client, inputPath string, metadata remoteci.PortableBuilderInputBundleMetadata, stdout io.Writer, writer *builderSimulationEventWriter) error {
	prepared, err := prepareBuilderSimulationBuild(bound, client, metadata, writer)
	if err != nil {
		request := bound.Request
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, nil, remoteci.BuilderSimulationExitCodeContractFailure, builderSimulationContractErrorCode, remoteci.BuilderSimulationEventStageInputBundleBuilt, true, true, remoteci.BuilderSimulationPublicationStateNotApplicable)
	}
	request := bound.Request
	if err := prepared.store.CreateInput(ctx, inputPath, prepared.inputKey, oss.PortableArtifactMetadata{SHA256: metadata.SHA256, SizeBytes: metadata.SizeBytes}); err != nil {
		groupAbsent, inputAbsent := cleanupBuilderSimulationInput(ctx, prepared.store, prepared.inputKey)
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, nil, builderSimulationFailureExitCode(groupAbsent, inputAbsent), builderSimulationStagingErrorCode, remoteci.BuilderSimulationEventStageInputStaged, groupAbsent, inputAbsent, remoteci.BuilderSimulationPublicationStateNotApplicable)
	}
	writer.emit(remoteci.BuilderSimulationEventStageInputStaged, remoteci.BuilderSimulationEventStateSucceeded, "", metadata.SHA256, "")
	evidence, buildErr := prepared.runtime.Build(ctx, prepared.input)
	return finishPreparedBuilderSimulation(bound, prepared, evidence, buildErr, stdout, writer)
}

// prepareBuilderSimulationBuild 构造既有 OSS/ECI runtime 且不产生副作用。
func prepareBuilderSimulationBuild(bound remoteci.BoundBuilderSimulationRequest, client *eci.Client, metadata remoteci.PortableBuilderInputBundleMetadata, writer *builderSimulationEventWriter) (builderSimulationPreparedBuild, error) {
	request := bound.Request
	ossClient, err := oss.NewCLI(oss.Config{Binary: request.ECI.AliyunCLI, Bucket: request.OSS.Bucket, Endpoint: request.OSS.Endpoint, Profile: request.ECI.CredentialProfile, Prefix: request.Artifact.Prefix})
	if err != nil {
		return builderSimulationPreparedBuild{}, err
	}
	store, err := oss.NewPortableArtifactStoreFromObjectStore(ossClient, request.Artifact.Prefix)
	if err != nil {
		return builderSimulationPreparedBuild{}, err
	}
	inputKey, err := oss.CanonicalPortableInputStagingKey(request.Artifact.Prefix, request.OperationID, metadata.SHA256)
	if err != nil {
		return builderSimulationPreparedBuild{}, err
	}
	input := builderSimulationBuildInput(request, inputKey, metadata)
	if err := input.Validate(); err != nil {
		return builderSimulationPreparedBuild{}, err
	}
	config, err := builderSimulationArtifactBuilderConfig(bound, request, input)
	if err != nil {
		return builderSimulationPreparedBuild{}, err
	}
	if _, err := eci.NewArtifactBuilderRequest(config); err != nil {
		return builderSimulationPreparedBuild{}, err
	}
	recorder := &builderSimulationRecorder{writer: writer}
	actor := &observingArtifactBuilderActor{delegate: eci.NewArtifactBuilderActor(client), recorder: recorder}
	runtimeStore := &builderSimulationRuntimeStore{portable: store, objects: ossClient, prefix: request.Artifact.Prefix}
	observingStore := &observingArtifactBuilderStore{delegate: runtimeStore}
	runtime, err := NewArtifactBuilderRuntime(ArtifactBuilderRuntimeDependencies{Actor: actor, Store: observingStore,
		RequestFactory: builderSimulationRequestFactory(config, input), Clock: time.Now,
		PollInterval: builderSimulationPollInterval, PollTimeout: builderSimulationPollTimeout,
		CleanupTimeout: builderSimulationCleanupTimeout, Observer: recorder.observe})
	if err != nil {
		return builderSimulationPreparedBuild{}, err
	}
	return builderSimulationPreparedBuild{store: store, runtime: runtime, observingStore: observingStore, recorder: recorder, input: input, inputKey: inputKey}, nil
}

// builderSimulationRequestFactory 将 runtime input 身份绑定到一个 ECI request。
func builderSimulationRequestFactory(config eci.ArtifactBuilderConfig, expected remoteci.SourceBaselineBuildInput) ArtifactBuilderRequestFactory {
	return func(actual remoteci.SourceBaselineBuildInput) (eci.ArtifactBuilderRequest, error) {
		if actual != expected {
			return eci.ArtifactBuilderRequest{}, errors.New("builder simulation runtime input identity drifted")
		}
		config.Input = artifactBuilderInput(actual)
		return eci.NewArtifactBuilderRequest(config)
	}
}

// finishPreparedBuilderSimulation 将 runtime 证据和清理证明映射为一个结果。
func finishPreparedBuilderSimulation(bound remoteci.BoundBuilderSimulationRequest, prepared builderSimulationPreparedBuild, evidence remoteci.SourceBaselineBuildEvidence, buildErr error, stdout io.Writer, writer *builderSimulationEventWriter) error {
	request := bound.Request
	groupAbsent, inputAbsent := prepared.recorder.groupAbsenceProof(), prepared.observingStore.inputAbsent
	if buildErr != nil {
		code := builderSimulationFailureExitCode(groupAbsent, inputAbsent)
		stage := builderSimulationFailureStage(prepared.recorder)
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, prepared.recorder, code, builderSimulationFailureCode(stage), stage, groupAbsent, inputAbsent, remoteci.BuilderSimulationPublicationStatePartial)
	}
	if !groupAbsent || !inputAbsent {
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, prepared.recorder, remoteci.BuilderSimulationExitCodeCleanupFailure, builderSimulationCleanupErrorCode, remoteci.BuilderSimulationEventStageCleanupGroupAbsent, groupAbsent, inputAbsent, remoteci.BuilderSimulationPublicationStatePartial)
	}
	artifact, err := builderSimulationResultArtifact(evidence)
	if err != nil {
		return finishBuilderSimulationFailure(stdout, writer, request.OperationID, bound, prepared.recorder, remoteci.BuilderSimulationExitCodeBuildFailure, builderSimulationArtifactErrorCode, remoteci.BuilderSimulationEventStageArtifactValidated, groupAbsent, inputAbsent, remoteci.BuilderSimulationPublicationStatePartial)
	}
	writer.emit(remoteci.BuilderSimulationEventStageCleanupGroupAbsent, remoteci.BuilderSimulationEventStateSucceeded, prepared.recorder.groupID, "", "")
	writer.emit(remoteci.BuilderSimulationEventStageCleanupInputAbsent, remoteci.BuilderSimulationEventStateSucceeded, "", "", "")
	return finishBuilderSimulationSuccess(stdout, writer, request.OperationID, bound, evidence, artifact)
}

// builderSimulationBuildInput 将 bound request 投影为既有 input DTO。
func builderSimulationBuildInput(request remoteci.BuilderSimulationRequest, inputKey string, metadata remoteci.PortableBuilderInputBundleMetadata) remoteci.SourceBaselineBuildInput {
	return remoteci.SourceBaselineBuildInput{
		SchemaVersion: remoteci.SourceBaselineBuilderInputSchema, ArtifactPrefix: request.Artifact.Prefix,
		RepositoryID: request.Engine.RepositoryID, ObjectFormat: request.Engine.ObjectFormat,
		CommitSHA: request.Engine.CommitSHA, TreeSHA: request.Engine.TreeSHA,
		InputObjectKey: inputKey, InputSHA256: metadata.SHA256, InputSizeBytes: metadata.SizeBytes,
		ExpectedBuilderDigest: request.Engine.ExpectedBuilderDigest, ExpectedToolchainDigest: request.Engine.ExpectedToolchainDigest,
		ProtocolABIDigest: request.Engine.ProtocolABIDigest, WorkloadManifestABIDigest: request.Target.WorkloadManifest.ABIDigest,
	}
}

// builderSimulationArtifactBuilderConfig 显式绑定每个 ECI builder 字段。
func builderSimulationArtifactBuilderConfig(bound remoteci.BoundBuilderSimulationRequest, request remoteci.BuilderSimulationRequest, input remoteci.SourceBaselineBuildInput) (eci.ArtifactBuilderConfig, error) {
	groupName, err := builderSimulationGroupName(bound.RequestDigest)
	if err != nil {
		return eci.ArtifactBuilderConfig{}, err
	}
	return eci.ArtifactBuilderConfig{
		Provider: eci.ArtifactBuilderProvider, RegionID: request.ECI.RegionID,
		ContainerGroupName: groupName, ContainerName: builderSimulationContainerName,
		CompilerPath: request.ECI.CompilerPath, Image: request.ECI.Image, ImageSnapshotID: request.ECI.ImageSnapshotID,
		EphemeralStorageGiB: eci.ArtifactBuilderEphemeralStorageGiB, CPU: eci.ArtifactBuilderCPU, MemoryGiB: eci.ArtifactBuilderMemoryGiB,
		VSwitches: request.ECI.VSwitches, SecurityGroupID: request.ECI.SecurityGroupID, RAMRoleName: request.ECI.RAMRoleName,
		OSSBucket: request.OSS.Bucket, OSSInternalEndpoint: request.OSS.InternalEndpoint, Input: artifactBuilderInput(input),
	}, nil
}

// artifactBuilderInput 将共享 input DTO 转换为 ECI request DTO。
func artifactBuilderInput(input remoteci.SourceBaselineBuildInput) eci.ArtifactBuilderInput {
	return eci.ArtifactBuilderInput{
		ArtifactPrefix: input.ArtifactPrefix, Key: input.InputObjectKey, SHA256: input.InputSHA256, SizeBytes: input.InputSizeBytes,
		RepositoryID: input.RepositoryID, ObjectFormat: input.ObjectFormat, SourceCommitSHA: input.CommitSHA, SourceTreeSHA: input.TreeSHA,
		ExpectedBuilderDigest: input.ExpectedBuilderDigest, ExpectedToolchainDigest: input.ExpectedToolchainDigest,
		ProtocolABIDigest: input.ProtocolABIDigest, WorkloadManifestABIDigest: input.WorkloadManifestABIDigest,
	}
}

// builderSimulationGroupName 将一个 request 确定性绑定到一个 group name。
func builderSimulationGroupName(requestDigest string) (string, error) {
	digest, found := strings.CutPrefix(requestDigest, "sha256:")
	if !found || len(digest) != sha256.Size*2 {
		return "", errors.New("builder simulation request digest is invalid")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", errors.New("builder simulation request digest is invalid")
	}
	return builderSimulationGroupNamePrefix + digest[:16], nil
}

// cleanupBuilderSimulationInput 在有界时间内重试删除并取得不存在证明。
func cleanupBuilderSimulationInput(ctx context.Context, store *oss.PortableArtifactStore, inputKey string) (bool, bool) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), builderSimulationCleanupTimeout)
	defer cancel()
	_ = store.Delete(cleanupCtx, inputKey)
	absent, err := store.ConfirmAbsent(cleanupCtx, inputKey)
	return true, err == nil && absent
}

// builderSimulationResultArtifact 将已验证 receipt 身份投影为 terminal 输出。
func builderSimulationResultArtifact(evidence remoteci.SourceBaselineBuildEvidence) (remoteci.BuilderSimulationResultArtifact, error) {
	receiptDigest, err := remoteci.SourceBaselineProducerReceiptDigest(evidence.Receipt)
	if err != nil {
		return remoteci.BuilderSimulationResultArtifact{}, err
	}
	return remoteci.BuilderSimulationResultArtifact{
		Kind: remoteci.BuilderSimulationArtifactKindPortableRuntime, BytesSHA256: evidence.Receipt.ArtifactSHA256,
		IdentityDigest: evidence.Receipt.ArtifactIdentityDigest, SizeBytes: evidence.Receipt.ArtifactSizeBytes,
		ReceiptDigest: receiptDigest,
	}, nil
}

// builderSimulationFailureExitCode 在冻结退出矩阵中保持清理优先级。
func builderSimulationFailureExitCode(groupAbsent, inputAbsent bool) int {
	if groupAbsent && inputAbsent {
		return remoteci.BuilderSimulationExitCodeBuildFailure
	}
	return remoteci.BuilderSimulationExitCodeCleanupFailure
}

// builderSimulationFailureStage 将内部失败映射为稳定公开阶段。
func builderSimulationFailureStage(recorder *builderSimulationRecorder) string {
	if recorder != nil && recorder.groupID != "" {
		return remoteci.BuilderSimulationEventStageECIGroupObserved
	}
	if recorder != nil && recorder.createAttempted {
		return remoteci.BuilderSimulationEventStageECICreateRequested
	}
	return remoteci.BuilderSimulationEventStageInputStaged
}

// builderSimulationFailureCode 将公开阶段映射为有界错误码。
func builderSimulationFailureCode(stage string) string {
	switch stage {
	case remoteci.BuilderSimulationEventStageInputBundleBuilt:
		return builderSimulationInputErrorCode
	case remoteci.BuilderSimulationEventStageInputStaged:
		return builderSimulationStagingErrorCode
	case remoteci.BuilderSimulationEventStageECICreateRequested, remoteci.BuilderSimulationEventStageECIGroupObserved:
		return builderSimulationCloudErrorCode
	case remoteci.BuilderSimulationEventStageArtifactValidated:
		return builderSimulationArtifactErrorCode
	case remoteci.BuilderSimulationEventStageCleanupStarted, remoteci.BuilderSimulationEventStageCleanupGroupAbsent, remoteci.BuilderSimulationEventStageCleanupInputAbsent:
		return builderSimulationCleanupErrorCode
	default:
		return builderSimulationContractErrorCode
	}
}

// builderSimulationContractFailure 即使没有 bound request 也渲染安全结果。
func builderSimulationContractFailure(stdout, stderr io.Writer, operationID, requestDigest, code string) error {
	writer := newBuilderSimulationEventWriter(stderr, operationID, requestDigest)
	writer.emit(remoteci.BuilderSimulationEventStageRequestValidated, remoteci.BuilderSimulationEventStateFailed, "", "", code)
	result := builderSimulationBaseResult(operationID, requestDigest, requestDigest, requestDigest, remoteci.BuilderSimulationExitCodeContractFailure, remoteci.BuilderSimulationResultCloud{Provider: eci.ArtifactBuilderProvider, Status: remoteci.BuilderSimulationCloudStatusNotStarted, ContainerExitCode: -1}, remoteci.BuilderSimulationResultCleanup{ContainerGroupAbsent: true, InputStagingAbsent: true, HostInputAbsent: true, PublicationState: remoteci.BuilderSimulationPublicationStateNotApplicable, ConfirmedAt: writer.clock().UTC().Format(time.RFC3339Nano)}, remoteci.BuilderSimulationResultError{Code: code, Stage: remoteci.BuilderSimulationEventStageRequestValidated})
	return writeBuilderSimulationResult(stdout, writer, result)
}

// finishBuilderSimulationFailure 在清理证明后渲染有界失败结果。
func finishBuilderSimulationFailure(stdout io.Writer, writer *builderSimulationEventWriter, operationID string, bound remoteci.BoundBuilderSimulationRequest, recorder *builderSimulationRecorder, code int, errorCode, stage string, groupAbsent, inputAbsent bool, publicationState string) error {
	writer.emit(stage, remoteci.BuilderSimulationEventStateFailed, safeRecorderGroupID(recorder), "", errorCode)
	writer.emit(remoteci.BuilderSimulationEventStageCleanupGroupAbsent, builderSimulationCleanupState(groupAbsent), safeRecorderGroupID(recorder), "", errorCode)
	writer.emit(remoteci.BuilderSimulationEventStageCleanupInputAbsent, builderSimulationCleanupState(inputAbsent), "", "", errorCode)
	cloud := builderSimulationFailureCloud(recorder)
	confirmedAt := ""
	if groupAbsent && inputAbsent {
		confirmedAt = writer.clock().UTC().Format(time.RFC3339Nano)
	}
	result := builderSimulationBaseResult(operationID, bound.RequestDigest, bound.EngineBindingDigest, bound.TargetBindingDigest, code, cloud, remoteci.BuilderSimulationResultCleanup{ContainerGroupAbsent: groupAbsent, InputStagingAbsent: inputAbsent, HostInputAbsent: true, PublicationState: publicationState, ConfirmedAt: confirmedAt}, remoteci.BuilderSimulationResultError{Code: errorCode, Stage: stage})
	return writeBuilderSimulationResult(stdout, writer, result)
}

// finishBuilderSimulationSuccess 渲染已验证 artifact 与 provider 证据。
func finishBuilderSimulationSuccess(stdout io.Writer, writer *builderSimulationEventWriter, operationID string, bound remoteci.BoundBuilderSimulationRequest, evidence remoteci.SourceBaselineBuildEvidence, artifact remoteci.BuilderSimulationResultArtifact) error {
	cloud := remoteci.BuilderSimulationResultCloud{Provider: eci.ArtifactBuilderProvider, Group: evidence.ContainerGroupID, Status: remoteci.BuilderSimulationCloudStatusSucceeded, ContainerExitCode: int(evidence.ContainerExitCode), StartedAt: evidence.StartedAt, FinishedAt: evidence.FinishedAt}
	result := builderSimulationBaseResult(operationID, bound.RequestDigest, bound.EngineBindingDigest, bound.TargetBindingDigest, remoteci.BuilderSimulationExitCodeSuccess, cloud, remoteci.BuilderSimulationResultCleanup{ContainerGroupAbsent: true, InputStagingAbsent: true, HostInputAbsent: true, PublicationState: remoteci.BuilderSimulationPublicationStateValidatedPair, ConfirmedAt: writer.clock().UTC().Format(time.RFC3339Nano)}, remoteci.BuilderSimulationResultError{})
	result.Artifact = artifact
	return writeBuilderSimulationResult(stdout, writer, result)
}

// builderSimulationBaseResult 填充稳定 terminal header 与 authority 边界。
func builderSimulationBaseResult(operationID, requestDigest, engineDigest, targetDigest string, code int, cloud remoteci.BuilderSimulationResultCloud, cleanup remoteci.BuilderSimulationResultCleanup, resultError remoteci.BuilderSimulationResultError) remoteci.BuilderSimulationResult {
	return remoteci.BuilderSimulationResult{SchemaVersion: remoteci.BuilderSimulationResultSchema, Status: remoteci.BuilderSimulationResultStatusNotVerified, Outcome: builderSimulationOutcome(code), ProcessExitCode: code, OperationID: operationID, RequestDigest: requestDigest, EngineBindingDigest: engineDigest, TargetBindingDigest: targetDigest, Cloud: cloud, Cleanup: cleanup, AuthorityEffects: remoteci.BuilderSimulationAuthorityEffectsNone, Error: resultError}
}

// builderSimulationOutcome 将进程矩阵映射为 terminal outcome 字段。
func builderSimulationOutcome(code int) string {
	if code == remoteci.BuilderSimulationExitCodeSuccess {
		return remoteci.BuilderSimulationOutcomeSucceeded
	}
	return remoteci.BuilderSimulationOutcomeFailed
}

// builderSimulationCleanupState 将证明布尔值映射为安全事件状态。
func builderSimulationCleanupState(absent bool) string {
	if absent {
		return remoteci.BuilderSimulationEventStateSucceeded
	}
	return remoteci.BuilderSimulationEventStateFailed
}

// builderSimulationFailureCloud 只投影已观测的安全 group 字段。
func builderSimulationFailureCloud(recorder *builderSimulationRecorder) remoteci.BuilderSimulationResultCloud {
	cloud := remoteci.BuilderSimulationResultCloud{Provider: eci.ArtifactBuilderProvider, Status: remoteci.BuilderSimulationCloudStatusNotStarted, ContainerExitCode: -1}
	if recorder == nil || recorder.groupID == "" {
		return cloud
	}
	cloud.Group = recorder.groupID
	cloud.Status = remoteci.BuilderSimulationCloudStatusFailed
	if recorder.group.Container.ExitCode != nil {
		cloud.ContainerExitCode = int(*recorder.group.Container.ExitCode)
	}
	if !recorder.group.Container.StartTime.IsZero() && !recorder.group.Container.FinishTime.IsZero() && recorder.group.Container.FinishTime.After(recorder.group.Container.StartTime) {
		cloud.StartedAt = recorder.group.Container.StartTime.UTC().Format(time.RFC3339Nano)
		cloud.FinishedAt = recorder.group.Container.FinishTime.UTC().Format(time.RFC3339Nano)
	}
	return cloud
}

// safeRecorderGroupID 在 provider 提供证据前省略 ID。
func safeRecorderGroupID(recorder *builderSimulationRecorder) string {
	if recorder == nil {
		return ""
	}
	return recorder.groupID
}

// writeBuilderSimulationResult 是该命令唯一的 machine stdout writer。
func writeBuilderSimulationResult(stdout io.Writer, writer *builderSimulationEventWriter, result remoteci.BuilderSimulationResult) error {
	if writer == nil || writer.writeErr != nil || isNilRuntimeDependency(stdout) {
		return builderSimulationExitError{code: remoteci.BuilderSimulationExitCodeOutputChannelFailure}
	}
	if writer.runLocalCleanup() != nil {
		result = builderSimulationLocalCleanupFailure(result)
	}
	payload, err := result.CanonicalEncode()
	if err != nil {
		return builderSimulationExitError{code: remoteci.BuilderSimulationExitCodeOutputChannelFailure}
	}
	if err := emitBuilderSimulationTerminal(writer, result); err != nil {
		return builderSimulationExitError{code: remoteci.BuilderSimulationExitCodeOutputChannelFailure}
	}
	if err := writeBuilderSimulationStdout(stdout, payload); err != nil {
		return builderSimulationExitError{code: remoteci.BuilderSimulationExitCodeOutputChannelFailure}
	}
	if result.ProcessExitCode == remoteci.BuilderSimulationExitCodeSuccess {
		return nil
	}
	return builderSimulationExitError{code: result.ProcessExitCode}
}

// emitBuilderSimulationTerminal 先写最终 JSONL 事件，保证通道顺序可验证。
func emitBuilderSimulationTerminal(writer *builderSimulationEventWriter, result remoteci.BuilderSimulationResult) error {
	terminalState := remoteci.BuilderSimulationEventStateSucceeded
	if result.ProcessExitCode != remoteci.BuilderSimulationExitCodeSuccess {
		terminalState = remoteci.BuilderSimulationEventStateFailed
	}
	writer.emit(remoteci.BuilderSimulationEventStageTerminal, terminalState, "", "", result.Error.Code)
	return writer.writeErr
}

// writeBuilderSimulationStdout 写出唯一 strict terminal JSON 并拒绝 short write。
func writeBuilderSimulationStdout(stdout io.Writer, payload []byte) error {
	written, err := stdout.Write(payload)
	if err != nil || written != len(payload) {
		return errors.New("builder simulation stdout write failed")
	}
	return nil
}

// builderSimulationLocalCleanupFailure 在 host 清理失败时保守撤销不存在声明。
func builderSimulationLocalCleanupFailure(result remoteci.BuilderSimulationResult) remoteci.BuilderSimulationResult {
	result.Outcome = remoteci.BuilderSimulationOutcomeFailed
	result.ProcessExitCode = remoteci.BuilderSimulationExitCodeCleanupFailure
	result.Cleanup.HostInputAbsent = false
	result.Cleanup.ConfirmedAt = ""
	result.Cleanup.PublicationState = remoteci.BuilderSimulationPublicationStatePartial
	result.Error = remoteci.BuilderSimulationResultError{Code: builderSimulationCleanupErrorCode, Stage: remoteci.BuilderSimulationEventStageCleanupInputAbsent}
	return result
}

// digestText 为非敏感资源身份创建安全 opaque digest。
func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
