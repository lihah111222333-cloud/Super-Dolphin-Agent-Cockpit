package dashboard

import (
	"context"

	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
)

type Service interface {
	GetDashboard(ctx context.Context) (*Dashboard, error)
	GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error)
	GetAgentDetail(ctx context.Context, agentID string) (*AgentDetail, error)
	GetSystemInfo(ctx context.Context) (*SystemInfo, error)
	GetLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]ailogstore.AILog, error)
	GetAILogStats(ctx context.Context) ([]ailogstore.StatusCount, error)
	GetRecentAILogs(ctx context.Context, limit int) ([]ailogstore.AILog, error)
}
