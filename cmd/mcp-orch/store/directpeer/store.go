package directpeer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

var (
	ErrUnauthorized        = errors.New("direct peer authorization denied")
	ErrIdempotencyConflict = errors.New("direct peer idempotency conflict")
	ErrEventOutboxFence    = errors.New("direct peer event outbox fence mismatch")
	// ErrDeliveryOutcomeUnknown 表示已接受消息尚无可权威重放的 provider 投递结果。
	ErrDeliveryOutcomeUnknown = errors.New("delivery_outcome_unknown")
)

const maxDirectPeerBodyBytes = 1 << 20

const (
	eventGrantActive           = "grant.active"
	eventGrantRevoked          = "grant.revoked"
	eventMessageAccepted       = "message.accepted"
	eventMessageSubmitted      = "message.submitted"
	eventMessageDeliveryFailed = "message.delivery_failed"
)

// Store 独占 direct-peer grant 与 message 的事务访问边界。
type Store struct {
	db         *sql.DB
	ownerToken [32]byte
	ownerStore
}

// OwnerCapability 是 direct-peer 状态写入 owner 的不可伪造进程内能力。
// 字段保持私有，且 capability 绑定创建它的 Store 实例；消息 ID 或普通
// provider 字符串本身不能构造该能力。
type OwnerCapability struct {
	store *Store
	token [32]byte
}

var ErrOwnerCapability = errors.New("direct peer owner capability required")

