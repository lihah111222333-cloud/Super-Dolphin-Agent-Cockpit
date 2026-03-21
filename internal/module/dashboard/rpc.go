package dashboard

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type uiDashboardGetParams struct {
	Page string `json:"page,omitempty"`
}

type agentDetailParams struct {
	AgentID      string `json:"agentId,omitempty"`
	AgentIDSnake string `json:"agent_id,omitempty"`
}

type logsParams struct {
	Source         string `json:"source,omitempty"`
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

func NewDashboardHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"ui/dashboard/get": rpc.StrictHandler(func(ctx context.Context, p uiDashboardGetParams) (any, error) {
			return svc.GetDashboardPage(ctx, p.Page)
		}),
		"dashboard/agentStatus": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "agents", func(page *DashboardPage) any { return page.Agents })
		}),
		"dashboard/dags": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "dags", func(page *DashboardPage) any { return page.Dags })
		}),
		"dashboard/taskAcks": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return dashboardPageField(ctx, svc, "tasks", func(page *DashboardPage) any { return page.TaskAcks }, "acks")
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
		"dashboard/aiLogs": rpc.StrictHandler(func(ctx context.Context, p logsParams) (any, error) {
			return dashboardLogField(ctx, svc, p, logSourceAI)
		}),
		"dashboard/logs": rpc.StrictHandler(func(ctx context.Context, p logsParams) (any, error) {
			return svc.GetLogs(ctx, p.filter())
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
	return map[string]any{key: selectField(page)}, nil
}

func dashboardLogField(ctx context.Context, svc Service, p logsParams, source string) (map[string]any, error) {
	filter := p.filter()
	filter.Source = source
	logs, err := svc.GetLogs(ctx, filter)
	if err != nil {
		return nil, err
	}
	return map[string]any{"logs": logs}, nil
}

func (p agentDetailParams) agentID() string {
	return firstNonEmpty(p.AgentID, p.AgentIDSnake)
}

func (p logsParams) filter() LogFilter {
	return LogFilter{
		Source:    strings.TrimSpace(p.Source),
		Keyword:   strings.TrimSpace(p.Keyword),
		Level:     strings.TrimSpace(p.Level),
		Logger:    strings.TrimSpace(p.Logger),
		Component: strings.TrimSpace(p.Component),
		AgentID:   firstNonEmpty(p.AgentID, p.AgentIDSnake),
		ThreadID:  firstNonEmpty(p.ThreadID, p.ThreadIDSnake),
		EventType: firstNonEmpty(p.EventType, p.EventTypeSnake),
		ToolName:  firstNonEmpty(p.ToolName, p.ToolNameSnake),
		Limit:     p.Limit,
	}
}
