package dashboard

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

// WorkflowMaterialWriteRequest 表示前端模板上传到 workflow sharedfile 的文本材料。
type WorkflowMaterialWriteRequest struct {
	Path    string
	Content string
}

type Service interface {
	GetDashboard(ctx context.Context) (*Dashboard, error)
	GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error)
	ListAgentStatuses(ctx context.Context, status string) ([]agentstatusstore.AgentStatus, error)
	GetAgentDetail(ctx context.Context, agentID string) (*AgentDetail, error)
	GetSystemInfo(ctx context.Context) (*SystemInfo, error)
	GetLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error)
	GetAuditLogs(ctx context.Context, filter auditlogstore.ListFilter) ([]auditlogstore.AuditEvent, error)
	GetBusLogs(ctx context.Context, filter buslogstore.ListFilter) ([]buslogstore.BusExceptionLog, error)
	ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error)
	GetDAGDetail(ctx context.Context, dagKey string) (*contract.DAGDetail, error)
	ListDAGRuns(ctx context.Context, dagKey, status string, limit int32) ([]contract.Run, error)
	GetDAGRun(ctx context.Context, runKey string) (contract.GetRunResponse, error)
	CreateAndStartDAG(ctx context.Context, req contract.CreateDAGRequest, idempotencyKey string) (contract.DAGDetail, contract.StartDAGResponse, error)
	StartDAG(ctx context.Context, dagKey, triggerSource, idempotencyKey string) (contract.StartDAGResponse, error)
	DispatchDAGNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
	TerminateDAG(ctx context.Context, dagKey, runKey, reason string) error
	DeleteDAG(ctx context.Context, dagKey string) error
	ApplyDAGOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error)
	ListSharedFiles(ctx context.Context) ([]sharedfilestore.SharedFile, error)
	WriteWorkflowMaterial(ctx context.Context, req WorkflowMaterialWriteRequest) (*sharedfilestore.SharedFile, error)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]ailogstore.AILog, error)
	GetAILogStats(ctx context.Context) ([]ailogstore.StatusCount, error)
	GetRecentAILogs(ctx context.Context, limit int) ([]ailogstore.AILog, error)
}
