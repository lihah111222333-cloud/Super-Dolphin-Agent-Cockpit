// Package terminaloutcome 持久化 canonical public terminal outcome 与事务 outbox。
package terminaloutcome

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store 使用同一个 SQLite 事务提交公开终态与 projector outbox。
type Store struct {
	db *sql.DB
}

// New 创建 terminal outcome store；nil DB 会在第一次调用时 fail-fast。
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// CommitTerminalOutcome 以 expected-state/terminal identity CAS 写入唯一公开终态和 outbox。
func (s *Store) CommitTerminalOutcome(ctx context.Context, commit contract.TerminalOutcomeCommit) (contract.TerminalOutcomeCommitResult, error) {
	if s == nil || s.db == nil {
		return contract.TerminalOutcomeCommitResult{}, errors.New("terminal outcome store database is required")
	}
	commit = normalizeTerminalCommitTime(commit)
	if err := commit.Validate(); err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	payload, err := json.Marshal(commit)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, fmt.Errorf("encode public terminal outcome: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, fmt.Errorf("begin terminal outcome commit: %w", err)
	}
	result, err := commitTerminalOutcomeTx(ctx, tx, commit, payload)
	if err != nil {
		_ = tx.Rollback()
		return contract.TerminalOutcomeCommitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.TerminalOutcomeCommitResult{}, fmt.Errorf("commit terminal outcome transaction: %w", err)
	}
	return result, nil
}

func normalizeTerminalCommitTime(commit contract.TerminalOutcomeCommit) contract.TerminalOutcomeCommit {
	occurredAt := time.UnixMilli(commit.OccurredAt.UTC().UnixMilli()).UTC()
	commit.OccurredAt = occurredAt
	commit.PublicOutcome.CompletedAt = occurredAt
	return commit
}

// commitTerminalOutcomeTx 在单事务内执行 replay/CAS、public record 和 outbox enqueue。
func commitTerminalOutcomeTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit, payload []byte) (contract.TerminalOutcomeCommitResult, error) {
	existing, outboxID, found, err := loadTerminalOutcomeTx(ctx, tx, commit.Identity.AgentID)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	if found {
		if sameCanonicalTerminal(existing, commit) {
			return contract.TerminalOutcomeCommitResult{Outcome: existing, OutboxID: outboxID, Replayed: true}, nil
		}
		return contract.TerminalOutcomeCommitResult{}, contract.ErrTerminalOutcomeConflict
	}
	if err := insertActiveHeadTx(ctx, tx, commit); err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	if err := insertPublicOutcomeTx(ctx, tx, commit); err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	outboxID, err = insertOutboxTx(ctx, tx, commit, payload)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	if err := sealTerminalHeadTx(ctx, tx, commit); err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	return contract.TerminalOutcomeCommitResult{Outcome: commit, OutboxID: outboxID}, nil
}

// loadTerminalOutcomeTx 从 public SSOT、identity head 和 outbox 重建严格 v2 commit。
func loadTerminalOutcomeTx(ctx context.Context, tx *sql.Tx, agentID string) (contract.TerminalOutcomeCommit, int64, bool, error) {
	var commit contract.TerminalOutcomeCommit
	var publicOutcome []byte
	var occurredAt int64
	var outboxID int64
	err := tx.QueryRowContext(ctx, `
		SELECT p.schema_version, p.projection_kind,
		       p.agent_id, h.capability, p.public_thread_id, p.provider_turn_id, p.session_id, p.generation,
		       p.event_id, p.terminal_identity, h.expected_active_state,
		       p.public_outcome_json, p.public_report, p.occurred_at, o.id
		FROM public_terminal_outcomes p
		JOIN terminal_outcome_heads h ON h.agent_id = p.agent_id AND h.terminal_identity = p.terminal_identity
		JOIN terminal_outcome_outbox o ON o.event_id = p.event_id
		WHERE p.agent_id = ?
	`, agentID).Scan(
		&commit.SchemaVersion, &commit.ProjectionKind,
		&commit.Identity.AgentID, &commit.Identity.Capability,
		&commit.Identity.PublicThreadID, &commit.Identity.ProviderTurnID,
		&commit.Identity.SessionID, &commit.Identity.Generation, &commit.Identity.EventID,
		&commit.Identity.TerminalIdentity, &commit.Identity.ExpectedActiveState,
		&publicOutcome, &commit.PublicReport, &occurredAt, &outboxID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.TerminalOutcomeCommit{}, 0, false, nil
	}
	if err != nil {
		return contract.TerminalOutcomeCommit{}, 0, false, fmt.Errorf("load canonical terminal outcome: %w", err)
	}
	commit.OccurredAt = time.UnixMilli(occurredAt).UTC()
	if err := json.Unmarshal(publicOutcome, &commit.PublicOutcome); err != nil {
		return contract.TerminalOutcomeCommit{}, 0, false, fmt.Errorf("decode canonical public terminal outcome: %w", err)
	}
	if err := commit.Validate(); err != nil {
		return contract.TerminalOutcomeCommit{}, 0, false, fmt.Errorf("validate canonical public terminal outcome: %w", err)
	}
	return commit, outboxID, true, nil
}

