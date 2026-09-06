package terminaloutcome

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// errTerminalOutboxIntegrity 表示 durable outbox 行、公开载荷和其引用的历史/私有载荷不一致。
// 该错误只供 store 内部在 quarantine 后向调用方保留稳定的 typed 根因；不扩大公共 contract surface。
var errTerminalOutboxIntegrity = errors.New("terminal outcome outbox integrity mismatch")

func terminalOutboxIntegrityf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errTerminalOutboxIntegrity, fmt.Sprintf(format, args...))
}

// currentHeadMatchesTerminalCommit 比较 current head 与公开历史的完整身份/版本闭包。
func currentHeadMatchesTerminalCommit(head storedCurrentHead, commit contract.TerminalOutcomeCommit) bool {
	identity := commit.Identity
	return head.State == "terminal" && head.Version == identity.HeadVersion &&
		currentHeadOwnerMatches(head, identity) && currentHeadTerminalIdentityMatches(head, identity)
}

// currentHeadOwnerMatches 比较 current head 的 capability 与 agent/session/generation 归属。
func currentHeadOwnerMatches(head storedCurrentHead, identity contract.CanonicalTerminalIdentity) bool {
	return head.Capability == identity.Capability && head.AgentID == identity.AgentID &&
		head.PublicThreadID == identity.PublicThreadID && head.ProviderTurnID == identity.ProviderTurnID &&
		head.SessionID == identity.SessionID && head.Generation == identity.Generation &&
		head.ExpectedActiveState == identity.ExpectedActiveState
}

// currentHeadTerminalIdentityMatches 比较 current head 的 terminal event 与 identity 键。
func currentHeadTerminalIdentityMatches(head storedCurrentHead, identity contract.CanonicalTerminalIdentity) bool {
	return head.TerminalEventID == identity.EventID && head.TerminalIdentity == identity.TerminalIdentity
}

// validateTerminalOutboxRenewal 校验续租输入，避免 Renew 主流程混入参数分支。
func validateTerminalOutboxRenewal(outboxID int64, workerID, claimToken string, lease time.Duration) error {
	if outboxID <= 0 || workerID == "" || claimToken == "" || lease <= 0 {
		return errors.New("terminal outcome renewal requires id, worker, token and positive lease")
	}
	return nil
}

type outboxCandidate struct {
	id                  int64
	terminalIdentity    string
	eventID             string
	publicPayload       []byte
	privateDAGPayloadID sql.NullInt64
	history             *outboxHistoryCandidate
	privateDAG          *outboxPrivateDAGCandidate
}

// outboxHistoryCandidate 是与 outbox 行按 terminal_identity 绑定的完整公开历史快照。
// history 缺失、字段为空或其公开 JSON 与 outbox payload 不同，均由 decodeOutboxCandidate quarantine。
type outboxHistoryCandidate struct {
	terminalIdentity    string
	eventID             string
	agentID             string
	headVersion         int64
	schemaVersion       int64
	projectionKind      string
	publicThreadID      string
	providerTurnID      string
	sessionID           string
	generation          int64
	expectedActiveState string
	publicOutcome       []byte
	publicReport        string
	occurredAt          int64
}

// outboxPrivateDAGCandidate 是 private_dag_payload_id 指向的行元数据与正文。
// 元数据不能仅依赖 JSON 正文，必须与 outbox/public identity 逐字段一致。
type outboxPrivateDAGCandidate struct {
	id               int64
	terminalIdentity string
	ownerAgentID     string
	publicThreadID   string
	providerTurnID   string
	payload          []byte
	createdAt        int64
}

