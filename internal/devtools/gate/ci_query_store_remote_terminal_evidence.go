package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// insertSQLiteRemoteCIShardTerminalEvidence 将终态容器和事件写入规范化表。
// 这里不写 JSON，避免出现第二个终态证据真相源。
func insertSQLiteRemoteCIShardTerminalEvidence(transaction *sql.Tx, record RemoteCIRunRecord, shard RemoteCIShardRecord) error {
	if shard.TerminalEvidence == nil {
		return nil
	}
	if err := shard.TerminalEvidence.Validate(); err != nil {
		return fmt.Errorf("validate remote CI terminal evidence: %w", err)
	}
	for ordinal, container := range shard.TerminalEvidence.Containers {
		if err := insertSQLiteRemoteCIContainerTerminalEvidence(transaction, record.JobID, shard.ShardIdentity, "container", ordinal, container); err != nil {
			return err
		}
	}
	for ordinal, container := range shard.TerminalEvidence.InitContainers {
		if err := insertSQLiteRemoteCIContainerTerminalEvidence(transaction, record.JobID, shard.ShardIdentity, "init", ordinal, container); err != nil {
			return err
		}
	}
	for ordinal, event := range shard.TerminalEvidence.Events {
		if _, err := transaction.Exec(`
			INSERT INTO ci_shard_terminal_events (
				job_id, shard_identity, ordinal, type, reason, message, count, last_timestamp
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, record.JobID, shard.ShardIdentity, ordinal, event.Type, event.Reason, event.Message, event.Count, event.LastTimestamp); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI shard terminal event", err)
		}
	}
	return nil
}

func insertSQLiteRemoteCIContainerTerminalEvidence(
	transaction *sql.Tx,
	jobID, shardIdentity, kind string,
	ordinal int,
	container RemoteCIContainerTerminalEvidence,
) error {
	var exitCode any
	if container.ExitCode != nil {
		exitCode = *container.ExitCode
	}
	if _, err := transaction.Exec(`
		INSERT INTO ci_shard_terminal_containers (
			job_id, shard_identity, container_kind, ordinal, name, state, exit_code, reason, message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, jobID, shardIdentity, kind, ordinal, container.Name, container.State, exitCode, container.Reason, container.Message); err != nil {
		return mapDurationLedgerSQLiteError("store remote CI shard terminal container", err)
	}
	return nil
}

// loadRemoteCIShardTerminalEvidence 从规范化表重建唯一的终态证据结构。
func loadRemoteCIShardTerminalEvidence(database sqliteRowQueryer, jobID string) (map[string]*RemoteCITerminalEvidence, error) {
	evidenceByShard := make(map[string]*RemoteCITerminalEvidence)
	if err := loadRemoteCIContainerTerminalEvidenceRows(database, jobID, evidenceByShard); err != nil {
		return nil, err
	}
	if err := loadRemoteCIEventTerminalEvidenceRows(database, jobID, evidenceByShard); err != nil {
		return nil, err
	}
	for shardIdentity, evidence := range evidenceByShard {
		if err := evidence.Validate(); err != nil {
			return nil, fmt.Errorf("validate stored remote CI terminal evidence for shard %q: %w", shardIdentity, err)
		}
	}
	return evidenceByShard, nil
}

// loadRemoteCIContainerTerminalEvidenceRows 读取规范化 container 与 init-container 行。
func loadRemoteCIContainerTerminalEvidenceRows(database sqliteRowQueryer, jobID string, evidenceByShard map[string]*RemoteCITerminalEvidence) error {
	rows, err := database.Query(`
		SELECT shard_identity, container_kind, ordinal, name, state, exit_code, reason, message
		FROM ci_shard_terminal_containers
		WHERE job_id = ?
		ORDER BY shard_identity, container_kind, ordinal
	`, jobID)
	if err != nil {
		return mapDurationLedgerSQLiteError("query remote CI terminal containers", err)
	}
	for rows.Next() {
		var (
			shardIdentity, kind string
			ordinal             int
			container           RemoteCIContainerTerminalEvidence
			exitCode            sql.NullInt64
		)
		if err := rows.Scan(&shardIdentity, &kind, &ordinal, &container.Name, &container.State, &exitCode, &container.Reason, &container.Message); err != nil {
			rows.Close()
			return mapDurationLedgerSQLiteError("scan remote CI terminal container", err)
		}
		if exitCode.Valid {
			value := exitCode.Int64
			container.ExitCode = &value
		}
		if err := appendRemoteCIContainerTerminalEvidenceRow(evidenceByShard, shardIdentity, kind, ordinal, container); err != nil {
			rows.Close()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return mapDurationLedgerSQLiteError("iterate remote CI terminal containers", err)
	}
	if err := rows.Close(); err != nil {
		return mapDurationLedgerSQLiteError("close remote CI terminal containers", err)
	}
	return nil
}

// appendRemoteCIContainerTerminalEvidenceRow 校验并按 provider ordinal 追加一个容器行。
func appendRemoteCIContainerTerminalEvidenceRow(evidenceByShard map[string]*RemoteCITerminalEvidence, shardIdentity, kind string, ordinal int, container RemoteCIContainerTerminalEvidence) error {
	if strings.TrimSpace(shardIdentity) == "" || (kind != "container" && kind != "init") || ordinal < 0 {
		return errors.New("stored remote CI terminal container identity is invalid")
	}
	evidence := remoteCITerminalEvidenceForShard(evidenceByShard, shardIdentity)
	if kind == "container" {
		if ordinal != len(evidence.Containers) {
			return fmt.Errorf("stored remote CI terminal container ordinal for shard %q is not contiguous", shardIdentity)
		}
		evidence.Containers = append(evidence.Containers, container)
		return nil
	}
	if ordinal != len(evidence.InitContainers) {
		return fmt.Errorf("stored remote CI terminal init-container ordinal for shard %q is not contiguous", shardIdentity)
	}
	evidence.InitContainers = append(evidence.InitContainers, container)
	return nil
}

// loadRemoteCIEventTerminalEvidenceRows 读取有序的最近 provider 事件行。
func loadRemoteCIEventTerminalEvidenceRows(database sqliteRowQueryer, jobID string, evidenceByShard map[string]*RemoteCITerminalEvidence) error {
	rows, err := database.Query(`
		SELECT shard_identity, ordinal, type, reason, message, count, last_timestamp
		FROM ci_shard_terminal_events
		WHERE job_id = ?
		ORDER BY shard_identity, ordinal
	`, jobID)
	if err != nil {
		return mapDurationLedgerSQLiteError("query remote CI terminal events", err)
	}
	for rows.Next() {
		var (
			shardIdentity string
			event         RemoteCIEventEvidence
			ordinal       int
		)
		if err := rows.Scan(&shardIdentity, &ordinal, &event.Type, &event.Reason, &event.Message, &event.Count, &event.LastTimestamp); err != nil {
			rows.Close()
			return mapDurationLedgerSQLiteError("scan remote CI terminal event", err)
		}
		if strings.TrimSpace(shardIdentity) == "" || ordinal < 0 {
			rows.Close()
			return errors.New("stored remote CI terminal event identity is invalid")
		}
		evidence := remoteCITerminalEvidenceForShard(evidenceByShard, shardIdentity)
		if ordinal != len(evidence.Events) {
			rows.Close()
			return fmt.Errorf("stored remote CI terminal event ordinal for shard %q is not contiguous", shardIdentity)
		}
		evidence.Events = append(evidence.Events, event)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return mapDurationLedgerSQLiteError("iterate remote CI terminal events", err)
	}
	if err := rows.Close(); err != nil {
		return mapDurationLedgerSQLiteError("close remote CI terminal events", err)
	}
	return nil
}

func remoteCITerminalEvidenceForShard(evidenceByShard map[string]*RemoteCITerminalEvidence, shardIdentity string) *RemoteCITerminalEvidence {
	evidence := evidenceByShard[shardIdentity]
	if evidence == nil {
		evidence = &RemoteCITerminalEvidence{}
		evidenceByShard[shardIdentity] = evidence
	}
	return evidence
}
