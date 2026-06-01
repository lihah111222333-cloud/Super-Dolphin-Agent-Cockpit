package contract

import (
	"context"
	"errors"
)

var ErrDreamExecutorNotConfigured = errors.New("dream executor is not configured")

type DreamExecutor interface {
	ExecuteDream(ctx context.Context, prompt string) (string, error)
}

type DreamOptions struct {
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
}

type DreamExecutorWithOptions interface {
	ExecuteDreamWithOptions(ctx context.Context, prompt string, options DreamOptions) (string, error)
}

type DreamExecutorProvider struct {
	Name     string
	Executor DreamExecutor
}
