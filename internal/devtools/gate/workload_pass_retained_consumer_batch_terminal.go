package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// loadRetainedConsumerTerminalEvidence 通过两条批量关系查询恢复完整的 shard 终态详情。
// 终态表必须绑定本批次已加载 shard；缺失绑定或篡改行均不能被当作空详情。
func loadRetainedConsumerTerminalEvidence(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	if len(jobIDs) == 0 {
		return nil
	}
	indexes, err := retainedConsumerTerminalShardIndexes(records)
	if err != nil {
		return err
	}
	evidence := make(map[string]map[string]*RemoteCITerminalEvidence, len(records))
	if err := loadRetainedConsumerTerminalContainerRows(tx, jobIDs, indexes, evidence, stats); err != nil {
		return err
	}
	if err := loadRetainedConsumerTerminalEventRows(tx, jobIDs, indexes, evidence, stats); err != nil {
		return err
	}
	return assignRetainedConsumerTerminalEvidence(records, indexes, evidence)
}

// retainedConsumerTerminalShardIndexes 固定 job/shard 归属，拒绝重复 shard 投影。
func retainedConsumerTerminalShardIndexes(records map[string]RemoteCIRunRecord) (map[string]map[string]int, error) {
	indexes := make(map[string]map[string]int, len(records))
	for jobID, record := range records {
		byShard := make(map[string]int, len(record.Shards))
		for index, shard := range record.Shards {
			if strings.TrimSpace(shard.ShardIdentity) == "" {
				return nil, fmt.Errorf("retained consumer %q has an invalid shard identity", jobID)
			}
			if _, duplicate := byShard[shard.ShardIdentity]; duplicate {
				return nil, fmt.Errorf("retained consumer %q duplicates shard %q", jobID, shard.ShardIdentity)
			}
			byShard[shard.ShardIdentity] = index
		}
		indexes[jobID] = byShard
	}
	return indexes, nil
}

// loadRetainedConsumerTerminalContainerRows 批量扫描 container 与 init-container 终态行。
func loadRetainedConsumerTerminalContainerRows(tx *sql.Tx, jobIDs []string, indexes map[string]map[string]int, evidence map[string]map[string]*RemoteCITerminalEvidence, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT terminal.job_id, terminal.shard_identity, shards.shard_identity, terminal.container_kind, terminal.ordinal, terminal.name, terminal.state, terminal.exit_code, terminal.reason, terminal.message
		FROM ci_shard_terminal_containers AS terminal
		LEFT JOIN ci_shards AS shards ON shards.job_id = terminal.job_id AND shards.shard_identity = terminal.shard_identity
		WHERE terminal.job_id IN (`+placeholders+") ORDER BY terminal.job_id, terminal.shard_identity, terminal.container_kind, terminal.ordinal", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer terminal containers", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		if err := scanAndAppendRetainedConsumerTerminalContainer(rows, indexes, evidence); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer terminal containers", err)
	}
	return nil
}

// scanAndAppendRetainedConsumerTerminalContainer 重用单项 reader 的 ordinal 组装规则。
func scanAndAppendRetainedConsumerTerminalContainer(rows *sql.Rows, indexes map[string]map[string]int, evidence map[string]map[string]*RemoteCITerminalEvidence) error {
	var jobID, shardID, kind string
	var boundShard sql.NullString
	var ordinal int
	var container RemoteCIContainerTerminalEvidence
	var exitCode sql.NullInt64
	if err := rows.Scan(&jobID, &shardID, &boundShard, &kind, &ordinal, &container.Name, &container.State, &exitCode, &container.Reason, &container.Message); err != nil {
		return mapDurationLedgerSQLiteError("scan retained consumer terminal container", err)
	}
	if !boundShard.Valid || boundShard.String != shardID {
		return errors.New("retained consumer terminal container references an unknown shard")
	}
	if exitCode.Valid {
		value := exitCode.Int64
		container.ExitCode = &value
	}
	byShard, err := retainedConsumerTerminalEvidenceForShard(indexes, evidence, jobID, shardID)
	if err != nil {
		return err
	}
	return appendRemoteCIContainerTerminalEvidenceRow(byShard, shardID, kind, ordinal, container)
}

// loadRetainedConsumerTerminalEventRows 批量扫描有序 provider 终态事件。
func loadRetainedConsumerTerminalEventRows(tx *sql.Tx, jobIDs []string, indexes map[string]map[string]int, evidence map[string]map[string]*RemoteCITerminalEvidence, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT terminal.job_id, terminal.shard_identity, shards.shard_identity, terminal.ordinal, terminal.type, terminal.reason, terminal.message, terminal.count, terminal.last_timestamp
		FROM ci_shard_terminal_events AS terminal
		LEFT JOIN ci_shards AS shards ON shards.job_id = terminal.job_id AND shards.shard_identity = terminal.shard_identity
		WHERE terminal.job_id IN (`+placeholders+") ORDER BY terminal.job_id, terminal.shard_identity, terminal.ordinal", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer terminal events", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	for rows.Next() {
		if err := scanAndAppendRetainedConsumerTerminalEvent(rows, indexes, evidence); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer terminal events", err)
	}
	return nil
}

