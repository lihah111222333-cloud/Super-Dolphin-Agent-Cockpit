package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// RemoteCITimingWarning 是运行中按 provider StartTime 观察到的非终止目标超限事实。
// Live 与最终表只表示同一事实的两个互斥生命周期阶段，不得同时保留同一 job。
type RemoteCITimingWarning struct {
	JobID              string                               `json:"job_id"`
	AgentTokenDigest   string                               `json:"agent_token_digest"`
	AcceptedGeneration uint64                               `json:"accepted_generation"`
	Scope              cicontract.TimingScope               `json:"scope"`
	ShardIdentity      string                               `json:"shard_identity"`
	WorkloadID         GateID                               `json:"workload_id"`
	EvidenceKind       cicontract.TimingWarningEvidenceKind `json:"evidence_kind"`
	Action             cicontract.TimingWarningAction       `json:"action"`
	EvidenceStartedAt  time.Time                            `json:"evidence_started_at"`
	ObservedAt         time.Time                            `json:"observed_at"`
	EvidenceDurationMS int64                                `json:"evidence_duration_ms"`
	TargetMS           int64                                `json:"target_ms"`
	WarningText        string                               `json:"warning_text"`
}

// CanonicalRemoteCITimingWarningText 从结构化身份生成稳定的人类投影，文本不参与幂等键。
func CanonicalRemoteCITimingWarningText(warning RemoteCITimingWarning) string {
	if warning.EvidenceKind == cicontract.TimingWarningEvidenceRunning {
		return fmt.Sprintf(
			"CI target warning: shard %q reached running target_ms=%d from provider StartTime; action=%s",
			warning.ShardIdentity, warning.TargetMS, warning.Action,
		)
	}
	return fmt.Sprintf(
		"CI target warning: workload %q in shard %q %s=%dms exceeded target_ms=%d; action=%s",
		warning.WorkloadID, warning.ShardIdentity, warning.EvidenceKind,
		warning.EvidenceDurationMS, warning.TargetMS, warning.Action,
	)
}

// Validate 拒绝伪造的作用域、终止动作、本地轮询时间或非规范文本。
func (warning RemoteCITimingWarning) Validate() error {
	if err := validateRemoteCITimingWarningIdentity(warning); err != nil {
		return err
	}
	if err := validateRemoteCITimingWarningSubject(warning); err != nil {
		return err
	}
	if err := cicontract.ValidateTimingWarningEvidenceKind(warning.EvidenceKind); err != nil {
		return err
	}
	if err := cicontract.ValidateTimingWarningAction(warning.Action); err != nil {
		return err
	}
	if warning.TargetMS != cicontract.ShardTargetDuration.Milliseconds() {
		return fmt.Errorf("remote CI timing warning target must equal %dms", cicontract.ShardTargetDuration.Milliseconds())
	}
	if err := validateRemoteCITimingWarningTimestamps(warning); err != nil {
		return err
	}
	if err := validateRemoteCITimingWarningThreshold(warning); err != nil {
		return err
	}
	if warning.WarningText != CanonicalRemoteCITimingWarningText(warning) {
		return errors.New("remote CI timing warning text is not canonical")
	}
	return nil
}

// validateRemoteCITimingWarningIdentity 校验告警的 job、agent 和 accepted generation 身份。
func validateRemoteCITimingWarningIdentity(warning RemoteCITimingWarning) error {
	if strings.TrimSpace(warning.JobID) == "" {
		return errors.New("remote CI timing warning job ID is required")
	}
	if warning.JobID != strings.TrimSpace(warning.JobID) {
		return errors.New("remote CI timing warning job ID is required")
	}
	if err := cicontract.ValidateAgentTokenDigest(warning.AgentTokenDigest); err != nil {
		return fmt.Errorf("remote CI timing warning agent token digest: %w", err)
	}
	if warning.AcceptedGeneration == 0 {
		return errors.New("remote CI timing warning accepted generation is required")
	}
	return nil
}

