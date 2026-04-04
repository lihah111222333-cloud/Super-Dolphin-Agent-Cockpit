package dashboard

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type uiDashboardGetParams struct {
	Page string `json:"page,omitempty"`
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

func NewDashboardHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"ui/dashboard/get": rpc.StrictHandler(func(ctx context.Context, p uiDashboardGetParams) (any, error) {
			return svc.GetDashboardPage(ctx, p.Page)
		}),
		"dashboard/agentStatus": rpc.StrictHandler(func(ctx context.Context, p agentStatusParams) (any, error) {
			agents, err := svc.ListAgentStatuses(ctx, p.Status)
			if err != nil {
				return nil, err
			}
			return wrapResponse("agents", agents), nil
		}),
		"dashboard/taskTraces": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "tasks", func(page *DashboardPage) any { return page.TaskTraces }, "traces")
		}),
		"dashboard/commandCards": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "commands", func(page *DashboardPage) any { return page.CommandCards }, "cards")
		}),
		"dashboard/prompts": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "commands", func(page *DashboardPage) any { return page.Prompts })
		}),
		"dashboard/sharedFiles": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "memory", func(page *DashboardPage) any { return page.Memory }, "files")
		}),
		"dashboard/skills": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "skills", func(page *DashboardPage) any { return page.Skills })
		}),
		"dashboard/agent/detail": rpc.StrictHandler(func(ctx context.Context, p agentDetailParams) (any, error) {
			return svc.GetAgentDetail(ctx, p.agentID())
		}),
		"dashboard/system/info": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return svc.GetSystemInfo(ctx)
		}),
		"dashboard/query": rpc.StrictHandler(func(ctx context.Context, p dashboardQueryParams) ([]map[string]any, error) {
			return svc.Query(ctx, p.Query, p.Args...)
		}),
		"dashboard/aiLogs": rpc.StrictHandler(func(ctx context.Context, p logsParams) (any, error) {
			return dashboardAILogField(ctx, svc, p)
		}),
		"dashboard/auditLogs": rpc.StrictHandler(func(ctx context.Context, p auditLogsParams) (any, error) {
			return dashboardAuditLogField(ctx, svc, p)
		}),
		"dashboard/busLogs": rpc.StrictHandler(func(ctx context.Context, p busLogsParams) (any, error) {
			return dashboardBusLogField(ctx, svc, p)
		}),
		"dashboard/dags": rpc.StrictHandler(func(ctx context.Context, p dagsParams) (any, error) {
			return dashboardDAGField(ctx, svc, p)
		}),
		"dashboard/dagDetail": rpc.StrictHandler(func(ctx context.Context, p dagDetailParams) (any, error) {
			return dashboardDAGDetailField(ctx, svc, p)
		}),
		"dashboard/aiLogs/recent": rpc.StrictHandler(func(ctx context.Context, p limitParams) (any, error) {
			return dashboardRecentAILogField(ctx, svc, p.Limit)
		}),
		"dashboard/aiLogs/stats": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardAILogStatsField(ctx, svc)
		}),
		"dashboard/logs": rpc.StrictHandler(func(ctx context.Context, p logsParams) (any, error) {
			return dashboardLogField(ctx, svc, p, p.Source)
		}),
	}}
}

func dashboardPageField(
	ctx context.Context,
	svc Service,
	pageName string,
	selectField func(*DashboardPage) any,
	keys ...string,
) (map[string]any, error) {
	page, err := svc.GetDashboardPage(ctx, pageName)
	if err != nil {
		return nil, err
	}
	key := pageName
	if len(keys) > 0 && strings.TrimSpace(keys[0]) != "" {
		key = strings.TrimSpace(keys[0])
	}
	return wrapResponse(key, selectField(page)), nil
}

func dashboardLogField(ctx context.Context, svc Service, p logsParams, source string) (map[string]any, error) {
	logs, err := svc.GetLogs(ctx, p.ToFilter(source))
	if err != nil {
		return nil, err
	}
	return wrapResponse("logs", logs), nil
}

func dashboardAILogField(ctx context.Context, svc Service, p logsParams) (map[string]any, error) {
	logs, err := svc.GetAILogsByCategory(ctx, p.Category, p.Keyword, p.Limit)
	if err != nil {
		return nil, err
	}
	return wrapResponse("logs", logs), nil
}

func dashboardAuditLogField(ctx context.Context, svc Service, p auditLogsParams) (map[string]any, error) {
	logs, err := svc.GetAuditLogs(ctx, p.ToFilter())
	if err != nil {
		return nil, err
	}
	return wrapResponse("logs", logs), nil
}

func dashboardBusLogField(ctx context.Context, svc Service, p busLogsParams) (map[string]any, error) {
	logs, err := svc.GetBusLogs(ctx, p.ToFilter())
	if err != nil {
		return nil, err
	}
	return wrapResponse("logs", logs), nil
}

func dashboardDAGField(ctx context.Context, svc Service, p dagsParams) (map[string]any, error) {
	dags, err := svc.ListDAGs(ctx, p.ToFilter())
	if err != nil {
		return nil, err
	}
	return wrapResponse("dags", dags), nil
}

func dashboardDAGDetailField(ctx context.Context, svc Service, p dagDetailParams) (map[string]any, error) {
	detail, err := svc.GetDAGDetail(ctx, p.DAGKey)
	if err != nil {
		return nil, err
	}
	return wrapResponses(
		responseField{key: "dag", value: detail.DAG},
		responseField{key: "nodes", value: detail.Nodes},
	), nil
}

func dashboardRecentAILogField(ctx context.Context, svc Service, limit int) (map[string]any, error) {
	logs, err := svc.GetRecentAILogs(ctx, limit)
	if err != nil {
		return nil, err
	}
	return wrapResponse("logs", logs), nil
}

func dashboardAILogStatsField(ctx context.Context, svc Service) (map[string]any, error) {
	stats, err := svc.GetAILogStats(ctx)
	if err != nil {
		return nil, err
	}
	return wrapResponse("stats", stats), nil
}

func (p agentDetailParams) agentID() string {
	return shared.FirstNonEmpty(p.AgentID, p.AgentIDSnake)
}
