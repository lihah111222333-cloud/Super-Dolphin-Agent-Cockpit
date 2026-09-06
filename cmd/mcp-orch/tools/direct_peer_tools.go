package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	directpeerstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type directPeerPort interface {
	GrantPeer(context.Context, string, string, string, string, bool, bool) (directpeerstore.Grant, error)
	RevokePeer(context.Context, string, string, string, string) (directpeerstore.Grant, error)
	Send(context.Context, directpeerstore.SendRequest) (directpeerstore.Message, error)
	ReadReceiver(context.Context, string, string, int64, int) ([]directpeerstore.Message, error)
	ListEventsForReceiver(context.Context, string, string, int64, int) ([]directpeerstore.Event, error)
	MarkDeliveryFailed(context.Context, string, string) (directpeerstore.Message, error)
}

// DirectPeerPorts 组合持久消息 owner 与 agent 存在性/工作区读取端口。
type DirectPeerPorts struct {
	Store  directPeerPort
	Agents contract.AgentStateReader
	Turns  contract.TurnSubmissionPort
}

type directPeerDeliveryFailureOwner interface {
	OwnerCapability() (directpeerstore.OwnerCapability, error)
	MarkDeliveryFailedByOwner(context.Context, directpeerstore.OwnerCapability, string, string) (directpeerstore.Message, error)
}

type manageDirectPeerInput struct {
	Action          string `json:"action"`
	SenderAgentID   string `json:"sender_agent_id"`
	ReceiverAgentID string `json:"receiver_agent_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	CanSend         bool   `json:"can_send"`
	CanRead         bool   `json:"can_read"`
}

type sendDirectPeerInput struct {
	ReceiverAgentID string `json:"receiver_agent_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	Message         string `json:"message"`
}

type readDirectPeerInput struct {
	AfterCursor int64 `json:"after_cursor"`
	Limit       int   `json:"limit"`
}

// directPeerToolDefinitions 注册 operator 授权、agent 发送和接收者 cursor 读取工具。
func directPeerToolDefinitions(ports DirectPeerPorts) []ToolDefinition {
	return buildToolDefinitions(
		defineTaskWriteTool("manage_direct_peer", "Grant or revoke a directed peer relationship. Operator scope only.", ObjectSchema(map[string]Schema{
			"action":            EnumStringSchema("Required action.", "grant", "revoke"),
			"sender_agent_id":   StringSchema("Directed sender agent ID."),
			"receiver_agent_id": StringSchema("Directed receiver agent ID."),
			"idempotency_key":   StringSchema("Required stable key for grant/revoke retry."),
			"can_send":          BooleanSchema("Grant send capability."),
			"can_read":          BooleanSchema("Grant receiver read capability."),
		}, "action", "sender_agent_id", "receiver_agent_id", "idempotency_key"), handleManageDirectPeer(ports), "orchestration.direct_peer.manage", ToolIdempotencyRequired, "orchestration.direct_peer.grant"),
		defineTaskWriteTool("send_direct_peer", "Persist one authorized message from the trusted caller agent.", ObjectSchema(map[string]Schema{
			"receiver_agent_id": StringSchema("Authorized receiver agent ID."),
			"idempotency_key":   StringSchema("Required stable key for safe retry."),
			"message":           StringSchema("Nonempty message body."),
		}, "receiver_agent_id", "idempotency_key", "message"), handleSendDirectPeer(ports), "orchestration.direct_peer.send", ToolIdempotencyRequired, "orchestration.direct_peer.message"),
		defineTaskReadTool("read_direct_peer", "Read authorized messages for the trusted caller agent after an exclusive cursor.", ObjectSchema(map[string]Schema{
			"after_cursor": IntegerSchema("Exclusive nonnegative cursor; use 0 initially."),
			"limit":        IntegerSchema("Page size 1..200; defaults to 50."),
		}), handleReadDirectPeer(ports.Store), "orchestration.direct_peer.read", "orchestration.direct_peer.cursor"),
		defineTaskReadTool("read_direct_peer_events", "Read authorized direct-peer event history after an exclusive cursor.", ObjectSchema(map[string]Schema{
			"after_cursor": IntegerSchema("Exclusive nonnegative event cursor; use 0 initially."),
			"limit":        IntegerSchema("Page size 1..200; defaults to 50."),
		}), handleReadDirectPeerEvents(ports.Store), "orchestration.direct_peer.events", "orchestration.direct_peer.cursor"),
	)
}

