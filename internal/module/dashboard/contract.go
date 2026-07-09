package dashboard

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// WorkflowMaterialWriteRequest 表示前端模板上传到 workflow sharedfile 的文本材料。
// Path 必须落在 workflow 上传前缀下，Content 不允许为空。
type WorkflowMaterialWriteRequest struct {
	Path    string
	Content string
}

// OrchestrationReader 是 dashboard 读取 agent 列表和快照所需的生命周期读端口。
type OrchestrationReader interface {
	ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error)
	Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error)
}

// OrchestrationReportReader 是 dashboard 读取 agent report 所需的报告读端口。
type OrchestrationReportReader interface {
	GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error)
}

// Service 定义 dashboard 模块对 RPC 层暴露的查询和操作接口。
// 读接口允许可选 store 返回空切片；写接口必须通过 runtime/store 显式能力检查。
type Service interface {
	GetDashboard(ctx context.Context) (*Dashboard, error)
	GetDashboardPage(ctx context.Context, page string) (*DashboardPage, error)
	ListAgentStatuses(ctx context.Context, status string) ([]AgentStatus, error)
	GetAgentDetail(ctx context.Context, agentID string) (*AgentDetail, error)
	GetSystemInfo(ctx context.Context) (*SystemInfo, error)
	GetLogs(ctx context.Context, filter LogFilter) ([]LogEntry, error)
	GetLogDetail(ctx context.Context, req LogDetailRequest) (*LogDetail, error)
	GetAuditLogs(ctx context.Context, filter AuditLogFilter) ([]AuditEvent, error)
	GetBusLogs(ctx context.Context, filter BusLogFilter) ([]BusExceptionLog, error)
	GetBusLog(ctx context.Context, id int64) (BusExceptionLog, error)
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
	ListSharedFiles(ctx context.Context) ([]SharedFile, error)
	WriteWorkflowMaterial(ctx context.Context, req WorkflowMaterialWriteRequest) (*SharedFile, error)
	Query(ctx context.Context, query string, args ...any) ([]map[string]any, error)
	GetAILogsByCategory(ctx context.Context, category, keyword string, limit int) ([]AILog, error)
	GetAILogStats(ctx context.Context) ([]AILogStatusCount, error)
	GetRecentAILogs(ctx context.Context, limit int) ([]AILog, error)
}
