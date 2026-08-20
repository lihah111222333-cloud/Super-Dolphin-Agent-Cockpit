package remotecicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	artifactBuildRuntimeSchema = "source-baseline-artifact-build/v1"
	artifactBuildRuntimePrefix = "SUPER_DOLPHIN_ARTIFACT_BUILD_RESULT "
)

// ArtifactBuilderRuntimeStore 是 runtime 需要的最小对象端口。
// receipt 尚未成为 OSS store 的公共方法时，生产接线可使用一个窄适配器。
type ArtifactBuilderRuntimeStore interface {
	ReadReceipt(context.Context, string, int64) ([]byte, oss.PortableArtifactMetadata, error)
	Resolve(context.Context, string, string) (io.ReadCloser, int64, oss.PortableArtifactMetadata, error)
	Delete(context.Context, string) error
	ConfirmAbsent(context.Context, string) (bool, error)
}

// PortableArtifactBuilderStore 是 ArtifactBuilderRuntimeStore 的语义别名。
type PortableArtifactBuilderStore = ArtifactBuilderRuntimeStore

// ArtifactBuilderRequestFactory 从已校验输入构造唯一 builder request。
type ArtifactBuilderRequestFactory func(remoteci.SourceBaselineBuildInput) (eci.ArtifactBuilderRequest, error)

// ArtifactBuilderRuntimeEvent 是安全旁路日志；不携带路径、凭据或原始日志。
type ArtifactBuilderRuntimeEvent struct {
	Stage   string
	GroupID string
	Status  string
	Key     string
	Digest  string
	Cleanup bool
}

// ArtifactBuilderRuntimeObserver 接收有限字段的 runtime 旁路事件。
type ArtifactBuilderRuntimeObserver func(ArtifactBuilderRuntimeEvent)

// ArtifactBuilderRuntimeDependencies 是一次 runtime 的显式依赖集合。
type ArtifactBuilderRuntimeDependencies struct {
	Actor          eci.ArtifactBuilderActor
	Store          ArtifactBuilderRuntimeStore
	RequestFactory ArtifactBuilderRequestFactory
	Clock          func() time.Time
	PollInterval   time.Duration
	PollTimeout    time.Duration
	CleanupTimeout time.Duration
	Observer       ArtifactBuilderRuntimeObserver
}

// ArtifactBuilderRuntime 只负责 builder 生命周期与证据编排。
type ArtifactBuilderRuntime struct {
	deps ArtifactBuilderRuntimeDependencies
}

var _ remoteci.SourceBaselineArtifactBuilderRuntime = (*ArtifactBuilderRuntime)(nil)

// NewArtifactBuilderRuntime 校验全部运行时依赖，不创建任何 provider 资源。
func NewArtifactBuilderRuntime(deps ArtifactBuilderRuntimeDependencies) (*ArtifactBuilderRuntime, error) {
	if isNilRuntimeDependency(deps.Actor) || isNilRuntimeDependency(deps.Store) || deps.RequestFactory == nil || deps.Clock == nil {
		return nil, errors.New("artifact builder runtime dependencies are incomplete")
	}
	if deps.PollInterval <= 0 || deps.PollTimeout <= 0 || deps.CleanupTimeout <= 0 {
		return nil, errors.New("artifact builder runtime timeouts must be positive")
	}
	return &ArtifactBuilderRuntime{deps: deps}, nil
}

// NewSourceBaselineArtifactBuilderRuntime 是组合根使用的语义化构造器。
func NewSourceBaselineArtifactBuilderRuntime(deps ArtifactBuilderRuntimeDependencies) (*ArtifactBuilderRuntime, error) {
	return NewArtifactBuilderRuntime(deps)
}

