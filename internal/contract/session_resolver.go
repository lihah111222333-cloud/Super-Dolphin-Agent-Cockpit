package contract

import "context"

type SessionResolver interface {
	ResolveSession(ctx context.Context, threadID string) (Session, error)
}
