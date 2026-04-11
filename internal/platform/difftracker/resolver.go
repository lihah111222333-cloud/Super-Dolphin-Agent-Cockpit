package difftracker

import "context"

type WorkDirResolver interface {
	ResolveAgentCWD(ctx context.Context, agentID string) (string, error)
}