// handleManageDirectPeer 只允许 operator scope 管理已存在且同工作区的有向 agent grant。
func handleManageDirectPeer(ports DirectPeerPorts) ToolHandler {
	return makeHandler(ports.Store, "direct peer store", func(ctx context.Context, input manageDirectPeerInput) (any, error) {
		scope, err := directPeerScope(ctx, false)
		if err != nil {
			return nil, err
		}
		if scope.AgentID != "" {
			return nil, errors.New("direct peer grants require operator scope")
		}
		sender, receiver := strings.TrimSpace(input.SenderAgentID), strings.TrimSpace(input.ReceiverAgentID)
		key := strings.TrimSpace(input.IdempotencyKey)
		if err := validateDirectPeerAgents(ctx, ports.Agents, scope.CWD, sender, receiver); err != nil {
			return nil, err
		}
		if input.Action == "grant" {
			return ports.Store.GrantPeer(ctx, scope.CWD, sender, receiver, key, input.CanSend, input.CanRead)
		}
		if input.Action == "revoke" {
			return ports.Store.RevokePeer(ctx, scope.CWD, sender, receiver, key)
		}
		return nil, errors.New("direct peer action must be grant or revoke")
	})
}

func validateDirectPeerAgents(ctx context.Context, agents contract.AgentStateReader, workspace, sender, receiver string) error {
	if agents == nil {
		return errors.New("direct peer agent state is unavailable")
	}
	for _, agentID := range []string{sender, receiver} {
		snapshot, err := agents.Snapshot(ctx, agentID)
		if err != nil {
			return err
		}
		if filepath.Clean(snapshot.Cwd) != filepath.Clean(workspace) {
			return errors.New("direct peer agents must belong to the operator workspace")
		}
	}
	return nil
}

func handleSendDirectPeer(ports DirectPeerPorts) ToolHandler {
	return makeHandler(ports.Store, "direct peer store", func(ctx context.Context, input sendDirectPeerInput) (any, error) {
		scope, err := directPeerScope(ctx, true)
		if err != nil {
			return nil, err
		}
		message, err := ports.Store.Send(ctx, directpeerstore.SendRequest{WorkspaceRoot: scope.CWD, SenderAgentID: scope.AgentID,
			ReceiverAgentID: strings.TrimSpace(input.ReceiverAgentID), IdempotencyKey: strings.TrimSpace(input.IdempotencyKey), Body: input.Message})
		if err != nil || message.Status != "accepted" {
			return message, err
		}
		if !message.Fresh {
			// An accepted replay proves only durable intent, not provider custody.
			// Until an authoritative OUT receipt exists, fail closed instead of
			// invoking SubmitTurn a second time.
			return nil, mcpcommon.NewCodedToolError("delivery_outcome_unknown",
				fmt.Errorf("%w: accepted direct peer message %s has no authoritative delivery receipt", directpeerstore.ErrDeliveryOutcomeUnknown, message.MessageID),
				false, "next: inspect direct-peer events/receipt state before retrying; do not replay the accepted message")
		}
		return submitDirectPeerTurn(ctx, ports, message)
	})
}

