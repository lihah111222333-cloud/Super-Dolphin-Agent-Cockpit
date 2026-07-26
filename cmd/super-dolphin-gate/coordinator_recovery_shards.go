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
	"strconv"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const coordinatorShardStoreSchema = `
CREATE TABLE IF NOT EXISTS coordinator_job_shards (
 job_id TEXT NOT NULL,
 shard_index INTEGER NOT NULL,
 shard_identity_digest TEXT NOT NULL,
 shard_json BLOB NOT NULL,
 container_phase TEXT NOT NULL DEFAULT '',
 container_id TEXT NOT NULL DEFAULT '',
 container_labels_json BLOB,
 container_image_reference TEXT NOT NULL DEFAULT '',
 container_config_digest TEXT NOT NULL DEFAULT '',
 container_host_config_digest TEXT NOT NULL DEFAULT '',
 container_resource_witness_json BLOB,
 container_resource_witness_digest TEXT NOT NULL DEFAULT '',
 container_resource_witness_verified INTEGER NOT NULL DEFAULT 0,
 source_snapshot_dir TEXT NOT NULL DEFAULT '',
 removal_proof_digest TEXT NOT NULL DEFAULT '',
 started_at TEXT,
 deadline_at TEXT,
 exited_at TEXT,
 completed_at TEXT,
 exit_code INTEGER,
 PRIMARY KEY (job_id, shard_index),
 UNIQUE (job_id, shard_identity_digest),
 FOREIGN KEY (job_id) REFERENCES coordinator_jobs(job_id)
);`

const coordinatorShardSelectColumns = `job_id, shard_index, shard_identity_digest, shard_json,
container_phase, container_id, container_labels_json, container_image_reference, container_config_digest,
container_host_config_digest, container_resource_witness_json, container_resource_witness_digest,
container_resource_witness_verified, source_snapshot_dir, removal_proof_digest,
started_at, deadline_at, exited_at, completed_at, exit_code`

type coordinatorShardRecord struct {
	JobID                            string                                 `db:"job_id"`
	Shard                            gatecontract.ContainerShard            `db:"shard_json"`
	ContainerPhase                   localci.FreshContainerLifecyclePhase   `db:"container_phase"`
	ContainerID                      string                                 `db:"container_id"`
	ContainerLabels                  map[string]string                      `db:"container_labels_json"`
	ContainerImageReference          string                                 `db:"container_image_reference"`
	ContainerConfigDigest            string                                 `db:"container_config_digest"`
	ContainerHostConfigDigest        string                                 `db:"container_host_config_digest"`
	ContainerResourceWitness         *gatecontract.ContainerResourceWitness `db:"container_resource_witness_json"`
	ContainerResourceWitnessDigest   string                                 `db:"container_resource_witness_digest"`
	ContainerResourceWitnessVerified bool                                   `db:"container_resource_witness_verified"`
	SourceSnapshotDir                string                                 `db:"source_snapshot_dir"`
	RemovalProofDigest               string                                 `db:"removal_proof_digest"`
	StartedAt                        *time.Time                             `db:"started_at"`
	Deadline                         *time.Time                             `db:"deadline_at"`
	ExitedAt                         *time.Time                             `db:"exited_at"`
	CompletedAt                      *time.Time                             `db:"completed_at"`
	ExitCode                         *int                                   `db:"exit_code"`
}

// ensureCoordinatorShardSchema 创建分片子表并迁移可空的 exited_at 证据列。
func ensureCoordinatorShardSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, coordinatorShardStoreSchema); err != nil {
		return fmt.Errorf("initialize coordinator shard schema: %w", err)
	}
	columns, err := coordinatorShardColumns(ctx, db)
	if err != nil {
		return err
	}
	for required := range strings.SplitSeq(strings.ReplaceAll(coordinatorShardSelectColumns, "\n", " "), ",") {
		name := strings.TrimSpace(required)
		if name != "exited_at" && !columns[name] {
			return fmt.Errorf("legacy coordinator shard schema requires rebuild; missing column %q", name)
		}
	}
	if !columns["exited_at"] {
		if _, err := db.ExecContext(ctx, "ALTER TABLE coordinator_job_shards ADD COLUMN exited_at TEXT"); err != nil {
			return fmt.Errorf("add coordinator shard exited_at: %w", err)
		}
		columns["exited_at"] = true
	}
	return nil
}

