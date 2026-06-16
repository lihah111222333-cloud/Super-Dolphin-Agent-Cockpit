package dashboard

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

// GetAILogsByCategory 按分类读取 AI 日志。
func (s *service) GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]contract.AILog, error) {
	return safeList(s.aiLogs != nil, func() ([]contract.AILog, error) {
		return s.aiLogs.ListByCategory(
			ctx,
			strings.TrimSpace(category),
			strings.TrimSpace(keyword),
			int32(kernel.ClampLimit(limit, 1, maxLogLimit, defaultLogLimit)),
		)
	})
}

// GetAILogStats 统计 AI 日志状态。
func (s *service) GetAILogStats(ctx context.Context) ([]contract.AILogStatusCount, error) {
	return safeList(s.aiLogs != nil, func() ([]contract.AILogStatusCount, error) {
		return s.aiLogs.CountByStatus(ctx)
	})
}

// GetRecentAILogs 读取最近的 AI 日志。
func (s *service) GetRecentAILogs(ctx context.Context, limit int) ([]contract.AILog, error) {
	return safeList(s.aiLogs != nil, func() ([]contract.AILog, error) {
		return s.aiLogs.ListRecent(ctx, int32(kernel.ClampLimit(limit, 1, maxLogLimit, defaultLogLimit)))
	})
}
