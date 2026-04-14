package claudecli

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type dreamExecutor struct{}

func provideDreamExecutorProvider() contract.DreamExecutorProvider {
	return contract.DreamExecutorProvider{
		Name:     "claude",
		Executor: dreamExecutor{},
	}
}

func (dreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_ = prompt
	// TODO: wire Claude session/turn execution to run dream consolidation prompts.
	return "", fmt.Errorf("%w: provider claude dream executor not configured", contract.ErrDreamExecutorNotConfigured)
}
