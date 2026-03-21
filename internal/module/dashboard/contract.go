package dashboard

import "context"

type Service interface {
	GetDashboard(ctx context.Context) (*Dashboard, error)
	GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error)
	GetAgentDetail(ctx context.Context, agentID string) (*AgentDetail, error)
	GetSystemInfo(ctx context.Context) (*SystemInfo, error)
	GetLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error)
}