// validateRemoteCITimingWarningTimestamps 校验 provider 起止时间使用精确毫秒。
func validateRemoteCITimingWarningTimestamps(warning RemoteCITimingWarning) error {
	if !canonicalRemoteCITimingWarningTime(warning.EvidenceStartedAt) {
		return errors.New("remote CI timing warning evidence timestamps must use exact milliseconds")
	}
	if !canonicalRemoteCITimingWarningTime(warning.ObservedAt) {
		return errors.New("remote CI timing warning evidence timestamps must use exact milliseconds")
	}
	return nil
}

// validateRemoteCITimingWarningSubject 校验 shard/workload 作用域与 evidence kind 的绑定。
func validateRemoteCITimingWarningSubject(warning RemoteCITimingWarning) error {
	shard := strings.TrimSpace(warning.ShardIdentity)
	workload := strings.TrimSpace(string(warning.WorkloadID))
	if shard != warning.ShardIdentity {
		return errors.New("remote CI timing warning subject identities are not canonical")
	}
	if workload != string(warning.WorkloadID) {
		return errors.New("remote CI timing warning subject identities are not canonical")
	}
	switch warning.Scope {
	case cicontract.TimingScopeShard:
		return validateRemoteCITimingWarningShardSubject(shard, workload, warning.EvidenceKind)
	case cicontract.TimingScopeWorkload:
		return validateRemoteCITimingWarningWorkloadSubject(shard, workload, warning.EvidenceKind)
	}
	return fmt.Errorf("remote CI timing warning scope %q has no target-warning producer", warning.Scope)
}

// validateRemoteCITimingWarningShardSubject 校验运行中 shard 告警只绑定 shard。
func validateRemoteCITimingWarningShardSubject(
	shard string,
	workload string,
	evidenceKind cicontract.TimingWarningEvidenceKind,
) error {
	if shard == "" {
		return errors.New("shard timing warning must bind running evidence to exactly one shard")
	}
	if workload != "" {
		return errors.New("shard timing warning must bind running evidence to exactly one shard")
	}
	if evidenceKind != cicontract.TimingWarningEvidenceRunning {
		return errors.New("shard timing warning must bind running evidence to exactly one shard")
	}
	return nil
}

// validateRemoteCITimingWarningWorkloadSubject 校验 workload 告警只绑定 test_body 或 total。
func validateRemoteCITimingWarningWorkloadSubject(
	shard string,
	workload string,
	evidenceKind cicontract.TimingWarningEvidenceKind,
) error {
	if shard == "" {
		return errors.New("workload timing warning must bind test_body or total evidence to shard and workload")
	}
	if workload == "" {
		return errors.New("workload timing warning must bind test_body or total evidence to shard and workload")
	}
	if evidenceKind != cicontract.TimingWarningEvidenceTestBody && evidenceKind != cicontract.TimingWarningEvidenceTotal {
		return errors.New("workload timing warning must bind test_body or total evidence to shard and workload")
	}
	return nil
}

// validateRemoteCITimingWarningThreshold 校验告警只在目标时长达到后产生。
func validateRemoteCITimingWarningThreshold(warning RemoteCITimingWarning) error {
	intervalMS := warning.ObservedAt.Sub(warning.EvidenceStartedAt).Milliseconds()
	if intervalMS <= 0 {
		return errors.New("remote CI timing warning evidence interval is invalid")
	}
	if warning.EvidenceKind == cicontract.TimingWarningEvidenceRunning {
		if warning.EvidenceDurationMS != intervalMS || warning.EvidenceDurationMS < warning.TargetMS {
			return errors.New("remote CI running warning precedes its provider target threshold")
		}
		return nil
	}
	if warning.EvidenceDurationMS <= warning.TargetMS {
		return errors.New("remote CI workload warning does not exceed its target")
	}
	if delta := intervalMS - warning.EvidenceDurationMS; delta < -1 || delta > 1 {
		return errors.New("remote CI workload warning duration does not match its raw timing interval")
	}
	return nil
}

func canonicalRemoteCITimingWarningTime(value time.Time) bool {
	return !value.IsZero() && value.Equal(value.UTC().Truncate(time.Millisecond))
}

type remoteCITimingWarningKey struct {
	JobID         string
	Scope         cicontract.TimingScope
	ShardIdentity string
	WorkloadID    GateID
	EvidenceKind  cicontract.TimingWarningEvidenceKind
	TargetMS      int64
}

