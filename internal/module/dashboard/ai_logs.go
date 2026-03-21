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

	limit = clampLogLimit(limit)
	queryLimit := limit
	keyword = strings.TrimSpace(keyword)
	if keyword != "" {
		queryLimit = maxLogLimit
	}

	logs, err := s.aiLogs.ListByCategory(ctx, strings.TrimSpace(category), int32(queryLimit))
	if err != nil {
		return nil, err
	}
	if keyword == "" {
		return logs, nil
	}
	return filterAILogsByKeyword(logs, keyword, limit), nil
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

func filterAILogsByKeyword(logs []ailogstore.AILog, keyword string, limit int) []ailogstore.AILog {
	needle := strings.ToLower(strings.TrimSpace(keyword))
	filtered := make([]ailogstore.AILog, 0, min(limit, len(logs)))
	for _, row := range logs {
		if !strings.Contains(strings.ToLower(row.Message), needle) {
			continue
		}
		filtered = append(filtered, row)
		if len(filtered) == limit {
			break
		}
	}
	return filtered
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
