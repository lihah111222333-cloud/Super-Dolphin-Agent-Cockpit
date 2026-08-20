package remoteci

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	BuilderSimulationResultSchema = "remote-ci-builder-simulation-result/v1"
	BuilderSimulationEventSchema  = "remote-ci-builder-simulation-event/v1"

	BuilderSimulationResultStatusNotVerified = "NOT_VERIFIED"
	BuilderSimulationOutcomeSucceeded        = "succeeded"
	BuilderSimulationOutcomeFailed           = "failed"

	BuilderSimulationExitCodeSuccess              = 0
	BuilderSimulationExitCodeBuildFailure         = 1
	BuilderSimulationExitCodeContractFailure      = 2
	BuilderSimulationExitCodeOutputChannelFailure = 3
	BuilderSimulationExitCodeCleanupFailure       = 4

	BuilderSimulationArtifactKindPortableRuntime   = PortableSourceBaselineArtifactKind
	BuilderSimulationPublicationStateValidatedPair = "validated_pair"
	BuilderSimulationPublicationStatePartial       = "partial"
	BuilderSimulationPublicationStateNotApplicable = "not_applicable"

	BuilderSimulationCloudStatusNotStarted = "not_started"
	BuilderSimulationCloudStatusSucceeded  = "succeeded"
	BuilderSimulationCloudStatusFailed     = "failed"

	BuilderSimulationAuthorityEffectsNone = "none"

	BuilderSimulationEventStateStarted   = "started"
	BuilderSimulationEventStateSucceeded = "succeeded"
	BuilderSimulationEventStateFailed    = "failed"
	BuilderSimulationEventStateSkipped   = "skipped"

	BuilderSimulationEventStageRequestValidated    = "request_validated"
	BuilderSimulationEventStageEngineBound         = "engine_bound"
	BuilderSimulationEventStageTargetManifestBound = "target_manifest_bound"
	BuilderSimulationEventStageInputBundleBuilt    = "input_bundle_built"
	BuilderSimulationEventStageInputStaged         = "input_staged"
	BuilderSimulationEventStageECICreateRequested  = "eci_create_requested"
	BuilderSimulationEventStageECIGroupObserved    = "eci_group_observed"
	BuilderSimulationEventStageArtifactValidated   = "artifact_validated"
	BuilderSimulationEventStageCleanupStarted      = "cleanup_started"
	BuilderSimulationEventStageCleanupGroupAbsent  = "cleanup_group_absent"
	BuilderSimulationEventStageCleanupInputAbsent  = "cleanup_input_absent"
	BuilderSimulationEventStageTerminal            = "terminal"

	builderSimulationResultMaxBytes = 1 << 20
	builderSimulationEventMaxBytes  = 1 << 20
)

type builderSimulationRequirement struct {
	valid   bool
	message string
}

// validateBuilderSimulationRequirements 以 fail-fast 方式执行固定字段守卫。
func validateBuilderSimulationRequirements(requirements ...builderSimulationRequirement) error {
	for _, requirement := range requirements {
		if !requirement.valid {
			return errors.New(requirement.message)
		}
	}
	return nil
}

// BuilderSimulationResult 是 builder-simulation stdout 的唯一 terminal DTO。
// 它只描述非权威演练结果，不授予 CI、workload、release 或 PASS authority。
type BuilderSimulationResult struct {
	SchemaVersion       string                          `json:"schema_version"`
	Status              string                          `json:"status"`
	Outcome             string                          `json:"outcome"`
	ProcessExitCode     int                             `json:"process_exit_code"`
	OperationID         string                          `json:"operation_id"`
	RequestDigest       string                          `json:"request_digest"`
	EngineBindingDigest string                          `json:"engine_binding_digest"`
	TargetBindingDigest string                          `json:"target_binding_digest"`
	Artifact            BuilderSimulationResultArtifact `json:"artifact"`
	Cloud               BuilderSimulationResultCloud    `json:"cloud"`
	Cleanup             BuilderSimulationResultCleanup  `json:"cleanup"`
	AuthorityEffects    string                          `json:"authority_effects"`
	Error               BuilderSimulationResultError    `json:"error"`
}

// BuilderSimulationResultArtifact 是 artifact/receipt 的公开摘要，不含原始对象引用。
type BuilderSimulationResultArtifact struct {
	Kind           string `json:"kind"`
	BytesSHA256    string `json:"bytes_sha256"`
	IdentityDigest string `json:"identity_digest"`
	SizeBytes      int64  `json:"size_bytes"`
	ReceiptDigest  string `json:"receipt_digest"`
}