// coordinatorShardColumns 读取已存在分片表的列集，供迁移与快速失败的表结构校验共同使用。
func coordinatorShardColumns(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(coordinator_job_shards)")
	if err != nil {
		return nil, fmt.Errorf("inspect coordinator shard schema: %w", err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, errors.Join(fmt.Errorf("scan coordinator shard schema: %w", err), rows.Close())
		}
		columns[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read coordinator shard schema: %w", err)
	}
	return columns, nil
}

// createContainerShardSet 原子持久化 canonical 集合；空子表明确表示 legacy 单容器协议。
func (store *coordinatorStore) createContainerShardSet(
	ctx context.Context,
	jobID string,
	set gatecontract.ContainerShardSet,
) (retErr error) {
	if err := set.Validate(); err != nil {
		return fmt.Errorf("validate coordinator shard set: %w", err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin coordinator shard set: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = errors.Join(retErr, rollbackErr)
		}
	}()
	if err := persistContainerShardSetTx(ctx, tx, jobID, set); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit coordinator shard set: %w", err)
	}
	return nil
}

// persistContainerShardSetTx 只接受 exact replay，否则一次性写入全部 shard。
func persistContainerShardSetTx(ctx context.Context, tx *sql.Tx, jobID string, set gatecontract.ContainerShardSet) error {
	if err := validateShardSetJobBinding(ctx, tx, jobID, set); err != nil {
		return err
	}
	existing, err := containerShardsFromQuery(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if len(existing) != 0 {
		if equalStoredShardSet(existing, set) {
			return nil
		}
		return fmt.Errorf("%w: coordinator shard set replay drifted", errCoordinatorState)
	}
	for _, shard := range set.Shards {
		if err := insertCoordinatorShard(ctx, tx, jobID, shard); err != nil {
			return err
		}
	}
	return nil
}

func insertCoordinatorShard(ctx context.Context, tx *sql.Tx, jobID string, shard gatecontract.ContainerShard) error {
	encoded, err := json.Marshal(shard)
	if err != nil {
		return fmt.Errorf("encode coordinator shard: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coordinator_job_shards
(job_id, shard_index, shard_identity_digest, shard_json) VALUES (?, ?, ?, ?)`,
		jobID, shard.Index, shard.IdentityDigest, encoded); err != nil {
		return fmt.Errorf("persist coordinator shard %d: %w", shard.Index, err)
	}
	return nil
}

// validateShardSetJobBinding 拒绝父 job 身份漂移以及 legacy/shard 混合记录。
func validateShardSetJobBinding(ctx context.Context, tx *sql.Tx, jobID string, set gatecontract.ContainerShardSet) error {
	var planJSON []byte
	var profile, sourceTree, imageSource, phase, containerID string
	var labelsJSON []byte
	err := tx.QueryRowContext(ctx, `SELECT plan_json, profile, job_source_tree_sha,
image_provenance_source_tree_sha, container_phase, container_id, container_labels_json
FROM coordinator_jobs WHERE job_id = ?`, jobID).Scan(
		&planJSON, &profile, &sourceTree, &imageSource, &phase, &containerID, &labelsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %q", errCoordinatorNotFound, jobID)
	}
	if err != nil {
		return fmt.Errorf("read coordinator shard parent: %w", err)
	}
	var plan gatecontract.GatePlan
	if err := gatecontract.DecodeStrictJSON(planJSON, &plan); err != nil {
		return fmt.Errorf("decode coordinator shard parent plan: %w", err)
	}
	got := struct {
		profile    gatecontract.Profile
		planDigest string
		sourceTree string
	}{gatecontract.Profile(profile), plan.PlanDigest, sourceTree}
	want := struct {
		profile    gatecontract.Profile
		planDigest string
		sourceTree string
	}{set.Profile, set.PlanDigest, set.SourceTreeSHA}
	if got != want {
		return fmt.Errorf("%w: coordinator shard set parent identity drifted", errCoordinatorState)
	}
	if imageSource == "" {
		return fmt.Errorf("%w: coordinator shard parent lacks image provenance source", errCoordinatorState)
	}
	if strings.Join([]string{phase, containerID, string(labelsJSON)}, "") != "" {
		return fmt.Errorf("%w: legacy single-container evidence cannot mix with shard protocol", errCoordinatorState)
	}
	return nil
}

func equalStoredShardSet(records []coordinatorShardRecord, set gatecontract.ContainerShardSet) bool {
	if len(records) != len(set.Shards) {
		return false
	}
	for index := range records {
		if !reflect.DeepEqual(records[index].Shard, set.Shards[index]) || records[index].ContainerPhase != "" {
			return false
		}
	}
	return true
}

func (store *coordinatorStore) containerShards(ctx context.Context, jobID string) ([]coordinatorShardRecord, error) {
	return containerShardsFromQuery(ctx, store.db, jobID)
}

type coordinatorShardQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func containerShardsFromQuery(ctx context.Context, queryer coordinatorShardQueryer, jobID string) (records []coordinatorShardRecord, retErr error) {
	rows, err := queryer.QueryContext(ctx, "SELECT "+coordinatorShardSelectColumns+" FROM coordinator_job_shards WHERE job_id = ? ORDER BY shard_index", jobID)
	if err != nil {
		return nil, fmt.Errorf("list coordinator shards: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	for rows.Next() {
		record, err := scanCoordinatorShard(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate coordinator shards: %w", err)
	}
	return records, nil
}

// scanCoordinatorShard 扫描固定列序并将结构化字段交给独立严格解码步骤。
func scanCoordinatorShard(row coordinatorRowScanner) (coordinatorShardRecord, error) {
	var record coordinatorShardRecord
	var index uint8
	var identity, phase string
	var shardJSON, labelsJSON, witnessJSON []byte
	var witnessVerified bool
	var started, deadline, exited, completed sql.NullString
	var exitCode sql.NullInt64
	if err := row.Scan(&record.JobID, &index, &identity, &shardJSON, &phase,
		&record.ContainerID, &labelsJSON, &record.ContainerImageReference, &record.ContainerConfigDigest,
		&record.ContainerHostConfigDigest, &witnessJSON, &record.ContainerResourceWitnessDigest,
		&witnessVerified, &record.SourceSnapshotDir, &record.RemovalProofDigest,
		&started, &deadline, &exited, &completed, &exitCode); err != nil {
		return coordinatorShardRecord{}, fmt.Errorf("scan coordinator shard: %w", err)
	}
	if err := decodeCoordinatorShardStructured(&record, index, identity, phase, shardJSON, labelsJSON, witnessJSON, witnessVerified); err != nil {
		return coordinatorShardRecord{}, err
	}
	if err := decodeCoordinatorShardTimes(&record, started, deadline, exited, completed, exitCode); err != nil {
		return coordinatorShardRecord{}, err
	}
	if err := validateCoordinatorShardRecord(record); err != nil {
		return coordinatorShardRecord{}, err
	}
	return record, nil
}

// decodeCoordinatorShardStructured 严格解码 shard、labels 与资源 witness。
func decodeCoordinatorShardStructured(
	record *coordinatorShardRecord,
	index uint8,
	identity string,
	phase string,
	shardJSON []byte,
	labelsJSON []byte,
	witnessJSON []byte,
	witnessVerified bool,
) error {
	if err := decodeStrictCoordinatorShard(shardJSON, &record.Shard); err != nil {
		return fmt.Errorf("decode coordinator shard identity: %w", err)
	}
	if record.Shard.Index != index || record.Shard.IdentityDigest != identity {
		return fmt.Errorf("%w: persisted coordinator shard key drifted", errCoordinatorState)
	}
	record.ContainerPhase = localci.FreshContainerLifecyclePhase(phase)
	record.ContainerResourceWitnessVerified = witnessVerified
	if len(labelsJSON) != 0 {
		if err := json.Unmarshal(labelsJSON, &record.ContainerLabels); err != nil {
			return fmt.Errorf("decode coordinator shard labels: %w", err)
		}
	}
	if len(witnessJSON) != 0 {
		var witness gatecontract.ContainerResourceWitness
		if err := gatecontract.DecodeStrictJSON(witnessJSON, &witness); err != nil {
			return fmt.Errorf("decode coordinator shard witness: %w", err)
		}
		record.ContainerResourceWitness = &witness
	}
	return nil
}

// decodeCoordinatorShardTimes 还原分片生命周期时钟与可空退出码。
func decodeCoordinatorShardTimes(
	record *coordinatorShardRecord,
	started sql.NullString,
	deadline sql.NullString,
	exited sql.NullString,
	completed sql.NullString,
	exitCode sql.NullInt64,
) error {
	var err error
	if record.StartedAt, err = parseNullableCoordinatorTime(started); err != nil {
		return err
	}
	if record.Deadline, err = parseNullableCoordinatorTime(deadline); err != nil {
		return err
	}
	if record.ExitedAt, err = parseNullableCoordinatorTime(exited); err != nil {
		return err
	}
	if record.CompletedAt, err = parseNullableCoordinatorTime(completed); err != nil {
		return err
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		record.ExitCode = &value
	}
	return nil
}

func decodeStrictCoordinatorShard(data []byte, shard *gatecontract.ContainerShard) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(shard); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("coordinator shard JSON has trailing data")
	}
	return nil
}

func parseNullableCoordinatorTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(timeFormat, value.String)
	if err != nil {
		return nil, fmt.Errorf("%w: decode coordinator shard timestamp: %v", errCoordinatorState, err)
	}
	return &parsed, nil
}

// validateCoordinatorShardRecord 逐层验证身份、资源、不可变容器证据与终态证明。
func validateCoordinatorShardRecord(record coordinatorShardRecord) error {
	if record.JobID == "" {
		return fmt.Errorf("%w: coordinator shard job identity is missing", errCoordinatorState)
	}
	if err := validateLifecyclePhaseOrder(record.ContainerPhase, record.ContainerPhase); err != nil && record.ContainerPhase != "" {
		return err
	}
	temporary := coordinatorJobRecord{
		ContainerPhase: record.ContainerPhase, ContainerHostConfigDigest: record.ContainerHostConfigDigest,
		ContainerResourceWitness:         record.ContainerResourceWitness,
		ContainerResourceWitnessDigest:   record.ContainerResourceWitnessDigest,
		ContainerResourceWitnessVerified: record.ContainerResourceWitnessVerified,
	}
	if err := validateCoordinatorResourceWitness(temporary); err != nil {
		return err
	}
	if record.ContainerPhase == "" {
		return validateUnstartedCoordinatorShard(record)
	}
	for _, validate := range []func(coordinatorShardRecord) error{
		validateStoredShardImmutableIdentity,
		validateStoredShardContainerIdentity,
		validateStoredShardExecutionClock,
		validateStoredShardCompletion,
		validateStoredShardRemoval,
	} {
		if err := validate(record); err != nil {
			return err
		}
	}
	return nil
}

func validateUnstartedCoordinatorShard(record coordinatorShardRecord) error {
	hasEvidence := slices.Contains([]bool{
		record.ContainerID != "", record.StartedAt != nil, record.Deadline != nil,
		record.ExitedAt != nil, record.CompletedAt != nil, record.ExitCode != nil,
	}, true)
	if hasEvidence {
		return fmt.Errorf("%w: unstarted coordinator shard has lifecycle evidence", errCoordinatorState)
	}
	return nil
}

func validateStoredShardImmutableIdentity(record coordinatorShardRecord) error {
	if record.ContainerImageReference == "" {
		return fmt.Errorf("%w: coordinator shard image reference is missing", errCoordinatorState)
	}
	if record.ContainerConfigDigest != record.Shard.AcceptedConfigDigest {
		return fmt.Errorf("%w: coordinator shard image config drifted", errCoordinatorState)
	}
	if !shardImageReferenceMatches(record.ContainerImageReference, record.Shard) {
		return fmt.Errorf("%w: coordinator shard image manifest drifted", errCoordinatorState)
	}
	if record.SourceSnapshotDir == "" {
		return fmt.Errorf("%w: coordinator shard source snapshot is missing", errCoordinatorState)
	}
	return validateCoordinatorShardLabels(record.JobID, record.Shard, record.ContainerLabels)
}

func validateStoredShardContainerIdentity(record coordinatorShardRecord) error {
	if slices.Contains([]localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting,
		localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited,
		localci.FreshContainerPhaseRemovalPending,
		localci.FreshContainerPhaseRemoved,
	}, record.ContainerPhase) && record.ContainerID == "" {
		return fmt.Errorf("%w: coordinator shard container ID is missing", errCoordinatorState)
	}
	return nil
}

// validateStoredShardExecutionClock 校验运行阶段必需时钟与 deadline 完整性。
func validateStoredShardExecutionClock(record coordinatorShardRecord) error {
	if slices.Contains([]localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited,
	}, record.ContainerPhase) && (record.StartedAt == nil || record.Deadline == nil) {
		return fmt.Errorf("%w: coordinator shard execution clock is missing", errCoordinatorState)
	}
	if record.StartedAt != nil && record.Deadline == nil && !cleanupShardMayLackExecutionDeadline(record.ContainerPhase) {
		return fmt.Errorf("%w: coordinator shard deadline is missing", errCoordinatorState)
	}
	return nil
}

// cleanupShardMayLackExecutionDeadline 仅允许已进入清理阶段的未执行分片保留开始标记而缺少执行期限。
func cleanupShardMayLackExecutionDeadline(phase localci.FreshContainerLifecyclePhase) bool {
	return phase == localci.FreshContainerPhaseRemovalPending || phase == localci.FreshContainerPhaseRemoved
}

// validateStoredShardCompletion 校验 exited 与可选完成证据的时序和退出码。
func validateStoredShardCompletion(record coordinatorShardRecord) error {
	if err := validateStoredShardExitTime(record); err != nil {
		return err
	}
	return validateStoredShardCompletionEvidence(record)
}

// validateStoredShardExitTime 校验已持久化退出时刻不早于分片启动时刻。
func validateStoredShardExitTime(record coordinatorShardRecord) error {
	if record.ExitedAt != nil && (record.StartedAt == nil || record.ExitedAt.Before(*record.StartedAt)) {
		return fmt.Errorf("%w: coordinator shard exit evidence is invalid", errCoordinatorState)
	}
	return nil
}

// validateStoredShardCompletionEvidence 校验完成时刻、退出码和终态证据的关联。
func validateStoredShardCompletionEvidence(record coordinatorShardRecord) error {
	if err := validateStoredShardCompletedEvidence(record); err != nil {
		return err
	}
	if err := validateStoredExitedShardEvidence(record); err != nil {
		return err
	}
	return validateStoredRemovedShardEvidence(record)
}

// validateStoredShardCompletedEvidence 校验完成时刻依赖退出时刻和退出码。
func validateStoredShardCompletedEvidence(record coordinatorShardRecord) error {
	if record.CompletedAt != nil && (record.ExitedAt == nil || record.CompletedAt.Before(*record.ExitedAt) || record.ExitCode == nil) {
		return fmt.Errorf("%w: coordinator shard completion evidence is invalid", errCoordinatorState)
	}
	return nil
}

// validateStoredExitedShardEvidence 校验 exited 分片具备完整终态证据。
func validateStoredExitedShardEvidence(record coordinatorShardRecord) error {
	if record.ContainerPhase == localci.FreshContainerPhaseExited && (record.ExitedAt == nil || record.CompletedAt == nil || record.ExitCode == nil) {
		return fmt.Errorf("%w: exited coordinator shard completion evidence is missing", errCoordinatorState)
	}
	return nil
}

// validateStoredRemovedShardEvidence 校验已退出后删除的分片未丢失完成证据。
func validateStoredRemovedShardEvidence(record coordinatorShardRecord) error {
	if record.ContainerPhase == localci.FreshContainerPhaseRemoved && record.ExitedAt != nil &&
		(record.CompletedAt == nil || record.ExitCode == nil) {
		return fmt.Errorf("%w: removed coordinator shard lost completion evidence", errCoordinatorState)
	}
	return nil
}

func validateStoredShardRemoval(record coordinatorShardRecord) error {
	if record.ContainerPhase == localci.FreshContainerPhaseRemoved && record.RemovalProofDigest == "" {
		return fmt.Errorf("%w: coordinator shard removal proof is missing", errCoordinatorState)
	}
	if record.RemovalProofDigest != "" {
		if err := validateCoordinatorDigest("coordinator shard removal proof", record.RemovalProofDigest); err != nil {
			return err
		}
	}
	return nil
}

func validateCoordinatorShardLabels(jobID string, shard gatecontract.ContainerShard, labels map[string]string) error {
	expected := map[string]string{
		coordinatorLabelJobID: jobID, coordinatorLabelShardIdentity: shard.IdentityDigest,
		coordinatorLabelShardIndex: strconv.Itoa(int(shard.Index)), coordinatorLabelPlanDigest: shard.PlanDigest,
		coordinatorLabelJobSource: shard.SourceTreeSHA, coordinatorLabelImageConfig: shard.AcceptedConfigDigest,
		coordinatorLabelImageManifest: shard.AcceptedManifestDigest,
	}
	for key, value := range expected {
		if labels[key] != value {
			return fmt.Errorf("%w: coordinator shard labels are incomplete or drifted", errCoordinatorState)
		}
	}
	return nil
}

// validateCoordinatorContainerMode 区分 legacy 空子表与 exact shard 集合并拒绝混合证据。
func validateCoordinatorContainerMode(record coordinatorJobRecord) error {
	if len(record.ContainerShards) == 0 {
		return validateLegacyContainerMode(record)
	}
	if err := rejectMixedCoordinatorContainerMode(record); err != nil {
		return err
	}
	if err := validateRecoveredCoordinatorShardSet(record); err != nil {
		return err
	}
	if err := validateNormalTerminalShardExitTimes(record); err != nil {
		return err
	}
	return validateCoordinatorShardClocks(record)
}

// validateNormalTerminalShardExitTimes 要求正常终态的所有 durable shards 均保留 Docker 退出时钟。
func validateNormalTerminalShardExitTimes(record coordinatorJobRecord) error {
	if !normalTerminalStateRequiresContainerExit(record.State) {
		return nil
	}
	if len(record.ContainerShards) == 0 {
		return fmt.Errorf("%w: normal shard terminal state has no durable shards", errCoordinatorState)
	}
	for _, shard := range record.ContainerShards {
		if shard.ExitedAt == nil {
			return fmt.Errorf("%w: normal shard terminal state lacks exited_at", errCoordinatorState)
		}
	}
	return nil
}

// validateLegacyContainerMode 保留空 shard 子表的单容器恢复语义。
func validateLegacyContainerMode(record coordinatorJobRecord) error {
	if record.State == jobStatePassed && record.RemovalProofDigest == "" {
		return fmt.Errorf("%w: passed job lacks removal proof", errCoordinatorState)
	}
	if normalTerminalStateRequiresContainerExit(record.State) && record.ContainerExitedAt == nil {
		return fmt.Errorf("%w: normal legacy terminal state lacks exited_at", errCoordinatorState)
	}
	return nil
}

// rejectMixedCoordinatorContainerMode 拒绝 shard 协议携带任何 legacy 容器证据。
func rejectMixedCoordinatorContainerMode(record coordinatorJobRecord) error {
	legacy := slices.Contains([]bool{
		record.ContainerPhase != "", record.ContainerID != "", len(record.ContainerLabels) != 0,
		record.ContainerImageReference != "", record.ContainerConfigDigest != "", record.SourceSnapshotDir != "",
	}, true)
	if legacy {
		return fmt.Errorf("%w: legacy single-container evidence cannot mix with shard protocol", errCoordinatorState)
	}
	return nil
}

// validateRecoveredCoordinatorShardSet 校验 canonical shard 集合及 passed removal proof。
func validateRecoveredCoordinatorShardSet(record coordinatorJobRecord) error {
	set := gatecontract.ContainerShardSet{
		Profile: record.Profile, PlanDigest: record.Plan.PlanDigest, SourceTreeSHA: record.JobSourceTreeSHA,
		Shards: make([]gatecontract.ContainerShard, len(record.ContainerShards)),
	}
	set.AcceptedManifestDigest = record.ContainerShards[0].Shard.AcceptedManifestDigest
	set.AcceptedConfigDigest = record.ContainerShards[0].Shard.AcceptedConfigDigest
	set.ShardsPerJob = record.ContainerShards[0].Shard.ShardsPerJob
	for index, shard := range record.ContainerShards {
		set.Shards[index] = shard.Shard
	}
	var err error
	if record.State.terminal() {
		err = set.ValidateStored(record.Plan)
	} else {
		err = set.Validate()
	}
	if err != nil {
		return fmt.Errorf("%w: recovered coordinator shard set is not exact: %v", errCoordinatorState, err)
	}
	if record.State == jobStatePassed {
		for _, shard := range record.ContainerShards {
			if shard.ContainerPhase != localci.FreshContainerPhaseRemoved || shard.RemovalProofDigest == "" {
				return fmt.Errorf("%w: passed shard job lacks durable removal proof", errCoordinatorState)
			}
		}
	}
	return nil
}

// validateCoordinatorShardClocks 证明 job 时钟来自最早 shard 且所有已启动 shard 共用 deadline。
func validateCoordinatorShardClocks(record coordinatorJobRecord) error {
	started, err := startedCoordinatorShards(record.ContainerShards)
	if err != nil {
		return err
	}
	if len(started) == 0 {
		return validateUnstartedInvocationClock(record)
	}
	firstStart, deadline, err := canonicalStartedShardClock(started)
	if err != nil {
		return err
	}
	return validateCoordinatorInvocationClock(record, firstStart, deadline)
}

// startedCoordinatorShards 收集拥有完整执行时钟的分片，并拒绝非清理阶段的半时钟状态。
func startedCoordinatorShards(shards []coordinatorShardRecord) ([]coordinatorShardRecord, error) {
	started := make([]coordinatorShardRecord, 0, len(shards))
	for _, shard := range shards {
		if shard.StartedAt == nil {
			continue
		}
		if shard.Deadline == nil {
			if cleanupShardMayLackExecutionDeadline(shard.ContainerPhase) {
				continue
			}
			return nil, fmt.Errorf("%w: coordinator shard deadline is missing", errCoordinatorState)
		}
		started = append(started, shard)
	}
	return started, nil
}

// 校验未启动分片调用的时钟要么全缺失，要么严格等于启动时刻加配置超时；不完整或漂移立即返回协调状态错误。
func validateUnstartedInvocationClock(record coordinatorJobRecord) error {
	if record.StartedAt == nil && record.Deadline == nil {
		return nil
	}
	if record.StartedAt == nil || record.Deadline == nil || !record.Deadline.Equal(record.StartedAt.Add(coordinatorTimeout(record.Profile))) {
		return fmt.Errorf("%w: unstarted shard invocation clock is incomplete or drifted", errCoordinatorState)
	}
	return nil
}

// canonicalStartedShardClock 计算最早 start 并证明所有已启动 shard deadline 一致。
func canonicalStartedShardClock(started []coordinatorShardRecord) (time.Time, *time.Time, error) {
	firstStart := *started[0].StartedAt
	deadline := started[0].Deadline
	for _, shard := range started[1:] {
		if shard.StartedAt.Before(firstStart) {
			firstStart = *shard.StartedAt
		}
		if shard.Deadline == nil || deadline == nil || !shard.Deadline.Equal(*deadline) {
			return time.Time{}, nil, fmt.Errorf("%w: coordinator shard deadlines differ", errCoordinatorState)
		}
	}
	return firstStart, deadline, nil
}

func validateCoordinatorInvocationClock(record coordinatorJobRecord, firstStart time.Time, deadline *time.Time) error {
	if slices.Contains([]bool{
		record.StartedAt == nil, record.Deadline == nil, deadline == nil,
	}, true) {
		return fmt.Errorf("%w: coordinator shard invocation clock is incomplete", errCoordinatorState)
	}
	if !record.StartedAt.Equal(firstStart) {
		return fmt.Errorf("%w: coordinator shard invocation start drifted", errCoordinatorState)
	}
	if !record.Deadline.Equal(*deadline) {
		return fmt.Errorf("%w: coordinator shard invocation deadline drifted", errCoordinatorState)
	}
	if !record.Deadline.Equal(record.StartedAt.Add(coordinatorTimeout(record.Profile))) {
		return fmt.Errorf("%w: coordinator shard invocation clock drifted", errCoordinatorState)
	}
	return nil
}

// recordContainerShardLifecycle 在一个事务内推进指定 shard 并同步声明 invocation 首次时钟。
func (store *coordinatorStore) recordContainerShardLifecycle(
	ctx context.Context,
	jobID string,
	shardIdentity string,
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) (retErr error) {
	labelsJSON, witnessJSON, err := encodeLifecyclePersistence(labels, event)
	if err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin coordinator shard lifecycle: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = errors.Join(retErr, rollbackErr)
		}
	}()
	if err := persistContainerShardLifecycleTx(ctx, tx, jobID, shardIdentity, labels, labelsJSON, witnessJSON, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit coordinator shard lifecycle: %w", err)
	}
	return nil
}

// persistContainerShardLifecycleTx 校验并写入单个 shard 阶段及必要的 invocation 时钟。
func persistContainerShardLifecycleTx(
	ctx context.Context,
	tx *sql.Tx,
	jobID string,
	shardIdentity string,
	labels map[string]string,
	labelsJSON []byte,
	witnessJSON any,
	event localci.FreshContainerLifecycleEvent,
) error {
	records, err := containerShardsFromQuery(ctx, tx, jobID)
	if err != nil {
		return err
	}
	index := slices.IndexFunc(records, func(record coordinatorShardRecord) bool {
		return record.Shard.IdentityDigest == shardIdentity
	})
	if index < 0 {
		return fmt.Errorf("%w: coordinator shard identity is missing", errCoordinatorState)
	}
	record := records[index]
	if err := validateContainerShardLifecycleTransition(record, labels, event); err != nil {
		return err
	}
	values := coordinatorShardLifecycleValues(record, event)
	result, err := tx.ExecContext(ctx, `UPDATE coordinator_job_shards SET container_phase = ?, container_id = ?,
container_labels_json = ?, container_image_reference = ?, container_config_digest = ?, container_host_config_digest = ?,
container_resource_witness_json = ?, container_resource_witness_digest = ?, container_resource_witness_verified = ?,
source_snapshot_dir = ?, removal_proof_digest = ?, started_at = ?, deadline_at = ?, exited_at = ?, completed_at = ?, exit_code = ?
WHERE job_id = ? AND shard_identity_digest = ?`, event.Phase, values.containerID, labelsJSON,
		event.ImageReference, event.ConfigDigest, event.HostConfigDigest, witnessJSON, event.ResourceWitnessDigest,
		event.ResourceWitnessDigest != "", event.SourceSnapshotDir, values.removalProof, values.startedAt, values.deadlineAt,
		values.exitedAt, values.completedAt, values.exitCode, jobID, shardIdentity)
	if err != nil {
		return fmt.Errorf("persist coordinator shard lifecycle: %w", err)
	}
	if err := requireOneCoordinatorRow(result, "record coordinator shard lifecycle"); err != nil {
		return err
	}
	if event.Phase == localci.FreshContainerPhaseStarting {
		return claimCoordinatorInvocationClock(ctx, tx, jobID, event.StartedAt, event.Deadline)
	}
	return nil
}

// claimCoordinatorInvocationClock 固定最早 shard 的 profile deadline 并拒绝后续重算。
func claimCoordinatorInvocationClock(ctx context.Context, tx *sql.Tx, jobID string, startedAt, deadline time.Time) error {
	var persistedStarted, persistedDeadline sql.NullString
	var profile string
	if err := tx.QueryRowContext(ctx, "SELECT started_at, deadline_at, profile FROM coordinator_jobs WHERE job_id = ? AND state = ?", jobID, jobStateStarted).
		Scan(&persistedStarted, &persistedDeadline, &profile); err != nil {
		return fmt.Errorf("read coordinator invocation clock: %w", err)
	}
	if !persistedStarted.Valid {
		if !deadline.Equal(startedAt.Add(coordinatorTimeout(gatecontract.Profile(profile)))) {
			return fmt.Errorf("%w: first shard deadline does not match profile timeout", errCoordinatorState)
		}
		result, err := tx.ExecContext(ctx, `UPDATE coordinator_jobs SET started_at = ?, deadline_at = ?
WHERE job_id = ? AND state = ? AND started_at IS NULL`, startedAt.Format(timeFormat), deadline.Format(timeFormat), jobID, jobStateStarted)
		if err != nil {
			return fmt.Errorf("persist first coordinator shard clock: %w", err)
		}
		return requireOneCoordinatorRow(result, "persist first coordinator shard clock")
	}
	first, err := time.Parse(timeFormat, persistedStarted.String)
	if err != nil {
		return fmt.Errorf("decode coordinator invocation start: %w", err)
	}
	persisted, err := time.Parse(timeFormat, persistedDeadline.String)
	if err != nil {
		return fmt.Errorf("decode coordinator invocation deadline: %w", err)
	}
	if startedAt.Before(first) || !deadline.Equal(persisted) {
		return fmt.Errorf("%w: shard lifecycle invocation clock drifted", errCoordinatorState)
	}
	return nil
}
