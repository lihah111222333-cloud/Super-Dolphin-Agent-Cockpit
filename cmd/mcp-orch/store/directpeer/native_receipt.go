package directpeer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// nativeReceiptEvent 保留既有消息投影并持久保存完整 CLI 确认；它不是 OUT 回执。
type nativeReceiptEvent struct {
	Message
	ProviderReceipt contract.ProviderSubmissionReceipt `json:"provider_receipt"`
}

// RecordNativeReceipt 在单事务内绑定 receiver、原生确认、submitted 事件与既有 outbox。
func (o *ownerStore) RecordNativeReceipt(ctx context.Context, capability OwnerCapability, messageID string, receipt contract.ProviderSubmissionReceipt) (Message, error) {
	if err := o.validateOwnerCapability(capability); err != nil {
		return Message{}, err
	}
	if err := validateNativeReceiptMessageID(messageID, receipt); err != nil {
		return Message{}, err
	}
	tx, err := o.parent.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	message, err := recordNativeReceiptTx(ctx, tx, messageID, receipt)
	if err != nil {
		return Message{}, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// validateNativeReceiptMessageID 拒绝缺失确认与把消息键伪装为 provider turn/receipt 的写入。
func validateNativeReceiptMessageID(messageID string, receipt contract.ProviderSubmissionReceipt) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if messageID == "" || strings.TrimSpace(messageID) != messageID {
		return errors.New("native receipt message id is required")
	}
	if messageID == receipt.TurnID || messageID == receipt.ReceiptID {
		return errors.New("native receipt identity must differ from message id")
	}
	return nil
}

// recordNativeReceiptTx 仅转换 accepted 行；错误身份或事件写入故障由调用方整体回滚。
func recordNativeReceiptTx(ctx context.Context, tx *sql.Tx, messageID string, receipt contract.ProviderSubmissionReceipt) (Message, error) {
	now := time.Now().UTC().UnixMilli()
	row, err := storedb.New(tx).MarkDirectPeerMessageSubmitted(ctx, storedb.MarkDirectPeerMessageSubmittedParams{SubmissionID: receipt.TurnID, DeliveredAt: &now, MessageID: messageID})
	if err != nil {
		return Message{}, err
	}
	message := messageFromRow(row)
	if message.ReceiverAgentID != receipt.AgentID {
		return Message{}, errors.New("native receipt receiver mismatch")
	}
	payload := nativeReceiptEvent{Message: message, ProviderReceipt: receipt}
	if err := appendDirectPeerEventTx(ctx, tx, eventMessageSubmitted, message.WorkspaceRoot, "", message.MessageID, message.SenderAgentID, message.ReceiverAgentID, message.GrantRevision, payload); err != nil {
		return Message{}, err
	}
	return message, nil
}

// ReadNativeReceipt 精确读取 submitted 事件；旧状态字符串或不完整载荷只能返回 unknown。
func (s *Store) ReadNativeReceipt(ctx context.Context, message Message) (contract.ProviderSubmissionReceipt, error) {
	if s == nil || s.db == nil {
		return contract.ProviderSubmissionReceipt{}, errors.New("direct peer store database is required")
	}
	row, err := storedb.New(s.db).GetDirectPeerSubmittedEvent(ctx, storedb.GetDirectPeerSubmittedEventParams{MessageID: message.MessageID})
	if err != nil {
		return contract.ProviderSubmissionReceipt{}, errors.Join(ErrDeliveryOutcomeUnknown, err)
	}
	receipt, err := decodeNativeReceiptEvent(row, message)
	if err != nil {
		return contract.ProviderSubmissionReceipt{}, errors.Join(ErrDeliveryOutcomeUnknown, err)
	}
	return receipt, nil
}

// decodeNativeReceiptEvent 以生产 DTO 严格解码并核对事件外层和消息内层的身份闭包。
func decodeNativeReceiptEvent(row storedb.OrchestrationDirectPeerEvent, message Message) (contract.ProviderSubmissionReceipt, error) {
	event, err := eventFromRow(row)
	if err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	if err := validateNativeEventEnvelope(event, message); err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	var payload nativeReceiptEvent
	decoder := json.NewDecoder(bytes.NewReader(event.Payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return contract.ProviderSubmissionReceipt{}, fmt.Errorf("native receipt payload trailing data: %v", err)
	}
	if err := validateNativeReceiptMessageID(message.MessageID, payload.ProviderReceipt); err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	message.Fresh = false
	if payload.Message != message || payload.ProviderReceipt.AgentID != message.ReceiverAgentID || payload.ProviderReceipt.TurnID != message.SubmissionID {
		return contract.ProviderSubmissionReceipt{}, errors.New("native receipt payload identity mismatch")
	}
	return payload.ProviderReceipt, nil
}

// validateNativeEventEnvelope 不接受外部传入的伪造 submitted 快照或错关联事件。
func validateNativeEventEnvelope(event Event, message Message) error {
	if message.Status != "submitted" || message.SubmissionID == "" {
		return errors.New("native receipt requires submitted message")
	}
	if event.EventType != eventMessageSubmitted || event.MessageID != message.MessageID || event.WorkspaceRoot != message.WorkspaceRoot {
		return errors.New("native receipt event message mismatch")
	}
	if event.SenderAgentID != message.SenderAgentID || event.ReceiverAgentID != message.ReceiverAgentID || event.GrantRevision != message.GrantRevision {
		return errors.New("native receipt event peer mismatch")
	}
	return nil
}