// BuilderSimulationResultCloud 是 provider terminal identity 的安全投影。
type BuilderSimulationResultCloud struct {
	Provider          string `json:"provider"`
	Group             string `json:"group"`
	Status            string `json:"status"`
	ContainerExitCode int    `json:"container_exit_code"`
	StartedAt         string `json:"started_at"`
	FinishedAt        string `json:"finished_at"`
}

// BuilderSimulationResultCleanup 是 side-effect cleanup 的双 absence proof。
type BuilderSimulationResultCleanup struct {
	ContainerGroupAbsent bool   `json:"container_group_absent"`
	InputStagingAbsent   bool   `json:"input_staging_absent"`
	PublicationState     string `json:"publication_state"`
	ConfirmedAt          string `json:"confirmed_at"`
}

// BuilderSimulationResultError 是安全、稳定的非权威错误摘要。
type BuilderSimulationResultError struct {
	Code      string `json:"code"`
	Stage     string `json:"stage"`
	Retryable bool   `json:"retryable"`
}

// BuilderSimulationEvent 是 observable stderr 的单行 JSONL 事件。
type BuilderSimulationEvent struct {
	SchemaVersion  string `json:"schema_version"`
	OperationID    string `json:"operation_id"`
	Seq            uint64 `json:"seq"`
	Time           string `json:"time"`
	Stage          string `json:"stage"`
	State          string `json:"state"`
	RequestDigest  string `json:"request_digest"`
	ResourceID     string `json:"resource_id"`
	ResourceDigest string `json:"resource_digest"`
	ErrorCode      string `json:"error_code"`
}

// Validate 校验 terminal 结果和冻结的退出码矩阵。
func (result BuilderSimulationResult) Validate() error {
	if err := validateBuilderSimulationResultHeader(result); err != nil {
		return err
	}
	if err := validateBuilderSimulationResultArtifact(result.Artifact); err != nil {
		return err
	}
	if err := validateBuilderSimulationResultCloud(result.Cloud); err != nil {
		return err
	}
	if err := validateBuilderSimulationResultCleanup(result.Cleanup); err != nil {
		return err
	}
	if result.AuthorityEffects != BuilderSimulationAuthorityEffectsNone {
		return errors.New("builder simulation result authority effects must be none")
	}
	return validateBuilderSimulationResultExitCode(result)
}

// validateBuilderSimulationResultHeader 校验 terminal header、operation 和 binding digest。
func validateBuilderSimulationResultHeader(result BuilderSimulationResult) error {
	return validateBuilderSimulationRequirements(
		builderSimulationRequirement{result.SchemaVersion == BuilderSimulationResultSchema, "builder simulation result schema is invalid"},
		builderSimulationRequirement{result.Status == BuilderSimulationResultStatusNotVerified, "builder simulation result status is invalid"},
		builderSimulationRequirement{validBuilderSimulationToken(result.OperationID, 128), "builder simulation result operation is invalid"},
		builderSimulationRequirement{validPortableDigest(result.RequestDigest), "builder simulation result request digest is invalid"},
		builderSimulationRequirement{validPortableDigest(result.EngineBindingDigest), "builder simulation result engine digest is invalid"},
		builderSimulationRequirement{validPortableDigest(result.TargetBindingDigest), "builder simulation result target digest is invalid"},
		builderSimulationRequirement{result.Outcome == BuilderSimulationOutcomeSucceeded || result.Outcome == BuilderSimulationOutcomeFailed, "builder simulation result outcome is invalid"},
	)
}

// validateBuilderSimulationResultExitCode 应用冻结的五类 process exit matrix。
func validateBuilderSimulationResultExitCode(result BuilderSimulationResult) error {
	switch result.ProcessExitCode {
	case BuilderSimulationExitCodeSuccess:
		return validateBuilderSimulationSuccessResult(result)
	case BuilderSimulationExitCodeBuildFailure:
		return validateBuilderSimulationBuildFailureResult(result)
	case BuilderSimulationExitCodeContractFailure:
		return validateBuilderSimulationContractFailureResult(result)
	case BuilderSimulationExitCodeOutputChannelFailure:
		return errors.New("builder simulation output-channel exit code cannot form a result JSON")
	case BuilderSimulationExitCodeCleanupFailure:
		return validateBuilderSimulationCleanupFailureResult(result)
	default:
		return errors.New("builder simulation result exit code is invalid")
	}
}

