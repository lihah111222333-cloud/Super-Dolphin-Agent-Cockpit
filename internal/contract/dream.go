package contract

import (
	"context"
	"errors"
)

var ErrDreamExecutorNotConfigured = errors.New("dream executor is not configured")

type DreamExecutor interface {
	ExecuteDream(ctx context.Context, prompt string) (string, error)
}

type DreamExecutorProvider struct {
	Name     string
	Executor DreamExecutor
}
