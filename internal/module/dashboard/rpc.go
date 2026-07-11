package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
)

// uiDashboardGetParams 是 ui/dashboard/get 的请求参数。
type uiDashboardGetParams struct {
	Page string `json:"page,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

// dashboardPromptsParams 是 prompt/skill dashboard 请求的 cwd scope 参数。
type dashboardPromptsParams struct {
	Cwd string `json:"cwd,omitempty"`
}

// agentStatusParams 是 dashboard/agentStatus 的状态过滤参数。
type agentStatusParams struct {
	Status string `json:"status,omitempty"`
}

// dashboardQueryParams 是 dashboard/query 的只读 SQL 透传参数。
type dashboardQueryParams struct {
	Query string `json:"query"`
	Args  []any  `json:"args,omitempty"`
}

// agentDetailParams 是 dashboard/agent/detail 的请求参数，支持驼峰和下划线两种字段名。
type agentDetailParams struct {
	AgentID      string `json:"agentId,omitempty"`
	AgentIDSnake string `json:"agent_id,omitempty"`
}

// logsParams 是 dashboard 日志类接口的统一 wire 参数。
// 同时保留 camelCase 和 snake_case 字段以兼容旧前端。
type logsParams struct {
	Source            string `json:"source,omitempty"`
	Category          string `json:"category,omitempty"`
	Keyword           string `json:"keyword,omitempty"`
	Level             string `json:"level,omitempty"`
	Logger            string `json:"logger,omitempty"`
	Component         string `json:"component,omitempty"`
	AgentID           string `json:"agentId,omitempty"`
	AgentIDSnake      string `json:"agent_id,omitempty"`
	ThreadID          string `json:"threadId,omitempty"`
	ThreadIDSnake     string `json:"thread_id,omitempty"`
	TraceID           string `json:"traceId,omitempty"`
	TraceIDSnake      string `json:"trace_id,omitempty"`
	SpanID            string `json:"spanId,omitempty"`
	SpanIDSnake       string `json:"span_id,omitempty"`
	ParentSpanID      string `json:"parentSpanId,omitempty"`
	ParentSpanIDSnake string `json:"parent_span_id,omitempty"`
	EventType         string `json:"eventType,omitempty"`
	EventTypeSnake    string `json:"event_type,omitempty"`
	ToolName          string `json:"toolName,omitempty"`
	ToolNameSnake     string `json:"tool_name,omitempty"`
	Limit             int    `json:"limit,omitempty"`
}

// logDetailParams 是 dashboard/logDetail 的请求参数。
type logDetailParams struct {
	Source string `json:"source,omitempty"`
	ID     int64  `json:"id,omitempty"`
}

// limitParams 是只需要 limit 的日志列表请求参数。
type limitParams struct {
	Limit int `json:"limit,omitempty"`
}

// auditLogsParams 是 dashboard/auditLogs 的过滤参数。
type auditLogsParams struct {
	EventType string `json:"eventType,omitempty"`
	Action    string `json:"action,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Keyword   string `json:"keyword,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// busLogsParams 是 dashboard/busLogs 的过滤参数。
type busLogsParams struct {
	Category string `json:"category,omitempty"`
	Severity string `json:"severity,omitempty"`
	Keyword  string `json:"keyword,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// busLogDetailParams 是 dashboard/busLogs/detail 的请求参数。
type busLogDetailParams struct {
	ID int64 `json:"id,omitempty"`
}

// dagsParams 是 dashboard/dags 的过滤参数。
type dagsParams struct {
	Keyword string `json:"keyword,omitempty"`
	Status  string `json:"status,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// dagDetailParams 是 dashboard/dagDetail 的请求参数。
type dagDetailParams struct {
	DAGKey string `json:"dagKey,omitempty"`
}

// dagRunsParams 是 dashboard/dagRuns 的请求参数。
type dagRunsParams struct {
	DAGKey string `json:"dagKey,omitempty"`
	Status string `json:"status,omitempty"`
	Limit  int32  `json:"limit,omitempty"`
}

// dagRunParams 是 dashboard/dagRun 的请求参数。
type dagRunParams struct {
	RunKey string `json:"runKey,omitempty"`
}

// dagStartParams 是 dashboard/dagStart 的请求参数。
type dagStartParams struct {
	DAGKey         string `json:"dagKey,omitempty"`
	TriggerSource  string `json:"triggerSource,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// dagCreateAndStartParams 是 dashboard/dagCreateAndStart 的 wire 请求。
// 保留 snake_case 别名，避免旧 UI 草稿字段丢失。
type dagCreateAndStartParams struct {
	DAGKey            string               `json:"dagKey,omitempty"`
	DAGKeySnake       string               `json:"dag_key,omitempty"`
	Title             string               `json:"title,omitempty"`
	Description       string               `json:"description,omitempty"`
	FinalNodeKey      string               `json:"finalNodeKey,omitempty"`
	FinalNodeKeySnake string               `json:"final_node_key,omitempty"`
	Metadata          json.RawMessage      `json:"metadata,omitempty"`
	Nodes             []dagCreateNodeParam `json:"nodes,omitempty"`
	IdempotencyKey    string               `json:"idempotencyKey,omitempty"`
}

// dagCreateNodeParam 是前端创建 DAG 时传入的节点 wire 结构。
// 字段同样兼容 camelCase/snake_case，转换时会复制 config 和 depends_on。
type dagCreateNodeParam struct {
	NodeKey         string          `json:"nodeKey,omitempty"`
	NodeKeySnake    string          `json:"node_key,omitempty"`
	Title           string          `json:"title,omitempty"`
	NodeType        string          `json:"nodeType,omitempty"`
	NodeTypeSnake   string          `json:"node_type,omitempty"`
	AssignedTo      string          `json:"assignedTo,omitempty"`
	AssignedToSnake string          `json:"assigned_to,omitempty"`
	DependsOn       []string        `json:"dependsOn,omitempty"`
	DependsOnSnake  []string        `json:"depends_on,omitempty"`
	CommandRef      string          `json:"commandRef,omitempty"`
	CommandRefSnake string          `json:"command_ref,omitempty"`
	Config          json.RawMessage `json:"config,omitempty"`
}

// dagDispatchNodeParams 是 dashboard/dagDispatchNode 的严格请求参数。
type dagDispatchNodeParams struct {
	DAGKey     string `json:"dagKey,omitempty"`
	NodeKey    string `json:"nodeKey,omitempty"`
	RunID      int64  `json:"runId,omitempty"`
	AssignedTo string `json:"assignedTo,omitempty"`
}

// UnmarshalJSON 拒绝 dashboard/dagDispatchNode 的未知字段。
// 该接口会改变节点归属，严格字段检查可提前暴露前端拼写错误。
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

// dagDispatchNodeFields 是 dagDispatchNodeParams 允许的 JSON 字段集合。
var dagDispatchNodeFields = map[string]struct{}{
	"assignedTo": {},
	"dagKey":     {},
	"nodeKey":    {},
	"runId":      {},
}

// dagTerminateParams 是 dashboard/dagTerminate 的请求参数。
type dagTerminateParams struct {
	DAGKey string `json:"dagKey,omitempty"`
	RunKey string `json:"runKey,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// dagDeleteParams 是 dashboard/dagDelete 的请求参数。
type dagDeleteParams struct {
	DAGKey string `json:"dagKey,omitempty"`
}

// dagApplyOpsParams 是 dashboard/dagApplyOps 的请求参数。
// BaseVersion 使用指针区分缺失和显式 0。
type dagApplyOpsParams struct {
	DAGKey      string          `json:"dagKey,omitempty"`
	BaseVersion *int64          `json:"baseVersion"`
	Ops         json.RawMessage `json:"ops"`
}

// workflowMaterialWriteParams 是 workflow 材料上传的 wire 请求。
type workflowMaterialWriteParams struct {
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
}

// typed RPC response structs 固定 dashboard JSON 字段名，替代易漂移的 map wrapper。

// agentsResponse 是 dashboard/agentStatus 的响应结构。
type agentsResponse struct {
	Agents []AgentStatus `json:"agents"`
}

// cardsResponse 是 dashboard/commandCards 的响应结构。
type cardsResponse struct {
	Cards []CommandCard `json:"cards"`
}

// promptsResponse 是 dashboard/prompts 的响应结构。
type promptsResponse struct {
	Prompts []PromptTemplate `json:"prompts"`
}

// filesResponse 是 dashboard/sharedFiles 的响应结构，附带 final output 保留分析。
type filesResponse struct {
	Files               []SharedFile        `json:"files"`
	FinalOutputRefs     []FinalOutputRef    `json:"finalOutputRefs"`
	SharedFileRetention SharedFileRetention `json:"sharedFileRetention"`
}

// workflowMaterialWriteResponse 是 workflow material 写入后的响应。
type workflowMaterialWriteResponse struct {
	Path string `json:"path"`
}

// finalOutputSnapshotLister 是 service 的快照 final output 查询窄接口。
type finalOutputSnapshotLister interface {
	listDashboardFinalOutputRefsFromSnapshot(context.Context) ([]FinalOutputRef, error)
}

// skillsResponse 是 dashboard/skills 的响应结构。
type skillsResponse struct {
	Skills []contract.SkillInfo `json:"skills"`
}

// logsResponse 是 dashboard/logs 的响应结构。
type logsResponse struct {
	Logs []LogEntry `json:"logs"`
}

// logDetailResponse 是 dashboard/logDetail 的响应结构。
type logDetailResponse struct {
	Detail *LogDetail `json:"detail"`
}

// aiLogsResponse 是 AI 日志列表接口的响应结构。
type aiLogsResponse struct {
	Logs []AILog `json:"logs"`
}

// auditLogsResponse 是审计日志列表接口的响应结构。
type auditLogsResponse struct {
	Logs []AuditEvent `json:"logs"`
}

// busLogsResponse 是 bus 异常日志列表接口的响应结构。
type busLogsResponse struct {
	Logs []BusExceptionLog `json:"logs"`
}

// busLogDetailResponse 是单条 bus 异常日志详情接口的响应结构。
type busLogDetailResponse struct {
	Log BusExceptionLog `json:"log"`
}

// dagsResponse 是 dashboard/dags 的响应结构。
type dagsResponse struct {
	DAGs []contract.DAGSummary `json:"dags"`
}

// dagDetailResponse 是 dashboard/dagDetail 的响应结构。
type dagDetailResponse struct {
	DAG   contract.DAGSummary `json:"dag"`
	Nodes []contract.DAGNode  `json:"nodes"`
}

// dagRunsResponse 是 dashboard/dagRuns 的响应结构。
type dagRunsResponse struct {
	Runs []contract.Run `json:"runs"`
}

type dagRunResponse = contract.GetRunResponse

// dagStartResponse 是 dashboard/dagStart 的响应结构。
type dagStartResponse struct {
	RunID            int64  `json:"runId,omitempty"`
	RunKey           string `json:"runKey"`
	Version          int64  `json:"version"`
	ReadyRootNodes   int64  `json:"readyRootNodes"`
	ScheduledWakeups int64  `json:"scheduledWakeups"`
	ExecutionState   string `json:"executionState,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

// dagCreateAndStartResponse 是 dashboard/dagCreateAndStart 的响应结构。
type dagCreateAndStartResponse struct {
	DAGKey           string `json:"dagKey"`
	RunID            int64  `json:"runId,omitempty"`
	RunKey           string `json:"runKey"`
	Version          int64  `json:"version"`
	ReadyRootNodes   int64  `json:"readyRootNodes"`
	ScheduledWakeups int64  `json:"scheduledWakeups"`
	ExecutionState   string `json:"executionState,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

// dagApplyOpsResponse 是 dashboard/dagApplyOps 的响应结构。
type dagApplyOpsResponse struct {
	NewVersion int64 `json:"newVersion"`
}

// aiLogStatsResponse 是 dashboard/aiLogs/stats 的响应结构。
type aiLogStatsResponse struct {
	Stats []AILogStatusCount `json:"stats"`
}

// NewDashboardHandlers 注册 dashboard JSON-RPC handler 集合。
// 函数只组装 handler map，不访问后端依赖，便于 fx 构造期保持轻量。
func NewDashboardHandlers(svc Service) platformrpc.HandlerMapResult {
	m := handler.Map{}
	registerDashboardCoreHandlers(m, svc)
	registerDashboardDataHandlers(m, svc)
	return platformrpc.HandlerMapResult{Handlers: m}
}

// registerDashboardCoreHandlers 注册页面、agent、system 和 query 相关 handler。
// 这些 handler 主要做 wire 参数校验和上下文 scope 注入，业务边界仍在 Service。
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
	m["dashboard/workflowMaterialWrite"] = platformrpc.StrictHandler(func(ctx context.Context, p workflowMaterialWriteParams) (any, error) {
		file, err := svc.WriteWorkflowMaterial(ctx, WorkflowMaterialWriteRequest{
			Path:    p.Path,
			Content: p.Content,
		})
		if err != nil {
			return nil, err
		}
		return workflowMaterialWriteResponse{Path: file.Path}, nil
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

// registerDashboardDataHandlers 注册日志、审计、bus、DAG 和 AI log handler。
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
	m["dashboard/busLogs/detail"] = platformrpc.StrictHandler(func(ctx context.Context, p busLogDetailParams) (any, error) {
		log, err := svc.GetBusLog(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		return busLogDetailResponse{Log: log}, nil
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
	m["dashboard/logDetail"] = platformrpc.StrictHandler(func(ctx context.Context, p logDetailParams) (any, error) {
		detail, err := svc.GetLogDetail(ctx, LogDetailRequest{Source: p.Source, ID: p.ID})
		if err != nil {
			return nil, err
		}
		return logDetailResponse{Detail: detail}, nil
	})
}

// registerDashboardDAGHandlers 注册 DAG 查询、启动、派发和编辑 handler。
// 所有写操作都通过 Service 再进入 runtime，避免 RPC 层直接改持久化状态。
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
	registerDashboardDAGCreateAndStartHandler(m, svc)
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

// registerDashboardDAGCreateAndStartHandler 注册模板 DAG 的创建并启动入口。
// 这个入口会先把 UI 草稿转换为 CreateDAGRequest，再显式调用 StartDAG，失败时直接返回错误。
func registerDashboardDAGCreateAndStartHandler(m handler.Map, svc Service) {
	m["dashboard/dagCreateAndStart"] = platformrpc.StrictHandler(func(ctx context.Context, p dagCreateAndStartParams) (any, error) {
		req, err := p.createDAGRequest()
		if err != nil {
			return nil, err
		}
		_, resp, err := svc.CreateAndStartDAG(ctx, req, p.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		return dagCreateAndStartResponse{
			DAGKey:           req.DagKey,
			RunID:            resp.RunID,
			RunKey:           resp.RunKey,
			Version:          resp.Version,
			ReadyRootNodes:   resp.ReadyRootNodes,
			ScheduledWakeups: resp.ScheduledWakeups,
			ExecutionState:   resp.ExecutionState,
			Warning:          resp.Warning,
		}, nil
	})
}

// agentID 返回兼容 camelCase/snake_case 的 agent ID。
func (p agentDetailParams) agentID() string {
	return util.FirstNonEmpty(p.AgentID, p.AgentIDSnake)
}

// createDAGRequest 将 UI 草稿参数转换为 runtime CreateDAGRequest。
// finalNodeKey 必须指向已有节点，metadata 必须是对象，避免创建后才发现 DAG 不可执行。
func (p dagCreateAndStartParams) createDAGRequest() (contract.CreateDAGRequest, error) {
	nodes := make([]contract.CreateDAGNodeRequest, 0, len(p.Nodes))
	for _, node := range p.Nodes {
		nodes = append(nodes, node.createDAGNodeRequest())
	}
	finalNodeKey := strings.TrimSpace(util.FirstNonEmpty(p.FinalNodeKey, p.FinalNodeKeySnake))
	if err := validateDashboardFinalNodeKey(finalNodeKey, nodes); err != nil {
		return contract.CreateDAGRequest{}, err
	}
	metadata, err := dashboardCreateDAGMetadata(p.Metadata, finalNodeKey)
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	return contract.CreateDAGRequest{
		DagKey:      strings.TrimSpace(util.FirstNonEmpty(p.DAGKey, p.DAGKeySnake)),
		Title:       strings.TrimSpace(p.Title),
		Description: strings.TrimSpace(p.Description),
		CreatedBy:   dashboardUICreatedBy,
		Metadata:    metadata,
		Nodes:       nodes,
	}, nil
}

// createDAGNodeRequest 将单个 UI 节点参数转换为 runtime 节点请求。
// depends_on 复制到新切片，避免后续调用方修改原始 payload 影响 request。
func (p dagCreateNodeParam) createDAGNodeRequest() contract.CreateDAGNodeRequest {
	dependsOn := p.DependsOn
	if len(dependsOn) == 0 {
		dependsOn = p.DependsOnSnake
	}
	return contract.CreateDAGNodeRequest{
		NodeKey:    strings.TrimSpace(util.FirstNonEmpty(p.NodeKey, p.NodeKeySnake)),
		Title:      strings.TrimSpace(p.Title),
		NodeType:   strings.TrimSpace(util.FirstNonEmpty(p.NodeType, p.NodeTypeSnake)),
		AssignedTo: strings.TrimSpace(util.FirstNonEmpty(p.AssignedTo, p.AssignedToSnake)),
		DependsOn:  append([]string(nil), dependsOn...),
		CommandRef: strings.TrimSpace(util.FirstNonEmpty(p.CommandRef, p.CommandRefSnake)),
		Config:     append(json.RawMessage(nil), p.Config...),
	}
}

// dashboardCreateDAGMetadata 合并用户 metadata 并注入 dashboard 需要的运行标记。
// metadata 必须是 JSON object；缺 schedule 时默认 manual，避免 UI 创建入口意外注册定时触发。
func dashboardCreateDAGMetadata(raw json.RawMessage, finalNodeKey string) (json.RawMessage, error) {
	metadata := map[string]any{}
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &metadata); err != nil {
			return nil, fmt.Errorf("dashboard: dag metadata must be an object: %w", err)
		}
		if metadata == nil {
			metadata = map[string]any{}
		}
	}
	if finalNodeKey != "" {
		metadata["final_node_key"] = finalNodeKey
	}
	if _, ok := metadata["schedule"]; !ok {
		metadata["schedule"] = map[string]any{"trigger": "manual"}
	}
	return json.Marshal(metadata)
}

// validateDashboardFinalNodeKey 检查 finalNodeKey 是否存在于 nodes 列表中。
// 不存在时 fail-fast，避免 final output retention 指向永远不会产生的节点。
func validateDashboardFinalNodeKey(finalNodeKey string, nodes []contract.CreateDAGNodeRequest) error {
	if finalNodeKey == "" {
		return nil
	}
	for _, node := range nodes {
		if node.NodeKey == finalNodeKey {
			return nil
		}
	}
	return fmt.Errorf("dashboard: finalNodeKey %q must match a nodeKey", finalNodeKey)
}