func sameCanonicalTerminal(existing, incoming contract.TerminalOutcomeCommit) bool {
	return reflect.DeepEqual(existing, incoming)
}

func insertActiveHeadTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit) error {
	identity := commit.Identity
	_, err := tx.ExecContext(ctx, `
		INSERT INTO terminal_outcome_heads (
			agent_id, capability, public_thread_id, provider_turn_id, session_id, generation,
			event_id, terminal_identity, expected_active_state, state, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)
	`, identity.AgentID, identity.Capability, identity.PublicThreadID, identity.ProviderTurnID, identity.SessionID,
		identity.Generation, identity.EventID, identity.TerminalIdentity,
		identity.ExpectedActiveState, commit.OccurredAt.UTC().UnixMilli())
	if err != nil {
		return mapTerminalConflict("insert terminal outcome head", err)
	}
	return nil
}

func insertPublicOutcomeTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit) error {
	identity := commit.Identity
	publicOutcome, err := json.Marshal(commit.PublicOutcome)
	if err != nil {
		return fmt.Errorf("encode durable public outcome: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO public_terminal_outcomes (
			agent_id, schema_version, projection_kind,
			public_thread_id, provider_turn_id, session_id, generation,
			event_id, terminal_identity, public_outcome_json, public_report, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, identity.AgentID, commit.SchemaVersion, commit.ProjectionKind,
		identity.PublicThreadID, identity.ProviderTurnID, identity.SessionID,
		identity.Generation, identity.EventID, identity.TerminalIdentity,
		string(publicOutcome), commit.PublicReport, commit.OccurredAt.UTC().UnixMilli())
	if err != nil {
		return mapTerminalConflict("insert public terminal outcome", err)
	}
	return nil
}

func insertOutboxTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit, payload []byte) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO terminal_outcome_outbox (
			event_id, payload_json, status, created_at
		) VALUES (?, ?, 'pending', ?)
	`, commit.Identity.EventID, payload, commit.OccurredAt.UTC().UnixMilli())
	if err != nil {
		return 0, mapTerminalConflict("enqueue terminal outcome outbox", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read terminal outcome outbox id: %w", err)
	}
	return id, nil
}

func sealTerminalHeadTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit) error {
	identity := commit.Identity
	result, err := tx.ExecContext(ctx, `
		UPDATE terminal_outcome_heads
		SET state = 'terminal', updated_at = ?
		WHERE agent_id = ? AND public_thread_id = ? AND provider_turn_id = ?
		  AND session_id = ? AND generation = ? AND event_id = ?
		  AND terminal_identity = ? AND expected_active_state = ? AND state = 'active'
	`, commit.OccurredAt.UTC().UnixMilli(), identity.AgentID, identity.PublicThreadID,
		identity.ProviderTurnID, identity.SessionID, identity.Generation, identity.EventID,
		identity.TerminalIdentity, identity.ExpectedActiveState)
	if err != nil {
		return fmt.Errorf("seal terminal outcome head: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read terminal outcome CAS result: %w", err)
	}
	if rows != 1 {
		return contract.ErrTerminalOutcomeConflict
	}
	return nil
}

func mapTerminalConflict(action string, err error) error {
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_CONSTRAINT {
		return fmt.Errorf("%s: %w", action, contract.ErrTerminalOutcomeConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}

// GetPublicTerminalOutcome 只读取 public outcome SSOT，不读取 runtime report 或 provider 原文。
func (s *Store) GetPublicTerminalOutcome(ctx context.Context, agentID string) (contract.TerminalOutcomeCommit, error) {
	if s == nil || s.db == nil {
		return contract.TerminalOutcomeCommit{}, errors.New("terminal outcome store database is required")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return contract.TerminalOutcomeCommit{}, errors.New("terminal outcome agent id is required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return contract.TerminalOutcomeCommit{}, fmt.Errorf("begin public terminal outcome read: %w", err)
	}
	commit, _, found, err := loadTerminalOutcomeTx(ctx, tx, agentID)
	if rollbackErr := tx.Rollback(); err == nil && rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		return contract.TerminalOutcomeCommit{}, rollbackErr
	}
	if err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	if !found {
		return contract.TerminalOutcomeCommit{}, sql.ErrNoRows
	}
	return commit, nil
}

// ClaimTerminalOutcomeOutbox 领取 pending 或 lease 已过期的安全公开投影记录。
func (s *Store) ClaimTerminalOutcomeOutbox(ctx context.Context, workerID string, lease time.Duration, limit int) ([]contract.TerminalOutcomeOutboxItem, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("terminal outcome store database is required")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || lease <= 0 || limit <= 0 {
		return nil, errors.New("terminal outcome outbox worker id, positive lease and positive limit are required")
	}
	now := time.Now().UTC()
	cutoff := now.Add(-lease).UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin terminal outbox claim: %w", err)
	}
	items, err := claimOutboxTx(ctx, tx, workerID, now.UnixMilli(), cutoff, limit)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit terminal outbox claim: %w", err)
	}
	return items, nil
}

// claimOutboxTx 在事务中领取 pending 或 lease 过期的 public 投影任务。
func claimOutboxTx(ctx context.Context, tx *sql.Tx, workerID string, now, cutoff int64, limit int) ([]contract.TerminalOutcomeOutboxItem, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, payload_json
		FROM terminal_outcome_outbox
		WHERE status = 'pending' OR (status = 'claimed' AND claimed_at <= ?)
		ORDER BY id
		LIMIT ?
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list terminal outcome outbox: %w", err)
	}
	defer rows.Close()
	var items []contract.TerminalOutcomeOutboxItem
	for rows.Next() {
		var item contract.TerminalOutcomeOutboxItem
		var payload []byte
		if err := rows.Scan(&item.ID, &payload); err != nil {
			return nil, fmt.Errorf("scan terminal outcome outbox: %w", err)
		}
		if err := json.Unmarshal(payload, &item.Outcome); err != nil {
			return nil, fmt.Errorf("decode terminal outcome outbox %d: %w", item.ID, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal outcome outbox: %w", err)
	}
	for _, item := range items {
		result, err := tx.ExecContext(ctx, `
			UPDATE terminal_outcome_outbox
			SET status = 'claimed', claimed_by = ?, claimed_at = ?
			WHERE id = ? AND (status = 'pending' OR (status = 'claimed' AND claimed_at <= ?))
		`, workerID, now, item.ID, cutoff)
		if err != nil {
			return nil, fmt.Errorf("claim terminal outcome outbox %d: %w", item.ID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return nil, contract.ErrTerminalOutboxFence
		}
	}
	return items, nil
}

// MarkTerminalOutcomeProjected 只允许当前 claim owner 幂等完成投影。
func (s *Store) MarkTerminalOutcomeProjected(ctx context.Context, outboxID int64, workerID string) error {
	if s == nil || s.db == nil {
		return errors.New("terminal outcome store database is required")
	}
	workerID = strings.TrimSpace(workerID)
	if outboxID <= 0 || workerID == "" {
		return errors.New("terminal outcome outbox id and worker id are required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE terminal_outcome_outbox
		SET status = 'projected', projected_at = ?
		WHERE id = ? AND status = 'claimed' AND claimed_by = ?
	`, time.Now().UTC().UnixMilli(), outboxID, workerID)
	if err != nil {
		return fmt.Errorf("mark terminal outcome projected: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read terminal outcome projected rows: %w", err)
	}
	if affected != 1 {
		return contract.ErrTerminalOutboxFence
	}
	return nil
}

var _ contract.TerminalOutcomeCommitPort = (*Store)(nil)
