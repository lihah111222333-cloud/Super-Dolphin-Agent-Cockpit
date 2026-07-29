// Package terminaloutcome 持久化 canonical public terminal outcome、私有 DAG 载荷与事务 outbox。
package terminaloutcome

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store 使用同一个 SQLite 事务提交 current-head CAS、公开历史、私有 DAG 载荷与 outbox。
type Store struct {
	db *sql.DB
}

// New 创建 terminal outcome store；nil DB 会在第一次调用时 fail-fast。
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// ActivateTerminalOutcomeHead 在 provider turn 身份确定后建立或推进真实 current head。
func (s *Store) ActivateTerminalOutcomeHead(ctx context.Context, activation contract.TerminalOutcomeHeadActivation) (contract.TerminalOutcomeHead, error) {
	if s == nil || s.db == nil {
		return contract.TerminalOutcomeHead{}, errors.New("terminal outcome store database is required")
	}
	activation.ActivatedAt = normalizeTerminalTime(activation.ActivatedAt)
	if err := activation.Validate(); err != nil {
		return contract.TerminalOutcomeHead{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.TerminalOutcomeHead{}, fmt.Errorf("begin terminal head activation: %w", err)
	}
	head, err := activateTerminalHeadTx(ctx, tx, activation)
	if err != nil {
		_ = tx.Rollback()
		return contract.TerminalOutcomeHead{}, err
	}
	if err := tx.Commit(); err != nil {
		return contract.TerminalOutcomeHead{}, fmt.Errorf("commit terminal head activation: %w", err)
	}
	return head, nil
}

type storedCurrentHead struct {
	contract.TerminalOutcomeHeadActivation
	Version          uint64
	State            string
	TerminalEventID  string
	TerminalIdentity string
}

func activateTerminalHeadTx(ctx context.Context, tx *sql.Tx, activation contract.TerminalOutcomeHeadActivation) (contract.TerminalOutcomeHead, error) {
	current, found, err := loadCurrentHeadTx(ctx, tx, activation.AgentID)
	if err != nil {
		return contract.TerminalOutcomeHead{}, err
	}
	if !found {
		if activation.ExpectedHeadVersion != 0 {
			return contract.TerminalOutcomeHead{}, contract.ErrTerminalOutcomeConflict
		}
		if err := insertCurrentHeadTx(ctx, tx, activation); err != nil {
			return contract.TerminalOutcomeHead{}, err
		}
		return contract.TerminalOutcomeHead{TerminalOutcomeHeadActivation: activation, Version: 1}, nil
	}
	if activation.ExpectedHeadVersion != current.Version {
		return contract.TerminalOutcomeHead{}, contract.ErrTerminalOutcomeConflict
	}
	if current.State == "active" {
		if sameActiveHead(current, activation) {
			return contract.TerminalOutcomeHead{TerminalOutcomeHeadActivation: activation, Version: current.Version}, nil
		}
		if activeSessionHeadCanAdvanceToTurn(current, activation) {
			return advanceActiveSessionHeadTx(ctx, tx, current, activation)
		}
		return contract.TerminalOutcomeHead{}, contract.ErrTerminalOutcomeConflict
	}
	return advanceTerminalHeadTx(ctx, tx, current, activation)
}

func advanceTerminalHeadTx(ctx context.Context, tx *sql.Tx, current storedCurrentHead, activation contract.TerminalOutcomeHeadActivation) (contract.TerminalOutcomeHead, error) {
	if activation.Generation < current.Generation {
		return contract.TerminalOutcomeHead{}, contract.ErrTerminalOutcomeConflict
	}
	if activation.Generation == current.Generation && !sameGenerationHeadOwner(current, activation) {
		return contract.TerminalOutcomeHead{}, contract.ErrTerminalOutcomeConflict
	}
	if terminalHeadReopensSameTurn(current, activation) {
		return contract.TerminalOutcomeHead{}, contract.ErrTerminalOutcomeConflict
	}
	nextVersion := current.Version + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE terminal_outcome_current_heads
		SET capability = ?, public_thread_id = ?, provider_turn_id = ?, session_id = ?,
		    generation = ?, expected_active_state = ?, version = ?, state = 'active',
		    terminal_event_id = '', terminal_identity = '', activated_at = ?, updated_at = ?
		WHERE agent_id = ? AND version = ? AND state = 'terminal'
	`, activation.Capability, activation.PublicThreadID, activation.ProviderTurnID,
		activation.SessionID, activation.Generation, activation.ExpectedActiveState, nextVersion,
		activation.ActivatedAt.UnixMilli(), activation.ActivatedAt.UnixMilli(),
		activation.AgentID, current.Version)
	if err != nil {
		return contract.TerminalOutcomeHead{}, fmt.Errorf("advance terminal current head: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return contract.TerminalOutcomeHead{}, err
	}
	return contract.TerminalOutcomeHead{TerminalOutcomeHeadActivation: activation, Version: nextVersion}, nil
}

func sameGenerationHeadOwner(current storedCurrentHead, activation contract.TerminalOutcomeHeadActivation) bool {
	return activation.Capability == current.Capability &&
		activation.AgentID == current.AgentID &&
		activation.PublicThreadID == current.PublicThreadID &&
		activation.SessionID == current.SessionID &&
		activation.Generation == current.Generation
}

func terminalHeadReopensSameTurn(current storedCurrentHead, activation contract.TerminalOutcomeHeadActivation) bool {
	return activation.Generation == current.Generation &&
		activation.ProviderTurnID == current.ProviderTurnID
}

func activeSessionHeadCanAdvanceToTurn(current storedCurrentHead, activation contract.TerminalOutcomeHeadActivation) bool {
	return current.Capability == activation.Capability &&
		current.AgentID == activation.AgentID &&
		current.PublicThreadID == activation.PublicThreadID &&
		current.SessionID == activation.SessionID &&
		current.Generation == activation.Generation &&
		current.ProviderTurnID == "session-terminal:"+current.SessionID &&
		activation.ProviderTurnID != current.ProviderTurnID
}

func advanceActiveSessionHeadTx(ctx context.Context, tx *sql.Tx, current storedCurrentHead, activation contract.TerminalOutcomeHeadActivation) (contract.TerminalOutcomeHead, error) {
	nextVersion := current.Version + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE terminal_outcome_current_heads
		SET provider_turn_id = ?, expected_active_state = ?, version = ?, activated_at = ?, updated_at = ?
		WHERE agent_id = ? AND version = ? AND state = 'active'
		  AND provider_turn_id = ? AND session_id = ? AND generation = ?
	`, activation.ProviderTurnID, activation.ExpectedActiveState, nextVersion,
		activation.ActivatedAt.UnixMilli(), activation.ActivatedAt.UnixMilli(),
		activation.AgentID, current.Version, current.ProviderTurnID, current.SessionID, current.Generation)
	if err != nil {
		return contract.TerminalOutcomeHead{}, fmt.Errorf("advance active session head to turn: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return contract.TerminalOutcomeHead{}, err
	}
	return contract.TerminalOutcomeHead{TerminalOutcomeHeadActivation: activation, Version: nextVersion}, nil
}

func loadCurrentHeadTx(ctx context.Context, tx *sql.Tx, agentID string) (storedCurrentHead, bool, error) {
	var head storedCurrentHead
	var generation, version int64
	var activatedAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT capability, agent_id, public_thread_id, provider_turn_id, session_id, generation,
		       expected_active_state, version, state, terminal_event_id, terminal_identity, activated_at
		FROM terminal_outcome_current_heads
		WHERE agent_id = ?
	`, agentID).Scan(
		&head.Capability, &head.AgentID, &head.PublicThreadID, &head.ProviderTurnID,
		&head.SessionID, &generation, &head.ExpectedActiveState, &version, &head.State,
		&head.TerminalEventID, &head.TerminalIdentity, &activatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return storedCurrentHead{}, false, nil
	}
	if err != nil {
		return storedCurrentHead{}, false, fmt.Errorf("load terminal current head: %w", err)
	}
	if generation <= 0 || version <= 0 {
		return storedCurrentHead{}, false, errors.New("terminal current head contains invalid generation or version")
	}
	head.Generation = uint64(generation)
	head.Version = uint64(version)
	head.ActivatedAt = time.UnixMilli(activatedAt).UTC()
	return head, true, nil
}

func insertCurrentHeadTx(ctx context.Context, tx *sql.Tx, activation contract.TerminalOutcomeHeadActivation) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO terminal_outcome_current_heads (
			agent_id, capability, public_thread_id, provider_turn_id, session_id, generation,
			expected_active_state, version, state, terminal_event_id, terminal_identity,
			activated_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 'active', '', '', ?, ?)
	`, activation.AgentID, activation.Capability, activation.PublicThreadID,
		activation.ProviderTurnID, activation.SessionID, activation.Generation,
		activation.ExpectedActiveState, activation.ActivatedAt.UnixMilli(), activation.ActivatedAt.UnixMilli())
	if err != nil {
		return mapTerminalConflict("insert terminal current head", err)
	}
	return nil
}