func validateBuilderSimulationSuccessResult(result BuilderSimulationResult) error {
	if err := validateBuilderSimulationRequirements(
		builderSimulationRequirement{result.Outcome == BuilderSimulationOutcomeSucceeded, "builder simulation success outcome is invalid"},
		builderSimulationRequirement{result.Artifact.Kind != "", "builder simulation success artifact is missing"},
		builderSimulationRequirement{result.Cleanup.PublicationState == BuilderSimulationPublicationStateValidatedPair, "builder simulation success publication is invalid"},
		builderSimulationRequirement{result.Cleanup.ContainerGroupAbsent, "builder simulation success group cleanup is unproven"},
		builderSimulationRequirement{result.Cleanup.InputStagingAbsent, "builder simulation success input cleanup is unproven"},
		builderSimulationRequirement{result.Cleanup.ConfirmedAt != "", "builder simulation success confirmation time is missing"},
		builderSimulationRequirement{result.Cloud.Group != "", "builder simulation success group is missing"},
		builderSimulationRequirement{result.Cloud.Status == BuilderSimulationCloudStatusSucceeded, "builder simulation success cloud status is invalid"},
		builderSimulationRequirement{result.Cloud.ContainerExitCode == 0, "builder simulation success container exit is invalid"},
		builderSimulationRequirement{result.Error == (BuilderSimulationResultError{}), "builder simulation success error must be empty"},
	); err != nil {
		return err
	}
	return validateBuilderSimulationResultTimes(result.Cloud.StartedAt, result.Cloud.FinishedAt)
}

// validateBuilderSimulationBuildFailureResult 校验 cleanup-proven 的构建或云失败。
func validateBuilderSimulationBuildFailureResult(result BuilderSimulationResult) error {
	if err := validateBuilderSimulationRequirements(
		builderSimulationRequirement{result.Outcome == BuilderSimulationOutcomeFailed, "builder simulation build failure outcome is invalid"},
		builderSimulationRequirement{result.Cleanup.ContainerGroupAbsent, "builder simulation build failure group cleanup is unproven"},
		builderSimulationRequirement{result.Cleanup.InputStagingAbsent, "builder simulation build failure input cleanup is unproven"},
		builderSimulationRequirement{result.Cleanup.ConfirmedAt != "", "builder simulation build failure confirmation is missing"},
	); err != nil {
		return err
	}
	if err := validateBuilderSimulationFailureCloudStatus(result.Cloud.Status); err != nil {
		return err
	}
	return validateBuilderSimulationResultError(result.Error)
}

func validateBuilderSimulationContractFailureResult(result BuilderSimulationResult) error {
	if err := validateBuilderSimulationRequirements(
		builderSimulationRequirement{result.Outcome == BuilderSimulationOutcomeFailed, "builder simulation contract failure outcome is invalid"},
		builderSimulationRequirement{builderSimulationArtifactEmpty(result.Artifact), "builder simulation contract failure artifact is invalid"},
	); err != nil {
		return err
	}
	if err := validateBuilderSimulationContractFailureCloud(result.Cloud); err != nil {
		return err
	}
	if err := validateBuilderSimulationContractFailureCleanup(result.Cleanup); err != nil {
		return err
	}
	return validateBuilderSimulationResultError(result.Error)
}

// validateBuilderSimulationContractFailureCloud 校验 side-effect 前的 cloud 空证据。
func validateBuilderSimulationContractFailureCloud(cloud BuilderSimulationResultCloud) error {
	return validateBuilderSimulationRequirements(
		builderSimulationRequirement{cloud.Group == "", "builder simulation contract failure group is present"},
		builderSimulationRequirement{cloud.Status == BuilderSimulationCloudStatusNotStarted, "builder simulation contract failure cloud status is invalid"},
		builderSimulationRequirement{cloud.ContainerExitCode == -1, "builder simulation contract failure container exit is invalid"},
		builderSimulationRequirement{cloud.StartedAt == "", "builder simulation contract failure start is present"},
		builderSimulationRequirement{cloud.FinishedAt == "", "builder simulation contract failure finish is present"},
	)
}

