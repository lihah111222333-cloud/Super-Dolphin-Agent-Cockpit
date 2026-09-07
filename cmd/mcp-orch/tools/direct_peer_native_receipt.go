package tools

import (
	"context"
	"errors"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type nativeTurnSubmitter interface {
	SubmitTurnWithReceipt(context.Context, contract.TurnSubmission) (contract.ProviderSubmissionReceipt, error)
}

type nativeDirectPeerStore interface {
	OwnerCapability() (directpeer.OwnerCapability, error)
	RecordNativeReceipt(context.Context, directpeer.OwnerCapability, string, contract.ProviderSubmissionReceipt) (directpeer.Message, error)
	ReadNativeReceipt(context.Context, directpeer.Message) (contract.ProviderSubmissionReceipt, error)
}

// submitDirectPeerNative 仅将真实原生协议确认写入消息 owner；任何未知结果都不重发。
func submitDirectPeerNative(ctx context.Context, store directPeerPort, turns nativeTurnSubmitter, message directpeer.Message, req contract.TurnSubmission) (any, error) {
	owner, ok := store.(nativeDirectPeerStore)
	if !ok {
		return nil, errors.New("direct peer native receipt owner is unavailable")
	}
	capability, err := owner.OwnerCapability()
	if err != nil {
		return nil, err
	}
	receipt, err := turns.SubmitTurnWithReceipt(ctx, req)
	if err != nil {
		return nil, nativeDeliveryUnknown()
	}
	if err := receipt.Validate(); err != nil {
		return nil, nativeDeliveryUnknown()
	}
	if receipt.AgentID != req.AgentID || receipt.ThreadID != req.ThreadID {
		return nil, nativeDeliveryUnknown()
	}
	result, err := owner.RecordNativeReceipt(ctx, capability, message.MessageID, receipt)
	if err != nil {
		return nil, nativeDeliveryUnknown()
	}
	return result, nil
}

// readDirectPeerNativeReplay 按原消息查询持久确认；旧 submitted 字符串不是确认凭证。
func readDirectPeerNativeReplay(ctx context.Context, store directPeerPort, message directpeer.Message) (any, error) {
	owner, ok := store.(nativeDirectPeerStore)
	if !ok {
		return nil, nativeDeliveryUnknown()
	}
	if _, err := owner.ReadNativeReceipt(ctx, message); err != nil {
		return nil, nativeDeliveryUnknown()
	}
	return message, nil
}

// nativeDeliveryUnknown 保留非重试语义并脱敏 provider/持久化错误。
func nativeDeliveryUnknown() error {
	return mcpcommon.NewCodedToolError("delivery_outcome_unknown", directpeer.ErrDeliveryOutcomeUnknown, false,
		"next: inspect the original message and native acknowledgement; do not resubmit")
}
