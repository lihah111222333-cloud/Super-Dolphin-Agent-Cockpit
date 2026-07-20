package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

// coordinatorRowScanner 是持久化 coordinator job 行的严格读取边界。
type coordinatorRowScanner interface {
	Scan(...any) error
}

// scanCoordinatorJob 严格解码数据库行并执行完整持久字段守卫。
func scanCoordinatorJob(row coordinatorRowScanner, jobID string) (coordinatorJobRecord, error) {
	var record coordinatorJobRecord
	var planJSON, schedulerDependenciesJSON, resultsJSON, containerLabelsJSON, resourceWitnessJSON, receiptJSON []byte
	var entrypoint, authorityOwner, authorityAttestation, profile, state, submitted, activeGateID, containerPhase string
	var started, deadline, containerExited, completed, receiptID sql.NullString
	var resourceWitnessVerified sql.NullBool
	err := row.Scan(
		&record.InvocationID, &record.JobID, &record.EnqueueSequence, &record.RepositoryRoot,
		&entrypoint, &authorityOwner, &authorityAttestation,
		&planJSON, &profile, &record.JobSourceTreeSHA, &record.ImageProvenanceSourceTreeSHA,
		&state, &submitted, &record.SchedulerSubsequence, &schedulerDependenciesJSON,
		&started, &deadline, &containerExited, &completed, &activeGateID, &containerPhase,
		&record.ContainerID, &containerLabelsJSON, &record.ContainerImageReference,
		&record.ContainerConfigDigest, &record.ContainerHostConfigDigest, &resourceWitnessJSON,
		&record.ContainerResourceWitnessDigest, &resourceWitnessVerified, &record.SourceSnapshotDir, &record.RemovalProofDigest,
		&resultsJSON, &receiptID, &receiptJSON, &record.Error,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return coordinatorJobRecord{}, fmt.Errorf("%w: %q", errCoordinatorNotFound, jobID)
	}
	if err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("read coordinator job: %w", err)
	}
	if !resourceWitnessVerified.Valid {
		return coordinatorJobRecord{}, fmt.Errorf("%w: persisted container resource witness verification state is unknown", errCoordinatorState)
	}
	record.ContainerResourceWitnessVerified = resourceWitnessVerified.Bool
	record.Authority = submissionAuthority{
		Entrypoint:  gatecontract.CIEntrypointID(entrypoint),
		Owner:       gatecontract.CIEntrypointOwner(authorityOwner),
		Attestation: authorityAttestation,
	}
	record.Profile, record.State = gatecontract.Profile(profile), jobState(state)
	record.ActiveGateID = gatecontract.GateID(activeGateID)
	record.ContainerPhase = localci.FreshContainerLifecyclePhase(containerPhase)
	if err := decodeCoordinatorJSON(schedulerDependenciesJSON, &record.SchedulerDependencies); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted scheduler dependencies: %w", err)
	}
	if err := decodeOptionalCoordinatorJSON(containerLabelsJSON, &record.ContainerLabels); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted container labels: %w", err)
	}
	if err := decodeOptionalCoordinatorJSON(resourceWitnessJSON, &record.ContainerResourceWitness); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted container resource witness: %w", err)
	}
	return decodeScannedCoordinatorJob(
		record, planJSON, resultsJSON, receiptJSON, submitted, started, deadline, containerExited, completed, receiptID,
	)
}

// decodeScannedCoordinatorJob 严格解码 scan 已取得的结构化字段。
func decodeScannedCoordinatorJob(
	record coordinatorJobRecord,
	planJSON []byte,
	resultsJSON []byte,
	receiptJSON []byte,
	submitted string,
	started sql.NullString,
	deadline sql.NullString,
	containerExited sql.NullString,
	completed sql.NullString,
	receiptID sql.NullString,
) (coordinatorJobRecord, error) {
	if err := gatecontract.DecodeStrictJSON(planJSON, &record.Plan); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted coordinator plan: %w", err)
	}
	if err := decodeCoordinatorTimes(&record, submitted, started, deadline, containerExited, completed); err != nil {
		return coordinatorJobRecord{}, err
	}
	if err := decodeCoordinatorGateResults(&record, resultsJSON); err != nil {
		return coordinatorJobRecord{}, err
	}
	if err := decodeCoordinatorStoredReceipt(&record, receiptID, receiptJSON); err != nil {
		return coordinatorJobRecord{}, err
	}
	if err := validateCoordinatorRecord(record); err != nil {
		return coordinatorJobRecord{}, err
	}
	return record, nil
}

