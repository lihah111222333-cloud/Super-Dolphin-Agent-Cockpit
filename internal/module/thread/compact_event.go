package thread

import (
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

func (s *service) publishThreadCompacted(result dto.ThreadCompactResult) {
	if s == nil || s.emitCompacted == nil || strings.TrimSpace(result.ThreadID) == "" {
		return
	}
	// NOTE: currently only triggered by explicit Compact(); provider-side compact completion needs separate integration
	s.emitCompacted(threaddto.Compacted{
		EventHeader:  shared.EventHeader{Timestamp: time.Now()},
		ThreadID:     strings.TrimSpace(result.ThreadID),
		Command:      strings.TrimSpace(result.Command),
		BeforeTokens: result.BeforeTokens,
		AfterTokens:  result.AfterTokens,
		Compacted:    result.Compacted,
		Estimated:    result.Estimated,
	})
}