func validateBuilderSimulationContractFailureCleanup(cleanup BuilderSimulationResultCleanup) error {
	return validateBuilderSimulationRequirements(
		builderSimulationRequirement{cleanup.ContainerGroupAbsent, "builder simulation contract failure group cleanup is unproven"},
		builderSimulationRequirement{cleanup.InputStagingAbsent, "builder simulation contract failure input cleanup is unproven"},
		builderSimulationRequirement{cleanup.ConfirmedAt != "", "builder simulation contract failure confirmation is missing"},
		builderSimulationRequirement{cleanup.PublicationState == BuilderSimulationPublicationStateNotApplicable, "builder simulation contract failure publication is invalid"},
	)
}

func validateBuilderSimulationCleanupFailureResult(result BuilderSimulationResult) error {
	if result.Outcome != BuilderSimulationOutcomeFailed {
		return errors.New("builder simulation cleanup failure outcome is invalid")
	}
	if result.Cleanup.ContainerGroupAbsent && result.Cleanup.InputStagingAbsent {
		return errors.New("builder simulation cleanup failure requires an unproven absence")
	}
	return validateBuilderSimulationResultError(result.Error)
}

// validateBuilderSimulationResultArtifact 校验 artifact kind、bytes、identity 和 receipt 摘要。
func validateBuilderSimulationResultArtifact(artifact BuilderSimulationResultArtifact) error {
	if builderSimulationArtifactEmpty(artifact) {
		return nil
	}
	return validateBuilderSimulationRequirements(
		builderSimulationRequirement{artifact.Kind == BuilderSimulationArtifactKindPortableRuntime, "builder simulation result artifact kind is invalid"},
		builderSimulationRequirement{validPortableDigest(artifact.BytesSHA256), "builder simulation result artifact bytes digest is invalid"},
		builderSimulationRequirement{validPortableDigest(artifact.IdentityDigest), "builder simulation result artifact identity digest is invalid"},
		builderSimulationRequirement{artifact.SizeBytes > 0, "builder simulation result artifact size is invalid"},
		builderSimulationRequirement{validPortableDigest(artifact.ReceiptDigest), "builder simulation result artifact receipt digest is invalid"},
	)
}

func builderSimulationArtifactEmpty(artifact BuilderSimulationResultArtifact) bool {
	return artifact.Kind == "" && artifact.BytesSHA256 == "" && artifact.IdentityDigest == "" && artifact.SizeBytes == 0 && artifact.ReceiptDigest == ""
}

// validateBuilderSimulationResultCloud 校验 provider、safe group、status、exit 和时间形状。
func validateBuilderSimulationResultCloud(cloud BuilderSimulationResultCloud) error {
	if cloud.Provider != "aliyun-eci/v1" {
		return errors.New("builder simulation result provider is invalid")
	}
	if cloud.Group != "" && !validBuilderSimulationSafeID(cloud.Group) {
		return errors.New("builder simulation result group identity is invalid")
	}
	if cloud.Status != "" && !validBuilderSimulationSafeID(cloud.Status) {
		return errors.New("builder simulation result cloud status is invalid")
	}
	if cloud.ContainerExitCode < -1 {
		return errors.New("builder simulation result container exit code is invalid")
	}
	if (cloud.StartedAt == "") != (cloud.FinishedAt == "") {
		return errors.New("builder simulation result cloud times must be paired")
	}
	return nil
}

func validateBuilderSimulationFailureCloudStatus(status string) error {
	switch status {
	case BuilderSimulationCloudStatusNotStarted, BuilderSimulationCloudStatusFailed:
		return nil
	default:
		return errors.New("builder simulation build failure cloud status is invalid")
	}
}

// validateBuilderSimulationResultCleanup 校验 publication 与双 absence 的确认时间。
func validateBuilderSimulationResultCleanup(cleanup BuilderSimulationResultCleanup) error {
	switch cleanup.PublicationState {
	case BuilderSimulationPublicationStateValidatedPair, BuilderSimulationPublicationStatePartial, BuilderSimulationPublicationStateNotApplicable:
	default:
		return errors.New("builder simulation result publication state is invalid")
	}
	if cleanup.ContainerGroupAbsent && cleanup.InputStagingAbsent {
		if cleanup.ConfirmedAt == "" {
			return errors.New("builder simulation result cleanup confirmation time is required")
		}
		if _, err := parseBuilderSimulationUTC(cleanup.ConfirmedAt); err != nil {
			return errors.New("builder simulation result cleanup confirmation time is invalid")
		}
		return nil
	}
	if cleanup.ConfirmedAt != "" {
		return errors.New("builder simulation result cleanup confirmation time requires both absence proofs")
	}
	return nil
}