// listOutboxCandidatesTx 在 claim 事务内读取候选及其历史/private 关联，供后续严格解码和 quarantine。
func listOutboxCandidatesTx(ctx context.Context, tx *sql.Tx, now int64, limit int) ([]outboxCandidate, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT o.id, o.terminal_identity, o.event_id, o.public_payload_json, o.private_dag_payload_id,
		       p.terminal_identity, p.event_id, p.agent_id, p.head_version, p.schema_version,
		       p.projection_kind, p.public_thread_id, p.provider_turn_id, p.session_id,
		       p.generation, p.expected_active_state, p.public_outcome_json, p.public_report,
		       p.occurred_at,
		       d.id, d.terminal_identity, d.owner_agent_id, d.public_thread_id,
		       d.provider_turn_id, d.payload_json, d.created_at
		FROM terminal_outcome_outbox_v2 o
		LEFT JOIN public_terminal_outcome_history p ON p.terminal_identity = o.terminal_identity
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
		candidate, err := scanOutboxCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("scan terminal outcome outbox: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate terminal outcome outbox: %w", err)
	}
	return candidates, nil
}

// loadOutboxCandidateByIDTx 在同一事务中读取 outbox、历史和私有载荷的完整候选。
func loadOutboxCandidateByIDTx(ctx context.Context, tx *sql.Tx, id int64) (outboxCandidate, error) {
	if id <= 0 {
		return outboxCandidate{}, terminalOutboxIntegrityf("terminal outbox id is invalid")
	}
	row := tx.QueryRowContext(ctx, `
		SELECT o.id, o.terminal_identity, o.event_id, o.public_payload_json, o.private_dag_payload_id,
		       p.terminal_identity, p.event_id, p.agent_id, p.head_version, p.schema_version,
		       p.projection_kind, p.public_thread_id, p.provider_turn_id, p.session_id,
		       p.generation, p.expected_active_state, p.public_outcome_json, p.public_report,
		       p.occurred_at,
		       d.id, d.terminal_identity, d.owner_agent_id, d.public_thread_id,
		       d.provider_turn_id, d.payload_json, d.created_at
		FROM terminal_outcome_outbox_v2 o
		LEFT JOIN public_terminal_outcome_history p ON p.terminal_identity = o.terminal_identity
		LEFT JOIN terminal_outcome_private_dag_payloads d ON d.id = o.private_dag_payload_id
		WHERE o.id = ?
	`, id)
	candidate, err := scanOutboxCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return outboxCandidate{}, contract.ErrTerminalOutboxFence
	}
	if err != nil {
		return outboxCandidate{}, fmt.Errorf("load terminal outcome outbox %d: %w", id, err)
	}
	return candidate, nil
}

