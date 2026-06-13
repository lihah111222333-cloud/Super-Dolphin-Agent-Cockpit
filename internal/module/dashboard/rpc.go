package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

type uiDashboardGetParams struct {
	Page string `json:"page,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

type dashboardPromptsParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type agentStatusParams struct {
	Status string `json:"status,omitempty"`
}

type dashboardQueryParams struct {
	Query string `json:"query"`
	Args  []any  `json:"args,omitempty"`
}

type agentDetailParams struct {
	AgentID      string `json:"agentId,omitempty"`
	AgentIDSnake string `json:"agent_id,omitempty"`
}

type logsParams struct {
	Source         string `json:"source,omitempty"`
	Category       string `json:"category,omitempty"`
	Keyword        string `json:"keyword,omitempty"`
	Level          string `json:"level,omitempty"`
	Logger         string `json:"logger,omitempty"`
	Component      string `json:"component,omitempty"`
	AgentID        string `json:"agentId,omitempty"`
	AgentIDSnake   string `json:"agent_id,omitempty"`
	ThreadID       string `json:"threadId,omitempty"`
	ThreadIDSnake  string `json:"thread_id,omitempty"`
	EventType      string `json:"eventType,omitempty"`
	EventTypeSnake string `json:"event_type,omitempty"`
	ToolName       string `json:"toolName,omitempty"`
	ToolNameSnake  string `json:"tool_name,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

type limitParams struct {
	Limit int `json:"limit,omitempty"`
}

type auditLogsParams struct {
	EventType string `json:"eventType,omitempty"`
	Action    string `json:"action,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Keyword   string `json:"keyword,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type busLogsParams struct {
	Category string `json:"category,omitempty"`
	Severity string `json:"severity,omitempty"`
	Keyword  string `json:"keyword,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type dagsParams struct {
	Keyword string `json:"keyword,omitempty"`
	Status  string `json:"status,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type dagDetailParams struct {
	DAGKey string `json:"dagKey,omitempty"`
}

type dagRunsParams struct {
	DAGKey string `json:"dagKey,omitempty"`
	Status string `json:"status,omitempty"`
	Limit  int32  `json:"limit,omitempty"`
}

type dagRunParams struct {
	RunKey string `json:"runKey,omitempty"`
}

type dagStartParams struct {
	DAGKey         string `json:"dagKey,omitempty"`
	TriggerSource  string `json:"triggerSource,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

type dagDispatchNodeParams struct {
	DAGKey     string `json:"dagKey,omitempty"`
	NodeKey    string `json:"nodeKey,omitempty"`
	RunID      int64  `json:"runId,omitempty"`
	AssignedTo string `json:"assignedTo,omitempty"`
}

// UnmarshalJSON 解码JSON。
func (p *dagDispatchNodeParams) UnmarshalJSON(data []byte) error {
	type raw dagDispatchNodeParams
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	for key := range payload {
		if _, ok := dagDispatchNodeFields[key]; !ok {
			return fmt.Errorf("dashboard/dagDispatchNode: unknown field %q", key)
		}
	}
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = dagDispatchNodeParams(current)
	return nil
}

var dagDispatchNodeFields = map[string]struct{}{
	"assignedTo": {},
	"dagKey":     {},
	"nodeKey":    {},
	"runId":      {},
}

type dagTerminateParams struct {
	DAGKey string `json:"dagKey,omitempty"`
	RunKey string `json:"runKey,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type dagDeleteParams struct {
	DAGKey string `json:"dagKey,omitempty"`
}

type dagApplyOpsParams struct {
	DAGKey      string          `json:"dagKey,omitempty"`
	BaseVersion *int64          `json:"baseVersion"`
	Ops         json.RawMessage `json:"ops"`
}

// --- typed RPC response structs (replace map[string]any wrappers) ---

type agentsResponse struct {
	Agents []agentstatusstore.AgentStatus `json:"agents"`
}

type cardsResponse struct {
	Cards []commandcardstore.CommandCard `json:"cards"`
}

type promptsResponse struct {
	Prompts []promptstore.PromptTemplate `json:"prompts"`
}

type filesResponse struct {
	Files               []sharedfilestore.SharedFile `json:"files"`
	FinalOutputRefs     []FinalOutputRef             `json:"finalOutputRefs"`
	SharedFileRetention SharedFileRetention          `json:"sharedFileRetention"`
}

type finalOutputSnapshotLister interface {
	listDashboardFinalOutputRefsFromSnapshot(context.Context) ([]FinalOutputRef, error)
}

type skillsResponse struct {
	Skills []contract.SkillInfo `json:"skills"`
}

type logsResponse struct {
	Logs []LogEntry `json:"logs"`
}

type aiLogsResponse struct {
	Logs []ailogstore.AILog `json:"logs"`
}

type auditLogsResponse struct {
	Logs []auditlogstore.AuditEvent `json:"logs"`
}

type busLogsResponse struct {
	Logs []buslogstore.BusExceptionLog `json:"logs"`
}

type dagsResponse struct {
	DAGs []contract.DAGSummary `json:"dags"`
}

type dagDetailResponse struct {
	DAG   contract.DAGSummary `json:"dag"`
	Nodes []contract.DAGNode  `json:"nodes"`
}

type dagRunsResponse struct {
	Runs []contract.Run `json:"runs"`
}

type dagRunResponse = contract.GetRunResponse

type dagStartResponse struct {
	RunID            int64  `json:"runId,omitempty"`
	RunKey           string `json:"runKey"`
	Version          int64  `json:"version"`
	ReadyRootNodes   int64  `json:"readyRootNodes"`
	ScheduledWakeups int64  `json:"scheduledWakeups"`
	ExecutionState   string `json:"executionState,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

type dagApplyOpsResponse struct {
	NewVersion int64 `json:"newVersion"`
}

type aiLogStatsResponse struct {
	Stats []ailogstore.StatusCount `json:"stats"`
}

// NewDashboardHandlers 创建dashboard处理器。
func NewDashboardHandlers(svc Service) platformrpc.HandlerMapResult {
	m := handler.Map{}
	registerDashboardCoreHandlers(m, svc)
	registerDashboardDataHandlers(m, svc)
	return platformrpc.HandlerMapResult{Handlers: m}
}

// registerDashboardCoreHandlers registers page-level, agent, system and query handlers.
// registerDashboardCoreHandlers 注册dashboardcore处理器。
func registerDashboardCoreHandlers(m handler.Map, svc Service) {
	m["ui/dashboard/get"] = platformrpc.StrictHandler(func(ctx context.Context, p uiDashboardGetParams) (any, error) {
		ctx = withDashboardPromptScopeCWD(ctx, p.Cwd)
		return svc.GetDashboardPage(ctx, p.Page)
	})
	m["dashboard/agentStatus"] = platformrpc.StrictHandler(func(ctx context.Context, p agentStatusParams) (any, error) {
		agents, err := svc.ListAgentStatuses(ctx, p.Status)
		if err != nil {
			return nil, err
		}
		return agentsResponse{Agents: agents}, nil
	})
	m["dashboard/commandCards"] = platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
		page, err := svc.GetDashboardPage(ctx, "commandCards")
		if err != nil {
			return nil, err
		}
		return cardsResponse{Cards: page.CommandCards}, nil
	})
	m["dashboard/prompts"] = platformrpc.StrictHandler(func(ctx context.Context, p dashboardPromptsParams) (any, error) {
		cwd := strings.TrimSpace(p.Cwd)
		if cwd == "" {
			return nil, errors.New("dashboard: prompt cwd is required")
		}
		ctx = withDashboardPromptScopeCWD(ctx, cwd)
		page, err := svc.GetDashboardPage(ctx, "commands")
		if err != nil {
			return nil, err
		}
		return promptsResponse{Prompts: page.Prompts}, nil
	})
	m["dashboard/sharedFiles"] = platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
		files, err := svc.ListSharedFiles(ctx)
		if err != nil {
			return nil, err
		}
		refs := []FinalOutputRef{}
		if lister, ok := svc.(finalOutputSnapshotLister); ok {
			refs, err = lister.listDashboardFinalOutputRefsFromSnapshot(ctx)
			if err != nil {
				return nil, err
			}
		}
		return filesResponse{
			Files:               files,
			FinalOutputRefs:     refs,
			SharedFileRetention: buildSharedFileRetention(files, refs),
		}, nil
	})
	m["dashboard/skills"] = platformrpc.StrictHandler(func(ctx context.Context, p dashboardPromptsParams) (any, error) {
		ctx = withDashboardPromptScopeCWD(ctx, p.Cwd)
		page, err := svc.GetDashboardPage(ctx, "skills")
		if err != nil {
			return nil, err
		}
		return skillsResponse{Skills: page.Skills}, nil
	})
	m["dashboard/agent/detail"] = platformrpc.StrictHandler(func(ctx context.Context, p agentDetailParams) (any, error) {
		return svc.GetAgentDetail(ctx, p.agentID())
	})
	m["dashboard/system/info"] = platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
		return svc.GetSystemInfo(ctx)
	})
	m["dashboard/query"] = platformrpc.StrictHandler(func(ctx context.Context, p dashboardQueryParams) ([]map[string]any, error) {
		return svc.Query(ctx, p.Query, p.Args...)
	})
}

// registerDashboardDataHandlers registers log, audit, bus, DAG and AI-log handlers.
// registerDashboardDataHandlers 注册dashboard数据处理器。
func registerDashboardDataHandlers(m handler.Map, svc Service) {
	m["dashboard/aiLogs"] = platformrpc.StrictHandler(func(ctx context.Context, p logsParams) (any, error) {
		logs, err := svc.GetAILogsByCategory(ctx, p.Category, p.Keyword, p.Limit)
		if err != nil {
			return nil, err
		}
		return aiLogsResponse{Logs: logs}, nil
	})
	m["dashboard/auditLogs"] = platformrpc.StrictHandler(func(ctx context.Context, p auditLogsParams) (any, error) {
		logs, err := svc.GetAuditLogs(ctx, p.ToFilter())
		if err != nil {
			return nil, err
		}
		return auditLogsResponse{Logs: logs}, nil
	})
	m["dashboard/busLogs"] = platformrpc.StrictHandler(func(ctx context.Context, p busLogsParams) (any, error) {
		logs, err := svc.GetBusLogs(ctx, p.ToFilter())
		if err != nil {
			return nil, err
		}
		return busLogsResponse{Logs: logs}, nil
	})
	registerDashboardDAGHandlers(m, svc)
	m["dashboard/aiLogs/recent"] = platformrpc.StrictHandler(func(ctx context.Context, p limitParams) (any, error) {
		logs, err := svc.GetRecentAILogs(ctx, p.Limit)
		if err != nil {
			return nil, err
		}
		return aiLogsResponse{Logs: logs}, nil
	})
	m["dashboard/aiLogs/stats"] = platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
		stats, err := svc.GetAILogStats(ctx)
		if err != nil {
			return nil, err
		}
		return aiLogStatsResponse{Stats: stats}, nil
	})
	m["dashboard/logs"] = platformrpc.StrictHandler(func(ctx context.Context, p logsParams) (any, error) {
		logs, err := svc.GetLogs(ctx, p.ToFilter(p.Source))
		if err != nil {
			return nil, err
		}
		return logsResponse{Logs: logs}, nil
	})
}

// registerDashboardDAGHandlers 注册dashboardDAG处理器。
func registerDashboardDAGHandlers(m handler.Map, svc Service) {
	m["dashboard/dags"] = platformrpc.StrictHandler(func(ctx context.Context, p dagsParams) (any, error) {
		dags, err := svc.ListDAGs(ctx, p.ToFilter())
		if err != nil {
			return nil, err
		}
		return dagsResponse{DAGs: dags}, nil
	})
	m["dashboard/dagDetail"] = platformrpc.StrictHandler(func(ctx context.Context, p dagDetailParams) (any, error) {
		detail, err := svc.GetDAGDetail(ctx, p.DAGKey)
		if err != nil {
			return nil, err
		}
		return dagDetailResponse{DAG: detail.DAG, Nodes: detail.Nodes}, nil
	})
	m["dashboard/dagRuns"] = platformrpc.StrictHandler(func(ctx context.Context, p dagRunsParams) (any, error) {
		runs, err := svc.ListDAGRuns(ctx, p.DAGKey, p.Status, p.Limit)
		if err != nil {
			return nil, err
		}
		return dagRunsResponse{Runs: runs}, nil
	})
	m["dashboard/dagRun"] = platformrpc.StrictHandler(func(ctx context.Context, p dagRunParams) (any, error) {
		resp, err := svc.GetDAGRun(ctx, p.RunKey)
		if err != nil {
			return nil, err
		}
		return dagRunResponse(resp), nil
	})
	m["dashboard/dagStart"] = platformrpc.StrictHandler(func(ctx context.Context, p dagStartParams) (any, error) {
		resp, err := svc.StartDAG(ctx, p.DAGKey, p.TriggerSource, p.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		return dagStartResponse{
			RunID:            resp.RunID,
			RunKey:           resp.RunKey,
			Version:          resp.Version,
			ReadyRootNodes:   resp.ReadyRootNodes,
			ScheduledWakeups: resp.ScheduledWakeups,
			ExecutionState:   resp.ExecutionState,
			Warning:          resp.Warning,
		}, nil
	})
	m["dashboard/dagDispatchNode"] = platformrpc.StrictHandler(func(ctx context.Context, p dagDispatchNodeParams) (any, error) {
		return svc.DispatchDAGNode(ctx, contract.DispatchNodeRequest{
			DagKey:     p.DAGKey,
			NodeKey:    p.NodeKey,
			RunID:      p.RunID,
			AssignedTo: p.AssignedTo,
		})
	})
	m["dashboard/dagTerminate"] = platformrpc.StrictHandler(func(ctx context.Context, p dagTerminateParams) (any, error) {
		if err := svc.TerminateDAG(ctx, p.DAGKey, p.RunKey, p.Reason); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	m["dashboard/dagDelete"] = platformrpc.StrictHandler(func(ctx context.Context, p dagDeleteParams) (any, error) {
		if err := svc.DeleteDAG(ctx, p.DAGKey); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	})
	m["dashboard/dagApplyOps"] = platformrpc.StrictHandler(func(ctx context.Context, p dagApplyOpsParams) (any, error) {
		if p.BaseVersion == nil {
			return nil, errors.New("baseVersion is required")
		}
		resp, err := svc.ApplyDAGOps(ctx, contract.ApplyOpsRequest{
			DagKey:      p.DAGKey,
			BaseVersion: *p.BaseVersion,
			Ops:         append(json.RawMessage(nil), p.Ops...),
		})
		if err != nil {
			return nil, err
		}
		return dagApplyOpsResponse{NewVersion: resp.NewVersion}, nil
	})
}

func (p agentDetailParams) agentID() string {
	return util.FirstNonEmpty(p.AgentID, p.AgentIDSnake)
}
