package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.SessionThreadLookup = (*sessionThreadAdapter)(nil)

type sessionThreadAdapter struct {
	store Store
}

// NewSessionThreadLookup adapts the concrete thread Store to the narrow
// contract.SessionThreadLookup interface consumed by the session resolver.
func NewSessionThreadLookup(store Store) contract.SessionThreadLookup {
	if store == nil {
		return nil
	}
	return &sessionThreadAdapter{store: store}
}

func (a *sessionThreadAdapter) GetByThreadID(ctx context.Context, threadID string) (*contract.SessionThreadRef, error) {
	thread, err := a.store.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return nil, err
	}
	return &contract.SessionThreadRef{
		ThreadID: thread.ThreadID,
		AgentID:  thread.AgentID,
	}, nil
}
