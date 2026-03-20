package agent

import "context"

// TransitionGuard is the orchestration-owned predicate for conditional transitions.
type TransitionGuard func(ctx context.Context, agentID string) bool