// scanOutboxCandidate 将 outbox/历史/private 三组列保留为 nullable，避免损坏数值在 Scan 阶段绕过 quarantine。
func scanOutboxCandidate(row terminalOutcomeRowScanner) (outboxCandidate, error) {
	var (
		candidate                             outboxCandidate
		rowID                                 sql.NullString
		terminalIdentity, eventID             sql.NullString
		publicPayload                         sql.NullString
		privatePayloadID                      sql.NullString
		historyIdentity, historyEventID       sql.NullString
		historyAgentID, historyProjection     sql.NullString
		historyThreadID, historyTurnID        sql.NullString
		historySessionID, historyState        sql.NullString
		historyOutcome, historyReport         sql.NullString
		historyHeadVersion, historySchema     sql.NullString
		historyGeneration, historyOccurred    sql.NullString
		privateID                             sql.NullString
		privateTerminalIdentity               sql.NullString
		privateOwnerAgentID, privateThreadID  sql.NullString
		privateProviderTurnID, privatePayload sql.NullString
		privateCreatedAt                      sql.NullString
	)
	if err := row.Scan(
		&rowID, &terminalIdentity, &eventID, &publicPayload, &privatePayloadID,
		&historyIdentity, &historyEventID, &historyAgentID, &historyHeadVersion, &historySchema,
		&historyProjection, &historyThreadID, &historyTurnID, &historySessionID,
		&historyGeneration, &historyState, &historyOutcome, &historyReport, &historyOccurred,
		&privateID, &privateTerminalIdentity, &privateOwnerAgentID, &privateThreadID,
		&privateProviderTurnID, &privatePayload, &privateCreatedAt,
	); err != nil {
		return outboxCandidate{}, err
	}
	candidate.id = parseTerminalNullableInt(rowID).Int64
	candidate.terminalIdentity = terminalIdentity.String
	candidate.eventID = eventID.String
	if publicPayload.Valid {
		candidate.publicPayload = []byte(publicPayload.String)
	}
	candidate.privateDAGPayloadID = parseTerminalNullableInt(privatePayloadID)
	if historyIdentity.Valid {
		candidate.history = &outboxHistoryCandidate{
			terminalIdentity:    historyIdentity.String,
			eventID:             historyEventID.String,
			agentID:             historyAgentID.String,
			headVersion:         parseTerminalNullableInt(historyHeadVersion).Int64,
			schemaVersion:       parseTerminalNullableInt(historySchema).Int64,
			projectionKind:      historyProjection.String,
			publicThreadID:      historyThreadID.String,
			providerTurnID:      historyTurnID.String,
			sessionID:           historySessionID.String,
			generation:          parseTerminalNullableInt(historyGeneration).Int64,
			expectedActiveState: historyState.String,
			publicOutcome:       []byte(historyOutcome.String),
			publicReport:        historyReport.String,
			occurredAt:          parseTerminalNullableInt(historyOccurred).Int64,
		}
	}
	if privateID.Valid {
		candidate.privateDAG = &outboxPrivateDAGCandidate{
			id:               parseTerminalNullableInt(privateID).Int64,
			terminalIdentity: privateTerminalIdentity.String,
			ownerAgentID:     privateOwnerAgentID.String,
			publicThreadID:   privateThreadID.String,
			providerTurnID:   privateProviderTurnID.String,
			payload:          []byte(privatePayload.String),
			createdAt:        parseTerminalNullableInt(privateCreatedAt).Int64,
		}
	}
	return candidate, nil
}

func parseTerminalNullableInt(value sql.NullString) sql.NullInt64 {
	if !value.Valid {
		return sql.NullInt64{}
	}
	parsed, err := strconv.ParseInt(value.String, 10, 64)
	if err != nil {
		return sql.NullInt64{Int64: -1, Valid: true}
	}
	return sql.NullInt64{Int64: parsed, Valid: true}
}

// decodeOutboxCandidate 严格交叉校验 outbox 行、公开 payload、history 与 owner-scoped private payload。
func decodeOutboxCandidate(candidate outboxCandidate) (contract.TerminalOutcomeOutboxItem, error) {
	if err := validateOutboxRowIdentity(candidate); err != nil {
		return contract.TerminalOutcomeOutboxItem{}, err
	}
	item := contract.TerminalOutcomeOutboxItem{ID: candidate.id}
	if err := json.Unmarshal(candidate.publicPayload, &item.Outcome); err != nil {
		return contract.TerminalOutcomeOutboxItem{}, terminalOutboxIntegrityf("decode public payload: %v", err)
	}
	if err := validateOutboxPayloadIdentity(candidate, item.Outcome.Identity); err != nil {
		return contract.TerminalOutcomeOutboxItem{}, err
	}
	if err := validateOutboxHistoryPayload(candidate.history, item.Outcome); err != nil {
		return contract.TerminalOutcomeOutboxItem{}, err
	}
	private, err := decodeOutboxPrivateDAG(candidate, item.Outcome.Identity)
	if err != nil {
		return contract.TerminalOutcomeOutboxItem{}, err
	}
	if private != nil {
		item.PrivateDAG = private
	}
	return item, nil
}