// scanAndAppendRetainedConsumerTerminalEvent 保持 provider ordinal 和 timestamp 的严格校验。
func scanAndAppendRetainedConsumerTerminalEvent(rows *sql.Rows, indexes map[string]map[string]int, evidence map[string]map[string]*RemoteCITerminalEvidence) error {
	var jobID, shardID string
	var boundShard sql.NullString
	var ordinal int
	var event RemoteCIEventEvidence
	if err := rows.Scan(&jobID, &shardID, &boundShard, &ordinal, &event.Type, &event.Reason, &event.Message, &event.Count, &event.LastTimestamp); err != nil {
		return mapDurationLedgerSQLiteError("scan retained consumer terminal event", err)
	}
	if !boundShard.Valid || boundShard.String != shardID {
		return errors.New("retained consumer terminal event references an unknown shard")
	}
	byShard, err := retainedConsumerTerminalEvidenceForShard(indexes, evidence, jobID, shardID)
	if err != nil {
		return err
	}
	stored := byShard[shardID]
	if ordinal != len(stored.Events) {
		return fmt.Errorf("stored remote CI terminal event ordinal for shard %q is not contiguous", shardID)
	}
	stored.Events = append(stored.Events, event)
	return nil
}

// retainedConsumerTerminalEvidenceForShard 只为已读取且已验证的 job/shard 创建详情。
func retainedConsumerTerminalEvidenceForShard(indexes map[string]map[string]int, evidence map[string]map[string]*RemoteCITerminalEvidence, jobID, shardID string) (map[string]*RemoteCITerminalEvidence, error) {
	if _, knownJob := indexes[jobID]; !knownJob {
		return nil, errors.New("retained consumer terminal evidence references an unknown run")
	}
	if _, knownShard := indexes[jobID][shardID]; !knownShard {
		return nil, errors.New("retained consumer terminal evidence references an unknown shard")
	}
	byShard := evidence[jobID]
	if byShard == nil {
		byShard = make(map[string]*RemoteCITerminalEvidence)
		evidence[jobID] = byShard
	}
	if byShard[shardID] == nil {
		byShard[shardID] = &RemoteCITerminalEvidence{}
	}
	return byShard, nil
}

// assignRetainedConsumerTerminalEvidence 校验完整终态结构后写回各自 shard。
func assignRetainedConsumerTerminalEvidence(records map[string]RemoteCIRunRecord, indexes map[string]map[string]int, evidence map[string]map[string]*RemoteCITerminalEvidence) error {
	for jobID, byShard := range evidence {
		record := records[jobID]
		for shardID, details := range byShard {
			if err := details.Validate(); err != nil {
				return fmt.Errorf("validate retained consumer %q terminal evidence for shard %q: %w", jobID, shardID, err)
			}
			record.Shards[indexes[jobID][shardID]].TerminalEvidence = details
		}
		records[jobID] = record
	}
	return nil
}
