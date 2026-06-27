package dashboard

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

// GetAILogsByCategory 按分类和关键词读取 AI 日志。
// limit 会被 clamp，store 缺失时返回空切片以支持无 AI 日志的运行模式。
func (s *service) GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]AILog, error) {
	return safeList(s.aiLogs != nil, func() ([]AILog, error) {
		return s.aiLogs.ListByCategory(
			ctx,
			strings.TrimSpace(category),
			strings.TrimSpace(keyword),
			int32(util.ClampLimit(limit, 1, maxLogLimit, defaultLogLimit)),
		)
	})
}

// GetAILogStats 统计 AI 日志状态分布。
// store 缺失时返回空切片，避免 dashboard 状态卡阻断页面。
func (s *service) GetAILogStats(ctx context.Context) ([]AILogStatusCount, error) {
	return safeList(s.aiLogs != nil, func() ([]AILogStatusCount, error) {
		return s.aiLogs.CountByStatus(ctx)
	})
}

// GetRecentAILogs 读取最近 AI 日志并限制返回数量。
// limit 统一按 dashboard 日志上限裁剪，防止前端请求无界列表。
func (s *service) GetRecentAILogs(ctx context.Context, limit int) ([]AILog, error) {
	return safeList(s.aiLogs != nil, func() ([]AILog, error) {
		return s.aiLogs.ListRecent(ctx, int32(util.ClampLimit(limit, 1, maxLogLimit, defaultLogLimit)))
	})
}