func (warning RemoteCITimingWarning) key() remoteCITimingWarningKey {
	return remoteCITimingWarningKey{
		JobID: warning.JobID, Scope: warning.Scope, ShardIdentity: warning.ShardIdentity,
		WorkloadID: warning.WorkloadID, EvidenceKind: warning.EvidenceKind, TargetMS: warning.TargetMS,
	}
}

// RecordLiveRemoteCITimingWarning 在 run 终态行建立前写入一次结构化告警事实。
// 并发重复写返回第一次观测；相同幂等键的身份漂移会阻断。
func (store *DurationLedgerStore) RecordLiveRemoteCITimingWarning(
	warning RemoteCITimingWarning,
) (stored RemoteCITimingWarning, inserted bool, returnErr error) {
	if store == nil {
		return stored, false, errors.New("duration ledger store is nil")
	}
	if err := warning.Validate(); err != nil {
		return stored, false, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return stored, false, err
	}
	defer database.Close()
	returnErr = withSQLiteWriteTransaction(database, "live remote CI timing warning", func(transaction *sql.Tx) error {
		if err := requireHistoricallyAcceptedGeneration(transaction, warning.AcceptedGeneration); err != nil {
			return err
		}
		if err := requireRetainedRemoteCITimingWarningGeneration(transaction, warning.AcceptedGeneration); err != nil {
			return err
		}
		if err := rejectFinalizedRemoteCITimingWarningWrite(transaction, warning.JobID); err != nil {
			return err
		}
		var err error
		stored, inserted, err = recordLiveRemoteCITimingWarningInTransaction(transaction, warning)
		if err != nil {
			return err
		}
		return compactDurationLedgerAuthority(transaction)
	})
	return stored, inserted, returnErr
}

// recordLiveRemoteCITimingWarningInTransaction 在同一 SQLite 写事务中完成告警幂等写入。
func recordLiveRemoteCITimingWarningInTransaction(
	transaction *sql.Tx,
	warning RemoteCITimingWarning,
) (RemoteCITimingWarning, bool, error) {
	var stored RemoteCITimingWarning
	inserted, err := insertLiveRemoteCITimingWarning(transaction, warning)
	if err != nil {
		return stored, false, err
	}
	stored, err = loadRemoteCITimingWarningByKey(transaction, cicontract.LiveTimingWarningsTable, warning.key())
	if err != nil {
		return stored, inserted, err
	}
	if !remoteCITimingWarningRetryMatches(stored, warning) {
		return stored, inserted, fmt.Errorf("remote CI timing warning %v conflicts with its first live observation", warning.key())
	}
	if inserted {
		if err := advanceCIQueryRevision(transaction, warning.ObservedAt); err != nil {
			return stored, inserted, err
		}
	}
	return stored, inserted, nil
}

func requireRetainedRemoteCITimingWarningGeneration(transaction *sql.Tx, generation uint64) error {
	current, err := currentAcceptedBaselineGeneration(transaction)
	if err != nil {
		return err
	}
	if generation > current || current-generation >= cicontract.RetentionGenerations {
		return fmt.Errorf("remote CI timing warning accepted generation %d is outside the retained authority window", generation)
	}
	return nil
}

// compactLiveRemoteCITimingWarnings 用当前 accepted singleton 的数值窗口清理崩溃残留。
// Live 行不参与五个历史根的保留集合，因此不会反向延长任何历史 generation。
func compactLiveRemoteCITimingWarnings(transaction *sql.Tx) error {
	current, err := currentAcceptedBaselineGeneration(transaction)
	if err != nil {
		return err
	}
	retained := make([]string, 0, cicontract.RetentionGenerations)
	for offset := uint64(0); offset < cicontract.RetentionGenerations && offset < current; offset++ {
		retained = append(retained, strconv.FormatUint(current-offset, 10))
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(retained)), ",")
	arguments := make([]any, len(retained))
	for index, generation := range retained {
		arguments[index] = generation
	}
	query := `DELETE FROM ` + cicontract.LiveTimingWarningsTable + ` WHERE accepted_generation NOT IN (` + placeholders + `)`
	if _, err := transaction.Exec(query, arguments...); err != nil {
		return mapDurationLedgerSQLiteError("compact live remote CI timing warnings", err)
	}
	return nil
}

