package thread

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

func (s *service) publishThreadCompacted(result dto.ThreadCompactResult) {
	if s == nil || s.emitCompacted == nil || strings.TrimSpace(result.ThreadID) == "" {
		return
	}
	// NOTE: currently only triggered by explicit Compact(); provider-side compact completion needs separate integration
	event := newThreadEvent(threadEventCompactedKind, result.ThreadID, threadEventFields{
		Command:      result.Command,
		BeforeTokens: result.BeforeTokens,
		AfterTokens:  result.AfterTokens,
		Compacted:    result.Compacted,
		Estimated:    result.Estimated,
	})
	if event == nil {
		return
	}
	s.emitCompacted(event.(threaddto.Compacted))
}

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
