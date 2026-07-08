package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

// 下列枚举切片同时驱动 schema 和 handler 层校验。
// 新增工具字段枚举时放在这里，避免工具描述允许的值与运行时拒绝的值不一致。
// 命名规约：<tool>_<field>_Enum。
var (
	listDAGsStatusEnum   = []string{"draft", "active", "ready", "running", "archived"}
	listRunsStatusEnum   = []string{"running", "succeeded", "failed", "cancelled"}
	startDAGTriggerEnum  = []string{"manual", "auto", "scheduled", "external"}
	updateNodeStatusEnum = []string{"ready", "running", "retrying", "done", "failed", "cancelled"}
	recoveryActionEnum   = []string{"cancel_with_cleanup", "retry_failed_node"}
)

// CreateDAGInput 是 task_create_dag 的 wire 入参。
// handler 会把它转换成服务层请求，并在创建期拦截坏拓扑、保留节点类型和缺失执行身份。
type CreateDAGInput struct {
	AgentID           string               `json:"agent_id"`
	DagKey            string               `json:"dag_key"`
	Title             string               `json:"title"`
	Description       string               `json:"description,omitempty"`
	Schedule          DAGScheduleInput     `json:"schedule"`
	Trigger           string               `json:"trigger,omitempty"`
	DefaultRetry      int                  `json:"default_retry,omitempty"`
	DefaultTimeoutSec int                  `json:"default_timeout_sec,omitempty"`
	FailFast          bool                 `json:"fail_fast,omitempty"`
	MaxConcurrency    int                  `json:"max_concurrency,omitempty"`
	QueuePolicy       string               `json:"queue_policy,omitempty"`
	FinalNodeKey      string               `json:"final_node_key,omitempty"`
	Nodes             []CreateDAGNodeInput `json:"nodes,omitempty"`
}

// DAGScheduleInput 承载创建期可直接落库的调度默认值。
// 定时表达式和需要并发版本保护的字段必须走 apply_ops，避免创建入口绕过 OCC。
type DAGScheduleInput struct {
	Trigger           string `json:"trigger,omitempty"`
	DefaultRetry      int    `json:"default_retry,omitempty"`
	DefaultTimeoutSec int    `json:"default_timeout_sec,omitempty"`
	FailFast          bool   `json:"fail_fast,omitempty"`
	MaxConcurrency    int    `json:"max_concurrency,omitempty"`
	QueuePolicy       string `json:"queue_policy,omitempty"`
}

// CreateDAGNodeInput 是创建 DAG 时单个节点的 wire 结构。
// execution 保留嵌套配置，扁平字段只做兼容输入，最终都会归一到服务层节点请求。
type CreateDAGNodeInput struct {
	NodeKey    string             `json:"node_key"`
	Title      string             `json:"title"`
	NodeType   string             `json:"node_type,omitempty"`
	AssignedTo string             `json:"assigned_to,omitempty"`
	DependsOn  []string           `json:"depends_on,omitempty"`
	Reads      []string           `json:"reads,omitempty"`
	Writes     []string           `json:"writes,omitempty"`
	CommandRef string             `json:"command_ref,omitempty"`
	Config     json.RawMessage    `json:"config,omitempty"`
	Execution  *DAGExecutionInput `json:"execution,omitempty"`
	OnFailure  string             `json:"on_failure,omitempty"`
	Pool       string             `json:"pool,omitempty"`
	Priority   int                `json:"priority,omitempty"`
	Retry      int                `json:"retry,omitempty"`
	TimeoutSec int                `json:"timeout_sec,omitempty"`
}

