package thread

import (
	"strings"

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