func validateBuilderSimulationResultError(resultError BuilderSimulationResultError) error {
	if !validBuilderSimulationSafeID(resultError.Code) || !validBuilderSimulationEventStage(resultError.Stage) {
		return errors.New("builder simulation result error is invalid")
	}
	return nil
}

func validateBuilderSimulationResultTimes(startedAt, finishedAt string) error {
	start, err := parseBuilderSimulationUTC(startedAt)
	if err != nil {
		return errors.New("builder simulation result start time is invalid")
	}
	finish, err := parseBuilderSimulationUTC(finishedAt)
	if err != nil {
		return errors.New("builder simulation result finish time is invalid")
	}
	if !finish.After(start) {
		return errors.New("builder simulation result finish time is invalid")
	}
	return nil
}

// CanonicalEncode 返回 bounded terminal JSON。
func (result BuilderSimulationResult) CanonicalEncode() ([]byte, error) {
	if err := result.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, errors.New("encode builder simulation result")
	}
	if len(data) == 0 || len(data) > builderSimulationResultMaxBytes {
		return nil, errors.New("builder simulation result exceeds bounded size")
	}
	return data, nil
}

// DecodeBuilderSimulationResult 严格解码唯一 terminal JSON。
func DecodeBuilderSimulationResult(data []byte) (BuilderSimulationResult, error) {
	var result BuilderSimulationResult
	if err := decodeBuilderSimulationResultJSON(data, &result); err != nil {
		return result, err
	}
	canonical, err := result.CanonicalEncode()
	if err != nil || !bytes.Equal(canonical, data) {
		return BuilderSimulationResult{}, errors.New("builder simulation result JSON is not canonical")
	}
	return result, nil
}

// decodeBuilderSimulationResultJSON 对 result/event 执行 bounded strict JSON 解码。
func decodeBuilderSimulationResultJSON(data []byte, target any) error {
	if len(data) == 0 || len(data) > builderSimulationResultMaxBytes || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("builder simulation result JSON size or root is invalid")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return errors.New("builder simulation result JSON has duplicate fields")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("decode builder simulation result")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("builder simulation result JSON has trailing data")
	}
	return nil
}

// Validate 校验单个事件的固定 schema、stage、token、digest 和 UTC 时间。
func (event BuilderSimulationEvent) Validate() error {
	if err := validateBuilderSimulationEventHeader(event); err != nil {
		return err
	}
	if _, err := parseBuilderSimulationUTC(event.Time); err != nil {
		return errors.New("builder simulation event time is invalid")
	}
	return validateBuilderSimulationEventOptionalFields(event)
}

// validateBuilderSimulationEventHeader 校验 event schema、seq、stage、state 和 request digest。
func validateBuilderSimulationEventHeader(event BuilderSimulationEvent) error {
	if event.SchemaVersion != BuilderSimulationEventSchema {
		return errors.New("builder simulation event schema is invalid")
	}
	if !validBuilderSimulationToken(event.OperationID, 128) || event.Seq == 0 {
		return errors.New("builder simulation event operation or sequence is invalid")
	}
	if !validBuilderSimulationEventStage(event.Stage) || !validBuilderSimulationEventState(event.State) {
		return errors.New("builder simulation event stage or state is invalid")
	}
	if !validPortableDigest(event.RequestDigest) {
		return errors.New("builder simulation event request digest is invalid")
	}
	return nil
}

// validateBuilderSimulationEventOptionalFields 校验可选安全资源摘要和稳定 error code。
func validateBuilderSimulationEventOptionalFields(event BuilderSimulationEvent) error {
	if event.ResourceID != "" && !validBuilderSimulationSafeID(event.ResourceID) {
		return errors.New("builder simulation event resource id is invalid")
	}
	if event.ResourceDigest != "" && !validPortableDigest(event.ResourceDigest) {
		return errors.New("builder simulation event resource digest is invalid")
	}
	if event.ErrorCode != "" && !validBuilderSimulationSafeID(event.ErrorCode) {
		return errors.New("builder simulation event error code is invalid")
	}
	return nil
}

