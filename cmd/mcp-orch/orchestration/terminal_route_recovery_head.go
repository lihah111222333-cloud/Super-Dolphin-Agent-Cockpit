package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// durableRemoteTerminalHeadNoop 仅在公开 outcome 缺失或 active 时读取 current head。
// A different opaque provider turn never proves stale ordering, so recovery
// remains required until the exact terminal identity is durable.
func (s *service) durableRemoteTerminalHeadNoop(ctx context.Context, ownerAgentID, threadID, turnID, eventID string) (bool, error) {
	reader := terminalHeadReader(s)
	if reader == nil {
		return false, nil
	}
	head, err := reader.LoadTerminalOutcomeCurrentHead(ctx, ownerAgentID)
	if errors.Is(err, contract.ErrTerminalOutcomeHeadNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read durable remote terminal head: %w", err)
	}
	if err := head.Validate(); err != nil {
		return false, fmt.Errorf("validate durable remote terminal head: %w", err)
	}
	if !terminalHeadOwnerAndThreadMatch(head, ownerAgentID, threadID) {
		return false, nil
	}
	return terminalHeadTerminalIdentityMatches(head, turnID, eventID), nil
}

// terminalHeadReader 返回 current-head 窄读端口；缺失时保持 recovery-required。
func terminalHeadReader(s *service) contract.TerminalOutcomeHeadReadPort {
	if s == nil {
		return nil
	}
	if s.terminalHeadReader != nil {
		return s.terminalHeadReader
	}
	reader, _ := s.terminalOutcomes.(contract.TerminalOutcomeHeadReadPort)
	return reader
}

// terminalHeadOwnerAndThreadMatch 只接受属于同一 agent 与公开 thread 的 head。
func terminalHeadOwnerAndThreadMatch(head contract.DurableTerminalOutcomeHead, ownerAgentID, threadID string) bool {
	return strings.TrimSpace(head.AgentID) == ownerAgentID && strings.TrimSpace(head.PublicThreadID) == threadID
}

// terminalHeadTerminalIdentityMatches 只接受 terminal 状态且完整身份一致的 head。
func terminalHeadTerminalIdentityMatches(head contract.DurableTerminalOutcomeHead, turnID, eventID string) bool {
	return head.State == "terminal" &&
		strings.TrimSpace(head.ProviderTurnID) == turnID &&
		strings.TrimSpace(head.TerminalEventID) == eventID
}