// decodeCoordinatorGateResults 解码可选的逐 gate 结果数组。
func decodeCoordinatorGateResults(record *coordinatorJobRecord, resultsJSON []byte) error {
	if len(resultsJSON) == 0 {
		return nil
	}
	if err := decodeCoordinatorJSON(resultsJSON, &record.GateResults); err != nil {
		return fmt.Errorf("decode persisted gate results: %w", err)
	}
	return nil
}

// decodeCoordinatorStoredReceipt 校验双列完整性并严格解码可选 receipt。
func decodeCoordinatorStoredReceipt(
	record *coordinatorJobRecord,
	receiptID sql.NullString,
	receiptJSON []byte,
) error {
	if receiptID.Valid != (len(receiptJSON) > 0) {
		return fmt.Errorf("%w: persisted receipt columns are incomplete", errCoordinatorState)
	}
	if !receiptID.Valid {
		return nil
	}
	var receipt gatecontract.ResultReceipt
	if err := decodeCoordinatorJSON(receiptJSON, &receipt); err != nil {
		return fmt.Errorf("decode persisted result receipt: %w", err)
	}
	record.ReceiptID, record.Receipt = receiptID.String, &receipt
	return nil
}

func decodeCoordinatorJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("coordinator JSON contains trailing data")
	}
	return nil
}

func decodeOptionalCoordinatorJSON(payload []byte, target any) error {
	if len(payload) == 0 {
		return nil
	}
	return decodeCoordinatorJSON(payload, target)
}

func decodeCoordinatorTimes(record *coordinatorJobRecord, submitted string, started, deadline, containerExited, completed sql.NullString) error {
	var err error
	record.SubmittedAt, err = time.Parse(time.RFC3339Nano, submitted)
	if err != nil {
		return fmt.Errorf("parse coordinator submitted_at: %w", err)
	}
	if record.StartedAt, err = parseCoordinatorTime(started); err != nil {
		return err
	}
	if record.Deadline, err = parseCoordinatorTime(deadline); err != nil {
		return err
	}
	if record.ContainerExitedAt, err = parseCoordinatorTime(containerExited); err != nil {
		return err
	}
	record.CompletedAt, err = parseCoordinatorTime(completed)
	return err
}

// validateCoordinatorRecord 拒绝 plan、profile、tree 或状态字段的任何持久化漂移。
func validateCoordinatorRecord(record coordinatorJobRecord) error {
	validators := []func(coordinatorJobRecord) error{
		validateCoordinatorIdentity,
		validateCoordinatorPlanState,
		validateCoordinatorRecordState,
		validateCoordinatorClock,
		validateCoordinatorContainer,
	}
	for _, validate := range validators {
		if err := validate(record); err != nil {
			return err
		}
	}
	return nil
}

