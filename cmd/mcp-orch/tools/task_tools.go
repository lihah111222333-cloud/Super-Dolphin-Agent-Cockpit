package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
)

// 下列包级 enum 切片是 schema 与 handler requireEnum 的单一真正来源。
// 修改 schema 字面量时必须同步切片，反之亦然 —— 编译期通过类型 + 单测覆盖
// 防止 drift。命名规约：<tool>_<field>_Enum。
//
// The slices below are the single source of truth shared by the schema
// builder (EnumStringSchema) and the handler-layer requireEnum fallback.
// Any change to one must update the other; tests cover the wiring.
var (
	listDAGsStatusEnum   = []string{"draft", "active", "ready", "running", "archived"}
	listRunsStatusEnum   = []string{"running", "succeeded", "failed", "cancelled"}
	startDAGTriggerEnum  = []string{"manual", "auto", "scheduled", "external"}
	updateNodeStatusEnum = []string{"pending", "running", "done", "failed"}
)

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

type DAGScheduleInput struct {
	Trigger           string `json:"trigger,omitempty"`
	DefaultRetry      int    `json:"default_retry,omitempty"`
	DefaultTimeoutSec int    `json:"default_timeout_sec,omitempty"`
	FailFast          bool   `json:"fail_fast,omitempty"`
	MaxConcurrency    int    `json:"max_concurrency,omitempty"`
	QueuePolicy       string `json:"queue_policy,omitempty"`
}

type CreateDAGNodeInput struct {
	NodeKey    string             `json:"node_key"`
	Title      string             `json:"title"`
	NodeType   string             `json:"node_type,omitempty"`
	AssignedTo string             `json:"assigned_to,omitempty"`
	DependsOn  []string           `json:"depends_on,omitempty"`
	CommandRef string             `json:"command_ref,omitempty"`
	Config     json.RawMessage    `json:"config,omitempty"`
	Execution  *DAGExecutionInput `json:"execution,omitempty"`
	OnFailure  string             `json:"on_failure,omitempty"`
	Pool       string             `json:"pool,omitempty"`
	Priority   int                `json:"priority,omitempty"`
	Retry      int                `json:"retry,omitempty"`
	TimeoutSec int                `json:"timeout_sec,omitempty"`
}

type DAGExecutionInput struct {
	OnFailure  string `json:"on_failure,omitempty"`
	Pool       string `json:"pool,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	Retry      int    `json:"retry,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type DAGKeyInput struct {
	DagKey string `json:"dag_key"`
	Pos    string `json:"pos,omitempty"`
}

