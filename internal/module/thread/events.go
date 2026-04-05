package thread

import (
	"context"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/kelindar/event"
)

func (s *service) bindDispatcher(dispatcher *event.Dispatcher) {
	if s == nil || dispatcher == nil {
		return
	}
	s.bus = dispatcher
}

func registerThreadSubscriptions(svc *service) []context.CancelFunc {
	if svc == nil {
		return nil
	}
	return []context.CancelFunc{
		platformbus.ResilientSubscribe(svc.bus, svc.onAgentLaunched, svc.logger),
	}
}

func (s *service) onAgentLaunched(ev agentdto.AgentLaunched) {
	if s == nil || s.bindingStore == nil {
		return
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	agentID := strings.TrimSpace(ev.AgentID)
	sessionID := strings.TrimSpace(ev.SessionID)
	if sessionID == "" || !looksLikeUUID(sessionID) {
		return
	}

	ctx := context.Background()
	// Claude system:init may not carry agent_id; resolve from threadID → binding.
	binding, err := s.resolveBindingForEvent(ctx, agentID, threadID)
	if err != nil || binding == nil {
		return
	}
	agentID = strings.TrimSpace(binding.AgentID)
	if agentID == "" {
		return
	}
	if strings.TrimSpace(binding.SessionUUID) == sessionID {
		return
	}
	if err := s.bindingStore.UpdateSessionUUID(ctx, bindingstore.UpdateSessionUUIDParams{
		AgentID:     agentID,
		SessionUUID: sessionID,
		UpdatedAt:   time.Now().Unix(),
	}); err != nil {
		s.logger.Warn("thread: update session_uuid from agent event failed", "thread_id", threadID, "agent_id", agentID, "session_uuid", sessionID, "error", err)
		return
	}
	s.logger.Info("thread: updated session_uuid from agent event", "thread_id", threadID, "agent_id", agentID, "session_uuid", sessionID)
}

func (s *service) resolveBindingForEvent(ctx context.Context, agentID, threadID string) (*bindingstore.Binding, error) {
	if agentID != "" {
		b, err := s.bindingStore.GetByAgentID(ctx, agentID)
		if err == nil && b != nil {
			return b, nil
		}
	}
	if threadID != "" {
		return s.resolveBinding(ctx, threadID)
	}
	return nil, nil
}