// validateCoordinatorIdentity 校验持久化 invocation 与 job 主身份。
func validateCoordinatorIdentity(record coordinatorJobRecord) error {
	if record.InvocationID == "" || record.JobID == "" || record.EnqueueSequence == 0 {
		return fmt.Errorf("%w: persisted invocation/job identity is incomplete", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorPlanState 校验 plan 派生字段及可识别的 job 状态。
func validateCoordinatorPlanState(record coordinatorJobRecord) error {
	if err := record.Plan.Validate(); err != nil {
		return fmt.Errorf("%w: persisted plan is invalid: %v", errCoordinatorState, err)
	}
	if err := validatePersistedSubmissionAuthority(record); err != nil {
		return fmt.Errorf("%w: persisted submission authority is invalid: %v", errCoordinatorState, err)
	}
	if record.Profile != record.Plan.Profile || record.JobSourceTreeSHA != record.Plan.Source.SourceTreeSHA {
		return fmt.Errorf("%w: persisted job fields drifted from plan", errCoordinatorState)
	}
	return nil
}

// validatePersistedSubmissionAuthority 重验 durable envelope，不伪造仅在 INSERT 前存在的 capability。
func validatePersistedSubmissionAuthority(record coordinatorJobRecord) error {
	entrypoint, err := submissionEntrypoint(record.Authority.Entrypoint)
	if err != nil || entrypoint.Owner != record.Authority.Owner {
		return errors.New("entrypoint and authority owner are invalid")
	}
	if record.Plan.Profile == gatecontract.ProfileRelease {
		return validatePersistedReleaseSubmissionAuthority(record, entrypoint)
	}
	if entrypoint.ID == gatecontract.CIEntrypointWorkflowRequired {
		return validatePersistedWorkflowSubmissionAuthority(record, entrypoint)
	}
	if !entrypoint.Authoritative && record.Authority.Attestation != "" {
		return errors.New("non-authoritative persisted submission carries an attestation")
	}
	return nil
}

func validatePersistedWorkflowSubmissionAuthority(
	record coordinatorJobRecord,
	entrypoint gatecontract.CIEntrypoint,
) error {
	if !entrypoint.Authoritative {
		return errors.New("workflow persisted submission requires the authoritative workflow entrypoint")
	}
	if err := validateWorkflowPlan(record.Plan); err != nil {
		return err
	}
	if !validWorkflowAttestationDigest(record.Authority.Attestation) || record.InvocationID != "workflow-"+strings.TrimPrefix(record.Authority.Attestation, "sha256:") {
		return errors.New("workflow persisted submission is not bound to its OIDC attestation")
	}
	return nil
}

// validatePersistedReleaseSubmissionAuthority 证明已验签 envelope 仍绑定 durable job。
func validatePersistedReleaseSubmissionAuthority(
	record coordinatorJobRecord,
	entrypoint gatecontract.CIEntrypoint,
) error {
	if entrypoint.ID != gatecontract.CIEntrypointRelease || !entrypoint.Authoritative {
		return errors.New("release persisted submission requires the authoritative release entrypoint")
	}
	attestation, err := gatecontract.DecodeReleaseAuthorityAttestation(record.Authority.Attestation)
	if err != nil {
		return err
	}
	if attestation.InvocationID != record.InvocationID || !reflect.DeepEqual(attestation.Source, record.Plan.Source) || attestation.PlanDigest != record.Plan.PlanDigest {
		return errors.New("release persisted attestation does not bind invocation, source, and plan")
	}
	return nil
}

// validateCoordinatorRecordState 校验状态枚举及 passed/receipt 不变量。
func validateCoordinatorRecordState(record coordinatorJobRecord) error {
	if record.State != jobStateQueued && record.State != jobStateStarted && !record.State.terminal() {
		return fmt.Errorf("%w: unknown persisted job state %q", errCoordinatorState, record.State)
	}
	return validateCoordinatorSchedulerMetadata(record)
}

// validateStoredResultReceipt 校验持久化 receipt 与当前 durable job 完全一致。
func validateStoredResultReceipt(record coordinatorJobRecord) error {
	receipt := record.Receipt
	if receipt == nil || receipt.ReceiptID != record.ReceiptID || receipt.ReceiptID != resultReceiptID(record.JobID) {
		return fmt.Errorf("%w: persisted receipt identity does not match job", errCoordinatorState)
	}
	if receipt.SchemaVersion != gatecontract.ResultReceiptSchemaVersion || len(receipt.ShardReceipts) != gatecontract.MaxContainerShards {
		return fmt.Errorf("%w: persisted receipt must use the canonical v%d three-shard schema", errCoordinatorState, gatecontract.ResultReceiptSchemaVersion)
	}
	if err := receipt.Validate(); err != nil {
		return fmt.Errorf("%w: persisted receipt is invalid: %v", errCoordinatorState, err)
	}
	if storedResultReceiptDrifted(record, receipt) {
		return fmt.Errorf("%w: persisted receipt drifted from coordinator job", errCoordinatorState)
	}
	return validateStoredReceiptCompletion(record, receipt)
}

// storedResultReceiptDrifted 比较 receipt 中所有 job 侧权威绑定。
func storedResultReceiptDrifted(record coordinatorJobRecord, receipt *gatecontract.ResultReceipt) bool {
	return storedResultReceiptCoreDrifted(record, receipt) || storedResultReceiptEvidenceDrifted(record, receipt)
}

// storedResultReceiptCoreDrifted 比较签名回执与 job 的身份、来源和计划绑定。
func storedResultReceiptCoreDrifted(record coordinatorJobRecord, receipt *gatecontract.ResultReceipt) bool {
	return receipt.InvocationID != record.InvocationID || storedReceiptAuthorityDrifted(record, receipt) ||
		!reflect.DeepEqual(receipt.Source, record.Plan.Source) ||
		receipt.PlanDigest != record.Plan.PlanDigest || receipt.PolicyDigest != record.Plan.PolicyDigest
}

// storedResultReceiptEvidenceDrifted 比较 gate、shard 和资源见证执行证据。
func storedResultReceiptEvidenceDrifted(record coordinatorJobRecord, receipt *gatecontract.ResultReceipt) bool {
	return !reflect.DeepEqual(receipt.GateResults, record.GateResults) || storedShardReceiptExitedAtDrifted(record, receipt) ||
		storedReceiptResourceWitnessDrifted(record, receipt) || receipt.Status != gatecontract.ResultStatusPassed
}

// storedReceiptResourceWitnessDrifted 比较可选的持久化容器资源见证。
func storedReceiptResourceWitnessDrifted(record coordinatorJobRecord, receipt *gatecontract.ResultReceipt) bool {
	if record.ContainerResourceWitness == nil {
		return false
	}
	return !reflect.DeepEqual(receipt.Container.ResourceWitness, *record.ContainerResourceWitness) ||
		receipt.Container.ResourceWitnessDigest != record.ContainerResourceWitnessDigest
}

// storedShardReceiptExitedAtDrifted 比较已签名分片退出时间与 SQLite 持久化记录。
func storedShardReceiptExitedAtDrifted(record coordinatorJobRecord, receipt *gatecontract.ResultReceipt) bool {
	if len(record.ContainerShards) != gatecontract.MaxContainerShards || len(receipt.ShardReceipts) != gatecontract.MaxContainerShards {
		return true
	}
	for index, durable := range record.ContainerShards {
		signed := receipt.ShardReceipts[index]
		if !reflect.DeepEqual(signed.Shard, durable.Shard) || durable.ExitedAt == nil || signed.ExitedAt.IsZero() ||
			!signed.ExitedAt.Equal(*durable.ExitedAt) {
			return true
		}
	}
	return false
}

func storedReceiptAuthorityDrifted(record coordinatorJobRecord, receipt *gatecontract.ResultReceipt) bool {
	return receipt.Entrypoint != record.Authority.Entrypoint || receipt.AuthorityOwner != record.Authority.Owner || receipt.AuthorityAttestation != record.Authority.Attestation
}

// validateStoredReceiptCompletion 校验 terminal 完成时间与 receipt 时间一致。
func validateStoredReceiptCompletion(record coordinatorJobRecord, receipt *gatecontract.ResultReceipt) error {
	if record.CompletedAt == nil || !record.CompletedAt.Equal(receipt.CompletedAt) {
		return fmt.Errorf("%w: persisted receipt completion time drifted from job", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorSchedulerMetadata 仅接受无依赖 job 或单一 build 前驱。
func validateCoordinatorSchedulerMetadata(record coordinatorJobRecord) error {
	if record.SchedulerSubsequence == 0 && len(record.SchedulerDependencies) == 0 {
		return nil
	}
	if record.SchedulerSubsequence != 1 || len(record.SchedulerDependencies) != 1 || record.SchedulerDependencies[0] == "" {
		return fmt.Errorf("%w: persisted scheduler build dependency is invalid", errCoordinatorState)
	}
	return nil
}

func receiptsEqual(left, right *gatecontract.ResultReceipt) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.DeepEqual(*left, *right)
}

// validateCoordinatorClock 校验首次启动时钟成对存在且未被重算。
func validateCoordinatorClock(record coordinatorJobRecord) error {
	if (record.StartedAt == nil) != (record.Deadline == nil) {
		return fmt.Errorf("%w: persisted started_at and deadline_at are incomplete", errCoordinatorState)
	}
	if record.StartedAt != nil && !record.Deadline.Equal(record.StartedAt.Add(coordinatorTimeout(record.Profile))) {
		return fmt.Errorf("%w: persisted deadline drifted from first start", errCoordinatorState)
	}
	if record.State == jobStateQueued && (record.ContainerPhase != "" || record.StartedAt != nil) {
		return fmt.Errorf("%w: queued job contains execution lifecycle state", errCoordinatorState)
	}
	if record.State == jobStateStarted && record.StartedAt == nil && coordinatorPhaseRequiresClock(record.ContainerPhase) {
		return fmt.Errorf("%w: executing job is missing its execution deadline", errCoordinatorState)
	}
	return nil
}

func coordinatorPhaseRequiresClock(phase localci.FreshContainerLifecyclePhase) bool {
	switch phase {
	case localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted,
		localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemovalPending:
		return true
	default:
		return false
	}
}

// validateCoordinatorContainer 校验生命周期身份、阶段与销毁证明。
func validateCoordinatorContainer(record coordinatorJobRecord) error {
	if record.ContainerPhase != "" {
		if !completeCoordinatorContainerIdentity(record) {
			return fmt.Errorf("%w: persisted container identity is incomplete", errCoordinatorState)
		}
		if !knownCoordinatorContainerPhase(record.ContainerPhase) {
			return fmt.Errorf("%w: unknown persisted container phase %q", errCoordinatorState, record.ContainerPhase)
		}
	}
	if record.ContainerPhase == localci.FreshContainerPhaseRemoved && record.RemovalProofDigest == "" {
		return fmt.Errorf("%w: removed container lacks removal proof", errCoordinatorState)
	}
	if err := validateStoredCoordinatorContainerExit(record); err != nil {
		return err
	}
	return validateCoordinatorResourceWitness(record)
}

// validateStoredCoordinatorContainerExit 校验单容器持久退出时刻与生命周期阶段一致。
func validateStoredCoordinatorContainerExit(record coordinatorJobRecord) error {
	if record.ContainerExitedAt == nil {
		if record.ContainerPhase == localci.FreshContainerPhaseExited {
			return fmt.Errorf("%w: exited container phase lacks exited_at", errCoordinatorState)
		}
		return nil
	}
	if record.StartedAt == nil || record.ContainerExitedAt.Before(*record.StartedAt) {
		return fmt.Errorf("%w: persisted container exited_at is invalid", errCoordinatorState)
	}
	if record.ContainerPhase != localci.FreshContainerPhaseExited && record.ContainerPhase != localci.FreshContainerPhaseRemovalPending && record.ContainerPhase != localci.FreshContainerPhaseRemoved {
		return fmt.Errorf("%w: nonterminal container persists exited_at", errCoordinatorState)
	}
	return nil
}

// normalTerminalStateRequiresContainerExit 标记必须保留容器退出时刻的作业终态。
func normalTerminalStateRequiresContainerExit(state jobState) bool {
	return state == jobStatePassed || state == jobStateFailed || state == jobStateCancelled || state == jobStateTimeout
}

func completeCoordinatorContainerIdentity(record coordinatorJobRecord) bool {
	return record.ActiveGateID != "" && len(record.ContainerLabels) > 0 && record.ContainerImageReference != "" && record.ContainerConfigDigest != "" && record.SourceSnapshotDir != ""
}

func knownCoordinatorContainerPhase(phase localci.FreshContainerLifecyclePhase) bool {
	_, exists := map[localci.FreshContainerLifecyclePhase]struct{}{localci.FreshContainerPhasePrepared: {}, localci.FreshContainerPhaseCreating: {}, localci.FreshContainerPhaseCreated: {}, localci.FreshContainerPhaseStarting: {}, localci.FreshContainerPhaseStarted: {}, localci.FreshContainerPhaseExited: {}, localci.FreshContainerPhaseRemovalPending: {}, localci.FreshContainerPhaseRemoved: {}}[phase]
	return exists
}

func parseCoordinatorTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse coordinator timestamp: %w", err)
	}
	return &parsed, nil
}

func requireOneCoordinatorRow(result sql.Result, action string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: %s changed %d rows", errCoordinatorState, action, rows)
	}
	return nil
}

func (record coordinatorJobRecord) status() jobStatus {
	return jobStatus{InvocationID: record.InvocationID, JobID: record.JobID, EnqueueSequence: record.EnqueueSequence, State: record.State, Profile: record.Profile, JobSourceTreeSHA: record.JobSourceTreeSHA, ImageProvenanceSourceTreeSHA: record.ImageProvenanceSourceTreeSHA, SubmittedAt: record.SubmittedAt, StartedAt: record.StartedAt, CompletedAt: record.CompletedAt, GateResults: append([]gatecontract.GateResult(nil), record.GateResults...), Error: record.Error, ContainerHostConfigDigest: record.ContainerHostConfigDigest, ContainerResourceWitness: record.ContainerResourceWitness, ContainerResourceWitnessDigest: record.ContainerResourceWitnessDigest, ContainerResourceWitnessVerified: record.ContainerResourceWitnessVerified, ReceiptID: record.ReceiptID, Terminal: record.State.terminal()}
}

func (store *coordinatorStore) close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

type coordinatorShardLifecycleUpdate struct {
	containerID, removalProof       string
	startedAt, deadlineAt           any
	exitedAt, completedAt, exitCode any
}

// coordinatorShardLifecycleValues 保留未被当前事件推进的持久化终态字段。
func coordinatorShardLifecycleValues(record coordinatorShardRecord, event localci.FreshContainerLifecycleEvent) coordinatorShardLifecycleUpdate {
	values := coordinatorShardLifecycleUpdate{
		containerID: event.ContainerID, removalProof: record.RemovalProofDigest,
		startedAt: nullableCoordinatorTime(record.StartedAt), deadlineAt: nullableCoordinatorTime(record.Deadline),
		exitedAt: nullableCoordinatorTime(record.ExitedAt), completedAt: nullableCoordinatorTime(record.CompletedAt),
	}
	if record.ExitCode != nil {
		values.exitCode = *record.ExitCode
	}
	if event.Phase == localci.FreshContainerPhasePrepared {
		values.containerID, values.removalProof = "", ""
	}
	if event.Phase == localci.FreshContainerPhaseStarting && record.StartedAt == nil {
		values.startedAt, values.deadlineAt = event.StartedAt.Format(timeFormat), event.Deadline.Format(timeFormat)
	}
	if event.Phase == localci.FreshContainerPhaseExited ||
		(event.Phase == localci.FreshContainerPhaseRemovalPending && !event.ExitedAt.IsZero()) {
		values.exitedAt, values.completedAt, values.exitCode = event.ExitedAt.Format(timeFormat), event.CompletedAt.Format(timeFormat), event.ExitCode
	}
	if event.Phase == localci.FreshContainerPhaseRemoved {
		values.removalProof = event.RemovalProofDigest
	}
	return values
}

// validateContainerShardLifecycleTransition 组合身份、顺序、资源和时钟守卫。
func validateContainerShardLifecycleTransition(record coordinatorShardRecord, labels map[string]string, event localci.FreshContainerLifecycleEvent) error {
	if err := validateCoordinatorShardLabels(record.JobID, record.Shard, labels); err != nil {
		return err
	}
	if record.ContainerPhase == localci.FreshContainerPhaseRemoved && event.Phase == localci.FreshContainerPhaseRemoved {
		return validateRemovedShardLifecycleReplay(record, labels, event)
	}
	if err := validateShardLifecycleImmutable(record, labels, event); err != nil {
		return err
	}
	if err := validateShardLifecycleContainerEvidence(record, event); err != nil {
		return err
	}
	return validateShardLifecycleTimes(record, event)
}

// validateRemovedShardLifecycleReplay 只接受与已持久化删除终态完全一致的重放。
func validateRemovedShardLifecycleReplay(record coordinatorShardRecord, labels map[string]string, event localci.FreshContainerLifecycleEvent) error {
	type replayEvidence struct {
		labels                                         map[string]string
		image, config, source, containerID, hostConfig string
		witnessDigest, removalProof                    string
		witness                                        *gatecontract.ContainerResourceWitness
		startedAt, deadline, exitedAt, completedAt     *time.Time
		exitCode                                       *int
	}
	stored := replayEvidence{
		record.ContainerLabels, record.ContainerImageReference, record.ContainerConfigDigest,
		record.SourceSnapshotDir, record.ContainerID, record.ContainerHostConfigDigest,
		record.ContainerResourceWitnessDigest, record.RemovalProofDigest, record.ContainerResourceWitness,
		record.StartedAt, record.Deadline, record.ExitedAt, record.CompletedAt, record.ExitCode,
	}
	incoming := replayEvidence{
		labels, event.ImageReference, event.ConfigDigest, event.SourceSnapshotDir, event.ContainerID, event.HostConfigDigest,
		event.ResourceWitnessDigest, event.RemovalProofDigest, lifecycleWitnessPointer(event),
		lifecycleTimePointer(event.StartedAt), lifecycleTimePointer(event.Deadline), lifecycleTimePointer(event.ExitedAt),
		lifecycleTimePointer(event.CompletedAt), lifecycleExitCodePointer(event),
	}
	if !reflect.DeepEqual(stored, incoming) {
		return fmt.Errorf("%w: removed shard lifecycle replay drifted", errCoordinatorState)
	}
	return nil
}

func lifecycleWitnessPointer(event localci.FreshContainerLifecycleEvent) *gatecontract.ContainerResourceWitness {
	if event.ResourceWitnessDigest == "" {
		return nil
	}
	return &event.ResourceWitness
}

func lifecycleTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func lifecycleExitCodePointer(event localci.FreshContainerLifecycleEvent) *int {
	value := event.ExitCode
	return &value
}

// validateShardLifecycleImmutable 拒绝镜像、来源、标签和阶段顺序漂移。
func validateShardLifecycleImmutable(record coordinatorShardRecord, labels map[string]string, event localci.FreshContainerLifecycleEvent) error {
	if event.ImageReference == "" {
		return fmt.Errorf("%w: shard lifecycle image reference is missing", errCoordinatorState)
	}
	if event.ConfigDigest != record.Shard.AcceptedConfigDigest {
		return fmt.Errorf("%w: shard lifecycle image config drifted", errCoordinatorState)
	}
	if !strings.HasSuffix(event.ImageReference, "@"+record.Shard.AcceptedManifestDigest) {
		return fmt.Errorf("%w: shard lifecycle image manifest drifted", errCoordinatorState)
	}
	if event.SourceSnapshotDir == "" {
		return fmt.Errorf("%w: shard lifecycle source snapshot is missing", errCoordinatorState)
	}
	if record.ContainerPhase != "" && !equalShardLifecycleImmutable(record, labels, event) {
		return fmt.Errorf("%w: shard lifecycle immutable evidence drifted", errCoordinatorState)
	}
	if record.ContainerPhase == localci.FreshContainerPhaseRemoved {
		return fmt.Errorf("%w: removed coordinator shard cannot be reused", errCoordinatorState)
	}
	return validateLifecyclePhaseOrder(record.ContainerPhase, event.Phase)
}

func equalShardLifecycleImmutable(record coordinatorShardRecord, labels map[string]string, event localci.FreshContainerLifecycleEvent) bool {
	stored := struct {
		labels                map[string]string
		image, config, source string
	}{record.ContainerLabels, record.ContainerImageReference, record.ContainerConfigDigest, record.SourceSnapshotDir}
	incoming := struct {
		labels                map[string]string
		image, config, source string
	}{labels, event.ImageReference, event.ConfigDigest, event.SourceSnapshotDir}
	return reflect.DeepEqual(stored, incoming)
}

// validateShardLifecycleContainerEvidence 将分片证据映射为单容器校验所需字段。
func validateShardLifecycleContainerEvidence(record coordinatorShardRecord, event localci.FreshContainerLifecycleEvent) error {
	temporary := coordinatorJobRecord{
		ContainerPhase: record.ContainerPhase, ContainerID: record.ContainerID,
		ContainerHostConfigDigest:        record.ContainerHostConfigDigest,
		ContainerResourceWitness:         record.ContainerResourceWitness,
		ContainerResourceWitnessDigest:   record.ContainerResourceWitnessDigest,
		ContainerResourceWitnessVerified: record.ContainerResourceWitnessVerified,
	}
	if err := validateLifecycleContainerID(temporary, event); err != nil {
		return err
	}
	return validateLifecycleResourceWitness(temporary, event)
}

// validateShardLifecycleTimes 组合启动、重放、完成和删除时序守卫。
func validateShardLifecycleTimes(record coordinatorShardRecord, event localci.FreshContainerLifecycleEvent) error {
	if err := validateShardLifecycleStart(event); err != nil {
		return err
	}
	if err := validateShardLifecycleDeadlineReplay(record, event); err != nil {
		return err
	}
	if err := validateShardLifecycleCompletion(record, event); err != nil {
		return err
	}
	return validateShardLifecycleRemoval(event)
}

// validateShardLifecycleStart 校验首次启动事件携带完整且单调的运行时钟。
func validateShardLifecycleStart(event localci.FreshContainerLifecycleEvent) error {
	if event.Phase == localci.FreshContainerPhaseStarting && slices.Contains([]bool{
		event.StartedAt.IsZero(), event.Deadline.IsZero(), !event.Deadline.After(event.StartedAt),
	}, true) {
		return fmt.Errorf("%w: shard lifecycle start timestamps are incomplete", errCoordinatorState)
	}
	return nil
}

// validateShardLifecycleDeadlineReplay 拒绝对已持久化时限的重新计算。
func validateShardLifecycleDeadlineReplay(record coordinatorShardRecord, event localci.FreshContainerLifecycleEvent) error {
	if record.StartedAt != nil && event.Phase == localci.FreshContainerPhaseStarting &&
		(record.Deadline == nil || !record.Deadline.Equal(event.Deadline)) {
		return fmt.Errorf("%w: shard lifecycle attempted to reset deadline", errCoordinatorState)
	}
	return nil
}

// validateShardLifecycleCompletion 校验退出和删除事件携带的可信退出时刻。
func validateShardLifecycleCompletion(record coordinatorShardRecord, event localci.FreshContainerLifecycleEvent) error {
	if event.Phase == localci.FreshContainerPhaseExited {
		return validateShardExitedLifecycleCompletion(record, event)
	}
	if event.Phase == localci.FreshContainerPhaseRemovalPending {
		if event.ExitedAt.IsZero() {
			return nil
		}
		return validateShardExitedLifecycleCompletion(record, event)
	}
	if event.Phase == localci.FreshContainerPhaseRemoved {
		return validateShardRemovedLifecycleCompletion(record, event)
	}
	return nil
}

// validateShardExitedLifecycleCompletion 校验退出事件的时间顺序与重放一致性。
func validateShardExitedLifecycleCompletion(record coordinatorShardRecord, event localci.FreshContainerLifecycleEvent) error {
	if event.ExitedAt.IsZero() || event.CompletedAt.IsZero() || record.StartedAt == nil {
		return fmt.Errorf("%w: shard lifecycle completion is incomplete", errCoordinatorState)
	}
	if event.ExitedAt.Before(*record.StartedAt) || event.CompletedAt.Before(event.ExitedAt) {
		return fmt.Errorf("%w: shard lifecycle completion is invalid", errCoordinatorState)
	}
	if record.ExitedAt != nil && !event.ExitedAt.Equal(*record.ExitedAt) {
		return fmt.Errorf("%w: shard lifecycle exit timestamp drifted", errCoordinatorState)
	}
	return nil
}

// validateShardRemovedLifecycleCompletion 校验删除事件不会伪造或改写退出时刻。
func validateShardRemovedLifecycleCompletion(record coordinatorShardRecord, event localci.FreshContainerLifecycleEvent) error {
	if record.ContainerPhase != localci.FreshContainerPhaseExited &&
		record.ContainerPhase != localci.FreshContainerPhaseRemovalPending {
		if !event.ExitedAt.IsZero() {
			return fmt.Errorf("%w: unproved shard removal carries a forged exit timestamp", errCoordinatorState)
		}
		return nil
	}
	if record.ExitedAt == nil {
		if event.ExitedAt.IsZero() {
			return nil
		}
		return fmt.Errorf("%w: shard lifecycle exit timestamp drifted", errCoordinatorState)
	}
	if event.ExitedAt.IsZero() || !event.ExitedAt.Equal(*record.ExitedAt) {
		return fmt.Errorf("%w: shard lifecycle exit timestamp drifted", errCoordinatorState)
	}
	return nil
}

// validateShardLifecycleRemoval 校验删除事件具有不可伪造的删除证明。
func validateShardLifecycleRemoval(event localci.FreshContainerLifecycleEvent) error {
	if event.Phase == localci.FreshContainerPhaseRemoved && event.RemovalProofDigest == "" {
		return fmt.Errorf("%w: shard lifecycle removal proof is required", errCoordinatorState)
	}
	return nil
}

// waitCoordinatorStatusRetry 在重读 durable 状态前等待一个有界轮询间隔。
func waitCoordinatorStatusRetry(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return nil
	}
}

// readCoordinatorStatus 从同一 scheduler 快照计算状态与真实 FIFO 位置。
func (client *coordinatorTransportClient) readCoordinatorStatus(
	ctx context.Context,
	record coordinatorJobRecord,
) (jobStatus, error) {
	scheduler, err := client.schedulerForOperation(ctx)
	if err != nil {
		return jobStatus{}, err
	}
	snapshot, err := scheduler.Snapshot(ctx)
	if err != nil {
		return jobStatus{}, fmt.Errorf("read scheduler snapshot for %q: %w", record.JobID, err)
	}
	workloadID := record.JobID
	if len(record.ContainerShards) != 0 {
		workloadID += "/shards"
	}
	schedulerState, queuePosition, err := schedulerJobObservation(snapshot, workloadID)
	if err != nil {
		return jobStatus{}, coordinatorSchedulerObservationError(record, workloadID, err)
	}
	return coordinatorStatusForSchedulerObservation(record, workloadID, schedulerState, queuePosition)
}

// schedulerJobObservation 在权威排序快照中定位 workload 及其 1-based queued 位置。
func schedulerJobObservation(snapshot localci.SchedulerSnapshot, jobID string) (localci.WorkloadStatus, int, error) {
	queuePosition := 0
	for _, workload := range snapshot.Workloads {
		if workload.Status == localci.WorkloadStatusQueued {
			queuePosition++
		}
		if workload.Request.ID != jobID {
			continue
		}
		if workload.Status == localci.WorkloadStatusQueued {
			return workload.Status, queuePosition, nil
		}
		return workload.Status, 0, nil
	}
	return "", 0, fmt.Errorf("%w: scheduler workload %q is missing", errCoordinatorState, jobID)
}

// schedulerWorkloadTerminal 判断 scheduler 已持久化的三种终态。
func schedulerWorkloadTerminal(state localci.WorkloadStatus) bool {
	return state == localci.WorkloadStatusPassed ||
		state == localci.WorkloadStatusFailed ||
		state == localci.WorkloadStatusInfraFailed
}

// coordinatorTimeout 根据 profile 返回不可变的容器执行时限。
func coordinatorTimeout(profile gatecontract.Profile) time.Duration {
	if profile == gatecontract.ProfileRelease {
		return coordinatorReleaseTimeout
	}
	return coordinatorNormalTimeout
}