// Build 使用一个 group 完成 build、receipt/artifact 校验，并始终执行有界清理。
func (runtime *ArtifactBuilderRuntime) Build(ctx context.Context, input remoteci.SourceBaselineBuildInput) (evidence remoteci.SourceBaselineBuildEvidence, err error) {
	if runtime == nil {
		return evidence, errors.New("artifact builder runtime is nil")
	}
	if ctx == nil {
		return evidence, errors.New("artifact builder runtime context is nil")
	}
	if err = input.Validate(); err != nil {
		return evidence, err
	}
	var groupID string
	var request eci.ArtifactBuilderRequest
	var requestReady bool
	defer func() {
		cleanupErr := runtime.cleanup(ctx, request, requestReady, groupID, input.InputObjectKey)
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return evidence, ctxErr
	}
	inputDigest, digestErr := remoteci.SourceBaselineBuilderInputDigest(input)
	if digestErr != nil {
		return evidence, digestErr
	}
	runtime.observe(ArtifactBuilderRuntimeEvent{Stage: "input", Digest: inputDigest})
	request, requestErr := runtime.deps.RequestFactory(input)
	if requestErr != nil {
		return evidence, requestErr
	}
	requestReady = true
	return runtime.buildRequest(ctx, input, inputDigest, request, &groupID)
}

func (runtime *ArtifactBuilderRuntime) buildRequest(ctx context.Context, input remoteci.SourceBaselineBuildInput, inputDigest string, request eci.ArtifactBuilderRequest, groupID *string) (evidence remoteci.SourceBaselineBuildEvidence, err error) {
	if err = runtime.validateRequestBinding(input, inputDigest, request); err != nil {
		return evidence, err
	}
	runtime.observe(ArtifactBuilderRuntimeEvent{Stage: "request", Digest: inputDigest})
	pollCtx, cancel := context.WithTimeout(ctx, runtime.deps.PollTimeout)
	defer cancel()
	group, lifecycle, createErr := runtime.createBuilder(pollCtx, request, groupID)
	if createErr != nil {
		return evidence, createErr
	}
	group, logs, observeErr := runtime.observeBuilder(pollCtx, lifecycle, group)
	if observeErr != nil {
		return evidence, observeErr
	}
	return runtime.collectEvidence(ctx, pollCtx, input, inputDigest, group, logs)
}

// createBuilder 创建唯一 ECI group，并把 provider identity 交给 lifecycle 校验。
func (runtime *ArtifactBuilderRuntime) createBuilder(ctx context.Context, request eci.ArtifactBuilderRequest, groupID *string) (eci.ArtifactBuilderGroup, *eci.ArtifactBuilderLifecycle, error) {
	group, err := runtime.deps.Actor.Create(ctx, request)
	*groupID = group.ID
	runtime.observe(ArtifactBuilderRuntimeEvent{Stage: "create", GroupID: group.ID, Status: group.Status})
	if err != nil {
		return group, nil, err
	}
	lifecycle, err := eci.NewArtifactBuilderLifecycle(request)
	if err != nil {
		return group, nil, err
	}
	if err := lifecycle.ObserveCreated(group); err != nil {
		return group, nil, err
	}
	return group, lifecycle, nil
}

// observeBuilder 有界轮询 lifecycle，并只取得一次 bounded builder log。
func (runtime *ArtifactBuilderRuntime) observeBuilder(ctx context.Context, lifecycle *eci.ArtifactBuilderLifecycle, group eci.ArtifactBuilderGroup) (eci.ArtifactBuilderGroup, string, error) {
	group, err := runtime.poll(ctx, lifecycle, group)
	if err != nil {
		return group, "", err
	}
	logs, err := runtime.deps.Actor.Logs(ctx, group.ID, group.Container.Name)
	if err != nil {
		return group, "", err
	}
	if err := lifecycle.ObserveLogs(logs); err != nil {
		return group, "", err
	}
	return group, logs, nil
}