func sameActiveHead(current storedCurrentHead, activation contract.TerminalOutcomeHeadActivation) bool {
	return current.Capability == activation.Capability &&
		current.AgentID == activation.AgentID &&
		current.PublicThreadID == activation.PublicThreadID &&
		current.ProviderTurnID == activation.ProviderTurnID &&
		current.SessionID == activation.SessionID &&
		current.Generation == activation.Generation &&
		current.ExpectedActiveState == activation.ExpectedActiveState
}

// CommitTerminalOutcome 只对真实 active current head 做全身份/version CAS，再写公开历史与 outbox。
func (s *Store) CommitTerminalOutcome(ctx context.Context, commit contract.TerminalOutcomeCommit) (contract.TerminalOutcomeCommitResult, error) {
	if s == nil || s.db == nil {
		return contract.TerminalOutcomeCommitResult{}, errors.New("terminal outcome store database is required")
	}
	commit = normalizeTerminalCommitTime(commit)
	if err := commit.Validate(); err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	publicPayload, err := json.Marshal(commit)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, fmt.Errorf("encode public terminal outcome: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, fmt.Errorf("begin terminal outcome commit: %w", err)
	}
	result, err := commitTerminalOutcomeTx(ctx, tx, commit, publicPayload)
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
	occurredAt := normalizeTerminalTime(commit.OccurredAt)
	commit.OccurredAt = occurredAt
	commit.PublicOutcome.CompletedAt = occurredAt
	return commit
}

func normalizeTerminalTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return time.UnixMilli(value.UTC().UnixMilli()).UTC()
}

func commitTerminalOutcomeTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit, publicPayload []byte) (contract.TerminalOutcomeCommitResult, error) {
	existing, outboxID, found, err := loadHistoryByIdentityTx(ctx, tx, commit.Identity.TerminalIdentity, commit.Identity.EventID)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	if found {
		privateMatches, privateErr := storedPrivateDAGMatchesTx(ctx, tx, commit)
		if privateErr != nil {
			return contract.TerminalOutcomeCommitResult{}, privateErr
		}
		if sameCanonicalTerminal(existing, commit) && privateMatches {
			return contract.TerminalOutcomeCommitResult{Outcome: existing, OutboxID: outboxID, Replayed: true}, nil
		}
		return contract.TerminalOutcomeCommitResult{}, contract.ErrTerminalOutcomeConflict
	}
	if err := sealCurrentHeadTx(ctx, tx, commit); err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	if err := insertPublicHistoryTx(ctx, tx, commit); err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	privateID, err := insertPrivateDAGPayloadTx(ctx, tx, commit)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	outboxID, err = insertOutboxTx(ctx, tx, commit, publicPayload, privateID)
	if err != nil {
		return contract.TerminalOutcomeCommitResult{}, err
	}
	return contract.TerminalOutcomeCommitResult{Outcome: publicCommit(commit), OutboxID: outboxID}, nil
}

func storedPrivateDAGMatchesTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit) (bool, error) {
	var payload []byte
	err := tx.QueryRowContext(ctx, `
		SELECT payload_json FROM terminal_outcome_private_dag_payloads WHERE terminal_identity = ?
	`, commit.Identity.TerminalIdentity).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return commit.PrivateDAG == nil, nil
	}
	if err != nil {
		return false, fmt.Errorf("load owner-scoped DAG replay payload: %w", err)
	}
	if commit.PrivateDAG == nil {
		return false, nil
	}
	var existing contract.OwnerScopedDAGPayload
	if err := strictJSONDecode(payload, &existing); err != nil {
		return false, fmt.Errorf("decode owner-scoped DAG replay payload: %w", err)
	}
	return reflect.DeepEqual(existing, *commit.PrivateDAG), nil
}

func sealCurrentHeadTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit) error {
	identity := commit.Identity
	result, err := tx.ExecContext(ctx, `
		UPDATE terminal_outcome_current_heads
		SET state = 'terminal', terminal_event_id = ?, terminal_identity = ?, updated_at = ?
		WHERE agent_id = ? AND capability = ? AND public_thread_id = ? AND provider_turn_id = ?
		  AND session_id = ? AND generation = ? AND expected_active_state = ? AND version = ?
		  AND state = 'active' AND terminal_event_id = '' AND terminal_identity = ''
	`, identity.EventID, identity.TerminalIdentity, commit.OccurredAt.UnixMilli(),
		identity.AgentID, identity.Capability, identity.PublicThreadID, identity.ProviderTurnID,
		identity.SessionID, identity.Generation, identity.ExpectedActiveState, identity.HeadVersion)
	if err != nil {
		return fmt.Errorf("seal terminal current head: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return contract.ErrTerminalOutcomeConflict
	}
	return nil
}

func requireOneRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read terminal CAS rows: %w", err)
	}
	if rows != 1 {
		return contract.ErrTerminalOutcomeConflict
	}
	return nil
}

func insertPublicHistoryTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit) error {
	identity := commit.Identity
	publicOutcome, err := json.Marshal(commit.PublicOutcome)
	if err != nil {
		return fmt.Errorf("encode durable public outcome: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO public_terminal_outcome_history (
			terminal_identity, event_id, agent_id, head_version, schema_version, projection_kind,
			public_thread_id, provider_turn_id, session_id, generation, expected_active_state,
			public_outcome_json, public_report, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, identity.TerminalIdentity, identity.EventID, identity.AgentID, identity.HeadVersion,
		commit.SchemaVersion, commit.ProjectionKind, identity.PublicThreadID, identity.ProviderTurnID,
		identity.SessionID, identity.Generation, identity.ExpectedActiveState,
		string(publicOutcome), commit.PublicReport, commit.OccurredAt.UnixMilli())
	if err != nil {
		return mapTerminalConflict("insert public terminal history", err)
	}
	return nil
}

func insertPrivateDAGPayloadTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit) (sql.NullInt64, error) {
	if commit.PrivateDAG == nil {
		return sql.NullInt64{}, nil
	}
	payload, err := json.Marshal(commit.PrivateDAG)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("encode owner-scoped DAG payload: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO terminal_outcome_private_dag_payloads (
			terminal_identity, owner_agent_id, public_thread_id, provider_turn_id, payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, commit.Identity.TerminalIdentity, commit.PrivateDAG.OwnerAgentID,
		commit.PrivateDAG.PublicThreadID, commit.PrivateDAG.ProviderTurnID,
		string(payload), commit.OccurredAt.UnixMilli())
	if err != nil {
		return sql.NullInt64{}, mapTerminalConflict("insert owner-scoped DAG payload", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("read owner-scoped DAG payload id: %w", err)
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

func insertOutboxTx(ctx context.Context, tx *sql.Tx, commit contract.TerminalOutcomeCommit, payload []byte, privateID sql.NullInt64) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO terminal_outcome_outbox_v2 (
			terminal_identity, event_id, public_payload_json, private_dag_payload_id, status, created_at
		) VALUES (?, ?, ?, ?, 'pending', ?)
	`, commit.Identity.TerminalIdentity, commit.Identity.EventID, payload, privateID, commit.OccurredAt.UnixMilli())
	if err != nil {
		return 0, mapTerminalConflict("enqueue terminal outcome outbox", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read terminal outcome outbox id: %w", err)
	}
	return id, nil
}

func publicCommit(commit contract.TerminalOutcomeCommit) contract.TerminalOutcomeCommit {
	commit.PrivateDAG = nil
	return commit
}

func sameCanonicalTerminal(existing, incoming contract.TerminalOutcomeCommit) bool {
	return reflect.DeepEqual(existing, publicCommit(incoming))
}

// GetPublicTerminalOutcome 读取 current head 指向的公开终态；active head 显式返回 ErrTerminalOutcomeActive。
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
	commit, err := getPublicTerminalOutcomeTx(ctx, tx, agentID)
	rollbackErr := tx.Rollback()
	if err == nil && rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		return contract.TerminalOutcomeCommit{}, rollbackErr
	}
	if err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	return commit, nil
}

func getPublicTerminalOutcomeTx(ctx context.Context, tx *sql.Tx, agentID string) (contract.TerminalOutcomeCommit, error) {
	head, found, err := loadCurrentHeadTx(ctx, tx, agentID)
	if err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	if !found {
		return contract.TerminalOutcomeCommit{}, sql.ErrNoRows
	}
	if head.State == "active" {
		return contract.TerminalOutcomeCommit{}, contract.ErrTerminalOutcomeActive
	}
	commit, _, found, err := loadHistoryByIdentityTx(ctx, tx, head.TerminalIdentity, head.TerminalEventID)
	if err != nil {
		return contract.TerminalOutcomeCommit{}, err
	}
	if !found {
		return contract.TerminalOutcomeCommit{}, errors.New("terminal current head references missing public history")
	}
	return commit, nil
}

func loadHistoryByIdentityTx(ctx context.Context, tx *sql.Tx, terminalIdentity, eventID string) (contract.TerminalOutcomeCommit, int64, bool, error) {
	var commit contract.TerminalOutcomeCommit
	var publicOutcome []byte
	var occurredAt int64
	var generation, headVersion int64
	var outboxID int64
	err := tx.QueryRowContext(ctx, `
		SELECT p.schema_version, p.projection_kind, p.agent_id, h.capability,
		       p.public_thread_id, p.provider_turn_id, p.session_id, p.generation,
		       p.event_id, p.terminal_identity, p.expected_active_state, p.head_version,
		       p.public_outcome_json, p.public_report, p.occurred_at, o.id
		FROM public_terminal_outcome_history p
		JOIN terminal_outcome_current_heads h ON h.agent_id = p.agent_id
		JOIN terminal_outcome_outbox_v2 o ON o.terminal_identity = p.terminal_identity
		WHERE p.terminal_identity = ? OR p.event_id = ?
	`, terminalIdentity, eventID).Scan(
		&commit.SchemaVersion, &commit.ProjectionKind, &commit.Identity.AgentID,
		&commit.Identity.Capability, &commit.Identity.PublicThreadID,
		&commit.Identity.ProviderTurnID, &commit.Identity.SessionID, &generation,
		&commit.Identity.EventID, &commit.Identity.TerminalIdentity,
		&commit.Identity.ExpectedActiveState, &headVersion, &publicOutcome,
		&commit.PublicReport, &occurredAt, &outboxID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.TerminalOutcomeCommit{}, 0, false, nil
	}
	if err != nil {
		return contract.TerminalOutcomeCommit{}, 0, false, fmt.Errorf("load canonical terminal history: %w", err)
	}
	if generation <= 0 || headVersion <= 0 {
		return contract.TerminalOutcomeCommit{}, 0, false, errors.New("terminal history contains invalid generation or head version")
	}
	commit.Identity.Generation = uint64(generation)
	commit.Identity.HeadVersion = uint64(headVersion)
	commit.OccurredAt = time.UnixMilli(occurredAt).UTC()
	if err := json.Unmarshal(publicOutcome, &commit.PublicOutcome); err != nil {
		return contract.TerminalOutcomeCommit{}, 0, false, fmt.Errorf("decode canonical public terminal outcome: %w", err)
	}
	if err := commit.Validate(); err != nil {
		return contract.TerminalOutcomeCommit{}, 0, false, fmt.Errorf("validate canonical public terminal outcome: %w", err)
	}
	return commit, outboxID, true, nil
}

type outboxCandidate struct {
	id             int64
	publicPayload  []byte
	privatePayload []byte
}

// ClaimTerminalOutcomeOutbox 为每次 claim 生成唯一 token 和绝对 lease expiry。
func (s *Store) ClaimTerminalOutcomeOutbox(ctx context.Context, workerID string, lease time.Duration, limit int) ([]contract.TerminalOutcomeOutboxItem, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("terminal outcome store database is required")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || lease <= 0 || limit <= 0 {
		return nil, errors.New("terminal outcome outbox worker id, positive lease and positive limit are required")
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin terminal outbox claim: %w", err)
	}
	items, err := claimOutboxTx(ctx, tx, workerID, now, lease, limit)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit terminal outbox claim: %w", err)
	}
	return items, nil
}

func claimOutboxTx(ctx context.Context, tx *sql.Tx, workerID string, now time.Time, lease time.Duration, limit int) ([]contract.TerminalOutcomeOutboxItem, error) {
	candidates, err := listOutboxCandidatesTx(ctx, tx, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	items := make([]contract.TerminalOutcomeOutboxItem, 0, len(candidates))
	for _, candidate := range candidates {
		item, err := decodeOutboxCandidate(candidate)
		if err != nil {
			if poisonErr := poisonOutboxTx(ctx, tx, candidate.id, err); poisonErr != nil {
				return nil, poisonErr
			}
			continue
		}
		token, err := newClaimToken()
		if err != nil {
			return nil, err
		}
		expiresAt := now.Add(lease)
		result, err := tx.ExecContext(ctx, `
			UPDATE terminal_outcome_outbox_v2
			SET status = 'claimed', claimed_by = ?, claim_token = ?, lease_expires_at = ?,
			    attempt_count = attempt_count + 1, last_error = ''
			WHERE id = ? AND (status = 'pending' OR (status = 'claimed' AND lease_expires_at <= ?))
		`, workerID, token, expiresAt.UnixMilli(), candidate.id, now.UnixMilli())
		if err != nil {
			return nil, fmt.Errorf("claim terminal outcome outbox %d: %w", candidate.id, err)
		}
		if err := requireOneRow(result); err != nil {
			return nil, contract.ErrTerminalOutboxFence
		}
		item.ClaimToken = token
		item.LeaseExpiresAt = expiresAt
		items = append(items, item)
	}
	return items, nil
}

func listOutboxCandidatesTx(ctx context.Context, tx *sql.Tx, now int64, limit int) ([]outboxCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT o.id, o.public_payload_json, COALESCE(d.payload_json, '')
		FROM terminal_outcome_outbox_v2 o
		LEFT JOIN terminal_outcome_private_dag_payloads d ON d.id = o.private_dag_payload_id
		WHERE o.status = 'pending' OR (o.status = 'claimed' AND o.lease_expires_at <= ?)
		ORDER BY o.id
		LIMIT ?
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list terminal outcome outbox: %w", err)
	}
	defer rows.Close()
	var candidates []outboxCandidate
	for rows.Next() {
		var candidate outboxCandidate
		if err := rows.Scan(&candidate.id, &candidate.publicPayload, &candidate.privatePayload); err != nil {
			return nil, fmt.Errorf("scan terminal outcome outbox: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal outcome outbox: %w", err)
	}
	return candidates, nil
}

func decodeOutboxCandidate(candidate outboxCandidate) (contract.TerminalOutcomeOutboxItem, error) {
	item := contract.TerminalOutcomeOutboxItem{ID: candidate.id}
	if err := json.Unmarshal(candidate.publicPayload, &item.Outcome); err != nil {
		return contract.TerminalOutcomeOutboxItem{}, fmt.Errorf("decode public payload: %w", err)
	}
	if len(candidate.privatePayload) != 0 {
		var payload contract.OwnerScopedDAGPayload
		if err := strictJSONDecode(candidate.privatePayload, &payload); err != nil {
			return contract.TerminalOutcomeOutboxItem{}, fmt.Errorf("decode private DAG payload: %w", err)
		}
		if err := payload.Validate(item.Outcome.Identity); err != nil {
			return contract.TerminalOutcomeOutboxItem{}, err
		}
		item.PrivateDAG = &payload
	}
	return item, nil
}

func strictJSONDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains multiple values")
		}
		return err
	}
	return nil
}

func poisonOutboxTx(ctx context.Context, tx *sql.Tx, id int64, _ error) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE terminal_outcome_outbox_v2
		SET status = 'poisoned', claimed_by = '', claim_token = '', lease_expires_at = NULL,
		    attempt_count = attempt_count + 1, last_error = ?
		WHERE id = ? AND status IN ('pending', 'claimed')
	`, "terminal outbox payload failed strict validation", id)
	if err != nil {
		return fmt.Errorf("quarantine terminal outcome outbox %d: %w", id, err)
	}
	if err := requireOneRow(result); err != nil {
		return contract.ErrTerminalOutboxFence
	}
	return nil
}

func newClaimToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate terminal outbox claim token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// RenewTerminalOutcomeOutbox 仅允许未过期的当前 claim token 延长租约。
func (s *Store) RenewTerminalOutcomeOutbox(ctx context.Context, outboxID int64, workerID, claimToken string, lease time.Duration) (time.Time, error) {
	if s == nil || s.db == nil {
		return time.Time{}, errors.New("terminal outcome store database is required")
	}
	workerID, claimToken = strings.TrimSpace(workerID), strings.TrimSpace(claimToken)
	if outboxID <= 0 || workerID == "" || claimToken == "" || lease <= 0 {
		return time.Time{}, errors.New("terminal outcome renewal requires id, worker, token and positive lease")
	}
	now := time.Now().UTC()
	expiresAt := now.Add(lease)
	result, err := s.db.ExecContext(ctx, `
		UPDATE terminal_outcome_outbox_v2
		SET lease_expires_at = ?
		WHERE id = ? AND status = 'claimed' AND claimed_by = ? AND claim_token = ?
		  AND lease_expires_at > ?
	`, expiresAt.UnixMilli(), outboxID, workerID, claimToken, now.UnixMilli())
	if err != nil {
		return time.Time{}, fmt.Errorf("renew terminal outcome outbox: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return time.Time{}, contract.ErrTerminalOutboxFence
	}
	return expiresAt, nil
}

// MarkTerminalOutcomeProjected 仅允许未过期的当前 claim token 完成 ACK。
func (s *Store) MarkTerminalOutcomeProjected(ctx context.Context, outboxID int64, workerID, claimToken string) error {
	if s == nil || s.db == nil {
		return errors.New("terminal outcome store database is required")
	}
	workerID, claimToken = strings.TrimSpace(workerID), strings.TrimSpace(claimToken)
	if outboxID <= 0 || workerID == "" || claimToken == "" {
		return errors.New("terminal outcome outbox id, worker id and claim token are required")
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE terminal_outcome_outbox_v2
		SET status = 'projected', claimed_by = '', claim_token = '', lease_expires_at = NULL,
		    projected_at = ?
		WHERE id = ? AND status = 'claimed' AND claimed_by = ? AND claim_token = ?
		  AND lease_expires_at > ?
	`, now.UnixMilli(), outboxID, workerID, claimToken, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("mark terminal outcome projected: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return contract.ErrTerminalOutboxFence
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

var _ contract.TerminalOutcomeCommitPort = (*Store)(nil)
