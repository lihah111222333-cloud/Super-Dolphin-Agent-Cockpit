package unified

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

type sessionResolver struct {
	threadStore threadstore.Store
	sessions    *SessionManager
}

var _ contract.SessionResolver = (*sessionResolver)(nil)

func NewSessionResolver(threadStore threadstore.Store, sessions *SessionManager) contract.SessionResolver {
	return &sessionResolver{threadStore: threadStore, sessions: sessions}
}

func (r *sessionResolver) ResolveSession(ctx context.Context, threadID string) (contract.Session, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, fmt.Errorf("resolve session: thread id is required")
	}
	if r.threadStore == nil {
		return nil, fmt.Errorf("resolve session: thread store is not configured")
	}
	if r.sessions == nil {
		return nil, fmt.Errorf("resolve session: session manager is not configured")
	}
	ref, err := r.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		return nil, fmt.Errorf("resolve session: thread %q: %w", threadID, err)
	}
	if ref == nil {
		return nil, fmt.Errorf("resolve session: thread %q not found", threadID)
	}
	agentID := strings.TrimSpace(ref.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("resolve session: thread %q has no agent id", threadID)
	}
	return r.sessions.Get(agentID)
}
