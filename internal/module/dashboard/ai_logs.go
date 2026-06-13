package dashboard

import (
	"context"
	"strings"

	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

// GetAILogsByCategory 按分类读取 AI 日志。
func (s *service) GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]ailogstore.AILog, error) {
	return safeList(s.aiLogs != nil, func() ([]ailogstore.AILog, error) {
		return s.aiLogs.ListByCategory(
			ctx,
			strings.TrimSpace(category),
			strings.TrimSpace(keyword),
			int32(util.ClampLimit(limit, 1, maxLogLimit, defaultLogLimit)),
		)
	})
}

// GetAILogStats 统计 AI 日志状态。
func (s *service) GetAILogStats(ctx context.Context) ([]ailogstore.StatusCount, error) {
	return safeList(s.aiLogs != nil, func() ([]ailogstore.StatusCount, error) {
		return s.aiLogs.CountByStatus(ctx)
	})
}

// GetRecentAILogs 读取最近的 AI 日志。
func (s *service) GetRecentAILogs(ctx context.Context, limit int) ([]ailogstore.AILog, error) {
	return safeList(s.aiLogs != nil, func() ([]ailogstore.AILog, error) {
		return s.aiLogs.ListRecent(ctx, int32(util.ClampLimit(limit, 1, maxLogLimit, defaultLogLimit)))
	})
}
