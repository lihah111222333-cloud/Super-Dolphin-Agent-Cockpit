package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// errRemoteTerminalRecoveryRequired marks a canonical terminal that could not
// be routed to a live runtime and was not proven to be an already-durable
// duplicate or stale turn. Receipt callers must retry/recover instead of ACKing.
var errRemoteTerminalRecoveryRequired = errors.New("remote terminal recovery is required")

// durableRemoteTerminalNoop 证明路由缺失仅在完整 terminal 身份已持久化时可安全视为重复。
// It proves that a route miss is safe to treat as an
// idempotent duplicate (including a replay of a stale terminal whose exact
// identity is already sealed). No proof is inferred from runtime absence,
// owner lookup success, or a missing durable record.
func (s *service) durableRemoteTerminalNoop(ctx context.Context, ownerAgentID string, terminal turndto.TurnTerminalV2) (bool, error) {
	if s == nil || s.terminalOutcomes == nil {
		return false, nil
	}
	ownerAgentID = strings.TrimSpace(ownerAgentID)
	threadID, turnID, eventID := strings.TrimSpace(terminal.ThreadID), strings.TrimSpace(terminal.TurnID), strings.TrimSpace(terminal.EventID)
	outcome, outcomeErr := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, ownerAgentID)
	switch {
	case outcomeErr == nil:
		if remoteTerminalOutcomeMatches(outcome, ownerAgentID, threadID, turnID, eventID) {
			return true, nil
		}
		// A readable but different public terminal is durable evidence of a
		// conflict, not permission to let a separate head read override it.
		return false, nil
	case errors.Is(outcomeErr, sql.ErrNoRows), errors.Is(outcomeErr, contract.ErrTerminalOutcomeActive):
		// An absent/active public outcome is not proof. The current-head read
		// below can still prove an exact terminal identity.
	default:
		// An integrity or database error means the public durable outcome is
		// not trustworthy; do not let a separate head read turn that into ACK.
		return false, fmt.Errorf("read durable remote terminal outcome: %w", outcomeErr)
	}
	proven, headErr := s.durableRemoteTerminalHeadNoop(ctx, ownerAgentID, threadID, turnID, eventID)
	if headErr != nil {
		return false, headErr
	}
	if proven {
		return true, nil
	}
	return false, nil
}

// remoteTerminalOutcomeMatches requires the full public turn identity and
// event identity before acknowledging an undeliverable receipt as duplicate.
func remoteTerminalOutcomeMatches(outcome contract.TerminalOutcomeCommit, ownerAgentID, threadID, turnID, eventID string) bool {
	identity := outcome.Identity
	return strings.TrimSpace(identity.AgentID) == ownerAgentID &&
		strings.TrimSpace(identity.PublicThreadID) == threadID &&
		strings.TrimSpace(identity.ProviderTurnID) == turnID &&
		strings.TrimSpace(identity.EventID) == eventID
}
