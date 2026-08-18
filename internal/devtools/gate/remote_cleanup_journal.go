package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RemoteCleanupTargetKind 区分 OSS prefix 与 ECI container group 恢复目标。
type RemoteCleanupTargetKind string

const (
	RemoteCleanupTargetOSSPrefix         RemoteCleanupTargetKind = "oss_prefix"
	RemoteCleanupTargetECIContainerGroup RemoteCleanupTargetKind = "eci_container_group"
)

// RemoteCleanupTarget 是不依赖 ci_runs 的远程副作用恢复记录。
type RemoteCleanupTarget struct {
	Kind              RemoteCleanupTargetKind
	Key               string
	ProviderID        string
	DeterministicName string
	JobTag            string
}

// RemoteCleanupJournal 只保存 cleanup 操作所需的 provider 归属信息。
type RemoteCleanupJournal struct {
	JobID   string
	Targets []RemoteCleanupTarget
}

// BeginRemoteCleanupJournal 在任何 job 前缀 OSS 写入前登记唯一前缀意图。
func (store *DurationLedgerStore) BeginRemoteCleanupJournal(jobID, prefix string) error {
	if store == nil {
		return errors.New("remote cleanup journal store is nil")
	}
	if err := validateRemoteCleanupJobAndPrefix(jobID, prefix); err != nil {
		return err
	}
	return store.withRemoteCleanupJournalDatabase(func(database durationLedgerSQLiteExecutor) error {
		if _, err := database.ExecContext(context.Background(), `INSERT INTO ci_remote_cleanup_journals(job_id, created_at) VALUES (?, ?) ON CONFLICT(job_id) DO NOTHING`, jobID, store.nowFunc().UTC().Format(time.RFC3339Nano)); err != nil {
			return mapDurationLedgerSQLiteError("insert remote cleanup journal", err)
		}
		target := RemoteCleanupTarget{Kind: RemoteCleanupTargetOSSPrefix, Key: prefix}
		if err := validateRemoteCleanupTarget(jobID, target); err != nil {
			return err
		}
		return ensureRemoteCleanupTarget(database, jobID, target)
	})
}

// RecordRemoteCleanupContainerGroupIntent 在 CreateContainerGroup 前登记可重建的精确查询键。
func (store *DurationLedgerStore) RecordRemoteCleanupContainerGroupIntent(jobID, name string) error {
	if store == nil {
		return errors.New("remote cleanup journal store is nil")
	}
	target := RemoteCleanupTarget{Kind: RemoteCleanupTargetECIContainerGroup, Key: name, DeterministicName: name, JobTag: jobID}
	if err := validateRemoteCleanupTarget(jobID, target); err != nil {
		return err
	}
	return store.withRemoteCleanupJournalDatabase(func(database durationLedgerSQLiteExecutor) error {
		return ensureRemoteCleanupTarget(database, jobID, target)
	})
}

// SetRemoteCleanupContainerGroupProviderID 将 Create 成功的真实 provider ID 回写到既有意图。
func (store *DurationLedgerStore) SetRemoteCleanupContainerGroupProviderID(jobID, name, providerID string) error {
	if store == nil {
		return errors.New("remote cleanup journal store is nil")
	}
	if err := validateRemoteCleanupTarget(jobID, RemoteCleanupTarget{Kind: RemoteCleanupTargetECIContainerGroup, Key: name, DeterministicName: name, JobTag: jobID}); err != nil {
		return err
	}
	if strings.TrimSpace(providerID) == "" {
		return errors.New("remote cleanup provider ID is required")
	}
	return store.withRemoteCleanupJournalDatabase(func(database durationLedgerSQLiteExecutor) error {
		result, err := database.ExecContext(context.Background(), `UPDATE ci_remote_cleanup_targets SET provider_id = ? WHERE job_id = ? AND target_kind = ? AND target_key = ? AND (provider_id = '' OR provider_id = ?)`, providerID, jobID, RemoteCleanupTargetECIContainerGroup, name, providerID)
		if err != nil {
			return mapDurationLedgerSQLiteError("update remote cleanup provider ID", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return mapDurationLedgerSQLiteError("read remote cleanup provider ID update", err)
		}
		if changed != 1 {
			return errors.New("remote cleanup container group intent is missing or provider ID drifted")
		}
		return nil
	})
}

// LoadRemoteCleanupJournal 只读取独立运维表；缺失 journal 不能被解释为已清理。
func (store *DurationLedgerStore) LoadRemoteCleanupJournal(jobID string) (RemoteCleanupJournal, error) {
	if store == nil {
		return RemoteCleanupJournal{}, errors.New("remote cleanup journal store is nil")
	}
	if strings.TrimSpace(jobID) == "" {
		return RemoteCleanupJournal{}, errors.New("remote cleanup job ID is required")
	}
	var journal RemoteCleanupJournal
	err := store.withRemoteCleanupJournalDatabase(func(database durationLedgerSQLiteExecutor) error {
		var found string
		if err := database.QueryRowContext(context.Background(), `SELECT job_id FROM ci_remote_cleanup_journals WHERE job_id = ?`, jobID).Scan(&found); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("remote cleanup journal is missing")
			}
			return mapDurationLedgerSQLiteError("load remote cleanup journal", err)
		}
		rows, err := database.QueryContext(context.Background(), `SELECT target_kind, target_key, provider_id, deterministic_name, job_tag FROM ci_remote_cleanup_targets WHERE job_id = ? ORDER BY target_kind, target_key`, jobID)
		if err != nil {
			return mapDurationLedgerSQLiteError("load remote cleanup targets", err)
		}
		defer rows.Close()
		journal.JobID = found
		journal.Targets, err = loadRemoteCleanupTargets(rows, jobID)
		if err != nil {
			return err
		}
		if len(journal.Targets) == 0 {
			return errors.New("remote cleanup journal has no targets")
		}
		return nil
	})
	return journal, err
}

