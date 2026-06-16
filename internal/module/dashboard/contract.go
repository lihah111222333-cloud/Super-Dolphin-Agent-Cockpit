package dashboard

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Service exposes dashboard read and operator actions to RPC adapters.
type Service interface {
	GetDashboard(ctx context.Context) (*Dashboard, error)
	GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error)
	ListAgentStatuses(ctx context.Context, status string) ([]contract.AgentStatus, error)
	GetAgentDetail(ctx context.Context, agentID string) (*AgentDetail, error)
	GetSystemInfo(ctx context.Context) (*SystemInfo, error)
	GetLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error)
	GetAuditLogs(ctx context.Context, filter contract.AuditLogListFilter) ([]contract.AuditEvent, error)
	GetBusLogs(ctx context.Context, filter contract.BusLogListFilter) ([]contract.BusExceptionLog, error)
	ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error)
	GetDAGDetail(ctx context.Context, dagKey string) (*contract.DAGDetail, error)
	ListDAGRuns(ctx context.Context, dagKey, status string, limit int32) ([]contract.Run, error)
	GetDAGRun(ctx context.Context, runKey string) (contract.GetRunResponse, error)
	StartDAG(ctx context.Context, dagKey, triggerSource, idempotencyKey string) (contract.StartDAGResponse, error)
	DispatchDAGNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
	TerminateDAG(ctx context.Context, dagKey, runKey, reason string) error
	DeleteDAG(ctx context.Context, dagKey string) error
	ApplyDAGOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error)
	ListSharedFiles(ctx context.Context) ([]contract.SharedFile, error)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]contract.AILog, error)
	GetAILogStats(ctx context.Context) ([]contract.AILogStatusCount, error)
	GetRecentAILogs(ctx context.Context, limit int) ([]contract.AILog, error)
}