// CanonicalEncode 返回单个事件的 canonical JSON（不带换行）。
func (event BuilderSimulationEvent) CanonicalEncode() ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return nil, errors.New("encode builder simulation event")
	}
	if len(data) == 0 || len(data) > builderSimulationEventMaxBytes {
		return nil, errors.New("builder simulation event exceeds bounded size")
	}
	return data, nil
}

// DecodeBuilderSimulationEvent 严格解码单个 canonical event JSON。
func DecodeBuilderSimulationEvent(data []byte) (BuilderSimulationEvent, error) {
	var event BuilderSimulationEvent
	if err := decodeBuilderSimulationResultJSON(data, &event); err != nil {
		return event, err
	}
	canonical, err := event.CanonicalEncode()
	if err != nil || !bytes.Equal(canonical, data) {
		return BuilderSimulationEvent{}, errors.New("builder simulation event JSON is not canonical")
	}
	return event, nil
}

// EncodeBuilderSimulationEventJSONL 返回一行、含终止换行符的 canonical event。
func EncodeBuilderSimulationEventJSONL(event BuilderSimulationEvent) ([]byte, error) {
	data, err := event.CanonicalEncode()
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeBuilderSimulationEventJSONL 严格解码 bounded JSONL 并检查序列。
func DecodeBuilderSimulationEventJSONL(data []byte) ([]BuilderSimulationEvent, error) {
	if len(data) == 0 || len(data) > builderSimulationEventMaxBytes || data[len(data)-1] != '\n' {
		return nil, errors.New("builder simulation event JSONL size or terminator is invalid")
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	events := make([]BuilderSimulationEvent, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			return nil, errors.New("builder simulation event JSONL contains an empty line")
		}
		event, err := DecodeBuilderSimulationEvent(line)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := ValidateBuilderSimulationEventSequence(events); err != nil {
		return nil, err
	}
	return events, nil
}

// ValidateBuilderSimulationEventSequence 检查同一 operation 的严格递增 seq。
func ValidateBuilderSimulationEventSequence(events []BuilderSimulationEvent) error {
	if len(events) == 0 {
		return errors.New("builder simulation event sequence is empty")
	}
	operation := events[0].OperationID
	requestDigest := events[0].RequestDigest
	var previous uint64
	for index, event := range events {
		if err := event.Validate(); err != nil {
			return err
		}
		if index == 0 {
			previous = event.Seq
			continue
		}
		if event.OperationID != operation || event.RequestDigest != requestDigest || event.Seq <= previous {
			return errors.New("builder simulation event sequence is not strictly increasing")
		}
		previous = event.Seq
	}
	return nil
}

func builderSimulationEventStages() []string {
	return []string{
		BuilderSimulationEventStageRequestValidated, BuilderSimulationEventStageEngineBound,
		BuilderSimulationEventStageTargetManifestBound, BuilderSimulationEventStageInputBundleBuilt,
		BuilderSimulationEventStageInputStaged, BuilderSimulationEventStageECICreateRequested,
		BuilderSimulationEventStageECIGroupObserved, BuilderSimulationEventStageArtifactValidated,
		BuilderSimulationEventStageCleanupStarted, BuilderSimulationEventStageCleanupGroupAbsent,
		BuilderSimulationEventStageCleanupInputAbsent, BuilderSimulationEventStageTerminal,
	}
}

func validBuilderSimulationEventStage(stage string) bool {
	return slices.Contains(builderSimulationEventStages(), stage)
}

func validBuilderSimulationEventState(state string) bool {
	switch state {
	case BuilderSimulationEventStateStarted, BuilderSimulationEventStateSucceeded, BuilderSimulationEventStateFailed, BuilderSimulationEventStateSkipped:
		return true
	default:
		return false
	}
}

// validBuilderSimulationSafeID 限制 resource/group/error 标识不携带路径或自由文本。
func validBuilderSimulationSafeID(value string) bool {
	if !validBuilderSimulationToken(value, 256) || strings.ContainsAny(value, "/\\") {
		return false
	}
	for _, runeValue := range value {
		if runeValue == ':' || runeValue == ' ' || runeValue == '\t' || runeValue == '\n' {
			return false
		}
	}
	return true
}

func parseBuilderSimulationUTC(value string) (time.Time, error) {
	if value == "" || !strings.HasSuffix(value, "Z") {
		return time.Time{}, errors.New("timestamp must be UTC RFC3339")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.UTC().Format(time.RFC3339Nano) != value {
		return time.Time{}, errors.New("timestamp is not canonical UTC RFC3339")
	}
	return parsed, nil
}