// collectEvidence 严格解码输出、回执并验证最终 portable artifact。
func (runtime *ArtifactBuilderRuntime) collectEvidence(ctx, pollCtx context.Context, input remoteci.SourceBaselineBuildInput, inputDigest string, group eci.ArtifactBuilderGroup, logs string) (remoteci.SourceBaselineBuildEvidence, error) {
	output, err := decodeArtifactBuildRuntimeOutput(logs)
	if err != nil {
		return remoteci.SourceBaselineBuildEvidence{}, err
	}
	if err := validateArtifactBuildRuntimeOutput(output, inputDigest); err != nil {
		return remoteci.SourceBaselineBuildEvidence{}, err
	}
	receipt, err := runtime.readReceipt(pollCtx, output, input.ArtifactPrefix)
	if err != nil {
		return remoteci.SourceBaselineBuildEvidence{}, err
	}
	if group.Container.ExitCode == nil {
		return remoteci.SourceBaselineBuildEvidence{}, errors.New("artifact builder terminal group omitted exit code")
	}
	evidence := remoteci.SourceBaselineBuildEvidence{Request: input, InputDigest: inputDigest, Provider: output.ExecutionProvider,
		ContainerGroupID: group.ID, ContainerGroupStatus: group.Status, ContainerExitCode: *group.Container.ExitCode,
		StartedAt: group.Container.StartTime.UTC().Format(time.RFC3339Nano), FinishedAt: group.Container.FinishTime.UTC().Format(time.RFC3339Nano),
		ArtifactKey: output.ArtifactKey, ReceiptKey: output.ReceiptKey, Receipt: receipt}
	if err := evidence.ValidateAgainst(input); err != nil {
		return remoteci.SourceBaselineBuildEvidence{}, err
	}
	runtime.observe(ArtifactBuilderRuntimeEvent{Stage: "receipt", GroupID: group.ID, Key: runtimeDigestBytes([]byte(output.ReceiptKey)), Digest: output.ReceiptDigest})
	if err := runtime.validateArtifact(ctx, output, receipt); err != nil {
		return remoteci.SourceBaselineBuildEvidence{}, err
	}
	runtime.observe(ArtifactBuilderRuntimeEvent{Stage: "artifact", GroupID: group.ID, Key: runtimeDigestBytes([]byte(output.ArtifactKey)), Digest: output.ArtifactSHA256})
	return evidence, nil
}

// readReceipt 读取并核对 canonical receipt 的原始摘要和 producer digest。
func (runtime *ArtifactBuilderRuntime) readReceipt(ctx context.Context, output artifactBuildRuntimeOutput, artifactPrefix string) (remoteci.SourceBaselineProducerReceipt, error) {
	receiptBytes, metadata, err := runtime.deps.Store.ReadReceipt(ctx, output.ReceiptKey, 1<<20)
	if err != nil {
		return remoteci.SourceBaselineProducerReceipt{}, err
	}
	receipt, err := remoteci.DecodeSourceBaselineProducerReceipt(receiptBytes)
	if err != nil {
		return remoteci.SourceBaselineProducerReceipt{}, err
	}
	if err := validateArtifactBuildRuntimeReceipt(output, receiptBytes, metadata, receipt, artifactPrefix); err != nil {
		return remoteci.SourceBaselineProducerReceipt{}, err
	}
	return receipt, nil
}

func (runtime *ArtifactBuilderRuntime) validateRequestBinding(input remoteci.SourceBaselineBuildInput, inputDigest string, request eci.ArtifactBuilderRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	requestInput := remoteci.SourceBaselineBuildInput{
		SchemaVersion:  remoteci.SourceBaselineBuilderInputSchema,
		ArtifactPrefix: input.ArtifactPrefix,
		RepositoryID:   input.RepositoryID, ObjectFormat: input.ObjectFormat,
		CommitSHA: input.CommitSHA, TreeSHA: input.TreeSHA,
		InputObjectKey: request.Input.Key, InputSHA256: request.Input.SHA256,
		InputSizeBytes: request.Input.SizeBytes, ExpectedBuilderDigest: request.Input.ExpectedBuilderDigest,
		ExpectedToolchainDigest: request.Input.ExpectedToolchainDigest, ProtocolABIDigest: request.Input.ProtocolABIDigest,
		WorkloadManifestABIDigest: request.Input.WorkloadManifestABIDigest,
	}
	requestDigest, err := remoteci.SourceBaselineBuilderInputDigest(requestInput)
	if err != nil {
		return err
	}
	if requestDigest != inputDigest || !artifactBuilderInputsEqual(input, request.Input) {
		return errors.New("artifact builder request input digest does not match staged input")
	}
	return nil
}

