package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type transientInvalidator func(context.Context, contract.InvalidateReason) error

func (s *service) RunPostCompactCleanup(ctx context.Context, reason contract.InvalidateReason) error {
	return runTransientInvalidators(ctx, reason, s.invalidatePromptAssembly)
}

func runTransientInvalidators(
	ctx context.Context,
	reason contract.InvalidateReason,
	invalidators ...transientInvalidator,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, invalidator := range invalidators {
		if invalidator == nil {
			continue
		}
		if err := invalidator(ctx, reason); err != nil {
			return err
		}
	}
	return nil
}