func validateOutboxRowIdentity(candidate outboxCandidate) error {
	if candidate.id <= 0 {
		return terminalOutboxIntegrityf("terminal outbox row id is invalid")
	}
	if strings.TrimSpace(candidate.terminalIdentity) == "" || strings.TrimSpace(candidate.eventID) == "" {
		return terminalOutboxIntegrityf("terminal outbox row identity is incomplete")
	}
	return nil
}

func validateOutboxPayloadIdentity(candidate outboxCandidate, identity contract.CanonicalTerminalIdentity) error {
	if identity.TerminalIdentity != candidate.terminalIdentity || identity.EventID != candidate.eventID {
		return terminalOutboxIntegrityf("outbox row and public payload identity mismatch")
	}
	return nil
}

func validateOutboxHistoryPayload(history *outboxHistoryCandidate, outcome contract.TerminalOutcomeCommit) error {
	if history == nil {
		return terminalOutboxIntegrityf("outbox references missing public history")
	}
	decoded, err := decodeOutboxHistory(*history, outcome.Identity)
	if err != nil {
		return err
	}
	if !sameCanonicalTerminal(decoded, outcome) {
		return terminalOutboxIntegrityf("history and public payload identity mismatch")
	}
	return nil
}

// decodeOutboxPrivateDAG 严格验证 private 行的 row metadata、JSON 正文和公开 identity。
func decodeOutboxPrivateDAG(candidate outboxCandidate, identity contract.CanonicalTerminalIdentity) (*contract.OwnerScopedDAGPayload, error) {
	if !candidate.privateDAGPayloadID.Valid {
		if candidate.privateDAG != nil {
			return nil, terminalOutboxIntegrityf("private DAG row is not referenced by outbox")
		}
		return nil, nil
	}
	if candidate.privateDAG == nil {
		return nil, terminalOutboxIntegrityf("outbox references missing private DAG payload")
	}
	var payload contract.OwnerScopedDAGPayload
	if err := strictJSONDecode(candidate.privateDAG.payload, &payload); err != nil {
		return nil, terminalOutboxIntegrityf("decode private DAG payload: %v", err)
	}
	if err := payload.Validate(identity); err != nil {
		return nil, terminalOutboxIntegrityf("private DAG payload identity mismatch: %v", err)
	}
	if !privateDAGRowMatches(candidate.privateDAG, candidate.privateDAGPayloadID.Int64, identity) ||
		!privateDAGMetadataMatchesPayload(candidate.privateDAG, payload) {
		return nil, terminalOutboxIntegrityf("private DAG row and payload identity mismatch")
	}
	return &payload, nil
}

// privateDAGRowMatches 比较 private 行主键和公开 terminal identity/owner fence。
func privateDAGRowMatches(private *outboxPrivateDAGCandidate, payloadID int64, identity contract.CanonicalTerminalIdentity) bool {
	return private != nil && private.id == payloadID && private.id > 0 &&
		private.terminalIdentity == identity.TerminalIdentity &&
		private.ownerAgentID == identity.AgentID && private.publicThreadID == identity.PublicThreadID &&
		private.providerTurnID == identity.ProviderTurnID && private.createdAt > 0
}

func privateDAGMetadataMatchesPayload(private *outboxPrivateDAGCandidate, payload contract.OwnerScopedDAGPayload) bool {
	return private != nil && private.ownerAgentID == payload.OwnerAgentID &&
		private.publicThreadID == payload.PublicThreadID && private.providerTurnID == payload.ProviderTurnID
}