// artifactBuilderInputsEqual 逐字段核对请求是否仍指向已 staging 的输入。
func artifactBuilderInputsEqual(input remoteci.SourceBaselineBuildInput, request eci.ArtifactBuilderInput) bool {
	return artifactBuilderInputMaterialEqual(input, request) && artifactBuilderInputSourceEqual(input, request) && artifactBuilderInputABIEqual(input, request)
}

// artifactBuilderInputMaterialEqual 核对 staging object 的 raw key、摘要和大小。
func artifactBuilderInputMaterialEqual(input remoteci.SourceBaselineBuildInput, request eci.ArtifactBuilderInput) bool {
	return request.ArtifactPrefix == input.ArtifactPrefix && request.Key == input.InputObjectKey && request.SHA256 == input.InputSHA256 && request.SizeBytes == input.InputSizeBytes
}

// artifactBuilderInputSourceEqual 核对 repository、object format 与 exact source identity。
func artifactBuilderInputSourceEqual(input remoteci.SourceBaselineBuildInput, request eci.ArtifactBuilderInput) bool {
	return request.RepositoryID == input.RepositoryID && request.ObjectFormat == input.ObjectFormat && request.SourceCommitSHA == input.CommitSHA && request.SourceTreeSHA == input.TreeSHA
}

// artifactBuilderInputABIEqual 核对 expected builder/toolchain 与 protocol ABI。
func artifactBuilderInputABIEqual(input remoteci.SourceBaselineBuildInput, request eci.ArtifactBuilderInput) bool {
	return request.ExpectedBuilderDigest == input.ExpectedBuilderDigest && request.ExpectedToolchainDigest == input.ExpectedToolchainDigest && request.ProtocolABIDigest == input.ProtocolABIDigest && request.WorkloadManifestABIDigest == input.WorkloadManifestABIDigest
}

// poll 沿同一 group 的 lifecycle 有界等待终态，禁止重建或创建第二 group。
func (runtime *ArtifactBuilderRuntime) poll(ctx context.Context, lifecycle *eci.ArtifactBuilderLifecycle, group eci.ArtifactBuilderGroup) (eci.ArtifactBuilderGroup, error) {
	deadline := runtime.deps.Clock().Add(runtime.deps.PollTimeout)
	for !artifactBuilderTerminal(lifecycle.State()) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return group, errors.Join(eci.ErrArtifactBuilderCancelled, ctxErr)
		}
		if !runtime.deps.Clock().Before(deadline) {
			return group, errors.New("artifact builder polling timed out")
		}
		timer := time.NewTimer(runtime.deps.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return group, errors.Join(eci.ErrArtifactBuilderCancelled, ctx.Err())
		case <-timer.C:
		}
		observed, describeErr := runtime.deps.Actor.Describe(ctx, group.ID)
		if describeErr != nil {
			return group, describeErr
		}
		runtime.observe(ArtifactBuilderRuntimeEvent{Stage: "describe", GroupID: observed.ID, Status: observed.Status})
		if observeErr := lifecycle.Observe(observed); observeErr != nil {
			return observed, observeErr
		}
		group = observed
	}
	if lifecycle.State() != eci.ArtifactBuilderStateSucceeded {
		return group, fmt.Errorf("artifact builder group ended in %s", lifecycle.State())
	}
	return group, nil
}

func artifactBuilderTerminal(state eci.ArtifactBuilderState) bool {
	switch state {
	case eci.ArtifactBuilderStateSucceeded, eci.ArtifactBuilderStateFailed, eci.ArtifactBuilderStateError, eci.ArtifactBuilderStateCancelled, eci.ArtifactBuilderStateIdentityDrift:
		return true
	default:
		return false
	}
}