// durationLedgerSQLiteExecutor 统一 journal 所需的连接与事务最小 SQL 能力。
type durationLedgerSQLiteExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// loadRemoteCleanupTargets 逐行校验独立 journal 的 provider 归属字段。
func loadRemoteCleanupTargets(rows *sql.Rows, jobID string) ([]RemoteCleanupTarget, error) {
	var targets []RemoteCleanupTarget
	for rows.Next() {
		var target RemoteCleanupTarget
		if err := rows.Scan(&target.Kind, &target.Key, &target.ProviderID, &target.DeterministicName, &target.JobTag); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote cleanup target", err)
		}
		if err := validateRemoteCleanupTarget(jobID, target); err != nil {
			return nil, fmt.Errorf("remote cleanup target shape: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote cleanup targets", err)
	}
	return targets, nil
}

// withRemoteCleanupJournalDatabase 复用唯一 SQLite authority 打开和严格 schema preflight。
func (store *DurationLedgerStore) withRemoteCleanupJournalDatabase(run func(durationLedgerSQLiteExecutor) error) error {
	if run == nil {
		return errors.New("remote cleanup journal operation is required")
	}
	if err := store.prepareSQLiteAuthorityPath(false); err != nil {
		return err
	}
	database, err := openSQLiteLedgerDatabase(store.path, store.nowFunc, store.schemaValidator)
	if err != nil {
		return err
	}
	defer database.Close()
	return run(database)
}

// ensureRemoteCleanupTarget 以 insert-once 后精确读回阻断同 key 意图漂移。
func ensureRemoteCleanupTarget(database durationLedgerSQLiteExecutor, jobID string, target RemoteCleanupTarget) error {
	if _, err := database.ExecContext(context.Background(), `INSERT INTO ci_remote_cleanup_targets(job_id, target_kind, target_key, deterministic_name, job_tag) VALUES (?, ?, ?, ?, ?) ON CONFLICT(job_id, target_kind, target_key) DO NOTHING`, jobID, target.Kind, target.Key, target.DeterministicName, target.JobTag); err != nil {
		return mapDurationLedgerSQLiteError("insert remote cleanup target", err)
	}
	var stored RemoteCleanupTarget
	if err := database.QueryRowContext(context.Background(), `SELECT target_kind, target_key, provider_id, deterministic_name, job_tag FROM ci_remote_cleanup_targets WHERE job_id = ? AND target_kind = ? AND target_key = ?`, jobID, target.Kind, target.Key).Scan(&stored.Kind, &stored.Key, &stored.ProviderID, &stored.DeterministicName, &stored.JobTag); err != nil {
		return mapDurationLedgerSQLiteError("read remote cleanup target", err)
	}
	if stored.Kind != target.Kind || stored.Key != target.Key || stored.DeterministicName != target.DeterministicName || stored.JobTag != target.JobTag {
		return errors.New("remote cleanup target intent drifted")
	}
	return nil
}

// validateRemoteCleanupJobAndPrefix 拒绝空 job 或越出 generation 子目录的 OSS 前缀。
func validateRemoteCleanupJobAndPrefix(jobID, prefix string) error {
	if strings.TrimSpace(jobID) == "" {
		return errors.New("remote cleanup job ID is required")
	}
	if strings.TrimSpace(prefix) == "" || !strings.HasSuffix(prefix, "/") {
		return errors.New("remote cleanup OSS prefix must be non-empty and end with slash")
	}
	return nil
}

// validateRemoteCleanupTarget 按资源类型校验恢复查询键和 provider 字段闭包。
func validateRemoteCleanupTarget(jobID string, target RemoteCleanupTarget) error {
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(target.Key) == "" {
		return errors.New("remote cleanup target job ID and key are required")
	}
	switch target.Kind {
	case RemoteCleanupTargetOSSPrefix:
		return validateRemoteCleanupOSSTarget(target)
	case RemoteCleanupTargetECIContainerGroup:
		return validateRemoteCleanupECITarget(jobID, target)
	default:
		return errors.New("remote cleanup target kind is unsupported")
	}
}

// validateRemoteCleanupOSSTarget 保证 OSS 目标只携带受限子前缀。
func validateRemoteCleanupOSSTarget(target RemoteCleanupTarget) error {
	if target.ProviderID != "" || target.DeterministicName != "" || target.JobTag != "" || !strings.HasSuffix(target.Key, "/") {
		return errors.New("remote cleanup OSS target drifted")
	}
	return nil
}

// validateRemoteCleanupECITarget 保证名称、journal key 与 exact job tag 同源。
func validateRemoteCleanupECITarget(jobID string, target RemoteCleanupTarget) error {
	if target.Key != target.DeterministicName || target.JobTag != jobID {
		return errors.New("remote cleanup ECI target drifted")
	}
	return nil
}
