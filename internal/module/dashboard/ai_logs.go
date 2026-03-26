package dashboard

import (
	"context"
	"strings"

	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
)

func (s *service) GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]ailogstore.AILog, error) {
	if s.aiLogs == nil {
		return []ailogstore.AILog{}, nil
	}

	return s.aiLogs.ListByCategory(
		ctx,
		strings.TrimSpace(category),
		strings.TrimSpace(keyword),
		int32(clampLogLimit(limit)),
	)
}

func (s *service) GetAILogStats(ctx context.Context) ([]ailogstore.StatusCount, error) {
	if s.aiLogs == nil {
		return []ailogstore.StatusCount{}, nil
	}
	return s.aiLogs.CountByStatus(ctx)
}

func (s *service) GetRecentAILogs(ctx context.Context, limit int) ([]ailogstore.AILog, error) {
	if s.aiLogs == nil {
		return []ailogstore.AILog{}, nil
	}
	return s.aiLogs.ListRecent(ctx, int32(clampLogLimit(limit)))
}
