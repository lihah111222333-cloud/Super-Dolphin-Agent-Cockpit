package codexapp

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type dreamExecutor struct{}

func provideDreamExecutorProvider() contract.DreamExecutorProvider {
	return contract.DreamExecutorProvider{
		Name:     "codex",
		Executor: dreamExecutor{},
	}
}

func (dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_ = prompt
	// TODO: wire Codex session/turn execution to run dream consolidation prompts.
	return "", fmt.Errorf("%w: provider codex dream executor not configured", contract.ErrDreamExecutorNotConfigured)
}