// decodeOutboxHistory 将 history 行转换为可与 public payload 做 canonical 比较的 commit。
func decodeOutboxHistory(history outboxHistoryCandidate, identity contract.CanonicalTerminalIdentity) (contract.TerminalOutcomeCommit, error) {
	if !outboxHistoryTextFieldsPresent(history) || !outboxHistoryNumericFieldsPresent(history) {
		return contract.TerminalOutcomeCommit{}, terminalOutboxIntegrityf("public history identity or numeric fields are invalid")
	}
	var publicOutcome contract.PublicOutcome
	if err := strictJSONDecode(history.publicOutcome, &publicOutcome); err != nil {
		return contract.TerminalOutcomeCommit{}, terminalOutboxIntegrityf("decode public history outcome: %v", err)
	}
	commit := contract.TerminalOutcomeCommit{
		SchemaVersion: int(history.schemaVersion), ProjectionKind: history.projectionKind,
		Identity: contract.CanonicalTerminalIdentity{
			Capability: identity.Capability, AgentID: history.agentID,
			PublicThreadID: history.publicThreadID, ProviderTurnID: history.providerTurnID,
			SessionID: history.sessionID, Generation: uint64(history.generation),
			EventID: history.eventID, TerminalIdentity: history.terminalIdentity,
			ExpectedActiveState: history.expectedActiveState, HeadVersion: uint64(history.headVersion),
		},
		PublicOutcome: publicOutcome, PublicReport: history.publicReport,
		OccurredAt: time.UnixMilli(history.occurredAt).UTC(),
	}
	if err := commit.Validate(); err != nil {
		return contract.TerminalOutcomeCommit{}, terminalOutboxIntegrityf("validate public history: %v", err)
	}
	return commit, nil
}

// outboxHistoryTextFieldsPresent 拒绝 history 身份字符串缺失或全空白。
func outboxHistoryTextFieldsPresent(history outboxHistoryCandidate) bool {
	return strings.TrimSpace(history.terminalIdentity) != "" && strings.TrimSpace(history.eventID) != "" &&
		strings.TrimSpace(history.agentID) != "" && strings.TrimSpace(history.publicThreadID) != "" &&
		strings.TrimSpace(history.providerTurnID) != "" && strings.TrimSpace(history.sessionID) != "" &&
		strings.TrimSpace(history.expectedActiveState) != ""
}

func outboxHistoryNumericFieldsPresent(history outboxHistoryCandidate) bool {
	return history.generation > 0 && history.headVersion > 0 && history.schemaVersion > 0 && history.occurredAt > 0
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

// validateOutboxCandidateForMutationTx 在 Renew/ACK 的同一事务中复用 claim 前的完整性检查。
func validateOutboxCandidateForMutationTx(ctx context.Context, tx *sql.Tx, outboxID int64) error {
	candidate, err := loadOutboxCandidateByIDTx(ctx, tx, outboxID)
	if err != nil {
		return err
	}
	if _, err := decodeOutboxCandidate(candidate); err != nil {
		return err
	}
	return nil
}

// validateHistoryOutboxLinkTx 确认 history 与其 outbox 行/公开 payload/private payload 完整闭合。
func validateHistoryOutboxLinkTx(ctx context.Context, tx *sql.Tx, history contract.TerminalOutcomeCommit, outboxID int64) error {
	candidate, err := loadOutboxCandidateByIDTx(ctx, tx, outboxID)
	if err != nil {
		return err
	}
	item, err := decodeOutboxCandidate(candidate)
	if err != nil {
		return err
	}
	if !sameCanonicalTerminal(history, item.Outcome) {
		return terminalOutboxIntegrityf("history and public outbox payload do not match")
	}
	return nil
}

// beginValidatedOutboxMutationTx 打开 Renew/ACK 事务并在任何 mutation 前完成完整性 quarantine。
func (s *Store) beginValidatedOutboxMutationTx(ctx context.Context, outboxID int64, action string) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin terminal outcome %s: %w", action, err)
	}
	if err := validateOutboxCandidateForMutationTx(ctx, tx, outboxID); err == nil {
		return tx, nil
	} else {
		if poisonErr := poisonOutboxTx(ctx, tx, outboxID, err); poisonErr != nil {
			_ = tx.Rollback()
			return nil, poisonErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, fmt.Errorf("commit terminal outbox quarantine: %w", commitErr)
		}
		return nil, err
	}
}