// validateArtifact 通过 store resolver 复核最终 bundle 的 bytes、receipt 和 Git closure。
func (runtime *ArtifactBuilderRuntime) validateArtifact(ctx context.Context, output artifactBuildRuntimeOutput, receipt remoteci.SourceBaselineProducerReceipt) error {
	body, size, metadata, err := runtime.deps.Store.Resolve(ctx, remoteci.PortableSourceBaselineArtifactKind, output.ArtifactKey)
	if err != nil {
		return err
	}
	if body == nil {
		return errors.New("portable artifact store returned no body")
	}
	if metadata.SHA256 != output.ArtifactSHA256 || metadata.SizeBytes != output.ArtifactSizeBytes || size != output.ArtifactSizeBytes {
		_ = body.Close()
		return errors.New("portable artifact store identity does not match build output")
	}
	target := remoteci.PortableRunTarget{
		RepositoryID: receipt.RepositoryID, ObjectFormat: receipt.ObjectFormat,
		SourceBaseline: remoteci.PortableSourceBaseline{
			ObjectFormat: receipt.ObjectFormat, BaselineTreeSHA: receipt.BaselineTreeSHA,
			BaselineCommitSHA: receipt.BaselineCommitSHA, BundleContractID: remoteci.PortableBundleContractID,
			ArtifactKind: remoteci.PortableSourceBaselineArtifactKind, ArtifactKey: output.ArtifactKey,
			ArtifactSHA256: output.ArtifactSHA256, ArtifactSizeBytes: output.ArtifactSizeBytes,
		},
	}
	return remoteci.ValidatePortableSourceBaselineArtifact(ctx, runtimeArtifactResolver{body: body, size: size, receipt: receipt, kind: remoteci.PortableSourceBaselineArtifactKind, key: output.ArtifactKey}, target)
}

type runtimeArtifactResolver struct {
	body    io.ReadCloser
	size    int64
	receipt remoteci.SourceBaselineProducerReceipt
	kind    string
	key     string
}

// Resolve 只允许 validator 请求本次 output 已绑定的 kind/key。
func (resolver runtimeArtifactResolver) Resolve(_ context.Context, kind, key string) (io.ReadCloser, int64, remoteci.SourceBaselineProducerReceipt, error) {
	if kind != resolver.kind || key != resolver.key {
		return nil, 0, remoteci.SourceBaselineProducerReceipt{}, errors.New("portable artifact resolver identity drifted")
	}
	return resolver.body, resolver.size, resolver.receipt, nil
}

// cleanup 使用 WithoutCancel 和统一 deadline 清理 group 与 input staging。
func (runtime *ArtifactBuilderRuntime) cleanup(ctx context.Context, request eci.ArtifactBuilderRequest, requestReady bool, groupID, inputKey string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtime.deps.CleanupTimeout)
	defer cancel()
	var cleanupErr error
	runtime.observe(ArtifactBuilderRuntimeEvent{Stage: "cleanup", GroupID: groupID, Cleanup: true})
	if err := runtime.cleanupBuilderGroup(cleanupCtx, request, requestReady, groupID); err != nil {
		cleanupErr = errors.Join(cleanupErr, errors.New("artifact builder group cleanup failed"))
	}
	if err := runtime.deps.Store.Delete(cleanupCtx, inputKey); err != nil {
		cleanupErr = errors.Join(cleanupErr, errors.New("artifact builder input cleanup failed"))
	}
	if err := waitRuntimeAbsence(cleanupCtx, runtime.deps.PollInterval, func() (bool, error) {
		return runtime.deps.Store.ConfirmAbsent(cleanupCtx, inputKey)
	}); err != nil {
		cleanupErr = errors.Join(cleanupErr, errors.New("artifact builder input absence proof failed"))
	}
	return cleanupErr
}

const cleanupRequestAbsenceRounds = 2

type builderGroupCleanupState struct {
	groupID              string
	requestAbsenceRounds int
	idAbsent             bool
	deleteIssued         bool
	permanentErr         error
}