// Grant 是同工作区内发送者到接收者的有向授权快照。
type Grant struct {
	GrantID         string `json:"grant_id"`
	WorkspaceRoot   string `json:"workspace_root"`
	SenderAgentID   string `json:"sender_agent_id"`
	ReceiverAgentID string `json:"receiver_agent_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	Status          string `json:"status"`
	CanSend         bool   `json:"can_send"`
	CanRead         bool   `json:"can_read"`
	Revision        int64  `json:"revision"`
}

// Message 是持久 direct-peer 消息及其单调读取 cursor。
type Message struct {
	Cursor          int64  `json:"cursor"`
	GrantRevision   int64  `json:"grant_revision"`
	MessageID       string `json:"message_id"`
	WorkspaceRoot   string `json:"workspace_root"`
	SenderAgentID   string `json:"sender_agent_id"`
	ReceiverAgentID string `json:"receiver_agent_id"`
	Body            string `json:"body"`
	Status          string `json:"status"`
	SubmissionID    string `json:"submission_id"`
	DeliveryError   string `json:"delivery_error,omitempty"`
	// Fresh 仅在插入该消息的事务返回 true；accepted 重放不得把它当作投递授权，
	// 该字段只是诊断元数据，不是 provider receipt。
	Fresh bool `json:"fresh,omitempty"`
}

// Event 是 direct-peer append-only 历史项；Cursor 只在同一 workspace 内递增。
type Event struct {
	Cursor          int64           `json:"cursor"`
	EventID         string          `json:"event_id"`
	EventType       string          `json:"event_type"`
	WorkspaceRoot   string          `json:"workspace_root"`
	GrantID         string          `json:"grant_id,omitempty"`
	MessageID       string          `json:"message_id,omitempty"`
	SenderAgentID   string          `json:"sender_agent_id"`
	ReceiverAgentID string          `json:"receiver_agent_id"`
	GrantRevision   int64           `json:"grant_revision"`
	Payload         json.RawMessage `json:"payload"`
	CreatedAt       time.Time       `json:"created_at"`
}

// EventOutboxItem 是一次带 claim token 的 direct-peer 事件投影任务。
type EventOutboxItem struct {
	ID             int64     `json:"id"`
	Event          Event     `json:"event"`
	AttemptCount   int64     `json:"attempt_count"`
	ClaimToken     string    `json:"claim_token"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

// PublishedEvent 是 direct-peer outbox 投影到进程内事件总线的通知载荷。
type PublishedEvent struct {
	Event Event `json:"event"`
}

// Type 使 PublishedEvent 可被统一 event dispatcher 订阅。
func (PublishedEvent) Type() uint32 { return shareddto.EventTypeOrchestrationDirectPeer }

// SendRequest 绑定发送授权域、幂等键和不可空正文。
type SendRequest struct {
	WorkspaceRoot   string `json:"workspace_root"`
	SenderAgentID   string `json:"sender_agent_id"`
	ReceiverAgentID string `json:"receiver_agent_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	Body            string `json:"body"`
}

// NewStore 创建唯一 direct-peer 数据库 owner，nil 数据库直接失败。
func NewStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("direct peer store requires database")
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, fmt.Errorf("direct peer store owner capability: %w", err)
	}
	store := &Store{db: db, ownerToken: token}
	store.ownerStore.parent = store
	return store, nil
}

// GrantPeer 以 required key 激活一个有向权限；同 key 同请求返回原 grant。
func (s *Store) GrantPeer(ctx context.Context, workspaceRoot, sender, receiver, key string, canSend, canRead bool) (Grant, error) {
	if err := validateGrantInput(workspaceRoot, sender, receiver, canSend, canRead); err != nil {
		return Grant{}, err
	}
	if strings.TrimSpace(key) == "" {
		return Grant{}, errors.New("direct peer grant requires idempotency key")
	}
	digest := grantRequestDigest("grant", workspaceRoot, sender, receiver, canSend, canRead)
	if existing, found, err := s.grantByKey(ctx, workspaceRoot, key, digest); found || err != nil {
		return existing, err
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, err
	}
	row, err := storedb.New(tx).CreateDirectPeerGrant(ctx, storedb.CreateDirectPeerGrantParams{
		GrantID: shared.NewID("direct-peer-grant"), WorkspaceRoot: workspaceRoot,
		SenderAgentID: sender, ReceiverAgentID: receiver, IdempotencyKey: key, RequestDigest: digest,
		CanSend: boolInt(canSend), CanRead: boolInt(canRead), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		_ = tx.Rollback()
		if platformdb.IsConflict(err) {
			return Grant{}, fmt.Errorf("direct peer grant identity conflict: %w", ErrIdempotencyConflict)
		}
		return Grant{}, err
	}
	grant := grantFromRow(row)
	if err := appendGrantEventTx(ctx, tx, grant, eventGrantActive); err != nil {
		return Grant{}, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, err
	}
	return grant, nil
}

// RevokePeer 以 required key 撤回权限；历史消息保留供审计。
func (s *Store) RevokePeer(ctx context.Context, workspaceRoot, sender, receiver, key string) (Grant, error) {
	if err := validatePeerIdentity(workspaceRoot, sender, receiver); err != nil {
		return Grant{}, err
	}
	if strings.TrimSpace(key) == "" {
		return Grant{}, errors.New("direct peer revoke requires idempotency key")
	}
	digest := grantRequestDigest("revoke", workspaceRoot, sender, receiver, false, false)
	if existing, found, err := s.grantByKey(ctx, workspaceRoot, key, digest); found || err != nil {
		return existing, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, err
	}
	current, err := storedb.New(tx).GetDirectPeerGrant(ctx, storedb.GetDirectPeerGrantParams{WorkspaceRoot: workspaceRoot, SenderAgentID: sender, ReceiverAgentID: receiver})
	if err != nil {
		return Grant{}, errors.Join(err, tx.Rollback())
	}
	row, err := storedb.New(tx).RevokeDirectPeerGrant(ctx, storedb.RevokeDirectPeerGrantParams{
		IdempotencyKey: key, RequestDigest: digest, UpdatedAt: time.Now().UTC().UnixMilli(),
		WorkspaceRoot: workspaceRoot, SenderAgentID: sender, ReceiverAgentID: receiver, Revision: current.Revision,
	})
	if err != nil {
		return Grant{}, errors.Join(err, tx.Rollback())
	}
	grant := grantFromRow(row)
	if err := appendGrantEventTx(ctx, tx, grant, eventGrantRevoked); err != nil {
		return Grant{}, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return Grant{}, err
	}
	return grant, nil
}

// grantByKey 返回同 digest 的原 grant，异 digest 稳定冲突。
func (s *Store) grantByKey(ctx context.Context, workspaceRoot, key, digest string) (Grant, bool, error) {
	row, err := storedb.New(s.db).GetDirectPeerGrantByKey(ctx, storedb.GetDirectPeerGrantByKeyParams{WorkspaceRoot: workspaceRoot, IdempotencyKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, false, nil
	}
	if err != nil {
		return Grant{}, false, err
	}
	if row.RequestDigest != digest {
		return Grant{}, true, ErrIdempotencyConflict
	}
	return grantFromRow(row), true, nil
}

// Send 原子验证当前 grant 并插入消息；同 key 同 digest 返回原消息，异 digest 冲突。
func (s *Store) Send(ctx context.Context, request SendRequest) (Message, error) {
	if err := validateSendRequest(request); err != nil {
		return Message{}, err
	}
	digest := requestDigest(request)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	message, created, err := sendInTransaction(ctx, tx, request, digest)
	if err != nil {
		return Message{}, errors.Join(err, tx.Rollback())
	}
	if created {
		if err := appendMessageEventTx(ctx, tx, message, eventMessageAccepted); err != nil {
			return Message{}, errors.Join(err, tx.Rollback())
		}
	}
	message.Fresh = created
	return message, tx.Commit()
}

// sendInTransaction 在单事务里冻结幂等结果或验证 grant 后写入一条消息。
func sendInTransaction(ctx context.Context, tx *sql.Tx, request SendRequest, digest string) (Message, bool, error) {
	q := storedb.New(tx)
	grant, err := q.GetDirectPeerGrant(ctx, storedb.GetDirectPeerGrantParams{
		WorkspaceRoot: request.WorkspaceRoot, SenderAgentID: request.SenderAgentID, ReceiverAgentID: request.ReceiverAgentID,
	})
	if err != nil || grant.Status != "active" || grant.CanSend != 1 {
		return Message{}, false, errors.Join(ErrUnauthorized, err)
	}
	existing, err := q.GetDirectPeerMessageByKey(ctx, storedb.GetDirectPeerMessageByKeyParams{
		WorkspaceRoot: request.WorkspaceRoot, SenderAgentID: request.SenderAgentID, IdempotencyKey: request.IdempotencyKey,
	})
	if err == nil {
		if existing.RequestDigest != digest {
			return Message{}, false, ErrIdempotencyConflict
		}
		return messageFromRow(existing), false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Message{}, false, err
	}
	row, err := q.CreateDirectPeerMessage(ctx, storedb.CreateDirectPeerMessageParams{
		MessageID: shared.NewID("direct-peer-message"), IdempotencyKey: request.IdempotencyKey,
		RequestDigest: digest, WorkspaceRoot: request.WorkspaceRoot, SenderAgentID: request.SenderAgentID,
		ReceiverAgentID: request.ReceiverAgentID, GrantRevision: grant.Revision, Body: request.Body, CreatedAt: time.Now().UTC().UnixMilli(),
	})
	return messageFromRow(row), true, err
}

// ReadReceiver 从 exclusive cursor 正序读取，仅允许当前 active read grant 对应的发送者消息。
func (s *Store) ReadReceiver(ctx context.Context, workspaceRoot, receiver string, afterCursor int64, limit int) ([]Message, error) {
	if !filepath.IsAbs(workspaceRoot) || strings.TrimSpace(receiver) == "" || afterCursor < 0 || limit < 1 || limit > 200 {
		return nil, errors.New("direct peer read requires workspace, receiver, nonnegative cursor and limit 1..200")
	}
	rows, err := storedb.New(s.db).ListDirectPeerMessagesForReceiver(ctx, storedb.ListDirectPeerMessagesForReceiverParams{
		WorkspaceRoot: workspaceRoot, ReceiverAgentID: receiver, ID: afterCursor, Limit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	result := make([]Message, 0, len(rows))
	for _, row := range rows {
		result = append(result, messageFromRow(row))
	}
	return result, nil
}

// MarkSubmitted 保留旧签名但拒绝无 owner 能力的状态写入。
func (s *Store) MarkSubmitted(ctx context.Context, messageID, submissionID string) (Message, error) {
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(submissionID) == "" {
		return Message{}, errors.New("direct peer submission requires message and submission ids")
	}
	if strings.TrimSpace(messageID) == strings.TrimSpace(submissionID) {
		return Message{}, errors.New("direct peer submission id must be distinct from message id")
	}
	return Message{}, ErrOwnerCapability
}

// markSubmitted 在已验证 owner 能力下写入 provider submission 状态。
func (s *Store) markSubmitted(ctx context.Context, messageID, submissionID string) (Message, error) {
	if strings.TrimSpace(messageID) == "" || strings.TrimSpace(submissionID) == "" {
		return Message{}, errors.New("direct peer submission requires message and submission ids")
	}
	if strings.TrimSpace(messageID) == strings.TrimSpace(submissionID) {
		return Message{}, errors.New("direct peer submission id must be distinct from message id")
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	row, err := storedb.New(tx).MarkDirectPeerMessageSubmitted(ctx, storedb.MarkDirectPeerMessageSubmittedParams{
		SubmissionID: submissionID, DeliveredAt: &now, MessageID: messageID,
	})
	if err != nil {
		return Message{}, errors.Join(err, tx.Rollback())
	}
	message := messageFromRow(row)
	if err := appendMessageEventTx(ctx, tx, message, eventMessageSubmitted); err != nil {
		return Message{}, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// MarkDeliveryFailed 保留旧签名但拒绝无 owner 能力的状态写入。
func (s *Store) MarkDeliveryFailed(ctx context.Context, messageID, failure string) (Message, error) {
	failure = strings.TrimSpace(failure)
	if strings.TrimSpace(messageID) == "" || failure == "" {
		return Message{}, errors.New("direct peer failure requires message id and error")
	}
	if err := validateDeliveryFailure(failure); err != nil {
		return Message{}, err
	}
	return Message{}, ErrOwnerCapability
}

// markDeliveryFailed 在已验证 owner 能力下写入提交前失败状态。
func (s *Store) markDeliveryFailed(ctx context.Context, messageID, failure string) (Message, error) {
	failure = strings.TrimSpace(failure)
	if strings.TrimSpace(messageID) == "" || failure == "" {
		return Message{}, errors.New("direct peer failure requires message id and error")
	}
	if err := validateDeliveryFailure(failure); err != nil {
		return Message{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	row, err := storedb.New(tx).MarkDirectPeerMessageDeliveryFailed(ctx, storedb.MarkDirectPeerMessageDeliveryFailedParams{
		DeliveryError: failure, MessageID: messageID,
	})
	if err != nil {
		return Message{}, errors.Join(err, tx.Rollback())
	}
	message := messageFromRow(row)
	if err := appendMessageEventTx(ctx, tx, message, eventMessageDeliveryFailed); err != nil {
		return Message{}, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

func validateDeliveryFailure(failure string) error {
	if len(failure) > 512 {
		return errors.New("direct peer failure exceeds 512 bytes")
	}
	if !utf8.ValidString(failure) || strings.ContainsRune(failure, 0) {
		return errors.New("direct peer failure must be valid UTF-8 without NUL")
	}
	return nil
}

// ListEventsForReceiver 以 active read grant 和 exclusive event cursor 恢复事件历史。
func (s *Store) ListEventsForReceiver(ctx context.Context, workspaceRoot, receiver string, afterCursor int64, limit int) ([]Event, error) {
	if err := validateEventRead(s, workspaceRoot, receiver, afterCursor, limit); err != nil {
		return nil, err
	}
	rows, err := storedb.New(s.db).ListDirectPeerEventsForReceiver(ctx, storedb.ListDirectPeerEventsForReceiverParams{
		WorkspaceRoot: workspaceRoot, ReceiverAgentID: receiver, ID: afterCursor, Limit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows)
}

// ClaimDirectPeerEventOutbox 以短租约领取 pending 或已过期任务，避免崩溃窗口丢失事件。
func (s *Store) ClaimDirectPeerEventOutbox(ctx context.Context, workerID string, lease time.Duration, limit int) ([]EventOutboxItem, error) {
	if err := validateEventOutboxWorker(s, workerID, lease, limit); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin direct peer event outbox claim: %w", err)
	}
	items, err := claimDirectPeerEventOutboxTx(ctx, tx, workerID, now, lease, limit)
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit direct peer event outbox claim: %w", err)
	}
	return items, nil
}

// validateEventOutboxWorker 拒绝空 worker、无界租约和超出协议上限的批量领取。
func validateEventOutboxWorker(s *Store, workerID string, lease time.Duration, limit int) error {
	if s == nil || s.db == nil {
		return errors.New("direct peer store database is required")
	}
	if strings.TrimSpace(workerID) == "" || lease <= 0 || limit < 1 || limit > 200 {
		return errors.New("direct peer event outbox requires worker, positive lease and limit 1..200")
	}
	return nil
}

// claimDirectPeerEventOutboxTx 在同一事务内读取候选并安装唯一 worker 租约。
func claimDirectPeerEventOutboxTx(ctx context.Context, tx *sql.Tx, workerID string, now time.Time, lease time.Duration, limit int) ([]EventOutboxItem, error) {
	nowMs := now.UnixMilli()
	rows, err := storedb.New(tx).ListDirectPeerEventOutboxCandidates(ctx, storedb.ListDirectPeerEventOutboxCandidatesParams{LeaseExpiresAt: &nowMs, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list direct peer event outbox: %w", err)
	}
	items := make([]EventOutboxItem, 0, len(rows))
	for _, row := range rows {
		item, err := eventOutboxItemFromRow(row)
		if err != nil {
			if poisonErr := poisonDirectPeerEventOutboxTx(ctx, tx, row.ID, err); poisonErr != nil {
				return nil, poisonErr
			}
			continue
		}
		token, err := newDirectPeerClaimToken()
		if err != nil {
			return nil, err
		}
		expiresAt := now.Add(lease)
		expiresMs := expiresAt.UnixMilli()
		if err := claimDirectPeerEventOutboxRow(ctx, tx, row.ID, workerID, token, &expiresMs, &nowMs); err != nil {
			return nil, err
		}
		item.ClaimToken, item.LeaseExpiresAt = token, expiresAt
		item.AttemptCount = row.AttemptCount + 1
		items = append(items, item)
	}
	return items, nil
}

func eventOutboxItemFromRow(row storedb.ListDirectPeerEventOutboxCandidatesRow) (EventOutboxItem, error) {
	event, err := eventFromRow(storedb.OrchestrationDirectPeerEvent{ID: row.ID_2, EventID: row.EventID_2,
		EventType: row.EventType, WorkspaceRoot: row.WorkspaceRoot, GrantID: row.GrantID, MessageID: row.MessageID,
		SenderAgentID: row.SenderAgentID, ReceiverAgentID: row.ReceiverAgentID, GrantRevision: row.GrantRevision,
		PayloadJson: row.PayloadJson, CreatedAt: row.CreatedAt_2})
	if err != nil {
		return EventOutboxItem{}, err
	}
	return EventOutboxItem{ID: row.ID, Event: event, AttemptCount: row.AttemptCount}, nil
}

func claimDirectPeerEventOutboxRow(ctx context.Context, tx *sql.Tx, id int64, workerID, token string, expiresAt, now *int64) error {
	rows, err := storedb.New(tx).ClaimDirectPeerEventOutbox(ctx, storedb.ClaimDirectPeerEventOutboxParams{
		ClaimedBy: workerID, ClaimToken: token, LeaseExpiresAt: expiresAt, ID: id, LeaseExpiresAt_2: now,
	})
	if err != nil {
		return fmt.Errorf("claim direct peer event outbox %d: %w", id, err)
	}
	if rows != 1 {
		return ErrEventOutboxFence
	}
	return nil
}

func poisonDirectPeerEventOutboxTx(ctx context.Context, tx *sql.Tx, id int64, _ error) error {
	rows, err := storedb.New(tx).PoisonDirectPeerEventOutbox(ctx, storedb.PoisonDirectPeerEventOutboxParams{
		LastError: "direct peer event failed durable validation", ID: id,
	})
	if err != nil {
		return fmt.Errorf("poison direct peer event outbox %d: %w", id, err)
	}
	if rows != 1 {
		return ErrEventOutboxFence
	}
	return nil
}

// RenewDirectPeerEventOutbox 只允许当前 worker/token 在租约未过期时续租。
func (s *Store) RenewDirectPeerEventOutbox(ctx context.Context, id int64, workerID, token string, lease time.Duration) (time.Time, error) {
	if err := validateEventOutboxClaim(s, id, workerID, token, lease); err != nil {
		return time.Time{}, err
	}
	if lease <= 0 {
		return time.Time{}, errors.New("direct peer event outbox renewal requires positive lease")
	}
	now := time.Now().UTC()
	expiresAt := now.Add(lease)
	nowMs, expiresMs := now.UnixMilli(), expiresAt.UnixMilli()
	rows, err := storedb.New(s.db).RenewDirectPeerEventOutbox(ctx, storedb.RenewDirectPeerEventOutboxParams{
		LeaseExpiresAt: &expiresMs, ID: id, ClaimedBy: strings.TrimSpace(workerID), ClaimToken: strings.TrimSpace(token), LeaseExpiresAt_2: &nowMs,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("renew direct peer event outbox %d: %w", id, err)
	}
	if rows != 1 {
		return time.Time{}, ErrEventOutboxFence
	}
	return expiresAt, nil
}

// MarkDirectPeerEventOutboxProjected 仅允许未过期的当前 claim 完成 ACK。
func (s *Store) MarkDirectPeerEventOutboxProjected(ctx context.Context, id int64, workerID, token string) error {
	if err := validateEventOutboxClaim(s, id, workerID, token, 0); err != nil {
		return err
	}
	nowMs := time.Now().UTC().UnixMilli()
	rows, err := storedb.New(s.db).MarkDirectPeerEventOutboxProjected(ctx, storedb.MarkDirectPeerEventOutboxProjectedParams{
		ProjectedAt: &nowMs, ID: id, ClaimedBy: strings.TrimSpace(workerID), ClaimToken: strings.TrimSpace(token), LeaseExpiresAt: &nowMs,
	})
	if err != nil {
		return fmt.Errorf("project direct peer event outbox %d: %w", id, err)
	}
	if rows != 1 {
		return ErrEventOutboxFence
	}
	return nil
}

// validateEventOutboxClaim 校验 ACK/续租所需的持久 store、身份、token 和租约参数。
func validateEventOutboxClaim(s *Store, id int64, workerID, token string, lease time.Duration) error {
	if s == nil || s.db == nil {
		return errors.New("direct peer store database is required")
	}
	if id <= 0 || strings.TrimSpace(workerID) == "" || strings.TrimSpace(token) == "" || lease < 0 {
		return errors.New("direct peer event outbox claim requires id, worker, token and nonnegative lease")
	}
	return nil
}

func newDirectPeerClaimToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate direct peer event claim token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// validateEventRead 校验事件读取的 workspace、身份、exclusive cursor 和页大小。
func validateEventRead(s *Store, workspaceRoot, receiver string, afterCursor int64, limit int) error {
	if s == nil || s.db == nil {
		return errors.New("direct peer store database is required")
	}
	if !filepath.IsAbs(workspaceRoot) || strings.TrimSpace(receiver) == "" || afterCursor < 0 || limit < 1 || limit > 200 {
		return errors.New("direct peer event read requires workspace, receiver, cursor and limit 1..200")
	}
	return nil
}

func eventsFromRows(rows []storedb.OrchestrationDirectPeerEvent) ([]Event, error) {
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

// validateGrantInput 要求授权至少开启一项能力并具有合法有向身份。
func validateGrantInput(workspaceRoot, sender, receiver string, canSend, canRead bool) error {
	if !canSend && !canRead {
		return errors.New("direct peer grant must enable send or read")
	}
	return validatePeerIdentity(workspaceRoot, sender, receiver)
}

// validatePeerIdentity 拒绝跨相对工作区、空 agent 或 self grant。
func validatePeerIdentity(workspaceRoot, sender, receiver string) error {
	if !filepath.IsAbs(workspaceRoot) || strings.TrimSpace(sender) == "" || strings.TrimSpace(receiver) == "" || sender == receiver {
		return errors.New("direct peer requires absolute workspace and distinct sender/receiver")
	}
	return nil
}

// validateSendRequest 要求稳定幂等键和非空正文，防止隐式默认消息。
func validateSendRequest(request SendRequest) error {
	if err := validatePeerIdentity(request.WorkspaceRoot, request.SenderAgentID, request.ReceiverAgentID); err != nil {
		return err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.Body) == "" {
		return errors.New("direct peer send requires idempotency key and body")
	}
	if len([]byte(request.Body)) > maxDirectPeerBodyBytes {
		return errors.New("direct peer message exceeds 1 MiB")
	}
	return nil
}

// requestDigest 绑定授权域和正文，使同 key 的异请求稳定冲突。
func requestDigest(request SendRequest) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{request.WorkspaceRoot, request.SenderAgentID, request.ReceiverAgentID, request.Body}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// grantRequestDigest 绑定动作、agent 方向及授权位，避免 key 跨动作重用。
func grantRequestDigest(action, workspace, sender, receiver string, canSend, canRead bool) string {
	parts := []string{action, workspace, sender, receiver, fmt.Sprint(canSend), fmt.Sprint(canRead)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// boolInt 映射 SQLite 的受约束整数布尔值。
func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// grantFromRow 把生成层记录映射为 store DTO。
func grantFromRow(row storedb.OrchestrationDirectPeerGrant) Grant {
	return Grant{GrantID: row.GrantID, WorkspaceRoot: row.WorkspaceRoot, SenderAgentID: row.SenderAgentID,
		IdempotencyKey:  row.IdempotencyKey,
		ReceiverAgentID: row.ReceiverAgentID, Status: row.Status, CanSend: row.CanSend == 1, CanRead: row.CanRead == 1, Revision: row.Revision}
}

// messageFromRow 保留数据库单调 ID 作为 exclusive cursor。
func messageFromRow(row storedb.OrchestrationDirectPeerMessage) Message {
	return Message{Cursor: row.ID, GrantRevision: row.GrantRevision, MessageID: row.MessageID,
		WorkspaceRoot: row.WorkspaceRoot, SenderAgentID: row.SenderAgentID, ReceiverAgentID: row.ReceiverAgentID,
		Body: row.Body, Status: row.Status, SubmissionID: row.SubmissionID, DeliveryError: row.DeliveryError}
}

func appendGrantEventTx(ctx context.Context, tx *sql.Tx, grant Grant, eventType string) error {
	return appendDirectPeerEventTx(ctx, tx, eventType, grant.WorkspaceRoot, grant.GrantID, "",
		grant.SenderAgentID, grant.ReceiverAgentID, grant.Revision, grant)
}

func appendMessageEventTx(ctx context.Context, tx *sql.Tx, message Message, eventType string) error {
	return appendDirectPeerEventTx(ctx, tx, eventType, message.WorkspaceRoot, "", message.MessageID,
		message.SenderAgentID, message.ReceiverAgentID, message.GrantRevision, message)
}

func appendDirectPeerEventTx(ctx context.Context, tx *sql.Tx, eventType, workspace, grantID, messageID, sender, receiver string, revision int64, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode direct peer %s event: %w", eventType, err)
	}
	now := time.Now().UTC().UnixMilli()
	q := storedb.New(tx)
	row, err := q.CreateDirectPeerEvent(ctx, storedb.CreateDirectPeerEventParams{
		EventID: shared.NewID("direct-peer-event"), EventType: eventType, WorkspaceRoot: workspace,
		GrantID: grantID, MessageID: messageID, SenderAgentID: sender, ReceiverAgentID: receiver,
		GrantRevision: revision, PayloadJson: string(encoded), CreatedAt: now,
	})
	if err != nil {
		return fmt.Errorf("append direct peer %s event: %w", eventType, err)
	}
	if err := q.CreateDirectPeerEventOutbox(ctx, storedb.CreateDirectPeerEventOutboxParams{EventID: row.EventID, CreatedAt: now}); err != nil {
		return fmt.Errorf("enqueue direct peer %s event: %w", eventType, err)
	}
	return nil
}

func eventFromRow(row storedb.OrchestrationDirectPeerEvent) (Event, error) {
	if row.ID < 1 || row.CreatedAt < 1 || row.EventID == "" || !json.Valid([]byte(row.PayloadJson)) {
		return Event{}, errors.New("direct peer event contains invalid durable fields")
	}
	return Event{Cursor: row.ID, EventID: row.EventID, EventType: row.EventType, WorkspaceRoot: row.WorkspaceRoot,
		GrantID: row.GrantID, MessageID: row.MessageID, SenderAgentID: row.SenderAgentID,
		ReceiverAgentID: row.ReceiverAgentID, GrantRevision: row.GrantRevision,
		Payload: json.RawMessage(row.PayloadJson), CreatedAt: time.UnixMilli(row.CreatedAt).UTC()}, nil
}