// submitDirectPeerTurn 提交接收者 turn；没有 typed provider receipt 时保持 accepted/unknown。
func submitDirectPeerTurn(ctx context.Context, ports DirectPeerPorts, message directpeerstore.Message) (any, error) {
	if err := requireDependency(ports.Store, "direct peer store"); err != nil {
		return nil, err
	}
	if err := requireDependency(ports.Agents, "direct peer agent state"); err != nil {
		markErr := markDirectPeerDeliveryFailed(ctx, ports.Store, message.MessageID, "delivery ports unavailable")
		return nil, errors.Join(errors.New("direct peer delivery ports are unavailable"), markErr)
	}
	if err := requireDependency(ports.Turns, "direct peer turn submission"); err != nil {
		markErr := markDirectPeerDeliveryFailed(ctx, ports.Store, message.MessageID, "delivery ports unavailable")
		return nil, errors.Join(errors.New("direct peer delivery ports are unavailable"), markErr)
	}
	snapshot, err := ports.Agents.Snapshot(ctx, message.ReceiverAgentID)
	if err != nil {
		markErr := markDirectPeerDeliveryFailed(ctx, ports.Store, message.MessageID, "receiver snapshot unavailable")
		return nil, errors.Join(err, markErr)
	}
	submission := contract.TurnSubmission{AgentID: message.ReceiverAgentID, ThreadID: snapshot.ThreadID,
		Inputs: []turndto.InputItem{{Type: "text", Content: message.Body}}}
	if err := ports.Turns.SubmitTurn(ctx, submission); err != nil {
		// TurnSubmissionPort 只返回 error，无法证明 provider 是否已经接受请求。
		// 保留 durable accepted 消息并返回脱敏的不可授权 unknown；不写伪造失败回执，
		// 也不邀请可能重复 external effect 的重试。
		return nil, mcpcommon.NewCodedToolError("delivery_outcome_unknown",
			fmt.Errorf("%w: provider submission outcome is unknown", directpeerstore.ErrDeliveryOutcomeUnknown), false,
			"next: reconcile the original submission before retrying")
	}
	return nil, mcpcommon.NewCodedToolError("delivery_outcome_unknown",
		fmt.Errorf("%w: provider submission returned without an authoritative receipt", directpeerstore.ErrDeliveryOutcomeUnknown), false,
		"next: reconcile the original submission before retrying")
}

func markDirectPeerDeliveryFailed(ctx context.Context, port directPeerPort, messageID, failure string) error {
	owner, ok := port.(directPeerDeliveryFailureOwner)
	if !ok {
		_, err := port.MarkDeliveryFailed(ctx, messageID, failure)
		return err
	}
	capability, err := owner.OwnerCapability()
	if err != nil {
		return err
	}
	_, err = owner.MarkDeliveryFailedByOwner(ctx, capability, messageID, failure)
	return err
}

func handleReadDirectPeer(port directPeerPort) ToolHandler {
	return makeHandler(port, "direct peer store", func(ctx context.Context, input readDirectPeerInput) (any, error) {
		scope, err := directPeerScope(ctx, true)
		if err != nil {
			return nil, err
		}
		limit := input.Limit
		if limit == 0 {
			limit = 50
		}
		return port.ReadReceiver(ctx, scope.CWD, scope.AgentID, input.AfterCursor, limit)
	})
}

func handleReadDirectPeerEvents(port directPeerPort) ToolHandler {
	return makeHandler(port, "direct peer store", func(ctx context.Context, input readDirectPeerInput) (any, error) {
		scope, err := directPeerScope(ctx, true)
		if err != nil {
			return nil, err
		}
		limit := input.Limit
		if limit == 0 {
			limit = 50
		}
		return port.ListEventsForReceiver(ctx, scope.CWD, scope.AgentID, input.AfterCursor, limit)
	})
}

func directPeerScope(ctx context.Context, requireAgent bool) (mcpcommon.ToolScope, error) {
	scope, ok := mcpcommon.ToolScopeFromContext(ctx)
	if !ok || scope.CWD == "" || (requireAgent && scope.AgentID == "") {
		return mcpcommon.ToolScope{}, errors.New("direct peer requires trusted workspace and agent scope")
	}
	return scope, nil
}