func rejectFinalizedRemoteCITimingWarningWrite(transaction *sql.Tx, jobID string) error {
	var count int
	if err := transaction.QueryRow(`SELECT COUNT(*) FROM ci_runs WHERE job_id = ?`, jobID).Scan(&count); err != nil {
		return mapDurationLedgerSQLiteError("inspect finalized remote CI timing warning job", err)
	}
	if count != 0 {
		return fmt.Errorf("remote CI timing warning job %q already has a final run projection", jobID)
	}
	return nil
}

func insertLiveRemoteCITimingWarning(transaction *sql.Tx, warning RemoteCITimingWarning) (bool, error) {
	result, err := transaction.Exec(`
		INSERT INTO ci_live_timing_warnings (
			job_id, agent_token_digest, accepted_generation, scope, shard_identity, workload_id,
			evidence_kind, action, evidence_started_at_unix_ms, observed_at_unix_ms,
			evidence_duration_ms, target_ms, warning_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(job_id, scope, shard_identity, workload_id, evidence_kind, target_ms) DO NOTHING
	`, remoteCITimingWarningSQLArguments(warning)...)
	if err != nil {
		return false, mapDurationLedgerSQLiteError("store live remote CI timing warning", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read live remote CI timing warning insert count: %w", err)
	}
	if rows != 0 && rows != 1 {
		return false, fmt.Errorf("live remote CI timing warning insert affected %d rows", rows)
	}
	return rows == 1, nil
}

func remoteCITimingWarningSQLArguments(warning RemoteCITimingWarning) []any {
	return []any{
		warning.JobID, warning.AgentTokenDigest, strconv.FormatUint(warning.AcceptedGeneration, 10),
		string(warning.Scope), warning.ShardIdentity, string(warning.WorkloadID), string(warning.EvidenceKind),
		string(warning.Action), warning.EvidenceStartedAt.UTC().UnixMilli(), warning.ObservedAt.UTC().UnixMilli(),
		warning.EvidenceDurationMS, warning.TargetMS, warning.WarningText,
	}
}

// remoteCITimingWarningRetryMatches 校验重复写请求与首次 live 观测身份一致。
func remoteCITimingWarningRetryMatches(stored, requested RemoteCITimingWarning) bool {
	return stored.key() == requested.key() &&
		stored.AgentTokenDigest == requested.AgentTokenDigest &&
		stored.AcceptedGeneration == requested.AcceptedGeneration &&
		stored.EvidenceKind == requested.EvidenceKind &&
		stored.Action == requested.Action &&
		stored.EvidenceStartedAt.Equal(requested.EvidenceStartedAt) &&
		stored.EvidenceDurationMS == requested.EvidenceDurationMS &&
		stored.WarningText == requested.WarningText
}

func remoteCITimingWarningEqual(first, second RemoteCITimingWarning) bool {
	return remoteCITimingWarningRetryMatches(first, second) && first.ObservedAt.Equal(second.ObservedAt)
}

// loadRemoteCITimingWarningByKey 按结构化幂等键读取并校验一条 timing warning。
func loadRemoteCITimingWarningByKey(
	database sqliteRowQueryer,
	table string,
	key remoteCITimingWarningKey,
) (RemoteCITimingWarning, error) {
	if err := validateRemoteCITimingWarningTableName(table); err != nil {
		return RemoteCITimingWarning{}, err
	}
	query := fmt.Sprintf(`SELECT agent_token_digest, accepted_generation, action,
		evidence_started_at_unix_ms, observed_at_unix_ms, evidence_duration_ms, warning_text
		FROM %s WHERE job_id = ? AND scope = ? AND shard_identity = ? AND workload_id = ? AND evidence_kind = ? AND target_ms = ?`, table)
	warning := RemoteCITimingWarning{
		JobID: key.JobID, Scope: key.Scope, ShardIdentity: key.ShardIdentity,
		WorkloadID: key.WorkloadID, EvidenceKind: key.EvidenceKind, TargetMS: key.TargetMS,
	}
	var acceptedGeneration string
	var action string
	var providerStartedAtMS, observedAtMS int64
	err := database.QueryRow(query, key.JobID, string(key.Scope), key.ShardIdentity, string(key.WorkloadID), string(key.EvidenceKind), key.TargetMS).Scan(
		&warning.AgentTokenDigest, &acceptedGeneration, &action,
		&providerStartedAtMS, &observedAtMS, &warning.EvidenceDurationMS, &warning.WarningText,
	)
	if err != nil {
		return RemoteCITimingWarning{}, mapDurationLedgerSQLiteError("load remote CI timing warning", err)
	}
	generation, err := strconv.ParseUint(acceptedGeneration, 10, 64)
	if err != nil || generation == 0 || acceptedGeneration != strconv.FormatUint(generation, 10) {
		return RemoteCITimingWarning{}, errors.New("stored remote CI timing warning accepted generation is invalid")
	}
	warning.AcceptedGeneration = generation
	warning.Action = cicontract.TimingWarningAction(action)
	warning.EvidenceStartedAt = time.UnixMilli(providerStartedAtMS).UTC()
	warning.ObservedAt = time.UnixMilli(observedAtMS).UTC()
	if err := warning.Validate(); err != nil {
		return RemoteCITimingWarning{}, fmt.Errorf("validate stored remote CI timing warning: %w", err)
	}
	return warning, nil
}

// loadRemoteCITimingWarnings 读取指定 job 在 live 或 final 表中的全部告警。
func loadRemoteCITimingWarnings(
	database sqliteRowQueryer,
	table string,
	jobID string,
) ([]RemoteCITimingWarning, error) {
	if err := validateRemoteCITimingWarningTableName(table); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT agent_token_digest, accepted_generation, scope, shard_identity,
		workload_id, evidence_kind, action, evidence_started_at_unix_ms, observed_at_unix_ms,
		evidence_duration_ms, target_ms, warning_text
		FROM %s WHERE job_id = ? ORDER BY scope, shard_identity, workload_id, evidence_kind, target_ms`, table)
	rows, err := database.Query(query, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI timing warnings", err)
	}
	defer rows.Close()
	warnings := make([]RemoteCITimingWarning, 0)
	for rows.Next() {
		warning := RemoteCITimingWarning{JobID: jobID}
		var acceptedGeneration, scope, workloadID, evidenceKind, action string
		var providerStartedAtMS, observedAtMS int64
		if err := rows.Scan(
			&warning.AgentTokenDigest, &acceptedGeneration, &scope, &warning.ShardIdentity,
			&workloadID, &evidenceKind, &action, &providerStartedAtMS, &observedAtMS,
			&warning.EvidenceDurationMS, &warning.TargetMS,
			&warning.WarningText,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI timing warning", err)
		}
		generation, err := strconv.ParseUint(acceptedGeneration, 10, 64)
		if err != nil || generation == 0 || acceptedGeneration != strconv.FormatUint(generation, 10) {
			return nil, errors.New("stored remote CI timing warning accepted generation is invalid")
		}
		warning.AcceptedGeneration = generation
		warning.Scope = cicontract.TimingScope(scope)
		warning.WorkloadID = GateID(workloadID)
		warning.EvidenceKind = cicontract.TimingWarningEvidenceKind(evidenceKind)
		warning.Action = cicontract.TimingWarningAction(action)
		warning.EvidenceStartedAt = time.UnixMilli(providerStartedAtMS).UTC()
		warning.ObservedAt = time.UnixMilli(observedAtMS).UTC()
		if err := warning.Validate(); err != nil {
			return nil, fmt.Errorf("validate stored remote CI timing warning: %w", err)
		}
		warnings = append(warnings, warning)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI timing warnings", err)
	}
	return warnings, nil
}

func canonicalRemoteCITimingWarnings(warnings []RemoteCITimingWarning) []RemoteCITimingWarning {
	result := append([]RemoteCITimingWarning(nil), warnings...)
	sort.Slice(result, func(left, right int) bool {
		first, second := result[left].key(), result[right].key()
		if first.Scope != second.Scope {
			return first.Scope < second.Scope
		}
		if first.ShardIdentity != second.ShardIdentity {
			return first.ShardIdentity < second.ShardIdentity
		}
		if first.WorkloadID != second.WorkloadID {
			return first.WorkloadID < second.WorkloadID
		}
		if first.EvidenceKind != second.EvidenceKind {
			return first.EvidenceKind < second.EvidenceKind
		}
		return first.TargetMS < second.TargetMS
	})
	return result
}

func remoteCITimingWarningSetsEqual(first, second []RemoteCITimingWarning) bool {
	if len(first) != len(second) {
		return false
	}
	first, second = canonicalRemoteCITimingWarnings(first), canonicalRemoteCITimingWarnings(second)
	for index := range first {
		if !remoteCITimingWarningEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

// validateRemoteCIRunTimingWarnings 闭合结构化事实与最终 run 身份及人类文本投影。
func validateRemoteCIRunTimingWarnings(record RemoteCIRunRecord) error {
	expectedWorkload, err := BuildRemoteCIWorkloadTimingWarnings(
		record.JobID, record.AgentTokenDigest, record.AcceptedGeneration,
		record.WorkloadExecutions, record.TimingObservations,
	)
	if err != nil {
		return err
	}
	actualWorkload := make([]RemoteCITimingWarning, 0, len(expectedWorkload))
	seen := make(map[remoteCITimingWarningKey]struct{}, len(record.TimingWarnings))
	for _, warning := range record.TimingWarnings {
		if err := validateRemoteCIRunTimingWarning(record, warning, seen); err != nil {
			return err
		}
		if warning.Scope == cicontract.TimingScopeWorkload {
			actualWorkload = append(actualWorkload, warning)
		}
	}
	if !remoteCITimingWarningSetsEqual(actualWorkload, expectedWorkload) {
		return errors.New("remote CI workload timing warnings do not equal exact workload observations")
	}
	return validateRemoteCIRunTimingWarningHumanProjection(record)
}

// validateRemoteCIRunTimingWarning 校验单条告警与 run 身份及 shard 回执的绑定。
func validateRemoteCIRunTimingWarning(
	record RemoteCIRunRecord,
	warning RemoteCITimingWarning,
	seen map[remoteCITimingWarningKey]struct{},
) error {
	if err := warning.Validate(); err != nil {
		return err
	}
	if warning.JobID != record.JobID || warning.AgentTokenDigest != record.AgentTokenDigest ||
		warning.AcceptedGeneration != record.AcceptedGeneration {
		return errors.New("remote CI timing warning does not match its run identity")
	}
	if _, duplicate := seen[warning.key()]; duplicate {
		return fmt.Errorf("remote CI timing warning %v is duplicated", warning.key())
	}
	seen[warning.key()] = struct{}{}
	if warning.Scope == cicontract.TimingScopeShard && !remoteCIRunHasShard(record.Shards, warning.ShardIdentity) {
		return fmt.Errorf("remote CI running warning shard %q is not part of its run", warning.ShardIdentity)
	}
	return nil
}

func remoteCIRunHasShard(shards []RemoteCIShardRecord, identity string) bool {
	for _, shard := range shards {
		if shard.ShardIdentity == identity {
			return true
		}
	}
	return false
}

// validateRemoteCIRunTimingWarningHumanProjection 校验人类文本是结构化告警的精确投影。
func validateRemoteCIRunTimingWarningHumanProjection(record RemoteCIRunRecord) error {
	if len(record.Warnings) != len(record.TimingWarnings) {
		return errors.New("remote CI human warnings must be derived exactly from structured timing warnings")
	}
	expected := make(map[string]struct{}, len(record.TimingWarnings))
	for _, warning := range record.TimingWarnings {
		expected[warning.WarningText] = struct{}{}
	}
	if len(expected) != len(record.TimingWarnings) {
		return errors.New("remote CI structured timing warning human projections are duplicated")
	}
	for _, warning := range record.Warnings {
		if _, exists := expected[warning]; !exists {
			return errors.New("remote CI human warning lacks a structured timing warning fact")
		}
		delete(expected, warning)
	}
	if len(expected) != 0 {
		return errors.New("remote CI structured timing warning lacks its canonical human projection")
	}
	return nil
}

// finalizeSQLiteRemoteCITimingWarnings 在 run 写事务内把 live 事实精确移动到最终投影。
func finalizeSQLiteRemoteCITimingWarnings(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if err := validateRemoteCIRunTimingWarnings(record); err != nil {
		return err
	}
	requested := canonicalRemoteCITimingWarnings(record.TimingWarnings)
	live, err := loadRemoteCITimingWarnings(transaction, cicontract.LiveTimingWarningsTable, record.JobID)
	if err != nil {
		return err
	}
	final, err := loadRemoteCITimingWarnings(transaction, cicontract.RunTimingWarningsTable, record.JobID)
	if err != nil {
		return err
	}
	if len(final) != 0 {
		return validateExistingRemoteCITimingWarningFinalization(live, final, requested)
	}
	running := remoteCIRunningTimingWarnings(requested)
	if !remoteCITimingWarningSetsEqual(live, running) {
		return errors.New("remote CI live timing warnings conflict with the requested final projection")
	}
	if err := insertFinalRemoteCITimingWarnings(transaction, requested); err != nil {
		return err
	}
	if err := deleteFinalizedLiveRemoteCITimingWarnings(transaction, record.JobID, len(live)); err != nil {
		return err
	}
	return verifyFinalRemoteCITimingWarnings(transaction, record.JobID, requested)
}

func validateExistingRemoteCITimingWarningFinalization(live, final, requested []RemoteCITimingWarning) error {
	if len(live) != 0 {
		return errors.New("remote CI timing warning exists in both live and final lifecycle tables")
	}
	if !remoteCITimingWarningSetsEqual(final, requested) {
		return errors.New("remote CI final timing warnings conflict with the requested projection")
	}
	return nil
}

func remoteCIRunningTimingWarnings(warnings []RemoteCITimingWarning) []RemoteCITimingWarning {
	running := make([]RemoteCITimingWarning, 0, len(warnings))
	for _, warning := range warnings {
		if warning.EvidenceKind == cicontract.TimingWarningEvidenceRunning {
			running = append(running, warning)
		}
	}
	return canonicalRemoteCITimingWarnings(running)
}

func insertFinalRemoteCITimingWarnings(transaction *sql.Tx, warnings []RemoteCITimingWarning) error {
	for _, warning := range warnings {
		if err := insertFinalRemoteCITimingWarning(transaction, warning); err != nil {
			return err
		}
	}
	return nil
}

func deleteFinalizedLiveRemoteCITimingWarnings(transaction *sql.Tx, jobID string, want int) error {
	deleted, err := transaction.Exec(`DELETE FROM ci_live_timing_warnings WHERE job_id = ?`, jobID)
	if err != nil {
		return mapDurationLedgerSQLiteError("clear finalized live remote CI timing warnings", err)
	}
	count, err := deleted.RowsAffected()
	if err != nil {
		return fmt.Errorf("read finalized live remote CI timing warning delete count: %w", err)
	}
	if count != int64(want) {
		return fmt.Errorf("finalized remote CI timing warning delete count = %d, want %d", count, want)
	}
	return nil
}

func verifyFinalRemoteCITimingWarnings(transaction *sql.Tx, jobID string, requested []RemoteCITimingWarning) error {
	stored, err := loadRemoteCITimingWarnings(transaction, cicontract.RunTimingWarningsTable, jobID)
	if err != nil {
		return err
	}
	if !remoteCITimingWarningSetsEqual(stored, requested) {
		return errors.New("remote CI final timing warning readback drifted")
	}
	return nil
}

func insertFinalRemoteCITimingWarning(transaction *sql.Tx, warning RemoteCITimingWarning) error {
	if _, err := transaction.Exec(`
		INSERT INTO ci_run_timing_warnings (
			job_id, agent_token_digest, accepted_generation, scope, shard_identity, workload_id,
			evidence_kind, action, evidence_started_at_unix_ms, observed_at_unix_ms,
			evidence_duration_ms, target_ms, warning_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, remoteCITimingWarningSQLArguments(warning)...); err != nil {
		return mapDurationLedgerSQLiteError("store final remote CI timing warning", err)
	}
	return nil
}