// DAGExecutionInput 描述节点启动子 agent 时需要的执行身份和超时策略。
// 空结构不会写入 metadata，缺少必填身份会在创建校验阶段直接报错。
type DAGExecutionInput struct {
	OnFailure  string `json:"on_failure,omitempty"`
	Pool       string `json:"pool,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	Retry      int    `json:"retry,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// DAGKeyInput 是只读/写 DAG 工具的兼容定位符。
// pos 是首选扁平定位符，dag_key 仍接收旧调用方输入，二者冲突时由解析函数 fail-fast。
type DAGKeyInput struct {
	DagKey string `json:"dag_key"`
	Pos    string `json:"pos,omitempty"`
}

// ListDAGsInput 是 DAG 列表工具的过滤条件。
// status 枚举在 handler 层校验，limit 只影响返回裁剪，不改变服务层查询语义。
type ListDAGsInput struct {
	Status  string `json:"status,omitempty"`
	Keyword string `json:"keyword,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// ListDAGsOutput 是 DAG 列表的兼容返回结构。
// DAGs 保留旧字段，Data/Total/Showing 给通用列表控件使用，避免 UI 改造时改动工具名。
type ListDAGsOutput struct {
	DAGs      []contract.DAGSummary `json:"dags"`
	Data      []contract.DAGSummary `json:"data"`
	Total     int                   `json:"total"`
	Showing   int                   `json:"showing"`
	Truncated bool                  `json:"truncated"`
	Hint      string                `json:"hint,omitempty"`
}

// ListRunsOutput 是运行列表的兼容返回结构。
// Runs 和 Data 指向同一批数据，既保留旧工具调用方，也支持通用列表控件分页展示。
type ListRunsOutput struct {
	Runs      []contract.Run `json:"runs"`
	Data      []contract.Run `json:"data"`
	Total     int            `json:"total"`
	Showing   int            `json:"showing"`
	Truncated bool           `json:"truncated"`
	Hint      string         `json:"hint,omitempty"`
}

// task_update_node 只改某次 run 的节点，不改 DAG 模板。
// done/failed 会继续触发下游调度或失败级联，别当普通 status 覆盖用。
type UpdateNodeInput struct {
	DagKey  string `json:"dag_key"`
	NodeKey string `json:"node_key"`
	RunID   int64  `json:"run_id"`
	Pos     string `json:"pos,omitempty"`
	Status  string `json:"status"`
	Result  string `json:"result,omitempty"`
}

// DispatchNodeInput 是单节点派发工具的 wire 入参。
// run_id 必须随 dag_key/node_key 一起提供，确保派发的是某次运行中的节点而不是 DAG 模板节点。
type DispatchNodeInput struct {
	DagKey     string `json:"dag_key"`
	NodeKey    string `json:"node_key"`
	RunID      int64  `json:"run_id"`
	Pos        string `json:"pos,omitempty"`
	AssignedTo string `json:"assigned_to"`
}

type taskNodeStatusUpdater interface {
	UpdateNodeStatus(ctx context.Context, req contract.UpdateNodeStatusRequest) (contract.DAGNode, error)
}

type taskNodeDispatcher interface {
	DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
}

// HandleCreateDAG 将工具入参归一化后创建 DAG 模板。
// agent_id 以调用作用域为准，拒绝让外部 JSON 伪造创建者身份。
func HandleCreateDAG(svc contract.DAGCreateRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in CreateDAGInput) (any, error) {
		req, err := createDAGRequestFromInput(in, trustedAgentID(ctx))
		if err != nil {
			return nil, err
		}
		if err := validateAutomationCommandNodesForCreate(req.Nodes); err != nil {
			return nil, err
		}
		return svc.CreateDAG(ctx, req)
	})
}

// validateAutomationCommandNodesForCreate 在工具创建入口拦截不可执行的 automation command 配置。
// 这里要求 cwd/workspace_roots 显式存在，避免 DAG 创建成功后到 dispatcher 才发现执行边界缺失。
func validateAutomationCommandNodesForCreate(nodes []contract.CreateDAGNodeRequest) error {
	for i, node := range nodes {
		if strings.TrimSpace(node.NodeType) != "automation" {
			continue
		}
		if err := nodeexec.ValidateAutomationCommandDispatchConfig(node.Config); err != nil {
			return fmt.Errorf("nodes[%d].config invalid for automation command: %w", i, err)
		}
	}
	return nil
}

// trustedAgentID 从工具调用作用域取可信 agent ID。
func trustedAgentID(ctx context.Context) string {
	if scope, ok := mcpcommon.ToolScopeFromContext(ctx); ok {
		return strings.TrimSpace(scope.AgentID)
	}
	return ""
}

// HandleGetDAG 解析兼容定位符并读取 DAG 模板。
// 解析失败会停在工具层，避免空 dag_key 落到服务层产生模糊错误。
func HandleGetDAG(svc contract.DAGRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DAGKeyInput) (any, error) {
		dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
		if err != nil {
			return nil, err
		}
		return svc.GetDAG(ctx, dagKey)
	})
}

// HandleListDAGs 校验列表过滤条件并返回兼容分页对象。
// status 不允许静默透传未知值，防止工具描述和运行时结果分叉。
func HandleListDAGs(svc contract.DAGRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ListDAGsInput) (any, error) {
		filter, err := listDAGsFilterFromInput(in)
		if err != nil {
			return nil, err
		}
		dags, err := svc.ListDAGs(ctx, filter)
		if err != nil {
			return nil, err
		}
		return newListDAGsOutput(dags, in.Limit), nil
	})
}

// HandleUpdateNode 更新某次运行中的节点状态。
// run_id 是运行时边界，done/failed 会触发后续调度或失败级联，不能当作模板状态更新。
func HandleUpdateNode(svc taskNodeStatusUpdater) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in UpdateNodeInput) (any, error) {
		req, err := updateNodeRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.UpdateNodeStatus(ctx, req)
	})
}

// HandleDispatchNode 是单节点派发入口：补 assigned_to 后入队 wakeup。
// 它只推进 pending/ready 的运行时节点，不启动整张 DAG；store 未接线或节点不可派发时会返回中英双语阻断错误。
func HandleDispatchNode(svc taskNodeDispatcher) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DispatchNodeInput) (any, error) {
		req, err := dispatchNodeRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		resp, err := svc.DispatchNode(ctx, req)
		if err != nil {
			return nil, translateDispatchNodeError(req, err)
		}
		return resp, nil
	})
}

// translateDispatchNodeError 把哨兵错误转换为中英双语工具错误。
func translateDispatchNodeError(req contract.DispatchNodeRequest, err error) error {
	switch {
	case errors.Is(err, orchestration.ErrDispatchStoreUnset):
		return fmt.Errorf(
			"dispatch store 未接线，该 mcp-orch 启动模式不支持 task_dispatch_node; dispatch store not wired in this mcp-orch build: %w",
			err,
		)
	case errors.Is(err, orchestration.ErrDispatchNodeIneligible):
		return fmt.Errorf(
			"节点 %s/%s 当前状态不允许 dispatch（仅 pending/ready 可推进）; node %s/%s not in pending/ready: %w",
			req.DagKey, req.NodeKey, req.DagKey, req.NodeKey, err,
		)
	case errors.Is(err, orchestration.ErrDispatchIncomplete):
		return fmt.Errorf(
			"节点 %s/%s 已标记 dispatch_incomplete：assigned_to 已写入但没有 active wakeup，请人工检查后重建派发; node %s/%s marked dispatch_incomplete: %w",
			req.DagKey, req.NodeKey, req.DagKey, req.NodeKey, err,
		)
	}
	return err
}

// StartDAGInput 是整图启动工具的 wire 入参。
// trigger_source 会先按工具枚举校验；idempotency_key 原样交给服务层处理重复启动。
type StartDAGInput struct {
	DagKey         string `json:"dag_key"`
	Pos            string `json:"pos,omitempty"`
	TriggerSource  string `json:"trigger_source,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// TerminateDAGInput 定位要终止的运行。
// pos 可同时携带 dag/run 定位符，reason 只做审计记录，不参与状态判定。
type TerminateDAGInput struct {
	DagKey string `json:"dag_key"`
	RunKey string `json:"run_key"`
	Pos    string `json:"pos,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// DeleteDAGInput 是删除 DAG 模板的兼容定位符。
// pos 优先用于扁平定位，dag_key 保留给旧工具调用方，解析冲突时直接报错。
type DeleteDAGInput struct {
	DagKey string `json:"dag_key"`
	Pos    string `json:"pos,omitempty"`
}

// GetRunInput 通过 run_key 或 pos 读取单次 DAG 运行。
// pos 适合新 UI 的扁平定位符，run_key 保留给现有 MCP 调用方。
type GetRunInput struct {
	RunKey string `json:"run_key"`
	Pos    string `json:"pos,omitempty"`
}

// ListRunsInput 是运行列表工具的过滤条件。
// dag_key/pos 负责限定 DAG，status 非空时必须命中工具枚举，避免非法状态一路传到持久化层。
type ListRunsInput struct {
	DagKey string `json:"dag_key"`
	Pos    string `json:"pos,omitempty"`
	Status string `json:"status,omitempty"`
	Limit  int32  `json:"limit,omitempty"`
}

// ApplyOpsInput 是 DAG 模板变更工具的 wire 入参。
// base_version 原样传给服务层做 OCC；Ops 保持 raw JSON，由 nodeexec 解码并统一校验。
type ApplyOpsInput struct {
	DagKey      string          `json:"dag_key"`
	Pos         string          `json:"pos,omitempty"`
	BaseVersion int64           `json:"base_version"`
	Ops         json.RawMessage `json:"ops"`
	Action      string          `json:"action,omitempty"`
	NodeKey     string          `json:"node_key,omitempty"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Trigger     string          `json:"trigger,omitempty"`
	CronExpr    string          `json:"cron_expr,omitempty"`
	OwnerID     string          `json:"owner_id,omitempty"`
	NodeType    string          `json:"node_type,omitempty"`
	AssignedTo  string          `json:"assigned_to,omitempty"`
	DependsOn   []string        `json:"depends_on,omitempty"`
	Reads       []string        `json:"reads,omitempty"`
	Writes      []string        `json:"writes,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
	Patch       json.RawMessage `json:"patch,omitempty"`
}

// HandleListRuns 返回运行列表的包装对象。
// list 路径没有业务哨兵错误，DAG 未命中时保持空 slice；其他错误由通用工具错误转换处理。
func HandleListRuns(svc contract.DAGRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ListRunsInput) (any, error) {
		req, err := listRunsRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		resp, err := svc.ListRuns(ctx, req)
		if err != nil {
			return nil, err
		}
		return newListRunsOutput(enrichWorkflowRuns(resp.Runs, nil), int(req.Limit)), nil
	})
}

// newListDAGsOutput 构造 task_list_dags 的分页包装返回对象。
func newListDAGsOutput(dags []contract.DAGSummary, limit int) ListDAGsOutput {
	env := newListEnvelope(dags, limit, "next: use task_get_dag pos=dag:<dag_key> for details")
	return ListDAGsOutput{
		DAGs:      dags,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

// newListRunsOutput 构造 task_list_runs 的分页包装返回对象。
func newListRunsOutput(runs []contract.Run, limit int) ListRunsOutput {
	env := newListEnvelope(runs, limit, "next: use task_get_run pos=dag:<dag_key>/run:<run_key> for details")
	return ListRunsOutput{
		Runs:      runs,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

// listDAGsFilterFromInput 把 ListDAGsInput 转换为 contract.ListDAGsFilter。
func listDAGsFilterFromInput(in ListDAGsInput) (contract.ListDAGsFilter, error) {
	status := strings.TrimSpace(in.Status)
	if status != "" {
		validated, err := requireEnum(status, "status", listDAGsStatusEnum)
		if err != nil {
			return contract.ListDAGsFilter{}, err
		}
		status = validated
	}
	return contract.ListDAGsFilter{
		Status:  status,
		Keyword: strings.TrimSpace(in.Keyword),
		Limit:   in.Limit,
	}, nil
}

// listRunsRequestFromInput 把 ListRunsInput 校验为服务层请求。
// status 为空表示不过滤；非空必须命中工具枚举，和 schema 共用同一份值域。
func listRunsRequestFromInput(in ListRunsInput) (contract.ListRunsRequest, error) {
	dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
	if err != nil {
		return contract.ListRunsRequest{}, err
	}
	status := strings.TrimSpace(in.Status)
	if status != "" {
		validated, err := requireEnum(status, "status", listRunsStatusEnum)
		if err != nil {
			return contract.ListRunsRequest{}, err
		}
		status = validated
	}
	return contract.ListRunsRequest{
		DagKey: dagKey,
		Status: status,
		Limit:  in.Limit,
	}, nil
}

// HandleStartDAG 创建一次 DAG run、复制模板节点，并把根节点推到 ready。
// 未指派的根节点不会自动失败，会等 task_dispatch_node 或人工接管。
//
// 错误转译：
//   - ErrIdempotencyKeyExhausted → 中英双语提示 + 携带旧 RunKey + status，
//     方便 AI agent 决策是否换 idempotency_key 重试。
//   - ErrDAGAlreadyRunning → 中英双语提示。
//   - ErrDAGNotFound → 中英双语提示 + 提示先调 task_create_dag。
func HandleStartDAG(svc contract.DAGRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in StartDAGInput) (any, error) {
		req, err := startDAGRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		resp, err := svc.StartDAG(ctx, req)
		if err != nil {
			return nil, translateStartDAGError(req.DagKey, err)
		}
		return resp, nil
	})
}

// HandleTerminateDAG 终止指定 DAG run。
// 终止错误会在工具层翻译成可操作提示，避免调用方把状态冲突误当作瞬时失败重试。
func HandleTerminateDAG(svc contract.DAGRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in TerminateDAGInput) (any, error) {
		req, err := terminateDAGRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		if err := svc.TerminateDAG(ctx, req); err != nil {
			return nil, translateTerminateDAGError(req, err)
		}
		return struct{}{}, nil
	})
}

// HandleDeleteDAG 删除 DAG 模板。
// 删除前只解析定位符，运行状态和引用约束由服务层统一判定。
func HandleDeleteDAG(svc contract.DAGDeleteRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DeleteDAGInput) (any, error) {
		dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
		if err != nil {
			return nil, err
		}
		if err := svc.DeleteDAG(ctx, contract.DeleteDAGRequest{DagKey: dagKey}); err != nil {
			return nil, translateDeleteDAGError(dagKey, err)
		}
		return struct{}{}, nil
	})
}

// startDAGRequestFromInput 把 StartDAGInput 校验为服务层请求。
// trigger_source 可选；非空必须命中工具枚举，idempotency_key 只裁剪空白不改写语义。
func startDAGRequestFromInput(in StartDAGInput) (contract.StartDAGRequest, error) {
	dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
	if err != nil {
		return contract.StartDAGRequest{}, err
	}
	trigger := strings.TrimSpace(in.TriggerSource)
	if trigger != "" {
		validated, err := requireEnum(trigger, "trigger_source", startDAGTriggerEnum)
		if err != nil {
			return contract.StartDAGRequest{}, err
		}
		trigger = validated
	}
	return contract.StartDAGRequest{
		DagKey:         dagKey,
		TriggerSource:  trigger,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
	}, nil
}

// terminateDAGRequestFromInput 把 TerminateDAGInput 转换为 contract.TerminateDAGRequest。
func terminateDAGRequestFromInput(in TerminateDAGInput) (contract.TerminateDAGRequest, error) {
	dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
	if err != nil {
		return contract.TerminateDAGRequest{}, err
	}
	runKey, err := resolveRunKeyInput(in.RunKey, in.Pos)
	if err != nil {
		return contract.TerminateDAGRequest{}, err
	}
	return contract.TerminateDAGRequest{
		DagKey: dagKey,
		RunKey: runKey,
		Reason: strings.TrimSpace(in.Reason),
	}, nil
}

// encodeJSONRaw 把任意值序列化为 json.RawMessage。
func encodeJSONRaw(value any) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{})
}

// encodeOptionalString 把字符串序列化为 json.RawMessage，空串时返回 nil。
func encodeOptionalString(value string) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{OmitEmptyString: true})
}