type ListDAGsInput struct {
	Status  string `json:"status,omitempty"`
	Keyword string `json:"keyword,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type ListDAGsOutput struct {
	DAGs      []contract.DAGSummary `json:"dags"`
	Data      []contract.DAGSummary `json:"data"`
	Total     int                   `json:"total"`
	Showing   int                   `json:"showing"`
	Truncated bool                  `json:"truncated"`
	Hint      string                `json:"hint,omitempty"`
}

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

// DispatchNodeInput 是 task_dispatch_node MCP 工具的 typed 入参。
// F6.5 后 run_id 必填，用来定位当前 run 的 runtime node。
//
// DispatchNodeInput is the typed input for the task_dispatch_node MCP tool.
// All fields are required; run_id scopes the runtime node.
type DispatchNodeInput struct {
	DagKey     string `json:"dag_key"`
	NodeKey    string `json:"node_key"`
	RunID      int64  `json:"run_id"`
	Pos        string `json:"pos,omitempty"`
	AssignedTo string `json:"assigned_to"`
}

// HandleCreateDAG 处理创建 DAG 的工具调用。
func HandleCreateDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in CreateDAGInput) (any, error) {
		req, err := createDAGRequestFromInput(in, trustedAgentID(ctx))
		if err != nil {
			return nil, err
		}
		return svc.CreateDAG(ctx, req)
	})
}

func trustedAgentID(ctx context.Context) string {
	if scope, ok := mcpcommon.ToolScopeFromContext(ctx); ok {
		return strings.TrimSpace(scope.AgentID)
	}
	return ""
}

// HandleGetDAG 处理读取 DAG 的工具调用。
func HandleGetDAG(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in DAGKeyInput) (any, error) {
		dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
		if err != nil {
			return nil, err
		}
		return svc.GetDAG(ctx, dagKey)
	})
}

// HandleListDAGs 处理列出 DAG 的工具调用。
func HandleListDAGs(svc contract.OrchestrationService) ToolHandler {
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

// HandleUpdateNode 处理更新节点状态的工具调用。
func HandleUpdateNode(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in UpdateNodeInput) (any, error) {
		req, err := updateNodeRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.UpdateNodeStatus(ctx, req)
	})
}

// HandleDispatchNode 是 task_dispatch_node MCP 工具的 handler（ADR-004 §Open Q1）。
// 这是“启动某个节点”的入口：先补 assigned_to，再入队 wakeup。
// 当前没有 task_start_node，别和 task_start_dag 的整图启动混用。
// 在 service.DispatchNode 返 ErrDispatchStoreUnset / ErrDispatchNodeIneligible
// 时转中英双语错误，让使用者一眼看出不能继续的原因。
//
// HandleDispatchNode wires the task_dispatch_node MCP tool. Sentinel errors
// (ErrDispatchStoreUnset / ErrDispatchNodeIneligible) are translated into
// bilingual messages here so callers get actionable context.
func HandleDispatchNode(svc contract.OrchestrationService) ToolHandler {
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
	}
	return err
}

// StartDAGInput 是 task_start_dag MCP 工具的 typed 入参（T1.1）。
type StartDAGInput struct {
	DagKey         string `json:"dag_key"`
	Pos            string `json:"pos,omitempty"`
	TriggerSource  string `json:"trigger_source,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// TerminateDAGInput 是 task_terminate_dag MCP 工具的 typed 入参。
type TerminateDAGInput struct {
	DagKey string `json:"dag_key"`
	RunKey string `json:"run_key"`
	Pos    string `json:"pos,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type DeleteDAGInput struct {
	DagKey string `json:"dag_key"`
	Pos    string `json:"pos,omitempty"`
}

// GetRunInput 是 task_get_run MCP 工具的 typed 入参（T3.1）。
// GetRunInput is the typed input for the task_get_run MCP tool (T3.1).
type GetRunInput struct {
	RunKey string `json:"run_key"`
	Pos    string `json:"pos,omitempty"`
}

// ListRunsInput 是 task_list_runs MCP 工具的 typed 入参（T3.2）。
// dag_key 必填；status / limit 可选。status 枚举与 migration 0080
// task_dag_runs.status CHECK 对齐：service / store 不重复校验，错误
// status 由 DB CHECK 拒绝。
//
// ListRunsInput is the typed input for the task_list_runs MCP tool (T3.2).
// dag_key required; status / limit optional. The status enum mirrors
// migration 0080 task_dag_runs.status CHECK; service / store skip
// re-validation and rely on the DB CHECK to reject illegal values.
type ListRunsInput struct {
	DagKey string `json:"dag_key"`
	Pos    string `json:"pos,omitempty"`
	Status string `json:"status,omitempty"`
	Limit  int32  `json:"limit,omitempty"`
}

// ApplyOpsInput 是 task_dag_apply_ops MCP 工具的 typed 入参（T2.1）。
// Ops 是 raw JSON：service 内部用 nodeexec.Ops UnmarshalJSON 解码为 typed Op slice。
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
	Config      json.RawMessage `json:"config,omitempty"`
	Patch       json.RawMessage `json:"patch,omitempty"`
}

// HandleListRuns 是 task_list_runs MCP 工具的 handler（T3.2）。
// 调 service.ListRuns 后返回 {runs: [...]} 包对象（留 next_cursor / total
// 等扩展位）。list 路径无业务 sentinel（DAG 不存在返空 slice，store
// 未定义判空为 sentinel），错误走默认 fallback。
//
// HandleListRuns is the task_list_runs MCP tool handler (T3.2). It calls
// service.ListRuns and returns {runs: [...]} (object wrapper reserves room
// for next_cursor / total etc.). The list path has no business sentinels
// (a missing DAG yields an empty slice rather than an error), so errors
// fall through to the default translation.
func HandleListRuns(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ListRunsInput) (any, error) {
		req, err := listRunsRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		resp, err := svc.ListRuns(ctx, req)
		if err != nil {
			return nil, err
		}
		return newListRunsOutput(resp.Runs, int(req.Limit)), nil
	})
}

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

// listRunsRequestFromInput 把 ListRunsInput 校验为 contract.ListRunsRequest。
// status 可选：空串视为「不过滤」放行；非空必须命中 listRunsStatusEnum
// （与 schema 单源 + DB CHECK 三层互锁）。
//
// listRunsRequestFromInput validates the ListRunsInput. status is optional:
// empty means "no filter"; a non-empty value must hit listRunsStatusEnum
// (single source shared with the schema; mirrored by the migration CHECK).
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

// HandleStartDAG 是 task_start_dag MCP 工具的 handler（T1.1）。
// 它创建 run、复制模板节点，并把根节点推到 ready。
// 未指派的根节点不会自动失败，会等 task_dispatch_node 或人工接管。
//
// 错误转译（路线 N）：
//   - ErrIdempotencyKeyExhausted → 中英双语提示 + 携带旧 RunKey + status，
//     方便 AI agent 决策是否换 idempotency_key 重试。
//   - ErrDAGAlreadyRunning → 中英双语提示。
//   - ErrDAGNotFound → 中英双语提示 + 提示先调 task_create_dag。
//
// Error translation (route N):
//   - ErrIdempotencyKeyExhausted → bilingual hint with previous RunKey +
//     status so the AI caller can decide to retry with a fresh idempotency_key.
//   - ErrDAGAlreadyRunning → bilingual hint.
//   - ErrDAGNotFound → bilingual hint pointing the caller to task_create_dag.
func HandleStartDAG(svc contract.OrchestrationService) ToolHandler {
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

// HandleTerminateDAG 处理终止 DAG 的工具调用。
func HandleTerminateDAG(svc contract.OrchestrationService) ToolHandler {
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

// HandleDeleteDAG 处理删除 DAG 的工具调用。
func HandleDeleteDAG(svc contract.OrchestrationService) ToolHandler {
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

// startDAGRequestFromInput 把 StartDAGInput 校验为 contract.StartDAGRequest。
// trigger_source 可选：非空必须命中 startDAGTriggerEnum（schema 单源 +
// migration 0082 CHECK 双闸）。
//
// startDAGRequestFromInput validates the StartDAGInput. trigger_source is
// optional; a non-empty value must hit startDAGTriggerEnum (single source
// shared with the schema; mirrored by migration 0082 CHECK).
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

func encodeJSONRaw(value any) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{})
}

func encodeOptionalString(value string) (json.RawMessage, error) {
	return marshalRawJSON(value, rawJSONOptions{OmitEmptyString: true})
}