// cleanupBuilderGroup 轮询 request identity 与已知 group ID，直到两者都证明不存在。
func (runtime *ArtifactBuilderRuntime) cleanupBuilderGroup(ctx context.Context, request eci.ArtifactBuilderRequest, requestReady bool, groupID string) error {
	if !requestReady && groupID == "" {
		return nil
	}
	requestAbsenceRounds, idAbsent := cleanupRequestInitialState(requestReady, groupID)
	state := builderGroupCleanupState{groupID: groupID, requestAbsenceRounds: requestAbsenceRounds, idAbsent: idAbsent}
	for {
		if requestReady {
			runtime.reconcileCleanupRequest(ctx, request, &state)
		}
		if err := runtime.cleanupKnownGroup(ctx, &state); err != nil {
			return err
		}
		if state.requestAbsenceRounds >= cleanupRequestAbsenceRounds && state.idAbsent {
			return state.permanentErr
		}
		if err := waitRuntimeCleanupInterval(ctx, runtime.deps.PollInterval); err != nil {
			return errors.Join(state.permanentErr, errors.New("artifact builder group cleanup deadline exceeded"))
		}
	}
}

// reconcileCleanupRequest 把一次 name+tags 查询结果合并到 cleanup 状态。
func (runtime *ArtifactBuilderRuntime) reconcileCleanupRequest(ctx context.Context, request eci.ArtifactBuilderRequest, state *builderGroupCleanupState) {
	match, err := runtime.deps.Actor.ReconcileRequest(ctx, request)
	if match.Present {
		state.requestAbsenceRounds = 0
		bindCleanupRequestMatch(state, match)
	} else if err == nil {
		state.requestAbsenceRounds++
	}
	if err != nil {
		state.requestAbsenceRounds = 0
		if errors.Is(err, eci.ErrArtifactBuilderIdentityDrift) {
			state.permanentErr = errors.Join(state.permanentErr, errors.New("artifact builder request identity drifted"))
		}
		return
	}
}

// bindCleanupRequestMatch 绑定 later-visible group，并拒绝同一 request 指向不同 ID。
func bindCleanupRequestMatch(state *builderGroupCleanupState, match eci.ArtifactBuilderRequestMatch) {
	if match.GroupID == "" {
		state.permanentErr = errors.Join(state.permanentErr, errors.New("artifact builder request match omitted group identity"))
		return
	}
	if state.idAbsent || state.groupID == "" {
		state.groupID, state.idAbsent, state.deleteIssued = match.GroupID, false, false
		return
	}
	if state.groupID != match.GroupID {
		state.permanentErr = errors.Join(state.permanentErr, errors.New("artifact builder request identity drifted"))
	}
}

// cleanupKnownGroup 删除并回读当前已知 group ID；瞬时 provider 错误留给下一轮重试。
func (runtime *ArtifactBuilderRuntime) cleanupKnownGroup(ctx context.Context, state *builderGroupCleanupState) error {
	if state.groupID == "" || state.idAbsent {
		return nil
	}
	absent, err := runtime.ensureGroupAbsent(ctx, state.groupID, &state.deleteIssued)
	if err != nil {
		if ctx.Err() != nil {
			return errors.Join(state.permanentErr, errors.New("artifact builder group cleanup deadline exceeded"))
		}
		return nil
	}
	state.idAbsent = absent
	return nil
}

func cleanupRequestInitialState(requestReady bool, groupID string) (int, bool) {
	if !requestReady {
		return cleanupRequestAbsenceRounds, groupID == ""
	}
	return 0, groupID == ""
}

func (runtime *ArtifactBuilderRuntime) ensureGroupAbsent(ctx context.Context, groupID string, deleteIssued *bool) (bool, error) {
	if !*deleteIssued {
		if err := runtime.deps.Actor.Delete(ctx, groupID); err != nil {
			return false, err
		}
		*deleteIssued = true
	}
	return runtime.deps.Actor.ConfirmAbsent(ctx, groupID)
}

func waitRuntimeCleanupInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// waitRuntimeAbsence 在 cleanup deadline 内轮询 provider/store 的 absence proof。
func waitRuntimeAbsence(ctx context.Context, interval time.Duration, confirm func() (bool, error)) error {
	for {
		absent, err := confirm()
		if err != nil {
			return err
		}
		if absent {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (runtime *ArtifactBuilderRuntime) observe(event ArtifactBuilderRuntimeEvent) {
	if runtime.deps.Observer != nil {
		runtime.deps.Observer(event)
	}
}

func isNilRuntimeDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type artifactBuildRuntimeOutput struct {
	Schema                 string `json:"schema"`
	Status                 string `json:"status"`
	ExecutionProvider      string `json:"execution_provider"`
	InputDigest            string `json:"input_digest"`
	ArtifactKey            string `json:"artifact_key"`
	ReceiptKey             string `json:"receipt_key"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ArtifactSizeBytes      int64  `json:"artifact_size_bytes"`
	ArtifactIdentityDigest string `json:"artifact_identity_digest"`
	ReceiptSHA256          string `json:"receipt_sha256"`
	ReceiptDigest          string `json:"receipt_digest"`
}

func decodeArtifactBuildRuntimeOutput(data string) (artifactBuildRuntimeOutput, error) {
	payload, err := extractArtifactBuildPayload(data)
	if err != nil {
		return artifactBuildRuntimeOutput{}, err
	}
	return decodeCanonicalArtifactBuildOutput(payload)
}

// extractArtifactBuildPayload 从混合 progress/log 输出提取唯一带前缀的结果行。
func extractArtifactBuildPayload(data string) ([]byte, error) {
	var payload []byte
	progressSeen, resultSeen := false, false
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "progress: ") {
			if !runtimeProgressLine(line) {
				return nil, errors.New("artifact build output contains an unsupported progress line")
			}
			progressSeen = true
			continue
		}
		if strings.HasPrefix(line, artifactBuildRuntimePrefix) {
			if resultSeen {
				return nil, errors.New("artifact build output contains duplicate results")
			}
			resultSeen = true
			payload = []byte(strings.TrimPrefix(line, artifactBuildRuntimePrefix))
			continue
		}
		return nil, errors.New("artifact build output contains an unexpected line")
	}
	if !resultSeen || (progressSeen && len(payload) == 0) {
		return nil, errors.New("artifact build output is missing its single result")
	}
	return bytes.TrimSpace(payload), nil
}

// decodeCanonicalArtifactBuildOutput 严格解码并复核结果 JSON 的 canonical bytes。
func decodeCanonicalArtifactBuildOutput(payload []byte) (artifactBuildRuntimeOutput, error) {
	if len(payload) == 0 || bytes.Contains(payload, []byte("\x00")) || rejectRuntimeJSONDuplicates(payload) != nil {
		return artifactBuildRuntimeOutput{}, errors.New("artifact build output is empty or malformed")
	}
	var output artifactBuildRuntimeOutput
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return output, errors.New("artifact build output is not strict JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return output, errors.New("artifact build output has trailing JSON")
	}
	canonical, err := json.Marshal(output)
	if err != nil || !bytes.Equal(canonical, payload) {
		return artifactBuildRuntimeOutput{}, errors.New("artifact build output is not canonical")
	}
	return output, nil
}

func runtimeProgressLine(line string) bool {
	switch line {
	case "progress: artifact build arguments accepted", "progress: artifact input client configured", "progress: artifact build started", "progress: artifact build completed":
		return true
	default:
		return false
	}
}

func rejectRuntimeJSONDuplicates(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkRuntimeJSON(decoder); err != nil {
		return err
	}
	return nil
}

// walkRuntimeJSON 递归拒绝 null、重复键和异常 JSON 容器。
func walkRuntimeJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("JSON null is not allowed")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return walkRuntimeJSONObject(decoder)
	case '[':
		return walkRuntimeJSONArray(decoder)
	default:
		return errors.New("JSON delimiter is invalid")
	}
}

// walkRuntimeJSONObject 校验对象键唯一并递归读取每个值。
func walkRuntimeJSONObject(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("JSON object key is duplicated")
		}
		seen[name] = struct{}{}
		if err := walkRuntimeJSON(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("JSON object is unterminated")
	}
	return nil
}

// walkRuntimeJSONArray 递归读取数组值并校验结束分隔符。
func walkRuntimeJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := walkRuntimeJSON(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return errors.New("JSON array is unterminated")
	}
	return nil
}

func validateArtifactBuildRuntimeOutput(output artifactBuildRuntimeOutput, inputDigest string) error {
	if err := validateArtifactBuildRuntimeOutputProtocol(output, inputDigest); err != nil {
		return err
	}
	return validateArtifactBuildRuntimeOutputIdentity(output)
}

func validateArtifactBuildRuntimeOutputProtocol(output artifactBuildRuntimeOutput, inputDigest string) error {
	if output.Schema != artifactBuildRuntimeSchema || output.Status != "NOT_VERIFIED" || output.ExecutionProvider != remoteci.PortableExecutionProvider || output.InputDigest != inputDigest {
		return errors.New("artifact build output protocol identity is invalid")
	}
	return nil
}

// validateArtifactBuildRuntimeOutputIdentity 校验结果中所有内容寻址字段。
func validateArtifactBuildRuntimeOutputIdentity(output artifactBuildRuntimeOutput) error {
	if output.ArtifactKey == "" || output.ReceiptKey == "" || !runtimeDigest(output.ArtifactSHA256) || !runtimeDigest(output.ArtifactIdentityDigest) || !runtimeDigest(output.ReceiptSHA256) || !runtimeDigest(output.ReceiptDigest) || output.ArtifactSizeBytes <= 0 {
		return errors.New("artifact build output content identity is incomplete")
	}
	return nil
}

func validateArtifactBuildRuntimeReceipt(output artifactBuildRuntimeOutput, raw []byte, metadata oss.PortableArtifactMetadata, receipt remoteci.SourceBaselineProducerReceipt, artifactPrefix string) error {
	rawDigest := runtimeDigestBytes(raw)
	receiptDigest, err := remoteci.SourceBaselineProducerReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if err := validateArtifactBuildRuntimeReceiptMaterial(output, raw, metadata, rawDigest, receiptDigest, receipt); err != nil {
		return err
	}
	return validateArtifactBuildRuntimeReceiptKeys(output, receipt, artifactPrefix)
}

// validateArtifactBuildRuntimeReceiptMaterial 核对 receipt 正文、对象元数据和 artifact 绑定。
func validateArtifactBuildRuntimeReceiptMaterial(output artifactBuildRuntimeOutput, raw []byte, metadata oss.PortableArtifactMetadata, rawDigest, receiptDigest string, receipt remoteci.SourceBaselineProducerReceipt) error {
	if output.ReceiptSHA256 != rawDigest || metadata.SHA256 != rawDigest || metadata.SizeBytes != int64(len(raw)) || output.ReceiptDigest != receiptDigest || receipt.ArtifactSHA256 != output.ArtifactSHA256 || receipt.ArtifactSizeBytes != output.ArtifactSizeBytes || receipt.ArtifactIdentityDigest != output.ArtifactIdentityDigest {
		return errors.New("artifact build receipt identity does not match output")
	}
	return nil
}

func validateArtifactBuildRuntimeReceiptKeys(output artifactBuildRuntimeOutput, receipt remoteci.SourceBaselineProducerReceipt, artifactPrefix string) error {
	artifactKey, receiptKey, err := remoteci.CanonicalSourceBaselineBuilderKeys(artifactPrefix, receipt.RepositoryID, receipt.ObjectFormat, receipt.SourceTreeSHA, receipt.ArtifactIdentityDigest)
	if err != nil || output.ArtifactKey != artifactKey || output.ReceiptKey != receiptKey {
		return errors.New("artifact build receipt keys are not canonical")
	}
	return nil
}

func runtimeDigest(value string) bool {
	return len(value) == len("sha256:")+64 && strings.HasPrefix(value, "sha256:") && strings.TrimPrefix(value, "sha256:") == strings.ToLower(strings.TrimPrefix(value, "sha256:")) && strings.Trim(strings.TrimPrefix(value, "sha256:"), "0123456789abcdef") == ""
}

func runtimeDigestBytes(data []byte) string {
	return digestBytes(data)
}
